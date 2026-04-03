package codereview

import (
	"fmt"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newTestState() *CodeReviewStateJSON {
	return &CodeReviewStateJSON{
		State:       CRInit,
		Round:       1,
		FeatureName: "test-feature",
		CodePath:    "/tmp/repo",
	}
}

func newTestSM(ws *CodeReviewStateJSON) *CRStateMachine {
	return NewCRStateMachine(ws, DefaultCRStateMachineConfig(), nil)
}

// ---------------------------------------------------------------------------
// Transition table tests
// ---------------------------------------------------------------------------

func TestValidTransitions(t *testing.T) {
	valid := []struct {
		from, to CodeReviewState
	}{
		{CRInit, CRHumanGateScope},
		{CRHumanGateScope, CRReviewing},
		{CRHumanGateScope, CREscalated},
		{CRReviewing, CRFixing},
		{CRReviewing, CRHumanGateFixes},
		{CRReviewing, CRComplete},
		{CRReviewing, CREscalated},
		{CRFixing, CRReviewing},
		{CRFixing, CRHumanGateFixes},
		{CRFixing, CREscalated},
		{CRHumanGateFixes, CRReviewing},
		{CRHumanGateFixes, CRComplete},
		{CRHumanGateFixes, CREscalated},
	}
	for _, tc := range valid {
		if !isValidCRTransition(tc.from, tc.to) {
			t.Errorf("expected %s -> %s to be valid", tc.from, tc.to)
		}
	}
}

func TestInvalidTransitions(t *testing.T) {
	invalid := []struct {
		from, to CodeReviewState
	}{
		{CRInit, CRReviewing},
		{CRInit, CRComplete},
		{CRHumanGateScope, CRFixing},
		{CRComplete, CRReviewing},
		{CRComplete, CRInit},
		{CREscalated, CRReviewing},
		{CREscalated, CRInit},
		{CRFixing, CRComplete},
	}
	for _, tc := range invalid {
		if isValidCRTransition(tc.from, tc.to) {
			t.Errorf("expected %s -> %s to be invalid", tc.from, tc.to)
		}
	}
}

func TestTerminalStates(t *testing.T) {
	if !isCRTerminal(CRComplete) {
		t.Error("CRComplete should be terminal")
	}
	if !isCRTerminal(CREscalated) {
		t.Error("CREscalated should be terminal")
	}
	if isCRTerminal(CRInit) {
		t.Error("CRInit should not be terminal")
	}
	if isCRTerminal(CRReviewing) {
		t.Error("CRReviewing should not be terminal")
	}
}

// ---------------------------------------------------------------------------
// StateMachine basic tests
// ---------------------------------------------------------------------------

func TestStateMachineHappyPath(t *testing.T) {
	ws := newTestState()
	sm := newTestSM(ws)

	steps := []CodeReviewState{
		CRHumanGateScope,
		CRReviewing,
		CRFixing,
		CRReviewing,
		CRComplete,
	}
	for _, step := range steps {
		if err := sm.Transition(step); err != nil {
			t.Fatalf("Transition to %s: %v", step, err)
		}
	}
	if sm.Current() != CRComplete {
		t.Errorf("expected CRComplete, got %s", sm.Current())
	}
	if !sm.IsTerminal() {
		t.Error("expected terminal state")
	}
}

func TestStateMachineRejectsInvalidTransition(t *testing.T) {
	ws := newTestState()
	sm := newTestSM(ws)

	err := sm.Transition(CRReviewing) // CRInit -> CRReviewing is invalid
	if err == nil {
		t.Fatal("expected error for invalid transition")
	}
	if !strings.Contains(err.Error(), "invalid transition") {
		t.Errorf("expected 'invalid transition' error, got: %v", err)
	}
	// State should be unchanged.
	if sm.Current() != CRInit {
		t.Errorf("state should remain CRInit, got %s", sm.Current())
	}
}

func TestStateMachineTerminalStateRejectsTransition(t *testing.T) {
	ws := newTestState()
	ws.State = CRComplete
	sm := newTestSM(ws)

	err := sm.Transition(CRReviewing)
	if err == nil {
		t.Fatal("expected error transitioning from terminal state")
	}
}

func TestStateMachineOnTransitionCallback(t *testing.T) {
	ws := newTestState()
	called := false
	sm := NewCRStateMachine(ws, DefaultCRStateMachineConfig(), func(ws *CodeReviewStateJSON) error {
		called = true
		return nil
	})

	if err := sm.Transition(CRHumanGateScope); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("expected onTransition callback to be called")
	}
}

