package specworkflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// orchMockRunner — test-only AgentRunner for orchestrator tests
// ---------------------------------------------------------------------------

// orchMockRunner implements AgentRunner for orchestrator tests. When Run is
// called, it writes a pre-configured JSON payload to the outputPath,
// simulating agent output without launching a real subprocess.
type orchMockRunner struct {
	mu       sync.Mutex
	outputs  map[string]interface{} // prompt substring -> output payload
	calls    []orchMockCall
	failNext bool
	failMsg  string
}

type orchMockCall struct {
	Prompt     string
	OutputPath string
}

func holdoutRoundFromOutputPath(outputPath string) int {
	base := filepath.Base(outputPath)
	marker := "-round-"
	idx := strings.LastIndex(base, marker)
	if idx == -1 {
		return 1
	}
	roundPart := strings.TrimSuffix(base[idx+len(marker):], filepath.Ext(base))
	round, err := strconv.Atoi(roundPart)
	if err != nil || round < 1 {
		return 1
	}
	return round
}

func newOrchMockRunner() *orchMockRunner {
	return &orchMockRunner{
		outputs: make(map[string]interface{}),
	}
}

// SetOutput configures the runner to write payload when prompt contains sub.
func (m *orchMockRunner) SetOutput(sub string, payload interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.outputs[sub] = payload
}

// SetFail configures the next Run call to return an error.
func (m *orchMockRunner) SetFail(msg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failNext = true
	m.failMsg = msg
}

// Run implements AgentRunner.
func (m *orchMockRunner) Run(prompt string, outputPath string, timeoutSeconds int) (int, string, float64, int64, error) {
	m.mu.Lock()
	m.calls = append(m.calls, orchMockCall{Prompt: prompt, OutputPath: outputPath})

	if m.failNext {
		m.failNext = false
		msg := m.failMsg
		m.mu.Unlock()
		return 1, msg, 0.01, 100, fmt.Errorf("%s", msg)
	}

	// Find matching output by prompt substring.
	var payload interface{}
	for sub, p := range m.outputs {
		if orchContains(prompt, sub) {
			payload = p
			break
		}
	}
	m.mu.Unlock()

	if payload == nil {
		if orchContains(prompt, "Holdout Agent") {
			round := holdoutRoundFromOutputPath(outputPath)
			mdPath := strings.Replace(filepath.Base(outputPath), "holdout-", "holdouts-", 1)
			mdPath = strings.TrimSuffix(mdPath, ".json") + ".md"
			mdPath = filepath.Join(filepath.Dir(outputPath), mdPath)

			os.MkdirAll(filepath.Dir(outputPath), 0o755)
			_ = os.WriteFile(mdPath, []byte("### Scenario H1\n\nExercise the edge cases.\n"), 0o644)
			holdout := HoldoutOutput{
				SchemaVersion: "1.0",
				Agent:         "holdout",
				Round:         round,
				ScenarioCount: 2,
				Categories:    []string{"edge-cases", "error-paths"},
				HoldoutFile:   mdPath,
			}
			data, _ := json.MarshalIndent(holdout, "", "  ")
			_ = os.WriteFile(outputPath, data, 0o644)
			return 0, "", 0.01, 100, nil
		}
		os.MkdirAll(filepath.Dir(outputPath), 0o755)
		os.WriteFile(outputPath, []byte(`{}`), 0o644)
		return 0, "", 0.01, 100, nil
	}

	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return 1, err.Error(), 0, 0, err
	}

	os.MkdirAll(filepath.Dir(outputPath), 0o755)
	if err := os.WriteFile(outputPath, data, 0o644); err != nil {
		return 1, err.Error(), 0, 0, err
	}

	return 0, "", 0.01, 100, nil
}

