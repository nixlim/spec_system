package codereview

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// LoadPersistedWorkflows tests
// ---------------------------------------------------------------------------

func TestCrashRecovery_LoadPersistedWorkflows_FindsAll(t *testing.T) {
	root := t.TempDir()

	// Create two feature directories with valid state files.
	for _, name := range []string{"feature-a", "feature-b"} {
		dir := filepath.Join(root, name)
		os.MkdirAll(dir, 0755)
		state := &CodeReviewStateJSON{
			State:       CRHumanGateFixes,
			FeatureName: name,
			Round:       2,
		}
		if err := SaveCRState(dir, state); err != nil {
			t.Fatalf("SaveCRState(%s): %v", name, err)
		}
	}

	workflows, errs := LoadPersistedWorkflows(root)
	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	if len(workflows) != 2 {
		t.Fatalf("expected 2 workflows, got %d", len(workflows))
	}

	// Verify both features were discovered.
	names := map[string]bool{}
	for _, w := range workflows {
		names[w.State.FeatureName] = true
	}
	if !names["feature-a"] || !names["feature-b"] {
		t.Errorf("expected feature-a and feature-b, got %v", names)
	}
}

func TestCrashRecovery_LoadPersistedWorkflows_SkipsNonDirs(t *testing.T) {
	root := t.TempDir()

	// Create a regular file at the root level (not a dir).
	os.WriteFile(filepath.Join(root, "random-file.txt"), []byte("hi"), 0644)

	// Create one valid workflow.
	dir := filepath.Join(root, "my-feature")
	os.MkdirAll(dir, 0755)
	SaveCRState(dir, &CodeReviewStateJSON{State: CRReviewing, FeatureName: "my-feature"})

	workflows, errs := LoadPersistedWorkflows(root)
	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	if len(workflows) != 1 {
		t.Fatalf("expected 1 workflow, got %d", len(workflows))
	}
}

func TestCrashRecovery_LoadPersistedWorkflows_SkipsDirWithoutState(t *testing.T) {
	root := t.TempDir()

	// Create a directory without a workflow-state.json.
	os.MkdirAll(filepath.Join(root, "empty-dir"), 0755)

	// Create one valid workflow.
	dir := filepath.Join(root, "valid-feature")
	os.MkdirAll(dir, 0755)
	SaveCRState(dir, &CodeReviewStateJSON{State: CRComplete, FeatureName: "valid-feature"})

	workflows, errs := LoadPersistedWorkflows(root)
	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	if len(workflows) != 1 {
		t.Fatalf("expected 1 workflow, got %d", len(workflows))
	}
}

func TestCrashRecovery_LoadPersistedWorkflows_ReportsCorrupt(t *testing.T) {
	root := t.TempDir()

	// Create a directory with corrupt state file.
	dir := filepath.Join(root, "corrupt-feature")
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "workflow-state.json"), []byte("{bad json"), 0644)

	workflows, errs := LoadPersistedWorkflows(root)
	if len(workflows) != 0 {
		t.Errorf("expected 0 workflows, got %d", len(workflows))
	}
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errs))
	}
	if !strings.Contains(errs[0].Error(), "corrupt-feature") {
		t.Errorf("error should mention feature name: %v", errs[0])
	}
}

func TestCrashRecovery_LoadPersistedWorkflows_NonexistentRoot(t *testing.T) {
	workflows, errs := LoadPersistedWorkflows("/tmp/nonexistent-root-12345")
	if len(workflows) != 0 {
		t.Errorf("expected 0 workflows, got %d", len(workflows))
	}
	if len(errs) != 0 {
		t.Errorf("expected no errors for nonexistent root, got %v", errs)
	}
}

func TestCrashRecovery_LoadPersistedWorkflows_IncludesTerminal(t *testing.T) {
	root := t.TempDir()

	// Terminal state workflow should still be discovered.
	dir := filepath.Join(root, "done-feature")
	os.MkdirAll(dir, 0755)
	SaveCRState(dir, &CodeReviewStateJSON{State: CRComplete, FeatureName: "done-feature"})

	workflows, errs := LoadPersistedWorkflows(root)
	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	if len(workflows) != 1 {
		t.Fatalf("expected 1 workflow, got %d", len(workflows))
	}
	if workflows[0].State.State != CRComplete {
		t.Errorf("expected CR_COMPLETE, got %s", workflows[0].State.State)
	}
}

// ---------------------------------------------------------------------------
// RecoverWorkflow tests
// ---------------------------------------------------------------------------

