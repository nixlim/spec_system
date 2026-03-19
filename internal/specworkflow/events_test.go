package specworkflow

import (
	"encoding/json"
	"testing"
)

// ---------------------------------------------------------------------------
// Event constructor tests
// ---------------------------------------------------------------------------

func TestNewSpecVersionEvent(t *testing.T) {
	env := NewSpecVersionEvent(3, 2, "/tmp/spec.md")
	if env.Event != EventSpecVersion {
		t.Errorf("Event = %q, want %q", env.Event, EventSpecVersion)
	}
	data, ok := env.Data.(SpecVersionEvent)
	if !ok {
		t.Fatalf("Data type = %T, want SpecVersionEvent", env.Data)
	}
	if data.Version != 3 {
		t.Errorf("Version = %d, want 3", data.Version)
	}
	if data.Round != 2 {
		t.Errorf("Round = %d, want 2", data.Round)
	}
	if data.FilePath != "/tmp/spec.md" {
		t.Errorf("FilePath = %q, want %q", data.FilePath, "/tmp/spec.md")
	}
	if data.Timestamp == "" {
		t.Error("Timestamp should be set automatically")
	}
}

func TestNewIssueUpdateEvent(t *testing.T) {
	env := NewIssueUpdateEvent("ISS-1", "open", 1, "CRITICAL", "security")
	if env.Event != EventIssueUpdate {
		t.Errorf("Event = %q, want %q", env.Event, EventIssueUpdate)
	}
	data, ok := env.Data.(IssueUpdateEvent)
	if !ok {
		t.Fatalf("Data type = %T, want IssueUpdateEvent", env.Data)
	}
	if data.IssueID != "ISS-1" {
		t.Errorf("IssueID = %q, want %q", data.IssueID, "ISS-1")
	}
	if data.Status != "open" {
		t.Errorf("Status = %q, want %q", data.Status, "open")
	}
	if data.Round != 1 {
		t.Errorf("Round = %d, want 1", data.Round)
	}
	if data.Severity != "CRITICAL" {
		t.Errorf("Severity = %q, want %q", data.Severity, "CRITICAL")
	}
	if data.Lens != "security" {
		t.Errorf("Lens = %q, want %q", data.Lens, "security")
	}
}

func TestNewConvergenceUpdateEvent(t *testing.T) {
	env := NewConvergenceUpdateEvent(2, "REVISE", 1, 3, 5, true, "improving")
	if env.Event != EventConvergenceUpdate {
		t.Errorf("Event = %q, want %q", env.Event, EventConvergenceUpdate)
	}
	data, ok := env.Data.(ConvergenceUpdateEvent)
	if !ok {
		t.Fatalf("Data type = %T, want ConvergenceUpdateEvent", env.Data)
	}
	if data.Round != 2 {
		t.Errorf("Round = %d, want 2", data.Round)
	}
	if data.Verdict != "REVISE" {
		t.Errorf("Verdict = %q, want %q", data.Verdict, "REVISE")
	}
	if data.OpenCritical != 1 {
		t.Errorf("OpenCritical = %d, want 1", data.OpenCritical)
	}
	if data.OpenMajor != 3 {
		t.Errorf("OpenMajor = %d, want 3", data.OpenMajor)
	}
	if data.OpenMinor != 5 {
		t.Errorf("OpenMinor = %d, want 5", data.OpenMinor)
	}
	if !data.Progress {
		t.Error("Progress = false, want true")
	}
	if data.Rationale != "improving" {
		t.Errorf("Rationale = %q, want %q", data.Rationale, "improving")
	}
}

func TestNewGateRequestEvent(t *testing.T) {
	payload := map[string]string{"question": "approve?"}
	env := NewGateRequestEvent("requirements_confirmation", "task-42", payload)
	if env.Event != EventGateRequest {
		t.Errorf("Event = %q, want %q", env.Event, EventGateRequest)
	}
	data, ok := env.Data.(GateRequestEvent)
	if !ok {
		t.Fatalf("Data type = %T, want GateRequestEvent", env.Data)
	}
	if data.GateType != "requirements_confirmation" {
		t.Errorf("GateType = %q, want %q", data.GateType, "requirements_confirmation")
	}
	if data.TaskID != "task-42" {
		t.Errorf("TaskID = %q, want %q", data.TaskID, "task-42")
	}
}

