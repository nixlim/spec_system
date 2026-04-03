package codereview

import (
	"fmt"
	"time"
)

// ---------------------------------------------------------------------------
// ScopeGateData
// ---------------------------------------------------------------------------

// ScopeGateData contains the structured data presented at the scope
// confirmation gate (CR_HUMAN_GATE_SCOPE).
type ScopeGateData struct {
	// SpecPath is the optional path to the spec file.
	SpecPath string `json:"spec_path,omitempty"`
	// CodePath is the filesystem path to the target repository.
	CodePath string `json:"code_path"`
	// TaskListPath is the optional path to the task list file.
	TaskListPath string `json:"task_list_path,omitempty"`
	// GrillCodeMode is the detected review context mode.
	GrillCodeMode string `json:"grill_code_mode"`
	// GitBranch is the branch name at workflow start.
	GitBranch string `json:"git_branch"`
	// GitSHA is the HEAD commit SHA at workflow start.
	GitSHA string `json:"git_sha"`
}

// ---------------------------------------------------------------------------
// FixesGateData
// ---------------------------------------------------------------------------

// FixesGateData contains the structured data presented at the fixes review
// gate (CR_HUMAN_GATE_FIXES).
type FixesGateData struct {
	// FindingsSummary holds aggregate finding counts by severity and status.
	FindingsSummary CodeReviewFindingsSummary `json:"findings_summary"`
	// FixDetails lists the individual fix actions taken by the fix agent.
	FixDetails []FixAction `json:"fix_details"`
	// DeferredItems lists finding IDs that were intentionally deferred.
	DeferredItems []string `json:"deferred_items"`
	// GitDiffStat is the output of `git diff --stat` after fixes.
	GitDiffStat string `json:"git_diff_stat"`
	// TestResults contains structured test execution results.
	TestResults *TestResults `json:"test_results"`
	// Warnings holds any warnings (e.g., reduced_coverage, staleness).
	Warnings []string `json:"warnings,omitempty"`
}

// ---------------------------------------------------------------------------
// GetScopeGateData
// ---------------------------------------------------------------------------

// GetScopeGateData returns the structured data for the scope confirmation gate.
func (o *CodeReviewOrchestrator) GetScopeGateData() (*ScopeGateData, error) {
	if o.sm == nil {
		return nil, fmt.Errorf("orchestrator not started")
	}
	ws := o.sm.State()
	return &ScopeGateData{
		SpecPath:      ws.SpecPath,
		CodePath:      ws.CodePath,
		TaskListPath:  ws.TaskListPath,
		GrillCodeMode: ws.GrillCodeMode.String(),
		GitBranch:     ws.GitBranch,
		GitSHA:        ws.GitHeadSHA,
	}, nil
}

// ---------------------------------------------------------------------------
// GetFixesGateData
// ---------------------------------------------------------------------------

// GetFixesGateData returns the structured data for the fixes review gate.
// It reads the latest fix output and builds a summary for human review.
func (o *CodeReviewOrchestrator) GetFixesGateData() (*FixesGateData, error) {
	if o.sm == nil {
		return nil, fmt.Errorf("orchestrator not started")
	}
	ws := o.sm.State()

	data := &FixesGateData{
		FindingsSummary: ws.FindingsSummary,
		Warnings:        ws.Warnings,
	}

	// Populate fix details from the latest fix output if available.
	if o.lastFixOutput != nil {
		data.FixDetails = o.lastFixOutput.FixesApplied
		data.GitDiffStat = o.lastFixOutput.GitDiffStat
		data.TestResults = o.lastFixOutput.TestResults

		// Collect deferred items.
		for _, action := range o.lastFixOutput.FixesApplied {
			if action.Status == FixStatusDeferred {
				data.DeferredItems = append(data.DeferredItems, action.FindingID)
			}
		}
	}

	// Include any persisted warnings (e.g., reduced_coverage from review phase).
	for _, w := range o.sm.State().Warnings {
		data.Warnings = appendUniqueWarning(data.Warnings, w)
	}

	return data, nil
}

// ---------------------------------------------------------------------------
// HandleFixesGate
// ---------------------------------------------------------------------------

// HandleFixesGate processes the human response at the fixes review gate.
func (o *CodeReviewOrchestrator) HandleFixesGate(resp CRGateResponse) error {
	if o.sm == nil {
		return fmt.Errorf("orchestrator not started")
	}
	if o.sm.Current() != CRHumanGateFixes {
		return fmt.Errorf("workflow is not at fixes gate (current state: %s)", o.sm.Current())
	}

	switch resp.Action {
	case "re-review":
		// Increment round counter before transition so the guard evaluates
		// against the new round number.
		o.sm.State().Round++
		if err := o.sm.Transition(CRReviewing); err != nil {
			// Roll back round increment on failure.
			o.sm.State().Round--
			return fmt.Errorf("transition to reviewing: %w", err)
		}

	case "accept":
		if err := o.sm.Transition(CRComplete); err != nil {
			return fmt.Errorf("transition to complete: %w", err)
		}

	case "escalate":
		o.sm.State().EscalationReason = "operator escalated at fixes gate"
		if resp.Comment != "" {
			o.sm.State().EscalationReason = resp.Comment
		}
		if err := o.sm.Transition(CREscalated); err != nil {
			return fmt.Errorf("transition to escalated: %w", err)
		}

	default:
		return fmt.Errorf("invalid gate action %q, must be one of: re-review, accept, escalate", resp.Action)
	}

	// Persist comment if provided.
	if resp.Comment != "" {
		entry := CRCommentEntry{
			Gate:      "CR_HUMAN_GATE_FIXES",
			Action:    resp.Action,
			Comment:   resp.Comment,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}
		if err := AppendCRComment(o.featureDir, entry); err != nil {
			return fmt.Errorf("persist comment: %w", err)
		}
	}

	// Audit log.
	if o.auditLogger != nil {
		o.auditLogger.LogCodeReviewGate(o.sm.State().FeatureName, "CR_HUMAN_GATE_FIXES", resp.Action, resp.Comment)
	}

	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// appendUniqueWarning appends a warning only if it is not already present.
func appendUniqueWarning(warnings []string, warning string) []string {
	for _, w := range warnings {
		if w == warning {
			return warnings
		}
	}
	return append(warnings, warning)
}
