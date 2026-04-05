package specworkflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// gateActionCancelled is a sentinel action returned by waitForGateResponse when
// the run context is cancelled. It is distinct from the user-facing "cancel"
// gate action to avoid collision.
const gateActionCancelled = "__ctx_cancelled"

// beadsKVKey returns a feature-namespaced KV key: "{feature}:{suffix}".
// All Beads KV operations use this to ensure multi-feature isolation (FR-008, US-4 AC-1).
func (o *Orchestrator) beadsKVKey(suffix string) string {
	return fmt.Sprintf("%s:%s", o.featureName, suffix)
}

// createRunEpic creates a Beads epic for the workflow run, generates a UUID
// run_id, and stores both in KV and WorkflowStateJSON. The run_id (UUID) is
// distinct from the epic bead ID (FR-040–042). Silently skipped if Beads is
// unavailable (graceful degradation).
func (o *Orchestrator) createRunEpic(state *WorkflowStateJSON) {
	if o.beadsClient == nil {
		return
	}
	ctx := context.Background()

	// Generate UUID run_id (FR-040).
	runID := uuid.New().String()
	state.RunID = runID

	epicID, err := o.beadsClient.CreateEpic(ctx, "orchestrator", o.featureName+" spec-review run")
	if err != nil {
		log.Printf("[orchestrator] WARNING: failed to create Beads run epic: %v", err)
		return
	}

	log.Printf("[orchestrator] created Beads run epic: %s (run_id: %s)", epicID, runID)
	o.runEpicID = epicID

	// Store run_id (UUID) in KV (FR-040, US-4 AC-2).
	if err := o.beadsClient.KVSet(ctx, "orchestrator", o.beadsKVKey("run_id"), runID); err != nil {
		log.Printf("[orchestrator] WARNING: failed to store run_id in KV: %v", err)
	}
	// Store run_epic_id (bead ID) separately in KV (US-4 AC-2).
	if err := o.beadsClient.KVSet(ctx, "orchestrator", o.beadsKVKey("run_epic_id"), epicID); err != nil {
		log.Printf("[orchestrator] WARNING: failed to store run_epic_id in KV: %v", err)
	}
}

// createBeadsFindings creates Beads finding issues for each merged finding
// and stores the mapping from finding ID to Beads issue ID in KV. Silently
// skipped if Beads is unavailable (graceful degradation).
// Per FR-009, checks KV before creating to prevent duplicates on recovery replay.
func (o *Orchestrator) createBeadsFindings(findings []MergedFinding) {
	if o.beadsClient == nil {
		return
	}
	ctx := context.Background()

	// Retrieve the run epic ID from cache or KV.
	runEpicID := o.runEpicID
	if runEpicID == "" {
		var err error
		runEpicID, err = o.beadsClient.KVGet(ctx, "orchestrator", o.beadsKVKey("run_epic_id"))
		if err != nil || runEpicID == "" {
			log.Printf("[orchestrator] WARNING: cannot create Beads findings — run_epic_id not available: %v", err)
			return
		}
		o.runEpicID = runEpicID
	}

	runID, _ := o.beadsClient.KVGet(ctx, "orchestrator", o.beadsKVKey("run_id"))

	for _, f := range findings {
		kvKey := o.beadsKVKey(fmt.Sprintf("finding:%s", f.ID))

		// FR-009: check if this finding already has a Beads issue (dedup on recovery).
		existingID, _ := o.beadsClient.KVGet(ctx, "orchestrator", kvKey)
		if existingID != "" {
			continue
		}

		beadID, err := o.beadsClient.CreateFinding(ctx, "orchestrator", runEpicID, f, runID)
		if err != nil {
			log.Printf("[orchestrator] WARNING: failed to create Beads finding for %s: %v", f.ID, err)
			continue
		}
		// Store mapping immediately after create (FR-008).
		if err := o.beadsClient.KVSet(ctx, "orchestrator", kvKey, beadID); err != nil {
			log.Printf("[orchestrator] WARNING: failed to store Beads finding mapping for %s: %v", f.ID, err)
		}
	}
}