func orchContains(s, sub string) bool {
	if len(sub) == 0 || len(s) < len(sub) {
		return false
	}
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Orchestrator test helpers
// ---------------------------------------------------------------------------

func orchTestConfig() SpecWorkflowConfig {
	cfg := DefaultConfig()
	cfg.MaxRounds = 5
	cfg.MinRounds = 1
	cfg.MaxCostUSD = 100.0
	cfg.MaxWallClockMinutes = 120
	cfg.MaxTotalFindings = 100
	cfg.MaxRetries = 1
	cfg.MaxGateCorrections = 3
	cfg.EnableCodexReviewers = false // Disable codex in tests to avoid real CLI dependency
	return cfg
}

func orchDiscoveryOutput() *DiscoveryOutput {
	return &DiscoveryOutput{
		SchemaVersion:    "1.0",
		Agent:            "discovery",
		ProblemStatement: "Test problem statement",
		Actors: []Actor{
			{Name: "User", Type: "human", Description: "End user"},
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

func orchDrafterOutput(specDir string) *DrafterOutput {
	return &DrafterOutput{
		SchemaVersion: "1.0",
		Agent:         "drafter",
		SpecFile:      filepath.Join(specDir, "spec-v0.md"),
		HoldoutFile:   filepath.Join(specDir, "test-holdouts.md"),
		StructuralSummary: StructuralSummary{
			UserStoryCount:   2,
			BDDScenarioCount: 3,
			FRCount:          5,
			TestCount:        4,
		},
	}
}

func orchReviewerOutputWith(agent string, round int, findings []Finding) *ReviewerOutput {
	lenses := []string{"AMB", "INC"}
	switch agent {
	case "reviewer-consistency":
		lenses = []string{"CON", "FEA"}
	case "reviewer-security":
		lenses = []string{"SEC", "OPS"}
	case "reviewer-correctness":
		lenses = []string{"COR", "CPX"}
	}
	return &ReviewerOutput{
		SchemaVersion:       "1.0",
		Agent:               agent,
		Round:               round,
		LensesApplied:       lenses,
		Findings:            findings,
		StructuralIntegrity: StructuralIntegrity{Performed: true},
		MarkdownReportFile:  "report.md",
	}
}

func orchRevisionOutput(round int) *RevisionOutput {
	return &RevisionOutput{
		SchemaVersion:   "1.0",
		Agent:           "reviser",
		Round:           round,
		RevisedSpecFile: fmt.Sprintf("spec-v%d.md", round),
		Changes: []Change{
			{
				FindingID:        "CRIT-001",
				Action:           "revised",
				Description:      "Fixed critical issue",
				SectionsModified: []string{"section 1"},
			},
			{
				FindingID:        "MAJ-001",
				Action:           "revised",
				Description:      "Fixed major issue",
				SectionsModified: []string{"section 2"},
			},
		},
	}
}

func orchJudgeRevise(round int) *JudgeOutput {
	return &JudgeOutput{
		SchemaVersion:   "1.0",
		Agent:           "judge",
		Round:           round,
		Verdict:         VerdictRevise,
		Rationale:       "Needs more work",
		IssueUpdates:    []IssueUpdate{},
		StructuralDelta: StructuralDelta{RegressionsFound: false},
	}
}

func orchJudgePass(round int) *JudgeOutput {
	return &JudgeOutput{
		SchemaVersion: "1.0",
		Agent:         "judge",
		Round:         round,
		Verdict:       VerdictPass,
		Rationale:     "All issues resolved",
		IssueUpdates: []IssueUpdate{
			{FindingID: "CRIT-001", NewStatus: "verified", Explanation: "resolved"},
			{FindingID: "MAJ-001", NewStatus: "verified", Explanation: "resolved"},
		},
		StructuralDelta: StructuralDelta{RegressionsFound: false},
	}
}

// setupOrch creates an Orchestrator with an orchMockRunner for testing.
func setupOrch(t *testing.T) (*Orchestrator, *orchMockRunner, string) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
	workspace := t.TempDir()
	feature := "test-feature"
	config := orchTestConfig()

	runner := newOrchMockRunner()
	emitter := NewChannelEmitter(64)

	orch, err := NewOrchestrator(OrchestratorConfig{
		WorkspaceDir: workspace,
		FeatureName:  feature,
		Config:       config,
		Runner:       runner,
		Emitter:      emitter,
	})
	if err != nil {
		t.Fatalf("NewOrchestrator: %v", err)
	}

	// Inject dummy skill content so prompt builders work without real files.
	orch.skills = &SkillCache{
		contents: map[string]string{
			SpecTemplate:        "# Spec Template\nDummy spec template for testing.",
			BDDTemplate:         "# BDD Template\nDummy BDD template for testing.",
			TestDatasetTemplate: "# Test Dataset Template\nDummy test dataset template.",
			ReviewConstitution:  "# Review Constitution\nDummy constitution.",
			ReportTemplate:      "# Report Template\nDummy report template.",
		},
		checksums: map[string]string{
			"plan_spec":  "sha256:test",
			"grill_spec": "sha256:test",
		},
		loaded: true,
	}
	// Re-create prompt builder with the populated skills.
	orch.promptBuilder = NewPromptBuilder(orch.skills, workspace, feature)

	specDir := filepath.Join(workspace, "specs", feature)

	// Write a minimal spec-v0.md.
	os.WriteFile(filepath.Join(specDir, "spec-v0.md"), []byte("# Test Spec\n\nThis is a test spec.\n"), 0o644)

	return orch, runner, workspace
}

// orchSequentialRunner wraps orchMockRunner and returns different outputs
// on successive calls for specified agent types (judge, reviser, etc.).
type orchSequentialRunner struct {
	base             *orchMockRunner
	judgeOutputs     []interface{}
	reviserOutputs   []interface{}
	mu               sync.Mutex
	judgeCallCount   int
	reviserCallCount int
}

func (s *orchSequentialRunner) Run(prompt string, outputPath string, timeoutSeconds int) (int, string, float64, int64, error) {
	// Handle taskify/review prompts so tests that run through FINALIZED → TASKIFY → COMPLETE work.
	if orchContains(prompt, "Decompose this") || orchContains(prompt, "Revise this task graph") {
		os.MkdirAll(filepath.Dir(outputPath), 0o755)
		os.WriteFile(outputPath, validTaskGraphJSON(), 0o644)
		return 0, "", 0.01, 100, nil
	}
	if orchContains(prompt, "Task Graph Review") {
		out := &ReviewerOutput{
			SchemaVersion: "1.0", Agent: "task-reviewer", Round: 1,
			LensesApplied: []string{"completeness"},
			Findings: []Finding{{ID: "TF-1", Description: "Minor", Severity: SeverityMinor,
				Impact: "Low", Recommendation: "Fix", Lens: "scope", AffectedSection: "task-1"}},
			StructuralIntegrity: StructuralIntegrity{Performed: true,
				Checks: []IntegrityCheck{{Check: "dag-check", Result: "pass"}}},
			MarkdownReportFile: "/tmp/report.md",
		}
		data, _ := json.MarshalIndent(out, "", "  ")
		os.MkdirAll(filepath.Dir(outputPath), 0o755)
		os.WriteFile(outputPath, data, 0o644)
		return 0, "", 0.01, 100, nil
	}
	if orchContains(prompt, "Judge Agent") {
		s.mu.Lock()
		idx := s.judgeCallCount
		s.judgeCallCount++
		s.mu.Unlock()

		if idx < len(s.judgeOutputs) {
			data, _ := json.MarshalIndent(s.judgeOutputs[idx], "", "  ")
			os.MkdirAll(filepath.Dir(outputPath), 0o755)
			os.WriteFile(outputPath, data, 0o644)
			return 0, "", 0.01, 100, nil
		}
	}
	if orchContains(prompt, "Reviser Agent") && len(s.reviserOutputs) > 0 {
		s.mu.Lock()
		idx := s.reviserCallCount
		s.reviserCallCount++
		s.mu.Unlock()

		if idx < len(s.reviserOutputs) {
			data, _ := json.MarshalIndent(s.reviserOutputs[idx], "", "  ")
			os.MkdirAll(filepath.Dir(outputPath), 0o755)
			os.WriteFile(outputPath, data, 0o644)
			return 0, "", 0.01, 100, nil
		}
	}
	return s.base.Run(prompt, outputPath, timeoutSeconds)
}

// sendGateResponse retries sending a gate response until the orchestrator
// accepts it (i.e. a gate is waiting). This avoids timing-dependent failures
// where sleep durations don't match workflow speed.
func sendGateResponse(orch *Orchestrator, resp GateResponse) {
	for i := 0; i < 200; i++ {
		if err := orch.HandleGateResponse(resp); err == nil {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
}

type orchHoldoutRunner struct {
	invalidJSON  bool
	skipMarkdown bool
}

func (r orchHoldoutRunner) Run(_ string, outputPath string, _ int) (int, string, float64, int64, error) {
	round := holdoutRoundFromOutputPath(outputPath)
	mdPath := strings.Replace(filepath.Base(outputPath), "holdout-", "holdouts-", 1)
	mdPath = strings.TrimSuffix(mdPath, ".json") + ".md"
	mdPath = filepath.Join(filepath.Dir(outputPath), mdPath)

	if !r.skipMarkdown {
		if err := os.WriteFile(mdPath, []byte("### Scenario H1\n\nExercise the edge cases.\n"), 0o644); err != nil {
			return 1, err.Error(), 0, 0, err
		}
	}
	if r.invalidJSON {
		if err := os.WriteFile(outputPath, []byte(`{"broken":`), 0o644); err != nil {
			return 1, err.Error(), 0, 0, err
		}
		return 0, "", 0.01, 100, nil
	}

	data, _ := json.MarshalIndent(HoldoutOutput{
		SchemaVersion: "1.0",
		Agent:         "holdout",
		Round:         round,
		ScenarioCount: 2,
		Categories:    []string{"edge-cases", "error-paths"},
		HoldoutFile:   mdPath,
	}, "", "  ")
	if err := os.WriteFile(outputPath, data, 0o644); err != nil {
		return 1, err.Error(), 0, 0, err
	}
	return 0, "", 0.01, 100, nil
}

// ---------------------------------------------------------------------------
// Feature name validation tests
// ---------------------------------------------------------------------------

func TestFeatureName_ColonRejected(t *testing.T) {
	err := ValidateFeatureName("auth:login")
	if err == nil {
		t.Fatal("expected error for feature name with colon")
	}
	if !strings.Contains(err.Error(), "must match [a-zA-Z0-9_-]+") {
		t.Fatalf("error should mention pattern, got: %v", err)
	}
}

func TestFeatureName_SlashRejected(t *testing.T) {
	err := ValidateFeatureName("auth/login")
	if err == nil {
		t.Fatal("expected error for feature name with slash")
	}
	if !strings.Contains(err.Error(), "must match [a-zA-Z0-9_-]+") {
		t.Fatalf("error should mention pattern, got: %v", err)
	}
}

func TestFeatureName_ValidNames(t *testing.T) {
	valid := []string{"payment-disputes", "auth_flow", "MyFeature", "test123", "a", "A-b_c"}
	for _, name := range valid {
		if err := ValidateFeatureName(name); err != nil {
			t.Errorf("ValidateFeatureName(%q) = %v, want nil", name, err)
		}
	}

	invalid := []string{"", "has space", "auth:login", "auth/login", "hello@world", "semi;colon", "a.b"}
	for _, name := range invalid {
		if err := ValidateFeatureName(name); err == nil {
			t.Errorf("ValidateFeatureName(%q) = nil, want error", name)
		}
	}
}

// ---------------------------------------------------------------------------
// Lock file tests
// ---------------------------------------------------------------------------

func TestStartupLock_PreventsSecondProcess(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "test.workflow.lock")

	// Write a lock file with our own PID (which IS running).
	currentPID := os.Getpid()
	if err := os.WriteFile(lockPath, []byte(strconv.Itoa(currentPID)), 0o644); err != nil {
		t.Fatal(err)
	}

	// Attempting to acquire the lock should fail.
	err := acquireLock(lockPath)
	if err == nil {
		t.Fatal("expected error when lock file has a running PID")
	}
	if !strings.Contains(err.Error(), "Another orchestrator process may be running") {
		t.Fatalf("unexpected error message: %v", err)
	}
	if !strings.Contains(err.Error(), strconv.Itoa(currentPID)) {
		t.Fatalf("error should contain PID %d, got: %v", currentPID, err)
	}
}

func TestStartupLock_StaleLockAutoRemoved(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "test.workflow.lock")

	// Write a lock file with a PID that is definitely not running.
	// PID 2147483647 (max int32) is extremely unlikely to be a real process.
	stalePID := 2147483647
	if err := os.WriteFile(lockPath, []byte(strconv.Itoa(stalePID)), 0o644); err != nil {
		t.Fatal(err)
	}

	// Acquiring the lock should succeed — stale lock is auto-removed.
	if err := acquireLock(lockPath); err != nil {
		t.Fatalf("acquireLock failed for stale PID: %v", err)
	}

	// Verify the lock file now contains our PID.
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("failed to read lock file: %v", err)
	}
	gotPID, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	if gotPID != os.Getpid() {
		t.Errorf("lock file PID = %d, want %d", gotPID, os.Getpid())
	}
}

func TestStartupLock_ReleasedOnCleanExit(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "test.workflow.lock")

	// Acquire the lock.
	if err := acquireLock(lockPath); err != nil {
		t.Fatal(err)
	}
	// Verify it exists.
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatal("lock file should exist after acquire")
	}

	// Release the lock.
	releaseLock(lockPath)

	// Verify it's gone.
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Error("lock file should be removed after release")
	}
}

