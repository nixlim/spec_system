package specworkflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// mockDraftRunner is a test double for AgentRunner that writes a DrafterOutput
// JSON file when Run is called.
type mockDraftRunner struct {
	output    *DrafterOutput
	fail      bool
	failError string
	callCount atomic.Int32
}

func (m *mockDraftRunner) Run(prompt string, outputPath string, timeoutSeconds int) (int, string, float64, int64, error) {
	m.callCount.Add(1)
	if m.fail {
		errMsg := "mock failure"
		if m.failError != "" {
			errMsg = m.failError
		}
		return 1, errMsg, 0.1, 100, nil
	}
	if m.output != nil {
		data, _ := json.MarshalIndent(m.output, "", "  ")
		os.WriteFile(outputPath, data, 0o644)
	}
	return 0, "", 0.5, 500, nil
}

// mockDraftRunnerWithError returns an error from Run (infrastructure failure).
type mockDraftRunnerWithError struct {
	errMsg    string
	callCount atomic.Int32
}

func (m *mockDraftRunnerWithError) Run(prompt string, outputPath string, timeoutSeconds int) (int, string, float64, int64, error) {
	m.callCount.Add(1)
	return 0, "", 0.1, 100, fmt.Errorf("%s", m.errMsg)
}

func newTestDrafterOutput(provider string) *DrafterOutput {
	return &DrafterOutput{
		SchemaVersion: "1.0",
		Agent:         "drafter-" + provider,
		SpecFile:      "spec-v0.md",
		HoldoutFile:   "test-holdouts.md",
		AmbiguityWarnings: []AmbiguityWarning{},
		StructuralSummary: StructuralSummary{
			UserStoryCount:   5,
			BDDScenarioCount: 10,
			FRCount:          20,
			TestCount:        15,
		},
	}
}

// setupDraftingOrchestrator creates a minimal Orchestrator for drafting tests.
func setupDraftingOrchestrator(t *testing.T, claudeRunner, codexRunner AgentRunner, enableCodexDrafting bool) (*Orchestrator, string, *WorkflowStateJSON) {
	t.Helper()

	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, "specs", "test-feature")
	sourceDocsDir := filepath.Join(tmpDir, "source-docs")
	os.MkdirAll(specDir, 0o755)
	os.MkdirAll(sourceDocsDir, 0o755)

	// Write a minimal discovery output for the drafter to reference.
	discoveryOutput := DiscoveryOutput{
		SchemaVersion:    "1.0",
		Agent:            "discovery",
		Actors:           []Actor{{Name: "user", Type: "human", Description: "end user"}},
		ProblemStatement: "test problem",
		Scope:            Scope{InScope: []string{"everything"}, OutOfScope: []string{}},
	}
	dData, _ := json.Marshal(discoveryOutput)
	os.WriteFile(filepath.Join(specDir, "discovery-output.json"), dData, 0o644)

	cfg := DefaultConfig()
	cfg.EnableCodexDrafting = enableCodexDrafting
	cfg.AgentTimeoutSeconds = 60

	state := &WorkflowStateJSON{
		State:       StateDrafting,
		Round:       1,
		FeatureName: "test-feature",
	}

	smConfig := StateMachineConfig{
		MaxGateCorrections: cfg.MaxGateCorrections,
		MaxGate2Redrafts:   cfg.MaxGate2Redrafts,
		MaxRounds:          cfg.MaxRounds,
	}

	sm := NewStateMachine(state, smConfig, nil)

	// Transition to DRAFTING.
	sm.RestoreState(state)

	logger, logErr := NewWorkflowLogger(specDir)
	if logErr != nil {
		t.Fatalf("create logger: %v", logErr)
	}
	t.Cleanup(func() { logger.Close() })

	emitter := NewChannelEmitter(64)

	skills := &SkillCache{
		contents: map[string]string{
			SpecTemplate:        "spec template content",
			BDDTemplate:         "bdd template content",
			TestDatasetTemplate: "test dataset template content",
		},
		checksums: make(map[string]string),
		loaded:    true,
	}
	promptBuilder := NewPromptBuilder(skills, tmpDir, "test-feature")

	orch := &Orchestrator{
		config:        cfg,
		sm:            sm,
		emitter:       emitter,
		promptBuilder: promptBuilder,
		runner:        claudeRunner,
		codexDraftingRunner: codexRunner,
		workspaceDir:  tmpDir,
		featureName:   "test-feature",
		logger:        logger,
		tracker:       NewIssueTracker(),
		gateCh:        make(chan GateResponse, 1),
		issueHistory:  make(map[string][]string),
		activeAgents:  make(map[string]string),
	}

	return orch, specDir, state
}

