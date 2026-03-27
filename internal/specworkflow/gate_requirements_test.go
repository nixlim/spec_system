package specworkflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

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

func TestGate1EnterGate_SingleProvider_WrapsInGateData(t *testing.T) {
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

	event := <-emitter.Events()
	payload := event.Data.(GateRequestEvent)
	gateData, ok := payload.Data.(*DiscoveryGateData)
	if !ok {
		t.Fatalf("expected *DiscoveryGateData, got %T", payload.Data)
	}
	if gateData.DualProvider {
		t.Error("expected DualProvider=false for single-provider EnterGate")
	}
	if gateData.MergedOutput == nil {
		t.Error("expected MergedOutput to be set")
	}
	if gateData.ClaudeOutput != nil || gateData.CodexOutput != nil {
		t.Error("expected ClaudeOutput and CodexOutput to be nil in single-provider mode")
	}
}

func TestGate1EnterDualGate_EmitsDualProviderData(t *testing.T) {
	handler, emitter := newGate1TestSetup(0)

	merged := &DiscoveryOutput{SchemaVersion: "1.0", Agent: "merged", ProblemStatement: "merged"}
	claude := &DiscoveryOutput{SchemaVersion: "1.0", Agent: "claude", ProblemStatement: "claude view"}
	codex := &DiscoveryOutput{SchemaVersion: "1.0", Agent: "codex", ProblemStatement: "codex view"}

	data := &DiscoveryGateData{
		MergedOutput: merged,
		ClaudeOutput: claude,
		CodexOutput:  codex,
		DualProvider: true,
		ClaudeStatus: "success",
		CodexStatus:  "success",
	}

	if err := handler.EnterDualGate(data); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	event := <-emitter.Events()
	payload := event.Data.(GateRequestEvent)
	gateData, ok := payload.Data.(*DiscoveryGateData)
	if !ok {
		t.Fatalf("expected *DiscoveryGateData, got %T", payload.Data)
	}
	if !gateData.DualProvider {
		t.Error("expected DualProvider=true")
	}
	if gateData.ClaudeOutput == nil || gateData.CodexOutput == nil {
		t.Error("expected both provider outputs to be set")
	}
	if gateData.ClaudeStatus != "success" || gateData.CodexStatus != "success" {
		t.Errorf("expected both statuses 'success', got claude=%s codex=%s", gateData.ClaudeStatus, gateData.CodexStatus)
	}
}

func TestGate1EnterDualGate_WithFailureNotice(t *testing.T) {
	handler, emitter := newGate1TestSetup(0)

	data := &DiscoveryGateData{
		MergedOutput:  &DiscoveryOutput{SchemaVersion: "1.0", Agent: "claude", ProblemStatement: "test"},
		ClaudeOutput:  &DiscoveryOutput{SchemaVersion: "1.0", Agent: "claude", ProblemStatement: "test"},
		DualProvider:  true,
		ClaudeStatus:  "success",
		CodexStatus:   "failed",
		FailureNotice: "Codex discovery agent failed; showing Claude output only",
	}

	if err := handler.EnterDualGate(data); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	event := <-emitter.Events()
	payload := event.Data.(GateRequestEvent)
	gateData := payload.Data.(*DiscoveryGateData)
	if gateData.FailureNotice == "" {
		t.Error("expected failure notice to be set")
	}
	if gateData.CodexOutput != nil {
		t.Error("expected CodexOutput to be nil when codex failed")
	}
}

// --- buildDiscoveryGateData tests ---

func makeTestDiscoveryOutput(agent string) *DiscoveryOutput {
	return &DiscoveryOutput{
		SchemaVersion:    "1.0",
		Agent:            agent,
		ProblemStatement: agent + " problem",
		Actors:           []Actor{{Name: "User", Type: "human", Description: "test"}},
		Scope:            Scope{InScope: []string{"test"}},
	}
}

