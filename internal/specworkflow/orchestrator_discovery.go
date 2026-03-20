package specworkflow

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// handleDiscovery builds the discovery prompt, dispatches the discovery agent,
// validates the output, and transitions to HUMAN_GATE_1.
func (o *Orchestrator) handleDiscovery(goal GoalInput, state *WorkflowStateJSON, specDir string) error {
	// Load the full correction history from numbered files
	// (gate1-corrections-1.json, gate1-corrections-2.json, ...).
	discoveryCtx := loadCorrectionHistory(specDir)

	prompt, err := o.promptBuilder.BuildDiscoveryPrompt(goal.SourceDocPaths, discoveryCtx...)
	if err != nil {
		return fmt.Errorf("build discovery prompt: %w", err)
	}

	outPath := filepath.Join(specDir, "discovery-output.json")
	cost, duration, err := o.dispatchAgent("discovery", prompt, outPath)
	if err != nil {
		return o.handleAgentError("discovery", err, cost, duration)
	}

	// Parse and validate output.
	data, err := os.ReadFile(outPath)
	if err != nil {
		return fmt.Errorf("read discovery output: %w", err)
	}

	var discovery DiscoveryOutput
	if err := json.Unmarshal(data, &discovery); err != nil {
		// The agent may have written a text summary instead of JSON.
		// Log detailed error and the first 200 chars of the file content.
		log.Printf("[orchestrator] FAILED to parse discovery output as JSON: %v", err)
		log.Printf("[orchestrator] file content (first 200 chars): %s", truncate(string(data), 200))
		return fmt.Errorf("parse discovery output as JSON: %w (agent may have written text instead of JSON — file starts with: %s)",
			err, truncate(string(data), 60))
	}

	if errs := ValidateDiscoveryOutput(&discovery); len(errs) > 0 {
		log.Printf("[orchestrator] discovery output validation failed: %v", errs)
		return fmt.Errorf("validate discovery output: %v", errs[0])
	}

	log.Printf("[orchestrator] discovery output valid: %d actors, %d priorities, %d open questions",
		len(discovery.Actors), len(discovery.Priorities), len(discovery.OpenQuestions))

	// Save a numbered copy for history (discovery-output-{N}.json).
	discoveryRound := state.Gate1CorrectionCount + 1
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
	gate1.EnterGate(&disc)

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
