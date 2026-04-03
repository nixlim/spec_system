package codedoc

// ---------------------------------------------------------------------------
// Codedoc Verdict
// ---------------------------------------------------------------------------

const (
	// VerdictPass means all findings are resolved or MINOR/OBSERVATION only.
	// Routes to CD_WRITING.
	VerdictPass = "PASS"
	// VerdictPassWithGate means unresolved CRITICAL or MAJOR remain but
	// min_rounds is met. Routes to CD_HUMAN_GATE_FINAL for human decision.
	VerdictPassWithGate = "PASS_WITH_GATE"
	// VerdictRevise means unresolved CRITICAL or MAJOR remain and min_rounds
	// not yet met. Routes to CD_REVIEWING for another round.
	VerdictRevise = "REVISE"
)

// ---------------------------------------------------------------------------
// Codedoc states (string constants for next-state routing)
// ---------------------------------------------------------------------------

const (
	CDStateReviewing     = "CD_REVIEWING"
	CDStateHumanGateFinal = "CD_HUMAN_GATE_FINAL"
	CDStateWriting       = "CD_WRITING"
)

// ---------------------------------------------------------------------------
// FindingSummary
// ---------------------------------------------------------------------------

// FindingSummary holds per-severity counts for the judge output.
type FindingSummary struct {
	OpenCritical    int `json:"open_critical"`
	OpenMajor       int `json:"open_major"`
	OpenMinor       int `json:"open_minor"`
	OpenObservation int `json:"open_observation"`
	Resolved        int `json:"resolved"`
	WontFix         int `json:"wontfix"`
}

// ---------------------------------------------------------------------------
// ConvergenceResult
// ---------------------------------------------------------------------------

// ConvergenceResult is the outcome of evaluating convergence for a codedoc
// review round.
type ConvergenceResult struct {
	Verdict        string         `json:"verdict"`
	NextState      string         `json:"next_state"`
	Reasoning      string         `json:"reasoning"`
	FindingSummary FindingSummary `json:"finding_summary"`
	StaleDetected  bool           `json:"stale_detected"`
}

// ---------------------------------------------------------------------------
// EvaluateConvergence
// ---------------------------------------------------------------------------

// EvaluateConvergence determines the codedoc verdict based on finding
// severities, round count, and staleness detection.
//
// Rules:
//   - Zero open findings → PASS, CD_WRITING
//   - Only MINOR/OBSERVATION open → PASS, CD_WRITING
//   - CRITICAL or MAJOR open + round < minRounds → REVISE, CD_REVIEWING
//   - CRITICAL or MAJOR open + round >= minRounds → PASS_WITH_GATE, CD_HUMAN_GATE_FINAL
//   - Staleness detected → PASS_WITH_GATE, CD_HUMAN_GATE_FINAL (escalate to human)
func EvaluateConvergence(
	findings []ReviewFinding,
	round int,
	minRounds int,
	roundCounts []int,
	stalenessThreshold int,
) ConvergenceResult {
	summary := countFindings(findings)

	// Check staleness first — overrides normal flow.
	stale := DetectStaleness(roundCounts, stalenessThreshold)
	if stale {
		return ConvergenceResult{
			Verdict:        VerdictPassWithGate,
			NextState:      CDStateHumanGateFinal,
			Reasoning:      "Staleness detected: no improvement in CRITICAL+MAJOR count for consecutive rounds; escalating to human gate",
			FindingSummary: summary,
			StaleDetected:  true,
		}
	}

	hasCriticalOrMajor := summary.OpenCritical > 0 || summary.OpenMajor > 0

	if !hasCriticalOrMajor {
		return ConvergenceResult{
			Verdict:        VerdictPass,
			NextState:      CDStateWriting,
			Reasoning:      "No open CRITICAL or MAJOR findings; documentation approved for writing",
			FindingSummary: summary,
		}
	}

	// CRITICAL or MAJOR findings remain.
	if round < minRounds {
		return ConvergenceResult{
			Verdict:        VerdictRevise,
			NextState:      CDStateReviewing,
			Reasoning:      "Open CRITICAL/MAJOR findings remain and min_rounds not yet reached; continuing review",
			FindingSummary: summary,
		}
	}

	return ConvergenceResult{
		Verdict:        VerdictPassWithGate,
		NextState:      CDStateHumanGateFinal,
		Reasoning:      "Open CRITICAL/MAJOR findings remain after min_rounds reached; escalating to human gate for decision",
		FindingSummary: summary,
	}
}

// ---------------------------------------------------------------------------
// countFindings
// ---------------------------------------------------------------------------

// countFindings tallies findings by severity and status.
func countFindings(findings []ReviewFinding) FindingSummary {
	var s FindingSummary
	for _, f := range findings {
		switch f.Status {
		case "resolved":
			s.Resolved++
			continue
		case "wontfix":
			s.WontFix++
			continue
		}
		// Open findings.
		switch f.Severity {
		case SeverityCritical:
			s.OpenCritical++
		case SeverityMajor:
			s.OpenMajor++
		case SeverityMinor:
			s.OpenMinor++
		case SeverityObservation:
			s.OpenObservation++
		}
	}
	return s
}

// ---------------------------------------------------------------------------
// CountOpenCriticalMajor
// ---------------------------------------------------------------------------

// CountOpenCriticalMajor counts the number of open CRITICAL and MAJOR
// findings in a findings list (excludes resolved/wontfix).
func CountOpenCriticalMajor(findings []ReviewFinding) int {
	count := 0
	for _, f := range findings {
		if f.Status == "resolved" || f.Status == "wontfix" {
			continue
		}
		if f.Severity == SeverityCritical || f.Severity == SeverityMajor {
			count++
		}
	}
	return count
}

// ---------------------------------------------------------------------------
// DetectStaleness
// ---------------------------------------------------------------------------

// DetectStaleness returns true if the count of open CRITICAL+MAJOR findings
// has not decreased for the last `threshold` consecutive rounds. The
// roundCounts slice holds the CRITICAL+MAJOR count for each completed round,
// indexed from 0 (round 1 = index 0).
//
// Returns false if threshold < 2 or there are fewer rounds than threshold.
func DetectStaleness(roundCounts []int, threshold int) bool {
	if threshold < 2 || len(roundCounts) < threshold {
		return false
	}

	tail := roundCounts[len(roundCounts)-threshold:]
	for i := 1; i < len(tail); i++ {
		if tail[i] < tail[i-1] {
			return false // improvement detected
		}
	}
	return true
}
