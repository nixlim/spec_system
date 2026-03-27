package specworkflow

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// CodexRunner implements AgentRunner by invoking the Codex CLI as a
// subprocess. It passes the prompt via stdin and uses --output-last-message
// to write the result to a file.
type CodexRunner struct {
	// Command is the path or name of the Codex CLI executable (e.g. "codex").
	Command string
	// Model is the model to pass via -m flag (e.g. "gpt-5.4").
	// If empty, the -m flag is omitted and codex uses its default model.
	Model string
	// WorkspaceDir is the --cd directory for the codex subprocess.
	WorkspaceDir string
	// SchemaBytes is the JSON schema bytes written to a temp file for --output-schema.
	SchemaBytes []byte
}

// buildArgs constructs the argument list for the codex CLI invocation.
// schemaPath is the path to a temp file containing the JSON schema; empty
// string means no schema constraint is applied.
// outputPath is where codex writes its last message.
func (r *CodexRunner) buildArgs(schemaPath string, outputPath string) []string {
	args := []string{"exec", "--full-auto"}

	if r.Model != "" {
		args = append(args, "-m", r.Model)
	}

	if schemaPath != "" {
		args = append(args, "--output-schema", schemaPath)
	}
	args = append(args, "--output-last-message", outputPath)

	if r.WorkspaceDir != "" {
		args = append(args, "--cd", r.WorkspaceDir)
	}

	args = append(args, "--ephemeral", "-")

	return args
}

// Run implements AgentRunner. It launches the Codex CLI with the given
// prompt on stdin, writes the result to outputPath via --output-last-message,
// and returns execution metadata. Cost is always 0 (codex cost not tracked).
func (r *CodexRunner) Run(prompt string, outputPath string, timeoutSeconds int) (exitCode int, stderr string, costUSD float64, durationMS int64, err error) {
	if prompt == "" {
		return 1, "", 0, 0, fmt.Errorf("empty prompt")
	}
	if outputPath == "" {
		return 1, "", 0, 0, fmt.Errorf("empty output path")
	}

	// Write schema to a temp file when schema bytes are provided.
	var schemaPath string
	if len(r.SchemaBytes) > 0 {
		schemaFile, tmpErr := os.CreateTemp("", "codex-schema-*.json")
		if tmpErr != nil {
			return 1, "", 0, 0, fmt.Errorf("create schema temp file: %w", tmpErr)
		}
		defer os.Remove(schemaFile.Name())

		if _, writeErr := schemaFile.Write(r.SchemaBytes); writeErr != nil {
			schemaFile.Close()
			return 1, "", 0, 0, fmt.Errorf("write schema temp file: %w", writeErr)
		}
		schemaFile.Close()
		schemaPath = schemaFile.Name()
	}

	// Build command.
	args := r.buildArgs(schemaPath, outputPath)
	cmd := exec.Command(r.Command, args...)
	cmd.Stdin = strings.NewReader(prompt)

	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	log.Printf("[codex-runner] dispatching: %s %v", r.Command, args[:3])
	log.Printf("[codex-runner] working dir: %s", r.WorkspaceDir)
	log.Printf("[codex-runner] output path: %s", outputPath)
	log.Printf("[codex-runner] timeout: %ds", timeoutSeconds)
	log.Printf("[codex-runner] prompt length: %d chars", len(prompt))

	// Start the process and measure time.
	startTime := time.Now()
	if startErr := cmd.Start(); startErr != nil {
		log.Printf("[codex-runner] FAILED to start process: %v", startErr)
		return 1, "", 0, 0, fmt.Errorf("start codex process: %w", startErr)
	}
	log.Printf("[codex-runner] process started (PID %d)", cmd.Process.Pid)

	// Wait for completion or timeout.
	type waitResult struct {
		err error
	}
	done := make(chan waitResult, 1)
	go func() {
		done <- waitResult{err: cmd.Wait()}
	}()

	timeout := time.Duration(timeoutSeconds) * time.Second
	select {
	case <-time.After(timeout):
		log.Printf("[codex-runner] TIMEOUT after %v, sending SIGTERM", timeout)
		_ = cmd.Process.Signal(syscall.SIGTERM)

		// Wait up to 2 seconds for graceful shutdown.
		select {
		case <-done:
			log.Printf("[codex-runner] process exited after SIGTERM")
		case <-time.After(2 * time.Second):
			log.Printf("[codex-runner] process did not exit after SIGTERM, sending SIGKILL")
			_ = cmd.Process.Kill()
			<-done
		}

		elapsed := time.Since(startTime)
		return 1, stderrBuf.String(), 0, elapsed.Milliseconds(), fmt.Errorf("codex process timed out after %v", timeout)

	case res := <-done:
		elapsed := time.Since(startTime)
		stderrStr := stderrBuf.String()

		log.Printf("[codex-runner] process exited after %v, stderr=%d bytes",
			elapsed.Round(time.Millisecond), stderrBuf.Len())

		if len(stderrStr) > 0 {
			// Log the header (first 500 chars) and the tail (last 2000 chars)
			// separately — the error is usually at the end.
			log.Printf("[codex-runner] stderr head: %s", truncate(stderrStr, 500))
			if len(stderrStr) > 500 {
				tail := stderrStr
				if len(tail) > 2000 {
					tail = tail[len(tail)-2000:]
				}
				log.Printf("[codex-runner] stderr tail: %s", tail)
			}
		}

		// Determine exit code.
		code := 0
		if res.err != nil {
			if exitErr, ok := res.err.(*exec.ExitError); ok {
				code = exitErr.ExitCode()
				log.Printf("[codex-runner] non-zero exit code: %d", code)
			} else {
				log.Printf("[codex-runner] process error: %v", res.err)
				return 1, stderrStr, 0, elapsed.Milliseconds(), fmt.Errorf("codex process error: %w", res.err)
			}
		}

		if code == 0 {
			log.Printf("[codex-runner] SUCCESS: duration=%dms", elapsed.Milliseconds())
		} else {
			log.Printf("[codex-runner] FAILED: exit_code=%d, duration=%dms", code, elapsed.Milliseconds())
		}
		return code, stderrStr, 0, elapsed.Milliseconds(), nil
	}
}

// DefaultCodexRunner returns a CodexRunner configured with standard defaults.
func DefaultCodexRunner(model string, workspaceDir string, schemaBytes []byte) *CodexRunner {
	return &CodexRunner{
		Command:      "codex",
		Model:        model,
		WorkspaceDir: workspaceDir,
		SchemaBytes:  schemaBytes,
	}
}
