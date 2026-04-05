package process

import (
	"context"
	"fmt"
	"log"
	"sync"
	"syscall"
	"time"
)

// DefaultEscalationTimeout is the default duration to wait after SIGTERM
// before escalating to SIGKILL.
const DefaultEscalationTimeout = 10 * time.Second

// ---------------------------------------------------------------------------
// Sentinel errors
// ---------------------------------------------------------------------------

// ErrNotFound is returned when a kill is requested for a PID that has never
// been tracked by the ProcessTracker.
var ErrNotFound = fmt.Errorf("process not found in tracker")

// ErrAlreadyTerminated is returned when a kill is requested for a PID that
// was tracked but has already exited, been killed, or been marked lost.
var ErrAlreadyTerminated = fmt.Errorf("process already terminated")

// ErrInvalidPID is returned when a kill is requested with a PID <= 0.
var ErrInvalidPID = fmt.Errorf("invalid pid: must be positive")

// ---------------------------------------------------------------------------
// KillService
// ---------------------------------------------------------------------------

// KillService sends termination signals to tracked agent subprocesses.
// It first sends SIGTERM and, if the process survives the escalation
// timeout, escalates to SIGKILL.
type KillService struct {
	sender            SignalSender
	tracker           *ProcessTracker
	escalationTimeout time.Duration

	// mu protects pendingKills for graceful shutdown.
	mu           sync.Mutex
	pendingKills map[int]context.CancelFunc // pid -> cancel for escalation timer
}

// NewKillService creates a KillService with the given signal sender, process
// tracker, and SIGTERM-to-SIGKILL escalation timeout. If escalationTimeout
// is zero, DefaultEscalationTimeout (10s) is used.
func NewKillService(sender SignalSender, tracker *ProcessTracker, escalationTimeout time.Duration) *KillService {
	if escalationTimeout <= 0 {
		escalationTimeout = DefaultEscalationTimeout
	}
	return &KillService{
		sender:            sender,
		tracker:           tracker,
		escalationTimeout: escalationTimeout,
		pendingKills:      make(map[int]context.CancelFunc),
	}
}

// Kill sends SIGTERM to the process identified by pid, waits up to the
// escalation timeout, then escalates to SIGKILL if the process is still
// running. The tracker is notified via RequestKill before any signal is
// sent so that RecordEnd marks the process as "killed".
//
// Returns:
//   - ErrInvalidPID if pid <= 0
//   - ErrNotFound if the PID was never tracked
//   - ErrAlreadyTerminated if the PID was tracked but is no longer running
//   - A wrapped syscall error if the OS rejects the signal (e.g. EPERM)
func (k *KillService) Kill(ctx context.Context, pid int) error {
	if pid <= 0 {
		return ErrInvalidPID
	}

	// Check runtime map first — is it currently running?
	rec := k.tracker.Get(pid)
	if rec != nil {
		// Process is in runtime map and running — proceed to kill.
		return k.doKill(ctx, pid)
	}

	// Not in runtime map. Check if it was ever tracked (in the store).
	all := k.tracker.GetAll()
	for _, r := range all {
		if r.PID == pid {
			// Was tracked but no longer running.
			return ErrAlreadyTerminated
		}
	}

	return ErrNotFound
}

// doKill performs the actual SIGTERM/SIGKILL sequence for a PID known to be
// in the runtime map.
func (k *KillService) doKill(ctx context.Context, pid int) error {
	// Mark kill-requested atomically — if another goroutine already
	// claimed this PID, return ErrAlreadyTerminated to avoid duplicate SIGTERMs.
	if !k.tracker.RequestKill(pid) {
		return ErrAlreadyTerminated
	}

	log.Printf("[kill-service] sending SIGTERM to PID %d", pid)
	if err := k.sender.Send(pid, syscall.SIGTERM); err != nil {
		log.Printf("[kill-service] SIGTERM to PID %d failed: %v", pid, err)
		return fmt.Errorf("send SIGTERM to pid %d: %w", pid, err)
	}

	// Track this pending kill so Shutdown can cancel it.
	escalationCtx, cancelEscalation := context.WithCancel(ctx)
	k.mu.Lock()
	k.pendingKills[pid] = cancelEscalation
	k.mu.Unlock()

	defer func() {
		k.mu.Lock()
		delete(k.pendingKills, pid)
		k.mu.Unlock()
		cancelEscalation()
	}()

	// Wait up to escalationTimeout for the process to exit. Poll the
	// tracker's runtime map — when RecordEnd is called (by the runner
	// goroutine), the PID is removed from the runtime map.
	deadline := time.After(k.escalationTimeout)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-escalationCtx.Done():
			// Context cancelled (either caller or Shutdown).
			return escalationCtx.Err()
		case <-deadline:
			// Process survived SIGTERM — escalate to SIGKILL.
			log.Printf("[kill-service] PID %d survived SIGTERM after %v, sending SIGKILL", pid, k.escalationTimeout)
			if err := k.sender.Send(pid, syscall.SIGKILL); err != nil {
				log.Printf("[kill-service] SIGKILL to PID %d failed: %v", pid, err)
				// Don't return error — the process may have just exited.
			}
			return nil
		case <-ticker.C:
			if k.tracker.Get(pid) == nil {
				log.Printf("[kill-service] PID %d exited after SIGTERM", pid)
				return nil
			}
		}
	}
}

// Shutdown cancels all pending SIGTERM escalation timers and immediately
// sends SIGKILL to every process still tracked as running. This is called
// during graceful server shutdown to ensure no agent subprocesses outlive
// the server.
func (k *KillService) Shutdown(ctx context.Context) error {
	// Cancel all pending escalation timers so Kill goroutines unblock.
	k.mu.Lock()
	for pid, cancel := range k.pendingKills {
		log.Printf("[kill-service] shutdown: cancelling pending escalation for PID %d", pid)
		cancel()
	}
	k.pendingKills = make(map[int]context.CancelFunc)
	k.mu.Unlock()

	// Send SIGKILL to all processes still in the tracker's runtime map.
	records := k.tracker.GetAll()
	var lastErr error
	for _, rec := range records {
		if rec.Status != StatusRunning {
			continue
		}
		log.Printf("[kill-service] shutdown: sending SIGKILL to PID %d (feature=%s, role=%s)", rec.PID, rec.Feature, rec.Role)
		if err := k.sender.Send(rec.PID, syscall.SIGKILL); err != nil {
			log.Printf("[kill-service] shutdown: SIGKILL to PID %d failed: %v", rec.PID, err)
			lastErr = err
		}
	}
	return lastErr
}
