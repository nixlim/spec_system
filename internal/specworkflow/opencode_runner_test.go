package specworkflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenCodeRunner_BuildArgs_Basic(t *testing.T) {
	r := &OpenCodeRunner{
		Command: "opencode",
		Model:   "anthropic/claude-sonnet-4-5",
	}
	args := r.buildArgs("hello world")

	expected := []string{"run", "--format", "json", "--dangerously-skip-permissions", "-m", "anthropic/claude-sonnet-4-5", "hello world"}
	if len(args) != len(expected) {
		t.Fatalf("expected %d args, got %d: %v", len(expected), len(args), args)
	}
	for i, want := range expected {
		if args[i] != want {
			t.Errorf("arg[%d]: want %q, got %q", i, want, args[i])
		}
	}
}

func TestOpenCodeRunner_BuildArgs_NoModel(t *testing.T) {
	r := &OpenCodeRunner{
		Command: "opencode",
	}
	args := r.buildArgs("test prompt")

	for _, a := range args {
		if a == "-m" {
			t.Error("expected no -m flag when model is empty")
		}
	}
}

func TestOpenCodeRunner_BuildArgs_WithSchema(t *testing.T) {
	schema := []byte(`{"type":"object","properties":{"findings":{"type":"array"}}}`)
	r := &OpenCodeRunner{
		Command:     "opencode",
		Model:       "google/gemini-2.5-pro",
		SchemaBytes: schema,
	}
	args := r.buildArgs("analyze this")

	// The last arg is the full prompt with schema prefix.
	fullPrompt := args[len(args)-1]
	if !strings.Contains(fullPrompt, "You MUST respond with a single JSON object") {
		t.Error("expected schema prefix in prompt")
	}
	if !strings.Contains(fullPrompt, `"type":"object"`) {
		t.Error("expected schema content in prompt")
	}
	if !strings.Contains(fullPrompt, "analyze this") {
		t.Error("expected original prompt after schema prefix")
	}
}

func TestParseJSONLOutput_TextEvents(t *testing.T) {
	jsonl := strings.Join([]string{
		`{"type":"step_start","timestamp":"2026-01-01T00:00:00Z","part":{"id":"s1"}}`,
		`{"type":"text","timestamp":"2026-01-01T00:00:01Z","part":{"text":"Hello "}}`,
		`{"type":"text","timestamp":"2026-01-01T00:00:02Z","part":{"text":"world"}}`,
		`{"type":"step_finish","timestamp":"2026-01-01T00:00:03Z","part":{"reason":"end_turn","cost":0.0042,"tokens":{"input":100,"output":50}}}`,
	}, "\n")

	text, cost, err := parseJSONLOutput([]byte(jsonl))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", text)
	}
	if cost != 0.0042 {
		t.Errorf("expected cost 0.0042, got %f", cost)
	}
}

func TestParseJSONLOutput_MultipleStepFinish(t *testing.T) {
	jsonl := strings.Join([]string{
		`{"type":"text","part":{"text":"part1"}}`,
		`{"type":"step_finish","part":{"cost":0.01,"tokens":{"input":50,"output":25}}}`,
		`{"type":"text","part":{"text":"part2"}}`,
		`{"type":"step_finish","part":{"cost":0.02,"tokens":{"input":100,"output":50}}}`,
	}, "\n")

	text, cost, err := parseJSONLOutput([]byte(jsonl))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "part1part2" {
		t.Errorf("expected 'part1part2', got %q", text)
	}
	if cost != 0.03 {
		t.Errorf("expected cost 0.03, got %f", cost)
	}
}

func TestParseJSONLOutput_ErrorEvent(t *testing.T) {
	jsonl := `{"type":"error","error":{"name":"RateLimitError","data":{"message":"rate limit exceeded"}}}`

	_, _, err := parseJSONLOutput([]byte(jsonl))
	if err == nil {
		t.Fatal("expected error for error-only output")
	}
	if !strings.Contains(err.Error(), "rate limit exceeded") {
		t.Errorf("expected rate limit message in error, got: %v", err)
	}
}

