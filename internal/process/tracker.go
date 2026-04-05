package process

import (
	"log"
	"sync"
	"time"
)

// EventEmitFunc is a callback that the ProcessTracker calls to broadcast
// process lifecycle events. The eventType is "process_started" or
// "process_ended", featureName identifies the workflow, and data is the
// event payload (ProcessStartedEvent or ProcessEndedEvent).
type EventEmitFunc func(eventType, featureName string, data interface{})

// ProcessTracker maintains a runtime map of active processes keyed by PID.
// It coordinates with a ProcessStore for persistence and emits WebSocket
// events via a callback on process start and end.
type ProcessTracker struct {
	mu           sync.Mutex
	runtime      map[int]*ProcessRecord
	killRequests map[int]bool
	store        ProcessStore
	emitFn       EventEmitFunc
}

// NewProcessTracker creates a ProcessTracker backed by the given store and
// event emit function. Either may be nil: a nil store disables persistence,
// and a nil emitFn disables event broadcasting.
func NewProcessTracker(store ProcessStore, emitFn EventEmitFunc) *ProcessTracker {
	return &ProcessTracker{
		runtime:      make(map[int]*ProcessRecord),
		killRequests: make(map[int]bool),
		store:        store,
		emitFn:       emitFn,
	}
}

// Register records a newly started subprocess. It persists the record to the
// store (if available), adds it to the runtime map, and emits a
// process_started event. If the store write fails, the record is NOT added
// to the runtime map and no event is emitted — the process runs unmanaged.
func (t *ProcessTracker) Register(record ProcessRecord) error {
	record.Status = StatusRunning

	// Persist first — spec requires that on store failure we do NOT add to
	// runtime or emit events.
	if t.store != nil {
		if err := t.store.Save(record); err != nil {
			log.Printf("[process-tracker] ERROR: failed to persist record for PID %d: %v", record.PID, err)
			return err
		}
	}

	t.mu.Lock()
	r := record // copy into heap
	t.runtime[record.PID] = &r
	t.mu.Unlock()

	if t.emitFn != nil {
		t.emitFn("process_started", record.Feature, ProcessStartedEvent{
			Type:      "process_started",
			Feature:   record.Feature,
			Role:      record.Role,
			PID:       record.PID,
			StartedAt: record.StartedAt.UTC().Format(time.RFC3339),
		})
	}

	log.Printf("[process-tracker] registered PID %d (feature=%s, role=%s)", record.PID, record.Feature, record.Role)
	return nil
}

// RecordEnd records a subprocess termination. If the PID was in the kill-
// request set, the status is "killed"; otherwise "exited". Returns the
// updated record. The record is removed from the runtime map.
func (t *ProcessTracker) RecordEnd(pid int, exitCode int) ProcessRecord {
	now := time.Now().UTC()

	t.mu.Lock()
	rec, ok := t.runtime[pid]
	killed := t.killRequests[pid]
	delete(t.killRequests, pid)
	delete(t.runtime, pid)
	t.mu.Unlock()

	status := StatusExited
	if killed {
		status = StatusKilled
	}

	if !ok {
		// PID not tracked — return a minimal record.
		log.Printf("[process-tracker] RecordEnd called for unknown PID %d", pid)
		return ProcessRecord{PID: pid, Status: status, EndedAt: &now, ExitCode: &exitCode}
	}

	rec.Status = status
	rec.EndedAt = &now
	rec.ExitCode = &exitCode

	if t.store != nil {
		if err := t.store.UpdateEnd(pid, status, exitCode, now); err != nil {
			log.Printf("[process-tracker] ERROR: failed to persist end for PID %d: %v", pid, err)
		}
	}

	if t.emitFn != nil {
		t.emitFn("process_ended", rec.Feature, ProcessEndedEvent{
			Type:     "process_ended",
			PID:      pid,
			ExitCode: exitCode,
			Status:   status,
			EndedAt:  now.Format(time.RFC3339),
		})
	}

	log.Printf("[process-tracker] ended PID %d (status=%s, exit_code=%d)", pid, status, exitCode)
	return *rec
}

