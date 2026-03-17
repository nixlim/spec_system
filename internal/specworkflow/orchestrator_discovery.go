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
	prompt, err := o.promptBuilder.BuildDiscoveryPrompt(goal.SourceDocPaths)
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

	switch resp.Action {
	case "confirm":
		nextState, _ := gate1.HandleConfirm()
		o.logTransition(StateHumanGate1, nextState)
		if err := o.sm.Transition(nextState); err != nil {
			return fmt.Errorf("transition HUMAN_GATE_1 -> %s: %w", nextState, err)
		}
	case "correct":
		corrections, _ := resp.Data.(map[string]string)
		nextState, err := gate1.HandleCorrect(corrections)
		if err != nil {
			// Correction limit reached, escalate.
			o.escalateFrom(StateHumanGate1)
			return nil
		}
		o.logTransition(StateHumanGate1, nextState)
		if err := o.sm.Transition(nextState); err != nil {
			return fmt.Errorf("transition HUMAN_GATE_1 -> %s: %w", nextState, err)
		}
	case "cancel":
		o.escalateFrom(StateHumanGate1)
	default:
		return fmt.Errorf("unknown gate 1 action: %q", resp.Action)
	}
	return nil
}
