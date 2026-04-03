// Package codereview defines the core types for the automated code review
// workflow, including workflow states, fix output schemas, convergence
// verdicts, and grill-code mode selection.
package codereview

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ---------------------------------------------------------------------------
// CodeReviewState
// ---------------------------------------------------------------------------

// CodeReviewState represents the current phase of the code review workflow.
type CodeReviewState int

const (
	// CRInit is the initial state before any work has begun.
	CRInit CodeReviewState = iota
	// CRHumanGateScope is the scope confirmation gate before review begins.
	CRHumanGateScope
	// CRReviewing is the parallel code review phase (grill-code across lenses).
	CRReviewing
	// CRFixing is the automated fix agent phase.
	CRFixing
	// CRHumanGateFixes is the human review gate after fixes are applied.
	CRHumanGateFixes
	// CRComplete is the terminal state when the review passes.
	CRComplete
	// CREscalated is the terminal state when the workflow requires human intervention.
	CREscalated
)

// codeReviewStateNames maps each CodeReviewState to its canonical string form.
var codeReviewStateNames = [...]string{
	CRInit:           "CR_INIT",
	CRHumanGateScope: "CR_HUMAN_GATE_SCOPE",
	CRReviewing:      "CR_REVIEWING",
	CRFixing:         "CR_FIXING",
	CRHumanGateFixes: "CR_HUMAN_GATE_FIXES",
	CRComplete:       "CR_COMPLETE",
	CREscalated:      "CR_ESCALATED",
}

// codeReviewStateLookup maps canonical uppercase names back to CodeReviewState values.
var codeReviewStateLookup map[string]CodeReviewState

func init() {
	codeReviewStateLookup = make(map[string]CodeReviewState, len(codeReviewStateNames))
	for i, name := range codeReviewStateNames {
		codeReviewStateLookup[name] = CodeReviewState(i)
	}
}

// String returns the canonical string representation of a CodeReviewState.
func (s CodeReviewState) String() string {
	if int(s) >= 0 && int(s) < len(codeReviewStateNames) {
		return codeReviewStateNames[s]
	}
	return fmt.Sprintf("CodeReviewState(%d)", int(s))
}

// MarshalJSON serializes a CodeReviewState to its JSON string representation.
func (s CodeReviewState) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.String())
}

// UnmarshalJSON deserializes a CodeReviewState from a JSON string (case-insensitive).
func (s *CodeReviewState) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("CodeReviewState must be a string: %w", err)
	}
	v, ok := codeReviewStateLookup[strings.ToUpper(raw)]
	if !ok {
		return fmt.Errorf("unknown CodeReviewState: %q", raw)
	}
	*s = v
	return nil
}

// IsTerminal returns true for terminal states (CRComplete, CREscalated).
func (s CodeReviewState) IsTerminal() bool {
	return s == CRComplete || s == CREscalated
}

// IsGate returns true for human gate states (CRHumanGateScope, CRHumanGateFixes).
func (s CodeReviewState) IsGate() bool {
	return s == CRHumanGateScope || s == CRHumanGateFixes
}

// ParseCodeReviewState parses a string into a CodeReviewState (case-insensitive).
func ParseCodeReviewState(s string) (CodeReviewState, error) {
	v, ok := codeReviewStateLookup[strings.ToUpper(s)]
	if !ok {
		return CRInit, fmt.Errorf("unknown CodeReviewState: %q", s)
	}
	return v, nil
}

// ---------------------------------------------------------------------------
// GrillCodeMode
// ---------------------------------------------------------------------------

// GrillCodeMode determines what context is provided to the grill-code skill.
type GrillCodeMode int

const (
	// GrillCodeModeCodeOnly means only code is reviewed (no spec or task list).
	GrillCodeModeCodeOnly GrillCodeMode = iota
	// GrillCodeModeSpecOnly means code is reviewed against a spec (no task list).
	GrillCodeModeSpecOnly
	// GrillCodeModeFullContext means code is reviewed against both spec and task list.
	GrillCodeModeFullContext
)

// grillCodeModeNames maps each GrillCodeMode to its canonical string form.
var grillCodeModeNames = [...]string{
	GrillCodeModeCodeOnly:    "code-only",
	GrillCodeModeSpecOnly:    "spec-only",
	GrillCodeModeFullContext: "full-context",
}

// grillCodeModeLookup maps lowercase names back to GrillCodeMode values.
var grillCodeModeLookup map[string]GrillCodeMode

func init() {
	grillCodeModeLookup = make(map[string]GrillCodeMode, len(grillCodeModeNames))
	for i, name := range grillCodeModeNames {
		grillCodeModeLookup[name] = GrillCodeMode(i)
	}
}

// String returns the canonical string representation of a GrillCodeMode.
func (m GrillCodeMode) String() string {
	if int(m) >= 0 && int(m) < len(grillCodeModeNames) {
		return grillCodeModeNames[m]
	}
	return fmt.Sprintf("GrillCodeMode(%d)", int(m))
}

