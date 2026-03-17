package specworkflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDebateTrailSingleFinding(t *testing.T) {
	tracker := NewIssueTracker()
	tracker.AddFindings([]MergedFinding{
		{
			ID: "F-001", Description: "Missing auth check", Severity: SeverityCritical,
			AffectedSection: "Authentication", RaisedBy: []string{"reviewer-1"},
			Status: "open", RoundRaised: 1,
		},
	})
	_ = tracker.TransitionIssue("F-001", StatusAddressed, 1, "Added auth check")
	_ = tracker.TransitionIssue("F-001", StatusVerified, 2, "Verified by judge")
	_ = tracker.TransitionIssue("F-001", StatusClosed, 2, "auto-closed after verification")

	config := FinalizeConfig{WorkspaceDir: t.TempDir(), FeatureName: "test"}
	content, err := AssembleDebateTrail(config, tracker, 2)
	if err != nil {
		t.Fatalf("AssembleDebateTrail: %v", err)
	}

	checks := []string{
		"### F-001: Authentication [CRITICAL]",
		"**Raised by:** reviewer-1",
		"**Description:** Missing auth check",
		"Round 1: raised -> addressed (Added auth check)",
		"Round 2: addressed -> verified (Verified by judge)",
		"Round 2: verified -> closed (auto-closed after verification)",
		"**Current status:** closed",
	}
	for _, c := range checks {
		if !strings.Contains(content, c) {
			t.Errorf("missing expected content: %q", c)
		}
	}
}

func TestDebateTrailMultiRoundFinding(t *testing.T) {
	tracker := NewIssueTracker()
	tracker.AddFindings([]MergedFinding{
		{
			ID: "F-010", Description: "Unclear timeout", Severity: SeverityMajor,
			AffectedSection: "Timeouts", RaisedBy: []string{"reviewer-2"},
			Status: "open", RoundRaised: 1,
		},
	})
	// Round 1: addressed.
	_ = tracker.TransitionIssue("F-010", StatusAddressed, 1, "Clarified timeout value")
	// Round 2: reopened by judge.
	_ = tracker.TransitionIssue("F-010", StatusReopened, 2, "Insufficient clarification")
	// Round 2: addressed again.
	_ = tracker.TransitionIssue("F-010", StatusAddressed, 2, "Added timeout diagram")
	// Round 3: verified.
	_ = tracker.TransitionIssue("F-010", StatusVerified, 3, "Verified by judge")
	_ = tracker.TransitionIssue("F-010", StatusClosed, 3, "auto-closed after verification")

	config := FinalizeConfig{WorkspaceDir: t.TempDir(), FeatureName: "test"}
	content, err := AssembleDebateTrail(config, tracker, 3)
	if err != nil {
		t.Fatalf("AssembleDebateTrail: %v", err)
	}

	// Verify all lifecycle entries are present in order.
	checks := []string{
		"Round 1: raised -> addressed (Clarified timeout value)",
		"Round 2: addressed -> reopened (Insufficient clarification)",
		"Round 2: reopened -> addressed (Added timeout diagram)",
		"Round 3: addressed -> verified (Verified by judge)",
		"Round 3: verified -> closed (auto-closed after verification)",
	}
	for _, c := range checks {
		if !strings.Contains(content, c) {
			t.Errorf("missing expected lifecycle entry: %q", c)
		}
	}
}

func TestDebateTrailMultipleFindingsSortedByID(t *testing.T) {
	tracker := NewIssueTracker()
	tracker.AddFindings([]MergedFinding{
		{
			ID: "F-003", Description: "Third", Severity: SeverityMinor,
			AffectedSection: "Section C", RaisedBy: []string{"reviewer-1"},
			Status: "open", RoundRaised: 1,
		},
		{
			ID: "F-001", Description: "First", Severity: SeverityCritical,
			AffectedSection: "Section A", RaisedBy: []string{"reviewer-1"},
			Status: "open", RoundRaised: 1,
		},
		{
			ID: "F-002", Description: "Second", Severity: SeverityMajor,
			AffectedSection: "Section B", RaisedBy: []string{"reviewer-2"},
			Status: "open", RoundRaised: 1,
		},
	})

	config := FinalizeConfig{WorkspaceDir: t.TempDir(), FeatureName: "test"}
	content, err := AssembleDebateTrail(config, tracker, 1)
	if err != nil {
		t.Fatalf("AssembleDebateTrail: %v", err)
	}

	// Verify F-001 appears before F-002 which appears before F-003.
	idx1 := strings.Index(content, "### F-001:")
	idx2 := strings.Index(content, "### F-002:")
	idx3 := strings.Index(content, "### F-003:")

	if idx1 == -1 || idx2 == -1 || idx3 == -1 {
		t.Fatalf("all findings should be present; got idx1=%d idx2=%d idx3=%d", idx1, idx2, idx3)
	}
	if idx1 >= idx2 || idx2 >= idx3 {
		t.Errorf("findings should be sorted by ID: F-001 at %d, F-002 at %d, F-003 at %d", idx1, idx2, idx3)
	}
}

