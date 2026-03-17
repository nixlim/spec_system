package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// LogBuffer tests
// ---------------------------------------------------------------------------

func TestLogBuffer_AddAndLines(t *testing.T) {
	buf := NewLogBuffer(5)
	buf.Add("line1")
	buf.Add("line2")
	buf.Add("line3")

	lines := buf.Lines()
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
	if lines[0] != "line1" || lines[2] != "line3" {
		t.Errorf("unexpected lines: %v", lines)
	}
}

func TestLogBuffer_WrapAround(t *testing.T) {
	buf := NewLogBuffer(3)
	buf.Add("a")
	buf.Add("b")
	buf.Add("c")
	buf.Add("d") // wraps: overwrites "a"
	buf.Add("e") // overwrites "b"

	lines := buf.Lines()
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines after wrap, got %d", len(lines))
	}
	// Expect: c, d, e (oldest to newest)
	if lines[0] != "c" || lines[1] != "d" || lines[2] != "e" {
		t.Errorf("expected [c d e], got %v", lines)
	}
}

func TestLogBuffer_Empty(t *testing.T) {
	buf := NewLogBuffer(10)
	lines := buf.Lines()
	if len(lines) != 0 {
		t.Errorf("expected 0 lines, got %d", len(lines))
	}
}

func TestLogBuffer_Write(t *testing.T) {
	buf := NewLogBuffer(10)
	n, err := buf.Write([]byte("hello world\n"))
	if err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if n != 12 {
		t.Errorf("expected n=12, got %d", n)
	}
	lines := buf.Lines()
	if len(lines) != 1 || lines[0] != "hello world" {
		t.Errorf("expected [hello world], got %v", lines)
	}
}

func TestLogBuffer_DefaultCapacity(t *testing.T) {
	buf := NewLogBuffer(0)
	if buf.cap != 500 {
		t.Errorf("expected default capacity 500, got %d", buf.cap)
	}
}

// ---------------------------------------------------------------------------
// HandleGetServerLogs tests
// ---------------------------------------------------------------------------

func TestHandleGetServerLogs(t *testing.T) {
	buf := NewLogBuffer(10)
	buf.Add("log line 1")
	buf.Add("log line 2")

	handler := HandleGetServerLogs(buf)
	req := httptest.NewRequest(http.MethodGet, "/api/logs/server", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var lines []string
	if err := json.NewDecoder(rec.Body).Decode(&lines); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	if lines[0] != "log line 1" {
		t.Errorf("expected first line 'log line 1', got %q", lines[0])
	}
}

func TestHandleGetServerLogs_MethodNotAllowed(t *testing.T) {
	buf := NewLogBuffer(10)
	handler := HandleGetServerLogs(buf)

	req := httptest.NewRequest(http.MethodPost, "/api/logs/server", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestHandleGetServerLogs_TruncatesTo200(t *testing.T) {
	buf := NewLogBuffer(500)
	for i := 0; i < 300; i++ {
		buf.Add("line")
	}

	handler := HandleGetServerLogs(buf)
	req := httptest.NewRequest(http.MethodGet, "/api/logs/server", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var lines []string
	json.NewDecoder(rec.Body).Decode(&lines)
	if len(lines) != 200 {
		t.Errorf("expected 200 lines, got %d", len(lines))
	}
}

// ---------------------------------------------------------------------------
// HandleGetMessages tests
// ---------------------------------------------------------------------------

func TestHandleGetMessages_NoSpecsDir(t *testing.T) {
	dir := t.TempDir()
	handler := HandleGetMessages(dir)

	req := httptest.NewRequest(http.MethodGet, "/api/messages", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var msgs []interface{}
	json.NewDecoder(rec.Body).Decode(&msgs)
	if len(msgs) != 0 {
		t.Errorf("expected empty array, got %d entries", len(msgs))
	}
}

func TestHandleGetMessages_WithLogs(t *testing.T) {
	dir := t.TempDir()
	featureDir := filepath.Join(dir, "specs", "test-feature")
	os.MkdirAll(featureDir, 0o755)

	logContent := `{"timestamp":"2024-01-01T00:00:02Z","event":"agent_dispatch","agent":"discovery"}
{"timestamp":"2024-01-01T00:00:01Z","event":"state_transition","from":"INIT","to":"DISCOVERY"}
`
	os.WriteFile(filepath.Join(featureDir, "workflow-log.jsonl"), []byte(logContent), 0o644)

	handler := HandleGetMessages(dir)
	req := httptest.NewRequest(http.MethodGet, "/api/messages", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var msgs []map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&msgs)
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}

	// Should be sorted by timestamp (oldest first).
	if msgs[0]["event"] != "state_transition" {
		t.Errorf("expected first event to be state_transition, got %v", msgs[0]["event"])
	}
}

func TestHandleGetMessages_FeatureFilter(t *testing.T) {
	dir := t.TempDir()

	// Create two feature dirs.
	for _, name := range []string{"feature-a", "feature-b"} {
		featureDir := filepath.Join(dir, "specs", name)
		os.MkdirAll(featureDir, 0o755)
		os.WriteFile(filepath.Join(featureDir, "workflow-log.jsonl"),
			[]byte(`{"timestamp":"2024-01-01T00:00:00Z","event":"test","feature":"`+name+`"}`+"\n"), 0o644)
	}

	handler := HandleGetMessages(dir)
	req := httptest.NewRequest(http.MethodGet, "/api/messages?feature=feature-a", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var msgs []map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&msgs)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message with filter, got %d", len(msgs))
	}
	if msgs[0]["feature"] != "feature-a" {
		t.Errorf("expected feature-a, got %v", msgs[0]["feature"])
	}
}

func TestHandleGetMessages_MethodNotAllowed(t *testing.T) {
	handler := HandleGetMessages(t.TempDir())
	req := httptest.NewRequest(http.MethodPost, "/api/messages", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}
