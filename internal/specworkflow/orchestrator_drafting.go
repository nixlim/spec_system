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

	// Determine drafting version counter (increments on re-drafts).
	version := state.Gate2RedraftCount + 1

	// Multi-provider path. Each provider gets its own prompt with a per-provider
	// versioned output path so the runners can read what the agents actually
	// wrote, instead of racing on the shared canonical drafter-output.json.
	if (o.config.EnableCodexDrafting && o.codexDraftingRunner != nil) ||
		(o.config.EnableOpenCodeDrafting && o.opencodeDraftingRunner != nil) {
		return o.handleDualDrafting(state, specDir, confirmedReqsPath, userAnswers, contextDocs, version)
	}

	prompt, err := o.promptBuilder.BuildDrafterPrompt(confirmedReqsPath, userAnswers, contextDocs)
	if err != nil {
		return fmt.Errorf("build drafter prompt: %w", err)
	}

	// Codex requested but unavailable — log fallback.
	if o.config.EnableCodexDrafting && o.codexDraftingRunner == nil {
		log.Printf("[orchestrator] Codex unavailable, falling back to Claude-only drafting")
	}

	// Single-provider path — enforce structured output when the runner supports it.
	drafterRunner := o.runnerFor("drafter")
	if se, ok := drafterRunner.(SchemaEnforcer); ok {
		drafterRunner = se.WithSchemaEnforcement(DrafterOutputSchema())
	}
	err = o.handleSingleDrafting(state, specDir, prompt, drafterRunner)
	return err
}

