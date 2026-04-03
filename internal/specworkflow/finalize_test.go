package specworkflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newFinalizeTestDir creates a temporary workspace with the spec directory
// structure and returns the config pointing at it.
func newFinalizeTestDir(t *testing.T, featureName string) (FinalizeConfig, string) {
	t.Helper()
	tmpDir := t.TempDir()
	dir := filepath.Join(tmpDir, "specs", featureName)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("create spec dir: %v", err)
	}
	return FinalizeConfig{
		WorkspaceDir: tmpDir,
		FeatureName:  featureName,
	}, dir
}

// newFinalizeState returns a WorkflowStateJSON suitable for finalize tests.
func newFinalizeState() *WorkflowStateJSON {
	return &WorkflowStateJSON{
		State:                      StateHumanGateFinal,
		Round:                      3,
		FeatureName:                "test-feature",
		StartedAt:                  "2025-01-01T00:00:00Z",
		UpdatedAt:                  "2025-01-01T01:00:00Z",
		CumulativeCostUSD:          1.2345,
		CumulativeWallClockSeconds: 3600.5,
		AgentInvocations:           12,
		CurrentSpecVersion:         3,
	}
}

// newFinalizeTracker returns an IssueTracker with a mix of finding statuses.
func newFinalizeTracker(t *testing.T) *IssueTracker {
	t.Helper()
	tracker := NewIssueTracker()
	dismissalRationale := "Out of scope for this feature"
	resolutionNotes := "Low impact, accepted"
	tracker.AddFindings([]MergedFinding{
		{
			ID: "F-001", Description: "Missing error handling", Severity: SeverityCritical,
			AffectedSection: "Error Handling", RaisedBy: []string{"reviewer-1"},
			Status: "open", RoundRaised: 1,
		},
		{
			ID: "F-002", Description: "Ambiguous timeout behaviour", Severity: SeverityMajor,
			AffectedSection: "Timeouts", RaisedBy: []string{"reviewer-2"},
			Status: "open", RoundRaised: 1,
		},
		{
			ID: "F-003", Description: "Minor typo in section 3", Severity: SeverityMinor,
			AffectedSection: "Section 3", RaisedBy: []string{"reviewer-1"},
			Status: "open", RoundRaised: 1,
			ResolutionNotes: &resolutionNotes,
		},
		{
			ID: "F-004", Description: "Observation about naming", Severity: SeverityObservation,
			AffectedSection: "Naming", RaisedBy: []string{"reviewer-2"},
			Status: "open", RoundRaised: 2,
			DismissalRationale: &dismissalRationale,
		},
	})

	// F-001: raised -> addressed -> verified -> closed
	_ = tracker.TransitionIssue("F-001", StatusAddressed, 1, "Added error handling")
	_ = tracker.TransitionIssue("F-001", StatusVerified, 2, "Verified by judge")
	_ = tracker.TransitionIssue("F-001", StatusClosed, 2, "auto-closed after verification")

	// F-002: raised -> addressed -> verified -> closed
	_ = tracker.TransitionIssue("F-002", StatusAddressed, 2, "Clarified timeout behaviour")
	_ = tracker.TransitionIssue("F-002", StatusVerified, 3, "Verified by judge")
	_ = tracker.TransitionIssue("F-002", StatusClosed, 3, "auto-closed after verification")

	// F-003: raised -> acknowledged
	_ = tracker.TransitionIssue("F-003", StatusAcknowledged, 2, "auto-acknowledged minor/observation finding")

	// F-004: raised -> dismissed
	_ = tracker.TransitionIssue("F-004", StatusDismissed, 2, "Out of scope")

	return tracker
}

