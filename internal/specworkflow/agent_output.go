// Package specworkflow defines the core types for the adversarial spec review
// workflow. This file contains the structured output schemas for each agent
// in the pipeline (discovery, drafter, reviewer, revision, judge) plus the
// merged-findings ledger, along with validation functions that collect all
// errors rather than failing on the first.
package specworkflow

import (
	"fmt"
	"regexp"
	"strings"
)

// ---------------------------------------------------------------------------
// Shared / embedded sub-types
// ---------------------------------------------------------------------------

// Actor represents a human, system, or external entity that interacts with
// the system under specification.
type Actor struct {
	// Name is the actor identifier (e.g. "Admin User").
	Name string `json:"name"`
	// Type is one of "human", "system", or "external".
	Type string `json:"type"`
	// Description summarises the actor's role.
	Description string `json:"description"`
}

// Scope separates what is and is not covered by a specification.
type Scope struct {
	// InScope lists items explicitly covered.
	InScope []string `json:"in_scope"`
	// OutOfScope lists items explicitly excluded.
	OutOfScope []string `json:"out_of_scope"`
}

// IntegrationPoint describes an external system that integrates with the
// feature being specified.
type IntegrationPoint struct {
	// System is the name of the external system.
	System string `json:"system"`
	// Description summarises the integration.
	Description string `json:"description"`
	// Direction is one of "inbound", "outbound", or "bidirectional".
	Direction string `json:"direction"`
}

// Priority assigns a priority level to a scope item or requirement.
type Priority struct {
	// Item is the requirement or scope item being prioritised.
	Item string `json:"item"`
	// Priority is one of "P0" through "P4".
	Priority string `json:"priority"`
	// Rationale explains why this priority was assigned.
	Rationale string `json:"rationale"`
}

// Assumption captures an assumption made during discovery along with the
// agent's confidence level and an optional question for the user.
type Assumption struct {
	// Assumption is the assumption text.
	Assumption string `json:"assumption"`
	// Confidence is one of "high", "medium", or "low".
	Confidence string `json:"confidence"`
	// QuestionForUser is an optional clarifying question.
	QuestionForUser *string `json:"question_for_user,omitempty"`
}

// AmbiguityWarning flags a section of the drafted spec that the drafter
// considers ambiguous.
type AmbiguityWarning struct {
	// ID is a unique identifier in the form "AMB-W-NNN".
	ID string `json:"id"`
	// Section is the spec section where the ambiguity was found.
	Section string `json:"section"`
	// Ambiguity describes the ambiguous language.
	Ambiguity string `json:"ambiguity"`
	// AgentAssumption is the assumption the drafter made.
	AgentAssumption string `json:"agent_assumption"`
	// QuestionForUser is a clarifying question for the human reviewer.
	QuestionForUser string `json:"question_for_user"`
}

// StructuralSummary provides aggregate counts of structural elements in
// the drafted specification.
type StructuralSummary struct {
	// UserStoryCount is the number of user stories in the draft.
	UserStoryCount int `json:"user_story_count"`
	// BDDScenarioCount is the number of BDD scenarios.
	BDDScenarioCount int `json:"bdd_scenario_count"`
	// FRCount is the number of functional requirements.
	FRCount int `json:"fr_count"`
	// TestCount is the number of test-dataset entries.
	TestCount int `json:"test_count"`
}

// Finding represents a single issue raised during adversarial review.
type Finding struct {
	// ID uniquely identifies the finding (e.g. "F-001").
	ID string `json:"id"`
	// Description summarises the issue.
	Description string `json:"description"`
	// Severity is the finding severity level.
	Severity Severity `json:"severity"`
	// Impact describes the potential impact of the issue.
	Impact string `json:"impact"`
	// Recommendation suggests how to address the finding.
	Recommendation string `json:"recommendation"`
	// Lens identifies which review lens raised this finding.
	Lens string `json:"lens"`
	// AffectedSection is the spec section affected.
	AffectedSection string `json:"affected_section"`
	// Target indicates whether the finding applies to the spec or the holdout set.
	Target string `json:"target,omitempty"`
	// ConstitutionPrinciple optionally links the finding to a constitution
	// principle.
	ConstitutionPrinciple *string `json:"constitution_principle,omitempty"`
}

