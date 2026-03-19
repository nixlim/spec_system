package specworkflow

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestIsRewindable(t *testing.T) {
	tests := []struct {
		state WorkflowState
		want  bool
	}{
		{StateInit, false},
		{StateDiscovery, true},
		{StateHumanGate1, false},
		{StateDrafting, true},
		{StateHumanGate2, false},
		{StateReviewing, true},
		{StateRevising, true},
		{StateJudging, true},
		{StateHumanGateFinal, false},
		{StateFinalized, false},
		{StateEscalated, false},
		{StateError, false},
	}
	for _, tt := range tests {
		t.Run(tt.state.String(), func(t *testing.T) {
			if got := IsRewindable(tt.state); got != tt.want {
				t.Errorf("IsRewindable(%s) = %v, want %v", tt.state, got, tt.want)
			}
		})
	}
}

// createTestArtefacts populates a spec directory with a full set of workflow
// artefacts simulating a completed round-1 workflow.
func createTestArtefacts(t *testing.T, specDir string) {
	t.Helper()
	files := []string{
		"workflow-state.json",
		"workflow-log.jsonl",
		"human-comments.json",
		"discovery-output.json",
		"gate1-corrections.json",
		"drafter-output.json",
		"gate2-resolutions.json",
		"b5-holdouts.md",
		"spec-v0.md",
		"spec-v1.md",
		"review-a-round-1.json",
		"review-a-round-1.md",
		"review-b-round-1.json",
		"review-b-round-1.md",
		"review-c-round-1.json",
		"review-c-round-1.md",
		"review-d-round-1.json",
		"review-d-round-1.md",
		"merged-findings-round-1.json",
		"revision-round-1.json",
		"judge-round-1.json",
		"debate-trail.md",
		"escalation-summary.md",
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(specDir, f), []byte("{}"), 0o644); err != nil {
			t.Fatalf("create %s: %v", f, err)
		}
	}
}

