package codereview

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// readJSONLLines reads all JSONL lines from a file and returns them as
// a slice of map[string]interface{}.
func readJSONLLines(t *testing.T, path string) []map[string]interface{} {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()

	var lines []map[string]interface{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var m map[string]interface{}
		if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
			t.Fatalf("unmarshal line: %v", err)
		}
		lines = append(lines, m)
	}
	return lines
}

func TestCRAuditLoggerCreatesFile(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewCRAuditLogger(dir)
	if err != nil {
		t.Fatalf("NewCRAuditLogger: %v", err)
	}
	defer logger.Close()

	path := filepath.Join(dir, "codereview-audit.jsonl")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("audit log file should exist: %v", err)
	}
}

func TestLogCodeReviewStart(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewCRAuditLogger(dir)
	if err != nil {
		t.Fatalf("NewCRAuditLogger: %v", err)
	}

	err = logger.LogCodeReviewStart("my-feature", "/code", "/spec.md", "/tasks.json", GrillCodeModeFullContext)
	if err != nil {
		t.Fatalf("LogCodeReviewStart: %v", err)
	}
	logger.Close()

	lines := readJSONLLines(t, filepath.Join(dir, "codereview-audit.jsonl"))
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}

	line := lines[0]
	if line["event"] != "codereview_start" {
		t.Errorf("event = %v, want codereview_start", line["event"])
	}
	if line["feature_name"] != "my-feature" {
		t.Errorf("feature_name = %v, want my-feature", line["feature_name"])
	}
	if line["grill_code_mode"] != "full-context" {
		t.Errorf("grill_code_mode = %v, want full-context", line["grill_code_mode"])
	}
	if _, ok := line["timestamp"]; !ok {
		t.Error("expected timestamp field")
	}
}

func TestLogCodeReviewGate(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewCRAuditLogger(dir)
	if err != nil {
		t.Fatalf("NewCRAuditLogger: %v", err)
	}

	err = logger.LogCodeReviewGate("my-feature", "CR_HUMAN_GATE_SCOPE", "confirm", "looks good")
	if err != nil {
		t.Fatalf("LogCodeReviewGate: %v", err)
	}
	logger.Close()

	lines := readJSONLLines(t, filepath.Join(dir, "codereview-audit.jsonl"))
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	if lines[0]["event"] != "codereview_gate" {
		t.Errorf("event = %v, want codereview_gate", lines[0]["event"])
	}
	if lines[0]["action"] != "confirm" {
		t.Errorf("action = %v, want confirm", lines[0]["action"])
	}
}

func TestLogCodeReviewCancel(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewCRAuditLogger(dir)
	if err != nil {
		t.Fatalf("NewCRAuditLogger: %v", err)
	}

	err = logger.LogCodeReviewCancel("my-feature", "operator cancelled")
	if err != nil {
		t.Fatalf("LogCodeReviewCancel: %v", err)
	}
	logger.Close()

	lines := readJSONLLines(t, filepath.Join(dir, "codereview-audit.jsonl"))
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	if lines[0]["event"] != "codereview_cancel" {
		t.Errorf("event = %v, want codereview_cancel", lines[0]["event"])
	}
	if lines[0]["reason"] != "operator cancelled" {
		t.Errorf("reason = %v, want 'operator cancelled'", lines[0]["reason"])
	}
}

func TestLogCodeReviewReset(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewCRAuditLogger(dir)
	if err != nil {
		t.Fatalf("NewCRAuditLogger: %v", err)
	}

	err = logger.LogCodeReviewReset("my-feature")
	if err != nil {
		t.Fatalf("LogCodeReviewReset: %v", err)
	}
	logger.Close()

	lines := readJSONLLines(t, filepath.Join(dir, "codereview-audit.jsonl"))
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	if lines[0]["event"] != "codereview_reset" {
		t.Errorf("event = %v, want codereview_reset", lines[0]["event"])
	}
}

func TestLogStateTransition(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewCRAuditLogger(dir)
	if err != nil {
		t.Fatalf("NewCRAuditLogger: %v", err)
	}

	err = logger.LogStateTransition("my-feature", CRInit, CRHumanGateScope, 1)
	if err != nil {
		t.Fatalf("LogStateTransition: %v", err)
	}
	logger.Close()

	lines := readJSONLLines(t, filepath.Join(dir, "codereview-audit.jsonl"))
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	if lines[0]["from"] != "CR_INIT" {
		t.Errorf("from = %v, want CR_INIT", lines[0]["from"])
	}
	if lines[0]["to"] != "CR_HUMAN_GATE_SCOPE" {
		t.Errorf("to = %v, want CR_HUMAN_GATE_SCOPE", lines[0]["to"])
	}
}

func TestMultipleLogEntries(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewCRAuditLogger(dir)
	if err != nil {
		t.Fatalf("NewCRAuditLogger: %v", err)
	}

	logger.LogCodeReviewStart("feat", "/code", "", "", GrillCodeModeCodeOnly)
	logger.LogCodeReviewGate("feat", "CR_HUMAN_GATE_SCOPE", "confirm", "")
	logger.LogStateTransition("feat", CRHumanGateScope, CRReviewing, 1)
	logger.Close()

	lines := readJSONLLines(t, filepath.Join(dir, "codereview-audit.jsonl"))
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}

	events := []string{
		lines[0]["event"].(string),
		lines[1]["event"].(string),
		lines[2]["event"].(string),
	}
	expected := []string{"codereview_start", "codereview_gate", "codereview_state_transition"}
	for i, want := range expected {
		if events[i] != want {
			t.Errorf("line %d: event = %q, want %q", i, events[i], want)
		}
	}
}

func TestLogAgentDispatchAndComplete(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewCRAuditLogger(dir)
	if err != nil {
		t.Fatalf("NewCRAuditLogger: %v", err)
	}

	logger.LogAgentDispatch("feat", "reviewer-security-claude", "security", "claude", 1)
	logger.LogAgentComplete("feat", "reviewer-security-claude", true, 5000, 1.23)
	logger.Close()

	lines := readJSONLLines(t, filepath.Join(dir, "codereview-audit.jsonl"))
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}

	if lines[0]["event"] != "codereview_agent_dispatch" {
		t.Errorf("line 0: event = %v, want codereview_agent_dispatch", lines[0]["event"])
	}
	if lines[0]["lens"] != "security" {
		t.Errorf("line 0: lens = %v, want security", lines[0]["lens"])
	}
	if lines[1]["event"] != "codereview_agent_complete" {
		t.Errorf("line 1: event = %v, want codereview_agent_complete", lines[1]["event"])
	}
	if lines[1]["success"] != true {
		t.Errorf("line 1: success = %v, want true", lines[1]["success"])
	}
}

func TestLogFixPhase(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewCRAuditLogger(dir)
	if err != nil {
		t.Fatalf("NewCRAuditLogger: %v", err)
	}

	logger.LogFixPhase("feat", 1, 2.50, 10000, CRReviewing, "all fixes applied")
	logger.Close()

	lines := readJSONLLines(t, filepath.Join(dir, "codereview-audit.jsonl"))
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	if lines[0]["event"] != "codereview_fix_phase" {
		t.Errorf("event = %v, want codereview_fix_phase", lines[0]["event"])
	}
	if lines[0]["next_state"] != "CR_REVIEWING" {
		t.Errorf("next_state = %v, want CR_REVIEWING", lines[0]["next_state"])
	}
}
