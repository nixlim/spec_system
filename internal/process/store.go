package process

import (
	"context"
	"time"
)

// ProcessStore persists agent subprocess records across server restarts.
type ProcessStore interface {
	// Save inserts or upserts a ProcessRecord.
	Save(record ProcessRecord) error

	// UpdateEnd sets the ended_at, exit_code, and status for a given PID.
	UpdateEnd(pid int, status string, exitCode int, endedAt time.Time) error

	// MarkLostOnStartup transitions all "running" records to "lost" and
	// returns them so the caller can emit ProcessLostEvent for each.
	MarkLostOnStartup(ctx context.Context) ([]ProcessRecord, error)

	// List returns process records matching the supplied filters.
	// Empty strings and zero limit are treated as "no filter".
	List(feature, role, status string, limit int) ([]ProcessRecord, error)
}
