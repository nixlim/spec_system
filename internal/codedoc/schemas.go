package codedoc

import (
	"fmt"
	"strings"
)

// ---------------------------------------------------------------------------
// Valid enum values
// ---------------------------------------------------------------------------

// validSeverities is the set of accepted severity values for codedoc findings.
var validSeverities = map[string]bool{
	SeverityCritical:    true,
	SeverityMajor:       true,
	SeverityMinor:       true,
	SeverityObservation: true,
}

// validCompletionStatuses is the set of accepted completion_status.status values.
var validCompletionStatuses = map[string]bool{
	"complete": true,
	"partial":  true,
}

// validFindingStatuses is the set of accepted finding status values.
var validFindingStatuses = map[string]bool{
	"open":     true,
	"resolved": true,
	"wontfix":  true,
}

// validVerdicts is the set of accepted judge verdicts.
var validVerdicts = map[string]bool{
	"PASS":   true,
	"REVISE": true,
	"BLOCK":  true,
}

// ---------------------------------------------------------------------------
// Reviewer Output types (Section 11)
// ---------------------------------------------------------------------------

// ReviewerOutput is the structured output produced by a codedoc reviewer agent.
type ReviewerOutput struct {
	SchemaVersion       string               `json:"schema_version"`
	Agent               string               `json:"agent"`
	Round               int                  `json:"round"`
	LensesApplied       []string             `json:"lenses_applied"`
	Findings            []ReviewFinding      `json:"findings"`
	StructuralIntegrity StructuralIntegrity  `json:"structural_integrity"`
}

// ReviewFinding represents a single finding from a codedoc reviewer.
type ReviewFinding struct {
	ID              string `json:"id"`
	Description     string `json:"description"`
	Severity        string `json:"severity"`
	Status          string `json:"status"`
	Impact          string `json:"impact"`
	Recommendation  string `json:"recommendation"`
	Lens            string `json:"lens"`
	AffectedSection string `json:"affected_section"`
	AffectedFile    string `json:"affected_file"`
}

// StructuralIntegrity holds the results of structural checks performed
// by a reviewer (e.g., Mermaid syntax, cross-references, audit JSON schema).
type StructuralIntegrity struct {
	Performed bool                     `json:"performed"`
	Checks    []StructuralCheck        `json:"checks"`
}

// StructuralCheck is a single structural integrity check result.
type StructuralCheck struct {
	Name    string `json:"name"`
	Passed  bool   `json:"passed"`
	Details string `json:"details"`
}

// ---------------------------------------------------------------------------
// Judge Output types
// ---------------------------------------------------------------------------

// JudgeOutput is the structured output produced by the codedoc judge agent.
type JudgeOutput struct {
	SchemaVersion string          `json:"schema_version"`
	Agent         string          `json:"agent"`
	Round         int             `json:"round"`
	Verdict       string          `json:"verdict"`
	Rationale     string          `json:"rationale"`
	IssueUpdates  []IssueUpdate   `json:"issue_updates"`
	Downgrades    []Downgrade     `json:"downgrades"`
}

// IssueUpdate records a status change the judge applies to a finding.
type IssueUpdate struct {
	FindingID string `json:"finding_id"`
	NewStatus string `json:"new_status"`
	Reason    string `json:"reason"`
}

// Downgrade records a severity downgrade the judge applies to a finding.
type Downgrade struct {
	FindingID   string `json:"finding_id"`
	OldSeverity string `json:"old_severity"`
	NewSeverity string `json:"new_severity"`
	ReasonCode  string `json:"reason_code"`
}

// ---------------------------------------------------------------------------
// Validation helpers
// ---------------------------------------------------------------------------

func requireNonEmpty(errs *[]error, field, value string) {
	if strings.TrimSpace(value) == "" {
		*errs = append(*errs, fmt.Errorf("%s must not be empty", field))
	}
}

func requirePositive(errs *[]error, field string, value int) {
	if value <= 0 {
		*errs = append(*errs, fmt.Errorf("%s must be > 0, got %d", field, value))
	}
}

// ---------------------------------------------------------------------------
// ValidateDiscoveryOutput
// ---------------------------------------------------------------------------

// ValidateDiscoveryOutput checks a DiscoveryOutput for required fields and
// valid enum values. Returns all errors found; an empty slice means valid.
func ValidateDiscoveryOutput(o *DiscoveryOutput) []error {
	var errs []error

	requireNonEmpty(&errs, "schema_version", o.SchemaVersion)
	requireNonEmpty(&errs, "agent", o.Agent)
	requireNonEmpty(&errs, "mode", o.Mode)

	if o.Mode != "" && o.Mode != "full" && o.Mode != "incremental" {
		errs = append(errs, fmt.Errorf("mode must be \"full\" or \"incremental\", got %q", o.Mode))
	}

	if !validCompletionStatuses[o.CompletionStatus.Status] {
		errs = append(errs, fmt.Errorf("completion_status.status must be \"complete\" or \"partial\", got %q", o.CompletionStatus.Status))
	}

	for i, m := range o.Modules {
		requireNonEmpty(&errs, fmt.Sprintf("modules[%d].path", i), m.Path)
	}

	for i, ep := range o.EntryPoints {
		requireNonEmpty(&errs, fmt.Sprintf("entry_points[%d].path", i), ep.Path)
	}

	return errs
}

