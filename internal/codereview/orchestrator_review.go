package codereview

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/foundry-zero/adversarial-spec-system/internal/specworkflow"
)

// HandleReviewing dispatches parallel code review agents across all 6 lens
// groups (12 agents with codex, 6 without), merges findings using the code
// review dedup key, evaluates convergence, checks circuit breakers and
// staleness, and routes to the next state.
func (o *CodeReviewOrchestrator) HandleReviewing() error {
	if o.sm == nil {
		return fmt.Errorf("orchestrator not started")
	}
	if o.sm.Current() != CRReviewing {
		return fmt.Errorf("workflow is not in CR_REVIEWING (current state: %s)", o.sm.Current())
	}
	if o.runner == nil {
		return fmt.Errorf("no agent runner configured")
	}

	state := o.sm.State()

	// Check circuit breakers before dispatch.
	if err := o.checkBreakers(state); err != nil {
		state.EscalationReason = err.Error()
		return o.sm.Transition(CREscalated)
	}

	// Read spec content if available (for prompts).
	var specContent string
	if state.SpecPath != "" {
		data, err := os.ReadFile(state.SpecPath)
		if err != nil {
			log.Printf("[codereview] warning: cannot read spec %s: %v", state.SpecPath, err)
		} else {
			specContent = string(data)
		}
	}

	// Build prompts and output paths for all lens groups.
	prompts := make(map[string]string)
	outputPaths := make(map[string]string)
	for _, lens := range CodeReviewLensGroups {
		prompts[lens] = BuildReviewerPrompt(lens, state.CodePath, specContent, state.GrillCodeMode)
		outputPaths[lens] = filepath.Join(o.featureDir, fmt.Sprintf("review-%s-claude-round-%d.json", lens, state.Round))
	}

	// Build codex output paths when codex runner is available.
	var codexOutputPaths map[string]string
	if o.codexRunner != nil {
		codexOutputPaths = make(map[string]string)
		for _, lens := range CodeReviewLensGroups {
			codexOutputPaths[lens] = filepath.Join(o.featureDir, fmt.Sprintf("review-%s-codex-round-%d.json", lens, state.Round))
		}
	}

	// Dispatch reviewers in parallel.
	dispatchCfg := specworkflow.ReviewDispatchConfig{
		MaxRetries:     o.config.MaxRetries,
		TimeoutSeconds: o.config.ReviewerTimeoutSeconds,
	}
	// Build onComplete callback to emit agent events.
	var onComplete specworkflow.AgentCompleteFunc
	if o.crEmitter != nil {
		onComplete = func(r specworkflow.ReviewerResult) {
			success := r.Error == nil
			o.crEmitter.EmitAgentComplete(r.AgentName, state.Round, success, r.DurationMS, r.CostUSD)
		}
	}

	// Emit dispatch events for each agent.
	if o.crEmitter != nil {
		for _, lens := range CodeReviewLensGroups {
			o.crEmitter.EmitAgentDispatch(fmt.Sprintf("reviewer-%s-claude", lens), lens, "claude", state.Round)
			if o.codexRunner != nil {
				o.crEmitter.EmitAgentDispatch(fmt.Sprintf("reviewer-%s-codex", lens), lens, "codex", state.Round)
			}
		}
	}

	result, err := specworkflow.DispatchReviewers(
		o.runner, o.codexRunner,
		CodeReviewLensGroups,
		prompts, outputPaths, codexOutputPaths,
		dispatchCfg,
		func(d time.Duration) {},
		onComplete,
		nil, nil,
	)

	if err != nil {
		// Too many failures — escalate.
		state.EscalationReason = fmt.Sprintf("review dispatch failed: %v", err)
		if result != nil {
			state.CumulativeCostUSD += result.TotalCostUSD
			state.AgentInvocations += len(result.Results) + len(result.Failures)
		}
		return o.sm.Transition(CREscalated)
	}

	// Accumulate cost and invocations.
	state.CumulativeCostUSD += result.TotalCostUSD
	state.AgentInvocations += len(result.Results) + len(result.Failures)

	// Track reduced coverage warning — either from agent failures or Codex being unavailable.
	if result.ReducedCoverage {
		state.Warnings = appendUniqueWarning(state.Warnings, "reduced_coverage: Codex provider unavailable, review used Claude only")
		log.Printf("[codereview] reduced coverage: lost agents %v", result.CoverageLoss)
	} else if o.codexRunner == nil {
		state.Warnings = appendUniqueWarning(state.Warnings, "reduced_coverage: Codex provider unavailable, review used Claude only")
	}

	// Collect successful reviewer outputs for merging.
	var reviewerOutputs []*specworkflow.ReviewerOutput
	for _, r := range result.Results {
		if r.Output != nil {
			reviewerOutputs = append(reviewerOutputs, r.Output)
		}
	}

	if len(reviewerOutputs) == 0 {
		state.EscalationReason = "no reviewer outputs produced"
		return o.sm.Transition(CREscalated)
	}

	// Merge findings using code review dedup key (no severity promotion).
	merged, err := specworkflow.MergeReviewerOutputs(
		reviewerOutputs, state.Round,
		CodeReviewDedupKeyFunc(), false,
	)
	if err != nil {
		return fmt.Errorf("merge reviewer outputs: %w", err)
	}

	// Write merged findings.
	findingsPath := filepath.Join(o.featureDir, fmt.Sprintf("code-findings-round-%d.json", state.Round))
	findingsData, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal merged findings: %w", err)
	}
	if err := os.WriteFile(findingsPath, findingsData, 0o644); err != nil {
		return fmt.Errorf("write merged findings: %w", err)
	}

	// Evaluate convergence based on finding severities (no judge).
	verdict := EvaluateConvergence(merged.Findings)
	state.Verdict = verdict

	// Update findings summary.
	state.FindingsSummary = buildFindingsSummary(merged.Findings)

	// Track staleness.
	critMajCount := CountOpenCriticalMajor(merged.Findings)
	o.roundCounts = append(o.roundCounts, critMajCount)

	stale := DetectStaleness(o.roundCounts, o.config.StalenessThreshold)
	if stale {
		state.Warnings = appendUniqueWarning(state.Warnings, "staleness_detected")
		log.Printf("[codereview] staleness detected after %d rounds", len(o.roundCounts))
		// Staleness routes to human gate.
		return o.sm.Transition(CRHumanGateFixes)
	}

	// Route based on verdict.
	switch verdict {
	case CodeReviewVerdictPass:
		return o.sm.Transition(CRComplete)

	case CodeReviewVerdictPassWithObservations:
		return o.sm.Transition(CRHumanGateFixes)

	case CodeReviewVerdictRevise:
		return o.sm.Transition(CRFixing)
	}

	return nil
}

