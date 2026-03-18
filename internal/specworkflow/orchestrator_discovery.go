package specworkflow

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
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

	// Persist reviewer comment if provided.
	persistComment(specDir, "HUMAN_GATE_1", resp.Action, resp.Comment)

	switch resp.Action {
	case "confirm":
		o.emitter.Emit(NewGateResponseEvent("requirements_confirmation", "confirm", "Human confirmed discovery output"))

		// Save user answers if provided.
		if resp.Data != nil {
			answersPath := filepath.Join(specDir, "user-answers.json")
			answersData, marshalErr := json.MarshalIndent(resp.Data, "", "  ")
			if marshalErr == nil {
				if writeErr := os.WriteFile(answersPath, answersData, 0o644); writeErr != nil {
					log.Printf("[orchestrator] failed to write user answers: %v", writeErr)
				} else {
					log.Printf("[orchestrator] saved user answers to %s (%d bytes)", answersPath, len(answersData))
				}
			}
		}
		nextState, _ := gate1.HandleConfirm()
		o.logTransition(StateHumanGate1, nextState)
		if err := o.sm.Transition(nextState); err != nil {
			return fmt.Errorf("transition HUMAN_GATE_1 -> %s: %w", nextState, err)
		}

	case "correct":
		// resp.Data is a map[string]interface{} with "corrections" and optionally "user_answers".
		// Save the full structure to disk so handleDiscovery can include it in the re-run prompt.
		correctionData := map[string]interface{}{
			"action": "correct",
		}

		if m, ok := resp.Data.(map[string]interface{}); ok {
			if corr, exists := m["corrections"]; exists {
				correctionData["corrections"] = corr
			}
			if ua, exists := m["user_answers"]; exists {
				correctionData["user_answers"] = ua
			}
		} else {
			// Fallback: treat the whole Data as corrections.
			correctionData["corrections"] = resp.Data
		}

		// Include reviewer comment in corrections file so it's available
		// during re-discovery (not just via human-comments.json).
		if resp.Comment != "" {
			correctionData["reviewer_comment"] = resp.Comment
		}

		corrPath := filepath.Join(specDir, "gate1-corrections.json")
		corrJSON, marshalErr := json.MarshalIndent(correctionData, "", "  ")
		if marshalErr == nil {
			if writeErr := os.WriteFile(corrPath, corrJSON, 0o644); writeErr != nil {
				log.Printf("[orchestrator] failed to write gate 1 corrections: %v", writeErr)
			} else {
				log.Printf("[orchestrator] saved gate 1 corrections to %s (%d bytes)", corrPath, len(corrJSON))
			}
		}

		nCorrections := 0
		hasUserAnswers := false
		hasComment := resp.Comment != ""
		if m, ok := resp.Data.(map[string]interface{}); ok {
			if corr, exists := m["corrections"]; exists {
				if cm, ok := corr.(map[string]interface{}); ok {
					nCorrections = len(cm)
				} else if cm, ok := corr.(map[string]string); ok {
					nCorrections = len(cm)
				}
			}
			if _, exists := m["user_answers"]; exists {
				hasUserAnswers = true
			}
		}
		o.emitter.Emit(NewGateResponseEvent("requirements_confirmation", "correct",
			fmt.Sprintf("Human requested corrections (%d fields, answers=%v, comment=%v), re-running discovery (attempt %d/%d)",
				nCorrections, hasUserAnswers, hasComment, state.Gate1CorrectionCount+1, o.config.MaxGateCorrections)))

		// Extract corrections map for the gate handler (just the corrections,
		// not user_answers — those are already saved to disk).
		var corrections map[string]string
		if m, ok := resp.Data.(map[string]interface{}); ok {
			if corr, exists := m["corrections"]; exists {
				if cm, ok := corr.(map[string]interface{}); ok {
					corrections = make(map[string]string)
					for k, v := range cm {
						corrections[k] = fmt.Sprintf("%v", v)
					}
				}
			}
		}
		nextState, err := gate1.HandleCorrect(corrections)
		if err != nil {
			// Correction limit reached. Don't escalate — the user's answers
			// are already saved to gate1-corrections.json. Treat this as a
			// confirm with corrections: proceed to DRAFTING so the drafter
			// can use the saved answers.
			log.Printf("[orchestrator] correction limit reached (%d/%d), proceeding to DRAFTING with saved corrections",
				state.Gate1CorrectionCount, o.config.MaxGateCorrections)
			o.emitter.Emit(NewGateResponseEvent("requirements_confirmation", "confirm",
				"Correction limit reached — proceeding to drafting with saved feedback"))

			// Also save as user-answers.json so the drafter picks them up.
			answersPath := filepath.Join(specDir, "user-answers.json")
			if answersData, marshalErr := json.MarshalIndent(correctionData, "", "  "); marshalErr == nil {
				os.WriteFile(answersPath, answersData, 0o644)
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
