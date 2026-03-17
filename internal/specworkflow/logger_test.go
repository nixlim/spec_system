package specworkflow

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// readJSONLines reads all lines from a JSONL file and returns them as decoded
// maps. It fails the test on any read or parse error.
func readJSONLines(t *testing.T, path string) []map[string]interface{} {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open log file: %v", err)
	}
	defer f.Close()

	var lines []map[string]interface{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var m map[string]interface{}
		if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
			t.Fatalf("invalid JSON line %q: %v", scanner.Text(), err)
		}
		lines = append(lines, m)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner error: %v", err)
	}
	return lines
}

// requireField asserts that the key exists in the map and returns its value.
func requireField(t *testing.T, m map[string]interface{}, key string) interface{} {
	t.Helper()
	v, ok := m[key]
	if !ok {
		t.Fatalf("missing required field %q in %v", key, m)
	}
	return v
}

// requireStringField asserts that the key exists and is a string.
func requireStringField(t *testing.T, m map[string]interface{}, key, want string) {
	t.Helper()
	v := requireField(t, m, key)
	got, ok := v.(string)
	if !ok {
		t.Fatalf("field %q: expected string, got %T", key, v)
	}
	if got != want {
		t.Errorf("field %q: got %q, want %q", key, got, want)
	}
}

// requireNumberField asserts that the key exists and equals the expected float64.
func requireNumberField(t *testing.T, m map[string]interface{}, key string, want float64) {
	t.Helper()
	v := requireField(t, m, key)
	got, ok := v.(float64)
	if !ok {
		t.Fatalf("field %q: expected number, got %T", key, v)
	}
	if got != want {
		t.Errorf("field %q: got %v, want %v", key, got, want)
	}
}

// requireBoolField asserts that the key exists and equals the expected bool.
func requireBoolField(t *testing.T, m map[string]interface{}, key string, want bool) {
	t.Helper()
	v := requireField(t, m, key)
	got, ok := v.(bool)
	if !ok {
		t.Fatalf("field %q: expected bool, got %T", key, v)
	}
	if got != want {
		t.Errorf("field %q: got %v, want %v", key, got, want)
	}
}

