// Package api provides HTTP handlers for the adversarial spec system.
// This file implements a SQLite-backed metrics store that persists OTEL
// telemetry data (aggregate counters and individual tool/API events) so
// dashboard metrics survive browser refreshes and server restarts. Data
// is retained for 90 days and cleaned up on startup.
package api

import (
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"

	// Pure-Go SQLite driver — no CGO required.
	_ "modernc.org/sqlite"
)

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const (
	// metricsRetentionDays is how long metric events are kept before cleanup.
	metricsRetentionDays = 90

	// maxEventsPerQuery caps the number of events returned in a single query
	// to prevent unbounded memory usage.
	maxEventsPerQuery = 500
)

// ---------------------------------------------------------------------------
// MetricsStore
// ---------------------------------------------------------------------------

// MetricsStore persists OTEL telemetry data in a SQLite database. It stores
// aggregate metric counters per workflow run (feature_name) and individual
// tool/API events for the activity feed. Thread-safe for concurrent reads
// and writes.
type MetricsStore struct {
	db *sql.DB
	mu sync.Mutex // serialises writes to avoid SQLite busy errors
}

// NewMetricsStore opens (or creates) a SQLite database at the given path
// and initialises the schema. It runs 90-day cleanup on startup.
func NewMetricsStore(dbPath string) (*MetricsStore, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open metrics db: %w", err)
	}

	// Enable WAL mode for better concurrent read/write performance.
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set WAL mode: %w", err)
	}

	// Reduce sync for better write performance — acceptable for metrics
	// data that can tolerate loss of the last few seconds on crash.
	if _, err := db.Exec("PRAGMA synchronous=NORMAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set synchronous: %w", err)
	}

	store := &MetricsStore{db: db}

	if err := store.createSchema(); err != nil {
		db.Close()
		return nil, err
	}

	if err := store.cleanup(); err != nil {
		log.Printf("[metrics-store] cleanup warning: %v", err)
	}

	return store, nil
}

// Close closes the underlying database connection.
func (s *MetricsStore) Close() error {
	return s.db.Close()
}

// ---------------------------------------------------------------------------
// Schema
// ---------------------------------------------------------------------------

