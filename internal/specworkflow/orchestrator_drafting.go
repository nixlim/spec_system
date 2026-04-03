package specworkflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// drafterResult holds the outcome of a single drafter agent invocation.
type drafterResult struct {
	provider string
	outPath  string
	cost     float64
	duration int64
	err      error
}

// handleDrafting builds the drafter prompt, dispatches drafter agent(s),
// validates the output, and transitions to HUMAN_GATE_2.
//
// When EnableCodexDrafting is true and the codex runner is available, both
// Claude and Codex drafters run in parallel. If both succeed, a Claude
// reviser combines them. If one fails, the survivor is used directly. If
// both fail, the workflow escalates.
//
// When EnableCodexDrafting is false (default), only Claude runs with the
// current single-provider behavior.
func (o *Orchestrator) handleDrafting(state *WorkflowStateJSON, specDir string) error {
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

	// Determine drafting version counter (increments on re-drafts).
	version := state.Gate2RedraftCount + 1

	// Dual-provider path.
	if o.config.EnableCodexDrafting && o.codexDraftingRunner != nil {
		return o.handleDualDrafting(state, specDir, prompt, version)
	}

	// Codex requested but unavailable — log fallback.
	if o.config.EnableCodexDrafting && o.codexDraftingRunner == nil {
		log.Printf("[orchestrator] Codex unavailable, falling back to Claude-only drafting")
	}

	// Single-provider (Claude only) — current behavior.
	// Use --json-schema to enforce structured output when the runner supports it.
	origRunner := o.runner
	if cr, ok := o.runner.(*ClaudeRunner); ok {
		o.runner = cr.WithJSONSchema(string(DrafterOutputSchema()))
	}
	err = o.handleSingleDrafting(state, specDir, prompt)
	o.runner = origRunner // restore
	return err
}

// handleSingleDrafting is the original single-provider drafting path.
func (o *Orchestrator) handleSingleDrafting(state *WorkflowStateJSON, specDir, prompt string) error {
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

	// Record draft source for gate 2 UI.
	state.DraftSource = "single_provider"
	state.DraftFailureNotice = ""

	o.logTransition(StateDrafting, StateHumanGate2)
	if err := o.sm.Transition(StateHumanGate2); err != nil {
		return fmt.Errorf("transition DRAFTING -> HUMAN_GATE_2: %w", err)
	}
	return nil
}

