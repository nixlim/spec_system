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
	"strings"
	"time"

	"github.com/foundry-zero/adversarial-spec-system/internal/process"
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
	// Model is the Claude model passed via --model flag (e.g. "claude-opus-4-6").
	// If empty, the claude CLI uses its own default model.
	Model string
	// Ctx is the parent context for subprocess execution. When cancelled, any
	// running subprocess is killed immediately via exec.CommandContext. If nil,
	// context.Background() is used as the parent.
	Ctx context.Context
	// Timeout is the maximum duration for a single agent invocation.
	// If zero, defaults to 120 seconds.
	Timeout time.Duration
	// WorkspaceDir is the working directory for the subprocess.
	WorkspaceDir string
	// Env holds additional environment variables for the subprocess.
	// These are merged with the current process environment.
	Env map[string]string
	// JSONSchema is an optional JSON Schema string for structured output
	// validation. When set, --json-schema is appended to the CLI args.
	// The CLI will validate the output against this schema.
	JSONSchema string
	// Tracker is an optional ProcessTracker for recording subprocess lifecycle.
	// When set, Register is called after cmd.Start() and RecordEnd after cmd.Wait().
	Tracker *process.ProcessTracker
	// Feature identifies the workflow feature for process tracking events.
	Feature string
	// Role identifies the agent role for process tracking events.
	Role string
}

