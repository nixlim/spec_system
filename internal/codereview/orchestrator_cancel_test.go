package codereview

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Cancel tests
// ---------------------------------------------------------------------------

func TestCancel_NotStarted(t *testing.T) {
	o := &CodeReviewOrchestrator{}
	err := o.Cancel()
	if err == nil {
		t.Fatal("expected error for unstarted orchestrator")
	}
	if !strings.Contains(err.Error(), "not started") {
		t.Errorf("error = %q, want substring %q", err.Error(), "not started")
	}
	// Flag should still be set even though transition failed.
	if !o.IsCancelled() {
		t.Error("IsCancelled() = false after Cancel()")
	}
}

func TestCancel_FromReviewing(t *testing.T) {
	ws := &CodeReviewStateJSON{
		State: CRReviewing,
		Round: 1,
	}
	cfg := DefaultCRStateMachineConfig()
	sm := NewCRStateMachine(ws, cfg, nil)

	o := &CodeReviewOrchestrator{sm: sm}

	if o.IsCancelled() {
		t.Fatal("IsCancelled() = true before Cancel()")
	}

	err := o.Cancel()
	if err != nil {
		t.Fatalf("Cancel() error: %v", err)
	}

	if sm.Current() != CREscalated {
		t.Errorf("state = %s, want %s", sm.Current(), CREscalated)
	}
	if ws.EscalationReason != "operator cancelled" {
		t.Errorf("EscalationReason = %q, want %q", ws.EscalationReason, "operator cancelled")
	}
	if !o.IsCancelled() {
		t.Error("IsCancelled() = false after Cancel()")
	}
}

func TestCancel_FromFixing(t *testing.T) {
	ws := &CodeReviewStateJSON{
		State: CRFixing,
		Round: 1,
	}
	cfg := DefaultCRStateMachineConfig()
	sm := NewCRStateMachine(ws, cfg, nil)

	o := &CodeReviewOrchestrator{sm: sm}

	err := o.Cancel()
	if err != nil {
		t.Fatalf("Cancel() error: %v", err)
	}

	if sm.Current() != CREscalated {
		t.Errorf("state = %s, want %s", sm.Current(), CREscalated)
	}
	if ws.EscalationReason != "operator cancelled" {
		t.Errorf("EscalationReason = %q, want %q", ws.EscalationReason, "operator cancelled")
	}
}

func TestCancel_FromHumanGateScope(t *testing.T) {
	ws := &CodeReviewStateJSON{
		State: CRHumanGateScope,
		Round: 1,
	}
	cfg := DefaultCRStateMachineConfig()
	sm := NewCRStateMachine(ws, cfg, nil)

	o := &CodeReviewOrchestrator{sm: sm}

	err := o.Cancel()
	if err != nil {
		t.Fatalf("Cancel() error: %v", err)
	}

	if sm.Current() != CREscalated {
		t.Errorf("state = %s, want %s", sm.Current(), CREscalated)
	}
}

func TestCancel_FromHumanGateFixes(t *testing.T) {
	ws := &CodeReviewStateJSON{
		State: CRHumanGateFixes,
		Round: 1,
	}
	cfg := DefaultCRStateMachineConfig()
	sm := NewCRStateMachine(ws, cfg, nil)

	o := &CodeReviewOrchestrator{sm: sm}

	err := o.Cancel()
	if err != nil {
		t.Fatalf("Cancel() error: %v", err)
	}

	if sm.Current() != CREscalated {
		t.Errorf("state = %s, want %s", sm.Current(), CREscalated)
	}
}

func TestCancel_AlreadyComplete(t *testing.T) {
	ws := &CodeReviewStateJSON{
		State: CRComplete,
		Round: 1,
	}
	cfg := DefaultCRStateMachineConfig()
	sm := NewCRStateMachine(ws, cfg, nil)

	o := &CodeReviewOrchestrator{sm: sm}

	err := o.Cancel()
	if err != nil {
		t.Fatalf("Cancel() error: %v", err)
	}

	// Should remain in CR_COMPLETE (no-op).
	if sm.Current() != CRComplete {
		t.Errorf("state = %s, want %s", sm.Current(), CRComplete)
	}
	if !o.IsCancelled() {
		t.Error("IsCancelled() = false after Cancel()")
	}
}

func TestCancel_AlreadyEscalated(t *testing.T) {
	ws := &CodeReviewStateJSON{
		State:            CREscalated,
		Round:            1,
		EscalationReason: "cost budget exceeded",
	}
	cfg := DefaultCRStateMachineConfig()
	sm := NewCRStateMachine(ws, cfg, nil)

	o := &CodeReviewOrchestrator{sm: sm}

	err := o.Cancel()
	if err != nil {
		t.Fatalf("Cancel() error: %v", err)
	}

	// Should remain in CR_ESCALATED; original reason preserved.
	if sm.Current() != CREscalated {
		t.Errorf("state = %s, want %s", sm.Current(), CREscalated)
	}
	if ws.EscalationReason != "cost budget exceeded" {
		t.Errorf("EscalationReason = %q, want original reason preserved", ws.EscalationReason)
	}
}

func TestCancel_Idempotent(t *testing.T) {
	ws := &CodeReviewStateJSON{
		State: CRReviewing,
		Round: 1,
	}
	cfg := DefaultCRStateMachineConfig()
	sm := NewCRStateMachine(ws, cfg, nil)

	o := &CodeReviewOrchestrator{sm: sm}

	// First cancel.
	if err := o.Cancel(); err != nil {
		t.Fatalf("first Cancel() error: %v", err)
	}

	// Second cancel — should be no-op since already terminal.
	if err := o.Cancel(); err != nil {
		t.Fatalf("second Cancel() error: %v", err)
	}

	if sm.Current() != CREscalated {
		t.Errorf("state = %s, want %s", sm.Current(), CREscalated)
	}
}

func TestIsCancelled_DefaultFalse(t *testing.T) {
	o := &CodeReviewOrchestrator{}
	if o.IsCancelled() {
		t.Error("IsCancelled() = true on fresh orchestrator")
	}
}
