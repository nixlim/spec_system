package codereview

import (
	"time"

	"github.com/foundry-zero/adversarial-spec-system/internal/specworkflow"
)

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