// updateBeadsIssueStatus updates the Beads issue status corresponding to a
// finding when its status changes. Silently skipped if Beads is unavailable
// or the finding has no corresponding Beads issue (graceful degradation).
// Per FR-010, uses bd close for StatusVerified and StatusClosed.
func (o *Orchestrator) updateBeadsIssueStatus(findingID string, newStatus IssueStatus) {
	if o.beadsClient == nil {
		return
	}
	ctx := context.Background()

	kvKey := o.beadsKVKey(fmt.Sprintf("finding:%s", findingID))
	beadID, err := o.beadsClient.KVGet(ctx, "orchestrator", kvKey)
	if err != nil || beadID == "" {
		// No Beads issue for this finding — skip silently.
		return
	}

	// FR-010/FR-011: pass issue_status metadata so crash recovery can read status from Beads.
	meta := map[string]string{"issue_status": string(newStatus)}

	// FR-010: use bd close for StatusVerified/StatusClosed, bd update otherwise.
	// The Beads status is mapped per spec TD-B (not the raw IssueStatus string).
	switch newStatus {
	case StatusVerified, StatusClosed:
		if err := o.beadsClient.CloseIssue(ctx, "orchestrator", beadID, string(newStatus), meta); err != nil {
			log.Printf("[orchestrator] WARNING: failed to close Beads issue %s (%s): %v", beadID, newStatus, err)
		}
	default:
		beadsStatus := issueStatusToBeadsStatus(newStatus)
		if err := o.beadsClient.UpdateIssue(ctx, "orchestrator", beadID, beadsStatus, meta); err != nil {
			log.Printf("[orchestrator] WARNING: failed to update Beads issue %s status to %s: %v", beadID, newStatus, err)
		}
	}
}

// writeBeadsStateSnapshot writes individual convergence keys and then the
// full state snapshot to Beads KV on each SaveState call. Per US-4 AC-1 and
// FR-017 (MAJ-002), state_snapshot is written LAST so a reader observing it
// as current can trust all individual keys are also up-to-date.
// If the first KV write returns ErrBeadsUnavailable, remaining writes are
// skipped (FR-018/MAJ-005).
func (o *Orchestrator) writeBeadsStateSnapshot(state *WorkflowStateJSON) {
	if o.beadsClient == nil {
		return
	}
	ctx := context.Background()

	// Individual keys first (US-4 AC-1).
	kvWrites := []struct{ key, value string }{
		{o.beadsKVKey("state"), state.State.String()},
		{o.beadsKVKey("round"), fmt.Sprintf("%d", state.Round)},
		{o.beadsKVKey("open_critical"), fmt.Sprintf("%d", state.FindingsSummary.OpenCritical)},
		{o.beadsKVKey("open_major"), fmt.Sprintf("%d", state.FindingsSummary.OpenMajor)},
		{o.beadsKVKey("total_findings"), fmt.Sprintf("%d", state.FindingsSummary.Raised)},
		{o.beadsKVKey("cost_usd"), fmt.Sprintf("%.4f", state.CumulativeCostUSD)},
	}

	for _, kv := range kvWrites {
		if err := o.beadsClient.KVSet(ctx, "orchestrator", kv.key, kv.value); err != nil {
			if errors.Is(err, ErrBeadsUnavailable) {
				log.Printf("[orchestrator] WARNING: Beads unavailable during SaveState — KV not updated")
				return
			}
			log.Printf("[orchestrator] WARNING: failed to write KV %s: %v", kv.key, err)
		}
	}

	// state_snapshot written LAST (FR-017/MAJ-002).
	snapshot, err := json.Marshal(state)
	if err != nil {
		log.Printf("[orchestrator] WARNING: failed to marshal state snapshot for KV: %v", err)
		return
	}
	if err := o.beadsClient.KVSet(ctx, "orchestrator", o.beadsKVKey("state_snapshot"), string(snapshot)); err != nil {
		log.Printf("[orchestrator] WARNING: failed to write state_snapshot to KV: %v", err)
	}
}

// ---------------------------------------------------------------------------
// CD-88v.16: Comment posting (judge verdicts + reviser actions)
// ---------------------------------------------------------------------------

