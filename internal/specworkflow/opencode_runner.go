package specworkflow

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/foundry-zero/adversarial-spec-system/internal/process"
)

// OpenCodeRunner implements AgentRunner by invoking the OpenCode CLI as a
// subprocess. It parses the JSONL streaming output to extract text content
// and cost metadata.
type OpenCodeRunner struct {
	// Command is the path or name of the OpenCode CLI executable.
	Command string
	// Model is the provider/model string passed via -m flag
	// (e.g. "anthropic/claude-sonnet-4-5", "google/gemini-2.5-pro").
	Model string
	// Ctx is the parent context for subprocess execution.
	Ctx context.Context
	// WorkspaceDir is the working directory for the subprocess (set via cmd.Dir).
	WorkspaceDir string
	// SchemaBytes is the JSON schema bytes embedded in the prompt preamble.
	SchemaBytes []byte
	// Tracker is an optional ProcessTracker for recording subprocess lifecycle.
	Tracker *process.ProcessTracker
	// Feature identifies the workflow feature for process tracking events.
	Feature string
	// Role identifies the agent role for process tracking events.
	Role string
	// Env holds additional environment variables for the subprocess.
	Env map[string]string
}

// openCodeEvent represents a single JSONL event from the OpenCode CLI output.
type openCodeEvent struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp,omitempty"`
	Part      json.RawMessage `json:"part,omitempty"`
	Error     *struct {
		Name string `json:"name,omitempty"`
		Data *struct {
			Message string `json:"message,omitempty"`
		} `json:"data,omitempty"`
	} `json:"error,omitempty"`
}

// openCodeTextPart is the "part" payload for type=="text" events.
type openCodeTextPart struct {
	Text string `json:"text"`
}

// openCodeStepFinishPart is the "part" payload for type=="step_finish" events.
type openCodeStepFinishPart struct {
	Reason string  `json:"reason,omitempty"`
	Cost   float64 `json:"cost,omitempty"`
	Tokens *struct {
		Input      int `json:"input,omitempty"`
		Output     int `json:"output,omitempty"`
		CacheRead  int `json:"cache_read,omitempty"`
		CacheWrite int `json:"cache_write,omitempty"`
	} `json:"tokens,omitempty"`
}

// buildArgs constructs the argument list for the opencode CLI invocation.
func (r *OpenCodeRunner) buildArgs(prompt string) []string {
	args := []string{"run", "--format", "json", "--dangerously-skip-permissions"}

	if r.Model != "" {
		args = append(args, "-m", r.Model)
	}

	// Embed schema in prompt preamble if provided.
	fullPrompt := prompt
	if len(r.SchemaBytes) > 0 {
		fullPrompt = schemaPromptPrefix(r.SchemaBytes) + prompt
	}

	args = append(args, fullPrompt)
	return args
}

// schemaPromptPrefix builds a prompt preamble that instructs the model to
// produce output conforming to the given JSON schema.
func schemaPromptPrefix(schemaBytes []byte) string {
	return fmt.Sprintf(`You MUST respond with a single JSON object that conforms to this schema:

%s

Do not include any text before or after the JSON object. Output ONLY valid JSON.

---

`, string(schemaBytes))
}

