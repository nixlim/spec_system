package codereview

import "github.com/foundry-zero/adversarial-spec-system/internal/specworkflow"

// EvaluateConvergence determines the code review verdict based purely on
// finding severities. No LLM judge is involved — the decision is deterministic:
//   - Zero findings → PASS
//   - Only MINOR and OBSERVATION → PASS_WITH_OBSERVATIONS
//   - Any CRITICAL or MAJOR → REVISE
func EvaluateConvergence(findings []specworkflow.MergedFinding) CodeReviewVerdict {
	hasCriticalOrMajor := false
	hasAny := false

	for i := range findings {
		hasAny = true
		sev := findings[i].Severity
		if sev == specworkflow.SeverityCritical || sev == specworkflow.SeverityMajor {
			hasCriticalOrMajor = true
			break
		}
	}

	if !hasAny {
		return CodeReviewVerdictPass
	}
	if hasCriticalOrMajor {
		return CodeReviewVerdictRevise
	}
	return CodeReviewVerdictPassWithObservations
}

// CountOpenCriticalMajor counts the number of CRITICAL and MAJOR findings
// in a merged findings list.
func CountOpenCriticalMajor(findings []specworkflow.MergedFinding) int {
	count := 0
	for i := range findings {
		sev := findings[i].Severity
		if sev == specworkflow.SeverityCritical || sev == specworkflow.SeverityMajor {
			count++
		}
	}
	return count
}

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

	// Check the last `threshold` entries: each must be >= the one before it
	// (i.e., no improvement).
	tail := roundCounts[len(roundCounts)-threshold:]
	for i := 1; i < len(tail); i++ {
		if tail[i] < tail[i-1] {
			return false // improvement detected
		}
	}
	return true
}