// postJudgeComment posts a judge verdict comment on a finding's Beads issue.
// Format: "Judge [round N]: <verdict> — <rationale>"
func (o *Orchestrator) postJudgeComment(findingID string, round int, verdict string, rationale string) {
	if o.beadsClient == nil {
		return
	}
	ctx := context.Background()

	kvKey := o.beadsKVKey(fmt.Sprintf("finding:%s", findingID))
	beadID, _ := o.beadsClient.KVGet(ctx, "orchestrator", kvKey)
	if beadID == "" {
		return
	}

	body := fmt.Sprintf("Judge [round %d]: %s — %s", round, verdict, rationale)
	if err := o.beadsClient.Comment(ctx, "judge", beadID, body); err != nil {
		log.Printf("[orchestrator] WARNING: failed to post judge comment on %s: %v", findingID, err)
	}
}

// postReviserComment posts a reviser action comment on a finding's Beads issue.
// Format: "Reviser [round N]: revised — <description>"
func (o *Orchestrator) postReviserComment(findingID string, round int, description string) {
	if o.beadsClient == nil {
		return
	}
	ctx := context.Background()

	kvKey := o.beadsKVKey(fmt.Sprintf("finding:%s", findingID))
	beadID, _ := o.beadsClient.KVGet(ctx, "orchestrator", kvKey)
	if beadID == "" {
		return
	}

	body := fmt.Sprintf("Reviser [round %d]: revised — %s", round, description)
	if err := o.beadsClient.Comment(ctx, "reviser", beadID, body); err != nil {
		log.Printf("[orchestrator] WARNING: failed to post reviser comment on %s: %v", findingID, err)
	}
}

// postRunEpicComment posts a comment on the run epic.
func (o *Orchestrator) postRunEpicComment(actor string, body string) {
	if o.beadsClient == nil || o.runEpicID == "" {
		return
	}
	ctx := context.Background()
	if err := o.beadsClient.Comment(ctx, actor, o.runEpicID, body); err != nil {
		log.Printf("[orchestrator] WARNING: failed to post comment on run epic: %v", err)
	}
}

// ---------------------------------------------------------------------------
// CD-88v.10: Gate proxy task creation, polling loop, and KV storage
// ---------------------------------------------------------------------------

// gateShowResult represents the parsed JSON from bd show --json for a gate task.
type gateShowResult struct {
	Status      string `json:"status"`
	CloseReason string `json:"close_reason"`
}

// openGateProxy creates a Beads task as a gate proxy and tracks it on the
// orchestrator. On recovery, reuses an existing open gate proxy from KV
// instead of creating a duplicate (dedup per FR-009).
func (o *Orchestrator) openGateProxy(gateName string, gateTitle string) {
	if o.beadsClient == nil {
		return
	}
	ctx := context.Background()

	// Check for existing gate proxy in KV (recovery dedup).
	kvKey := o.beadsKVKey(fmt.Sprintf("gate:%s", gateName))
	existingID, _ := o.beadsClient.KVGet(ctx, "orchestrator", kvKey)
	if existingID != "" {
		// Verify the gate task is still open before reusing.
		showJSON, err := o.beadsClient.GateShow(ctx, "orchestrator", existingID)
		if err == nil {
			var result gateShowResult
			if json.Unmarshal(showJSON, &result) == nil && result.Status == "open" {
				o.activeGateBeadID = existingID
				o.activeGateName = gateName
				log.Printf("[orchestrator] restored gate proxy for %s: %s", gateName, existingID)
				return
			}
		}
		// Gate no longer open — fall through to create new one.
	}

	beadID, err := o.beadsClient.GateCreate(ctx, "orchestrator", gateTitle)
	if err != nil {
		log.Printf("[orchestrator] WARNING: failed to create gate proxy for %s: %v", gateName, err)
		return
	}

	o.activeGateBeadID = beadID
	o.activeGateName = gateName

	if err := o.beadsClient.KVSet(ctx, "orchestrator", kvKey, beadID); err != nil {
		log.Printf("[orchestrator] WARNING: failed to store gate proxy ID in KV: %v", err)
	}

	log.Printf("[orchestrator] created gate proxy for %s: %s", gateName, beadID)
}

// parseCloseReason parses the close_reason from a Beads gate task.
// Returns (action, comment, recognized). Normalizes whitespace before parsing.
// Per MAJ-001: whitespace-only or bare "Closed" is unrecognized.
func parseCloseReason(raw, acceptAction, rejectAction string) (action, comment string, recognized bool) {
	// Strip \n, \r\n, \r and leading/trailing whitespace.
	trimmed := strings.TrimSpace(
		strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ").Replace(raw),
	)
	if trimmed == "" {
		return "", "", false
	}

	upper := strings.ToUpper(trimmed)

	if strings.HasPrefix(upper, "ACCEPT:") {
		return acceptAction, strings.TrimSpace(trimmed[7:]), true
	}
	if strings.HasPrefix(upper, "REJECT:") {
		return rejectAction, strings.TrimSpace(trimmed[7:]), true
	}
	return "", "", false
}

