package codereview

import (
	"testing"

	"github.com/foundry-zero/adversarial-spec-system/internal/specworkflow"
)

// ---------------------------------------------------------------------------
// CREventEmitter tests
// ---------------------------------------------------------------------------

func TestCREventEmitter_EmitWorkflowStatus(t *testing.T) {
	ch := specworkflow.NewChannelEmitter(10)
	emitter := NewCREventEmitter(ch, "test-feature")

	emitter.EmitWorkflowStatus("CR_REVIEWING", 2, 12.50, 300.0)

	select {
	case env := <-ch.Events():
		if env.Event != CREventWorkflowStatus {
			t.Errorf("Event = %q, want %q", env.Event, CREventWorkflowStatus)
		}
		if env.FeatureName != "test-feature" {
			t.Errorf("FeatureName = %q, want %q", env.FeatureName, "test-feature")
		}
		data, ok := env.Data.(CRWorkflowStatusEvent)
		if !ok {
			t.Fatalf("Data type = %T, want CRWorkflowStatusEvent", env.Data)
		}
		if data.State != "CR_REVIEWING" {
			t.Errorf("State = %q, want %q", data.State, "CR_REVIEWING")
		}
		if data.Round != 2 {
			t.Errorf("Round = %d, want 2", data.Round)
		}
		if data.CostUSD != 12.50 {
			t.Errorf("CostUSD = %f, want 12.50", data.CostUSD)
		}
		if data.WallClockSeconds != 300.0 {
			t.Errorf("WallClockSeconds = %f, want 300.0", data.WallClockSeconds)
		}
		if data.Timestamp == "" {
			t.Error("Timestamp should be set automatically")
		}
	default:
		t.Fatal("expected event on channel")
	}
}

func TestCREventEmitter_EmitAgentDispatch(t *testing.T) {
	ch := specworkflow.NewChannelEmitter(10)
	emitter := NewCREventEmitter(ch, "test-feature")

	emitter.EmitAgentDispatch("reviewer-security-claude", "security", "claude", 1)

	select {
	case env := <-ch.Events():
		if env.Event != CREventAgentDispatch {
			t.Errorf("Event = %q, want %q", env.Event, CREventAgentDispatch)
		}
		if env.FeatureName != "test-feature" {
			t.Errorf("FeatureName = %q, want %q", env.FeatureName, "test-feature")
		}
		data, ok := env.Data.(CRAgentDispatchEvent)
		if !ok {
			t.Fatalf("Data type = %T, want CRAgentDispatchEvent", env.Data)
		}
		if data.Agent != "reviewer-security-claude" {
			t.Errorf("Agent = %q, want %q", data.Agent, "reviewer-security-claude")
		}
		if data.Lens != "security" {
			t.Errorf("Lens = %q, want %q", data.Lens, "security")
		}
		if data.Provider != "claude" {
			t.Errorf("Provider = %q, want %q", data.Provider, "claude")
		}
		if data.Round != 1 {
			t.Errorf("Round = %d, want 1", data.Round)
		}
		if data.Timestamp == "" {
			t.Error("Timestamp should be set automatically")
		}
	default:
		t.Fatal("expected event on channel")
	}
}

func TestCREventEmitter_EmitAgentComplete(t *testing.T) {
	ch := specworkflow.NewChannelEmitter(10)
	emitter := NewCREventEmitter(ch, "test-feature")

	emitter.EmitAgentComplete("reviewer-correctness-codex", 1, true, 5000, 1.25)

	select {
	case env := <-ch.Events():
		if env.Event != CREventAgentComplete {
			t.Errorf("Event = %q, want %q", env.Event, CREventAgentComplete)
		}
		if env.FeatureName != "test-feature" {
			t.Errorf("FeatureName = %q, want %q", env.FeatureName, "test-feature")
		}
		data, ok := env.Data.(CRAgentCompleteEvent)
		if !ok {
			t.Fatalf("Data type = %T, want CRAgentCompleteEvent", env.Data)
		}
		if data.Agent != "reviewer-correctness-codex" {
			t.Errorf("Agent = %q, want %q", data.Agent, "reviewer-correctness-codex")
		}
		if !data.Success {
			t.Error("Success = false, want true")
		}
		if data.DurationMS != 5000 {
			t.Errorf("DurationMS = %d, want 5000", data.DurationMS)
		}
		if data.CostUSD != 1.25 {
			t.Errorf("CostUSD = %f, want 1.25", data.CostUSD)
		}
		if data.Round != 1 {
			t.Errorf("Round = %d, want 1", data.Round)
		}
		if data.Timestamp == "" {
			t.Error("Timestamp should be set automatically")
		}
	default:
		t.Fatal("expected event on channel")
	}
}

