package codedoc

import "fmt"

// ---------------------------------------------------------------------------
// ScopeGateHandler — CD_HUMAN_GATE_SCOPE
// ---------------------------------------------------------------------------

// ScopeGateHandler manages the CD_HUMAN_GATE_SCOPE phase where a human
// confirms, adjusts, or cancels the discovery scope before drafting.
type ScopeGateHandler struct {
	state              *CDStateJSON
	maxGateCorrections int
}

// NewScopeGateHandler creates a ScopeGateHandler bound to the given state.
func NewScopeGateHandler(state *CDStateJSON, maxGateCorrections int) *ScopeGateHandler {
	return &ScopeGateHandler{
		state:              state,
		maxGateCorrections: maxGateCorrections,
	}
}

// HandleConfirm processes a human confirmation of the discovery scope.
// Returns CDDrafting as the next state.
func (h *ScopeGateHandler) HandleConfirm() (CDState, error) {
	return CDDrafting, nil
}

// HandleCorrect processes scope adjustments. It increments the correction
// count and returns CDDiscovery so the discovery agent can re-run with
// adjustments. Returns an error if the correction limit has been reached.
func (h *ScopeGateHandler) HandleCorrect() (CDState, error) {
	if h.state.GateScopeCorrectionCount >= h.maxGateCorrections {
		return CDEscalated, fmt.Errorf(
			"scope gate correction limit reached (%d/%d)",
			h.state.GateScopeCorrectionCount, h.maxGateCorrections,
		)
	}
	h.state.GateScopeCorrectionCount++
	return CDDiscovery, nil
}

// HandleCancel processes a human cancellation at the scope gate.
// Returns CDEscalated.
func (h *ScopeGateHandler) HandleCancel() (CDState, error) {
	return CDEscalated, nil
}

// ---------------------------------------------------------------------------
// DraftGateHandler — CD_HUMAN_GATE_DRAFT
// ---------------------------------------------------------------------------

// DraftGateHandler manages the CD_HUMAN_GATE_DRAFT phase where a human
// reviews the draft documentation and decides to approve, re-draft, or cancel.
type DraftGateHandler struct {
	state       *CDStateJSON
	maxRedrafts int
}

// NewDraftGateHandler creates a DraftGateHandler bound to the given state.
func NewDraftGateHandler(state *CDStateJSON, maxRedrafts int) *DraftGateHandler {
	return &DraftGateHandler{
		state:       state,
		maxRedrafts: maxRedrafts,
	}
}

// HandleApprove processes a human approval of the draft.
// Returns CDReviewing as the next state.
func (h *DraftGateHandler) HandleApprove() (CDState, error) {
	return CDReviewing, nil
}

// HandleRedraft processes a re-draft request with user feedback. It increments
// the redraft count and returns CDDrafting. Returns an error if the redraft
// limit has been reached.
func (h *DraftGateHandler) HandleRedraft() (CDState, error) {
	if h.state.GateDraftRedraftCount >= h.maxRedrafts {
		return CDEscalated, fmt.Errorf(
			"draft gate redraft limit reached (%d/%d)",
			h.state.GateDraftRedraftCount, h.maxRedrafts,
		)
	}
	h.state.GateDraftRedraftCount++
	return CDDrafting, nil
}

// HandleCancel processes a human cancellation at the draft gate.
// Returns CDEscalated.
func (h *DraftGateHandler) HandleCancel() (CDState, error) {
	return CDEscalated, nil
}

// IsRedraftDisabled reports whether re-drafting is disabled because
// the redraft limit has been reached.
func (h *DraftGateHandler) IsRedraftDisabled() bool {
	return h.state.GateDraftRedraftCount >= h.maxRedrafts
}

// ---------------------------------------------------------------------------
// FinalGateHandler — CD_HUMAN_GATE_FINAL
// ---------------------------------------------------------------------------

// FinalGateHandler manages the CD_HUMAN_GATE_FINAL phase where a human
// reviews remaining unresolved CRITICAL/MAJOR findings and decides to
// approve writing, reject (escalate), or request another review round.
type FinalGateHandler struct {
	state *CDStateJSON
}

// NewFinalGateHandler creates a FinalGateHandler bound to the given state.
func NewFinalGateHandler(state *CDStateJSON) *FinalGateHandler {
	return &FinalGateHandler{state: state}
}

// HandleAccept processes a human approval to proceed with writing despite
// unresolved findings. Returns CDWriting.
func (h *FinalGateHandler) HandleAccept() (CDState, error) {
	return CDWriting, nil
}

// HandleReject processes a human rejection at the final gate.
// Returns CDEscalated.
func (h *FinalGateHandler) HandleReject() (CDState, error) {
	return CDEscalated, nil
}

// HandleRequestReview processes a request for another review round.
// Increments the round counter and returns CDReviewing.
func (h *FinalGateHandler) HandleRequestReview() (CDState, error) {
	h.state.Round++
	return CDReviewing, nil
}
