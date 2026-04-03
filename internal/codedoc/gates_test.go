package codedoc

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// ScopeGateHandler tests
// ---------------------------------------------------------------------------

func TestGates_ScopeConfirm(t *testing.T) {
	ws := &CDStateJSON{State: CDHumanGateScope}
	h := NewScopeGateHandler(ws, 3)
	next, err := h.HandleConfirm()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if next != CDDrafting {
		t.Errorf("expected CDDrafting, got %s", next)
	}
}

func TestGates_ScopeCorrect(t *testing.T) {
	ws := &CDStateJSON{State: CDHumanGateScope}
	h := NewScopeGateHandler(ws, 3)

	// First correction
	next, err := h.HandleCorrect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if next != CDDiscovery {
		t.Errorf("expected CDDiscovery, got %s", next)
	}
	if ws.GateScopeCorrectionCount != 1 {
		t.Errorf("expected correction count 1, got %d", ws.GateScopeCorrectionCount)
	}

	// Second correction
	next, err = h.HandleCorrect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if next != CDDiscovery {
		t.Errorf("expected CDDiscovery, got %s", next)
	}

	// Third correction
	next, err = h.HandleCorrect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if next != CDDiscovery {
		t.Errorf("expected CDDiscovery, got %s", next)
	}
	if ws.GateScopeCorrectionCount != 3 {
		t.Errorf("expected correction count 3, got %d", ws.GateScopeCorrectionCount)
	}
}

func TestGates_ScopeCorrectLimit(t *testing.T) {
	ws := &CDStateJSON{
		State:                    CDHumanGateScope,
		GateScopeCorrectionCount: 3, // already at max
	}
	h := NewScopeGateHandler(ws, 3)
	next, err := h.HandleCorrect()
	if err == nil {
		t.Error("expected error when correction limit reached")
	}
	if next != CDEscalated {
		t.Errorf("expected CDEscalated on limit, got %s", next)
	}
	if !strings.Contains(err.Error(), "scope gate correction limit") {
		t.Errorf("error should mention 'scope gate correction limit', got: %v", err)
	}
}

func TestGates_ScopeCancel(t *testing.T) {
	ws := &CDStateJSON{State: CDHumanGateScope}
	h := NewScopeGateHandler(ws, 3)
	next, err := h.HandleCancel()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if next != CDEscalated {
		t.Errorf("expected CDEscalated, got %s", next)
	}
}

// ---------------------------------------------------------------------------
// DraftGateHandler tests
// ---------------------------------------------------------------------------

func TestGates_DraftApprove(t *testing.T) {
	ws := &CDStateJSON{State: CDHumanGateDraft}
	h := NewDraftGateHandler(ws, 2)
	next, err := h.HandleApprove()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if next != CDReviewing {
		t.Errorf("expected CDReviewing, got %s", next)
	}
}

func TestGates_DraftRedraft(t *testing.T) {
	ws := &CDStateJSON{State: CDHumanGateDraft}
	h := NewDraftGateHandler(ws, 2)

	// First redraft
	next, err := h.HandleRedraft()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if next != CDDrafting {
		t.Errorf("expected CDDrafting, got %s", next)
	}
	if ws.GateDraftRedraftCount != 1 {
		t.Errorf("expected redraft count 1, got %d", ws.GateDraftRedraftCount)
	}

	// Second redraft
	next, err = h.HandleRedraft()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if next != CDDrafting {
		t.Errorf("expected CDDrafting, got %s", next)
	}
	if ws.GateDraftRedraftCount != 2 {
		t.Errorf("expected redraft count 2, got %d", ws.GateDraftRedraftCount)
	}
}

func TestGates_DraftRedraftLimit(t *testing.T) {
	ws := &CDStateJSON{
		State:                 CDHumanGateDraft,
		GateDraftRedraftCount: 2, // already at max
	}
	h := NewDraftGateHandler(ws, 2)
	next, err := h.HandleRedraft()
	if err == nil {
		t.Error("expected error when redraft limit reached")
	}
	if next != CDEscalated {
		t.Errorf("expected CDEscalated on limit, got %s", next)
	}
	if !strings.Contains(err.Error(), "draft gate redraft limit") {
		t.Errorf("error should mention 'draft gate redraft limit', got: %v", err)
	}
}

