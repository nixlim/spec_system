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

// taskReviewMockRunner is a mock AgentRunner that writes a ReviewerOutput to
// the output path and allows test control over success/failure.
type taskReviewMockRunner struct {
	output    *ReviewerOutput
	fail      bool
	failError string
	cost      float64
	callCount atomic.Int32
}

func (r *taskReviewMockRunner) Run(prompt string, outputPath string, timeoutSeconds int) (int, string, float64, int64, error) {
	r.callCount.Add(1)
	if r.fail {
		return 1, r.failError, r.cost, 100, fmt.Errorf("%s", r.failError)
	}
	data, _ := json.MarshalIndent(r.output, "", "  ")
	os.MkdirAll(filepath.Dir(outputPath), 0o755)
	os.WriteFile(outputPath, data, 0o644)
	return 0, "", r.cost, 100, nil
}

func makeTaskReviewOutput(agent string, findings []Finding) *ReviewerOutput {
	return &ReviewerOutput{
		SchemaVersion: "1.0",
		Agent:         agent,
		Round:         1,
		LensesApplied: []string{"completeness", "dependencies"},
		Findings:      findings,
		StructuralIntegrity: StructuralIntegrity{
			Performed: true,
			Checks:    []IntegrityCheck{{Check: "dag-check", Result: "pass"}},
		},
		MarkdownReportFile: "/tmp/report.md",
	}
}

// taskReviewNoopEmitter discards events (used in task review tests).
type taskReviewNoopEmitter struct{}

func (e *taskReviewNoopEmitter) Emit(event EventEnvelope) error { return nil }

func setupTaskReviewOrchestrator(t *testing.T, claudeRunner, codexRunner AgentRunner, taskReviewMaxRounds int) (*Orchestrator, string, *WorkflowStateJSON) {
	t.Helper()
	dir := t.TempDir()
	specDir := filepath.Join(dir, "specs", "test-feature")
	tasksDir := filepath.Join(dir, ".tasks")
	os.MkdirAll(specDir, 0o755)
	os.MkdirAll(tasksDir, 0o755)

	// Write a valid task graph.
	taskGraph := `{"version":"1.0","tasks":[{"task_id":"task-1","task_name":"Task 1","goal":"Do thing","inputs":[],"outputs":[],"acceptance":["Done"],"depends_on":[],"constraints":[],"files_scope":[],"priority":"high","estimate":"small"}]}`
	os.WriteFile(filepath.Join(tasksDir, "test-feature.task.json"), []byte(taskGraph), 0o644)

	state := &WorkflowStateJSON{
		State:       StateTaskReview,
		Round:       1,
		FeatureName: "test-feature",
		StartedAt:   "2026-03-25T10:00:00Z",
		UpdatedAt:   "2026-03-25T10:00:00Z",
	}

	cfg := DefaultConfig()
	cfg.TaskReviewMaxRounds = taskReviewMaxRounds
	cfg.AgentTimeoutSeconds = 10

	smCfg := DefaultStateMachineConfig()
	sm := NewStateMachine(state, smCfg, nil)

	logDir := t.TempDir()
	logger, err := NewWorkflowLogger(logDir)
	if err != nil {
		t.Fatalf("NewWorkflowLogger: %v", err)
	}

	orch := &Orchestrator{
		config:       cfg,
		sm:           sm,
		runner:       claudeRunner,
		codexRunner:  codexRunner,
		workspaceDir: dir,
		featureName:  "test-feature",
		emitter:      &taskReviewNoopEmitter{},
		logger:       logger,
		activeAgents: make(map[string]string),
	}

	return orch, specDir, state
}

