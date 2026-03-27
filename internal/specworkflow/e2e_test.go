package specworkflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// E2E: Task review → revision → re-review loop
// ---------------------------------------------------------------------------

// e2eTaskReviewRunner simulates the full task review/revision loop:
//   - First task review round: CRITICAL findings → routes to TASK_REVISION
//   - Task revision: writes a valid revised task graph
//   - Second task review round: MINOR-only findings → routes to TASK_HUMAN_GATE
type e2eTaskReviewRunner struct {
	base            *orchMockRunner
	taskGraphDir    string
	featureName     string
	mu              sync.Mutex
	reviewCallCount int
}

func (r *e2eTaskReviewRunner) Run(prompt string, outputPath string, timeoutSeconds int) (int, string, float64, int64, error) {
	// Taskify agent — write a valid task graph.
	if orchContains(prompt, "Decompose this") {
		tg := validTaskGraphJSON()
		os.MkdirAll(filepath.Dir(outputPath), 0o755)
		os.WriteFile(outputPath, tg, 0o644)
		return 0, "", 0.01, 100, nil
	}

	// Task review agent — alternate between critical and minor findings.
	if orchContains(prompt, "Task Graph Review") {
		r.mu.Lock()
		callNum := r.reviewCallCount
		r.reviewCallCount++
		r.mu.Unlock()

		var findings []Finding
		if callNum < 2 {
			// First review round (two parallel reviewers): CRITICAL findings.
			findings = []Finding{
				{ID: fmt.Sprintf("TF-%d", callNum+1), Description: "Critical gap",
					Severity: SeverityCritical, Impact: "High",
					Recommendation: "Add missing task", Lens: "completeness",
					AffectedSection: "task-1"},
			}
		} else {
			// Second review round: minor-only findings.
			findings = []Finding{
				{ID: fmt.Sprintf("TF-%d", callNum+1), Description: "Minor naming",
					Severity: SeverityMinor, Impact: "Low",
					Recommendation: "Fix naming", Lens: "scope",
					AffectedSection: "task-1"},
			}
		}

		out := &ReviewerOutput{
			SchemaVersion: "1.0",
			Agent:         "task-reviewer",
			Round:         1,
			LensesApplied: []string{"completeness", "scope"},
			Findings:      findings,
			StructuralIntegrity: StructuralIntegrity{
				Performed: true,
				Checks:    []IntegrityCheck{{Check: "dag-check", Result: "pass"}},
			},
			MarkdownReportFile: "/tmp/report.md",
		}
		data, _ := json.MarshalIndent(out, "", "  ")
		os.MkdirAll(filepath.Dir(outputPath), 0o755)
		os.WriteFile(outputPath, data, 0o644)
		return 0, "", 0.01, 100, nil
	}

	// Task revision agent — write a valid task graph.
	if orchContains(prompt, "Revise this task graph") {
		tg := validTaskGraphJSON()
		os.MkdirAll(filepath.Dir(outputPath), 0o755)
		os.WriteFile(outputPath, tg, 0o644)
		return 0, "", 0.01, 100, nil
	}

	// Fall back to base runner for spec stages.
	return r.base.Run(prompt, outputPath, timeoutSeconds)
}

