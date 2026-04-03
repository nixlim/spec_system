package specworkflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// Test helpers for dual-provider discovery
// ---------------------------------------------------------------------------

// discoveryMockRunner implements AgentRunner for discovery tests.
type discoveryMockRunner struct {
	output  *DiscoveryOutput
	failErr error
}

func (m *discoveryMockRunner) Run(prompt string, outputPath string, timeoutSeconds int) (int, string, float64, int64, error) {
	if m.failErr != nil {
		return 1, m.failErr.Error(), 0.01, 100, m.failErr
	}
	if m.output != nil {
		data, _ := json.MarshalIndent(m.output, "", "  ")
		os.MkdirAll(filepath.Dir(outputPath), 0o755)
		os.WriteFile(outputPath, data, 0o644)
	}
	return 0, "", 0.01, 100, nil
}

func testDiscoveryOutput(agent string) *DiscoveryOutput {
	return &DiscoveryOutput{
		SchemaVersion:    "1.0",
		Agent:            agent,
		ProblemStatement: "Test problem from " + agent,
		Actors: []Actor{
			{Name: "User", Type: "human", Description: "End user from " + agent},
		},
		Scope: Scope{
			InScope:    []string{"feature A"},
			OutOfScope: []string{"feature B"},
		},
		Constraints:       []string{"must be fast"},
		IntegrationPoints: []IntegrationPoint{},
		Priorities: []Priority{
			{Item: "feature A", Priority: "P0", Rationale: "core"},
		},
		Assumptions: []Assumption{
			{Assumption: "users exist", Confidence: "high"},
		},
		OpenQuestions: []string{"what about edge cases?"},
	}
}

// makeTestOrchestrator creates a minimal Orchestrator for discovery tests.
func makeTestOrchestrator(t *testing.T, claudeRunner, codexRunner AgentRunner, enableCodex bool) (*Orchestrator, string) {
	t.Helper()
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, "specs", "test-feature")
	os.MkdirAll(specDir, 0o755)

	cfg := DefaultConfig()
	cfg.EnableCodexDiscovery = enableCodex
	cfg.AgentTimeoutSeconds = 10
	cfg.EnableCodexReviewers = false

	ws := &WorkflowStateJSON{
		State:       StateDiscovery,
		Round:       1,
		FeatureName: "test-feature",
		StartedAt:   "2026-01-01T00:00:00Z",
		UpdatedAt:   "2026-01-01T00:00:00Z",
	}

	smConfig := StateMachineConfig{
		MaxGateCorrections: cfg.MaxGateCorrections,
		MaxGate2Redrafts:   cfg.MaxGate2Redrafts,
		MaxRounds:          cfg.MaxRounds,
	}
	sm := NewStateMachine(ws, smConfig, func(s *WorkflowStateJSON) error { return nil })
	sm.RestoreState(ws)

	skills := &SkillCache{
		contents: map[string]string{
			SpecTemplate:        "# Spec Template\nDummy spec template for testing.",
			BDDTemplate:         "# BDD Template\nDummy BDD template for testing.",
			TestDatasetTemplate: "# Test Dataset Template\nDummy test dataset template.",
		},
		checksums: map[string]string{},
		loaded:    true,
	}

	logger, logErr := NewWorkflowLogger(specDir)
	if logErr != nil {
		t.Fatalf("create logger: %v", logErr)
	}
	t.Cleanup(func() { logger.Close() })

	orch := &Orchestrator{
		config:               cfg,
		sm:                   sm,
		tracker:              NewIssueTracker(),
		logger:               logger,
		emitter:              NewChannelEmitter(64),
		promptBuilder:        NewPromptBuilder(skills, tmpDir, "test-feature"),
		skills:               skills,
		progressTracker:      NewProgressTracker(),
		runner:               claudeRunner,
		codexDiscoveryRunner: codexRunner,
		workspaceDir:         tmpDir,
		featureName:          "test-feature",
		gateCh:               make(chan GateResponse, 1),
		issueHistory:         make(map[string][]string),
		activeAgents:         make(map[string]string),
	}

	return orch, specDir
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestDualDiscovery_BothSucceed(t *testing.T) {
	claudeOutput := testDiscoveryOutput("claude")
	codexOutput := testDiscoveryOutput("codex")
	codexOutput.Actors = append(codexOutput.Actors, Actor{
		Name: "Admin", Type: "human", Description: "Admin from codex",
	})

	claudeRunner := &discoveryMockRunner{output: claudeOutput}
	codexRunner := &discoveryMockRunner{output: codexOutput}

	orch, specDir := makeTestOrchestrator(t, claudeRunner, codexRunner, true)

	goal := GoalInput{Title: "test", SourceDocPaths: []string{}}
	state := orch.sm.State()

	err := orch.handleDiscovery(goal, state, specDir)
	if err != nil {
		t.Fatalf("handleDiscovery failed: %v", err)
	}

	// Verify merged output was written.
	mergedPath := filepath.Join(specDir, "discovery-output-merged-v1.json")
	if _, err := os.Stat(mergedPath); os.IsNotExist(err) {
		t.Error("discovery-output-merged-v1.json not created")
	}

	// Verify canonical output was written.
	canonPath := filepath.Join(specDir, "discovery-output.json")
	data, err := os.ReadFile(canonPath)
	if err != nil {
		t.Fatalf("failed to read discovery-output.json: %v", err)
	}

	var merged DiscoveryOutput
	if err := json.Unmarshal(data, &merged); err != nil {
		t.Fatalf("failed to parse merged output: %v", err)
	}

	// Merged output should have at least 1 actor (agent merge or mechanical fallback).
	if len(merged.Actors) == 0 {
		t.Error("merged output has no actors")
	}

	// Verify per-provider files exist.
	claudePath := filepath.Join(specDir, "discovery-output-claude-v1.json")
	codexPath := filepath.Join(specDir, "discovery-output-codex-v1.json")
	if _, err := os.Stat(claudePath); os.IsNotExist(err) {
		t.Error("discovery-output-claude-v1.json not created")
	}
	if _, err := os.Stat(codexPath); os.IsNotExist(err) {
		t.Error("discovery-output-codex-v1.json not created")
	}

	// State should have transitioned to HUMAN_GATE_1.
	if orch.sm.Current() != StateHumanGate1 {
		t.Errorf("state = %s, want HUMAN_GATE_1", orch.sm.Current())
	}
}

