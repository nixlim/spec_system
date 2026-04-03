package codereview

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Mock GitStatusChecker
// ---------------------------------------------------------------------------

type mockGitChecker struct {
	hasChanges bool
	files      []string
	err        error
}

func (m *mockGitChecker) HasUncommittedChanges(codePath string) (bool, []string, error) {
	return m.hasChanges, m.files, m.err
}

// ---------------------------------------------------------------------------
// SaveState / LoadState
// ---------------------------------------------------------------------------

func TestPersistence_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()

	state := &CodeReviewStateJSON{
		State:                      CRReviewing,
		Round:                      2,
		FeatureName:                "my-feature",
		CodePath:                   "/tmp/repo",
		SpecPath:                   "/tmp/spec.md",
		GrillCodeMode:              GrillCodeModeFullContext,
		GitBranch:                  "main",
		GitHeadSHA:                 "abc123",
		CommitMode:                 "branch_per_round",
		StartedAt:                  "2026-03-28T12:00:00Z",
		UpdatedAt:                  "2026-03-28T12:30:00Z",
		CumulativeCostUSD:          35.50,
		CumulativeWallClockSeconds: 1800,
		AgentInvocations:           12,
		Warnings:                   []string{"reduced_coverage"},
	}

	if err := SaveCRState(dir, state); err != nil {
		t.Fatalf("SaveCRState: %v", err)
	}

	loaded, err := LoadCRState(dir)
	if err != nil {
		t.Fatalf("LoadCRState: %v", err)
	}

	if loaded.State != CRReviewing {
		t.Errorf("State: got %s, want CR_REVIEWING", loaded.State)
	}
	if loaded.Round != 2 {
		t.Errorf("Round: got %d, want 2", loaded.Round)
	}
	if loaded.FeatureName != "my-feature" {
		t.Errorf("FeatureName: got %q, want %q", loaded.FeatureName, "my-feature")
	}
	if loaded.CodePath != "/tmp/repo" {
		t.Errorf("CodePath: got %q, want %q", loaded.CodePath, "/tmp/repo")
	}
	if loaded.SpecPath != "/tmp/spec.md" {
		t.Errorf("SpecPath: got %q, want %q", loaded.SpecPath, "/tmp/spec.md")
	}
	if loaded.GrillCodeMode != GrillCodeModeFullContext {
		t.Errorf("GrillCodeMode: got %s, want full-context", loaded.GrillCodeMode)
	}
	if loaded.GitBranch != "main" {
		t.Errorf("GitBranch: got %q, want %q", loaded.GitBranch, "main")
	}
	if loaded.GitHeadSHA != "abc123" {
		t.Errorf("GitHeadSHA: got %q, want %q", loaded.GitHeadSHA, "abc123")
	}
	if loaded.CumulativeCostUSD != 35.50 {
		t.Errorf("CumulativeCostUSD: got %f, want 35.50", loaded.CumulativeCostUSD)
	}
	if loaded.CumulativeWallClockSeconds != 1800 {
		t.Errorf("CumulativeWallClockSeconds: got %f, want 1800", loaded.CumulativeWallClockSeconds)
	}
	if len(loaded.Warnings) != 1 || loaded.Warnings[0] != "reduced_coverage" {
		t.Errorf("Warnings: got %v, want [reduced_coverage]", loaded.Warnings)
	}
}

func TestPersistence_CircuitBreakerSurvivesSaveLoad(t *testing.T) {
	dir := t.TempDir()

	state := &CodeReviewStateJSON{
		State:                      CRHumanGateFixes,
		Round:                      3,
		CumulativeCostUSD:          45.0,
		CumulativeWallClockSeconds: 5400,
	}

	if err := SaveCRState(dir, state); err != nil {
		t.Fatalf("SaveCRState: %v", err)
	}

	loaded, err := LoadCRState(dir)
	if err != nil {
		t.Fatalf("LoadCRState: %v", err)
	}

	if loaded.Round != 3 {
		t.Errorf("Round: got %d, want 3", loaded.Round)
	}
	if loaded.CumulativeCostUSD != 45.0 {
		t.Errorf("CumulativeCostUSD: got %f, want 45.0", loaded.CumulativeCostUSD)
	}
	if loaded.CumulativeWallClockSeconds != 5400 {
		t.Errorf("CumulativeWallClockSeconds: got %f, want 5400", loaded.CumulativeWallClockSeconds)
	}
}

