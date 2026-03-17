// Package specworkflow defines the core types for the adversarial spec review
// workflow. This file implements ClaudeRunner, an AgentRunner that executes
// the Claude CLI as a subprocess to perform agent tasks.
package specworkflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// ClaudeRunner implements AgentRunner by invoking the Claude CLI as a
// subprocess. It builds a command line with the prompt passed via the -p
// flag and captures structured JSON output.
type ClaudeRunner struct {
	// Command is the path or name of the Claude CLI executable (e.g. "claude").
	Command string
	// Args contains additional CLI flags appended after the prompt flag.
	// Typical values: ["--dangerously-skip-permissions", "--output-format", "json", "--verbose"]
	Args []string
	// Timeout is the maximum duration for a single agent invocation.
	// If zero, defaults to 120 seconds.
	Timeout time.Duration
	// WorkspaceDir is the working directory for the subprocess.
	WorkspaceDir string
	// Env holds additional environment variables for the subprocess.
	// These are merged with the current process environment.
	Env map[string]string
}

// claudeOutput is the normalised result extracted from the Claude CLI output.
type claudeOutput struct {
	Result     string  `json:"result"`
	CostUSD    float64 `json:"cost_usd"`
	DurationMS int64   `json:"duration_ms"`
	IsError    bool    `json:"is_error"`
}

// claudeStreamEvent is a single event in the verbose JSON stream array
// emitted by `claude -p --output-format json --verbose`.
type claudeStreamEvent struct {
	Type       string  `json:"type"`
	Subtype    string  `json:"subtype,omitempty"`
	CostUSD    float64 `json:"cost_usd,omitempty"`
	DurationMS int64   `json:"duration_ms,omitempty"`
	IsError    bool    `json:"is_error,omitempty"`
	// Result message content — present on type=="result" events
	Result string `json:"result,omitempty"`
	// For assistant messages, content may be in a "message" sub-object
	Message *struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text,omitempty"`
		} `json:"content,omitempty"`
		Usage *struct {
			InputTokens  int `json:"input_tokens,omitempty"`
			OutputTokens int `json:"output_tokens,omitempty"`
		} `json:"usage,omitempty"`
	} `json:"message,omitempty"`
}

// BuildCommand constructs the exec.Cmd for a given prompt and timeout context.
// Exported for testing command construction without executing.
func (r *ClaudeRunner) BuildCommand(ctx context.Context, prompt string) *exec.Cmd {
	args := []string{"-p", prompt}
	args = append(args, r.Args...)

	cmd := exec.CommandContext(ctx, r.Command, args...)

	if r.WorkspaceDir != "" {
		cmd.Dir = r.WorkspaceDir
	}

	// Inherit current environment and overlay custom vars.
	cmd.Env = os.Environ()
	for k, v := range r.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	return cmd
}

// ParseOutput parses the Claude CLI JSON output. It handles two formats:
//  1. Single object: {"result":"...","cost_usd":0.12,...} (non-verbose)
//  2. Streaming array: [{"type":"system",...},{"type":"result",...}] (verbose)
//
// In both cases it extracts the final result text, cost, and duration.
func ParseOutput(data []byte) (*claudeOutput, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, fmt.Errorf("empty output from claude")
	}

	// Try single-object format first (non-verbose output).
	if data[0] == '{' {
		var out claudeOutput
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, fmt.Errorf("parse claude JSON object: %w", err)
		}
		return &out, nil
	}

	// Streaming array format (verbose output).
	if data[0] == '[' {
		var events []claudeStreamEvent
		if err := json.Unmarshal(data, &events); err != nil {
			return nil, fmt.Errorf("parse claude JSON stream array: %w", err)
		}
		return extractFromStream(events)
	}

	return nil, fmt.Errorf("unexpected claude output format (starts with %q)", string(data[:1]))
}

