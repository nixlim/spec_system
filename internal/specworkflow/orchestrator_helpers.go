package specworkflow

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// VersionedFilename returns a versioned output filename for dual-provider mode.
// Format: {base}-{provider}-v{version}.{ext}
// Example: VersionedFilename("discovery-output", "claude", 2, ".json") → "discovery-output-claude-v2.json"
func VersionedFilename(base, provider string, version int, ext string) string {
	return fmt.Sprintf("%s-%s-v%d%s", base, provider, version, ext)
}

// VersionedMergedFilename returns a versioned merged output filename for dual-provider mode.
// Format: {base}-merged-v{version}.{ext}
// Example: VersionedMergedFilename("discovery-output", 2, ".json") → "discovery-output-merged-v2.json"
func VersionedMergedFilename(base string, version int, ext string) string {
	return fmt.Sprintf("%s-merged-v%d%s", base, version, ext)
}

// VersionedCombinedFilename returns a versioned combined output filename for dual-provider mode.
// Format: {base}-combined-v{version}.{ext}
// Example: VersionedCombinedFilename("drafter-output", 1, ".json") → "drafter-output-combined-v1.json"
func VersionedCombinedFilename(base string, version int, ext string) string {
	return fmt.Sprintf("%s-combined-v%d%s", base, version, ext)
}

// persistComment appends a human reviewer comment to human-comments.json
// in the spec directory. Comments accumulate across gate interactions so
// downstream agents have the full history. No-op if comment is empty.
// Returns *ErrCommentsCorrupted if the existing file contains invalid JSON.
func persistComment(specDir, gate, action, comment string) error {
	if comment == "" {
		return nil
	}

	entry := CommentEntry{
		Gate:      gate,
		Action:    action,
		Comment:   comment,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	if err := AppendComment(specDir, entry); err != nil {
		return err
	}

	log.Printf("[orchestrator] saved reviewer comment to %s (gate=%s, action=%s)",
		CommentsFilePath(specDir), gate, action)
	return nil
}

// mkdirAll creates multiple directories, returning the first error.
func mkdirAll(dirs ...string) error {
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create directory %s: %w", dir, err)
		}
	}
	return nil
}

// loadSkillsFromConfig loads or creates a minimal SkillCache from config.
func loadSkillsFromConfig(cfg OrchestratorConfig) (*SkillCache, error) {
	if cfg.Config.SkillPaths.PlanSpec != "" && cfg.Config.SkillPaths.GrillSpec != "" {
		skills, err := LoadSkills(cfg.Config.SkillPaths.PlanSpec, cfg.Config.SkillPaths.GrillSpec)
		if err != nil {
			return nil, fmt.Errorf("load skills: %w", err)
		}
		return skills, nil
	}
	// Create a minimal skill cache for testing/defaults.
	return &SkillCache{
		contents:  make(map[string]string),
		checksums: make(map[string]string),
		loaded:    true,
	}, nil
}

// dispatchAgent dispatches a named agent with the given prompt and output
// path using the provided runner. It checks cancellation before dispatch, logs
// the invocation, and accumulates cost in the workflow state. Returns the cost,
// duration, and any error.
func (o *Orchestrator) dispatchAgent(agentName, prompt, outputPath string, runner AgentRunner) (costUSD float64, durationMS int64, err error) {
	if o.cancelled.Load() {
		return 0, 0, fmt.Errorf("workflow cancelled before dispatching %s", agentName)
	}

	state := o.sm.State()

	// Log dispatch.
	o.logger.LogAgentDispatch(agentName, agentName, state.Round)

	// Emit agent dispatch event.
	o.emitter.Emit(NewAgentDispatchEvent(agentName, state.Round))

	exitCode, stderr, cost, duration, runErr := runner.Run(prompt, outputPath, o.config.AgentTimeoutSeconds)
	state.AgentInvocations++
	state.CumulativeCostUSD += cost

	if runErr != nil {
		// Classify the error: check for timeout (context deadline) before
		// falling back to generic crash. The runner returns a descriptive
		// error message that we can inspect.
		errType := classifyRunError(runErr)
		o.logger.LogAgentComplete(agentName, agentName, duration, cost, false)
		o.logger.LogAgentError(agentName, errType, runErr.Error())
		o.emitter.Emit(NewAgentCompleteEvent(agentName, state.Round, false, duration, cost))
		o.emitter.Emit(NewAgentErrorEvent(agentName, errType, runErr.Error(), 0, o.config.MaxRetries))
		return cost, duration, fmt.Errorf("agent %s failed: %s — %s", agentName, errType, runErr.Error())
	}

	// Detect failure type.
	failureType := DetectFailureType(exitCode, stderr, outputPath)
	if failureType != "" {
		o.logger.LogAgentComplete(agentName, agentName, duration, cost, false)
		o.logger.LogAgentError(agentName, failureType, stderr)
		o.emitter.Emit(NewAgentCompleteEvent(agentName, state.Round, false, duration, cost))
		o.emitter.Emit(NewAgentErrorEvent(agentName, failureType, stderr, 0, o.config.MaxRetries))
		return cost, duration, fmt.Errorf("agent %s failed: %s", agentName, failureType)
	}

	o.logger.LogAgentComplete(agentName, agentName, duration, cost, true)
	o.emitter.Emit(NewAgentCompleteEvent(agentName, state.Round, true, duration, cost))
	return cost, duration, nil
}