func TestE2E_TaskReviewRevisionLoop(t *testing.T) {
	orch, runner, workspace := setupOrch(t)
	feature := "test-feature"
	specDir := filepath.Join(workspace, "specs", feature)

	// Ensure .tasks directory exists.
	os.MkdirAll(filepath.Join(workspace, ".tasks"), 0o755)

	// Configure spec-stage outputs.
	runner.SetOutput("Discovery Agent", orchDiscoveryOutput())
	runner.SetOutput("Drafter Agent", orchDrafterOutput(specDir))

	minorFindings := []Finding{
		{ID: "F-001", Description: "Minor style", Severity: SeverityMinor,
			Impact: "low", Recommendation: "fix", Lens: "AMB",
			AffectedSection: "section 1"},
	}
	runner.SetOutput("Reviewer Agent", orchReviewerOutputWith("reviewer", 1, minorFindings))
	runner.SetOutput("Judge Agent", orchJudgePass(1))

	// Wrap with E2E task runner that simulates the review/revision loop.
	taskRunner := &e2eTaskReviewRunner{
		base:         runner,
		taskGraphDir: filepath.Join(workspace, ".tasks"),
		featureName:  feature,
	}
	orch.runner = taskRunner

	errCh := make(chan error, 1)
	go func() {
		errCh <- orch.RunWorkflow(GoalInput{
			Title:       "Test Feature",
			Description: "E2E test for task review revision loop",
		})
	}()

	go func() {
		// Gate 1: confirm discovery.
		sendGateResponse(orch, GateResponse{Action: "confirm"})
		// Gate 2: confirm draft.
		sendGateResponse(orch, GateResponse{Action: "confirm"})
		// Task human gate: approve (after the review/revision loop completes).
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

	// Verify the review/revision loop occurred — check that task-findings files exist.
	findingsR1 := filepath.Join(specDir, "task-findings-round-1.json")
	if _, err := os.Stat(findingsR1); os.IsNotExist(err) {
		t.Error("task-findings-round-1.json not created (first review)")
	}

	findingsR2 := filepath.Join(specDir, "task-findings-round-2.json")
	if _, err := os.Stat(findingsR2); os.IsNotExist(err) {
		t.Error("task-findings-round-2.json not created (second review after revision)")
	}
}

// ---------------------------------------------------------------------------
// E2E: State machine transition paths (integration tests from spec)
// ---------------------------------------------------------------------------

func TestStateMachine_TaskReviewToTaskRevision(t *testing.T) {
	ws := newTestState(StateTaskReview)
	sm := newSM(ws)

	if err := sm.Transition(StateTaskRevision); err != nil {
		t.Errorf("TASK_REVIEW -> TASK_REVISION: %v", err)
	}
}

func TestStateMachine_TaskRevisionToTaskReview(t *testing.T) {
	ws := newTestState(StateTaskRevision)
	sm := newSM(ws)

	if err := sm.Transition(StateTaskReview); err != nil {
		t.Errorf("TASK_REVISION -> TASK_REVIEW: %v", err)
	}
}

func TestStateMachine_TaskHumanGateApprove(t *testing.T) {
	ws := newTestState(StateTaskHumanGate)
	sm := newSM(ws)

	if err := sm.Transition(StateTasksApproved); err != nil {
		t.Errorf("TASK_HUMAN_GATE -> TASKS_APPROVED: %v", err)
	}
	if err := sm.Transition(StateComplete); err != nil {
		t.Errorf("TASKS_APPROVED -> COMPLETE: %v", err)
	}
}

func TestStateMachine_TaskHumanGateCorrect(t *testing.T) {
	ws := newTestState(StateTaskHumanGate)
	sm := newSM(ws)

	if err := sm.Transition(StateTaskify); err != nil {
		t.Errorf("TASK_HUMAN_GATE -> TASKIFY: %v", err)
	}
}

func TestStateMachine_TaskHumanGateSkip(t *testing.T) {
	ws := newTestState(StateTaskHumanGate)
	sm := newSM(ws)

	if err := sm.Transition(StateComplete); err != nil {
		t.Errorf("TASK_HUMAN_GATE -> COMPLETE: %v", err)
	}
}

func TestStateMachine_FinalizedToTaskify(t *testing.T) {
	ws := newTestState(StateFinalized)
	sm := newSM(ws)

	if err := sm.Transition(StateTaskify); err != nil {
		t.Errorf("FINALIZED -> TASKIFY: %v", err)
	}
}

func TestStateMachine_TaskifyToTaskReview(t *testing.T) {
	ws := newTestState(StateTaskify)
	sm := newSM(ws)

	if err := sm.Transition(StateTaskReview); err != nil {
		t.Errorf("TASKIFY -> TASK_REVIEW: %v", err)
	}
}

func TestStateMachine_CompleteIsTerminal(t *testing.T) {
	ws := newTestState(StateComplete)
	sm := newSM(ws)

	if !isTerminal(sm.Current()) {
		t.Error("StateComplete should be terminal")
	}
}

// ---------------------------------------------------------------------------
// E2E: handleTaskRevision in a full review/revision cycle (unit-level E2E)
// ---------------------------------------------------------------------------

func TestE2E_TaskReviewRevisionCycle_Unit(t *testing.T) {
	// Set up an orchestrator starting at TASK_REVIEW with critical findings
	// from Claude reviewer, then verify the full cycle:
	// TASK_REVIEW (critical) → TASK_REVISION → TASK_REVIEW (minor) → TASK_HUMAN_GATE

	dir := t.TempDir()
	specDir := filepath.Join(dir, "specs", "test-feature")
	tasksDir := filepath.Join(dir, ".tasks")
	os.MkdirAll(specDir, 0o755)
	os.MkdirAll(tasksDir, 0o755)

	// Write initial task graph.
	os.WriteFile(filepath.Join(tasksDir, "test-feature.task.json"),
		validTaskGraphJSON(), 0o644)

	state := &WorkflowStateJSON{
		State:       StateTaskReview,
		Round:       1,
		FeatureName: "test-feature",
		StartedAt:   "2026-03-25T10:00:00Z",
		UpdatedAt:   "2026-03-25T10:00:00Z",
	}

	cfg := DefaultConfig()
	cfg.TaskReviewMaxRounds = 3
	cfg.AgentTimeoutSeconds = 10

	smCfg := DefaultStateMachineConfig()
	sm := NewStateMachine(state, smCfg, nil)

	logDir := t.TempDir()
	logger, err := NewWorkflowLogger(logDir)
	if err != nil {
		t.Fatalf("NewWorkflowLogger: %v", err)
	}

	var reviewCallCount atomic.Int32

	// Mock runner that alternates critical/minor findings for reviews,
	// and writes valid task graphs for revisions.
	mockRunner := &e2eUnitRunner{
		reviewCallCount: &reviewCallCount,
		validGraph:      string(validTaskGraphJSON()),
	}

	orch := &Orchestrator{
		config:       cfg,
		sm:           sm,
		runner:       mockRunner,
		workspaceDir: dir,
		featureName:  "test-feature",
		emitter:      &taskReviewNoopEmitter{},
		logger:       logger,
		activeAgents: make(map[string]string),
	}

	// Step 1: Task review with CRITICAL findings → TASK_REVISION.
	err = orch.handleTaskReview(state, specDir)
	if err != nil {
		t.Fatalf("handleTaskReview (round 1): %v", err)
	}
	if orch.sm.Current() != StateTaskRevision {
		t.Fatalf("after round 1: state = %s, want TASK_REVISION", orch.sm.Current())
	}

	// Step 2: Task revision → TASK_REVIEW.
	err = orch.handleTaskRevision(state, specDir)
	if err != nil {
		t.Fatalf("handleTaskRevision: %v", err)
	}
	if orch.sm.Current() != StateTaskReview {
		t.Fatalf("after revision: state = %s, want TASK_REVIEW", orch.sm.Current())
	}

	// Step 3: Second task review with MINOR findings → TASK_HUMAN_GATE.
	err = orch.handleTaskReview(state, specDir)
	if err != nil {
		t.Fatalf("handleTaskReview (round 2): %v", err)
	}
	if orch.sm.Current() != StateTaskHumanGate {
		t.Errorf("after round 2: state = %s, want TASK_HUMAN_GATE", orch.sm.Current())
	}

	// Verify findings files from both rounds.
	for _, round := range []int{1, 2} {
		fp := filepath.Join(specDir, fmt.Sprintf("task-findings-round-%d.json", round))
		if _, err := os.Stat(fp); os.IsNotExist(err) {
			t.Errorf("task-findings-v%d.json not created", round)
		}
	}
}

// e2eUnitRunner alternates between critical and minor findings for task reviews,
// and writes valid task graphs for task revisions.
type e2eUnitRunner struct {
	reviewCallCount *atomic.Int32
	validGraph      string
}

func (r *e2eUnitRunner) Run(prompt string, outputPath string, timeoutSeconds int) (int, string, float64, int64, error) {
	os.MkdirAll(filepath.Dir(outputPath), 0o755)

	if strings.Contains(prompt, "Task Graph Review") {
		count := r.reviewCallCount.Add(1)
		var findings []Finding
		if count <= 1 {
			// First review round: CRITICAL finding.
			findings = []Finding{
				{ID: fmt.Sprintf("TF-%d", count), Description: "Critical gap",
					Severity: SeverityCritical, Impact: "High",
					Recommendation: "Fix", Lens: "completeness",
					AffectedSection: "task-1"},
			}
		} else {
			// After revision: MINOR only.
			findings = []Finding{
				{ID: fmt.Sprintf("TF-%d", count), Description: "Minor note",
					Severity: SeverityMinor, Impact: "Low",
					Recommendation: "Note", Lens: "scope",
					AffectedSection: "task-1"},
			}
		}
		out := &ReviewerOutput{
			SchemaVersion: "1.0",
			Agent:         "task-reviewer",
			Round:         1,
			LensesApplied: []string{"completeness"},
			Findings:      findings,
			StructuralIntegrity: StructuralIntegrity{Performed: true,
				Checks: []IntegrityCheck{{Check: "dag-check", Result: "pass"}}},
			MarkdownReportFile: "/tmp/report.md",
		}
		data, _ := json.MarshalIndent(out, "", "  ")
		os.WriteFile(outputPath, data, 0o644)
		return 0, "", 0.01, 100, nil
	}

	if strings.Contains(prompt, "Revise this task graph") {
		os.WriteFile(outputPath, []byte(r.validGraph), 0o644)
		return 0, "", 0.01, 100, nil
	}

	// Default: write empty JSON.
	os.WriteFile(outputPath, []byte(`{}`), 0o644)
	return 0, "", 0.01, 100, nil
}
