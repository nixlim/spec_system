package specworkflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Regression: full workflow INIT → COMPLETE with Codex disabled
// ---------------------------------------------------------------------------

// regressionTaskGraphRunner wraps orchMockRunner and writes a valid task graph
// when the prompt contains "Decompose this" (the taskify prompt), or a valid
// ReviewerOutput when the prompt contains "Task Graph Review".
type regressionTaskGraphRunner struct {
	base         *orchMockRunner
	taskGraphDir string
	featureName  string
}

func (r *regressionTaskGraphRunner) Run(prompt string, outputPath string, timeoutSeconds int) (int, string, float64, int64, error) {
	// Taskify agent — write a valid task graph.
	if orchContains(prompt, "Decompose this") {
		tg := validTaskGraphJSON()
		os.MkdirAll(filepath.Dir(outputPath), 0o755)
		os.WriteFile(outputPath, tg, 0o644)
		// Also ensure .tasks directory has the file.
		os.MkdirAll(filepath.Dir(outputPath), 0o755)
		return 0, "", 0.01, 100, nil
	}

	// Task review agent — write a ReviewerOutput with minor-only findings.
	if orchContains(prompt, "Task Graph Review") {
		out := &ReviewerOutput{
			SchemaVersion: "1.0",
			Agent:         "task-reviewer",
			Round:         1,
			LensesApplied: []string{"completeness", "scope"},
			Findings: []Finding{
				{ID: "TF-1", Description: "Minor naming", Severity: SeverityMinor,
					Impact: "Low", Recommendation: "Fix", Lens: "scope",
					AffectedSection: "task-1"},
			},
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

	// Fall back to base runner for discovery, drafting, review, judge, etc.
	return r.base.Run(prompt, outputPath, timeoutSeconds)
}

func TestRegression_FullWorkflowNoCodex(t *testing.T) {
	orch, runner, workspace := setupOrch(t)
	feature := "test-feature"
	specDir := filepath.Join(workspace, "specs", feature)

	// Ensure .tasks directory exists.
	os.MkdirAll(filepath.Join(workspace, ".tasks"), 0o755)

	// Configure outputs for spec stages.
	runner.SetOutput("Discovery Agent", orchDiscoveryOutput())
	runner.SetOutput("Drafter Agent", orchDrafterOutput(specDir))

	// Minor-only findings so judge passes quickly.
	minorFindings := []Finding{
		{ID: "F-001", Description: "Minor style issue", Severity: SeverityMinor,
			Impact: "low", Recommendation: "consider fixing", Lens: "AMB",
			AffectedSection: "section 1"},
	}
	runner.SetOutput("Reviewer Agent", orchReviewerOutputWith("reviewer", 1, minorFindings))
	runner.SetOutput("Judge Agent", orchJudgePass(1))

	// Wrap with task-graph-aware runner for taskify/review stages.
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
			Description: "A test feature for regression testing",
		})
	}()

	go func() {
		// Gate 1: confirm discovery.
		sendGateResponse(orch, GateResponse{Action: "confirm"})
		// Gate 2: confirm draft.
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

	// Verify spec-final.md was created during FINALIZED.
	finalSpecPath := filepath.Join(specDir, "spec-final.md")
	if _, err := os.Stat(finalSpecPath); os.IsNotExist(err) {
		t.Error("spec-final.md not created")
	}

	// Verify task graph was written.
	taskGraphPath := filepath.Join(workspace, ".tasks", feature+".task.json")
	if _, err := os.Stat(taskGraphPath); os.IsNotExist(err) {
		t.Error("task graph file not created")
	}
}

// ---------------------------------------------------------------------------
// Regression: FINALIZED is no longer terminal
// ---------------------------------------------------------------------------

func TestRegression_FinalizedNotTerminal(t *testing.T) {
	ws := newTestState(StateFinalized)
	sm := newSM(ws)

	// FINALIZED should NOT be terminal.
	if isTerminal(sm.Current()) {
		t.Error("FINALIZED should not be terminal")
	}

	// FINALIZED → TASKIFY should be valid.
	if err := sm.Transition(StateTaskify); err != nil {
		t.Errorf("FINALIZED -> TASKIFY should be valid: %v", err)
	}
}

func TestRegression_IsTerminalUpdated(t *testing.T) {
	tests := []struct {
		state    WorkflowState
		terminal bool
	}{
		{StateInit, false},
		{StateDiscovery, false},
		{StateFinalized, false},
		{StateTaskify, false},
		{StateTaskReview, false},
		{StateTaskRevision, false},
		{StateTaskHumanGate, false},
		{StateTasksApproved, false},
		{StateComplete, true},
		{StateEscalated, true},
	}

	for _, tt := range tests {
		ws := newTestState(tt.state)
		sm := newSM(ws)
		if isTerminal(sm.Current()) != tt.terminal {
			t.Errorf("IsTerminal(%s) = %v, want %v", tt.state, isTerminal(sm.Current()), tt.terminal)
		}
	}
}

// ---------------------------------------------------------------------------
// Regression: config backward compatibility
// ---------------------------------------------------------------------------

func TestRegression_ConfigBackwardCompat(t *testing.T) {
	cfg := DefaultConfig()

	// New fields must have sensible defaults.
	if cfg.EnableCodexDiscovery != false {
		t.Errorf("EnableCodexDiscovery default = %v, want false", cfg.EnableCodexDiscovery)
	}
	if cfg.EnableCodexDrafting != false {
		t.Errorf("EnableCodexDrafting default = %v, want false", cfg.EnableCodexDrafting)
	}
	if cfg.TaskifyMaxRetries < 1 {
		t.Errorf("TaskifyMaxRetries default = %d, want >= 1", cfg.TaskifyMaxRetries)
	}
	if cfg.TaskReviewMaxRounds < 1 {
		t.Errorf("TaskReviewMaxRounds default = %d, want >= 1", cfg.TaskReviewMaxRounds)
	}
}