// claudeOutput is the normalised result extracted from the Claude CLI output.
type claudeOutput struct {
	Result           string          `json:"result"`
	StructuredOutput json.RawMessage `json:"structured_output,omitempty"`
	CostUSD          float64         `json:"cost_usd"`
	DurationMS       int64           `json:"duration_ms"`
	IsError          bool            `json:"is_error"`
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
	// StructuredOutput contains validated JSON when --json-schema is used.
	StructuredOutput json.RawMessage `json:"structured_output,omitempty"`
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

// WithJSONSchema returns a copy of the runner with the given JSON schema set.
// The original runner is not modified.
func (r *ClaudeRunner) WithJSONSchema(schema string) *ClaudeRunner {
	clone := *r
	clone.JSONSchema = schema
	return &clone
}

// WithModel returns a copy of the runner with the given model set.
// The original runner is not modified. Passing an empty string returns an
// unmodified copy (no --model flag will be emitted).
func (r *ClaudeRunner) WithModel(model string) *ClaudeRunner {
	clone := *r
	clone.Model = model
	return &clone
}

// WithContext returns a copy of the runner with the given parent context set.
// When the context is cancelled, any running subprocess is killed immediately.
// The original runner is not modified.
func (r *ClaudeRunner) WithContext(ctx context.Context) *ClaudeRunner {
	clone := *r
	clone.Ctx = ctx
	return &clone
}

// ForJSONOnly returns a copy of the runner configured for pure JSON production:
// --json-schema set, all tools disabled (--tools ""), so structured_output is
// guaranteed to be populated. Use this for merge/combine agents that receive
// all input inline and only need to produce JSON.
func (r *ClaudeRunner) ForJSONOnly(schema string) *ClaudeRunner {
	clone := *r
	clone.JSONSchema = schema
	// Replace --dangerously-skip-permissions with --tools "" to disable all tools.
	// This ensures the agent produces a direct text response (no tool use),
	// which makes --json-schema reliably populate structured_output.
	var filteredArgs []string
	for _, arg := range clone.Args {
		if arg != "--dangerously-skip-permissions" {
			filteredArgs = append(filteredArgs, arg)
		}
	}
	filteredArgs = append(filteredArgs, "--tools", "")
	clone.Args = filteredArgs
	return &clone
}

// BuildCommand constructs the exec.Cmd for a given prompt and timeout context.
// Exported for testing command construction without executing.
func (r *ClaudeRunner) BuildCommand(ctx context.Context, prompt string) *exec.Cmd {
	args := []string{"-p", prompt}
	args = append(args, r.Args...)
	if r.Model != "" {
		args = append(args, "--model", r.Model)
	}
	if r.JSONSchema != "" {
		args = append(args, "--json-schema", r.JSONSchema)
	}

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
			out.StructuredOutput = ev.StructuredOutput
			out.CostUSD = ev.CostUSD
			out.DurationMS = ev.DurationMS
			out.IsError = ev.IsError
			log.Printf("[claude-runner] found result event: cost=$%.4f, duration=%dms, is_error=%v, result_len=%d, structured_output_len=%d",
				ev.CostUSD, ev.DurationMS, ev.IsError, len(ev.Result), len(ev.StructuredOutput))

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

	parentCtx := r.Ctx
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	ctx, cancel := context.WithTimeout(parentCtx, timeout)
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

	// Record mtime of output file before running (if it exists) so we can
	// detect if the agent wrote to it during execution.
	var outputExistedBefore bool
	var outputMtimeBefore time.Time
	if fi, statErr := os.Stat(outputPath); statErr == nil {
		outputExistedBefore = true
		outputMtimeBefore = fi.ModTime()
	}

	startTime := time.Now()
	if startErr := cmd.Start(); startErr != nil {
		log.Printf("[claude-runner] FAILED to start process: %v", startErr)
		return 1, "", 0, 0, fmt.Errorf("start claude process: %w", startErr)
	}
	log.Printf("[claude-runner] process started (PID %d)", cmd.Process.Pid)

	// Register with process tracker (if available).
	if r.Tracker != nil {
		if regErr := r.Tracker.Register(process.ProcessRecord{
			Feature:   r.Feature,
			Role:      r.Role,
			PID:       cmd.Process.Pid,
			StartedAt: startTime,
		}); regErr != nil {
			log.Printf("[claude-runner] WARNING: failed to register PID %d with process tracker: %v (process runs unmanaged)", cmd.Process.Pid, regErr)
		}
	}

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
		if r.Tracker != nil {
			r.Tracker.RecordEnd(cmd.Process.Pid, 1)
		}
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
			if r.Tracker != nil {
				r.Tracker.RecordEnd(cmd.Process.Pid, 1)
			}
			return 1, stderrStr, 0, 0, fmt.Errorf("claude process error: %w", waitErr)
		}
	}

	// Record process end with tracker (if available).
	if r.Tracker != nil {
		r.Tracker.RecordEnd(cmd.Process.Pid, code)
		// If the user explicitly killed this process, return a sentinel that
		// suppresses retry logic throughout the call stack.
		if r.Tracker.IsKillRequested(cmd.Process.Pid) {
			log.Printf("[claude-runner] PID %d was killed by user request — not retrying", cmd.Process.Pid)
			return code, stderrStr, 0, 0, fmt.Errorf("agent killed by user: %w", ErrProcessKilled)
		}
	}

	// Disk-first: prefer the file the agent actually wrote over anything in
	// stdout. The drafter prompt instructs the agent to produce a validated
	// JSON file via outvalid; once that file exists, the agent's terminal
	// stdout response is irrelevant (and is often a markdown summary like
	// "All three outputs are complete and validated:" rather than JSON,
	// which previously caused false-negative invalid_json escalations).
	//
	// We do this BEFORE attempting to parse stdout so a successful agent
	// run is not lost to a stdout-parse failure. Cost/duration metadata
	// from stdout is best-effort: if stdout parses we report it; if not,
	// we still succeed and report zeros.
	agentWroteFile := false
	if fi, statErr := os.Stat(outputPath); statErr == nil {
		if !outputExistedBefore || fi.ModTime().After(outputMtimeBefore) {
			agentWroteFile = true
		}
	}

	var diskData []byte
	diskHasValidJSON := false
	if agentWroteFile {
		if existingData, readErr := os.ReadFile(outputPath); readErr == nil && len(existingData) > 0 {
			diskData = existingData
			if json.Valid(bytes.TrimSpace(existingData)) {
				diskHasValidJSON = true
			}
		}
	}

	// Parse the JSON output for cost/duration metadata. Treat failure as
	// non-fatal when the agent already wrote a valid JSON file on disk.
	parsed, parseErr := ParseOutput(stdoutData)
	if parseErr != nil {
		log.Printf("[claude-runner] FAILED to parse stdout: %v", parseErr)
		log.Printf("[claude-runner] raw stdout (first 500 chars): %s", truncate(string(stdoutData), 500))
		if diskHasValidJSON {
			log.Printf("[claude-runner] stdout unparseable but agent wrote valid JSON to %s (%d bytes), accepting it", outputPath, len(diskData))
			return code, stderrStr, 0, 0, nil
		}
		_ = writeOutputFile(outputPath, stdoutData)
		return code, stderrStr, 0, 0, fmt.Errorf("failed to parse claude JSON output: %w", parseErr)
	}

	log.Printf("[claude-runner] parsed: cost=$%.4f, duration=%dms, is_error=%v, result_length=%d",
		parsed.CostUSD, parsed.DurationMS, parsed.IsError, len(parsed.Result))

	// If the agent wrote valid JSON to outputPath, that wins regardless of
	// what the result text or is_error flag says — the file is the source
	// of truth.
	if diskHasValidJSON {
		log.Printf("[claude-runner] agent wrote valid JSON output file directly, keeping it (%d bytes)", len(diskData))
		return code, stderrStr, parsed.CostUSD, parsed.DurationMS, nil
	}

	if parsed.IsError {
		log.Printf("[claude-runner] claude reported error: %s", truncate(parsed.Result, 200))
		_ = writeOutputFile(outputPath, []byte(parsed.Result))
		return 1, stderrStr, parsed.CostUSD, parsed.DurationMS, fmt.Errorf("claude reported error: %s", truncate(parsed.Result, 200))
	}

	if agentWroteFile && len(diskData) > 0 {
		trimmed := bytes.TrimSpace(diskData)
		log.Printf("[claude-runner] agent wrote output file but it's not JSON (starts with %q), overwriting", string(trimmed[:min(20, len(trimmed))]))
	}

	// Agent didn't write a valid JSON file — prefer structured_output (from --json-schema),
	// fall back to the result field, attempting JSON extraction if needed.
	var outputData []byte
	if len(parsed.StructuredOutput) > 0 && json.Valid(parsed.StructuredOutput) {
		outputData = parsed.StructuredOutput
		log.Printf("[claude-runner] using structured_output field (%d bytes)", len(outputData))
	} else {
		outputData = []byte(parsed.Result)
		trimmedResult := bytes.TrimSpace(outputData)
		if len(trimmedResult) > 0 && (trimmedResult[0] == '{' || trimmedResult[0] == '[') {
			log.Printf("[claude-runner] result field is JSON, writing to %s (%d bytes)", outputPath, len(outputData))
		} else {
			// Result is not pure JSON — try to extract a JSON object from the text.
			// The agent may have embedded JSON in its response along with commentary.
			if extracted := extractJSONFromText(parsed.Result); extracted != nil {
				log.Printf("[claude-runner] extracted JSON object from text result (%d bytes from %d)", len(extracted), len(parsed.Result))
				outputData = extracted
			} else {
				log.Printf("[claude-runner] WARNING: result field is not JSON and no JSON could be extracted (starts with %q), writing anyway (%d bytes)",
					truncate(parsed.Result, 40), len(outputData))
			}
		}
	}
	if writeErr := writeOutputFile(outputPath, outputData); writeErr != nil {
		return code, stderrStr, parsed.CostUSD, parsed.DurationMS, fmt.Errorf("write output file: %w", writeErr)
	}

	log.Printf("[claude-runner] SUCCESS: wrote %d bytes to %s", len(outputData), outputPath)
	return code, stderrStr, parsed.CostUSD, parsed.DurationMS, nil
}

