package specworkflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// taskifyTestOrchestrator creates a minimal Orchestrator wired for taskify tests.
func taskifyTestOrchestrator(t *testing.T, runner *orchMockRunner) (*Orchestrator, string) {
	t.Helper()

	dir := t.TempDir()
	specDir := filepath.Join(dir, "specs", "test-feature")
	tasksDir := filepath.Join(dir, ".tasks")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write spec-final.md.
	if err := os.WriteFile(filepath.Join(specDir, "spec-final.md"), []byte("# Final Spec\nContent here."), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := orchTestConfig()
	cfg.TaskifyMaxRetries = 3

	now := time.Now().UTC().Format(time.RFC3339)
	ws := &WorkflowStateJSON{
		State:       StateTaskify,
		Round:       1,
		FeatureName: "test-feature",
		StartedAt:   now,
		UpdatedAt:   now,
	}

	smConfig := StateMachineConfig{
		MaxGateCorrections: cfg.MaxGateCorrections,
		MaxGate2Redrafts:   cfg.MaxGate2Redrafts,
		MaxRounds:          cfg.MaxRounds,
	}
	sm := NewStateMachine(ws, smConfig, nil)

	logger, err := NewWorkflowLogger(specDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { logger.Close() })

	o := &Orchestrator{
		config:       cfg,
		sm:           sm,
		logger:       logger,
		emitter:      NewChannelEmitter(64),
		runner:       runner,
		workspaceDir: dir,
		featureName:  "test-feature",
		gateCh:       make(chan GateResponse, 1),
		issueHistory: make(map[string][]string),
		activeAgents: make(map[string]string),
		tracker:      NewIssueTracker(),
	}
	return o, specDir
}

// validTaskGraphJSON returns a valid task graph as JSON bytes.
func validTaskGraphJSON() []byte {
	g := TaskGraphFile{
		Version: "0.1.0",
		Tasks: []TaskGraphTask{
			{
				TaskID:     "task-one",
				TaskName:   "First task",
				Goal:       "Do the first thing",
				Acceptance: []string{"It works"},
				Priority:   "high",
			},
		},
	}
	data, _ := json.Marshal(g)
	return data
}

func TestHandleTaskify_ValidOutput(t *testing.T) {
	runner := newOrchMockRunner()
	o, specDir := taskifyTestOrchestrator(t, runner)

	taskGraphPath := filepath.Join(o.workspaceDir, ".tasks", "test-feature.task.json")
	os.MkdirAll(filepath.Dir(taskGraphPath), 0o755)

	// Use a custom runner that writes valid task graph on dispatch.
	callCount := 0
	o.runner = &taskifyTestRunner{
		callCount:      &callCount,
		taskGraphPath:  taskGraphPath,
		validJSON:      validTaskGraphJSON(),
		validOnAttempt: 1,
	}

	state := o.sm.State()
	err := o.handleTaskify(state, specDir)
	if err != nil {
		t.Fatalf("handleTaskify failed: %v", err)
	}

	if o.sm.Current() != StateTaskReview {
		t.Errorf("expected state TASK_REVIEW, got %s", o.sm.Current())
	}
}

func TestHandleTaskify_RetriesOnValidationFailure(t *testing.T) {
	runner := newOrchMockRunner()
	o, specDir := taskifyTestOrchestrator(t, runner)

	taskGraphPath := filepath.Join(o.workspaceDir, ".tasks", "test-feature.task.json")
	os.MkdirAll(filepath.Dir(taskGraphPath), 0o755)

	callCount := 0
	// Override Run to write invalid on first call, valid on second.
	origRun := runner.Run
	_ = origRun
	runner.outputs["Decompose"] = nil // match any taskify prompt

	// We'll use a custom approach: write invalid JSON first, valid on 2nd attempt.
	// Since the mock writes based on SetOutput, we'll instead pre-write bad data
	// and have the mock overwrite with good data on the second call.

	// Actually, let's use a simpler approach: pre-write invalid data, then
	// have the mock write valid data.
	invalidGraph := `{"version":"0.1.0","tasks":[]}`
	if err := os.WriteFile(taskGraphPath, []byte(invalidGraph), 0o644); err != nil {
		t.Fatal(err)
	}

	// The mock runner will be called but writes empty JSON by default.
	// We need the task graph file to exist with valid content after 2nd dispatch.
	// Let's create a custom runner that tracks calls.
	customRunner := &taskifyTestRunner{
		callCount: &callCount,
		taskGraphPath: taskGraphPath,
		invalidJSON:   []byte(invalidGraph),
		validJSON:     validTaskGraphJSON(),
		validOnAttempt: 2,
	}

	o.runner = customRunner

	state := o.sm.State()
	err := o.handleTaskify(state, specDir)
	if err != nil {
		t.Fatalf("handleTaskify failed: %v", err)
	}

	if o.sm.Current() != StateTaskReview {
		t.Errorf("expected state TASK_REVIEW, got %s", o.sm.Current())
	}

	if callCount < 2 {
		t.Errorf("expected at least 2 dispatch calls (retry), got %d", callCount)
	}
}

func TestHandleTaskify_EscalatesWhenRetriesExhausted(t *testing.T) {
	runner := newOrchMockRunner()
	o, specDir := taskifyTestOrchestrator(t, runner)

	taskGraphPath := filepath.Join(o.workspaceDir, ".tasks", "test-feature.task.json")
	os.MkdirAll(filepath.Dir(taskGraphPath), 0o755)

	// Always write invalid task graph.
	callCount := 0
	customRunner := &taskifyTestRunner{
		callCount: &callCount,
		taskGraphPath: taskGraphPath,
		invalidJSON:   []byte(`{"version":"0.1.0","tasks":[]}`),
		validJSON:     nil, // never valid
		validOnAttempt: 999,
	}
	o.runner = customRunner

	state := o.sm.State()
	err := o.handleTaskify(state, specDir)
	if err != nil {
		t.Fatalf("handleTaskify returned error: %v", err)
	}

	if o.sm.Current() != StateEscalated {
		t.Errorf("expected ESCALATED after retry exhaustion, got %s", o.sm.Current())
	}

	if callCount != 3 {
		t.Errorf("expected 3 dispatch calls (max retries), got %d", callCount)
	}
}

func TestHandleTaskify_MissingOutputFileTriggersRetry(t *testing.T) {
	runner := newOrchMockRunner()
	o, specDir := taskifyTestOrchestrator(t, runner)

	taskGraphPath := filepath.Join(o.workspaceDir, ".tasks", "test-feature.task.json")

	callCount := 0
	customRunner := &taskifyTestRunner{
		callCount:      &callCount,
		taskGraphPath:  taskGraphPath,
		invalidJSON:    nil, // don't write anything
		validJSON:      validTaskGraphJSON(),
		validOnAttempt: 2,
		skipWriteUntil: 2, // don't write file on first attempt
	}
	o.runner = customRunner

	state := o.sm.State()
	err := o.handleTaskify(state, specDir)
	if err != nil {
		t.Fatalf("handleTaskify failed: %v", err)
	}

	if o.sm.Current() != StateTaskReview {
		t.Errorf("expected TASK_REVIEW, got %s", o.sm.Current())
	}
	if callCount < 2 {
		t.Errorf("expected at least 2 calls, got %d", callCount)
	}
}

func TestHandleTaskify_IncludesHumanComments(t *testing.T) {
	runner := newOrchMockRunner()
	o, specDir := taskifyTestOrchestrator(t, runner)

	// Write human comments.
	comments := []struct {
		Gate    string `json:"gate"`
		Action  string `json:"action"`
		Comment string `json:"comment"`
		Ts      string `json:"timestamp"`
	}{
		{Gate: "HUMAN_GATE_FINAL", Action: "confirm", Comment: "Looks good!", Ts: "2026-03-25T12:00:00Z"},
	}
	cData, _ := json.Marshal(comments)
	os.WriteFile(filepath.Join(specDir, "human-comments.json"), cData, 0o644)

	taskGraphPath := filepath.Join(o.workspaceDir, ".tasks", "test-feature.task.json")
	os.MkdirAll(filepath.Dir(taskGraphPath), 0o755)

	// Use a capturing runner to inspect prompt content.
	callCount := 0
	var capturedPrompts []string
	o.runner = &taskifyCapturingRunner{
		callCount:       &callCount,
		capturedPrompts: &capturedPrompts,
		taskGraphPath:   taskGraphPath,
		responses: []taskifyRunResponse{
			{writeJSON: validTaskGraphJSON()},
		},
	}

	state := o.sm.State()
	err := o.handleTaskify(state, specDir)
	if err != nil {
		t.Fatalf("handleTaskify failed: %v", err)
	}

	if len(capturedPrompts) == 0 {
		t.Fatal("expected at least one dispatch call")
	}
	prompt := capturedPrompts[0]
	if !strings.Contains(prompt, "Looks good!") {
		t.Error("expected human comment in prompt")
	}
	if !strings.Contains(prompt, "<human_feedback>") {
		t.Error("expected <human_feedback> block in prompt")
	}
}

func TestHandleTaskify_IncludesValidationErrorsOnRetry(t *testing.T) {
	callCount := 0
	var capturedPrompts []string

	customRunner := &taskifyCapturingRunner{
		callCount:      &callCount,
		capturedPrompts: &capturedPrompts,
		taskGraphPath:  "", // set after orchestrator creation
		responses: []taskifyRunResponse{
			{writeJSON: []byte(`{"version":"","tasks":[{"task_id":"ok","task_name":"t","goal":"g","acceptance":["a"],"priority":"high"}]}`)}, // missing version
			{writeJSON: validTaskGraphJSON()}, // valid
		},
	}

	runner := newOrchMockRunner()
	o, specDir := taskifyTestOrchestrator(t, runner)

	taskGraphPath := filepath.Join(o.workspaceDir, ".tasks", "test-feature.task.json")
	os.MkdirAll(filepath.Dir(taskGraphPath), 0o755)
	customRunner.taskGraphPath = taskGraphPath
	o.runner = customRunner

	state := o.sm.State()
	err := o.handleTaskify(state, specDir)
	if err != nil {
		t.Fatalf("handleTaskify failed: %v", err)
	}

	if len(capturedPrompts) < 2 {
		t.Fatal("expected at least 2 prompts")
	}

	// Second prompt should contain validation errors from first attempt.
	if !strings.Contains(capturedPrompts[1], "Validation Errors") {
		t.Error("expected validation errors in retry prompt")
	}
	if !strings.Contains(capturedPrompts[1], "version required") {
		t.Error("expected 'version required' error in retry prompt")
	}
}

func TestHandleTaskify_AgentDispatchFailureTriggersRetry(t *testing.T) {
	callCount := 0
	customRunner := &taskifyFailThenSucceedRunner{
		callCount:     &callCount,
		taskGraphPath: "", // set below
		validJSON:     validTaskGraphJSON(),
		failOnAttempt: 1,
	}

	runner := newOrchMockRunner()
	o, specDir := taskifyTestOrchestrator(t, runner)
	taskGraphPath := filepath.Join(o.workspaceDir, ".tasks", "test-feature.task.json")
	os.MkdirAll(filepath.Dir(taskGraphPath), 0o755)
	customRunner.taskGraphPath = taskGraphPath
	o.runner = customRunner

	state := o.sm.State()
	err := o.handleTaskify(state, specDir)
	if err != nil {
		t.Fatalf("handleTaskify failed: %v", err)
	}

	if o.sm.Current() != StateTaskReview {
		t.Errorf("expected TASK_REVIEW, got %s", o.sm.Current())
	}
	if callCount != 2 {
		t.Errorf("expected 2 calls (fail then succeed), got %d", callCount)
	}
}

func TestBuildTaskifyPrompt_Basic(t *testing.T) {
	prompt := buildTaskifyPrompt("# My Spec", "", nil, "my-feature")

	if !strings.Contains(prompt, "Decompose this approved specification") {
		t.Error("missing preamble")
	}
	if !strings.Contains(prompt, "# My Spec") {
		t.Error("missing spec content")
	}
	if !strings.Contains(prompt, "<human_feedback>") {
		t.Error("missing human_feedback block")
	}
	if !strings.Contains(prompt, ".tasks/my-feature.task.json") {
		t.Error("missing output path")
	}
	if strings.Contains(prompt, "Validation Errors") {
		t.Error("should not contain validation errors on first attempt")
	}
}

func TestBuildTaskifyPrompt_WithValidationErrors(t *testing.T) {
	errors := []string{"version required", "cycle detected in depends_on"}
	prompt := buildTaskifyPrompt("spec", "", errors, "feat")

	if !strings.Contains(prompt, "Validation Errors") {
		t.Error("missing validation errors section")
	}
	if !strings.Contains(prompt, "version required") {
		t.Error("missing specific error")
	}
	if !strings.Contains(prompt, "cycle detected") {
		t.Error("missing cycle error")
	}
}

func TestBuildTaskifyPrompt_WithHumanComments(t *testing.T) {
	prompt := buildTaskifyPrompt("spec", "- [GATE/action]: some feedback\n", nil, "feat")

	if !strings.Contains(prompt, "some feedback") {
		t.Error("missing human comment")
	}
}

func TestLoadHumanComments_Empty(t *testing.T) {
	dir := t.TempDir()
	result, err := loadHumanComments(dir)
	if err != nil {
		t.Errorf("unexpected error for missing file: %v", err)
	}
	if result != "" {
		t.Errorf("expected empty string for missing file, got %q", result)
	}
}

func TestLoadHumanComments_ValidFile(t *testing.T) {
	dir := t.TempDir()
	comments := `[{"gate":"G1","action":"confirm","comment":"good","timestamp":"2026-01-01T00:00:00Z"}]`
	os.WriteFile(filepath.Join(dir, "human-comments.json"), []byte(comments), 0o644)

	result, err := loadHumanComments(dir)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "good") {
		t.Errorf("expected comment content in result, got %q", result)
	}
}