func TestCREventEmitter_EmitAgentComplete_Failure(t *testing.T) {
	ch := specworkflow.NewChannelEmitter(10)
	emitter := NewCREventEmitter(ch, "test-feature")

	emitter.EmitAgentComplete("fix-agent", 2, false, 10000, 3.50)

	select {
	case env := <-ch.Events():
		data, ok := env.Data.(CRAgentCompleteEvent)
		if !ok {
			t.Fatalf("Data type = %T, want CRAgentCompleteEvent", env.Data)
		}
		if data.Success {
			t.Error("Success = true, want false")
		}
		if data.DurationMS != 10000 {
			t.Errorf("DurationMS = %d, want 10000", data.DurationMS)
		}
	default:
		t.Fatal("expected event on channel")
	}
}

func TestCREventEmitter_EmitGateRequest(t *testing.T) {
	ch := specworkflow.NewChannelEmitter(10)
	emitter := NewCREventEmitter(ch, "test-feature")

	emitter.EmitGateRequest("scope", []string{"confirm", "cancel"})

	select {
	case env := <-ch.Events():
		if env.Event != CREventGateRequest {
			t.Errorf("Event = %q, want %q", env.Event, CREventGateRequest)
		}
		if env.FeatureName != "test-feature" {
			t.Errorf("FeatureName = %q, want %q", env.FeatureName, "test-feature")
		}
		data, ok := env.Data.(CRGateRequestEvent)
		if !ok {
			t.Fatalf("Data type = %T, want CRGateRequestEvent", env.Data)
		}
		if data.GateType != "scope" {
			t.Errorf("GateType = %q, want %q", data.GateType, "scope")
		}
		if len(data.Actions) != 2 {
			t.Fatalf("Actions length = %d, want 2", len(data.Actions))
		}
		if data.Actions[0] != "confirm" || data.Actions[1] != "cancel" {
			t.Errorf("Actions = %v, want [confirm cancel]", data.Actions)
		}
		if data.Timestamp == "" {
			t.Error("Timestamp should be set automatically")
		}
	default:
		t.Fatal("expected event on channel")
	}
}

func TestCREventEmitter_EmitGateRequest_Fixes(t *testing.T) {
	ch := specworkflow.NewChannelEmitter(10)
	emitter := NewCREventEmitter(ch, "fix-review")

	emitter.EmitGateRequest("fixes", []string{"re-review", "accept", "escalate"})

	select {
	case env := <-ch.Events():
		data, ok := env.Data.(CRGateRequestEvent)
		if !ok {
			t.Fatalf("Data type = %T, want CRGateRequestEvent", env.Data)
		}
		if data.GateType != "fixes" {
			t.Errorf("GateType = %q, want %q", data.GateType, "fixes")
		}
		if len(data.Actions) != 3 {
			t.Fatalf("Actions length = %d, want 3", len(data.Actions))
		}
	default:
		t.Fatal("expected event on channel")
	}
}

func TestCREventEmitter_NilInner(t *testing.T) {
	emitter := NewCREventEmitter(nil, "test-feature")

	// Should not panic with nil inner emitter.
	emitter.EmitWorkflowStatus("CR_INIT", 1, 0, 0)
	emitter.EmitAgentDispatch("agent", "lens", "provider", 1)
	emitter.EmitAgentComplete("agent", 1, true, 100, 0.1)
	emitter.EmitGateRequest("scope", []string{"confirm"})
}

func TestCREventEmitter_FeatureNameAutoSet(t *testing.T) {
	ch := specworkflow.NewChannelEmitter(10)
	emitter := NewCREventEmitter(ch, "auto-feature")

	emitter.EmitWorkflowStatus("CR_INIT", 1, 0, 0)

	select {
	case env := <-ch.Events():
		if env.FeatureName != "auto-feature" {
			t.Errorf("FeatureName = %q, want %q", env.FeatureName, "auto-feature")
		}
	default:
		t.Fatal("expected event on channel")
	}
}

func TestCREventEmitter_MultipleEvents(t *testing.T) {
	ch := specworkflow.NewChannelEmitter(10)
	emitter := NewCREventEmitter(ch, "multi-test")

	emitter.EmitWorkflowStatus("CR_REVIEWING", 1, 0, 0)
	emitter.EmitAgentDispatch("reviewer-security-claude", "security", "claude", 1)
	emitter.EmitAgentComplete("reviewer-security-claude", 1, true, 2000, 0.5)

	// Verify all three events were emitted.
	events := make([]specworkflow.EventEnvelope, 0, 3)
	for i := 0; i < 3; i++ {
		select {
		case env := <-ch.Events():
			events = append(events, env)
		default:
			t.Fatalf("expected 3 events, got %d", len(events))
		}
	}

	if events[0].Event != CREventWorkflowStatus {
		t.Errorf("events[0].Event = %q, want %q", events[0].Event, CREventWorkflowStatus)
	}
	if events[1].Event != CREventAgentDispatch {
		t.Errorf("events[1].Event = %q, want %q", events[1].Event, CREventAgentDispatch)
	}
	if events[2].Event != CREventAgentComplete {
		t.Errorf("events[2].Event = %q, want %q", events[2].Event, CREventAgentComplete)
	}
}
