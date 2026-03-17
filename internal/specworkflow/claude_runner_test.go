package specworkflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// TestBuildCommand
// ---------------------------------------------------------------------------

func TestBuildCommand_BasicArgs(t *testing.T) {
	runner := &ClaudeRunner{
		Command: "claude",
		Args:    []string{"--dangerously-skip-permissions", "--output-format", "json"},
	}

	ctx := context.Background()
	cmd := runner.BuildCommand(ctx, "Hello world")

	// The command should be "claude".
	if cmd.Path == "" {
		// exec.CommandContext resolves the path; just check it's non-empty.
		t.Error("expected non-empty command path")
	}

	// Check arguments: -p <prompt> followed by runner.Args.
	args := cmd.Args[1:] // cmd.Args[0] is the command name
	if len(args) < 5 {
		t.Fatalf("expected at least 5 args, got %d: %v", len(args), args)
	}
	if args[0] != "-p" {
		t.Errorf("expected first arg '-p', got %q", args[0])
	}
	if args[1] != "Hello world" {
		t.Errorf("expected prompt 'Hello world', got %q", args[1])
	}
	if args[2] != "--dangerously-skip-permissions" {
		t.Errorf("expected '--dangerously-skip-permissions', got %q", args[2])
	}
	if args[3] != "--output-format" {
		t.Errorf("expected '--output-format', got %q", args[3])
	}
	if args[4] != "json" {
		t.Errorf("expected 'json', got %q", args[4])
	}
}

func TestBuildCommand_WorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	runner := &ClaudeRunner{
		Command:      "claude",
		WorkspaceDir: dir,
	}

	cmd := runner.BuildCommand(context.Background(), "test")
	if cmd.Dir != dir {
		t.Errorf("expected Dir=%q, got %q", dir, cmd.Dir)
	}
}

func TestBuildCommand_EnvironmentVariables(t *testing.T) {
	runner := &ClaudeRunner{
		Command: "claude",
		Env: map[string]string{
			"CLAUDE_CODE_MAX_TURNS": "50",
			"CUSTOM_VAR":            "custom_value",
		},
	}

	cmd := runner.BuildCommand(context.Background(), "test")

	// The environment should contain our custom variables.
	envMap := make(map[string]string)
	for _, e := range cmd.Env {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}

	if envMap["CLAUDE_CODE_MAX_TURNS"] != "50" {
		t.Errorf("expected CLAUDE_CODE_MAX_TURNS=50, got %q", envMap["CLAUDE_CODE_MAX_TURNS"])
	}
	if envMap["CUSTOM_VAR"] != "custom_value" {
		t.Errorf("expected CUSTOM_VAR=custom_value, got %q", envMap["CUSTOM_VAR"])
	}
}

// ---------------------------------------------------------------------------
// TestParseOutput
// ---------------------------------------------------------------------------

