package codedoc

import (
	"fmt"
	"strings"
	"testing"
)

// newTestSM creates a CDStateMachine with default config and no callback.
func newTestSM(initialState CDState) *CDStateMachine {
	ws := &CDStateJSON{
		State: initialState,
		Round: 1,
	}
	cfg := DefaultCDStateMachineConfig()
	return NewCDStateMachine(ws, cfg, nil)
}

// newTestSMWithConfig creates a CDStateMachine with a custom config.
func newTestSMWithConfig(initialState CDState, cfg CDStateMachineConfig) *CDStateMachine {
	ws := &CDStateJSON{
		State: initialState,
		Round: 1,
	}
	return NewCDStateMachine(ws, cfg, nil)
}

// ---------------------------------------------------------------------------
// State constant tests
// ---------------------------------------------------------------------------

func TestStateMachine_AllStatesExist(t *testing.T) {
	expected := []CDState{
		CDInit, CDDiscovery, CDHumanGateScope, CDDrafting, CDSanitising,
		CDHumanGateDraft, CDReviewing, CDRevising, CDJudging,
		CDHumanGateFinal, CDWriting, CDComplete, CDEscalated, CDError,
	}
	for _, s := range expected {
		if s.String() == "" {
			t.Errorf("state %v has empty string representation", s)
		}
	}
	if len(expected) != 14 {
		t.Errorf("expected 14 states, got %d", len(expected))
	}
}

func TestStateMachine_StateStringValues(t *testing.T) {
	tests := []struct {
		state CDState
		want  string
	}{
		{CDInit, "CD_INIT"},
		{CDDiscovery, "CD_DISCOVERY"},
		{CDHumanGateScope, "CD_HUMAN_GATE_SCOPE"},
		{CDDrafting, "CD_DRAFTING"},
		{CDSanitising, "CD_SANITISING"},
		{CDHumanGateDraft, "CD_HUMAN_GATE_DRAFT"},
		{CDReviewing, "CD_REVIEWING"},
		{CDRevising, "CD_REVISING"},
		{CDJudging, "CD_JUDGING"},
		{CDHumanGateFinal, "CD_HUMAN_GATE_FINAL"},
		{CDWriting, "CD_WRITING"},
		{CDComplete, "CD_COMPLETE"},
		{CDEscalated, "CD_ESCALATED"},
		{CDError, "CD_ERROR"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("state.String() = %q, want %q", got, tt.want)
		}
	}
}

func TestStateMachine_IsTerminal(t *testing.T) {
	terminal := []CDState{CDComplete, CDEscalated}
	nonTerminal := []CDState{
		CDInit, CDDiscovery, CDHumanGateScope, CDDrafting, CDSanitising,
		CDHumanGateDraft, CDReviewing, CDRevising, CDJudging,
		CDHumanGateFinal, CDWriting, CDError,
	}
	for _, s := range terminal {
		if !s.IsTerminal() {
			t.Errorf("%s should be terminal", s)
		}
	}
	for _, s := range nonTerminal {
		if s.IsTerminal() {
			t.Errorf("%s should not be terminal", s)
		}
	}
}

func TestStateMachine_IsGate(t *testing.T) {
	gates := []CDState{CDHumanGateScope, CDHumanGateDraft, CDHumanGateFinal}
	nonGates := []CDState{
		CDInit, CDDiscovery, CDDrafting, CDSanitising,
		CDReviewing, CDRevising, CDJudging, CDWriting,
		CDComplete, CDEscalated, CDError,
	}
	for _, s := range gates {
		if !s.IsGate() {
			t.Errorf("%s should be a gate", s)
		}
	}
	for _, s := range nonGates {
		if s.IsGate() {
			t.Errorf("%s should not be a gate", s)
		}
	}
}

// ---------------------------------------------------------------------------
// Valid transition tests (Section 2 transition table)
// ---------------------------------------------------------------------------

