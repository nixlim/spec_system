package codedoc

import (
	"fmt"
	"time"
)

// ---------------------------------------------------------------------------
// Codedoc workflow states
// ---------------------------------------------------------------------------

// CDState represents the current phase of the codedoc workflow.
type CDState string

const (
	CDInit          CDState = "CD_INIT"
	CDDiscovery     CDState = "CD_DISCOVERY"
	CDHumanGateScope CDState = "CD_HUMAN_GATE_SCOPE"
	CDDrafting      CDState = "CD_DRAFTING"
	CDSanitising    CDState = "CD_SANITISING"
	CDHumanGateDraft CDState = "CD_HUMAN_GATE_DRAFT"
	CDReviewing     CDState = "CD_REVIEWING"
	CDRevising      CDState = "CD_REVISING"
	CDJudging       CDState = "CD_JUDGING"
	CDHumanGateFinal CDState = "CD_HUMAN_GATE_FINAL"
	CDWriting       CDState = "CD_WRITING"
	CDComplete      CDState = "CD_COMPLETE"
	CDEscalated     CDState = "CD_ESCALATED"
	CDError         CDState = "CD_ERROR"
)

// String returns the string representation of a CDState.
func (s CDState) String() string { return string(s) }

// IsTerminal returns true for terminal states (CD_COMPLETE, CD_ESCALATED).
func (s CDState) IsTerminal() bool {
	return s == CDComplete || s == CDEscalated
}

// IsGate returns true for human gate states.
func (s CDState) IsGate() bool {
	return s == CDHumanGateScope || s == CDHumanGateDraft || s == CDHumanGateFinal
}

// ---------------------------------------------------------------------------
// CDStateJSON — workflow state snapshot
// ---------------------------------------------------------------------------

// CDStateJSON is the persisted workflow state snapshot for codedoc workflows.
type CDStateJSON struct {
	// State is the current workflow phase.
	State CDState `json:"state"`
	// Round is the current review/revise iteration (1-indexed).
	Round int `json:"round"`
	// FeatureName is the human-readable name for this documentation workflow.
	FeatureName string `json:"feature_name"`
	// CodePath is the filesystem path to the target repository.
	CodePath string `json:"code_path"`
	// Mode is "full" or "incremental".
	Mode string `json:"mode"`
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
	// HadCriticalFindings indicates whether any CRITICAL or MAJOR findings remain unresolved.
	HadCriticalFindings bool `json:"had_critical_findings"`
	// GateScopeCorrectionCount is the number of corrections at the scope gate.
	GateScopeCorrectionCount int `json:"gate_scope_correction_count"`
	// GateDraftRedraftCount is the number of re-drafts at the draft gate.
	GateDraftRedraftCount int `json:"gate_draft_redraft_count"`
	// DraftVersion is the current draft iteration number (1-indexed).
	DraftVersion int `json:"draft_version"`
	// DraftSource indicates how the current draft was produced.
	DraftSource string `json:"draft_source,omitempty"`
	// DraftFailureNotice is a human-readable notice when one drafter failed.
	DraftFailureNotice string `json:"draft_failure_notice,omitempty"`
	// DiscoverySource indicates how the discovery output was produced.
	DiscoverySource string `json:"discovery_source,omitempty"`
}

// ---------------------------------------------------------------------------
// Configuration for state machine guards
// ---------------------------------------------------------------------------

// CDStateMachineConfig holds tuneable limits for codedoc state machine guards.
type CDStateMachineConfig struct {
	MaxGateCorrections  int
	MaxGateDraftRedrafts int
	MaxRounds           int
	MaxCostUSD          float64
	MaxWallClockMinutes int
	StalenessThreshold  int
}

// DefaultCDStateMachineConfig returns a CDStateMachineConfig with sensible defaults.
func DefaultCDStateMachineConfig() CDStateMachineConfig {
	return CDStateMachineConfig{
		MaxGateCorrections:  3,
		MaxGateDraftRedrafts: 2,
		MaxRounds:           3,
		MaxCostUSD:          50.0,
		MaxWallClockMinutes: 90,
		StalenessThreshold:  2,
	}
}