// IntegrityCheck represents a single structural integrity check result.
type IntegrityCheck struct {
	// Check is the name of the integrity check.
	Check string `json:"check"`
	// Result is "PASS" or "FAIL".
	Result string `json:"result"`
	// Detail provides optional additional information.
	Detail *string `json:"detail,omitempty"`
}

// StructuralIntegrity reports the results of structural integrity checks
// performed by the reviewer.
type StructuralIntegrity struct {
	// Performed indicates whether structural integrity checks were run.
	Performed bool `json:"performed"`
	// Checks is the list of individual check results.
	Checks []IntegrityCheck `json:"checks"`
}

// Change records how a finding was addressed during revision.
type Change struct {
	// FindingID references the finding that prompted this change.
	FindingID string `json:"finding_id"`
	// Action is "revised" or "dismissed".
	Action string `json:"action"`
	// Description summarises what was changed.
	Description string `json:"description"`
	// SectionsModified lists the spec sections that were modified.
	SectionsModified []string `json:"sections_modified"`
}

// DismissalRequest is a revision agent's request to dismiss a finding.
type DismissalRequest struct {
	// FindingID references the finding to dismiss.
	FindingID string `json:"finding_id"`
	// Rationale explains why the finding should be dismissed.
	Rationale string `json:"rationale"`
}

// IssueUpdate records a status change for a finding in the judge's output.
type IssueUpdate struct {
	// FindingID references the finding.
	FindingID string `json:"finding_id"`
	// NewStatus is "verified", "reopened", or "dismissed".
	NewStatus string `json:"new_status"`
	// Explanation justifies the status change.
	Explanation string `json:"explanation"`
}

// Downgrade records a severity downgrade decided by the judge.
type Downgrade struct {
	// FindingID references the finding being downgraded.
	FindingID string `json:"finding_id"`
	// FromSeverity is the original severity level.
	FromSeverity Severity `json:"from_severity"`
	// ToSeverity is the new (lower) severity level.
	ToSeverity Severity `json:"to_severity"`
	// ReasonCode is one of DUPLICATE_OF, OUT_OF_SCOPE,
	// CONTRADICTED_BY_REQUIREMENT, or REVIEWER_ERROR.
	ReasonCode string `json:"reason_code"`
	// ReasonDetail provides additional context for the downgrade.
	ReasonDetail string `json:"reason_detail"`
}

// StructuralDelta reports whether regressions were introduced during revision.
type StructuralDelta struct {
	// RegressionsFound indicates whether any regressions were detected.
	RegressionsFound bool `json:"regressions_found"`
	// Details lists the individual regression descriptions.
	Details []string `json:"details"`
}

// MergedFinding extends Finding with deduplication and lifecycle metadata.
type MergedFinding struct {
	// ID is the canonical finding ID in the merged ledger.
	ID string `json:"id"`
	// SourceIDs lists the original finding IDs that were merged.
	SourceIDs []string `json:"source_ids"`
	// RaisedBy lists the reviewer agents that raised this finding.
	RaisedBy []string `json:"raised_by"`
	// Description summarises the issue.
	Description string `json:"description"`
	// Severity is the finding severity level.
	Severity Severity `json:"severity"`
	// Impact describes the potential impact.
	Impact string `json:"impact"`
	// Recommendation suggests how to address the finding.
	Recommendation string `json:"recommendation"`
	// Lens identifies the review lens.
	Lens string `json:"lens"`
	// AffectedSection is the spec section affected.
	AffectedSection string `json:"affected_section"`
	// Target indicates whether the finding applies to the spec or the holdout set.
	Target string `json:"target,omitempty"`
	// ConstitutionPrinciple optionally links to a constitution principle.
	ConstitutionPrinciple *string `json:"constitution_principle,omitempty"`
	// Status is the current lifecycle status (e.g. "open", "closed").
	Status string `json:"status"`
	// RoundRaised is the review round in which the finding was first raised.
	RoundRaised int `json:"round_raised"`
	// RoundClosed is the round in which the finding was closed, if applicable.
	RoundClosed *int `json:"round_closed,omitempty"`
	// ResolutionNotes provides optional notes on how the finding was resolved.
	ResolutionNotes *string `json:"resolution_notes,omitempty"`
	// DismissalRationale provides the reason if the finding was dismissed.
	DismissalRationale *string `json:"dismissal_rationale,omitempty"`
}

