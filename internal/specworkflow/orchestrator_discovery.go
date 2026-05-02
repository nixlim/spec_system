package specworkflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// discoveryResult holds the outcome of a single discovery agent dispatch.
type discoveryResult struct {
	provider string
	output   *DiscoveryOutput
	data     []byte
	cost     float64
	duration int64
	err      error
}

// DiscoveryResumeData describes existing discovery artefacts found on disk
// when the orchestrator enters the DISCOVERY state. It is emitted as gate
// data so the UI can present resume options to the user.
type DiscoveryResumeData struct {
	// HasMergedOutput is true when a canonical discovery-output.json exists
	// and contains valid JSON.
	HasMergedOutput bool `json:"has_merged_output"`
	// HasClaudeOutput is true when a per-provider Claude output exists.
	HasClaudeOutput bool `json:"has_claude_output"`
	// HasCodexOutput is true when a per-provider Codex output exists.
	HasCodexOutput bool `json:"has_codex_output"`
	// CanReplayMerge is true when both per-provider outputs exist (merge
	// can be replayed without re-dispatching agents).
	CanReplayMerge bool `json:"can_replay_merge"`
	// Round is the discovery round these artefacts belong to.
	Round int `json:"round"`
	// MergedPreview is a summary of the merged output (actor count, etc.)
	// so the user can decide without reading the full file.
	MergedPreview *DiscoveryOutputSummary `json:"merged_preview,omitempty"`
}

// DiscoveryOutputSummary is a lightweight summary of a DiscoveryOutput
// for display in gate UIs without transmitting the full output.
type DiscoveryOutputSummary struct {
	ActorCount        int `json:"actor_count"`
	PriorityCount     int `json:"priority_count"`
	OpenQuestionCount int `json:"open_question_count"`
	ConstraintCount   int `json:"constraint_count"`
}

// checkDiscoveryArtefacts probes the spec directory for existing discovery
// outputs from a previous run. Returns nil when no artefacts are found.
func checkDiscoveryArtefacts(specDir string, round int) *DiscoveryResumeData {
	data := &DiscoveryResumeData{Round: round}

	// Check canonical merged output.
	canonicalPath := filepath.Join(specDir, "discovery-output.json")
	if raw, err := os.ReadFile(canonicalPath); err == nil {
		var out DiscoveryOutput
		if json.Unmarshal(raw, &out) == nil && out.SchemaVersion != "" {
			data.HasMergedOutput = true
			data.MergedPreview = &DiscoveryOutputSummary{
				ActorCount:        len(out.Actors),
				PriorityCount:     len(out.Priorities),
				OpenQuestionCount: len(out.OpenQuestions),
				ConstraintCount:   len(out.Constraints),
			}
		}
	}

	// Check per-provider outputs (try current round, then round 1 fallback).
	for _, r := range []int{round, 1} {
		claudePath := filepath.Join(specDir, VersionedFilename("discovery-output", "claude", r, ".json"))
		codexPath := filepath.Join(specDir, VersionedFilename("discovery-output", "codex", r, ".json"))
		if _, err := os.Stat(claudePath); err == nil {
			data.HasClaudeOutput = true
		}
		if _, err := os.Stat(codexPath); err == nil {
			data.HasCodexOutput = true
		}
		if data.HasClaudeOutput || data.HasCodexOutput {
			break
		}
	}

	data.CanReplayMerge = data.HasClaudeOutput && data.HasCodexOutput

	// Nothing found — return nil to signal no artefacts.
	if !data.HasMergedOutput && !data.HasClaudeOutput && !data.HasCodexOutput {
		return nil
	}

	return data
}