// waitForGateResponse races the gateCh channel (web UI) against Beads gate
// polling and the workflow's runCtx (cancellation). The gate proxy must already
// be open (openGateProxy called before).
// Returns the GateResponse from whichever source wins first.
// If Beads is unavailable, falls back to gateCh-only.
// After 10 consecutive unrecognized close_reasons, returns an "escalate" action.
func (o *Orchestrator) waitForGateResponse(acceptAction, rejectAction string) GateResponse {
	if o.beadsClient == nil || o.activeGateBeadID == "" {
		// No Beads or gate creation failed — just wait on channel.
		select {
		case resp := <-o.gateCh:
			o.closeGateProxy(resp.Action)
			return resp
		case <-o.runCtx.Done():
			o.closeGateProxy("cancelled")
			return GateResponse{Action: gateActionCancelled}
		}
	}

	// Configurable poll interval (default 5s).
	pollInterval := o.config.BeadsGatePollInterval
	if pollInterval <= 0 {
		pollInterval = 5 * time.Second
	}

	// Start polling goroutine.
	pollCh := make(chan GateResponse, 1)
	stopPoll := make(chan struct{})
	beadID := o.activeGateBeadID
	gateName := o.activeGateName
	ctx := o.runCtx

	go func() {
		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()

		unrecognizedCount := 0
		const maxUnrecognized = 10

		for {
			select {
			case <-stopPoll:
				return
			case <-ctx.Done():
				select {
				case pollCh <- GateResponse{Action: gateActionCancelled}:
				default:
				}
				return
			case <-ticker.C:
				showJSON, err := o.beadsClient.GateShow(ctx, "orchestrator", beadID)
				if err != nil {
					if ctx.Err() != nil {
						return
					}
					log.Printf("[orchestrator] WARNING: gate poll error for %s: %v", gateName, err)
					continue
				}

				var result gateShowResult
				if err := json.Unmarshal(showJSON, &result); err != nil {
					log.Printf("[orchestrator] WARNING: failed to parse gate show for %s: %v", gateName, err)
					continue
				}

				if result.Status != "closed" {
					// Still open, keep polling.
					unrecognizedCount = 0
					continue
				}

				action, comment, recognized := parseCloseReason(result.CloseReason, acceptAction, rejectAction)
				if recognized {
					select {
					case pollCh <- GateResponse{Action: action, Comment: comment}:
					default:
					}
					return
				}

				// Unrecognized close_reason (including bare "Closed").
				unrecognizedCount++
				log.Printf("[orchestrator] WARNING: unrecognized gate close_reason for %s: %q (%d/%d)",
					gateName, result.CloseReason, unrecognizedCount, maxUnrecognized)

				if unrecognizedCount >= maxUnrecognized {
					select {
					case pollCh <- GateResponse{Action: "escalate"}:
					default:
					}
					return
				}
			}
		}
	}()

	// Advisory timeout warning (US-3 AC-8): warn but continue polling.
	gateTimeout := o.config.BeadsGateTimeout
	if gateTimeout <= 0 {
		gateTimeout = 24 * time.Hour
	}
	timeoutTimer := time.NewTimer(gateTimeout)
	defer timeoutTimer.Stop()

	go func() {
		select {
		case <-timeoutTimer.C:
			log.Printf("[orchestrator] WARNING: Gate %s: open for > %s — human intervention may be required", gateName, gateTimeout)
		case <-stopPoll:
		case <-ctx.Done():
		}
	}()

	// Race: gateCh (web UI) vs pollCh (Beads gate) vs context cancellation.
	var resp GateResponse
	select {
	case resp = <-o.gateCh:
		close(stopPoll)
	case resp = <-pollCh:
		// Drain gateCh if something was queued (defensive).
		select {
		case <-o.gateCh:
		default:
		}
	case <-o.runCtx.Done():
		close(stopPoll)
		resp = GateResponse{Action: gateActionCancelled}
	}

	o.closeGateProxy(resp.Action)
	return resp
}

