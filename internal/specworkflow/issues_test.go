package specworkflow

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func makeMergedFinding(id string, severity Severity) MergedFinding {
	return MergedFinding{
		ID:              id,
		SourceIDs:       []string{id},
		RaisedBy:        []string{"test-reviewer"},
		Description:     "test finding " + id,
		Severity:        severity,
		Impact:          "test impact",
		Recommendation:  "test recommendation",
		Lens:            "test-lens",
		AffectedSection: "section-1",
		Status:          "open",
		RoundRaised:     1,
	}
}

func trackerWithFindings(findings ...MergedFinding) *IssueTracker {
	t := NewIssueTracker()
	t.AddFindings(findings)
	return t
}

// ---------------------------------------------------------------------------
// Valid transitions
// ---------------------------------------------------------------------------

func TestIssueTransition_ValidTransitions(t *testing.T) {
	tests := []struct {
		name string
		from IssueStatus
		to   IssueStatus
	}{
		{"raised->addressed", StatusRaised, StatusAddressed},
		{"raised->dismissed", StatusRaised, StatusDismissed},
		{"raised->acknowledged", StatusRaised, StatusAcknowledged},
		{"addressed->verified", StatusAddressed, StatusVerified},
		{"addressed->reopened", StatusAddressed, StatusReopened},
		{"verified->closed", StatusVerified, StatusClosed},
		{"reopened->addressed", StatusReopened, StatusAddressed},
		{"closed->reopened", StatusClosed, StatusReopened},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tracker := trackerWithFindings(makeMergedFinding("F-001", SeverityCritical))

			// Walk the issue to the desired "from" status via valid intermediate
			// transitions, since we start at StatusRaised.
			if err := walkToStatus(tracker, "F-001", tt.from); err != nil {
				t.Fatalf("setup: could not walk to %s: %v", tt.from, err)
			}

			if err := tracker.TransitionIssue("F-001", tt.to, 1, "test"); err != nil {
				t.Errorf("expected valid transition %s -> %s, got error: %v", tt.from, tt.to, err)
			}

			issue := tracker.Issues["F-001"]
			if issue.Status != tt.to {
				t.Errorf("status after transition: got %s, want %s", issue.Status, tt.to)
			}
		})
	}
}