// handleDiscovery builds the discovery prompt, dispatches discovery agent(s),
// validates output(s), and transitions to HUMAN_GATE_1. When existing
// artefacts are found from a previous run (e.g. after rewind), the user is
// offered a choice: restart from scratch, replay the merge step, or skip
// straight to the human gate with the existing merged output.
//
// When EnableCodexDiscovery is true and a codex discovery runner is available,
// both Claude and Codex run in parallel with AgentTimeoutSeconds timeout.
// Outputs are merged via MergeDiscoveryOutputs. On single-provider failure,
// the survivor is used. On both failures, the workflow escalates.
func (o *Orchestrator) handleDiscovery(goal GoalInput, state *WorkflowStateJSON, specDir string) error {
	discoveryRound := state.Gate1CorrectionCount + 1

	// Smart restart: check for existing discovery artefacts before dispatching.
	// Skip the resume gate when this is a correction re-entry (user just provided
	// corrections at HUMAN_GATE_1 and we're re-running with their feedback).
	// Gate1CorrectionCount > 0 means we've been through at least one correction
	// loop in this workflow run — artefacts are expected and should be overwritten.
	isCorrectingLoop := state.Gate1CorrectionCount > 0
	resumeData := checkDiscoveryArtefacts(specDir, discoveryRound)
	if resumeData != nil && !isCorrectingLoop {
		log.Printf("[orchestrator] existing discovery artefacts found: merged=%v, claude=%v, codex=%v, can_replay_merge=%v",
			resumeData.HasMergedOutput, resumeData.HasClaudeOutput, resumeData.HasCodexOutput, resumeData.CanReplayMerge)

		// Emit a discovery_resume gate so the user can choose how to proceed.
		o.emitter.Emit(NewGateRequestEvent("discovery_resume", state.FeatureName, resumeData))

		// Wait for the user's choice.
		resp := <-o.gateCh
		log.Printf("[orchestrator] discovery resume response: action=%s", resp.Action)

		switch resp.Action {
		case "skip_to_gate":
			// Jump directly to HUMAN_GATE_1 with existing merged output.
			if resumeData.HasMergedOutput {
				log.Printf("[orchestrator] skipping discovery — using existing merged output")
				o.emitter.Emit(NewGateResponseEvent("discovery_resume", "skip_to_gate",
					"Using existing discovery output — skipping to human gate"))
				o.logTransition(StateDiscovery, StateHumanGate1)
				if err := o.sm.Transition(StateHumanGate1); err != nil {
					return fmt.Errorf("transition DISCOVERY -> HUMAN_GATE_1 (skip): %w", err)
				}
				return nil
			}
			// No merged output — fall through to replay merge if possible.
			log.Printf("[orchestrator] skip_to_gate requested but no merged output — trying replay_merge")
			fallthrough

		case "replay_merge":
			// Replay the merge step using existing per-provider outputs.
			if resumeData.CanReplayMerge {
				log.Printf("[orchestrator] replaying discovery merge from existing per-provider outputs")
				o.emitter.Emit(NewGateResponseEvent("discovery_resume", "replay_merge",
					"Replaying merge from existing Claude and Codex outputs"))
				msg, replayErr := ReplayDiscoveryMerge(o.runner, specDir, discoveryRound, o.config.AgentTimeoutSeconds)
				if replayErr != nil {
					log.Printf("[orchestrator] replay merge failed: %v — falling through to fresh dispatch", replayErr)
					// Fall through to fresh dispatch below.
				} else {
					log.Printf("[orchestrator] replay merge succeeded: %s", msg)
					o.logTransition(StateDiscovery, StateHumanGate1)
					if err := o.sm.Transition(StateHumanGate1); err != nil {
						return fmt.Errorf("transition DISCOVERY -> HUMAN_GATE_1 (replay): %w", err)
					}
					return nil
				}
			} else {
				log.Printf("[orchestrator] replay_merge requested but per-provider outputs not available — running fresh")
			}
			// Fall through to fresh dispatch.
			fallthrough

		case "restart_fresh":
			// User chose to re-run discovery from scratch. Continue below.
			log.Printf("[orchestrator] user chose fresh discovery restart")
			o.emitter.Emit(NewGateResponseEvent("discovery_resume", "restart_fresh",
				"Re-running discovery from scratch"))

		default:
			// Unknown action — treat as restart_fresh to be safe.
			log.Printf("[orchestrator] unknown discovery resume action %q — defaulting to fresh restart", resp.Action)
		}
	}

	// Load the full correction history from numbered files
	// (gate1-corrections-1.json, gate1-corrections-2.json, ...).
	discoveryCtx := loadCorrectionHistory(specDir)

	prompt, err := o.promptBuilder.BuildDiscoveryPrompt(goal.SourceDocPaths, goal.CodePath, &goal, discoveryCtx...)
	if err != nil {
		return fmt.Errorf("build discovery prompt: %w", err)
	}

	// Multi-provider path: dispatch Claude + Codex + optionally OpenCode in parallel.
	if (o.config.EnableCodexDiscovery && o.codexDiscoveryRunner != nil) ||
		(o.config.EnableOpenCodeDiscovery && o.opencodeDiscoveryRunner != nil) {
		return o.handleDualDiscovery(prompt, state, specDir, discoveryRound, goal.SourceDocPaths)
	}

	// Single-provider path (current behavior).
	return o.handleSingleDiscovery(prompt, state, specDir, discoveryRound, goal.SourceDocPaths)
}

// handleSingleDiscovery is the single-provider discovery path with
// validation+retry. If the agent produces invalid JSON, the prompt is
// augmented with validation errors and the agent is re-dispatched.
func (o *Orchestrator) handleSingleDiscovery(prompt string, state *WorkflowStateJSON, specDir string, discoveryRound int, sourceDocPaths []string) error {
	outPath := filepath.Join(specDir, "discovery-output.json")

	maxAttempts := o.config.MaxRetries
	if maxAttempts < 2 {
		maxAttempts = 2
	}

	// Use --json-schema to enforce structured output when the runner supports it.
	discoveryRunner := o.runnerFor("discovery")
	if cr, ok := discoveryRunner.(*ClaudeRunner); ok {
		discoveryRunner = cr.WithJSONSchema(string(DiscoveryOutputSchema()))
	}

	var lastValidationErrors []string
	var discovery *DiscoveryOutput
	var data []byte

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		currentPrompt := AppendValidationErrorsToPrompt(prompt, lastValidationErrors)

		cost, duration, err := o.dispatchAgent("discovery", currentPrompt, outPath, discoveryRunner)
		if err != nil {
			if errors.Is(err, ErrProcessKilled) {
				return o.handleAgentError("discovery", err, cost, duration)
			}
			if attempt < maxAttempts {
				log.Printf("[orchestrator] discovery dispatch failed on attempt %d/%d: %v", attempt, maxAttempts, err)
				lastValidationErrors = []string{fmt.Sprintf("agent dispatch failed: %v", err)}
				continue
			}
			return o.handleAgentError("discovery", err, cost, duration)
		}

		d, rawData, parseErr := parseAndValidateDiscoveryOutput(outPath)
		if parseErr != nil {
			if attempt < maxAttempts {
				log.Printf("[orchestrator] discovery validation failed on attempt %d/%d: %v", attempt, maxAttempts, parseErr)
				lastValidationErrors = []string{parseErr.Error()}
				o.emitter.Emit(NewAgentErrorEvent("discovery", "validation_failure", parseErr.Error(), attempt, maxAttempts))
				continue
			}
			return parseErr
		}

		discovery = d
		data = rawData
		break
	}

	log.Printf("[orchestrator] discovery output valid: %d actors, %d priorities, %d open questions",
		len(discovery.Actors), len(discovery.Priorities), len(discovery.OpenQuestions))

	// Gate 1 heuristic: if the source corpus is large but discovery produced
	// few substantive questions, prepend a synthetic warning to OpenQuestions
	// so the human reviewer sees it on the first gate render. This catches
	// the "over-confident empty gate" failure mode.
	if discovery != nil {
		if newData, injected := o.maybeInjectGate1HeuristicWarning(sourceDocPaths, discovery); injected {
			canonicalPath := filepath.Join(specDir, "discovery-output.json")
			if writeErr := os.WriteFile(canonicalPath, newData, 0o644); writeErr != nil {
				log.Printf("[orchestrator] warning: failed to rewrite canonical discovery output after heuristic injection: %v", writeErr)
			} else {
				data = newData
			}
		}
	}

	// Save a numbered copy for history (discovery-output-{N}.json).
	versionedPath := filepath.Join(specDir, fmt.Sprintf("discovery-output-%d.json", discoveryRound))
	if copyErr := os.WriteFile(versionedPath, data, 0o644); copyErr != nil {
		log.Printf("[orchestrator] warning: failed to write versioned discovery output %s: %v", versionedPath, copyErr)
	}

	o.logTransition(StateDiscovery, StateHumanGate1)
	if err := o.sm.Transition(StateHumanGate1); err != nil {
		return fmt.Errorf("transition DISCOVERY -> HUMAN_GATE_1: %w", err)
	}
	return nil
}

