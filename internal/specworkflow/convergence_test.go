package specworkflow

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// makeTrackerWithCritical creates an IssueTracker with a single CRITICAL
// finding in the given status. It walks the finding through the required
// transitions to reach the target status.
func makeTrackerWithCritical(id string, status IssueStatus) *IssueTracker {
	tracker := trackerWithFindings(makeMergedFinding(id, SeverityCritical))
	_ = walkToStatus(tracker, id, status)
	return tracker
}

// minimalJudge returns a JudgeOutput with PASS verdict and no downgrades or
// issue updates.
func minimalJudge(verdict Verdict) *JudgeOutput {
	return &JudgeOutput{
		SchemaVersion: "1.0",
		Agent:         "judge",
		Round:         1,
		Verdict:       verdict,
		Rationale:     "test rationale",
	}
}

// minimalRevision returns a RevisionOutput with changes referencing the given
// finding IDs with non-empty sections_modified.
func minimalRevision(findingIDs ...string) *RevisionOutput {
	var changes []Change
	for _, id := range findingIDs {
		changes = append(changes, Change{
			FindingID:        id,
			Action:           "revised",
			Description:      "addressed finding " + id,
			SectionsModified: []string{"section-1"},
		})
	}
	return &RevisionOutput{
		SchemaVersion:   "1.0",
		Agent:           "revision",
		Round:           1,
		RevisedSpecFile: "spec-v2.md",
		Changes:         changes,
	}
}

// minimalState returns a WorkflowStateJSON at the given round.
func minimalState(round int) *WorkflowStateJSON {
	return &WorkflowStateJSON{
		State:       StateJudging,
		Round:       round,
		FeatureName: "test-feature",
	}
}

// ---------------------------------------------------------------------------
// PreCheck tests
// ---------------------------------------------------------------------------

func TestConvergence_PreCheck_RejectsCriticalNotClosed(t *testing.T) {
	// CRITICAL finding is still "raised" — not closed or dismissed.
	tracker := makeTrackerWithCritical("F-001", StatusRaised)
	judge := minimalJudge(VerdictPass)
	revision := minimalRevision("F-001")

	result := RunPreCheck(judge, tracker, revision, 3, 2)

	if result.Passed {
		t.Fatal("expected pre-check to fail when CRITICAL finding not closed/dismissed")
	}

	found := false
	for _, f := range result.Failures {
		if strings.Contains(f, "F-001") && strings.Contains(f, "CRITICAL") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected failure mentioning F-001 and CRITICAL, got: %v", result.Failures)
	}
}

func TestConvergence_PreCheck_RejectsChangeLogMissingCriticalReference(t *testing.T) {
	// CRITICAL finding is closed, but revision change log does not reference it.
	tracker := makeTrackerWithCritical("F-001", StatusClosed)
	judge := minimalJudge(VerdictPass)
	revision := minimalRevision() // empty — no changes at all

	result := RunPreCheck(judge, tracker, revision, 3, 2)

	if result.Passed {
		t.Fatal("expected pre-check to fail when change log missing CRITICAL reference")
	}

	found := false
	for _, f := range result.Failures {
		if strings.Contains(f, "F-001") && strings.Contains(f, "not referenced") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected failure about missing reference for F-001, got: %v", result.Failures)
	}
}

func TestConvergence_PreCheck_RejectsCriticalClosedEmptySections(t *testing.T) {
	// CRITICAL finding is closed, referenced in change log, but sections_modified is empty.
	tracker := makeTrackerWithCritical("F-001", StatusClosed)
	judge := minimalJudge(VerdictPass)
	revision := &RevisionOutput{
		SchemaVersion:   "1.0",
		Agent:           "revision",
		Round:           1,
		RevisedSpecFile: "spec-v2.md",
		Changes: []Change{
			{
				FindingID:        "F-001",
				Action:           "revised",
				Description:      "addressed",
				SectionsModified: []string{}, // empty
			},
		},
	}

	result := RunPreCheck(judge, tracker, revision, 3, 2)

	if result.Passed {
		t.Fatal("expected pre-check to fail when CRITICAL closed has empty sections_modified")
	}

	found := false
	for _, f := range result.Failures {
		if strings.Contains(f, "F-001") && strings.Contains(f, "empty sections_modified") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected failure about empty sections_modified for F-001, got: %v", result.Failures)
	}
}