// Run implements AgentRunner. It launches the OpenCode CLI, parses the JSONL
// output stream, concatenates text events, extracts cost from step_finish
// events, and writes the result to outputPath.
func (r *OpenCodeRunner) Run(prompt string, outputPath string, timeoutSeconds int) (exitCode int, stderr string, costUSD float64, durationMS int64, err error) {
	if prompt == "" {
		return 1, "", 0, 0, fmt.Errorf("empty prompt")
	}
	if outputPath == "" {
		return 1, "", 0, 0, fmt.Errorf("empty output path")
	}

	parentCtx := r.Ctx
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	timeout := time.Duration(timeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(parentCtx, timeout)
	defer cancel()

	args := r.buildArgs(prompt)
	cmd := exec.CommandContext(ctx, r.Command, args...)

	if r.WorkspaceDir != "" {
		cmd.Dir = r.WorkspaceDir
	}

	cmd.Env = os.Environ()
	for k, v := range r.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	stdoutPipe, pipeErr := cmd.StdoutPipe()
	if pipeErr != nil {
		return 1, "", 0, 0, fmt.Errorf("create stdout pipe: %w", pipeErr)
	}

	log.Printf("[opencode-runner] dispatching: %s run --format json -m %s", r.Command, r.Model)
	log.Printf("[opencode-runner] working dir: %s", r.WorkspaceDir)
	log.Printf("[opencode-runner] output path: %s", outputPath)
	log.Printf("[opencode-runner] timeout: %v", timeout)
	log.Printf("[opencode-runner] prompt length: %d chars", len(prompt))

	startTime := time.Now()
	if startErr := cmd.Start(); startErr != nil {
		log.Printf("[opencode-runner] FAILED to start process: %v", startErr)
		return 1, "", 0, 0, fmt.Errorf("start opencode process: %w", startErr)
	}
	log.Printf("[opencode-runner] process started (PID %d)", cmd.Process.Pid)

	if r.Tracker != nil {
		if regErr := r.Tracker.Register(process.ProcessRecord{
			Feature:   r.Feature,
			Role:      r.Role,
			PID:       cmd.Process.Pid,
			StartedAt: startTime,
		}); regErr != nil {
			log.Printf("[opencode-runner] WARNING: failed to register PID %d with process tracker: %v", cmd.Process.Pid, regErr)
		}
	}

	// Read stdout fully then parse JSONL.
	stdoutData, readErr := io.ReadAll(stdoutPipe)
	if readErr != nil {
		return 1, "", 0, 0, fmt.Errorf("read stdout: %w", readErr)
	}

	// Wait for process to finish.
	type waitResult struct{ err error }
	done := make(chan waitResult, 1)
	go func() {
		done <- waitResult{err: cmd.Wait()}
	}()

	pid := cmd.Process.Pid

	killProcess := func(reason string) {
		log.Printf("[opencode-runner] %s, sending SIGTERM", reason)
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
			log.Printf("[opencode-runner] process exited after SIGTERM")
		case <-time.After(2 * time.Second):
			log.Printf("[opencode-runner] process did not exit after SIGTERM, sending SIGKILL")
			_ = cmd.Process.Kill()
			<-done
		}
	}

	select {
	case <-ctx.Done():
		elapsed := time.Since(startTime)
		stderrStr := stderrBuf.String()
		if ctx.Err() == context.DeadlineExceeded {
			killProcess(fmt.Sprintf("TIMEOUT after %v", timeout))
			if r.Tracker != nil {
				r.Tracker.RecordEnd(pid, 1)
			}
			return 1, stderrStr, 0, elapsed.Milliseconds(), fmt.Errorf("opencode process timed out after %v", timeout)
		}
		killProcess("cancelled")
		if r.Tracker != nil {
			r.Tracker.RecordEnd(pid, 1)
		}
		return 1, stderrStr, 0, elapsed.Milliseconds(), fmt.Errorf("opencode process cancelled")

	case res := <-done:
		elapsed := time.Since(startTime)
		stderrStr := stderrBuf.String()

		log.Printf("[opencode-runner] process exited after %v, stdout=%d bytes, stderr=%d bytes",
			elapsed.Round(time.Millisecond), len(stdoutData), stderrBuf.Len())

		if len(stderrStr) > 0 {
			log.Printf("[opencode-runner] stderr (first 500 chars): %s", truncate(stderrStr, 500))
		}

		code := 0
		if res.err != nil {
			if exitErr, ok := res.err.(*exec.ExitError); ok {
				code = exitErr.ExitCode()
				log.Printf("[opencode-runner] non-zero exit code: %d", code)
			} else {
				log.Printf("[opencode-runner] process error: %v", res.err)
				if r.Tracker != nil {
					r.Tracker.RecordEnd(pid, 1)
				}
				return 1, stderrStr, 0, elapsed.Milliseconds(), fmt.Errorf("opencode process error: %w", res.err)
			}
		}

		if r.Tracker != nil {
			r.Tracker.RecordEnd(pid, code)
			if r.Tracker.IsKillRequested(pid) {
				log.Printf("[opencode-runner] PID %d was killed by user request — not retrying", pid)
				return code, stderrStr, 0, 0, fmt.Errorf("agent killed by user: %w", ErrProcessKilled)
			}
		}

		// Parse JSONL output.
		text, parsedCost, parseErr := parseJSONLOutput(stdoutData)
		if parseErr != nil {
			log.Printf("[opencode-runner] FAILED to parse JSONL output: %v", parseErr)
			log.Printf("[opencode-runner] raw stdout (first 500 chars): %s", truncate(string(stdoutData), 500))
			_ = writeOutputFile(outputPath, stdoutData)
			return code, stderrStr, 0, elapsed.Milliseconds(), fmt.Errorf("failed to parse opencode JSONL output: %w", parseErr)
		}

		log.Printf("[opencode-runner] parsed: cost=$%.4f, text_length=%d", parsedCost, len(text))

		// Try to extract valid JSON from the concatenated text.
		var outputData []byte
		trimmed := strings.TrimSpace(text)
		if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') && json.Valid([]byte(trimmed)) {
			outputData = []byte(trimmed)
		} else {
			if extracted := extractJSONFromText(text); extracted != nil {
				log.Printf("[opencode-runner] extracted JSON from text (%d bytes from %d)", len(extracted), len(text))
				outputData = extracted
			} else {
				log.Printf("[opencode-runner] WARNING: output is not JSON, writing raw text (%d bytes)", len(text))
				outputData = []byte(text)
			}
		}

		if writeErr := writeOutputFile(outputPath, outputData); writeErr != nil {
			return code, stderrStr, parsedCost, elapsed.Milliseconds(), fmt.Errorf("write output file: %w", writeErr)
		}

		log.Printf("[opencode-runner] SUCCESS: wrote %d bytes to %s", len(outputData), outputPath)
		return code, stderrStr, parsedCost, elapsed.Milliseconds(), nil
	}
}