func TestStartupLock_NoLockFileAcquiresCleanly(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "test.workflow.lock")

	// No pre-existing lock file — acquire should succeed.
	if err := acquireLock(lockPath); err != nil {
		t.Fatalf("acquireLock failed: %v", err)
	}

	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("lock file not created: %v", err)
	}
	gotPID, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	if gotPID != os.Getpid() {
		t.Errorf("lock file PID = %d, want %d", gotPID, os.Getpid())
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestDispatchHoldoutGeneration_WritesRoundArtifacts(t *testing.T) {
	orch, _, workspace := setupOrch(t)
	specDir := filepath.Join(workspace, "specs", "test-feature")
	state := orch.sm.State()
	state.Round = 1
	state.FindingsSummary = FindingsSummary{OpenCritical: 1}
	orch.runner = orchHoldoutRunner{}

	merged := &MergedFindings{TotalAfterDedup: 1}
	if err := orch.dispatchHoldoutGeneration(state, specDir, merged); err != nil {
		t.Fatalf("dispatchHoldoutGeneration: %v", err)
	}

	if _, err := os.Stat(filepath.Join(specDir, "holdouts-round-1.md")); err != nil {
		t.Fatalf("expected merged holdout markdown: %v", err)
	}
	jsonPath := filepath.Join(specDir, "holdout-round-1.json")
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("expected merged holdout JSON: %v", err)
	}
	var output HoldoutOutput
	if err := json.Unmarshal(data, &output); err != nil {
		t.Fatalf("unmarshal merged holdout JSON: %v", err)
	}
	if output.HoldoutFile != filepath.Join(specDir, "holdouts-round-1.md") {
		t.Fatalf("unexpected holdout_file: %s", output.HoldoutFile)
	}
}

