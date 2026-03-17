package specworkflow

import "testing"

// ---------------------------------------------------------------------------
// ComputeProgress — individual conditions
// ---------------------------------------------------------------------------

func TestProgressCondition1_OpenCountDecreased(t *testing.T) {
	prev := RoundSnapshot{Round: 1, OpenCritical: 3, OpenMajor: 2}
	curr := RoundSnapshot{Round: 2, OpenCritical: 2, OpenMajor: 1}

	result := ComputeProgress(curr, prev)

	if !result.IsProgress {
		t.Error("expected progress TRUE when open count decreased")
	}
	if !result.Conditions[0] {
		t.Error("expected condition[0] (open count decreased) to be true")
	}
}

func TestProgressCondition2_FiftyPercentClosed(t *testing.T) {
	// Previous round had 4 CRITICAL+MAJOR; current closed 2 (exactly 50%).
	prev := RoundSnapshot{Round: 1, OpenCritical: 2, OpenMajor: 2}
	curr := RoundSnapshot{
		Round:                       2,
		OpenCritical:                2,
		OpenMajor:                   2,
		ClosedCriticalMajor:         2,
		TotalCriticalMajorPrevRound: 4,
	}

	result := ComputeProgress(curr, prev)

	if !result.IsProgress {
		t.Error("expected progress TRUE when >=50% closed")
	}
	if !result.Conditions[1] {
		t.Error("expected condition[1] (>=50% closed) to be true")
	}
}

func TestProgressCondition2_FiftyPercentBoundary_OddTotal(t *testing.T) {
	// Previous round had 5 CRITICAL+MAJOR; threshold = 5/2 = 2 (integer division).
	// Closing exactly 2 should satisfy the condition.
	prev := RoundSnapshot{Round: 1, OpenCritical: 3, OpenMajor: 2}
	curr := RoundSnapshot{
		Round:                       2,
		OpenCritical:                3,
		OpenMajor:                   2,
		ClosedCriticalMajor:         2,
		TotalCriticalMajorPrevRound: 5,
	}

	result := ComputeProgress(curr, prev)

	if !result.IsProgress {
		t.Error("expected progress TRUE when closed == total/2 (integer division)")
	}
	if !result.Conditions[1] {
		t.Error("expected condition[1] to be true at exact boundary with odd total")
	}
}

func TestProgressCondition2_BelowThreshold(t *testing.T) {
	// Previous round had 4 CRITICAL+MAJOR; closed only 1 (below 50%).
	// Open counts are unchanged so condition 0 is false.
	// OpenCritical > 0 so condition 2 is false.
	prev := RoundSnapshot{Round: 1, OpenCritical: 2, OpenMajor: 2}
	curr := RoundSnapshot{
		Round:                       2,
		OpenCritical:                2,
		OpenMajor:                   2,
		ClosedCriticalMajor:         1,
		TotalCriticalMajorPrevRound: 4,
	}

	result := ComputeProgress(curr, prev)

	if result.Conditions[1] {
		t.Error("expected condition[1] to be false when closed < 50%")
	}
}

func TestProgressCondition3_NoCriticalRemain(t *testing.T) {
	// MAJOR unchanged, but CRITICAL == 0.
	prev := RoundSnapshot{Round: 1, OpenCritical: 1, OpenMajor: 3}
	curr := RoundSnapshot{Round: 2, OpenCritical: 0, OpenMajor: 3}

	result := ComputeProgress(curr, prev)

	if !result.IsProgress {
		t.Error("expected progress TRUE when no CRITICAL remain")
	}
	if !result.Conditions[2] {
		t.Error("expected condition[2] (no open CRITICAL) to be true")
	}
}

