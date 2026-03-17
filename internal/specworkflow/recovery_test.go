package specworkflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// DetectFailureType tests
// ---------------------------------------------------------------------------

func TestRecovery_DetectFailureType_ContextOverflow(t *testing.T) {
	patterns := []string{
		"exceeded context length limit",
		"token limit exceeded",
		"maximum context window reached",
	}
	for _, stderr := range patterns {
		got := DetectFailureType(1, stderr, "")
		if got != ErrContextOverflow {
			t.Errorf("DetectFailureType(1, %q, ...) = %q, want %q", stderr, got, ErrContextOverflow)
		}
	}
}

func TestRecovery_DetectFailureType_RateLimited(t *testing.T) {
	patterns := []string{
		"rate limit exceeded",
		"HTTP 429 Too Many Requests",
	}
	for _, stderr := range patterns {
		got := DetectFailureType(1, stderr, "")
		if got != ErrRateLimited {
			t.Errorf("DetectFailureType(1, %q, ...) = %q, want %q", stderr, got, ErrRateLimited)
		}
	}
}

func TestRecovery_DetectFailureType_Crash(t *testing.T) {
	got := DetectFailureType(1, "segfault", "")
	if got != ErrCrash {
		t.Errorf("DetectFailureType(1, 'segfault', ...) = %q, want %q", got, ErrCrash)
	}
}

func TestRecovery_DetectFailureType_MissingOutput(t *testing.T) {
	got := DetectFailureType(0, "", "/nonexistent/path/output.json")
	if got != ErrMissingOutput {
		t.Errorf("DetectFailureType(0, '', '/nonexistent/...') = %q, want %q", got, ErrMissingOutput)
	}
}

func TestRecovery_DetectFailureType_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "output.json")
	if err := os.WriteFile(path, []byte("not json {{{"), 0644); err != nil {
		t.Fatal(err)
	}

	got := DetectFailureType(0, "", path)
	if got != ErrInvalidJSON {
		t.Errorf("DetectFailureType(0, '', <invalid json>) = %q, want %q", got, ErrInvalidJSON)
	}
}

func TestRecovery_DetectFailureType_NoError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "output.json")
	if err := os.WriteFile(path, []byte(`{"ok": true}`), 0644); err != nil {
		t.Fatal(err)
	}

	got := DetectFailureType(0, "", path)
	if got != "" {
		t.Errorf("DetectFailureType(0, '', <valid json>) = %q, want empty string", got)
	}
}

func TestRecovery_DetectFailureType_CaseInsensitive(t *testing.T) {
	got := DetectFailureType(1, "CONTEXT LENGTH exceeded", "")
	if got != ErrContextOverflow {
		t.Errorf("DetectFailureType with uppercase stderr = %q, want %q", got, ErrContextOverflow)
	}

	got = DetectFailureType(1, "RATE LIMIT hit", "")
	if got != ErrRateLimited {
		t.Errorf("DetectFailureType with uppercase rate limit = %q, want %q", got, ErrRateLimited)
	}
}

// ---------------------------------------------------------------------------
// RetryDelay tests
// ---------------------------------------------------------------------------