// ---------------------------------------------------------------------------
// ValidateDrafterOutput
// ---------------------------------------------------------------------------

// ValidateDrafterOutput checks a DrafterOutput for required fields and
// valid enum values. Returns all errors found; an empty slice means valid.
func ValidateDrafterOutput(o *DrafterOutput) []error {
	var errs []error

	requireNonEmpty(&errs, "schema_version", o.SchemaVersion)
	requireNonEmpty(&errs, "agent", o.Agent)

	if strings.TrimSpace(o.AsImplementedReport.FilePath) == "" {
		errs = append(errs, fmt.Errorf("as_implemented_report.file_path must not be empty"))
	}

	for i, d := range o.ArchitectureDiagrams {
		requireNonEmpty(&errs, fmt.Sprintf("architecture_diagrams[%d].file_path", i), d.FilePath)
		requireNonEmpty(&errs, fmt.Sprintf("architecture_diagrams[%d].diagram_type", i), d.DiagramType)
		requireNonEmpty(&errs, fmt.Sprintf("architecture_diagrams[%d].mermaid_content", i), d.MermaidContent)
	}

	for i, f := range o.CodeAudit.Findings {
		requireNonEmpty(&errs, fmt.Sprintf("code_audit.findings[%d].id", i), f.ID)
		requireNonEmpty(&errs, fmt.Sprintf("code_audit.findings[%d].type", i), f.Type)
		if !validSeverities[f.Severity] {
			errs = append(errs, fmt.Errorf("code_audit.findings[%d].severity must be a valid severity, got %q", i, f.Severity))
		}
	}

	return errs
}

// ---------------------------------------------------------------------------
// ValidateReviewerOutput
// ---------------------------------------------------------------------------

// ValidateReviewerOutput checks a codedoc ReviewerOutput for required fields
// and valid enum values. It separates valid findings from rejected ones
// (findings with empty severity or recommendation are rejected).
// Returns valid findings, the count of rejected findings, and all errors.
func ValidateReviewerOutput(o *ReviewerOutput) (validFindings []ReviewFinding, rejectedCount int, errors []error) {
	var errs []error

	requireNonEmpty(&errs, "schema_version", o.SchemaVersion)
	requireNonEmpty(&errs, "agent", o.Agent)
	requirePositive(&errs, "round", o.Round)

	if len(o.LensesApplied) == 0 {
		errs = append(errs, fmt.Errorf("lenses_applied must not be empty"))
	}

	var valid []ReviewFinding
	rejected := 0

	for i, f := range o.Findings {
		// Reject findings with empty severity.
		if strings.TrimSpace(f.Severity) == "" {
			rejected++
			errs = append(errs, fmt.Errorf("findings[%d] (id=%q): rejected — empty severity", i, f.ID))
			continue
		}

		// Reject findings with invalid severity.
		if !validSeverities[strings.ToLower(strings.TrimSpace(f.Severity))] {
			rejected++
			errs = append(errs, fmt.Errorf("findings[%d] (id=%q): rejected — invalid severity %q", i, f.ID, f.Severity))
			continue
		}

		// Reject findings missing recommendation.
		if strings.TrimSpace(f.Recommendation) == "" {
			rejected++
			errs = append(errs, fmt.Errorf("findings[%d] (id=%q): rejected — missing recommendation", i, f.ID))
			continue
		}

		requireNonEmpty(&errs, fmt.Sprintf("findings[%d].id", i), f.ID)
		requireNonEmpty(&errs, fmt.Sprintf("findings[%d].description", i), f.Description)
		requireNonEmpty(&errs, fmt.Sprintf("findings[%d].lens", i), f.Lens)
		requireNonEmpty(&errs, fmt.Sprintf("findings[%d].affected_section", i), f.AffectedSection)

		valid = append(valid, f)
	}

	return valid, rejected, errs
}

// ---------------------------------------------------------------------------
// ValidateJudgeOutput
// ---------------------------------------------------------------------------

// ValidateJudgeOutput checks a JudgeOutput for required fields and valid
// enum values. Returns all errors found; an empty slice means valid.
func ValidateJudgeOutput(o *JudgeOutput) []error {
	var errs []error

	requireNonEmpty(&errs, "schema_version", o.SchemaVersion)
	requireNonEmpty(&errs, "agent", o.Agent)
	requirePositive(&errs, "round", o.Round)

	if !validVerdicts[strings.ToUpper(strings.TrimSpace(o.Verdict))] {
		errs = append(errs, fmt.Errorf("verdict must be PASS, REVISE, or BLOCK, got %q", o.Verdict))
	}

	requireNonEmpty(&errs, "rationale", o.Rationale)

	return errs
}