func TestStateMachine_ValidTransitions(t *testing.T) {
	tests := []struct {
		name string
		from CDState
		to   CDState
	}{
		{"INIT -> DISCOVERY", CDInit, CDDiscovery},
		{"DISCOVERY -> HUMAN_GATE_SCOPE", CDDiscovery, CDHumanGateScope},
		{"DISCOVERY -> ESCALATED", CDDiscovery, CDEscalated},
		{"HUMAN_GATE_SCOPE -> DRAFTING", CDHumanGateScope, CDDrafting},
		{"HUMAN_GATE_SCOPE -> DISCOVERY (scope re-run)", CDHumanGateScope, CDDiscovery},
		{"HUMAN_GATE_SCOPE -> ESCALATED", CDHumanGateScope, CDEscalated},
		{"DRAFTING -> SANITISING", CDDrafting, CDSanitising},
		{"DRAFTING -> ESCALATED", CDDrafting, CDEscalated},
		{"SANITISING -> HUMAN_GATE_DRAFT", CDSanitising, CDHumanGateDraft},
		{"SANITISING -> DRAFTING (unredacted secrets)", CDSanitising, CDDrafting},
		{"HUMAN_GATE_DRAFT -> REVIEWING", CDHumanGateDraft, CDReviewing},
		{"HUMAN_GATE_DRAFT -> DRAFTING", CDHumanGateDraft, CDDrafting},
		{"HUMAN_GATE_DRAFT -> ESCALATED", CDHumanGateDraft, CDEscalated},
		{"REVIEWING -> REVISING", CDReviewing, CDRevising},
		{"REVIEWING -> JUDGING", CDReviewing, CDJudging},
		{"REVIEWING -> ESCALATED", CDReviewing, CDEscalated},
		{"REVISING -> JUDGING", CDRevising, CDJudging},
		{"REVISING -> ESCALATED", CDRevising, CDEscalated},
		{"JUDGING -> REVIEWING (REVISE verdict)", CDJudging, CDReviewing},
		{"JUDGING -> HUMAN_GATE_FINAL", CDJudging, CDHumanGateFinal},
		{"JUDGING -> WRITING (PASS verdict)", CDJudging, CDWriting},
		{"JUDGING -> ESCALATED", CDJudging, CDEscalated},
		{"HUMAN_GATE_FINAL -> WRITING", CDHumanGateFinal, CDWriting},
		{"HUMAN_GATE_FINAL -> ESCALATED", CDHumanGateFinal, CDEscalated},
		{"WRITING -> COMPLETE", CDWriting, CDComplete},
		{"WRITING -> ESCALATED", CDWriting, CDEscalated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm := newTestSM(tt.from)
			// For JUDGING -> WRITING, need no critical findings
			if tt.from == CDJudging && tt.to == CDWriting {
				sm.State().HadCriticalFindings = false
			}
			// For JUDGING -> HUMAN_GATE_FINAL, need critical findings
			if tt.from == CDJudging && tt.to == CDHumanGateFinal {
				sm.State().HadCriticalFindings = true
			}
			if err := sm.Transition(tt.to); err != nil {
				t.Errorf("expected valid transition %s -> %s, got error: %v", tt.from, tt.to, err)
			}
			if sm.Current() != tt.to {
				t.Errorf("state should be %s after transition, got %s", tt.to, sm.Current())
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Invalid transition tests
// ---------------------------------------------------------------------------

func TestStateMachine_InvalidTransitions(t *testing.T) {
	tests := []struct {
		name string
		from CDState
		to   CDState
	}{
		{"INIT -> COMPLETE", CDInit, CDComplete},
		{"INIT -> WRITING", CDInit, CDWriting},
		{"INIT -> REVIEWING", CDInit, CDReviewing},
		{"DISCOVERY -> DRAFTING (skips gate)", CDDiscovery, CDDrafting},
		{"COMPLETE -> INIT", CDComplete, CDInit},
		{"COMPLETE -> DISCOVERY", CDComplete, CDDiscovery},
		{"ESCALATED -> INIT", CDEscalated, CDInit},
		{"WRITING -> REVIEWING", CDWriting, CDReviewing},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm := newTestSM(tt.from)
			err := sm.Transition(tt.to)
			if err == nil {
				t.Errorf("expected error for invalid transition %s -> %s", tt.from, tt.to)
			}
			if !strings.Contains(err.Error(), "invalid transition") {
				t.Errorf("error should contain 'invalid transition', got: %v", err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Universal CD_ERROR transition tests
// ---------------------------------------------------------------------------

func TestStateMachine_AnyNonTerminalToError(t *testing.T) {
	nonTerminal := []CDState{
		CDInit, CDDiscovery, CDHumanGateScope, CDDrafting, CDSanitising,
		CDHumanGateDraft, CDReviewing, CDRevising, CDJudging,
		CDHumanGateFinal, CDWriting, CDError,
	}
	for _, s := range nonTerminal {
		if s == CDError {
			continue // CD_ERROR -> CD_ERROR is separately tested
		}
		t.Run(fmt.Sprintf("%s -> CD_ERROR", s), func(t *testing.T) {
			sm := newTestSM(s)
			if err := sm.Transition(CDError); err != nil {
				t.Errorf("expected %s -> CD_ERROR to succeed, got: %v", s, err)
			}
		})
	}
}

func TestStateMachine_ErrorToErrorBlocked(t *testing.T) {
	sm := newTestSM(CDError)
	err := sm.Transition(CDError)
	if err == nil {
		t.Error("expected CD_ERROR -> CD_ERROR to be blocked")
	}
}

func TestStateMachine_TerminalToErrorBlocked(t *testing.T) {
	for _, s := range []CDState{CDComplete, CDEscalated} {
		t.Run(fmt.Sprintf("%s -> CD_ERROR", s), func(t *testing.T) {
			sm := newTestSM(s)
			err := sm.Transition(CDError)
			if err == nil {
				t.Errorf("expected %s -> CD_ERROR to be blocked", s)
			}
		})
	}
}

func TestStateMachine_ErrorRecovery(t *testing.T) {
	sm := newTestSM(CDError)
	if err := sm.Transition(CDDiscovery); err != nil {
		t.Errorf("expected CD_ERROR -> CD_DISCOVERY to succeed, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Guard tests
// ---------------------------------------------------------------------------

func TestStateMachine_MaxRoundsGuard(t *testing.T) {
	cfg := DefaultCDStateMachineConfig()
	cfg.MaxRounds = 3
	sm := newTestSMWithConfig(CDJudging, cfg)
	sm.State().Round = 4 // exceeds max of 3
	err := sm.Transition(CDReviewing)
	if err == nil {
		t.Error("expected MaxRoundsGuard to block transition")
	}
	if !strings.Contains(err.Error(), "max review rounds exceeded") {
		t.Errorf("expected 'max review rounds exceeded' in error, got: %v", err)
	}
}

func TestStateMachine_MaxRoundsGuard_Allows(t *testing.T) {
	cfg := DefaultCDStateMachineConfig()
	cfg.MaxRounds = 3
	sm := newTestSMWithConfig(CDJudging, cfg)
	sm.State().Round = 3 // at max, not exceeded
	if err := sm.Transition(CDReviewing); err != nil {
		t.Errorf("expected round 3 with max 3 to succeed, got: %v", err)
	}
}

func TestStateMachine_CostGuard(t *testing.T) {
	cfg := DefaultCDStateMachineConfig()
	cfg.MaxCostUSD = 50.0
	sm := newTestSMWithConfig(CDDiscovery, cfg)
	sm.State().CumulativeCostUSD = 51.0
	err := sm.Transition(CDHumanGateScope)
	if err == nil {
		t.Error("expected CostGuard to block transition")
	}
	if !strings.Contains(err.Error(), "cost budget exceeded") {
		t.Errorf("expected 'cost budget exceeded' in error, got: %v", err)
	}
}

func TestStateMachine_CostGuard_AllowsTerminal(t *testing.T) {
	cfg := DefaultCDStateMachineConfig()
	cfg.MaxCostUSD = 50.0
	sm := newTestSMWithConfig(CDDiscovery, cfg)
	sm.State().CumulativeCostUSD = 51.0
	if err := sm.Transition(CDEscalated); err != nil {
		t.Errorf("expected cost guard to allow terminal transition, got: %v", err)
	}
}

func TestStateMachine_WallClockGuard(t *testing.T) {
	cfg := DefaultCDStateMachineConfig()
	cfg.MaxWallClockMinutes = 90
	sm := newTestSMWithConfig(CDDiscovery, cfg)
	sm.State().CumulativeWallClockSeconds = 91 * 60 // 91 minutes
	err := sm.Transition(CDHumanGateScope)
	if err == nil {
		t.Error("expected WallClockGuard to block transition")
	}
	if !strings.Contains(err.Error(), "wall clock budget exceeded") {
		t.Errorf("expected 'wall clock budget exceeded' in error, got: %v", err)
	}
}

func TestStateMachine_WallClockGuard_AllowsTerminal(t *testing.T) {
	cfg := DefaultCDStateMachineConfig()
	cfg.MaxWallClockMinutes = 90
	sm := newTestSMWithConfig(CDDiscovery, cfg)
	sm.State().CumulativeWallClockSeconds = 91 * 60
	if err := sm.Transition(CDEscalated); err != nil {
		t.Errorf("expected wall clock guard to allow terminal transition, got: %v", err)
	}
}

func TestStateMachine_GateScopeCorrectionGuard(t *testing.T) {
	cfg := DefaultCDStateMachineConfig()
	cfg.MaxGateCorrections = 3
	sm := newTestSMWithConfig(CDHumanGateScope, cfg)
	sm.State().GateScopeCorrectionCount = 4 // exceeds max of 3
	err := sm.Transition(CDDiscovery)
	if err == nil {
		t.Error("expected GateScopeCorrectionGuard to block transition")
	}
	if !strings.Contains(err.Error(), "scope gate correction limit") {
		t.Errorf("expected 'scope gate correction limit' in error, got: %v", err)
	}
}

func TestStateMachine_GateDraftRedraftGuard(t *testing.T) {
	cfg := DefaultCDStateMachineConfig()
	cfg.MaxGateDraftRedrafts = 2
	sm := newTestSMWithConfig(CDHumanGateDraft, cfg)
	sm.State().GateDraftRedraftCount = 3 // exceeds max of 2
	err := sm.Transition(CDDrafting)
	if err == nil {
		t.Error("expected GateDraftRedraftGuard to block transition")
	}
	if !strings.Contains(err.Error(), "draft gate redraft limit") {
		t.Errorf("expected 'draft gate redraft limit' in error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Judging guard tests
// ---------------------------------------------------------------------------

func TestStateMachine_JudgingToWriting_BlockedWithCriticalFindings(t *testing.T) {
	sm := newTestSM(CDJudging)
	sm.State().HadCriticalFindings = true
	err := sm.Transition(CDWriting)
	if err == nil {
		t.Error("expected JudgingToWritingGuard to block when critical findings exist")
	}
}

func TestStateMachine_JudgingToWriting_AllowedWithoutCriticalFindings(t *testing.T) {
	sm := newTestSM(CDJudging)
	sm.State().HadCriticalFindings = false
	if err := sm.Transition(CDWriting); err != nil {
		t.Errorf("expected JUDGING -> WRITING to succeed without critical findings, got: %v", err)
	}
}

func TestStateMachine_JudgingToHumanGateFinal_BlockedWithoutCriticalFindings(t *testing.T) {
	sm := newTestSM(CDJudging)
	sm.State().HadCriticalFindings = false
	err := sm.Transition(CDHumanGateFinal)
	if err == nil {
		t.Error("expected JudgingToHumanGateFinalGuard to block when no critical findings")
	}
}

func TestStateMachine_JudgingToHumanGateFinal_AllowedWithCriticalFindings(t *testing.T) {
	sm := newTestSM(CDJudging)
	sm.State().HadCriticalFindings = true
	if err := sm.Transition(CDHumanGateFinal); err != nil {
		t.Errorf("expected JUDGING -> HUMAN_GATE_FINAL to succeed with critical findings, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// OnTransition callback tests
// ---------------------------------------------------------------------------

func TestStateMachine_OnTransitionCalled(t *testing.T) {
	called := false
	ws := &CDStateJSON{State: CDInit, Round: 1}
	cfg := DefaultCDStateMachineConfig()
	sm := NewCDStateMachine(ws, cfg, func(ws *CDStateJSON) error {
		called = true
		return nil
	})
	if err := sm.Transition(CDDiscovery); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("onTransition callback was not called")
	}
}

func TestStateMachine_OnTransitionRollback(t *testing.T) {
	ws := &CDStateJSON{State: CDInit, Round: 1}
	cfg := DefaultCDStateMachineConfig()
	sm := NewCDStateMachine(ws, cfg, func(ws *CDStateJSON) error {
		return fmt.Errorf("persist failed")
	})
	err := sm.Transition(CDDiscovery)
	if err == nil {
		t.Fatal("expected error from onTransition failure")
	}
	if sm.Current() != CDInit {
		t.Errorf("expected state to be rolled back to CD_INIT, got %s", sm.Current())
	}
}

// ---------------------------------------------------------------------------
// RestoreState test
// ---------------------------------------------------------------------------

func TestStateMachine_RestoreState(t *testing.T) {
	sm := newTestSM(CDInit)
	restored := &CDStateJSON{State: CDReviewing, Round: 2}
	sm.RestoreState(restored)
	if sm.Current() != CDReviewing {
		t.Errorf("expected restored state CDReviewing, got %s", sm.Current())
	}
	if sm.State().Round != 2 {
		t.Errorf("expected restored round 2, got %d", sm.State().Round)
	}
}

// ---------------------------------------------------------------------------
// AddGuard test
// ---------------------------------------------------------------------------

func TestStateMachine_AddGuard(t *testing.T) {
	sm := newTestSM(CDInit)
	sm.AddGuard(func(from, to CDState, ws *CDStateJSON) error {
		return fmt.Errorf("custom guard blocked")
	})
	err := sm.Transition(CDDiscovery)
	if err == nil {
		t.Error("expected custom guard to block transition")
	}
	if !strings.Contains(err.Error(), "custom guard blocked") {
		t.Errorf("expected 'custom guard blocked' in error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Config extraction test
// ---------------------------------------------------------------------------

func TestStateMachine_ConfigFromCodedocConfig(t *testing.T) {
	cfg := DefaultCodedocConfig()
	smCfg := CDStateMachineConfigFromConfig(&cfg)
	if smCfg.MaxRounds != cfg.MaxRounds {
		t.Errorf("MaxRounds mismatch: %d != %d", smCfg.MaxRounds, cfg.MaxRounds)
	}
	if smCfg.MaxCostUSD != cfg.MaxCostUSD {
		t.Errorf("MaxCostUSD mismatch: %f != %f", smCfg.MaxCostUSD, cfg.MaxCostUSD)
	}
	if smCfg.MaxWallClockMinutes != cfg.MaxWallClockMinutes {
		t.Errorf("MaxWallClockMinutes mismatch: %d != %d", smCfg.MaxWallClockMinutes, cfg.MaxWallClockMinutes)
	}
	if smCfg.MaxGateCorrections != cfg.MaxGateCorrections {
		t.Errorf("MaxGateCorrections mismatch: %d != %d", smCfg.MaxGateCorrections, cfg.MaxGateCorrections)
	}
	if smCfg.MaxGateDraftRedrafts != cfg.MaxGateDraftRedrafts {
		t.Errorf("MaxGateDraftRedrafts mismatch: %d != %d", smCfg.MaxGateDraftRedrafts, cfg.MaxGateDraftRedrafts)
	}
}

// ---------------------------------------------------------------------------
// Full happy-path walkthrough
// ---------------------------------------------------------------------------

func TestStateMachine_FullHappyPath(t *testing.T) {
	ws := &CDStateJSON{State: CDInit, Round: 1}
	cfg := DefaultCDStateMachineConfig()
	sm := NewCDStateMachine(ws, cfg, nil)

	steps := []CDState{
		CDDiscovery, CDHumanGateScope, CDDrafting, CDSanitising,
		CDHumanGateDraft, CDReviewing, CDRevising, CDJudging,
		CDWriting, CDComplete,
	}
	for _, step := range steps {
		if err := sm.Transition(step); err != nil {
			t.Fatalf("transition to %s failed: %v", step, err)
		}
	}
	if !sm.IsTerminal() {
		t.Error("expected terminal state after full walkthrough")
	}
	if sm.Current() != CDComplete {
		t.Errorf("expected CD_COMPLETE, got %s", sm.Current())
	}
}

func TestStateMachine_HappyPathWithHumanGateFinal(t *testing.T) {
	ws := &CDStateJSON{State: CDInit, Round: 1}
	cfg := DefaultCDStateMachineConfig()
	sm := NewCDStateMachine(ws, cfg, nil)

	steps := []CDState{
		CDDiscovery, CDHumanGateScope, CDDrafting, CDSanitising,
		CDHumanGateDraft, CDReviewing, CDRevising, CDJudging,
	}
	for _, step := range steps {
		if err := sm.Transition(step); err != nil {
			t.Fatalf("transition to %s failed: %v", step, err)
		}
	}
	// Set critical findings to route through HUMAN_GATE_FINAL
	sm.State().HadCriticalFindings = true
	if err := sm.Transition(CDHumanGateFinal); err != nil {
		t.Fatalf("transition to CD_HUMAN_GATE_FINAL failed: %v", err)
	}
	if err := sm.Transition(CDWriting); err != nil {
		t.Fatalf("transition to CD_WRITING failed: %v", err)
	}
	if err := sm.Transition(CDComplete); err != nil {
		t.Fatalf("transition to CD_COMPLETE failed: %v", err)
	}
}

// ---------------------------------------------------------------------------
// UpdatedAt is set on transition
// ---------------------------------------------------------------------------

func TestStateMachine_UpdatedAtSet(t *testing.T) {
	sm := newTestSM(CDInit)
	sm.State().UpdatedAt = ""
	if err := sm.Transition(CDDiscovery); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sm.State().UpdatedAt == "" {
		t.Error("expected UpdatedAt to be set after transition")
	}
}