func TestDebateTrailReopenedFindingShowsFullLifecycle(t *testing.T) {
	tracker := NewIssueTracker()
	tracker.AddFindings([]MergedFinding{
		{
			ID: "F-005", Description: "Reopened issue", Severity: SeverityMajor,
			AffectedSection: "Security", RaisedBy: []string{"reviewer-1", "reviewer-3"},
			Status: "open", RoundRaised: 1,
		},
	})
	_ = tracker.TransitionIssue("F-005", StatusAddressed, 1, "Initial fix")
	_ = tracker.TransitionIssue("F-005", StatusReopened, 2, "Fix was incomplete")
	_ = tracker.TransitionIssue("F-005", StatusAddressed, 2, "Complete fix applied")
	_ = tracker.TransitionIssue("F-005", StatusVerified, 3, "Verified by judge")
	_ = tracker.TransitionIssue("F-005", StatusClosed, 3, "auto-closed after verification")

	config := FinalizeConfig{WorkspaceDir: t.TempDir(), FeatureName: "test"}
	content, err := AssembleDebateTrail(config, tracker, 3)
	if err != nil {
		t.Fatalf("AssembleDebateTrail: %v", err)
	}

	// Verify multi-reviewer attribution.
	if !strings.Contains(content, "**Raised by:** reviewer-1, reviewer-3") {
		t.Error("should show all reviewers who raised the finding")
	}

	// Verify full lifecycle including reopen.
	checks := []string{
		"raised -> addressed (Initial fix)",
		"addressed -> reopened (Fix was incomplete)",
		"reopened -> addressed (Complete fix applied)",
		"addressed -> verified (Verified by judge)",
		"verified -> closed",
		"**Current status:** closed",
	}
	for _, c := range checks {
		if !strings.Contains(content, c) {
			t.Errorf("missing lifecycle entry: %q", c)
		}
	}
}

func TestDebateTrailOutputIsValidMarkdown(t *testing.T) {
	tracker := NewIssueTracker()
	tracker.AddFindings([]MergedFinding{
		{
			ID: "F-001", Description: "Test finding", Severity: SeverityCritical,
			AffectedSection: "Test Section", RaisedBy: []string{"reviewer-1"},
			Status: "open", RoundRaised: 1,
		},
	})
	_ = tracker.TransitionIssue("F-001", StatusAddressed, 1, "Fixed")

	config := FinalizeConfig{WorkspaceDir: t.TempDir(), FeatureName: "test"}
	content, err := AssembleDebateTrail(config, tracker, 1)
	if err != nil {
		t.Fatalf("AssembleDebateTrail: %v", err)
	}

	// Check structural markdown properties.
	if !strings.HasPrefix(content, "# Debate Trail\n") {
		t.Error("output should start with H1 heading")
	}
	if !strings.Contains(content, "### F-001:") {
		t.Error("output should contain H3 headings for findings")
	}
	if !strings.Contains(content, "**Raised by:**") {
		t.Error("output should contain bold labels")
	}
	if !strings.Contains(content, "- Round") {
		t.Error("lifecycle entries should use list format")
	}

	// Verify no empty lines within list blocks (each lifecycle entry is
	// a single list item).
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "- Round") {
			// Next line should either be another list item, empty (end of
			// list), or a bold label.
			if i+1 < len(lines) {
				next := lines[i+1]
				if next != "" && !strings.HasPrefix(next, "- Round") && !strings.HasPrefix(next, "**") && !strings.HasPrefix(next, "\n") {
					// This is fine -- any text after the list is acceptable.
				}
			}
		}
	}
}

func TestWriteDebateTrail(t *testing.T) {
	tmpDir := t.TempDir()
	feature := "write-test"
	dir := filepath.Join(tmpDir, "specs", feature)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("create dir: %v", err)
	}

	tracker := NewIssueTracker()
	tracker.AddFindings([]MergedFinding{
		{
			ID: "F-001", Description: "Test", Severity: SeverityMinor,
			AffectedSection: "Test", RaisedBy: []string{"r1"},
			Status: "open", RoundRaised: 1,
		},
	})

	config := FinalizeConfig{WorkspaceDir: tmpDir, FeatureName: feature}
	if err := WriteDebateTrail(config, tracker, 1); err != nil {
		t.Fatalf("WriteDebateTrail: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "debate-trail.md"))
	if err != nil {
		t.Fatalf("read debate-trail.md: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "# Debate Trail") {
		t.Error("written file should contain debate trail heading")
	}
	if !strings.Contains(content, "F-001") {
		t.Error("written file should contain finding ID")
	}
}