// CDStateMachineConfigFromConfig creates a CDStateMachineConfig from a
// CodedocConfig, extracting the relevant guard parameters.
func CDStateMachineConfigFromConfig(cfg *CodedocConfig) CDStateMachineConfig {
	return CDStateMachineConfig{
		MaxGateCorrections:  cfg.MaxGateCorrections,
		MaxGateDraftRedrafts: cfg.MaxGateDraftRedrafts,
		MaxRounds:           cfg.MaxRounds,
		MaxCostUSD:          cfg.MaxCostUSD,
		MaxWallClockMinutes: cfg.MaxWallClockMinutes,
		StalenessThreshold:  cfg.StalenessThreshold,
	}
}

// ---------------------------------------------------------------------------
// Guard function type
// ---------------------------------------------------------------------------

// CDGuard is a predicate that decides whether a transition from one codedoc
// state to another is permitted given the current workflow snapshot.
type CDGuard func(from, to CDState, ws *CDStateJSON) error

// ---------------------------------------------------------------------------
// Transition table
// ---------------------------------------------------------------------------

// cdTransitionTable is the static lookup table of valid codedoc state
// transitions. The universal rules "any non-terminal -> CD_ERROR" and
// "CD_ERROR -> CD_ERROR is blocked" are handled in code.
var cdTransitionTable = map[CDState][]CDState{
	CDInit:           {CDDiscovery},
	CDDiscovery:      {CDHumanGateScope, CDEscalated},
	CDHumanGateScope: {CDDrafting, CDDiscovery, CDEscalated},
	CDDrafting:       {CDSanitising, CDEscalated},
	CDSanitising:     {CDHumanGateDraft, CDDrafting},
	CDHumanGateDraft: {CDReviewing, CDDrafting, CDEscalated},
	CDReviewing:      {CDRevising, CDJudging, CDEscalated},
	CDRevising:       {CDJudging, CDEscalated},
	CDJudging:        {CDReviewing, CDHumanGateFinal, CDWriting, CDEscalated},
	CDHumanGateFinal: {CDWriting, CDReviewing, CDEscalated},
	CDWriting:        {CDComplete, CDEscalated},
	// CDComplete and CDEscalated are terminal — no outgoing edges
	// (except the universal "any non-terminal -> CD_ERROR" rule).
}

// isCDTerminal returns true for states with no normal outgoing transitions.
func isCDTerminal(s CDState) bool {
	return s == CDComplete || s == CDEscalated
}