func TestConvergence_PreCheck_RejectsMinRoundsNotMet(t *testing.T) {
	// No findings — but round 1 < minRounds 3.
	tracker := NewIssueTracker()
	judge := minimalJudge(VerdictPass)
	revision := minimalRevision()

	result := RunPreCheck(judge, tracker, revision, 1, 3)

	if result.Passed {
		t.Fatal("expected pre-check to fail when min_rounds not met")
	}

	found := false
	for _, f := range result.Failures {
		if strings.Contains(f, "min_rounds") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected failure about min_rounds, got: %v", result.Failures)
	}
}

func TestConvergence_PreCheck_PassesWhenAllConditionsMet(t *testing.T) {
	// CRITICAL finding closed, referenced in change log with sections_modified,
	// round meets minimum.
	tracker := makeTrackerWithCritical("F-001", StatusClosed)
	judge := minimalJudge(VerdictPass)
	revision := minimalRevision("F-001")

	result := RunPreCheck(judge, tracker, revision, 3, 2)

	if !result.Passed {
		t.Errorf("expected pre-check to pass, got failures: %v", result.Failures)
	}
}

// ---------------------------------------------------------------------------
// Authority limit tests
// ---------------------------------------------------------------------------

func TestConvergence_Authority_ExcessDowngradesRejected(t *testing.T) {
	judge := minimalJudge(VerdictRevise)
	judge.Downgrades = []Downgrade{
		{FindingID: "F-001", FromSeverity: SeverityCritical, ToSeverity: SeverityMajor, ReasonCode: "DUPLICATE_OF", ReasonDetail: "dup of F-010"},
		{FindingID: "F-002", FromSeverity: SeverityCritical, ToSeverity: SeverityMinor, ReasonCode: "OUT_OF_SCOPE", ReasonDetail: "out of scope"},
		{FindingID: "F-003", FromSeverity: SeverityMajor, ToSeverity: SeverityMinor, ReasonCode: "REVIEWER_ERROR", ReasonDetail: "reviewer was wrong"},
	}

	result := CheckAuthorityLimits(judge, 0, 0, 100)

	if result.Valid {
		t.Fatal("expected authority check to fail with >2 downgrades per round")
	}
	if len(result.RejectedDowngrades) == 0 {
		t.Fatal("expected at least one rejected downgrade")
	}
	if !strings.Contains(result.Reason, "downgrades exceed per-round limit") {
		t.Errorf("expected reason about downgrade limit, got: %s", result.Reason)
	}
}

func TestConvergence_Authority_ExcessDismissalsRejected(t *testing.T) {
	judge := minimalJudge(VerdictRevise)
	judge.IssueUpdates = []IssueUpdate{
		{FindingID: "F-001", NewStatus: "dismissed", Explanation: "not applicable"},
		{FindingID: "F-002", NewStatus: "dismissed", Explanation: "not applicable"},
		{FindingID: "F-003", NewStatus: "dismissed", Explanation: "not applicable"},
		{FindingID: "F-004", NewStatus: "dismissed", Explanation: "not applicable"},
	}

	result := CheckAuthorityLimits(judge, 0, 0, 100)

	if result.Valid {
		t.Fatal("expected authority check to fail with >3 dismissals per round")
	}
	if len(result.RejectedDismissals) == 0 {
		t.Fatal("expected at least one rejected dismissal")
	}
	if !strings.Contains(result.Reason, "dismissals exceed per-round limit") {
		t.Errorf("expected reason about dismissal limit, got: %s", result.Reason)
	}
}