func TestParseJSONLOutput_Empty(t *testing.T) {
	_, _, err := parseJSONLOutput([]byte(""))
	if err == nil {
		t.Fatal("expected error for empty output")
	}
}

func TestParseJSONLOutput_SkipsInvalidLines(t *testing.T) {
	jsonl := strings.Join([]string{
		`not json at all`,
		`{"type":"text","part":{"text":"valid"}}`,
		`{"type":"step_finish","part":{"cost":0.001}}`,
	}, "\n")

	text, cost, err := parseJSONLOutput([]byte(jsonl))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "valid" {
		t.Errorf("expected 'valid', got %q", text)
	}
	if cost != 0.001 {
		t.Errorf("expected cost 0.001, got %f", cost)
	}
}

func TestOpenCodeRunner_CloneForAgent(t *testing.T) {
	r := &OpenCodeRunner{
		Command:      "opencode",
		Model:        "anthropic/claude-sonnet-4-5",
		WorkspaceDir: "/tmp/ws",
		SchemaBytes:  []byte(`{"type":"object"}`),
		Feature:      "test-feature",
		Role:         "reviewer",
		Env:          map[string]string{"FOO": "bar"},
	}

	cloned := r.CloneForAgent("reviewer-clarity-opencode")

	oc, ok := cloned.(*OpenCodeRunner)
	if !ok {
		t.Fatal("CloneForAgent did not return *OpenCodeRunner")
	}

	if oc.Role != "reviewer-clarity-opencode" {
		t.Errorf("expected role 'reviewer-clarity-opencode', got %q", oc.Role)
	}
	if oc.Command != r.Command {
		t.Errorf("expected command %q, got %q", r.Command, oc.Command)
	}
	if oc.Model != r.Model {
		t.Errorf("expected model %q, got %q", r.Model, oc.Model)
	}
	if oc.WorkspaceDir != r.WorkspaceDir {
		t.Errorf("expected workspace %q, got %q", r.WorkspaceDir, oc.WorkspaceDir)
	}
	if string(oc.SchemaBytes) != string(r.SchemaBytes) {
		t.Error("schema bytes not preserved")
	}
	if oc.Feature != r.Feature {
		t.Errorf("expected feature %q, got %q", r.Feature, oc.Feature)
	}
	if oc.Env["FOO"] != "bar" {
		t.Error("expected FOO=bar in cloned env")
	}
	if !strings.Contains(oc.Env["OTEL_RESOURCE_ATTRIBUTES"], "workflow.agent=reviewer-clarity-opencode") {
		t.Errorf("expected OTEL resource attribute, got %q", oc.Env["OTEL_RESOURCE_ATTRIBUTES"])
	}
	// Verify clone doesn't mutate original.
	if _, ok := r.Env["OTEL_RESOURCE_ATTRIBUTES"]; ok {
		t.Error("original env was mutated by CloneForAgent")
	}
}

func TestOpenCodeRunner_CloneForAgent_SatisfiesAgentTagger(t *testing.T) {
	var runner AgentRunner = &OpenCodeRunner{
		Command: "opencode",
		Model:   "google/gemini-2.5-pro",
		Env:     map[string]string{},
	}

	tagged := taggedRunner(runner, "reviewer-security-opencode")
	oc, ok := tagged.(*OpenCodeRunner)
	if !ok {
		t.Fatal("taggedRunner did not return *OpenCodeRunner")
	}
	if oc.Role != "reviewer-security-opencode" {
		t.Errorf("expected role from taggedRunner, got %q", oc.Role)
	}
}