func writeDiscoveryJSON(t *testing.T, path string, output *DiscoveryOutput) {
	t.Helper()
	data, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func buildGateDataOrchestrator(t *testing.T, enableCodex bool) (*Orchestrator, string) {
	t.Helper()
	dir := t.TempDir()
	specDir := filepath.Join(dir, "specs", "test-feature")
	os.MkdirAll(specDir, 0o755)

	cfg := DefaultConfig()
	cfg.EnableCodexDiscovery = enableCodex

	orch := &Orchestrator{
		config:      cfg,
		workspaceDir: dir,
		featureName:  "test-feature",
	}
	return orch, specDir
}

func TestBuildDiscoveryGateData_SingleProvider_ReturnsNil(t *testing.T) {
	orch, specDir := buildGateDataOrchestrator(t, false)
	state := &WorkflowStateJSON{Gate1CorrectionCount: 0}
	merged := makeTestDiscoveryOutput("discovery")

	result := orch.buildDiscoveryGateData(merged, specDir, state)
	if result != nil {
		t.Error("expected nil for single-provider mode")
	}
}

func TestBuildDiscoveryGateData_DualProvider_BothSucceed(t *testing.T) {
	orch, specDir := buildGateDataOrchestrator(t, true)
	state := &WorkflowStateJSON{Gate1CorrectionCount: 0}

	claudeOut := makeTestDiscoveryOutput("claude")
	codexOut := makeTestDiscoveryOutput("codex")
	merged := makeTestDiscoveryOutput("merged")

	writeDiscoveryJSON(t, filepath.Join(specDir, "discovery-output-claude-v1.json"), claudeOut)
	writeDiscoveryJSON(t, filepath.Join(specDir, "discovery-output-codex-v1.json"), codexOut)

	result := orch.buildDiscoveryGateData(merged, specDir, state)
	if result == nil {
		t.Fatal("expected non-nil gate data")
	}
	if !result.DualProvider {
		t.Error("expected DualProvider=true")
	}
	if result.ClaudeOutput == nil || result.CodexOutput == nil {
		t.Error("expected both provider outputs")
	}
	if result.ClaudeStatus != "success" || result.CodexStatus != "success" {
		t.Errorf("statuses: claude=%s codex=%s", result.ClaudeStatus, result.CodexStatus)
	}
	if result.FailureNotice != "" {
		t.Errorf("expected no failure notice, got %q", result.FailureNotice)
	}
	if result.MergedOutput == nil {
		t.Error("expected MergedOutput set")
	}
}

func TestBuildDiscoveryGateData_DualProvider_CodexFailed(t *testing.T) {
	orch, specDir := buildGateDataOrchestrator(t, true)
	state := &WorkflowStateJSON{Gate1CorrectionCount: 0}

	claudeOut := makeTestDiscoveryOutput("claude")
	merged := makeTestDiscoveryOutput("claude")

	// Only Claude output exists.
	writeDiscoveryJSON(t, filepath.Join(specDir, "discovery-output-claude-v1.json"), claudeOut)

	result := orch.buildDiscoveryGateData(merged, specDir, state)
	if result == nil {
		t.Fatal("expected non-nil gate data")
	}
	if result.ClaudeStatus != "success" {
		t.Errorf("expected claude status success, got %s", result.ClaudeStatus)
	}
	if result.CodexStatus != "failed" {
		t.Errorf("expected codex status failed, got %s", result.CodexStatus)
	}
	if result.CodexOutput != nil {
		t.Error("expected CodexOutput to be nil")
	}
	if result.FailureNotice == "" {
		t.Error("expected failure notice")
	}
}

func TestBuildDiscoveryGateData_DualProvider_ClaudeFailed(t *testing.T) {
	orch, specDir := buildGateDataOrchestrator(t, true)
	state := &WorkflowStateJSON{Gate1CorrectionCount: 0}

	codexOut := makeTestDiscoveryOutput("codex")
	merged := makeTestDiscoveryOutput("codex")

	// Only Codex output exists.
	writeDiscoveryJSON(t, filepath.Join(specDir, "discovery-output-codex-v1.json"), codexOut)

	result := orch.buildDiscoveryGateData(merged, specDir, state)
	if result == nil {
		t.Fatal("expected non-nil gate data")
	}
	if result.ClaudeStatus != "failed" {
		t.Errorf("expected claude status failed, got %s", result.ClaudeStatus)
	}
	if result.CodexStatus != "success" {
		t.Errorf("expected codex status success, got %s", result.CodexStatus)
	}
	if result.ClaudeOutput != nil {
		t.Error("expected ClaudeOutput to be nil")
	}
	if result.FailureNotice == "" {
		t.Error("expected failure notice")
	}
}

func TestBuildDiscoveryGateData_DualProvider_NoPerProviderFiles(t *testing.T) {
	orch, specDir := buildGateDataOrchestrator(t, true)
	state := &WorkflowStateJSON{Gate1CorrectionCount: 0}
	merged := makeTestDiscoveryOutput("discovery")

	// No per-provider files exist (e.g. codex runner was nil, fell through to single-provider).
	result := orch.buildDiscoveryGateData(merged, specDir, state)
	if result != nil {
		t.Error("expected nil when no per-provider files exist")
	}
}

func TestBuildDiscoveryGateData_DualProvider_CorrectionRound2(t *testing.T) {
	orch, specDir := buildGateDataOrchestrator(t, true)
	state := &WorkflowStateJSON{Gate1CorrectionCount: 1} // round 2

	claudeOut := makeTestDiscoveryOutput("claude")
	codexOut := makeTestDiscoveryOutput("codex")
	merged := makeTestDiscoveryOutput("merged")

	// Round 2 files.
	writeDiscoveryJSON(t, filepath.Join(specDir, "discovery-output-claude-v2.json"), claudeOut)
	writeDiscoveryJSON(t, filepath.Join(specDir, "discovery-output-codex-v2.json"), codexOut)

	result := orch.buildDiscoveryGateData(merged, specDir, state)
	if result == nil {
		t.Fatal("expected non-nil gate data for round 2")
	}
	if result.ClaudeOutput == nil || result.CodexOutput == nil {
		t.Error("expected both provider outputs for round 2")
	}
}