// handleDualDiscovery dispatches both Claude and Codex discovery agents in
// parallel, merges successful outputs, and handles fallback/escalation.
func (o *Orchestrator) handleDualDiscovery(prompt string, state *WorkflowStateJSON, specDir string, discoveryRound int, sourceDocPaths []string) error {
	timeout := o.config.AgentTimeoutSeconds

	claudeOutPath := filepath.Join(specDir, VersionedFilename("discovery-output", "claude", discoveryRound, ".json"))
	codexOutPath := filepath.Join(specDir, VersionedFilename("discovery-output", "codex", discoveryRound, ".json"))

	// Track and emit dispatch events.
	o.SetAgentStatus("discovery-claude", "running")
	o.emitter.Emit(NewAgentDispatchEvent("discovery-claude", state.Round))
	if o.codexDiscoveryRunner != nil {
		o.SetAgentStatus("discovery-codex", "running")
		o.emitter.Emit(NewAgentDispatchEvent("discovery-codex", state.Round))
	}
	if o.opencodeDiscoveryRunner != nil {
		o.SetAgentStatus("discovery-opencode", "running")
		o.emitter.Emit(NewAgentDispatchEvent("discovery-opencode", state.Round))
	}

	providerCount := 1 // claude always
	if o.codexDiscoveryRunner != nil {
		providerCount++
	}
	if o.opencodeDiscoveryRunner != nil {
		providerCount++
	}
	resultsCh := make(chan discoveryResult, providerCount)
	var wg sync.WaitGroup

	// Build a schema-bound Claude runner for structured output.
	var discoveryClaudeRunner AgentRunner = o.runnerFor("discovery")
	if cr, ok := discoveryClaudeRunner.(*ClaudeRunner); ok {
		discoveryClaudeRunner = cr.WithJSONSchema(string(DiscoveryOutputSchema()))
	}

	// Claude discovery agent.
	wg.Add(1)
	go func() {
		defer wg.Done()
		o.logger.LogAgentDispatch("discovery-claude", "discovery-claude", state.Round)
		exitCode, stderr, cost, duration, runErr := discoveryClaudeRunner.Run(prompt, claudeOutPath, timeout)
		var dispatchErr error
		if runErr != nil {
			dispatchErr = runErr
		} else {
			failureType := DetectFailureType(exitCode, stderr, claudeOutPath)
			if failureType != "" {
				dispatchErr = fmt.Errorf("discovery-claude failed: %s", failureType)
			}
		}
		resultsCh <- discoveryResult{provider: "claude", cost: cost, duration: duration, err: dispatchErr}
	}()

	// Codex discovery agent (when available).
	if o.codexDiscoveryRunner != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			o.logger.LogAgentDispatch("discovery-codex", "discovery-codex", state.Round)
			exitCode, stderr, cost, duration, runErr := o.codexDiscoveryRunner.Run(prompt, codexOutPath, timeout)
			var dispatchErr error
			if runErr != nil {
				dispatchErr = runErr
			} else {
				failureType := DetectFailureType(exitCode, stderr, codexOutPath)
				if failureType != "" {
					dispatchErr = fmt.Errorf("discovery-codex failed: %s", failureType)
				}
			}
			resultsCh <- discoveryResult{provider: "codex", cost: cost, duration: duration, err: dispatchErr}
		}()
	}

	// OpenCode discovery agent (when available).
	opencodeOutPath := filepath.Join(specDir, VersionedFilename("discovery-output", "opencode", discoveryRound, ".json"))
	if o.opencodeDiscoveryRunner != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			o.logger.LogAgentDispatch("discovery-opencode", "discovery-opencode", state.Round)
			opencodeRunner := taggedRunner(o.opencodeDiscoveryRunner, "discovery-opencode")
			exitCode, stderr, cost, duration, runErr := opencodeRunner.Run(prompt, opencodeOutPath, timeout)
			var dispatchErr error
			if runErr != nil {
				dispatchErr = runErr
			} else {
				failureType := DetectFailureType(exitCode, stderr, opencodeOutPath)
				if failureType != "" {
					dispatchErr = fmt.Errorf("discovery-opencode failed: %s", failureType)
				}
			}
			resultsCh <- discoveryResult{provider: "opencode", cost: cost, duration: duration, err: dispatchErr}
		}()
	}

	wg.Wait()
	close(resultsCh)

	// Collect results and accumulate cost after all goroutines complete
	// to avoid data races on shared state fields.
	var claudeResult, codexResult, opencodeResult *discoveryResult
	for r := range resultsCh {
		r := r
		state.AgentInvocations++
		state.CumulativeCostUSD += r.cost
		switch r.provider {
		case "claude":
			claudeResult = &r
		case "codex":
			codexResult = &r
		case "opencode":
			opencodeResult = &r
		}
	}

	// Emit completion events.
	emitDiscoveryComplete := func(provider string, r *discoveryResult) {
		if r == nil {
			return
		}
		success := r.err == nil
		if success {
			o.SetAgentStatus("discovery-"+provider, "done")
		} else {
			o.SetAgentStatus("discovery-"+provider, "failed")
			log.Printf("[orchestrator] discovery-%s failed: %v", provider, r.err)
		}
		o.emitter.Emit(NewAgentCompleteEvent("discovery-"+provider, state.Round, success, r.duration, r.cost))
	}
	emitDiscoveryComplete("claude", claudeResult)
	emitDiscoveryComplete("codex", codexResult)
	emitDiscoveryComplete("opencode", opencodeResult)

	// Parse successful outputs.
	var claudeOutput, codexOutput, opencodeOutput *DiscoveryOutput
	var claudeData, codexData, opencodeData []byte

	if claudeResult != nil && claudeResult.err == nil {
		co, data, parseErr := parseAndValidateDiscoveryOutput(claudeOutPath)
		if parseErr != nil {
			log.Printf("[orchestrator] discovery-claude: output parse/validation failed: %v", parseErr)
			claudeResult.err = parseErr
		} else {
			claudeOutput = co
			claudeData = data
		}
	}

	if codexResult != nil && codexResult.err == nil {
		co, data, parseErr := parseAndValidateDiscoveryOutput(codexOutPath)
		if parseErr != nil {
			log.Printf("[orchestrator] discovery-codex: output parse/validation failed: %v", parseErr)
			codexResult.err = parseErr
		} else {
			codexOutput = co
			codexData = data
		}
	}

	if opencodeResult != nil && opencodeResult.err == nil {
		co, data, parseErr := parseAndValidateDiscoveryOutput(opencodeOutPath)
		if parseErr != nil {
			log.Printf("[orchestrator] discovery-opencode: output parse/validation failed: %v", parseErr)
			opencodeResult.err = parseErr
		} else {
			opencodeOutput = co
			opencodeData = data
		}
	}

	// All failed -> escalate.
	if claudeOutput == nil && codexOutput == nil && opencodeOutput == nil {
		reason := "all discovery agents failed:"
		if claudeResult != nil {
			reason += fmt.Sprintf(" claude=%v", claudeResult.err)
		}
		if codexResult != nil {
			reason += fmt.Sprintf(" codex=%v", codexResult.err)
		}
		if opencodeResult != nil {
			reason += fmt.Sprintf(" opencode=%v", opencodeResult.err)
		}
		log.Printf("[orchestrator] %s", reason)
		o.escalateFrom(StateDiscovery)
		return nil
	}

	// Collect all successful outputs for merging.
	type providerOutput struct {
		output *DiscoveryOutput
		data   []byte
		name   string
	}
	var successfulOutputs []providerOutput
	if claudeOutput != nil {
		successfulOutputs = append(successfulOutputs, providerOutput{claudeOutput, claudeData, "claude"})
	}
	if codexOutput != nil {
		successfulOutputs = append(successfulOutputs, providerOutput{codexOutput, codexData, "codex"})
	}
	if opencodeOutput != nil {
		successfulOutputs = append(successfulOutputs, providerOutput{opencodeOutput, opencodeData, "opencode"})
	}

	// Determine final output.
	var finalOutput *DiscoveryOutput
	var finalData []byte

	if len(successfulOutputs) >= 2 {
		// Multiple providers succeeded -> merge pairwise.
		// Start with first two via agent-based merge, then fold in remaining.
		mergedPath := filepath.Join(specDir, VersionedMergedFilename("discovery-output", discoveryRound, ".json"))
		mergedData, mergeErr := o.mergeDiscoveryWithAgent(successfulOutputs[0].data, successfulOutputs[1].data, mergedPath, state)
		if mergeErr != nil {
			log.Printf("[orchestrator] agent-based merge failed (%v), falling back to mechanical merge", mergeErr)
			finalOutput = MergeDiscoveryOutputs(successfulOutputs[0].output, successfulOutputs[1].output)
			fb, _ := json.MarshalIndent(finalOutput, "", "  ")
			finalData = fb
		} else {
			finalData = mergedData
			var fo DiscoveryOutput
			if jsonErr := json.Unmarshal(mergedData, &fo); jsonErr == nil {
				finalOutput = &fo
			}
		}
		// Fold in third provider if present via mechanical merge.
		if len(successfulOutputs) == 3 && finalOutput != nil {
			finalOutput = MergeDiscoveryOutputs(finalOutput, successfulOutputs[2].output)
			fb, _ := json.MarshalIndent(finalOutput, "", "  ")
			finalData = fb
		}
		if finalOutput != nil {
			log.Printf("[orchestrator] multi-provider discovery merged: %d actors, %d priorities, %d open questions",
				len(finalOutput.Actors), len(finalOutput.Priorities), len(finalOutput.OpenQuestions))
		}
	} else {
		// Single provider succeeded.
		finalOutput = successfulOutputs[0].output
		finalData = successfulOutputs[0].data
		log.Printf("[orchestrator] only %s discovery succeeded, using its output", successfulOutputs[0].name)
	}
	// Gate 1 heuristic: inject synthetic warning into OpenQuestions when
	// discovery produced few substantive questions against a large source
	// corpus. Must run BEFORE writing the canonical file and the versioned
	// history copy, so both reflect the same augmented output.
	if finalOutput != nil {
		if newData, injected := o.maybeInjectGate1HeuristicWarning(sourceDocPaths, finalOutput); injected {
			finalData = newData
		}
	}
	_ = finalOutput // used implicitly through finalData

	// Write the canonical discovery-output.json (for backward compat with gate handler).
	canonicalPath := filepath.Join(specDir, "discovery-output.json")
	if err := os.WriteFile(canonicalPath, finalData, 0o644); err != nil {
		return fmt.Errorf("write discovery-output.json: %w", err)
	}

	// Save a numbered copy for history.
	versionedPath := filepath.Join(specDir, fmt.Sprintf("discovery-output-%d.json", discoveryRound))
	if copyErr := os.WriteFile(versionedPath, finalData, 0o644); copyErr != nil {
		log.Printf("[orchestrator] warning: failed to write versioned discovery output %s: %v", versionedPath, copyErr)
	}

	// Also write the per-provider versioned files (data already written by agents).
	// Just persist the raw data if not already on disk (agent wrote to these paths).
	if claudeData != nil {
		_ = os.WriteFile(claudeOutPath, claudeData, 0o644)
	}
	if codexData != nil {
		_ = os.WriteFile(codexOutPath, codexData, 0o644)
	}
	if opencodeData != nil {
		_ = os.WriteFile(opencodeOutPath, opencodeData, 0o644)
	}

	o.logTransition(StateDiscovery, StateHumanGate1)
	if err := o.sm.Transition(StateHumanGate1); err != nil {
		return fmt.Errorf("transition DISCOVERY -> HUMAN_GATE_1: %w", err)
	}
	return nil
}

