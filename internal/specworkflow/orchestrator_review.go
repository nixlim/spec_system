package specworkflow

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

// handleReviewing dispatches all 4 reviewer lens groups in parallel, merges
// findings, updates the issue tracker, and transitions to either REVISING
// (if critical/major findings) or JUDGING (if none).
func (o *Orchestrator) handleReviewing(state *WorkflowStateJSON, specDir string) error {
	// Check circuit breakers before dispatch.
	if err := o.checkBreakersBefore(state); err != nil {
		o.escalateFrom(StateReviewing)
		return nil
	}

	// Build reviewer prompts for all 4 lens groups.
	specPath := o.currentSpecPath(state)
	prompts := make(map[string]string)
	outputPaths := make(map[string]string)

	for _, lens := range []string{"clarity", "consistency", "security", "correctness"} {
		p, err := o.promptBuilder.BuildReviewerPrompt(lens, state.Round, specPath)
		if err != nil {
			return fmt.Errorf("build reviewer prompt for %s: %w", lens, err)
		}
		prompts[lens] = p
		letter := reviewerGroupLetter[lens]
		outputPaths[lens] = filepath.Join(specDir, fmt.Sprintf("review-%s-round-%d.json", letter, state.Round))
	}

	// Dispatch all 4 reviewers in parallel.
	dispatchCfg := ReviewDispatchConfig{
		MaxRetries:     o.config.MaxRetries,
		TimeoutSeconds: 120,
	}
	result, err := DispatchReviewers(o.runner, prompts, outputPaths, dispatchCfg, func(d time.Duration) {})
	if err != nil {
		return fmt.Errorf("dispatch reviewers: %w", err)
	}

	// Accumulate cost.
	state.CumulativeCostUSD += result.TotalCostUSD
	state.AgentInvocations += len(result.Results) + len(result.Failures)

	// Collect successful reviewer outputs for merging.
	var reviewerOutputs []*ReviewerOutput
	for _, r := range result.Results {
		if r.Output != nil {
			reviewerOutputs = append(reviewerOutputs, r.Output)
		}
	}

	if len(reviewerOutputs) == 0 {
		return fmt.Errorf("no reviewer outputs produced")
	}

	// Merge findings.
	merged, err := MergeReviewerOutputs(reviewerOutputs, state.Round)
	if err != nil {
		return fmt.Errorf("merge reviewer outputs: %w", err)
	}

	// Write merged findings.
	mergedPath := filepath.Join(specDir, fmt.Sprintf("merged-findings-round-%d.json", state.Round))
	mergedData, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal merged findings: %w", err)
	}
	if err := os.WriteFile(mergedPath, mergedData, 0o644); err != nil {
		return fmt.Errorf("write merged findings: %w", err)
	}

	// Add findings to tracker.
	o.tracker.AddFindings(merged.Findings)

	// Update state findings summary.
	state.FindingsSummary = o.tracker.GetFindingSummary()

	// Track if critical findings were raised.
	if state.FindingsSummary.OpenCritical > 0 {
		state.HadCriticalFindings = true
	}

	// If zero CRITICAL/MAJOR: skip to JUDGING directly.
	if state.FindingsSummary.OpenCritical == 0 && state.FindingsSummary.OpenMajor == 0 {
		o.logTransition(StateReviewing, StateJudging)
		if err := o.sm.Transition(StateJudging); err != nil {
			return fmt.Errorf("transition REVIEWING -> JUDGING: %w", err)
		}
	} else {
		o.logTransition(StateReviewing, StateRevising)
		if err := o.sm.Transition(StateRevising); err != nil {
			return fmt.Errorf("transition REVIEWING -> REVISING: %w", err)
		}
	}
	return nil
}