func TestRecovery_RetryDelay_Values(t *testing.T) {
	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{0, 5 * time.Second},
		{1, 15 * time.Second},
		{2, 45 * time.Second},
	}
	for _, tt := range tests {
		got := RetryDelay(tt.attempt)
		if got != tt.want {
			t.Errorf("RetryDelay(%d) = %v, want %v", tt.attempt, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// ShouldRetry tests
// ---------------------------------------------------------------------------

func TestRecovery_ShouldRetry_RetriesRemaining(t *testing.T) {
	err := &AgentError{
		Type:       ErrCrash,
		Agent:      "reviewer",
		RetryCount: 1,
		MaxRetries: 3,
	}
	if !ShouldRetry(err) {
		t.Error("ShouldRetry should return true when RetryCount < MaxRetries")
	}
}

func TestRecovery_ShouldRetry_Exhausted(t *testing.T) {
	err := &AgentError{
		Type:       ErrCrash,
		Agent:      "reviewer",
		RetryCount: 3,
		MaxRetries: 3,
	}
	if ShouldRetry(err) {
		t.Error("ShouldRetry should return false when RetryCount >= MaxRetries")
	}
}

func TestRecovery_ShouldRetry_ZeroMaxRetries(t *testing.T) {
	err := &AgentError{
		Type:       ErrCrash,
		Agent:      "reviewer",
		RetryCount: 0,
		MaxRetries: 0,
	}
	if ShouldRetry(err) {
		t.Error("ShouldRetry should return false when MaxRetries is 0")
	}
}

// ---------------------------------------------------------------------------
// DetermineRecovery tests
// ---------------------------------------------------------------------------

func TestRecovery_DetermineRecovery_Retry(t *testing.T) {
	err := &AgentError{
		Type:       ErrCrash,
		Agent:      "drafter",
		RetryCount: 0,
		MaxRetries: 3,
	}
	action := DetermineRecovery(err, StateDrafting, 1, 3)
	if action.Action != ActionRetry {
		t.Errorf("DetermineRecovery with retries remaining = %q, want %q", action.Action, ActionRetry)
	}
}

func TestRecovery_DetermineRecovery_SchemaViolation(t *testing.T) {
	err := &AgentError{
		Type:       ErrSchemaViolation,
		Agent:      "reviewer",
		RetryCount: 3,
		MaxRetries: 3,
	}
	action := DetermineRecovery(err, StateReviewing, 1, 3)
	if action.Action != ActionBestEffortParse {
		t.Errorf("DetermineRecovery for schema_violation = %q, want %q", action.Action, ActionBestEffortParse)
	}
}

func TestRecovery_DetermineRecovery_JudgeDefaultRevise(t *testing.T) {
	err := &AgentError{
		Type:       ErrCrash,
		Agent:      "judge",
		RetryCount: 3,
		MaxRetries: 3,
	}
	action := DetermineRecovery(err, StateJudging, 1, 3)
	if action.Action != ActionDefaultRevise {
		t.Errorf("DetermineRecovery for JUDGING not at max rounds = %q, want %q", action.Action, ActionDefaultRevise)
	}
}

func TestRecovery_DetermineRecovery_JudgeAtMaxRoundsEscalates(t *testing.T) {
	err := &AgentError{
		Type:       ErrCrash,
		Agent:      "judge",
		RetryCount: 3,
		MaxRetries: 3,
	}
	action := DetermineRecovery(err, StateJudging, 3, 3)
	if action.Action != ActionEscalate {
		t.Errorf("DetermineRecovery for JUDGING at max rounds = %q, want %q", action.Action, ActionEscalate)
	}
}

func TestRecovery_DetermineRecovery_ReviewingEscalates(t *testing.T) {
	err := &AgentError{
		Type:       ErrCrash,
		Agent:      "reviewer",
		RetryCount: 3,
		MaxRetries: 3,
	}
	action := DetermineRecovery(err, StateReviewing, 1, 3)
	if action.Action != ActionEscalate {
		t.Errorf("DetermineRecovery for REVIEWING after max retries = %q, want %q", action.Action, ActionEscalate)
	}
}

func TestRecovery_DetermineRecovery_OtherStateEscalates(t *testing.T) {
	err := &AgentError{
		Type:       ErrMissingOutput,
		Agent:      "drafter",
		RetryCount: 3,
		MaxRetries: 3,
	}
	action := DetermineRecovery(err, StateDrafting, 1, 3)
	if action.Action != ActionEscalate {
		t.Errorf("DetermineRecovery for DRAFTING after max retries = %q, want %q", action.Action, ActionEscalate)
	}
}

func TestRecovery_DetermineRecovery_ContextOverflowRetryThenEscalate(t *testing.T) {
	// First attempt: should retry.
	err := &AgentError{
		Type:       ErrContextOverflow,
		Agent:      "reviewer",
		RetryCount: 0,
		MaxRetries: 2,
	}
	action := DetermineRecovery(err, StateReviewing, 1, 3)
	if action.Action != ActionRetry {
		t.Errorf("context_overflow with retries remaining = %q, want %q", action.Action, ActionRetry)
	}

	// Exhausted: should escalate.
	err.RetryCount = 2
	action = DetermineRecovery(err, StateReviewing, 1, 3)
	if action.Action != ActionEscalate {
		t.Errorf("context_overflow exhausted = %q, want %q", action.Action, ActionEscalate)
	}
}

func TestRecovery_DetermineRecovery_RateLimitedRetryThenEscalate(t *testing.T) {
	err := &AgentError{
		Type:       ErrRateLimited,
		Agent:      "judge",
		RetryCount: 0,
		MaxRetries: 3,
	}
	action := DetermineRecovery(err, StateJudging, 2, 3)
	if action.Action != ActionRetry {
		t.Errorf("rate_limited with retries remaining = %q, want %q", action.Action, ActionRetry)
	}

	err.RetryCount = 3
	action = DetermineRecovery(err, StateJudging, 2, 3)
	if action.Action != ActionDefaultRevise {
		t.Errorf("rate_limited exhausted in JUDGING not at max = %q, want %q", action.Action, ActionDefaultRevise)
	}
}

// ---------------------------------------------------------------------------
// BestEffortParse tests
// ---------------------------------------------------------------------------

func TestRecovery_BestEffortParse_ValidFindings(t *testing.T) {
	output := ReviewerOutput{
		SchemaVersion:      "1.0",
		Agent:              "reviewer-security",
		Round:              1,
		LensesApplied:      []string{"security"},
		MarkdownReportFile: "report.md",
		Findings: []Finding{
			{
				ID:              "F-001",
				Description:     "SQL injection risk",
				Severity:        SeverityCritical,
				Impact:          "Data breach",
				Recommendation:  "Use parameterised queries",
				Lens:            "security",
				AffectedSection: "3.1",
			},
			{
				ID:              "F-002",
				Description:     "Missing auth check",
				Severity:        SeverityMajor,
				Impact:          "Unauthorised access",
				Recommendation:  "Add auth middleware",
				Lens:            "security",
				AffectedSection: "3.2",
			},
		},
	}
	data, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}

	findings, errs := BestEffortParse(data)
	if len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
	if len(findings) != 2 {
		t.Errorf("expected 2 valid findings, got %d", len(findings))
	}
}

func TestRecovery_BestEffortParse_PartialFindings(t *testing.T) {
	output := ReviewerOutput{
		SchemaVersion:      "1.0",
		Agent:              "reviewer-security",
		Round:              1,
		LensesApplied:      []string{"security"},
		MarkdownReportFile: "report.md",
		Findings: []Finding{
			{
				ID:              "F-001",
				Description:     "Valid finding",
				Severity:        SeverityCritical,
				Impact:          "High",
				Recommendation:  "Fix it",
				Lens:            "security",
				AffectedSection: "3.1",
			},
			{
				// Invalid: missing required fields.
				ID:          "F-002",
				Description: "Invalid finding",
				Severity:    SeverityMajor,
				// Missing: Impact, Recommendation, Lens, AffectedSection
			},
		},
	}
	data, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}

	findings, errs := BestEffortParse(data)
	if len(findings) != 1 {
		t.Errorf("expected 1 valid finding, got %d", len(findings))
	}
	if len(findings) > 0 && findings[0].ID != "F-001" {
		t.Errorf("expected valid finding F-001, got %q", findings[0].ID)
	}
	if len(errs) == 0 {
		t.Error("expected validation errors for invalid finding")
	}
}

func TestRecovery_BestEffortParse_CompletelyInvalidJSON(t *testing.T) {
	data := []byte("not json at all {{{")
	findings, errs := BestEffortParse(data)
	if findings != nil {
		t.Errorf("expected nil findings for invalid JSON, got %v", findings)
	}
	if len(errs) != 1 {
		t.Errorf("expected 1 parse error, got %d", len(errs))
	}
}

func TestRecovery_BestEffortParse_EmptyFindings(t *testing.T) {
	output := ReviewerOutput{
		SchemaVersion:      "1.0",
		Agent:              "reviewer",
		Round:              1,
		LensesApplied:      []string{"security"},
		MarkdownReportFile: "report.md",
		Findings:           []Finding{},
	}
	data, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}

	findings, errs := BestEffortParse(data)
	if len(findings) != 0 {
		t.Errorf("expected 0 findings, got %d", len(findings))
	}
	if len(errs) != 0 {
		t.Errorf("expected 0 errors, got %d", len(errs))
	}
}

