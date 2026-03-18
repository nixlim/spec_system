package specworkflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// saveTestState persists a WorkflowStateJSON into the spec directory for the
// given feature within workspaceDir. It creates the directory tree if needed.
func saveTestState(t *testing.T, workspaceDir, featureName string, state *WorkflowStateJSON) {
	t.Helper()
	specDir := filepath.Join(workspaceDir, "specs", featureName)
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatalf("mkdir spec dir: %v", err)
	}
	if err := SaveState(specDir, state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
}

// touchFile creates an empty file at the given path, creating parent
// directories as needed.
func touchFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for touch: %v", err)
	}
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatalf("touch file: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestResumeWorkflow_NoWorkflowExists(t *testing.T) {
	dir := t.TempDir()
	featureName := "no-such-feature"

	result, err := ResumeWorkflow(dir, featureName, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Found {
		t.Error("Found should be false when workflow-state.json is missing")
	}
	if result.State != nil {
		t.Error("State should be nil when not found")
	}
	if result.IsGateState {
		t.Error("IsGateState should be false when not found")
	}
	if result.NeedsAgentRedispatch {
		t.Error("NeedsAgentRedispatch should be false when not found")
	}
	if result.SkillsChanged {
		t.Error("SkillsChanged should be false when not found")
	}
}

func TestResumeWorkflow_GateStateDetection(t *testing.T) {
	tests := []struct {
		name  string
		state WorkflowState
	}{
		{"HUMAN_GATE_1", StateHumanGate1},
		{"HUMAN_GATE_2", StateHumanGate2},
		{"HUMAN_GATE_FINAL", StateHumanGateFinal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			featureName := "gate-test"

			saveTestState(t, dir, featureName, &WorkflowStateJSON{
				State:       tt.state,
				Round:       1,
				FeatureName: featureName,
			})

			result, err := ResumeWorkflow(dir, featureName, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if !result.Found {
				t.Fatal("Found should be true")
			}
			if !result.IsGateState {
				t.Errorf("IsGateState should be true for %s", tt.name)
			}
			if result.NeedsAgentRedispatch {
				t.Error("NeedsAgentRedispatch should be false for gate states")
			}
		})
	}
}

func TestResumeWorkflow_AgentStateOutputPresent(t *testing.T) {
	tests := []struct {
		name        string
		state       WorkflowState
		round       int
		outputFiles []string // relative to workspace
	}{
		{
			"DISCOVERY with output",
			StateDiscovery, 1,
			[]string{"specs/my-feature/discovery-output.json"},
		},
		{
			"DRAFTING with output",
			StateDrafting, 1,
			[]string{"specs/my-feature/drafter-output.json"},
		},
		{
			"REVIEWING with one review file",
			StateReviewing, 2,
			[]string{"specs/my-feature/review-c-round-2.json"},
		},
		{
			"REVISING with output",
			StateRevising, 3,
			[]string{"specs/my-feature/revision-round-3.json"},
		},
		{
			"JUDGING with output",
			StateJudging, 2,
			[]string{"specs/my-feature/judge-round-2.json"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			featureName := "my-feature"

			saveTestState(t, dir, featureName, &WorkflowStateJSON{
				State:       tt.state,
				Round:       tt.round,
				FeatureName: featureName,
			})

			for _, rel := range tt.outputFiles {
				touchFile(t, filepath.Join(dir, rel))
			}

			result, err := ResumeWorkflow(dir, featureName, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if !result.Found {
				t.Fatal("Found should be true")
			}
			if result.NeedsAgentRedispatch {
				t.Errorf("NeedsAgentRedispatch should be false when output is present (state: %s)", tt.state)
			}
			if result.MissingOutputFile != "" {
				t.Errorf("MissingOutputFile should be empty, got %q", result.MissingOutputFile)
			}
			if result.IsGateState {
				t.Error("IsGateState should be false for agent states")
			}
		})
	}
}

func TestResumeWorkflow_AgentStateOutputMissing(t *testing.T) {
	tests := []struct {
		name            string
		state           WorkflowState
		round           int
		wantMissingFile string
	}{
		{
			"DISCOVERY missing output",
			StateDiscovery, 1,
			filepath.Join("specs", "absent-feature", "discovery-output.json"),
		},
		{
			"DRAFTING missing output",
			StateDrafting, 1,
			filepath.Join("specs", "absent-feature", "drafter-output.json"),
		},
		{
			"REVIEWING missing all review files",
			StateReviewing, 1,
			filepath.Join("specs", "absent-feature", "review-a-round-1.json"),
		},
		{
			"REVISING missing output",
			StateRevising, 2,
			filepath.Join("specs", "absent-feature", "revision-round-2.json"),
		},
		{
			"JUDGING missing output",
			StateJudging, 3,
			filepath.Join("specs", "absent-feature", "judge-round-3.json"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			featureName := "absent-feature"

			saveTestState(t, dir, featureName, &WorkflowStateJSON{
				State:       tt.state,
				Round:       tt.round,
				FeatureName: featureName,
			})
			// Do NOT create output files.

			result, err := ResumeWorkflow(dir, featureName, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if !result.Found {
				t.Fatal("Found should be true")
			}
			if !result.NeedsAgentRedispatch {
				t.Errorf("NeedsAgentRedispatch should be true when output is missing (state: %s)", tt.state)
			}
			if result.MissingOutputFile != tt.wantMissingFile {
				t.Errorf("MissingOutputFile: got %q, want %q", result.MissingOutputFile, tt.wantMissingFile)
			}
		})
	}
}

func TestResumeWorkflow_SkillChecksumsChanged(t *testing.T) {
	dir := t.TempDir()
	featureName := "checksum-test"

	saveTestState(t, dir, featureName, &WorkflowStateJSON{
		State:       StateHumanGate1,
		Round:       1,
		FeatureName: featureName,
		SkillChecksums: map[string]string{
			"plan_spec":  "sha256:aaa",
			"grill_spec": "sha256:bbb",
		},
	})

	t.Run("checksums unchanged", func(t *testing.T) {
		result, err := ResumeWorkflow(dir, featureName, map[string]string{
			"plan_spec":  "sha256:aaa",
			"grill_spec": "sha256:bbb",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.SkillsChanged {
			t.Error("SkillsChanged should be false when checksums match")
		}
	})

	t.Run("checksums changed", func(t *testing.T) {
		result, err := ResumeWorkflow(dir, featureName, map[string]string{
			"plan_spec":  "sha256:aaa",
			"grill_spec": "sha256:DIFFERENT",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.SkillsChanged {
			t.Error("SkillsChanged should be true when checksums differ")
		}
	})

	t.Run("extra key in current checksums", func(t *testing.T) {
		result, err := ResumeWorkflow(dir, featureName, map[string]string{
			"plan_spec":  "sha256:aaa",
			"grill_spec": "sha256:bbb",
			"new_skill":  "sha256:ccc",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.SkillsChanged {
			t.Error("SkillsChanged should be true when current has extra keys")
		}
	})
}

func TestResumeWorkflow_FinalizedState(t *testing.T) {
	dir := t.TempDir()
	featureName := "finalized-test"

	saveTestState(t, dir, featureName, &WorkflowStateJSON{
		State:       StateFinalized,
		Round:       3,
		FeatureName: featureName,
	})

	result, err := ResumeWorkflow(dir, featureName, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.Found {
		t.Fatal("Found should be true")
	}
	if result.NeedsAgentRedispatch {
		t.Error("NeedsAgentRedispatch should be false for FINALIZED")
	}
	if result.IsGateState {
		t.Error("IsGateState should be false for FINALIZED")
	}
}

func TestResumeWorkflow_EscalatedState(t *testing.T) {
	dir := t.TempDir()
	featureName := "escalated-test"

	saveTestState(t, dir, featureName, &WorkflowStateJSON{
		State:       StateEscalated,
		Round:       2,
		FeatureName: featureName,
	})

	result, err := ResumeWorkflow(dir, featureName, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.Found {
		t.Fatal("Found should be true")
	}
	if result.NeedsAgentRedispatch {
		t.Error("NeedsAgentRedispatch should be false for ESCALATED")
	}
	if result.IsGateState {
		t.Error("IsGateState should be false for ESCALATED")
	}
}

// ---------------------------------------------------------------------------
// ExpectedOutputFile unit tests
// ---------------------------------------------------------------------------

func TestResumeExpectedOutputFile(t *testing.T) {
	tests := []struct {
		state WorkflowState
		feat  string
		round int
		want  string
	}{
		{StateDiscovery, "auth", 1, filepath.Join("specs", "auth", "discovery-output.json")},
		{StateDrafting, "auth", 1, filepath.Join("specs", "auth", "drafter-output.json")},
		{StateReviewing, "auth", 2, filepath.Join("specs", "auth", "review-a-round-2.json")},
		{StateRevising, "auth", 3, filepath.Join("specs", "auth", "revision-round-3.json")},
		{StateJudging, "auth", 1, filepath.Join("specs", "auth", "judge-round-1.json")},
		{StateHumanGate1, "auth", 1, ""},
		{StateFinalized, "auth", 1, ""},
		{StateEscalated, "auth", 1, ""},
		{StateInit, "auth", 0, ""},
	}

	for _, tt := range tests {
		t.Run(tt.state.String(), func(t *testing.T) {
			got := ExpectedOutputFile(tt.state, tt.feat, tt.round)
			if got != tt.want {
				t.Errorf("ExpectedOutputFile(%s, %q, %d) = %q, want %q",
					tt.state, tt.feat, tt.round, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// IsGateState
// ---------------------------------------------------------------------------

func TestIsGateState(t *testing.T) {
	gateTests := []struct {
		state WorkflowState
		want  bool
	}{
		{StateInit, false},
		{StateDiscovery, false},
		{StateHumanGate1, true},
		{StateDrafting, false},
		{StateHumanGate2, true},
		{StateReviewing, false},
		{StateRevising, false},
		{StateJudging, false},
		{StateHumanGateFinal, true},
		{StateFinalized, false},
		{StateEscalated, false},
		{StateError, false},
	}

	for _, tt := range gateTests {
		t.Run(tt.state.String(), func(t *testing.T) {
			got := IsGateState(tt.state)
			if got != tt.want {
				t.Errorf("IsGateState(%s) = %v, want %v", tt.state, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// reloadFindings tests
// ---------------------------------------------------------------------------

func TestReloadFindings_LoadsMergedFindings(t *testing.T) {
	dir := t.TempDir()
	specDir := filepath.Join(dir, "specs", "test-feature")
	os.MkdirAll(specDir, 0o755)

	// Write merged findings for round 1 with 2 findings.
	merged := MergedFindings{
		SchemaVersion:   "1.0",
		Round:           1,
		TotalFindings:   2,
		TotalAfterDedup: 2,
		Findings: []MergedFinding{
			{
				ID:              "F-001",
				Description:     "SQL injection risk",
				Severity:        SeverityCritical,
				Impact:          "Data breach",
				Recommendation:  "Use parameterised queries",
				Lens:            "security",
				AffectedSection: "3.1",
				Status:          "open",
				RoundRaised:     1,
			},
			{
				ID:              "F-002",
				Description:     "Missing auth check",
				Severity:        SeverityMajor,
				Impact:          "Unauthorised access",
				Recommendation:  "Add auth middleware",
				Lens:            "security",
				AffectedSection: "3.2",
				Status:          "open",
				RoundRaised:     1,
			},
		},
	}
	data, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(specDir, "merged-findings-round-1.json"), data, 0o644)

	tracker := NewIssueTracker()
	reloadFindings(tracker, specDir, 1)

	summary := tracker.GetFindingSummary()
	if summary.Raised != 2 {
		t.Errorf("expected 2 raised findings, got %d", summary.Raised)
	}
	if summary.OpenCritical != 1 {
		t.Errorf("expected 1 open critical, got %d", summary.OpenCritical)
	}
	if summary.OpenMajor != 1 {
		t.Errorf("expected 1 open major, got %d", summary.OpenMajor)
	}
}

func TestReloadFindings_NoFiles(t *testing.T) {
	dir := t.TempDir()
	specDir := filepath.Join(dir, "specs", "empty")
	os.MkdirAll(specDir, 0o755)

	tracker := NewIssueTracker()
	reloadFindings(tracker, specDir, 3)

	summary := tracker.GetFindingSummary()
	if summary.Raised != 0 {
		t.Errorf("expected 0 raised findings, got %d", summary.Raised)
	}
}

func TestReloadFindings_MultipleRounds(t *testing.T) {
	dir := t.TempDir()
	specDir := filepath.Join(dir, "specs", "multi-round")
	os.MkdirAll(specDir, 0o755)

	// Round 1 findings.
	merged1 := MergedFindings{
		SchemaVersion: "1.0",
		Round:         1,
		Findings: []MergedFinding{
			{ID: "F-001", Description: "Issue 1", Severity: SeverityCritical, Status: "open", RoundRaised: 1,
				Impact: "High", Recommendation: "Fix", Lens: "security", AffectedSection: "1.0"},
		},
	}
	data1, _ := json.Marshal(merged1)
	os.WriteFile(filepath.Join(specDir, "merged-findings-round-1.json"), data1, 0o644)

	// Round 2 findings (new finding + same ID from round 1 should be skipped).
	merged2 := MergedFindings{
		SchemaVersion: "1.0",
		Round:         2,
		Findings: []MergedFinding{
			{ID: "F-001", Description: "Issue 1 again", Severity: SeverityCritical, Status: "open", RoundRaised: 1,
				Impact: "High", Recommendation: "Fix", Lens: "security", AffectedSection: "1.0"},
			{ID: "F-003", Description: "New issue", Severity: SeverityMinor, Status: "open", RoundRaised: 2,
				Impact: "Low", Recommendation: "Consider", Lens: "clarity", AffectedSection: "2.0"},
		},
	}
	data2, _ := json.Marshal(merged2)
	os.WriteFile(filepath.Join(specDir, "merged-findings-round-2.json"), data2, 0o644)

	tracker := NewIssueTracker()
	reloadFindings(tracker, specDir, 2)

	summary := tracker.GetFindingSummary()
	// F-001 from round 1 + F-003 from round 2 = 2 unique findings.
	if summary.Raised != 2 {
		t.Errorf("expected 2 raised findings (deduplicated), got %d", summary.Raised)
	}
}