// gate1HeuristicWarningPrefix marks the synthetic warning message injected
// into OpenQuestions when discovery produced suspiciously few substantive
// questions against a large source corpus. The prefix makes the warning
// identifiable in downstream UI rendering and prevents it from being counted
// as a "substantive" question on a subsequent heuristic re-check.
const gate1HeuristicWarningPrefix = "[SYSTEM WARNING]"

// gate1ProceduralQuestionMarkers are substrings that make an open question
// qualify as procedural (about the workflow/output format/pipeline) rather
// than substantive (about the feature being specified). Case-insensitive.
var gate1ProceduralQuestionMarkers = []string{
	"schema",
	"output format",
	"workflow",
	"additional source doc",
	"plan-spec",
	"which file",
	"output path",
}

// countSourceDocLines returns the total line count across all files listed
// in sourceDocPaths. Files that cannot be read are skipped silently — this
// is a best-effort heuristic, not a correctness check.
func countSourceDocLines(sourceDocPaths []string) int {
	total := 0
	for _, p := range sourceDocPaths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		total += strings.Count(string(data), "\n")
		if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
			total++
		}
	}
	return total
}

// isSubstantiveQuestion returns true if q is about the feature being
// specified (e.g. data model, failure handling, infrastructure choices) and
// false if it only asks about the workflow pipeline itself (schema paths,
// output formats, etc.). The heuristic is conservative: any question
// containing one of gate1ProceduralQuestionMarkers AND under 120 characters
// is treated as procedural. A long question that mentions "schema" in
// passing (e.g. "should the Neo4j schema enforce uniqueness on datom IDs?")
// is still substantive.
func isSubstantiveQuestion(q string) bool {
	if strings.HasPrefix(q, gate1HeuristicWarningPrefix) {
		return false
	}
	lower := strings.ToLower(q)
	if len(q) >= 120 {
		return true
	}
	for _, marker := range gate1ProceduralQuestionMarkers {
		if strings.Contains(lower, marker) {
			return false
		}
	}
	return true
}