// extractJSONFromText attempts to find and extract the largest valid JSON object
// from a text string that may contain commentary around it. This handles the
// common case where an agent produces text like "Here's the output: {...}" or
// embeds JSON in markdown code fences.
func extractJSONFromText(text string) []byte {
	// Try extracting from markdown code fences first: ```json ... ``` or ``` ... ```
	fenceStarts := []string{"```json\n", "```json\r\n", "```\n", "```\r\n"}
	for _, start := range fenceStarts {
		idx := strings.Index(text, start)
		if idx < 0 {
			continue
		}
		content := text[idx+len(start):]
		endIdx := strings.Index(content, "```")
		if endIdx < 0 {
			continue
		}
		candidate := strings.TrimSpace(content[:endIdx])
		if len(candidate) > 0 && candidate[0] == '{' && json.Valid([]byte(candidate)) {
			return []byte(candidate)
		}
	}

	// Try finding the largest top-level JSON object by scanning for { and matching }.
	// Find the first { and try to parse from there.
	firstBrace := strings.Index(text, "{")
	if firstBrace < 0 {
		return nil
	}

	// Try progressively from the first { to find valid JSON.
	candidate := text[firstBrace:]
	// Find the matching closing brace by trying json.Valid on increasingly larger substrings.
	// Start from the end and work backwards for the largest valid object.
	for i := len(candidate); i > 0; i-- {
		if candidate[i-1] == '}' {
			sub := candidate[:i]
			if json.Valid([]byte(sub)) {
				return []byte(sub)
			}
		}
	}

	return nil
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

// CloneForAgent returns a shallow copy of this ClaudeRunner with an
// additional OTEL resource attribute identifying the agent. This allows
// the OTEL receiver to attribute telemetry to specific agents when
// multiple reviewers run in parallel.
func (r *ClaudeRunner) CloneForAgent(agentName string) AgentRunner {
	envCopy := make(map[string]string, len(r.Env)+1)
	for k, v := range r.Env {
		envCopy[k] = v
	}
	// Append workflow.agent to existing resource attributes.
	existing := envCopy["OTEL_RESOURCE_ATTRIBUTES"]
	if existing != "" {
		envCopy["OTEL_RESOURCE_ATTRIBUTES"] = existing + ",workflow.agent=" + agentName
	} else {
		envCopy["OTEL_RESOURCE_ATTRIBUTES"] = "workflow.agent=" + agentName
	}
	return &ClaudeRunner{
		Command:      r.Command,
		Args:         r.Args,
		Timeout:      r.Timeout,
		WorkspaceDir: r.WorkspaceDir,
		Env:          envCopy,
		Tracker:      r.Tracker,
		Feature:      r.Feature,
		Role:         agentName,
	}
}

// DefaultClaudeRunner returns a ClaudeRunner configured with standard
// defaults for the adversarial spec workflow. If otelPort > 0, OTEL
// environment variables are set so child Claude processes export
// telemetry back to the embedded OTLP receiver. The featureName is
// included as a workflow.feature resource attribute so the OTEL receiver
// can partition telemetry by workflow. If model is non-empty, it is
// passed via the --model flag to the claude CLI.
func DefaultClaudeRunner(workspaceDir string, otelPort int, featureName string, model string) *ClaudeRunner {
	env := map[string]string{
		"CLAUDE_CODE_MAX_TURNS":        "50",
		"CLAUDE_CODE_ENABLE_TELEMETRY": "1",
	}
	if otelPort > 0 {
		env["OTEL_METRICS_EXPORTER"] = "otlp"
		env["OTEL_LOGS_EXPORTER"] = "otlp"
		env["OTEL_EXPORTER_OTLP_PROTOCOL"] = "grpc"
		env["OTEL_EXPORTER_OTLP_ENDPOINT"] = fmt.Sprintf("http://localhost:%d", otelPort)
		env["OTEL_METRIC_EXPORT_INTERVAL"] = "5000"
		env["OTEL_LOGS_EXPORT_INTERVAL"] = "2000"
		env["OTEL_LOG_TOOL_DETAILS"] = "1"
		// Tag child processes so the OTEL receiver can filter out
		// telemetry from unrelated Claude Code instances on the system.
		env["OTEL_SERVICE_NAME"] = "adversarial-spec-system"
		// Include workflow.feature so the OTEL receiver can partition
		// telemetry accumulators by workflow.
		if featureName != "" {
			env["OTEL_RESOURCE_ATTRIBUTES"] = "workflow.feature=" + featureName
		}
	}

	return &ClaudeRunner{
		Command:      "claude",
		Args:         []string{"--dangerously-skip-permissions", "--output-format", "json", "--verbose"},
		Model:        model,
		Timeout:      1800 * time.Second,
		WorkspaceDir: workspaceDir,
		Env:          env,
	}
}
