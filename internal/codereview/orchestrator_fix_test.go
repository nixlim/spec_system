package codereview

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Mock implementations
// ---------------------------------------------------------------------------

type mockAgentRunner struct {
	exitCode   int
	stderr     string
	costUSD    float64
	durationMS int64
	err        error
	// writeOutput writes the given JSON to the output path.
	writeOutput interface{}
}

func (m *mockAgentRunner) Run(prompt, outputPath string, timeoutSeconds int) (int, string, float64, int64, error) {
	if m.writeOutput != nil {
		data, _ := json.Marshal(m.writeOutput)
		os.WriteFile(outputPath, data, 0644)
	}
	return m.exitCode, m.stderr, m.costUSD, m.durationMS, m.err
}

type fixTestBranchManager struct {
	createCalled bool
	createErr    error
	diffFiles    []string
	diffErr      error
}

func (m *fixTestBranchManager) CreateFixBranch(codePath string, round int) error {
	m.createCalled = true
	return m.createErr
}

func (m *fixTestBranchManager) DiffNameOnly(codePath, baseSHA string) ([]string, error) {
	return m.diffFiles, m.diffErr
}

func (m *fixTestBranchManager) SubmodulePaths(codePath string) ([]string, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// RunFixPhase tests
// ---------------------------------------------------------------------------

func TestRunFixPhaseHappyPath(t *testing.T) {
	tmpDir := t.TempDir()

	fixOutput := FixOutput{
		Round: 1,
		FixesApplied: []FixAction{
			{FindingID: "CRIT-001", Status: FixStatusFixed, Description: "Fixed"},
		},
		TestResults: &TestResults{Total: 10, Passed: 10},
		GitDiffStat: " 1 file changed",
	}

	runner := &mockAgentRunner{
		writeOutput: fixOutput,
		costUSD:     1.5,
		durationMS:  5000,
	}
	bm := &fixTestBranchManager{diffFiles: []string{"internal/foo.go"}}

	cfg := FixPhaseConfig{
		Runner:              runner,
		BranchManager:       bm,
		CodePath:            tmpDir,
		WorkspaceDir:        tmpDir,
		Round:               1,
		CommitMode:          "branch_per_round",
		FindingsPath:        filepath.Join(tmpDir, "findings.json"),
		FixerTimeoutSeconds: 600,
		CriticalMajorIDs:    map[string]bool{"CRIT-001": true},
		HeadSHA:             "abc123",
	}

	result, err := RunFixPhase(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !bm.createCalled {
		t.Error("expected CreateFixBranch to be called in branch_per_round mode")
	}
	if result.RouteDecision.NextState != CRReviewing {
		t.Errorf("next state = %s, want CR_REVIEWING", result.RouteDecision.NextState)
	}
	if result.CostUSD != 1.5 {
		t.Errorf("cost = %f, want 1.5", result.CostUSD)
	}
	if result.FixOutput == nil {
		t.Error("expected non-nil FixOutput")
	}
}

func TestRunFixPhaseDirectCommitSkipsBranch(t *testing.T) {
	tmpDir := t.TempDir()

	fixOutput := FixOutput{
		Round: 1,
		FixesApplied: []FixAction{
			{FindingID: "CRIT-001", Status: FixStatusFixed, Description: "Fixed"},
		},
	}

	runner := &mockAgentRunner{writeOutput: fixOutput}
	bm := &fixTestBranchManager{diffFiles: []string{}}

	cfg := FixPhaseConfig{
		Runner:              runner,
		BranchManager:       bm,
		CodePath:            tmpDir,
		WorkspaceDir:        tmpDir,
		Round:               1,
		CommitMode:          "direct_commit",
		FindingsPath:        filepath.Join(tmpDir, "findings.json"),
		FixerTimeoutSeconds: 600,
		CriticalMajorIDs:    map[string]bool{"CRIT-001": true},
	}

	_, err := RunFixPhase(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if bm.createCalled {
		t.Error("CreateFixBranch should NOT be called in direct_commit mode")
	}
}

func TestRunFixPhaseBranchCreationFailure(t *testing.T) {
	tmpDir := t.TempDir()
	runner := &mockAgentRunner{}
	bm := &fixTestBranchManager{createErr: fmt.Errorf("branch error")}

	cfg := FixPhaseConfig{
		Runner:              runner,
		BranchManager:       bm,
		CodePath:            tmpDir,
		WorkspaceDir:        tmpDir,
		Round:               1,
		CommitMode:          "branch_per_round",
		FindingsPath:        filepath.Join(tmpDir, "findings.json"),
		FixerTimeoutSeconds: 600,
	}

	_, err := RunFixPhase(cfg)
	if err == nil {
		t.Fatal("expected error for branch creation failure")
	}
	if !strings.Contains(err.Error(), "create fix branch") {
		t.Errorf("error should mention branch creation: %v", err)
	}
}

func TestRunFixPhaseAgentFailure(t *testing.T) {
	tmpDir := t.TempDir()

	runner := &mockAgentRunner{
		exitCode: 1,
		stderr:   "out of memory",
	}
	bm := &fixTestBranchManager{}

	cfg := FixPhaseConfig{
		Runner:              runner,
		BranchManager:       bm,
		CodePath:            tmpDir,
		WorkspaceDir:        tmpDir,
		Round:               1,
		CommitMode:          "direct_commit",
		FindingsPath:        filepath.Join(tmpDir, "findings.json"),
		FixerTimeoutSeconds: 600,
	}

	result, err := RunFixPhase(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.RouteDecision.NextState != CRHumanGateFixes {
		t.Errorf("next state = %s, want CR_HUMAN_GATE_FIXES", result.RouteDecision.NextState)
	}
	if len(result.RouteDecision.Warnings) == 0 {
		t.Error("expected warnings for agent failure")
	}
}

func TestRunFixPhaseAgentRunError(t *testing.T) {
	tmpDir := t.TempDir()

	runner := &mockAgentRunner{err: fmt.Errorf("timeout killed")}
	bm := &fixTestBranchManager{}

	cfg := FixPhaseConfig{
		Runner:              runner,
		BranchManager:       bm,
		CodePath:            tmpDir,
		WorkspaceDir:        tmpDir,
		Round:               1,
		CommitMode:          "direct_commit",
		FindingsPath:        filepath.Join(tmpDir, "findings.json"),
		FixerTimeoutSeconds: 600,
	}

	result, err := RunFixPhase(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.RouteDecision.NextState != CRHumanGateFixes {
		t.Errorf("next state = %s, want CR_HUMAN_GATE_FIXES", result.RouteDecision.NextState)
	}
	if !strings.Contains(result.RouteDecision.Reason, "timeout killed") {
		t.Errorf("reason should mention timeout: %v", result.RouteDecision.Reason)
	}
}

func TestRunFixPhaseParseError(t *testing.T) {
	tmpDir := t.TempDir()

	// Runner writes invalid JSON to the output path.
	runner := &mockAgentRunner{}
	outputPath := filepath.Join(tmpDir, "fix-output-round-1.json")
	os.WriteFile(outputPath, []byte("not json"), 0644)

	bm := &fixTestBranchManager{}

	cfg := FixPhaseConfig{
		Runner:              runner,
		BranchManager:       bm,
		CodePath:            tmpDir,
		WorkspaceDir:        tmpDir,
		Round:               1,
		CommitMode:          "direct_commit",
		FindingsPath:        filepath.Join(tmpDir, "findings.json"),
		FixerTimeoutSeconds: 600,
	}

	result, err := RunFixPhase(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.RouteDecision.NextState != CRHumanGateFixes {
		t.Errorf("next state = %s, want CR_HUMAN_GATE_FIXES", result.RouteDecision.NextState)
	}
	if result.RouteDecision.Reason != "fix output parse error" {
		t.Errorf("reason = %q, want 'fix output parse error'", result.RouteDecision.Reason)
	}
}

func TestRunFixPhaseDeferredFinding(t *testing.T) {
	tmpDir := t.TempDir()

	fixOutput := FixOutput{
		Round: 1,
		FixesApplied: []FixAction{
			{FindingID: "CRIT-001", Status: FixStatusFixed, Description: "Fixed"},
			{FindingID: "MAJ-001", Status: FixStatusDeferred, Description: "Needs arch change"},
		},
	}

	runner := &mockAgentRunner{writeOutput: fixOutput}
	bm := &fixTestBranchManager{diffFiles: []string{}}

	cfg := FixPhaseConfig{
		Runner:              runner,
		BranchManager:       bm,
		CodePath:            tmpDir,
		WorkspaceDir:        tmpDir,
		Round:               1,
		CommitMode:          "direct_commit",
		FindingsPath:        filepath.Join(tmpDir, "findings.json"),
		FixerTimeoutSeconds: 600,
		CriticalMajorIDs:    map[string]bool{"CRIT-001": true, "MAJ-001": true},
	}

	result, err := RunFixPhase(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.RouteDecision.NextState != CRHumanGateFixes {
		t.Errorf("next state = %s, want CR_HUMAN_GATE_FIXES (deferred finding)", result.RouteDecision.NextState)
	}
}

func TestRunFixPhaseOutOfScopeFile(t *testing.T) {
	tmpDir := t.TempDir()

	fixOutput := FixOutput{
		Round: 1,
		FixesApplied: []FixAction{
			{FindingID: "CRIT-001", Status: FixStatusFixed, Description: "Fixed"},
		},
	}

	runner := &mockAgentRunner{writeOutput: fixOutput}
	// Simulate a file outside code_path scope using path traversal.
	bm := &fixTestBranchManager{diffFiles: []string{"../../etc/passwd"}}

	cfg := FixPhaseConfig{
		Runner:              runner,
		BranchManager:       bm,
		CodePath:            tmpDir,
		WorkspaceDir:        tmpDir,
		Round:               1,
		CommitMode:          "direct_commit",
		FindingsPath:        filepath.Join(tmpDir, "findings.json"),
		FixerTimeoutSeconds: 600,
		CriticalMajorIDs:    map[string]bool{"CRIT-001": true},
		HeadSHA:             "abc123",
	}

	result, err := RunFixPhase(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.RouteDecision.NextState != CREscalated {
		t.Errorf("next state = %s, want CR_ESCALATED for out-of-scope file", result.RouteDecision.NextState)
	}
	if !strings.Contains(result.RouteDecision.Reason, "outside code_path scope") {
		t.Errorf("reason should mention out-of-scope: %v", result.RouteDecision.Reason)
	}
}

func TestRunFixPhaseTestFailures(t *testing.T) {
	tmpDir := t.TempDir()

	fixOutput := FixOutput{
		Round: 1,
		FixesApplied: []FixAction{
			{FindingID: "CRIT-001", Status: FixStatusFixed, Description: "Fixed"},
		},
		TestResults: &TestResults{Total: 10, Passed: 8, Failed: 2, Failures: []string{"TestFoo", "TestBar"}},
	}

	runner := &mockAgentRunner{writeOutput: fixOutput}
	bm := &fixTestBranchManager{diffFiles: []string{"internal/foo.go"}}

	cfg := FixPhaseConfig{
		Runner:              runner,
		BranchManager:       bm,
		CodePath:            tmpDir,
		WorkspaceDir:        tmpDir,
		Round:               1,
		CommitMode:          "direct_commit",
		FindingsPath:        filepath.Join(tmpDir, "findings.json"),
		FixerTimeoutSeconds: 600,
		CriticalMajorIDs:    map[string]bool{"CRIT-001": true},
		HeadSHA:             "abc123",
	}

	result, err := RunFixPhase(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.RouteDecision.NextState != CRHumanGateFixes {
		t.Errorf("next state = %s, want CR_HUMAN_GATE_FIXES (test failures)", result.RouteDecision.NextState)
	}
}

func TestRunFixPhaseNoHeadSHASkipsValidation(t *testing.T) {
	tmpDir := t.TempDir()

	fixOutput := FixOutput{
		Round: 1,
		FixesApplied: []FixAction{
			{FindingID: "CRIT-001", Status: FixStatusFixed, Description: "Fixed"},
		},
	}

	runner := &mockAgentRunner{writeOutput: fixOutput}
	bm := &fixTestBranchManager{}

	cfg := FixPhaseConfig{
		Runner:              runner,
		BranchManager:       bm,
		CodePath:            tmpDir,
		WorkspaceDir:        tmpDir,
		Round:               1,
		CommitMode:          "direct_commit",
		FindingsPath:        filepath.Join(tmpDir, "findings.json"),
		FixerTimeoutSeconds: 600,
		CriticalMajorIDs:    map[string]bool{"CRIT-001": true},
		HeadSHA:             "", // no SHA = skip validation
	}

	result, err := RunFixPhase(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.RouteDecision.NextState != CRReviewing {
		t.Errorf("next state = %s, want CR_REVIEWING", result.RouteDecision.NextState)
	}
}

// ---------------------------------------------------------------------------
// truncate tests
// ---------------------------------------------------------------------------

func TestTruncate(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Errorf("truncate short: got %q", got)
	}
	if got := truncate("hello world", 5); got != "hello..." {
		t.Errorf("truncate long: got %q", got)
	}
}
