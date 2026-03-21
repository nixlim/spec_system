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

// TestRewindPreservesAllFiles verifies that rewind never deletes artefact
// files, regardless of the target state.
func TestRewindPreservesAllFiles(t *testing.T) {
	targets := []WorkflowState{
		StateDiscovery,
		StateDrafting,
		StateReviewing,
		StateRevising,
		StateJudging,
	}

	for _, target := range targets {
		t.Run(target.String(), func(t *testing.T) {
			dir := t.TempDir()
			specDir := filepath.Join(dir, "specs", "test")
			os.MkdirAll(specDir, 0o755)
			createTestArtefacts(t, specDir)

			before := remainingFiles(t, specDir)

			state := &WorkflowStateJSON{
				State:       StateFinalized,
				Round:       1,
				FeatureName: "test",
			}

			result, err := RewindWorkflow(specDir, state, target, 1)
			if err != nil {
				t.Fatal(err)
			}

			after := remainingFiles(t, specDir)

			// No files should be removed.
			if len(result.FilesRemoved) != 0 {
				t.Errorf("expected 0 files removed, got %d: %v", len(result.FilesRemoved), result.FilesRemoved)
			}

			// All original files should still exist.
			if len(after) != len(before) {
				t.Errorf("file count changed: before=%d after=%d", len(before), len(after))
				t.Errorf("before: %v", before)
				t.Errorf("after:  %v", after)
			}

			for _, f := range before {
				if !fileExists(after, f) {
					t.Errorf("file %s was deleted", f)
				}
			}
		})
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

func TestRewindSetsSpecVersion(t *testing.T) {
	tests := []struct {
		target      WorkflowState
		round       int
		wantVersion int
	}{
		{StateDiscovery, 1, 0},
		{StateDrafting, 1, 0},
		{StateReviewing, 1, 0},
		{StateReviewing, 2, 1},
		{StateRevising, 1, 0},
		{StateRevising, 2, 1},
		{StateJudging, 1, 1},
		{StateJudging, 2, 2},
	}
	for _, tt := range tests {
		t.Run(tt.target.String(), func(t *testing.T) {
			dir := t.TempDir()
			specDir := filepath.Join(dir, "specs", "test")
			os.MkdirAll(specDir, 0o755)
			createTestArtefacts(t, specDir)

			state := &WorkflowStateJSON{
				State:              StateFinalized,
				Round:              1,
				FeatureName:        "test",
				CurrentSpecVersion: 99,
			}

			_, err := RewindWorkflow(specDir, state, tt.target, tt.round)
			if err != nil {
				t.Fatal(err)
			}

			if state.CurrentSpecVersion != tt.wantVersion {
				t.Errorf("CurrentSpecVersion = %d, want %d", state.CurrentSpecVersion, tt.wantVersion)
			}
		})
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

func TestRewindResetsFindings(t *testing.T) {
	dir := t.TempDir()
	specDir := filepath.Join(dir, "specs", "test")
	os.MkdirAll(specDir, 0o755)
	createTestArtefacts(t, specDir)

	state := &WorkflowStateJSON{
		State:       StateFinalized,
		Round:       1,
		FeatureName: "test",
		FindingsSummary: FindingsSummary{
			Raised:       78,
			Closed:       74,
			OpenCritical: 0,
			OpenMajor:    0,
		},
		HadCriticalFindings: true,
	}

	_, err := RewindWorkflow(specDir, state, StateReviewing, 1)
	if err != nil {
		t.Fatal(err)
	}

	if state.FindingsSummary.Raised != 0 {
		t.Errorf("FindingsSummary.Raised should be reset to 0, got %d", state.FindingsSummary.Raised)
	}
}