func TestFinalizeFullAssembly(t *testing.T) {
	config, dir := newFinalizeTestDir(t, "test-feature")
	state := newFinalizeState()
	tracker := newFinalizeTracker(t)

	// Create source spec.
	specContent := "# Test Spec v3\n\nThis is the spec content.\n"
	if err := os.WriteFile(filepath.Join(dir, "spec-v3.md"), []byte(specContent), 0644); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	// Create holdout file.
	holdoutContent := "### Scenario H1\n\nHoldout test scenario content.\n"
	if err := os.WriteFile(filepath.Join(dir, "test-feature-holdouts.md"), []byte(holdoutContent), 0644); err != nil {
		t.Fatalf("write holdout: %v", err)
	}

	// Create debate trail.
	debateContent := "# Debate Trail\n\nSome debate content.\n"
	if err := os.WriteFile(filepath.Join(dir, "debate-trail.md"), []byte(debateContent), 0644); err != nil {
		t.Fatalf("write debate trail: %v", err)
	}

	if err := AssembleFinalSpec(config, state, tracker); err != nil {
		t.Fatalf("AssembleFinalSpec: %v", err)
	}

	// Read the output.
	finalPath := filepath.Join(dir, "spec-final.md")
	data, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatalf("read final spec: %v", err)
	}
	content := string(data)

	// Verify all sections are present.
	sections := []string{
		"# Test Spec v3",
		"## Holdout Evaluation Scenarios",
		"Holdout test scenario content.",
		"## Convergence Summary",
		"## Accepted Risks",
		"## Debate Trail",
		"Some debate content.",
	}
	for _, s := range sections {
		if !strings.Contains(content, s) {
			t.Errorf("final spec missing section: %q", s)
		}
	}
}