func TestLoadHumanComments_CorruptedFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "human-comments.json"), []byte("not json"), 0o644)

	result, err := loadHumanComments(dir)
	if err == nil {
		t.Error("expected error for corrupted file, got nil")
	}
	if result != "" {
		t.Errorf("expected empty string for corrupted file, got %q", result)
	}
	if !strings.Contains(err.Error(), "corrupted") {
		t.Errorf("error should mention corrupted, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Task Human Gate tests
// ---------------------------------------------------------------------------

// taskHumanGateOrchestrator creates an orchestrator in TASK_HUMAN_GATE state.
func taskHumanGateOrchestrator(t *testing.T) (*Orchestrator, string) {
	t.Helper()

	dir := t.TempDir()
	specDir := filepath.Join(dir, "specs", "test-feature")
	tasksDir := filepath.Join(dir, ".tasks")
	os.MkdirAll(specDir, 0o755)
	os.MkdirAll(tasksDir, 0o755)

	// Write spec-final.md (needed if correct action re-runs taskify).
	os.WriteFile(filepath.Join(specDir, "spec-final.md"), []byte("# Final Spec"), 0o644)

	// Write valid task graph (simulating prior taskify output).
	os.WriteFile(filepath.Join(tasksDir, "test-feature.task.json"), validTaskGraphJSON(), 0o644)

	cfg := orchTestConfig()
	cfg.TaskifyMaxRetries = 3

	now := time.Now().UTC().Format(time.RFC3339)
	ws := &WorkflowStateJSON{
		State:       StateTaskHumanGate,
		Round:       1,
		FeatureName: "test-feature",
		StartedAt:   now,
		UpdatedAt:   now,
	}

	smConfig := StateMachineConfig{
		MaxGateCorrections: cfg.MaxGateCorrections,
		MaxGate2Redrafts:   cfg.MaxGate2Redrafts,
		MaxRounds:          cfg.MaxRounds,
	}
	sm := NewStateMachine(ws, smConfig, nil)

	logger, err := NewWorkflowLogger(specDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { logger.Close() })

	o := &Orchestrator{
		config:       cfg,
		sm:           sm,
		logger:       logger,
		emitter:      NewChannelEmitter(64),
		runner:       newOrchMockRunner(),
		workspaceDir: dir,
		featureName:  "test-feature",
		gateCh:       make(chan GateResponse, 1),
		issueHistory: make(map[string][]string),
		activeAgents: make(map[string]string),
		tracker:      NewIssueTracker(),
	}
	return o, specDir
}

func TestHandleTaskHumanGate_Approve(t *testing.T) {
	o, specDir := taskHumanGateOrchestrator(t)

	// Send approve action.
	o.gateCh <- GateResponse{
		Action:  "approve",
		Comment: "Looks good, approve tasks",
	}

	state := o.sm.State()
	err := o.handleTaskHumanGate(state, specDir)
	if err != nil {
		t.Fatalf("handleTaskHumanGate failed: %v", err)
	}

	if o.sm.Current() != StateComplete {
		t.Errorf("expected COMPLETE, got %s", o.sm.Current())
	}

	// Verify task graph file still exists.
	taskGraphPath := filepath.Join(o.workspaceDir, ".tasks", "test-feature.task.json")
	if _, err := os.Stat(taskGraphPath); err != nil {
		t.Errorf("task graph file should be preserved: %v", err)
	}

	// Verify comment was persisted.
	commentsPath := filepath.Join(specDir, "human-comments.json")
	data, err := os.ReadFile(commentsPath)
	if err != nil {
		t.Fatalf("failed to read comments: %v", err)
	}
	if !strings.Contains(string(data), "TASK_HUMAN_GATE") {
		t.Error("expected TASK_HUMAN_GATE in persisted comments")
	}
	if !strings.Contains(string(data), "Looks good") {
		t.Error("expected comment text in persisted comments")
	}
}

func TestHandleTaskHumanGate_Correct(t *testing.T) {
	o, specDir := taskHumanGateOrchestrator(t)

	// Send correct action with feedback.
	o.gateCh <- GateResponse{
		Action:  "correct",
		Comment: "Add more granular tasks for the API layer",
	}

	state := o.sm.State()
	err := o.handleTaskHumanGate(state, specDir)
	if err != nil {
		t.Fatalf("handleTaskHumanGate failed: %v", err)
	}

	if o.sm.Current() != StateTaskify {
		t.Errorf("expected TASKIFY, got %s", o.sm.Current())
	}

	// Verify comment was persisted before state transition.
	commentsPath := filepath.Join(specDir, "human-comments.json")
	data, err := os.ReadFile(commentsPath)
	if err != nil {
		t.Fatalf("failed to read comments: %v", err)
	}
	if !strings.Contains(string(data), "Add more granular tasks") {
		t.Error("expected correction feedback in persisted comments")
	}
}

func TestHandleTaskHumanGate_Skip(t *testing.T) {
	o, specDir := taskHumanGateOrchestrator(t)

	// Send skip action.
	o.gateCh <- GateResponse{
		Action: "skip",
	}

	state := o.sm.State()
	err := o.handleTaskHumanGate(state, specDir)
	if err != nil {
		t.Fatalf("handleTaskHumanGate failed: %v", err)
	}

	if o.sm.Current() != StateComplete {
		t.Errorf("expected COMPLETE, got %s", o.sm.Current())
	}
}

func TestHandleTaskHumanGate_ApproveWithNoComment(t *testing.T) {
	o, specDir := taskHumanGateOrchestrator(t)

	o.gateCh <- GateResponse{Action: "approve"}

	state := o.sm.State()
	err := o.handleTaskHumanGate(state, specDir)
	if err != nil {
		t.Fatalf("handleTaskHumanGate failed: %v", err)
	}

	if o.sm.Current() != StateComplete {
		t.Errorf("expected COMPLETE, got %s", o.sm.Current())
	}

	// No comment file should be created when no comment is provided.
	commentsPath := filepath.Join(specDir, "human-comments.json")
	if _, err := os.Stat(commentsPath); !os.IsNotExist(err) {
		// File may exist from other interactions; check it doesn't have TASK_HUMAN_GATE
		if data, readErr := os.ReadFile(commentsPath); readErr == nil {
			if strings.Contains(string(data), "TASK_HUMAN_GATE") {
				t.Error("should not have TASK_HUMAN_GATE comment when no comment provided")
			}
		}
	}
}

func TestHandleTaskHumanGate_UnknownAction(t *testing.T) {
	o, specDir := taskHumanGateOrchestrator(t)

	o.gateCh <- GateResponse{Action: "invalid-action"}

	state := o.sm.State()
	err := o.handleTaskHumanGate(state, specDir)
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
	if !strings.Contains(err.Error(), "unknown task human gate action") {
		t.Errorf("expected 'unknown task human gate action' error, got: %v", err)
	}
}

func TestAttemptCreateBeads_TaskvalUnavailable(t *testing.T) {
	// Override PATH so taskval cannot be found.
	t.Setenv("PATH", t.TempDir())
	result := attemptCreateBeads("/nonexistent/path.json")
	if !strings.Contains(result, "taskval unavailable") {
		t.Errorf("expected 'taskval unavailable' warning, got: %s", result)
	}
}

func TestHandleTaskHumanGate_SkipWithComment(t *testing.T) {
	o, specDir := taskHumanGateOrchestrator(t)

	o.gateCh <- GateResponse{
		Action:  "skip",
		Comment: "Not needed for this iteration",
	}

	state := o.sm.State()
	err := o.handleTaskHumanGate(state, specDir)
	if err != nil {
		t.Fatalf("handleTaskHumanGate failed: %v", err)
	}

	if o.sm.Current() != StateComplete {
		t.Errorf("expected COMPLETE, got %s", o.sm.Current())
	}

	// Comment should be persisted even on skip.
	data, _ := os.ReadFile(filepath.Join(specDir, "human-comments.json"))
	if !strings.Contains(string(data), "Not needed") {
		t.Error("expected skip comment persisted")
	}
	if !strings.Contains(string(data), "TASK_HUMAN_GATE") {
		t.Error("expected gate name in persisted comment")
	}
}

// --- Test runners ---

// taskifyTestRunner writes different JSON on different attempts.
type taskifyTestRunner struct {
	callCount      *int
	taskGraphPath  string
	invalidJSON    []byte
	validJSON      []byte
	validOnAttempt int
	skipWriteUntil int // don't write file until this attempt
}

func (r *taskifyTestRunner) Run(prompt string, outputPath string, timeoutSeconds int) (int, string, float64, int64, error) {
	*r.callCount++
	attempt := *r.callCount

	if r.skipWriteUntil > 0 && attempt < r.skipWriteUntil {
		// Don't write anything — simulate missing output.
		return 0, "", 0.01, 100, nil
	}

	os.MkdirAll(filepath.Dir(r.taskGraphPath), 0o755)
	if attempt >= r.validOnAttempt && r.validJSON != nil {
		os.WriteFile(r.taskGraphPath, r.validJSON, 0o644)
	} else if r.invalidJSON != nil {
		os.WriteFile(r.taskGraphPath, r.invalidJSON, 0o644)
	}

	return 0, "", 0.01, 100, nil
}

// taskifyCapturingRunner captures prompts and writes different responses per attempt.
type taskifyCapturingRunner struct {
	callCount       *int
	capturedPrompts *[]string
	taskGraphPath   string
	responses       []taskifyRunResponse
}

type taskifyRunResponse struct {
	writeJSON []byte
	err       error
}

func (r *taskifyCapturingRunner) Run(prompt string, outputPath string, timeoutSeconds int) (int, string, float64, int64, error) {
	*r.callCount++
	*r.capturedPrompts = append(*r.capturedPrompts, prompt)

	idx := *r.callCount - 1
	if idx < len(r.responses) {
		resp := r.responses[idx]
		if resp.err != nil {
			return 1, resp.err.Error(), 0.01, 100, resp.err
		}
		if resp.writeJSON != nil {
			os.MkdirAll(filepath.Dir(r.taskGraphPath), 0o755)
			os.WriteFile(r.taskGraphPath, resp.writeJSON, 0o644)
		}
	}

	return 0, "", 0.01, 100, nil
}

// taskifyFailThenSucceedRunner fails on a specific attempt, succeeds otherwise.
type taskifyFailThenSucceedRunner struct {
	callCount     *int
	taskGraphPath string
	validJSON     []byte
	failOnAttempt int
}

func (r *taskifyFailThenSucceedRunner) Run(prompt string, outputPath string, timeoutSeconds int) (int, string, float64, int64, error) {
	*r.callCount++
	if *r.callCount == r.failOnAttempt {
		return 1, "agent crashed", 0.01, 100, fmt.Errorf("agent crashed")
	}
	os.MkdirAll(filepath.Dir(r.taskGraphPath), 0o755)
	os.WriteFile(r.taskGraphPath, r.validJSON, 0o644)
	return 0, "", 0.01, 100, nil
}
