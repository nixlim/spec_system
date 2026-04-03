// Package specworkflow defines the core types for the adversarial spec review
// workflow. This file implements convergence judge verdict processing with
// deterministic anti-gaming pre-checks. All override logic is code-enforced
// (not LLM judgement) and produces structured reasons for auditability.
package specworkflow

import "fmt"

// ---------------------------------------------------------------------------
// Valid downgrade reason codes
// ---------------------------------------------------------------------------

// ValidDowngradeReasons is the set of reason codes that the judge may use
// when downgrading a finding's severity. Any reason code not in this set is
// rejected by the authority limit checker.
var ValidDowngradeReasons = map[string]bool{
	"DUPLICATE_OF":                true,
	"OUT_OF_SCOPE":                true,
	"CONTRADICTED_BY_REQUIREMENT": true,
	"REVIEWER_ERROR":              true,
}

// ---------------------------------------------------------------------------
// ConvergenceConfig
// ---------------------------------------------------------------------------

// ConvergenceConfig holds tuning parameters for convergence verdict
// processing. CumulativeDowngrades and CumulativeDismissals represent totals
// accumulated across all prior rounds (not including the current round).
type ConvergenceConfig struct {
	// MinRounds is the minimum number of review/revise rounds required
	// before a PASS verdict can be accepted.
	MinRounds int
	// CumulativeDowngrades is the total number of downgrades across all
	// prior rounds.
	CumulativeDowngrades int
	// CumulativeDismissals is the total number of dismissals across all
	// prior rounds.
	CumulativeDismissals int
	// TotalRaised is the total number of findings raised across all rounds,
	// used as the denominator for percentage-based escalation checks.
	TotalRaised int
}

// ---------------------------------------------------------------------------
// PreCheckResult
// ---------------------------------------------------------------------------

// PreCheckResult captures the outcome of the deterministic pre-check that
// validates whether a PASS verdict should be accepted.
type PreCheckResult struct {
	// Passed is true when all pre-check conditions are satisfied.
	Passed bool
	// Failures lists the reasons the pre-check failed (empty when Passed is true).
	Failures []string
}

// ---------------------------------------------------------------------------
// AuthorityCheckResult
// ---------------------------------------------------------------------------

// AuthorityCheckResult captures the outcome of verifying the judge's
// downgrade and dismissal counts against per-round and cumulative limits.
type AuthorityCheckResult struct {
	// Valid is true when all authority limits are respected.
	Valid bool
	// RejectedDowngrades lists finding IDs whose downgrades were rejected.
	RejectedDowngrades []string
	// RejectedDismissals lists finding IDs whose dismissals were rejected.
	RejectedDismissals []string
	// Reason describes why the authority check failed (empty when Valid is true).
	Reason string
}

// ---------------------------------------------------------------------------
// VerdictResult
// ---------------------------------------------------------------------------

// VerdictResult is the final processed verdict after pre-checks and authority
// limit enforcement. It records both the original and (possibly overridden)
// final verdict, along with structured reasons for any override.
type VerdictResult struct {
	// FinalVerdict is the verdict after all checks and possible overrides.
	FinalVerdict Verdict
	// OriginalVerdict is the verdict as issued by the judge before checks.
	OriginalVerdict Verdict
	// Overridden is true when the final verdict differs from the original.
	Overridden bool
	// OverrideReason explains why the verdict was overridden (empty when not overridden).
	OverrideReason string
	// PreCheckResult is the outcome of the deterministic pre-check.
	PreCheckResult PreCheckResult
	// AuthorityCheck is the outcome of the authority limit check.
	AuthorityCheck AuthorityCheckResult
	// ShouldEscalate is true when cumulative downgrade+dismissal limits
	// have been breached and the workflow should be escalated.
	ShouldEscalate bool
}

// ---------------------------------------------------------------------------
// RunPreCheck
// ---------------------------------------------------------------------------

