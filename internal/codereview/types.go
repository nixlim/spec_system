// Package codereview defines the core types for the automated code review
// workflow, including workflow states, fix output schemas, convergence
// verdicts, and grill-code mode selection.
package codereview

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/foundry-zero/adversarial-spec-system/internal/specworkflow"
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

// ---------------------------------------------------------------------------
// Gate payload types
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
// Fix phase types
// ---------------------------------------------------------------------------

// GitBranchManager abstracts git branch operations for the fix phase.
type GitBranchManager interface {
	// CreateFixBranch creates (or recreates) the fix branch for the given round.
	// If the branch already exists, it is deleted first.
	CreateFixBranch(codePath string, round int) error
	// DiffNameOnly returns the list of files modified since the given commit SHA.
	DiffNameOnly(codePath, baseSHA string) ([]string, error)
	// SubmodulePaths returns the list of submodule directory paths in the repo.
	SubmodulePaths(codePath string) ([]string, error)
}

// FixPhaseConfig holds the parameters for running the fix phase.
type FixPhaseConfig struct {
	// Runner is the agent runner for the fix agent (ClaudeRunner).
	Runner specworkflow.AgentRunner
	// BranchManager abstracts git branch operations (nil = default).
	BranchManager GitBranchManager
	// CodePath is the path to the target repository.
	CodePath string
	// WorkspaceDir is the feature workspace directory for storing artefacts.
	WorkspaceDir string
	// Round is the current review round.
	Round int
	// CommitMode is "branch_per_round" or "direct_commit".
	CommitMode string
	// FindingsPath is the path to the merged findings JSON file.
	FindingsPath string
	// SpecContent is optional spec content for context.
	SpecContent string
	// FixerTimeoutSeconds is the timeout for the fix agent.
	FixerTimeoutSeconds int
	// CriticalMajorIDs is the set of CRITICAL+MAJOR finding IDs for routing.
	CriticalMajorIDs map[string]bool
	// HeadSHA is the HEAD commit SHA before fixes, for post-fix diff validation.
	HeadSHA string
}

// FixPhaseResult captures the outcome of the fix phase.
type FixPhaseResult struct {
	// RouteDecision describes where the workflow should transition next.
	RouteDecision FixRouteDecision
	// FixOutput is the parsed fix output, nil on parse or agent failure.
	FixOutput *FixOutput
	// CostUSD is the cost of the fix agent invocation.
	CostUSD float64
	// DurationMS is the wall-clock duration of the fix agent invocation.
	DurationMS int64
	// FixOutputRaw is the raw JSON output from the fix agent for logging.
	FixOutputRaw string
}

// ---------------------------------------------------------------------------
// Fix output parse/route types
// ---------------------------------------------------------------------------

// ParseFixOutputResult holds the outcome of parsing a fix agent's JSON output.
type ParseFixOutputResult struct {
	// Output is the parsed FixOutput, nil on parse error.
	Output *FixOutput
	// Warnings holds non-fatal issues found during validation.
	Warnings []string
	// Err is set when the JSON is invalid or missing required fields.
	Err error
}

// FixRouteDecision describes where the workflow should transition after a fix.
type FixRouteDecision struct {
	// NextState is the recommended next state.
	NextState CodeReviewState
	// Reason explains why this route was chosen.
	Reason string
	// Warnings holds advisory messages to display at the human gate.
	Warnings []string
}

// ---------------------------------------------------------------------------
// Event type constants and payload types
// ---------------------------------------------------------------------------

const (
	// CREventWorkflowStatus is emitted on every code review state transition.
	CREventWorkflowStatus = "workflow_status"
	// CREventAgentDispatch is emitted when a reviewer or fix agent is dispatched.
	CREventAgentDispatch = "agent_dispatch"
	// CREventAgentComplete is emitted when an agent finishes (success or failure).
	CREventAgentComplete = "agent_complete"
	// CREventGateRequest is emitted when the workflow reaches a human gate.
	CREventGateRequest = "gate_request"
)

// CRWorkflowStatusEvent is the payload for CREventWorkflowStatus.
type CRWorkflowStatusEvent struct {
	// State is the current workflow state name.
	State string `json:"state"`
	// Round is the current review round.
	Round int `json:"round"`
	// CostUSD is the cumulative cost so far.
	CostUSD float64 `json:"cost_usd"`
	// WallClockSeconds is the elapsed wall-clock time in seconds.
	WallClockSeconds float64 `json:"wall_clock_seconds"`
	// Timestamp is the ISO 8601 time of the event.
	Timestamp string `json:"timestamp"`
}

