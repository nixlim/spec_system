package specworkflow

import "testing"

func newGate1TestSetup(correctionCount int) (*Gate1Handler, *ChannelEmitter) {
	state := &WorkflowStateJSON{
		State:                StateHumanGate1,
		Gate1CorrectionCount: correctionCount,
	}
	emitter := NewChannelEmitter(16)
	handler := NewGate1Handler(state, emitter, 3)
	return handler, emitter
}

func TestGate1HandleConfirm_TransitionsToDrafting(t *testing.T) {
	handler, _ := newGate1TestSetup(0)

	next, err := handler.HandleConfirm()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if next != StateDrafting {
		t.Errorf("expected StateDrafting, got %s", next)
	}
}

func TestGate1HandleCorrect_IncrementsCountAndReturnsDiscovery(t *testing.T) {
	handler, _ := newGate1TestSetup(0)

	corrections := map[string]string{"scope": "expanded"}
	next, err := handler.HandleCorrect(corrections)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if next != StateDiscovery {
		t.Errorf("expected StateDiscovery, got %s", next)
	}
	if handler.state.Gate1CorrectionCount != 1 {
		t.Errorf("expected gate1_correction_count=1, got %d", handler.state.Gate1CorrectionCount)
	}
}

func TestGate1HandleCorrect_BlockedAtLimit(t *testing.T) {
	handler, _ := newGate1TestSetup(3) // already at limit of 3

	corrections := map[string]string{"scope": "expanded"}
	_, err := handler.HandleCorrect(corrections)
	if err == nil {
		t.Fatal("expected error when at correction limit, got nil")
	}
}

func TestGate1HandleCancel_ReturnsEscalated(t *testing.T) {
	handler, _ := newGate1TestSetup(0)

	next, err := handler.HandleCancel()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if next != StateEscalated {
		t.Errorf("expected StateEscalated, got %s", next)
	}
}

func TestGate1EnterGate_EmitsGateRequestEvent(t *testing.T) {
	handler, emitter := newGate1TestSetup(0)

	discovery := &DiscoveryOutput{
		SchemaVersion:    "1.0",
		Agent:            "discovery",
		ProblemStatement: "test problem",
		Actors:           []Actor{{Name: "User", Type: "human", Description: "test user"}},
		Scope:            Scope{InScope: []string{"feature X"}},
	}

	if err := handler.EnterGate(discovery); err != nil {
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
		if payload.GateType != "requirements_confirmation" {
			t.Errorf("expected gate_type %q, got %q", "requirements_confirmation", payload.GateType)
		}
	default:
		t.Fatal("no event emitted")
	}
}