// handleDualDrafting dispatches Claude and Codex drafters in parallel,
// combines successful outputs via a Claude reviser, and handles fallback.
func (o *Orchestrator) handleDualDrafting(state *WorkflowStateJSON, specDir, prompt string, version int) error {
	claudeOutPath := filepath.Join(specDir, VersionedFilename("drafter-output", "claude", version, ".json"))
	codexOutPath := filepath.Join(specDir, VersionedFilename("drafter-output", "codex", version, ".json"))
	timeout := o.config.AgentTimeoutSeconds

	// Dispatch both drafters in parallel.
	var wg sync.WaitGroup
	var claudeResult, codexResult drafterResult

	// Build a schema-bound Claude runner for structured output.
	var drafterClaudeRunner AgentRunner = o.runner
	if cr, ok := o.runner.(*ClaudeRunner); ok {
		drafterClaudeRunner = cr.WithJSONSchema(string(DrafterOutputSchema()))
	}

	wg.Add(2)
	go func() {
		defer wg.Done()
		claudeRunner := taggedRunner(drafterClaudeRunner, "drafter-claude")
		exitCode, stderr, cost, duration, runErr := claudeRunner.Run(prompt, claudeOutPath, timeout)
		claudeResult = drafterResult{provider: "claude", outPath: claudeOutPath, cost: cost, duration: duration}
		if runErr != nil {
			claudeResult.err = fmt.Errorf("drafter-claude failed: %v", runErr)
			return
		}
		failureType := DetectFailureType(exitCode, stderr, claudeOutPath)
		if failureType != "" {
			claudeResult.err = fmt.Errorf("drafter-claude failed: %s", failureType)
		}
	}()

	go func() {
		defer wg.Done()
		codexRunner := taggedRunner(o.codexDraftingRunner, "drafter-codex")
		exitCode, stderr, cost, duration, runErr := codexRunner.Run(prompt, codexOutPath, timeout)
		codexResult = drafterResult{provider: "codex", outPath: codexOutPath, cost: cost, duration: duration}
		if runErr != nil {
			codexResult.err = fmt.Errorf("drafter-codex failed: %v", runErr)
			return
		}
		failureType := DetectFailureType(exitCode, stderr, codexOutPath)
		if failureType != "" {
			codexResult.err = fmt.Errorf("drafter-codex failed: %s", failureType)
		}
	}()

	wg.Wait()

	// Accumulate cost.
	smState := o.sm.State()
	smState.CumulativeCostUSD += claudeResult.cost + codexResult.cost
	smState.AgentInvocations += 2

	claudeOK := claudeResult.err == nil
	codexOK := codexResult.err == nil

	// Both failed → escalate.
	if !claudeOK && !codexOK {
		reason := fmt.Sprintf("both drafters failed: claude=%v; codex=%v", claudeResult.err, codexResult.err)
		log.Printf("[orchestrator] %s", reason)
		o.escalateFrom(StateDrafting)
		return nil
	}

	// Determine the final output path for drafter-output.json (used by gate 2).
	finalOutPath := filepath.Join(specDir, "drafter-output.json")

	// One failed → use survivor.
	if !claudeOK || !codexOK {
		var survivor drafterResult
		var failedProvider string
		if claudeOK {
			survivor = claudeResult
			failedProvider = "codex"
			log.Printf("[orchestrator] Codex drafter failed, using Claude draft: %v", codexResult.err)
		} else {
			survivor = codexResult
			failedProvider = "claude"
			log.Printf("[orchestrator] Claude drafter failed, using Codex draft: %v", claudeResult.err)
		}

		// Validate survivor output.
		data, err := os.ReadFile(survivor.outPath)
		if err != nil {
			return fmt.Errorf("read surviving drafter output: %w", err)
		}
		var drafter DrafterOutput
		if err := json.Unmarshal(data, &drafter); err != nil {
			return fmt.Errorf("parse surviving drafter output: %w", err)
		}

		// Copy survivor output to the canonical path for gate 2.
		if err := os.WriteFile(finalOutPath, data, 0o644); err != nil {
			return fmt.Errorf("write drafter-output.json: %w", err)
		}

		// Record draft source for gate 2 UI with failure notice.
		failureNotice := fmt.Sprintf("%s drafter failed — reviewing %s draft only", failedProvider, survivor.provider)
		smState.DraftSource = "single_survivor"
		smState.DraftFailureNotice = failureNotice

		// Emit UI notice about the failure.
		o.emitter.Emit(NewDraftingEvent("single_provider_fallback",
			fmt.Sprintf("%s drafter failed — using %s draft only", failedProvider, survivor.provider)))

		o.logTransition(StateDrafting, StateHumanGate2)
		if err := o.sm.Transition(StateHumanGate2); err != nil {
			return fmt.Errorf("transition DRAFTING -> HUMAN_GATE_2: %w", err)
		}
		return nil
	}

	// Both succeeded → validate both, then combine via Claude reviser.
	claudeData, err := os.ReadFile(claudeOutPath)
	if err != nil {
		return fmt.Errorf("read claude drafter output: %w", err)
	}
	var claudeDraft DrafterOutput
	if err := json.Unmarshal(claudeData, &claudeDraft); err != nil {
		return fmt.Errorf("parse claude drafter output: %w", err)
	}

	codexData, err := os.ReadFile(codexOutPath)
	if err != nil {
		return fmt.Errorf("read codex drafter output: %w", err)
	}
	var codexDraft DrafterOutput
	if err := json.Unmarshal(codexData, &codexDraft); err != nil {
		return fmt.Errorf("parse codex drafter output: %w", err)
	}

	// Dispatch Claude reviser to combine both drafts.
	combinedOutPath := filepath.Join(specDir, VersionedCombinedFilename("drafter-output", version, ".json"))
	combinePrompt := buildCombinePrompt(claudeOutPath, codexOutPath, combinedOutPath)

	combineRunner := taggedRunner(o.runner, "drafter-combine")
	combineExitCode, combineStderr, combineCost, _, combineErr := combineRunner.Run(combinePrompt, combinedOutPath, o.config.AgentTimeoutSeconds)
	smState.CumulativeCostUSD += combineCost
	smState.AgentInvocations++

	// Check for combine failure: Go error, non-zero exit, or detected failure type.
	if combineErr == nil && combineExitCode != 0 {
		combineErr = fmt.Errorf("combine agent exited with code %d: %s", combineExitCode, combineStderr)
	}
	if combineErr == nil {
		if ft := DetectFailureType(combineExitCode, combineStderr, combinedOutPath); ft != "" {
			combineErr = fmt.Errorf("combine agent failure: %s", ft)
		}
	}

	if combineErr != nil {
		// Combine failed → fallback: concatenate with attribution headers.
		log.Printf("[orchestrator] combine reviser failed, falling back to concatenation: %v", combineErr)
		concatenated := concatenateDrafts(claudeData, codexData)
		if err := os.WriteFile(combinedOutPath, concatenated, 0o644); err != nil {
			return fmt.Errorf("write concatenated draft: %w", err)
		}
	}

	// Read combined output and copy to canonical path.
	combinedData, err := os.ReadFile(combinedOutPath)
	if err != nil {
		return fmt.Errorf("read combined drafter output: %w", err)
	}
	if err := os.WriteFile(finalOutPath, combinedData, 0o644); err != nil {
		return fmt.Errorf("write drafter-output.json: %w", err)
	}

	// Record draft source for gate 2 UI.
	smState.DraftSource = "combined"
	smState.DraftFailureNotice = ""

	log.Printf("[orchestrator] dual-provider drafting complete: claude=%s, codex=%s, combined=%s",
		claudeOutPath, codexOutPath, combinedOutPath)

	o.logTransition(StateDrafting, StateHumanGate2)
	if err := o.sm.Transition(StateHumanGate2); err != nil {
		return fmt.Errorf("transition DRAFTING -> HUMAN_GATE_2: %w", err)
	}
	return nil
}