func TestDispatchHoldoutGeneration_FailsWhenMarkdownMissing(t *testing.T) {
	orch, _, workspace := setupOrch(t)
	specDir := filepath.Join(workspace, "specs", "test-feature")
	state := orch.sm.State()
	state.Round = 1
	state.FindingsSummary = FindingsSummary{OpenCritical: 1}
	orch.runner = orchHoldoutRunner{skipMarkdown: true}

	err := orch.dispatchHoldoutGeneration(state, specDir, &MergedFindings{TotalAfterDedup: 1})
	if err == nil {
		t.Fatal("expected holdout generation to fail when markdown is missing")
	}
}

func TestDispatchHoldoutGeneration_FailsWhenJSONInvalid(t *testing.T) {
	orch, _, workspace := setupOrch(t)
	specDir := filepath.Join(workspace, "specs", "test-feature")
	state := orch.sm.State()
	state.Round = 1
	state.FindingsSummary = FindingsSummary{OpenCritical: 1}
	orch.runner = orchHoldoutRunner{invalidJSON: true}

	err := orch.dispatchHoldoutGeneration(state, specDir, &MergedFindings{TotalAfterDedup: 1})
	if err == nil {
		t.Fatal("expected holdout generation to fail when JSON is invalid")
	}
}

func TestWriteRevisionFindingsInput_ExcludesHoldoutTargets(t *testing.T) {
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "merged-findings-round-2.json")
	outputPath := filepath.Join(dir, "merged-findings-spec-round-2.json")

	merged := MergedFindings{
		SchemaVersion:   "1.0",
		Round:           2,
		Timestamp:       time.Now().UTC().Format(time.RFC3339),
		TotalFindings:   2,
		TotalAfterDedup: 2,
		Findings: []MergedFinding{
			{ID: "CRIT-001", AffectedSection: "FR-1", Lens: "SEC", Description: "spec issue", Impact: "high", Recommendation: "fix", Severity: SeverityCritical, Target: "spec"},
			{ID: "MAJ-001", AffectedSection: "H1", Lens: "COR", Description: "holdout issue", Impact: "medium", Recommendation: "review", Severity: SeverityMajor, Target: "holdout"},
		},
	}
	data, _ := json.MarshalIndent(merged, "", "  ")
	if err := os.WriteFile(sourcePath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := writeRevisionFindingsInput(sourcePath, outputPath, nil); err != nil {
		t.Fatalf("writeRevisionFindingsInput: %v", err)
	}

	out, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	var filtered MergedFindings
	if err := json.Unmarshal(out, &filtered); err != nil {
		t.Fatalf("unmarshal filtered findings: %v", err)
	}
	if len(filtered.Findings) != 1 {
		t.Fatalf("expected 1 spec finding, got %d", len(filtered.Findings))
	}
	if filtered.Findings[0].ID != "CRIT-001" {
		t.Fatalf("unexpected surviving finding: %s", filtered.Findings[0].ID)
	}
}