func TestTaskReview_BothSucceed_MinorOnly_RoutesToHumanGate(t *testing.T) {
	claudeOutput := makeTaskReviewOutput("task-reviewer-claude", []Finding{
		{ID: "TF-1", Description: "Minor naming issue", Severity: SeverityMinor,
			Impact: "Low", Recommendation: "Fix naming", Lens: "scope",
			AffectedSection: "task-1"},
	})
	codexOutput := makeTaskReviewOutput("task-reviewer-codex", []Finding{
		{ID: "TF-2", Description: "Observation about docs", Severity: SeverityObservation,
			Impact: "None", Recommendation: "Add docs", Lens: "completeness",
			AffectedSection: "task-1"},
	})

	claudeRunner := &taskReviewMockRunner{output: claudeOutput, cost: 0.01}
	codexRunner := &taskReviewMockRunner{output: codexOutput, cost: 0.01}

	orch, specDir, _ := setupTaskReviewOrchestrator(t, claudeRunner, codexRunner, 3)

	err := orch.handleTaskReview(orch.sm.State(), specDir)
	if err != nil {
		t.Fatalf("handleTaskReview: %v", err)
	}

	if orch.sm.Current() != StateTaskHumanGate {
		t.Errorf("state = %s, want TASK_HUMAN_GATE", orch.sm.Current())
	}

	// Verify findings file exists.
	findingsPath := filepath.Join(specDir, "task-findings-round-1.json")
	if _, err := os.Stat(findingsPath); os.IsNotExist(err) {
		t.Error("task-findings-round-1.json not created")
	}
}

func TestTaskReview_BothSucceed_CriticalFindings_RoutesToRevision(t *testing.T) {
	claudeOutput := makeTaskReviewOutput("task-reviewer-claude", []Finding{
		{ID: "TF-1", Description: "Critical gap", Severity: SeverityCritical,
			Impact: "High", Recommendation: "Add missing task", Lens: "completeness",
			AffectedSection: "task-1"},
	})
	codexOutput := makeTaskReviewOutput("task-reviewer-codex", []Finding{
		{ID: "TF-2", Description: "Major dep issue", Severity: SeverityMajor,
			Impact: "Medium", Recommendation: "Fix deps", Lens: "dependencies",
			AffectedSection: "task-1"},
	})

	claudeRunner := &taskReviewMockRunner{output: claudeOutput, cost: 0.02}
	codexRunner := &taskReviewMockRunner{output: codexOutput, cost: 0.02}

	orch, specDir, _ := setupTaskReviewOrchestrator(t, claudeRunner, codexRunner, 3)

	err := orch.handleTaskReview(orch.sm.State(), specDir)
	if err != nil {
		t.Fatalf("handleTaskReview: %v", err)
	}

	if orch.sm.Current() != StateTaskRevision {
		t.Errorf("state = %s, want TASK_REVISION", orch.sm.Current())
	}

	if orch.sm.State().TaskReviewRound != 2 {
		t.Errorf("TaskReviewRound = %d, want 2", orch.sm.State().TaskReviewRound)
	}
}

func TestTaskReview_MaxRoundsReached_RoutesToHumanGate(t *testing.T) {
	claudeOutput := makeTaskReviewOutput("task-reviewer-claude", []Finding{
		{ID: "TF-1", Description: "Critical issue", Severity: SeverityCritical,
			Impact: "High", Recommendation: "Fix it", Lens: "completeness",
			AffectedSection: "task-1"},
	})

	claudeRunner := &taskReviewMockRunner{output: claudeOutput, cost: 0.01}

	orch, specDir, _ := setupTaskReviewOrchestrator(t, claudeRunner, nil, 1)
	orch.sm.State().TaskReviewRound = 1

	err := orch.handleTaskReview(orch.sm.State(), specDir)
	if err != nil {
		t.Fatalf("handleTaskReview: %v", err)
	}

	if orch.sm.Current() != StateTaskHumanGate {
		t.Errorf("state = %s, want TASK_HUMAN_GATE (max rounds reached)", orch.sm.Current())
	}
}

