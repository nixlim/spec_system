package specworkflow

import (
	"fmt"
	"time"
)

// ---------------------------------------------------------------------------
// Configuration
// ---------------------------------------------------------------------------

// StateMachineConfig holds tuneable limits for the state machine guards.
type StateMachineConfig struct {
	// MaxGateCorrections is the maximum number of HUMAN_GATE_1 -> DISCOVERY
	// correction loops allowed before the guard blocks the transition.
	MaxGateCorrections int
	// MaxGate2Redrafts is the maximum number of HUMAN_GATE_2 -> DRAFTING
	// redraft loops allowed before the guard blocks the transition.
	MaxGate2Redrafts int
	// MaxRounds is the maximum number of review/revise rounds allowed before
	// the guard blocks transitions into REVIEWING.
	MaxRounds int
}

// DefaultStateMachineConfig returns a StateMachineConfig with sensible defaults.
func DefaultStateMachineConfig() StateMachineConfig {
	return StateMachineConfig{
		MaxGateCorrections: 3,
		MaxGate2Redrafts:   1,
		MaxRounds:          5,
	}
}

// ---------------------------------------------------------------------------
// Guard function type
// ---------------------------------------------------------------------------

// Guard is a predicate that decides whether a transition from one state to
// another is permitted given the current workflow snapshot. It returns a
// non-nil error describing the reason when the transition is blocked.
type Guard func(from, to WorkflowState, ws *WorkflowStateJSON) error

// ---------------------------------------------------------------------------
// Transition table
// ---------------------------------------------------------------------------

// transitionTable is the static lookup table of valid state transitions.
// Every key maps to the set of states reachable from it. The special rules
// "any state -> ERROR" and "ERROR -> any state" are handled in code so the
// table stays compact.
var transitionTable = map[WorkflowState][]WorkflowState{
	StateInit:           {StateDiscovery},
	StateDiscovery:      {StateHumanGate1, StateError},
	StateHumanGate1:     {StateDrafting, StateDiscovery, StateEscalated},
	StateDrafting:       {StateHumanGate2, StateError},
	StateHumanGate2:     {StateReviewing, StateDrafting, StateEscalated},
	StateReviewing:      {StateRevising, StateJudging, StateError},
	StateRevising:       {StateJudging, StateError},
	StateJudging:        {StateReviewing, StateHumanGateFinal, StateFinalized, StateEscalated, StateError},
	StateHumanGateFinal: {StateFinalized, StateReviewing, StateEscalated},
	StateFinalized:      {StateTaskify},
	StateTaskify:        {StateTaskReview, StateTaskify, StateEscalated},
	StateTaskReview:     {StateTaskHumanGate, StateTaskRevision, StateEscalated},
	StateTaskRevision:   {StateTaskReview},
	StateTaskHumanGate:  {StateTasksApproved, StateTaskify, StateComplete},
	StateTasksApproved:  {StateComplete},
	// StateComplete and StateEscalated are terminal — no outgoing edges
	// (except the universal "any -> ERROR" rule).
}

// isTerminal returns true for states that have no normal outgoing transitions.
func isTerminal(s WorkflowState) bool {
	return s == StateComplete || s == StateEscalated
}

