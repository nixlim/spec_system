package specworkflow

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
		letter := reviewerGroupLetter[lens]
		outPath := filepath.Join(specDir, fmt.Sprintf("review-%s-claude-round-%d.json", letter, state.Round))
		p, err := o.promptBuilder.BuildReviewerPrompt(lens, state.Round, specPath, outPath)
		if err != nil {
			return fmt.Errorf("build reviewer prompt for %s: %w", lens, err)
		}
		prompts[lens] = p
		outputPaths[lens] = outPath
	}

	// Build codex output paths when codex runner is available.
	var codexOutputPaths map[string]string
	if o.codexRunner != nil {
		codexOutputPaths = make(map[string]string)
		for _, lens := range []string{"clarity", "consistency", "security", "correctness"} {
			letter := reviewerGroupLetter[lens]
			codexOutputPaths[lens] = filepath.Join(specDir, fmt.Sprintf("review-%s-codex-round-%d.json", letter, state.Round))
		}
	}

	// Emit dispatch events and track active agents so the dashboard shows
	// which agents are currently running.
	for _, lens := range []string{"clarity", "consistency", "security", "correctness"} {
		name := "reviewer-" + lens + "-claude"
		o.SetAgentStatus(name, "running")
		o.emitter.Emit(NewAgentDispatchEvent(name, state.Round))
	}
	if o.codexRunner != nil {
		for _, lens := range []string{"clarity", "consistency", "security", "correctness"} {
			name := "reviewer-" + lens + "-codex"
			o.SetAgentStatus(name, "running")
			o.emitter.Emit(NewAgentDispatchEvent(name, state.Round))
		}
	}

	// Dispatch reviewers in parallel (4 claude + optionally 4 codex).
	// The onComplete callback fires in real-time as each agent finishes,
	// updating the dashboard immediately rather than waiting for all to complete.
	dispatchCfg := ReviewDispatchConfig{
		MaxRetries:     o.config.MaxRetries,
		TimeoutSeconds: o.config.ReviewerTimeoutSeconds,
	}
	round := state.Round
	result, err := DispatchReviewers(o.runner, o.codexRunner, SpecReviewerLensGroups(), prompts, outputPaths, codexOutputPaths, dispatchCfg, func(d time.Duration) {},
		func(r ReviewerResult) {
			success := r.Error == nil
			if success {
				o.SetAgentStatus(r.AgentName, "done")
			} else {
				o.SetAgentStatus(r.AgentName, "failed")
			}
			o.emitter.Emit(NewAgentCompleteEvent(r.AgentName, round, success, r.DurationMS, r.CostUSD))
			if r.Error != nil {
				o.emitter.Emit(NewAgentErrorEvent(r.AgentName, r.Error.Type, r.Error.Detail, r.Error.RetryCount, r.Error.MaxRetries))
			}
		},
	)

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
	merged, err := MergeReviewerOutputs(reviewerOutputs, state.Round, SpecDedupKey, true)
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

	// Dispatch holdout generation agents (dual-provider when codex available).
	if holdoutErr := o.dispatchHoldoutGeneration(state, specDir, merged); holdoutErr != nil {
		log.Printf("[orchestrator] holdout generation escalation: %v", holdoutErr)
		o.escalateFrom(StateReviewing)
		return nil
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

// dispatchHoldoutGeneration dispatches holdout agents (1 claude + optionally
// 1 codex) in parallel, merges their markdown outputs with provider
// attribution, and writes the merged holdout file. If only one provider
// fails, the workflow proceeds with single-provider holdouts. If both
// fail, returns an error for escalation.
func (o *Orchestrator) dispatchHoldoutGeneration(state *WorkflowStateJSON, specDir string, merged *MergedFindings) error {
	specPath := o.currentSpecPath(state)
	mergedFindingsPath := filepath.Join(specDir, fmt.Sprintf("merged-findings-round-%d.json", state.Round))
	timeout := o.config.HoldoutTimeoutSeconds
	maxAttempts := o.config.MaxRetries
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	var wg sync.WaitGroup
	resultsCh := make(chan holdoutDispatchResult, 2)

	// Track and emit dispatch events for holdout agents.
	o.SetAgentStatus("holdout-claude", "running")
	o.emitter.Emit(NewAgentDispatchEvent("holdout-claude", state.Round))
	if o.codexHoldoutRunner != nil {
		o.SetAgentStatus("holdout-codex", "running")
		o.emitter.Emit(NewAgentDispatchEvent("holdout-codex", state.Round))
	}

	// Claude holdout agent — uses o.runner.Run() directly with configured
	// holdout timeout plus explicit JSON contract validation.
	claudeJSONPath := filepath.Join(specDir, fmt.Sprintf("holdout-claude-round-%d.json", state.Round))
	claudeMDPath := filepath.Join(specDir, fmt.Sprintf("holdouts-claude-round-%d.md", state.Round))
	wg.Add(1)
	go func() {
		defer wg.Done()
		resultsCh <- o.runHoldoutProvider("claude", o.runner, specPath, mergedFindingsPath, state.Round, claudeJSONPath, claudeMDPath, timeout, maxAttempts)
	}()

	// Codex holdout agent (when available).
	if o.codexHoldoutRunner != nil {
		codexJSONPath := filepath.Join(specDir, fmt.Sprintf("holdout-codex-round-%d.json", state.Round))
		codexMDPath := filepath.Join(specDir, fmt.Sprintf("holdouts-codex-round-%d.md", state.Round))
		wg.Add(1)
		go func() {
			defer wg.Done()
			resultsCh <- o.runHoldoutProvider("codex", o.codexHoldoutRunner, specPath, mergedFindingsPath, state.Round, codexJSONPath, codexMDPath, timeout, maxAttempts)
		}()
	}

	wg.Wait()
	close(resultsCh)

	// Collect results and emit completion events.
	var claudeOutput, codexOutput *HoldoutOutput
	var claudeMD, codexMD string
	for r := range resultsCh {
		state.CumulativeCostUSD += r.cost
		if r.err != nil {
			log.Printf("[orchestrator] holdout-%s failed: %v", r.provider, r.err)
			o.SetAgentStatus("holdout-"+r.provider, "failed")
			o.emitter.Emit(NewAgentCompleteEvent("holdout-"+r.provider, state.Round, false, 0, r.cost))
			continue
		}
		o.SetAgentStatus("holdout-"+r.provider, "done")
		o.emitter.Emit(NewAgentCompleteEvent("holdout-"+r.provider, state.Round, true, 0, r.cost))
		switch r.provider {
		case "claude":
			claudeOutput = r.output
			claudeMD = r.md
		case "codex":
			codexOutput = r.output
			codexMD = r.md
		}
	}

	// Merge holdout outputs with provider attribution.
	claudeOK := claudeOutput != nil
	codexOK := codexOutput != nil
	if !claudeOK && !codexOK {
		return fmt.Errorf("holdout generation failed: all providers failed — escalation required")
	}

	mergedMD := MergeHoldoutMarkdown(state.Round, claudeMD, codexMD, claudeOK, codexOK)
	mergedPath := filepath.Join(specDir, fmt.Sprintf("holdouts-round-%d.md", state.Round))
	if err := os.WriteFile(mergedPath, []byte(mergedMD), 0o644); err != nil {
		return fmt.Errorf("write merged holdouts: %w", err)
	}
	if _, err := os.Stat(mergedPath); err != nil {
		return fmt.Errorf("merged holdout markdown missing: %w", err)
	}

	finalOutput := mergeHoldoutOutputs(state.Round, mergedPath, claudeOutput, codexOutput)
	if errs := ValidateHoldoutOutput(&finalOutput, state.Round, mergedPath); len(errs) > 0 {
		msgs := make([]string, len(errs))
		for i, err := range errs {
			msgs[i] = err.Error()
		}
		return fmt.Errorf("validate merged holdout output: %s", strings.Join(msgs, "; "))
	}

	finalJSONPath := filepath.Join(specDir, fmt.Sprintf("holdout-round-%d.json", state.Round))
	data, err := json.MarshalIndent(finalOutput, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal merged holdout output: %w", err)
	}
	if err := os.WriteFile(finalJSONPath, data, 0o644); err != nil {
		return fmt.Errorf("write merged holdout output: %w", err)
	}

	log.Printf("[orchestrator] holdout generation complete: claude=%v codex=%v merged=%s",
		claudeOK, codexOK, mergedPath)
	_ = merged.TotalAfterDedup
	return nil
}

type holdoutDispatchResult struct {
	provider string
	output   *HoldoutOutput
	md       string
	cost     float64
	err      error
}

func (o *Orchestrator) runHoldoutProvider(provider string, runner AgentRunner, specPath, mergedFindingsPath string, round int, outputJSONPath, expectedMDPath string, timeout, maxAttempts int) holdoutDispatchResult {
	cfg := ValidateAndRetryConfig{
		AgentName:      "holdout-" + provider,
		MaxAttempts:    maxAttempts,
		OutputPath:     outputJSONPath,
		TimeoutSeconds: timeout,
		Runner:         runner,
		Validator:      HoldoutOutputValidator(round, expectedMDPath),
		BuildPrompt: func(validationErrors []string) string {
			basePrompt, err := o.promptBuilder.BuildHoldoutPrompt(specPath, mergedFindingsPath, round, outputJSONPath, expectedMDPath)
			if err != nil {
				return AppendValidationErrorsToPrompt(fmt.Sprintf("prompt build failed: %v", err), validationErrors)
			}
			return AppendValidationErrorsToPrompt(basePrompt, validationErrors)
		},
	}

	result, err := RunWithValidation(cfg)
	if err != nil {
		return holdoutDispatchResult{provider: provider, err: err}
	}
	if result == nil {
		return holdoutDispatchResult{provider: provider, err: fmt.Errorf("holdout validation returned no result")}
	}
	if result.Data == nil {
		return holdoutDispatchResult{
			provider: provider,
			cost:     result.TotalCost,
			err:      fmt.Errorf("holdout validation failed: %s", strings.Join(result.LastErrors, "; ")),
		}
	}

	var output HoldoutOutput
	if err := json.Unmarshal(result.Data, &output); err != nil {
		return holdoutDispatchResult{
			provider: provider,
			cost:     result.TotalCost,
			err:      fmt.Errorf("parse validated holdout output: %w", err),
		}
	}

	mdData, err := os.ReadFile(output.HoldoutFile)
	if err != nil {
		return holdoutDispatchResult{
			provider: provider,
			cost:     result.TotalCost,
			err:      fmt.Errorf("read holdout markdown: %w", err),
		}
	}

	return holdoutDispatchResult{
		provider: provider,
		output:   &output,
		md:       string(mdData),
		cost:     result.TotalCost,
	}
}

func mergeHoldoutOutputs(round int, mergedPath string, outputs ...*HoldoutOutput) HoldoutOutput {
	categories := make([]string, 0, 8)
	seen := make(map[string]struct{})
	totalScenarios := 0

	for _, output := range outputs {
		if output == nil {
			continue
		}
		totalScenarios += output.ScenarioCount
		for _, category := range output.Categories {
			if _, ok := seen[category]; ok {
				continue
			}
			seen[category] = struct{}{}
			categories = append(categories, category)
		}
	}

	return HoldoutOutput{
		SchemaVersion: "1.0",
		Agent:         "holdout-merge",
		Round:         round,
		ScenarioCount: totalScenarios,
		Categories:    categories,
		HoldoutFile:   mergedPath,
	}
}

// handleRevising dispatches the revision agent to address findings and
// transitions to JUDGING.
func (o *Orchestrator) handleRevising(state *WorkflowStateJSON, specDir string) error {
	specPath := o.currentSpecPath(state)
	mergedPath := filepath.Join(specDir, fmt.Sprintf("merged-findings-round-%d.json", state.Round))
	revisionInputPath := filepath.Join(specDir, fmt.Sprintf("merged-findings-spec-round-%d.json", state.Round))

	if err := writeRevisionFindingsInput(mergedPath, revisionInputPath); err != nil {
		return fmt.Errorf("prepare revision findings: %w", err)
	}

	prompt, err := o.promptBuilder.BuildReviserPrompt(specPath, revisionInputPath, state.Round)
	if err != nil {
		return fmt.Errorf("build reviser prompt: %w", err)
	}

	outPath := filepath.Join(specDir, fmt.Sprintf("revision-round-%d.json", state.Round))

	maxAttempts := o.config.MaxRetries
	if maxAttempts < 2 {
		maxAttempts = 2
	}

	var lastValidationErrors []string
	var revision RevisionOutput
	var data []byte

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		currentPrompt := AppendValidationErrorsToPrompt(prompt, lastValidationErrors)

		cost, duration, dispatchErr := o.dispatchAgent("reviser", currentPrompt, outPath)
		if dispatchErr != nil {
			if attempt < maxAttempts {
				log.Printf("[orchestrator] reviser dispatch failed on attempt %d/%d: %v", attempt, maxAttempts, dispatchErr)
				lastValidationErrors = []string{fmt.Sprintf("agent dispatch failed: %v", dispatchErr)}
				continue
			}
			return o.handleAgentError("reviser", dispatchErr, cost, duration)
		}

		rawData, readErr := os.ReadFile(outPath)
		if readErr != nil {
			if attempt < maxAttempts {
				lastValidationErrors = []string{fmt.Sprintf("output file not created: %v", readErr)}
				continue
			}
			return fmt.Errorf("read revision output: %w", readErr)
		}

		if parseErr := json.Unmarshal(rawData, &revision); parseErr != nil {
			if attempt < maxAttempts {
				log.Printf("[orchestrator] reviser JSON parse failed on attempt %d/%d: %v", attempt, maxAttempts, parseErr)
				lastValidationErrors = []string{fmt.Sprintf("invalid JSON: %v", parseErr)}
				o.emitter.Emit(NewAgentErrorEvent("reviser", "validation_failure", parseErr.Error(), attempt, maxAttempts))
				continue
			}
			return fmt.Errorf("parse revision output: %w", parseErr)
		}

		data = rawData
		break
	}
	_ = data // used for side effects above

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

func writeRevisionFindingsInput(sourcePath, outputPath string) error {
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return err
	}

	var merged MergedFindings
	if err := json.Unmarshal(data, &merged); err != nil {
		return err
	}

	filtered := merged
	filteredFindings := make([]MergedFinding, 0, len(merged.Findings))
	for _, finding := range merged.Findings {
		if normalizeFindingTarget(finding.Target) == "holdout" {
			continue
		}
		finding.Target = normalizeFindingTarget(finding.Target)
		filteredFindings = append(filteredFindings, finding)
	}
	filtered.Findings = filteredFindings
	filtered.TotalAfterDedup = len(filteredFindings)
	filtered.TotalFindings = len(filteredFindings)

	out, err := json.MarshalIndent(filtered, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(outputPath, out, 0o644)
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

	maxAttempts := o.config.MaxRetries
	if maxAttempts < 2 {
		maxAttempts = 2
	}

	var lastValidationErrors []string
	var judge JudgeOutput

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		currentPrompt := AppendValidationErrorsToPrompt(prompt, lastValidationErrors)

		cost, duration, dispatchErr := o.dispatchAgent("judge", currentPrompt, outPath)
		if dispatchErr != nil {
			if attempt < maxAttempts {
				log.Printf("[orchestrator] judge dispatch failed on attempt %d/%d: %v", attempt, maxAttempts, dispatchErr)
				lastValidationErrors = []string{fmt.Sprintf("agent dispatch failed: %v", dispatchErr)}
				continue
			}
			return o.handleAgentError("judge", dispatchErr, cost, duration)
		}

		data, readErr := os.ReadFile(outPath)
		if readErr != nil {
			if attempt < maxAttempts {
				lastValidationErrors = []string{fmt.Sprintf("output file not created: %v", readErr)}
				continue
			}
			return fmt.Errorf("read judge output: %w", readErr)
		}

		if parseErr := json.Unmarshal(data, &judge); parseErr != nil {
			if attempt < maxAttempts {
				log.Printf("[orchestrator] judge JSON parse failed on attempt %d/%d: %v", attempt, maxAttempts, parseErr)
				lastValidationErrors = []string{fmt.Sprintf("invalid JSON: %v", parseErr)}
				o.emitter.Emit(NewAgentErrorEvent("judge", "validation_failure", parseErr.Error(), attempt, maxAttempts))
				continue
			}
			return fmt.Errorf("parse judge output: %w", parseErr)
		}

		break
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
		TotalRaised:          state.FindingsSummary.Raised,
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
		log.Printf("escalating from JUDGING: authority limits exceeded: %s", verdictResult.AuthorityCheck.Reason)
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
	o.emitter.Emit(NewGateRequestEvent("final_review", state.FeatureName, state))

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