func TestTaskReview_OneReviewerFails_SurvivorUsed(t *testing.T) {
	claudeOutput := makeTaskReviewOutput("task-reviewer-claude", []Finding{
		{ID: "TF-1", Description: "Minor issue", Severity: SeverityMinor,
			Impact: "Low", Recommendation: "Fix", Lens: "scope",
			AffectedSection: "task-1"},
	})

	claudeRunner := &taskReviewMockRunner{output: claudeOutput, cost: 0.01}
	codexRunner := &taskReviewMockRunner{fail: true, failError: "codex timeout", cost: 0.00}

	orch, specDir, _ := setupTaskReviewOrchestrator(t, claudeRunner, codexRunner, 3)

	err := orch.handleTaskReview(orch.sm.State(), specDir)
	if err != nil {
		t.Fatalf("handleTaskReview: %v", err)
	}

	if orch.sm.Current() != StateTaskHumanGate {
		t.Errorf("state = %s, want TASK_HUMAN_GATE", orch.sm.Current())
	}

	// Verify findings file uses Claude's output.
	findingsPath := filepath.Join(specDir, "task-findings-round-1.json")
	data, err := os.ReadFile(findingsPath)
	if err != nil {
		t.Fatalf("read findings: %v", err)
	}
	var merged MergedFindings
	if err := json.Unmarshal(data, &merged); err != nil {
		t.Fatalf("parse findings: %v", err)
	}
	if merged.TotalAfterDedup != 1 {
		t.Errorf("TotalAfterDedup = %d, want 1", merged.TotalAfterDedup)
	}
}

func TestTaskReview_BothFail_Escalates(t *testing.T) {
	claudeRunner := &taskReviewMockRunner{fail: true, failError: "claude crash"}
	codexRunner := &taskReviewMockRunner{fail: true, failError: "codex crash"}

	orch, specDir, _ := setupTaskReviewOrchestrator(t, claudeRunner, codexRunner, 3)

	err := orch.handleTaskReview(orch.sm.State(), specDir)
	if err != nil {
		t.Fatalf("handleTaskReview: %v", err)
	}

	if orch.sm.Current() != StateEscalated {
		t.Errorf("state = %s, want ESCALATED", orch.sm.Current())
	}
}

func TestTaskReview_ClaudeOnly_NoCodex(t *testing.T) {
	claudeOutput := makeTaskReviewOutput("task-reviewer-claude", []Finding{
		{ID: "TF-1", Description: "Observation", Severity: SeverityObservation,
			Impact: "None", Recommendation: "Consider", Lens: "scope",
			AffectedSection: "task-1"},
	})

	claudeRunner := &taskReviewMockRunner{output: claudeOutput, cost: 0.01}

	orch, specDir, _ := setupTaskReviewOrchestrator(t, claudeRunner, nil, 3)

	err := orch.handleTaskReview(orch.sm.State(), specDir)
	if err != nil {
		t.Fatalf("handleTaskReview: %v", err)
	}

	if orch.sm.Current() != StateTaskHumanGate {
		t.Errorf("state = %s, want TASK_HUMAN_GATE", orch.sm.Current())
	}

	if got := claudeRunner.callCount.Load(); got != 1 {
		t.Errorf("claude call count = %d, want 1", got)
	}
}

func TestTaskReview_FindingsFileVersioned(t *testing.T) {
	claudeOutput := makeTaskReviewOutput("task-reviewer-claude", []Finding{
		{ID: "TF-1", Description: "Minor", Severity: SeverityMinor,
			Impact: "Low", Recommendation: "Fix", Lens: "scope",
			AffectedSection: "task-1"},
	})

	claudeRunner := &taskReviewMockRunner{output: claudeOutput, cost: 0.01}

	orch, specDir, _ := setupTaskReviewOrchestrator(t, claudeRunner, nil, 3)
	orch.sm.State().TaskReviewRound = 3

	err := orch.handleTaskReview(orch.sm.State(), specDir)
	if err != nil {
		t.Fatalf("handleTaskReview: %v", err)
	}

	findingsPath := filepath.Join(specDir, "task-findings-round-3.json")
	if _, err := os.Stat(findingsPath); os.IsNotExist(err) {
		t.Error("task-findings-round-3.json not created")
	}
}

