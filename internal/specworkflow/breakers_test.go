package specworkflow

import (
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// CheckMaxRounds
// ---------------------------------------------------------------------------

func TestBreakerMaxRounds_NotTriggered(t *testing.T) {
	r := CheckMaxRounds(5, 5)
	if r.Triggered {
		t.Errorf("expected not triggered for round 5 with max 5")
	}
	if r.BreakerName != "max_rounds" {
		t.Errorf("BreakerName = %q, want %q", r.BreakerName, "max_rounds")
	}
	if r.CurrentValue != 5 {
		t.Errorf("CurrentValue = %v, want 5", r.CurrentValue)
	}
	if r.Limit != 5 {
		t.Errorf("Limit = %v, want 5", r.Limit)
	}
}

func TestBreakerMaxRounds_Triggered(t *testing.T) {
	r := CheckMaxRounds(6, 5)
	if !r.Triggered {
		t.Errorf("expected triggered for round 6 with max 5")
	}
	if r.Message == "" {
		t.Error("expected non-empty message when triggered")
	}
}

func TestBreakerMaxRounds_Boundary(t *testing.T) {
	tests := []struct {
		round, max int
		want       bool
	}{
		{0, 5, false},
		{1, 5, false},
		{4, 5, false},
		{5, 5, false},
		{6, 5, true},
		{100, 5, true},
		{1, 1, false},
		{2, 1, true},
	}
	for _, tt := range tests {
		r := CheckMaxRounds(tt.round, tt.max)
		if r.Triggered != tt.want {
			t.Errorf("CheckMaxRounds(%d, %d).Triggered = %v, want %v", tt.round, tt.max, r.Triggered, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// CheckMaxFindings
// ---------------------------------------------------------------------------

func TestBreakerMaxFindings_NotTriggered(t *testing.T) {
	r := CheckMaxFindings(60, 60)
	if r.Triggered {
		t.Errorf("expected not triggered for 60 findings with max 60")
	}
	if r.BreakerName != "max_findings" {
		t.Errorf("BreakerName = %q, want %q", r.BreakerName, "max_findings")
	}
}

func TestBreakerMaxFindings_Triggered(t *testing.T) {
	r := CheckMaxFindings(61, 60)
	if !r.Triggered {
		t.Errorf("expected triggered for 61 findings with max 60")
	}
	if r.Message == "" {
		t.Error("expected non-empty message when triggered")
	}
}

func TestBreakerMaxFindings_Boundary(t *testing.T) {
	tests := []struct {
		findings, max int
		want          bool
	}{
		{0, 60, false},
		{59, 60, false},
		{60, 60, false},
		{61, 60, true},
		{100, 60, true},
		{0, 0, false},
		{1, 0, true},
	}
	for _, tt := range tests {
		r := CheckMaxFindings(tt.findings, tt.max)
		if r.Triggered != tt.want {
			t.Errorf("CheckMaxFindings(%d, %d).Triggered = %v, want %v", tt.findings, tt.max, r.Triggered, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// CheckStaleness
// ---------------------------------------------------------------------------

func TestBreakerStaleness_Triggered(t *testing.T) {
	history := map[string][]string{
		"CRIT-001": {"open", "open"},
	}
	r := CheckStaleness(history, 2)
	if !r.Triggered {
		t.Error("expected triggered for CRIT finding unchanged for 2 rounds with threshold 2")
	}
	if r.Message == "" {
		t.Error("expected non-empty message")
	}
	// Message should contain the stale finding ID.
	if r.BreakerName != "staleness" {
		t.Errorf("BreakerName = %q, want %q", r.BreakerName, "staleness")
	}
}

func TestBreakerStaleness_NotTriggered_StatusChanged(t *testing.T) {
	history := map[string][]string{
		"CRIT-001": {"open", "in_progress"},
	}
	r := CheckStaleness(history, 2)
	if r.Triggered {
		t.Error("expected not triggered when status changed between rounds")
	}
}

func TestBreakerStaleness_NotTriggered_BelowThreshold(t *testing.T) {
	history := map[string][]string{
		"CRIT-001": {"open"},
	}
	r := CheckStaleness(history, 2)
	if r.Triggered {
		t.Error("expected not triggered when history length < threshold")
	}
}

func TestBreakerStaleness_IgnoresMinorAndObservation(t *testing.T) {
	history := map[string][]string{
		"MIN-001": {"open", "open", "open"},
		"OBS-001": {"open", "open", "open"},
	}
	r := CheckStaleness(history, 2)
	if r.Triggered {
		t.Error("expected not triggered for MINOR/OBSERVATION findings")
	}
}

func TestBreakerStaleness_MajorFindingTriggered(t *testing.T) {
	history := map[string][]string{
		"MAJ-005": {"open", "open", "open"},
	}
	r := CheckStaleness(history, 3)
	if !r.Triggered {
		t.Error("expected triggered for MAJ finding unchanged for 3 rounds with threshold 3")
	}
}

func TestBreakerStaleness_CaseInsensitivePrefix(t *testing.T) {
	history := map[string][]string{
		"crit-010": {"open", "open"},
	}
	r := CheckStaleness(history, 2)
	if !r.Triggered {
		t.Error("expected triggered with lowercase crit- prefix")
	}
}

func TestBreakerStaleness_EmptyHistory(t *testing.T) {
	r := CheckStaleness(map[string][]string{}, 2)
	if r.Triggered {
		t.Error("expected not triggered for empty history")
	}
}

func TestBreakerStaleness_NilHistory(t *testing.T) {
	r := CheckStaleness(nil, 2)
	if r.Triggered {
		t.Error("expected not triggered for nil history")
	}
}

func TestBreakerStaleness_OnlyLastNRoundsChecked(t *testing.T) {
	// Status changed earlier but last 2 are the same.
	history := map[string][]string{
		"CRIT-001": {"in_progress", "open", "open"},
	}
	r := CheckStaleness(history, 2)
	if !r.Triggered {
		t.Error("expected triggered: last 2 statuses are identical")
	}
}

// ---------------------------------------------------------------------------
// CheckWallClock
// ---------------------------------------------------------------------------

func TestBreakerWallClock_NotTriggered(t *testing.T) {
	// Start time is right now — elapsed ~0 minutes.
	startedAt := time.Now().UTC().Format(time.RFC3339)
	r := CheckWallClock(startedAt, 30)
	if r.Triggered {
		t.Error("expected not triggered for just-started workflow")
	}
	if r.BreakerName != "wall_clock" {
		t.Errorf("BreakerName = %q, want %q", r.BreakerName, "wall_clock")
	}
}

func TestBreakerWallClock_Triggered(t *testing.T) {
	// Start time was 60 minutes ago; limit is 30 minutes.
	startedAt := time.Now().Add(-60 * time.Minute).UTC().Format(time.RFC3339)
	r := CheckWallClock(startedAt, 30)
	if !r.Triggered {
		t.Error("expected triggered for workflow started 60 min ago with limit 30")
	}
	if r.Message == "" {
		t.Error("expected non-empty message when triggered")
	}
}

func TestBreakerWallClock_ExactlyAtLimit(t *testing.T) {
	// Start time was exactly 30 minutes ago; limit is 30 minutes.
	// elapsed >= limit should trigger.
	startedAt := time.Now().Add(-30 * time.Minute).UTC().Format(time.RFC3339)
	r := CheckWallClock(startedAt, 30)
	if !r.Triggered {
		t.Error("expected triggered when elapsed equals limit")
	}
}

func TestBreakerWallClock_InvalidTimestamp(t *testing.T) {
	r := CheckWallClock("not-a-timestamp", 30)
	if r.Triggered {
		t.Error("expected not triggered for unparseable timestamp")
	}
	if r.Message == "" {
		t.Error("expected message explaining parse failure")
	}
}

// ---------------------------------------------------------------------------
// CheckCost
// ---------------------------------------------------------------------------

func TestBreakerCost_NotTriggered(t *testing.T) {
	r := CheckCost(4.99, 5.00)
	if r.Triggered {
		t.Error("expected not triggered for cost below limit")
	}
	if r.BreakerName != "cost" {
		t.Errorf("BreakerName = %q, want %q", r.BreakerName, "cost")
	}
}

func TestBreakerCost_TriggeredAtExactLimit(t *testing.T) {
	r := CheckCost(5.00, 5.00)
	if !r.Triggered {
		t.Error("expected triggered when cost equals limit")
	}
}

func TestBreakerCost_TriggeredOverLimit(t *testing.T) {
	r := CheckCost(5.01, 5.00)
	if !r.Triggered {
		t.Error("expected triggered when cost exceeds limit")
	}
	if r.Message == "" {
		t.Error("expected non-empty message when triggered")
	}
}

func TestBreakerCost_ZeroCost(t *testing.T) {
	r := CheckCost(0.0, 5.00)
	if r.Triggered {
		t.Error("expected not triggered for zero cost")
	}
}

func TestBreakerCost_Boundary(t *testing.T) {
	tests := []struct {
		cost, max float64
		want      bool
	}{
		{0.0, 5.0, false},
		{4.999, 5.0, false},
		{5.0, 5.0, true},
		{5.001, 5.0, true},
		{0.0, 0.0, true}, // 0 >= 0
	}
	for _, tt := range tests {
		r := CheckCost(tt.cost, tt.max)
		if r.Triggered != tt.want {
			t.Errorf("CheckCost(%.4f, %.4f).Triggered = %v, want %v", tt.cost, tt.max, r.Triggered, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// CheckAllBreakers
// ---------------------------------------------------------------------------

func TestBreakerCheckAll_NoTriggered(t *testing.T) {
	state := &WorkflowStateJSON{
		Round:             2,
		StartedAt:         time.Now().UTC().Format(time.RFC3339),
		CumulativeCostUSD: 1.00,
		FindingsSummary: FindingsSummary{
			Raised: 10,
		},
	}
	config := BreakerConfig{
		MaxRounds:           5,
		MaxTotalFindings:    60,
		StalenessThreshold:  3,
		MaxWallClockMinutes: 120,
		MaxCostUSD:          10.00,
	}
	history := map[string][]string{
		"CRIT-001": {"open", "in_progress"},
	}

	results := CheckAllBreakers(state, config, history)
	if len(results) != 0 {
		t.Errorf("expected 0 triggered breakers, got %d", len(results))
		for _, r := range results {
			t.Logf("  triggered: %s — %s", r.BreakerName, r.Message)
		}
	}
}

func TestBreakerCheckAll_MultipleTriggered(t *testing.T) {
	state := &WorkflowStateJSON{
		Round:             6,
		StartedAt:         time.Now().Add(-3 * time.Hour).UTC().Format(time.RFC3339),
		CumulativeCostUSD: 15.00,
		FindingsSummary: FindingsSummary{
			Raised: 61,
		},
	}
	config := BreakerConfig{
		MaxRounds:           5,
		MaxTotalFindings:    60,
		StalenessThreshold:  2,
		MaxWallClockMinutes: 120,
		MaxCostUSD:          10.00,
	}
	history := map[string][]string{
		"CRIT-001": {"open", "open"},
	}

	results := CheckAllBreakers(state, config, history)

	// Expect: max_rounds, max_findings, staleness, wall_clock, cost = 5 breakers.
	if len(results) != 5 {
		t.Errorf("expected 5 triggered breakers, got %d", len(results))
		for _, r := range results {
			t.Logf("  triggered: %s — %s", r.BreakerName, r.Message)
		}
	}

	// Verify all breaker names are present.
	names := make(map[string]bool)
	for _, r := range results {
		names[r.BreakerName] = true
	}
	for _, expected := range []string{"max_rounds", "max_findings", "staleness", "wall_clock", "cost"} {
		if !names[expected] {
			t.Errorf("expected breaker %q in results", expected)
		}
	}
}

func TestBreakerCheckAll_OnlyTriggeredReturned(t *testing.T) {
	// Only cost should trigger.
	state := &WorkflowStateJSON{
		Round:             1,
		StartedAt:         time.Now().UTC().Format(time.RFC3339),
		CumulativeCostUSD: 10.00,
		FindingsSummary: FindingsSummary{
			Raised: 0,
		},
	}
	config := BreakerConfig{
		MaxRounds:           5,
		MaxTotalFindings:    60,
		StalenessThreshold:  3,
		MaxWallClockMinutes: 120,
		MaxCostUSD:          10.00,
	}

	results := CheckAllBreakers(state, config, nil)
	if len(results) != 1 {
		t.Errorf("expected 1 triggered breaker, got %d", len(results))
		for _, r := range results {
			t.Logf("  triggered: %s — %s", r.BreakerName, r.Message)
		}
		return
	}
	if results[0].BreakerName != "cost" {
		t.Errorf("expected cost breaker, got %q", results[0].BreakerName)
	}
}