func TestNewCircuitBreakerEvent(t *testing.T) {
	env := NewCircuitBreakerEvent("max_rounds", 5, 4)
	if env.Event != EventCircuitBreaker {
		t.Errorf("Event = %q, want %q", env.Event, EventCircuitBreaker)
	}
	data, ok := env.Data.(CircuitBreakerEvent)
	if !ok {
		t.Fatalf("Data type = %T, want CircuitBreakerEvent", env.Data)
	}
	if data.Breaker != "max_rounds" {
		t.Errorf("Breaker = %q, want %q", data.Breaker, "max_rounds")
	}
}

func TestNewAgentErrorEvent(t *testing.T) {
	env := NewAgentErrorEvent("reviewer", "timeout", 2, 3)
	if env.Event != EventAgentError {
		t.Errorf("Event = %q, want %q", env.Event, EventAgentError)
	}
	data, ok := env.Data.(AgentErrorEvent)
	if !ok {
		t.Fatalf("Data type = %T, want AgentErrorEvent", env.Data)
	}
	if data.Agent != "reviewer" {
		t.Errorf("Agent = %q, want %q", data.Agent, "reviewer")
	}
	if data.ErrorType != "timeout" {
		t.Errorf("ErrorType = %q, want %q", data.ErrorType, "timeout")
	}
	if data.RetryCount != 2 {
		t.Errorf("RetryCount = %d, want 2", data.RetryCount)
	}
	if data.MaxRetries != 3 {
		t.Errorf("MaxRetries = %d, want 3", data.MaxRetries)
	}
}

// ---------------------------------------------------------------------------
// JSON serialization tests
// ---------------------------------------------------------------------------

func TestEventEnvelope_AllEventsSerializeToValidJSON(t *testing.T) {
	envelopes := []EventEnvelope{
		NewSpecVersionEvent(1, 1, "/spec.md"),
		NewIssueUpdateEvent("ISS-1", "open", 1, "MAJOR", "correctness"),
		NewConvergenceUpdateEvent(1, "PASS", 0, 0, 2, true, "all clear"),
		NewGateRequestEvent("ambiguity_resolution", "t-1", map[string]string{"q": "which?"}),
		NewCircuitBreakerEvent("cost_usd", 9.5, 10.0),
		NewAgentErrorEvent("drafter", "rate_limit", 1, 3),
	}

	for _, env := range envelopes {
		data, err := json.Marshal(env)
		if err != nil {
			t.Errorf("Marshal %q event: %v", env.Event, err)
			continue
		}

		// Verify it round-trips to a map with "event" and "data" keys.
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Errorf("Unmarshal %q to map: %v", env.Event, err)
			continue
		}
		if _, ok := raw["event"]; !ok {
			t.Errorf("%q: missing \"event\" key in JSON", env.Event)
		}
		if _, ok := raw["data"]; !ok {
			t.Errorf("%q: missing \"data\" key in JSON", env.Event)
		}
	}
}

func TestEventEnvelope_SpecVersionJSONFieldNames(t *testing.T) {
	env := NewSpecVersionEvent(1, 1, "/spec.md")
	assertJSONDataKeys(t, env, []string{
		"version", "round", "timestamp", "change_summary", "file_path",
	})
}

func TestEventEnvelope_IssueUpdateJSONFieldNames(t *testing.T) {
	env := NewIssueUpdateEvent("ISS-1", "open", 1, "CRITICAL", "security")
	assertJSONDataKeys(t, env, []string{
		"issue_id", "status", "round", "severity", "lens", "detail",
	})
}