func TestPersistence_AtomicWrite(t *testing.T) {
	dir := t.TempDir()

	state := &CodeReviewStateJSON{
		State:       CRInit,
		FeatureName: "test",
	}

	if err := SaveCRState(dir, state); err != nil {
		t.Fatalf("SaveCRState: %v", err)
	}

	// Verify no temp files remain.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".cr-workflow-state-") && strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temp file %s should not remain after successful write", e.Name())
		}
	}

	// Verify the target file exists.
	target := CRStateFilePath(dir)
	if _, err := os.Stat(target); err != nil {
		t.Errorf("expected target file %s to exist: %v", target, err)
	}
}

func TestPersistence_LoadNotFound(t *testing.T) {
	dir := t.TempDir()

	_, err := LoadCRState(dir)
	if err == nil {
		t.Fatal("expected error for missing state file")
	}
	var notFound *ErrCRStateNotFound
	if !isErrCRStateNotFound(err) {
		t.Errorf("expected ErrCRStateNotFound, got: %T: %v", err, err)
	}
	_ = notFound
}

func isErrCRStateNotFound(err error) bool {
	var e *ErrCRStateNotFound
	return errors.Is(err, e) || func() bool { var x *ErrCRStateNotFound; return as(err, &x) }()
}

func as(err error, target interface{}) bool {
	switch t := target.(type) {
	case **ErrCRStateNotFound:
		for err != nil {
			if x, ok := err.(*ErrCRStateNotFound); ok {
				*t = x
				return true
			}
			err = errors.Unwrap(err)
		}
	case **ErrCRStateParseFailed:
		for err != nil {
			if x, ok := err.(*ErrCRStateParseFailed); ok {
				*t = x
				return true
			}
			err = errors.Unwrap(err)
		}
	}
	return false
}

func TestPersistence_LoadCorruptJSON(t *testing.T) {
	dir := t.TempDir()
	p := CRStateFilePath(dir)
	os.WriteFile(p, []byte("{invalid json"), 0644)

	_, err := LoadCRState(dir)
	if err == nil {
		t.Fatal("expected error for corrupt JSON")
	}
	var parseFailed *ErrCRStateParseFailed
	if !func() bool { return as(err, &parseFailed) }() {
		t.Errorf("expected ErrCRStateParseFailed, got: %T: %v", err, err)
	}
}

// ---------------------------------------------------------------------------
// RecoverFromCrash — CR_REVIEWING
// ---------------------------------------------------------------------------

func TestPersistence_RecoverReviewing_AllPresent(t *testing.T) {
	dir := t.TempDir()

	// Create all 12 valid output files (6 lenses × 2 providers) for round 2.
	for _, provider := range []string{"claude", "codex"} {
		for _, lens := range CodeReviewLensGroups {
			name := reviewOutputFileName(provider, lens, 2)
			data, _ := json.Marshal(map[string]string{"status": "ok"})
			os.WriteFile(filepath.Join(dir, name), data, 0644)
		}
	}

	state := &CodeReviewStateJSON{State: CRReviewing, Round: 2}
	action, err := RecoverFromCrash(dir, state, true, nil)
	if err != nil {
		t.Fatalf("RecoverFromCrash: %v", err)
	}
	if action.Type != RecoveryReDispatchReviewers {
		t.Errorf("expected RecoveryReDispatchReviewers, got %s", action.Type)
	}
	if len(action.AgentsToReDispatch) != 0 {
		t.Errorf("expected 0 agents to re-dispatch, got %d", len(action.AgentsToReDispatch))
	}
}

func TestPersistence_RecoverReviewing_SomeMissing(t *testing.T) {
	dir := t.TempDir()

	// Create 8 of 12 output files (missing 4).
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

	state := &CodeReviewStateJSON{State: CRReviewing, Round: 1}
	action, err := RecoverFromCrash(dir, state, true, nil)
	if err != nil {
		t.Fatalf("RecoverFromCrash: %v", err)
	}
	if action.Type != RecoveryReDispatchReviewers {
		t.Errorf("expected RecoveryReDispatchReviewers, got %s", action.Type)
	}
	if len(action.AgentsToReDispatch) != 4 {
		t.Errorf("expected 4 agents to re-dispatch, got %d: %v",
			len(action.AgentsToReDispatch), action.AgentsToReDispatch)
	}
}

