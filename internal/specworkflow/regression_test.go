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

// ---------------------------------------------------------------------------
// Regression: DispatchReviewers with spec 4-group lens set
// ---------------------------------------------------------------------------

type regressionDispatchRunner struct {
	handler func(prompt, outputPath string, timeoutSeconds int) (int, string, float64, int64, error)
}

func (m *regressionDispatchRunner) Run(prompt, outputPath string, timeoutSeconds int) (int, string, float64, int64, error) {
	return m.handler(prompt, outputPath, timeoutSeconds)
}

// regressionLensFromPath extracts the lens name from an output path.
func regressionLensFromPath(path string) string {
	base := filepath.Base(path)
	// Format: review-{lens}-{provider}-round-{N}.json
	if len(base) > 7 && base[:7] == "review-" {
		rest := base[7:]
		for _, provider := range []string{"-claude-", "-codex-"} {
			for i := 0; i <= len(rest)-len(provider); i++ {
				if rest[i:i+len(provider)] == provider {
					return rest[:i]
				}
			}
		}
	}
	return "unknown"
}

// TestRegression_SpecDispatchReviewersWith4Groups verifies that DispatchReviewers
// with the spec workflow's 4 lens groups still dispatches correctly.
func TestRegression_SpecDispatchReviewersWith4Groups(t *testing.T) {
	dir := t.TempDir()

	runner := &regressionDispatchRunner{
		handler: func(prompt, outputPath string, timeoutSeconds int) (int, string, float64, int64, error) {
			lens := regressionLensFromPath(outputPath)
			output := ReviewerOutput{
				SchemaVersion:      "1.0",
				Agent:              "reviewer-" + lens + "-claude",
				Round:              1,
				MarkdownReportFile: "report-" + lens + ".md",
				LensesApplied:      []string{lens},
				Findings: []Finding{
					{
						ID:                    "F-" + lens,
						Description:           "Test finding for " + lens,
						Severity:              SeverityMajor,
						Impact:                "Test impact",
						Recommendation:        "Fix it",
						Lens:                  lens,
						AffectedSection:       "Section A",
						ConstitutionPrinciple: strPtr("Principle 1"),
					},
				},
			}
			data, _ := json.Marshal(output)
			os.WriteFile(outputPath, data, 0644)
			return 0, "", 0.01, 100, nil
		},
	}

	lensGroups := SpecReviewerLensGroups()
	if len(lensGroups) != 4 {
		t.Fatalf("SpecReviewerLensGroups: expected 4, got %d", len(lensGroups))
	}

	prompts := make(map[string]string)
	outputPaths := make(map[string]string)
	for _, lens := range lensGroups {
		prompts[lens] = "Review with lens: " + lens
		outputPaths[lens] = filepath.Join(dir, "review-"+lens+"-claude-round-1.json")
	}

	config := ReviewDispatchConfig{
		MaxRetries:     2,
		TimeoutSeconds: 300,
	}

	result, err := DispatchReviewers(
		runner, nil, lensGroups,
		prompts, outputPaths, nil,
		config, func(d time.Duration) {}, nil,
	)
	if err != nil {
		t.Fatalf("DispatchReviewers: %v", err)
	}

	if len(result.Results) != 4 {
		t.Errorf("expected 4 results, got %d", len(result.Results))
	}
	if result.TotalCostUSD < 0.03 {
		t.Errorf("expected cost >= $0.03, got $%.4f", result.TotalCostUSD)
	}

	// Verify all 4 lens groups produced output.
	seenLenses := map[string]bool{}
	for _, r := range result.Results {
		if r.Output != nil {
			for _, l := range r.Output.LensesApplied {
				seenLenses[l] = true
			}
		}
	}
	for _, lens := range lensGroups {
		if !seenLenses[lens] {
			t.Errorf("missing output for lens %s", lens)
		}
	}
}

// TestRegression_SpecMergeWithSeverityPromotion verifies that
// MergeReviewerOutputs with SpecDedupKey and promoteSeverity=true produces
// the expected merge behavior.
func TestRegression_SpecMergeWithSeverityPromotion(t *testing.T) {
	output1 := &ReviewerOutput{
		SchemaVersion:      "1.0",
		Agent:              "reviewer-clarity-claude",
		Round:              1,
		MarkdownReportFile: "report-clarity-claude.md",
		LensesApplied:      []string{"clarity"},
		Findings: []Finding{
			{
				ID:                    "F-001",
				Description:           "Unclear section",
				Severity:              SeverityMinor,
				Impact:                "Confusion",
				Recommendation:        "Rewrite",
				Lens:                  "clarity",
				AffectedSection:       "Section A",
				ConstitutionPrinciple: strPtr("Principle 1"),
			},
		},
	}
	output2 := &ReviewerOutput{
		SchemaVersion:      "1.0",
		Agent:              "reviewer-clarity-codex",
		Round:              1,
		MarkdownReportFile: "report-clarity-codex.md",
		LensesApplied:      []string{"clarity"},
		Findings: []Finding{
			{
				ID:                    "F-002",
				Description:           "Same unclear section, more severe",
				Severity:              SeverityCritical,
				Impact:                "Major confusion",
				Recommendation:        "Complete rewrite",
				Lens:                  "clarity",
				AffectedSection:       "Section A",
				ConstitutionPrinciple: strPtr("Principle 1"),
			},
		},
	}

	merged, err := MergeReviewerOutputs(
		[]*ReviewerOutput{output1, output2}, 1,
		SpecDedupKey, true,
	)
	if err != nil {
		t.Fatalf("MergeReviewerOutputs: %v", err)
	}

	if len(merged.Findings) != 1 {
		t.Fatalf("expected 1 merged finding, got %d", len(merged.Findings))
	}

	// Severity should be promoted to CRITICAL.
	if merged.Findings[0].Severity != SeverityCritical {
		t.Errorf("expected CRITICAL (promoted), got %s", merged.Findings[0].Severity)
	}

	if len(merged.Findings[0].RaisedBy) != 2 {
		t.Errorf("expected 2 sources (raised_by), got %d", len(merged.Findings[0].RaisedBy))
	}
}

// TestRegression_SpecMergeWithoutPromotion verifies that merge without
// severity promotion keeps the original severity.
func TestRegression_SpecMergeWithoutPromotion(t *testing.T) {
	output := &ReviewerOutput{
		SchemaVersion:      "1.0",
		Agent:              "reviewer-security-claude",
		Round:              1,
		MarkdownReportFile: "report-security.md",
		LensesApplied:      []string{"security"},
		Findings: []Finding{
			{
				ID:                    "S-001",
				Description:           "SQL injection",
				Severity:              SeverityMajor,
				Impact:                "Data leak",
				Recommendation:        "Use params",
				Lens:                  "security",
				AffectedSection:       "api/handler.go",
				ConstitutionPrinciple: strPtr("Security"),
			},
		},
	}

	merged, err := MergeReviewerOutputs(
		[]*ReviewerOutput{output}, 1,
		SpecDedupKey, false,
	)
	if err != nil {
		t.Fatalf("MergeReviewerOutputs: %v", err)
	}

	if len(merged.Findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(merged.Findings))
	}
	if merged.Findings[0].Severity != SeverityMajor {
		t.Errorf("expected MAJOR, got %s", merged.Findings[0].Severity)
	}
}