// handleSingleDrafting is the original single-provider drafting path.
func (o *Orchestrator) handleSingleDrafting(state *WorkflowStateJSON, specDir, prompt string, drafterRunner AgentRunner) error {
	outPath := filepath.Join(specDir, "drafter-output.json")

	cost, duration, err := o.dispatchAgent("drafter", prompt, outPath, drafterRunner)
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
//
// Each provider gets its OWN prompt with its own per-provider versioned
// output path (drafter-output-claude-vN.json / drafter-output-codex-vN.json).
// This avoids two consequences of the previous shared-prompt design:
//  1. Both agents racing to write the same canonical drafter-output.json.
//  2. The runner's outputPath not matching what the agent was actually
//     instructed to write, which caused claude-runner to fall back to
//     parsing stdout (a markdown summary) and report invalid_json even
//     when the spec was successfully drafted on disk.
func (o *Orchestrator) handleDualDrafting(state *WorkflowStateJSON, specDir, confirmedReqsPath string, userAnswers map[string]string, contextDocs []string, version int) error {
	claudeOutPath := filepath.Join(specDir, VersionedFilename("drafter-output", o.primaryProviderName(), version, ".json"))
	codexOutPath := filepath.Join(specDir, VersionedFilename("drafter-output", "codex", version, ".json"))
	opencodeOutPath := filepath.Join(specDir, VersionedFilename("drafter-output", "opencode", version, ".json"))
	timeout := o.config.AgentTimeoutSeconds

	// Build per-provider prompts pointing at the per-provider output paths.
	claudePrompt, err := o.promptBuilder.BuildDrafterPrompt(confirmedReqsPath, userAnswers, contextDocs, claudeOutPath)
	if err != nil {
		return fmt.Errorf("build claude drafter prompt: %w", err)
	}
	codexPrompt, err := o.promptBuilder.BuildDrafterPrompt(confirmedReqsPath, userAnswers, contextDocs, codexOutPath)
	if err != nil {
		return fmt.Errorf("build codex drafter prompt: %w", err)
	}
	opencodePrompt, err := o.promptBuilder.BuildDrafterPrompt(confirmedReqsPath, userAnswers, contextDocs, opencodeOutPath)
	if err != nil {
		return fmt.Errorf("build opencode drafter prompt: %w", err)
	}

	// Dispatch drafters in parallel.
	var wg sync.WaitGroup
	var claudeResult, codexResult, opencodeResult drafterResult

	// Build a schema-bound primary runner for structured output.
	var drafterClaudeRunner AgentRunner = o.runnerFor("drafter")
	if se, ok := drafterClaudeRunner.(SchemaEnforcer); ok {
		drafterClaudeRunner = se.WithSchemaEnforcement(DrafterOutputSchema())
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		claudeRunner := taggedRunner(drafterClaudeRunner, "drafter-claude")
		exitCode, stderr, cost, duration, runErr := claudeRunner.Run(claudePrompt, claudeOutPath, timeout)
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

	if o.codexDraftingRunner != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codexRunner := taggedRunner(o.codexDraftingRunner, "drafter-codex")
			exitCode, stderr, cost, duration, runErr := codexRunner.Run(codexPrompt, codexOutPath, timeout)
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
	}

	if o.opencodeDraftingRunner != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ocRunner := taggedRunner(o.opencodeDraftingRunner, "drafter-opencode")
			exitCode, stderr, cost, duration, runErr := ocRunner.Run(opencodePrompt, opencodeOutPath, timeout)
			opencodeResult = drafterResult{provider: "opencode", outPath: opencodeOutPath, cost: cost, duration: duration}
			if runErr != nil {
				opencodeResult.err = fmt.Errorf("drafter-opencode failed: %v", runErr)
				return
			}
			failureType := DetectFailureType(exitCode, stderr, opencodeOutPath)
			if failureType != "" {
				opencodeResult.err = fmt.Errorf("drafter-opencode failed: %s", failureType)
			}
		}()
	}

	wg.Wait()

	// Accumulate cost.
	smState := o.sm.State()
	smState.CumulativeCostUSD += claudeResult.cost + codexResult.cost + opencodeResult.cost
	invocations := 1 // claude always
	if o.codexDraftingRunner != nil {
		invocations++
	}
	if o.opencodeDraftingRunner != nil {
		invocations++
	}
	smState.AgentInvocations += invocations

	// Collect successful results.
	var successfulDrafts []drafterResult
	if claudeResult.err == nil {
		successfulDrafts = append(successfulDrafts, claudeResult)
	}
	if o.codexDraftingRunner != nil && codexResult.err == nil {
		successfulDrafts = append(successfulDrafts, codexResult)
	}
	if o.opencodeDraftingRunner != nil && opencodeResult.err == nil {
		successfulDrafts = append(successfulDrafts, opencodeResult)
	}

	// All failed → escalate.
	if len(successfulDrafts) == 0 {
		reason := fmt.Sprintf("all drafters failed: claude=%v", claudeResult.err)
		if o.codexDraftingRunner != nil {
			reason += fmt.Sprintf("; codex=%v", codexResult.err)
		}
		if o.opencodeDraftingRunner != nil {
			reason += fmt.Sprintf("; opencode=%v", opencodeResult.err)
		}
		log.Printf("[orchestrator] %s", reason)
		o.escalateFrom(StateDrafting)
		return nil
	}

	// Determine the final output path for drafter-output.json (used by gate 2).
	finalOutPath := filepath.Join(specDir, "drafter-output.json")

	// Single survivor → use directly.
	if len(successfulDrafts) == 1 {
		survivor := successfulDrafts[0]

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

		// Build failure notice listing failed providers.
		var failedProviders []string
		if claudeResult.err != nil {
			failedProviders = append(failedProviders, "claude")
		}
		if o.codexDraftingRunner != nil && codexResult.err != nil {
			failedProviders = append(failedProviders, "codex")
		}
		if o.opencodeDraftingRunner != nil && opencodeResult.err != nil {
			failedProviders = append(failedProviders, "opencode")
		}
		smState.DraftSource = "single_survivor"
		smState.DraftFailureNotice = fmt.Sprintf("%s drafter(s) failed — reviewing %s draft only",
			strings.Join(failedProviders, ", "), survivor.provider)

		o.emitter.Emit(NewDraftingEvent("single_provider_fallback",
			fmt.Sprintf("%s drafter(s) failed — using %s draft only",
				strings.Join(failedProviders, ", "), survivor.provider)))

		o.logTransition(StateDrafting, StateHumanGate2)
		if err := o.sm.Transition(StateHumanGate2); err != nil {
			return fmt.Errorf("transition DRAFTING -> HUMAN_GATE_2: %w", err)
		}
		return nil
	}

	// Multiple succeeded → validate and combine via Claude reviser.
	// Use the first two successful drafts for the agent-based combine,
	// then fold in any additional drafts via concatenation.
	first := successfulDrafts[0]
	second := successfulDrafts[1]

	claudeData, err := os.ReadFile(first.outPath)
	if err != nil {
		return fmt.Errorf("read %s drafter output: %w", first.provider, err)
	}
	var firstDraft DrafterOutput
	if err := json.Unmarshal(claudeData, &firstDraft); err != nil {
		return fmt.Errorf("parse %s drafter output: %w", first.provider, err)
	}

	codexData, err := os.ReadFile(second.outPath)
	if err != nil {
		return fmt.Errorf("read %s drafter output: %w", second.provider, err)
	}
	var secondDraft DrafterOutput
	if err := json.Unmarshal(codexData, &secondDraft); err != nil {
		return fmt.Errorf("parse %s drafter output: %w", second.provider, err)
	}

	// Dispatch Claude reviser to combine drafts.
	combinedOutPath := filepath.Join(specDir, VersionedCombinedFilename("drafter-output", version, ".json"))
	combinePrompt := buildCombinePrompt(first.outPath, second.outPath, combinedOutPath)

	combineRunner := taggedRunner(o.runnerFor("drafter"), "drafter-combine")
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

	// Fold in third provider's output if present.
	if len(successfulDrafts) >= 3 {
		combinedData, readErr := os.ReadFile(combinedOutPath)
		if readErr == nil {
			thirdData, thirdReadErr := os.ReadFile(successfulDrafts[2].outPath)
			if thirdReadErr == nil {
				merged := concatenateDrafts(combinedData, thirdData)
				_ = os.WriteFile(combinedOutPath, merged, 0o644)
			}
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

	log.Printf("[orchestrator] multi-provider drafting complete: %d providers succeeded, combined=%s",
		len(successfulDrafts), combinedOutPath)

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
	// Create gate proxy in Beads (CD-88v.10).
	o.openGateProxy("gate2", o.featureName+": gate-2 — Spec review required")

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

	// Wait for human response — race gateCh against Beads gate polling (CD-88v.10).
	resp := o.waitForGateResponse("confirm", "correct")
	if resp.Action == gateActionCancelled {
		return fmt.Errorf("workflow cancelled")
	}

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
