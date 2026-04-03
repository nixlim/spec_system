package codereview

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Mock GitInfoProvider (shared across orchestrator test files)
// ---------------------------------------------------------------------------

type mockGitProvider struct {
	isRepo bool
	branch string
	sha    string
	err    error
}

func (m *mockGitProvider) IsGitRepo(path string) bool { return m.isRepo }
func (m *mockGitProvider) GetBranch(path string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return m.branch, nil
}
func (m *mockGitProvider) GetHeadSHA(path string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return m.sha, nil
}

// ---------------------------------------------------------------------------
// Helper: defaultMockGit and newTestOrchestrator
// ---------------------------------------------------------------------------

func defaultMockGit() *mockGitProvider {
	return &mockGitProvider{
		isRepo: true,
		branch: "main",
		sha:    "abc123def456",
	}
}

// ---------------------------------------------------------------------------
// Helper
// ---------------------------------------------------------------------------

func newTestOrchestrator(t *testing.T, gitProvider GitInfoProvider) (*CodeReviewOrchestrator, string) {
	t.Helper()
	workspaceDir := t.TempDir()
	cfg := DefaultCodeReviewConfig()
	return NewCodeReviewOrchestrator(CROrchestratorConfig{
		WorkspaceDir: workspaceDir,
		Config:       cfg,
		GitProvider:  gitProvider,
	}), workspaceDir
}

// ---------------------------------------------------------------------------
// Start — happy path
// ---------------------------------------------------------------------------