func TestOrchestratorHappyPath2Rounds(t *testing.T) {
	orch, runner, workspace := setupOrch(t)
	feature := "test-feature"
	specDir := filepath.Join(workspace, "specs", feature)

	// Configure outputs.
	runner.SetOutput("Discovery Agent", orchDiscoveryOutput())
	runner.SetOutput("Drafter Agent", orchDrafterOutput(specDir))

	critFindings := []Finding{
		{
			ID: "F-001", Description: "Critical issue", Severity: SeverityCritical,
			Impact: "high", Recommendation: "fix it", Lens: "AMB",
			AffectedSection: "section 1",
		},
		{
			ID: "F-002", Description: "Major issue", Severity: SeverityMajor,
			Impact: "medium", Recommendation: "fix it too", Lens: "CON",
			AffectedSection: "section 2",
		},
	}
	runner.SetOutput("Reviewer Agent", orchReviewerOutputWith("reviewer", 1, critFindings))

	// Judge: round 1 = REVISE, round 2 = PASS.
	// Reviser: round 1 addresses findings, round 2 has no changes (findings
	// were already addressed).
	wrapper := &orchSequentialRunner{
		base: runner,
		judgeOutputs: []interface{}{
			orchJudgeRevise(1),
			orchJudgePass(2),
		},
		reviserOutputs: []interface{}{
			orchRevisionOutput(1),
			&RevisionOutput{
				SchemaVersion:   "1.0",
				Agent:           "reviser",
				Round:           2,
				RevisedSpecFile: "spec-v2.md",
				Changes: []Change{
					{
						FindingID:        "CRIT-001",
						Action:           "verified",
						Description:      "Already addressed in round 1",
						SectionsModified: []string{"section 1"},
					},
					{
						FindingID:        "MAJ-001",
						Action:           "verified",
						Description:      "Already addressed in round 1",
						SectionsModified: []string{"section 2"},
					},
				},
			},
		},
	}
	orch.runner = wrapper

	// Ensure .tasks directory exists for taskify stage.
	os.MkdirAll(filepath.Join(workspace, ".tasks"), 0o755)

	// Write spec files for both rounds.
	os.WriteFile(filepath.Join(specDir, "spec-v1.md"), []byte("# Revised Spec v1\n"), 0o644)
	os.WriteFile(filepath.Join(specDir, "spec-v2.md"), []byte("# Revised Spec v2\n"), 0o644)

	errCh := make(chan error, 1)
	go func() {
		errCh <- orch.RunWorkflow(GoalInput{
			Title:       "Test Feature",
			Description: "A test feature",
		})
	}()

	// Feed gate responses in a separate goroutine to avoid timing issues.
	// The workflow will block on each gate and consume them in order.
	// Use retries to handle the buffered channel (capacity 1) reliably.
	go func() {
		// Gate 1: confirm.
		sendGateResponse(orch, GateResponse{Action: "confirm"})

		// Gate 2: confirm.
		sendGateResponse(orch, GateResponse{Action: "confirm"})

		// HUMAN_GATE_FINAL: accept (since critical findings → gate required).
		sendGateResponse(orch, GateResponse{Action: "accept"})

		// Task human gate: approve tasks.
		sendGateResponse(orch, GateResponse{Action: "approve"})
	}()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("RunWorkflow returned error: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("RunWorkflow timed out")
	}

	finalState := orch.sm.State()
	if finalState.State != StateComplete {
		t.Errorf("expected COMPLETE, got %s", finalState.State)
	}

	// Verify debate trail was written.
	debateTrailPath := filepath.Join(specDir, "debate-trail.md")
	if _, err := os.Stat(debateTrailPath); os.IsNotExist(err) {
		t.Error("debate trail file not created")
	}

	// Verify AssembleFinalSpec produced spec-final.md.
	finalSpecPath := filepath.Join(specDir, "spec-final.md")
	if _, err := os.Stat(finalSpecPath); os.IsNotExist(err) {
		t.Error("spec-final.md not created by AssembleFinalSpec")
	} else {
		content, err := os.ReadFile(finalSpecPath)
		if err != nil {
			t.Fatalf("failed to read spec-final.md: %v", err)
		}
		// AssembleFinalSpec should include convergence summary, not just a bare copy.
		if !orchContains(string(content), "Convergence Summary") {
			t.Error("spec-final.md missing Convergence Summary section from AssembleFinalSpec")
		}
		if !orchContains(string(content), "Accepted Risks") {
			t.Error("spec-final.md missing Accepted Risks section from AssembleFinalSpec")
		}
	}
}

func TestOrchestratorZeroCriticalPath(t *testing.T) {
	orch, runner, workspace := setupOrch(t)
	feature := "test-feature"
	specDir := filepath.Join(workspace, "specs", feature)

	// Ensure .tasks directory exists for taskify stage.
	os.MkdirAll(filepath.Join(workspace, ".tasks"), 0o755)

	runner.SetOutput("Discovery Agent", orchDiscoveryOutput())
	runner.SetOutput("Drafter Agent", orchDrafterOutput(specDir))

	// Only MINOR findings — no CRITICAL/MAJOR.
	minorFindings := []Finding{
		{
			ID: "F-001", Description: "Minor style issue", Severity: SeverityMinor,
			Impact: "low", Recommendation: "consider fixing", Lens: "AMB",
			AffectedSection: "section 1",
		},
	}
	runner.SetOutput("Reviewer Agent", orchReviewerOutputWith("reviewer", 1, minorFindings))

	// Judge passes immediately.
	runner.SetOutput("Judge Agent", &JudgeOutput{
		SchemaVersion:   "1.0",
		Agent:           "judge",
		Round:           1,
		Verdict:         VerdictPass,
		Rationale:       "No critical issues found",
		IssueUpdates:    []IssueUpdate{},
		StructuralDelta: StructuralDelta{RegressionsFound: false},
	})

	// Wrap with taskify/review-aware runner.
	taskRunner := &regressionTaskGraphRunner{
		base:         runner,
		taskGraphDir: filepath.Join(workspace, ".tasks"),
		featureName:  feature,
	}
	orch.runner = taskRunner

	errCh := make(chan error, 1)
	go func() {
		errCh <- orch.RunWorkflow(GoalInput{
			Title:       "Test Feature",
			Description: "A test feature",
		})
	}()

	go func() {
		// Gate 1: confirm.
		sendGateResponse(orch, GateResponse{Action: "confirm"})
		// Gate 2: confirm.
		sendGateResponse(orch, GateResponse{Action: "confirm"})
		// Task human gate: approve.
		sendGateResponse(orch, GateResponse{Action: "approve"})
	}()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("RunWorkflow returned error: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("RunWorkflow timed out")
	}

	finalState := orch.sm.State()
	if finalState.State != StateComplete {
		t.Errorf("expected COMPLETE, got %s", finalState.State)
	}
	if finalState.HadCriticalFindings {
		t.Error("expected HadCriticalFindings=false for zero-critical path")
	}

	// Verify AssembleFinalSpec produced spec-final.md.
	finalSpecPath := filepath.Join(specDir, "spec-final.md")
	if _, err := os.Stat(finalSpecPath); os.IsNotExist(err) {
		t.Error("spec-final.md not created by AssembleFinalSpec")
	}
}

