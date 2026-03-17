// Package specworkflow defines the core types for the adversarial spec review
// workflow. This file implements issue lifecycle tracking for findings raised
// during adversarial review, including declarative status transitions, batch
// operations driven by agent outputs, and aggregate summaries.
package specworkflow

import (
	"fmt"
	"sort"
	"time"
)

// ---------------------------------------------------------------------------
// IssueStatus
// ---------------------------------------------------------------------------

// IssueStatus represents the lifecycle status of a tracked finding.
type IssueStatus string

const (
	// StatusRaised indicates a finding has been raised but not yet addressed.
	StatusRaised IssueStatus = "raised"
	// StatusAddressed indicates a finding has been addressed in a revision.
	StatusAddressed IssueStatus = "addressed"
	// StatusVerified indicates a finding's resolution has been verified by the judge.
	StatusVerified IssueStatus = "verified"
	// StatusClosed indicates a verified finding has been closed.
	StatusClosed IssueStatus = "closed"
	// StatusReopened indicates a previously addressed finding was reopened.
	StatusReopened IssueStatus = "reopened"
	// StatusDismissed indicates a finding was dismissed as not applicable.
	StatusDismissed IssueStatus = "dismissed"
	// StatusAcknowledged indicates a minor finding was acknowledged without revision.
	StatusAcknowledged IssueStatus = "acknowledged"
)

// ---------------------------------------------------------------------------
// Transition table (declarative)
// ---------------------------------------------------------------------------

// validTransitions defines the allowed status transitions. Terminal statuses
// (closed, dismissed, acknowledged) have no outgoing transitions.
var validTransitions = map[IssueStatus]map[IssueStatus]bool{
	StatusRaised:    {StatusAddressed: true, StatusDismissed: true, StatusAcknowledged: true},
	StatusAddressed: {StatusVerified: true, StatusReopened: true},
	StatusVerified:  {StatusClosed: true},
	StatusReopened:  {StatusAddressed: true},
	// Terminal statuses — no outgoing transitions.
	StatusClosed:       {},
	StatusDismissed:    {},
	StatusAcknowledged: {},
}

// terminalStatuses is the set of statuses from which no further transition
// is possible.
var terminalStatuses = map[IssueStatus]bool{
	StatusClosed:       true,
	StatusDismissed:    true,
	StatusAcknowledged: true,
}

// IsTerminal reports whether the given status is a terminal status.
func IsTerminal(s IssueStatus) bool {
	return terminalStatuses[s]
}

// IsValidTransition reports whether transitioning from one status to another
// is permitted by the transition table.
func IsValidTransition(from, to IssueStatus) bool {
	targets, ok := validTransitions[from]
	if !ok {
		return false
	}
	return targets[to]
}

// ---------------------------------------------------------------------------
// StatusChange
// ---------------------------------------------------------------------------

// StatusChange records a single status transition for a tracked finding.
type StatusChange struct {
	// From is the status before the transition.
	From IssueStatus `json:"from"`
	// To is the status after the transition.
	To IssueStatus `json:"to"`
	// Round is the workflow round in which the transition occurred.
	Round int `json:"round"`
	// Reason describes why the transition was made.
	Reason string `json:"reason"`
	// Timestamp is the ISO 8601 time when the transition occurred.
	Timestamp string `json:"timestamp"`
}

// ---------------------------------------------------------------------------
// TrackedIssue
// ---------------------------------------------------------------------------

// TrackedIssue pairs a merged finding with its current lifecycle status and
// the full history of status changes.
type TrackedIssue struct {
	// Finding is the underlying merged finding.
	Finding MergedFinding `json:"finding"`
	// Status is the current lifecycle status.
	Status IssueStatus `json:"status"`
	// StatusHistory is the ordered list of status transitions.
	StatusHistory []StatusChange `json:"status_history"`
}

// ---------------------------------------------------------------------------
// IssueTracker
// ---------------------------------------------------------------------------

// IssueTracker maintains the lifecycle state of all tracked findings across
// review rounds. It enforces the declarative transition table and records a
// full audit trail of status changes.
type IssueTracker struct {
	// Issues maps finding IDs to their tracked issue state.
	Issues map[string]*TrackedIssue
	// History maps finding IDs to their ordered list of status changes.
	History map[string][]StatusChange
}

// NewIssueTracker returns an initialised IssueTracker with empty maps.
func NewIssueTracker() *IssueTracker {
	return &IssueTracker{
		Issues:  make(map[string]*TrackedIssue),
		History: make(map[string][]StatusChange),
	}
}

// AddFindings adds new merged findings to the tracker with status "raised".
// Findings whose ID already exists in the tracker are silently skipped to
// prevent duplicates.
func (t *IssueTracker) AddFindings(findings []MergedFinding) {
	for _, f := range findings {
		if _, exists := t.Issues[f.ID]; exists {
			continue
		}
		t.Issues[f.ID] = &TrackedIssue{
			Finding: f,
			Status:  StatusRaised,
		}
		t.History[f.ID] = nil
	}
}