func TestCrashRecovery_RecoverWorkflow_GateState(t *testing.T) {
	dir := t.TempDir()
	SaveCRState(dir, &CodeReviewStateJSON{
		State:       CRHumanGateFixes,
		FeatureName: "gate-feature",
		Round:       2,
	})

	state, action, err := RecoverWorkflow(dir, false, nil)
	if err != nil {
		t.Fatalf("RecoverWorkflow: %v", err)
	}
	if state.FeatureName != "gate-feature" {
		t.Errorf("feature = %q, want gate-feature", state.FeatureName)
	}
	if action.Type != RecoveryResumeAtGate {
		t.Errorf("action = %s, want RecoveryResumeAtGate", action.Type)
	}
	if action.NextState != CRHumanGateFixes {
		t.Errorf("next state = %s, want CR_HUMAN_GATE_FIXES", action.NextState)
	}
}

func TestCrashRecovery_RecoverWorkflow_Terminal(t *testing.T) {
	dir := t.TempDir()
	SaveCRState(dir, &CodeReviewStateJSON{
		State:       CRComplete,
		FeatureName: "done-feature",
	})

	state, action, err := RecoverWorkflow(dir, false, nil)
	if err != nil {
		t.Fatalf("RecoverWorkflow: %v", err)
	}
	if state.State != CRComplete {
		t.Errorf("state = %s, want CR_COMPLETE", state.State)
	}
	if action != nil {
		t.Errorf("expected nil action for terminal state, got %+v", action)
	}
}

func TestCrashRecovery_RecoverWorkflow_MissingState(t *testing.T) {
	dir := t.TempDir()
	_, _, err := RecoverWorkflow(dir, false, nil)
	if err == nil {
		t.Fatal("expected error for missing state file")
	}
}

// ---------------------------------------------------------------------------
// Crash recovery integration: CR_REVIEWING with partial outputs
// ---------------------------------------------------------------------------

func TestCrashRecovery_ReviewingPartialOutputs(t *testing.T) {
	dir := t.TempDir()

	// Persist state in CR_REVIEWING at round 1.
	SaveCRState(dir, &CodeReviewStateJSON{
		State:       CRReviewing,
		FeatureName: "partial-review",
		Round:       1,
	})

	// Create 8 of 12 valid output files.
	providers := []string{"claude", "codex"}
	created := 0
	for _, provider := range providers {
		for _, lens := range CodeReviewLensGroups {
			if created >= 8 {
				break
			}
			name := reviewOutputFileName(provider, lens, 1)
			data, _ := json.Marshal(map[string]string{"status": "ok"})
			os.WriteFile(filepath.Join(dir, name), data, 0644)
			created++
		}
	}

	state, action, err := RecoverWorkflow(dir, true, nil)
	if err != nil {
		t.Fatalf("RecoverWorkflow: %v", err)
	}
	if state.State != CRReviewing {
		t.Errorf("state = %s, want CR_REVIEWING", state.State)
	}
	if action.Type != RecoveryReDispatchReviewers {
		t.Errorf("action = %s, want RecoveryReDispatchReviewers", action.Type)
	}
	if len(action.AgentsToReDispatch) != 4 {
		t.Errorf("expected 4 agents to re-dispatch, got %d: %v",
			len(action.AgentsToReDispatch), action.AgentsToReDispatch)
	}
}

// ---------------------------------------------------------------------------
// Crash recovery integration: CR_REVIEWING with corrupt output
// ---------------------------------------------------------------------------

func TestCrashRecovery_ReviewingCorruptOutput(t *testing.T) {
	dir := t.TempDir()

	SaveCRState(dir, &CodeReviewStateJSON{
		State:       CRReviewing,
		FeatureName: "corrupt-output",
		Round:       1,
	})

	// Create all 6 Claude outputs: 5 valid, 1 corrupt.
	for i, lens := range CodeReviewLensGroups {
		name := reviewOutputFileName("claude", lens, 1)
		var data []byte
		if i == 0 {
			data = []byte("{corrupt")
		} else {
			data, _ = json.Marshal(map[string]string{"ok": "true"})
		}
		os.WriteFile(filepath.Join(dir, name), data, 0644)
	}

	state, action, err := RecoverWorkflow(dir, false, nil)
	if err != nil {
		t.Fatalf("RecoverWorkflow: %v", err)
	}
	if state.State != CRReviewing {
		t.Errorf("state = %s, want CR_REVIEWING", state.State)
	}
	if len(action.AgentsToReDispatch) != 1 {
		t.Errorf("expected 1 agent to re-dispatch (corrupt), got %d: %v",
			len(action.AgentsToReDispatch), action.AgentsToReDispatch)
	}

	// Verify the corrupt file was deleted.
	corruptPath := filepath.Join(dir, reviewOutputFileName("claude", CodeReviewLensGroups[0], 1))
	if _, err := os.Stat(corruptPath); err == nil {
		t.Error("expected corrupt output file to be deleted")
	}
}

