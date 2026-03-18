package specworkflow

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// handleDrafting builds the drafter prompt, dispatches the drafter agent,
// validates the output, and transitions to HUMAN_GATE_2.
//
// On crash recovery, if drafter-output.json already exists and is valid,
// the agent dispatch is skipped and the workflow transitions directly
// to HUMAN_GATE_2.
func (o *Orchestrator) handleDrafting(state *WorkflowStateJSON, specDir string) error {
	outPath := filepath.Join(specDir, "drafter-output.json")

	// Check for existing valid output (crash recovery).
	if data, err := os.ReadFile(outPath); err == nil {
		var drafter DrafterOutput
		if json.Unmarshal(data, &drafter) == nil && drafter.Agent == "drafter" {
			log.Printf("[orchestrator] drafter output already exists, skipping re-dispatch (crash recovery)")
			o.logTransition(StateDrafting, StateHumanGate2)
			if err := o.sm.Transition(StateHumanGate2); err != nil {
				return fmt.Errorf("transition DRAFTING -> HUMAN_GATE_2: %w", err)
			}
			return nil
		}
	}

	confirmedReqsPath := filepath.Join(specDir, "discovery-output.json")

	// Load user answers from disk if they exist.
	var userAnswers map[string]string
	answersPath := filepath.Join(specDir, "user-answers.json")
	if data, err := os.ReadFile(answersPath); err == nil {
		if err := json.Unmarshal(data, &userAnswers); err != nil {
			log.Printf("[orchestrator] warning: failed to parse user-answers.json: %v", err)
		}
	}

	// Collect all context documents the drafter should read.
	contextDocs := collectDrafterContext(specDir, o.workspaceDir)

	prompt, err := o.promptBuilder.BuildDrafterPrompt(confirmedReqsPath, userAnswers, contextDocs)
	if err != nil {
		return fmt.Errorf("build drafter prompt: %w", err)
	}

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

// collectDrafterContext scans the spec directory and source-docs directory
// for files the drafter agent should read. It excludes output files that
// the drafter itself will write, and the discovery output which is already
// passed separately as confirmedReqsPath.
func collectDrafterContext(specDir, workspaceDir string) []string {
	var docs []string

	// Spec directory — include corrections, user answers, and other context.
	// Exclude files the drafter writes or that are passed separately.
	exclude := map[string]bool{
		"discovery-output.json": true, // already passed as confirmedReqsPath
		"drafter-output.json":   true, // drafter writes this
		"workflow-state.json":   true, // internal bookkeeping
		"workflow-log.jsonl":    true, // internal log
	}

	if entries, err := os.ReadDir(specDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if exclude[name] {
				continue
			}
			// Skip spec output files (spec-v*.md, *-holdouts.md).
			if strings.HasPrefix(name, "spec-v") && strings.HasSuffix(name, ".md") {
				continue
			}
			if strings.HasSuffix(name, "-holdouts.md") {
				continue
			}
			docs = append(docs, filepath.Join(specDir, name))
		}
	}

	// Source documents — uploaded reference material.
	sourceDocsDir := filepath.Join(workspaceDir, "source-docs")
	if entries, err := os.ReadDir(sourceDocsDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			docs = append(docs, filepath.Join(sourceDocsDir, e.Name()))
		}
	}

	if len(docs) > 0 {
		log.Printf("[orchestrator] drafter context documents: %v", docs)
	}

	return docs
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

	// Persist reviewer comment if provided.
	persistComment(specDir, "HUMAN_GATE_2", resp.Action, resp.Comment)

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
