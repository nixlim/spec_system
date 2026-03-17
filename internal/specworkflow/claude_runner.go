// Package specworkflow defines the core types for the adversarial spec review
// workflow. This file implements ClaudeRunner, an AgentRunner that executes
// the Claude CLI as a subprocess to perform agent tasks.
package specworkflow

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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

// claudeOutput represents the JSON structure emitted by claude --output-format json.
type claudeOutput struct {
	Result     string  `json:"result"`
	CostUSD    float64 `json:"cost_usd"`
	DurationMS int64   `json:"duration_ms"`
	IsError    bool    `json:"is_error"`
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

// ParseOutput parses the Claude CLI JSON output and extracts the result,
// cost, duration, and error status. Exported for testing without running
// the actual CLI.
func ParseOutput(data []byte) (*claudeOutput, error) {
	var out claudeOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse claude output: %w", err)
	}
	return &out, nil
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

	if startErr := cmd.Start(); startErr != nil {
		return 1, "", 0, 0, fmt.Errorf("start claude process: %w", startErr)
	}

	// Read stdout fully.
	stdoutData, readErr := io.ReadAll(stdoutPipe)
	if readErr != nil {
		return 1, "", 0, 0, fmt.Errorf("read stdout: %w", readErr)
	}

	// Read stderr fully.
	stderrBuf, _ = io.ReadAll(stderrPipe)

	waitErr := cmd.Wait()

	stderrStr := string(stderrBuf)

	// Check for context timeout.
	if ctx.Err() == context.DeadlineExceeded {
		return 1, stderrStr, 0, 0, fmt.Errorf("claude process timed out after %v", timeout)
	}

	// Determine exit code.
	code := 0
	if waitErr != nil {
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			return 1, stderrStr, 0, 0, fmt.Errorf("claude process error: %w", waitErr)
		}
	}

	// Parse the JSON output.
	parsed, parseErr := ParseOutput(stdoutData)
	if parseErr != nil {
		// If we can't parse output, write raw stdout to outputPath and report.
		_ = writeOutputFile(outputPath, stdoutData)
		return code, stderrStr, 0, 0, fmt.Errorf("failed to parse claude JSON output: %w", parseErr)
	}

	if parsed.IsError {
		_ = writeOutputFile(outputPath, []byte(parsed.Result))
		return 1, stderrStr, parsed.CostUSD, parsed.DurationMS, fmt.Errorf("claude reported error: %s", truncate(parsed.Result, 200))
	}

	// Write the result field content to the output path.
	if writeErr := writeOutputFile(outputPath, []byte(parsed.Result)); writeErr != nil {
		return code, stderrStr, parsed.CostUSD, parsed.DurationMS, fmt.Errorf("write output file: %w", writeErr)
	}

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
