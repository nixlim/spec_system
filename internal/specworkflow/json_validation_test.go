package specworkflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mockValidationRunner is a test AgentRunner that writes predetermined output.
type mockValidationRunner struct {
	outputs []string // output to write on each successive call
	callIdx int
}

func (m *mockValidationRunner) Run(prompt string, outputPath string, timeoutSeconds int) (int, string, float64, int64, error) {
	if m.callIdx < len(m.outputs) {
		_ = os.MkdirAll(filepath.Dir(outputPath), 0o755)
		_ = os.WriteFile(outputPath, []byte(m.outputs[m.callIdx]), 0o644)
		m.callIdx++
	}
	return 0, "", 0.01, 100, nil
}

func (m *mockValidationRunner) CloneForAgent(agentName string) AgentRunner {
	return m
}

func TestRunWithValidation_SuccessOnFirstAttempt(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "output.json")

	validJSON := `{"schema_version":"1.0","agent":"merged","actors":[{"name":"User","type":"human","description":"A user"}],"problem_statement":"test","scope":{"in_scope":["x"],"out_of_scope":[]},"constraints":[],"integration_points":[],"priorities":[],"assumptions":[],"open_questions":[]}`

	runner := &mockValidationRunner{outputs: []string{validJSON}}

	result, err := RunWithValidation(ValidateAndRetryConfig{
		AgentName:      "test-agent",
		MaxAttempts:    3,
		OutputPath:     outPath,
		TimeoutSeconds: 30,
		Validator:      DiscoveryOutputValidator(),
		Runner:         runner,
		BuildPrompt: func(errs []string) string {
			return "test prompt"
		},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Data == nil {
		t.Fatal("expected valid data, got nil")
	}
	if result.Attempts != 1 {
		t.Errorf("expected 1 attempt, got %d", result.Attempts)
	}
	if len(result.LastErrors) > 0 {
		t.Errorf("expected no errors, got %v", result.LastErrors)
	}
}

func TestRunWithValidation_RetryOnInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "output.json")

	invalidJSON := `{"schema_version":"1.0","agent":"merged"}` // missing required fields
	validJSON := `{"schema_version":"1.0","agent":"merged","actors":[{"name":"User","type":"human","description":"A user"}],"problem_statement":"test","scope":{"in_scope":["x"],"out_of_scope":[]},"constraints":[],"integration_points":[],"priorities":[],"assumptions":[],"open_questions":[]}`

	runner := &mockValidationRunner{outputs: []string{invalidJSON, validJSON}}

	var promptsReceived []string
	result, err := RunWithValidation(ValidateAndRetryConfig{
		AgentName:      "test-agent",
		MaxAttempts:    3,
		OutputPath:     outPath,
		TimeoutSeconds: 30,
		Validator:      DiscoveryOutputValidator(),
		Runner:         runner,
		BuildPrompt: func(errs []string) string {
			prompt := AppendValidationErrorsToPrompt("base prompt", errs)
			promptsReceived = append(promptsReceived, prompt)
			return prompt
		},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Data == nil {
		t.Fatal("expected valid data after retry, got nil")
	}
	if result.Attempts != 2 {
		t.Errorf("expected 2 attempts, got %d", result.Attempts)
	}

	// Second prompt should contain validation errors.
	if len(promptsReceived) < 2 {
		t.Fatalf("expected at least 2 prompts, got %d", len(promptsReceived))
	}
	if !strings.Contains(promptsReceived[1], "Validation Errors") {
		t.Error("second prompt should contain validation errors section")
	}
}

func TestRunWithValidation_AllAttemptsExhausted(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "output.json")

	invalidJSON := `{"schema_version":"1.0","agent":"merged"}` // always invalid

	runner := &mockValidationRunner{outputs: []string{invalidJSON, invalidJSON, invalidJSON}}

	result, err := RunWithValidation(ValidateAndRetryConfig{
		AgentName:      "test-agent",
		MaxAttempts:    3,
		OutputPath:     outPath,
		TimeoutSeconds: 30,
		Validator:      DiscoveryOutputValidator(),
		Runner:         runner,
		BuildPrompt: func(errs []string) string {
			return "prompt"
		},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Data != nil {
		t.Error("expected nil data after exhausting attempts")
	}
	if result.Attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", result.Attempts)
	}
	if len(result.LastErrors) == 0 {
		t.Error("expected validation errors in result")
	}
}

func TestRunWithValidation_ExtractsJSONFromText(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "output.json")

	// Agent wraps JSON in markdown code fences.
	wrappedJSON := "Here's the merged output:\n```json\n" +
		`{"schema_version":"1.0","agent":"merged","actors":[{"name":"User","type":"human","description":"A user"}],"problem_statement":"test","scope":{"in_scope":["x"],"out_of_scope":[]},"constraints":[],"integration_points":[],"priorities":[],"assumptions":[],"open_questions":[]}` +
		"\n```\nDone!"

	runner := &mockValidationRunner{outputs: []string{wrappedJSON}}

	result, err := RunWithValidation(ValidateAndRetryConfig{
		AgentName:      "test-agent",
		MaxAttempts:    2,
		OutputPath:     outPath,
		TimeoutSeconds: 30,
		Validator:      DiscoveryOutputValidator(),
		Runner:         runner,
		BuildPrompt: func(errs []string) string {
			return "prompt"
		},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Data == nil {
		t.Fatal("expected extracted JSON data, got nil")
	}
	// Verify the extracted data is valid JSON.
	if !json.Valid(result.Data) {
		t.Errorf("extracted data is not valid JSON: %s", string(result.Data))
	}
}

func TestAppendValidationErrorsToPrompt_NoErrors(t *testing.T) {
	prompt := "original prompt"
	result := AppendValidationErrorsToPrompt(prompt, nil)
	if result != prompt {
		t.Errorf("expected original prompt unchanged, got %q", result)
	}
}

func TestAppendValidationErrorsToPrompt_WithErrors(t *testing.T) {
	prompt := "original prompt"
	errors := []string{"missing field: actors", "scope.in_scope must not be empty"}
	result := AppendValidationErrorsToPrompt(prompt, errors)

	if !strings.Contains(result, "Validation Errors") {
		t.Error("result should contain 'Validation Errors' header")
	}
	if !strings.Contains(result, "missing field: actors") {
		t.Error("result should contain first error")
	}
	if !strings.Contains(result, "scope.in_scope must not be empty") {
		t.Error("result should contain second error")
	}
}

func TestDiscoveryOutputValidator(t *testing.T) {
	validator := DiscoveryOutputValidator()

	// Valid output.
	validJSON := `{"schema_version":"1.0","agent":"discovery","actors":[{"name":"User","type":"human","description":"desc"}],"problem_statement":"test","scope":{"in_scope":["x"],"out_of_scope":[]},"constraints":[],"integration_points":[],"priorities":[],"assumptions":[],"open_questions":[]}`
	errs := validator([]byte(validJSON))
	if len(errs) > 0 {
		t.Errorf("expected no errors for valid output, got: %v", errs)
	}

	// Invalid output — missing required fields.
	invalidJSON := `{"schema_version":"1.0","agent":"discovery"}`
	errs = validator([]byte(invalidJSON))
	if len(errs) == 0 {
		t.Error("expected validation errors for invalid output")
	}
}