func TestConvergence_Authority_InvalidReasonCodeRejected(t *testing.T) {
	judge := minimalJudge(VerdictRevise)
	judge.Downgrades = []Downgrade{
		{FindingID: "F-001", FromSeverity: SeverityCritical, ToSeverity: SeverityMajor, ReasonCode: "BECAUSE_I_SAID_SO", ReasonDetail: "invalid"},
	}

	result := CheckAuthorityLimits(judge, 0, 0, 100)

	if result.Valid {
		t.Fatal("expected authority check to fail with invalid reason_code")
	}

	found := false
	for _, id := range result.RejectedDowngrades {
		if id == "F-001" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected F-001 in rejected downgrades, got: %v", result.RejectedDowngrades)
	}
	if !strings.Contains(result.Reason, "invalid reason_code") {
		t.Errorf("expected reason about invalid reason_code, got: %s", result.Reason)
	}
}

func TestConvergence_Authority_DismissalPercentageExceedsThreshold(t *testing.T) {
	judge := minimalJudge(VerdictRevise)
	judge.IssueUpdates = []IssueUpdate{
		{FindingID: "F-001", NewStatus: "dismissed", Explanation: "not relevant"},
	}

	// Prior cumulative: 0 downgrades + 7 dismissals.
	// This round: 1 dismissal.
	// Total dismissals: 8 out of 10 raised = 80% -> escalate.
	result := CheckAuthorityLimits(judge, 0, 7, 10)

	if result.Valid {
		t.Fatal("expected authority check to fail when dismissals reach 80%")
	}
	if !strings.Contains(result.Reason, "cumulative dismissals") && !strings.Contains(result.Reason, "80%") {
		t.Errorf("expected reason about dismissal percentage, got: %s", result.Reason)
	}
}

func TestConvergence_Authority_DismissalsBelowThreshold(t *testing.T) {
	judge := minimalJudge(VerdictRevise)
	judge.IssueUpdates = []IssueUpdate{
		{FindingID: "F-001", NewStatus: "dismissed", Explanation: "not relevant"},
	}

	// Prior: 6 dismissals. This round: 1. Total: 7/10 = 70% -> no escalation.
	result := CheckAuthorityLimits(judge, 0, 6, 10)

	if !result.Valid {
		t.Errorf("expected authority check to pass at 70%%, got reason: %s", result.Reason)
	}
}

// ---------------------------------------------------------------------------
// ProcessVerdict tests
// ---------------------------------------------------------------------------

func TestConvergence_ProcessVerdict_PassOverriddenToRevise(t *testing.T) {
	// CRITICAL finding still raised -> pre-check should fail -> override to REVISE.
	tracker := makeTrackerWithCritical("F-001", StatusRaised)
	judge := minimalJudge(VerdictPass)
	revision := minimalRevision("F-001")
	state := minimalState(3)
	config := ConvergenceConfig{MinRounds: 2}

	result := ProcessVerdict(judge, tracker, revision, state, config)

	if result.FinalVerdict != VerdictRevise {
		t.Errorf("expected final verdict REVISE, got %s", result.FinalVerdict)
	}
	if result.OriginalVerdict != VerdictPass {
		t.Errorf("expected original verdict PASS, got %s", result.OriginalVerdict)
	}
	if !result.Overridden {
		t.Error("expected Overridden to be true")
	}
	if result.OverrideReason == "" {
		t.Error("expected non-empty OverrideReason")
	}
	if !strings.Contains(result.OverrideReason, "pre-check failed") {
		t.Errorf("expected override reason to mention pre-check, got: %s", result.OverrideReason)
	}
}

