package specworkflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// handleTaskify dispatches Claude CLI with /taskify to decompose the approved
// spec into a structured task graph. It validates output against the task graph
// schema and retries with validation errors on failure, up to TaskifyMaxRetries.
// On exhausting retries, the workflow escalates.
func (o *Orchestrator) handleTaskify(state *WorkflowStateJSON, specDir string) error {
	taskGraphPath := filepath.Join(o.workspaceDir, ".tasks", o.featureName+".task.json")

	// Ensure .tasks directory exists.
	if err := os.MkdirAll(filepath.Dir(taskGraphPath), 0o755); err != nil {
		return fmt.Errorf("create .tasks directory: %w", err)
	}

	// Read spec-final.md content for the prompt. If it is missing (e.g. a
	// prior run failed to write it), attempt to assemble it now before
	// proceeding so the workflow can self-recover.
	specFinalPath := filepath.Join(specDir, "spec-final.md")
	if _, statErr := os.Stat(specFinalPath); os.IsNotExist(statErr) {
		log.Printf("[orchestrator] spec-final.md missing — attempting late assembly")
		finConfig := FinalizeConfig{WorkspaceDir: o.workspaceDir, FeatureName: o.featureName}
		if asmErr := AssembleFinalSpec(finConfig, state, o.tracker); asmErr != nil {
			return fmt.Errorf("spec-final.md missing and late assembly failed: %w", asmErr)
		}
		log.Printf("[orchestrator] late assembly of spec-final.md succeeded")
	}
	specContent, err := os.ReadFile(specFinalPath)
	if err != nil {
		return fmt.Errorf("read spec-final.md: %w", err)
	}

	// Load human comments for inclusion in prompt.
	humanComments, commentsErr := loadHumanComments(specDir)
	if commentsErr != nil {
		log.Printf("[orchestrator] %v", commentsErr)
		o.escalateFrom(StateTaskify)
		return nil
	}

	maxRetries := o.config.TaskifyMaxRetries
	var lastValidationErrors []string

	for attempt := 1; attempt <= maxRetries; attempt++ {
		log.Printf("[orchestrator] taskify attempt %d/%d", attempt, maxRetries)

		// Build prompt.
		prompt := buildTaskifyPrompt(string(specContent), humanComments, lastValidationErrors, o.featureName)

		// Emit dispatch event.
		o.emitter.Emit(NewAgentDispatchEvent("taskify", state.Round))

		// Dispatch agent.
		cost, duration, runErr := o.dispatchAgent("taskify", prompt, taskGraphPath, o.runnerFor("taskify"))
		if runErr != nil {
			log.Printf("[orchestrator] taskify agent failed on attempt %d: %v", attempt, runErr)
			lastValidationErrors = []string{fmt.Sprintf("agent dispatch failed: %v", runErr)}
			o.emitter.Emit(NewAgentErrorEvent("taskify", "dispatch_failure", runErr.Error(), attempt, maxRetries))
			_ = cost
			_ = duration
			continue
		}

		// Check if output file exists.
		data, readErr := os.ReadFile(taskGraphPath)
		if readErr != nil {
			log.Printf("[orchestrator] taskify output file missing on attempt %d: %v", attempt, readErr)
			lastValidationErrors = []string{"task graph output file was not created"}
			o.emitter.Emit(NewAgentErrorEvent("taskify", "missing_output", readErr.Error(), attempt, maxRetries))
			continue
		}

		// Validate against schema and DAG rules.
		validationErrors := ValidateTaskGraph(data)
		if len(validationErrors) == 0 {
			log.Printf("[orchestrator] taskify output valid on attempt %d", attempt)
			o.logTransition(StateTaskify, StateTaskReview)
			if err := o.sm.Transition(StateTaskReview); err != nil {
				return fmt.Errorf("transition TASKIFY -> TASK_REVIEW: %w", err)
			}
			return nil
		}

		// Validation failed — log and prepare for retry.
		log.Printf("[orchestrator] taskify validation failed on attempt %d: %v", attempt, validationErrors)
		lastValidationErrors = validationErrors
		o.emitter.Emit(NewAgentErrorEvent("taskify", "validation_failure",
			fmt.Sprintf("validation errors: %s", strings.Join(validationErrors, "; ")),
			attempt, maxRetries))
	}

	// All retries exhausted — post Beads notification and escalate (CD-88v.15).
	o.postTaskvalExhaustion(lastValidationErrors)
	reason := fmt.Sprintf("taskify failed after %d attempts: %s", maxRetries, strings.Join(lastValidationErrors, "; "))
	log.Printf("[orchestrator] %s", reason)
	o.escalateFrom(StateTaskify)
	return nil
}