// MarshalJSON serializes a GrillCodeMode to its JSON string representation.
func (m GrillCodeMode) MarshalJSON() ([]byte, error) {
	return json.Marshal(m.String())
}

// UnmarshalJSON deserializes a GrillCodeMode from a JSON string (case-insensitive).
func (m *GrillCodeMode) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("GrillCodeMode must be a string: %w", err)
	}
	v, ok := grillCodeModeLookup[strings.ToLower(raw)]
	if !ok {
		return fmt.Errorf("unknown GrillCodeMode: %q", raw)
	}
	*m = v
	return nil
}

// ParseGrillCodeMode parses a string into a GrillCodeMode (case-insensitive).
func ParseGrillCodeMode(s string) (GrillCodeMode, error) {
	v, ok := grillCodeModeLookup[strings.ToLower(s)]
	if !ok {
		return GrillCodeModeCodeOnly, fmt.Errorf("unknown GrillCodeMode: %q", s)
	}
	return v, nil
}

// DetermineGrillCodeMode selects the appropriate GrillCodeMode based on
// whether a spec path and/or task list path are provided.
func DetermineGrillCodeMode(specPath, taskListPath string) GrillCodeMode {
	if specPath == "" {
		return GrillCodeModeCodeOnly
	}
	if taskListPath != "" {
		return GrillCodeModeFullContext
	}
	return GrillCodeModeSpecOnly
}

// ---------------------------------------------------------------------------
// CodeReviewVerdict
// ---------------------------------------------------------------------------

// CodeReviewVerdict represents the convergence decision after a review round.
type CodeReviewVerdict int

const (
	// CodeReviewVerdictPass means no findings at all — review is complete.
	CodeReviewVerdictPass CodeReviewVerdict = iota
	// CodeReviewVerdictPassWithObservations means only MINOR/OBSERVATION
	// findings remain — human decides if acceptable.
	CodeReviewVerdictPassWithObservations
	// CodeReviewVerdictRevise means CRITICAL or MAJOR findings exist —
	// fix agent should address them.
	CodeReviewVerdictRevise
)

// codeReviewVerdictNames maps each CodeReviewVerdict to its canonical string form.
var codeReviewVerdictNames = [...]string{
	CodeReviewVerdictPass:                "PASS",
	CodeReviewVerdictPassWithObservations: "PASS_WITH_OBSERVATIONS",
	CodeReviewVerdictRevise:              "REVISE",
}

// codeReviewVerdictLookup maps uppercase names back to CodeReviewVerdict values.
var codeReviewVerdictLookup map[string]CodeReviewVerdict

func init() {
	codeReviewVerdictLookup = make(map[string]CodeReviewVerdict, len(codeReviewVerdictNames))
	for i, name := range codeReviewVerdictNames {
		codeReviewVerdictLookup[name] = CodeReviewVerdict(i)
	}
}

// String returns the canonical string representation of a CodeReviewVerdict.
func (v CodeReviewVerdict) String() string {
	if int(v) >= 0 && int(v) < len(codeReviewVerdictNames) {
		return codeReviewVerdictNames[v]
	}
	return fmt.Sprintf("CodeReviewVerdict(%d)", int(v))
}

// MarshalJSON serializes a CodeReviewVerdict to its JSON string representation.
func (v CodeReviewVerdict) MarshalJSON() ([]byte, error) {
	return json.Marshal(v.String())
}

// UnmarshalJSON deserializes a CodeReviewVerdict from a JSON string (case-insensitive).
func (v *CodeReviewVerdict) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("CodeReviewVerdict must be a string: %w", err)
	}
	val, ok := codeReviewVerdictLookup[strings.ToUpper(raw)]
	if !ok {
		return fmt.Errorf("unknown CodeReviewVerdict: %q", raw)
	}
	*v = val
	return nil
}

// ParseCodeReviewVerdict parses a string into a CodeReviewVerdict (case-insensitive).
func ParseCodeReviewVerdict(s string) (CodeReviewVerdict, error) {
	v, ok := codeReviewVerdictLookup[strings.ToUpper(s)]
	if !ok {
		return CodeReviewVerdictPass, fmt.Errorf("unknown CodeReviewVerdict: %q", s)
	}
	return v, nil
}

// ---------------------------------------------------------------------------
// FixOutput and related types
// ---------------------------------------------------------------------------

// FixOutput is the structured output produced by the fix agent after
// addressing findings from a review round.
type FixOutput struct {
	// Round is the fix round number (1-indexed, matches review round).
	Round int `json:"round"`

	// FixesApplied is the list of fix actions taken, one per finding addressed.
	FixesApplied []FixAction `json:"fixes_applied"`

	// TestResults contains structured test execution results.
	// Nil if the fix agent did not run tests.
	TestResults *TestResults `json:"test_results"`

	// GitDiffStat is the output of `git diff --stat` after all fixes.
	GitDiffStat string `json:"git_diff_stat"`
}

