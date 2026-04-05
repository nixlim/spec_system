package process

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	// maxWriteRetries is the number of attempts for transient SQLite errors.
	maxWriteRetries = 3
	// baseRetryDelay is the initial backoff delay between retries.
	baseRetryDelay = 50 * time.Millisecond
	// maxRetryDelay caps exponential backoff.
	maxRetryDelay = 500 * time.Millisecond
)

// SQLiteStore implements ProcessStore backed by a SQLite database.
type SQLiteStore struct {
	db *sql.DB
	mu sync.Mutex // serialises writes to avoid SQLite busy errors
}

// NewSQLiteStore creates a new SQLiteStore using the provided database
// connection and ensures the process_records table exists.
func NewSQLiteStore(db *sql.DB) (*SQLiteStore, error) {
	s := &SQLiteStore{db: db}
	if err := s.createSchema(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *SQLiteStore) createSchema() error {
	const schema = `
	CREATE TABLE IF NOT EXISTS process_records (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		pid        INTEGER NOT NULL,
		feature    TEXT NOT NULL,
		role       TEXT NOT NULL,
		status     TEXT NOT NULL DEFAULT 'running',
		started_at TEXT NOT NULL,
		ended_at   TEXT,
		exit_code  INTEGER
	);

	CREATE UNIQUE INDEX IF NOT EXISTS idx_process_records_pid_started
		ON process_records(pid, started_at);
	CREATE INDEX IF NOT EXISTS idx_process_records_feature
		ON process_records(feature);
	CREATE INDEX IF NOT EXISTS idx_process_records_status
		ON process_records(status);
	`
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("create process_records schema: %w", err)
	}
	return nil
}

// isSQLiteTransient reports whether err is a transient SQLite error
// (SQLITE_BUSY or SQLITE_LOCKED) that may succeed on retry.
func isSQLiteTransient(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "database table is locked") ||
		strings.Contains(msg, "SQLITE_BUSY") ||
		strings.Contains(msg, "SQLITE_LOCKED")
}

// retryWrite executes fn up to maxWriteRetries times with exponential backoff
// for transient SQLite errors.
func retryWrite(fn func() error) error {
	var err error
	delay := baseRetryDelay
	for attempt := 0; attempt < maxWriteRetries; attempt++ {
		err = fn()
		if err == nil || !isSQLiteTransient(err) {
			return err
		}
		time.Sleep(delay)
		delay *= 2
		if delay > maxRetryDelay {
			delay = maxRetryDelay
		}
	}
	return err
}

// Save inserts or upserts a ProcessRecord.
func (s *SQLiteStore) Save(record ProcessRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var endedAt *string
	if record.EndedAt != nil {
		v := record.EndedAt.Format(time.RFC3339)
		endedAt = &v
	}

	return retryWrite(func() error {
		_, err := s.db.Exec(`
			INSERT INTO process_records (pid, feature, role, status, started_at, ended_at, exit_code)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(pid, started_at) DO UPDATE SET
				feature    = excluded.feature,
				role       = excluded.role,
				status     = excluded.status,
				ended_at   = excluded.ended_at,
				exit_code  = excluded.exit_code
		`, record.PID, record.Feature, record.Role, record.Status,
			record.StartedAt.Format(time.RFC3339), endedAt, record.ExitCode)
		if err != nil {
			return fmt.Errorf("save process record: %w", err)
		}
		return nil
	})
}

// UpdateEnd sets the ended_at, exit_code, and status for a given PID.
func (s *SQLiteStore) UpdateEnd(pid int, status string, exitCode int, endedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return retryWrite(func() error {
		result, err := s.db.Exec(`
			UPDATE process_records
			SET status = ?, exit_code = ?, ended_at = ?
			WHERE pid = ? AND status = ?
		`, status, exitCode, endedAt.Format(time.RFC3339), pid, StatusRunning)
		if err != nil {
			return fmt.Errorf("update process end: %w", err)
		}
		n, _ := result.RowsAffected()
		if n == 0 {
			return fmt.Errorf("no running process record found for pid %d", pid)
		}
		return nil
	})
}

// MarkLostOnStartup atomically transitions all "running" records to "lost"
// and returns them so the caller can emit ProcessLostEvent for each.
func (s *SQLiteStore) MarkLostOnStartup(ctx context.Context) ([]ProcessRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // rollback after commit is a no-op

	rows, err := tx.QueryContext(ctx, `
		SELECT pid, feature, role, started_at
		FROM process_records
		WHERE status = ?
	`, StatusRunning)
	if err != nil {
		return nil, fmt.Errorf("query running processes: %w", err)
	}
	defer rows.Close()

	var lost []ProcessRecord
	for rows.Next() {
		var r ProcessRecord
		var startedAt string
		if err := rows.Scan(&r.PID, &r.Feature, &r.Role, &startedAt); err != nil {
			return nil, fmt.Errorf("scan process record: %w", err)
		}
		r.StartedAt, _ = time.Parse(time.RFC3339, startedAt)
		r.Status = StatusLost
		lost = append(lost, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate running processes: %w", err)
	}

	if len(lost) > 0 {
		_, err = tx.ExecContext(ctx, `
			UPDATE process_records SET status = ? WHERE status = ?
		`, StatusLost, StatusRunning)
		if err != nil {
			return nil, fmt.Errorf("mark lost on startup: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit mark-lost transaction: %w", err)
	}
	return lost, nil
}

// List returns process records matching the supplied filters. Empty strings
// and zero limit are treated as "no filter".
func (s *SQLiteStore) List(feature, role, status string, limit int) ([]ProcessRecord, error) {
	var (
		clauses []string
		args    []any
	)
	if feature != "" {
		clauses = append(clauses, "feature = ?")
		args = append(args, feature)
	}
	if role != "" {
		clauses = append(clauses, "role = ?")
		args = append(args, role)
	}
	if status != "" {
		clauses = append(clauses, "status = ?")
		args = append(args, status)
	}

	query := "SELECT pid, feature, role, status, started_at, ended_at, exit_code FROM process_records"
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += " ORDER BY started_at DESC"
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list process records: %w", err)
	}
	defer rows.Close()

	var records []ProcessRecord
	for rows.Next() {
		var (
			r         ProcessRecord
			startedAt string
			endedAt   sql.NullString
			exitCode  sql.NullInt64
		)
		if err := rows.Scan(&r.PID, &r.Feature, &r.Role, &r.Status,
			&startedAt, &endedAt, &exitCode); err != nil {
			return nil, fmt.Errorf("scan process record: %w", err)
		}
		r.StartedAt, _ = time.Parse(time.RFC3339, startedAt)
		if endedAt.Valid {
			t, _ := time.Parse(time.RFC3339, endedAt.String)
			r.EndedAt = &t
		}
		if exitCode.Valid {
			v := int(exitCode.Int64)
			r.ExitCode = &v
		}
		records = append(records, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate process records: %w", err)
	}
	return records, nil
}
