package specworkflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// WorkflowLogger writes structured JSON events to an append-only JSONL file.
// Each call to a Log method writes exactly one JSON object followed by a
// newline, and flushes the write to disk immediately. All methods are safe
// for concurrent use.
type WorkflowLogger struct {
	file *os.File
	mu   sync.Mutex
}

// NewWorkflowLogger opens (or creates) the file {dir}/workflow-log.jsonl in
// append-only mode and returns a ready-to-use logger. The caller must call
// Close when the logger is no longer needed.
func NewWorkflowLogger(dir string) (*WorkflowLogger, error) {
	path := filepath.Join(dir, "workflow-log.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open workflow log: %w", err)
	}
	return &WorkflowLogger{file: f}, nil
}

// Close flushes and closes the underlying log file.
func (l *WorkflowLogger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.file.Close()
}

// LogStateTransition records a workflow state transition event.
func (l *WorkflowLogger) LogStateTransition(from, to WorkflowState, round int) error {
	return l.writeEvent(map[string]interface{}{
		"event": "state_transition",
		"from":  from.String(),
		"to":    to.String(),
		"round": round,
	})
}

// LogAgentDispatch records the dispatch of an agent for a specific task.
func (l *WorkflowLogger) LogAgentDispatch(agent, taskID string, round int) error {
	return l.writeEvent(map[string]interface{}{
		"event":   "agent_dispatch",
		"agent":   agent,
		"task_id": taskID,
		"round":   round,
	})
}

// LogAgentComplete records the completion of an agent invocation, including
// its duration, cost, and whether it succeeded.
func (l *WorkflowLogger) LogAgentComplete(agent, taskID string, durationMS int64, costUSD float64, success bool) error {
	return l.writeEvent(map[string]interface{}{
		"event":       "agent_complete",
		"agent":       agent,
		"task_id":     taskID,
		"duration_ms": durationMS,
		"cost_usd":    costUSD,
		"success":     success,
	})
}

// LogAgentError records an error encountered by an agent.
func (l *WorkflowLogger) LogAgentError(agent, errorType, detail string) error {
	return l.writeEvent(map[string]interface{}{
		"event":      "agent_error",
		"agent":      agent,
		"error_type": errorType,
		"detail":     detail,
	})
}

// LogDedupMerge records the merge of a duplicate finding.
func (l *WorkflowLogger) LogDedupMerge(keptID, mergedID, reason string) error {
	return l.writeEvent(map[string]interface{}{
		"event":     "dedup_merge",
		"kept_id":   keptID,
		"merged_id": mergedID,
		"reason":    reason,
	})
}

// LogConvergenceCheck records the result of a convergence evaluation.
func (l *WorkflowLogger) LogConvergenceCheck(round, openCritical, openMajor int, verdict string, progress bool) error {
	return l.writeEvent(map[string]interface{}{
		"event":         "convergence_check",
		"round":         round,
		"open_critical": openCritical,
		"open_major":    openMajor,
		"verdict":       verdict,
		"progress":      progress,
	})
}

// writeEvent marshals the data map to JSON (with a timestamp), appends a
// newline, writes the line to the file, and flushes to disk.
func (l *WorkflowLogger) writeEvent(data map[string]interface{}) error {
	data["timestamp"] = time.Now().UTC().Format(time.RFC3339)

	line, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	line = append(line, '\n')

	l.mu.Lock()
	defer l.mu.Unlock()

	if _, err := l.file.Write(line); err != nil {
		return fmt.Errorf("write event: %w", err)
	}
	if err := l.file.Sync(); err != nil {
		return fmt.Errorf("sync event: %w", err)
	}
	return nil
}