// ---------------------------------------------------------------------------
// IsTimeoutError tests
// ---------------------------------------------------------------------------

func TestRecovery_IsTimeoutError_Exceeded(t *testing.T) {
	if !IsTimeoutError(10*time.Second, 5*time.Second) {
		t.Error("IsTimeoutError should return true when elapsed > timeout")
	}
}

func TestRecovery_IsTimeoutError_Equal(t *testing.T) {
	if !IsTimeoutError(5*time.Second, 5*time.Second) {
		t.Error("IsTimeoutError should return true when elapsed == timeout")
	}
}

func TestRecovery_IsTimeoutError_NotExceeded(t *testing.T) {
	if IsTimeoutError(3*time.Second, 5*time.Second) {
		t.Error("IsTimeoutError should return false when elapsed < timeout")
	}
}

// ---------------------------------------------------------------------------
// AgentError.Error() test
// ---------------------------------------------------------------------------

func TestRecovery_AgentError_Error(t *testing.T) {
	err := &AgentError{
		Type:       ErrCrash,
		Agent:      "drafter",
		Detail:     "segfault",
		RetryCount: 1,
		MaxRetries: 3,
	}
	got := err.Error()
	if got == "" {
		t.Error("AgentError.Error() should return a non-empty string")
	}
	// Verify it contains key information.
	for _, want := range []string{"drafter", "crash", "segfault", "1/3"} {
		if !contains(got, want) {
			t.Errorf("AgentError.Error() = %q, expected to contain %q", got, want)
		}
	}
}

// contains is a test helper that checks if s contains substr.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && containsSubstring(s, substr)
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
