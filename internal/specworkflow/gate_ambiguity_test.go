package specworkflow

import "testing"

func newGate2TestSetup(redraftCount int) (*Gate2Handler, *ChannelEmitter) {
	state := &WorkflowStateJSON{
		State:             StateHumanGate2,
		Gate2RedraftCount: redraftCount,
	}
	emitter := NewChannelEmitter(16)
	handler := NewGate2Handler(state, emitter, 1)
	return handler, emitter
}

func TestGate2AllAcceptDefer_ReturnsReviewing(t *testing.T) {
	handler, _ := newGate2TestSetup(0)

	resolutions := []AmbiguityResolution{
		{WarningID: "AMB-W-001", Action: "accept"},
		{WarningID: "AMB-W-002", Action: "defer"},
	}

	needsRedraft, next, err := handler.HandleResolutions(resolutions)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if needsRedraft {
		t.Error("expected needsRedraft=false for all accept/defer")
	}
	if next != StateReviewing {
		t.Errorf("expected StateReviewing, got %s", next)
	}
}

func TestGate2WithAnswers_ReturnsDraftingAndIncrementsCount(t *testing.T) {
	handler, _ := newGate2TestSetup(0)

	resolutions := []AmbiguityResolution{
		{WarningID: "AMB-W-001", Action: "accept"},
		{WarningID: "AMB-W-002", Action: "answer", Answer: "The timeout is 30s"},
	}

	needsRedraft, next, err := handler.HandleResolutions(resolutions)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !needsRedraft {
		t.Error("expected needsRedraft=true when an answer is present")
	}
	if next != StateDrafting {
		t.Errorf("expected StateDrafting, got %s", next)
	}
	if handler.state.Gate2RedraftCount != 1 {
		t.Errorf("expected gate2_redraft_count=1, got %d", handler.state.Gate2RedraftCount)
	}
}

func TestGate2RedraftBlockedAfterFirstUse(t *testing.T) {
	handler, _ := newGate2TestSetup(1) // already used the one allowed redraft

	resolutions := []AmbiguityResolution{
		{WarningID: "AMB-W-001", Action: "answer", Answer: "new answer"},
	}

	_, _, err := handler.HandleResolutions(resolutions)
	if err == nil {
		t.Fatal("expected error when redraft limit reached, got nil")
	}
}

func TestGate2AnswerDisabledAfterRedraft(t *testing.T) {
	handler, _ := newGate2TestSetup(1)

	if !handler.IsAnswerDisabled() {
		t.Error("expected IsAnswerDisabled=true after redraft count reaches limit")
	}
}

func TestGate2AnswerEnabledBeforeRedraft(t *testing.T) {
	handler, _ := newGate2TestSetup(0)

	if handler.IsAnswerDisabled() {
		t.Error("expected IsAnswerDisabled=false before any redraft")
	}
}

func TestGate2EnterGate_EmitsGateRequestEvent(t *testing.T) {
	handler, emitter := newGate2TestSetup(0)

	drafter := &DrafterOutput{
		SchemaVersion: "1.0",
		Agent:         "drafter",
		SpecFile:      "/workspace/spec-v0.md",
		HoldoutFile:   "/workspace/holdouts.md",
		AmbiguityWarnings: []AmbiguityWarning{
			{
				ID:              "AMB-W-001",
				Section:         "Requirements",
				Ambiguity:       "Timeout undefined",
				AgentAssumption: "30s default",
				QuestionForUser: "What should the timeout be?",
			},
		},
	}

	if err := handler.EnterGate(drafter); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	select {
	case event := <-emitter.Events():
		if event.Event != EventGateRequest {
			t.Errorf("expected event type %q, got %q", EventGateRequest, event.Event)
		}
		payload, ok := event.Data.(GateRequestEvent)
		if !ok {
			t.Fatalf("expected GateRequestEvent payload, got %T", event.Data)
		}
		if payload.GateType != "ambiguity_resolution" {
			t.Errorf("expected gate_type %q, got %q", "ambiguity_resolution", payload.GateType)
		}
	default:
		t.Fatal("no event emitted")
	}
}