func TestFinalizePrefersRoundHoldoutFile(t *testing.T) {
	config, dir := newFinalizeTestDir(t, "test-feature")
	state := newFinalizeState()
	tracker := NewIssueTracker()

	if err := os.WriteFile(filepath.Join(dir, "spec-v3.md"), []byte("# Spec\n"), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "test-feature-holdouts.md"), []byte("legacy holdouts\n"), 0o644); err != nil {
		t.Fatalf("write legacy holdouts: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "holdouts-round-3.md"), []byte("round holdouts\n"), 0o644); err != nil {
		t.Fatalf("write round holdouts: %v", err)
	}

	if err := AssembleFinalSpec(config, state, tracker); err != nil {
		t.Fatalf("AssembleFinalSpec: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "spec-final.md"))
	if err != nil {
		t.Fatalf("read final spec: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "round holdouts") {
		t.Fatal("expected final spec to use round holdout content")
	}
	if strings.Contains(content, "legacy holdouts") {
		t.Fatal("expected round holdouts to override legacy holdouts")
	}
}

func TestFinalizeFallsBackToLegacyHoldoutFile(t *testing.T) {
	config, dir := newFinalizeTestDir(t, "test-feature")
	state := newFinalizeState()
	tracker := NewIssueTracker()

	if err := os.WriteFile(filepath.Join(dir, "spec-v3.md"), []byte("# Spec\n"), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "test-feature-holdouts.md"), []byte("legacy holdouts\n"), 0o644); err != nil {
		t.Fatalf("write legacy holdouts: %v", err)
	}

	if err := AssembleFinalSpec(config, state, tracker); err != nil {
		t.Fatalf("AssembleFinalSpec: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "spec-final.md"))
	if err != nil {
		t.Fatalf("read final spec: %v", err)
	}
	if !strings.Contains(string(data), "legacy holdouts") {
		t.Fatal("expected final spec to fall back to legacy holdouts")
	}
}

func TestFinalizeMissingHoldoutFile(t *testing.T) {
	config, dir := newFinalizeTestDir(t, "test-feature")
	state := newFinalizeState()
	tracker := NewIssueTracker()

	// Create source spec but NO holdout file.
	if err := os.WriteFile(filepath.Join(dir, "spec-v3.md"), []byte("# Spec\n"), 0644); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	// Should proceed without error (warning logged, not returned).
	if err := AssembleFinalSpec(config, state, tracker); err != nil {
		t.Fatalf("AssembleFinalSpec should succeed without holdout file: %v", err)
	}

	// Verify spec-final.md was created.
	finalPath := filepath.Join(dir, "spec-final.md")
	data, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatalf("read final spec: %v", err)
	}
	content := string(data)

	// Holdout section should NOT be present.
	if strings.Contains(content, "## Holdout Evaluation Scenarios") {
		t.Error("holdout section should not be present when holdout file is missing")
	}

	// Convergence summary should still be present.
	if !strings.Contains(content, "## Convergence Summary") {
		t.Error("convergence summary should be present")
	}
}

func TestFinalizeConvergenceSummary(t *testing.T) {
	config, dir := newFinalizeTestDir(t, "test-feature")
	state := newFinalizeState()
	tracker := newFinalizeTracker(t)

	if err := os.WriteFile(filepath.Join(dir, "spec-v3.md"), []byte("# Spec\n"), 0644); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	if err := AssembleFinalSpec(config, state, tracker); err != nil {
		t.Fatalf("AssembleFinalSpec: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "spec-final.md"))
	if err != nil {
		t.Fatalf("read final spec: %v", err)
	}
	content := string(data)

	checks := []string{
		"**Rounds completed:** 3",
		"**Total findings raised:** 4",
		"**Total findings closed:** 2",
		"**Findings acknowledged:** 1",
		"**Final verdict:** PASS",
		"**Cumulative cost:** $1.2345",
		"**Wall clock time:** 3600.5s",
	}
	for _, c := range checks {
		if !strings.Contains(content, c) {
			t.Errorf("convergence summary missing: %q", c)
		}
	}
}

func TestFinalizeAcceptedRisks(t *testing.T) {
	config, dir := newFinalizeTestDir(t, "test-feature")
	state := newFinalizeState()
	tracker := newFinalizeTracker(t)

	if err := os.WriteFile(filepath.Join(dir, "spec-v3.md"), []byte("# Spec\n"), 0644); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	if err := AssembleFinalSpec(config, state, tracker); err != nil {
		t.Fatalf("AssembleFinalSpec: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "spec-final.md"))
	if err != nil {
		t.Fatalf("read final spec: %v", err)
	}
	content := string(data)

	// F-003 is acknowledged, F-004 is dismissed -- both should appear.
	if !strings.Contains(content, "F-003") {
		t.Error("accepted risks should include acknowledged finding F-003")
	}
	if !strings.Contains(content, "F-004") {
		t.Error("accepted risks should include dismissed finding F-004")
	}
	if !strings.Contains(content, "Minor typo in section 3") {
		t.Error("accepted risks should include F-003 description")
	}
	if !strings.Contains(content, "Out of scope for this feature") {
		t.Error("accepted risks should include F-004 dismissal rationale")
	}
	if !strings.Contains(content, "Low impact, accepted") {
		t.Error("accepted risks should include F-003 resolution notes as rationale")
	}
}

func TestFinalizeDebateTrailEmbedded(t *testing.T) {
	config, dir := newFinalizeTestDir(t, "test-feature")
	state := newFinalizeState()
	tracker := NewIssueTracker()

	if err := os.WriteFile(filepath.Join(dir, "spec-v3.md"), []byte("# Spec\n"), 0644); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	debateContent := "# Debate Trail\n\n### F-001: Error Handling [CRITICAL]\n\nFull lifecycle here.\n"
	if err := os.WriteFile(filepath.Join(dir, "debate-trail.md"), []byte(debateContent), 0644); err != nil {
		t.Fatalf("write debate trail: %v", err)
	}

	if err := AssembleFinalSpec(config, state, tracker); err != nil {
		t.Fatalf("AssembleFinalSpec: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "spec-final.md"))
	if err != nil {
		t.Fatalf("read final spec: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "## Debate Trail") {
		t.Error("debate trail section should be embedded in final spec")
	}
	if !strings.Contains(content, "Full lifecycle here.") {
		t.Error("debate trail content should be present in final spec")
	}
}

func TestFinalizeCreatesSpecFinal(t *testing.T) {
	config, dir := newFinalizeTestDir(t, "test-feature")
	state := newFinalizeState()
	tracker := NewIssueTracker()

	if err := os.WriteFile(filepath.Join(dir, "spec-v3.md"), []byte("# Spec\n"), 0644); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	if err := AssembleFinalSpec(config, state, tracker); err != nil {
		t.Fatalf("AssembleFinalSpec: %v", err)
	}

	// Verify file exists and is non-empty.
	finalPath := filepath.Join(dir, "spec-final.md")
	info, err := os.Stat(finalPath)
	if err != nil {
		t.Fatalf("spec-final.md should exist: %v", err)
	}
	if info.Size() == 0 {
		t.Error("spec-final.md should not be empty")
	}

	// Verify state was updated to FINALIZED.
	loaded, err := LoadState(dir)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if loaded.State != StateFinalized {
		t.Errorf("state should be FINALIZED, got %s", loaded.State)
	}
}

func TestFinalizeSpecDirHelper(t *testing.T) {
	config := FinalizeConfig{
		WorkspaceDir: "/workspace",
		FeatureName:  "my-feature",
	}
	got := specDir(config)
	want := filepath.Join("/workspace", "specs", "my-feature")
	if got != want {
		t.Errorf("specDir: got %q, want %q", got, want)
	}
}