// closeGateProxy closes the active gate proxy with a reason derived from
// the human's action.
func (o *Orchestrator) closeGateProxy(action string) {
	if o.beadsClient == nil || o.activeGateBeadID == "" {
		return
	}
	ctx := context.Background()

	reason := fmt.Sprintf("%s:%s", strings.ToUpper(action), o.activeGateName)
	if err := o.beadsClient.GateClose(ctx, "orchestrator", o.activeGateBeadID, reason); err != nil {
		log.Printf("[orchestrator] WARNING: failed to close gate proxy %s: %v", o.activeGateBeadID, err)
	}

	o.activeGateBeadID = ""
	o.activeGateName = ""
}

// ---------------------------------------------------------------------------
// CD-88v.14: Close open gate proxies on error/escalation
// ---------------------------------------------------------------------------

// closeOpenGateProxies closes any active gate proxy when the workflow
// transitions to ERROR or ESCALATED. Called by escalateFrom.
func (o *Orchestrator) closeOpenGateProxies(reason string) {
	if o.beadsClient == nil || o.activeGateBeadID == "" {
		return
	}
	ctx := context.Background()

	closeReason := fmt.Sprintf("ESCALATED:%s", reason)
	if err := o.beadsClient.GateClose(ctx, "orchestrator", o.activeGateBeadID, closeReason); err != nil {
		log.Printf("[orchestrator] WARNING: failed to close gate proxy on escalation: %v", err)
	}

	o.activeGateBeadID = ""
	o.activeGateName = ""
}

// ---------------------------------------------------------------------------
// CD-88v.11: Review-cycle molecule per round
// ---------------------------------------------------------------------------

// pourReviewMolecule pours a review-cycle molecule at round start.
// Requires a proto bead ID stored in the global KV key "beads:proto:review-cycle"
// (not feature-namespaced, per spec Section 2.3 and FR-023).
// Silently skipped if no proto is configured (graceful degradation).
func (o *Orchestrator) pourReviewMolecule(round int) {
	if o.beadsClient == nil {
		return
	}
	ctx := context.Background()

	protoID, err := o.beadsClient.KVGet(ctx, "orchestrator", "beads:proto:review-cycle")
	if err != nil || protoID == "" {
		log.Printf("[orchestrator] review-cycle proto not configured -- skipping molecule tracking")
		return
	}

	vars := map[string]string{
		"round":   fmt.Sprintf("%d", round),
		"feature": o.featureName,
		"epic":    o.runEpicID,
	}

	molID, err := o.beadsClient.MolPour(ctx, "orchestrator", protoID, vars)
	if err != nil {
		log.Printf("[orchestrator] WARNING: failed to pour review-cycle molecule for round %d: %v", round, err)
		return
	}

	kvKey := o.beadsKVKey(fmt.Sprintf("mol:round:%d", round))
	if err := o.beadsClient.KVSet(ctx, "orchestrator", kvKey, molID); err != nil {
		log.Printf("[orchestrator] WARNING: failed to store molecule ID for round %d: %v", round, err)
	}

	// FR-025: discover step bead IDs and store each in KV.
	stepsJSON, err := o.beadsClient.MolShowSteps(ctx, "orchestrator", molID)
	if err != nil {
		log.Printf("[orchestrator] WARNING: failed to discover molecule steps for round %d: %v", round, err)
	} else {
		var steps []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		}
		if err := json.Unmarshal(stepsJSON, &steps); err != nil {
			log.Printf("[orchestrator] WARNING: failed to parse molecule steps for round %d: %v", round, err)
		} else {
			for _, step := range steps {
				stepKV := o.beadsKVKey(fmt.Sprintf("mol:round:%d:step:%s", round, strings.ToLower(step.Title)))
				if err := o.beadsClient.KVSet(ctx, "orchestrator", stepKV, step.ID); err != nil {
					log.Printf("[orchestrator] WARNING: failed to store step %s ID for round %d: %v", step.Title, round, err)
				}
			}
		}
	}

	log.Printf("[orchestrator] poured review-cycle molecule for round %d: %s", round, molID)
}

