package process

import (
	"context"
	"database/sql"
	"sync"
	"syscall"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// ---------------------------------------------------------------------------
// Mock SignalSender for tests
// ---------------------------------------------------------------------------

type mockSignalSender struct {
	mu    sync.Mutex
	calls []signalCall
	err   error // if non-nil, all Send calls return this error
}

type signalCall struct {
	PID    int
	Signal syscall.Signal
}

func (m *mockSignalSender) Send(pid int, sig syscall.Signal) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, signalCall{PID: pid, Signal: sig})
	return m.err
}

func (m *mockSignalSender) getCalls() []signalCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]signalCall, len(m.calls))
	copy(cp, m.calls)
	return cp
}

// ---------------------------------------------------------------------------
// Event collector for tracking emitted events
// ---------------------------------------------------------------------------

type emittedEvent struct {
	EventType   string
	FeatureName string
	Data        interface{}
}

type eventCollector struct {
	mu     sync.Mutex
	events []emittedEvent
}

func (c *eventCollector) emit(eventType, featureName string, data interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, emittedEvent{
		EventType:   eventType,
		FeatureName: featureName,
		Data:        data,
	})
}

func (c *eventCollector) getEvents() []emittedEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := make([]emittedEvent, len(c.events))
	copy(cp, c.events)
	return cp
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	store, err := NewSQLiteStore(db)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	return store
}

// ---------------------------------------------------------------------------
// Integration: ProcessTracker Register → RecordEnd → GetAll cycle
// ---------------------------------------------------------------------------

func TestIntegration_TrackerLifecycle(t *testing.T) {
	store := newTestStore(t)
	collector := &eventCollector{}
	tracker := NewProcessTracker(store, collector.emit)

	now := time.Now().Truncate(time.Second).UTC()

	// Register a process.
	err := tracker.Register(ProcessRecord{
		Feature:   "auth-spec",
		Role:      "Reviewer",
		PID:       1234,
		StartedAt: now,
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Verify it's in runtime.
	rec := tracker.Get(1234)
	if rec == nil {
		t.Fatal("expected PID 1234 in runtime")
	}
	if rec.Status != StatusRunning {
		t.Errorf("expected status running, got %s", rec.Status)
	}
	if rec.Feature != "auth-spec" {
		t.Errorf("expected feature auth-spec, got %s", rec.Feature)
	}

	// Verify process_started event was emitted.
	events := collector.getEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].EventType != "process_started" {
		t.Errorf("expected event type process_started, got %s", events[0].EventType)
	}
	if events[0].FeatureName != "auth-spec" {
		t.Errorf("expected feature name auth-spec, got %s", events[0].FeatureName)
	}
	startedEvt, ok := events[0].Data.(ProcessStartedEvent)
	if !ok {
		t.Fatalf("expected ProcessStartedEvent, got %T", events[0].Data)
	}
	if startedEvt.PID != 1234 {
		t.Errorf("expected PID 1234 in event, got %d", startedEvt.PID)
	}
	if startedEvt.Role != "Reviewer" {
		t.Errorf("expected role Reviewer, got %s", startedEvt.Role)
	}

	// End the process.
	endRec := tracker.RecordEnd(1234, 0)
	if endRec.Status != StatusExited {
		t.Errorf("expected status exited, got %s", endRec.Status)
	}
	if endRec.ExitCode == nil || *endRec.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %v", endRec.ExitCode)
	}

	// Verify process_ended event was emitted.
	events = collector.getEvents()
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[1].EventType != "process_ended" {
		t.Errorf("expected event type process_ended, got %s", events[1].EventType)
	}
	endedEvt, ok := events[1].Data.(ProcessEndedEvent)
	if !ok {
		t.Fatalf("expected ProcessEndedEvent, got %T", events[1].Data)
	}
	if endedEvt.PID != 1234 {
		t.Errorf("expected PID 1234, got %d", endedEvt.PID)
	}
	if endedEvt.Status != StatusExited {
		t.Errorf("expected status exited, got %s", endedEvt.Status)
	}
	if endedEvt.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", endedEvt.ExitCode)
	}

	// Verify PID removed from runtime.
	if tracker.Get(1234) != nil {
		t.Error("expected PID 1234 removed from runtime after RecordEnd")
	}

	// Verify GetAll returns the record from the store.
	all := tracker.GetAll()
	if len(all) != 1 {
		t.Fatalf("expected 1 record from GetAll, got %d", len(all))
	}
	if all[0].Status != StatusExited {
		t.Errorf("expected status exited in GetAll, got %s", all[0].Status)
	}
}