// extractFromStream walks the verbose stream event array and extracts
// the final result, cost, and error status.
func extractFromStream(events []claudeStreamEvent) (*claudeOutput, error) {
	if len(events) == 0 {
		return nil, fmt.Errorf("empty event stream from claude")
	}

	out := &claudeOutput{}

	// Walk events and accumulate. The "result" event at the end has the
	// final text and cost. Fall back to the last assistant message if no
	// explicit result event exists.
	var lastAssistantText string
	for _, ev := range events {
		switch ev.Type {
		case "result":
			out.Result = ev.Result
			out.CostUSD = ev.CostUSD
			out.DurationMS = ev.DurationMS
			out.IsError = ev.IsError
			log.Printf("[claude-runner] found result event: cost=$%.4f, duration=%dms, is_error=%v, result_len=%d",
				ev.CostUSD, ev.DurationMS, ev.IsError, len(ev.Result))

		case "assistant":
			// Extract text from the assistant message content blocks.
			if ev.Message != nil {
				for _, block := range ev.Message.Content {
					if block.Type == "text" && block.Text != "" {
						lastAssistantText = block.Text
					}
				}
			}
			// Accumulate cost from each assistant turn.
			if ev.CostUSD > 0 {
				out.CostUSD += ev.CostUSD
			}
		}
	}

	// If no explicit result event, use the last assistant text.
	if out.Result == "" && lastAssistantText != "" {
		out.Result = lastAssistantText
		log.Printf("[claude-runner] no result event found, using last assistant text (%d chars)", len(lastAssistantText))
	}

	if out.Result == "" {
		return nil, fmt.Errorf("no result found in %d stream events", len(events))
	}

	return out, nil
}

// Run implements AgentRunner. It launches the Claude CLI with the given
// prompt, captures stdout as JSON, writes the result field to outputPath,
// and returns execution metadata. The timeoutSeconds parameter is used if
// ClaudeRunner.Timeout is zero.
func (r *ClaudeRunner) Run(prompt string, outputPath string, timeoutSeconds int) (exitCode int, stderr string, costUSD float64, durationMS int64, err error) {
	timeout := r.Timeout
	if timeout == 0 {
		timeout = time.Duration(timeoutSeconds) * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := r.BuildCommand(ctx, prompt)

	log.Printf("[claude-runner] dispatching: %s %v", cmd.Path, cmd.Args[1:3])
	log.Printf("[claude-runner] working dir: %s", cmd.Dir)
	log.Printf("[claude-runner] output path: %s", outputPath)
	log.Printf("[claude-runner] timeout: %v", timeout)
	log.Printf("[claude-runner] prompt length: %d chars", len(prompt))

	// Capture stdout and stderr separately.
	var stderrBuf []byte
	stderrPipe, pipeErr := cmd.StderrPipe()
	if pipeErr != nil {
		return 1, "", 0, 0, fmt.Errorf("create stderr pipe: %w", pipeErr)
	}

	stdoutPipe, pipeErr := cmd.StdoutPipe()
	if pipeErr != nil {
		return 1, "", 0, 0, fmt.Errorf("create stdout pipe: %w", pipeErr)
	}

	startTime := time.Now()
	if startErr := cmd.Start(); startErr != nil {
		log.Printf("[claude-runner] FAILED to start process: %v", startErr)
		return 1, "", 0, 0, fmt.Errorf("start claude process: %w", startErr)
	}
	log.Printf("[claude-runner] process started (PID %d)", cmd.Process.Pid)

	// Read stdout fully.
	stdoutData, readErr := io.ReadAll(stdoutPipe)
	if readErr != nil {
		return 1, "", 0, 0, fmt.Errorf("read stdout: %w", readErr)
	}

	// Read stderr fully.
	stderrBuf, _ = io.ReadAll(stderrPipe)

	waitErr := cmd.Wait()
	elapsed := time.Since(startTime)

	stderrStr := string(stderrBuf)

	log.Printf("[claude-runner] process exited after %v, stdout=%d bytes, stderr=%d bytes",
		elapsed.Round(time.Millisecond), len(stdoutData), len(stderrBuf))

	if len(stderrStr) > 0 {
		log.Printf("[claude-runner] stderr (first 500 chars): %s", truncate(stderrStr, 500))
	}

	// Check for context timeout.
	if ctx.Err() == context.DeadlineExceeded {
		log.Printf("[claude-runner] TIMEOUT after %v", timeout)
		return 1, stderrStr, 0, 0, fmt.Errorf("claude process timed out after %v", timeout)
	}

	// Determine exit code.
	code := 0
	if waitErr != nil {
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
			log.Printf("[claude-runner] non-zero exit code: %d", code)
		} else {
			log.Printf("[claude-runner] process error: %v", waitErr)
			return 1, stderrStr, 0, 0, fmt.Errorf("claude process error: %w", waitErr)
		}
	}

	// Parse the JSON output.
	parsed, parseErr := ParseOutput(stdoutData)
	if parseErr != nil {
		log.Printf("[claude-runner] FAILED to parse JSON output: %v", parseErr)
		log.Printf("[claude-runner] raw stdout (first 500 chars): %s", truncate(string(stdoutData), 500))
		_ = writeOutputFile(outputPath, stdoutData)
		return code, stderrStr, 0, 0, fmt.Errorf("failed to parse claude JSON output: %w", parseErr)
	}

	log.Printf("[claude-runner] parsed: cost=$%.4f, duration=%dms, is_error=%v, result_length=%d",
		parsed.CostUSD, parsed.DurationMS, parsed.IsError, len(parsed.Result))

	if parsed.IsError {
		log.Printf("[claude-runner] claude reported error: %s", truncate(parsed.Result, 200))
		_ = writeOutputFile(outputPath, []byte(parsed.Result))
		return 1, stderrStr, parsed.CostUSD, parsed.DurationMS, fmt.Errorf("claude reported error: %s", truncate(parsed.Result, 200))
	}

	// Check if the agent already wrote the output file directly (via Write tool).
	if existingData, readErr := os.ReadFile(outputPath); readErr == nil && len(existingData) > 0 {
		// Validate it's JSON (starts with { or [).
		trimmed := bytes.TrimSpace(existingData)
		if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
			log.Printf("[claude-runner] agent wrote output file directly, keeping it (%d bytes)", len(existingData))
			return code, stderrStr, parsed.CostUSD, parsed.DurationMS, nil
		}
		// Agent wrote non-JSON; overwrite with result field.
		log.Printf("[claude-runner] agent output file exists but is not JSON, overwriting with result field")
	}
	// Agent didn't write the file, or wrote non-JSON — use result field.
	if writeErr := writeOutputFile(outputPath, []byte(parsed.Result)); writeErr != nil {
		return code, stderrStr, parsed.CostUSD, parsed.DurationMS, fmt.Errorf("write output file: %w", writeErr)
	}

	log.Printf("[claude-runner] SUCCESS: wrote %d bytes to %s", len(parsed.Result), outputPath)
	return code, stderrStr, parsed.CostUSD, parsed.DurationMS, nil
}

