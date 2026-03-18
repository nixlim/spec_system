package specworkflow

import "fmt"

// ---------------------------------------------------------------------------
// Gate2Handler — HUMAN_GATE_2 handler
// ---------------------------------------------------------------------------

// AmbiguityResolution represents a human's resolution of a single ambiguity
// warning from the drafter output.
type AmbiguityResolution struct {
	// WarningID is the AMB-W-NNN identifier of the ambiguity warning.
	WarningID string `json:"warning_id"`
	// Action is one of "accept", "answer", or "defer".
	Action string `json:"action"`
	// Answer is the human-provided answer (required when Action is "answer").
	Answer string `json:"answer,omitempty"`
}

// Gate2Handler manages the HUMAN_GATE_2 phase where a human resolves
// ambiguity warnings from the drafter output before review proceeds.
type Gate2Handler struct {
	state       *WorkflowStateJSON
	emitter     EventEmitter
	maxRedrafts int
}

// NewGate2Handler creates a Gate2Handler bound to the given workflow state
// and event emitter. maxRedrafts controls the maximum number of redrafts
// allowed (the redraft count must be strictly less than maxRedrafts).
func NewGate2Handler(state *WorkflowStateJSON, emitter EventEmitter, maxRedrafts int) *Gate2Handler {
	return &Gate2Handler{
		state:       state,
		emitter:     emitter,
		maxRedrafts: maxRedrafts,
	}
}

// EnterGate emits a gate_request event with gate_type "ambiguity_resolution"
// so that the UI can present the drafter's ambiguity warnings to the human.
func (h *Gate2Handler) EnterGate(drafter *DrafterOutput) error {
	event := NewGateRequestEvent("ambiguity_resolution", h.state.FeatureName, drafter)
	return h.emitter.Emit(event)
}

// HandleResolutions processes the human's resolutions to ambiguity warnings.
// It returns:
//   - needsRedraft: true if any resolution has action "answer" (triggering a redraft)
//   - nextState: StateDrafting if redraft needed, StateReviewing otherwise
//   - error: non-nil if a redraft is needed but the redraft limit is reached
func (h *Gate2Handler) HandleResolutions(resolutions []AmbiguityResolution) (bool, WorkflowState, error) {
	hasAnswer := false
	for _, r := range resolutions {
		if r.Action == "answer" {
			hasAnswer = true
			break
		}
	}

	if !hasAnswer {
		// All accept/defer — proceed to reviewing.
		return false, StateReviewing, nil
	}

	// An answer requires a redraft.
	if h.state.Gate2RedraftCount >= h.maxRedrafts {
		return false, StateEscalated, fmt.Errorf(
			"gate 2 redraft limit reached (%d/%d)",
			h.state.Gate2RedraftCount, h.maxRedrafts,
		)
	}
	h.state.Gate2RedraftCount++
	return true, StateDrafting, nil
}

// IsAnswerDisabled reports whether the "answer" action is disabled because
// the redraft limit has been reached.
func (h *Gate2Handler) IsAnswerDisabled() bool {
	return h.state.Gate2RedraftCount >= h.maxRedrafts
}
