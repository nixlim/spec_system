package codereview

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/foundry-zero/adversarial-spec-system/internal/specworkflow"
)

// ---------------------------------------------------------------------------
// Mock agent runner for review tests
// ---------------------------------------------------------------------------

type mockReviewRunner struct {
	handler func(prompt, outputPath string, timeoutSeconds int) (int, string, float64, int64, error)
}

func (m *mockReviewRunner) Run(prompt, outputPath string, timeoutSeconds int) (int, string, float64, int64, error) {
	return m.handler(prompt, outputPath, timeoutSeconds)
}

// validCodeReviewOutput returns valid ReviewerOutput JSON with a finding.
func validCodeReviewOutput(lens string, severity specworkflow.Severity) []byte {
	output := specworkflow.ReviewerOutput{
		SchemaVersion:      "1.0",
		Agent:              "reviewer-" + lens + "-claude",
		Round:              1,
		MarkdownReportFile: "report-" + lens + ".md",
		LensesApplied:      []string{lens},
		Findings: []specworkflow.Finding{
			{
				ID:              "F-001",
				Description:     "Test finding in " + lens,
				Severity:        severity,
				Impact:          "Test impact",
				Recommendation:  "Fix it",
				Lens:            lens,
				AffectedSection: "main.go:1-10",
			},
		},
	}
	data, _ := json.Marshal(output)
	return data
}

// validCodeReviewOutputNoFindings returns valid ReviewerOutput JSON with zero findings.
func validCodeReviewOutputNoFindings(lens string) []byte {
	output := specworkflow.ReviewerOutput{
		SchemaVersion:      "1.0",
		Agent:              "reviewer-" + lens + "-claude",
		Round:              1,
		MarkdownReportFile: "report-" + lens + ".md",
		LensesApplied:      []string{lens},
		Findings:           []specworkflow.Finding{},
	}
	data, _ := json.Marshal(output)
	return data
}