// maybeInjectGate1HeuristicWarning audits the merged discovery output
// against a heuristic for "suspiciously confident" results: if the source
// corpus is > gate1MinSourceLines lines AND fewer than
// gate1MinSubstantiveQuestions substantive questions were produced, prepend
// a synthetic warning to discovery.OpenQuestions asking the human to verify
// that operational topics are covered before confirming the gate.
//
// Returns (re-marshalled JSON bytes, true) when a warning was injected,
// or (nil, false) when the heuristic did not trigger or injection failed.
// The caller is responsible for persisting the returned bytes to disk
// BEFORE the StateHumanGate1 transition, so the UI sees the warning on
// the first gate render.
func (o *Orchestrator) maybeInjectGate1HeuristicWarning(sourceDocPaths []string, discovery *DiscoveryOutput) ([]byte, bool) {
	const gate1MinSourceLines = 500
	const gate1MinSubstantiveQuestions = 3

	sourceLines := countSourceDocLines(sourceDocPaths)
	if sourceLines < gate1MinSourceLines {
		return nil, false
	}

	substantive := 0
	for _, q := range discovery.OpenQuestions {
		if isSubstantiveQuestion(q) {
			substantive++
		}
	}
	if substantive >= gate1MinSubstantiveQuestions {
		return nil, false
	}

	warning := fmt.Sprintf(
		"%s Discovery produced only %d substantive questions against a source corpus of %d lines. "+
			"Please verify that operational topics (infrastructure, deployment, setup, tech stack) "+
			"are adequately covered in the scope, constraints, and integration_points before "+
			"confirming. If you notice anything missing, reject this gate with corrections.",
		gate1HeuristicWarningPrefix, substantive, sourceLines,
	)
	discovery.OpenQuestions = append([]string{warning}, discovery.OpenQuestions...)

	newData, marshalErr := json.MarshalIndent(discovery, "", "  ")
	if marshalErr != nil {
		log.Printf("[orchestrator] gate1 heuristic: failed to re-marshal discovery output: %v", marshalErr)
		return nil, false
	}

	log.Printf("[orchestrator] gate1 heuristic triggered: source=%d lines, substantive_questions=%d — injected warning",
		sourceLines, substantive)
	return newData, true
}