// handleAgentError determines the recovery action for an agent failure and
// routes to the appropriate recovery strategy: retry, best-effort parse,
// default revise (for judge failures), or escalate.
func (o *Orchestrator) handleAgentError(agentName string, err error, cost float64, duration int64) error {
	// Extract the failure type from the error message if possible.
	// dispatchAgent embeds it as "agent <name> failed: <type>".
	errType := detectErrorType(err.Error())

	agentErr := &AgentError{
		Type:       errType,
		Agent:      agentName,
		Detail:     err.Error(),
		RetryCount: o.config.MaxRetries,
		MaxRetries: o.config.MaxRetries,
	}

	recovery := DetermineRecovery(agentErr, o.sm.Current(), o.sm.State().Round, o.config.MaxRounds)

	switch recovery.Action {
	case ActionBestEffortParse:
		// Try to extract valid findings from the (possibly malformed) output file.
		state := o.sm.State()
		expectedRel := ExpectedOutputFile(o.sm.Current(), o.featureName, state.Round)
		if expectedRel != "" {
			outputPath := filepath.Join(o.workspaceDir, expectedRel)
			data, readErr := os.ReadFile(outputPath)
			if readErr == nil {
				findings, parseErrs := BestEffortParse(data)
				if len(findings) > 0 {
					log.Printf("best-effort parse recovered %d findings from %s (%d errors)",
						len(findings), agentName, len(parseErrs))
					// Convert raw findings to merged findings for the tracker.
					merged := findingsToMerged(findings, agentName, state.Round)
					o.tracker.AddFindings(merged)
					return nil
				}
			}
		}
		// Best-effort also failed — fall through to escalate.
		log.Printf("best-effort parse failed for %s, escalating", agentName)
		o.escalateFrom(o.sm.Current())
		return nil

	case ActionDefaultRevise:
		// Judge produced invalid output; apply a conservative REVISE verdict by
		// incrementing the round and transitioning directly to REVIEWING.
		// We must NOT fall through to ProcessVerdict — judge is zero-valued
		// (VerdictPass = 0) which would produce a false PASS.
		ws := o.sm.State()
		log.Printf("judge failed: applying default REVISE verdict (round %d -> %d)", ws.Round, ws.Round+1)
		ws.Round++
		if transErr := o.sm.Transition(StateReviewing); transErr != nil {
			return fmt.Errorf("default revise transition JUDGING -> REVIEWING: %w", transErr)
		}
		return nil

	case ActionEscalate:
		o.escalateFrom(o.sm.Current())
		return nil

	case ActionRetry:
		// Retry is handled at the dispatch level; if we reach here the
		// retry budget is already exhausted — treat as escalation.
		o.escalateFrom(o.sm.Current())
		return nil

	default:
		return fmt.Errorf("agent %s failed: %w", agentName, err)
	}
}

// classifyRunError determines the failure type from a runner error.
// It inspects the error message for timeout indicators (from context
// deadline exceeded) before falling back to ErrCrash.
func classifyRunError(err error) string {
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "timed out") || strings.Contains(msg, "deadline exceeded") {
		return ErrTimeout
	}
	if containsAny(msg, contextOverflowPatterns) {
		return ErrContextOverflow
	}
	if containsAny(msg, rateLimitPatterns) {
		return ErrRateLimited
	}
	return ErrCrash
}

// detectErrorType extracts the failure type from an error message.
// Falls back to ErrCrash if no known type is found.
func detectErrorType(errMsg string) string {
	knownTypes := []string{
		ErrSchemaViolation, ErrInvalidJSON, ErrMissingOutput,
		ErrTimeout, ErrContextOverflow, ErrRateLimited,
	}
	for _, t := range knownTypes {
		if strings.Contains(errMsg, t) {
			return t
		}
	}
	return ErrCrash
}