// DedupEntry records a single deduplication merge in the merged-findings log.
type DedupEntry struct {
	// KeptID is the finding ID that was kept.
	KeptID string `json:"kept_id"`
	// MergedID is the finding ID that was merged into KeptID.
	MergedID string `json:"merged_id"`
	// Reason explains why the two were considered duplicates.
	Reason string `json:"reason"`
}

// ---------------------------------------------------------------------------
// Agent output types
// ---------------------------------------------------------------------------

// DiscoveryOutput is the structured output schema for the discovery agent.
type DiscoveryOutput struct {
	// SchemaVersion identifies the output schema version.
	SchemaVersion string `json:"schema_version"`
	// Agent is the name of the agent that produced this output.
	Agent string `json:"agent"`
	// Actors lists the actors discovered during analysis.
	Actors []Actor `json:"actors"`
	// ProblemStatement is the synthesised problem statement.
	ProblemStatement string `json:"problem_statement"`
	// Scope defines what is in and out of scope.
	Scope Scope `json:"scope"`
	// Constraints lists constraints identified during discovery.
	Constraints []string `json:"constraints"`
	// IntegrationPoints lists external system integration points.
	IntegrationPoints []IntegrationPoint `json:"integration_points"`
	// Priorities lists prioritised items.
	Priorities []Priority `json:"priorities"`
	// Assumptions lists assumptions made during discovery.
	Assumptions []Assumption `json:"assumptions"`
	// OpenQuestions lists unresolved questions for the user.
	OpenQuestions []string `json:"open_questions"`
}

// DrafterOutput is the structured output schema for the drafter agent.
type DrafterOutput struct {
	// SchemaVersion identifies the output schema version.
	SchemaVersion string `json:"schema_version"`
	// Agent is the name of the agent that produced this output.
	Agent string `json:"agent"`
	// SpecFile is the path to the generated specification file.
	SpecFile string `json:"spec_file"`
	// HoldoutFile is the path to the holdout test-data file.
	HoldoutFile string `json:"holdout_file"`
	// AmbiguityWarnings lists ambiguities detected during drafting.
	AmbiguityWarnings []AmbiguityWarning `json:"ambiguity_warnings"`
	// StructuralSummary provides aggregate counts of spec elements.
	StructuralSummary StructuralSummary `json:"structural_summary"`
}

// ReviewerOutput is the structured output schema for the reviewer agent.
type ReviewerOutput struct {
	// SchemaVersion identifies the output schema version.
	SchemaVersion string `json:"schema_version"`
	// Agent is the name of the agent that produced this output.
	Agent string `json:"agent"`
	// Round is the review iteration number.
	Round int `json:"round"`
	// LensesApplied lists the review lenses that were applied.
	LensesApplied []string `json:"lenses_applied"`
	// Findings lists all issues found during review.
	Findings []Finding `json:"findings"`
	// StructuralIntegrity reports structural integrity check results.
	StructuralIntegrity StructuralIntegrity `json:"structural_integrity"`
	// MarkdownReportFile is the path to the full markdown report.
	MarkdownReportFile string `json:"markdown_report_file"`
}

// RevisionOutput is the structured output schema for the revision agent.
type RevisionOutput struct {
	// SchemaVersion identifies the output schema version.
	SchemaVersion string `json:"schema_version"`
	// Agent is the name of the agent that produced this output.
	Agent string `json:"agent"`
	// Round is the revision iteration number.
	Round int `json:"round"`
	// RevisedSpecFile is the path to the revised specification file.
	RevisedSpecFile string `json:"revised_spec_file"`
	// Changes lists the changes made to address findings.
	Changes []Change `json:"changes"`
	// DismissalRequests lists requests to dismiss findings.
	DismissalRequests []DismissalRequest `json:"dismissal_requests"`
}

// JudgeOutput is the structured output schema for the judge agent.
type JudgeOutput struct {
	// SchemaVersion identifies the output schema version.
	SchemaVersion string `json:"schema_version"`
	// Agent is the name of the agent that produced this output.
	Agent string `json:"agent"`
	// Round is the judging iteration number.
	Round int `json:"round"`
	// Verdict is the judge's decision.
	Verdict Verdict `json:"verdict"`
	// Rationale explains the verdict.
	Rationale string `json:"rationale"`
	// IssueUpdates lists status changes for findings.
	IssueUpdates []IssueUpdate `json:"issue_updates"`
	// Downgrades lists severity downgrades applied by the judge.
	Downgrades []Downgrade `json:"downgrades"`
	// StructuralDelta reports regressions introduced during revision.
	StructuralDelta StructuralDelta `json:"structural_delta"`
}