// assertValidTimestamp verifies the timestamp field parses as RFC 3339.
func assertValidTimestamp(t *testing.T, m map[string]interface{}) {
	t.Helper()
	v := requireField(t, m, "timestamp")
	ts, ok := v.(string)
	if !ok {
		t.Fatalf("timestamp: expected string, got %T", v)
	}
	if _, err := time.Parse(time.RFC3339, ts); err != nil {
		t.Errorf("timestamp %q is not valid RFC 3339 / ISO 8601: %v", ts, err)
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestLoggerNewAndClose(t *testing.T) {
	dir := t.TempDir()
	l, err := NewWorkflowLogger(dir)
	if err != nil {
		t.Fatalf("NewWorkflowLogger: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// The file should exist (possibly empty).
	path := filepath.Join(dir, "workflow-log.jsonl")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("log file not created: %v", err)
	}
}

func TestLoggerNewBadDir(t *testing.T) {
	_, err := NewWorkflowLogger("/nonexistent/path/that/does/not/exist")
	if err == nil {
		t.Fatal("expected error for nonexistent directory, got nil")
	}
}

func TestLoggerStateTransition(t *testing.T) {
	dir := t.TempDir()
	l, err := NewWorkflowLogger(dir)
	if err != nil {
		t.Fatalf("NewWorkflowLogger: %v", err)
	}
	defer l.Close()

	if err := l.LogStateTransition(StateInit, StateDiscovery, 1); err != nil {
		t.Fatalf("LogStateTransition: %v", err)
	}

	lines := readJSONLines(t, filepath.Join(dir, "workflow-log.jsonl"))
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	m := lines[0]
	requireStringField(t, m, "event", "state_transition")
	requireStringField(t, m, "from", "INIT")
	requireStringField(t, m, "to", "DISCOVERY")
	requireNumberField(t, m, "round", 1)
	assertValidTimestamp(t, m)
}

func TestLoggerAgentDispatch(t *testing.T) {
	dir := t.TempDir()
	l, err := NewWorkflowLogger(dir)
	if err != nil {
		t.Fatalf("NewWorkflowLogger: %v", err)
	}
	defer l.Close()

	if err := l.LogAgentDispatch("reviewer", "task-42", 3); err != nil {
		t.Fatalf("LogAgentDispatch: %v", err)
	}

	lines := readJSONLines(t, filepath.Join(dir, "workflow-log.jsonl"))
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	m := lines[0]
	requireStringField(t, m, "event", "agent_dispatch")
	requireStringField(t, m, "agent", "reviewer")
	requireStringField(t, m, "task_id", "task-42")
	requireNumberField(t, m, "round", 3)
	assertValidTimestamp(t, m)
}

func TestLoggerAgentComplete(t *testing.T) {
	dir := t.TempDir()
	l, err := NewWorkflowLogger(dir)
	if err != nil {
		t.Fatalf("NewWorkflowLogger: %v", err)
	}
	defer l.Close()

	if err := l.LogAgentComplete("drafter", "task-7", 1500, 0.035, true); err != nil {
		t.Fatalf("LogAgentComplete: %v", err)
	}

	lines := readJSONLines(t, filepath.Join(dir, "workflow-log.jsonl"))
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	m := lines[0]
	requireStringField(t, m, "event", "agent_complete")
	requireStringField(t, m, "agent", "drafter")
	requireStringField(t, m, "task_id", "task-7")
	requireNumberField(t, m, "duration_ms", 1500)
	requireNumberField(t, m, "cost_usd", 0.035)
	requireBoolField(t, m, "success", true)
	assertValidTimestamp(t, m)
}

func TestLoggerAgentError(t *testing.T) {
	dir := t.TempDir()
	l, err := NewWorkflowLogger(dir)
	if err != nil {
		t.Fatalf("NewWorkflowLogger: %v", err)
	}
	defer l.Close()

	if err := l.LogAgentError("judge", "timeout", "agent did not respond within 30s"); err != nil {
		t.Fatalf("LogAgentError: %v", err)
	}

	lines := readJSONLines(t, filepath.Join(dir, "workflow-log.jsonl"))
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	m := lines[0]
	requireStringField(t, m, "event", "agent_error")
	requireStringField(t, m, "agent", "judge")
	requireStringField(t, m, "error_type", "timeout")
	requireStringField(t, m, "detail", "agent did not respond within 30s")
	assertValidTimestamp(t, m)
}

func TestLoggerDedupMerge(t *testing.T) {
	dir := t.TempDir()
	l, err := NewWorkflowLogger(dir)
	if err != nil {
		t.Fatalf("NewWorkflowLogger: %v", err)
	}
	defer l.Close()

	if err := l.LogDedupMerge("finding-1", "finding-5", "semantic overlap >80%"); err != nil {
		t.Fatalf("LogDedupMerge: %v", err)
	}

	lines := readJSONLines(t, filepath.Join(dir, "workflow-log.jsonl"))
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	m := lines[0]
	requireStringField(t, m, "event", "dedup_merge")
	requireStringField(t, m, "kept_id", "finding-1")
	requireStringField(t, m, "merged_id", "finding-5")
	requireStringField(t, m, "reason", "semantic overlap >80%")
	assertValidTimestamp(t, m)
}

func TestLoggerConvergenceCheck(t *testing.T) {
	dir := t.TempDir()
	l, err := NewWorkflowLogger(dir)
	if err != nil {
		t.Fatalf("NewWorkflowLogger: %v", err)
	}
	defer l.Close()

	if err := l.LogConvergenceCheck(2, 0, 3, "continue", true); err != nil {
		t.Fatalf("LogConvergenceCheck: %v", err)
	}

	lines := readJSONLines(t, filepath.Join(dir, "workflow-log.jsonl"))
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	m := lines[0]
	requireStringField(t, m, "event", "convergence_check")
	requireNumberField(t, m, "round", 2)
	requireNumberField(t, m, "open_critical", 0)
	requireNumberField(t, m, "open_major", 3)
	requireStringField(t, m, "verdict", "continue")
	requireBoolField(t, m, "progress", true)
	assertValidTimestamp(t, m)
}

func TestLoggerAppendOnly(t *testing.T) {
	dir := t.TempDir()
	l, err := NewWorkflowLogger(dir)
	if err != nil {
		t.Fatalf("NewWorkflowLogger: %v", err)
	}

	// Write three different events.
	if err := l.LogStateTransition(StateInit, StateDiscovery, 1); err != nil {
		t.Fatalf("LogStateTransition: %v", err)
	}
	if err := l.LogAgentDispatch("reviewer", "task-1", 1); err != nil {
		t.Fatalf("LogAgentDispatch: %v", err)
	}
	if err := l.LogAgentError("reviewer", "crash", "segfault"); err != nil {
		t.Fatalf("LogAgentError: %v", err)
	}
	l.Close()

	path := filepath.Join(dir, "workflow-log.jsonl")
	lines := readJSONLines(t, path)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines after first session, got %d", len(lines))
	}

	// Re-open the same file and write more — must append, not truncate.
	l2, err := NewWorkflowLogger(dir)
	if err != nil {
		t.Fatalf("NewWorkflowLogger (reopen): %v", err)
	}
	if err := l2.LogDedupMerge("a", "b", "dup"); err != nil {
		t.Fatalf("LogDedupMerge: %v", err)
	}
	l2.Close()

	lines = readJSONLines(t, path)
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines after reopen append, got %d", len(lines))
	}

	// Verify the events are in order.
	requireStringField(t, lines[0], "event", "state_transition")
	requireStringField(t, lines[1], "event", "agent_dispatch")
	requireStringField(t, lines[2], "event", "agent_error")
	requireStringField(t, lines[3], "event", "dedup_merge")
}

func TestLoggerFlushAfterEachWrite(t *testing.T) {
	dir := t.TempDir()
	l, err := NewWorkflowLogger(dir)
	if err != nil {
		t.Fatalf("NewWorkflowLogger: %v", err)
	}
	defer l.Close()

	path := filepath.Join(dir, "workflow-log.jsonl")

	// Write one event, then immediately read the file without closing the
	// logger — the data must be present because writeEvent calls Sync.
	if err := l.LogStateTransition(StateDiscovery, StateDrafting, 1); err != nil {
		t.Fatalf("LogStateTransition: %v", err)
	}

	lines := readJSONLines(t, path)
	if len(lines) != 1 {
		t.Fatalf("expected 1 flushed line without closing, got %d", len(lines))
	}

	// Write another and check again.
	if err := l.LogAgentDispatch("drafter", "task-2", 1); err != nil {
		t.Fatalf("LogAgentDispatch: %v", err)
	}

	lines = readJSONLines(t, path)
	if len(lines) != 2 {
		t.Fatalf("expected 2 flushed lines without closing, got %d", len(lines))
	}
}

func TestLoggerTimestampIsISO8601(t *testing.T) {
	dir := t.TempDir()
	l, err := NewWorkflowLogger(dir)
	if err != nil {
		t.Fatalf("NewWorkflowLogger: %v", err)
	}
	defer l.Close()

	before := time.Now().UTC().Add(-time.Second)

	if err := l.LogStateTransition(StateInit, StateDiscovery, 1); err != nil {
		t.Fatalf("LogStateTransition: %v", err)
	}

	after := time.Now().UTC().Add(time.Second)

	lines := readJSONLines(t, filepath.Join(dir, "workflow-log.jsonl"))
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}

	ts, ok := lines[0]["timestamp"].(string)
	if !ok {
		t.Fatal("timestamp field is not a string")
	}

	parsed, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		t.Fatalf("timestamp %q is not valid RFC 3339: %v", ts, err)
	}

	if parsed.Before(before) || parsed.After(after) {
		t.Errorf("timestamp %v is outside expected range [%v, %v]", parsed, before, after)
	}
}