func TestDualDiscovery_CodexFails_ClaudeSurvives(t *testing.T) {
	claudeOutput := testDiscoveryOutput("claude")
	claudeRunner := &discoveryMockRunner{output: claudeOutput}
	codexRunner := &discoveryMockRunner{failErr: os.ErrNotExist}

	orch, specDir := makeTestOrchestrator(t, claudeRunner, codexRunner, true)

	goal := GoalInput{Title: "test", SourceDocPaths: []string{}}
	state := orch.sm.State()

	err := orch.handleDiscovery(goal, state, specDir)
	if err != nil {
		t.Fatalf("handleDiscovery failed: %v", err)
	}

	// Should use Claude's output as canonical.
	data, err := os.ReadFile(filepath.Join(specDir, "discovery-output.json"))
	if err != nil {
		t.Fatalf("failed to read discovery-output.json: %v", err)
	}
	var disc DiscoveryOutput
	json.Unmarshal(data, &disc)
	if disc.Agent != "claude" {
		t.Errorf("agent = %q, want 'claude' (Claude-only fallback)", disc.Agent)
	}

	// No merged file should exist (only one succeeded).
	mergedPath := filepath.Join(specDir, "discovery-output-merged-v1.json")
	if _, err := os.Stat(mergedPath); !os.IsNotExist(err) {
		t.Error("merged file should not exist when only one provider succeeds")
	}

	if orch.sm.Current() != StateHumanGate1 {
		t.Errorf("state = %s, want HUMAN_GATE_1", orch.sm.Current())
	}
}