func TestProgressAllConditionsFalse(t *testing.T) {
	// Open count unchanged, <50% closed, CRITICAL still open.
	prev := RoundSnapshot{Round: 1, OpenCritical: 2, OpenMajor: 2}
	curr := RoundSnapshot{
		Round:                       2,
		OpenCritical:                2,
		OpenMajor:                   2,
		ClosedCriticalMajor:         0,
		TotalCriticalMajorPrevRound: 4,
	}

	result := ComputeProgress(curr, prev)

	if result.IsProgress {
		t.Error("expected progress FALSE when no conditions met")
	}
	if result.Reason != "no progress" {
		t.Errorf("expected reason 'no progress', got %q", result.Reason)
	}
	for i, c := range result.Conditions {
		if c {
			t.Errorf("expected condition[%d] to be false", i)
		}
	}
}

// ---------------------------------------------------------------------------
// CheckConsecutiveNoProgress
// ---------------------------------------------------------------------------

func TestProgressConsecutiveNoProgress_TwoFalse(t *testing.T) {
	history := []bool{true, true, false, false}
	if !CheckConsecutiveNoProgress(history) {
		t.Error("expected escalation when last 2 entries are false")
	}
}

func TestProgressConsecutiveNoProgress_SingleFalse(t *testing.T) {
	history := []bool{true, true, false}
	if CheckConsecutiveNoProgress(history) {
		t.Error("should NOT escalate with only 1 false at end")
	}
}

func TestProgressConsecutiveNoProgress_TooShort(t *testing.T) {
	history := []bool{false}
	if CheckConsecutiveNoProgress(history) {
		t.Error("should NOT escalate with fewer than 2 entries")
	}
}

func TestProgressConsecutiveNoProgress_Empty(t *testing.T) {
	if CheckConsecutiveNoProgress(nil) {
		t.Error("should NOT escalate with empty history")
	}
}

func TestProgressConsecutiveNoProgress_FalseThenTrue(t *testing.T) {
	history := []bool{false, true}
	if CheckConsecutiveNoProgress(history) {
		t.Error("should NOT escalate when last entry is true")
	}
}

// ---------------------------------------------------------------------------
// CheckRegressionEscalation
// ---------------------------------------------------------------------------

func TestProgressRegression_TwoConsecutiveIncreases(t *testing.T) {
	snapshots := []RoundSnapshot{
		{Round: 1, OpenCritical: 1, OpenMajor: 1}, // total 2
		{Round: 2, OpenCritical: 2, OpenMajor: 1}, // total 3 (increase)
		{Round: 3, OpenCritical: 2, OpenMajor: 2}, // total 4 (increase)
	}
	if !CheckRegressionEscalation(snapshots) {
		t.Error("expected regression escalation with 2 consecutive increases")
	}
}

func TestProgressRegression_IncreaseThenDecrease(t *testing.T) {
	snapshots := []RoundSnapshot{
		{Round: 1, OpenCritical: 1, OpenMajor: 1}, // total 2
		{Round: 2, OpenCritical: 2, OpenMajor: 1}, // total 3 (increase)
		{Round: 3, OpenCritical: 1, OpenMajor: 1}, // total 2 (decrease)
	}
	if CheckRegressionEscalation(snapshots) {
		t.Error("should NOT escalate when increase followed by decrease")
	}
}

func TestProgressRegression_TooFewSnapshots(t *testing.T) {
	snapshots := []RoundSnapshot{
		{Round: 1, OpenCritical: 1, OpenMajor: 1},
		{Round: 2, OpenCritical: 2, OpenMajor: 2},
	}
	if CheckRegressionEscalation(snapshots) {
		t.Error("should NOT escalate with fewer than 3 snapshots")
	}
}

func TestProgressRegression_StableCount(t *testing.T) {
	snapshots := []RoundSnapshot{
		{Round: 1, OpenCritical: 2, OpenMajor: 2},
		{Round: 2, OpenCritical: 2, OpenMajor: 2},
		{Round: 3, OpenCritical: 2, OpenMajor: 2},
	}
	if CheckRegressionEscalation(snapshots) {
		t.Error("should NOT escalate when counts are stable")
	}
}

// ---------------------------------------------------------------------------
// ProgressTracker — multi-round tracking
// ---------------------------------------------------------------------------