// handleRevising dispatches the revision agent to address findings and
// transitions to JUDGING.
func (o *Orchestrator) handleRevising(state *WorkflowStateJSON, specDir string) error {
	specPath := o.currentSpecPath(state)
	mergedPath := filepath.Join(specDir, fmt.Sprintf("merged-findings-round-%d.json", state.Round))

	prompt, err := o.promptBuilder.BuildReviserPrompt(specPath, mergedPath, state.Round)
	if err != nil {
		return fmt.Errorf("build reviser prompt: %w", err)
	}

	outPath := filepath.Join(specDir, fmt.Sprintf("revision-round-%d.json", state.Round))
	cost, duration, err := o.dispatchAgent("reviser", prompt, outPath)
	if err != nil {
		return o.handleAgentError("reviser", err, cost, duration)
	}

	// Parse and apply revision.
	data, err := os.ReadFile(outPath)
	if err != nil {
		return fmt.Errorf("read revision output: %w", err)
	}
	var revision RevisionOutput
	if err := json.Unmarshal(data, &revision); err != nil {
		return fmt.Errorf("parse revision output: %w", err)
	}

	// Apply revision changes to tracker.
	if warnings, err := o.tracker.ApplyRevisionChanges(&revision, state.Round); err != nil {
		return fmt.Errorf("apply revision changes: %w", err)
	} else {
		for _, w := range warnings {
			o.logger.LogAgentError("reviser", "tracker_warning", w)
		}
	}

	// Increment spec version.
	state.CurrentSpecVersion = state.Round

	// Emit spec version event.
	specVersionPath := filepath.Join(specDir, fmt.Sprintf("spec-v%d.md", state.Round))
	o.emitter.Emit(NewSpecVersionEvent(state.Round, state.Round, specVersionPath))

	o.logTransition(StateRevising, StateJudging)
	if err := o.sm.Transition(StateJudging); err != nil {
		return fmt.Errorf("transition REVISING -> JUDGING: %w", err)
	}
	return nil
}