func TestOrchestrator_Start_CreatesWorkspace(t *testing.T) {
	codePath := t.TempDir()
	orch, workspaceDir := newTestOrchestrator(t, defaultMockGit())

	err := orch.Start(StartCodeReviewRequest{
		CodePath:    codePath,
		FeatureName: "my-feature",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	featureDir := filepath.Join(workspaceDir, "code-reviews", "my-feature")
	if _, err := os.Stat(featureDir); err != nil {
		t.Errorf("expected workspace directory to exist: %v", err)
	}
}

func TestOrchestrator_Start_CapturesGitState(t *testing.T) {
	codePath := t.TempDir()
	orch, _ := newTestOrchestrator(t, defaultMockGit())

	if err := orch.Start(StartCodeReviewRequest{
		CodePath:    codePath,
		FeatureName: "test-feature",
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	ws := orch.StateMachine().State()
	if ws.GitBranch != "main" {
		t.Errorf("GitBranch: got %q, want %q", ws.GitBranch, "main")
	}
	if ws.GitHeadSHA != "abc123def456" {
		t.Errorf("GitHeadSHA: got %q, want %q", ws.GitHeadSHA, "abc123def456")
	}
}

func TestOrchestrator_Start_TransitionsToScopeGate(t *testing.T) {
	codePath := t.TempDir()
	orch, _ := newTestOrchestrator(t, defaultMockGit())

	if err := orch.Start(StartCodeReviewRequest{
		CodePath:    codePath,
		FeatureName: "test-feature",
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if orch.StateMachine().Current() != CRHumanGateScope {
		t.Errorf("expected CR_HUMAN_GATE_SCOPE, got %s", orch.StateMachine().Current())
	}
}

// ---------------------------------------------------------------------------
// Start — grill-code mode detection
// ---------------------------------------------------------------------------

func TestOrchestrator_Start_FullContextMode(t *testing.T) {
	codePath := t.TempDir()
	specPath := filepath.Join(t.TempDir(), "spec.md")
	os.WriteFile(specPath, []byte("# Spec"), 0644)
	taskListPath := filepath.Join(t.TempDir(), "tasks.json")
	os.WriteFile(taskListPath, []byte("{}"), 0644)

	orch, _ := newTestOrchestrator(t, defaultMockGit())
	if err := orch.Start(StartCodeReviewRequest{
		CodePath:     codePath,
		FeatureName:  "test-feature",
		SpecPath:     specPath,
		TaskListPath: taskListPath,
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if orch.StateMachine().State().GrillCodeMode != GrillCodeModeFullContext {
		t.Errorf("expected full-context mode, got %s", orch.StateMachine().State().GrillCodeMode)
	}
}

func TestOrchestrator_Start_SpecOnlyMode(t *testing.T) {
	codePath := t.TempDir()
	specPath := filepath.Join(t.TempDir(), "spec.md")
	os.WriteFile(specPath, []byte("# Spec"), 0644)

	orch, _ := newTestOrchestrator(t, defaultMockGit())
	if err := orch.Start(StartCodeReviewRequest{
		CodePath:    codePath,
		FeatureName: "test-feature",
		SpecPath:    specPath,
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if orch.StateMachine().State().GrillCodeMode != GrillCodeModeSpecOnly {
		t.Errorf("expected spec-only mode, got %s", orch.StateMachine().State().GrillCodeMode)
	}
}

func TestOrchestrator_Start_CodeOnlyMode(t *testing.T) {
	codePath := t.TempDir()
	orch, _ := newTestOrchestrator(t, defaultMockGit())

	if err := orch.Start(StartCodeReviewRequest{
		CodePath:    codePath,
		FeatureName: "test-feature",
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if orch.StateMachine().State().GrillCodeMode != GrillCodeModeCodeOnly {
		t.Errorf("expected code-only mode, got %s", orch.StateMachine().State().GrillCodeMode)
	}
}

// ---------------------------------------------------------------------------
// Start — validation errors
// ---------------------------------------------------------------------------

func TestOrchestrator_Start_NonexistentCodePath(t *testing.T) {
	orch, _ := newTestOrchestrator(t, defaultMockGit())

	err := orch.Start(StartCodeReviewRequest{
		CodePath:    "/tmp/nonexistent-path-12345",
		FeatureName: "test-feature",
	})
	if err == nil {
		t.Fatal("expected error for nonexistent code_path")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("expected error containing 'does not exist', got: %v", err)
	}
}

func TestOrchestrator_Start_NotGitRepo(t *testing.T) {
	codePath := t.TempDir()
	orch, _ := newTestOrchestrator(t, &mockGitProvider{isRepo: false})

	err := orch.Start(StartCodeReviewRequest{
		CodePath:    codePath,
		FeatureName: "test-feature",
	})
	if err == nil {
		t.Fatal("expected error for non-git directory")
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("expected error containing 'not a git repository', got: %v", err)
	}
}

func TestOrchestrator_Start_FeatureNameWithSpaces(t *testing.T) {
	orch, _ := newTestOrchestrator(t, defaultMockGit())

	err := orch.Start(StartCodeReviewRequest{
		CodePath:    t.TempDir(),
		FeatureName: "my feature",
	})
	if err == nil {
		t.Fatal("expected error for feature_name with spaces")
	}
	if !strings.Contains(err.Error(), "kebab-case") {
		t.Errorf("expected error containing 'kebab-case', got: %v", err)
	}
}

func TestOrchestrator_Start_FeatureNameUppercase(t *testing.T) {
	orch, _ := newTestOrchestrator(t, defaultMockGit())

	err := orch.Start(StartCodeReviewRequest{
		CodePath:    t.TempDir(),
		FeatureName: "My-Feature",
	})
	if err == nil {
		t.Fatal("expected error for uppercase feature_name")
	}
	if !strings.Contains(err.Error(), "kebab-case") {
		t.Errorf("expected error containing 'kebab-case', got: %v", err)
	}
}

func TestOrchestrator_Start_PathTraversal(t *testing.T) {
	orch, _ := newTestOrchestrator(t, defaultMockGit())

	err := orch.Start(StartCodeReviewRequest{
		CodePath:    "/tmp/../etc/passwd",
		FeatureName: "test-feature",
	})
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
	if !strings.Contains(err.Error(), "..") {
		t.Errorf("expected error containing path traversal indicator, got: %v", err)
	}
}

func TestOrchestrator_Start_NonexistentSpecPath(t *testing.T) {
	codePath := t.TempDir()
	orch, _ := newTestOrchestrator(t, defaultMockGit())

	err := orch.Start(StartCodeReviewRequest{
		CodePath:    codePath,
		FeatureName: "test-feature",
		SpecPath:    "/tmp/nonexistent-spec.md",
	})
	if err == nil {
		t.Fatal("expected error for nonexistent spec_path")
	}
	if !strings.Contains(err.Error(), "spec file not found") {
		t.Errorf("expected error containing 'spec file not found', got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// HandleScopeGate
// ---------------------------------------------------------------------------

func TestOrchestrator_HandleScopeGate_Confirm(t *testing.T) {
	codePath := t.TempDir()
	orch, _ := newTestOrchestrator(t, defaultMockGit())

	if err := orch.Start(StartCodeReviewRequest{
		CodePath:    codePath,
		FeatureName: "test-feature",
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := orch.HandleScopeGate(CRGateResponse{Action: "confirm"}); err != nil {
		t.Fatalf("HandleScopeGate: %v", err)
	}

	if orch.StateMachine().Current() != CRReviewing {
		t.Errorf("expected CR_REVIEWING, got %s", orch.StateMachine().Current())
	}
}

func TestOrchestrator_HandleScopeGate_Cancel(t *testing.T) {
	codePath := t.TempDir()
	orch, _ := newTestOrchestrator(t, defaultMockGit())

	if err := orch.Start(StartCodeReviewRequest{
		CodePath:    codePath,
		FeatureName: "test-feature",
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := orch.HandleScopeGate(CRGateResponse{Action: "cancel"}); err != nil {
		t.Fatalf("HandleScopeGate: %v", err)
	}

	if orch.StateMachine().Current() != CREscalated {
		t.Errorf("expected CR_ESCALATED, got %s", orch.StateMachine().Current())
	}

	reason := orch.StateMachine().State().EscalationReason
	if !strings.Contains(reason, "operator cancelled") {
		t.Errorf("expected escalation reason containing 'operator cancelled', got: %q", reason)
	}
}

func TestOrchestrator_HandleScopeGate_InvalidAction(t *testing.T) {
	codePath := t.TempDir()
	orch, _ := newTestOrchestrator(t, defaultMockGit())

	if err := orch.Start(StartCodeReviewRequest{
		CodePath:    codePath,
		FeatureName: "test-feature",
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	err := orch.HandleScopeGate(CRGateResponse{Action: "invalid"})
	if err == nil {
		t.Fatal("expected error for invalid gate action")
	}
}

func TestOrchestrator_HandleScopeGate_NotStarted(t *testing.T) {
	orch, _ := newTestOrchestrator(t, defaultMockGit())
	err := orch.HandleScopeGate(CRGateResponse{Action: "confirm"})
	if err == nil {
		t.Fatal("expected error when orchestrator not started")
	}
}

func TestOrchestrator_HandleScopeGate_WrongState(t *testing.T) {
	codePath := t.TempDir()
	orch, _ := newTestOrchestrator(t, defaultMockGit())

	if err := orch.Start(StartCodeReviewRequest{
		CodePath:    codePath,
		FeatureName: "test-feature",
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Confirm first to move past scope gate.
	if err := orch.HandleScopeGate(CRGateResponse{Action: "confirm"}); err != nil {
		t.Fatalf("HandleScopeGate confirm: %v", err)
	}

	// Try scope gate again — should fail.
	err := orch.HandleScopeGate(CRGateResponse{Action: "confirm"})
	if err == nil {
		t.Fatal("expected error when not at scope gate")
	}
}

// ---------------------------------------------------------------------------
// State persisted after transitions
// ---------------------------------------------------------------------------

func TestOrchestrator_Start_PersistsState(t *testing.T) {
	codePath := t.TempDir()
	orch, workspaceDir := newTestOrchestrator(t, defaultMockGit())

	if err := orch.Start(StartCodeReviewRequest{
		CodePath:    codePath,
		FeatureName: "test-feature",
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Verify workflow-state.json was created.
	stateFile := filepath.Join(workspaceDir, "code-reviews", "test-feature", "workflow-state.json")
	if _, err := os.Stat(stateFile); err != nil {
		t.Errorf("expected workflow-state.json to exist: %v", err)
	}

	// Load and verify.
	loaded, err := LoadCRState(filepath.Join(workspaceDir, "code-reviews", "test-feature"))
	if err != nil {
		t.Fatalf("LoadCRState: %v", err)
	}
	if loaded.State != CRHumanGateScope {
		t.Errorf("expected persisted state CR_HUMAN_GATE_SCOPE, got %s", loaded.State)
	}
	if loaded.FeatureName != "test-feature" {
		t.Errorf("expected feature_name 'test-feature', got %q", loaded.FeatureName)
	}
}

// ---------------------------------------------------------------------------
// Valid kebab-case feature names
// ---------------------------------------------------------------------------

func TestOrchestrator_Start_ValidFeatureNames(t *testing.T) {
	validNames := []string{"my-feature", "test", "a-b-c", "feature1", "test-123"}
	for _, name := range validNames {
		t.Run(name, func(t *testing.T) {
			codePath := t.TempDir()
			orch, _ := newTestOrchestrator(t, defaultMockGit())
			err := orch.Start(StartCodeReviewRequest{
				CodePath:    codePath,
				FeatureName: name,
			})
			if err != nil {
				t.Errorf("expected valid feature name %q to be accepted: %v", name, err)
			}
		})
	}
}

func TestOrchestrator_Start_InvalidFeatureNames(t *testing.T) {
	invalidNames := []string{
		"My-Feature",
		"my feature",
		"my_feature",
		"-leading",
		"trailing-",
		"",
		"UPPERCASE",
	}
	for _, name := range invalidNames {
		t.Run(fmt.Sprintf("%q", name), func(t *testing.T) {
			codePath := t.TempDir()
			orch, _ := newTestOrchestrator(t, defaultMockGit())
			err := orch.Start(StartCodeReviewRequest{
				CodePath:    codePath,
				FeatureName: name,
			})
			if err == nil {
				t.Errorf("expected invalid feature name %q to be rejected", name)
			}
		})
	}
}
