package codereview

import (
	"testing"

	"github.com/foundry-zero/adversarial-spec-system/internal/specworkflow"
)

func TestCodeReviewDedupKey_BasicPath(t *testing.T) {
	f := &specworkflow.Finding{
		AffectedSection: "internal/api/handler.go:45-62",
		Lens:            "security",
		Severity:        specworkflow.SeverityCritical,
	}
	got := CodeReviewDedupKey(f)
	want := "internal/api/handler.go|45-62|security|critical"
	if got != want {
		t.Errorf("CodeReviewDedupKey = %q, want %q", got, want)
	}
}

func TestCodeReviewDedupKey_NoLineRange(t *testing.T) {
	f := &specworkflow.Finding{
		AffectedSection: "main.go",
		Lens:            "correctness",
		Severity:        specworkflow.SeverityMajor,
	}
	got := CodeReviewDedupKey(f)
	want := "main.go||correctness|major"
	if got != want {
		t.Errorf("CodeReviewDedupKey = %q, want %q", got, want)
	}
}

func TestCodeReviewDedupKey_CaseInsensitive(t *testing.T) {
	f1 := &specworkflow.Finding{
		AffectedSection: "Internal/API/Handler.go:45-62",
		Lens:            "Security",
		Severity:        specworkflow.SeverityCritical,
	}
	f2 := &specworkflow.Finding{
		AffectedSection: "internal/api/handler.go:45-62",
		Lens:            "security",
		Severity:        specworkflow.SeverityCritical,
	}
	if CodeReviewDedupKey(f1) != CodeReviewDedupKey(f2) {
		t.Errorf("keys should match: %q != %q", CodeReviewDedupKey(f1), CodeReviewDedupKey(f2))
	}
}

func TestCodeReviewDedupKey_DifferentSeverityDistinct(t *testing.T) {
	f1 := &specworkflow.Finding{
		AffectedSection: "handler.go:10",
		Lens:            "security",
		Severity:        specworkflow.SeverityCritical,
	}
	f2 := &specworkflow.Finding{
		AffectedSection: "handler.go:10",
		Lens:            "security",
		Severity:        specworkflow.SeverityMajor,
	}
	if CodeReviewDedupKey(f1) == CodeReviewDedupKey(f2) {
		t.Error("findings with different severities should have different keys")
	}
}

func TestCodeReviewDedupKey_DifferentLensDistinct(t *testing.T) {
	f1 := &specworkflow.Finding{
		AffectedSection: "handler.go:10",
		Lens:            "security",
		Severity:        specworkflow.SeverityCritical,
	}
	f2 := &specworkflow.Finding{
		AffectedSection: "handler.go:10",
		Lens:            "correctness",
		Severity:        specworkflow.SeverityCritical,
	}
	if CodeReviewDedupKey(f1) == CodeReviewDedupKey(f2) {
		t.Error("findings with different lenses should have different keys")
	}
}

func TestCodeReviewDedupKey_SingleLineNumber(t *testing.T) {
	f := &specworkflow.Finding{
		AffectedSection: "pkg/foo.go:99",
		Lens:            "testing",
		Severity:        specworkflow.SeverityMinor,
	}
	got := CodeReviewDedupKey(f)
	want := "pkg/foo.go|99|testing|minor"
	if got != want {
		t.Errorf("CodeReviewDedupKey = %q, want %q", got, want)
	}
}

func TestSplitAffectedSection(t *testing.T) {
	tests := []struct {
		input    string
		wantPath string
		wantLine string
	}{
		{"handler.go:45-62", "handler.go", "45-62"},
		{"handler.go:99", "handler.go", "99"},
		{"handler.go", "handler.go", ""},
		{"  handler.go:10  ", "handler.go", "10"},
		{"", "", ""},
	}
	for _, tt := range tests {
		gotPath, gotLine := splitAffectedSection(tt.input)
		if gotPath != tt.wantPath || gotLine != tt.wantLine {
			t.Errorf("splitAffectedSection(%q) = (%q, %q), want (%q, %q)",
				tt.input, gotPath, gotLine, tt.wantPath, tt.wantLine)
		}
	}
}

func TestCodeReviewDedupKeyFunc_ReturnsFunction(t *testing.T) {
	fn := CodeReviewDedupKeyFunc()
	if fn == nil {
		t.Fatal("CodeReviewDedupKeyFunc returned nil")
	}

	f := &specworkflow.Finding{
		AffectedSection: "main.go:1-10",
		Lens:            "observability",
		Severity:        specworkflow.SeverityObservation,
	}
	got := fn(f)
	want := "main.go|1-10|observability|observation"
	if got != want {
		t.Errorf("fn(f) = %q, want %q", got, want)
	}
}

func TestFormatCodeReviewDedupReason(t *testing.T) {
	f := &specworkflow.Finding{
		AffectedSection: "api/server.go:100-120",
		Lens:            "Error-Handling",
		Severity:        specworkflow.SeverityMajor,
	}
	reason := FormatCodeReviewDedupReason(f)
	if reason == "" {
		t.Error("expected non-empty reason")
	}
	// Verify it contains key components.
	for _, substr := range []string{"api/server.go", "100-120", "error-handling", "major"} {
		if !contains(reason, substr) {
			t.Errorf("reason %q should contain %q", reason, substr)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