func TestProgressTracker_MultiRound(t *testing.T) {
	pt := NewProgressTracker()

	// Round 1: first round always progress.
	r1 := pt.RecordRound(RoundSnapshot{Round: 1, OpenCritical: 3, OpenMajor: 2})
	if !r1.IsProgress {
		t.Error("first round should always be progress")
	}

	// Round 2: open count decreased (5 -> 3).
	r2 := pt.RecordRound(RoundSnapshot{Round: 2, OpenCritical: 1, OpenMajor: 2})
	if !r2.IsProgress {
		t.Error("round 2 should be progress (count decreased)")
	}

	// Round 3: no change, still CRITICAL open, nothing closed.
	r3 := pt.RecordRound(RoundSnapshot{
		Round:                       3,
		OpenCritical:                1,
		OpenMajor:                   2,
		ClosedCriticalMajor:         0,
		TotalCriticalMajorPrevRound: 3,
	})
	if r3.IsProgress {
		t.Error("round 3 should NOT be progress (no conditions met)")
	}

	// Should not escalate yet (only 1 no-progress round).
	escalate, _ := pt.ShouldEscalate()
	if escalate {
		t.Error("should NOT escalate after only 1 no-progress round")
	}

	// Round 4: still no change.
	r4 := pt.RecordRound(RoundSnapshot{
		Round:                       4,
		OpenCritical:                1,
		OpenMajor:                   2,
		ClosedCriticalMajor:         0,
		TotalCriticalMajorPrevRound: 3,
	})
	if r4.IsProgress {
		t.Error("round 4 should NOT be progress")
	}

	// Now should escalate (2 consecutive no-progress).
	escalate, reason := pt.ShouldEscalate()
	if !escalate {
		t.Error("should escalate after 2 consecutive no-progress rounds")
	}
	if reason == "" {
		t.Error("escalation reason should not be empty")
	}
}

// ---------------------------------------------------------------------------
// ProgressTracker — first round edge case
// ---------------------------------------------------------------------------

func TestProgressTracker_FirstRoundAlwaysProgress(t *testing.T) {
	pt := NewProgressTracker()

	// Even with terrible metrics, first round is always progress.
	result := pt.RecordRound(RoundSnapshot{
		Round:        1,
		OpenCritical: 10,
		OpenMajor:    20,
	})

	if !result.IsProgress {
		t.Error("first round must always be progress regardless of metrics")
	}
	if result.Reason != "first round" {
		t.Errorf("first round reason: got %q, want 'first round'", result.Reason)
	}
}

// ---------------------------------------------------------------------------
// ProgressTracker — ShouldEscalate regression path
// ---------------------------------------------------------------------------

func TestProgressTracker_ShouldEscalate_Regression(t *testing.T) {
	pt := NewProgressTracker()

	// Round 1: baseline.
	pt.RecordRound(RoundSnapshot{Round: 1, OpenCritical: 1, OpenMajor: 1})

	// Round 2: increase (1+1=2 -> 2+1=3).
	pt.RecordRound(RoundSnapshot{Round: 2, OpenCritical: 2, OpenMajor: 1})

	// Round 3: increase again (3 -> 4).
	pt.RecordRound(RoundSnapshot{Round: 3, OpenCritical: 2, OpenMajor: 2})

	escalate, reason := pt.ShouldEscalate()
	if !escalate {
		t.Error("should escalate on regression (2 consecutive increases)")
	}
	if reason == "" {
		t.Error("regression escalation reason should not be empty")
	}
}

func TestProgressTracker_ShouldEscalate_NoEscalation(t *testing.T) {
	pt := NewProgressTracker()

	// Round 1: baseline.
	pt.RecordRound(RoundSnapshot{Round: 1, OpenCritical: 3, OpenMajor: 2})

	// Round 2: improvement.
	pt.RecordRound(RoundSnapshot{Round: 2, OpenCritical: 1, OpenMajor: 1})

	escalate, reason := pt.ShouldEscalate()
	if escalate {
		t.Errorf("should NOT escalate when progress is being made, got reason: %s", reason)
	}
}
