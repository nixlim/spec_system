package specworkflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// handleDrafting builds the drafter prompt, dispatches the drafter agent,
// validates the output, and transitions to HUMAN_GATE_2.
func (o *Orchestrator) handleDrafting(state *WorkflowStateJSON, specDir string) error {
	confirmedReqsPath := filepath.Join(specDir, "discovery-output.json")
	prompt, err := o.promptBuilder.BuildDrafterPrompt(confirmedReqsPath, nil)
	if err != nil {
		return fmt.Errorf("build drafter prompt: %w", err)
	}

	outPath := filepath.Join(specDir, "drafter-output.json")
	cost, duration, err := o.dispatchAgent("drafter", prompt, outPath)
	if err != nil {
		return o.handleAgentError("drafter", err, cost, duration)
	}

	// Parse and validate.
	data, err := os.ReadFile(outPath)
	if err != nil {
		return fmt.Errorf("read drafter output: %w", err)
	}
	var drafter DrafterOutput
	if err := json.Unmarshal(data, &drafter); err != nil {
		return fmt.Errorf("parse drafter output: %w", err)
	}

	o.logTransition(StateDrafting, StateHumanGate2)
	if err := o.sm.Transition(StateHumanGate2); err != nil {
		return fmt.Errorf("transition DRAFTING -> HUMAN_GATE_2: %w", err)
	}
	return nil
}

// handleHumanGate2 waits for a human gate response and either confirms,
// resolves ambiguities, or cancels the draft.
func (o *Orchestrator) handleHumanGate2(state *WorkflowStateJSON, specDir string) error {
	gate2 := NewGate2Handler(state, o.emitter, 1)

	drafterPath := filepath.Join(specDir, "drafter-output.json")
	dData, err := os.ReadFile(drafterPath)
	if err != nil {
		return fmt.Errorf("read drafter output for gate 2: %w", err)
	}
	var drafter DrafterOutput
	if err := json.Unmarshal(dData, &drafter); err != nil {
		return fmt.Errorf("parse drafter output for gate 2: %w", err)
	}
	gate2.EnterGate(&drafter)

	resp := <-o.gateCh

	switch resp.Action {
	case "confirm":
		o.logTransition(StateHumanGate2, StateReviewing)
		if err := o.sm.Transition(StateReviewing); err != nil {
			return fmt.Errorf("transition HUMAN_GATE_2 -> REVIEWING: %w", err)
		}
	case "resolve":
		resolutions, _ := resp.Data.([]AmbiguityResolution)
		_, nextState, err := gate2.HandleResolutions(resolutions)
		if err != nil {
			o.escalateFrom(StateHumanGate2)
			return nil
		}
		o.logTransition(StateHumanGate2, nextState)
		if err := o.sm.Transition(nextState); err != nil {
			return fmt.Errorf("transition HUMAN_GATE_2 -> %s: %w", nextState, err)
		}
	case "cancel":
		o.escalateFrom(StateHumanGate2)
	default:
		return fmt.Errorf("unknown gate 2 action: %q", resp.Action)
	}
	return nil
}