// MergedFindings is the deduplicated findings ledger maintained across
// review rounds.
type MergedFindings struct {
	// SchemaVersion identifies the output schema version.
	SchemaVersion string `json:"schema_version"`
	// Round is the review round this ledger reflects.
	Round int `json:"round"`
	// Timestamp is the ISO 8601 timestamp when the ledger was produced.
	Timestamp string `json:"timestamp"`
	// TotalFindings is the total number of findings before deduplication.
	TotalFindings int `json:"total_findings"`
	// TotalAfterDedup is the number of findings after deduplication.
	TotalAfterDedup int `json:"total_after_dedup"`
	// DuplicatesMerged is the number of duplicates that were merged.
	DuplicatesMerged int `json:"duplicates_merged"`
	// FindingsRejected is the number of findings rejected during validation.
	FindingsRejected int `json:"findings_rejected"`
	// Findings is the deduplicated list of merged findings.
	Findings []MergedFinding `json:"findings"`
	// DedupLog records each deduplication merge.
	DedupLog []DedupEntry `json:"dedup_log"`
}

// ---------------------------------------------------------------------------
// Validation helpers
// ---------------------------------------------------------------------------

var (
	actorTypes         = map[string]bool{"human": true, "system": true, "external": true}
	integrationDirs    = map[string]bool{"inbound": true, "outbound": true, "bidirectional": true}
	priorityLevels     = map[string]bool{"P0": true, "P1": true, "P2": true, "P3": true, "P4": true}
	confidenceLevels   = map[string]bool{"high": true, "medium": true, "low": true}
	findingTargets     = map[string]bool{"spec": true, "holdout": true}
	ambiguityIDPattern = regexp.MustCompile(`^AMB-W-\d{3}$`)
	changeActions      = map[string]bool{"revised": true, "dismissed": true}
	issueStatuses      = map[string]bool{"verified": true, "reopened": true, "dismissed": true}
	integrityResults   = map[string]bool{"PASS": true, "FAIL": true}
	downgradeReasons   = map[string]bool{"DUPLICATE_OF": true, "OUT_OF_SCOPE": true, "CONTRADICTED_BY_REQUIREMENT": true, "REVIEWER_ERROR": true}
)

func requireNonEmpty(errs *[]error, field, value string) {
	if strings.TrimSpace(value) == "" {
		*errs = append(*errs, fmt.Errorf("%s must not be empty", field))
	}
}

func requirePositive(errs *[]error, field string, value int) {
	if value < 1 {
		*errs = append(*errs, fmt.Errorf("%s must be >= 1, got %d", field, value))
	}
}

func normalizeFindingTarget(target string) string {
	target = strings.ToLower(strings.TrimSpace(target))
	if target == "" {
		return "spec"
	}
	return target
}

// ---------------------------------------------------------------------------
// Validation functions
// ---------------------------------------------------------------------------

// ValidateDiscoveryOutput validates a DiscoveryOutput and returns all errors
// found. An empty slice means the output is valid.
func ValidateDiscoveryOutput(o *DiscoveryOutput) []error {
	var errs []error
	requireNonEmpty(&errs, "schema_version", o.SchemaVersion)
	requireNonEmpty(&errs, "agent", o.Agent)
	requireNonEmpty(&errs, "problem_statement", o.ProblemStatement)

	if len(o.Actors) == 0 {
		errs = append(errs, fmt.Errorf("actors must not be empty"))
	}
	for i, a := range o.Actors {
		requireNonEmpty(&errs, fmt.Sprintf("actors[%d].name", i), a.Name)
		if !actorTypes[a.Type] {
			errs = append(errs, fmt.Errorf("actors[%d].type must be human|system|external, got %q", i, a.Type))
		}
		requireNonEmpty(&errs, fmt.Sprintf("actors[%d].description", i), a.Description)
	}

	if len(o.Scope.InScope) == 0 {
		errs = append(errs, fmt.Errorf("scope.in_scope must not be empty"))
	}

	for i, ip := range o.IntegrationPoints {
		requireNonEmpty(&errs, fmt.Sprintf("integration_points[%d].system", i), ip.System)
		if !integrationDirs[ip.Direction] {
			errs = append(errs, fmt.Errorf("integration_points[%d].direction must be inbound|outbound|bidirectional, got %q", i, ip.Direction))
		}
	}

	for i, p := range o.Priorities {
		requireNonEmpty(&errs, fmt.Sprintf("priorities[%d].item", i), p.Item)
		if !priorityLevels[p.Priority] {
			errs = append(errs, fmt.Errorf("priorities[%d].priority must be P0-P4, got %q", i, p.Priority))
		}
	}

	for i, a := range o.Assumptions {
		requireNonEmpty(&errs, fmt.Sprintf("assumptions[%d].assumption", i), a.Assumption)
		if !confidenceLevels[a.Confidence] {
			errs = append(errs, fmt.Errorf("assumptions[%d].confidence must be high|medium|low, got %q", i, a.Confidence))
		}
	}

	return errs
}

