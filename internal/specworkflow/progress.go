// Package specworkflow defines the core types for the adversarial spec review
// workflow. This file implements deterministic progress metric computation for
// the review/revise loop, including escalation detection based on consecutive
// stalls and regression patterns.
package specworkflow

import "fmt"

// ---------------------------------------------------------------------------
// RoundSnapshot
// ---------------------------------------------------------------------------

// RoundSnapshot captures the key metrics for a single review round, used as
// input to the deterministic progress computation.
type RoundSnapshot struct {
	// Round is the review round number (1-indexed).
	Round int
	// OpenCritical is the number of open CRITICAL-severity findings.
	OpenCritical int
	// OpenMajor is the number of open MAJOR-severity findings.
	OpenMajor int
	// ClosedCriticalMajor is the count of CRITICAL+MAJOR findings closed
	// during this round.
	ClosedCriticalMajor int
	// TotalCriticalMajorPrevRound is the total CRITICAL+MAJOR count from
	// the previous round (used for the 50% closure threshold).
	TotalCriticalMajorPrevRound int
}

// openCriticalMajor returns the combined count of open CRITICAL and MAJOR
// findings in this snapshot.
func (s RoundSnapshot) openCriticalMajor() int {
	return s.OpenCritical + s.OpenMajor
}

// ---------------------------------------------------------------------------
// ProgressResult
// ---------------------------------------------------------------------------

// ProgressResult holds the outcome of a deterministic progress evaluation
// between two consecutive rounds.
type ProgressResult struct {
	// IsProgress is true if at least one of the three progress conditions
	// was satisfied.
	IsProgress bool
	// Reason describes which condition was met, or "no progress" if none.
	Reason string
	// Conditions records which of the three progress conditions held:
	//   [0] open CRITICAL+MAJOR decreased
	//   [1] >=50% of previous round's CRITICAL+MAJOR were closed
	//   [2] no open CRITICAL findings remain
	Conditions [3]bool
}

// ---------------------------------------------------------------------------
// ComputeProgress
// ---------------------------------------------------------------------------

// ComputeProgress evaluates whether the current round shows progress relative
// to the previous round. Progress is TRUE if ANY of three conditions hold:
//
//   - Condition A: open CRITICAL+MAJOR count strictly decreased.
//   - Condition B: at least 50% of previous round's CRITICAL+MAJOR were
//     closed (using integer division: closed >= total / 2).
//   - Condition C: no CRITICAL findings remain open (OpenCritical == 0),
//     even if MAJOR count is unchanged.
//
// This is a pure, deterministic computation with no LLM involvement.
func ComputeProgress(current, previous RoundSnapshot) ProgressResult {
	var result ProgressResult

	// Condition A: open CRITICAL+MAJOR count decreased.
	if current.openCriticalMajor() < previous.openCriticalMajor() {
		result.Conditions[0] = true
	}

	// Condition B: >=50% of previous round's CRITICAL+MAJOR closed.
	// Uses integer division: threshold = total / 2.
	threshold := current.TotalCriticalMajorPrevRound / 2
	if current.TotalCriticalMajorPrevRound > 0 && current.ClosedCriticalMajor >= threshold {
		result.Conditions[1] = true
	}

	// Condition C: no CRITICAL findings remain open.
	if current.OpenCritical == 0 {
		result.Conditions[2] = true
	}

	// Determine overall progress and reason.
	switch {
	case result.Conditions[0]:
		result.IsProgress = true
		result.Reason = fmt.Sprintf("open CRITICAL+MAJOR decreased from %d to %d",
			previous.openCriticalMajor(), current.openCriticalMajor())
	case result.Conditions[1]:
		result.IsProgress = true
		result.Reason = fmt.Sprintf("closed %d of %d previous CRITICAL+MAJOR (>=50%%)",
			current.ClosedCriticalMajor, current.TotalCriticalMajorPrevRound)
	case result.Conditions[2]:
		result.IsProgress = true
		result.Reason = "no CRITICAL findings remain open"
	default:
		result.Reason = "no progress"
	}

	return result
}

// ---------------------------------------------------------------------------
// CheckConsecutiveNoProgress
// ---------------------------------------------------------------------------

// CheckConsecutiveNoProgress returns true if the last 2 entries in the
// progress history are both false, indicating two consecutive rounds without
// progress. This condition triggers escalation to human intervention.
func CheckConsecutiveNoProgress(progressHistory []bool) bool {
	n := len(progressHistory)
	if n < 2 {
		return false
	}
	return !progressHistory[n-1] && !progressHistory[n-2]
}

// ---------------------------------------------------------------------------
// CheckRegressionEscalation
// ---------------------------------------------------------------------------

// CheckRegressionEscalation returns true if the total open CRITICAL+MAJOR
// count increased for 2 consecutive rounds, indicating a regression pattern
// that warrants escalation.
func CheckRegressionEscalation(snapshots []RoundSnapshot) bool {
	n := len(snapshots)
	if n < 3 {
		return false
	}
	// Check if the last two transitions were both increases.
	increase1 := snapshots[n-2].openCriticalMajor() > snapshots[n-3].openCriticalMajor()
	increase2 := snapshots[n-1].openCriticalMajor() > snapshots[n-2].openCriticalMajor()
	return increase1 && increase2
}

// ---------------------------------------------------------------------------
// ProgressTracker
// ---------------------------------------------------------------------------

// ProgressTracker maintains the progress history and round snapshots across
// the review/revise loop, providing escalation detection.
type ProgressTracker struct {
	history   []ProgressResult
	snapshots []RoundSnapshot
}

// NewProgressTracker returns an initialised ProgressTracker with empty
// history and snapshot slices.
func NewProgressTracker() *ProgressTracker {
	return &ProgressTracker{}
}

// RecordRound computes progress for the given snapshot against the previous
// round, records the result, and returns it. The first round (no previous
// snapshot) always counts as progress.
func (pt *ProgressTracker) RecordRound(snapshot RoundSnapshot) ProgressResult {
	var result ProgressResult

	if len(pt.snapshots) == 0 {
		// First round: always progress.
		result = ProgressResult{
			IsProgress: true,
			Reason:     "first round",
		}
	} else {
		previous := pt.snapshots[len(pt.snapshots)-1]
		result = ComputeProgress(snapshot, previous)
	}

	pt.snapshots = append(pt.snapshots, snapshot)
	pt.history = append(pt.history, result)

	return result
}

// ShouldEscalate checks whether the workflow should be escalated to human
// intervention. It returns true with a reason string if either:
//   - Two consecutive rounds showed no progress, or
//   - Open CRITICAL+MAJOR count increased for two consecutive rounds
//     (regression).
func (pt *ProgressTracker) ShouldEscalate() (bool, string) {
	// Build progress-only history for consecutive no-progress check.
	progressBools := make([]bool, len(pt.history))
	for i, r := range pt.history {
		progressBools[i] = r.IsProgress
	}

	if CheckConsecutiveNoProgress(progressBools) {
		return true, "two consecutive rounds with no progress"
	}

	if CheckRegressionEscalation(pt.snapshots) {
		return true, "open CRITICAL+MAJOR increased for two consecutive rounds"
	}

	return false, ""
}
