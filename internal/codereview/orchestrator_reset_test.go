package codereview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// ResetWorkspace (in-memory orchestrator)
// ---------------------------------------------------------------------------

func TestReset_CompletedWorkflow_DeletesWorkspace(t *testing.T) {
	codePath := t.TempDir()
	orch, workspaceDir := newTestOrchestrator(t, defaultMockGit())

	if err := orch.Start(StartCodeReviewRequest{
		CodePath:    codePath,
		FeatureName: "reset-test",
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Move to terminal state.
	orch.HandleScopeGate(CRGateResponse{Action: "confirm"})
	orch.sm.Transition(CRFixing)
	orch.sm.Transition(CRHumanGateFixes)
	orch.sm.Transition(CRComplete)

	featureDir := filepath.Join(workspaceDir, "code-reviews", "reset-test")
	if _, err := os.Stat(featureDir); err != nil {
		t.Fatalf("expected workspace to exist before reset: %v", err)
	}

	if err := orch.ResetWorkspace(); err != nil {
		t.Fatalf("ResetWorkspace: %v", err)
	}

	if _, err := os.Stat(featureDir); !os.IsNotExist(err) {
		t.Error("expected workspace directory to be deleted after reset")
	}
}

func TestReset_EscalatedWorkflow_DeletesWorkspace(t *testing.T) {
	codePath := t.TempDir()
	orch, workspaceDir := newTestOrchestrator(t, defaultMockGit())

	if err := orch.Start(StartCodeReviewRequest{
		CodePath:    codePath,
		FeatureName: "escalated-test",
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	orch.HandleScopeGate(CRGateResponse{Action: "cancel"})

	featureDir := filepath.Join(workspaceDir, "code-reviews", "escalated-test")
	if err := orch.ResetWorkspace(); err != nil {
		t.Fatalf("ResetWorkspace: %v", err)
	}

	if _, err := os.Stat(featureDir); !os.IsNotExist(err) {
		t.Error("expected workspace directory to be deleted after reset")
	}
}

func TestReset_RunningWorkflow_Returns409(t *testing.T) {
	codePath := t.TempDir()
	orch, _ := newTestOrchestrator(t, defaultMockGit())

	if err := orch.Start(StartCodeReviewRequest{
		CodePath:    codePath,
		FeatureName: "running-test",
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Still at scope gate — not terminal.
	err := orch.ResetWorkspace()
	if err == nil {
		t.Fatal("expected error for non-terminal workflow")
	}
	if !strings.Contains(err.Error(), "cannot reset running workflow") {
		t.Errorf("expected error about running workflow, got: %v", err)
	}
}

func TestReset_NotStarted(t *testing.T) {
	orch, _ := newTestOrchestrator(t, defaultMockGit())
	err := orch.ResetWorkspace()
	if err == nil {
		t.Fatal("expected error when orchestrator not started")
	}
}

// ---------------------------------------------------------------------------
// ResetWorkspaceFromDisk
// ---------------------------------------------------------------------------

func TestReset_FromDisk_CompletedWorkflow(t *testing.T) {
	workspaceDir := t.TempDir()
	featureDir := filepath.Join(workspaceDir, "code-reviews", "disk-reset")
	os.MkdirAll(featureDir, 0755)

	state := &CodeReviewStateJSON{
		State:       CRComplete,
		Round:       2,
		FeatureName: "disk-reset",
	}
	if err := SaveCRState(featureDir, state); err != nil {
		t.Fatalf("save state: %v", err)
	}

	if err := ResetWorkspaceFromDisk(workspaceDir, "disk-reset"); err != nil {
		t.Fatalf("ResetWorkspaceFromDisk: %v", err)
	}

	if _, err := os.Stat(featureDir); !os.IsNotExist(err) {
		t.Error("expected workspace directory to be deleted")
	}
}

func TestReset_FromDisk_RunningWorkflow(t *testing.T) {
	workspaceDir := t.TempDir()
	featureDir := filepath.Join(workspaceDir, "code-reviews", "disk-running")
	os.MkdirAll(featureDir, 0755)

	state := &CodeReviewStateJSON{
		State:       CRReviewing,
		Round:       1,
		FeatureName: "disk-running",
	}
	SaveCRState(featureDir, state)

	err := ResetWorkspaceFromDisk(workspaceDir, "disk-running")
	if err == nil {
		t.Fatal("expected error for non-terminal state")
	}
	if !strings.Contains(err.Error(), "cannot reset running workflow") {
		t.Errorf("expected error about running workflow, got: %v", err)
	}
}

func TestReset_FromDisk_UnknownFeature(t *testing.T) {
	workspaceDir := t.TempDir()
	err := ResetWorkspaceFromDisk(workspaceDir, "nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown feature")
	}
	// Should return ErrCRStateNotFound.
	if _, ok := err.(*ErrCRStateNotFound); !ok {
		t.Errorf("expected ErrCRStateNotFound, got %T: %v", err, err)
	}
}

func TestReset_DoesNotTouchTargetRepoBranches(t *testing.T) {
	workspaceDir := t.TempDir()
	featureDir := filepath.Join(workspaceDir, "code-reviews", "branch-test")
	os.MkdirAll(featureDir, 0755)

	// Create a dummy file to represent workspace artefacts.
	dummyFile := filepath.Join(featureDir, "review-output.json")
	os.WriteFile(dummyFile, []byte("{}"), 0644)

	state := &CodeReviewStateJSON{
		State:       CRComplete,
		Round:       1,
		FeatureName: "branch-test",
		CodePath:    t.TempDir(), // target repo
	}
	SaveCRState(featureDir, state)

	// Create a file in the "target repo" to verify it's NOT deleted.
	targetFile := filepath.Join(state.CodePath, "important.go")
	os.WriteFile(targetFile, []byte("package main"), 0644)

	if err := ResetWorkspaceFromDisk(workspaceDir, "branch-test"); err != nil {
		t.Fatalf("ResetWorkspaceFromDisk: %v", err)
	}

	// Workspace should be gone.
	if _, err := os.Stat(featureDir); !os.IsNotExist(err) {
		t.Error("expected workspace deleted")
	}

	// Target repo file should still exist.
	if _, err := os.Stat(targetFile); err != nil {
		t.Error("target repo file should NOT be deleted by reset")
	}
}

// ---------------------------------------------------------------------------
// CRFeatureDir
// ---------------------------------------------------------------------------

func TestCRFeatureDir(t *testing.T) {
	got := CRFeatureDir("/workspace", "my-feature")
	expected := "/workspace/code-reviews/my-feature"
	if got != expected {
		t.Errorf("CRFeatureDir = %q, want %q", got, expected)
	}
}
