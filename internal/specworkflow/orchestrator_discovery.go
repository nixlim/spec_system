package specworkflow

import (
	"encoding/json"
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

// handleDiscovery builds the discovery prompt, dispatches discovery agent(s),
// validates output(s), and transitions to HUMAN_GATE_1. When
// EnableCodexDiscovery is true and a codex discovery runner is available, both
// Claude and Codex run in parallel with AgentTimeoutSeconds timeout. Outputs
// are merged via MergeDiscoveryOutputs. On single-provider failure, the
// survivor is used. On both failures, the workflow escalates.
func (o *Orchestrator) handleDiscovery(goal GoalInput, state *WorkflowStateJSON, specDir string) error {
	// Load the full correction history from numbered files
	// (gate1-corrections-1.json, gate1-corrections-2.json, ...).
	discoveryCtx := loadCorrectionHistory(specDir)

	prompt, err := o.promptBuilder.BuildDiscoveryPrompt(goal.SourceDocPaths, discoveryCtx...)
	if err != nil {
		return fmt.Errorf("build discovery prompt: %w", err)
	}

	discoveryRound := state.Gate1CorrectionCount + 1

	// Dual-provider path: dispatch both Claude and Codex in parallel.
	if o.config.EnableCodexDiscovery && o.codexDiscoveryRunner != nil {
		return o.handleDualDiscovery(prompt, state, specDir, discoveryRound)
	}

	// Single-provider path (current behavior).
	return o.handleSingleDiscovery(prompt, state, specDir, discoveryRound)
}

// handleSingleDiscovery is the original single-provider discovery path.
func (o *Orchestrator) handleSingleDiscovery(prompt string, state *WorkflowStateJSON, specDir string, discoveryRound int) error {
	outPath := filepath.Join(specDir, "discovery-output.json")
	cost, duration, err := o.dispatchAgent("discovery", prompt, outPath)
	if err != nil {
		return o.handleAgentError("discovery", err, cost, duration)
	}

	discovery, data, err := parseAndValidateDiscoveryOutput(outPath)
	if err != nil {
		return err
	}

	log.Printf("[orchestrator] discovery output valid: %d actors, %d priorities, %d open questions",
		len(discovery.Actors), len(discovery.Priorities), len(discovery.OpenQuestions))

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
func (o *Orchestrator) handleDualDiscovery(prompt string, state *WorkflowStateJSON, specDir string, discoveryRound int) error {
	timeout := o.config.AgentTimeoutSeconds

	claudeOutPath := filepath.Join(specDir, VersionedFilename("discovery-output", "claude", discoveryRound, ".json"))
	codexOutPath := filepath.Join(specDir, VersionedFilename("discovery-output", "codex", discoveryRound, ".json"))

	// Track and emit dispatch events.
	o.SetAgentStatus("discovery-claude", "running")
	o.emitter.Emit(NewAgentDispatchEvent("discovery-claude", state.Round))
	o.SetAgentStatus("discovery-codex", "running")
	o.emitter.Emit(NewAgentDispatchEvent("discovery-codex", state.Round))

	resultsCh := make(chan discoveryResult, 2)
	var wg sync.WaitGroup

	// Claude discovery agent.
	wg.Add(1)
	go func() {
		defer wg.Done()
		o.logger.LogAgentDispatch("discovery-claude", "discovery-claude", state.Round)
		exitCode, stderr, cost, duration, runErr := o.runner.Run(prompt, claudeOutPath, timeout)
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

	// Codex discovery agent.
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

	wg.Wait()
	close(resultsCh)

	// Collect results and accumulate cost after both goroutines complete
	// to avoid data races on shared state fields.
	var claudeResult, codexResult *discoveryResult
	for r := range resultsCh {
		r := r
		state.AgentInvocations++
		state.CumulativeCostUSD += r.cost
		if r.provider == "claude" {
			claudeResult = &r
		} else {
			codexResult = &r
		}
	}

	// Emit completion events.
	emitDiscoveryComplete := func(provider string, r *discoveryResult) {
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

	// Parse successful outputs.
	var claudeOutput, codexOutput *DiscoveryOutput
	var claudeData, codexData []byte

	if claudeResult.err == nil {
		co, data, parseErr := parseAndValidateDiscoveryOutput(claudeOutPath)
		if parseErr != nil {
			log.Printf("[orchestrator] discovery-claude: output parse/validation failed: %v", parseErr)
			claudeResult.err = parseErr
		} else {
			claudeOutput = co
			claudeData = data
		}
	}

	if codexResult.err == nil {
		co, data, parseErr := parseAndValidateDiscoveryOutput(codexOutPath)
		if parseErr != nil {
			log.Printf("[orchestrator] discovery-codex: output parse/validation failed: %v", parseErr)
			codexResult.err = parseErr
		} else {
			codexOutput = co
			codexData = data
		}
	}

	// Both failed -> escalate.
	if claudeOutput == nil && codexOutput == nil {
		reason := fmt.Sprintf("both discovery agents failed: claude=%v, codex=%v", claudeResult.err, codexResult.err)
		log.Printf("[orchestrator] %s", reason)
		o.escalateFrom(StateDiscovery)
		return nil
	}

	// Determine final output.
	var finalOutput *DiscoveryOutput
	var finalData []byte

	if claudeOutput != nil && codexOutput != nil {
		// Both succeeded -> merge.
		finalOutput = MergeDiscoveryOutputs(claudeOutput, codexOutput)
		mergedData, err := json.MarshalIndent(finalOutput, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal merged discovery output: %w", err)
		}
		finalData = mergedData

		mergedPath := filepath.Join(specDir, VersionedMergedFilename("discovery-output", discoveryRound, ".json"))
		if err := os.WriteFile(mergedPath, mergedData, 0o644); err != nil {
			log.Printf("[orchestrator] warning: failed to write merged discovery output: %v", err)
		}
		log.Printf("[orchestrator] dual-provider discovery merged: %d actors, %d priorities, %d open questions",
			len(finalOutput.Actors), len(finalOutput.Priorities), len(finalOutput.OpenQuestions))
	} else if claudeOutput != nil {
		// Only Claude succeeded.
		finalOutput = claudeOutput
		finalData = claudeData
		log.Printf("[orchestrator] Codex discovery failed, using Claude-only output")
	} else {
		// Only Codex succeeded.
		finalOutput = codexOutput
		finalData = codexData
		log.Printf("[orchestrator] Claude discovery failed, using Codex-only output")
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

	o.logTransition(StateDiscovery, StateHumanGate1)
	if err := o.sm.Transition(StateHumanGate1); err != nil {
		return fmt.Errorf("transition DISCOVERY -> HUMAN_GATE_1: %w", err)
	}
	return nil
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

	// Wait for human response.
	resp := <-o.gateCh

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
