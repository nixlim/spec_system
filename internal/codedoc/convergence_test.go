package codedoc

import (
	"encoding/json"
	"testing"
)

// ---------------------------------------------------------------------------
// EvaluateConvergence — PASS verdicts
// ---------------------------------------------------------------------------

func TestConvergenceZeroFindingsReturnsPASS(t *testing.T) {
	result := EvaluateConvergence(nil, 1, 1, nil, 2)
	if result.Verdict != VerdictPass {
		t.Errorf("verdict: got %q, want %q", result.Verdict, VerdictPass)
	}
	if result.NextState != CDStateWriting {
		t.Errorf("next_state: got %q, want %q", result.NextState, CDStateWriting)
	}
}

func TestConvergenceAllResolvedReturnsPASS(t *testing.T) {
	findings := []ReviewFinding{
		{ID: "ACC-001", Severity: SeverityCritical, Status: "resolved"},
		{ID: "CMP-001", Severity: SeverityMajor, Status: "resolved"},
	}
	result := EvaluateConvergence(findings, 2, 1, nil, 2)
	if result.Verdict != VerdictPass {
		t.Errorf("verdict: got %q, want %q", result.Verdict, VerdictPass)
	}
	if result.NextState != CDStateWriting {
		t.Errorf("next_state: got %q, want %q", result.NextState, CDStateWriting)
	}
}

func TestConvergenceOnlyMinorObservationReturnsPASS(t *testing.T) {
	findings := []ReviewFinding{
		{ID: "STR-001", Severity: SeverityMinor, Status: "open"},
		{ID: "CON-001", Severity: SeverityObservation, Status: "open"},
	}
	result := EvaluateConvergence(findings, 1, 1, nil, 2)
	if result.Verdict != VerdictPass {
		t.Errorf("verdict: got %q, want %q", result.Verdict, VerdictPass)
	}
	if result.NextState != CDStateWriting {
		t.Errorf("next_state: got %q, want %q", result.NextState, CDStateWriting)
	}
}

// ---------------------------------------------------------------------------
// EvaluateConvergence — PASS_WITH_GATE verdicts
// ---------------------------------------------------------------------------

func TestConvergenceOpenCriticalWithMinRoundsReturnsPASSWithGate(t *testing.T) {
	findings := []ReviewFinding{
		{ID: "ACC-001", Severity: SeverityCritical, Status: "open"},
	}
	result := EvaluateConvergence(findings, 2, 1, nil, 2)
	if result.Verdict != VerdictPassWithGate {
		t.Errorf("verdict: got %q, want %q", result.Verdict, VerdictPassWithGate)
	}
	if result.NextState != CDStateHumanGateFinal {
		t.Errorf("next_state: got %q, want %q", result.NextState, CDStateHumanGateFinal)
	}
}

func TestConvergenceOpenMajorWithMinRoundsReturnsPASSWithGate(t *testing.T) {
	findings := []ReviewFinding{
		{ID: "CMP-001", Severity: SeverityMajor, Status: "open"},
	}
	result := EvaluateConvergence(findings, 1, 1, nil, 2)
	if result.Verdict != VerdictPassWithGate {
		t.Errorf("verdict: got %q, want %q", result.Verdict, VerdictPassWithGate)
	}
	if result.NextState != CDStateHumanGateFinal {
		t.Errorf("next_state: got %q, want %q", result.NextState, CDStateHumanGateFinal)
	}
}

// ---------------------------------------------------------------------------
// EvaluateConvergence — REVISE verdicts
// ---------------------------------------------------------------------------

func TestConvergenceOpenCriticalBeforeMinRoundsReturnsREVISE(t *testing.T) {
	findings := []ReviewFinding{
		{ID: "ACC-001", Severity: SeverityCritical, Status: "open"},
	}
	// round=1, minRounds=2 → not yet met
	result := EvaluateConvergence(findings, 1, 2, nil, 2)
	if result.Verdict != VerdictRevise {
		t.Errorf("verdict: got %q, want %q", result.Verdict, VerdictRevise)
	}
	if result.NextState != CDStateReviewing {
		t.Errorf("next_state: got %q, want %q", result.NextState, CDStateReviewing)
	}
}

func TestConvergenceOpenMajorBeforeMinRoundsReturnsREVISE(t *testing.T) {
	findings := []ReviewFinding{
		{ID: "CMP-001", Severity: SeverityMajor, Status: "open"},
	}
	result := EvaluateConvergence(findings, 1, 3, nil, 2)
	if result.Verdict != VerdictRevise {
		t.Errorf("verdict: got %q, want %q", result.Verdict, VerdictRevise)
	}
	if result.NextState != CDStateReviewing {
		t.Errorf("next_state: got %q, want %q", result.NextState, CDStateReviewing)
	}
}

// ---------------------------------------------------------------------------
// Staleness detection
// ---------------------------------------------------------------------------

func TestConvergenceDetectStalenessTrue(t *testing.T) {
	// 3 rounds with no improvement (5, 5, 5)
	roundCounts := []int{5, 5, 5}
	if !DetectStaleness(roundCounts, 3) {
		t.Errorf("expected staleness to be detected")
	}
}

func TestConvergenceDetectStalenessIncreasing(t *testing.T) {
	// Counts increasing (getting worse) — still stale (no improvement)
	roundCounts := []int{3, 4, 5}
	if !DetectStaleness(roundCounts, 3) {
		t.Errorf("expected staleness to be detected (counts increasing = no improvement)")
	}
}

