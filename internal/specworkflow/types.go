// Package specworkflow defines the core types for the adversarial spec review
// workflow, including workflow states, severity levels, verdicts, and the
// serializable state snapshot structure.
package specworkflow

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ---------------------------------------------------------------------------
// WorkflowState
// ---------------------------------------------------------------------------

// WorkflowState represents the current phase of the spec review workflow.
type WorkflowState int

const (
	// StateInit is the initial state before any work has begun.
	StateInit WorkflowState = iota
	// StateDiscovery is the codebase/context discovery phase.
	StateDiscovery
	// StateHumanGate1 is the first human approval gate (post-discovery).
	StateHumanGate1
	// StateDrafting is the spec drafting phase.
	StateDrafting
	// StateHumanGate2 is the second human approval gate (post-draft).
	StateHumanGate2
	// StateReviewing is the adversarial review phase.
	StateReviewing
	// StateRevising is the spec revision phase (addressing findings).
	StateRevising
	// StateJudging is the judge evaluation phase.
	StateJudging
	// StateHumanGateFinal is the final human approval gate.
	StateHumanGateFinal
	// StateFinalized means the spec has been accepted and finalized.
	StateFinalized
	// StateTaskify is the task decomposition phase (post-finalization).
	StateTaskify
	// StateTaskReview is the dual-provider task review phase.
	StateTaskReview
	// StateTaskRevision is the automated task graph revision phase.
	StateTaskRevision
	// StateTaskHumanGate is the human review gate for the task graph.
	StateTaskHumanGate
	// StateTasksApproved means tasks were approved and Beads creation attempted.
	StateTasksApproved
	// StateComplete is the terminal state after taskify completes.
	StateComplete
	// StateEscalated means the workflow was escalated for human intervention.
	StateEscalated
	// StateError means the workflow encountered an unrecoverable error.
	StateError
)

// workflowStateNames maps each WorkflowState to its canonical string form.
var workflowStateNames = [...]string{
	StateInit:           "INIT",
	StateDiscovery:      "DISCOVERY",
	StateHumanGate1:     "HUMAN_GATE_1",
	StateDrafting:       "DRAFTING",
	StateHumanGate2:     "HUMAN_GATE_2",
	StateReviewing:      "REVIEWING",
	StateRevising:       "REVISING",
	StateJudging:        "JUDGING",
	StateHumanGateFinal: "HUMAN_GATE_FINAL",
	StateFinalized:      "FINALIZED",
	StateTaskify:        "TASKIFY",
	StateTaskReview:     "TASK_REVIEW",
	StateTaskRevision:   "TASK_REVISION",
	StateTaskHumanGate:  "TASK_HUMAN_GATE",
	StateTasksApproved:  "TASKS_APPROVED",
	StateComplete:       "COMPLETE",
	StateEscalated:      "ESCALATED",
	StateError:          "ERROR",
}

// workflowStateLookup maps canonical uppercase names back to WorkflowState values.
var workflowStateLookup map[string]WorkflowState

func init() {
	workflowStateLookup = make(map[string]WorkflowState, len(workflowStateNames))
	for i, name := range workflowStateNames {
		workflowStateLookup[name] = WorkflowState(i)
	}
}

// String returns the canonical string representation of a WorkflowState.
func (s WorkflowState) String() string {
	if int(s) >= 0 && int(s) < len(workflowStateNames) {
		return workflowStateNames[s]
	}
	return fmt.Sprintf("WorkflowState(%d)", int(s))
}

// MarshalJSON serializes a WorkflowState to its JSON string representation.
func (s WorkflowState) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.String())
}

// UnmarshalJSON deserializes a WorkflowState from a JSON string (case-insensitive).
func (s *WorkflowState) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("WorkflowState must be a string: %w", err)
	}
	v, ok := workflowStateLookup[strings.ToUpper(raw)]
	if !ok {
		return fmt.Errorf("unknown WorkflowState: %q", raw)
	}
	*s = v
	return nil
}

// ParseWorkflowState parses a string into a WorkflowState (case-insensitive).
// Returns an error if the string does not match any known state.
func ParseWorkflowState(s string) (WorkflowState, error) {
	v, ok := workflowStateLookup[strings.ToUpper(s)]
	if !ok {
		return StateInit, fmt.Errorf("unknown WorkflowState: %q", s)
	}
	return v, nil
}

// ---------------------------------------------------------------------------
// Severity
// ---------------------------------------------------------------------------

// Severity represents the severity level of a finding.
type Severity int

const (
	// SeverityCritical indicates a critical finding that blocks acceptance.
	SeverityCritical Severity = iota
	// SeverityMajor indicates a major finding that should be addressed.
	SeverityMajor
	// SeverityMinor indicates a minor finding.
	SeverityMinor
	// SeverityObservation indicates an informational observation.
	SeverityObservation
)

// severityNames maps each Severity to its canonical string form.
var severityNames = [...]string{
	SeverityCritical:    "CRITICAL",
	SeverityMajor:       "MAJOR",
	SeverityMinor:       "MINOR",
	SeverityObservation: "OBSERVATION",
}

// severityLookup maps uppercase names back to Severity values.
var severityLookup map[string]Severity

func init() {
	severityLookup = make(map[string]Severity, len(severityNames))
	for i, name := range severityNames {
		severityLookup[name] = Severity(i)
	}
}