// RunPreCheck performs a deterministic pre-check on a PASS verdict. It
// validates that:
//   - All CRITICAL findings are in terminal status "closed" or "dismissed"
//   - The revision change log references every CRITICAL and MAJOR finding by ID
//   - Any CRITICAL finding marked "closed" has a non-empty sections_modified
//     entry in the change log
//   - The minimum number of review rounds has been reached
//   - Judge authority limits have not been exceeded
//
// The check is pure logic — no LLM judgement is involved.
func RunPreCheck(judge *JudgeOutput, tracker *IssueTracker, revision *RevisionOutput, round, minRounds int) PreCheckResult {
	var failures []string

	// (a) Any CRITICAL finding not closed or dismissed.
	for _, issue := range tracker.Issues {
		if issue.Finding.Severity != SeverityCritical {
			continue
		}
		if issue.Status != StatusClosed && issue.Status != StatusDismissed {
			failures = append(failures, fmt.Sprintf(
				"CRITICAL finding %s has status %q (must be closed or dismissed)",
				issue.Finding.ID, issue.Status,
			))
		}
	}

	// Build lookup: finding_id -> Change from revision change log.
	changeByID := make(map[string]*Change, len(revision.Changes))
	for i := range revision.Changes {
		changeByID[revision.Changes[i].FindingID] = &revision.Changes[i]
	}

	// (b) Revision change log must reference every CRITICAL and MAJOR finding.
	// (c) CRITICAL findings marked "closed" must have non-empty sections_modified.
	for _, issue := range tracker.Issues {
		if issue.Finding.Severity != SeverityCritical && issue.Finding.Severity != SeverityMajor {
			continue
		}

		ch, referenced := changeByID[issue.Finding.ID]
		if !referenced {
			failures = append(failures, fmt.Sprintf(
				"%s finding %s not referenced in revision change log",
				issue.Finding.Severity, issue.Finding.ID,
			))
			continue
		}

		// (c) CRITICAL + closed must have sections_modified.
		if issue.Finding.Severity == SeverityCritical && issue.Status == StatusClosed {
			if len(ch.SectionsModified) == 0 {
				failures = append(failures, fmt.Sprintf(
					"CRITICAL finding %s is closed but change log has empty sections_modified",
					issue.Finding.ID,
				))
			}
		}
	}

	// (d) Minimum rounds not met.
	if round < minRounds {
		failures = append(failures, fmt.Sprintf(
			"round %d < min_rounds %d",
			round, minRounds,
		))
	}

	// (e) Authority limits.
	authCheck := CheckAuthorityLimits(judge, 0, 0, 0)
	if !authCheck.Valid {
		failures = append(failures, fmt.Sprintf(
			"authority limit exceeded: %s", authCheck.Reason,
		))
	}

	return PreCheckResult{
		Passed:   len(failures) == 0,
		Failures: failures,
	}
}

// ---------------------------------------------------------------------------
// CheckAuthorityLimits
// ---------------------------------------------------------------------------