func TestParseOutput_Success(t *testing.T) {
	data := []byte(`{
		"result": "{\"schema_version\":\"1.0\",\"agent\":\"discovery\"}",
		"cost_usd": 0.0523,
		"duration_ms": 15432,
		"is_error": false
	}`)

	out, err := ParseOutput(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.IsError {
		t.Error("expected is_error=false")
	}
	if out.CostUSD < 0.05 || out.CostUSD > 0.06 {
		t.Errorf("expected cost ~0.0523, got %f", out.CostUSD)
	}
	if out.DurationMS != 15432 {
		t.Errorf("expected duration 15432, got %d", out.DurationMS)
	}
	if !strings.Contains(out.Result, "schema_version") {
		t.Errorf("expected result to contain schema_version, got %q", out.Result)
	}
}

func TestParseOutput_Error(t *testing.T) {
	data := []byte(`{
		"result": "Rate limit exceeded",
		"cost_usd": 0.001,
		"duration_ms": 500,
		"is_error": true
	}`)

	out, err := ParseOutput(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !out.IsError {
		t.Error("expected is_error=true")
	}
	if out.Result != "Rate limit exceeded" {
		t.Errorf("expected 'Rate limit exceeded', got %q", out.Result)
	}
}

func TestParseOutput_StreamArray(t *testing.T) {
	// Simulates the verbose JSON stream format from claude -p --output-format json --verbose
	data := []byte(`[
		{"type":"system","subtype":"init","session_id":"abc123"},
		{"type":"assistant","message":{"content":[{"type":"text","text":"thinking..."}]}},
		{"type":"result","result":"{\"schema_version\":\"1.0\"}","cost_usd":0.15,"duration_ms":30000,"is_error":false}
	]`)

	out, err := ParseOutput(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.IsError {
		t.Error("expected is_error=false")
	}
	if out.CostUSD < 0.14 || out.CostUSD > 0.16 {
		t.Errorf("expected cost ~0.15, got %f", out.CostUSD)
	}
	if out.DurationMS != 30000 {
		t.Errorf("expected duration 30000, got %d", out.DurationMS)
	}
	if !strings.Contains(out.Result, "schema_version") {
		t.Errorf("expected result to contain schema_version, got %q", out.Result)
	}
}

func TestParseOutput_StreamArrayNoResultEvent(t *testing.T) {
	// Some versions may not emit a "result" event; fall back to last assistant text.
	data := []byte(`[
		{"type":"system","subtype":"init"},
		{"type":"assistant","message":{"content":[{"type":"text","text":"Here is the output: {\"data\":\"value\"}"}]},"cost_usd":0.05}
	]`)

	out, err := ParseOutput(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.Result, "data") {
		t.Errorf("expected result from last assistant text, got %q", out.Result)
	}
}

func TestParseOutput_StreamArrayError(t *testing.T) {
	data := []byte(`[
		{"type":"system","subtype":"init"},
		{"type":"result","result":"Rate limit exceeded","cost_usd":0.001,"duration_ms":500,"is_error":true}
	]`)

	out, err := ParseOutput(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !out.IsError {
		t.Error("expected is_error=true")
	}
}

func TestParseOutput_EmptyStream(t *testing.T) {
	data := []byte(`[]`)
	_, err := ParseOutput(data)
	if err == nil {
		t.Fatal("expected error for empty stream")
	}
}

func TestParseOutput_InvalidJSON(t *testing.T) {
	data := []byte(`not json at all`)

	_, err := ParseOutput(data)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestParseOutput_EmptyInput(t *testing.T) {
	_, err := ParseOutput([]byte(""))
	if err == nil {
		t.Fatal("expected error for empty input")
	}
}

func TestParseOutput_EmptyResult(t *testing.T) {
	data := []byte(`{
		"result": "",
		"cost_usd": 0,
		"duration_ms": 0,
		"is_error": false
	}`)

	out, err := ParseOutput(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Result != "" {
		t.Errorf("expected empty result, got %q", out.Result)
	}
}

// ---------------------------------------------------------------------------
// TestWriteOutputFile
// ---------------------------------------------------------------------------

func TestWriteOutputFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "output.json")

	data := []byte(`{"test": true}`)
	if err := writeOutputFile(path, data); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read output file: %v", err)
	}
	if string(content) != string(data) {
		t.Errorf("expected %q, got %q", string(data), string(content))
	}
}

// ---------------------------------------------------------------------------
// TestTruncate
// ---------------------------------------------------------------------------

func TestTruncate(t *testing.T) {
	short := "hello"
	if truncate(short, 10) != "hello" {
		t.Errorf("expected no truncation for short string")
	}

	long := strings.Repeat("a", 300)
	result := truncate(long, 200)
	if len(result) != 203 { // 200 + "..."
		t.Errorf("expected length 203, got %d", len(result))
	}
	if !strings.HasSuffix(result, "...") {
		t.Error("expected truncated string to end with '...'")
	}
}

// ---------------------------------------------------------------------------
// TestDefaultClaudeRunner
// ---------------------------------------------------------------------------

func TestDefaultClaudeRunner(t *testing.T) {
	runner := DefaultClaudeRunner("/tmp/workspace", 0)

	if runner.Command != "claude" {
		t.Errorf("expected command 'claude', got %q", runner.Command)
	}
	if runner.WorkspaceDir != "/tmp/workspace" {
		t.Errorf("expected workspace '/tmp/workspace', got %q", runner.WorkspaceDir)
	}
	if runner.Timeout != 600*time.Second {
		t.Errorf("expected timeout 600s, got %v", runner.Timeout)
	}
	if runner.Env["CLAUDE_CODE_MAX_TURNS"] != "50" {
		t.Errorf("expected CLAUDE_CODE_MAX_TURNS=50, got %q", runner.Env["CLAUDE_CODE_MAX_TURNS"])
	}
	if runner.Env["CLAUDE_CODE_ENABLE_TELEMETRY"] != "1" {
		t.Errorf("expected CLAUDE_CODE_ENABLE_TELEMETRY=1, got %q", runner.Env["CLAUDE_CODE_ENABLE_TELEMETRY"])
	}
	// With otelPort=0, no OTEL exporter vars should be set.
	if _, ok := runner.Env["OTEL_METRICS_EXPORTER"]; ok {
		t.Error("expected no OTEL_METRICS_EXPORTER when otelPort=0")
	}

	// Verify args include the expected flags.
	expectedArgs := []string{"--dangerously-skip-permissions", "--output-format", "json", "--verbose"}
	if len(runner.Args) != len(expectedArgs) {
		t.Fatalf("expected %d args, got %d", len(expectedArgs), len(runner.Args))
	}
	for i, expected := range expectedArgs {
		if runner.Args[i] != expected {
			t.Errorf("args[%d]: expected %q, got %q", i, expected, runner.Args[i])
		}
	}
}

func TestDefaultClaudeRunner_WithOTELPort(t *testing.T) {
	runner := DefaultClaudeRunner("/tmp/workspace", 4318)

	if runner.Env["OTEL_METRICS_EXPORTER"] != "otlp" {
		t.Errorf("expected OTEL_METRICS_EXPORTER=otlp, got %q", runner.Env["OTEL_METRICS_EXPORTER"])
	}
	if runner.Env["OTEL_LOGS_EXPORTER"] != "otlp" {
		t.Errorf("expected OTEL_LOGS_EXPORTER=otlp, got %q", runner.Env["OTEL_LOGS_EXPORTER"])
	}
	if runner.Env["OTEL_EXPORTER_OTLP_PROTOCOL"] != "http/json" {
		t.Errorf("expected OTEL_EXPORTER_OTLP_PROTOCOL=http/json, got %q", runner.Env["OTEL_EXPORTER_OTLP_PROTOCOL"])
	}
	want := "http://localhost:4318"
	if runner.Env["OTEL_EXPORTER_OTLP_ENDPOINT"] != want {
		t.Errorf("expected OTEL_EXPORTER_OTLP_ENDPOINT=%q, got %q", want, runner.Env["OTEL_EXPORTER_OTLP_ENDPOINT"])
	}
}

// ---------------------------------------------------------------------------
// TestClaudeRunnerImplementsInterface
// ---------------------------------------------------------------------------

func TestClaudeRunnerImplementsAgentRunner(t *testing.T) {
	// Compile-time interface check.
	var _ AgentRunner = (*ClaudeRunner)(nil)
}