func (s *MetricsStore) createSchema() error {
	const schema = `
	CREATE TABLE IF NOT EXISTS workflow_metrics (
		feature_name    TEXT PRIMARY KEY,
		input_tokens    INTEGER NOT NULL DEFAULT 0,
		output_tokens   INTEGER NOT NULL DEFAULT 0,
		cache_read_tokens INTEGER NOT NULL DEFAULT 0,
		total_cost_usd  REAL NOT NULL DEFAULT 0,
		total_api_calls INTEGER NOT NULL DEFAULT 0,
		started_at      TEXT NOT NULL DEFAULT '',
		updated_at      TEXT NOT NULL DEFAULT ''
	);

	CREATE TABLE IF NOT EXISTS metric_events (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		feature_name    TEXT NOT NULL,
		event_type      TEXT NOT NULL,
		tool_name       TEXT NOT NULL DEFAULT '',
		model           TEXT NOT NULL DEFAULT '',
		success         INTEGER NOT NULL DEFAULT 1,
		duration_ms     REAL NOT NULL DEFAULT 0,
		cost_usd        REAL NOT NULL DEFAULT 0,
		timestamp       TEXT NOT NULL,
		created_at      TEXT NOT NULL DEFAULT (datetime('now'))
	);

	CREATE INDEX IF NOT EXISTS idx_metric_events_feature
		ON metric_events(feature_name);
	CREATE INDEX IF NOT EXISTS idx_metric_events_created
		ON metric_events(created_at);
	`
	_, err := s.db.Exec(schema)
	if err != nil {
		return fmt.Errorf("create metrics schema: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Write operations
// ---------------------------------------------------------------------------

// WorkflowMetrics represents the aggregate counters for a workflow run.
type WorkflowMetrics struct {
	FeatureName     string  `json:"feature_name"`
	InputTokens     int64   `json:"input_tokens"`
	OutputTokens    int64   `json:"output_tokens"`
	CacheReadTokens int64   `json:"cache_read_tokens"`
	TotalCostUSD    float64 `json:"total_cost_usd"`
	TotalAPICalls   int     `json:"total_api_calls"`
	StartedAt       string  `json:"started_at"`
	UpdatedAt       string  `json:"updated_at"`
}

// UpsertWorkflowMetrics inserts or replaces the aggregate counters for a
// workflow run identified by feature name. This is called on every OTEL
// metrics update.
func (s *MetricsStore) UpsertWorkflowMetrics(m WorkflowMetrics) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	const query = `
	INSERT INTO workflow_metrics (
		feature_name, input_tokens, output_tokens, cache_read_tokens,
		total_cost_usd, total_api_calls, started_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(feature_name) DO UPDATE SET
		input_tokens = excluded.input_tokens,
		output_tokens = excluded.output_tokens,
		cache_read_tokens = excluded.cache_read_tokens,
		total_cost_usd = excluded.total_cost_usd,
		total_api_calls = excluded.total_api_calls,
		started_at = CASE WHEN excluded.started_at != '' THEN excluded.started_at ELSE workflow_metrics.started_at END,
		updated_at = excluded.updated_at
	`
	_, err := s.db.Exec(query,
		m.FeatureName, m.InputTokens, m.OutputTokens, m.CacheReadTokens,
		m.TotalCostUSD, m.TotalAPICalls, m.StartedAt, m.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("upsert workflow metrics: %w", err)
	}
	return nil
}

// MetricEvent represents a single tool or API event for the activity feed.
type MetricEvent struct {
	ID          int64   `json:"id,omitempty"`
	FeatureName string  `json:"feature_name"`
	EventType   string  `json:"event_type"` // "tool" or "api"
	ToolName    string  `json:"tool_name,omitempty"`
	Model       string  `json:"model,omitempty"`
	Success     bool    `json:"success"`
	DurationMS  float64 `json:"duration_ms"`
	CostUSD     float64 `json:"cost_usd,omitempty"`
	Timestamp   string  `json:"timestamp"`
}

// RecordEvent persists a single tool or API event.
func (s *MetricsStore) RecordEvent(e MetricEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	const query = `
	INSERT INTO metric_events (
		feature_name, event_type, tool_name, model, success,
		duration_ms, cost_usd, timestamp
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`
	successInt := 0
	if e.Success {
		successInt = 1
	}
	_, err := s.db.Exec(query,
		e.FeatureName, e.EventType, e.ToolName, e.Model, successInt,
		e.DurationMS, e.CostUSD, e.Timestamp,
	)
	if err != nil {
		return fmt.Errorf("record metric event: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Read operations
// ---------------------------------------------------------------------------

// GetWorkflowMetrics returns the aggregate counters for the given feature.
// Returns nil if no metrics exist for the feature.
func (s *MetricsStore) GetWorkflowMetrics(featureName string) (*WorkflowMetrics, error) {
	const query = `
	SELECT feature_name, input_tokens, output_tokens, cache_read_tokens,
	       total_cost_usd, total_api_calls, started_at, updated_at
	FROM workflow_metrics WHERE feature_name = ?
	`
	var m WorkflowMetrics
	err := s.db.QueryRow(query, featureName).Scan(
		&m.FeatureName, &m.InputTokens, &m.OutputTokens, &m.CacheReadTokens,
		&m.TotalCostUSD, &m.TotalAPICalls, &m.StartedAt, &m.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get workflow metrics: %w", err)
	}
	return &m, nil
}

// GetCurrentCostUSD returns the persisted cumulative cost for the given
// feature. Returns 0 if no metrics exist.
func (s *MetricsStore) GetCurrentCostUSD(featureName string) float64 {
	m, err := s.GetWorkflowMetrics(featureName)
	if err != nil || m == nil {
		return 0
	}
	return m.TotalCostUSD
}

// GetEvents returns the most recent events for the given feature, up to
// maxEventsPerQuery. Events are ordered newest-first.
func (s *MetricsStore) GetEvents(featureName string) ([]MetricEvent, error) {
	const query = `
	SELECT id, feature_name, event_type, tool_name, model, success,
	       duration_ms, cost_usd, timestamp
	FROM metric_events
	WHERE feature_name = ?
	ORDER BY id DESC
	LIMIT ?
	`
	rows, err := s.db.Query(query, featureName, maxEventsPerQuery)
	if err != nil {
		return nil, fmt.Errorf("get metric events: %w", err)
	}
	defer rows.Close()

	var events []MetricEvent
	for rows.Next() {
		var e MetricEvent
		var successInt int
		if err := rows.Scan(
			&e.ID, &e.FeatureName, &e.EventType, &e.ToolName, &e.Model,
			&successInt, &e.DurationMS, &e.CostUSD, &e.Timestamp,
		); err != nil {
			return nil, fmt.Errorf("scan metric event: %w", err)
		}
		e.Success = successInt == 1
		events = append(events, e)
	}
	return events, rows.Err()
}

// ---------------------------------------------------------------------------
// CostProvider interface (specworkflow.CostProvider)
// ---------------------------------------------------------------------------

// MetricsCostProvider wraps a MetricsStore to implement specworkflow.CostProvider
// for a specific feature. The orchestrator uses this to read persisted cost.
type MetricsCostProvider struct {
	store       *MetricsStore
	featureName string
}

// NewMetricsCostProvider creates a CostProvider backed by the metrics store.
func NewMetricsCostProvider(store *MetricsStore, featureName string) *MetricsCostProvider {
	return &MetricsCostProvider{store: store, featureName: featureName}
}

// GetCostUSD returns the cumulative cost from the persisted metrics.
func (p *MetricsCostProvider) GetCostUSD() float64 {
	return p.store.GetCurrentCostUSD(p.featureName)
}

// ---------------------------------------------------------------------------
// Maintenance
// ---------------------------------------------------------------------------

// cleanup removes metric events older than metricsRetentionDays and
// workflow_metrics rows that haven't been updated in that period.
func (s *MetricsStore) cleanup() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().AddDate(0, 0, -metricsRetentionDays).Format(time.RFC3339)

	res, err := s.db.Exec(
		"DELETE FROM metric_events WHERE created_at < ?", cutoff)
	if err != nil {
		return fmt.Errorf("cleanup events: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		log.Printf("[metrics-store] cleaned up %d old events", n)
	}

	res, err = s.db.Exec(
		"DELETE FROM workflow_metrics WHERE updated_at < ? AND updated_at != ''", cutoff)
	if err != nil {
		return fmt.Errorf("cleanup metrics: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		log.Printf("[metrics-store] cleaned up %d old workflow metrics", n)
	}

	return nil
}

// ResetForFeature clears all metrics and events for a specific feature.
// Used when starting a new workflow run for the same feature.
func (s *MetricsStore) ResetForFeature(featureName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.db.Exec("DELETE FROM workflow_metrics WHERE feature_name = ?", featureName); err != nil {
		return fmt.Errorf("reset workflow metrics: %w", err)
	}
	if _, err := s.db.Exec("DELETE FROM metric_events WHERE feature_name = ?", featureName); err != nil {
		return fmt.Errorf("reset metric events: %w", err)
	}
	return nil
}