func remainingFiles(t *testing.T, specDir string) []string {
	t.Helper()
	entries, err := os.ReadDir(specDir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

func fileExists(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

func TestRewindToDiscovery(t *testing.T) {
	dir := t.TempDir()
	specDir := filepath.Join(dir, "specs", "test")
	os.MkdirAll(specDir, 0o755)
	createTestArtefacts(t, specDir)

	state := &WorkflowStateJSON{
		State:       StateJudging,
		Round:       1,
		FeatureName: "test",
	}

	result, err := RewindWorkflow(specDir, state, StateDiscovery, 1)
	if err != nil {
		t.Fatal(err)
	}

	remaining := remainingFiles(t, specDir)

	// Should only keep workflow-state.json, workflow-log.jsonl, human-comments.json
	if len(remaining) != 3 {
		t.Errorf("expected 3 remaining files, got %d: %v", len(remaining), remaining)
	}
	if !fileExists(remaining, "workflow-state.json") {
		t.Error("workflow-state.json should be kept")
	}
	if result.TargetState != StateDiscovery {
		t.Errorf("expected target DISCOVERY, got %s", result.TargetState)
	}
}

func TestRewindToDrafting(t *testing.T) {
	dir := t.TempDir()
	specDir := filepath.Join(dir, "specs", "test")
	os.MkdirAll(specDir, 0o755)
	createTestArtefacts(t, specDir)

	state := &WorkflowStateJSON{
		State:       StateJudging,
		Round:       1,
		FeatureName: "test",
	}

	_, err := RewindWorkflow(specDir, state, StateDrafting, 1)
	if err != nil {
		t.Fatal(err)
	}

	remaining := remainingFiles(t, specDir)
	if !fileExists(remaining, "discovery-output.json") {
		t.Error("discovery-output.json should be kept")
	}
	if !fileExists(remaining, "gate1-corrections.json") {
		t.Error("gate1-corrections.json should be kept")
	}
	if fileExists(remaining, "drafter-output.json") {
		t.Error("drafter-output.json should be removed")
	}
	if fileExists(remaining, "spec-v0.md") {
		t.Error("spec-v0.md should be removed")
	}
}

func TestRewindToReviewingRound1(t *testing.T) {
	dir := t.TempDir()
	specDir := filepath.Join(dir, "specs", "test")
	os.MkdirAll(specDir, 0o755)
	createTestArtefacts(t, specDir)

	state := &WorkflowStateJSON{
		State:       StateJudging,
		Round:       1,
		FeatureName: "test",
	}

	_, err := RewindWorkflow(specDir, state, StateReviewing, 1)
	if err != nil {
		t.Fatal(err)
	}

	remaining := remainingFiles(t, specDir)
	// Should keep discovery, gates, drafter, spec-v0, holdouts
	if !fileExists(remaining, "discovery-output.json") {
		t.Error("discovery-output.json should be kept")
	}
	if !fileExists(remaining, "drafter-output.json") {
		t.Error("drafter-output.json should be kept")
	}
	if !fileExists(remaining, "spec-v0.md") {
		t.Error("spec-v0.md should be kept")
	}
	if !fileExists(remaining, "b5-holdouts.md") {
		t.Error("holdouts should be kept")
	}
	// Should remove round-1 reviews, merged, revision, judge
	if fileExists(remaining, "review-a-round-1.json") {
		t.Error("review-a-round-1.json should be removed")
	}
	if fileExists(remaining, "merged-findings-round-1.json") {
		t.Error("merged-findings-round-1.json should be removed")
	}
	if fileExists(remaining, "revision-round-1.json") {
		t.Error("revision-round-1.json should be removed")
	}
	if fileExists(remaining, "judge-round-1.json") {
		t.Error("judge-round-1.json should be removed")
	}
	if fileExists(remaining, "spec-v1.md") {
		t.Error("spec-v1.md should be removed")
	}
}

func TestRewindToRevisingRound1(t *testing.T) {
	dir := t.TempDir()
	specDir := filepath.Join(dir, "specs", "test")
	os.MkdirAll(specDir, 0o755)
	createTestArtefacts(t, specDir)

	state := &WorkflowStateJSON{
		State:       StateJudging,
		Round:       1,
		FeatureName: "test",
	}

	_, err := RewindWorkflow(specDir, state, StateRevising, 1)
	if err != nil {
		t.Fatal(err)
	}

	remaining := remainingFiles(t, specDir)
	// Should keep reviews and merged for round 1
	if !fileExists(remaining, "review-a-round-1.json") {
		t.Error("review-a-round-1.json should be kept")
	}
	if !fileExists(remaining, "merged-findings-round-1.json") {
		t.Error("merged-findings-round-1.json should be kept")
	}
	// Should remove revision and judge for round 1
	if fileExists(remaining, "revision-round-1.json") {
		t.Error("revision-round-1.json should be removed")
	}
	if fileExists(remaining, "judge-round-1.json") {
		t.Error("judge-round-1.json should be removed")
	}
	if fileExists(remaining, "spec-v1.md") {
		t.Error("spec-v1.md should be removed (revised spec)")
	}
}

func TestRewindToJudgingRound1(t *testing.T) {
	dir := t.TempDir()
	specDir := filepath.Join(dir, "specs", "test")
	os.MkdirAll(specDir, 0o755)
	createTestArtefacts(t, specDir)

	state := &WorkflowStateJSON{
		State:       StateFinalized,
		Round:       1,
		FeatureName: "test",
	}

	_, err := RewindWorkflow(specDir, state, StateJudging, 1)
	if err != nil {
		t.Fatal(err)
	}

	remaining := remainingFiles(t, specDir)
	// Should keep revision and spec-v1
	if !fileExists(remaining, "revision-round-1.json") {
		t.Error("revision-round-1.json should be kept")
	}
	if !fileExists(remaining, "spec-v1.md") {
		t.Error("spec-v1.md should be kept")
	}
	// Should remove judge
	if fileExists(remaining, "judge-round-1.json") {
		t.Error("judge-round-1.json should be removed")
	}
}

func TestRewindToInvalidState(t *testing.T) {
	dir := t.TempDir()
	specDir := filepath.Join(dir, "specs", "test")
	os.MkdirAll(specDir, 0o755)

	state := &WorkflowStateJSON{
		State:       StateFinalized,
		Round:       1,
		FeatureName: "test",
	}

	_, err := RewindWorkflow(specDir, state, StateHumanGate1, 1)
	if err == nil {
		t.Error("expected error for non-rewindable state")
	}
}

func TestRewindUpdatesState(t *testing.T) {
	dir := t.TempDir()
	specDir := filepath.Join(dir, "specs", "test")
	os.MkdirAll(specDir, 0o755)
	createTestArtefacts(t, specDir)

	state := &WorkflowStateJSON{
		State:       StateFinalized,
		Round:       1,
		FeatureName: "test",
	}

	_, err := RewindWorkflow(specDir, state, StateReviewing, 1)
	if err != nil {
		t.Fatal(err)
	}

	if state.State != StateReviewing {
		t.Errorf("state should be REVIEWING, got %s", state.State)
	}
	if state.Round != 1 {
		t.Errorf("round should be 1, got %d", state.Round)
	}

	// Verify it was persisted.
	loaded, err := LoadState(specDir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != StateReviewing {
		t.Errorf("persisted state should be REVIEWING, got %s", loaded.State)
	}
}