func TestTaskReview_ProviderAttribution(t *testing.T) {
	claudeOutput := makeTaskReviewOutput("task-reviewer-claude", []Finding{
		{ID: "TF-C1", Description: "Task too broad", Severity: SeverityMajor,
			Impact: "Medium", Recommendation: "Split task", Lens: "scope",
			AffectedSection: "task-1"},
	})
	codexOutput := makeTaskReviewOutput("task-reviewer-codex", []Finding{
		{ID: "TF-X1", Description: "Task too broad", Severity: SeverityMajor,
			Impact: "Medium", Recommendation: "Break it up", Lens: "scope",
			AffectedSection: "task-1"},
	})

	claudeRunner := &taskReviewMockRunner{output: claudeOutput, cost: 0.01}
	codexRunner := &taskReviewMockRunner{output: codexOutput, cost: 0.01}

	orch, specDir, _ := setupTaskReviewOrchestrator(t, claudeRunner, codexRunner, 3)

	err := orch.handleTaskReview(orch.sm.State(), specDir)
	if err != nil {
		t.Fatalf("handleTaskReview: %v", err)
	}

	findingsPath := filepath.Join(specDir, "task-findings-round-1.json")
	data, err := os.ReadFile(findingsPath)
	if err != nil {
		t.Fatalf("read findings: %v", err)
	}
	var merged MergedFindings
	if err := json.Unmarshal(data, &merged); err != nil {
		t.Fatalf("parse findings: %v", err)
	}

	if merged.TotalAfterDedup != 1 {
		t.Errorf("TotalAfterDedup = %d, want 1 (deduped)", merged.TotalAfterDedup)
	}
	if len(merged.Findings) != 1 {
		t.Fatalf("len(Findings) = %d, want 1", len(merged.Findings))
	}
	if len(merged.Findings[0].RaisedBy) < 2 {
		t.Errorf("RaisedBy = %v, want both providers", merged.Findings[0].RaisedBy)
	}
}

func TestTaskReview_CostAccumulated(t *testing.T) {
	claudeOutput := makeTaskReviewOutput("task-reviewer-claude", nil)
	codexOutput := makeTaskReviewOutput("task-reviewer-codex", nil)

	claudeRunner := &taskReviewMockRunner{output: claudeOutput, cost: 0.50}
	codexRunner := &taskReviewMockRunner{output: codexOutput, cost: 0.30}

	orch, specDir, state := setupTaskReviewOrchestrator(t, claudeRunner, codexRunner, 3)
	state.CumulativeCostUSD = 1.00

	err := orch.handleTaskReview(state, specDir)
	if err != nil {
		t.Fatalf("handleTaskReview: %v", err)
	}

	if state.CumulativeCostUSD < 1.79 || state.CumulativeCostUSD > 1.81 {
		t.Errorf("CumulativeCostUSD = %.2f, want ~1.80", state.CumulativeCostUSD)
	}
	if state.AgentInvocations < 2 {
		t.Errorf("AgentInvocations = %d, want >= 2", state.AgentInvocations)
	}
}

func TestCountSeverity(t *testing.T) {
	findings := []MergedFinding{
		{Severity: SeverityCritical},
		{Severity: SeverityMajor},
		{Severity: SeverityMajor},
		{Severity: SeverityMinor},
		{Severity: SeverityObservation},
	}

	if got := countSeverity(findings, SeverityCritical); got != 1 {
		t.Errorf("critical = %d, want 1", got)
	}
	if got := countSeverity(findings, SeverityMajor); got != 2 {
		t.Errorf("major = %d, want 2", got)
	}
	if got := countSeverity(findings, SeverityMinor); got != 1 {
		t.Errorf("minor = %d, want 1", got)
	}
	if got := countSeverity(findings, SeverityObservation); got != 1 {
		t.Errorf("observation = %d, want 1", got)
	}
}

