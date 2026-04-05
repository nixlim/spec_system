package specworkflow

import (
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/foundry-zero/adversarial-spec-system/internal/process"

	_ "modernc.org/sqlite"
)

// ---------------------------------------------------------------------------
// Mock tracker helpers — captures Register and RecordEnd calls
// ---------------------------------------------------------------------------

type trackerCall struct {
	Method string
	Record process.ProcessRecord
	PID    int
	Code   int
}

type callCollector struct {
	mu    sync.Mutex
	calls []trackerCall
}

func (c *callCollector) add(call trackerCall) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, call)
}

func (c *callCollector) getCalls() []trackerCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := make([]trackerCall, len(c.calls))
	copy(cp, c.calls)
	return cp
}

// newTestTracker creates a real ProcessTracker backed by an in-memory SQLite
// store and an event collector that records all emitted events.
func newTestTracker(t *testing.T) (*process.ProcessTracker, *callCollector) {
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

	collector := &callCollector{}
	emitFn := func(eventType, featureName string, data interface{}) {
		switch d := data.(type) {
		case process.ProcessStartedEvent:
			collector.add(trackerCall{
				Method: "Register",
				Record: process.ProcessRecord{Feature: d.Feature, Role: d.Role, PID: d.PID},
			})
		case process.ProcessEndedEvent:
			collector.add(trackerCall{
				Method: "RecordEnd",
				PID:    d.PID,
				Code:   d.ExitCode,
			})
		}
	}

	tracker := process.NewProcessTracker(store, emitFn)
	return tracker, collector
}

// ---------------------------------------------------------------------------
// TestClaudeRunner_EmitsProcessEvents
// ---------------------------------------------------------------------------

func TestClaudeRunner_EmitsProcessEvents(t *testing.T) {
	tracker, collector := newTestTracker(t)

	outputDir := t.TempDir()
	outputPath := filepath.Join(outputDir, "output.json")

	// Use a helper script that outputs valid Claude JSON to stdout and exits 0.
	script := filepath.Join(t.TempDir(), "mock-claude.sh")
	err := os.WriteFile(script, []byte(`#!/bin/sh
echo '{"result":"ok","cost_usd":0.01,"duration_ms":100,"is_error":false}'
`), 0755)
	if err != nil {
		t.Fatalf("write script: %v", err)
	}

	runner := &ClaudeRunner{
		Command: script,
		Args:    []string{},
		Tracker: tracker,
		Feature: "auth-spec",
		Role:    "Reviewer",
		Timeout: 10 * time.Second,
	}

	exitCode, _, _, _, runErr := runner.Run("test prompt", outputPath, 10)
	// The runner may fail to parse output or find the file — that's OK.
	// We only care that Register and RecordEnd were called.
	_ = exitCode
	_ = runErr

	// Give a moment for any goroutines to complete.
	time.Sleep(100 * time.Millisecond)

	calls := collector.getCalls()

	// Verify Register was called.
	var foundRegister, foundEnd bool
	for _, c := range calls {
		if c.Method == "Register" {
			foundRegister = true
			if c.Record.Feature != "auth-spec" {
				t.Errorf("Register: expected feature auth-spec, got %s", c.Record.Feature)
			}
			if c.Record.Role != "Reviewer" {
				t.Errorf("Register: expected role Reviewer, got %s", c.Record.Role)
			}
			if c.Record.PID <= 0 {
				t.Errorf("Register: expected positive PID, got %d", c.Record.PID)
			}
		}
		if c.Method == "RecordEnd" {
			foundEnd = true
		}
	}

	if !foundRegister {
		t.Error("expected Register to be called on the tracker")
	}
	if !foundEnd {
		t.Error("expected RecordEnd to be called on the tracker")
	}
}

// ---------------------------------------------------------------------------
// TestCodexRunner_EmitsProcessEvents
// ---------------------------------------------------------------------------

func TestCodexRunner_EmitsProcessEvents(t *testing.T) {
	tracker, collector := newTestTracker(t)

	outputDir := t.TempDir()
	outputPath := filepath.Join(outputDir, "output.json")

	// Use a helper script that reads stdin and writes output to the file.
	script := filepath.Join(t.TempDir(), "mock-codex.sh")
	// The codex runner expects --output-last-message to write to a file.
	// Our mock script just creates the output file and exits.
	err := os.WriteFile(script, []byte(fmt.Sprintf(`#!/bin/sh
echo '{"result":"ok"}' > %s
`, outputPath)), 0755)
	if err != nil {
		t.Fatalf("write script: %v", err)
	}

	runner := &CodexRunner{
		Command: script,
		Tracker: tracker,
		Feature: "billing-spec",
		Role:    "Drafter",
	}

	exitCode, _, _, _, runErr := runner.Run("test prompt", outputPath, 10)
	_ = exitCode
	_ = runErr

	time.Sleep(100 * time.Millisecond)

	calls := collector.getCalls()

	var foundRegister, foundEnd bool
	for _, c := range calls {
		if c.Method == "Register" {
			foundRegister = true
			if c.Record.Feature != "billing-spec" {
				t.Errorf("Register: expected feature billing-spec, got %s", c.Record.Feature)
			}
			if c.Record.Role != "Drafter" {
				t.Errorf("Register: expected role Drafter, got %s", c.Record.Role)
			}
			if c.Record.PID <= 0 {
				t.Errorf("Register: expected positive PID, got %d", c.Record.PID)
			}
		}
		if c.Method == "RecordEnd" {
			foundEnd = true
		}
	}

	if !foundRegister {
		t.Error("expected Register to be called on the tracker")
	}
	if !foundEnd {
		t.Error("expected RecordEnd to be called on the tracker")
	}
}

// ---------------------------------------------------------------------------
// TestNoEventOnStartFailure — no Register call when cmd.Start() fails
// ---------------------------------------------------------------------------

func TestNoEventOnStartFailure(t *testing.T) {
	tracker, collector := newTestTracker(t)

	outputDir := t.TempDir()
	outputPath := filepath.Join(outputDir, "output.json")

	// Use a non-existent binary to trigger cmd.Start() failure.
	nonexistent := "/tmp/nonexistent-binary-" + fmt.Sprintf("%d", time.Now().UnixNano())

	// Verify the binary truly doesn't exist.
	if _, err := exec.LookPath(nonexistent); err == nil {
		t.Skip("unexpectedly found the nonexistent binary")
	}

	runner := &ClaudeRunner{
		Command: nonexistent,
		Tracker: tracker,
		Feature: "auth-spec",
		Role:    "Reviewer",
		Timeout: 5 * time.Second,
	}

	_, _, _, _, runErr := runner.Run("test prompt", outputPath, 5)
	if runErr == nil {
		t.Fatal("expected error from Run with nonexistent binary")
	}

	time.Sleep(50 * time.Millisecond)

	calls := collector.getCalls()
	for _, c := range calls {
		if c.Method == "Register" {
			t.Errorf("Register should NOT be called when cmd.Start() fails, but was called for PID %d", c.Record.PID)
		}
	}
}
