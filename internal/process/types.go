// Package process provides types and interfaces for tracking agent subprocess
// lifecycles — start, end, kill, and lost-on-restart detection.
package process

import "time"

// ---------------------------------------------------------------------------
// Status constants
// ---------------------------------------------------------------------------

const (
	StatusRunning = "running"
	StatusExited  = "exited"
	StatusKilled  = "killed"
	StatusLost    = "lost"
)

// ---------------------------------------------------------------------------
// ProcessRecord — persistent row in the process store
// ---------------------------------------------------------------------------

// ProcessRecord represents a single agent subprocess tracked by the system.
type ProcessRecord struct {
	Feature   string     `json:"feature"`
	Role      string     `json:"role"`
	PID       int        `json:"pid"`
	Status    string     `json:"status"`
	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	ExitCode  *int       `json:"exit_code,omitempty"`
}

// ---------------------------------------------------------------------------
// WebSocket event types
// ---------------------------------------------------------------------------

// ProcessStartedEvent is broadcast when an agent subprocess launches.
type ProcessStartedEvent struct {
	Type      string `json:"type"`
	Feature   string `json:"feature"`
	Role      string `json:"role"`
	PID       int    `json:"pid"`
	StartedAt string `json:"started_at"` // RFC 3339
}

// ProcessEndedEvent is broadcast when an agent subprocess terminates.
type ProcessEndedEvent struct {
	Type     string `json:"type"`
	PID      int    `json:"pid"`
	ExitCode int    `json:"exit_code"`
	Status   string `json:"status"`
	EndedAt  string `json:"ended_at"` // RFC 3339
}

// ProcessLostEvent is broadcast on server startup for subprocesses that were
// still marked "running" in the store (i.e. the server crashed or was killed).
type ProcessLostEvent struct {
	Type    string `json:"type"`
	PID     int    `json:"pid"`
	Feature string `json:"feature"`
	Role    string `json:"role"`
}