func TestOrchestratorCancellation(t *testing.T) {
	orch, runner, workspace := setupOrch(t)
	feature := "test-feature"
	specDir := filepath.Join(workspace, "specs", feature)

	runner.SetOutput("Discovery Agent", orchDiscoveryOutput())
	runner.SetOutput("Drafter Agent", orchDrafterOutput(specDir))

	errCh := make(chan error, 1)
	go func() {
		errCh <- orch.RunWorkflow(GoalInput{
			Title:       "Test Feature",
			Description: "A test feature",
		})
	}()

	// Confirm gate 1.
	time.Sleep(50 * time.Millisecond)
	orch.HandleGateResponse(GateResponse{Action: "confirm"})

	// Cancel before gate 2 response.
	time.Sleep(50 * time.Millisecond)
	orch.Cancel()

	// Send gate 2 response to unblock.
	time.Sleep(50 * time.Millisecond)
	orch.HandleGateResponse(GateResponse{Action: "confirm"})

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected error from cancellation, got nil")
		}
		if !orchContains(err.Error(), "cancelled") {
			t.Errorf("expected cancellation error, got: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RunWorkflow timed out")
	}
}

func TestOrchestratorConcurrentRejection(t *testing.T) {
	orch, runner, _ := setupOrch(t)

	runner.SetOutput("Discovery Agent", orchDiscoveryOutput())

	// Start first workflow.
	errCh1 := make(chan error, 1)
	go func() {
		errCh1 <- orch.RunWorkflow(GoalInput{
			Title:       "Test Feature",
			Description: "A test feature",
		})
	}()

	// Give it time to acquire the lock.
	time.Sleep(50 * time.Millisecond)

	// Start second workflow — should be rejected.
	errCh2 := make(chan error, 1)
	go func() {
		errCh2 <- orch.RunWorkflow(GoalInput{
			Title:       "Test Feature 2",
			Description: "Should be rejected",
		})
	}()

	select {
	case err := <-errCh2:
		if err == nil {
			t.Fatal("expected concurrent rejection error, got nil")
		}
		if !orchContains(err.Error(), "already running") {
			t.Errorf("expected 'already running' error, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent workflow rejection timed out")
	}

	// Clean up first workflow.
	orch.Cancel()
	time.Sleep(50 * time.Millisecond)
	orch.HandleGateResponse(GateResponse{Action: "cancel"})
	select {
	case <-errCh1:
	case <-time.After(5 * time.Second):
	}
}

func TestOrchestratorCircuitBreakerMaxRounds(t *testing.T) {
	orch, runner, workspace := setupOrch(t)
	feature := "test-feature"
	specDir := filepath.Join(workspace, "specs", feature)

	// Set max rounds to 1.
	orch.config.MaxRounds = 1

	runner.SetOutput("Discovery Agent", orchDiscoveryOutput())
	runner.SetOutput("Drafter Agent", orchDrafterOutput(specDir))

	critFindings := []Finding{
		{
			ID: "F-001", Description: "Critical issue", Severity: SeverityCritical,
			Impact: "high", Recommendation: "fix it", Lens: "AMB",
			AffectedSection: "section 1",
		},
	}
	runner.SetOutput("Reviewer Agent", orchReviewerOutputWith("reviewer", 1, critFindings))
	runner.SetOutput("Reviser Agent", orchRevisionOutput(1))
	runner.SetOutput("Judge Agent", orchJudgeRevise(1))

	os.WriteFile(filepath.Join(specDir, "spec-v1.md"), []byte("# Spec v1\n"), 0o644)

	errCh := make(chan error, 1)
	go func() {
		errCh <- orch.RunWorkflow(GoalInput{
			Title:       "Test Feature",
			Description: "A test feature",
		})
	}()

	// Gate 1: confirm.
	time.Sleep(50 * time.Millisecond)
	orch.HandleGateResponse(GateResponse{Action: "confirm"})

	// Gate 2: confirm.
	time.Sleep(50 * time.Millisecond)
	orch.HandleGateResponse(GateResponse{Action: "confirm"})

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("RunWorkflow returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RunWorkflow timed out")
	}

	finalState := orch.sm.State()
	if finalState.State != StateEscalated {
		t.Errorf("expected ESCALATED from circuit breaker, got %s", finalState.State)
	}
}

func TestOrchestratorGateConfirm(t *testing.T) {
	orch, runner, workspace := setupOrch(t)
	feature := "test-feature"
	specDir := filepath.Join(workspace, "specs", feature)

	os.MkdirAll(filepath.Join(workspace, ".tasks"), 0o755)

	runner.SetOutput("Discovery Agent", orchDiscoveryOutput())
	runner.SetOutput("Drafter Agent", orchDrafterOutput(specDir))
	runner.SetOutput("Reviewer Agent", orchReviewerOutputWith("reviewer", 1, nil))
	runner.SetOutput("Judge Agent", &JudgeOutput{
		SchemaVersion:   "1.0",
		Agent:           "judge",
		Round:           1,
		Verdict:         VerdictPass,
		Rationale:       "All good",
		StructuralDelta: StructuralDelta{RegressionsFound: false},
	})

	taskRunner := &regressionTaskGraphRunner{
		base:         runner,
		taskGraphDir: filepath.Join(workspace, ".tasks"),
		featureName:  feature,
	}
	orch.runner = taskRunner

	errCh := make(chan error, 1)
	go func() {
		errCh <- orch.RunWorkflow(GoalInput{
			Title:       "Test Feature",
			Description: "Test",
		})
	}()

	go func() {
		sendGateResponse(orch, GateResponse{Action: "confirm"})
		sendGateResponse(orch, GateResponse{Action: "confirm"})
		sendGateResponse(orch, GateResponse{Action: "approve"})
	}()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("RunWorkflow returned error: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("timeout")
	}

	if orch.sm.State().State != StateComplete {
		t.Errorf("expected COMPLETE, got %s", orch.sm.State().State)
	}
	_ = specDir
}