// isValidCDTransition checks whether transitioning from -> to is structurally
// allowed by the transition table plus the universal rules:
//   - Any non-terminal state may transition to CD_ERROR.
//   - CD_ERROR -> CD_ERROR is blocked (prevents loops).
func isValidCDTransition(from, to CDState) bool {
	// CD_ERROR -> CD_ERROR is a no-op; block it.
	if from == CDError && to == CDError {
		return false
	}
	// Universal: any non-terminal -> CD_ERROR.
	if to == CDError && !isCDTerminal(from) {
		return true
	}
	// Terminal states cannot transition anywhere (except CD_ERROR, handled above).
	if isCDTerminal(from) {
		return false
	}
	// CD_ERROR can recover to any state (for resume).
	if from == CDError {
		return true
	}
	// Look up the explicit table.
	for _, allowed := range cdTransitionTable[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Built-in guards
// ---------------------------------------------------------------------------

// CDGateScopeCorrectionGuard blocks CD_HUMAN_GATE_SCOPE -> CD_DISCOVERY
// when the correction count has exceeded the configured maximum.
func CDGateScopeCorrectionGuard(cfg CDStateMachineConfig) CDGuard {
	return func(from, to CDState, ws *CDStateJSON) error {
		if from == CDHumanGateScope && to == CDDiscovery {
			if ws.GateScopeCorrectionCount > cfg.MaxGateCorrections {
				return fmt.Errorf(
					"scope gate correction limit reached (%d/%d)",
					ws.GateScopeCorrectionCount, cfg.MaxGateCorrections,
				)
			}
		}
		return nil
	}
}

// CDGateDraftRedraftGuard blocks CD_HUMAN_GATE_DRAFT -> CD_DRAFTING
// when the redraft count has exceeded the configured maximum.
func CDGateDraftRedraftGuard(cfg CDStateMachineConfig) CDGuard {
	return func(from, to CDState, ws *CDStateJSON) error {
		if from == CDHumanGateDraft && to == CDDrafting {
			if ws.GateDraftRedraftCount > cfg.MaxGateDraftRedrafts {
				return fmt.Errorf(
					"draft gate redraft limit reached (%d/%d)",
					ws.GateDraftRedraftCount, cfg.MaxGateDraftRedrafts,
				)
			}
		}
		return nil
	}
}

// CDMaxRoundsGuard blocks transitions into CD_REVIEWING when the current
// round exceeds the configured maximum.
func CDMaxRoundsGuard(cfg CDStateMachineConfig) CDGuard {
	return func(_, to CDState, ws *CDStateJSON) error {
		if to == CDReviewing {
			if ws.Round > cfg.MaxRounds {
				return fmt.Errorf(
					"max review rounds exceeded (%d/%d)",
					ws.Round, cfg.MaxRounds,
				)
			}
		}
		return nil
	}
}

// CDCostGuard blocks transitions when cumulative cost exceeds the budget.
// Transitions to terminal states (CDEscalated, CDComplete) and CD_ERROR are
// always allowed so that the workflow can reach a final state.
func CDCostGuard(cfg CDStateMachineConfig) CDGuard {
	return func(_, to CDState, ws *CDStateJSON) error {
		if isCDTerminal(to) || to == CDError {
			return nil
		}
		if ws.CumulativeCostUSD > cfg.MaxCostUSD {
			return fmt.Errorf(
				"cost budget exceeded ($%.2f/$%.2f)",
				ws.CumulativeCostUSD, cfg.MaxCostUSD,
			)
		}
		return nil
	}
}

// CDWallClockGuard blocks transitions when elapsed wall-clock time exceeds
// the configured limit. Transitions to terminal states and CD_ERROR are
// always allowed.
func CDWallClockGuard(cfg CDStateMachineConfig) CDGuard {
	return func(_, to CDState, ws *CDStateJSON) error {
		if isCDTerminal(to) || to == CDError {
			return nil
		}
		limitSeconds := float64(cfg.MaxWallClockMinutes) * 60
		if ws.CumulativeWallClockSeconds > limitSeconds {
			return fmt.Errorf(
				"wall clock budget exceeded (%.0fs/%.0fs)",
				ws.CumulativeWallClockSeconds, limitSeconds,
			)
		}
		return nil
	}
}

// CDJudgingToWritingGuard blocks CD_JUDGING -> CD_WRITING when unresolved
// CRITICAL or MAJOR findings exist (must go through CD_HUMAN_GATE_FINAL).
func CDJudgingToWritingGuard() CDGuard {
	return func(from, to CDState, ws *CDStateJSON) error {
		if from == CDJudging && to == CDWriting {
			if ws.HadCriticalFindings {
				return fmt.Errorf(
					"cannot write directly from JUDGING: unresolved CRITICAL/MAJOR findings exist",
				)
			}
		}
		return nil
	}
}

// CDJudgingToHumanGateFinalGuard blocks CD_JUDGING -> CD_HUMAN_GATE_FINAL
// when there are no unresolved CRITICAL or MAJOR findings (can write directly).
func CDJudgingToHumanGateFinalGuard() CDGuard {
	return func(from, to CDState, ws *CDStateJSON) error {
		if from == CDJudging && to == CDHumanGateFinal {
			if !ws.HadCriticalFindings {
				return fmt.Errorf(
					"CD_HUMAN_GATE_FINAL not required: no unresolved CRITICAL/MAJOR findings",
				)
			}
		}
		return nil
	}
}

// ---------------------------------------------------------------------------
// OnTransition callback
// ---------------------------------------------------------------------------

// CDOnTransitionFunc is called after every successful state transition. It
// receives the updated workflow snapshot and may persist it, emit events, etc.
// A non-nil return value rolls back the in-memory state change.
type CDOnTransitionFunc func(ws *CDStateJSON) error

// ---------------------------------------------------------------------------
// CDStateMachine
// ---------------------------------------------------------------------------

// CDStateMachine drives a single codedoc workflow instance through its
// lifecycle. It validates transitions against the lookup table, evaluates
// guard predicates, and notifies an optional callback on every successful
// transition.
type CDStateMachine struct {
	state        *CDStateJSON
	transitions  map[CDState][]CDState
	guards       []CDGuard
	onTransition CDOnTransitionFunc
}

// NewCDStateMachine creates a CDStateMachine for the given workflow state
// with the standard transition table and the default set of guards derived
// from cfg. Additional guards may be appended via AddGuard.
func NewCDStateMachine(ws *CDStateJSON, cfg CDStateMachineConfig, onTransition CDOnTransitionFunc) *CDStateMachine {
	return &CDStateMachine{
		state:       ws,
		transitions: cdTransitionTable,
		guards: []CDGuard{
			CDGateScopeCorrectionGuard(cfg),
			CDGateDraftRedraftGuard(cfg),
			CDMaxRoundsGuard(cfg),
			CDCostGuard(cfg),
			CDWallClockGuard(cfg),
			CDJudgingToWritingGuard(),
			CDJudgingToHumanGateFinalGuard(),
		},
		onTransition: onTransition,
	}
}

// AddGuard appends a custom guard to the state machine. Guards are evaluated
// in the order they were added; the first error wins.
func (sm *CDStateMachine) AddGuard(g CDGuard) {
	sm.guards = append(sm.guards, g)
}

// RestoreState sets the current state without validating transitions.
// Used when resuming from persisted state after a crash or server restart.
func (sm *CDStateMachine) RestoreState(ws *CDStateJSON) {
	sm.state = ws
}

// Current returns the current workflow state value.
func (sm *CDStateMachine) Current() CDState {
	return sm.state.State
}

// IsTerminal reports whether the state machine is in a terminal state.
func (sm *CDStateMachine) IsTerminal() bool {
	return isCDTerminal(sm.state.State)
}

// State returns a pointer to the full workflow state snapshot.
func (sm *CDStateMachine) State() *CDStateJSON {
	return sm.state
}

// Transition attempts to move the workflow from its current state to the
// target state. Returns a non-nil error if the transition is invalid, a
// guard blocks it, or the onTransition callback fails. On callback failure
// the in-memory state is rolled back.
func (sm *CDStateMachine) Transition(to CDState) error {
	from := sm.state.State

	// 1. Structural validation via the lookup table + universal rules.
	if !isValidCDTransition(from, to) {
		return fmt.Errorf("invalid transition: %s -> %s", from, to)
	}

	// 2. Evaluate every registered guard.
	for _, g := range sm.guards {
		if err := g(from, to, sm.state); err != nil {
			return fmt.Errorf("guard blocked %s -> %s: %w", from, to, err)
		}
	}

	// 3. Apply transition.
	sm.state.State = to
	sm.state.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	// 4. Notify callback; roll back on error.
	if sm.onTransition != nil {
		if err := sm.onTransition(sm.state); err != nil {
			sm.state.State = from
			return fmt.Errorf("onTransition callback failed for %s -> %s: %w", from, to, err)
		}
	}

	return nil
}
