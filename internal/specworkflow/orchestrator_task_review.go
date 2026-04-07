package specworkflow

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// taskReviewResult holds the outcome of a single task reviewer agent invocation.
type taskReviewResult struct {
	provider string
	outPath  string
	output   *ReviewerOutput
	cost     float64
	duration int64
	err      error
}

// handleTaskReview dispatches Claude and Codex task reviewers in parallel,
// merges findings using the existing dedup algorithm, and routes to
// StateTaskRevision on CRITICAL/MAJOR findings or StateTaskHumanGate on
// minor-only findings. When TaskReviewMaxRounds is reached, findings pass
// to StateTaskHumanGate regardless of severity.
func (o *Orchestrator) handleTaskReview(state *WorkflowStateJSON, specDir string) error {
	taskGraphPath := filepath.Join(filepath.Dir(filepath.Clean(o.workspaceDir)), ".tasks", o.featureName+".task.json")
	taskGraphData, err := os.ReadFile(taskGraphPath)
	if err != nil {
		return fmt.Errorf("read task graph for review: %w", err)
	}

	// Determine the task review round from persisted state (survives crash recovery).
	round := state.TaskReviewRound
	if round == 0 {
		round = 1
	}

	timeout := o.config.AgentTimeoutSeconds

	// Output paths — all inside the workspace (specDir).
	claudeOutPath := filepath.Join(specDir, VersionedFilename("task-review", "claude", round, ".json"))
	codexOutPath := filepath.Join(specDir, VersionedFilename("task-review", "codex", round, ".json"))
	claudeMDPath := filepath.Join(specDir, fmt.Sprintf("task-review-report-claude-round-%d.md", round))
	codexMDPath := filepath.Join(specDir, fmt.Sprintf("task-review-report-codex-round-%d.md", round))

	// Build task review prompts with explicit output paths so agents write only inside the workspace.
	claudePrompt := buildTaskReviewPrompt(string(taskGraphData), round, o.featureName, claudeOutPath, claudeMDPath)
	codexPrompt := buildTaskReviewPrompt(string(taskGraphData), round, o.featureName, codexOutPath, codexMDPath)

	// Dispatch both reviewers in parallel.
	var wg sync.WaitGroup
	var claudeResult, codexResult taskReviewResult

	// Claude reviewer.
	wg.Add(1)
	o.SetAgentStatus("task-reviewer-claude", "running")
	o.emitter.Emit(NewAgentDispatchEvent("task-reviewer-claude", round))
	go func() {
		defer wg.Done()
		runner := taggedRunner(o.runnerFor("task_reviewer"), "task-reviewer-claude")
		exitCode, stderr, cost, duration, runErr := runner.Run(claudePrompt, claudeOutPath, timeout)
		claudeResult = taskReviewResult{provider: "claude", outPath: claudeOutPath, cost: cost, duration: duration}
		if runErr != nil {
			claudeResult.err = fmt.Errorf("task-reviewer-claude failed: %v", runErr)
			return
		}
		failureType := DetectFailureType(exitCode, stderr, claudeOutPath)
		if failureType != "" {
			claudeResult.err = fmt.Errorf("task-reviewer-claude failed: %s", failureType)
			return
		}
		data, readErr := os.ReadFile(claudeOutPath)
		if readErr != nil {
			claudeResult.err = fmt.Errorf("task-reviewer-claude: read output: %v", readErr)
			return
		}
		var output ReviewerOutput
		if parseErr := json.Unmarshal(data, &output); parseErr != nil {
			claudeResult.err = fmt.Errorf("task-reviewer-claude: parse output: %v", parseErr)
			return
		}
		claudeResult.output = &output
	}()

	// Codex reviewer (when available).
	hasCodex := o.codexRunner != nil
	if hasCodex {
		wg.Add(1)
		o.SetAgentStatus("task-reviewer-codex", "running")
		o.emitter.Emit(NewAgentDispatchEvent("task-reviewer-codex", round))
		go func() {
			defer wg.Done()
			runner := taggedRunner(o.codexRunner, "task-reviewer-codex")
			exitCode, stderr, cost, duration, runErr := runner.Run(codexPrompt, codexOutPath, timeout)
			codexResult = taskReviewResult{provider: "codex", outPath: codexOutPath, cost: cost, duration: duration}
			if runErr != nil {
				codexResult.err = fmt.Errorf("task-reviewer-codex failed: %v", runErr)
				return
			}
			failureType := DetectFailureType(exitCode, stderr, codexOutPath)
			if failureType != "" {
				codexResult.err = fmt.Errorf("task-reviewer-codex failed: %s", failureType)
				return
			}
			data, readErr := os.ReadFile(codexOutPath)
			if readErr != nil {
				codexResult.err = fmt.Errorf("task-reviewer-codex: read output: %v", readErr)
				return
			}
			var output ReviewerOutput
			if parseErr := json.Unmarshal(data, &output); parseErr != nil {
				codexResult.err = fmt.Errorf("task-reviewer-codex: parse output: %v", parseErr)
				return
			}
			codexResult.output = &output
		}()
	}

	wg.Wait()

	// Accumulate cost.
	smState := o.sm.State()
	smState.CumulativeCostUSD += claudeResult.cost
	smState.AgentInvocations++
	if hasCodex {
		smState.CumulativeCostUSD += codexResult.cost
		smState.AgentInvocations++
	}

	// Emit completion events.
	claudeOK := claudeResult.err == nil
	codexOK := !hasCodex || codexResult.err == nil

	if claudeOK {
		o.SetAgentStatus("task-reviewer-claude", "done")
		o.emitter.Emit(NewAgentCompleteEvent("task-reviewer-claude", round, true, claudeResult.duration, claudeResult.cost))
	} else {
		o.SetAgentStatus("task-reviewer-claude", "failed")
		o.emitter.Emit(NewAgentCompleteEvent("task-reviewer-claude", round, false, claudeResult.duration, claudeResult.cost))
		log.Printf("[orchestrator] task-reviewer-claude failed: %v", claudeResult.err)
	}
	if hasCodex {
		if codexResult.err == nil {
			o.SetAgentStatus("task-reviewer-codex", "done")
			o.emitter.Emit(NewAgentCompleteEvent("task-reviewer-codex", round, true, codexResult.duration, codexResult.cost))
		} else {
			o.SetAgentStatus("task-reviewer-codex", "failed")
			o.emitter.Emit(NewAgentCompleteEvent("task-reviewer-codex", round, false, codexResult.duration, codexResult.cost))
			log.Printf("[orchestrator] task-reviewer-codex failed: %v", codexResult.err)
		}
	}

	// Both failed → escalate.
	if !claudeOK && (!codexOK || !hasCodex) {
		reason := fmt.Sprintf("both task reviewers failed: claude=%v", claudeResult.err)
		if hasCodex {
			reason = fmt.Sprintf("both task reviewers failed: claude=%v; codex=%v", claudeResult.err, codexResult.err)
		}
		log.Printf("[orchestrator] %s", reason)
		o.escalateFrom(StateTaskReview)
		return nil
	}

	// Collect successful outputs.
	var reviewerOutputs []*ReviewerOutput
	if claudeOK && claudeResult.output != nil {
		reviewerOutputs = append(reviewerOutputs, claudeResult.output)
	}
	if hasCodex && codexOK && codexResult.output != nil {
		reviewerOutputs = append(reviewerOutputs, codexResult.output)
	}

	// Log reduced coverage when one reviewer failed.
	if hasCodex && (claudeOK != codexOK) {
		var failedProvider string
		if !claudeOK {
			failedProvider = "claude"
		} else {
			failedProvider = "codex"
		}
		log.Printf("[orchestrator] task review: %s reviewer failed — using single-provider findings (reduced coverage)", failedProvider)
	}

	// Merge findings using the same dedup algorithm as spec review.
	merged, err := MergeReviewerOutputs(reviewerOutputs, round, SpecDedupKey, true)
	if err != nil {
		return fmt.Errorf("merge task review findings: %w", err)
	}

	// Write merged findings to task-findings-round-{N}.json.
	findingsPath := filepath.Join(specDir, fmt.Sprintf("task-findings-round-%d.json", round))
	findingsData, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal task review findings: %w", err)
	}
	if err := os.WriteFile(findingsPath, findingsData, 0o644); err != nil {
		return fmt.Errorf("write task-findings-round-%d.json: %w", round, err)
	}

	log.Printf("[orchestrator] task review round %d: %d findings (%d after dedup, %d critical, %d major)",
		round, merged.TotalFindings, merged.TotalAfterDedup,
		countSeverity(merged.Findings, SeverityCritical), countSeverity(merged.Findings, SeverityMajor))

	// Route based on severity and round limits.
	hasCriticalOrMajor := countSeverity(merged.Findings, SeverityCritical) > 0 ||
		countSeverity(merged.Findings, SeverityMajor) > 0

	maxRoundsReached := round >= o.config.TaskReviewMaxRounds

	if hasCriticalOrMajor && !maxRoundsReached {
		state.TaskReviewRound = round + 1
		o.logTransition(StateTaskReview, StateTaskRevision)
		if err := o.sm.Transition(StateTaskRevision); err != nil {
			return fmt.Errorf("transition TASK_REVIEW -> TASK_REVISION: %w", err)
		}
	} else {
		if maxRoundsReached && hasCriticalOrMajor {
			log.Printf("[orchestrator] task review max rounds reached (%d/%d) with critical/major findings — routing to human gate",
				round, o.config.TaskReviewMaxRounds)
		}
		o.logTransition(StateTaskReview, StateTaskHumanGate)
		if err := o.sm.Transition(StateTaskHumanGate); err != nil {
			return fmt.Errorf("transition TASK_REVIEW -> TASK_HUMAN_GATE: %w", err)
		}
	}

	return nil
}