func TestBuildTaskReviewPrompt(t *testing.T) {
	prompt := buildTaskReviewPrompt(`{"version":"1.0","tasks":[]}`, 2, "my-feature")

	if len(prompt) == 0 {
		t.Fatal("empty prompt")
	}
	for _, want := range []string{"Task Graph Review Agent", "round 2", "my-feature", "task_graph", "ReviewerOutput"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

// --- Task Revision Tests ---

// taskRevisionMockRunner writes a task graph JSON file to the output path,
// simulating the Claude revision agent.
type taskRevisionMockRunner struct {
	taskGraphJSON string // JSON to write to output path
	fail          bool
	failError     string
	cost          float64
	callCount     atomic.Int32
}

func (r *taskRevisionMockRunner) Run(prompt string, outputPath string, timeoutSeconds int) (int, string, float64, int64, error) {
	r.callCount.Add(1)
	if r.fail {
		return 1, r.failError, r.cost, 100, fmt.Errorf("%s", r.failError)
	}
	os.MkdirAll(filepath.Dir(outputPath), 0o755)
	os.WriteFile(outputPath, []byte(r.taskGraphJSON), 0o644)
	return 0, "", r.cost, 100, nil
}

const revisionValidTaskGraph = `{"version":"1.0","tasks":[{"task_id":"task-1","task_name":"Task 1","goal":"Do thing","inputs":[],"outputs":[],"acceptance":["Done"],"depends_on":[],"constraints":[],"files_scope":[],"priority":"high","estimate":"small"}]}`

const revisionInvalidTaskGraph = `{"version":"1.0","tasks":[]}`

func setupTaskRevisionOrchestrator(t *testing.T, runner AgentRunner, taskReviewRound int) (*Orchestrator, string, *WorkflowStateJSON) {
	t.Helper()
	dir := t.TempDir()
	specDir := filepath.Join(dir, "specs", "test-feature")
	tasksDir := filepath.Join(dir, ".tasks")
	os.MkdirAll(specDir, 0o755)
	os.MkdirAll(tasksDir, 0o755)

	// Write a valid task graph (the "current" one to be revised).
	os.WriteFile(filepath.Join(tasksDir, "test-feature.task.json"), []byte(revisionValidTaskGraph), 0o644)

	// Write findings from prior review round.
	findingsRound := taskReviewRound - 1
	if findingsRound < 1 {
		findingsRound = 1
	}
	findings := `{"round":1,"total_findings":1,"total_after_dedup":1,"findings":[{"id":"TF-1","description":"Critical gap","severity":"CRITICAL","impact":"High","recommendation":"Add task","lens":"completeness","affected_section":"task-1","raised_by":["claude"]}]}`
	os.WriteFile(filepath.Join(specDir, fmt.Sprintf("task-findings-round-%d.json", findingsRound)), []byte(findings), 0o644)

	state := &WorkflowStateJSON{
		State:           StateTaskRevision,
		Round:           1,
		FeatureName:     "test-feature",
		StartedAt:       "2026-03-25T10:00:00Z",
		UpdatedAt:       "2026-03-25T10:00:00Z",
		TaskReviewRound: taskReviewRound,
	}

	cfg := DefaultConfig()
	cfg.AgentTimeoutSeconds = 10

	smCfg := DefaultStateMachineConfig()
	sm := NewStateMachine(state, smCfg, nil)

	logDir := t.TempDir()
	logger, err := NewWorkflowLogger(logDir)
	if err != nil {
		t.Fatalf("NewWorkflowLogger: %v", err)
	}

	orch := &Orchestrator{
		config:       cfg,
		sm:           sm,
		runner:       runner,
		workspaceDir: dir,
		featureName:  "test-feature",
		emitter:      &taskReviewNoopEmitter{},
		logger:       logger,
		activeAgents: make(map[string]string),
	}

	return orch, specDir, state
}

func TestTaskRevision_Success_TransitionsToTaskReview(t *testing.T) {
	runner := &taskRevisionMockRunner{taskGraphJSON: revisionValidTaskGraph, cost: 0.05}
	orch, specDir, _ := setupTaskRevisionOrchestrator(t, runner, 2)

	err := orch.handleTaskRevision(orch.sm.State(), specDir)
	if err != nil {
		t.Fatalf("handleTaskRevision: %v", err)
	}

	if orch.sm.Current() != StateTaskReview {
		t.Errorf("state = %s, want TASK_REVIEW", orch.sm.Current())
	}

	if got := runner.callCount.Load(); got != 1 {
		t.Errorf("runner call count = %d, want 1", got)
	}
}

func TestTaskRevision_AgentFails_Escalates(t *testing.T) {
	runner := &taskRevisionMockRunner{fail: true, failError: "revision agent crash"}
	orch, specDir, _ := setupTaskRevisionOrchestrator(t, runner, 2)

	err := orch.handleTaskRevision(orch.sm.State(), specDir)
	if err != nil {
		t.Fatalf("handleTaskRevision: %v", err)
	}

	if orch.sm.Current() != StateEscalated {
		t.Errorf("state = %s, want ESCALATED", orch.sm.Current())
	}
}

func TestTaskRevision_InvalidOutput_Escalates(t *testing.T) {
	runner := &taskRevisionMockRunner{taskGraphJSON: revisionInvalidTaskGraph, cost: 0.03}
	orch, specDir, _ := setupTaskRevisionOrchestrator(t, runner, 2)

	err := orch.handleTaskRevision(orch.sm.State(), specDir)
	if err != nil {
		t.Fatalf("handleTaskRevision: %v", err)
	}

	if orch.sm.Current() != StateEscalated {
		t.Errorf("state = %s, want ESCALATED", orch.sm.Current())
	}
}

func TestTaskRevision_IncludesHumanComments(t *testing.T) {
	var capturedPrompt string
	runner := &taskRevisionMockRunner{taskGraphJSON: revisionValidTaskGraph, cost: 0.01}

	// Override Run to capture prompt.
	origRun := runner.Run
	_ = origRun
	orch, specDir, _ := setupTaskRevisionOrchestrator(t, runner, 2)

	// Write human comments.
	comments := `[{"gate":"TASK_HUMAN_GATE","action":"correct","comment":"Add error handling tasks","timestamp":"2026-03-25T10:00:00Z"}]`
	os.WriteFile(filepath.Join(specDir, "human-comments.json"), []byte(comments), 0o644)

	// Replace runner with a capturing one.
	capturingRunner := &promptCapturingRunner{
		taskGraphJSON:   revisionValidTaskGraph,
		capturedPrompts: &[]string{},
	}
	orch.runner = capturingRunner

	err := orch.handleTaskRevision(orch.sm.State(), specDir)
	if err != nil {
		t.Fatalf("handleTaskRevision: %v", err)
	}

	if len(*capturingRunner.capturedPrompts) == 0 {
		t.Fatal("no prompts captured")
	}
	capturedPrompt = (*capturingRunner.capturedPrompts)[0]
	if !strings.Contains(capturedPrompt, "Add error handling tasks") {
		t.Error("prompt does not include human comments")
	}
	if !strings.Contains(capturedPrompt, "human_feedback") {
		t.Error("prompt does not include human_feedback block")
	}
}

// promptCapturingRunner captures prompts passed to Run and writes a valid task graph.
type promptCapturingRunner struct {
	taskGraphJSON   string
	capturedPrompts *[]string
}

func (r *promptCapturingRunner) Run(prompt string, outputPath string, timeoutSeconds int) (int, string, float64, int64, error) {
	*r.capturedPrompts = append(*r.capturedPrompts, prompt)
	os.MkdirAll(filepath.Dir(outputPath), 0o755)
	os.WriteFile(outputPath, []byte(r.taskGraphJSON), 0o644)
	return 0, "", 0.01, 100, nil
}

func TestBuildTaskRevisionPrompt(t *testing.T) {
	prompt := buildTaskRevisionPrompt(
		`{"version":"1.0","tasks":[]}`,
		`{"findings":[]}`,
		"- [TASK_HUMAN_GATE/correct]: Fix deps",
		"my-feature",
	)

	if len(prompt) == 0 {
		t.Fatal("empty prompt")
	}
	for _, want := range []string{
		"Revise this task graph",
		"Current Task Graph",
		"Review Findings",
		"human_feedback",
		"Fix deps",
		"CRITICAL and MAJOR",
		"my-feature",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

func TestTaskRevision_CostAccumulated(t *testing.T) {
	runner := &taskRevisionMockRunner{taskGraphJSON: revisionValidTaskGraph, cost: 0.25}
	orch, specDir, state := setupTaskRevisionOrchestrator(t, runner, 2)
	state.CumulativeCostUSD = 1.00

	err := orch.handleTaskRevision(state, specDir)
	if err != nil {
		t.Fatalf("handleTaskRevision: %v", err)
	}

	if state.CumulativeCostUSD < 1.24 || state.CumulativeCostUSD > 1.26 {
		t.Errorf("CumulativeCostUSD = %.2f, want ~1.25", state.CumulativeCostUSD)
	}
}