// closeMoleculeStep closes a named step of the current round's molecule.
// Looks up the step bead ID from KV first (populated by pourReviewMolecule),
// falling back to MolShowSteps if the KV key is missing.
func (o *Orchestrator) closeMoleculeStep(round int, stepName string) {
	if o.beadsClient == nil {
		return
	}
	ctx := context.Background()

	// Try KV lookup first (FR-025: step IDs stored after pour).
	stepKVKey := o.beadsKVKey(fmt.Sprintf("mol:round:%d:step:%s", round, strings.ToLower(stepName)))
	stepBeadID, _ := o.beadsClient.KVGet(ctx, "orchestrator", stepKVKey)
	if stepBeadID != "" {
		if err := o.beadsClient.CloseStep(ctx, "orchestrator", stepBeadID); err != nil {
			log.Printf("[orchestrator] WARNING: failed to close molecule step %s: %v", stepName, err)
		}
		return
	}

	// Fallback: discover steps from molecule (pre-KV-storage data or recovery).
	molKVKey := o.beadsKVKey(fmt.Sprintf("mol:round:%d", round))
	molID, _ := o.beadsClient.KVGet(ctx, "orchestrator", molKVKey)
	if molID == "" {
		return
	}

	stepsJSON, err := o.beadsClient.MolShowSteps(ctx, "orchestrator", molID)
	if err != nil {
		log.Printf("[orchestrator] WARNING: failed to show molecule steps for round %d: %v", round, err)
		return
	}

	var steps []struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal(stepsJSON, &steps); err != nil {
		log.Printf("[orchestrator] WARNING: failed to parse molecule steps: %v", err)
		return
	}

	for _, step := range steps {
		if strings.EqualFold(step.Title, stepName) {
			if err := o.beadsClient.CloseStep(ctx, "orchestrator", step.ID); err != nil {
				log.Printf("[orchestrator] WARNING: failed to close molecule step %s: %v", stepName, err)
			}
			return
		}
	}
}

// ---------------------------------------------------------------------------
// CD-88v.17: Crash recovery — rebuild IssueTracker from Beads
// ---------------------------------------------------------------------------

// beadsFinding represents a finding as stored in Beads, used for crash recovery.
type beadsFinding struct {
	ID        string            `json:"id"`
	Title     string            `json:"title"`
	Status    string            `json:"status"`
	IssueType string            `json:"issue_type"`
	Metadata  map[string]string `json:"metadata"`
}

// rebuildIssueTrackerFromBeads rebuilds the finding-to-bead-ID mapping from
// Beads children of the run epic. Called during crash recovery when Beads has
// authoritative state that may differ from disk.
//
// RunID mismatch detection: compares the KV run_id (UUID) with the state's
// RunID (UUID from WorkflowStateJSON). If they differ, the KV belongs to a
// different run — skip recovery (prevents cross-run contamination).
func (o *Orchestrator) rebuildIssueTrackerFromBeads(tracker *IssueTracker, stateRunID string) {
	if o.beadsClient == nil {
		return
	}
	ctx := context.Background()

	// Load run_epic_id from KV.
	epicID, err := o.beadsClient.KVGet(ctx, "orchestrator", o.beadsKVKey("run_epic_id"))
	if err != nil || epicID == "" {
		return
	}

	// RunID mismatch detection: compare KV run_id (UUID) against the state's
	// RunID (UUID). Both are UUIDs, unlike epicID which is a bead ID.
	kvRunID, _ := o.beadsClient.KVGet(ctx, "orchestrator", o.beadsKVKey("run_id"))
	if kvRunID != "" && stateRunID != "" && kvRunID != stateRunID {
		log.Printf("[orchestrator] WARNING: Beads run_id mismatch (kv=%s, state=%s) — skipping Beads recovery", kvRunID, stateRunID)
		return
	}

	o.runEpicID = epicID

	childrenJSON, err := o.beadsClient.Children(ctx, "orchestrator", epicID)
	if err != nil {
		log.Printf("[orchestrator] WARNING: failed to get Beads children for recovery: %v", err)
		return
	}

	var children []beadsFinding
	if err := json.Unmarshal(childrenJSON, &children); err != nil {
		log.Printf("[orchestrator] WARNING: failed to parse Beads children: %v", err)
		return
	}

	recovered := 0
	for _, child := range children {
		// FR-028: filter to finding issues only — skip gate proxies,
		// molecule steps, and other non-finding children of the run epic.
		if child.IssueType != "finding" {
			continue
		}

		findingID := child.Metadata["finding_id"]
		if findingID == "" {
			findingID = child.Title
		}

		// Restore finding ID → Beads bead ID mapping in KV (FR-031).
		kvKey := o.beadsKVKey(fmt.Sprintf("finding:%s", findingID))
		_ = o.beadsClient.KVSet(ctx, "orchestrator", kvKey, child.ID)

		// FR-029: Restore IssueTracker status from Beads issue_status metadata.
		issueStatus := mapBeadsToIssueStatus(child.Metadata["issue_status"], child.Status)
		if issueStatus != "" {
			if issue, ok := tracker.Issues[findingID]; ok && issueStatus != issue.Status {
				issue.Status = issueStatus
			}
		}

		recovered++
	}

	if recovered > 0 {
		log.Printf("[orchestrator] rebuilt %d finding mappings from Beads", recovered)
	}
}