func TestGates_DraftRedraftDisabled(t *testing.T) {
	ws := &CDStateJSON{GateDraftRedraftCount: 2}
	h := NewDraftGateHandler(ws, 2)
	if !h.IsRedraftDisabled() {
		t.Error("expected redraft to be disabled at limit")
	}

	ws2 := &CDStateJSON{GateDraftRedraftCount: 1}
	h2 := NewDraftGateHandler(ws2, 2)
	if h2.IsRedraftDisabled() {
		t.Error("expected redraft to be enabled below limit")
	}
}

func TestGates_DraftCancel(t *testing.T) {
	ws := &CDStateJSON{State: CDHumanGateDraft}
	h := NewDraftGateHandler(ws, 2)
	next, err := h.HandleCancel()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if next != CDEscalated {
		t.Errorf("expected CDEscalated, got %s", next)
	}
}

func TestGates_DraftForceApproveOrCancelAfterLimit(t *testing.T) {
	// After max_gate_draft_redrafts exhausted, only approve or cancel should work
	ws := &CDStateJSON{
		State:                 CDHumanGateDraft,
		GateDraftRedraftCount: 2,
	}
	h := NewDraftGateHandler(ws, 2)

	// Redraft should fail
	_, err := h.HandleRedraft()
	if err == nil {
		t.Error("expected redraft to fail at limit")
	}

	// Approve should still work
	next, err := h.HandleApprove()
	if err != nil {
		t.Fatalf("approve should still work: %v", err)
	}
	if next != CDReviewing {
		t.Errorf("expected CDReviewing, got %s", next)
	}
}

// ---------------------------------------------------------------------------
// FinalGateHandler tests
// ---------------------------------------------------------------------------

func TestGates_FinalAccept(t *testing.T) {
	ws := &CDStateJSON{State: CDHumanGateFinal}
	h := NewFinalGateHandler(ws)
	next, err := h.HandleAccept()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if next != CDWriting {
		t.Errorf("expected CDWriting, got %s", next)
	}
}

func TestGates_FinalReject(t *testing.T) {
	ws := &CDStateJSON{State: CDHumanGateFinal}
	h := NewFinalGateHandler(ws)
	next, err := h.HandleReject()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if next != CDEscalated {
		t.Errorf("expected CDEscalated, got %s", next)
	}
}

func TestGates_FinalRequestReview(t *testing.T) {
	ws := &CDStateJSON{State: CDHumanGateFinal, Round: 2}
	h := NewFinalGateHandler(ws)
	next, err := h.HandleRequestReview()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if next != CDReviewing {
		t.Errorf("expected CDReviewing, got %s", next)
	}
	if ws.Round != 3 {
		t.Errorf("expected round to increment to 3, got %d", ws.Round)
	}
}

// ---------------------------------------------------------------------------
// Payload type tests
// ---------------------------------------------------------------------------

func TestGates_ScopeGatePayloadFields(t *testing.T) {
	p := ScopeGatePayload{
		Modules:          []ModuleInfo{{Path: "internal/api", Name: "api"}},
		CompletionStatus: CompletionStatus{Status: "complete", Reason: "done"},
		DependencyGraph:  DependencyGraph{Edges: []DependencyEdge{{From: "a", To: "b"}}},
		ExistingDocs:     []ExistingDoc{{Path: "README.md"}},
		SuggestedScope:   SuggestedScope{Include: []string{"internal/"}, Exclude: []string{"vendor/"}},
		MergeConflicts:   []MergeConflict{{ModulePath: "internal/api", Field: "description"}},
		DualProvider:     true,
	}
	if len(p.Modules) != 1 {
		t.Errorf("expected 1 module, got %d", len(p.Modules))
	}
	if len(p.MergeConflicts) != 1 {
		t.Errorf("expected 1 merge conflict, got %d", len(p.MergeConflicts))
	}
}