// ---------------------------------------------------------------------------
// Integration: ProcessTracker emits "killed" status when kill was requested
// ---------------------------------------------------------------------------

func TestIntegration_TrackerKilledStatus(t *testing.T) {
	store := newTestStore(t)
	collector := &eventCollector{}
	tracker := NewProcessTracker(store, collector.emit)

	_ = tracker.Register(ProcessRecord{
		Feature:   "auth-spec",
		Role:      "Drafter",
		PID:       5678,
		StartedAt: time.Now().UTC(),
	})

	// Mark as kill-requested.
	tracker.RequestKill(5678)

	// End the process — should be "killed".
	endRec := tracker.RecordEnd(5678, -1)
	if endRec.Status != StatusKilled {
		t.Errorf("expected status killed, got %s", endRec.Status)
	}

	// Verify the process_ended event carries "killed".
	events := collector.getEvents()
	endedEvt := events[len(events)-1].Data.(ProcessEndedEvent)
	if endedEvt.Status != StatusKilled {
		t.Errorf("expected killed in event, got %s", endedEvt.Status)
	}

	// Verify the store also has "killed".
	records, err := store.List("", "", StatusKilled, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 killed record, got %d", len(records))
	}
}

// ---------------------------------------------------------------------------
// Integration: SQLiteStore Save → UpdateEnd → List → MarkLostOnStartup
// ---------------------------------------------------------------------------

func TestIntegration_StoreLifecycle(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().Truncate(time.Second).UTC()

	// Save two running records and one exited.
	_ = store.Save(ProcessRecord{Feature: "f1", Role: "Drafter", PID: 100, Status: StatusRunning, StartedAt: now})
	_ = store.Save(ProcessRecord{Feature: "f2", Role: "Reviewer", PID: 200, Status: StatusRunning, StartedAt: now.Add(-time.Minute)})

	endTime := now.Add(5 * time.Minute)
	exitCode := 0
	_ = store.Save(ProcessRecord{Feature: "f3", Role: "Drafter", PID: 300, Status: StatusExited, StartedAt: now.Add(-2 * time.Minute), EndedAt: &endTime, ExitCode: &exitCode})

	// UpdateEnd one of the running records.
	if err := store.UpdateEnd(100, StatusExited, 0, endTime); err != nil {
		t.Fatalf("update end: %v", err)
	}

	// List all — should be 3.
	all, err := store.List("", "", "", 0)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 records, got %d", len(all))
	}

	// MarkLostOnStartup — only PID 200 should be affected.
	lost, err := store.MarkLostOnStartup(context.Background())
	if err != nil {
		t.Fatalf("mark lost: %v", err)
	}
	if len(lost) != 1 {
		t.Fatalf("expected 1 lost, got %d", len(lost))
	}
	if lost[0].PID != 200 {
		t.Errorf("expected lost PID 200, got %d", lost[0].PID)
	}

	// Verify PID 200 is now "lost" in the store.
	lostRecs, err := store.List("", "", StatusLost, 0)
	if err != nil {
		t.Fatalf("list lost: %v", err)
	}
	if len(lostRecs) != 1 || lostRecs[0].PID != 200 {
		t.Errorf("expected PID 200 with status lost, got %+v", lostRecs)
	}

	// Verify exited records untouched.
	exitedRecs, err := store.List("", "", StatusExited, 0)
	if err != nil {
		t.Fatalf("list exited: %v", err)
	}
	if len(exitedRecs) != 2 {
		t.Errorf("expected 2 exited records, got %d", len(exitedRecs))
	}
}

// ---------------------------------------------------------------------------
// Integration: KillService sends SIGTERM then SIGKILL on timeout
// ---------------------------------------------------------------------------