func TestConvergenceDetectStalenessFalseImprovement(t *testing.T) {
	// Improvement in last round
	roundCounts := []int{5, 5, 3}
	if DetectStaleness(roundCounts, 3) {
		t.Errorf("expected staleness NOT detected (improvement in last round)")
	}
}

func TestConvergenceDetectStalenessFalseTooFewRounds(t *testing.T) {
	roundCounts := []int{5}
	if DetectStaleness(roundCounts, 2) {
		t.Errorf("expected staleness NOT detected (fewer rounds than threshold)")
	}
}

func TestConvergenceDetectStalenessThresholdTooLow(t *testing.T) {
	roundCounts := []int{5, 5, 5}
	if DetectStaleness(roundCounts, 1) {
		t.Errorf("expected staleness NOT detected (threshold < 2)")
	}
}

func TestConvergenceStalenessOverridesNormalVerdict(t *testing.T) {
	findings := []ReviewFinding{
		{ID: "ACC-001", Severity: SeverityCritical, Status: "open"},
	}
	// round=1, minRounds=3 → would normally be REVISE
	// but staleness detected → PASS_WITH_GATE
	roundCounts := []int{5, 5}
	result := EvaluateConvergence(findings, 1, 3, roundCounts, 2)
	if result.Verdict != VerdictPassWithGate {
		t.Errorf("verdict: got %q, want %q (staleness should override)", result.Verdict, VerdictPassWithGate)
	}
	if !result.StaleDetected {
		t.Errorf("StaleDetected: got false, want true")
	}
}

// ---------------------------------------------------------------------------
// CountOpenCriticalMajor
// ---------------------------------------------------------------------------

func TestConvergenceCountOpenCriticalMajor(t *testing.T) {
	findings := []ReviewFinding{
		{Severity: SeverityCritical, Status: "open"},
		{Severity: SeverityMajor, Status: "open"},
		{Severity: SeverityMinor, Status: "open"},
		{Severity: SeverityCritical, Status: "resolved"},
		{Severity: SeverityMajor, Status: "wontfix"},
	}
	got := CountOpenCriticalMajor(findings)
	if got != 2 {
		t.Errorf("CountOpenCriticalMajor: got %d, want 2", got)
	}
}

// ---------------------------------------------------------------------------
// FindingSummary counts
// ---------------------------------------------------------------------------

func TestConvergenceFindingSummaryCounts(t *testing.T) {
	findings := []ReviewFinding{
		{Severity: SeverityCritical, Status: "open"},
		{Severity: SeverityMajor, Status: "open"},
		{Severity: SeverityMinor, Status: "open"},
		{Severity: SeverityObservation, Status: "open"},
		{Severity: SeverityCritical, Status: "resolved"},
		{Severity: SeverityMajor, Status: "wontfix"},
	}
	result := EvaluateConvergence(findings, 2, 1, nil, 2)
	s := result.FindingSummary
	if s.OpenCritical != 1 {
		t.Errorf("OpenCritical: got %d, want 1", s.OpenCritical)
	}
	if s.OpenMajor != 1 {
		t.Errorf("OpenMajor: got %d, want 1", s.OpenMajor)
	}
	if s.OpenMinor != 1 {
		t.Errorf("OpenMinor: got %d, want 1", s.OpenMinor)
	}
	if s.OpenObservation != 1 {
		t.Errorf("OpenObservation: got %d, want 1", s.OpenObservation)
	}
	if s.Resolved != 1 {
		t.Errorf("Resolved: got %d, want 1", s.Resolved)
	}
	if s.WontFix != 1 {
		t.Errorf("WontFix: got %d, want 1", s.WontFix)
	}
}

// ---------------------------------------------------------------------------
// Judge output JSON serialization
// ---------------------------------------------------------------------------

func TestConvergenceResultJSON(t *testing.T) {
	result := EvaluateConvergence(nil, 1, 1, nil, 2)

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	requiredKeys := []string{"verdict", "next_state", "reasoning", "finding_summary", "stale_detected"}
	for _, key := range requiredKeys {
		if _, ok := m[key]; !ok {
			t.Errorf("missing JSON key %q in ConvergenceResult", key)
		}
	}

	// Check finding_summary has per-severity counts.
	fs := m["finding_summary"].(map[string]interface{})
	severityKeys := []string{"open_critical", "open_major", "open_minor", "open_observation", "resolved", "wontfix"}
	for _, key := range severityKeys {
		if _, ok := fs[key]; !ok {
			t.Errorf("missing JSON key %q in FindingSummary", key)
		}
	}
}

// ---------------------------------------------------------------------------
// Mixed scenario: wontfix CRITICAL with open MINOR
// ---------------------------------------------------------------------------

func TestConvergenceWontfixCriticalWithOpenMinorReturnsPASS(t *testing.T) {
	findings := []ReviewFinding{
		{ID: "ACC-001", Severity: SeverityCritical, Status: "wontfix"},
		{ID: "STR-001", Severity: SeverityMinor, Status: "open"},
	}
	result := EvaluateConvergence(findings, 1, 1, nil, 2)
	if result.Verdict != VerdictPass {
		t.Errorf("verdict: got %q, want %q (wontfix CRITICAL should not block)", result.Verdict, VerdictPass)
	}
}