// writeOutputFile creates parent directories and writes data to path.
func writeOutputFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// truncate returns the first n bytes of s, appending "..." if truncated.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// DefaultClaudeRunner returns a ClaudeRunner configured with standard
// defaults for the adversarial spec workflow. If otelPort > 0, OTEL
// environment variables are set so child Claude processes export
// telemetry back to the embedded OTLP receiver.
func DefaultClaudeRunner(workspaceDir string, otelPort int) *ClaudeRunner {
	env := map[string]string{
		"CLAUDE_CODE_MAX_TURNS":        "50",
		"CLAUDE_CODE_ENABLE_TELEMETRY": "1",
	}
	if otelPort > 0 {
		env["OTEL_METRICS_EXPORTER"] = "otlp"
		env["OTEL_LOGS_EXPORTER"] = "otlp"
		env["OTEL_EXPORTER_OTLP_PROTOCOL"] = "http/json"
		env["OTEL_EXPORTER_OTLP_ENDPOINT"] = fmt.Sprintf("http://localhost:%d", otelPort)
		env["OTEL_METRIC_EXPORT_INTERVAL"] = "10000"
		env["OTEL_LOGS_EXPORT_INTERVAL"] = "5000"
	}

	return &ClaudeRunner{
		Command:      "claude",
		Args:         []string{"--dangerously-skip-permissions", "--output-format", "json", "--verbose"},
		Timeout:      600 * time.Second,
		WorkspaceDir: workspaceDir,
		Env:          env,
	}
}