func TestStateMachineOnTransitionRollback(t *testing.T) {
	ws := newTestState()
	sm := NewCRStateMachine(ws, DefaultCRStateMachineConfig(), func(ws *CodeReviewStateJSON) error {
		return fmt.Errorf("persist failed")
	})

	err := sm.Transition(CRHumanGateScope)
	if err == nil {
		t.Fatal("expected error from callback failure")
	}
	if !strings.Contains(err.Error(), "persist failed") {
		t.Errorf("expected 'persist failed' in error, got: %v", err)
	}
	// State should be rolled back.
	if sm.Current() != CRInit {
		t.Errorf("state should be rolled back to CRInit, got %s", sm.Current())
	}
}

func TestStateMachineUpdatedAt(t *testing.T) {
	ws := newTestState()
	sm := newTestSM(ws)

	before := ws.UpdatedAt
	if err := sm.Transition(CRHumanGateScope); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ws.UpdatedAt == before {
		t.Error("expected UpdatedAt to be updated")
	}
}

func TestStateMachineRestoreState(t *testing.T) {
	ws := newTestState()
	sm := newTestSM(ws)

	newWS := &CodeReviewStateJSON{
		State: CRReviewing,
		Round: 2,
	}
	sm.RestoreState(newWS)

	if sm.Current() != CRReviewing {
		t.Errorf("expected CRReviewing after restore, got %s", sm.Current())
	}
	if sm.State().Round != 2 {
		t.Errorf("expected round 2 after restore, got %d", sm.State().Round)
	}
}

func TestStateMachineStateAccessor(t *testing.T) {
	ws := newTestState()
	sm := newTestSM(ws)

	if sm.State() != ws {
		t.Error("State() should return the same pointer")
	}
}

// ---------------------------------------------------------------------------
// MaxRoundsGuard tests
// ---------------------------------------------------------------------------

func TestMaxRoundsGuardAllowsInitialReview(t *testing.T) {
	ws := newTestState()
	ws.State = CRHumanGateScope
	ws.Round = 1
	sm := newTestSM(ws)

	if err := sm.Transition(CRReviewing); err != nil {
		t.Fatalf("initial review (round 1) should be allowed: %v", err)
	}
}

func TestMaxRoundsGuardAllowsReReviews(t *testing.T) {
	cfg := CRStateMachineConfig{
		MaxRounds:           3,
		MaxCostUSD:          1000,
		MaxWallClockMinutes: 1000,
	}
	// Rounds 1, 2, 3 should all be allowed with MaxRounds=3.
	for _, round := range []int{1, 2, 3} {
		ws := &CodeReviewStateJSON{
			State: CRHumanGateFixes,
			Round: round,
		}
		sm := NewCRStateMachine(ws, cfg, nil)
		if err := sm.Transition(CRReviewing); err != nil {
			t.Errorf("round %d should be allowed with MaxRounds=3: %v", round, err)
		}
	}
}

func TestMaxRoundsGuardBlocksExcess(t *testing.T) {
	cfg := CRStateMachineConfig{
		MaxRounds:           3,
		MaxCostUSD:          1000,
		MaxWallClockMinutes: 1000,
	}
	// Round 4 exceeds MaxRounds=3, should be blocked.
	ws := &CodeReviewStateJSON{
		State: CRHumanGateFixes,
		Round: 4,
	}
	sm := NewCRStateMachine(ws, cfg, nil)
	err := sm.Transition(CRReviewing)
	if err == nil {
		t.Fatal("expected guard to block round 4 with MaxRounds=3")
	}
	if !strings.Contains(err.Error(), "max review rounds exceeded") {
		t.Errorf("expected max rounds error, got: %v", err)
	}
}

func TestMaxRoundsGuardZeroBlocksAllReviews(t *testing.T) {
	cfg := CRStateMachineConfig{
		MaxRounds:           0,
		MaxCostUSD:          1000,
		MaxWallClockMinutes: 1000,
	}
	// With MaxRounds=0, even round 1 is blocked (round 1 > 0).
	// The orchestrator must route to CR_HUMAN_GATE_FIXES instead.
	ws := &CodeReviewStateJSON{
		State: CRHumanGateScope,
		Round: 1,
	}
	sm := NewCRStateMachine(ws, cfg, nil)
	err := sm.Transition(CRReviewing)
	if err == nil {
		t.Fatal("expected guard to block round 1 with MaxRounds=0")
	}
}