// String returns the canonical string representation of a Severity.
func (s Severity) String() string {
	if int(s) >= 0 && int(s) < len(severityNames) {
		return severityNames[s]
	}
	return fmt.Sprintf("Severity(%d)", int(s))
}

// MarshalJSON serializes a Severity to its JSON string representation.
func (s Severity) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.String())
}

// UnmarshalJSON deserializes a Severity from a JSON string (case-insensitive).
func (s *Severity) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("Severity must be a string: %w", err)
	}
	v, ok := severityLookup[strings.ToUpper(raw)]
	if !ok {
		return fmt.Errorf("unknown Severity: %q", raw)
	}
	*s = v
	return nil
}

// ParseSeverity parses a string into a Severity value (case-insensitive).
// Returns an error if the string does not match any known severity.
func ParseSeverity(s string) (Severity, error) {
	v, ok := severityLookup[strings.ToUpper(s)]
	if !ok {
		return SeverityCritical, fmt.Errorf("unknown Severity: %q", s)
	}
	return v, nil
}

// ---------------------------------------------------------------------------
// Verdict
// ---------------------------------------------------------------------------

// Verdict represents the judge's decision on a spec revision.
type Verdict int

const (
	// VerdictPass means the spec is accepted.
	VerdictPass Verdict = iota
	// VerdictRevise means the spec needs further revision.
	VerdictRevise
	// VerdictBlock means the spec is blocked and requires escalation.
	VerdictBlock
)

// verdictNames maps each Verdict to its canonical string form.
var verdictNames = [...]string{
	VerdictPass:   "PASS",
	VerdictRevise: "REVISE",
	VerdictBlock:  "BLOCK",
}

// verdictLookup maps uppercase names back to Verdict values.
var verdictLookup map[string]Verdict

func init() {
	verdictLookup = make(map[string]Verdict, len(verdictNames))
	for i, name := range verdictNames {
		verdictLookup[name] = Verdict(i)
	}
}

// String returns the canonical string representation of a Verdict.
func (v Verdict) String() string {
	if int(v) >= 0 && int(v) < len(verdictNames) {
		return verdictNames[v]
	}
	return fmt.Sprintf("Verdict(%d)", int(v))
}

// MarshalJSON serializes a Verdict to its JSON string representation.
func (v Verdict) MarshalJSON() ([]byte, error) {
	return json.Marshal(v.String())
}

// UnmarshalJSON deserializes a Verdict from a JSON string (case-insensitive).
func (v *Verdict) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("Verdict must be a string: %w", err)
	}
	val, ok := verdictLookup[strings.ToUpper(raw)]
	if !ok {
		return fmt.Errorf("unknown Verdict: %q", raw)
	}
	*v = val
	return nil
}

// ParseVerdict parses a string into a Verdict value (case-insensitive).
// Returns an error if the string does not match any known verdict.
func ParseVerdict(s string) (Verdict, error) {
	v, ok := verdictLookup[strings.ToUpper(s)]
	if !ok {
		return VerdictPass, fmt.Errorf("unknown Verdict: %q", s)
	}
	return v, nil
}

// ---------------------------------------------------------------------------
// Workflow state snapshot (JSON-serializable)
// ---------------------------------------------------------------------------

// FindingsSummary holds aggregate counts of findings by status and severity.
type FindingsSummary struct {
	// Raised is the total number of findings raised during review.
	Raised int `json:"raised"`
	// Closed is the number of findings that have been resolved.
	Closed int `json:"closed"`
	// OpenCritical is the number of open critical-severity findings.
	OpenCritical int `json:"open_critical"`
	// OpenMajor is the number of open major-severity findings.
	OpenMajor int `json:"open_major"`
}

// WorkflowStateJSON is the complete serializable snapshot of a workflow's
// current state. All time fields use ISO 8601 string representation.
type WorkflowStateJSON struct {
	// State is the current workflow phase.
	State WorkflowState `json:"state"`
	// Round is the current review/revise iteration (starts at 1).
	Round int `json:"round"`
	// FeatureName is the human-readable name of the feature being specified.
	FeatureName string `json:"feature_name"`
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
	FindingsSummary FindingsSummary `json:"findings_summary"`
	// HadCriticalFindings indicates whether any critical findings were raised.
	HadCriticalFindings bool `json:"had_critical_findings"`
	// Gate1CorrectionCount is the number of corrections applied at gate 1.
	Gate1CorrectionCount int `json:"gate1_correction_count"`
	// Gate2RedraftCount is the number of re-drafts triggered at gate 2.
	Gate2RedraftCount int `json:"gate2_redraft_count"`
	// CurrentSpecVersion is the version counter of the current spec draft.
	CurrentSpecVersion int `json:"current_spec_version"`
	// SkillChecksums maps skill file paths to their SHA-256 checksums.
	SkillChecksums map[string]string `json:"skill_checksums"`
	// DraftSource indicates how the current draft was produced.
	// Values: "combined" (both providers succeeded), "single_survivor" (one
	// provider failed), "single_provider" (dual-provider disabled or Codex
	// unavailable). Empty for workflows that have not yet reached DRAFTING.
	DraftSource string `json:"draft_source,omitempty"`
	// DraftFailureNotice is a human-readable notice when one drafter failed.
	// Only set when DraftSource is "single_survivor".
	DraftFailureNotice string `json:"draft_failure_notice,omitempty"`
	// TaskReviewRound is the per-stage round counter for task review.
	// Persisted for crash recovery so max rounds enforcement survives restarts.
	TaskReviewRound int `json:"task_review_round,omitempty"`
}