// parseJSONLOutput parses the JSONL stream from opencode. It concatenates all
// "text" event part.text values and sums cost from all "step_finish" events.
// Returns the concatenated text, total cost, and any parse error.
func parseJSONLOutput(data []byte) (text string, costUSD float64, err error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return "", 0, fmt.Errorf("empty output from opencode")
	}

	var textParts []string
	var totalCost float64
	var errorMsg string

	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		var event openCodeEvent
		if jsonErr := json.Unmarshal(line, &event); jsonErr != nil {
			continue
		}

		switch event.Type {
		case "text":
			var part openCodeTextPart
			if json.Unmarshal(event.Part, &part) == nil && part.Text != "" {
				textParts = append(textParts, part.Text)
			}

		case "step_finish":
			var part openCodeStepFinishPart
			if json.Unmarshal(event.Part, &part) == nil {
				totalCost += part.Cost
			}

		case "error":
			if event.Error != nil && event.Error.Data != nil {
				errorMsg = event.Error.Data.Message
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return "", 0, fmt.Errorf("scanning JSONL: %w", err)
	}

	concatenated := strings.Join(textParts, "")
	if concatenated == "" && errorMsg != "" {
		return "", totalCost, fmt.Errorf("opencode error: %s", errorMsg)
	}
	if concatenated == "" {
		return "", totalCost, fmt.Errorf("no text content in opencode JSONL output")
	}

	return concatenated, totalCost, nil
}

// CloneForAgent returns a shallow copy of this OpenCodeRunner with the Role
// field set to agentName and OTEL resource attributes updated.
func (r *OpenCodeRunner) CloneForAgent(agentName string) AgentRunner {
	envCopy := make(map[string]string, len(r.Env)+1)
	for k, v := range r.Env {
		envCopy[k] = v
	}
	existing := envCopy["OTEL_RESOURCE_ATTRIBUTES"]
	if existing != "" {
		envCopy["OTEL_RESOURCE_ATTRIBUTES"] = existing + ",workflow.agent=" + agentName
	} else {
		envCopy["OTEL_RESOURCE_ATTRIBUTES"] = "workflow.agent=" + agentName
	}
	return &OpenCodeRunner{
		Command:      r.Command,
		Model:        r.Model,
		Ctx:          r.Ctx,
		WorkspaceDir: r.WorkspaceDir,
		SchemaBytes:  r.SchemaBytes,
		Tracker:      r.Tracker,
		Feature:      r.Feature,
		Role:         agentName,
		Env:          envCopy,
	}
}

// WithModelOverride implements the ModelOverrider interface.
func (r *OpenCodeRunner) WithModelOverride(model string) AgentRunner {
	clone := *r
	clone.Model = model
	return &clone
}

// WithSchemaEnforcement implements the SchemaEnforcer interface.
func (r *OpenCodeRunner) WithSchemaEnforcement(schemaBytes []byte) AgentRunner {
	clone := *r
	clone.SchemaBytes = schemaBytes
	return &clone
}

// ForJSONOnlyMode implements the JSONOnlyRunner interface. For OpenCode,
// schema enforcement and JSON-only mode are equivalent — both embed the
// schema in the prompt preamble.
func (r *OpenCodeRunner) ForJSONOnlyMode(schemaBytes []byte) AgentRunner {
	clone := *r
	clone.SchemaBytes = schemaBytes
	return &clone
}

// WithContext returns a copy of the runner with the given parent context.
func (r *OpenCodeRunner) WithContext(ctx context.Context) AgentRunner {
	clone := *r
	clone.Ctx = ctx
	return &clone
}

// DefaultOpenCodeRunner returns an OpenCodeRunner configured with standard defaults.
func DefaultOpenCodeRunner(model string, workspaceDir string, schemaBytes []byte) *OpenCodeRunner {
	return &OpenCodeRunner{
		Command:      "opencode",
		Model:        model,
		WorkspaceDir: workspaceDir,
		SchemaBytes:  schemaBytes,
		Env:          make(map[string]string),
	}
}