func TestOrchestratorGateCancel(t *testing.T) {
	orch, runner, workspace := setupOrch(t)
	feature := "test-feature"
	specDir := filepath.Join(workspace, "specs", feature)

	runner.SetOutput("Discovery Agent", orchDiscoveryOutput())

	errCh := make(chan error, 1)
	go func() {
		errCh <- orch.RunWorkflow(GoalInput{
			Title:       "Test Feature",
			Description: "Test",
		})
	}()

	// Gate 1: cancel.
	time.Sleep(50 * time.Millisecond)
	orch.HandleGateResponse(GateResponse{Action: "cancel"})

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("RunWorkflow returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timeout")
	}

	if orch.sm.State().State != StateEscalated {
		t.Errorf("expected ESCALATED from cancel, got %s", orch.sm.State().State)
	}

	// Verify escalation summary was written.
	escalationPath := filepath.Join(specDir, "escalation-summary.md")
	if _, err := os.Stat(escalationPath); os.IsNotExist(err) {
		t.Error("escalation-summary.md not created")
	} else {
		content, err := os.ReadFile(escalationPath)
		if err != nil {
			t.Fatalf("failed to read escalation-summary.md: %v", err)
		}
		contentStr := string(content)
		if !orchContains(contentStr, "Escalation Summary") {
			t.Error("escalation-summary.md missing header")
		}
		if !orchContains(contentStr, "Findings Summary") {
			t.Error("escalation-summary.md missing Findings Summary section")
		}
		if !orchContains(contentStr, "Escalation Reason") {
			t.Error("escalation-summary.md missing Escalation Reason section")
		}
	}
}

func TestOrchestratorGateCorrect(t *testing.T) {
	orch, runner, workspace := setupOrch(t)
	feature := "test-feature"
	specDir := filepath.Join(workspace, "specs", feature)

	os.MkdirAll(filepath.Join(workspace, ".tasks"), 0o755)

	runner.SetOutput("Discovery Agent", orchDiscoveryOutput())
	runner.SetOutput("Drafter Agent", orchDrafterOutput(specDir))
	runner.SetOutput("Reviewer Agent", orchReviewerOutputWith("reviewer", 1, nil))
	runner.SetOutput("Judge Agent", &JudgeOutput{
		SchemaVersion:   "1.0",
		Agent:           "judge",
		Round:           1,
		Verdict:         VerdictPass,
		Rationale:       "All good",
		StructuralDelta: StructuralDelta{RegressionsFound: false},
	})

	taskRunner := &regressionTaskGraphRunner{
		base:         runner,
		taskGraphDir: filepath.Join(workspace, ".tasks"),
		featureName:  feature,
	}
	orch.runner = taskRunner

	errCh := make(chan error, 1)
	go func() {
		errCh <- orch.RunWorkflow(GoalInput{
			Title:       "Test Feature",
			Description: "Test",
		})
	}()

	go func() {
		// Gate 1: correct (goes back to DISCOVERY).
		corrections := map[string]string{"scope": "add feature C"}
		sendGateResponse(orch, GateResponse{Action: "correct", Data: corrections})

		// Gate 1 again (after re-discovery): confirm.
		sendGateResponse(orch, GateResponse{Action: "confirm"})

		// Gate 2: confirm.
		sendGateResponse(orch, GateResponse{Action: "confirm"})

		// Task human gate: approve.
		sendGateResponse(orch, GateResponse{Action: "approve"})
	}()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("RunWorkflow returned error: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("timeout")
	}

	if orch.sm.State().State != StateComplete {
		t.Errorf("expected COMPLETE, got %s", orch.sm.State().State)
	}
	if orch.sm.State().Gate1CorrectionCount != 1 {
		t.Errorf("expected Gate1CorrectionCount=1, got %d", orch.sm.State().Gate1CorrectionCount)
	}
}

// ---------------------------------------------------------------------------
// Resume from gate state tests
// ---------------------------------------------------------------------------

func TestOrchestratorResumeFromGate1(t *testing.T) {
	// Simulate: a workflow ran discovery, reached HUMAN_GATE_1, then the
	// server restarted. A new orchestrator should restore the persisted
	// state and resume from HUMAN_GATE_1, accepting a gate response.
	t.Setenv("PATH", t.TempDir())
	workspace := t.TempDir()
	feature := "test-feature"
	specDir := filepath.Join(workspace, "specs", feature)
	os.MkdirAll(specDir, 0o755)
	os.MkdirAll(filepath.Join(workspace, ".tasks"), 0o755)

	// Write discovery output (required for gate 1).
	disco := orchDiscoveryOutput()
	discoData, _ := json.MarshalIndent(disco, "", "  ")
	os.WriteFile(filepath.Join(specDir, "discovery-output.json"), discoData, 0o644)

	// Write persisted state at HUMAN_GATE_1.
	now := time.Now().UTC().Format(time.RFC3339)
	persistedState := &WorkflowStateJSON{
		State:       StateHumanGate1,
		Round:       1,
		FeatureName: feature,
		StartedAt:   now,
		UpdatedAt:   now,
	}
	if err := SaveState(specDir, persistedState); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	// Create a new orchestrator — it should restore from disk.
	config := orchTestConfig()
	runner := newOrchMockRunner()
	emitter := NewChannelEmitter(64)

	// Set up outputs for post-gate stages.
	runner.SetOutput("Drafter Agent", orchDrafterOutput(specDir))
	runner.SetOutput("Reviewer Agent", orchReviewerOutputWith("reviewer", 1, nil))
	runner.SetOutput("Judge Agent", &JudgeOutput{
		SchemaVersion:   "1.0",
		Agent:           "judge",
		Round:           1,
		Verdict:         VerdictPass,
		Rationale:       "All good",
		StructuralDelta: StructuralDelta{RegressionsFound: false},
	})

	orch, err := NewOrchestrator(OrchestratorConfig{
		WorkspaceDir: workspace,
		FeatureName:  feature,
		Config:       config,
		Runner:       runner,
		Emitter:      emitter,
	})
	if err != nil {
		t.Fatalf("NewOrchestrator: %v", err)
	}

	// Inject dummy skills.
	orch.skills = &SkillCache{
		contents: map[string]string{
			SpecTemplate:        "# Spec Template\nDummy.",
			BDDTemplate:         "# BDD Template\nDummy.",
			TestDatasetTemplate: "# Test Dataset Template\nDummy.",
			ReviewConstitution:  "# Review Constitution\nDummy.",
			ReportTemplate:      "# Report Template\nDummy.",
		},
		checksums: map[string]string{
			"plan_spec":  "sha256:test",
			"grill_spec": "sha256:test",
		},
		loaded: true,
	}
	orch.promptBuilder = NewPromptBuilder(orch.skills, workspace, feature)
	os.WriteFile(filepath.Join(specDir, "spec-v0.md"), []byte("# Test Spec\n"), 0o644)

	// Wrap runner with taskify/review support.
	taskRunner := &regressionTaskGraphRunner{
		base:         runner,
		taskGraphDir: filepath.Join(workspace, ".tasks"),
		featureName:  feature,
	}
	orch.runner = taskRunner

	// Verify the orchestrator restored the gate state.
	if orch.sm.Current() != StateHumanGate1 {
		t.Fatalf("expected restored state HUMAN_GATE_1, got %s", orch.sm.Current())
	}

	// Run workflow — it should enter handleHumanGate1 and wait on gateCh.
	errCh := make(chan error, 1)
	go func() {
		errCh <- orch.RunWorkflow(GoalInput{
			Title:       feature,
			Description: "Resumed from gate",
		})
	}()

	go func() {
		sendGateResponse(orch, GateResponse{Action: "confirm"})
		sendGateResponse(orch, GateResponse{Action: "confirm"})
		sendGateResponse(orch, GateResponse{Action: "approve"})
	}()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("RunWorkflow returned error: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("RunWorkflow timed out")
	}

	if orch.sm.State().State != StateComplete {
		t.Errorf("expected COMPLETE, got %s", orch.sm.State().State)
	}
}