// ---------------------------------------------------------------------------
// Single-provider (disabled) tests
// ---------------------------------------------------------------------------

func TestDualDrafting_Disabled(t *testing.T) {
	claudeOutput := newTestDrafterOutput("claude")
	claudeRunner := &mockDraftRunner{output: claudeOutput}

	orch, specDir, state := setupDraftingOrchestrator(t, claudeRunner, nil, false)

	err := orch.handleDrafting(state, specDir)
	if err != nil {
		t.Fatalf("handleDrafting: %v", err)
	}

	// Should produce drafter-output.json (current naming).
	outPath := filepath.Join(specDir, "drafter-output.json")
	if _, err := os.Stat(outPath); err != nil {
		t.Fatalf("expected drafter-output.json to exist: %v", err)
	}

	// Should only call Claude once.
	if got := claudeRunner.callCount.Load(); got != 1 {
		t.Errorf("claude call count: got %d, want 1", got)
	}

	// State should be HUMAN_GATE_2.
	if got := orch.sm.Current(); got != StateHumanGate2 {
		t.Errorf("state: got %s, want HUMAN_GATE_2", got)
	}

	// Draft source should be single_provider.
	if state.DraftSource != "single_provider" {
		t.Errorf("DraftSource = %q, want %q", state.DraftSource, "single_provider")
	}
	if state.DraftFailureNotice != "" {
		t.Errorf("DraftFailureNotice = %q, want empty", state.DraftFailureNotice)
	}
}

func TestDualDrafting_CodexUnavailable(t *testing.T) {
	claudeOutput := newTestDrafterOutput("claude")
	claudeRunner := &mockDraftRunner{output: claudeOutput}

	// EnableCodexDrafting=true but codexRunner=nil (Codex not available).
	orch, specDir, state := setupDraftingOrchestrator(t, claudeRunner, nil, true)

	err := orch.handleDrafting(state, specDir)
	if err != nil {
		t.Fatalf("handleDrafting: %v", err)
	}

	// Should fall back to single-provider behavior.
	outPath := filepath.Join(specDir, "drafter-output.json")
	if _, err := os.Stat(outPath); err != nil {
		t.Fatalf("expected drafter-output.json: %v", err)
	}

	if got := claudeRunner.callCount.Load(); got != 1 {
		t.Errorf("claude call count: got %d, want 1", got)
	}
}

// ---------------------------------------------------------------------------
// Dual-provider: both succeed
// ---------------------------------------------------------------------------