// FixAction describes a single fix action taken against a finding.
type FixAction struct {
	// FindingID references the merged finding ID (e.g., "CRIT-001").
	FindingID string `json:"finding_id"`

	// Status is the outcome of the fix attempt.
	Status FixStatus `json:"status"`

	// FilesModified is the list of file paths changed to address this finding.
	FilesModified []string `json:"files_modified"`

	// Description explains what was changed and why.
	Description string `json:"description"`
}

// FixStatus represents the outcome of a fix attempt.
type FixStatus string

const (
	// FixStatusFixed means the finding was successfully addressed.
	FixStatusFixed FixStatus = "fixed"
	// FixStatusDeferred means the fix was intentionally deferred.
	FixStatusDeferred FixStatus = "deferred"
	// FixStatusFailed means the fix attempt failed.
	FixStatusFailed FixStatus = "failed"
)

// ValidFixStatuses is the set of valid FixStatus values.
var ValidFixStatuses = map[FixStatus]bool{
	FixStatusFixed:    true,
	FixStatusDeferred: true,
	FixStatusFailed:   true,
}

// TestResults contains structured test execution results from the fix agent.
type TestResults struct {
	// Total is the number of tests executed.
	Total int `json:"total"`

	// Passed is the number of tests that passed.
	Passed int `json:"passed"`

	// Failed is the number of tests that failed.
	Failed int `json:"failed"`

	// Failures lists the names of failed tests for human review.
	Failures []string `json:"failures,omitempty"`
}

// ---------------------------------------------------------------------------
// CodeReviewStateJSON — serializable workflow snapshot
// ---------------------------------------------------------------------------

// CodeReviewFindingsSummary holds aggregate counts of findings by severity.
type CodeReviewFindingsSummary struct {
	// OpenCritical is the number of open critical-severity findings.
	OpenCritical int `json:"open_critical"`
	// OpenMajor is the number of open major-severity findings.
	OpenMajor int `json:"open_major"`
	// OpenMinor is the number of open minor-severity findings.
	OpenMinor int `json:"open_minor"`
	// OpenObservation is the number of open observation-severity findings.
	OpenObservation int `json:"open_observation"`
	// Fixed is the number of findings that have been fixed.
	Fixed int `json:"fixed"`
	// Deferred is the number of findings that were deferred.
	Deferred int `json:"deferred"`
	// Failed is the number of findings where the fix failed.
	Failed int `json:"failed"`
}

// CodeReviewStateJSON is the complete serializable snapshot of a code review
// workflow's current state. All time fields use ISO 8601 string representation.
type CodeReviewStateJSON struct {
	// State is the current workflow phase.
	State CodeReviewState `json:"state"`
	// Round is the current review round (1-indexed).
	Round int `json:"round"`
	// FeatureName is the human-readable name for this code review.
	FeatureName string `json:"feature_name"`
	// CodePath is the filesystem path to the target repository.
	CodePath string `json:"code_path"`
	// SpecPath is the optional path to the spec file (empty if code-only).
	SpecPath string `json:"spec_path,omitempty"`
	// TaskListPath is the optional path to the task list file.
	TaskListPath string `json:"task_list_path,omitempty"`
	// GrillCodeMode is the detected review context mode.
	GrillCodeMode GrillCodeMode `json:"grill_code_mode"`
	// GitBranch is the branch name at workflow start.
	GitBranch string `json:"git_branch"`
	// GitHeadSHA is the HEAD commit SHA at workflow start.
	GitHeadSHA string `json:"git_head_sha"`
	// CommitMode is the fix commit strategy in use.
	CommitMode string `json:"commit_mode"`
	// StartedAt is the ISO 8601 timestamp when the workflow began.
	StartedAt string `json:"started_at"`
	// UpdatedAt is the ISO 8601 timestamp of the last state change.
	UpdatedAt string `json:"updated_at"`
	// CumulativeCostUSD is the total estimated cost in USD so far.
	CumulativeCostUSD float64 `json:"cumulative_cost_usd"`
	// CumulativeWallClockSeconds is the total elapsed wall-clock time in seconds.
	CumulativeWallClockSeconds float64 `json:"cumulative_wall_clock_seconds"`
	// AgentInvocations is the total number of agent calls made.
	AgentInvocations int `json:"agent_invocations"`
	// FindingsSummary holds aggregate finding counts.
	FindingsSummary CodeReviewFindingsSummary `json:"findings_summary"`
	// Verdict is the most recent convergence verdict.
	Verdict CodeReviewVerdict `json:"verdict,omitempty"`
	// EscalationReason is set when the workflow transitions to CR_ESCALATED.
	EscalationReason string `json:"escalation_reason,omitempty"`
	// Warnings holds accumulated warnings (e.g., reduced_coverage, staleness).
	Warnings []string `json:"warnings,omitempty"`
}

// ---------------------------------------------------------------------------
// Lens groups
// ---------------------------------------------------------------------------

// CodeReviewLensGroups defines the 6 lens groups used for code review.
// Each lens maps to a grill-code skill invocation.
var CodeReviewLensGroups = []string{
	"correctness",
	"security",
	"testing",
	"error-handling",
	"observability",
	"overcomplexity",
}