func TestDualDiscovery_ClaudeFails_CodexSurvives(t *testing.T) {
	codexOutput := testDiscoveryOutput("codex")
	claudeRunner := &discoveryMockRunner{failErr: os.ErrNotExist}
	codexRunner := &discoveryMockRunner{output: codexOutput}

	orch, specDir := makeTestOrchestrator(t, claudeRunner, codexRunner, true)

	goal := GoalInput{Title: "test", SourceDocPaths: []string{}}
	state := orch.sm.State()

	err := orch.handleDiscovery(goal, state, specDir)
	if err != nil {
		t.Fatalf("handleDiscovery failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(specDir, "discovery-output.json"))
	if err != nil {
		t.Fatalf("failed to read discovery-output.json: %v", err)
	}
	var disc DiscoveryOutput
	json.Unmarshal(data, &disc)
	if disc.Agent != "codex" {
		t.Errorf("agent = %q, want 'codex' (Codex-only fallback)", disc.Agent)
	}

	if orch.sm.Current() != StateHumanGate1 {
		t.Errorf("state = %s, want HUMAN_GATE_1", orch.sm.Current())
	}
}

func TestDualDiscovery_BothFail_Escalates(t *testing.T) {
	claudeRunner := &discoveryMockRunner{failErr: os.ErrNotExist}
	codexRunner := &discoveryMockRunner{failErr: os.ErrPermission}

	orch, specDir := makeTestOrchestrator(t, claudeRunner, codexRunner, true)

	goal := GoalInput{Title: "test", SourceDocPaths: []string{}}
	state := orch.sm.State()

	err := orch.handleDiscovery(goal, state, specDir)
	if err != nil {
		t.Fatalf("handleDiscovery returned error: %v", err)
	}

	// Should have escalated.
	current := orch.sm.Current()
	if current != StateEscalated {
		t.Errorf("state = %s, want ESCALATED", current)
	}
}

func TestDualDiscovery_Disabled_SingleProviderPath(t *testing.T) {
	claudeOutput := testDiscoveryOutput("claude")
	claudeRunner := &discoveryMockRunner{output: claudeOutput}

	// EnableCodexDiscovery=false -> single-provider path.
	orch, specDir := makeTestOrchestrator(t, claudeRunner, nil, false)

	goal := GoalInput{Title: "test", SourceDocPaths: []string{}}
	state := orch.sm.State()

	err := orch.handleDiscovery(goal, state, specDir)
	if err != nil {
		t.Fatalf("handleDiscovery failed: %v", err)
	}

	// Should produce discovery-output.json (not versioned per-provider).
	canonPath := filepath.Join(specDir, "discovery-output.json")
	if _, err := os.Stat(canonPath); os.IsNotExist(err) {
		t.Error("discovery-output.json not created in single-provider mode")
	}

	// Per-provider files should NOT exist.
	claudePath := filepath.Join(specDir, "discovery-output-claude-v1.json")
	if _, err := os.Stat(claudePath); !os.IsNotExist(err) {
		t.Error("discovery-output-claude-v1.json should not exist in single-provider mode")
	}

	if orch.sm.Current() != StateHumanGate1 {
		t.Errorf("state = %s, want HUMAN_GATE_1", orch.sm.Current())
	}
}

func TestDualDiscovery_VersionedFilenames(t *testing.T) {
	claudeOutput := testDiscoveryOutput("claude")
	codexOutput := testDiscoveryOutput("codex")

	claudeRunner := &discoveryMockRunner{output: claudeOutput}
	codexRunner := &discoveryMockRunner{output: codexOutput}

	orch, specDir := makeTestOrchestrator(t, claudeRunner, codexRunner, true)

	goal := GoalInput{Title: "test", SourceDocPaths: []string{}}
	state := orch.sm.State()

	err := orch.handleDiscovery(goal, state, specDir)
	if err != nil {
		t.Fatalf("handleDiscovery failed: %v", err)
	}

	// Check versioned filenames.
	expectedFiles := []string{
		"discovery-output-claude-v1.json",
		"discovery-output-codex-v1.json",
		"discovery-output-merged-v1.json",
		"discovery-output.json",
		"discovery-output-1.json",
	}
	for _, name := range expectedFiles {
		path := filepath.Join(specDir, name)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("expected file %s not found", name)
		}
	}
}

func TestDualDiscovery_MergedOutputProduced(t *testing.T) {
	claudeOutput := testDiscoveryOutput("claude")
	codexOutput := testDiscoveryOutput("codex")
	codexOutput.Actors = append(codexOutput.Actors, Actor{
		Name: "Admin", Type: "human", Description: "Admin user",
	})

	claudeRunner := &discoveryMockRunner{output: claudeOutput}
	codexRunner := &discoveryMockRunner{output: codexOutput}

	orch, specDir := makeTestOrchestrator(t, claudeRunner, codexRunner, true)

	goal := GoalInput{Title: "test", SourceDocPaths: []string{}}
	state := orch.sm.State()

	err := orch.handleDiscovery(goal, state, specDir)
	if err != nil {
		t.Fatalf("handleDiscovery failed: %v", err)
	}

	// Verify merged output file exists and is valid JSON.
	data, readErr := os.ReadFile(filepath.Join(specDir, "discovery-output.json"))
	if readErr != nil {
		t.Fatalf("failed to read merged output: %v", readErr)
	}
	var merged DiscoveryOutput
	if jsonErr := json.Unmarshal(data, &merged); jsonErr != nil {
		t.Fatalf("merged output is not valid JSON: %v", jsonErr)
	}
	if len(merged.Actors) == 0 {
		t.Error("merged output has no actors")
	}
	if merged.ProblemStatement == "" {
		t.Error("merged output has empty problem statement")
	}
}

func TestDualDiscovery_DifferingProblemStatements(t *testing.T) {
	claudeOutput := testDiscoveryOutput("claude")
	claudeOutput.ProblemStatement = "Build a dashboard for analytics"
	codexOutput := testDiscoveryOutput("codex")
	codexOutput.ProblemStatement = "Create an observability platform"

	claudeRunner := &discoveryMockRunner{output: claudeOutput}
	codexRunner := &discoveryMockRunner{output: codexOutput}

	orch, specDir := makeTestOrchestrator(t, claudeRunner, codexRunner, true)

	goal := GoalInput{Title: "test", SourceDocPaths: []string{}}
	state := orch.sm.State()

	err := orch.handleDiscovery(goal, state, specDir)
	if err != nil {
		t.Fatalf("handleDiscovery failed: %v", err)
	}

	// Verify merged output is valid (agent merge or mechanical fallback).
	data, _ := os.ReadFile(filepath.Join(specDir, "discovery-output.json"))
	var merged DiscoveryOutput
	json.Unmarshal(data, &merged)

	if merged.ProblemStatement == "" {
		t.Error("merged problem_statement is empty")
	}
}

func TestDualDiscovery_AgentTimeoutApplied(t *testing.T) {
	// Custom runner that records the timeout it receives.
	var claudeTimeout, codexTimeout int

	claudeRunner := &timeoutRecordingRunner{
		output:     testDiscoveryOutput("claude"),
		timeoutPtr: &claudeTimeout,
	}
	codexRunner := &timeoutRecordingRunner{
		output:     testDiscoveryOutput("codex"),
		timeoutPtr: &codexTimeout,
	}

	orch, specDir := makeTestOrchestrator(t, claudeRunner, codexRunner, true)
	orch.config.AgentTimeoutSeconds = 42

	goal := GoalInput{Title: "test", SourceDocPaths: []string{}}
	state := orch.sm.State()

	err := orch.handleDiscovery(goal, state, specDir)
	if err != nil {
		t.Fatalf("handleDiscovery failed: %v", err)
	}

	if claudeTimeout != 42 {
		t.Errorf("claude timeout = %d, want 42", claudeTimeout)
	}
	if codexTimeout != 42 {
		t.Errorf("codex timeout = %d, want 42", codexTimeout)
	}
}

// timeoutRecordingRunner records the timeout passed to Run.
type timeoutRecordingRunner struct {
	output     *DiscoveryOutput
	timeoutPtr *int
}

func (r *timeoutRecordingRunner) Run(prompt string, outputPath string, timeoutSeconds int) (int, string, float64, int64, error) {
	*r.timeoutPtr = timeoutSeconds
	if r.output != nil {
		data, _ := json.MarshalIndent(r.output, "", "  ")
		os.MkdirAll(filepath.Dir(outputPath), 0o755)
		os.WriteFile(outputPath, data, 0o644)
	}
	return 0, "", 0.01, 100, nil
}

func TestDualDiscovery_CodexUnavailable_FallsBackToSingle(t *testing.T) {
	// EnableCodexDiscovery=true but codexDiscoveryRunner is nil (codex not on PATH).
	claudeOutput := testDiscoveryOutput("claude")
	claudeRunner := &discoveryMockRunner{output: claudeOutput}

	orch, specDir := makeTestOrchestrator(t, claudeRunner, nil, true)
	// Simulate codex not being available (codexDiscoveryRunner already nil).

	goal := GoalInput{Title: "test", SourceDocPaths: []string{}}
	state := orch.sm.State()

	err := orch.handleDiscovery(goal, state, specDir)
	if err != nil {
		t.Fatalf("handleDiscovery failed: %v", err)
	}

	// Should use single-provider path.
	canonPath := filepath.Join(specDir, "discovery-output.json")
	if _, err := os.Stat(canonPath); os.IsNotExist(err) {
		t.Error("discovery-output.json not created")
	}

	if orch.sm.Current() != StateHumanGate1 {
		t.Errorf("state = %s, want HUMAN_GATE_1", orch.sm.Current())
	}
}

func TestDiscoveryOutputSchema_ValidJSON(t *testing.T) {
	var schema map[string]interface{}
	if err := json.Unmarshal(DiscoveryOutputSchema(), &schema); err != nil {
		t.Fatalf("DiscoveryOutputSchema is not valid JSON: %v", err)
	}
}

func TestDiscoveryOutputSchema_RequiredFields(t *testing.T) {
	var schema map[string]interface{}
	json.Unmarshal(DiscoveryOutputSchema(), &schema)

	required, ok := schema["required"].([]interface{})
	if !ok {
		t.Fatal("schema missing 'required' field")
	}

	expectedFields := []string{
		"schema_version", "agent", "actors", "problem_statement",
		"scope", "constraints", "integration_points", "priorities",
		"assumptions", "open_questions",
	}

	requiredSet := make(map[string]bool)
	for _, r := range required {
		requiredSet[r.(string)] = true
	}

	for _, f := range expectedFields {
		if !requiredSet[f] {
			t.Errorf("expected required field %q not found in schema", f)
		}
	}
}