func TestDualDrafting_BothSucceed(t *testing.T) {
	claudeOutput := newTestDrafterOutput("claude")
	codexOutput := newTestDrafterOutput("codex")
	combinedOutput := newTestDrafterOutput("combined")

	claudeRunner := &mockDraftRunner{output: claudeOutput}
	codexRunner := &mockDraftRunner{output: codexOutput}

	orch, specDir, state := setupDraftingOrchestrator(t, claudeRunner, codexRunner, true)

	// The combine step also uses claudeRunner, which will produce output.
	// We need to make the combine step work — the runner writes the DrafterOutput
	// to whatever outputPath it gets. For the combine step, that will be the
	// combined output path.
	_ = combinedOutput // Combine uses the same claudeRunner which writes a valid output.

	err := orch.handleDrafting(state, specDir)
	if err != nil {
		t.Fatalf("handleDrafting: %v", err)
	}

	// Check versioned output files exist.
	claudePath := filepath.Join(specDir, "drafter-output-claude-v1.json")
	codexPath := filepath.Join(specDir, "drafter-output-codex-v1.json")
	combinedPath := filepath.Join(specDir, "drafter-output-combined-v1.json")
	finalPath := filepath.Join(specDir, "drafter-output.json")

	for _, p := range []string{claudePath, codexPath, combinedPath, finalPath} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected %s to exist: %v", filepath.Base(p), err)
		}
	}

	// Both runners should be called (claude for drafting + combine, codex for drafting).
	if got := claudeRunner.callCount.Load(); got < 2 {
		t.Errorf("claude call count: got %d, want >= 2 (drafter + combine)", got)
	}
	if got := codexRunner.callCount.Load(); got != 1 {
		t.Errorf("codex call count: got %d, want 1", got)
	}

	// State should be HUMAN_GATE_2.
	if got := orch.sm.Current(); got != StateHumanGate2 {
		t.Errorf("state: got %s, want HUMAN_GATE_2", got)
	}

	// Draft source should be combined.
	if state.DraftSource != "combined" {
		t.Errorf("DraftSource = %q, want %q", state.DraftSource, "combined")
	}
	if state.DraftFailureNotice != "" {
		t.Errorf("DraftFailureNotice = %q, want empty", state.DraftFailureNotice)
	}
}

// ---------------------------------------------------------------------------
// Dual-provider: one fails, survivor used
// ---------------------------------------------------------------------------

func TestDualDrafting_CodexFailsClaudeSurvives(t *testing.T) {
	claudeOutput := newTestDrafterOutput("claude")
	claudeRunner := &mockDraftRunner{output: claudeOutput}
	codexRunner := &mockDraftRunner{fail: true, failError: "codex timeout"}

	orch, specDir, state := setupDraftingOrchestrator(t, claudeRunner, codexRunner, true)

	err := orch.handleDrafting(state, specDir)
	if err != nil {
		t.Fatalf("handleDrafting: %v", err)
	}

	// drafter-output.json should exist (copied from claude output).
	finalPath := filepath.Join(specDir, "drafter-output.json")
	data, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatalf("expected drafter-output.json: %v", err)
	}

	var output DrafterOutput
	if err := json.Unmarshal(data, &output); err != nil {
		t.Fatalf("parse drafter-output.json: %v", err)
	}

	// State should be HUMAN_GATE_2.
	if got := orch.sm.Current(); got != StateHumanGate2 {
		t.Errorf("state: got %s, want HUMAN_GATE_2", got)
	}

	// Draft source should be single_survivor with failure notice.
	if state.DraftSource != "single_survivor" {
		t.Errorf("DraftSource = %q, want %q", state.DraftSource, "single_survivor")
	}
	if !strings.Contains(state.DraftFailureNotice, "codex") {
		t.Errorf("DraftFailureNotice = %q, want notice mentioning codex", state.DraftFailureNotice)
	}
}