// buildDiscoveryMergePrompt constructs the base prompt for the discovery merge
// agent. It is shared between mergeDiscoveryWithAgent and ReplayDiscoveryMerge.
func buildDiscoveryMergePrompt(claudeData, codexData []byte) string {
	var b strings.Builder
	b.WriteString("# Discovery Merge Agent\n\n")
	b.WriteString("You are given two discovery analysis outputs from different AI providers (Claude and Codex) ")
	b.WriteString("that analysed the same source documents describing a software system.\n\n")
	b.WriteString("Your task: produce a SINGLE merged discovery output that combines the best elements from both, ")
	b.WriteString("resolving conflicts and eliminating redundancy. The result should be more complete and accurate ")
	b.WriteString("than either individual output.\n\n")

	b.WriteString("## Merge Rules\n\n")
	b.WriteString("- **problem_statement**: Synthesise into one clear statement. Do not concatenate — write a new unified version.\n")
	b.WriteString("- **actors**: Union of both lists. Deduplicate by name (case-insensitive). Prefer the richer description.\n")
	b.WriteString("- **scope**: Union in_scope and out_of_scope. Deduplicate similar items.\n")
	b.WriteString("- **constraints**: Union and deduplicate.\n")
	b.WriteString("- **integration_points**: Union by system name. Merge descriptions if both mention the same system.\n")
	b.WriteString("- **priorities**: Union by item. If both providers assigned different priorities, prefer the higher (more urgent).\n")
	b.WriteString("- **assumptions**: Union and deduplicate. Keep the richer question_for_user.\n")
	b.WriteString("- **open_questions**: Union and deduplicate.\n")
	b.WriteString("- **schema_version**: Use \"1.0\"\n")
	b.WriteString("- **agent**: Set to \"merged\"\n\n")

	b.WriteString("## Claude's Discovery Output\n\n```json\n")
	b.Write(claudeData)
	b.WriteString("\n```\n\n")

	b.WriteString("## Codex's Discovery Output\n\n```json\n")
	b.Write(codexData)
	b.WriteString("\n```\n\n")

	b.WriteString("## Output\n\n")
	b.WriteString("Produce the merged JSON object as your response. The JSON must conform to the DiscoveryOutput schema.\n")
	b.WriteString("Output ONLY the raw JSON object — no commentary, no markdown code fences, no explanation.\n")

	return b.String()
}

// mergeDiscoveryWithAgent dispatches a Claude agent to intelligently merge
// two discovery outputs into a single comprehensive result, with validation
// and retry on failure. Uses the taskval pattern: dispatch → validate →
// feed errors back → retry.
func (o *Orchestrator) mergeDiscoveryWithAgent(claudeData, codexData []byte, outputPath string, state *WorkflowStateJSON) ([]byte, error) {
	log.Printf("[orchestrator] dispatching merge agent for dual-provider discovery")

	o.SetAgentStatus("discovery-merge", "running")
	o.emitter.Emit(NewAgentDispatchEvent("discovery-merge", state.Round))

	basePrompt := buildDiscoveryMergePrompt(claudeData, codexData)

	// Use ForJSONOnly: disable tools and enforce --json-schema. The merge agent
	// receives all data inline and only needs to produce JSON — no file access needed.
	var mergeRunner AgentRunner = o.runnerFor("discovery")
	if cr, ok := mergeRunner.(*ClaudeRunner); ok {
		mergeRunner = cr.ForJSONOnly(string(DiscoveryOutputSchema()))
	}

	maxAttempts := o.config.MaxRetries
	if maxAttempts < 2 {
		maxAttempts = 2 // at least one retry for merge
	}

	result, err := RunWithValidation(ValidateAndRetryConfig{
		AgentName:      "discovery-merge",
		MaxAttempts:    maxAttempts,
		OutputPath:     outputPath,
		TimeoutSeconds: o.config.AgentTimeoutSeconds,
		Validator:      DiscoveryOutputValidator(),
		Runner:         mergeRunner,
		BuildPrompt: func(validationErrors []string) string {
			return AppendValidationErrorsToPrompt(basePrompt, validationErrors)
		},
	})
	if err != nil {
		o.SetAgentStatus("discovery-merge", "failed")
		return nil, fmt.Errorf("merge validation loop error: %w", err)
	}

	state.AgentInvocations += result.Attempts
	state.CumulativeCostUSD += result.TotalCost
	o.emitter.Emit(NewAgentCompleteEvent("discovery-merge", state.Round, result.Data != nil, result.TotalDuration, result.TotalCost))

	if result.Data == nil {
		o.SetAgentStatus("discovery-merge", "failed")
		return nil, fmt.Errorf("merge agent failed after %d attempts: %s",
			result.Attempts, strings.Join(result.LastErrors, "; "))
	}

	o.SetAgentStatus("discovery-merge", "done")

	var out DiscoveryOutput
	_ = json.Unmarshal(result.Data, &out)
	log.Printf("[orchestrator] discovery merge agent produced: %d actors, %d priorities, %d open questions",
		len(out.Actors), len(out.Priorities), len(out.OpenQuestions))

	return result.Data, nil
}

// parseAndValidateDiscoveryOutput reads, parses, and validates a discovery
// output JSON file. Returns the parsed output, the raw bytes, and any error.
func parseAndValidateDiscoveryOutput(path string) (*DiscoveryOutput, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read discovery output: %w", err)
	}

	var discovery DiscoveryOutput
	if err := json.Unmarshal(data, &discovery); err != nil {
		log.Printf("[orchestrator] FAILED to parse discovery output as JSON: %v", err)
		log.Printf("[orchestrator] file content (first 200 chars): %s", truncate(string(data), 200))
		return nil, nil, fmt.Errorf("parse discovery output as JSON: %w (agent may have written text instead of JSON — file starts with: %s)",
			err, truncate(string(data), 60))
	}

	if errs := ValidateDiscoveryOutput(&discovery); len(errs) > 0 {
		log.Printf("[orchestrator] discovery output validation failed: %v", errs)
		return nil, nil, fmt.Errorf("validate discovery output: %v", errs[0])
	}

	return &discovery, data, nil
}