// walkToStatus transitions a finding from StatusRaised to the target status
// through the shortest valid path.
func walkToStatus(tracker *IssueTracker, id string, target IssueStatus) error {
	paths := map[IssueStatus][]IssueStatus{
		StatusRaised:       {},
		StatusAddressed:    {StatusAddressed},
		StatusVerified:     {StatusAddressed, StatusVerified},
		StatusClosed:       {StatusAddressed, StatusVerified, StatusClosed},
		StatusReopened:     {StatusAddressed, StatusReopened},
		StatusDismissed:    {StatusDismissed},
		StatusAcknowledged: {StatusAcknowledged},
	}

	steps, ok := paths[target]
	if !ok {
		return nil
	}

	for _, s := range steps {
		if err := tracker.TransitionIssue(id, s, 0, "setup"); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Invalid transitions
// ---------------------------------------------------------------------------

func TestIssueTransition_InvalidTransitions(t *testing.T) {
	tests := []struct {
		name string
		from IssueStatus
		to   IssueStatus
	}{
		{"closed->raised", StatusClosed, StatusRaised},
		{"closed->addressed", StatusClosed, StatusAddressed},
		{"dismissed->addressed", StatusDismissed, StatusAddressed},
		{"dismissed->raised", StatusDismissed, StatusRaised},
		{"acknowledged->addressed", StatusAcknowledged, StatusAddressed},
		{"acknowledged->raised", StatusAcknowledged, StatusRaised},
		{"raised->verified", StatusRaised, StatusVerified},
		{"raised->closed", StatusRaised, StatusClosed},
		{"raised->reopened", StatusRaised, StatusReopened},
		{"addressed->closed", StatusAddressed, StatusClosed},
		{"addressed->dismissed", StatusAddressed, StatusDismissed},
		{"verified->addressed", StatusVerified, StatusAddressed},
		{"verified->reopened", StatusVerified, StatusReopened},
		{"reopened->closed", StatusReopened, StatusClosed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tracker := trackerWithFindings(makeMergedFinding("F-001", SeverityCritical))

			if err := walkToStatus(tracker, "F-001", tt.from); err != nil {
				t.Fatalf("setup: could not walk to %s: %v", tt.from, err)
			}

			err := tracker.TransitionIssue("F-001", tt.to, 1, "test")
			if err == nil {
				t.Errorf("expected error for invalid transition %s -> %s, got nil", tt.from, tt.to)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Transition on non-existent finding
// ---------------------------------------------------------------------------

func TestIssueTransition_NotFound(t *testing.T) {
	tracker := NewIssueTracker()
	err := tracker.TransitionIssue("F-999", StatusAddressed, 1, "test")
	if err == nil {
		t.Error("expected error for non-existent finding, got nil")
	}
}

// ---------------------------------------------------------------------------
// ApplyRevisionChanges
// ---------------------------------------------------------------------------

func TestIssueApplyRevisionChanges(t *testing.T) {
	tracker := trackerWithFindings(
		makeMergedFinding("F-001", SeverityCritical),
		makeMergedFinding("F-002", SeverityMajor),
	)

	revision := &RevisionOutput{
		SchemaVersion:   "1.0",
		Agent:           "revision",
		Round:           1,
		RevisedSpecFile: "spec-v2.md",
		Changes: []Change{
			{FindingID: "F-001", Action: "revised", Description: "fixed it", SectionsModified: []string{"s1"}},
			{FindingID: "F-002", Action: "dismissed", Description: "not applicable", SectionsModified: []string{"s2"}},
		},
	}

	warnings, err := tracker.ApplyRevisionChanges(revision, 1)
	if err != nil {
		t.Fatalf("ApplyRevisionChanges: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %v", warnings)
	}

	// F-001 should be addressed (action=revised).
	if tracker.Issues["F-001"].Status != StatusAddressed {
		t.Errorf("F-001 status: got %s, want %s", tracker.Issues["F-001"].Status, StatusAddressed)
	}

	// F-002 should still be raised (action=dismissed is skipped by this method).
	if tracker.Issues["F-002"].Status != StatusRaised {
		t.Errorf("F-002 status: got %s, want %s", tracker.Issues["F-002"].Status, StatusRaised)
	}
}

func TestIssueApplyRevisionChanges_SkipsMissingFindings(t *testing.T) {
	tracker := trackerWithFindings(makeMergedFinding("F-001", SeverityCritical))

	revision := &RevisionOutput{
		SchemaVersion:   "1.0",
		Agent:           "revision",
		Round:           1,
		RevisedSpecFile: "spec-v2.md",
		Changes: []Change{
			{FindingID: "F-999", Action: "revised", Description: "unknown", SectionsModified: []string{"s1"}},
		},
	}

	// Should not error — returns a warning for the unknown finding.
	warnings, err := tracker.ApplyRevisionChanges(revision, 1)
	if err != nil {
		t.Fatalf("expected no error for missing finding, got: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(warnings))
	}
	if !strings.Contains(warnings[0], "F-999") {
		t.Errorf("warning should mention F-999, got: %s", warnings[0])
	}
}

// ---------------------------------------------------------------------------
// ApplyJudgeUpdates
// ---------------------------------------------------------------------------

func TestIssueApplyJudgeUpdates(t *testing.T) {
	tracker := trackerWithFindings(
		makeMergedFinding("F-001", SeverityCritical),
		makeMergedFinding("F-002", SeverityMajor),
		makeMergedFinding("F-003", SeverityMinor),
	)

	// Move F-001 and F-002 to addressed first (judge operates on addressed findings).
	_ = tracker.TransitionIssue("F-001", StatusAddressed, 1, "revised")
	_ = tracker.TransitionIssue("F-002", StatusAddressed, 1, "revised")

	judge := &JudgeOutput{
		SchemaVersion: "1.0",
		Agent:         "judge",
		Round:         1,
		Verdict:       VerdictRevise,
		Rationale:     "needs more work",
		IssueUpdates: []IssueUpdate{
			{FindingID: "F-001", NewStatus: "verified", Explanation: "looks good"},
			{FindingID: "F-002", NewStatus: "reopened", Explanation: "not actually fixed"},
			{FindingID: "F-003", NewStatus: "dismissed", Explanation: "not relevant"},
		},
	}

	warnings, err := tracker.ApplyJudgeUpdates(judge, 1)
	if err != nil {
		t.Fatalf("ApplyJudgeUpdates: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %v", warnings)
	}

	if tracker.Issues["F-001"].Status != StatusVerified {
		t.Errorf("F-001: got %s, want %s", tracker.Issues["F-001"].Status, StatusVerified)
	}
	if tracker.Issues["F-002"].Status != StatusReopened {
		t.Errorf("F-002: got %s, want %s", tracker.Issues["F-002"].Status, StatusReopened)
	}
	if tracker.Issues["F-003"].Status != StatusDismissed {
		t.Errorf("F-003: got %s, want %s", tracker.Issues["F-003"].Status, StatusDismissed)
	}
}

func TestIssueApplyJudgeUpdates_SkipsMissingFindings(t *testing.T) {
	tracker := NewIssueTracker()

	judge := &JudgeOutput{
		SchemaVersion: "1.0",
		Agent:         "judge",
		Round:         1,
		Verdict:       VerdictPass,
		Rationale:     "all good",
		IssueUpdates: []IssueUpdate{
			{FindingID: "F-999", NewStatus: "verified", Explanation: "ghost"},
		},
	}

	// Should not error — returns a warning for the unknown finding.
	warnings, err := tracker.ApplyJudgeUpdates(judge, 1)
	if err != nil {
		t.Fatalf("expected no error for missing finding, got: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(warnings))
	}
	if !strings.Contains(warnings[0], "F-999") {
		t.Errorf("warning should mention F-999, got: %s", warnings[0])
	}
}

// ---------------------------------------------------------------------------
// CloseVerifiedFindings
// ---------------------------------------------------------------------------

func TestIssueCloseVerifiedFindings(t *testing.T) {
	tracker := trackerWithFindings(
		makeMergedFinding("F-001", SeverityCritical),
		makeMergedFinding("F-002", SeverityMajor),
		makeMergedFinding("F-003", SeverityMinor),
	)

	// F-001 -> addressed -> verified
	_ = tracker.TransitionIssue("F-001", StatusAddressed, 1, "revised")
	_ = tracker.TransitionIssue("F-001", StatusVerified, 1, "judge verified")

	// F-002 -> addressed (not verified)
	_ = tracker.TransitionIssue("F-002", StatusAddressed, 1, "revised")

	// F-003 stays raised.

	tracker.CloseVerifiedFindings(2)

	if tracker.Issues["F-001"].Status != StatusClosed {
		t.Errorf("F-001: got %s, want %s", tracker.Issues["F-001"].Status, StatusClosed)
	}
	if tracker.Issues["F-002"].Status != StatusAddressed {
		t.Errorf("F-002: should remain addressed, got %s", tracker.Issues["F-002"].Status)
	}
	if tracker.Issues["F-003"].Status != StatusRaised {
		t.Errorf("F-003: should remain raised, got %s", tracker.Issues["F-003"].Status)
	}
}

// ---------------------------------------------------------------------------
// AcknowledgeMinorFindings
// ---------------------------------------------------------------------------

func TestIssueAcknowledgeMinorFindings(t *testing.T) {
	tracker := trackerWithFindings(
		makeMergedFinding("F-001", SeverityCritical),
		makeMergedFinding("F-002", SeverityMajor),
		makeMergedFinding("F-003", SeverityMinor),
		makeMergedFinding("F-004", SeverityObservation),
	)

	tracker.AcknowledgeMinorFindings(1)

	// CRITICAL and MAJOR should remain raised.
	if tracker.Issues["F-001"].Status != StatusRaised {
		t.Errorf("F-001 (CRITICAL): got %s, want raised", tracker.Issues["F-001"].Status)
	}
	if tracker.Issues["F-002"].Status != StatusRaised {
		t.Errorf("F-002 (MAJOR): got %s, want raised", tracker.Issues["F-002"].Status)
	}

	// MINOR and OBSERVATION should be acknowledged.
	if tracker.Issues["F-003"].Status != StatusAcknowledged {
		t.Errorf("F-003 (MINOR): got %s, want acknowledged", tracker.Issues["F-003"].Status)
	}
	if tracker.Issues["F-004"].Status != StatusAcknowledged {
		t.Errorf("F-004 (OBSERVATION): got %s, want acknowledged", tracker.Issues["F-004"].Status)
	}
}

func TestIssueAcknowledgeMinorFindings_OnlyAffectsRaised(t *testing.T) {
	tracker := trackerWithFindings(
		makeMergedFinding("F-001", SeverityMinor),
		makeMergedFinding("F-002", SeverityMinor),
	)

	// Move F-001 to addressed — should not be acknowledged.
	_ = tracker.TransitionIssue("F-001", StatusAddressed, 1, "revised")

	tracker.AcknowledgeMinorFindings(1)

	if tracker.Issues["F-001"].Status != StatusAddressed {
		t.Errorf("F-001 (addressed MINOR): got %s, want addressed", tracker.Issues["F-001"].Status)
	}
	if tracker.Issues["F-002"].Status != StatusAcknowledged {
		t.Errorf("F-002 (raised MINOR): got %s, want acknowledged", tracker.Issues["F-002"].Status)
	}
}

// ---------------------------------------------------------------------------
// Lifecycle history recording
// ---------------------------------------------------------------------------

func TestIssueLifecycleHistory(t *testing.T) {
	tracker := trackerWithFindings(makeMergedFinding("F-001", SeverityCritical))

	_ = tracker.TransitionIssue("F-001", StatusAddressed, 1, "revised in round 1")
	_ = tracker.TransitionIssue("F-001", StatusReopened, 1, "judge reopened")
	_ = tracker.TransitionIssue("F-001", StatusAddressed, 2, "revised in round 2")
	_ = tracker.TransitionIssue("F-001", StatusVerified, 2, "judge verified")
	_ = tracker.TransitionIssue("F-001", StatusClosed, 2, "auto-closed")

	issue := tracker.Issues["F-001"]
	if len(issue.StatusHistory) != 5 {
		t.Fatalf("expected 5 status changes, got %d", len(issue.StatusHistory))
	}

	expected := []struct {
		from IssueStatus
		to   IssueStatus
	}{
		{StatusRaised, StatusAddressed},
		{StatusAddressed, StatusReopened},
		{StatusReopened, StatusAddressed},
		{StatusAddressed, StatusVerified},
		{StatusVerified, StatusClosed},
	}

	for i, e := range expected {
		if issue.StatusHistory[i].From != e.from || issue.StatusHistory[i].To != e.to {
			t.Errorf("history[%d]: got %s->%s, want %s->%s",
				i, issue.StatusHistory[i].From, issue.StatusHistory[i].To, e.from, e.to)
		}
	}

	// History map should match.
	hist := tracker.History["F-001"]
	if len(hist) != 5 {
		t.Fatalf("tracker.History length: got %d, want 5", len(hist))
	}

	// Each entry should have a non-empty timestamp.
	for i, h := range hist {
		if h.Timestamp == "" {
			t.Errorf("history[%d].Timestamp is empty", i)
		}
	}
}

// ---------------------------------------------------------------------------
// GetFindingSummary
// ---------------------------------------------------------------------------

func TestIssueGetFindingSummary(t *testing.T) {
	tracker := trackerWithFindings(
		makeMergedFinding("F-001", SeverityCritical),
		makeMergedFinding("F-002", SeverityMajor),
		makeMergedFinding("F-003", SeverityMinor),
		makeMergedFinding("F-004", SeverityCritical),
		makeMergedFinding("F-005", SeverityMajor),
	)

	// Close F-004: raised -> addressed -> verified -> closed.
	_ = tracker.TransitionIssue("F-004", StatusAddressed, 1, "revised")
	_ = tracker.TransitionIssue("F-004", StatusVerified, 1, "verified")
	_ = tracker.TransitionIssue("F-004", StatusClosed, 1, "closed")

	// Dismiss F-003.
	_ = tracker.TransitionIssue("F-003", StatusDismissed, 1, "dismissed")

	summary := tracker.GetFindingSummary()

	if summary.Raised != 5 {
		t.Errorf("Raised: got %d, want 5", summary.Raised)
	}
	if summary.Closed != 1 {
		t.Errorf("Closed: got %d, want 1", summary.Closed)
	}
	// Open (non-terminal): F-001 (critical), F-002 (major), F-005 (major).
	if summary.OpenCritical != 1 {
		t.Errorf("OpenCritical: got %d, want 1", summary.OpenCritical)
	}
	if summary.OpenMajor != 2 {
		t.Errorf("OpenMajor: got %d, want 2", summary.OpenMajor)
	}
}

// ---------------------------------------------------------------------------
// GetOpenFindings
// ---------------------------------------------------------------------------

func TestIssueGetOpenFindings(t *testing.T) {
	tracker := trackerWithFindings(
		makeMergedFinding("F-001", SeverityCritical),
		makeMergedFinding("F-002", SeverityMajor),
		makeMergedFinding("F-003", SeverityMinor),
		makeMergedFinding("F-004", SeverityObservation),
	)

	// F-002 -> closed.
	_ = tracker.TransitionIssue("F-002", StatusAddressed, 1, "revised")
	_ = tracker.TransitionIssue("F-002", StatusVerified, 1, "verified")
	_ = tracker.TransitionIssue("F-002", StatusClosed, 1, "closed")

	// F-003 -> dismissed.
	_ = tracker.TransitionIssue("F-003", StatusDismissed, 1, "dismissed")

	// F-004 -> acknowledged.
	_ = tracker.TransitionIssue("F-004", StatusAcknowledged, 1, "ack")

	open := tracker.GetOpenFindings()
	if len(open) != 1 {
		t.Fatalf("expected 1 open finding, got %d", len(open))
	}
	if open[0].Finding.ID != "F-001" {
		t.Errorf("open finding ID: got %s, want F-001", open[0].Finding.ID)
	}
}

func TestIssueGetOpenFindings_IncludesNonTerminal(t *testing.T) {
	tracker := trackerWithFindings(
		makeMergedFinding("F-001", SeverityCritical),
		makeMergedFinding("F-002", SeverityMajor),
		makeMergedFinding("F-003", SeverityMinor),
	)

	// F-001 stays raised (open).
	// F-002 -> addressed (open).
	_ = tracker.TransitionIssue("F-002", StatusAddressed, 1, "revised")
	// F-003 -> addressed -> reopened (open).
	_ = tracker.TransitionIssue("F-003", StatusAddressed, 1, "revised")
	_ = tracker.TransitionIssue("F-003", StatusReopened, 1, "reopened")

	open := tracker.GetOpenFindings()
	if len(open) != 3 {
		t.Fatalf("expected 3 open findings, got %d", len(open))
	}
}

// ---------------------------------------------------------------------------
// GetFindingsByStatus
// ---------------------------------------------------------------------------

func TestIssueGetFindingsByStatus(t *testing.T) {
	tracker := trackerWithFindings(
		makeMergedFinding("F-001", SeverityCritical),
		makeMergedFinding("F-002", SeverityMajor),
		makeMergedFinding("F-003", SeverityMinor),
	)

	_ = tracker.TransitionIssue("F-002", StatusAddressed, 1, "revised")

	raised := tracker.GetFindingsByStatus(StatusRaised)
	if len(raised) != 2 {
		t.Errorf("raised count: got %d, want 2", len(raised))
	}

	addressed := tracker.GetFindingsByStatus(StatusAddressed)
	if len(addressed) != 1 {
		t.Errorf("addressed count: got %d, want 1", len(addressed))
	}
	if addressed[0].Finding.ID != "F-002" {
		t.Errorf("addressed finding: got %s, want F-002", addressed[0].Finding.ID)
	}
}

// ---------------------------------------------------------------------------
// AddFindings skips duplicates
// ---------------------------------------------------------------------------

func TestIssueAddFindings_SkipsDuplicates(t *testing.T) {
	tracker := NewIssueTracker()
	f := makeMergedFinding("F-001", SeverityCritical)
	tracker.AddFindings([]MergedFinding{f})
	tracker.AddFindings([]MergedFinding{f}) // duplicate

	if len(tracker.Issues) != 1 {
		t.Errorf("expected 1 issue, got %d", len(tracker.Issues))
	}
}

// ---------------------------------------------------------------------------
// IsValidTransition / IsTerminal helpers
// ---------------------------------------------------------------------------

func TestIssueIsTerminal(t *testing.T) {
	terminal := []IssueStatus{StatusClosed, StatusDismissed, StatusAcknowledged}
	for _, s := range terminal {
		if !IsTerminal(s) {
			t.Errorf("IsTerminal(%s) = false, want true", s)
		}
	}

	nonTerminal := []IssueStatus{StatusRaised, StatusAddressed, StatusVerified, StatusReopened}
	for _, s := range nonTerminal {
		if IsTerminal(s) {
			t.Errorf("IsTerminal(%s) = true, want false", s)
		}
	}
}

func TestIssueIsValidTransition(t *testing.T) {
	if !IsValidTransition(StatusRaised, StatusAddressed) {
		t.Error("raised->addressed should be valid")
	}
	if IsValidTransition(StatusClosed, StatusRaised) {
		t.Error("closed->raised should be invalid")
	}
}