func TestDualDrafting_ClaudeFailsCodexSurvives(t *testing.T) {
	codexOutput := newTestDrafterOutput("codex")
	claudeRunner := &mockDraftRunner{fail: true, failError: "claude crash"}
	codexRunner := &mockDraftRunner{output: codexOutput}

	orch, specDir, state := setupDraftingOrchestrator(t, claudeRunner, codexRunner, true)

	err := orch.handleDrafting(state, specDir)
	if err != nil {
		t.Fatalf("handleDrafting: %v", err)
	}

	// drafter-output.json should exist (copied from codex output).
	finalPath := filepath.Join(specDir, "drafter-output.json")
	data, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatalf("expected drafter-output.json: %v", err)
	}

	var output DrafterOutput
	if err := json.Unmarshal(data, &output); err != nil {
		t.Fatalf("parse drafter-output.json: %v", err)
	}
	if output.Agent != "drafter-codex" {
		t.Errorf("expected codex output, got agent=%s", output.Agent)
	}

	if got := orch.sm.Current(); got != StateHumanGate2 {
		t.Errorf("state: got %s, want HUMAN_GATE_2", got)
	}

	// Draft source should be single_survivor with failure notice mentioning claude.
	if state.DraftSource != "single_survivor" {
		t.Errorf("DraftSource = %q, want %q", state.DraftSource, "single_survivor")
	}
	if !strings.Contains(state.DraftFailureNotice, "claude") {
		t.Errorf("DraftFailureNotice = %q, want notice mentioning claude", state.DraftFailureNotice)
	}
}

// ---------------------------------------------------------------------------
// Dual-provider: both fail → escalate
// ---------------------------------------------------------------------------

func TestDualDrafting_BothFail(t *testing.T) {
	claudeRunner := &mockDraftRunner{fail: true, failError: "claude crash"}
	codexRunner := &mockDraftRunner{fail: true, failError: "codex crash"}

	orch, specDir, state := setupDraftingOrchestrator(t, claudeRunner, codexRunner, true)

	err := orch.handleDrafting(state, specDir)
	if err != nil {
		t.Fatalf("handleDrafting: %v", err)
	}

	// State should be ESCALATED (both failed).
	current := orch.sm.Current()
	if current != StateEscalated {
		t.Errorf("state: got %s, want ESCALATED", current)
	}
}

// ---------------------------------------------------------------------------
// Combine reviser failure → concatenation fallback
// ---------------------------------------------------------------------------

func TestDualDrafting_CombineFailsFallbackToConcatenation(t *testing.T) {
	claudeOutput := newTestDrafterOutput("claude")
	codexOutput := newTestDrafterOutput("codex")

	// Create a runner that succeeds for drafting but fails for combine.
	callNum := atomic.Int32{}
	claudeRunner := &mockDraftRunner{output: claudeOutput}
	codexRunner := &mockDraftRunner{output: codexOutput}

	orch, specDir, state := setupDraftingOrchestrator(t, claudeRunner, codexRunner, true)

	// Replace the runner with one that fails on the 2nd call (combine step).
	// Since claudeRunner is used for both drafting and combine, we need a
	// runner that writes output first time but fails second time.
	orch.runner = &conditionalRunner{
		callNum:   &callNum,
		output:    claudeOutput,
		failAfter: 1, // fail on call #2 (combine)
	}

	err := orch.handleDrafting(state, specDir)
	if err != nil {
		t.Fatalf("handleDrafting: %v", err)
	}

	// Combined file should still exist (concatenation fallback).
	combinedPath := filepath.Join(specDir, "drafter-output-combined-v1.json")
	data, err := os.ReadFile(combinedPath)
	if err != nil {
		t.Fatalf("expected combined output: %v", err)
	}

	// Should contain the AMB-W-COMBINE warning from concatenation fallback.
	var output DrafterOutput
	if err := json.Unmarshal(data, &output); err != nil {
		t.Fatalf("parse combined output: %v", err)
	}

	foundCombineWarning := false
	for _, w := range output.AmbiguityWarnings {
		if w.ID == "AMB-W-COMBINE" {
			foundCombineWarning = true
			break
		}
	}
	if !foundCombineWarning {
		t.Error("expected AMB-W-COMBINE warning in concatenation fallback output")
	}

	if got := orch.sm.Current(); got != StateHumanGate2 {
		t.Errorf("state: got %s, want HUMAN_GATE_2", got)
	}
}

// conditionalRunner succeeds for the first N calls, then fails.
type conditionalRunner struct {
	callNum   *atomic.Int32
	output    *DrafterOutput
	failAfter int32 // fail starting at this call number (1-based)
}

