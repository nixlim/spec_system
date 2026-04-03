package codereview

import (
	"fmt"
	"time"
)

// DefaultCRStateMachineConfig returns a CRStateMachineConfig with sensible defaults.
func DefaultCRStateMachineConfig() CRStateMachineConfig {
	return CRStateMachineConfig{
		MaxRounds:           3,
		MaxCostUSD:          50.0,
		MaxWallClockMinutes: 120,
	}
}

// CRStateMachineConfigFromConfig creates a CRStateMachineConfig from a
// CodeReviewConfig, extracting the relevant guard parameters.
func CRStateMachineConfigFromConfig(cfg *CodeReviewConfig) CRStateMachineConfig {
	return CRStateMachineConfig{
		MaxRounds:           cfg.MaxRounds,
		MaxCostUSD:          cfg.MaxCostUSD,
		MaxWallClockMinutes: cfg.MaxWallClockMinutes,
	}
}

// ---------------------------------------------------------------------------
// Transition table
// ---------------------------------------------------------------------------

// crTransitionTable is the static lookup table of valid code review state
// transitions.
var crTransitionTable = map[CodeReviewState][]CodeReviewState{
	CRInit:           {CRHumanGateScope},
	CRHumanGateScope: {CRReviewing, CREscalated},
	CRReviewing:      {CRFixing, CRHumanGateFixes, CRComplete, CREscalated},
	CRFixing:         {CRReviewing, CRHumanGateFixes, CREscalated},
	CRHumanGateFixes: {CRReviewing, CRComplete, CREscalated},
	// CRComplete and CREscalated are terminal — no outgoing edges.
}

// isCRTerminal returns true for code review states with no outgoing transitions.
func isCRTerminal(s CodeReviewState) bool {
	return s == CRComplete || s == CREscalated
}

// isValidCRTransition checks whether transitioning from -> to is structurally
// allowed by the code review transition table.
func isValidCRTransition(from, to CodeReviewState) bool {
	if isCRTerminal(from) {
		return false
	}
	for _, allowed := range crTransitionTable[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Built-in guards
// ---------------------------------------------------------------------------

// CRMaxRoundsGuard blocks transitions into CR_REVIEWING when the current
// round exceeds the configured maximum. The guard blocks when
// round > MaxRounds. When MaxRounds is 0, every transition to CR_REVIEWING
// is blocked (no re-reviews allowed; the orchestrator should route to
// CR_HUMAN_GATE_FIXES instead).
func CRMaxRoundsGuard(cfg CRStateMachineConfig) CRGuard {
	return func(_, to CodeReviewState, ws *CodeReviewStateJSON) error {
		if to == CRReviewing {
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

// CRCostGuard blocks transitions when cumulative cost exceeds the budget.
// Transitions to terminal states (CREscalated, CRComplete) are always allowed
// so that the workflow can reach a final state when the budget is exceeded.
func CRCostGuard(cfg CRStateMachineConfig) CRGuard {
	return func(_, to CodeReviewState, ws *CodeReviewStateJSON) error {
		if isCRTerminal(to) {
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

// CRWallClockGuard blocks transitions when elapsed wall-clock time exceeds
// the configured limit. Transitions to terminal states are always allowed.
func CRWallClockGuard(cfg CRStateMachineConfig) CRGuard {
	return func(_, to CodeReviewState, ws *CodeReviewStateJSON) error {
		if isCRTerminal(to) {
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

// ---------------------------------------------------------------------------
// OnTransition callback
// ---------------------------------------------------------------------------

// CROnTransitionFunc is called after every successful state transition. It
// receives the updated workflow snapshot and may persist it, emit events, etc.
// A non-nil return value rolls back the in-memory state change.
type CROnTransitionFunc func(ws *CodeReviewStateJSON) error

// ---------------------------------------------------------------------------
// CRStateMachine
// ---------------------------------------------------------------------------

// CRStateMachine drives a single code review workflow instance through its
// lifecycle. It validates transitions against the lookup table, evaluates
// guard predicates, and notifies an optional callback on every successful
// transition.
type CRStateMachine struct {
	state        *CodeReviewStateJSON
	transitions  map[CodeReviewState][]CodeReviewState
	guards       []CRGuard
	onTransition CROnTransitionFunc
}

// NewCRStateMachine creates a CRStateMachine for the given workflow state
// with the standard transition table and the default set of guards derived
// from cfg. Additional guards may be appended via AddGuard.
func NewCRStateMachine(ws *CodeReviewStateJSON, cfg CRStateMachineConfig, onTransition CROnTransitionFunc) *CRStateMachine {
	return &CRStateMachine{
		state:       ws,
		transitions: crTransitionTable,
		guards: []CRGuard{
			CRMaxRoundsGuard(cfg),
			CRCostGuard(cfg),
			CRWallClockGuard(cfg),
		},
		onTransition: onTransition,
	}
}

// AddGuard appends a custom guard to the state machine. Guards are evaluated
// in the order they were added; the first error wins.
func (sm *CRStateMachine) AddGuard(g CRGuard) {
	sm.guards = append(sm.guards, g)
}

// RestoreState sets the current state without validating transitions.
// This is used when resuming from persisted state after a crash or server
// restart. It bypasses both the transition table and guards.
func (sm *CRStateMachine) RestoreState(ws *CodeReviewStateJSON) {
	sm.state = ws
}

// Current returns the current workflow state value.
func (sm *CRStateMachine) Current() CodeReviewState {
	return sm.state.State
}

// IsTerminal reports whether the state machine is in a terminal state
// (CR_COMPLETE or CR_ESCALATED).
func (sm *CRStateMachine) IsTerminal() bool {
	return isCRTerminal(sm.state.State)
}

// State returns a pointer to the full workflow state snapshot.
func (sm *CRStateMachine) State() *CodeReviewStateJSON {
	return sm.state
}

// Transition attempts to move the workflow from its current state to the
// target state. It returns a non-nil error if the transition is invalid, a
// guard blocks it, or the onTransition callback fails. On callback failure
// the in-memory state is rolled back.
func (sm *CRStateMachine) Transition(to CodeReviewState) error {
	from := sm.state.State

	// 1. Structural validation via the lookup table.
	if !isValidCRTransition(from, to) {
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
