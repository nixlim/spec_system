package specworkflow

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// handleDiscovery builds the discovery prompt, dispatches the discovery agent,
// validates the output, and transitions to HUMAN_GATE_1.
func (o *Orchestrator) handleDiscovery(goal GoalInput, state *WorkflowStateJSON, specDir string) error {
	// Check for corrections from a previous gate 1 correction loop.
	var discoveryCtx []DiscoveryContext
	corrPath := filepath.Join(specDir, "gate1-corrections.json")
	if corrData, readErr := os.ReadFile(corrPath); readErr == nil {
		var corrFile map[string]interface{}
		if json.Unmarshal(corrData, &corrFile) == nil {
			dc := DiscoveryContext{}
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
			// Load previous discovery output for context.
			outPath := filepath.Join(specDir, "discovery-output.json")
			if prevData, prevErr := os.ReadFile(outPath); prevErr == nil {
				var prevOutput DiscoveryOutput
				if json.Unmarshal(prevData, &prevOutput) == nil {
					dc.PreviousOutput = &prevOutput
				}
			}
			discoveryCtx = append(discoveryCtx, dc)
			log.Printf("[orchestrator] loaded gate 1 corrections: %d corrections, has_user_answers=%v, has_previous_output=%v",
				len(dc.Corrections), dc.UserAnswers != nil, dc.PreviousOutput != nil)
		}
	}

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

	o.logTransition(StateDiscovery, StateHumanGate1)
	if err := o.sm.Transition(StateHumanGate1); err != nil {
		return fmt.Errorf("transition DISCOVERY -> HUMAN_GATE_1: %w", err)
	}
	return nil
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
		// Gate data already persisted to gate1-corrections.json and
		// human-comments.json by the HTTP handler.
		// Read back from disk to compute statistics for the log message.
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