// HandleFixing dispatches the fix agent and routes to the next state based on
// the fix output. This method integrates RunFixPhase into the orchestrator
// lifecycle, updating internal state (lastFixOutput, cost, roundCounts).
func (o *CodeReviewOrchestrator) HandleFixing() error {
	if o.sm == nil {
		return fmt.Errorf("orchestrator not started")
	}
	if o.sm.Current() != CRFixing {
		return fmt.Errorf("workflow is not in CR_FIXING (current state: %s)", o.sm.Current())
	}

	state := o.sm.State()

	// Read spec content for fix prompt.
	var specContent string
	if state.SpecPath != "" {
		if data, err := os.ReadFile(state.SpecPath); err == nil {
			specContent = string(data)
		}
	}

	// Build set of CRITICAL+MAJOR finding IDs from the merged findings file.
	critMajorIDs := make(map[string]bool)
	findingsPath := filepath.Join(o.featureDir, fmt.Sprintf("code-findings-round-%d.json", state.Round))
	if findingsData, readErr := os.ReadFile(findingsPath); readErr == nil {
		var merged specworkflow.MergedFindings
		if jsonErr := json.Unmarshal(findingsData, &merged); jsonErr == nil {
			for _, f := range merged.Findings {
				if f.Severity == specworkflow.SeverityCritical || f.Severity == specworkflow.SeverityMajor {
					critMajorIDs[f.ID] = true
				}
			}
		}
	}

	// Determine which runner to use for fixes. Prefer the dedicated fix runner
	// (restricted --allowedTools), or create one on-demand. Never fall back to
	// the unrestricted review runner — the fix agent MUST run with restricted
	// tool access per spec (FR-018, FR-026).
	fixRunner := o.fixRunner
	if fixRunner == nil && state.CodePath != "" {
		fixRunner = NewFixAgentRunner(state.CodePath, o.config.FixerTimeoutSeconds, o.otelPort, state.FeatureName)
	}
	if fixRunner == nil {
		return fmt.Errorf("no fix agent runner available (code_path is required)")
	}

	cfg := FixPhaseConfig{
		Runner:              fixRunner,
		CodePath:            state.CodePath,
		WorkspaceDir:        o.featureDir,
		Round:               state.Round,
		CommitMode:          state.CommitMode,
		FindingsPath:        findingsPath,
		SpecContent:         specContent,
		FixerTimeoutSeconds: o.config.FixerTimeoutSeconds,
		CriticalMajorIDs:    critMajorIDs,
		HeadSHA:             state.GitHeadSHA,
	}

	result, err := RunFixPhase(cfg)
	if err != nil {
		state.EscalationReason = fmt.Sprintf("fix phase error: %v", err)
		return o.sm.Transition(CREscalated)
	}

	// Update orchestrator state.
	state.CumulativeCostUSD += result.CostUSD
	state.AgentInvocations++
	o.lastFixOutput = result.FixOutput

	// Append warnings.
	for _, w := range result.RouteDecision.Warnings {
		state.Warnings = appendUniqueWarning(state.Warnings, w)
	}

	// Emit fix agent completion event.
	if o.crEmitter != nil {
		o.crEmitter.EmitAgentComplete("fix-agent", state.Round, result.FixOutput != nil, result.DurationMS, result.CostUSD)
	}

	// Transition to the decided next state.
	switch result.RouteDecision.NextState {
	case CRReviewing:
		// Re-review: increment round.
		state.Round++
		return o.sm.Transition(CRReviewing)
	case CRHumanGateFixes:
		return o.sm.Transition(CRHumanGateFixes)
	case CREscalated:
		state.EscalationReason = result.RouteDecision.Reason
		return o.sm.Transition(CREscalated)
	default:
		return o.sm.Transition(CRHumanGateFixes)
	}
}