// TransitionIssue moves a finding from its current status to newStatus,
// validating the transition against the declarative transition table. It
// records a StatusChange entry in both the issue's StatusHistory and the
// tracker's History map. Returns an error if the finding does not exist or
// the transition is not permitted.
func (t *IssueTracker) TransitionIssue(findingID string, newStatus IssueStatus, round int, reason string) error {
	issue, ok := t.Issues[findingID]
	if !ok {
		return fmt.Errorf("finding %q not found in tracker", findingID)
	}

	if !IsValidTransition(issue.Status, newStatus) {
		return fmt.Errorf("invalid transition for finding %q: %s -> %s", findingID, issue.Status, newStatus)
	}

	change := StatusChange{
		From:      issue.Status,
		To:        newStatus,
		Round:     round,
		Reason:    reason,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	issue.Status = newStatus
	issue.StatusHistory = append(issue.StatusHistory, change)
	t.History[findingID] = append(t.History[findingID], change)

	return nil
}

// ApplyRevisionChanges processes a RevisionOutput and transitions each finding
// with action "revised" to StatusAddressed. Findings referenced in the revision
// but not present in the tracker are collected as warnings. Returns any
// warnings and the first transition error encountered, if any.
func (t *IssueTracker) ApplyRevisionChanges(revision *RevisionOutput, round int) (warnings []string, err error) {
	for _, change := range revision.Changes {
		if change.Action != "revised" {
			continue
		}
		if _, ok := t.Issues[change.FindingID]; !ok {
			warnings = append(warnings, fmt.Sprintf("finding %q from revision not found in tracker, skipping", change.FindingID))
			continue
		}
		if err := t.TransitionIssue(change.FindingID, StatusAddressed, round, change.Description); err != nil {
			return warnings, fmt.Errorf("applying revision change for %q: %w", change.FindingID, err)
		}
	}
	return warnings, nil
}

// ApplyJudgeUpdates processes a JudgeOutput and transitions each finding to
// its new status (verified, reopened, or dismissed). Findings referenced in
// the judge output but not present in the tracker are collected as warnings.
// Returns any warnings and the first transition error encountered, if any.
func (t *IssueTracker) ApplyJudgeUpdates(judge *JudgeOutput, round int) (warnings []string, err error) {
	for _, update := range judge.IssueUpdates {
		if _, ok := t.Issues[update.FindingID]; !ok {
			warnings = append(warnings, fmt.Sprintf("finding %q from judge not found in tracker, skipping", update.FindingID))
			continue
		}
		newStatus := IssueStatus(update.NewStatus)
		if err := t.TransitionIssue(update.FindingID, newStatus, round, update.Explanation); err != nil {
			return warnings, fmt.Errorf("applying judge update for %q: %w", update.FindingID, err)
		}
	}
	return warnings, nil
}

// CloseVerifiedFindings transitions all findings currently in StatusVerified
// to StatusClosed.
func (t *IssueTracker) CloseVerifiedFindings(round int) {
	for id, issue := range t.Issues {
		if issue.Status == StatusVerified {
			// Transition is guaranteed valid by the table (verified -> closed).
			_ = t.TransitionIssue(id, StatusClosed, round, "auto-closed after verification")
		}
	}
}

// AcknowledgeMinorFindings transitions all findings with severity MINOR or
// OBSERVATION that are currently in StatusRaised to StatusAcknowledged.
func (t *IssueTracker) AcknowledgeMinorFindings(round int) {
	for id, issue := range t.Issues {
		if issue.Status != StatusRaised {
			continue
		}
		if issue.Finding.Severity == SeverityMinor || issue.Finding.Severity == SeverityObservation {
			_ = t.TransitionIssue(id, StatusAcknowledged, round, "auto-acknowledged minor/observation finding")
		}
	}
}

// GetOpenFindings returns all tracked issues whose status is not terminal
// (i.e. not closed, dismissed, or acknowledged). The returned slice is
// sorted by finding ID for deterministic output.
func (t *IssueTracker) GetOpenFindings() []TrackedIssue {
	var open []TrackedIssue
	for _, issue := range t.Issues {
		if !IsTerminal(issue.Status) {
			open = append(open, *issue)
		}
	}
	sort.Slice(open, func(i, j int) bool {
		return open[i].Finding.ID < open[j].Finding.ID
	})
	return open
}

// GetFindingsByStatus returns all tracked issues with the given status. The
// returned slice is sorted by finding ID for deterministic output.
func (t *IssueTracker) GetFindingsByStatus(status IssueStatus) []TrackedIssue {
	var result []TrackedIssue
	for _, issue := range t.Issues {
		if issue.Status == status {
			result = append(result, *issue)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Finding.ID < result[j].Finding.ID
	})
	return result
}

// GetFindingSummary returns a FindingsSummary with aggregate counts of
// raised, closed, open critical, and open major findings.
func (t *IssueTracker) GetFindingSummary() FindingsSummary {
	var summary FindingsSummary
	for _, issue := range t.Issues {
		summary.Raised++
		if issue.Status == StatusClosed {
			summary.Closed++
		}
		if !IsTerminal(issue.Status) {
			switch issue.Finding.Severity {
			case SeverityCritical:
				summary.OpenCritical++
			case SeverityMajor:
				summary.OpenMajor++
			}
		}
	}
	return summary
}