func TestSchemaPromptPrefix(t *testing.T) {
	schema := []byte(`{"type":"object","required":["findings"]}`)
	prefix := schemaPromptPrefix(schema)

	if !strings.Contains(prefix, "You MUST respond with a single JSON object") {
		t.Error("missing instruction in prefix")
	}
	if !strings.Contains(prefix, `"required":["findings"]`) {
		t.Error("missing schema in prefix")
	}
	if !strings.HasSuffix(prefix, "---\n\n") {
		t.Error("prefix should end with separator")
	}
}

func TestOpenCodeRunner_Run_WithMockBinary(t *testing.T) {
	// Create a mock opencode binary that outputs JSONL.
	tmpDir := t.TempDir()
	mockBin := filepath.Join(tmpDir, "opencode")

	// The mock script outputs JSONL with a JSON result.
	script := `#!/bin/sh
echo '{"type":"text","part":{"text":"{\"findings\":[]}"}}'
echo '{"type":"step_finish","part":{"cost":0.005,"tokens":{"input":200,"output":100}}}'
`
	if err := os.WriteFile(mockBin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	outputPath := filepath.Join(tmpDir, "output.json")
	r := &OpenCodeRunner{
		Command:      mockBin,
		Model:        "test/model",
		WorkspaceDir: tmpDir,
		Env:          make(map[string]string),
	}

	exitCode, _, costUSD, _, err := r.Run("test prompt", outputPath, 30)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if exitCode != 0 {
		t.Errorf("expected exit code 0, got %d", exitCode)
	}
	if costUSD != 0.005 {
		t.Errorf("expected cost 0.005, got %f", costUSD)
	}

	// Verify output file was written with valid JSON.
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if !json.Valid(data) {
		t.Errorf("output is not valid JSON: %s", data)
	}
}

func TestOpenCodeRunner_Run_EmptyPrompt(t *testing.T) {
	r := &OpenCodeRunner{Command: "opencode", Env: make(map[string]string)}
	_, _, _, _, err := r.Run("", "/tmp/out.json", 30)
	if err == nil {
		t.Error("expected error for empty prompt")
	}
}

func TestOpenCodeRunner_Run_EmptyOutputPath(t *testing.T) {
	r := &OpenCodeRunner{Command: "opencode", Env: make(map[string]string)}
	_, _, _, _, err := r.Run("prompt", "", 30)
	if err == nil {
		t.Error("expected error for empty output path")
	}
}

func TestDefaultOpenCodeRunner(t *testing.T) {
	r := DefaultOpenCodeRunner("anthropic/claude-sonnet-4-5", "/workspace", []byte(`{}`))
	if r.Command != "opencode" {
		t.Errorf("expected command 'opencode', got %q", r.Command)
	}
	if r.Model != "anthropic/claude-sonnet-4-5" {
		t.Errorf("expected model, got %q", r.Model)
	}
	if r.WorkspaceDir != "/workspace" {
		t.Errorf("expected workspace, got %q", r.WorkspaceDir)
	}
	if string(r.SchemaBytes) != `{}` {
		t.Error("schema bytes not set")
	}
}

func TestOpenCodeModelConfig_For(t *testing.T) {
	cfg := OpenCodeModelConfig{
		Default:  "anthropic/claude-sonnet-4-5",
		Reviewer: "google/gemini-2.5-pro",
	}

	if got := cfg.For("reviewer"); got != "google/gemini-2.5-pro" {
		t.Errorf("For(reviewer): want google/gemini-2.5-pro, got %q", got)
	}
	if got := cfg.For("drafter"); got != "anthropic/claude-sonnet-4-5" {
		t.Errorf("For(drafter): want default, got %q", got)
	}
	if got := cfg.For("unknown"); got != "anthropic/claude-sonnet-4-5" {
		t.Errorf("For(unknown): want default, got %q", got)
	}

	empty := OpenCodeModelConfig{}
	if got := empty.For("reviewer"); got != "" {
		t.Errorf("For(reviewer) on empty: want empty, got %q", got)
	}
}
