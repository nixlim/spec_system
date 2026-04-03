package codereview

import "fmt"

// Cancel signals the orchestrator to stop at the next safe point. It sets
// an atomic cancellation flag and, if the state machine is active and not
// already in a terminal state, transitions to CR_ESCALATED with reason
// "operator cancelled".
//
// Cancel is safe to call from any goroutine. If the workflow is already
// terminal, Cancel returns nil (no-op).
func (o *CodeReviewOrchestrator) Cancel() error {
	o.cancelled.Store(true)

	if o.sm == nil {
		return fmt.Errorf("orchestrator not started")
	}

	// If already terminal, nothing to do.
	if o.sm.IsTerminal() {
		return nil
	}

	o.sm.State().EscalationReason = "operator cancelled"
	if o.auditLogger != nil {
		o.auditLogger.LogCodeReviewCancel(o.sm.State().FeatureName, "operator cancelled")
	}
	return o.sm.Transition(CREscalated)
}

// IsCancelled reports whether Cancel has been called on this orchestrator.
func (o *CodeReviewOrchestrator) IsCancelled() bool {
	return o.cancelled.Load()
}