// countSeverity counts the number of findings with the given severity level.
func countSeverity(findings []MergedFinding, severity Severity) int {
	count := 0
	for _, f := range findings {
		if f.Severity == severity {
			count++
		}
	}
	return count
}

// handleTaskRevision dispatches a Claude revision agent with the current task
// graph, merged review findings, and human comments. The revised task graph is
// validated before transitioning back to StateTaskReview. If validation fails,
// the workflow escalates.
func (o *Orchestrator) handleTaskRevision(state *WorkflowStateJSON, specDir string) error {
	taskGraphPath := filepath.Join(filepath.Dir(filepath.Clean(o.workspaceDir)), ".tasks", o.featureName+".task.json")
	taskGraphData, err := os.ReadFile(taskGraphPath)
	if err != nil {
		return fmt.Errorf("read task graph for revision: %w", err)
	}

	// Determine the round: the findings file is from the previous review round.
	round := state.TaskReviewRound
	if round == 0 {
		round = 1
	}
	findingsRound := round - 1
	if findingsRound < 1 {
		findingsRound = 1
	}

	// Read merged findings from the most recent task review round.
	findingsPath := filepath.Join(specDir, fmt.Sprintf("task-findings-round-%d.json", findingsRound))
	findingsData, err := os.ReadFile(findingsPath)
	if err != nil {
		log.Printf("[orchestrator] warning: could not read task findings round %d: %v", findingsRound, err)
		findingsData = []byte("{}")
	}

	// Load human comments.
	humanComments, commentsErr := loadHumanComments(specDir)
	if commentsErr != nil {
		log.Printf("[orchestrator] %v", commentsErr)
		o.escalateFrom(StateTaskRevision)
		return nil
	}

	// Build revision prompt.
	prompt := buildTaskRevisionPrompt(string(taskGraphData), string(findingsData), humanComments, o.featureName)

	// Dispatch Claude revision agent (single provider).
	o.SetAgentStatus("task-revision-claude", "running")
	o.emitter.Emit(NewAgentDispatchEvent("task-revision-claude", round))

	cost, duration, runErr := o.dispatchAgent("task-revision-claude", prompt, taskGraphPath, o.runnerFor("task_reviser"))

	if runErr != nil {
		o.SetAgentStatus("task-revision-claude", "failed")
		log.Printf("[orchestrator] task revision agent failed: %v", runErr)
		o.emitter.Emit(NewAgentCompleteEvent("task-revision-claude", round, false, duration, cost))
		o.escalateFrom(StateTaskRevision)
		return nil
	}

	o.SetAgentStatus("task-revision-claude", "done")
	o.emitter.Emit(NewAgentCompleteEvent("task-revision-claude", round, true, duration, cost))

	// Read and validate the revised task graph.
	revisedData, readErr := os.ReadFile(taskGraphPath)
	if readErr != nil {
		log.Printf("[orchestrator] task revision: could not read revised task graph: %v", readErr)
		o.escalateFrom(StateTaskRevision)
		return nil
	}

	validationErrors := ValidateTaskGraph(revisedData)
	if len(validationErrors) > 0 {
		log.Printf("[orchestrator] task revision: revised graph failed validation: %v", validationErrors)
		o.escalateFrom(StateTaskRevision)
		return nil
	}

	log.Printf("[orchestrator] task revision complete — revised graph valid, transitioning to TASK_REVIEW")
	o.logTransition(StateTaskRevision, StateTaskReview)
	if err := o.sm.Transition(StateTaskReview); err != nil {
		return fmt.Errorf("transition TASK_REVISION -> TASK_REVIEW: %w", err)
	}

	return nil
}