func TestPersistence_RecoverReviewing_CorruptFile(t *testing.T) {
	dir := t.TempDir()

	// Create 11 valid files and 1 corrupt file.
	count := 0
	for _, provider := range []string{"claude", "codex"} {
		for _, lens := range CodeReviewLensGroups {
			name := reviewOutputFileName(provider, lens, 1)
			var data []byte
			if count == 0 {
				data = []byte("{corrupt json")
			} else {
				data, _ = json.Marshal(map[string]string{"status": "ok"})
			}
			os.WriteFile(filepath.Join(dir, name), data, 0644)
			count++
		}
	}

	state := &CodeReviewStateJSON{State: CRReviewing, Round: 1}
	action, err := RecoverFromCrash(dir, state, true, nil)
	if err != nil {
		t.Fatalf("RecoverFromCrash: %v", err)
	}
	if len(action.AgentsToReDispatch) != 1 {
		t.Errorf("expected 1 agent to re-dispatch (corrupt file), got %d: %v",
			len(action.AgentsToReDispatch), action.AgentsToReDispatch)
	}

	// Verify the corrupt file was deleted.
	corruptPath := filepath.Join(dir, reviewOutputFileName("claude", CodeReviewLensGroups[0], 1))
	if _, err := os.Stat(corruptPath); err == nil {
		t.Error("expected corrupt file to be deleted")
	}
}

func TestPersistence_RecoverReviewing_IgnoresPreviousRounds(t *testing.T) {
	dir := t.TempDir()

	// Create output files from round 1 (previous round).
	for _, provider := range []string{"claude", "codex"} {
		for _, lens := range CodeReviewLensGroups {
			name := reviewOutputFileName(provider, lens, 1)
			data, _ := json.Marshal(map[string]string{"status": "ok"})
			os.WriteFile(filepath.Join(dir, name), data, 0644)
		}
	}

	// State is at round 2 — files from round 1 should be ignored.
	state := &CodeReviewStateJSON{State: CRReviewing, Round: 2}
	action, err := RecoverFromCrash(dir, state, true, nil)
	if err != nil {
		t.Fatalf("RecoverFromCrash: %v", err)
	}
	// All 12 agents need re-dispatch because no round-2 files exist.
	if len(action.AgentsToReDispatch) != 12 {
		t.Errorf("expected 12 agents to re-dispatch, got %d", len(action.AgentsToReDispatch))
	}
}

// ---------------------------------------------------------------------------
// RecoverFromCrash — CR_FIXING
// ---------------------------------------------------------------------------

func TestPersistence_RecoverFixing_UncommittedChanges(t *testing.T) {
	dir := t.TempDir()

	checker := &mockGitChecker{
		hasChanges: true,
		files:      []string{"internal/api/handler.go", "internal/api/router.go"},
	}

	state := &CodeReviewStateJSON{State: CRFixing, CodePath: "/tmp/repo"}
	action, err := RecoverFromCrash(dir, state, false, checker)
	if err != nil {
		t.Fatalf("RecoverFromCrash: %v", err)
	}
	if action.Type != RecoveryRouteToGate {
		t.Errorf("expected RecoveryRouteToGate, got %s", action.Type)
	}
	if action.NextState != CRHumanGateFixes {
		t.Errorf("expected CR_HUMAN_GATE_FIXES, got %s", action.NextState)
	}
	if !strings.Contains(action.Warning, "partial fix") {
		t.Errorf("expected warning containing 'partial fix', got: %q", action.Warning)
	}
	if len(action.UncommittedFiles) != 2 {
		t.Errorf("expected 2 uncommitted files, got %d", len(action.UncommittedFiles))
	}
}

