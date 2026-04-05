package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/foundry-zero/adversarial-spec-system/internal/process"

	_ "modernc.org/sqlite"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newTestProcessStore(t *testing.T) *process.SQLiteStore {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	store, err := process.NewSQLiteStore(db)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	return store
}

// ---------------------------------------------------------------------------
// TestProcessHandler_List — GET /api/processes returns correct JSON
// ---------------------------------------------------------------------------

func TestProcessHandler_List(t *testing.T) {
	store := newTestProcessStore(t)
	tracker := process.NewProcessTracker(store, nil)

	now := time.Now().UTC()
	_ = tracker.Register(process.ProcessRecord{
		Feature:   "auth-spec",
		Role:      "Reviewer",
		PID:       1234,
		StartedAt: now,
	})
	_ = tracker.Register(process.ProcessRecord{
		Feature:   "auth-spec",
		Role:      "Drafter",
		PID:       5678,
		StartedAt: now.Add(-time.Minute),
	})

	handler := HandleListProcesses(tracker)

	req := httptest.NewRequest(http.MethodGet, "/api/processes", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var records []process.ProcessRecord
	if err := json.NewDecoder(w.Body).Decode(&records); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}

	// Verify content type.
	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", ct)
	}
}

// ---------------------------------------------------------------------------
// TestProcessHandler_List_Empty — returns empty array, not null
// ---------------------------------------------------------------------------