// loadCorrectionHistory loads all numbered gate1-corrections-{N}.json files
// and their corresponding discovery-output-{N}.json files, returning a
// DiscoveryContext per correction round in chronological order.
// Falls back to the unversioned gate1-corrections.json for backward
// compatibility with workspaces created before versioned corrections.
func loadCorrectionHistory(specDir string) []DiscoveryContext {
	// Try numbered files first.
	var corrFiles []string
	entries, _ := os.ReadDir(specDir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "gate1-corrections-") && strings.HasSuffix(e.Name(), ".json") {
			corrFiles = append(corrFiles, e.Name())
		}
	}
	sort.Strings(corrFiles) // lexicographic sort gives numeric order for single-digit

	if len(corrFiles) == 0 {
		// Backward compat: try the unversioned file.
		return loadSingleCorrectionFile(specDir, "gate1-corrections.json", "discovery-output.json")
	}

	var history []DiscoveryContext
	for i, filename := range corrFiles {
		dc := parseCorrectionFile(specDir, filename)
		if dc == nil {
			continue
		}
		// Load the corresponding discovery output for this round.
		// Round N corrections were made against discovery-output-{N}.json.
		round := i + 1
		outFile := fmt.Sprintf("discovery-output-%d.json", round)
		outPath := filepath.Join(specDir, outFile)
		if prevData, err := os.ReadFile(outPath); err == nil {
			var prevOutput DiscoveryOutput
			if json.Unmarshal(prevData, &prevOutput) == nil {
				dc.PreviousOutput = &prevOutput
			}
		}
		dc.Round = round
		history = append(history, *dc)
		log.Printf("[orchestrator] loaded correction round %d: %d corrections, has_user_answers=%v, has_previous_output=%v",
			round, len(dc.Corrections), dc.UserAnswers != nil, dc.PreviousOutput != nil)
	}
	return history
}

// loadSingleCorrectionFile loads the unversioned gate1-corrections.json
// (backward compat for workspaces created before versioned corrections).
func loadSingleCorrectionFile(specDir, corrFilename, outFilename string) []DiscoveryContext {
	dc := parseCorrectionFile(specDir, corrFilename)
	if dc == nil {
		return nil
	}
	outPath := filepath.Join(specDir, outFilename)
	if prevData, err := os.ReadFile(outPath); err == nil {
		var prevOutput DiscoveryOutput
		if json.Unmarshal(prevData, &prevOutput) == nil {
			dc.PreviousOutput = &prevOutput
		}
	}
	log.Printf("[orchestrator] loaded gate 1 corrections (unversioned): %d corrections, has_user_answers=%v, has_previous_output=%v",
		len(dc.Corrections), dc.UserAnswers != nil, dc.PreviousOutput != nil)
	return []DiscoveryContext{*dc}
}

// parseCorrectionFile reads and parses a single correction JSON file.
func parseCorrectionFile(specDir, filename string) *DiscoveryContext {
	corrPath := filepath.Join(specDir, filename)
	corrData, err := os.ReadFile(corrPath)
	if err != nil {
		return nil
	}
	var corrFile map[string]interface{}
	if json.Unmarshal(corrData, &corrFile) != nil {
		return nil
	}
	dc := &DiscoveryContext{}
	if corrections, ok := corrFile["corrections"].(map[string]interface{}); ok {
		dc.Corrections = make(map[string]string)
		for k, v := range corrections {
			dc.Corrections[k] = fmt.Sprintf("%v", v)
		}
	}
	if ua, ok := corrFile["user_answers"].(map[string]interface{}); ok {
		dc.UserAnswers = ua
	}
	if rc, ok := corrFile["reviewer_comment"].(string); ok {
		dc.ReviewerComment = rc
	}
	return dc
}

