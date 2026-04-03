package codereview

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/foundry-zero/adversarial-spec-system/internal/specworkflow"
)

// ---------------------------------------------------------------------------
// E2E test helpers
// ---------------------------------------------------------------------------

// initGitRepo creates a real git repo in a temp directory with an initial commit.
func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	cmds := [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git init setup %v: %s: %v", args, out, err)
		}
	}

	// Write a file and commit so HEAD exists.
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"git", "add", "."},
		{"git", "commit", "-m", "initial"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git commit setup %v: %s: %v", args, out, err)
		}
	}

	return dir
}

// e2eBranchManager records branch creation calls without using git.
type e2eBranchManager struct {
	createdBranches []string
	diffFiles       []string
}

func (m *e2eBranchManager) CreateFixBranch(_ string, round int) error {
	m.createdBranches = append(m.createdBranches, fmt.Sprintf("cr-fix-round-%d", round))
	return nil
}

func (m *e2eBranchManager) DiffNameOnly(_, _ string) ([]string, error) {
	return m.diffFiles, nil
}

func (m *e2eBranchManager) SubmodulePaths(_ string) ([]string, error) {
	return nil, nil
}

// e2eGitStatusChecker implements GitStatusChecker for recovery tests.
type e2eGitStatusChecker struct {
	hasChanges bool
	files      []string
	err        error
}

func (m *e2eGitStatusChecker) HasUncommittedChanges(_ string) (bool, []string, error) {
	return m.hasChanges, m.files, m.err
}

// reviewRunnerWithFindings creates a mock runner that writes reviewer output
// with the given severity for all lenses.
func reviewRunnerWithFindings(severity specworkflow.Severity) *mockReviewRunner {
	return &mockReviewRunner{
		handler: func(_, outputPath string, _ int) (int, string, float64, int64, error) {
			lens := lensFromOutputPath(outputPath)
			os.WriteFile(outputPath, validCodeReviewOutput(lens, severity), 0644)
			return 0, "", 0.02, 200, nil
		},
	}
}

// reviewRunnerNoFindings creates a mock runner that writes reviewer output
// with zero findings.
func reviewRunnerNoFindings() *mockReviewRunner {
	return &mockReviewRunner{
		handler: func(_, outputPath string, _ int) (int, string, float64, int64, error) {
			lens := lensFromOutputPath(outputPath)
			os.WriteFile(outputPath, validCodeReviewOutputNoFindings(lens), 0644)
			return 0, "", 0.01, 100, nil
		},
	}
}

// fixRunnerAllFixed creates a mock runner that writes a FixOutput with all
// provided finding IDs marked as "fixed".
func fixRunnerAllFixed(findingIDs []string) *mockReviewRunner {
	return &mockReviewRunner{
		handler: func(_, outputPath string, _ int) (int, string, float64, int64, error) {
			var fixes []FixAction
			for _, id := range findingIDs {
				fixes = append(fixes, FixAction{
					FindingID:     id,
					Status:        FixStatusFixed,
					FilesModified: []string{"main.go"},
					Description:   "Fixed " + id,
				})
			}
			output := FixOutput{
				Round:        1,
				FixesApplied: fixes,
				TestResults:  &TestResults{Total: 10, Passed: 10, Failed: 0},
				GitDiffStat:  " main.go | 5 ++---\n 1 file changed",
			}
			data, _ := json.MarshalIndent(output, "", "  ")
			os.WriteFile(outputPath, data, 0644)
			return 0, "", 0.05, 500, nil
		},
	}
}

// ---------------------------------------------------------------------------
// TestE2EReviewFixCycle
// ---------------------------------------------------------------------------