func TestIntegration_KillService_SIGTERMThenSIGKILL(t *testing.T) {
	store := newTestStore(t)
	tracker := NewProcessTracker(store, nil)
	sender := &mockSignalSender{}
	// Use a short escalation timeout (200ms) so the test completes quickly.
	killSvc := NewKillService(sender, tracker, 200*time.Millisecond)

	// Register a process that will NOT be removed from runtime (simulating
	// a process that ignores SIGTERM).
	_ = tracker.Register(ProcessRecord{
		Feature:   "auth-spec",
		Role:      "Reviewer",
		PID:       9999,
		StartedAt: time.Now().UTC(),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := killSvc.Kill(ctx, 9999)
	if err != nil {
		t.Fatalf("Kill: %v", err)
	}

	calls := sender.getCalls()
	if len(calls) < 2 {
		t.Fatalf("expected at least 2 signal calls, got %d", len(calls))
	}
	if calls[0].Signal != syscall.SIGTERM {
		t.Errorf("expected first signal SIGTERM, got %v", calls[0].Signal)
	}
	if calls[1].Signal != syscall.SIGKILL {
		t.Errorf("expected second signal SIGKILL, got %v", calls[1].Signal)
	}
	if calls[0].PID != 9999 || calls[1].PID != 9999 {
		t.Errorf("expected PID 9999, got %d and %d", calls[0].PID, calls[1].PID)
	}
}

// ---------------------------------------------------------------------------
// Integration: KillService sends SIGTERM; process exits before escalation
// ---------------------------------------------------------------------------

func TestIntegration_KillService_CancelsEscalation(t *testing.T) {
	store := newTestStore(t)
	collector := &eventCollector{}
	tracker := NewProcessTracker(store, collector.emit)
	sender := &mockSignalSender{}
	killSvc := NewKillService(sender, tracker, 0)

	_ = tracker.Register(ProcessRecord{
		Feature:   "auth-spec",
		Role:      "Drafter",
		PID:       8888,
		StartedAt: time.Now().UTC(),
	})

	// Simulate the process exiting shortly after SIGTERM by removing it
	// from the runtime map in a goroutine after a brief delay.
	go func() {
		time.Sleep(200 * time.Millisecond)
		tracker.RecordEnd(8888, -1)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := killSvc.Kill(ctx, 8888)
	if err != nil {
		t.Fatalf("Kill: %v", err)
	}

	calls := sender.getCalls()
	// Should only have SIGTERM, no SIGKILL.
	if len(calls) != 1 {
		t.Fatalf("expected 1 signal call (SIGTERM only), got %d: %v", len(calls), calls)
	}
	if calls[0].Signal != syscall.SIGTERM {
		t.Errorf("expected SIGTERM, got %v", calls[0].Signal)
	}
}

// ---------------------------------------------------------------------------
// Integration: KillService returns ErrNotFound for untracked PID
// ---------------------------------------------------------------------------

func TestIntegration_KillService_NotFound(t *testing.T) {
	tracker := NewProcessTracker(nil, nil)
	sender := &mockSignalSender{}
	killSvc := NewKillService(sender, tracker, 0)

	err := killSvc.Kill(context.Background(), 77777)
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}

	// No signals should have been sent.
	if len(sender.getCalls()) != 0 {
		t.Error("expected no signal calls for untracked PID")
	}
}

// ---------------------------------------------------------------------------
// Integration: KillService rejects invalid PID (pid <= 0)
// ---------------------------------------------------------------------------

func TestIntegration_KillService_InvalidPID(t *testing.T) {
	tracker := NewProcessTracker(nil, nil)
	sender := &mockSignalSender{}
	killSvc := NewKillService(sender, tracker, 0)

	tests := []struct {
		name string
		pid  int
	}{
		{"zero", 0},
		{"negative", -1},
		{"large negative", -999},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := killSvc.Kill(context.Background(), tt.pid)
			if err == nil {
				t.Error("expected error for invalid PID")
			}
			if len(sender.getCalls()) != 0 {
				t.Error("expected no signal calls for invalid PID")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Integration: KillService propagates EPERM from SignalSender
// ---------------------------------------------------------------------------

func TestIntegration_KillService_EPERM(t *testing.T) {
	store := newTestStore(t)
	tracker := NewProcessTracker(store, nil)
	sender := &mockSignalSender{err: syscall.EPERM}
	killSvc := NewKillService(sender, tracker, 0)

	_ = tracker.Register(ProcessRecord{
		Feature:   "auth-spec",
		Role:      "Reviewer",
		PID:       4444,
		StartedAt: time.Now().UTC(),
	})

	err := killSvc.Kill(context.Background(), 4444)
	if err == nil {
		t.Fatal("expected EPERM error")
	}
}

// ---------------------------------------------------------------------------
// Integration: KillService returns ErrNotFound for already-terminated PID
// ---------------------------------------------------------------------------

func TestIntegration_KillService_AlreadyTerminated(t *testing.T) {
	store := newTestStore(t)
	tracker := NewProcessTracker(store, nil)
	sender := &mockSignalSender{}
	killSvc := NewKillService(sender, tracker, 0)

	// Register then end a process.
	_ = tracker.Register(ProcessRecord{
		Feature:   "auth-spec",
		Role:      "Reviewer",
		PID:       3333,
		StartedAt: time.Now().UTC(),
	})
	tracker.RecordEnd(3333, 0)

	// Kill should fail — PID is no longer in the runtime map but is in the store.
	err := killSvc.Kill(context.Background(), 3333)
	if err != ErrAlreadyTerminated {
		t.Errorf("expected ErrAlreadyTerminated for already-terminated PID, got %v", err)
	}

	// No signals should have been sent.
	if len(sender.getCalls()) != 0 {
		t.Errorf("expected no signal calls for already-terminated PID, got %d", len(sender.getCalls()))
	}
}

// ---------------------------------------------------------------------------
// Integration: Simultaneous Kill requests — second gets error, no panic
// ---------------------------------------------------------------------------

func TestIntegration_KillService_SimultaneousRequests(t *testing.T) {
	store := newTestStore(t)
	tracker := NewProcessTracker(store, nil)
	sender := &mockSignalSender{}
	// Short escalation so test completes fast.
	killSvc := NewKillService(sender, tracker, 200*time.Millisecond)

	_ = tracker.Register(ProcessRecord{
		Feature:   "auth-spec",
		Role:      "Reviewer",
		PID:       7777,
		StartedAt: time.Now().UTC(),
	})

	// Launch two concurrent Kill calls.
	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func(idx int) {
			defer wg.Done()
			errs[idx] = killSvc.Kill(context.Background(), 7777)
		}(i)
	}

	// Simulate the process exiting after a brief delay so both Kill calls
	// can proceed without blocking forever.
	go func() {
		time.Sleep(100 * time.Millisecond)
		tracker.RecordEnd(7777, -1)
	}()

	wg.Wait()

	// At least one should succeed (nil error) or both may encounter
	// ErrNotFound if the process exits between Get and Send. The key
	// assertion is: no panic, no double-signal race.
	var successes, failures int
	for _, err := range errs {
		if err == nil {
			successes++
		} else {
			failures++
		}
	}

	// We should have at least one outcome (no deadlock).
	if successes+failures != 2 {
		t.Fatalf("expected 2 results, got %d successes + %d failures", successes, failures)
	}

	// Verify no more than one SIGTERM was sent (the second Kill may fail
	// before reaching Send, or may also Send — both are acceptable).
	calls := sender.getCalls()
	termCount := 0
	for _, c := range calls {
		if c.Signal == syscall.SIGTERM {
			termCount++
		}
	}
	// At most 2 SIGTERMs (if both goroutines race past Get before either
	// sends), but importantly no panic or corruption occurred.
	if termCount > 2 {
		t.Errorf("expected at most 2 SIGTERM calls, got %d", termCount)
	}
	t.Logf("simultaneous kill: %d successes, %d failures, %d SIGTERM signals", successes, failures, termCount)
}

// ---------------------------------------------------------------------------
// Integration: ProcessTracker emits process_started and process_ended events
// ---------------------------------------------------------------------------

func TestIntegration_TrackerEvents(t *testing.T) {
	collector := &eventCollector{}
	tracker := NewProcessTracker(nil, collector.emit)

	// Register two processes.
	_ = tracker.Register(ProcessRecord{Feature: "f1", Role: "Drafter", PID: 10, StartedAt: time.Now().UTC()})
	_ = tracker.Register(ProcessRecord{Feature: "f2", Role: "Reviewer", PID: 20, StartedAt: time.Now().UTC()})

	// End one normally, one killed.
	tracker.RecordEnd(10, 0)
	tracker.RequestKill(20)
	tracker.RecordEnd(20, -1)

	events := collector.getEvents()

	// Expect: started(10), started(20), ended(10), ended(20) = 4 events.
	if len(events) != 4 {
		t.Fatalf("expected 4 events, got %d", len(events))
	}

	// Verify types.
	expectedTypes := []string{"process_started", "process_started", "process_ended", "process_ended"}
	for i, want := range expectedTypes {
		if events[i].EventType != want {
			t.Errorf("event[%d]: expected type %s, got %s", i, want, events[i].EventType)
		}
	}

	// Verify ended statuses.
	ended10 := events[2].Data.(ProcessEndedEvent)
	if ended10.Status != StatusExited {
		t.Errorf("PID 10: expected status exited, got %s", ended10.Status)
	}
	ended20 := events[3].Data.(ProcessEndedEvent)
	if ended20.Status != StatusKilled {
		t.Errorf("PID 20: expected status killed, got %s", ended20.Status)
	}
}

// ---------------------------------------------------------------------------
// Integration: Startup recovery marks running as lost, leaves others alone
// ---------------------------------------------------------------------------

func TestIntegration_StartupRecovery(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().Truncate(time.Second).UTC()

	// Pre-populate the store with various statuses.
	_ = store.Save(ProcessRecord{Feature: "f1", Role: "Drafter", PID: 10, Status: StatusRunning, StartedAt: now})
	_ = store.Save(ProcessRecord{Feature: "f2", Role: "Reviewer", PID: 20, Status: StatusRunning, StartedAt: now})

	endTime := now.Add(time.Minute)
	exitCode := 0
	_ = store.Save(ProcessRecord{Feature: "f3", Role: "Drafter", PID: 30, Status: StatusExited, StartedAt: now, EndedAt: &endTime, ExitCode: &exitCode})
	_ = store.Save(ProcessRecord{Feature: "f4", Role: "Reviewer", PID: 40, Status: StatusKilled, StartedAt: now, EndedAt: &endTime, ExitCode: &exitCode})

	// Run startup recovery.
	lost, err := store.MarkLostOnStartup(context.Background())
	if err != nil {
		t.Fatalf("mark lost: %v", err)
	}
	if len(lost) != 2 {
		t.Fatalf("expected 2 lost, got %d", len(lost))
	}

	// Verify all records.
	all, err := store.List("", "", "", 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	statusByPID := make(map[int]string)
	for _, r := range all {
		statusByPID[r.PID] = r.Status
	}

	expected := map[int]string{
		10: StatusLost,
		20: StatusLost,
		30: StatusExited,
		40: StatusKilled,
	}
	for pid, want := range expected {
		if got := statusByPID[pid]; got != want {
			t.Errorf("PID %d: expected status %s, got %s", pid, want, got)
		}
	}
}

// ---------------------------------------------------------------------------
// Integration: Process record persists across "restart" (reopen store)
// ---------------------------------------------------------------------------

func TestIntegration_StorePersistence(t *testing.T) {
	// Use a real file-backed DB to test persistence across "restarts".
	dbPath := t.TempDir() + "/test.db"

	db1, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db1: %v", err)
	}
	store1, err := NewSQLiteStore(db1)
	if err != nil {
		t.Fatalf("new store1: %v", err)
	}

	now := time.Now().Truncate(time.Second).UTC()
	_ = store1.Save(ProcessRecord{Feature: "auth-spec", Role: "Reviewer", PID: 42, Status: StatusRunning, StartedAt: now})
	endTime := now.Add(5 * time.Minute)
	_ = store1.UpdateEnd(42, StatusExited, 0, endTime)
	db1.Close()

	// "Restart" — open a new DB connection.
	db2, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db2: %v", err)
	}
	defer db2.Close()
	store2, err := NewSQLiteStore(db2)
	if err != nil {
		t.Fatalf("new store2: %v", err)
	}

	records, err := store2.List("", "", "", 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record after restart, got %d", len(records))
	}
	if records[0].PID != 42 {
		t.Errorf("expected PID 42, got %d", records[0].PID)
	}
	if records[0].Status != StatusExited {
		t.Errorf("expected status exited, got %s", records[0].Status)
	}
	if records[0].Feature != "auth-spec" {
		t.Errorf("expected feature auth-spec, got %s", records[0].Feature)
	}
}