// CRAgentDispatchEvent is the payload for CREventAgentDispatch.
type CRAgentDispatchEvent struct {
	// Agent is the name of the agent being dispatched.
	Agent string `json:"agent"`
	// Lens is the grill-code lens group (e.g. "security", "correctness").
	Lens string `json:"lens"`
	// Provider is the agent provider ("claude" or "codex").
	Provider string `json:"provider"`
	// Round is the current review round.
	Round int `json:"round"`
	// Timestamp is the ISO 8601 time of dispatch.
	Timestamp string `json:"timestamp"`
}

// CRAgentCompleteEvent is the payload for CREventAgentComplete.
type CRAgentCompleteEvent struct {
	// Agent is the name of the agent that completed.
	Agent string `json:"agent"`
	// Success indicates whether the agent completed successfully.
	Success bool `json:"success"`
	// DurationMS is the agent execution time in milliseconds.
	DurationMS int64 `json:"duration_ms"`
	// CostUSD is the estimated cost of this agent invocation.
	CostUSD float64 `json:"cost_usd"`
	// Round is the review round in which the agent ran.
	Round int `json:"round"`
	// Timestamp is the ISO 8601 time of completion.
	Timestamp string `json:"timestamp"`
}

// CRGateRequestEvent is the payload for CREventGateRequest.
type CRGateRequestEvent struct {
	// GateType identifies the gate (e.g. "scope", "fixes").
	GateType string `json:"gate_type"`
	// Actions lists the available gate actions.
	Actions []string `json:"actions"`
	// Timestamp is the ISO 8601 time of the gate request.
	Timestamp string `json:"timestamp"`
}

// ---------------------------------------------------------------------------
// API / orchestrator contract types
// ---------------------------------------------------------------------------

// StartCodeReviewRequest contains the parameters for starting a code review.
type StartCodeReviewRequest struct {
	// CodePath is the filesystem path to the target git repository (required).
	CodePath string
	// FeatureName is the kebab-case identifier for this code review (required).
	FeatureName string
	// SpecPath is the optional path to a spec markdown file.
	SpecPath string
	// TaskListPath is the optional path to a task list file.
	TaskListPath string
}

// CRGateResponse carries a human's response to a code review gate.
type CRGateResponse struct {
	// Action is the gate action: "confirm", "cancel", "re-review", "accept", "escalate".
	Action string
	// Comment is optional free-text feedback.
	Comment string
}

// GitInfoProvider abstracts git operations for testing.
type GitInfoProvider interface {
	// IsGitRepo returns true if the path is a git repository.
	IsGitRepo(path string) bool
	// GetBranch returns the current branch name.
	GetBranch(path string) (string, error)
	// GetHeadSHA returns the HEAD commit SHA.
	GetHeadSHA(path string) (string, error)
}

// CROrchestratorConfig holds the parameters required to construct a
// CodeReviewOrchestrator.
type CROrchestratorConfig struct {
	// WorkspaceDir is the root directory for all workflow artefacts.
	WorkspaceDir string
	// Config is the code review configuration.
	Config CodeReviewConfig
	// GitProvider abstracts git operations (nil = default).
	GitProvider GitInfoProvider
	// Runner is the primary agent runner (Claude).
	Runner specworkflow.AgentRunner
	// CodexRunner is the optional Codex agent runner (nil = Claude-only).
	CodexRunner specworkflow.AgentRunner
	// FixRunner is the runner for the fix agent, constructed with
	// --allowedTools "Read,Write,Bash" (no --dangerously-skip-permissions).
	// If nil, falls back to Runner.
	FixRunner specworkflow.AgentRunner
	// Emitter broadcasts workflow events via WebSocket.
	Emitter specworkflow.EventEmitter
	// AuditLogger writes structured events to JSONL audit log (nil = no logging).
	AuditLogger *CRAuditLogger
	// OTELPort is the gRPC OTLP receiver port for telemetry (0 = disabled).
	OTELPort int
}

// ---------------------------------------------------------------------------
// State machine contract types
// ---------------------------------------------------------------------------

// CRStateMachineConfig holds tuneable limits for the code review state
// machine guards.
type CRStateMachineConfig struct {
	// MaxRounds is the maximum number of re-review rounds after the initial
	// review. The guard blocks when round > MaxRounds. 0 = no re-reviews.
	MaxRounds int
	// MaxCostUSD is the cumulative cost budget. Workflow escalates when exceeded.
	MaxCostUSD float64
	// MaxWallClockMinutes is the maximum wall-clock time in minutes.
	MaxWallClockMinutes int
}

// CRGuard is a predicate that decides whether a transition from one code
// review state to another is permitted given the current workflow snapshot.
// It returns a non-nil error describing the reason when the transition is blocked.
type CRGuard func(from, to CodeReviewState, ws *CodeReviewStateJSON) error