// TestE2EReviewFixCycle exercises a full review → fix → re-review → pass cycle
// with a real temp git repo, real file I/O, and mock agent runners.
//
// Traces to: Spec test #59, SC-007.
func TestE2EReviewFixCycle(t *testing.T) {
	repoDir := initGitRepo(t)
	workspaceDir := t.TempDir()

	// Round 1 review: returns CRITICAL findings → routes to CR_FIXING.
	round1ReviewRunner := reviewRunnerWithFindings(specworkflow.SeverityCritical)

	// Round 1 fix: marks all CRITICAL findings as fixed → routes to CR_REVIEWING.
	// We'll swap the runner between phases.
	round2ReviewRunner := reviewRunnerNoFindings()

	branchMgr := &e2eBranchManager{diffFiles: []string{"main.go"}}

	cfg := CROrchestratorConfig{
		WorkspaceDir: workspaceDir,
		Config:       DefaultCodeReviewConfig(),
		GitProvider:  &defaultGitInfoProvider{},
		Runner:       round1ReviewRunner,
	}
	cfg.Config.CommitMode = "branch_per_round"
	orch := NewCodeReviewOrchestrator(cfg)

	// --- Start workflow ---
	err := orch.Start(StartCodeReviewRequest{
		CodePath:    repoDir,
		FeatureName: "e2e-review-fix",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if orch.StateMachine().Current() != CRHumanGateScope {
		t.Fatalf("expected CR_HUMAN_GATE_SCOPE, got %s", orch.StateMachine().Current())
	}

	// Verify workspace was created on disk.
	featureDir := orch.FeatureDir()
	if _, err := os.Stat(featureDir); err != nil {
		t.Fatalf("workspace directory not created: %v", err)
	}

	// Verify state persisted.
	loaded, err := LoadCRState(featureDir)
	if err != nil {
		t.Fatalf("LoadCRState: %v", err)
	}
	if loaded.State != CRHumanGateScope {
		t.Errorf("persisted state = %s, want CR_HUMAN_GATE_SCOPE", loaded.State)
	}

	// --- Scope gate: confirm ---
	err = orch.HandleScopeGate(CRGateResponse{Action: "confirm"})
	if err != nil {
		t.Fatalf("HandleScopeGate: %v", err)
	}
	if orch.StateMachine().Current() != CRReviewing {
		t.Fatalf("expected CR_REVIEWING, got %s", orch.StateMachine().Current())
	}

	// --- Round 1 review: CRITICAL findings → CR_FIXING ---
	err = orch.HandleReviewing()
	if err != nil {
		t.Fatalf("HandleReviewing round 1: %v", err)
	}
	if orch.StateMachine().Current() != CRFixing {
		t.Fatalf("expected CR_FIXING after critical findings, got %s", orch.StateMachine().Current())
	}

	// Verify findings file written.
	findingsPath := filepath.Join(featureDir, "code-findings-round-1.json")
	if _, err := os.Stat(findingsPath); err != nil {
		t.Fatalf("findings file not written: %v", err)
	}

	// Verify findings summary populated.
	summary := orch.StateMachine().State().FindingsSummary
	if summary.OpenCritical < 1 {
		t.Errorf("expected OpenCritical >= 1, got %d", summary.OpenCritical)
	}

	// Verify cost accumulated.
	if orch.StateMachine().State().CumulativeCostUSD <= 0 {
		t.Error("expected non-zero cumulative cost")
	}

	// --- Round 1 fix: all fixed → CR_REVIEWING ---
	// Read findings to get finding IDs for the fix runner.
	findingsData, _ := os.ReadFile(findingsPath)
	var merged specworkflow.MergedFindings
	json.Unmarshal(findingsData, &merged)

	var critIDs []string
	for _, f := range merged.Findings {
		if f.Severity == specworkflow.SeverityCritical {
			critIDs = append(critIDs, f.ID)
		}
	}

	critIDSet := make(map[string]bool)
	for _, id := range critIDs {
		critIDSet[id] = true
	}

	fixRunner := fixRunnerAllFixed(critIDs)
	fixResult, err := RunFixPhase(FixPhaseConfig{
		Runner:              fixRunner,
		BranchManager:       branchMgr,
		CodePath:            repoDir,
		WorkspaceDir:        featureDir,
		Round:               1,
		CommitMode:          "branch_per_round",
		FindingsPath:        findingsPath,
		FixerTimeoutSeconds: 300,
		CriticalMajorIDs:    critIDSet,
		HeadSHA:             orch.StateMachine().State().GitHeadSHA,
	})
	if err != nil {
		t.Fatalf("RunFixPhase: %v", err)
	}

	// Verify branch was created.
	if len(branchMgr.createdBranches) != 1 || branchMgr.createdBranches[0] != "cr-fix-round-1" {
		t.Errorf("expected branch cr-fix-round-1, got %v", branchMgr.createdBranches)
	}

	// Verify fix output was parsed.
	if fixResult.FixOutput == nil {
		t.Fatal("expected parsed FixOutput")
	}

	// Verify routing: all CRIT fixed → re-review.
	if fixResult.RouteDecision.NextState != CRReviewing {
		t.Errorf("expected route to CR_REVIEWING, got %s (reason: %s)",
			fixResult.RouteDecision.NextState, fixResult.RouteDecision.Reason)
	}

	// Transition to CR_REVIEWING for round 2.
	// Store fix output on orchestrator for gate data.
	orch.lastFixOutput = fixResult.FixOutput
	orch.StateMachine().State().CumulativeCostUSD += fixResult.CostUSD
	orch.StateMachine().State().AgentInvocations++

	// Transition: CR_FIXING → CR_REVIEWING.
	err = orch.StateMachine().Transition(CRReviewing)
	if err != nil {
		t.Fatalf("transition to CR_REVIEWING: %v", err)
	}

	// --- Round 2 review: no findings → CR_COMPLETE ---
	// Swap to a clean runner and increment round.
	orch.runner = round2ReviewRunner
	orch.StateMachine().State().Round++

	err = orch.HandleReviewing()
	if err != nil {
		t.Fatalf("HandleReviewing round 2: %v", err)
	}
	if orch.StateMachine().Current() != CRComplete {
		t.Fatalf("expected CR_COMPLETE after clean review, got %s", orch.StateMachine().Current())
	}
	if orch.StateMachine().State().Verdict != CodeReviewVerdictPass {
		t.Errorf("expected PASS verdict, got %s", orch.StateMachine().State().Verdict)
	}

	// --- Verify workspace cleanup works on terminal state ---
	err = orch.ResetWorkspace()
	if err != nil {
		t.Fatalf("ResetWorkspace: %v", err)
	}
	if _, err := os.Stat(featureDir); !os.IsNotExist(err) {
		t.Error("expected workspace directory to be deleted after reset")
	}
}

// ---------------------------------------------------------------------------
// TestE2ECrashRecoveryMidReview
// ---------------------------------------------------------------------------

// TestE2ECrashRecoveryMidReview simulates a crash during the review phase
// where some agent outputs exist and others don't. Recovery should identify
// the missing agents for re-dispatch.
//
// Traces to: Spec test #60.
func TestE2ECrashRecoveryMidReview(t *testing.T) {
	workspaceDir := t.TempDir()
	featureDir := filepath.Join(workspaceDir, "code-reviews", "mid-review-crash")
	os.MkdirAll(featureDir, 0755)

	// Simulate partial review: write valid output for some lenses, skip others.
	// File naming: review-{provider}-{lens}-round-{N}.json (matches reviewOutputFileName).
	completedLenses := []string{"correctness", "security", "testing"}
	for _, lens := range completedLenses {
		outputPath := filepath.Join(featureDir, fmt.Sprintf("review-%s-claude-round-1.json", lens))
		os.WriteFile(outputPath, validCodeReviewOutputNoFindings(lens), 0644)
	}

	// Save state as CR_REVIEWING.
	state := &CodeReviewStateJSON{
		State:       CRReviewing,
		Round:       1,
		FeatureName: "mid-review-crash",
		CodePath:    "/tmp/repo",
		GitBranch:   "main",
		GitHeadSHA:  "abc123",
		StartedAt:   "2026-03-28T22:00:00Z",
		UpdatedAt:   "2026-03-28T22:05:00Z",
	}
	if err := SaveCRState(featureDir, state); err != nil {
		t.Fatalf("SaveCRState: %v", err)
	}

	// --- Simulate crash and recovery ---
	loaded, err := LoadCRState(featureDir)
	if err != nil {
		t.Fatalf("LoadCRState: %v", err)
	}
	if loaded.State != CRReviewing {
		t.Fatalf("expected CR_REVIEWING, got %s", loaded.State)
	}

	recovery, err := RecoverFromCrash(featureDir, loaded, false, nil)
	if err != nil {
		t.Fatalf("RecoverFromCrash: %v", err)
	}

	if recovery.Type != RecoveryReDispatchReviewers {
		t.Fatalf("expected RecoveryReDispatchReviewers, got %s", recovery.Type)
	}

	// The missing lenses should be in the re-dispatch list.
	missingLenses := []string{"error-handling", "observability", "overcomplexity"}
	for _, lens := range missingLenses {
		agentName := fmt.Sprintf("reviewer-%s-claude", lens)
		found := false
		for _, a := range recovery.AgentsToReDispatch {
			if a == agentName {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected %s in re-dispatch list, got %v", agentName, recovery.AgentsToReDispatch)
		}
	}

	// Completed lenses should NOT be in the re-dispatch list.
	for _, lens := range completedLenses {
		agentName := fmt.Sprintf("reviewer-%s-claude", lens)
		for _, a := range recovery.AgentsToReDispatch {
			if a == agentName {
				t.Errorf("did not expect %s in re-dispatch list (output already present)", agentName)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// TestE2ECrashRecoveryCorruptOutput
// ---------------------------------------------------------------------------

// TestE2ECrashRecoveryCorruptOutput simulates a crash that left a corrupt
// (invalid JSON) output file. Recovery should detect it, delete it, and
// add the agent to the re-dispatch list.
//
// Traces to: Spec test #61.
func TestE2ECrashRecoveryCorruptOutput(t *testing.T) {
	workspaceDir := t.TempDir()
	featureDir := filepath.Join(workspaceDir, "code-reviews", "corrupt-output")
	os.MkdirAll(featureDir, 0755)

	// Write valid outputs for all lenses except one.
	// File naming: review-{provider}-{lens}-round-{N}.json (matches reviewOutputFileName).
	for _, lens := range CodeReviewLensGroups {
		outputPath := filepath.Join(featureDir, fmt.Sprintf("review-%s-claude-round-1.json", lens))
		if lens == "security" {
			// Write corrupt JSON for the security lens.
			os.WriteFile(outputPath, []byte(`{"truncated": true, "findings": [`), 0644)
		} else {
			os.WriteFile(outputPath, validCodeReviewOutputNoFindings(lens), 0644)
		}
	}

	// Save state as CR_REVIEWING.
	state := &CodeReviewStateJSON{
		State:       CRReviewing,
		Round:       1,
		FeatureName: "corrupt-output",
		CodePath:    "/tmp/repo",
		GitBranch:   "main",
		GitHeadSHA:  "def456",
		StartedAt:   "2026-03-28T22:00:00Z",
		UpdatedAt:   "2026-03-28T22:05:00Z",
	}
	if err := SaveCRState(featureDir, state); err != nil {
		t.Fatalf("SaveCRState: %v", err)
	}

	// --- Recovery ---
	loaded, err := LoadCRState(featureDir)
	if err != nil {
		t.Fatalf("LoadCRState: %v", err)
	}

	recovery, err := RecoverFromCrash(featureDir, loaded, false, nil)
	if err != nil {
		t.Fatalf("RecoverFromCrash: %v", err)
	}

	if recovery.Type != RecoveryReDispatchReviewers {
		t.Fatalf("expected RecoveryReDispatchReviewers, got %s", recovery.Type)
	}

	// The corrupt security agent should be in the re-dispatch list.
	found := false
	for _, a := range recovery.AgentsToReDispatch {
		if a == "reviewer-security-claude" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected reviewer-security-claude in re-dispatch list, got %v", recovery.AgentsToReDispatch)
	}

	// The corrupt file should have been deleted by isValidOutputFile.
	corruptPath := filepath.Join(featureDir, "review-security-claude-round-1.json")
	if _, err := os.Stat(corruptPath); !os.IsNotExist(err) {
		t.Error("expected corrupt output file to be deleted during recovery")
	}

	// Other agents should NOT be in the re-dispatch list.
	for _, a := range recovery.AgentsToReDispatch {
		if a != "reviewer-security-claude" {
			t.Errorf("unexpected agent in re-dispatch list: %s", a)
		}
	}
}

// ---------------------------------------------------------------------------
// TestE2ECrashRecoveryFixPhase
// ---------------------------------------------------------------------------

// TestE2ECrashRecoveryFixPhase simulates a crash during the fix phase with
// uncommitted changes. Recovery should route to the human gate with a warning.
func TestE2ECrashRecoveryFixPhase(t *testing.T) {
	workspaceDir := t.TempDir()
	featureDir := filepath.Join(workspaceDir, "code-reviews", "fix-crash")
	os.MkdirAll(featureDir, 0755)

	state := &CodeReviewStateJSON{
		State:       CRFixing,
		Round:       1,
		FeatureName: "fix-crash",
		CodePath:    "/tmp/repo",
		GitBranch:   "main",
		GitHeadSHA:  "ghi789",
		StartedAt:   "2026-03-28T22:00:00Z",
		UpdatedAt:   "2026-03-28T22:10:00Z",
	}
	if err := SaveCRState(featureDir, state); err != nil {
		t.Fatalf("SaveCRState: %v", err)
	}

	gitChecker := &e2eGitStatusChecker{
		hasChanges: true,
		files:      []string{"main.go", "handler.go"},
	}

	loaded, err := LoadCRState(featureDir)
	if err != nil {
		t.Fatalf("LoadCRState: %v", err)
	}

	recovery, err := RecoverFromCrash(featureDir, loaded, false, gitChecker)
	if err != nil {
		t.Fatalf("RecoverFromCrash: %v", err)
	}

	if recovery.Type != RecoveryRouteToGate {
		t.Fatalf("expected RecoveryRouteToGate, got %s", recovery.Type)
	}
	if recovery.NextState != CRHumanGateFixes {
		t.Errorf("expected next state CR_HUMAN_GATE_FIXES, got %s", recovery.NextState)
	}
	if !strings.Contains(recovery.Warning, "partial fix") {
		t.Errorf("expected warning containing 'partial fix', got %q", recovery.Warning)
	}
	if len(recovery.UncommittedFiles) != 2 {
		t.Errorf("expected 2 uncommitted files, got %d", len(recovery.UncommittedFiles))
	}
}

// ---------------------------------------------------------------------------
// TestE2EWorkspaceCleanup
// ---------------------------------------------------------------------------

// TestE2EWorkspaceCleanup verifies that ResetWorkspace and
// ResetWorkspaceFromDisk both correctly remove workspace artefacts.
func TestE2EWorkspaceCleanup(t *testing.T) {
	workspaceDir := t.TempDir()
	featureName := "cleanup-test"
	featureDir := CRFeatureDir(workspaceDir, featureName)
	os.MkdirAll(featureDir, 0755)

	// Write some artefacts.
	os.WriteFile(filepath.Join(featureDir, "code-findings-round-1.json"), []byte("[]"), 0644)
	os.WriteFile(filepath.Join(featureDir, "fix-output-round-1.json"), []byte("{}"), 0644)

	// Save terminal state.
	state := &CodeReviewStateJSON{
		State:       CRComplete,
		FeatureName: featureName,
		Round:       2,
		StartedAt:   "2026-03-28T22:00:00Z",
		UpdatedAt:   "2026-03-28T22:30:00Z",
	}
	if err := SaveCRState(featureDir, state); err != nil {
		t.Fatalf("SaveCRState: %v", err)
	}

	// Verify files exist.
	if _, err := os.Stat(filepath.Join(featureDir, "code-findings-round-1.json")); err != nil {
		t.Fatalf("artefact not found before cleanup: %v", err)
	}

	// Reset from disk.
	err := ResetWorkspaceFromDisk(workspaceDir, featureName)
	if err != nil {
		t.Fatalf("ResetWorkspaceFromDisk: %v", err)
	}

	// Verify everything is gone.
	if _, err := os.Stat(featureDir); !os.IsNotExist(err) {
		t.Error("expected workspace directory to be deleted")
	}
}

// ---------------------------------------------------------------------------
// TestE2EResetRunningWorkflowRejected
// ---------------------------------------------------------------------------

// TestE2EResetRunningWorkflowRejected verifies that reset is rejected when
// the workflow is not in a terminal state.
func TestE2EResetRunningWorkflowRejected(t *testing.T) {
	workspaceDir := t.TempDir()
	featureName := "reset-reject"
	featureDir := CRFeatureDir(workspaceDir, featureName)
	os.MkdirAll(featureDir, 0755)

	state := &CodeReviewStateJSON{
		State:       CRReviewing,
		FeatureName: featureName,
		Round:       1,
		StartedAt:   "2026-03-28T22:00:00Z",
		UpdatedAt:   "2026-03-28T22:05:00Z",
	}
	if err := SaveCRState(featureDir, state); err != nil {
		t.Fatalf("SaveCRState: %v", err)
	}

	err := ResetWorkspaceFromDisk(workspaceDir, featureName)
	if err == nil {
		t.Fatal("expected error when resetting running workflow")
	}
	if !strings.Contains(err.Error(), "cannot reset running workflow") {
		t.Errorf("expected 'cannot reset running workflow' error, got: %v", err)
	}

	// Verify workspace still exists.
	if _, err := os.Stat(featureDir); err != nil {
		t.Error("workspace should still exist after rejected reset")
	}
}

// ---------------------------------------------------------------------------
// TestE2EFixOutputParsing
// ---------------------------------------------------------------------------

// TestE2EFixOutputParsing verifies FixOutput parsing and routing through the
// full RunFixPhase pipeline with real file I/O.
func TestE2EFixOutputParsing(t *testing.T) {
	workspaceDir := t.TempDir()
	codePath := t.TempDir()

	// Write a findings file.
	findingsPath := filepath.Join(workspaceDir, "code-findings-round-1.json")
	findings := specworkflow.MergedFindings{
		SchemaVersion: "1.0",
		Findings: []specworkflow.MergedFinding{
			{
				ID:       "CRIT-001",
				Severity: specworkflow.SeverityCritical,
			},
			{
				ID:       "MAJ-001",
				Severity: specworkflow.SeverityMajor,
			},
		},
	}
	findingsData, _ := json.MarshalIndent(findings, "", "  ")
	os.WriteFile(findingsPath, findingsData, 0644)

	critIDs := map[string]bool{"CRIT-001": true, "MAJ-001": true}
	fixRunner := fixRunnerAllFixed([]string{"CRIT-001", "MAJ-001"})
	branchMgr := &e2eBranchManager{diffFiles: []string{"main.go"}}

	result, err := RunFixPhase(FixPhaseConfig{
		Runner:              fixRunner,
		BranchManager:       branchMgr,
		CodePath:            codePath,
		WorkspaceDir:        workspaceDir,
		Round:               1,
		CommitMode:          "branch_per_round",
		FindingsPath:        findingsPath,
		FixerTimeoutSeconds: 300,
		CriticalMajorIDs:    critIDs,
		HeadSHA:             "abc123",
	})
	if err != nil {
		t.Fatalf("RunFixPhase: %v", err)
	}

	if result.FixOutput == nil {
		t.Fatal("expected parsed FixOutput")
	}
	if result.FixOutput.Round != 1 {
		t.Errorf("FixOutput.Round = %d, want 1", result.FixOutput.Round)
	}
	if len(result.FixOutput.FixesApplied) != 2 {
		t.Errorf("FixesApplied count = %d, want 2", len(result.FixOutput.FixesApplied))
	}

	// All CRIT+MAJ fixed → route to re-review.
	if result.RouteDecision.NextState != CRReviewing {
		t.Errorf("expected route to CR_REVIEWING, got %s", result.RouteDecision.NextState)
	}

	// Verify fix output file was written to disk.
	fixOutputPath := filepath.Join(workspaceDir, "fix-output-round-1.json")
	if _, err := os.Stat(fixOutputPath); err != nil {
		t.Errorf("expected fix output file to be written: %v", err)
	}
}

// ---------------------------------------------------------------------------
// TestE2EGateStateRecovery
// ---------------------------------------------------------------------------

// TestE2EGateStateRecovery verifies that recovery from gate states simply
// resumes at the gate.
func TestE2EGateStateRecovery(t *testing.T) {
	for _, tc := range []struct {
		state CodeReviewState
		name  string
	}{
		{CRHumanGateScope, "scope"},
		{CRHumanGateFixes, "fixes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspaceDir := t.TempDir()
			featureDir := filepath.Join(workspaceDir, "code-reviews", "gate-recovery-"+tc.name)
			os.MkdirAll(featureDir, 0755)

			state := &CodeReviewStateJSON{
				State:       tc.state,
				FeatureName: "gate-recovery-" + tc.name,
				Round:       1,
				StartedAt:   "2026-03-28T22:00:00Z",
				UpdatedAt:   "2026-03-28T22:05:00Z",
			}
			if err := SaveCRState(featureDir, state); err != nil {
				t.Fatalf("SaveCRState: %v", err)
			}

			loaded, err := LoadCRState(featureDir)
			if err != nil {
				t.Fatalf("LoadCRState: %v", err)
			}

			recovery, err := RecoverFromCrash(featureDir, loaded, false, nil)
			if err != nil {
				t.Fatalf("RecoverFromCrash: %v", err)
			}

			if recovery.Type != RecoveryResumeAtGate {
				t.Errorf("expected RecoveryResumeAtGate, got %s", recovery.Type)
			}
			if recovery.NextState != tc.state {
				t.Errorf("expected next state %s, got %s", tc.state, recovery.NextState)
			}
		})
	}
}