func TestConvergence_ProcessVerdict_RevisePassesThrough(t *testing.T) {
	tracker := NewIssueTracker()
	judge := minimalJudge(VerdictRevise)
	revision := minimalRevision()
	state := minimalState(1)
	config := ConvergenceConfig{MinRounds: 2}

	result := ProcessVerdict(judge, tracker, revision, state, config)

	if result.FinalVerdict != VerdictRevise {
		t.Errorf("expected final verdict REVISE, got %s", result.FinalVerdict)
	}
	if result.OriginalVerdict != VerdictRevise {
		t.Errorf("expected original verdict REVISE, got %s", result.OriginalVerdict)
	}
	if result.Overridden {
		t.Error("expected Overridden to be false for REVISE verdict")
	}
}

func TestConvergence_ProcessVerdict_BlockPassesThrough(t *testing.T) {
	tracker := NewIssueTracker()
	judge := minimalJudge(VerdictBlock)
	revision := minimalRevision()
	state := minimalState(1)
	config := ConvergenceConfig{MinRounds: 2}

	result := ProcessVerdict(judge, tracker, revision, state, config)

	if result.FinalVerdict != VerdictBlock {
		t.Errorf("expected final verdict BLOCK, got %s", result.FinalVerdict)
	}
	if result.OriginalVerdict != VerdictBlock {
		t.Errorf("expected original verdict BLOCK, got %s", result.OriginalVerdict)
	}
	if result.Overridden {
		t.Error("expected Overridden to be false for BLOCK verdict")
	}
}

func TestConvergence_ProcessVerdict_EscalationOnDismissalPercentage(t *testing.T) {
	tracker := NewIssueTracker()
	judge := minimalJudge(VerdictRevise)
	judge.IssueUpdates = []IssueUpdate{
		{FindingID: "F-001", NewStatus: "dismissed", Explanation: "not relevant"},
		{FindingID: "F-002", NewStatus: "dismissed", Explanation: "not relevant"},
	}
	revision := minimalRevision()
	state := minimalState(3)
	// Prior: 6 dismissals. This round: 2. Total: 8/10 = 80% -> escalate.
	config := ConvergenceConfig{
		MinRounds:            2,
		CumulativeDismissals: 6,
		TotalRaised:          10,
	}

	result := ProcessVerdict(judge, tracker, revision, state, config)

	if !result.ShouldEscalate {
		t.Error("expected ShouldEscalate to be true when dismissals reach 80%")
	}
}

func TestConvergence_ProcessVerdict_NoEscalationBelowDismissalThreshold(t *testing.T) {
	tracker := NewIssueTracker()
	judge := minimalJudge(VerdictRevise)
	judge.IssueUpdates = []IssueUpdate{
		{FindingID: "F-001", NewStatus: "dismissed", Explanation: "not relevant"},
	}
	revision := minimalRevision()
	state := minimalState(3)
	// Prior: 6 dismissals. This round: 1. Total: 7/10 = 70% -> no escalation.
	config := ConvergenceConfig{
		MinRounds:            2,
		CumulativeDismissals: 6,
		TotalRaised:          10,
	}

	result := ProcessVerdict(judge, tracker, revision, state, config)

	if result.ShouldEscalate {
		t.Error("expected ShouldEscalate to be false at 70%")
	}
}

func TestConvergence_ProcessVerdict_PassAcceptedWhenAllClean(t *testing.T) {
	// All CRITICAL findings closed, change log complete, round meets minimum.
	tracker := makeTrackerWithCritical("F-001", StatusClosed)
	judge := minimalJudge(VerdictPass)
	revision := minimalRevision("F-001")
	state := minimalState(3)
	config := ConvergenceConfig{MinRounds: 2}

	result := ProcessVerdict(judge, tracker, revision, state, config)

	if result.FinalVerdict != VerdictPass {
		t.Errorf("expected final verdict PASS, got %s", result.FinalVerdict)
	}
	if result.Overridden {
		t.Error("expected Overridden to be false when all conditions met")
	}
	if !result.PreCheckResult.Passed {
		t.Errorf("expected pre-check to pass, got failures: %v", result.PreCheckResult.Failures)
	}
}