// ---------------------------------------------------------------------------
// CostGuard tests
// ---------------------------------------------------------------------------

func TestCostGuardAllowsUnderBudget(t *testing.T) {
	ws := newTestState()
	ws.State = CRHumanGateScope
	ws.CumulativeCostUSD = 49.99
	sm := newTestSM(ws)

	if err := sm.Transition(CRReviewing); err != nil {
		t.Fatalf("under budget should be allowed: %v", err)
	}
}

func TestCostGuardBlocksOverBudget(t *testing.T) {
	ws := newTestState()
	ws.State = CRHumanGateScope
	ws.CumulativeCostUSD = 50.01

	sm := newTestSM(ws)
	err := sm.Transition(CRReviewing)
	if err == nil {
		t.Fatal("expected guard to block over-budget transition")
	}
	if !strings.Contains(err.Error(), "cost budget exceeded") {
		t.Errorf("expected cost budget error, got: %v", err)
	}
}

func TestCostGuardAllowsExactBudget(t *testing.T) {
	ws := newTestState()
	ws.State = CRHumanGateScope
	ws.CumulativeCostUSD = 50.0
	sm := newTestSM(ws)

	if err := sm.Transition(CRReviewing); err != nil {
		t.Fatalf("exact budget should be allowed (not exceeded): %v", err)
	}
}

// ---------------------------------------------------------------------------
// WallClockGuard tests
// ---------------------------------------------------------------------------

func TestWallClockGuardAllowsUnderLimit(t *testing.T) {
	ws := newTestState()
	ws.State = CRHumanGateScope
	ws.CumulativeWallClockSeconds = 7199 // just under 120 min
	sm := newTestSM(ws)

	if err := sm.Transition(CRReviewing); err != nil {
		t.Fatalf("under limit should be allowed: %v", err)
	}
}

func TestWallClockGuardBlocksOverLimit(t *testing.T) {
	ws := newTestState()
	ws.State = CRHumanGateScope
	ws.CumulativeWallClockSeconds = 7201 // just over 120 min

	sm := newTestSM(ws)
	err := sm.Transition(CRReviewing)
	if err == nil {
		t.Fatal("expected guard to block over-time transition")
	}
	if !strings.Contains(err.Error(), "wall clock budget exceeded") {
		t.Errorf("expected wall clock error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Custom guard tests
// ---------------------------------------------------------------------------

func TestAddGuard(t *testing.T) {
	ws := newTestState()
	sm := newTestSM(ws)

	sm.AddGuard(func(from, to CodeReviewState, ws *CodeReviewStateJSON) error {
		return fmt.Errorf("custom guard blocked")
	})

	err := sm.Transition(CRHumanGateScope)
	if err == nil {
		t.Fatal("expected custom guard to block transition")
	}
	if !strings.Contains(err.Error(), "custom guard blocked") {
		t.Errorf("expected custom guard error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// CRStateMachineConfigFromConfig tests
// ---------------------------------------------------------------------------

func TestCRStateMachineConfigFromConfig(t *testing.T) {
	cfg := &CodeReviewConfig{
		MaxRounds:           5,
		MaxCostUSD:          100.0,
		MaxWallClockMinutes: 180,
	}
	smCfg := CRStateMachineConfigFromConfig(cfg)

	if smCfg.MaxRounds != 5 {
		t.Errorf("MaxRounds: got %d, want 5", smCfg.MaxRounds)
	}
	if smCfg.MaxCostUSD != 100.0 {
		t.Errorf("MaxCostUSD: got %f, want 100.0", smCfg.MaxCostUSD)
	}
	if smCfg.MaxWallClockMinutes != 180 {
		t.Errorf("MaxWallClockMinutes: got %d, want 180", smCfg.MaxWallClockMinutes)
	}
}

// ---------------------------------------------------------------------------
// Escalation from various states
// ---------------------------------------------------------------------------

func TestEscalationFromAnyNonTerminal(t *testing.T) {
	escalatableStates := []CodeReviewState{
		CRHumanGateScope,
		CRReviewing,
		CRFixing,
		CRHumanGateFixes,
	}
	for _, state := range escalatableStates {
		ws := &CodeReviewStateJSON{State: state, Round: 1}
		sm := NewCRStateMachine(ws, CRStateMachineConfig{
			MaxRounds:           10,
			MaxCostUSD:          1000,
			MaxWallClockMinutes: 1000,
		}, nil)
		if err := sm.Transition(CREscalated); err != nil {
			t.Errorf("escalation from %s should be allowed: %v", state, err)
		}
	}
}