// isValidTransition checks whether transitioning from -> to is structurally
// allowed by the transition table plus the two universal rules:
//   - Any state may transition to ERROR  (error capture).
//   - ERROR may transition to any state  (retry / recovery).
func isValidTransition(from, to WorkflowState) bool {
	// ERROR -> ERROR is a no-op; block it before the universal rules fire.
	if from == StateError && to == StateError {
		return false
	}
	// Universal: any -> ERROR.
	if to == StateError {
		return true
	}
	// Universal: ERROR -> any (recovery).
	if from == StateError {
		return true
	}
	// Terminal states cannot transition anywhere (except ERROR, handled above).
	if isTerminal(from) {
		return false
	}
	// Look up the explicit table.
	for _, allowed := range transitionTable[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Built-in guards
// ---------------------------------------------------------------------------

// Gate1CorrectionGuard blocks HUMAN_GATE_1 -> DISCOVERY when the correction
// count has exceeded the configured maximum. Uses > (not >=) because
// HandleCorrect increments the count before the transition runs, so
// count == maxGateCorrections means "this is the last allowed correction."
func Gate1CorrectionGuard(cfg StateMachineConfig) Guard {
	return func(from, to WorkflowState, ws *WorkflowStateJSON) error {
		if from == StateHumanGate1 && to == StateDiscovery {
			if ws.Gate1CorrectionCount > cfg.MaxGateCorrections {
				return fmt.Errorf(
					"gate 1 correction limit reached (%d/%d)",
					ws.Gate1CorrectionCount, cfg.MaxGateCorrections,
				)
			}
		}
		return nil
	}
}

// Gate2RedraftGuard blocks HUMAN_GATE_2 -> DRAFTING when the redraft count
// has exceeded the configured maximum. Uses > (not >=) because the gate
// handler increments the count before the transition runs, so
// count == maxRedrafts means "this is the last allowed redraft."
func Gate2RedraftGuard(cfg StateMachineConfig) Guard {
	return func(from, to WorkflowState, ws *WorkflowStateJSON) error {
		if from == StateHumanGate2 && to == StateDrafting {
			if ws.Gate2RedraftCount > cfg.MaxGate2Redrafts {
				return fmt.Errorf(
					"gate 2 redraft limit reached (%d/%d)",
					ws.Gate2RedraftCount, cfg.MaxGate2Redrafts,
				)
			}
		}
		return nil
	}
}

// MaxRoundsGuard blocks any transition into REVIEWING when the current
// round exceeds the configured maximum.
func MaxRoundsGuard(cfg StateMachineConfig) Guard {
	return func(_, to WorkflowState, ws *WorkflowStateJSON) error {
		if to == StateReviewing {
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

// JudgingToFinalizedGuard blocks JUDGING -> FINALIZED when critical
// findings were raised (the spec must go through HUMAN_GATE_FINAL instead).
func JudgingToFinalizedGuard() Guard {
	return func(from, to WorkflowState, ws *WorkflowStateJSON) error {
		if from == StateJudging && to == StateFinalized {
			if ws.HadCriticalFindings {
				return fmt.Errorf(
					"cannot finalize directly from JUDGING: critical findings were raised",
				)
			}
		}
		return nil
	}
}

// JudgingToHumanGateFinalGuard blocks JUDGING -> HUMAN_GATE_FINAL when
// there were no critical findings (the spec can be finalized directly).
func JudgingToHumanGateFinalGuard() Guard {
	return func(from, to WorkflowState, ws *WorkflowStateJSON) error {
		if from == StateJudging && to == StateHumanGateFinal {
			if !ws.HadCriticalFindings {
				return fmt.Errorf(
					"HUMAN_GATE_FINAL not required: no critical findings were raised",
				)
			}
		}
		return nil
	}
}

// ---------------------------------------------------------------------------
// OnTransition callback
// ---------------------------------------------------------------------------

// OnTransitionFunc is called after every successful state transition. It
// receives the updated workflow snapshot and may persist it, emit events, etc.
// A non-nil return value rolls back the in-memory state change.
type OnTransitionFunc func(ws *WorkflowStateJSON) error

// ---------------------------------------------------------------------------
// StateMachine
// ---------------------------------------------------------------------------

// StateMachine drives a single workflow instance through its lifecycle. It
// validates transitions against the lookup table, evaluates guard predicates,
// and notifies an optional callback on every successful transition.
type StateMachine struct {
	state        *WorkflowStateJSON
	transitions  map[WorkflowState][]WorkflowState
	guards       []Guard
	onTransition OnTransitionFunc
}

// NewStateMachine creates a StateMachine for the given workflow state with
// the standard transition table and the default set of guards derived from
// cfg. Additional guards may be appended via AddGuard.
func NewStateMachine(ws *WorkflowStateJSON, cfg StateMachineConfig, onTransition OnTransitionFunc) *StateMachine {
	sm := &StateMachine{
		state:        ws,
		transitions:  transitionTable,
		onTransition: onTransition,
		guards: []Guard{
			Gate1CorrectionGuard(cfg),
			Gate2RedraftGuard(cfg),
			MaxRoundsGuard(cfg),
			JudgingToFinalizedGuard(),
			JudgingToHumanGateFinalGuard(),
		},
	}
	return sm
}

// AddGuard appends a custom guard to the state machine. Guards are evaluated
// in the order they were added; the first error wins.
func (sm *StateMachine) AddGuard(g Guard) {
	sm.guards = append(sm.guards, g)
}

// RestoreState sets the current state without validating transitions.
// This is used when resuming from persisted state after a crash or server
// restart. It bypasses both the transition table and guards.
func (sm *StateMachine) RestoreState(ws *WorkflowStateJSON) {
	sm.state = ws
}

// Current returns the current workflow state value.
func (sm *StateMachine) Current() WorkflowState {
	return sm.state.State
}

// IsTerminal reports whether the state machine is in a terminal state
// (COMPLETE or ESCALATED), meaning no further transitions are possible.
func (sm *StateMachine) IsTerminal() bool {
	return isTerminal(sm.state.State)
}

// State returns a pointer to the full workflow state snapshot.
func (sm *StateMachine) State() *WorkflowStateJSON {
	return sm.state
}

// Transition attempts to move the workflow from its current state to the
// target state. It returns a non-nil error if the transition is invalid, a
// guard blocks it, or the onTransition callback fails. On callback failure
// the in-memory state is rolled back.
func (sm *StateMachine) Transition(to WorkflowState) error {
	from := sm.state.State

	// 1. Structural validation via the lookup table + universal rules.
	if !isValidTransition(from, to) {
		return fmt.Errorf(
			"invalid transition: %s -> %s",
			from, to,
		)
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