// setupReviewOrchestrator creates an orchestrator in CR_REVIEWING state.
func setupReviewOrchestrator(t *testing.T, runner specworkflow.AgentRunner, codexRunner specworkflow.AgentRunner) (*CodeReviewOrchestrator, string) {
	t.Helper()
	dir := t.TempDir()
	codePath := filepath.Join(dir, "repo")
	os.MkdirAll(codePath, 0755)

	cfg := CROrchestratorConfig{
		WorkspaceDir: dir,
		Config:       DefaultCodeReviewConfig(),
		GitProvider:  &mockGitProvider{isRepo: true, branch: "main", sha: "abc123"},
		Runner:       runner,
		CodexRunner:  codexRunner,
	}
	orch := NewCodeReviewOrchestrator(cfg)

	// Start the orchestrator.
	err := orch.Start(StartCodeReviewRequest{
		CodePath:    codePath,
		FeatureName: "test-review",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Confirm scope gate to move to CR_REVIEWING.
	err = orch.HandleScopeGate(CRGateResponse{Action: "confirm"})
	if err != nil {
		t.Fatalf("HandleScopeGate: %v", err)
	}

	if orch.StateMachine().Current() != CRReviewing {
		t.Fatalf("expected CR_REVIEWING, got %s", orch.StateMachine().Current())
	}

	return orch, dir
}

// mockGitProvider is declared in orchestrator_test.go (shared across test files).

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestHandleReviewing_ZeroFindings_TransitionsToComplete(t *testing.T) {
	runner := &mockReviewRunner{
		handler: func(prompt, outputPath string, _ int) (int, string, float64, int64, error) {
			lens := lensFromOutputPath(outputPath)
			os.WriteFile(outputPath, validCodeReviewOutputNoFindings(lens), 0644)
			return 0, "", 0.01, 100, nil
		},
	}

	orch, _ := setupReviewOrchestrator(t, runner, nil)

	err := orch.HandleReviewing()
	if err != nil {
		t.Fatalf("HandleReviewing: %v", err)
	}

	if orch.StateMachine().Current() != CRComplete {
		t.Errorf("expected CR_COMPLETE, got %s", orch.StateMachine().Current())
	}
	if orch.StateMachine().State().Verdict != CodeReviewVerdictPass {
		t.Errorf("expected PASS verdict, got %s", orch.StateMachine().State().Verdict)
	}
}

func TestHandleReviewing_MinorOnly_TransitionsToHumanGate(t *testing.T) {
	runner := &mockReviewRunner{
		handler: func(prompt, outputPath string, _ int) (int, string, float64, int64, error) {
			lens := lensFromOutputPath(outputPath)
			os.WriteFile(outputPath, validCodeReviewOutput(lens, specworkflow.SeverityMinor), 0644)
			return 0, "", 0.01, 100, nil
		},
	}

	orch, _ := setupReviewOrchestrator(t, runner, nil)

	err := orch.HandleReviewing()
	if err != nil {
		t.Fatalf("HandleReviewing: %v", err)
	}

	if orch.StateMachine().Current() != CRHumanGateFixes {
		t.Errorf("expected CR_HUMAN_GATE_FIXES, got %s", orch.StateMachine().Current())
	}
	if orch.StateMachine().State().Verdict != CodeReviewVerdictPassWithObservations {
		t.Errorf("expected PASS_WITH_OBSERVATIONS, got %s", orch.StateMachine().State().Verdict)
	}
}

func TestHandleReviewing_CriticalFindings_TransitionsToFixing(t *testing.T) {
	runner := &mockReviewRunner{
		handler: func(prompt, outputPath string, _ int) (int, string, float64, int64, error) {
			lens := lensFromOutputPath(outputPath)
			os.WriteFile(outputPath, validCodeReviewOutput(lens, specworkflow.SeverityCritical), 0644)
			return 0, "", 0.05, 500, nil
		},
	}

	orch, _ := setupReviewOrchestrator(t, runner, nil)

	err := orch.HandleReviewing()
	if err != nil {
		t.Fatalf("HandleReviewing: %v", err)
	}

	if orch.StateMachine().Current() != CRFixing {
		t.Errorf("expected CR_FIXING, got %s", orch.StateMachine().Current())
	}
	if orch.StateMachine().State().Verdict != CodeReviewVerdictRevise {
		t.Errorf("expected REVISE, got %s", orch.StateMachine().State().Verdict)
	}
}

func TestHandleReviewing_MajorFindings_TransitionsToFixing(t *testing.T) {
	runner := &mockReviewRunner{
		handler: func(prompt, outputPath string, _ int) (int, string, float64, int64, error) {
			lens := lensFromOutputPath(outputPath)
			os.WriteFile(outputPath, validCodeReviewOutput(lens, specworkflow.SeverityMajor), 0644)
			return 0, "", 0.02, 200, nil
		},
	}

	orch, _ := setupReviewOrchestrator(t, runner, nil)

	err := orch.HandleReviewing()
	if err != nil {
		t.Fatalf("HandleReviewing: %v", err)
	}

	if orch.StateMachine().Current() != CRFixing {
		t.Errorf("expected CR_FIXING, got %s", orch.StateMachine().Current())
	}
}

func TestHandleReviewing_WritesFindingsFile(t *testing.T) {
	runner := &mockReviewRunner{
		handler: func(prompt, outputPath string, _ int) (int, string, float64, int64, error) {
			lens := lensFromOutputPath(outputPath)
			os.WriteFile(outputPath, validCodeReviewOutput(lens, specworkflow.SeverityMinor), 0644)
			return 0, "", 0.01, 100, nil
		},
	}

	orch, _ := setupReviewOrchestrator(t, runner, nil)

	err := orch.HandleReviewing()
	if err != nil {
		t.Fatalf("HandleReviewing: %v", err)
	}

	findingsPath := filepath.Join(orch.FeatureDir(), "code-findings-round-1.json")
	if _, err := os.Stat(findingsPath); os.IsNotExist(err) {
		t.Error("expected code-findings-round-1.json to be written")
	}
}

func TestHandleReviewing_AccumulatesCost(t *testing.T) {
	runner := &mockReviewRunner{
		handler: func(prompt, outputPath string, _ int) (int, string, float64, int64, error) {
			lens := lensFromOutputPath(outputPath)
			os.WriteFile(outputPath, validCodeReviewOutputNoFindings(lens), 0644)
			return 0, "", 0.10, 1000, nil
		},
	}

	orch, _ := setupReviewOrchestrator(t, runner, nil)

	err := orch.HandleReviewing()
	if err != nil {
		t.Fatalf("HandleReviewing: %v", err)
	}

	state := orch.StateMachine().State()
	// 6 agents * $0.10 = $0.60
	if state.CumulativeCostUSD < 0.5 {
		t.Errorf("expected cost >= $0.50, got $%.2f", state.CumulativeCostUSD)
	}
	if state.AgentInvocations < 6 {
		t.Errorf("expected >= 6 agent invocations, got %d", state.AgentInvocations)
	}
}

func TestHandleReviewing_AllFailures_Escalates(t *testing.T) {
	runner := &mockReviewRunner{
		handler: func(prompt, outputPath string, _ int) (int, string, float64, int64, error) {
			return 1, "agent crashed", 0.01, 100, nil
		},
	}

	orch, _ := setupReviewOrchestrator(t, runner, nil)

	err := orch.HandleReviewing()
	if err != nil {
		t.Fatalf("HandleReviewing: %v", err)
	}

	if orch.StateMachine().Current() != CREscalated {
		t.Errorf("expected CR_ESCALATED, got %s", orch.StateMachine().Current())
	}
}

func TestHandleReviewing_CostBreakerFires(t *testing.T) {
	runner := &mockReviewRunner{
		handler: func(prompt, outputPath string, _ int) (int, string, float64, int64, error) {
			lens := lensFromOutputPath(outputPath)
			os.WriteFile(outputPath, validCodeReviewOutputNoFindings(lens), 0644)
			return 0, "", 0.01, 100, nil
		},
	}

	orch, _ := setupReviewOrchestrator(t, runner, nil)
	// Set cost over budget.
	orch.StateMachine().State().CumulativeCostUSD = 100.0

	err := orch.HandleReviewing()
	if err != nil {
		t.Fatalf("HandleReviewing: %v", err)
	}

	if orch.StateMachine().Current() != CREscalated {
		t.Errorf("expected CR_ESCALATED, got %s", orch.StateMachine().Current())
	}
}

func TestHandleReviewing_WrongState_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	codePath := filepath.Join(dir, "repo")
	os.MkdirAll(codePath, 0755)

	cfg := CROrchestratorConfig{
		WorkspaceDir: dir,
		Config:       DefaultCodeReviewConfig(),
		GitProvider:  &mockGitProvider{isRepo: true, branch: "main", sha: "abc"},
	}
	orch := NewCodeReviewOrchestrator(cfg)
	orch.Start(StartCodeReviewRequest{CodePath: codePath, FeatureName: "test-wrong-state"})

	// Still at scope gate.
	err := orch.HandleReviewing()
	if err == nil {
		t.Fatal("expected error when not in CR_REVIEWING")
	}
}

func TestHandleReviewing_FindingsSummary(t *testing.T) {
	callCount := 0
	runner := &mockReviewRunner{
		handler: func(prompt, outputPath string, _ int) (int, string, float64, int64, error) {
			lens := lensFromOutputPath(outputPath)
			callCount++
			var severity specworkflow.Severity
			switch lens {
			case "security":
				severity = specworkflow.SeverityCritical
			case "correctness":
				severity = specworkflow.SeverityMajor
			default:
				severity = specworkflow.SeverityMinor
			}
			os.WriteFile(outputPath, validCodeReviewOutput(lens, severity), 0644)
			return 0, "", 0.01, 100, nil
		},
	}

	orch, _ := setupReviewOrchestrator(t, runner, nil)

	err := orch.HandleReviewing()
	if err != nil {
		t.Fatalf("HandleReviewing: %v", err)
	}

	summary := orch.StateMachine().State().FindingsSummary
	if summary.OpenCritical != 1 {
		t.Errorf("OpenCritical: got %d, want 1", summary.OpenCritical)
	}
	if summary.OpenMajor != 1 {
		t.Errorf("OpenMajor: got %d, want 1", summary.OpenMajor)
	}
	// 4 lenses with MINOR findings (testing, error-handling, observability, overcomplexity)
	if summary.OpenMinor != 4 {
		t.Errorf("OpenMinor: got %d, want 4", summary.OpenMinor)
	}
}

func TestHandleReviewing_ReducedCoverageWarning(t *testing.T) {
	failCount := 0
	runner := &mockReviewRunner{
		handler: func(prompt, outputPath string, _ int) (int, string, float64, int64, error) {
			lens := lensFromOutputPath(outputPath)
			if lens == "security" {
				failCount++
				return 1, "crash", 0.0, 0, nil
			}
			os.WriteFile(outputPath, validCodeReviewOutputNoFindings(lens), 0644)
			return 0, "", 0.01, 100, nil
		},
	}

	orch, _ := setupReviewOrchestrator(t, runner, nil)

	err := orch.HandleReviewing()
	if err != nil {
		t.Fatalf("HandleReviewing: %v", err)
	}

	warnings := orch.StateMachine().State().Warnings
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "reduced_coverage") {
			found = true
		}
	}
	if !found {
		t.Error("expected reduced_coverage warning")
	}
}

// ---------------------------------------------------------------------------
// Helper
// ---------------------------------------------------------------------------

// lensFromOutputPath extracts the lens name from an output path like
// "review-security-claude-round-1.json".
func lensFromOutputPath(path string) string {
	base := filepath.Base(path)
	// Format: review-{lens}-{provider}-round-{N}.json
	// Strip "review-" prefix.
	rest := base
	if len(rest) > 7 && rest[:7] == "review-" {
		rest = rest[7:]
	}
	// Find "-claude-" or "-codex-" and take everything before.
	for _, provider := range []string{"-claude-", "-codex-"} {
		for i := 0; i <= len(rest)-len(provider); i++ {
			if rest[i:i+len(provider)] == provider {
				return rest[:i]
			}
		}
	}
	return fmt.Sprintf("unknown-%s", base)
}