// buildCombinePrompt creates the prompt for the Claude reviser that combines
// two drafter outputs into a single cohesive draft.
func buildCombinePrompt(claudePath, codexPath, outputPath string) string {
	var b strings.Builder
	b.WriteString("# Draft Combine Agent\n\n")
	b.WriteString("You are the draft combine agent. Your task is to merge two independently-produced\n")
	b.WriteString("specification drafts into a single cohesive specification.\n\n")
	b.WriteString("## Source Drafts\n\n")
	fmt.Fprintf(&b, "- **Claude draft**: Read from `%s`\n", claudePath)
	fmt.Fprintf(&b, "- **Codex draft**: Read from `%s`\n\n", codexPath)
	b.WriteString("## Instructions\n\n")
	b.WriteString("1. Read both drafts completely.\n")
	b.WriteString("2. Produce a combined specification that incorporates the best elements of both.\n")
	b.WriteString("3. Where drafts agree, use the shared content.\n")
	b.WriteString("4. Where drafts differ, prefer Claude's version but incorporate unique insights from Codex.\n")
	b.WriteString("5. Preserve all BDD scenarios and test datasets from both drafts (deduplicate identical ones).\n")
	b.WriteString("6. The combined output must be valid JSON conforming to the DrafterOutput schema.\n\n")
	b.WriteString("## Output\n\n")
	fmt.Fprintf(&b, "Write the combined JSON output to: %s\n", outputPath)
	return b.String()
}

// concatenateDrafts creates a fallback combined output by taking the Claude
// draft as the base and noting that a Codex draft was also available.
// This is used when the Claude reviser fails to combine them.
func concatenateDrafts(claudeData, codexData []byte) []byte {
	// Use Claude's draft as the base (since Claude is the preferred provider).
	// Attempt to parse and annotate; if parsing fails, return Claude's raw data.
	var claudeDraft DrafterOutput
	if err := json.Unmarshal(claudeData, &claudeDraft); err != nil {
		return claudeData
	}

	// Add an ambiguity warning noting the combine failure.
	claudeDraft.AmbiguityWarnings = append(claudeDraft.AmbiguityWarnings, AmbiguityWarning{
		ID:              "AMB-W-COMBINE",
		Section:         "combined-draft",
		Ambiguity:       "Draft combine agent failed — this draft is from Claude only. A separate Codex draft is available for manual review.",
		AgentAssumption: "Using Claude draft as primary output",
		QuestionForUser: "Review the Codex draft file for any additional insights to incorporate.",
	})

	result, err := json.MarshalIndent(claudeDraft, "", "  ")
	if err != nil {
		return claudeData
	}
	return result
}

// NewDraftingEvent creates a workflow event envelope for drafting-related
// notifications such as single-provider fallback.
func NewDraftingEvent(eventType, detail string) EventEnvelope {
	return EventEnvelope{
		Event: "drafting_" + eventType,
		Data: GateResponseEvent{
			GateType: "drafting",
			Action:   eventType,
			Detail:   detail,
		},
	}
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
	gate2 := NewGate2Handler(state, o.emitter, o.config.MaxGate2Redrafts)

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

	// Persist reviewer comment if provided. Corrupted comments file → escalate.
	if err := persistComment(specDir, "HUMAN_GATE_2", resp.Action, resp.Comment); err != nil {
		var corrupted *ErrCommentsCorrupted
		if errors.As(err, &corrupted) {
			log.Printf("[orchestrator] %v", err)
			o.escalateFrom(StateHumanGate2)
			return nil
		}
		return fmt.Errorf("persist comment at HUMAN_GATE_2: %w", err)
	}

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