// CheckAuthorityLimits validates that the judge has not exceeded per-round
// downgrade (max 2) and dismissal (max 3) limits, that all downgrade reason
// codes are valid, and that cumulative dismissals have not reached 80% of
// total raised findings (indicating the judge is dismissing rather than
// driving genuine convergence).
func CheckAuthorityLimits(judge *JudgeOutput, cumulativeDowngrades, cumulativeDismissals, totalRaised int) AuthorityCheckResult {
	result := AuthorityCheckResult{Valid: true}

	// Count this round's dismissals from issue updates.
	var roundDismissals int
	for _, u := range judge.IssueUpdates {
		if u.NewStatus == "dismissed" {
			roundDismissals++
		}
	}

	// Per-round downgrade limit: max 2.
	if len(judge.Downgrades) > 2 {
		result.Valid = false
		for _, d := range judge.Downgrades[2:] {
			result.RejectedDowngrades = append(result.RejectedDowngrades, d.FindingID)
		}
	}

	// Per-round dismissal limit: max 3.
	if roundDismissals > 3 {
		result.Valid = false
		count := 0
		for _, u := range judge.IssueUpdates {
			if u.NewStatus == "dismissed" {
				count++
				if count > 3 {
					result.RejectedDismissals = append(result.RejectedDismissals, u.FindingID)
				}
			}
		}
	}

	// Validate downgrade reason codes.
	for _, d := range judge.Downgrades {
		if !ValidDowngradeReasons[d.ReasonCode] {
			result.Valid = false
			result.RejectedDowngrades = append(result.RejectedDowngrades, d.FindingID)
		}
	}

	// Build reason string.
	if !result.Valid {
		var parts []string
		if len(judge.Downgrades) > 2 {
			parts = append(parts, fmt.Sprintf("%d downgrades exceed per-round limit of 2", len(judge.Downgrades)))
		}
		if roundDismissals > 3 {
			parts = append(parts, fmt.Sprintf("%d dismissals exceed per-round limit of 3", roundDismissals))
		}
		for _, d := range judge.Downgrades {
			if !ValidDowngradeReasons[d.ReasonCode] {
				parts = append(parts, fmt.Sprintf("invalid reason_code %q for downgrade of %s", d.ReasonCode, d.FindingID))
			}
		}
		reason := ""
		for i, p := range parts {
			if i > 0 {
				reason += "; "
			}
			reason += p
		}
		result.Reason = reason
	}

	// Percentage-based escalation check: if cumulative dismissals (including
	// this round) reach >=80% of all raised findings, the judge is dismissing
	// rather than driving genuine convergence.
	totalDismissals := cumulativeDismissals + roundDismissals
	if totalRaised > 0 {
		dismissalPct := float64(totalDismissals) / float64(totalRaised) * 100
		if dismissalPct >= 80 {
			result.Valid = false
			if result.Reason != "" {
				result.Reason += "; "
			}
			result.Reason += fmt.Sprintf(
				"cumulative dismissals %d/%d (%.0f%%) reaches escalation threshold of 80%%",
				totalDismissals, totalRaised, dismissalPct,
			)
		}
	}

	return result
}

// ---------------------------------------------------------------------------
// ProcessVerdict
// ---------------------------------------------------------------------------

// ProcessVerdict applies deterministic anti-gaming checks to the judge's
// verdict and returns the final processed result. If the judge issued a PASS
// verdict, the pre-check is run and a failure overrides the verdict to REVISE.
// Authority limits are always checked. If cumulative downgrades+dismissals
// exceed the escalation threshold, ShouldEscalate is set to true.
func ProcessVerdict(judge *JudgeOutput, tracker *IssueTracker, revision *RevisionOutput, state *WorkflowStateJSON, config ConvergenceConfig) VerdictResult {
	result := VerdictResult{
		FinalVerdict:    judge.Verdict,
		OriginalVerdict: judge.Verdict,
	}

	// Always check authority limits.
	result.AuthorityCheck = CheckAuthorityLimits(judge, config.CumulativeDowngrades, config.CumulativeDismissals, config.TotalRaised)

	// Count this round's dismissals for cumulative check.
	var roundDismissals int
	for _, u := range judge.IssueUpdates {
		if u.NewStatus == "dismissed" {
			roundDismissals++
		}
	}
	// Percentage-based escalation: if cumulative dismissals reach >=80% of
	// all raised findings, escalate to human intervention.
	totalDismissals := config.CumulativeDismissals + roundDismissals
	if config.TotalRaised > 0 {
		dismissalPct := float64(totalDismissals) / float64(config.TotalRaised) * 100
		if dismissalPct >= 80 {
			result.ShouldEscalate = true
		}
	}

	// Run pre-check only for PASS verdicts.
	if judge.Verdict == VerdictPass {
		result.PreCheckResult = RunPreCheck(judge, tracker, revision, state.Round, config.MinRounds)

		if !result.PreCheckResult.Passed {
			result.FinalVerdict = VerdictRevise
			result.Overridden = true
			reason := "PASS overridden to REVISE: pre-check failed ["
			for i, f := range result.PreCheckResult.Failures {
				if i > 0 {
					reason += "; "
				}
				reason += f
			}
			reason += "]"
			result.OverrideReason = reason
		}
	}

	return result
}