func TestOrchestratorResumeFromGate2(t *testing.T) {
	// Simulate: workflow reached HUMAN_GATE_2, server restarted.
	t.Setenv("PATH", t.TempDir())
	workspace := t.TempDir()
	feature := "test-feature"
	specDir := filepath.Join(workspace, "specs", feature)
	os.MkdirAll(specDir, 0o755)
	os.MkdirAll(filepath.Join(workspace, ".tasks"), 0o755)

	// Write drafter output (required for gate 2).
	drafter := orchDrafterOutput(specDir)
	drafterData, _ := json.MarshalIndent(drafter, "", "  ")
	os.WriteFile(filepath.Join(specDir, "drafter-output.json"), drafterData, 0o644)
	os.WriteFile(filepath.Join(specDir, "spec-v0.md"), []byte("# Test Spec\n"), 0o644)

	// Write persisted state at HUMAN_GATE_2.
	now := time.Now().UTC().Format(time.RFC3339)
	persistedState := &WorkflowStateJSON{
		State:       StateHumanGate2,
		Round:       1,
		FeatureName: feature,
		StartedAt:   now,
		UpdatedAt:   now,
	}
	if err := SaveState(specDir, persistedState); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	config := orchTestConfig()
	runner := newOrchMockRunner()
	emitter := NewChannelEmitter(64)

	runner.SetOutput("Reviewer Agent", orchReviewerOutputWith("reviewer", 1, nil))
	runner.SetOutput("Judge Agent", &JudgeOutput{
		SchemaVersion:   "1.0",
		Agent:           "judge",
		Round:           1,
		Verdict:         VerdictPass,
		Rationale:       "All good",
		StructuralDelta: StructuralDelta{RegressionsFound: false},
	})

	orch, err := NewOrchestrator(OrchestratorConfig{
		WorkspaceDir: workspace,
		FeatureName:  feature,
		Config:       config,
		Runner:       runner,
		Emitter:      emitter,
	})
	if err != nil {
		t.Fatalf("NewOrchestrator: %v", err)
	}

	orch.skills = &SkillCache{
		contents: map[string]string{
			SpecTemplate:        "# Spec Template\nDummy.",
			BDDTemplate:         "# BDD Template\nDummy.",
			TestDatasetTemplate: "# Test Dataset Template\nDummy.",
			ReviewConstitution:  "# Review Constitution\nDummy.",
			ReportTemplate:      "# Report Template\nDummy.",
		},
		checksums: map[string]string{
			"plan_spec":  "sha256:test",
			"grill_spec": "sha256:test",
		},
		loaded: true,
	}
	orch.promptBuilder = NewPromptBuilder(orch.skills, workspace, feature)

	// Wrap runner with taskify/review support.
	taskRunner := &regressionTaskGraphRunner{
		base:         runner,
		taskGraphDir: filepath.Join(workspace, ".tasks"),
		featureName:  feature,
	}
	orch.runner = taskRunner

	// Verify the orchestrator restored the gate state.
	if orch.sm.Current() != StateHumanGate2 {
		t.Fatalf("expected restored state HUMAN_GATE_2, got %s", orch.sm.Current())
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- orch.RunWorkflow(GoalInput{
			Title:       feature,
			Description: "Resumed from gate 2",
		})
	}()

	go func() {
		// Gate 2: confirm — should proceed to REVIEWING.
		sendGateResponse(orch, GateResponse{Action: "confirm"})
		// Task human gate: approve.
		sendGateResponse(orch, GateResponse{Action: "approve"})
	}()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("RunWorkflow returned error: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("RunWorkflow timed out")
	}

	if orch.sm.State().State != StateComplete {
		t.Errorf("expected COMPLETE, got %s", orch.sm.State().State)
	}
}

func TestOrchestratorNoStateFile_StartsFromInit(t *testing.T) {
	// When no workflow-state.json exists, orchestrator should start from INIT.
	workspace := t.TempDir()
	feature := "fresh-feature"

	config := orchTestConfig()
	runner := newOrchMockRunner()
	emitter := NewChannelEmitter(64)

	orch, err := NewOrchestrator(OrchestratorConfig{
		WorkspaceDir: workspace,
		FeatureName:  feature,
		Config:       config,
		Runner:       runner,
		Emitter:      emitter,
	})
	if err != nil {
		t.Fatalf("NewOrchestrator: %v", err)
	}

	if orch.sm.Current() != StateInit {
		t.Errorf("expected INIT for fresh feature, got %s", orch.sm.Current())
	}
}
