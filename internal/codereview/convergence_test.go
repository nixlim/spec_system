package codereview

import (
	"testing"

	"github.com/foundry-zero/adversarial-spec-system/internal/specworkflow"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func makeFinding(severity specworkflow.Severity) specworkflow.MergedFinding {
	return specworkflow.MergedFinding{
		Severity: severity,
	}
}

// ---------------------------------------------------------------------------
// EvaluateConvergence tests
// ---------------------------------------------------------------------------

func TestEvaluateConvergenceZeroFindings(t *testing.T) {
	verdict := EvaluateConvergence(nil)
	if verdict != CodeReviewVerdictPass {
		t.Errorf("zero findings: got %v, want PASS", verdict)
	}
	verdict = EvaluateConvergence([]specworkflow.MergedFinding{})
	if verdict != CodeReviewVerdictPass {
		t.Errorf("empty slice: got %v, want PASS", verdict)
	}
}

func TestEvaluateConvergenceOnlyMinorAndObservation(t *testing.T) {
	findings := []specworkflow.MergedFinding{
		makeFinding(specworkflow.SeverityMinor),
		makeFinding(specworkflow.SeverityObservation),
		makeFinding(specworkflow.SeverityMinor),
	}
	verdict := EvaluateConvergence(findings)
	if verdict != CodeReviewVerdictPassWithObservations {
		t.Errorf("only MINOR/OBS: got %v, want PASS_WITH_OBSERVATIONS", verdict)
	}
}

func TestEvaluateConvergenceOnlyMinor(t *testing.T) {
	findings := []specworkflow.MergedFinding{
		makeFinding(specworkflow.SeverityMinor),
	}
	verdict := EvaluateConvergence(findings)
	if verdict != CodeReviewVerdictPassWithObservations {
		t.Errorf("only MINOR: got %v, want PASS_WITH_OBSERVATIONS", verdict)
	}
}

func TestEvaluateConvergenceOnlyObservation(t *testing.T) {
	findings := []specworkflow.MergedFinding{
		makeFinding(specworkflow.SeverityObservation),
	}
	verdict := EvaluateConvergence(findings)
	if verdict != CodeReviewVerdictPassWithObservations {
		t.Errorf("only OBSERVATION: got %v, want PASS_WITH_OBSERVATIONS", verdict)
	}
}

func TestEvaluateConvergenceWithCritical(t *testing.T) {
	findings := []specworkflow.MergedFinding{
		makeFinding(specworkflow.SeverityMinor),
		makeFinding(specworkflow.SeverityCritical),
		makeFinding(specworkflow.SeverityObservation),
	}
	verdict := EvaluateConvergence(findings)
	if verdict != CodeReviewVerdictRevise {
		t.Errorf("with CRITICAL: got %v, want REVISE", verdict)
	}
}

func TestEvaluateConvergenceWithMajor(t *testing.T) {
	findings := []specworkflow.MergedFinding{
		makeFinding(specworkflow.SeverityMajor),
		makeFinding(specworkflow.SeverityMinor),
	}
	verdict := EvaluateConvergence(findings)
	if verdict != CodeReviewVerdictRevise {
		t.Errorf("with MAJOR: got %v, want REVISE", verdict)
	}
}

func TestEvaluateConvergenceOnlyCritical(t *testing.T) {
	findings := []specworkflow.MergedFinding{
		makeFinding(specworkflow.SeverityCritical),
	}
	verdict := EvaluateConvergence(findings)
	if verdict != CodeReviewVerdictRevise {
		t.Errorf("only CRITICAL: got %v, want REVISE", verdict)
	}
}

// ---------------------------------------------------------------------------
// CountOpenCriticalMajor tests
// ---------------------------------------------------------------------------

func TestCountOpenCriticalMajorEmpty(t *testing.T) {
	if got := CountOpenCriticalMajor(nil); got != 0 {
		t.Errorf("nil: got %d, want 0", got)
	}
}

func TestCountOpenCriticalMajorMixed(t *testing.T) {
	findings := []specworkflow.MergedFinding{
		makeFinding(specworkflow.SeverityCritical),
		makeFinding(specworkflow.SeverityMajor),
		makeFinding(specworkflow.SeverityMinor),
		makeFinding(specworkflow.SeverityObservation),
		makeFinding(specworkflow.SeverityCritical),
	}
	if got := CountOpenCriticalMajor(findings); got != 3 {
		t.Errorf("mixed: got %d, want 3", got)
	}
}

func TestCountOpenCriticalMajorNoCritMaj(t *testing.T) {
	findings := []specworkflow.MergedFinding{
		makeFinding(specworkflow.SeverityMinor),
		makeFinding(specworkflow.SeverityObservation),
	}
	if got := CountOpenCriticalMajor(findings); got != 0 {
		t.Errorf("no CRIT/MAJ: got %d, want 0", got)
	}
}

// ---------------------------------------------------------------------------
// DetectStaleness tests
// ---------------------------------------------------------------------------

func TestDetectStalenessNotEnoughRounds(t *testing.T) {
	// Threshold 2, only 1 round of data.
	if DetectStaleness([]int{5}, 2) {
		t.Error("should not detect staleness with fewer rounds than threshold")
	}
}

func TestDetectStalenessThresholdLessThan2(t *testing.T) {
	if DetectStaleness([]int{5, 5}, 1) {
		t.Error("should not detect staleness with threshold < 2")
	}
	if DetectStaleness([]int{5, 5}, 0) {
		t.Error("should not detect staleness with threshold 0")
	}
}

func TestDetectStalenessNoImprovement(t *testing.T) {
	// 3 rounds: 5, 5, 5 — no improvement for 3 consecutive rounds.
	if !DetectStaleness([]int{5, 5, 5}, 3) {
		t.Error("expected staleness: no improvement for 3 rounds")
	}
}

func TestDetectStalenessIncreasing(t *testing.T) {
	// Counts increasing: 3, 4, 5 — also stale (no improvement means >= previous).
	if !DetectStaleness([]int{3, 4, 5}, 3) {
		t.Error("expected staleness: counts increasing (no improvement)")
	}
}

func TestDetectStalenessImprovement(t *testing.T) {
	// Last round shows improvement: 5, 5, 4.
	if DetectStaleness([]int{5, 5, 4}, 3) {
		t.Error("should not detect staleness when last round improved")
	}
}

func TestDetectStalenessImprovementInMiddle(t *testing.T) {
	// Middle round shows improvement: 5, 3, 5.
	if DetectStaleness([]int{5, 3, 5}, 3) {
		t.Error("should not detect staleness when middle round improved")
	}
}

func TestDetectStalenessDefaultThreshold(t *testing.T) {
	// Default threshold is 2. Two rounds with no improvement.
	if !DetectStaleness([]int{3, 3}, 2) {
		t.Error("expected staleness with threshold=2 and equal counts")
	}
	if !DetectStaleness([]int{3, 4}, 2) {
		t.Error("expected staleness with threshold=2 and increasing count")
	}
}

func TestDetectStalenessLongerHistory(t *testing.T) {
	// Only the last `threshold` entries matter.
	// History: 10, 8, 6, 5, 5 — threshold=2, last 2 are [5,5] = stale.
	if !DetectStaleness([]int{10, 8, 6, 5, 5}, 2) {
		t.Error("expected staleness in tail of longer history")
	}
	// History: 10, 8, 6, 5, 4 — threshold=2, last 2 are [5,4] = improved.
	if DetectStaleness([]int{10, 8, 6, 5, 4}, 2) {
		t.Error("should not detect staleness when tail shows improvement")
	}
}

func TestDetectStalenessEmptyRounds(t *testing.T) {
	if DetectStaleness(nil, 2) {
		t.Error("should not detect staleness with nil rounds")
	}
	if DetectStaleness([]int{}, 2) {
		t.Error("should not detect staleness with empty rounds")
	}
}