// checkBreakers validates circuit breakers before agent dispatch.
func (o *CodeReviewOrchestrator) checkBreakers(state *CodeReviewStateJSON) error {
	if state.CumulativeCostUSD > o.config.MaxCostUSD {
		return fmt.Errorf("cost budget exceeded ($%.2f/$%.2f)",
			state.CumulativeCostUSD, float64(o.config.MaxCostUSD))
	}
	limitSeconds := float64(o.config.MaxWallClockMinutes) * 60
	if state.CumulativeWallClockSeconds > limitSeconds {
		return fmt.Errorf("wall clock budget exceeded (%.0fs/%.0fs)",
			state.CumulativeWallClockSeconds, limitSeconds)
	}
	return nil
}

// buildFindingsSummary tallies findings by severity and status.
func buildFindingsSummary(findings []specworkflow.MergedFinding) CodeReviewFindingsSummary {
	var s CodeReviewFindingsSummary
	for i := range findings {
		switch findings[i].Severity {
		case specworkflow.SeverityCritical:
			s.OpenCritical++
		case specworkflow.SeverityMajor:
			s.OpenMajor++
		case specworkflow.SeverityMinor:
			s.OpenMinor++
		case specworkflow.SeverityObservation:
			s.OpenObservation++
		}
	}
	return s
}