// checkBreakersBefore runs pre-dispatch circuit breaker checks (wall clock
// and max rounds). Returns an error if any breaker trips.
func (o *Orchestrator) checkBreakersBefore(state *WorkflowStateJSON) error {
	// Wall clock check.
	wallResult := CheckWallClock(state.StartedAt, o.config.MaxWallClockMinutes)
	if wallResult.Triggered {
		o.emitter.Emit(NewCircuitBreakerEvent(wallResult.BreakerName, wallResult.CurrentValue, wallResult.Limit))
		return fmt.Errorf("circuit breaker tripped: %s", wallResult.Message)
	}

	// Max rounds check.
	roundResult := CheckMaxRounds(state.Round, o.config.MaxRounds)
	if roundResult.Triggered {
		o.emitter.Emit(NewCircuitBreakerEvent(roundResult.BreakerName, roundResult.CurrentValue, roundResult.Limit))
		return fmt.Errorf("circuit breaker tripped: %s", roundResult.Message)
	}

	return nil
}

// checkBreakersAfter runs post-dispatch circuit breaker checks (cost and
// total findings). Returns an error if any breaker trips.
func (o *Orchestrator) checkBreakersAfter(state *WorkflowStateJSON) error {
	// Cost check.
	costResult := CheckCost(state.CumulativeCostUSD, o.config.MaxCostUSD)
	if costResult.Triggered {
		o.emitter.Emit(NewCircuitBreakerEvent(costResult.BreakerName, costResult.CurrentValue, costResult.Limit))
		return fmt.Errorf("circuit breaker tripped: %s", costResult.Message)
	}

	// Total findings check.
	findingsResult := CheckMaxFindings(state.FindingsSummary.Raised, o.config.MaxTotalFindings)
	if findingsResult.Triggered {
		o.emitter.Emit(NewCircuitBreakerEvent(findingsResult.BreakerName, findingsResult.CurrentValue, findingsResult.Limit))
		return fmt.Errorf("circuit breaker tripped: %s", findingsResult.Message)
	}

	return nil
}

// currentSpecPath returns the path to the current spec version file.
func (o *Orchestrator) currentSpecPath(state *WorkflowStateJSON) string {
	specDir := filepath.Join(o.workspaceDir, "specs", o.featureName)
	if state.CurrentSpecVersion == 0 {
		return filepath.Join(specDir, "spec-v0.md")
	}
	return filepath.Join(specDir, fmt.Sprintf("spec-v%d.md", state.CurrentSpecVersion))
}

// logTransition logs a state transition event.
func (o *Orchestrator) logTransition(from, to WorkflowState) {
	o.logger.LogStateTransition(from, to, o.sm.State().Round)
	o.emitter.Emit(NewStateTransitionEvent(from.String(), to.String(), o.sm.State().Round))
}

// findingsToMerged converts raw Finding values recovered by BestEffortParse
// into MergedFinding values suitable for IssueTracker.AddFindings.
func findingsToMerged(findings []Finding, agent string, round int) []MergedFinding {
	merged := make([]MergedFinding, 0, len(findings))
	for _, f := range findings {
		merged = append(merged, MergedFinding{
			ID:              f.ID,
			SourceIDs:       []string{f.ID},
			RaisedBy:        []string{agent},
			Description:     f.Description,
			Severity:        f.Severity,
			Impact:          f.Impact,
			Recommendation:  f.Recommendation,
			Lens:            f.Lens,
			AffectedSection: f.AffectedSection,
			Target:          normalizeFindingTarget(f.Target),
			Status:          "open",
			RoundRaised:     round,
		})
	}
	return merged
}

// escalateFrom transitions to ESCALATED from the current state. If the
// direct transition is not valid (e.g. from REVIEWING or REVISING), it
// goes through ERROR first using the universal any->ERROR rule.
// Closes any open gate proxies before escalating (CD-88v.14).
func (o *Orchestrator) escalateFrom(from WorkflowState) {
	o.closeOpenGateProxies(from.String())
	if isValidTransition(from, StateEscalated) {
		o.logTransition(from, StateEscalated)
		if err := o.sm.Transition(StateEscalated); err != nil {
			log.Printf("warning: transition %s -> ESCALATED failed: %v", from, err)
		}
	} else {
		// Route through ERROR (universal rule: any->ERROR, ERROR->any).
		o.logTransition(from, StateError)
		if err := o.sm.Transition(StateError); err != nil {
			log.Printf("warning: transition %s -> ERROR failed: %v", from, err)
		}
		o.logTransition(StateError, StateEscalated)
		if err := o.sm.Transition(StateEscalated); err != nil {
			log.Printf("warning: transition ERROR -> ESCALATED failed: %v", err)
		}
	}
}
