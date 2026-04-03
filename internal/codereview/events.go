package codereview

import (
	"time"

	"github.com/foundry-zero/adversarial-spec-system/internal/specworkflow"
)

// ---------------------------------------------------------------------------
// Code review event type constants
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

// ---------------------------------------------------------------------------
// Code review event payloads
// ---------------------------------------------------------------------------

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

// CRAgentDispatchEvent is the payload for CREventAgentDispatch. It extends
// the base agent dispatch with lens group and provider fields.
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
// CREventEmitter
// ---------------------------------------------------------------------------

// CREventEmitter wraps a specworkflow.EventEmitter and provides code review
// specific convenience methods. All emitted events include the feature_name
// field. Emit errors are silently ignored (fire-and-forget).
type CREventEmitter struct {
	inner       specworkflow.EventEmitter
	featureName string
}

// NewCREventEmitter creates a CREventEmitter wrapping the given emitter.
// If inner is nil, all emit calls are no-ops.
func NewCREventEmitter(inner specworkflow.EventEmitter, featureName string) *CREventEmitter {
	return &CREventEmitter{
		inner:       inner,
		featureName: featureName,
	}
}

// emit sends an event envelope, silently ignoring errors (fire-and-forget).
func (e *CREventEmitter) emit(event specworkflow.EventEnvelope) {
	if e.inner == nil {
		return
	}
	if event.FeatureName == "" {
		event.FeatureName = e.featureName
	}
	_ = e.inner.Emit(event)
}

// EmitWorkflowStatus emits a workflow_status event with the current state,
// round, cost, and wall-clock time.
func (e *CREventEmitter) EmitWorkflowStatus(state string, round int, costUSD, wallClockSeconds float64) {
	e.emit(specworkflow.EventEnvelope{
		Event: CREventWorkflowStatus,
		Data: CRWorkflowStatusEvent{
			State:            state,
			Round:            round,
			CostUSD:          costUSD,
			WallClockSeconds: wallClockSeconds,
			Timestamp:        time.Now().UTC().Format(time.RFC3339),
		},
	})
}

// EmitAgentDispatch emits an agent_dispatch event when a reviewer or fix
// agent is about to be invoked.
func (e *CREventEmitter) EmitAgentDispatch(agent, lens, provider string, round int) {
	e.emit(specworkflow.EventEnvelope{
		Event: CREventAgentDispatch,
		Data: CRAgentDispatchEvent{
			Agent:     agent,
			Lens:      lens,
			Provider:  provider,
			Round:     round,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		},
	})
}

// EmitAgentComplete emits an agent_complete event when an agent finishes.
func (e *CREventEmitter) EmitAgentComplete(agent string, round int, success bool, durationMS int64, costUSD float64) {
	e.emit(specworkflow.EventEnvelope{
		Event: CREventAgentComplete,
		Data: CRAgentCompleteEvent{
			Agent:      agent,
			Success:    success,
			DurationMS: durationMS,
			CostUSD:    costUSD,
			Round:      round,
			Timestamp:  time.Now().UTC().Format(time.RFC3339),
		},
	})
}

// EmitGateRequest emits a gate_request event when the workflow reaches a
// human gate.
func (e *CREventEmitter) EmitGateRequest(gateType string, actions []string) {
	e.emit(specworkflow.EventEnvelope{
		Event: CREventGateRequest,
		Data: CRGateRequestEvent{
			GateType:  gateType,
			Actions:   actions,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		},
	})
}
