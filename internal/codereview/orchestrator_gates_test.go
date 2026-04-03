package codereview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Helper: create orchestrator at a specific gate state
// ---------------------------------------------------------------------------

func orchAtScopeGate(t *testing.T) (*CodeReviewOrchestrator, string) {
	t.Helper()
	codePath := t.TempDir()
	orch, workspaceDir := newTestOrchestrator(t, defaultMockGit())
	if err := orch.Start(StartCodeReviewRequest{
		CodePath:    codePath,
		FeatureName: "gate-test",
		SpecPath:    createTempFile(t, "spec.md", "# Test Spec"),
		TaskListPath: createTempFile(t, "tasks.json", "{}"),
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	return orch, workspaceDir
}

func orchAtFixesGate(t *testing.T) (*CodeReviewOrchestrator, string) {
	t.Helper()
	orch, workspaceDir := orchAtScopeGate(t)

	// Confirm scope gate → CR_REVIEWING.
	if err := orch.HandleScopeGate(CRGateResponse{Action: "confirm"}); err != nil {
		t.Fatalf("HandleScopeGate: %v", err)
	}

	// Manually transition to CR_FIXING → CR_HUMAN_GATE_FIXES.
	if err := orch.sm.Transition(CRFixing); err != nil {
		t.Fatalf("Transition to CRFixing: %v", err)
	}
	if err := orch.sm.Transition(CRHumanGateFixes); err != nil {
		t.Fatalf("Transition to CRHumanGateFixes: %v", err)
	}

	return orch, workspaceDir
}

func createTempFile(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatalf("create temp file %s: %v", name, err)
	}
	return p
}

// ---------------------------------------------------------------------------
// GetScopeGateData
// ---------------------------------------------------------------------------

func TestHumanGate_GetScopeGateData_ReturnsAllFields(t *testing.T) {
	orch, _ := orchAtScopeGate(t)

	data, err := orch.GetScopeGateData()
	if err != nil {
		t.Fatalf("GetScopeGateData: %v", err)
	}

	if data.CodePath == "" {
		t.Error("expected code_path to be set")
	}
	if data.SpecPath == "" {
		t.Error("expected spec_path to be set")
	}
	if data.TaskListPath == "" {
		t.Error("expected task_list_path to be set")
	}
	if data.GrillCodeMode != "full-context" {
		t.Errorf("expected grill_code_mode 'full-context', got %q", data.GrillCodeMode)
	}
	if data.GitBranch != "main" {
		t.Errorf("expected git_branch 'main', got %q", data.GitBranch)
	}
	if data.GitSHA != "abc123def456" {
		t.Errorf("expected git_sha 'abc123def456', got %q", data.GitSHA)
	}
}

func TestHumanGate_GetScopeGateData_NotStarted(t *testing.T) {
	orch, _ := newTestOrchestrator(t, defaultMockGit())
	_, err := orch.GetScopeGateData()
	if err == nil {
		t.Fatal("expected error when orchestrator not started")
	}
}

// ---------------------------------------------------------------------------
// GetFixesGateData
// ---------------------------------------------------------------------------

func TestHumanGate_GetFixesGateData_ReturnsFindingsSummary(t *testing.T) {
	orch, _ := orchAtFixesGate(t)

	orch.sm.State().FindingsSummary = CodeReviewFindingsSummary{
		OpenCritical:    1,
		OpenMajor:       2,
		OpenMinor:       3,
		OpenObservation: 4,
		Fixed:           5,
		Deferred:        1,
		Failed:          0,
	}

	data, err := orch.GetFixesGateData()
	if err != nil {
		t.Fatalf("GetFixesGateData: %v", err)
	}

	if data.FindingsSummary.OpenCritical != 1 {
		t.Errorf("expected OpenCritical=1, got %d", data.FindingsSummary.OpenCritical)
	}
	if data.FindingsSummary.Fixed != 5 {
		t.Errorf("expected Fixed=5, got %d", data.FindingsSummary.Fixed)
	}
}

func TestHumanGate_GetFixesGateData_ReturnsFixDetails(t *testing.T) {
	orch, _ := orchAtFixesGate(t)

	orch.lastFixOutput = &FixOutput{
		Round: 1,
		FixesApplied: []FixAction{
			{FindingID: "CRIT-001", Status: FixStatusFixed, Description: "Fixed null check"},
			{FindingID: "MAJ-002", Status: FixStatusDeferred, Description: "Deferred refactor"},
		},
		GitDiffStat: " 3 files changed, 10 insertions(+), 2 deletions(-)",
		TestResults: &TestResults{Total: 42, Passed: 40, Failed: 2, Failures: []string{"TestFoo", "TestBar"}},
	}

	data, err := orch.GetFixesGateData()
	if err != nil {
		t.Fatalf("GetFixesGateData: %v", err)
	}

	if len(data.FixDetails) != 2 {
		t.Fatalf("expected 2 fix details, got %d", len(data.FixDetails))
	}
	if data.FixDetails[0].FindingID != "CRIT-001" {
		t.Errorf("expected finding_id 'CRIT-001', got %q", data.FixDetails[0].FindingID)
	}
	if data.FixDetails[1].Description != "Deferred refactor" {
		t.Errorf("expected description 'Deferred refactor', got %q", data.FixDetails[1].Description)
	}
}

func TestHumanGate_GetFixesGateData_ReturnsTestResults(t *testing.T) {
	orch, _ := orchAtFixesGate(t)

	orch.lastFixOutput = &FixOutput{
		Round:        1,
		FixesApplied: []FixAction{},
		TestResults:  &TestResults{Total: 42, Passed: 40, Failed: 2, Failures: []string{"TestFoo", "TestBar"}},
	}

	data, err := orch.GetFixesGateData()
	if err != nil {
		t.Fatalf("GetFixesGateData: %v", err)
	}

	if data.TestResults == nil {
		t.Fatal("expected test_results to be set")
	}
	if data.TestResults.Total != 42 {
		t.Errorf("expected Total=42, got %d", data.TestResults.Total)
	}
	if data.TestResults.Failed != 2 {
		t.Errorf("expected Failed=2, got %d", data.TestResults.Failed)
	}
	if len(data.TestResults.Failures) != 2 {
		t.Errorf("expected 2 failure names, got %d", len(data.TestResults.Failures))
	}
}

func TestHumanGate_GetFixesGateData_ReturnsDeferredItems(t *testing.T) {
	orch, _ := orchAtFixesGate(t)

	orch.lastFixOutput = &FixOutput{
		Round: 1,
		FixesApplied: []FixAction{
			{FindingID: "CRIT-001", Status: FixStatusFixed},
			{FindingID: "MAJ-002", Status: FixStatusDeferred},
			{FindingID: "MIN-003", Status: FixStatusDeferred},
		},
	}

	data, err := orch.GetFixesGateData()
	if err != nil {
		t.Fatalf("GetFixesGateData: %v", err)
	}

	if len(data.DeferredItems) != 2 {
		t.Fatalf("expected 2 deferred items, got %d", len(data.DeferredItems))
	}
	if data.DeferredItems[0] != "MAJ-002" {
		t.Errorf("expected first deferred 'MAJ-002', got %q", data.DeferredItems[0])
	}
	if data.DeferredItems[1] != "MIN-003" {
		t.Errorf("expected second deferred 'MIN-003', got %q", data.DeferredItems[1])
	}
}

func TestHumanGate_GetFixesGateData_ReducedCoverageWarning(t *testing.T) {
	orch, _ := orchAtFixesGate(t)
	// Simulate the warning that HandleReviewing would persist when Codex is unavailable.
	orch.StateMachine().State().Warnings = appendUniqueWarning(
		orch.StateMachine().State().Warnings,
		"reduced_coverage: Codex provider unavailable, review used Claude only",
	)

	data, err := orch.GetFixesGateData()
	if err != nil {
		t.Fatalf("GetFixesGateData: %v", err)
	}

	found := false
	for _, w := range data.Warnings {
		if strings.Contains(w, "reduced_coverage") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected reduced_coverage warning when Codex unavailable")
	}
}

func TestHumanGate_GetFixesGateData_NoReducedCoverageWithCodex(t *testing.T) {
	codePath := t.TempDir()
	workspaceDir := t.TempDir()
	cfg := DefaultCodeReviewConfig()

	// Create orchestrator with a non-nil codexRunner.
	orch := NewCodeReviewOrchestrator(CROrchestratorConfig{
		WorkspaceDir: workspaceDir,
		Config:       cfg,
		GitProvider:  defaultMockGit(),
		CodexRunner:  &mockAgentRunner{},
	})
	if err := orch.Start(StartCodeReviewRequest{
		CodePath:    codePath,
		FeatureName: "codex-test",
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Move to fixes gate.
	if err := orch.HandleScopeGate(CRGateResponse{Action: "confirm"}); err != nil {
		t.Fatalf("HandleScopeGate: %v", err)
	}
	if err := orch.sm.Transition(CRFixing); err != nil {
		t.Fatalf("Transition to CRFixing: %v", err)
	}
	if err := orch.sm.Transition(CRHumanGateFixes); err != nil {
		t.Fatalf("Transition to CRHumanGateFixes: %v", err)
	}

	data, err := orch.GetFixesGateData()
	if err != nil {
		t.Fatalf("GetFixesGateData: %v", err)
	}

	for _, w := range data.Warnings {
		if strings.Contains(w, "reduced_coverage") {
			t.Error("should NOT have reduced_coverage warning when Codex is available")
		}
	}
}

func TestHumanGate_GetFixesGateData_NotStarted(t *testing.T) {
	orch, _ := newTestOrchestrator(t, defaultMockGit())
	_, err := orch.GetFixesGateData()
	if err == nil {
		t.Fatal("expected error when orchestrator not started")
	}
}

// ---------------------------------------------------------------------------
// HandleFixesGate — re-review
// ---------------------------------------------------------------------------

func TestHumanGate_HandleFixesGate_ReReview(t *testing.T) {
	orch, _ := orchAtFixesGate(t)
	initialRound := orch.sm.State().Round

	if err := orch.HandleFixesGate(CRGateResponse{Action: "re-review"}); err != nil {
		t.Fatalf("HandleFixesGate re-review: %v", err)
	}

	if orch.sm.Current() != CRReviewing {
		t.Errorf("expected CR_REVIEWING, got %s", orch.sm.Current())
	}
	if orch.sm.State().Round != initialRound+1 {
		t.Errorf("expected round %d, got %d", initialRound+1, orch.sm.State().Round)
	}
}

func TestHumanGate_HandleFixesGate_ReReview_BlockedByMaxRounds(t *testing.T) {
	orch, _ := orchAtFixesGate(t)
	// Set round to max so incrementing makes it exceed.
	orch.sm.State().Round = 3 // max_rounds=3, after increment round=4 > 3

	err := orch.HandleFixesGate(CRGateResponse{Action: "re-review"})
	if err == nil {
		t.Fatal("expected error when max rounds exceeded")
	}
	if !strings.Contains(err.Error(), "max review rounds") {
		t.Errorf("expected error about max rounds, got: %v", err)
	}

	// Verify round was rolled back.
	if orch.sm.State().Round != 3 {
		t.Errorf("expected round rolled back to 3, got %d", orch.sm.State().Round)
	}
}

// ---------------------------------------------------------------------------
// HandleFixesGate — accept
// ---------------------------------------------------------------------------

func TestHumanGate_HandleFixesGate_Accept(t *testing.T) {
	orch, _ := orchAtFixesGate(t)

	if err := orch.HandleFixesGate(CRGateResponse{Action: "accept"}); err != nil {
		t.Fatalf("HandleFixesGate accept: %v", err)
	}

	if orch.sm.Current() != CRComplete {
		t.Errorf("expected CR_COMPLETE, got %s", orch.sm.Current())
	}
}

// ---------------------------------------------------------------------------
// HandleFixesGate — escalate
// ---------------------------------------------------------------------------

func TestHumanGate_HandleFixesGate_Escalate(t *testing.T) {
	orch, _ := orchAtFixesGate(t)

	if err := orch.HandleFixesGate(CRGateResponse{Action: "escalate"}); err != nil {
		t.Fatalf("HandleFixesGate escalate: %v", err)
	}

	if orch.sm.Current() != CREscalated {
		t.Errorf("expected CR_ESCALATED, got %s", orch.sm.Current())
	}

	reason := orch.sm.State().EscalationReason
	if !strings.Contains(reason, "escalated at fixes gate") {
		t.Errorf("expected escalation reason about fixes gate, got: %q", reason)
	}
}

func TestHumanGate_HandleFixesGate_EscalateWithComment(t *testing.T) {
	orch, _ := orchAtFixesGate(t)

	if err := orch.HandleFixesGate(CRGateResponse{
		Action:  "escalate",
		Comment: "Too risky to merge",
	}); err != nil {
		t.Fatalf("HandleFixesGate escalate: %v", err)
	}

	if orch.sm.State().EscalationReason != "Too risky to merge" {
		t.Errorf("expected escalation reason 'Too risky to merge', got %q", orch.sm.State().EscalationReason)
	}
}

// ---------------------------------------------------------------------------
// HandleFixesGate — error cases
// ---------------------------------------------------------------------------

func TestHumanGate_HandleFixesGate_InvalidAction(t *testing.T) {
	orch, _ := orchAtFixesGate(t)

	err := orch.HandleFixesGate(CRGateResponse{Action: "invalid"})
	if err == nil {
		t.Fatal("expected error for invalid gate action")
	}
	if !strings.Contains(err.Error(), "invalid gate action") {
		t.Errorf("expected error about invalid gate action, got: %v", err)
	}
}

func TestHumanGate_HandleFixesGate_NotStarted(t *testing.T) {
	orch, _ := newTestOrchestrator(t, defaultMockGit())
	err := orch.HandleFixesGate(CRGateResponse{Action: "accept"})
	if err == nil {
		t.Fatal("expected error when orchestrator not started")
	}
}

func TestHumanGate_HandleFixesGate_WrongState(t *testing.T) {
	orch, _ := orchAtScopeGate(t)
	err := orch.HandleFixesGate(CRGateResponse{Action: "accept"})
	if err == nil {
		t.Fatal("expected error when not at fixes gate")
	}
	if !strings.Contains(err.Error(), "not at fixes gate") {
		t.Errorf("expected error about wrong state, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Comment persistence
// ---------------------------------------------------------------------------

func TestHumanGate_HandleFixesGate_PersistsComment(t *testing.T) {
	orch, workspaceDir := orchAtFixesGate(t)

	if err := orch.HandleFixesGate(CRGateResponse{
		Action:  "accept",
		Comment: "Looks good to me",
	}); err != nil {
		t.Fatalf("HandleFixesGate: %v", err)
	}

	commentsPath := filepath.Join(workspaceDir, "code-reviews", "gate-test", "human-comments.json")
	data, err := os.ReadFile(commentsPath)
	if err != nil {
		t.Fatalf("read human-comments.json: %v", err)
	}
	if !strings.Contains(string(data), "Looks good to me") {
		t.Error("expected comment to be persisted in human-comments.json")
	}
	if !strings.Contains(string(data), "CR_HUMAN_GATE_FIXES") {
		t.Error("expected gate name to be persisted in human-comments.json")
	}
}

func TestHumanGate_HandleFixesGate_EmptyCommentNotPersisted(t *testing.T) {
	orch, workspaceDir := orchAtFixesGate(t)

	if err := orch.HandleFixesGate(CRGateResponse{Action: "accept"}); err != nil {
		t.Fatalf("HandleFixesGate: %v", err)
	}

	commentsPath := filepath.Join(workspaceDir, "code-reviews", "gate-test", "human-comments.json")
	if _, err := os.Stat(commentsPath); err == nil {
		t.Error("expected human-comments.json to not exist when no comment provided")
	}
}