// handleJudging dispatches the judge agent, processes its verdict, checks
// convergence and authority limits, and routes to the next state.
func (o *Orchestrator) handleJudging(state *WorkflowStateJSON, specDir string) error {
	specPath := o.currentSpecPath(state)
	issueTrackerPath := filepath.Join(specDir, fmt.Sprintf("merged-findings-round-%d.json", state.Round))
	revisionPath := filepath.Join(specDir, fmt.Sprintf("revision-round-%d.json", state.Round))

	prompt, err := o.promptBuilder.BuildJudgePrompt(specPath, issueTrackerPath, revisionPath, state.Round)
	if err != nil {
		return fmt.Errorf("build judge prompt: %w", err)
	}

	outPath := filepath.Join(specDir, fmt.Sprintf("judge-round-%d.json", state.Round))
	cost, duration, err := o.dispatchAgent("judge", prompt, outPath)
	if err != nil {
		return o.handleAgentError("judge", err, cost, duration)
	}

	// Parse judge output.
	data, err := os.ReadFile(outPath)
	if err != nil {
		return fmt.Errorf("read judge output: %w", err)
	}
	var judge JudgeOutput
	if err := json.Unmarshal(data, &judge); err != nil {
		return fmt.Errorf("parse judge output: %w", err)
	}

	// Apply judge updates to tracker.
	if warnings, err := o.tracker.ApplyJudgeUpdates(&judge, state.Round); err != nil {
		return fmt.Errorf("apply judge updates: %w", err)
	} else {
		for _, w := range warnings {
			o.logger.LogAgentError("judge", "tracker_warning", w)
		}
	}
	o.tracker.CloseVerifiedFindings(state.Round)

	// Update findings summary.
	state.FindingsSummary = o.tracker.GetFindingSummary()

	// Update issue history for staleness tracking.
	for _, issue := range o.tracker.GetOpenFindings() {
		o.issueHistory[issue.Finding.ID] = append(
			o.issueHistory[issue.Finding.ID],
			string(issue.Status),
		)
	}

	// Check staleness: has any CRITICAL/MAJOR finding been stuck for too long?
	stalenessResult := CheckStaleness(o.issueHistory, o.config.StalenessThreshold)
	if stalenessResult.Triggered {
		o.logger.LogAgentError("orchestrator", "staleness_breaker", stalenessResult.Message)
		o.emitter.Emit(NewCircuitBreakerEvent(stalenessResult.BreakerName, stalenessResult.CurrentValue, stalenessResult.Limit))
		o.escalateFrom(StateJudging)
		return nil
	}

	// Run convergence pre-check + process verdict.
	// Build a dummy revision for rounds where no revision was performed
	// (zero-critical path goes directly to judging).
	var revisionForConvergence *RevisionOutput
	revData, revErr := os.ReadFile(revisionPath)
	if revErr == nil {
		var rev RevisionOutput
		if json.Unmarshal(revData, &rev) == nil {
			revisionForConvergence = &rev
		}
	}
	if revisionForConvergence == nil {
		revisionForConvergence = &RevisionOutput{
			SchemaVersion: "1.0",
			Agent:         "reviser",
			Round:         state.Round,
		}
	}

	convergenceCfg := ConvergenceConfig{
		MinRounds:            o.config.MinRounds,
		CumulativeDowngrades: o.cumulativeDowngrades,
		CumulativeDismissals: o.cumulativeDismissals,
	}
	verdictResult := ProcessVerdict(&judge, o.tracker, revisionForConvergence, state, convergenceCfg)

	// Update cumulative counters.
	o.cumulativeDowngrades += len(judge.Downgrades)
	roundDismissals := 0
	for _, u := range judge.IssueUpdates {
		if u.NewStatus == "dismissed" {
			roundDismissals++
		}
	}
	o.cumulativeDismissals += roundDismissals

	// Check progress.
	snapshot := RoundSnapshot{
		Round:        state.Round,
		OpenCritical: state.FindingsSummary.OpenCritical,
		OpenMajor:    state.FindingsSummary.OpenMajor,
	}
	progressResult := o.progressTracker.RecordRound(snapshot)

	// Emit convergence update.
	o.emitter.Emit(NewConvergenceUpdateEvent(
		state.Round,
		verdictResult.FinalVerdict.String(),
		state.FindingsSummary.OpenCritical,
		state.FindingsSummary.OpenMajor,
		0,
		progressResult.IsProgress,
		judge.Rationale,
	))

	// Log convergence check.
	o.logger.LogConvergenceCheck(
		state.Round,
		state.FindingsSummary.OpenCritical,
		state.FindingsSummary.OpenMajor,
		verdictResult.FinalVerdict.String(),
		progressResult.IsProgress,
	)

	// Check circuit breakers after agent.
	if err := o.checkBreakersAfter(state); err != nil {
		o.escalateFrom(StateJudging)
		return nil
	}

	// Check for progress-based escalation.
	if shouldEscalate, reason := o.progressTracker.ShouldEscalate(); shouldEscalate {
		log.Printf("escalating from JUDGING: progress stalled: %s", reason)
		o.escalateFrom(StateJudging)
		return nil
	}

	// Check authority-based escalation.
	if verdictResult.ShouldEscalate {
		o.escalateFrom(StateJudging)
		return nil
	}

	// Route based on final verdict.
	switch verdictResult.FinalVerdict {
	case VerdictPass:
		if state.HadCriticalFindings {
			o.logTransition(StateJudging, StateHumanGateFinal)
			if err := o.sm.Transition(StateHumanGateFinal); err != nil {
				return fmt.Errorf("transition JUDGING -> HUMAN_GATE_FINAL: %w", err)
			}
		} else {
			o.logTransition(StateJudging, StateFinalized)
			if err := o.sm.Transition(StateFinalized); err != nil {
				return fmt.Errorf("transition JUDGING -> FINALIZED: %w", err)
			}
		}
	case VerdictRevise:
		state.Round++
		o.logTransition(StateJudging, StateReviewing)
		if err := o.sm.Transition(StateReviewing); err != nil {
			return fmt.Errorf("transition JUDGING -> REVIEWING: %w", err)
		}
	case VerdictBlock:
		o.escalateFrom(StateJudging)
	}
	return nil
}

// handleHumanGateFinal presents the final spec for human review and handles
// the accept/reject decision.
func (o *Orchestrator) handleHumanGateFinal(state *WorkflowStateJSON, specDir string) error {
	// Present final spec + critical resolutions.
	o.emitter.Emit(NewGateRequestEvent("final_review", "", state))

	resp := <-o.gateCh

	switch resp.Action {
	case "accept":
		o.logTransition(StateHumanGateFinal, StateFinalized)
		if err := o.sm.Transition(StateFinalized); err != nil {
			return fmt.Errorf("transition HUMAN_GATE_FINAL -> FINALIZED: %w", err)
		}
	case "reject":
		state.Round++
		o.logTransition(StateHumanGateFinal, StateReviewing)
		if err := o.sm.Transition(StateReviewing); err != nil {
			return fmt.Errorf("transition HUMAN_GATE_FINAL -> REVIEWING: %w", err)
		}
	default:
		o.escalateFrom(StateHumanGateFinal)
	}
	return nil
}