// ValidateDrafterOutput validates a DrafterOutput and returns all errors found.
// An empty slice means the output is valid.
func ValidateDrafterOutput(o *DrafterOutput) []error {
	var errs []error
	requireNonEmpty(&errs, "schema_version", o.SchemaVersion)
	requireNonEmpty(&errs, "agent", o.Agent)
	requireNonEmpty(&errs, "spec_file", o.SpecFile)
	requireNonEmpty(&errs, "holdout_file", o.HoldoutFile)

	for i, w := range o.AmbiguityWarnings {
		if !ambiguityIDPattern.MatchString(w.ID) {
			errs = append(errs, fmt.Errorf("ambiguity_warnings[%d].id must match AMB-W-NNN, got %q", i, w.ID))
		}
		requireNonEmpty(&errs, fmt.Sprintf("ambiguity_warnings[%d].section", i), w.Section)
		requireNonEmpty(&errs, fmt.Sprintf("ambiguity_warnings[%d].ambiguity", i), w.Ambiguity)
		requireNonEmpty(&errs, fmt.Sprintf("ambiguity_warnings[%d].agent_assumption", i), w.AgentAssumption)
		requireNonEmpty(&errs, fmt.Sprintf("ambiguity_warnings[%d].question_for_user", i), w.QuestionForUser)
	}

	if o.StructuralSummary.UserStoryCount < 0 {
		errs = append(errs, fmt.Errorf("structural_summary.user_story_count must be >= 0"))
	}
	if o.StructuralSummary.BDDScenarioCount < 0 {
		errs = append(errs, fmt.Errorf("structural_summary.bdd_scenario_count must be >= 0"))
	}
	if o.StructuralSummary.FRCount < 0 {
		errs = append(errs, fmt.Errorf("structural_summary.fr_count must be >= 0"))
	}
	if o.StructuralSummary.TestCount < 0 {
		errs = append(errs, fmt.Errorf("structural_summary.test_count must be >= 0"))
	}

	return errs
}

// ValidateReviewerOutput validates a ReviewerOutput, rejecting findings that
// are missing a recommendation and normalising severity values to uppercase.
// It returns the valid findings, the count of rejected findings, and all
// validation errors found. An empty error slice means the output envelope is
// valid (individual findings may still have been rejected).
func ValidateReviewerOutput(o *ReviewerOutput) (validFindings []Finding, rejectedCount int, errors []error) {
	var errs []error
	requireNonEmpty(&errs, "schema_version", o.SchemaVersion)
	requireNonEmpty(&errs, "agent", o.Agent)
	requirePositive(&errs, "round", o.Round)
	requireNonEmpty(&errs, "markdown_report_file", o.MarkdownReportFile)

	if len(o.LensesApplied) == 0 {
		errs = append(errs, fmt.Errorf("lenses_applied must not be empty"))
	}

	for i, c := range o.StructuralIntegrity.Checks {
		requireNonEmpty(&errs, fmt.Sprintf("structural_integrity.checks[%d].check", i), c.Check)
		if !integrityResults[c.Result] {
			errs = append(errs, fmt.Errorf("structural_integrity.checks[%d].result must be PASS|FAIL, got %q", i, c.Result))
		}
	}

	var valid []Finding
	rejected := 0

	for i, f := range o.Findings {
		// Reject findings missing recommendation.
		if strings.TrimSpace(f.Recommendation) == "" {
			rejected++
			errs = append(errs, fmt.Errorf("findings[%d] (id=%q): rejected — missing recommendation", i, f.ID))
			continue
		}

		requireNonEmpty(&errs, fmt.Sprintf("findings[%d].id", i), f.ID)
		requireNonEmpty(&errs, fmt.Sprintf("findings[%d].description", i), f.Description)
		requireNonEmpty(&errs, fmt.Sprintf("findings[%d].impact", i), f.Impact)
		requireNonEmpty(&errs, fmt.Sprintf("findings[%d].lens", i), f.Lens)
		requireNonEmpty(&errs, fmt.Sprintf("findings[%d].affected_section", i), f.AffectedSection)
		f.Target = normalizeFindingTarget(f.Target)
		if !findingTargets[f.Target] {
			rejected++
			errs = append(errs, fmt.Errorf("findings[%d] (id=%q): rejected — target must be spec|holdout, got %q", i, f.ID, f.Target))
			continue
		}

		// Severity is already normalised to uppercase by the Severity
		// UnmarshalJSON. No extra work needed here — the type guarantees it.
		valid = append(valid, f)
	}

	return valid, rejected, errs
}

