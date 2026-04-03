package codereview

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// CRAuditLogger writes structured JSON events to an append-only JSONL file
// for code review workflow actions. Each call to a Log method writes exactly
// one JSON object followed by a newline and flushes to disk immediately.
// All methods are safe for concurrent use.
type CRAuditLogger struct {
	file *os.File
	mu   sync.Mutex
}

// NewCRAuditLogger opens (or creates) the file {dir}/codereview-audit.jsonl
// in append-only mode and returns a ready-to-use logger.
func NewCRAuditLogger(dir string) (*CRAuditLogger, error) {
	path := filepath.Join(dir, "codereview-audit.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open code review audit log: %w", err)
	}
	return &CRAuditLogger{file: f}, nil
}

// Close flushes and closes the underlying log file.
func (l *CRAuditLogger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.file.Close()
}

// LogCodeReviewStart records the start of a code review workflow.
func (l *CRAuditLogger) LogCodeReviewStart(featureName, codePath, specPath, taskListPath string, mode GrillCodeMode) error {
	return l.writeEvent(map[string]interface{}{
		"event":          "codereview_start",
		"feature_name":   featureName,
		"code_path":      codePath,
		"spec_path":      specPath,
		"task_list_path": taskListPath,
		"grill_code_mode": mode.String(),
	})
}

// LogCodeReviewGate records a human gate action.
func (l *CRAuditLogger) LogCodeReviewGate(featureName, gate, action, comment string) error {
	return l.writeEvent(map[string]interface{}{
		"event":        "codereview_gate",
		"feature_name": featureName,
		"gate":         gate,
		"action":       action,
		"comment":      comment,
	})
}

// LogCodeReviewCancel records a workflow cancellation.
func (l *CRAuditLogger) LogCodeReviewCancel(featureName, reason string) error {
	return l.writeEvent(map[string]interface{}{
		"event":        "codereview_cancel",
		"feature_name": featureName,
		"reason":       reason,
	})
}

// LogCodeReviewReset records a workspace cleanup/reset.
func (l *CRAuditLogger) LogCodeReviewReset(featureName string) error {
	return l.writeEvent(map[string]interface{}{
		"event":        "codereview_reset",
		"feature_name": featureName,
	})
}

// LogStateTransition records a code review state transition.
func (l *CRAuditLogger) LogStateTransition(featureName string, from, to CodeReviewState, round int) error {
	return l.writeEvent(map[string]interface{}{
		"event":        "codereview_state_transition",
		"feature_name": featureName,
		"from":         from.String(),
		"to":           to.String(),
		"round":        round,
	})
}

// LogAgentDispatch records the dispatch of a code review agent.
func (l *CRAuditLogger) LogAgentDispatch(featureName, agentName, lens, provider string, round int) error {
	return l.writeEvent(map[string]interface{}{
		"event":        "codereview_agent_dispatch",
		"feature_name": featureName,
		"agent_name":   agentName,
		"lens":         lens,
		"provider":     provider,
		"round":        round,
	})
}

// LogAgentComplete records the completion of a code review agent.
func (l *CRAuditLogger) LogAgentComplete(featureName, agentName string, success bool, durationMS int64, costUSD float64) error {
	return l.writeEvent(map[string]interface{}{
		"event":        "codereview_agent_complete",
		"feature_name": featureName,
		"agent_name":   agentName,
		"success":      success,
		"duration_ms":  durationMS,
		"cost_usd":     costUSD,
	})
}

// LogFixPhase records the outcome of a fix phase.
func (l *CRAuditLogger) LogFixPhase(featureName string, round int, costUSD float64, durationMS int64, nextState CodeReviewState, reason string) error {
	return l.writeEvent(map[string]interface{}{
		"event":        "codereview_fix_phase",
		"feature_name": featureName,
		"round":        round,
		"cost_usd":     costUSD,
		"duration_ms":  durationMS,
		"next_state":   nextState.String(),
		"reason":       reason,
	})
}

// writeEvent marshals the data map to JSON with an ISO 8601 timestamp,
// appends a newline, writes the line, and flushes to disk.
func (l *CRAuditLogger) writeEvent(data map[string]interface{}) error {
	data["timestamp"] = time.Now().UTC().Format(time.RFC3339)

	line, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal audit event: %w", err)
	}
	line = append(line, '\n')

	l.mu.Lock()
	defer l.mu.Unlock()

	if _, err := l.file.Write(line); err != nil {
		return fmt.Errorf("write audit event: %w", err)
	}
	if err := l.file.Sync(); err != nil {
		return fmt.Errorf("sync audit event: %w", err)
	}
	return nil
}