// loadHumanComments reads human-comments.json and returns the formatted
// content for inclusion in agent prompts. Returns empty string if no
// comments exist or if the file cannot be read. Returns a non-nil error
// if the file exists but contains invalid JSON (corrupted), so callers
// can escalate per the spec requirement.
func loadHumanComments(specDir string) (string, error) {
	commentsPath := filepath.Join(specDir, "human-comments.json")
	data, err := os.ReadFile(commentsPath)
	if err != nil {
		return "", nil
	}

	var comments []struct {
		Gate      string `json:"gate"`
		Action    string `json:"action"`
		Comment   string `json:"comment"`
		Timestamp string `json:"timestamp"`
	}
	if err := json.Unmarshal(data, &comments); err != nil {
		return "", fmt.Errorf("human-comments.json is corrupted: %w", err)
	}

	if len(comments) == 0 {
		return "", nil
	}

	var b strings.Builder
	for _, c := range comments {
		fmt.Fprintf(&b, "- [%s/%s at %s]: %s\n", c.Gate, c.Action, c.Timestamp, c.Comment)
	}
	return b.String(), nil
}

// handleTaskHumanGate waits for a human gate response at TASK_HUMAN_GATE and
// routes to one of three actions:
//   - approve: persist .tasks/{feature}.task.json, attempt taskval --create-beads,
//     transition through TASKS_APPROVED to COMPLETE
//   - correct: persist comment, dispatch fresh /taskify re-run with feedback,
//     transition to TASKIFY
//   - skip: transition directly to COMPLETE without creating tasks
func (o *Orchestrator) handleTaskHumanGate(state *WorkflowStateJSON, specDir string) error {
	taskGraphPath := filepath.Join(o.workspaceDir, ".tasks", o.featureName+".task.json")

	// Create gate proxy in Beads (CD-88v.10).
	o.openGateProxy("taskgate", o.featureName+": gate-taskify — Taskify review required")

	// Emit gate request event.
	o.emitter.Emit(NewGateRequestEvent("task_human_gate", "task_approval", map[string]string{
		"task_graph_path": taskGraphPath,
	}))

	// Wait for human response — race gateCh against Beads gate polling (CD-88v.10).
	resp := o.waitForGateResponse("approve", "correct")
	if resp.Action == gateActionCancelled {
		return fmt.Errorf("workflow cancelled")
	}

	log.Printf("[orchestrator] task human gate response: action=%s", resp.Action)

	// Persist comment before any state transition. Corrupted comments → escalate.
	if resp.Comment != "" {
		if err := persistComment(specDir, "TASK_HUMAN_GATE", resp.Action, resp.Comment); err != nil {
			var corrupted *ErrCommentsCorrupted
			if errors.As(err, &corrupted) {
				log.Printf("[orchestrator] %v", err)
				o.escalateFrom(StateTaskHumanGate)
				return nil
			}
			return fmt.Errorf("persist comment at TASK_HUMAN_GATE: %w", err)
		}
	}

	switch resp.Action {
	case "approve":
		o.emitter.Emit(NewGateResponseEvent("task_human_gate", "approve", "Tasks approved by human"))

		// Task graph MUST exist at taskGraphPath per FR-027.
		if _, err := os.Stat(taskGraphPath); err != nil {
			log.Printf("[orchestrator] task graph file not found at %s: %v", taskGraphPath, err)
			o.escalateFrom(StateTaskHumanGate)
			return nil
		}
		log.Printf("[orchestrator] task graph persisted at %s", taskGraphPath)

		// Attempt to create Beads issues via taskval.
		beadsResult := attemptCreateBeads(taskGraphPath)
		if beadsResult != "" {
			o.emitter.Emit(NewGateResponseEvent("task_human_gate", "beads_result", beadsResult))
			log.Printf("[orchestrator] taskval result: %s", beadsResult)
		}

		// Transition: TASK_HUMAN_GATE -> TASKS_APPROVED -> COMPLETE
		o.logTransition(StateTaskHumanGate, StateTasksApproved)
		if err := o.sm.Transition(StateTasksApproved); err != nil {
			return fmt.Errorf("transition TASK_HUMAN_GATE -> TASKS_APPROVED: %w", err)
		}
		o.logTransition(StateTasksApproved, StateComplete)
		if err := o.sm.Transition(StateComplete); err != nil {
			return fmt.Errorf("transition TASKS_APPROVED -> COMPLETE: %w", err)
		}
		return nil

	case "correct":
		o.emitter.Emit(NewGateResponseEvent("task_human_gate", "correct", "Fresh taskify re-run requested"))

		// Reset task review round for the fresh re-run.
		state.TaskReviewRound = 0

		// Transition to TASKIFY for a fresh re-run with human feedback.
		o.logTransition(StateTaskHumanGate, StateTaskify)
		if err := o.sm.Transition(StateTaskify); err != nil {
			return fmt.Errorf("transition TASK_HUMAN_GATE -> TASKIFY: %w", err)
		}
		return nil

	case "skip":
		log.Printf("[orchestrator] task creation skipped by human")
		o.emitter.Emit(NewGateResponseEvent("task_human_gate", "skip", "Task creation skipped by human"))

		// Transition directly to COMPLETE.
		o.logTransition(StateTaskHumanGate, StateComplete)
		if err := o.sm.Transition(StateComplete); err != nil {
			return fmt.Errorf("transition TASK_HUMAN_GATE -> COMPLETE: %w", err)
		}
		return nil

	default:
		return fmt.Errorf("unknown task human gate action: %q", resp.Action)
	}
}