// ValidateRevisionOutput validates a RevisionOutput and returns all errors
// found. An empty slice means the output is valid.
func ValidateRevisionOutput(o *RevisionOutput) []error {
	var errs []error
	requireNonEmpty(&errs, "schema_version", o.SchemaVersion)
	requireNonEmpty(&errs, "agent", o.Agent)
	requirePositive(&errs, "round", o.Round)
	requireNonEmpty(&errs, "revised_spec_file", o.RevisedSpecFile)

	for i, c := range o.Changes {
		requireNonEmpty(&errs, fmt.Sprintf("changes[%d].finding_id", i), c.FindingID)
		if !changeActions[c.Action] {
			errs = append(errs, fmt.Errorf("changes[%d].action must be revised|dismissed, got %q", i, c.Action))
		}
		requireNonEmpty(&errs, fmt.Sprintf("changes[%d].description", i), c.Description)
		if len(c.SectionsModified) == 0 {
			errs = append(errs, fmt.Errorf("changes[%d].sections_modified must not be empty", i))
		}
	}

	for i, d := range o.DismissalRequests {
		requireNonEmpty(&errs, fmt.Sprintf("dismissal_requests[%d].finding_id", i), d.FindingID)
		requireNonEmpty(&errs, fmt.Sprintf("dismissal_requests[%d].rationale", i), d.Rationale)
	}

	return errs
}

// ValidateJudgeOutput validates a JudgeOutput and returns all errors found.
// An empty slice means the output is valid.
func ValidateJudgeOutput(o *JudgeOutput) []error {
	var errs []error
	requireNonEmpty(&errs, "schema_version", o.SchemaVersion)
	requireNonEmpty(&errs, "agent", o.Agent)
	requirePositive(&errs, "round", o.Round)
	requireNonEmpty(&errs, "rationale", o.Rationale)

	for i, u := range o.IssueUpdates {
		requireNonEmpty(&errs, fmt.Sprintf("issue_updates[%d].finding_id", i), u.FindingID)
		if !issueStatuses[u.NewStatus] {
			errs = append(errs, fmt.Errorf("issue_updates[%d].new_status must be verified|reopened|dismissed, got %q", i, u.NewStatus))
		}
		requireNonEmpty(&errs, fmt.Sprintf("issue_updates[%d].explanation", i), u.Explanation)
	}

	for i, d := range o.Downgrades {
		requireNonEmpty(&errs, fmt.Sprintf("downgrades[%d].finding_id", i), d.FindingID)
		if !downgradeReasons[d.ReasonCode] {
			errs = append(errs, fmt.Errorf("downgrades[%d].reason_code must be DUPLICATE_OF|OUT_OF_SCOPE|CONTRADICTED_BY_REQUIREMENT|REVIEWER_ERROR, got %q", i, d.ReasonCode))
		}
		requireNonEmpty(&errs, fmt.Sprintf("downgrades[%d].reason_detail", i), d.ReasonDetail)
	}

	return errs
}

// ValidateMergedFindings validates a MergedFindings ledger and returns all
// errors found. An empty slice means the output is valid.
func ValidateMergedFindings(o *MergedFindings) []error {
	var errs []error
	requirePositive(&errs, "round", o.Round)
	requireNonEmpty(&errs, "timestamp", o.Timestamp)
	if o.Findings == nil {
		errs = append(errs, fmt.Errorf("findings must not be nil"))
	}
	return errs
}
