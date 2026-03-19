package api

import (
	"os"
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *MetricsStore {
	t.Helper()
	dir := t.TempDir()
	store, err := NewMetricsStore(filepath.Join(dir, "test-metrics.db"))
	if err != nil {
		t.Fatalf("NewMetricsStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// ---------------------------------------------------------------------------
// Workflow Metrics CRUD
// ---------------------------------------------------------------------------

func TestUpsertAndGetWorkflowMetrics(t *testing.T) {
	store := newTestStore(t)

	// Get nonexistent returns nil.
	m, err := store.GetWorkflowMetrics("no-such-feature")
	if err != nil {
		t.Fatalf("GetWorkflowMetrics: %v", err)
	}
	if m != nil {
		t.Fatalf("expected nil, got %+v", m)
	}

	// Insert.
	if err := store.UpsertWorkflowMetrics(WorkflowMetrics{
		FeatureName:     "feat-1",
		InputTokens:     1000,
		OutputTokens:    200,
		CacheReadTokens: 5000,
		TotalCostUSD:    1.50,
		TotalAPICalls:   10,
		StartedAt:       "2025-01-01T00:00:00Z",
		UpdatedAt:       "2025-01-01T00:01:00Z",
	}); err != nil {
		t.Fatalf("UpsertWorkflowMetrics (insert): %v", err)
	}

	m, err = store.GetWorkflowMetrics("feat-1")
	if err != nil {
		t.Fatalf("GetWorkflowMetrics: %v", err)
	}
	if m == nil {
		t.Fatal("expected metrics, got nil")
	}
	if m.InputTokens != 1000 {
		t.Errorf("InputTokens: got %d, want 1000", m.InputTokens)
	}
	if m.TotalCostUSD != 1.50 {
		t.Errorf("TotalCostUSD: got %f, want 1.50", m.TotalCostUSD)
	}
	if m.TotalAPICalls != 10 {
		t.Errorf("TotalAPICalls: got %d, want 10", m.TotalAPICalls)
	}

	// Upsert (update).
	if err := store.UpsertWorkflowMetrics(WorkflowMetrics{
		FeatureName:     "feat-1",
		InputTokens:     2000,
		OutputTokens:    400,
		CacheReadTokens: 10000,
		TotalCostUSD:    3.00,
		TotalAPICalls:   20,
		UpdatedAt:       "2025-01-01T00:02:00Z",
	}); err != nil {
		t.Fatalf("UpsertWorkflowMetrics (update): %v", err)
	}

	m, err = store.GetWorkflowMetrics("feat-1")
	if err != nil {
		t.Fatalf("GetWorkflowMetrics: %v", err)
	}
	if m.InputTokens != 2000 {
		t.Errorf("InputTokens after update: got %d, want 2000", m.InputTokens)
	}
	if m.TotalCostUSD != 3.00 {
		t.Errorf("TotalCostUSD after update: got %f, want 3.00", m.TotalCostUSD)
	}
	// StartedAt should be preserved from original insert (not overwritten by empty).
	if m.StartedAt != "2025-01-01T00:00:00Z" {
		t.Errorf("StartedAt after update: got %q, want '2025-01-01T00:00:00Z'", m.StartedAt)
	}
}

// ---------------------------------------------------------------------------
// Events CRUD
// ---------------------------------------------------------------------------

func TestRecordAndGetEvents(t *testing.T) {
	store := newTestStore(t)

	// Empty result.
	events, err := store.GetEvents("feat-1")
	if err != nil {
		t.Fatalf("GetEvents (empty): %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events, got %d", len(events))
	}

	// Record tool event.
	if err := store.RecordEvent(MetricEvent{
		FeatureName: "feat-1",
		EventType:   "tool",
		ToolName:    "Read",
		Success:     true,
		DurationMS:  12.5,
		Timestamp:   "2025-01-01T00:00:01Z",
	}); err != nil {
		t.Fatalf("RecordEvent (tool): %v", err)
	}

	// Record API event.
	if err := store.RecordEvent(MetricEvent{
		FeatureName: "feat-1",
		EventType:   "api",
		Model:       "claude-opus-4-6",
		Success:     true,
		DurationMS:  3814,
		CostUSD:     0.06,
		Timestamp:   "2025-01-01T00:00:02Z",
	}); err != nil {
		t.Fatalf("RecordEvent (api): %v", err)
	}

	events, err = store.GetEvents("feat-1")
	if err != nil {
		t.Fatalf("GetEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	// Events should be newest first.
	if events[0].EventType != "api" {
		t.Errorf("newest event type: got %q, want 'api'", events[0].EventType)
	}
	if events[0].Model != "claude-opus-4-6" {
		t.Errorf("newest event model: got %q, want 'claude-opus-4-6'", events[0].Model)
	}
	if events[1].EventType != "tool" {
		t.Errorf("oldest event type: got %q, want 'tool'", events[1].EventType)
	}
	if events[1].ToolName != "Read" {
		t.Errorf("oldest event tool_name: got %q, want 'Read'", events[1].ToolName)
	}

	// Events for other features should be empty.
	events2, err := store.GetEvents("other-feat")
	if err != nil {
		t.Fatalf("GetEvents (other): %v", err)
	}
	if len(events2) != 0 {
		t.Errorf("expected 0 events for other feature, got %d", len(events2))
	}
}

// ---------------------------------------------------------------------------
// GetCurrentCostUSD
// ---------------------------------------------------------------------------

func TestGetCurrentCostUSD(t *testing.T) {
	store := newTestStore(t)

	// No data returns 0.
	if cost := store.GetCurrentCostUSD("feat-1"); cost != 0 {
		t.Errorf("expected 0, got %f", cost)
	}

	store.UpsertWorkflowMetrics(WorkflowMetrics{
		FeatureName:  "feat-1",
		TotalCostUSD: 18.76,
		UpdatedAt:    "2025-01-01T00:00:00Z",
	})

	if cost := store.GetCurrentCostUSD("feat-1"); cost != 18.76 {
		t.Errorf("expected 18.76, got %f", cost)
	}
}

// ---------------------------------------------------------------------------
// MetricsCostProvider
// ---------------------------------------------------------------------------

func TestMetricsCostProvider(t *testing.T) {
	store := newTestStore(t)
	provider := NewMetricsCostProvider(store, "feat-1")

	if cost := provider.GetCostUSD(); cost != 0 {
		t.Errorf("expected 0, got %f", cost)
	}

	store.UpsertWorkflowMetrics(WorkflowMetrics{
		FeatureName:  "feat-1",
		TotalCostUSD: 5.25,
		UpdatedAt:    "2025-01-01T00:00:00Z",
	})

	if cost := provider.GetCostUSD(); cost != 5.25 {
		t.Errorf("expected 5.25, got %f", cost)
	}
}

// ---------------------------------------------------------------------------
// ResetForFeature
// ---------------------------------------------------------------------------

func TestResetForFeature(t *testing.T) {
	store := newTestStore(t)

	// Insert metrics and events.
	store.UpsertWorkflowMetrics(WorkflowMetrics{
		FeatureName:  "feat-1",
		TotalCostUSD: 10.0,
		UpdatedAt:    "2025-01-01T00:00:00Z",
	})
	store.RecordEvent(MetricEvent{
		FeatureName: "feat-1",
		EventType:   "tool",
		ToolName:    "Read",
		Timestamp:   "2025-01-01T00:00:00Z",
	})
	store.RecordEvent(MetricEvent{
		FeatureName: "feat-1",
		EventType:   "api",
		Model:       "opus",
		Timestamp:   "2025-01-01T00:00:01Z",
	})

	// Also insert for another feature to verify isolation.
	store.UpsertWorkflowMetrics(WorkflowMetrics{
		FeatureName:  "feat-2",
		TotalCostUSD: 5.0,
		UpdatedAt:    "2025-01-01T00:00:00Z",
	})

	// Reset feat-1.
	if err := store.ResetForFeature("feat-1"); err != nil {
		t.Fatalf("ResetForFeature: %v", err)
	}

	// feat-1 should be gone.
	m, _ := store.GetWorkflowMetrics("feat-1")
	if m != nil {
		t.Error("expected nil metrics after reset")
	}
	events, _ := store.GetEvents("feat-1")
	if len(events) != 0 {
		t.Errorf("expected 0 events after reset, got %d", len(events))
	}

	// feat-2 should be untouched.
	m2, _ := store.GetWorkflowMetrics("feat-2")
	if m2 == nil || m2.TotalCostUSD != 5.0 {
		t.Error("feat-2 should not be affected by feat-1 reset")
	}
}

// ---------------------------------------------------------------------------
// Per-workflow SQLite persistence
// ---------------------------------------------------------------------------

func TestPerWorkflowSQLitePersistence(t *testing.T) {
	store := newTestStore(t)

	// Upsert metrics for two independent workflows.
	if err := store.UpsertWorkflowMetrics(WorkflowMetrics{
		FeatureName:     "alpha",
		InputTokens:     5000,
		OutputTokens:    1000,
		CacheReadTokens: 2000,
		TotalCostUSD:    5.00,
		TotalAPICalls:   25,
		StartedAt:       "2025-06-01T00:00:00Z",
		UpdatedAt:       "2025-06-01T00:05:00Z",
	}); err != nil {
		t.Fatalf("UpsertWorkflowMetrics(alpha): %v", err)
	}

	if err := store.UpsertWorkflowMetrics(WorkflowMetrics{
		FeatureName:     "beta",
		InputTokens:     3000,
		OutputTokens:    600,
		CacheReadTokens: 1000,
		TotalCostUSD:    3.00,
		TotalAPICalls:   15,
		StartedAt:       "2025-06-01T00:10:00Z",
		UpdatedAt:       "2025-06-01T00:15:00Z",
	}); err != nil {
		t.Fatalf("UpsertWorkflowMetrics(beta): %v", err)
	}

	// Verify alpha independently.
	alpha, err := store.GetWorkflowMetrics("alpha")
	if err != nil {
		t.Fatalf("GetWorkflowMetrics(alpha): %v", err)
	}
	if alpha == nil {
		t.Fatal("expected alpha metrics, got nil")
	}
	if alpha.TotalCostUSD != 5.00 {
		t.Errorf("alpha cost: got %f, want 5.00", alpha.TotalCostUSD)
	}
	if alpha.InputTokens != 5000 {
		t.Errorf("alpha input tokens: got %d, want 5000", alpha.InputTokens)
	}
	if alpha.TotalAPICalls != 25 {
		t.Errorf("alpha api calls: got %d, want 25", alpha.TotalAPICalls)
	}

	// Verify beta independently.
	beta, err := store.GetWorkflowMetrics("beta")
	if err != nil {
		t.Fatalf("GetWorkflowMetrics(beta): %v", err)
	}
	if beta == nil {
		t.Fatal("expected beta metrics, got nil")
	}
	if beta.TotalCostUSD != 3.00 {
		t.Errorf("beta cost: got %f, want 3.00", beta.TotalCostUSD)
	}
	if beta.InputTokens != 3000 {
		t.Errorf("beta input tokens: got %d, want 3000", beta.InputTokens)
	}
	if beta.TotalAPICalls != 15 {
		t.Errorf("beta api calls: got %d, want 15", beta.TotalAPICalls)
	}

	// Reset beta — alpha must survive.
	if err := store.ResetForFeature("beta"); err != nil {
		t.Fatalf("ResetForFeature(beta): %v", err)
	}

	betaAfter, _ := store.GetWorkflowMetrics("beta")
	if betaAfter != nil {
		t.Error("expected nil beta metrics after reset")
	}

	alphaAfter, _ := store.GetWorkflowMetrics("alpha")
	if alphaAfter == nil {
		t.Fatal("alpha should survive beta reset")
	}
	if alphaAfter.TotalCostUSD != 5.00 {
		t.Errorf("alpha cost after beta reset: got %f, want 5.00", alphaAfter.TotalCostUSD)
	}
}

// ---------------------------------------------------------------------------
// GetAllWorkflowMetrics
// ---------------------------------------------------------------------------

func TestGetAllWorkflowMetrics(t *testing.T) {
	store := newTestStore(t)

	// Empty table returns empty slice.
	all, err := store.GetAllWorkflowMetrics()
	if err != nil {
		t.Fatalf("GetAllWorkflowMetrics (empty): %v", err)
	}
	if len(all) != 0 {
		t.Errorf("expected 0 rows, got %d", len(all))
	}

	// Insert two workflows.
	if err := store.UpsertWorkflowMetrics(WorkflowMetrics{
		FeatureName:     "alpha",
		InputTokens:     5000,
		OutputTokens:    1000,
		CacheReadTokens: 2000,
		TotalCostUSD:    5.00,
		TotalAPICalls:   25,
		StartedAt:       "2025-06-01T00:00:00Z",
		UpdatedAt:       "2025-06-01T00:05:00Z",
	}); err != nil {
		t.Fatalf("Upsert(alpha): %v", err)
	}

	if err := store.UpsertWorkflowMetrics(WorkflowMetrics{
		FeatureName:     "beta",
		InputTokens:     3000,
		OutputTokens:    600,
		CacheReadTokens: 1000,
		TotalCostUSD:    3.00,
		TotalAPICalls:   15,
		StartedAt:       "2025-06-01T00:10:00Z",
		UpdatedAt:       "2025-06-01T00:15:00Z",
	}); err != nil {
		t.Fatalf("Upsert(beta): %v", err)
	}

	all, err = store.GetAllWorkflowMetrics()
	if err != nil {
		t.Fatalf("GetAllWorkflowMetrics: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(all))
	}

	// Build a lookup map for order-independent assertions.
	byName := make(map[string]WorkflowMetrics)
	for _, m := range all {
		byName[m.FeatureName] = m
	}

	alphaM, ok := byName["alpha"]
	if !ok {
		t.Fatal("alpha not found in GetAllWorkflowMetrics result")
	}
	if alphaM.TotalCostUSD != 5.00 {
		t.Errorf("alpha cost: got %f, want 5.00", alphaM.TotalCostUSD)
	}
	if alphaM.InputTokens != 5000 {
		t.Errorf("alpha input tokens: got %d, want 5000", alphaM.InputTokens)
	}

	betaM, ok := byName["beta"]
	if !ok {
		t.Fatal("beta not found in GetAllWorkflowMetrics result")
	}
	if betaM.TotalCostUSD != 3.00 {
		t.Errorf("beta cost: got %f, want 3.00", betaM.TotalCostUSD)
	}
	if betaM.InputTokens != 3000 {
		t.Errorf("beta input tokens: got %d, want 3000", betaM.InputTokens)
	}
}

// ---------------------------------------------------------------------------
// Database file creation
// ---------------------------------------------------------------------------

func TestMetricsStoreCreatesFile(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "subdir", "metrics.db")

	// The directory doesn't exist yet — SQLite should create it.
	// Actually, modernc sqlite won't create parent dirs, so let's use
	// an existing directory.
	dbPath = filepath.Join(dir, "metrics.db")
	store, err := NewMetricsStore(dbPath)
	if err != nil {
		t.Fatalf("NewMetricsStore: %v", err)
	}
	defer store.Close()

	// Verify the file exists.
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error("expected database file to exist")
	}
}

// ---------------------------------------------------------------------------
// Success field round-trip
// ---------------------------------------------------------------------------

func TestEventSuccessRoundTrip(t *testing.T) {
	store := newTestStore(t)

	// Record a failed event.
	store.RecordEvent(MetricEvent{
		FeatureName: "feat-1",
		EventType:   "tool",
		ToolName:    "Write",
		Success:     false,
		DurationMS:  5,
		Timestamp:   "2025-01-01T00:00:00Z",
	})

	events, err := store.GetEvents("feat-1")
	if err != nil {
		t.Fatalf("GetEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Success {
		t.Error("expected Success=false, got true")
	}
}