// attemptCreateBeads tries to run taskval --create-beads on the task graph file.
// Returns a human-readable result string. If taskval is unavailable, returns a
// warning message. taskval unavailability must never block the workflow.
func attemptCreateBeads(taskGraphPath string) string {
	if _, err := exec.LookPath("taskval"); err != nil {
		return "taskval unavailable -- tasks saved but Beads not created"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "taskval", "--mode=graph", "--create-beads", taskGraphPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Sprintf("taskval failed: %v (output: %s)", err, strings.TrimSpace(string(output)))
	}

	return fmt.Sprintf("Beads created successfully: %s", strings.TrimSpace(string(output)))
}

// buildTaskifyPrompt constructs the prompt for the taskify agent following
// the spec's Taskify Prompt Template.
func buildTaskifyPrompt(specContent, humanComments string, validationErrors []string, featureName string) string {
	var b strings.Builder

	b.WriteString("Decompose this approved specification into a structured task graph.\n\n")

	b.WriteString("## Specification\n")
	b.WriteString(specContent)
	b.WriteString("\n\n")

	b.WriteString("## Human Feedback\n")
	b.WriteString("<human_feedback>\n")
	if humanComments != "" {
		b.WriteString(humanComments)
	} else {
		b.WriteString("No human feedback provided.\n")
	}
	b.WriteString("IMPORTANT: Address all human feedback with high priority.\n")
	b.WriteString("</human_feedback>\n\n")

	if len(validationErrors) > 0 {
		b.WriteString("## Validation Errors (from previous attempt)\n")
		b.WriteString("The previous attempt failed validation. Fix ALL of these errors:\n\n")
		for _, e := range validationErrors {
			fmt.Fprintf(&b, "- %s\n", e)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Output Requirements\n")
	fmt.Fprintf(&b, "Write a JSON file to: .tasks/%s.task.json\n", featureName)
	b.WriteString("The file MUST conform to the TaskGraphFile JSON Schema at .tasks/task-graph-schema.json.\n\n")

	b.WriteString("### Top-level fields\n")
	b.WriteString("- `version` (string, required): schema version, e.g. \"0.1.0\"\n")
	b.WriteString("- `tasks` (array, non-empty, required): list of task nodes\n\n")

	b.WriteString("### Required fields per task node (STRUCTURED_TEMPLATE_SPEC §3.1)\n")
	b.WriteString("Every task MUST include all 6 required fields:\n\n")
	b.WriteString("1. `task_id` (string): Kebab-case, globally unique. Pattern: ^[a-z0-9]+(-[a-z0-9]+)*$. Max 60 chars.\n")
	b.WriteString("2. `task_name` (string): Short imperative phrase starting with a verb. Max 80 chars.\n")
	b.WriteString("3. `goal` (string): Single sentence describing a testable outcome.\n")
	b.WriteString("   - MUST NOT contain the words \"try\", \"explore\", \"investigate\", or \"look into\" (these indicate underspecified tasks).\n")
	b.WriteString("4. `inputs` (list<InputSpec>): Each entry: {name, type, constraints, source}.\n")
	b.WriteString("5. `outputs` (list<OutputSpec>): Each entry: {name, type, constraints, destination}.\n")
	b.WriteString("6. `acceptance` (list<string>, non-empty): Testable assertions. Each must be independently verifiable.\n\n")

	b.WriteString("### Contextual fields\n")
	b.WriteString("- `depends_on`: list of task_id references forming a valid DAG (no cycles, no self-references, all refs must exist).\n")
	b.WriteString("  If a task has NO dependencies, write: {\"status\": \"N/A\", \"reason\": \"<justification>\"} — NOT an empty array.\n")
	b.WriteString("- `constraints`: list of hard rules or architectural boundaries.\n")
	b.WriteString("- `files_scope`: list of file paths or globs the task will create or modify.\n\n")

	b.WriteString("### Optional fields\n")
	b.WriteString("- `priority`: one of \"critical\", \"high\", \"medium\", \"low\"\n")
	b.WriteString("- `estimate`: one of \"trivial\", \"small\", \"medium\", \"large\", \"unknown\"\n\n")

	b.WriteString("### Type vocabulary (STRUCTURED_TEMPLATE_SPEC §4)\n")
	b.WriteString("Use these types for `inputs[].type` and `outputs[].type` — NOT Go or TypeScript types:\n")
	b.WriteString("- Primitives: string, int, i32, i64, float, f64, bool, bytes\n")
	b.WriteString("- Compound: list<T>, map<K,V>, option<T>, union(A,B,C), tuple(T1,T2)\n")
	b.WriteString("- Refined: int(1..100), float(> 0), string(len: 1..2000), string(pattern: \"...\"), list<T>(len: 1..50)\n")
	b.WriteString("- Domain: filepath, url, uuid, datetime, exit_code\n")

	return b.String()
}
