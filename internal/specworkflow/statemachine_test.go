package specworkflow

import (
	"errors"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// newTestState creates a minimal WorkflowStateJSON starting in the given state.
func newTestState(s WorkflowState) *WorkflowStateJSON {
	now := time.Now().UTC().Format(time.RFC3339)
	return &WorkflowStateJSON{
		State:     s,
		Round:     1,
		StartedAt: now,
		UpdatedAt: now,
	}
}

// newSM creates a StateMachine with default config and no persistence callback.
func newSM(ws *WorkflowStateJSON) *StateMachine {
	return NewStateMachine(ws, DefaultStateMachineConfig(), nil)
}

// ---------------------------------------------------------------------------
// Valid transition paths
// ---------------------------------------------------------------------------

func TestStateMachine_ValidTransitions(t *testing.T) {
	tests := []struct {
		name string
		from WorkflowState
		to   WorkflowState
		// setup mutates the state before transition (e.g. setting flags).
		setup func(ws *WorkflowStateJSON)
	}{
		// INIT -> DISCOVERY
		{"INIT->DISCOVERY", StateInit, StateDiscovery, nil},
		// DISCOVERY -> HUMAN_GATE_1
		{"DISCOVERY->HUMAN_GATE_1", StateDiscovery, StateHumanGate1, nil},
		// DISCOVERY -> ERROR
		{"DISCOVERY->ERROR", StateDiscovery, StateError, nil},
		// HUMAN_GATE_1 -> DRAFTING
		{"HUMAN_GATE_1->DRAFTING", StateHumanGate1, StateDrafting, nil},
		// HUMAN_GATE_1 -> DISCOVERY (correction, count=0 < limit)
		{"HUMAN_GATE_1->DISCOVERY", StateHumanGate1, StateDiscovery, nil},
		// HUMAN_GATE_1 -> ESCALATED
		{"HUMAN_GATE_1->ESCALATED", StateHumanGate1, StateEscalated, nil},
		// DRAFTING -> HUMAN_GATE_2
		{"DRAFTING->HUMAN_GATE_2", StateDrafting, StateHumanGate2, nil},
		// DRAFTING -> ERROR
		{"DRAFTING->ERROR", StateDrafting, StateError, nil},
		// HUMAN_GATE_2 -> REVIEWING
		{"HUMAN_GATE_2->REVIEWING", StateHumanGate2, StateReviewing, nil},
		// HUMAN_GATE_2 -> DRAFTING (redraft, count=0 < 1)
		{"HUMAN_GATE_2->DRAFTING", StateHumanGate2, StateDrafting, nil},
		// HUMAN_GATE_2 -> ESCALATED
		{"HUMAN_GATE_2->ESCALATED", StateHumanGate2, StateEscalated, nil},
		// REVIEWING -> REVISING
		{"REVIEWING->REVISING", StateReviewing, StateRevising, nil},
		// REVIEWING -> JUDGING (only when no critical findings)
		{"REVIEWING->JUDGING", StateReviewing, StateJudging, nil},
		// REVIEWING -> ERROR
		{"REVIEWING->ERROR", StateReviewing, StateError, nil},
		// REVISING -> JUDGING
		{"REVISING->JUDGING", StateRevising, StateJudging, nil},
		// REVISING -> ERROR
		{"REVISING->ERROR", StateRevising, StateError, nil},
		// JUDGING -> REVIEWING
		{"JUDGING->REVIEWING", StateJudging, StateReviewing, nil},
		// JUDGING -> HUMAN_GATE_FINAL (when had critical findings)
		{"JUDGING->HUMAN_GATE_FINAL", StateJudging, StateHumanGateFinal, func(ws *WorkflowStateJSON) {
			ws.HadCriticalFindings = true
		}},
		// JUDGING -> FINALIZED (when no critical findings)
		{"JUDGING->FINALIZED", StateJudging, StateFinalized, func(ws *WorkflowStateJSON) {
			ws.HadCriticalFindings = false
		}},
		// JUDGING -> ESCALATED
		{"JUDGING->ESCALATED", StateJudging, StateEscalated, nil},
		// JUDGING -> ERROR
		{"JUDGING->ERROR", StateJudging, StateError, nil},
		// HUMAN_GATE_FINAL -> FINALIZED
		{"HUMAN_GATE_FINAL->FINALIZED", StateHumanGateFinal, StateFinalized, nil},
		// HUMAN_GATE_FINAL -> REVIEWING
		{"HUMAN_GATE_FINAL->REVIEWING", StateHumanGateFinal, StateReviewing, nil},
		// HUMAN_GATE_FINAL -> ESCALATED
		{"HUMAN_GATE_FINAL->ESCALATED", StateHumanGateFinal, StateEscalated, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ws := newTestState(tt.from)
			if tt.setup != nil {
				tt.setup(ws)
			}
			sm := newSM(ws)
			if err := sm.Transition(tt.to); err != nil {
				t.Fatalf("expected valid transition %s -> %s, got error: %v", tt.from, tt.to, err)
			}
			if sm.Current() != tt.to {
				t.Fatalf("expected current state %s, got %s", tt.to, sm.Current())
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Invalid transitions
// ---------------------------------------------------------------------------

func TestStateMachine_InvalidTransitions(t *testing.T) {
	tests := []struct {
		name string
		from WorkflowState
		to   WorkflowState
	}{
		{"INIT->JUDGING", StateInit, StateJudging},
		{"INIT->FINALIZED", StateInit, StateFinalized},
		{"INIT->REVIEWING", StateInit, StateReviewing},
		{"FINALIZED->INIT", StateFinalized, StateInit},
		{"FINALIZED->DISCOVERY", StateFinalized, StateDiscovery},
		{"FINALIZED->REVIEWING", StateFinalized, StateReviewing},
		{"FINALIZED->ESCALATED", StateFinalized, StateEscalated},
		{"ESCALATED->INIT", StateEscalated, StateInit},
		{"ESCALATED->DISCOVERY", StateEscalated, StateDiscovery},
		{"DISCOVERY->DRAFTING", StateDiscovery, StateDrafting},
		{"DRAFTING->REVIEWING", StateDrafting, StateReviewing},
		{"REVIEWING->FINALIZED", StateReviewing, StateFinalized},
		{"REVISING->REVIEWING", StateRevising, StateReviewing},
		{"HUMAN_GATE_1->REVIEWING", StateHumanGate1, StateReviewing},
		{"HUMAN_GATE_2->JUDGING", StateHumanGate2, StateJudging},
		{"HUMAN_GATE_FINAL->DRAFTING", StateHumanGateFinal, StateDrafting},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ws := newTestState(tt.from)
			sm := newSM(ws)
			if err := sm.Transition(tt.to); err == nil {
				t.Fatalf("expected error for invalid transition %s -> %s, got nil", tt.from, tt.to)
			}
			// State must not change.
			if sm.Current() != tt.from {
				t.Fatalf("state changed to %s on invalid transition", sm.Current())
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Any state -> ERROR (universal rule)
// ---------------------------------------------------------------------------

func TestStateMachine_AnyStateToError(t *testing.T) {
	allNonError := []WorkflowState{
		StateInit, StateDiscovery, StateHumanGate1, StateDrafting,
		StateHumanGate2, StateReviewing, StateRevising, StateJudging,
		StateHumanGateFinal, StateFinalized, StateEscalated,
	}

	for _, from := range allNonError {
		t.Run(from.String()+"->ERROR", func(t *testing.T) {
			ws := newTestState(from)
			sm := newSM(ws)
			if err := sm.Transition(StateError); err != nil {
				t.Fatalf("expected any -> ERROR to succeed for %s, got: %v", from, err)
			}
			if sm.Current() != StateError {
				t.Fatalf("expected ERROR, got %s", sm.Current())
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ERROR recovery paths
// ---------------------------------------------------------------------------

func TestStateMachine_ErrorRecovery(t *testing.T) {
	// ERROR can transition to any non-ERROR state.
	targets := []WorkflowState{
		StateInit, StateDiscovery, StateHumanGate1, StateDrafting,
		StateHumanGate2, StateReviewing, StateRevising, StateJudging,
		StateHumanGateFinal, StateFinalized, StateEscalated,
	}

	for _, to := range targets {
		t.Run("ERROR->"+to.String(), func(t *testing.T) {
			ws := newTestState(StateError)
			sm := newSM(ws)
			if err := sm.Transition(to); err != nil {
				t.Fatalf("expected ERROR -> %s to succeed, got: %v", to, err)
			}
			if sm.Current() != to {
				t.Fatalf("expected %s, got %s", to, sm.Current())
			}
		})
	}
}

// ERROR -> ERROR should be rejected (no-op / meaningless).
func TestStateMachine_ErrorToErrorBlocked(t *testing.T) {
	ws := newTestState(StateError)
	sm := newSM(ws)
	if err := sm.Transition(StateError); err == nil {
		t.Fatal("expected ERROR -> ERROR to be blocked")
	}
}

// ---------------------------------------------------------------------------
// Gate1CorrectionGuard
// ---------------------------------------------------------------------------

func TestStateMachine_Gate1CorrectionGuard_Blocks(t *testing.T) {
	cfg := StateMachineConfig{MaxGateCorrections: 2, MaxGate2Redrafts: 1, MaxRounds: 5}
	ws := newTestState(StateHumanGate1)
	ws.Gate1CorrectionCount = 3 // past limit (HandleCorrect increments before transition)
	sm := NewStateMachine(ws, cfg, nil)

	err := sm.Transition(StateDiscovery)
	if err == nil {
		t.Fatal("expected Gate1CorrectionGuard to block transition when count > max")
	}
	if sm.Current() != StateHumanGate1 {
		t.Fatalf("state should remain HUMAN_GATE_1, got %s", sm.Current())
	}
}

func TestStateMachine_Gate1CorrectionGuard_Allows(t *testing.T) {
	cfg := StateMachineConfig{MaxGateCorrections: 3, MaxGate2Redrafts: 1, MaxRounds: 5}
	ws := newTestState(StateHumanGate1)
	ws.Gate1CorrectionCount = 2 // below limit of 3
	sm := NewStateMachine(ws, cfg, nil)

	if err := sm.Transition(StateDiscovery); err != nil {
		t.Fatalf("expected transition to be allowed, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Gate2RedraftGuard
// ---------------------------------------------------------------------------

func TestStateMachine_Gate2RedraftGuard_Blocks(t *testing.T) {
	ws := newTestState(StateHumanGate2)
	ws.Gate2RedraftCount = 2 // past limit (handler increments before transition)
	sm := newSM(ws)

	err := sm.Transition(StateDrafting)
	if err == nil {
		t.Fatal("expected Gate2RedraftGuard to block transition")
	}
	if sm.Current() != StateHumanGate2 {
		t.Fatalf("state should remain HUMAN_GATE_2, got %s", sm.Current())
	}
}

func TestStateMachine_Gate2RedraftGuard_Allows(t *testing.T) {
	ws := newTestState(StateHumanGate2)
	ws.Gate2RedraftCount = 0 // below limit
	sm := newSM(ws)

	if err := sm.Transition(StateDrafting); err != nil {
		t.Fatalf("expected transition to be allowed, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// MaxRoundsGuard
// ---------------------------------------------------------------------------

func TestStateMachine_MaxRoundsGuard_Blocks(t *testing.T) {
	cfg := StateMachineConfig{MaxGateCorrections: 3, MaxGate2Redrafts: 1, MaxRounds: 3}
	ws := newTestState(StateHumanGate2)
	ws.Round = 4 // exceeds max of 3
	sm := NewStateMachine(ws, cfg, nil)

	err := sm.Transition(StateReviewing)
	if err == nil {
		t.Fatal("expected MaxRoundsGuard to block transition")
	}
	if sm.Current() != StateHumanGate2 {
		t.Fatalf("state should remain HUMAN_GATE_2, got %s", sm.Current())
	}
}

func TestStateMachine_MaxRoundsGuard_Allows(t *testing.T) {
	cfg := StateMachineConfig{MaxGateCorrections: 3, MaxGate2Redrafts: 1, MaxRounds: 3}
	ws := newTestState(StateHumanGate2)
	ws.Round = 3 // at limit (not exceeding)
	sm := NewStateMachine(ws, cfg, nil)

	if err := sm.Transition(StateReviewing); err != nil {
		t.Fatalf("expected transition to be allowed, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// JudgingToFinalizedGuard
// ---------------------------------------------------------------------------

func TestStateMachine_JudgingToFinalized_BlockedWithCritical(t *testing.T) {
	ws := newTestState(StateJudging)
	ws.HadCriticalFindings = true
	sm := newSM(ws)

	err := sm.Transition(StateFinalized)
	if err == nil {
		t.Fatal("expected JudgingToFinalizedGuard to block when had_critical_findings=true")
	}
	if sm.Current() != StateJudging {
		t.Fatalf("state should remain JUDGING, got %s", sm.Current())
	}
}

func TestStateMachine_JudgingToFinalized_AllowedWithoutCritical(t *testing.T) {
	ws := newTestState(StateJudging)
	ws.HadCriticalFindings = false
	sm := newSM(ws)

	if err := sm.Transition(StateFinalized); err != nil {
		t.Fatalf("expected transition to be allowed, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// JudgingToHumanGateFinalGuard
// ---------------------------------------------------------------------------

func TestStateMachine_JudgingToHumanGateFinal_BlockedWithoutCritical(t *testing.T) {
	ws := newTestState(StateJudging)
	ws.HadCriticalFindings = false
	sm := newSM(ws)

	err := sm.Transition(StateHumanGateFinal)
	if err == nil {
		t.Fatal("expected JudgingToHumanGateFinalGuard to block when had_critical_findings=false")
	}
	if sm.Current() != StateJudging {
		t.Fatalf("state should remain JUDGING, got %s", sm.Current())
	}
}

func TestStateMachine_JudgingToHumanGateFinal_AllowedWithCritical(t *testing.T) {
	ws := newTestState(StateJudging)
	ws.HadCriticalFindings = true
	sm := newSM(ws)

	if err := sm.Transition(StateHumanGateFinal); err != nil {
		t.Fatalf("expected transition to be allowed, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// onTransition callback
// ---------------------------------------------------------------------------

func TestStateMachine_OnTransitionCalled(t *testing.T) {
	var called bool
	ws := newTestState(StateInit)
	sm := NewStateMachine(ws, DefaultStateMachineConfig(), func(ws *WorkflowStateJSON) error {
		called = true
		return nil
	})

	if err := sm.Transition(StateDiscovery); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("expected onTransition callback to be called")
	}
}

func TestStateMachine_OnTransitionRollback(t *testing.T) {
	ws := newTestState(StateInit)
	sm := NewStateMachine(ws, DefaultStateMachineConfig(), func(ws *WorkflowStateJSON) error {
		return errors.New("persistence failure")
	})

	err := sm.Transition(StateDiscovery)
	if err == nil {
		t.Fatal("expected error from failing onTransition callback")
	}
	// State must be rolled back.
	if sm.Current() != StateInit {
		t.Fatalf("expected rollback to INIT, got %s", sm.Current())
	}
}

// ---------------------------------------------------------------------------
// UpdatedAt is set on transition
// ---------------------------------------------------------------------------

func TestStateMachine_UpdatedAtSet(t *testing.T) {
	ws := newTestState(StateInit)
	// Set to a sentinel so we can detect that Transition overwrites it.
	ws.UpdatedAt = "1970-01-01T00:00:00Z"

	sm := newSM(ws)
	if err := sm.Transition(StateDiscovery); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ws.UpdatedAt == "1970-01-01T00:00:00Z" {
		t.Fatal("expected UpdatedAt to be updated after transition")
	}
	// Verify it parses as valid RFC3339.
	if _, err := time.Parse(time.RFC3339, ws.UpdatedAt); err != nil {
		t.Fatalf("UpdatedAt is not valid RFC3339: %s", ws.UpdatedAt)
	}
}

// ---------------------------------------------------------------------------
// Custom guard via AddGuard
// ---------------------------------------------------------------------------

func TestStateMachine_AddGuard(t *testing.T) {
	ws := newTestState(StateInit)
	sm := newSM(ws)

	// Add a custom guard that blocks everything.
	sm.AddGuard(func(from, to WorkflowState, ws *WorkflowStateJSON) error {
		return errors.New("custom block")
	})

	err := sm.Transition(StateDiscovery)
	if err == nil {
		t.Fatal("expected custom guard to block transition")
	}
	if sm.Current() != StateInit {
		t.Fatalf("state should remain INIT, got %s", sm.Current())
	}
}

// ---------------------------------------------------------------------------
// Full happy-path walkthrough
// ---------------------------------------------------------------------------

func TestStateMachine_FullHappyPath(t *testing.T) {
	ws := newTestState(StateInit)
	sm := newSM(ws)

	steps := []WorkflowState{
		StateDiscovery,
		StateHumanGate1,
		StateDrafting,
		StateHumanGate2,
		StateReviewing,
		StateRevising,
		StateJudging,
		StateFinalized, // no critical findings -> direct finalize
	}

	for _, step := range steps {
		if err := sm.Transition(step); err != nil {
			t.Fatalf("failed at %s -> %s: %v", sm.Current(), step, err)
		}
	}

	if sm.Current() != StateFinalized {
		t.Fatalf("expected FINALIZED, got %s", sm.Current())
	}
}

func TestStateMachine_FullPathWithCriticalFindings(t *testing.T) {
	ws := newTestState(StateInit)
	sm := newSM(ws)

	// Walk to JUDGING.
	for _, step := range []WorkflowState{
		StateDiscovery, StateHumanGate1, StateDrafting,
		StateHumanGate2, StateReviewing, StateRevising, StateJudging,
	} {
		if err := sm.Transition(step); err != nil {
			t.Fatalf("failed at -> %s: %v", step, err)
		}
	}

	// Mark critical findings.
	ws.HadCriticalFindings = true

	// JUDGING -> HUMAN_GATE_FINAL -> FINALIZED
	if err := sm.Transition(StateHumanGateFinal); err != nil {
		t.Fatalf("JUDGING -> HUMAN_GATE_FINAL failed: %v", err)
	}
	if err := sm.Transition(StateFinalized); err != nil {
		t.Fatalf("HUMAN_GATE_FINAL -> FINALIZED failed: %v", err)
	}
	if sm.Current() != StateFinalized {
		t.Fatalf("expected FINALIZED, got %s", sm.Current())
	}
}

// ---------------------------------------------------------------------------
// RestoreState
// ---------------------------------------------------------------------------

func TestStateMachine_RestoreState(t *testing.T) {
	// Start at INIT.
	ws := newTestState(StateInit)
	sm := newSM(ws)

	if sm.Current() != StateInit {
		t.Fatalf("expected INIT, got %s", sm.Current())
	}

	// Restore to HUMAN_GATE_1 — bypasses transition validation.
	restoredState := &WorkflowStateJSON{
		State:       StateHumanGate1,
		Round:       1,
		FeatureName: "test-feature",
		StartedAt:   ws.StartedAt,
		UpdatedAt:   ws.UpdatedAt,
	}
	sm.RestoreState(restoredState)

	if sm.Current() != StateHumanGate1 {
		t.Fatalf("expected HUMAN_GATE_1 after restore, got %s", sm.Current())
	}

	// Verify the state object is the one we passed in.
	if sm.State() != restoredState {
		t.Fatal("State() should return the restored state object")
	}

	// Now a normal transition from HUMAN_GATE_1 -> DRAFTING should work.
	if err := sm.Transition(StateDrafting); err != nil {
		t.Fatalf("transition HUMAN_GATE_1 -> DRAFTING after restore failed: %v", err)
	}
	if sm.Current() != StateDrafting {
		t.Fatalf("expected DRAFTING, got %s", sm.Current())
	}
}

func TestStateMachine_RestoreState_InvalidTransitionBefore(t *testing.T) {
	// Start at INIT.
	ws := newTestState(StateInit)
	sm := newSM(ws)

	// Restore directly to REVIEWING — skipping DISCOVERY, gates, etc.
	restoredState := &WorkflowStateJSON{
		State:     StateReviewing,
		Round:     2,
		StartedAt: ws.StartedAt,
		UpdatedAt: ws.UpdatedAt,
	}
	sm.RestoreState(restoredState)

	if sm.Current() != StateReviewing {
		t.Fatalf("expected REVIEWING after restore, got %s", sm.Current())
	}
}