// issueStatusToBeadsStatus maps an IssueStatus to the Beads status string
// used in bd update calls. Per spec TD-B:
//   StatusRaised       → "open"
//   StatusAddressed    → "in_progress"
//   StatusDismissed    → "closed"
//   StatusAcknowledged → "closed"
//   StatusReopened     → "open"
func issueStatusToBeadsStatus(status IssueStatus) string {
	switch status {
	case StatusRaised, StatusReopened:
		return "open"
	case StatusAddressed:
		return "in_progress"
	case StatusDismissed, StatusAcknowledged:
		return "closed"
	default:
		return string(status)
	}
}

// mapBeadsToIssueStatus maps Beads status and issue_status metadata to an
// IssueStatus. The issue_status metadata field takes priority over the raw
// Beads status, allowing distinction between verified and closed (FR-029).
func mapBeadsToIssueStatus(issueStatusMeta, beadsStatus string) IssueStatus {
	if issueStatusMeta != "" {
		switch IssueStatus(issueStatusMeta) {
		case StatusRaised, StatusAddressed, StatusVerified, StatusClosed,
			StatusReopened, StatusDismissed, StatusAcknowledged:
			return IssueStatus(issueStatusMeta)
		}
	}

	// FR-029 fallback: map raw Beads status to IssueStatus.
	switch beadsStatus {
	case "open":
		return StatusRaised
	case "in_progress":
		return StatusAddressed
	case "closed":
		return StatusClosed
	default:
		return ""
	}
}

// ---------------------------------------------------------------------------
// CD-88v.17: Stale output file cleanup on REVIEWING recovery (FR-028/MAJ-004)
// ---------------------------------------------------------------------------

// cleanStaleReviewerOutputs removes incomplete reviewer output files for the
// given round. Called during recovery when resuming into REVIEWING state to
// prevent stale partial outputs from being merged.
func cleanStaleReviewerOutputs(specDir string, round int) {
	patterns := []string{
		fmt.Sprintf("review-*-round-%d.json", round),
		fmt.Sprintf("review-*-claude-round-%d.json", round),
		fmt.Sprintf("review-*-codex-round-%d.json", round),
	}
	for _, pattern := range patterns {
		matches, _ := filepath.Glob(filepath.Join(specDir, pattern))
		for _, m := range matches {
			if err := os.Remove(m); err == nil {
				log.Printf("[orchestrator] removed stale reviewer output: %s", filepath.Base(m))
			}
		}
	}
}

// ---------------------------------------------------------------------------
// CD-88v.15: Taskval Beads notifications
// ---------------------------------------------------------------------------

// postTaskvalExhaustion posts a comment on the run epic when taskval
// refinement is exhausted after max retries.
func (o *Orchestrator) postTaskvalExhaustion(validationErrors []string) {
	body := fmt.Sprintf("Taskval refinement exhausted after max retries. Last errors:\n%s",
		strings.Join(validationErrors, "\n"))
	o.postRunEpicComment("orchestrator", body)
}

// postTaskvalInternalError posts a comment on the run epic when taskval
// encounters an internal error (exit code 2).
func (o *Orchestrator) postTaskvalInternalError(stderr string) {
	body := fmt.Sprintf("Taskval internal error:\n%s", stderr)
	o.postRunEpicComment("orchestrator", body)
}