func (r *conditionalRunner) Run(prompt string, outputPath string, timeoutSeconds int) (int, string, float64, int64, error) {
	n := r.callNum.Add(1)
	if n > r.failAfter {
		return 1, "combine failed", 0.1, 100, nil
	}
	if r.output != nil {
		data, _ := json.MarshalIndent(r.output, "", "  ")
		os.WriteFile(outputPath, data, 0o644)
	}
	return 0, "", 0.5, 500, nil
}

// ---------------------------------------------------------------------------
// Versioned file naming
// ---------------------------------------------------------------------------

func TestDualDrafting_VersionedFileNaming(t *testing.T) {
	claudeOutput := newTestDrafterOutput("claude")
	codexOutput := newTestDrafterOutput("codex")
	claudeRunner := &mockDraftRunner{output: claudeOutput}
	codexRunner := &mockDraftRunner{output: codexOutput}

	orch, specDir, state := setupDraftingOrchestrator(t, claudeRunner, codexRunner, true)

	// Simulate a re-draft (gate2 redraft count = 1).
	state.Gate2RedraftCount = 1

	err := orch.handleDrafting(state, specDir)
	if err != nil {
		t.Fatalf("handleDrafting: %v", err)
	}

	// Version should be 2 (Gate2RedraftCount + 1).
	claudePath := filepath.Join(specDir, "drafter-output-claude-v2.json")
	codexPath := filepath.Join(specDir, "drafter-output-codex-v2.json")
	combinedPath := filepath.Join(specDir, "drafter-output-combined-v2.json")

	for _, p := range []string{claudePath, codexPath, combinedPath} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected %s to exist: %v", filepath.Base(p), err)
		}
	}
}

// ---------------------------------------------------------------------------
// buildCombinePrompt test
// ---------------------------------------------------------------------------

func TestBuildCombinePrompt(t *testing.T) {
	prompt := buildCombinePrompt("/path/claude.json", "/path/codex.json", "/path/combined.json")

	if !strings.Contains(prompt, "/path/claude.json") {
		t.Error("prompt should reference claude path")
	}
	if !strings.Contains(prompt, "/path/codex.json") {
		t.Error("prompt should reference codex path")
	}
	if !strings.Contains(prompt, "/path/combined.json") {
		t.Error("prompt should reference combined output path")
	}
	if !strings.Contains(prompt, "Draft Combine Agent") {
		t.Error("prompt should identify as combine agent")
	}
}

// ---------------------------------------------------------------------------
// concatenateDrafts test
// ---------------------------------------------------------------------------

func TestConcatenateDrafts(t *testing.T) {
	claudeOutput := newTestDrafterOutput("claude")
	codexOutput := newTestDrafterOutput("codex")

	claudeData, _ := json.Marshal(claudeOutput)
	codexData, _ := json.Marshal(codexOutput)

	result := concatenateDrafts(claudeData, codexData)

	var output DrafterOutput
	if err := json.Unmarshal(result, &output); err != nil {
		t.Fatalf("parse concatenated output: %v", err)
	}

	// Should have the AMB-W-COMBINE warning.
	foundCombineWarning := false
	for _, w := range output.AmbiguityWarnings {
		if w.ID == "AMB-W-COMBINE" {
			foundCombineWarning = true
			break
		}
	}
	if !foundCombineWarning {
		t.Error("expected AMB-W-COMBINE warning")
	}

	// Agent should be from Claude (the base).
	if output.Agent != "drafter-claude" {
		t.Errorf("expected agent drafter-claude, got %s", output.Agent)
	}
}

func TestConcatenateDrafts_InvalidClaudeJSON(t *testing.T) {
	// When Claude data is not valid JSON, return it raw.
	claudeData := []byte("not json")
	codexData := []byte(`{"agent":"codex"}`)

	result := concatenateDrafts(claudeData, codexData)
	if string(result) != "not json" {
		t.Errorf("expected raw claude data, got %s", string(result))
	}
}