// handleHumanGate1 waits for a human gate response and either confirms,
// corrects, or cancels the discovery output.
func (o *Orchestrator) handleHumanGate1(state *WorkflowStateJSON, specDir string) error {
	// Create gate proxy in Beads (CD-88v.10).
	o.openGateProxy("gate1", o.featureName+": gate-1 — Discovery review required")

	gate1 := NewGate1Handler(state, o.emitter, o.config.MaxGateCorrections)

	discoveryPath := filepath.Join(specDir, "discovery-output.json")
	dData, err := os.ReadFile(discoveryPath)
	if err != nil {
		return fmt.Errorf("read discovery output for gate 1: %w", err)
	}
	var disc DiscoveryOutput
	if err := json.Unmarshal(dData, &disc); err != nil {
		return fmt.Errorf("parse discovery output for gate 1: %w", err)
	}

	// Check for dual-provider discovery outputs and emit richer gate data.
	if gateData := o.buildDiscoveryGateData(&disc, specDir, state); gateData != nil {
		gate1.EnterDualGate(gateData)
	} else {
		gate1.EnterGate(&disc)
	}

	// Wait for human response — race gateCh against Beads gate polling (CD-88v.10).
	resp := o.waitForGateResponse("confirm", "correct")
	if resp.Action == gateActionCancelled {
		return fmt.Errorf("workflow cancelled")
	}

	log.Printf("[orchestrator] gate 1 response received: action=%s", resp.Action)

	// NOTE: The HTTP handler persists all gate data to disk BEFORE
	// signaling this channel. The orchestrator reads from disk, not
	// from resp.Data. This ensures data is never lost even if the
	// channel signal fails or the orchestrator restarts.

	switch resp.Action {
	case "confirm":
		o.emitter.Emit(NewGateResponseEvent("requirements_confirmation", "confirm", "Human confirmed discovery output"))

		// User answers already persisted to user-answers.json by HTTP handler.
		nextState, _ := gate1.HandleConfirm()
		o.logTransition(StateHumanGate1, nextState)
		if err := o.sm.Transition(nextState); err != nil {
			return fmt.Errorf("transition HUMAN_GATE_1 -> %s: %w", nextState, err)
		}

	case "correct":
		// Gate data already persisted to gate1-corrections-{N}.json and
		// human-comments.json by the HTTP handler.
		// Read back the latest corrections file from disk for stats.
		nCorrections := 0
		hasUserAnswers := false
		hasComment := false
		corrPath := filepath.Join(specDir, "gate1-corrections.json")
		if corrData, readErr := os.ReadFile(corrPath); readErr == nil {
			var corrFile map[string]interface{}
			if json.Unmarshal(corrData, &corrFile) == nil {
				if corr, ok := corrFile["corrections"].(map[string]interface{}); ok {
					nCorrections = len(corr)
				}
				_, hasUserAnswers = corrFile["user_answers"]
				_, hasComment = corrFile["reviewer_comment"]
			}
		}
		detail := fmt.Sprintf("Re-running discovery (attempt %d/%d) with:",
			state.Gate1CorrectionCount+1, o.config.MaxGateCorrections)
		if nCorrections > 0 {
			detail += fmt.Sprintf(" %d inline corrections,", nCorrections)
		}
		if hasUserAnswers {
			detail += " question answers,"
		}
		if hasComment {
			detail += " reviewer comments,"
		}
		if !hasUserAnswers && !hasComment && nCorrections == 0 {
			detail += " no changes"
		}
		detail = strings.TrimRight(detail, ",")
		o.emitter.Emit(NewGateResponseEvent("requirements_confirmation", "correct", detail))

		// Read corrections for the gate handler.
		var corrections map[string]string
		if corrData, readErr := os.ReadFile(corrPath); readErr == nil {
			var corrFile map[string]interface{}
			if json.Unmarshal(corrData, &corrFile) == nil {
				if corr, ok := corrFile["corrections"].(map[string]interface{}); ok {
					corrections = make(map[string]string)
					for k, v := range corr {
						corrections[k] = fmt.Sprintf("%v", v)
					}
				}
			}
		}

		nextState, err := gate1.HandleCorrect(corrections)
		if err != nil {
			// Correction limit reached. Proceed to DRAFTING with saved data.
			log.Printf("[orchestrator] correction limit reached (%d/%d), proceeding to DRAFTING with saved corrections",
				state.Gate1CorrectionCount, o.config.MaxGateCorrections)
			o.emitter.Emit(NewGateResponseEvent("requirements_confirmation", "confirm",
				"Correction limit reached — proceeding to drafting with saved feedback"))

			// Copy corrections to user-answers.json so the drafter picks them up.
			answersPath := filepath.Join(specDir, "user-answers.json")
			if corrData, readErr := os.ReadFile(corrPath); readErr == nil {
				os.WriteFile(answersPath, corrData, 0o644)
			}

			o.logTransition(StateHumanGate1, StateDrafting)
			if transErr := o.sm.Transition(StateDrafting); transErr != nil {
				return fmt.Errorf("transition HUMAN_GATE_1 -> DRAFTING (after correction limit): %w", transErr)
			}
			return nil
		}
		o.logTransition(StateHumanGate1, nextState)
		if err := o.sm.Transition(nextState); err != nil {
			return fmt.Errorf("transition HUMAN_GATE_1 -> %s: %w", nextState, err)
		}

	case "cancel":
		o.emitter.Emit(NewGateResponseEvent("requirements_confirmation", "cancel", "Human cancelled workflow"))
		o.escalateFrom(StateHumanGate1)

	default:
		return fmt.Errorf("unknown gate 1 action: %q", resp.Action)
	}
	return nil
}

// buildDiscoveryGateData checks for dual-provider discovery outputs and builds
// a DiscoveryGateData if dual-provider mode was used. Returns nil when only
// single-provider discovery was used, so the caller can fall back to the
// simpler EnterGate path.
func (o *Orchestrator) buildDiscoveryGateData(merged *DiscoveryOutput, specDir string, state *WorkflowStateJSON) *DiscoveryGateData {
	if !o.config.EnableCodexDiscovery {
		return nil
	}

	discoveryRound := state.Gate1CorrectionCount + 1
	claudePath := filepath.Join(specDir, VersionedFilename("discovery-output", "claude", discoveryRound, ".json"))
	codexPath := filepath.Join(specDir, VersionedFilename("discovery-output", "codex", discoveryRound, ".json"))

	// If neither per-provider file exists, this was a single-provider run.
	_, claudeErr := os.Stat(claudePath)
	_, codexErr := os.Stat(codexPath)
	if claudeErr != nil && codexErr != nil {
		return nil
	}

	data := &DiscoveryGateData{
		MergedOutput: merged,
		DualProvider: true,
	}

	// Load Claude output.
	if claudeErr == nil {
		if raw, err := os.ReadFile(claudePath); err == nil {
			var co DiscoveryOutput
			if json.Unmarshal(raw, &co) == nil {
				data.ClaudeOutput = &co
				data.ClaudeStatus = "success"
			}
		}
	}
	if data.ClaudeStatus == "" {
		data.ClaudeStatus = "failed"
	}

	// Load Codex output.
	if codexErr == nil {
		if raw, err := os.ReadFile(codexPath); err == nil {
			var co DiscoveryOutput
			if json.Unmarshal(raw, &co) == nil {
				data.CodexOutput = &co
				data.CodexStatus = "success"
			}
		}
	}
	if data.CodexStatus == "" {
		data.CodexStatus = "failed"
	}

	// Build failure notice.
	switch {
	case data.ClaudeStatus == "failed" && data.CodexStatus == "failed":
		data.FailureNotice = "Both Claude and Codex discovery agents failed; using fallback output"
	case data.ClaudeStatus == "failed":
		data.FailureNotice = "Claude discovery agent failed; showing Codex output only"
	case data.CodexStatus == "failed":
		data.FailureNotice = "Codex discovery agent failed; showing Claude output only"
	}

	return data
}