func TestPersistence_RecoverFixing_NoChangesNoOutput(t *testing.T) {
	dir := t.TempDir()

	checker := &mockGitChecker{hasChanges: false}

	state := &CodeReviewStateJSON{State: CRFixing, CodePath: "/tmp/repo"}
	action, err := RecoverFromCrash(dir, state, false, checker)
	if err != nil {
		t.Fatalf("RecoverFromCrash: %v", err)
	}
	if action.Type != RecoveryReDispatchFixAgent {
		t.Errorf("expected RecoveryReDispatchFixAgent, got %s", action.Type)
	}
}

func TestPersistence_RecoverFixing_NoChangesWithOutput(t *testing.T) {
	dir := t.TempDir()

	// Create a fix-output-round-1.json file (matches round-specific naming).
	data, _ := json.Marshal(map[string]int{"round": 1})
	os.WriteFile(filepath.Join(dir, "fix-output-round-1.json"), data, 0644)

	checker := &mockGitChecker{hasChanges: false}

	state := &CodeReviewStateJSON{State: CRFixing, CodePath: "/tmp/repo", Round: 1}
	action, err := RecoverFromCrash(dir, state, false, checker)
	if err != nil {
		t.Fatalf("RecoverFromCrash: %v", err)
	}
	if action.Type != RecoveryRouteToGate {
		t.Errorf("expected RecoveryRouteToGate, got %s", action.Type)
	}
}

// ---------------------------------------------------------------------------
// RecoverFromCrash — Gate states
// ---------------------------------------------------------------------------

func TestPersistence_RecoverGateStates(t *testing.T) {
	for _, gateState := range []CodeReviewState{CRHumanGateScope, CRHumanGateFixes} {
		t.Run(gateState.String(), func(t *testing.T) {
			dir := t.TempDir()
			state := &CodeReviewStateJSON{State: gateState}
			action, err := RecoverFromCrash(dir, state, false, nil)
			if err != nil {
				t.Fatalf("RecoverFromCrash: %v", err)
			}
			if action.Type != RecoveryResumeAtGate {
				t.Errorf("expected RecoveryResumeAtGate, got %s", action.Type)
			}
			if action.NextState != gateState {
				t.Errorf("expected NextState %s, got %s", gateState, action.NextState)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// RecoverFromCrash — Terminal states
// ---------------------------------------------------------------------------

func TestPersistence_RecoverTerminalStates(t *testing.T) {
	for _, termState := range []CodeReviewState{CRComplete, CREscalated} {
		t.Run(termState.String(), func(t *testing.T) {
			dir := t.TempDir()
			state := &CodeReviewStateJSON{State: termState}
			action, err := RecoverFromCrash(dir, state, false, nil)
			if err != nil {
				t.Fatalf("RecoverFromCrash: %v", err)
			}
			if action != nil {
				t.Errorf("expected nil action for terminal state, got %+v", action)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Human comments
// ---------------------------------------------------------------------------

func TestPersistence_AppendComment(t *testing.T) {
	dir := t.TempDir()

	entry := CRCommentEntry{
		Gate:      "CR_HUMAN_GATE_FIXES",
		Action:    "accept",
		Comment:   "Looks good",
		Timestamp: "2026-03-28T12:00:00Z",
	}

	if err := AppendCRComment(dir, entry); err != nil {
		t.Fatalf("AppendCRComment: %v", err)
	}

	// Read back.
	data, err := os.ReadFile(CRCommentsFilePath(dir))
	if err != nil {
		t.Fatalf("read comments: %v", err)
	}

	var comments []CRCommentEntry
	if err := json.Unmarshal(data, &comments); err != nil {
		t.Fatalf("unmarshal comments: %v", err)
	}
	if len(comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(comments))
	}
	if comments[0].Comment != "Looks good" {
		t.Errorf("Comment: got %q, want %q", comments[0].Comment, "Looks good")
	}
}

func TestPersistence_AppendCommentEmpty(t *testing.T) {
	dir := t.TempDir()

	entry := CRCommentEntry{Comment: ""}
	if err := AppendCRComment(dir, entry); err != nil {
		t.Fatalf("AppendCRComment: %v", err)
	}

	// File should not be created for empty comments.
	if _, err := os.Stat(CRCommentsFilePath(dir)); err == nil {
		t.Error("expected no file for empty comment")
	}
}