func TestGates_DraftGatePayloadFields(t *testing.T) {
	p := DraftGatePayload{
		GeneratedFiles: []string{"docs/as-implemented-report.md"},
		Summary:        StructuralSummary{DiagramCount: 3, AuditFindingCount: 19},
		RedactionLog:   &SanitisationReport{SecretsFound: 1, SecretsRedacted: 1},
		RedraftCount:   1,
		MaxRedrafts:    2,
		RedraftDisabled: false,
	}
	if len(p.GeneratedFiles) != 1 {
		t.Errorf("expected 1 generated file, got %d", len(p.GeneratedFiles))
	}
	if p.RedactionLog.SecretsFound != 1 {
		t.Errorf("expected 1 secret found, got %d", p.RedactionLog.SecretsFound)
	}
}

func TestGates_FinalGatePayloadFields(t *testing.T) {
	p := FinalGatePayload{
		UnresolvedFindings: []UnresolvedFinding{
			{ID: "ACC-001", Description: "wrong module desc", Severity: SeverityCritical},
		},
		TotalUnresolved: 1,
	}
	if p.TotalUnresolved != 1 {
		t.Errorf("expected 1 unresolved, got %d", p.TotalUnresolved)
	}
}

// ---------------------------------------------------------------------------
// Integration: gate handlers with state machine
// ---------------------------------------------------------------------------

func TestGates_ScopeAcceptAdvancesToDrafting(t *testing.T) {
	ws := &CDStateJSON{State: CDHumanGateScope, Round: 1}
	cfg := DefaultCDStateMachineConfig()
	sm := NewCDStateMachine(ws, cfg, nil)
	h := NewScopeGateHandler(ws, cfg.MaxGateCorrections)

	next, err := h.HandleConfirm()
	if err != nil {
		t.Fatalf("HandleConfirm error: %v", err)
	}
	if err := sm.Transition(next); err != nil {
		t.Fatalf("transition to %s failed: %v", next, err)
	}
	if sm.Current() != CDDrafting {
		t.Errorf("expected CDDrafting, got %s", sm.Current())
	}
}

func TestGates_ScopeCorrectReentersDiscovery(t *testing.T) {
	ws := &CDStateJSON{State: CDHumanGateScope, Round: 1}
	cfg := DefaultCDStateMachineConfig()
	sm := NewCDStateMachine(ws, cfg, nil)
	h := NewScopeGateHandler(ws, cfg.MaxGateCorrections)

	next, err := h.HandleCorrect()
	if err != nil {
		t.Fatalf("HandleCorrect error: %v", err)
	}
	if err := sm.Transition(next); err != nil {
		t.Fatalf("transition to %s failed: %v", next, err)
	}
	if sm.Current() != CDDiscovery {
		t.Errorf("expected CDDiscovery, got %s", sm.Current())
	}
}

func TestGates_ScopeCancelEscalates(t *testing.T) {
	ws := &CDStateJSON{State: CDHumanGateScope, Round: 1}
	cfg := DefaultCDStateMachineConfig()
	sm := NewCDStateMachine(ws, cfg, nil)
	h := NewScopeGateHandler(ws, cfg.MaxGateCorrections)

	next, err := h.HandleCancel()
	if err != nil {
		t.Fatalf("HandleCancel error: %v", err)
	}
	if err := sm.Transition(next); err != nil {
		t.Fatalf("transition to %s failed: %v", next, err)
	}
	if sm.Current() != CDEscalated {
		t.Errorf("expected CDEscalated, got %s", sm.Current())
	}
}

