package process

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestSaveAndList(t *testing.T) {
	store, err := NewSQLiteStore(openTestDB(t))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	now := time.Now().Truncate(time.Second)
	rec := ProcessRecord{
		Feature:   "auth-spec",
		Role:      "Reviewer",
		PID:       1234,
		Status:    StatusRunning,
		StartedAt: now,
	}
	if err := store.Save(rec); err != nil {
		t.Fatalf("save: %v", err)
	}

	records, err := store.List("", "", "", 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].PID != 1234 || records[0].Feature != "auth-spec" {
		t.Errorf("unexpected record: %+v", records[0])
	}
}

func TestUpdateEnd(t *testing.T) {
	store, err := NewSQLiteStore(openTestDB(t))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	now := time.Now().Truncate(time.Second)
	_ = store.Save(ProcessRecord{
		Feature: "auth-spec", Role: "Drafter", PID: 42,
		Status: StatusRunning, StartedAt: now,
	})

	endTime := now.Add(5 * time.Minute)
	if err := store.UpdateEnd(42, StatusExited, 0, endTime); err != nil {
		t.Fatalf("update end: %v", err)
	}

	records, err := store.List("", "", StatusExited, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].Status != StatusExited {
		t.Errorf("expected status exited, got %s", records[0].Status)
	}
	if records[0].ExitCode == nil || *records[0].ExitCode != 0 {
		t.Errorf("expected exit code 0, got %v", records[0].ExitCode)
	}
}

func TestUpdateEndNotFound(t *testing.T) {
	store, err := NewSQLiteStore(openTestDB(t))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := store.UpdateEnd(9999, StatusExited, 1, time.Now()); err == nil {
		t.Fatal("expected error for unknown pid")
	}
}

func TestUpdateEndAlreadyEnded(t *testing.T) {
	store, err := NewSQLiteStore(openTestDB(t))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	now := time.Now().Truncate(time.Second)
	_ = store.Save(ProcessRecord{
		Feature: "f", Role: "Drafter", PID: 42,
		Status: StatusRunning, StartedAt: now,
	})
	// First end succeeds.
	if err := store.UpdateEnd(42, StatusExited, 0, now.Add(time.Minute)); err != nil {
		t.Fatalf("first update end: %v", err)
	}
	// Second end for same PID should fail (no longer running).
	if err := store.UpdateEnd(42, StatusExited, 0, now.Add(2*time.Minute)); err == nil {
		t.Fatal("expected error for already-ended pid")
	}
}

func TestMarkLostOnStartup(t *testing.T) {
	store, err := NewSQLiteStore(openTestDB(t))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	now := time.Now().Truncate(time.Second)
	_ = store.Save(ProcessRecord{
		Feature: "f1", Role: "Drafter", PID: 10,
		Status: StatusRunning, StartedAt: now,
	})
	_ = store.Save(ProcessRecord{
		Feature: "f2", Role: "Reviewer", PID: 20,
		Status: StatusRunning, StartedAt: now,
	})
	endTime := now.Add(time.Minute)
	exitCode := 0
	_ = store.Save(ProcessRecord{
		Feature: "f3", Role: "Drafter", PID: 30,
		Status: StatusExited, StartedAt: now, EndedAt: &endTime, ExitCode: &exitCode,
	})

	lost, err := store.MarkLostOnStartup(context.Background())
	if err != nil {
		t.Fatalf("mark lost: %v", err)
	}
	if len(lost) != 2 {
		t.Fatalf("expected 2 lost, got %d", len(lost))
	}

	// Verify the running records are now lost in the DB.
	records, err := store.List("", "", StatusLost, 0)
	if err != nil {
		t.Fatalf("list lost: %v", err)
	}
	if len(records) != 2 {
		t.Errorf("expected 2 lost records, got %d", len(records))
	}

	// Verify exited record was not touched.
	exited, err := store.List("", "", StatusExited, 0)
	if err != nil {
		t.Fatalf("list exited: %v", err)
	}
	if len(exited) != 1 {
		t.Errorf("expected 1 exited record, got %d", len(exited))
	}
}

func TestListFilters(t *testing.T) {
	store, err := NewSQLiteStore(openTestDB(t))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	now := time.Now().Truncate(time.Second)
	_ = store.Save(ProcessRecord{Feature: "a", Role: "Drafter", PID: 1, Status: StatusRunning, StartedAt: now})
	_ = store.Save(ProcessRecord{Feature: "a", Role: "Reviewer", PID: 2, Status: StatusExited, StartedAt: now.Add(-time.Minute)})
	_ = store.Save(ProcessRecord{Feature: "b", Role: "Drafter", PID: 3, Status: StatusRunning, StartedAt: now.Add(-2 * time.Minute)})

	tests := []struct {
		name                       string
		feature, role, status      string
		limit, expect              int
	}{
		{"all", "", "", "", 0, 3},
		{"by feature", "a", "", "", 0, 2},
		{"by role", "", "Drafter", "", 0, 2},
		{"by status", "", "", StatusRunning, 0, 2},
		{"combined", "a", "Drafter", StatusRunning, 0, 1},
		{"with limit", "", "", "", 2, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recs, err := store.List(tt.feature, tt.role, tt.status, tt.limit)
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			if len(recs) != tt.expect {
				t.Errorf("expected %d records, got %d", tt.expect, len(recs))
			}
		})
	}
}

func TestSaveUpsert(t *testing.T) {
	store, err := NewSQLiteStore(openTestDB(t))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	now := time.Now().Truncate(time.Second)
	_ = store.Save(ProcessRecord{Feature: "f", Role: "Drafter", PID: 55, Status: StatusRunning, StartedAt: now})
	_ = store.Save(ProcessRecord{Feature: "f", Role: "Drafter", PID: 55, Status: StatusKilled, StartedAt: now})

	records, err := store.List("", "", "", 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record after upsert, got %d", len(records))
	}
	if records[0].Status != StatusKilled {
		t.Errorf("expected status killed after upsert, got %s", records[0].Status)
	}
}

func TestPIDReuse(t *testing.T) {
	store, err := NewSQLiteStore(openTestDB(t))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	// First process with PID 100.
	t1 := time.Now().Truncate(time.Second)
	_ = store.Save(ProcessRecord{Feature: "f1", Role: "Drafter", PID: 100, Status: StatusRunning, StartedAt: t1})
	_ = store.UpdateEnd(100, StatusExited, 0, t1.Add(time.Minute))

	// Second process reuses PID 100 with a different start time.
	t2 := t1.Add(5 * time.Minute)
	_ = store.Save(ProcessRecord{Feature: "f2", Role: "Reviewer", PID: 100, Status: StatusRunning, StartedAt: t2})

	// Both records should exist.
	records, err := store.List("", "", "", 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records for reused PID, got %d", len(records))
	}

	// UpdateEnd should only affect the running one.
	_ = store.UpdateEnd(100, StatusKilled, -1, t2.Add(time.Minute))

	exited, _ := store.List("", "", StatusExited, 0)
	killed, _ := store.List("", "", StatusKilled, 0)
	if len(exited) != 1 {
		t.Errorf("expected 1 exited, got %d", len(exited))
	}
	if len(killed) != 1 {
		t.Errorf("expected 1 killed, got %d", len(killed))
	}
}