// buildTaskReviewPrompt constructs the prompt for task review agents.
// outputJSONPath is the absolute path where the agent must write its ReviewerOutput JSON.
// markdownReportPath is the absolute path where the agent must write its markdown report.
// Both paths must be inside the workspace (specDir).
func buildTaskReviewPrompt(taskGraphJSON string, round int, featureName, outputJSONPath, markdownReportPath string) string {
	var b strings.Builder

	b.WriteString("# Task Graph Review Agent\n\n")
	b.WriteString("You are a task graph review agent. Your job is to review the following task graph\n")
	b.WriteString("for completeness, correctness, and quality.\n\n")
	fmt.Fprintf(&b, "This is review round %d for feature: %s\n\n", round, featureName)

	b.WriteString("## Task Graph\n\n")
	b.WriteString("<task_graph>\n")
	b.WriteString(taskGraphJSON)
	b.WriteString("\n</task_graph>\n\n")

	b.WriteString("## Review Criteria\n\n")
	b.WriteString("Review the task graph for:\n")
	b.WriteString("1. **Completeness**: Are all necessary tasks present? Are there gaps in coverage?\n")
	b.WriteString("2. **Dependencies**: Are depends_on relationships correct? Is the DAG sound?\n")
	b.WriteString("3. **Acceptance Criteria**: Are acceptance criteria specific and testable?\n")
	b.WriteString("4. **Scope**: Is each task appropriately sized? Too broad or too narrow?\n")
	b.WriteString("5. **Priority/Estimate**: Are priorities and estimates reasonable?\n\n")

	b.WriteString("## Output Schema\n\n")
	b.WriteString("You MUST produce valid JSON conforming to the ReviewerOutput schema:\n")
	b.WriteString("- schema_version (string, required)\n")
	b.WriteString("- agent (string, required): set to \"task-reviewer\"\n")
	fmt.Fprintf(&b, "- round (int, required): set to %d\n", round)
	b.WriteString("- lenses_applied (array of string, required): [\"completeness\", \"dependencies\", \"acceptance\", \"scope\", \"priority\"]\n")
	b.WriteString("- findings (array of Finding): id, description, severity (CRITICAL|MAJOR|MINOR|OBSERVATION), impact, recommendation, lens, affected_section\n")
	b.WriteString("- structural_integrity (object): performed (bool), checks (array of IntegrityCheck)\n")
	fmt.Fprintf(&b, "- markdown_report_file (string, required): MUST be set to \"%s\"\n\n", markdownReportPath)

	b.WriteString("## Output Files\n\n")
	fmt.Fprintf(&b, "1. Write your ReviewerOutput JSON to: %s\n", outputJSONPath)
	fmt.Fprintf(&b, "2. Write your markdown report to: %s\n", markdownReportPath)
	b.WriteString("Do NOT write any files outside the workspace.\n")

	return b.String()
}