func TestGates_DraftApproveAdvancesToReviewing(t *testing.T) {
	ws := &CDStateJSON{State: CDHumanGateDraft, Round: 1}
	cfg := DefaultCDStateMachineConfig()
	sm := NewCDStateMachine(ws, cfg, nil)
	h := NewDraftGateHandler(ws, cfg.MaxGateDraftRedrafts)

	next, err := h.HandleApprove()
	if err != nil {
		t.Fatalf("HandleApprove error: %v", err)
	}
	if err := sm.Transition(next); err != nil {
		t.Fatalf("transition to %s failed: %v", next, err)
	}
	if sm.Current() != CDReviewing {
		t.Errorf("expected CDReviewing, got %s", sm.Current())
	}
}

func TestGates_DraftRedraftReentersDrafting(t *testing.T) {
	ws := &CDStateJSON{State: CDHumanGateDraft, Round: 1}
	cfg := DefaultCDStateMachineConfig()
	sm := NewCDStateMachine(ws, cfg, nil)
	h := NewDraftGateHandler(ws, cfg.MaxGateDraftRedrafts)

	next, err := h.HandleRedraft()
	if err != nil {
		t.Fatalf("HandleRedraft error: %v", err)
	}
	if err := sm.Transition(next); err != nil {
		t.Fatalf("transition to %s failed: %v", next, err)
	}
	if sm.Current() != CDDrafting {
		t.Errorf("expected CDDrafting, got %s", sm.Current())
	}
}

func TestGates_FinalAcceptAdvancesToWriting(t *testing.T) {
	ws := &CDStateJSON{State: CDHumanGateFinal, Round: 1, HadCriticalFindings: true}
	cfg := DefaultCDStateMachineConfig()
	sm := NewCDStateMachine(ws, cfg, nil)
	h := NewFinalGateHandler(ws)

	next, err := h.HandleAccept()
	if err != nil {
		t.Fatalf("HandleAccept error: %v", err)
	}
	// Final gate handler returns CDWriting, but the state machine
	// has the JudgingToWriting guard. Since we're at HUMAN_GATE_FINAL (not JUDGING),
	// the guard doesn't apply.
	if err := sm.Transition(next); err != nil {
		t.Fatalf("transition to %s failed: %v", next, err)
	}
	if sm.Current() != CDWriting {
		t.Errorf("expected CDWriting, got %s", sm.Current())
	}
}

func TestGates_FinalRejectEscalates(t *testing.T) {
	ws := &CDStateJSON{State: CDHumanGateFinal, Round: 1, HadCriticalFindings: true}
	cfg := DefaultCDStateMachineConfig()
	sm := NewCDStateMachine(ws, cfg, nil)
	h := NewFinalGateHandler(ws)

	next, err := h.HandleReject()
	if err != nil {
		t.Fatalf("HandleReject error: %v", err)
	}
	if err := sm.Transition(next); err != nil {
		t.Fatalf("transition to %s failed: %v", next, err)
	}
	if sm.Current() != CDEscalated {
		t.Errorf("expected CDEscalated, got %s", sm.Current())
	}
}

func TestGates_FinalRequestReviewReentersReviewing(t *testing.T) {
	ws := &CDStateJSON{State: CDHumanGateFinal, Round: 1, HadCriticalFindings: true}

	// Validate the handler logic (round increment and target state).
	h := NewFinalGateHandler(ws)
	next, err := h.HandleRequestReview()
	if err != nil {
		t.Fatalf("HandleRequestReview error: %v", err)
	}
	if next != CDReviewing {
		t.Errorf("expected CDReviewing, got %s", next)
	}
	if ws.Round != 2 {
		t.Errorf("expected round 2, got %d", ws.Round)
	}

	// Validate the state machine allows HUMAN_GATE_FINAL -> REVIEWING.
	ws2 := &CDStateJSON{State: CDHumanGateFinal, Round: 1, HadCriticalFindings: true}
	cfg := DefaultCDStateMachineConfig()
	sm := NewCDStateMachine(ws2, cfg, nil)
	if err := sm.Transition(CDReviewing); err != nil {
		t.Fatalf("state machine should allow HUMAN_GATE_FINAL -> REVIEWING: %v", err)
	}
}