func TestProcessHandler_List_Empty(t *testing.T) {
	tracker := process.NewProcessTracker(nil, nil)
	handler := HandleListProcesses(tracker)

	req := httptest.NewRequest(http.MethodGet, "/api/processes", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var records []process.ProcessRecord
	if err := json.NewDecoder(w.Body).Decode(&records); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if records == nil {
		t.Error("expected non-nil empty array, got nil")
	}
	if len(records) != 0 {
		t.Errorf("expected 0 records, got %d", len(records))
	}
}

// ---------------------------------------------------------------------------
// TestProcessHandler_List_MethodNotAllowed
// ---------------------------------------------------------------------------

func TestProcessHandler_List_MethodNotAllowed(t *testing.T) {
	tracker := process.NewProcessTracker(nil, nil)
	handler := HandleListProcesses(tracker)

	req := httptest.NewRequest(http.MethodPost, "/api/processes", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// TestProcessHandler_Kill_OK — returns 200 with kill_accepted
// ---------------------------------------------------------------------------

func TestProcessHandler_Kill_OK(t *testing.T) {
	store := newTestProcessStore(t)
	tracker := process.NewProcessTracker(store, nil)
	sender := &noopSignalSender{}
	killSvc := process.NewKillService(sender, tracker, 0)

	_ = tracker.Register(process.ProcessRecord{
		Feature:   "auth-spec",
		Role:      "Reviewer",
		PID:       1234,
		StartedAt: time.Now().UTC(),
	})

	// Simulate the process exiting after SIGTERM so Kill returns promptly.
	go func() {
		time.Sleep(100 * time.Millisecond)
		tracker.RecordEnd(1234, -1)
	}()

	handler := HandleKillProcess(killSvc)

	req := httptest.NewRequest(http.MethodPost, "/api/processes/1234/kill", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["status"] != "kill_accepted" {
		t.Errorf("expected status kill_accepted, got %s", resp["status"])
	}
}

// ---------------------------------------------------------------------------
// TestProcessHandler_Kill_404 — PID not in store
// ---------------------------------------------------------------------------

func TestProcessHandler_Kill_404(t *testing.T) {
	tracker := process.NewProcessTracker(nil, nil)
	sender := &noopSignalSender{}
	killSvc := process.NewKillService(sender, tracker, 0)
	handler := HandleKillProcess(killSvc)

	req := httptest.NewRequest(http.MethodPost, "/api/processes/99999/kill", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(resp["error"], "99999") {
		t.Errorf("expected error to contain PID 99999, got %q", resp["error"])
	}
}

// ---------------------------------------------------------------------------
// TestProcessHandler_Kill_409 — already terminated
// ---------------------------------------------------------------------------

func TestProcessHandler_Kill_409(t *testing.T) {
	store := newTestProcessStore(t)
	tracker := process.NewProcessTracker(store, nil)
	sender := &noopSignalSender{}
	killSvc := process.NewKillService(sender, tracker, 0)

	// Register then end a process.
	_ = tracker.Register(process.ProcessRecord{
		Feature:   "auth-spec",
		Role:      "Reviewer",
		PID:       2222,
		StartedAt: time.Now().UTC(),
	})
	tracker.RecordEnd(2222, 0)

	handler := HandleKillProcess(killSvc)

	req := httptest.NewRequest(http.MethodPost, "/api/processes/2222/kill", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// TestProcessHandler_Kill_InvalidPID — table-driven
// ---------------------------------------------------------------------------

func TestProcessHandler_Kill_InvalidPID(t *testing.T) {
	tracker := process.NewProcessTracker(nil, nil)
	sender := &noopSignalSender{}
	killSvc := process.NewKillService(sender, tracker, 0)
	handler := HandleKillProcess(killSvc)

	tests := []struct {
		name string
		path string
	}{
		{"zero", "/api/processes/0/kill"},
		{"negative", "/api/processes/-1/kill"},
		{"large negative", "/api/processes/-999/kill"},
		{"non-numeric", "/api/processes/abc/kill"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tt.path, nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestProcessHandler_Kill_MethodNotAllowed
// ---------------------------------------------------------------------------

func TestProcessHandler_Kill_MethodNotAllowed(t *testing.T) {
	tracker := process.NewProcessTracker(nil, nil)
	sender := &noopSignalSender{}
	killSvc := process.NewKillService(sender, tracker, 0)
	handler := HandleKillProcess(killSvc)

	req := httptest.NewRequest(http.MethodGet, "/api/processes/1234/kill", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// TestProcessHandler_List_FilterByFeature
// ---------------------------------------------------------------------------

func TestProcessHandler_List_FilterByFeature(t *testing.T) {
	store := newTestProcessStore(t)
	tracker := process.NewProcessTracker(store, nil)

	now := time.Now().UTC()
	_ = tracker.Register(process.ProcessRecord{Feature: "auth-spec", Role: "Reviewer", PID: 100, StartedAt: now})
	_ = tracker.Register(process.ProcessRecord{Feature: "auth-spec", Role: "Drafter", PID: 200, StartedAt: now})
	_ = tracker.Register(process.ProcessRecord{Feature: "billing", Role: "Reviewer", PID: 300, StartedAt: now})

	handler := HandleListProcesses(tracker)

	req := httptest.NewRequest(http.MethodGet, "/api/processes?feature=auth-spec", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var records []process.ProcessRecord
	if err := json.NewDecoder(w.Body).Decode(&records); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records for feature=auth-spec, got %d", len(records))
	}
	for _, r := range records {
		if r.Feature != "auth-spec" {
			t.Errorf("expected feature auth-spec, got %s", r.Feature)
		}
	}
}

// ---------------------------------------------------------------------------
// TestProcessHandler_List_FilterByStatus
// ---------------------------------------------------------------------------

func TestProcessHandler_List_FilterByStatus(t *testing.T) {
	store := newTestProcessStore(t)
	tracker := process.NewProcessTracker(store, nil)

	now := time.Now().UTC()
	_ = tracker.Register(process.ProcessRecord{Feature: "f1", Role: "Drafter", PID: 100, StartedAt: now})
	_ = tracker.Register(process.ProcessRecord{Feature: "f2", Role: "Reviewer", PID: 200, StartedAt: now})
	tracker.RecordEnd(200, 0) // PID 200 is now "exited"

	handler := HandleListProcesses(tracker)

	req := httptest.NewRequest(http.MethodGet, "/api/processes?status=running", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var records []process.ProcessRecord
	if err := json.NewDecoder(w.Body).Decode(&records); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 running record, got %d", len(records))
	}
	if records[0].PID != 100 {
		t.Errorf("expected PID 100, got %d", records[0].PID)
	}
}

// ---------------------------------------------------------------------------
// TestProcessHandler_List_FilterCombined
// ---------------------------------------------------------------------------

func TestProcessHandler_List_FilterCombined(t *testing.T) {
	store := newTestProcessStore(t)
	tracker := process.NewProcessTracker(store, nil)

	now := time.Now().UTC()
	_ = tracker.Register(process.ProcessRecord{Feature: "auth-spec", Role: "Drafter", PID: 100, StartedAt: now})
	_ = tracker.Register(process.ProcessRecord{Feature: "auth-spec", Role: "Reviewer", PID: 200, StartedAt: now})
	_ = tracker.Register(process.ProcessRecord{Feature: "billing", Role: "Drafter", PID: 300, StartedAt: now})
	tracker.RecordEnd(200, 0) // auth-spec Reviewer is now exited

	handler := HandleListProcesses(tracker)

	req := httptest.NewRequest(http.MethodGet, "/api/processes?feature=auth-spec&status=running", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var records []process.ProcessRecord
	if err := json.NewDecoder(w.Body).Decode(&records); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record for feature=auth-spec&status=running, got %d", len(records))
	}
	if records[0].PID != 100 {
		t.Errorf("expected PID 100, got %d", records[0].PID)
	}
}

// ---------------------------------------------------------------------------
// TestProcessHandler_List_InvalidStatus — returns 400 Bad Request
// ---------------------------------------------------------------------------

func TestProcessHandler_List_InvalidStatus(t *testing.T) {
	tracker := process.NewProcessTracker(nil, nil)
	handler := HandleListProcesses(tracker)

	tests := []struct {
		name   string
		status string
	}{
		{"pending", "pending"},
		{"active", "active"},
		{"unknown", "foobar"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/processes?status="+tt.status, nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
			}

			var resp map[string]string
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if resp["error"] == "" {
				t.Error("expected error message in response")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestProcessHandler_Kill_403 — EPERM returns 403 Forbidden
// ---------------------------------------------------------------------------

func TestProcessHandler_Kill_403(t *testing.T) {
	store := newTestProcessStore(t)
	tracker := process.NewProcessTracker(store, nil)
	sender := &epermSignalSender{}
	killSvc := process.NewKillService(sender, tracker, 0)

	_ = tracker.Register(process.ProcessRecord{
		Feature:   "auth-spec",
		Role:      "Reviewer",
		PID:       5555,
		StartedAt: time.Now().UTC(),
	})

	handler := HandleKillProcess(killSvc)

	req := httptest.NewRequest(http.MethodPost, "/api/processes/5555/kill", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["error"] == "" {
		t.Error("expected error message in response")
	}
}

// ---------------------------------------------------------------------------
// Signal sender mocks
// ---------------------------------------------------------------------------

type noopSignalSender struct{}

func (noopSignalSender) Send(pid int, sig syscall.Signal) error { return nil }

type epermSignalSender struct{}

func (epermSignalSender) Send(pid int, sig syscall.Signal) error { return syscall.EPERM }