// buildTaskRevisionPrompt constructs the prompt for the task revision agent
// following the spec's Task Revision Prompt Template.
func buildTaskRevisionPrompt(taskGraphJSON, findingsJSON, humanComments, featureName string) string {
	var b strings.Builder

	b.WriteString("Revise this task graph to address the review findings.\n\n")

	b.WriteString("## Current Task Graph\n")
	b.WriteString(taskGraphJSON)
	b.WriteString("\n\n")

	b.WriteString("## Review Findings\n")
	b.WriteString(findingsJSON)
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

	b.WriteString("## Instructions\n")
	b.WriteString("- Address all CRITICAL and MAJOR findings\n")
	b.WriteString("- Preserve task IDs where possible for traceability\n")
	b.WriteString("- You may add, modify, or remove tasks as needed to address findings\n")
	fmt.Fprintf(&b, "- The revised task graph MUST conform to the TaskGraphFile JSON Schema at .tasks/task-graph-schema.json\n")
	b.WriteString("- Ensure the dependency graph (depends_on) remains a valid DAG\n")
	b.WriteString("- Do NOT write any other files — only write the task graph JSON below\n")
	fmt.Fprintf(&b, "\n## Output File\n\nWrite the revised task graph JSON to: .tasks/%s.task.json\n", featureName)

	return b.String()
}
