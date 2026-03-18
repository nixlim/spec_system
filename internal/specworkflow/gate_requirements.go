package specworkflow

import "fmt"

// ---------------------------------------------------------------------------
// Gate1Handler — HUMAN_GATE_1 handler
// ---------------------------------------------------------------------------

// Gate1Handler manages the HUMAN_GATE_1 phase where a human confirms,
// corrects, or cancels the discovery output before drafting proceeds.
type Gate1Handler struct {
	state              *WorkflowStateJSON
	emitter            EventEmitter
	maxGateCorrections int
}

// NewGate1Handler creates a Gate1Handler bound to the given workflow state
// and event emitter. maxGateCorrections controls how many correction loops
// are allowed before the handler returns an error.
func NewGate1Handler(state *WorkflowStateJSON, emitter EventEmitter, maxGateCorrections int) *Gate1Handler {
	return &Gate1Handler{
		state:              state,
		emitter:            emitter,
		maxGateCorrections: maxGateCorrections,
	}
}

// EnterGate emits a gate_request event with gate_type "requirements_confirmation"
// so that the UI can present the discovery output to the human for review.
func (h *Gate1Handler) EnterGate(discovery *DiscoveryOutput) error {
	event := NewGateRequestEvent("requirements_confirmation", h.state.FeatureName, discovery)
	return h.emitter.Emit(event)
}

// HandleConfirm processes a human confirmation of the discovery output.
// It returns StateDrafting as the next workflow state.
func (h *Gate1Handler) HandleConfirm() (WorkflowState, error) {
	return StateDrafting, nil
}

// HandleCorrect processes human corrections to the discovery output. It
// increments gate1_correction_count and returns StateDiscovery so the
// discovery agent can re-run with the corrections applied. Returns an error
// if the correction limit has been reached.
func (h *Gate1Handler) HandleCorrect(corrections map[string]string) (WorkflowState, error) {
	if h.state.Gate1CorrectionCount >= h.maxGateCorrections {
		return StateEscalated, fmt.Errorf(
			"gate 1 correction limit reached (%d/%d)",
			h.state.Gate1CorrectionCount, h.maxGateCorrections,
		)
	}
	h.state.Gate1CorrectionCount++
	return StateDiscovery, nil
}

// HandleCancel processes a human cancellation of the workflow at gate 1.
// It returns StateEscalated as the next workflow state.
func (h *Gate1Handler) HandleCancel() (WorkflowState, error) {
	return StateEscalated, nil
}