// ---------------------------------------------------------------------------
// Crash recovery integration: CR_REVIEWING ignores stale round files
// ---------------------------------------------------------------------------

func TestCrashRecovery_ReviewingIgnoresStaleRounds(t *testing.T) {
	dir := t.TempDir()

	// State is at round 2.
	SaveCRState(dir, &CodeReviewStateJSON{
		State:       CRReviewing,
		FeatureName: "stale-rounds",
		Round:       2,
	})

	// Create output files from round 1 only.
	for _, lens := range CodeReviewLensGroups {
		name := reviewOutputFileName("claude", lens, 1)
		data, _ := json.Marshal(map[string]string{"ok": "true"})
		os.WriteFile(filepath.Join(dir, name), data, 0644)
	}

	_, action, err := RecoverWorkflow(dir, false, nil)
	if err != nil {
		t.Fatalf("RecoverWorkflow: %v", err)
	}
	// All 6 agents need re-dispatch because no round-2 files exist.
	if len(action.AgentsToReDispatch) != 6 {
		t.Errorf("expected 6 agents to re-dispatch, got %d", len(action.AgentsToReDispatch))
	}
}

// ---------------------------------------------------------------------------
// Crash recovery integration: CR_FIXING with uncommitted changes
// ---------------------------------------------------------------------------

func TestCrashRecovery_FixingUncommittedChanges(t *testing.T) {
	dir := t.TempDir()

	SaveCRState(dir, &CodeReviewStateJSON{
		State:       CRFixing,
		FeatureName: "partial-fix",
		CodePath:    "/tmp/repo",
	})

	checker := &mockGitChecker{
		hasChanges: true,
		files:      []string{"internal/handler.go"},
	}

	state, action, err := RecoverWorkflow(dir, false, checker)
	if err != nil {
		t.Fatalf("RecoverWorkflow: %v", err)
	}
	if state.State != CRFixing {
		t.Errorf("state = %s, want CR_FIXING", state.State)
	}
	if action.Type != RecoveryRouteToGate {
		t.Errorf("action = %s, want RecoveryRouteToGate", action.Type)
	}
	if action.NextState != CRHumanGateFixes {
		t.Errorf("next state = %s, want CR_HUMAN_GATE_FIXES", action.NextState)
	}
	if !strings.Contains(action.Warning, "partial fix") {
		t.Errorf("expected 'partial fix' warning, got: %q", action.Warning)
	}
	if len(action.UncommittedFiles) != 1 {
		t.Errorf("expected 1 uncommitted file, got %d", len(action.UncommittedFiles))
	}
}

// ---------------------------------------------------------------------------
// Crash recovery integration: CR_FIXING no changes, re-dispatch
// ---------------------------------------------------------------------------

func TestCrashRecovery_FixingNoChangesReDispatches(t *testing.T) {
	dir := t.TempDir()

	SaveCRState(dir, &CodeReviewStateJSON{
		State:       CRFixing,
		FeatureName: "clean-fix",
		CodePath:    "/tmp/repo",
	})

	checker := &mockGitChecker{hasChanges: false}

	_, action, err := RecoverWorkflow(dir, false, checker)
	if err != nil {
		t.Fatalf("RecoverWorkflow: %v", err)
	}
	if action.Type != RecoveryReDispatchFixAgent {
		t.Errorf("action = %s, want RecoveryReDispatchFixAgent", action.Type)
	}
}

// ---------------------------------------------------------------------------
// Circuit breakers persist across restarts
// ---------------------------------------------------------------------------

func TestCrashRecovery_CircuitBreakersPersist(t *testing.T) {
	dir := t.TempDir()

	// Simulate a workflow that has accumulated cost and time.
	SaveCRState(dir, &CodeReviewStateJSON{
		State:                      CRHumanGateFixes,
		FeatureName:                "expensive-review",
		Round:                      3,
		CumulativeCostUSD:          45.0,
		CumulativeWallClockSeconds: 6000, // 100 minutes
	})

	state, action, err := RecoverWorkflow(dir, false, nil)
	if err != nil {
		t.Fatalf("RecoverWorkflow: %v", err)
	}

	// State should preserve circuit breaker values.
	if state.Round != 3 {
		t.Errorf("Round = %d, want 3", state.Round)
	}
	if state.CumulativeCostUSD != 45.0 {
		t.Errorf("CumulativeCostUSD = %f, want 45.0", state.CumulativeCostUSD)
	}
	if state.CumulativeWallClockSeconds != 6000 {
		t.Errorf("CumulativeWallClockSeconds = %f, want 6000", state.CumulativeWallClockSeconds)
	}

	// Gate state should resume at gate.
	if action.Type != RecoveryResumeAtGate {
		t.Errorf("action = %s, want RecoveryResumeAtGate", action.Type)
	}
}