func TestEventEnvelope_ConvergenceUpdateJSONFieldNames(t *testing.T) {
	env := NewConvergenceUpdateEvent(1, "PASS", 0, 0, 0, true, "ok")
	assertJSONDataKeys(t, env, []string{
		"round", "verdict", "open_critical", "open_major", "open_minor",
		"progress", "rationale",
	})
}

func TestEventEnvelope_GateRequestJSONFieldNames(t *testing.T) {
	env := NewGateRequestEvent("requirements_confirmation", "t-1", nil)
	assertJSONDataKeys(t, env, []string{
		"gate_type", "task_id", "data",
	})
}

func TestEventEnvelope_CircuitBreakerJSONFieldNames(t *testing.T) {
	env := NewCircuitBreakerEvent("max_rounds", 5, 4)
	assertJSONDataKeys(t, env, []string{
		"breaker", "value", "limit",
	})
}

func TestEventEnvelope_AgentErrorJSONFieldNames(t *testing.T) {
	env := NewAgentErrorEvent("reviewer", "timeout", 1, 3)
	assertJSONDataKeys(t, env, []string{
		"agent", "error_type", "retry_count", "max_retries",
	})
}

// assertJSONDataKeys marshals an envelope and checks that the "data" object
// contains exactly the expected set of keys.
func assertJSONDataKeys(t *testing.T, env EventEnvelope, wantKeys []string) {
	t.Helper()

	data, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var outer struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(data, &outer); err != nil {
		t.Fatalf("Unmarshal outer: %v", err)
	}

	var dataMap map[string]json.RawMessage
	if err := json.Unmarshal(outer.Data, &dataMap); err != nil {
		t.Fatalf("Unmarshal data: %v", err)
	}

	for _, key := range wantKeys {
		if _, ok := dataMap[key]; !ok {
			t.Errorf("missing JSON key %q in data payload for event %q", key, env.Event)
		}
	}
}

// ---------------------------------------------------------------------------
// ChannelEmitter tests
// ---------------------------------------------------------------------------

func TestChannelEmitter_EmitAndReceive(t *testing.T) {
	emitter := NewChannelEmitter(8)
	defer emitter.Close()

	sent := NewSpecVersionEvent(1, 1, "/spec.md")
	if err := emitter.Emit(sent); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	select {
	case got := <-emitter.Events():
		if got.Event != sent.Event {
			t.Errorf("received Event = %q, want %q", got.Event, sent.Event)
		}
	default:
		t.Fatal("expected event on channel, got none")
	}
}

func TestChannelEmitter_NonBlockingDrop(t *testing.T) {
	bufSize := 2
	emitter := NewChannelEmitter(bufSize)
	defer emitter.Close()

	// Fill the buffer.
	for i := 0; i < bufSize; i++ {
		if err := emitter.Emit(NewAgentErrorEvent("a", "e", i, 3)); err != nil {
			t.Fatalf("Emit %d: %v", i, err)
		}
	}

	// This emit should not block — returns ErrChannelFull.
	err := emitter.Emit(NewAgentErrorEvent("a", "e", 99, 3))
	if err != ErrChannelFull {
		t.Fatalf("Emit overflow: got %v, want ErrChannelFull", err)
	}

	// Drain and count.
	count := 0
	for {
		select {
		case <-emitter.Events():
			count++
		default:
			goto done
		}
	}
done:
	if count != bufSize {
		t.Errorf("received %d events, want %d (overflow should be dropped)", count, bufSize)
	}
}

func TestChannelEmitter_MultipleEvents(t *testing.T) {
	emitter := NewChannelEmitter(16)
	defer emitter.Close()

	events := []EventEnvelope{
		NewSpecVersionEvent(1, 1, "/a.md"),
		NewIssueUpdateEvent("ISS-1", "open", 1, "MAJOR", "correctness"),
		NewConvergenceUpdateEvent(1, "REVISE", 1, 2, 0, false, "needs work"),
	}

	for _, ev := range events {
		if err := emitter.Emit(ev); err != nil {
			t.Fatalf("Emit %q: %v", ev.Event, err)
		}
	}

	for i, want := range events {
		select {
		case got := <-emitter.Events():
			if got.Event != want.Event {
				t.Errorf("event %d: Event = %q, want %q", i, got.Event, want.Event)
			}
		default:
			t.Fatalf("event %d: expected event on channel, got none", i)
		}
	}
}