// IsKillRequested reports whether a kill has been requested for the given PID.
func (t *ProcessTracker) IsKillRequested(pid int) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.killRequests[pid]
}

// RequestKill marks a PID as kill-requested so that RecordEnd uses status
// "killed" instead of "exited". Returns true if this is the first call to
// request kill for this PID (caller should proceed with signalling), or
// false if a kill was already requested (caller should treat as duplicate).
func (t *ProcessTracker) RequestKill(pid int) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.killRequests[pid] {
		return false
	}
	t.killRequests[pid] = true
	return true
}

// GetAll returns all process records. It merges runtime (in-memory running
// processes) with persisted history from the store. Runtime records take
// precedence for PIDs that appear in both.
func (t *ProcessTracker) GetAll() []ProcessRecord {
	t.mu.Lock()
	runtimeCopy := make(map[int]ProcessRecord, len(t.runtime))
	for pid, rec := range t.runtime {
		runtimeCopy[pid] = *rec
	}
	t.mu.Unlock()

	var result []ProcessRecord

	// If we have a store, get all persisted records.
	if t.store != nil {
		stored, err := t.store.List("", "", "", 0)
		if err != nil {
			log.Printf("[process-tracker] ERROR: failed to list stored records: %v", err)
		} else {
			// Use stored records as the base, but override with runtime
			// records for PIDs that are still running.
			seen := make(map[int]bool, len(stored))
			for _, rec := range stored {
				if rRec, ok := runtimeCopy[rec.PID]; ok {
					result = append(result, rRec)
				} else {
					result = append(result, rec)
				}
				seen[rec.PID] = true
			}
			// Add any runtime records not yet in the store.
			for pid, rec := range runtimeCopy {
				if !seen[pid] {
					result = append(result, rec)
				}
			}
			return result
		}
	}

	// No store — return runtime only.
	for _, rec := range runtimeCopy {
		result = append(result, rec)
	}
	return result
}

// ListFiltered returns process records matching the supplied filters, merging
// runtime (in-memory) records with persisted history from the store. Runtime
// records take precedence for PIDs that appear in both. Empty strings are
// treated as "no filter".
func (t *ProcessTracker) ListFiltered(feature, status string, limit int) []ProcessRecord {
	t.mu.Lock()
	runtimeCopy := make(map[int]ProcessRecord, len(t.runtime))
	for pid, rec := range t.runtime {
		runtimeCopy[pid] = *rec
	}
	t.mu.Unlock()

	var result []ProcessRecord

	if t.store != nil {
		stored, err := t.store.List(feature, "", status, limit)
		if err != nil {
			log.Printf("[process-tracker] ERROR: failed to list stored records: %v", err)
		} else {
			seen := make(map[int]bool, len(stored))
			for _, rec := range stored {
				if rRec, ok := runtimeCopy[rec.PID]; ok {
					if matchesFilter(rRec, feature, status) {
						result = append(result, rRec)
					}
				} else {
					result = append(result, rec)
				}
				seen[rec.PID] = true
			}
			// Add runtime records not yet in the store that match filters.
			for pid, rec := range runtimeCopy {
				if !seen[pid] && matchesFilter(rec, feature, status) {
					result = append(result, rec)
				}
			}
			return result
		}
	}

	// No store — filter runtime only.
	for _, rec := range runtimeCopy {
		if matchesFilter(rec, feature, status) {
			result = append(result, rec)
		}
	}
	return result
}

// matchesFilter checks if a record matches the given feature and status filters.
func matchesFilter(rec ProcessRecord, feature, status string) bool {
	if feature != "" && rec.Feature != feature {
		return false
	}
	if status != "" && rec.Status != status {
		return false
	}
	return true
}

// Get returns the runtime record for a given PID, or nil if not tracked.
func (t *ProcessTracker) Get(pid int) *ProcessRecord {
	t.mu.Lock()
	defer t.mu.Unlock()
	rec, ok := t.runtime[pid]
	if !ok {
		return nil
	}
	cp := *rec
	return &cp
}
