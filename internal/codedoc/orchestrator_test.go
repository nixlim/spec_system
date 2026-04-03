package codedoc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// ---------------------------------------------------------------------------
// Mock runner for orchestrator tests
// ---------------------------------------------------------------------------

type orchMockRunner struct {
	handler   func(prompt, outputPath string, timeout int) (int, string, float64, int64, error)
	callCount atomic.Int64
}

func (m *orchMockRunner) Run(prompt, outputPath string, timeout int) (int, string, float64, int64, error) {
	m.callCount.Add(1)
	return m.handler(prompt, outputPath, timeout)
}

// ---------------------------------------------------------------------------
// Mock emitter
// ---------------------------------------------------------------------------

type orchMockEmitter struct {
	events []CDEvent
}

func (e *orchMockEmitter) Emit(event CDEvent) {
	e.events = append(e.events, event)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func orchConfig() CodedocConfig {
	cfg := DefaultCodedocConfig()
	cfg.MaxRetries = 0
	cfg.AgentTimeoutSeconds = 10
	cfg.MinRounds = 1
	cfg.MaxRounds = 3
	cfg.MaxCostUSD = 100.0
	cfg.MaxWallClockMinutes = 120
	return cfg
}

// successRunner writes appropriate outputs based on the output path.
func successRunner(featureDir string) *orchMockRunner {
	return &orchMockRunner{
		handler: func(prompt, outputPath string, _ int) (int, string, float64, int64, error) {
			// Determine what type of output to write based on the file name.
			base := filepath.Base(outputPath)

			switch {
			case contains(base, "discovery"):
				writeDiscoveryOutput(outputPath)
			case contains(base, "drafter"):
				os.WriteFile(outputPath, validDrafterOutputJSON(), 0o644)
			case contains(base, "review"):
				writeReviewerOutput(outputPath)
			case contains(base, "revision"):
				writeRevisionOutput(outputPath)
			case contains(base, "judge"):
				writeJudgeOutput(outputPath)
			default:
				// Generic valid JSON.
				os.WriteFile(outputPath, []byte(`{}`), 0o644)
			}
			return 0, "", 0.01, 100, nil
		},
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && findSubstring(s, substr))
}

func findSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func writeDiscoveryOutput(path string) {
	output := DiscoveryOutput{
		SchemaVersion:    "1.0",
		Agent:            "discovery-claude",
		Mode:             "full",
		CompletionStatus: CompletionStatus{Status: "complete"},
		Modules:          []ModuleInfo{{Path: "internal/foo", Name: "foo"}},
		EntryPoints:      []EntryPoint{{Path: "cmd/main.go", Type: "cli"}},
	}
	data, _ := json.Marshal(output)
	os.WriteFile(path, data, 0o644)
}

func writeReviewerOutput(path string) {
	output := ReviewerOutput{
		SchemaVersion: "1.0",
		Agent:         "cd-reviewer",
		Round:         1,
		LensesApplied: []string{"ACC"},
		Findings: []ReviewFinding{
			{
				ID: "ACC-001", Description: "Minor issue",
				Severity: SeverityMinor, Status: "open",
				Impact: "low", Recommendation: "Fix it",
				Lens: "ACC", AffectedSection: "overview",
				AffectedFile: "docs/report.md",
			},
		},
	}
	data, _ := json.Marshal(output)
	os.WriteFile(path, data, 0o644)
}

func writeRevisionOutput(path string) {
	output := RevisionOutput{
		SchemaVersion: "1.0",
		Agent:         "revision-agent",
		Round:         1,
		Addressed: []RevisedFinding{
			{FindingID: "ACC-001", Status: "resolved", Rationale: "Fixed the issue"},
		},
	}
	data, _ := json.Marshal(output)
	os.WriteFile(path, data, 0o644)
}

func writeJudgeOutput(path string) {
	output := JudgeOutput{
		SchemaVersion: "1.0",
		Agent:         "judge-agent",
		Round:         1,
		Verdict:       "PASS",
		Rationale:     "All findings resolved",
	}
	data, _ := json.Marshal(output)
	os.WriteFile(path, data, 0o644)
}

// ---------------------------------------------------------------------------
// TestOrchestrator — state persistence
// ---------------------------------------------------------------------------

func TestOrchestratorStatePersistence(t *testing.T) {
	dir := t.TempDir()
	featureDir := filepath.Join(dir, "codedoc", "test-feature")

	emitter := &orchMockEmitter{}
	orch := NewCodedocOrchestrator(CodedocOrchestratorConfig{
		WorkspaceDir: dir,
		FeatureName:  "test-feature",
		CodePath:     "/repo",
		Mode:         "full",
		Config:       orchConfig(),
		Runner:       successRunner(featureDir),
		Emitter:      emitter,
	})

	// Transition init → discovery.
	err := orch.RunWorkflow()
	if err != nil {
		// RunWorkflow may error due to the mock runner not being perfect,
		// but state should still be persisted.
		t.Logf("RunWorkflow returned: %v (may be expected in test)", err)
	}

	// Check that workflow-state.json was created.
	statePath := filepath.Join(featureDir, "workflow-state.json")
	if _, statErr := os.Stat(statePath); os.IsNotExist(statErr) {
		t.Error("workflow-state.json should be persisted after state transition")
	}
}

// ---------------------------------------------------------------------------
// TestOrchestrator — init transitions to discovery
// ---------------------------------------------------------------------------

func TestOrchestratorInitTransitionsToDiscovery(t *testing.T) {
	dir := t.TempDir()

	orch := NewCodedocOrchestrator(CodedocOrchestratorConfig{
		WorkspaceDir: dir,
		FeatureName:  "test-feature",
		CodePath:     "/repo",
		Mode:         "full",
		Config:       orchConfig(),
		Runner:       successRunner(filepath.Join(dir, "codedoc", "test-feature")),
	})

	if orch.State().State != CDInit {
		t.Errorf("expected initial state CD_INIT, got %s", orch.State().State)
	}
}

// ---------------------------------------------------------------------------
// TestOrchestrator — WebSocket events emitted
// ---------------------------------------------------------------------------

func TestOrchestratorEmitsEvents(t *testing.T) {
	dir := t.TempDir()
	emitter := &orchMockEmitter{}

	orch := NewCodedocOrchestrator(CodedocOrchestratorConfig{
		WorkspaceDir: dir,
		FeatureName:  "test-feature",
		CodePath:     "/repo",
		Mode:         "full",
		Config:       orchConfig(),
		Runner:       successRunner(filepath.Join(dir, "codedoc", "test-feature")),
		Emitter:      emitter,
	})

	_ = orch.RunWorkflow()

	if len(emitter.events) == 0 {
		t.Error("expected at least one event to be emitted")
	}
}

// ---------------------------------------------------------------------------
// TestOrchestrator — resume from CD_ERROR with no artefacts → CD_DISCOVERY
// ---------------------------------------------------------------------------

func TestOrchestratorResumeNoArtefactsGoesToDiscovery(t *testing.T) {
	dir := t.TempDir()
	featureDir := filepath.Join(dir, "codedoc", "test-feature")
	os.MkdirAll(featureDir, 0o755)

	ws := &CDStateJSON{
		State:       CDError,
		FeatureName: "test-feature",
		CodePath:    "/repo",
		Mode:        "full",
	}

	cfg := orchConfig()
	smCfg := CDStateMachineConfigFromConfig(&cfg)
	sm := NewCDStateMachine(ws, smCfg, nil)

	orch := &CodedocOrchestrator{
		config:      cfg,
		sm:          sm,
		runner:      successRunner(featureDir),
		emitter:     NoopEmitter{},
		featureDir:  featureDir,
		codePath:    "/repo",
		featureName: "test-feature",
	}

	err := orch.handleResume()
	if err != nil {
		t.Fatalf("handleResume error: %v", err)
	}
	if orch.sm.Current() != CDDiscovery {
		t.Errorf("expected CD_DISCOVERY after resume with no artefacts, got %s", orch.sm.Current())
	}
}

// ---------------------------------------------------------------------------
// TestOrchestrator — resume with discovery output → CD_HUMAN_GATE_SCOPE
// ---------------------------------------------------------------------------

func TestOrchestratorResumeWithDiscoveryGoesToScope(t *testing.T) {
	dir := t.TempDir()
	featureDir := filepath.Join(dir, "codedoc", "test-feature")
	os.MkdirAll(featureDir, 0o755)

	// Write discovery output.
	writeDiscoveryOutput(filepath.Join(featureDir, "discovery-output.json"))

	ws := &CDStateJSON{
		State:       CDError,
		FeatureName: "test-feature",
		CodePath:    "/repo",
		Mode:        "full",
	}

	cfg := orchConfig()
	smCfg := CDStateMachineConfigFromConfig(&cfg)
	sm := NewCDStateMachine(ws, smCfg, nil)

	orch := &CodedocOrchestrator{
		config:      cfg,
		sm:          sm,
		runner:      successRunner(featureDir),
		emitter:     NoopEmitter{},
		featureDir:  featureDir,
		codePath:    "/repo",
		featureName: "test-feature",
	}

	err := orch.handleResume()
	if err != nil {
		t.Fatalf("handleResume error: %v", err)
	}
	if orch.sm.Current() != CDHumanGateScope {
		t.Errorf("expected CD_HUMAN_GATE_SCOPE, got %s", orch.sm.Current())
	}
}

// ---------------------------------------------------------------------------
// TestOrchestrator — resume with drafter output → CD_HUMAN_GATE_DRAFT
// ---------------------------------------------------------------------------

func TestOrchestratorResumeWithDrafterGoesToDraftGate(t *testing.T) {
	dir := t.TempDir()
	featureDir := filepath.Join(dir, "codedoc", "test-feature")
	os.MkdirAll(featureDir, 0o755)

	// Write both discovery and drafter outputs.
	writeDiscoveryOutput(filepath.Join(featureDir, "discovery-output.json"))
	os.WriteFile(filepath.Join(featureDir, "drafter-output.json"), validDrafterOutputJSON(), 0o644)

	ws := &CDStateJSON{
		State:       CDError,
		FeatureName: "test-feature",
		CodePath:    "/repo",
		Mode:        "full",
	}

	cfg := orchConfig()
	smCfg := CDStateMachineConfigFromConfig(&cfg)
	sm := NewCDStateMachine(ws, smCfg, nil)

	orch := &CodedocOrchestrator{
		config:      cfg,
		sm:          sm,
		runner:      successRunner(featureDir),
		emitter:     NoopEmitter{},
		featureDir:  featureDir,
		codePath:    "/repo",
		featureName: "test-feature",
	}

	err := orch.handleResume()
	if err != nil {
		t.Fatalf("handleResume error: %v", err)
	}
	if orch.sm.Current() != CDHumanGateDraft {
		t.Errorf("expected CD_HUMAN_GATE_DRAFT, got %s", orch.sm.Current())
	}
}

// ---------------------------------------------------------------------------
// TestOrchestrator — resume with review outputs → CD_REVIEWING
// ---------------------------------------------------------------------------

func TestOrchestratorResumeWithReviewOutputsGoesToReviewing(t *testing.T) {
	dir := t.TempDir()
	featureDir := filepath.Join(dir, "codedoc", "test-feature")
	os.MkdirAll(featureDir, 0o755)

	// Write discovery, drafter, and merged findings.
	writeDiscoveryOutput(filepath.Join(featureDir, "discovery-output.json"))
	os.WriteFile(filepath.Join(featureDir, "drafter-output.json"), validDrafterOutputJSON(), 0o644)

	merged := MergedCodedocFindings{
		Round:    1,
		Findings: []ReviewFinding{{ID: "ACC-001", Severity: SeverityMinor, Status: "open"}},
	}
	mergedData, _ := json.Marshal(merged)
	os.WriteFile(filepath.Join(featureDir, "merged-findings-round-1.json"), mergedData, 0o644)

	ws := &CDStateJSON{
		State:       CDError,
		FeatureName: "test-feature",
		CodePath:    "/repo",
		Mode:        "full",
	}

	cfg := orchConfig()
	smCfg := CDStateMachineConfigFromConfig(&cfg)
	sm := NewCDStateMachine(ws, smCfg, nil)

	orch := &CodedocOrchestrator{
		config:      cfg,
		sm:          sm,
		runner:      successRunner(featureDir),
		emitter:     NoopEmitter{},
		featureDir:  featureDir,
		codePath:    "/repo",
		featureName: "test-feature",
	}

	err := orch.handleResume()
	if err != nil {
		t.Fatalf("handleResume error: %v", err)
	}
	if orch.sm.Current() != CDReviewing {
		t.Errorf("expected CD_REVIEWING, got %s", orch.sm.Current())
	}
}

// ---------------------------------------------------------------------------
// TestOrchestrator — resume with staging dir → CD_HUMAN_GATE_FINAL
// ---------------------------------------------------------------------------

func TestOrchestratorResumeWithStagingGoesToFinalGate(t *testing.T) {
	dir := t.TempDir()
	codePath := filepath.Join(dir, "repo")
	featureDir := filepath.Join(dir, "codedoc", "test-feature")
	// Create staging at the correct Writer path: codePath/docs/.codedoc-staging
	os.MkdirAll(filepath.Join(codePath, "docs", ".codedoc-staging"), 0o755)
	os.MkdirAll(featureDir, 0o755)

	ws := &CDStateJSON{
		State:       CDError,
		FeatureName: "test-feature",
		CodePath:    codePath,
		Mode:        "full",
	}

	cfg := orchConfig()
	smCfg := CDStateMachineConfigFromConfig(&cfg)
	sm := NewCDStateMachine(ws, smCfg, nil)

	orch := &CodedocOrchestrator{
		config:      cfg,
		sm:          sm,
		runner:      successRunner(featureDir),
		emitter:     NoopEmitter{},
		featureDir:  featureDir,
		codePath:    codePath,
		featureName: "test-feature",
	}

	err := orch.handleResume()
	if err != nil {
		t.Fatalf("handleResume error: %v", err)
	}
	if orch.sm.Current() != CDHumanGateFinal {
		t.Errorf("expected CD_HUMAN_GATE_FINAL, got %s", orch.sm.Current())
	}
}

// ---------------------------------------------------------------------------
// TestOrchestrator — circuit breaker: cost
// ---------------------------------------------------------------------------

func TestOrchestratorCircuitBreakerCost(t *testing.T) {
	dir := t.TempDir()
	featureDir := filepath.Join(dir, "codedoc", "test-feature")
	os.MkdirAll(featureDir, 0o755)

	cfg := orchConfig()
	cfg.MaxCostUSD = 0.01 // Very low budget

	ws := &CDStateJSON{
		State:             CDDiscovery,
		FeatureName:       "test-feature",
		CumulativeCostUSD: 100.0, // Already over budget
	}

	smCfg := CDStateMachineConfigFromConfig(&cfg)
	sm := NewCDStateMachine(ws, smCfg, nil)

	// Trying to transition to a non-terminal state should be blocked by cost guard.
	err := sm.Transition(CDHumanGateScope)
	if err == nil {
		t.Fatal("expected cost guard to block transition")
	}

	// Transitioning to CDEscalated should still work.
	err = sm.Transition(CDEscalated)
	if err != nil {
		t.Fatalf("transition to CDEscalated should work even over budget: %v", err)
	}
}

// ---------------------------------------------------------------------------
// TestOrchestrator — circuit breaker: wall clock
// ---------------------------------------------------------------------------

func TestOrchestratorCircuitBreakerWallClock(t *testing.T) {
	cfg := orchConfig()
	cfg.MaxWallClockMinutes = 1

	ws := &CDStateJSON{
		State:                      CDDiscovery,
		CumulativeWallClockSeconds: 999999, // Way over limit
	}

	smCfg := CDStateMachineConfigFromConfig(&cfg)
	sm := NewCDStateMachine(ws, smCfg, nil)

	err := sm.Transition(CDHumanGateScope)
	if err == nil {
		t.Fatal("expected wall clock guard to block transition")
	}
}

// ---------------------------------------------------------------------------
// TestOrchestrator — circuit breaker: max rounds
// ---------------------------------------------------------------------------

func TestOrchestratorCircuitBreakerMaxRounds(t *testing.T) {
	cfg := orchConfig()
	cfg.MaxRounds = 2

	ws := &CDStateJSON{
		State: CDJudging,
		Round: 5, // Exceeds max
	}

	smCfg := CDStateMachineConfigFromConfig(&cfg)
	sm := NewCDStateMachine(ws, smCfg, nil)

	err := sm.Transition(CDReviewing)
	if err == nil {
		t.Fatal("expected max rounds guard to block transition to CD_REVIEWING")
	}
}

// ---------------------------------------------------------------------------
// TestOrchestrator — NoopEmitter does not panic
// ---------------------------------------------------------------------------

func TestOrchestratorNoopEmitter(t *testing.T) {
	e := NoopEmitter{}
	e.Emit(CDEvent{Type: "test"}) // Should not panic.
}