func TestChannelEmitter_ImplementsEventEmitter(t *testing.T) {
	// Compile-time interface check.
	var _ EventEmitter = (*ChannelEmitter)(nil)
}

// ---------------------------------------------------------------------------
// Event type constant value tests
// ---------------------------------------------------------------------------

func TestEventTypeConstants(t *testing.T) {
	tests := []struct {
		constant string
		want     string
	}{
		{EventSpecVersion, "spec_version"},
		{EventIssueUpdate, "issue_update"},
		{EventConvergenceUpdate, "convergence_update"},
		{EventGateRequest, "gate_request"},
		{EventCircuitBreaker, "circuit_breaker"},
		{EventAgentError, "agent_error"},
		{EventStateTransition, "state_transition"},
		{EventAgentDispatch, "agent_dispatch"},
		{EventAgentComplete, "agent_complete"},
		{EventWorkflowStatus, "workflow_status"},
	}
	for _, tt := range tests {
		if tt.constant != tt.want {
			t.Errorf("constant value = %q, want %q", tt.constant, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// New event constructor tests
// ---------------------------------------------------------------------------

func TestNewStateTransitionEvent(t *testing.T) {
	env := NewStateTransitionEvent("INIT", "DISCOVERY", 1)
	if env.Event != EventStateTransition {
		t.Errorf("Event = %q, want %q", env.Event, EventStateTransition)
	}
	data, ok := env.Data.(StateTransitionEvent)
	if !ok {
		t.Fatalf("Data type = %T, want StateTransitionEvent", env.Data)
	}
	if data.From != "INIT" {
		t.Errorf("From = %q, want %q", data.From, "INIT")
	}
	if data.To != "DISCOVERY" {
		t.Errorf("To = %q, want %q", data.To, "DISCOVERY")
	}
	if data.Round != 1 {
		t.Errorf("Round = %d, want 1", data.Round)
	}
	if data.Timestamp == "" {
		t.Error("Timestamp should be set automatically")
	}
}

func TestNewAgentDispatchEvent(t *testing.T) {
	env := NewAgentDispatchEvent("discovery", 2)
	if env.Event != EventAgentDispatch {
		t.Errorf("Event = %q, want %q", env.Event, EventAgentDispatch)
	}
	data, ok := env.Data.(AgentDispatchEvent)
	if !ok {
		t.Fatalf("Data type = %T, want AgentDispatchEvent", env.Data)
	}
	if data.Agent != "discovery" {
		t.Errorf("Agent = %q, want %q", data.Agent, "discovery")
	}
	if data.Round != 2 {
		t.Errorf("Round = %d, want 2", data.Round)
	}
	if data.Timestamp == "" {
		t.Error("Timestamp should be set automatically")
	}
}

func TestNewAgentCompleteEvent(t *testing.T) {
	env := NewAgentCompleteEvent("reviewer", 3, true, 5000, 0.25)
	if env.Event != EventAgentComplete {
		t.Errorf("Event = %q, want %q", env.Event, EventAgentComplete)
	}
	data, ok := env.Data.(AgentCompleteEvent)
	if !ok {
		t.Fatalf("Data type = %T, want AgentCompleteEvent", env.Data)
	}
	if data.Agent != "reviewer" {
		t.Errorf("Agent = %q, want %q", data.Agent, "reviewer")
	}
	if data.Round != 3 {
		t.Errorf("Round = %d, want 3", data.Round)
	}
	if !data.Success {
		t.Error("Success = false, want true")
	}
	if data.DurationMS != 5000 {
		t.Errorf("DurationMS = %d, want 5000", data.DurationMS)
	}
	if data.CostUSD != 0.25 {
		t.Errorf("CostUSD = %f, want 0.25", data.CostUSD)
	}
	if data.Timestamp == "" {
		t.Error("Timestamp should be set automatically")
	}
}

func TestNewAgentCompleteEvent_Failure(t *testing.T) {
	env := NewAgentCompleteEvent("drafter", 1, false, 120000, 1.50)
	data, ok := env.Data.(AgentCompleteEvent)
	if !ok {
		t.Fatalf("Data type = %T, want AgentCompleteEvent", env.Data)
	}
	if data.Success {
		t.Error("Success = true, want false")
	}
}

func TestNewWorkflowStatusEvent(t *testing.T) {
	env := NewWorkflowStatusEvent("REVIEWING", 2, "auth-flow", 1.23, 450.5, 8)
	if env.Event != EventWorkflowStatus {
		t.Errorf("Event = %q, want %q", env.Event, EventWorkflowStatus)
	}
	data, ok := env.Data.(WorkflowStatusEvent)
	if !ok {
		t.Fatalf("Data type = %T, want WorkflowStatusEvent", env.Data)
	}
	if data.State != "REVIEWING" {
		t.Errorf("State = %q, want %q", data.State, "REVIEWING")
	}
	if data.Round != 2 {
		t.Errorf("Round = %d, want 2", data.Round)
	}
	if data.Feature != "auth-flow" {
		t.Errorf("Feature = %q, want %q", data.Feature, "auth-flow")
	}
	if data.CostUSD != 1.23 {
		t.Errorf("CostUSD = %f, want 1.23", data.CostUSD)
	}
	if data.WallClock != 450.5 {
		t.Errorf("WallClock = %f, want 450.5", data.WallClock)
	}
	if data.Invocations != 8 {
		t.Errorf("Invocations = %d, want 8", data.Invocations)
	}
	if data.Timestamp == "" {
		t.Error("Timestamp should be set automatically")
	}
}

// ---------------------------------------------------------------------------
// New event JSON field name tests
// ---------------------------------------------------------------------------

func TestEventEnvelope_StateTransitionJSONFieldNames(t *testing.T) {
	env := NewStateTransitionEvent("INIT", "DISCOVERY", 1)
	assertJSONDataKeys(t, env, []string{
		"from", "to", "round", "timestamp",
	})
}

func TestEventEnvelope_AgentDispatchJSONFieldNames(t *testing.T) {
	env := NewAgentDispatchEvent("discovery", 1)
	assertJSONDataKeys(t, env, []string{
		"agent", "round", "timestamp",
	})
}

func TestEventEnvelope_AgentCompleteJSONFieldNames(t *testing.T) {
	env := NewAgentCompleteEvent("reviewer", 1, true, 5000, 0.25)
	assertJSONDataKeys(t, env, []string{
		"agent", "round", "success", "duration_ms", "cost_usd", "timestamp",
	})
}

func TestEventEnvelope_WorkflowStatusJSONFieldNames(t *testing.T) {
	env := NewWorkflowStatusEvent("REVIEWING", 2, "auth", 1.0, 100.0, 5)
	assertJSONDataKeys(t, env, []string{
		"state", "round", "feature_name", "cost_usd",
		"wall_clock_seconds", "agent_invocations", "timestamp",
	})
}

// ---------------------------------------------------------------------------
// FeatureName tests
// ---------------------------------------------------------------------------

func TestEventEnvelopeCarriesFeatureName(t *testing.T) {
	emitter := NewChannelEmitter(8, "alpha")
	defer emitter.Close()

	sent := NewSpecVersionEvent(1, 1, "/spec.md")
	if err := emitter.Emit(sent); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	select {
	case got := <-emitter.Events():
		if got.FeatureName != "alpha" {
			t.Errorf("FeatureName = %q, want %q", got.FeatureName, "alpha")
		}
		if got.Event != EventSpecVersion {
			t.Errorf("Event = %q, want %q", got.Event, EventSpecVersion)
		}
	default:
		t.Fatal("expected event on channel, got none")
	}
}

func TestEventEnvelopeJSONSerialization(t *testing.T) {
	env := EventEnvelope{
		Event:       EventStateTransition,
		FeatureName: "beta",
		Data: StateTransitionEvent{
			From: "INIT", To: "DISCOVERY", Round: 1, Timestamp: "2026-01-01T00:00:00Z",
		},
	}

	data, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	fnRaw, ok := raw["feature_name"]
	if !ok {
		t.Fatal("missing \"feature_name\" key in JSON output")
	}

	var fn string
	if err := json.Unmarshal(fnRaw, &fn); err != nil {
		t.Fatalf("Unmarshal feature_name: %v", err)
	}
	if fn != "beta" {
		t.Errorf("feature_name = %q, want %q", fn, "beta")
	}
}

func TestChannelEmitterPreservesExplicitFeatureName(t *testing.T) {
	emitter := NewChannelEmitter(8, "emitter-default")
	defer emitter.Close()

	explicit := EventEnvelope{
		Event:       EventAgentDispatch,
		FeatureName: "explicit-feature",
		Data:        AgentDispatchEvent{Agent: "reviewer", Round: 1, Timestamp: "2026-01-01T00:00:00Z"},
	}

	if err := emitter.Emit(explicit); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	select {
	case got := <-emitter.Events():
		if got.FeatureName != "explicit-feature" {
			t.Errorf("FeatureName = %q, want %q (emitter should not overwrite explicit value)",
				got.FeatureName, "explicit-feature")
		}
	default:
		t.Fatal("expected event on channel, got none")
	}
}

func TestChannelEmitter_SetFeatureName(t *testing.T) {
	emitter := NewChannelEmitter(8)
	defer emitter.Close()

	// Initially no feature name — event should pass through without one.
	ev1 := NewAgentErrorEvent("a", "timeout", 1, 3)
	if err := emitter.Emit(ev1); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	got1 := <-emitter.Events()
	if got1.FeatureName != "" {
		t.Errorf("FeatureName = %q, want empty", got1.FeatureName)
	}

	// After setting feature name, events should carry it.
	emitter.SetFeatureName("gamma")
	ev2 := NewAgentErrorEvent("b", "rate_limit", 0, 3)
	if err := emitter.Emit(ev2); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	got2 := <-emitter.Events()
	if got2.FeatureName != "gamma" {
		t.Errorf("FeatureName = %q, want %q", got2.FeatureName, "gamma")
	}
}

func TestEventEnvelopeJSONOmitsEmptyFeatureName(t *testing.T) {
	env := EventEnvelope{
		Event: EventCircuitBreaker,
		Data:  CircuitBreakerEvent{Breaker: "max_rounds", Value: 5, Limit: 4},
	}

	data, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if _, ok := raw["feature_name"]; ok {
		t.Error("feature_name should be omitted from JSON when empty")
	}
}

func TestEventEnvelope_NewEventsSerializeToValidJSON(t *testing.T) {
	envelopes := []EventEnvelope{
		NewStateTransitionEvent("INIT", "DISCOVERY", 1),
		NewAgentDispatchEvent("reviewer", 2),
		NewAgentCompleteEvent("drafter", 1, true, 3000, 0.50),
		NewWorkflowStatusEvent("JUDGING", 3, "my-feature", 2.5, 300.0, 10),
	}

	for _, env := range envelopes {
		data, err := json.Marshal(env)
		if err != nil {
			t.Errorf("Marshal %q event: %v", env.Event, err)
			continue
		}

		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Errorf("Unmarshal %q to map: %v", env.Event, err)
			continue
		}
		if _, ok := raw["event"]; !ok {
			t.Errorf("%q: missing \"event\" key in JSON", env.Event)
		}
		if _, ok := raw["data"]; !ok {
			t.Errorf("%q: missing \"data\" key in JSON", env.Event)
		}
	}
}
