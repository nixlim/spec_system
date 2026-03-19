package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/foundry-zero/adversarial-spec-system/internal/specworkflow"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func setupWorkflowManager(t *testing.T) (*WorkflowManager, string) {
	t.Helper()
	dir := t.TempDir()
	emitter := specworkflow.NewChannelEmitter(64)
	hub := NewWebSocketHub()
	config := specworkflow.DefaultConfig()
	// Clear skill paths to avoid file-not-found validation errors.
	config.SkillPaths = specworkflow.SkillPaths{}

	return NewWorkflowManager(emitter, hub, dir, config), dir
}

// ---------------------------------------------------------------------------
// TestSanitizeFeatureName
// ---------------------------------------------------------------------------

func TestSanitizeFeatureName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"User Auth Flow", "user-auth-flow"},
		{"  Spaces  Around  ", "spaces-around"},
		{"UPPER_CASE_NAME", "upper-case-name"},
		{"special!@#chars$%^", "special-chars"},
		{"", ""},
		{"a", "a"},
		{"multiple---dashes", "multiple-dashes"},
	}

	for _, tt := range tests {
		got := sanitizeFeatureName(tt.input)
		if got != tt.want {
			t.Errorf("sanitizeFeatureName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// TestDiscoverSourceDocs
// ---------------------------------------------------------------------------

func TestDiscoverSourceDocs(t *testing.T) {
	dir := t.TempDir()
	docsDir := filepath.Join(dir, "source-docs")
	os.MkdirAll(docsDir, 0o755)

	os.WriteFile(filepath.Join(docsDir, "doc1.md"), []byte("# Doc 1"), 0o644)
	os.WriteFile(filepath.Join(docsDir, "doc2.txt"), []byte("Doc 2"), 0o644)

	paths := discoverSourceDocs(dir)
	if len(paths) != 2 {
		t.Fatalf("expected 2 paths, got %d: %v", len(paths), paths)
	}
}

func TestDiscoverSourceDocs_NoDir(t *testing.T) {
	paths := discoverSourceDocs("/nonexistent")
	if len(paths) != 0 {
		t.Errorf("expected 0 paths for missing dir, got %d", len(paths))
	}
}

// ---------------------------------------------------------------------------
// TestHasAnswerResolution
// ---------------------------------------------------------------------------

func TestHasAnswerResolution(t *testing.T) {
	noAnswer := []specworkflow.AmbiguityResolution{
		{WarningID: "W-1", Action: "accept"},
		{WarningID: "W-2", Action: "defer"},
	}
	if hasAnswerResolution(noAnswer) {
		t.Error("expected false for no answer resolutions")
	}

	withAnswer := []specworkflow.AmbiguityResolution{
		{WarningID: "W-1", Action: "accept"},
		{WarningID: "W-2", Action: "answer", Answer: "yes"},
	}
	if !hasAnswerResolution(withAnswer) {
		t.Error("expected true for resolutions with answer")
	}
}

// ---------------------------------------------------------------------------
// TestHandleStartWorkflow
// ---------------------------------------------------------------------------

func TestHandleStartWorkflow_MissingFeatureName(t *testing.T) {
	manager, _ := setupWorkflowManager(t)
	handler := HandleStartWorkflow(manager)

	body := `{"title":"","description":"test"}`
	req := httptest.NewRequest(http.MethodPost, "/api/workflow/start", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing feature name, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleStartWorkflow_InvalidJSON(t *testing.T) {
	manager, _ := setupWorkflowManager(t)
	handler := HandleStartWorkflow(manager)

	req := httptest.NewRequest(http.MethodPost, "/api/workflow/start", bytes.NewBufferString("not json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid JSON, got %d", rec.Code)
	}
}

func TestHandleStartWorkflow_MethodNotAllowed(t *testing.T) {
	manager, _ := setupWorkflowManager(t)
	handler := HandleStartWorkflow(manager)

	req := httptest.NewRequest(http.MethodGet, "/api/workflow/start", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestHandleStartWorkflow_FeatureNameFromTitle(t *testing.T) {
	manager, _ := setupWorkflowManager(t)
	handler := HandleStartWorkflow(manager)

	body := `{"title":"My Feature","description":"A test feature"}`
	req := httptest.NewRequest(http.MethodPost, "/api/workflow/start", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// This will try to create a ClaudeRunner and orchestrator.
	// It should return 202 (the workflow will fail in the background since
	// claude CLI isn't available, but the HTTP response is immediate).
	if rec.Code != http.StatusAccepted {
		t.Errorf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["feature_name"] != "my-feature" {
		t.Errorf("expected feature_name 'my-feature', got %v", resp["feature_name"])
	}
	if resp["status"] != "started" {
		t.Errorf("expected status 'started', got %v", resp["status"])
	}
	if resp["state"] != "INIT" {
		t.Errorf("expected state 'INIT', got %v", resp["state"])
	}
	if resp["round"] != float64(1) {
		t.Errorf("expected round 1, got %v", resp["round"])
	}

	// Wait for background goroutine to finish so TempDir cleanup succeeds.
	time.Sleep(100 * time.Millisecond)
}

// ---------------------------------------------------------------------------
// TestHandleGateApprove
// ---------------------------------------------------------------------------

func TestHandleGateApprove_NoOrchestrator(t *testing.T) {
	manager, _ := setupWorkflowManager(t)
	handler := HandleGateApprove(manager)

	body := `{"action":"confirm"}`
	req := httptest.NewRequest(http.MethodPost, "/api/tasks/test/approve", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 with no orchestrator, got %d", rec.Code)
	}
}

func TestHandleGateApprove_MethodNotAllowed(t *testing.T) {
	manager, _ := setupWorkflowManager(t)
	handler := HandleGateApprove(manager)

	req := httptest.NewRequest(http.MethodGet, "/api/tasks/test/approve", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// TestHandleGateReject
// ---------------------------------------------------------------------------

func TestHandleGateReject_NoOrchestrator(t *testing.T) {
	manager, _ := setupWorkflowManager(t)
	handler := HandleGateReject(manager)

	req := httptest.NewRequest(http.MethodPost, "/api/tasks/test/reject", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 with no orchestrator, got %d", rec.Code)
	}
}

func TestHandleGateReject_MethodNotAllowed(t *testing.T) {
	manager, _ := setupWorkflowManager(t)
	handler := HandleGateReject(manager)

	req := httptest.NewRequest(http.MethodGet, "/api/tasks/test/reject", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// TestHandleCancelWorkflowAPI
// ---------------------------------------------------------------------------

func TestHandleCancelWorkflowAPI_NoOrchestrator(t *testing.T) {
	manager, _ := setupWorkflowManager(t)
	handler := HandleCancelWorkflowAPI(manager)

	req := httptest.NewRequest(http.MethodPost, "/api/workflow/cancel", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("expected 409 with no orchestrator, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleCancelWorkflowAPI_MethodNotAllowed(t *testing.T) {
	manager, _ := setupWorkflowManager(t)
	handler := HandleCancelWorkflowAPI(manager)

	req := httptest.NewRequest(http.MethodGet, "/api/workflow/cancel", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// TestWorkflowManager_GetState_NoOrchestrator
// ---------------------------------------------------------------------------

func TestWorkflowManager_GetState_NoOrchestrator(t *testing.T) {
	manager, _ := setupWorkflowManager(t)

	state := manager.GetState()
	if state == nil {
		t.Fatal("expected non-nil default state")
	}
}

func TestWorkflowManager_GetTracker_NoOrchestrator(t *testing.T) {
	manager, _ := setupWorkflowManager(t)

	tracker := manager.GetTracker()
	if tracker == nil {
		t.Fatal("expected non-nil default tracker")
	}
}

func TestWorkflowManager_CancelWorkflow_NoOrchestrator(t *testing.T) {
	manager, _ := setupWorkflowManager(t)

	err := manager.CancelWorkflow()
	if err == nil {
		t.Error("expected error when cancelling with no orchestrator")
	}
}

// ---------------------------------------------------------------------------
// TestHandleGetWorkflowStatus
// ---------------------------------------------------------------------------

func TestHandleGetWorkflowStatus_NoOrchestrator(t *testing.T) {
	manager, _ := setupWorkflowManager(t)
	handler := HandleGetWorkflowStatus(manager)

	req := httptest.NewRequest(http.MethodGet, "/api/workflow/status", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["state"] != "idle" {
		t.Errorf("state = %q, want %q", resp["state"], "idle")
	}
	if resp["message"] != "No workflow running" {
		t.Errorf("message = %q, want %q", resp["message"], "No workflow running")
	}
}

func TestHandleGetWorkflowStatus_MethodNotAllowed(t *testing.T) {
	manager, _ := setupWorkflowManager(t)
	handler := HandleGetWorkflowStatus(manager)

	req := httptest.NewRequest(http.MethodPost, "/api/workflow/status", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// TestStatusMessage
// ---------------------------------------------------------------------------

func TestStatusMessage(t *testing.T) {
	tests := []struct {
		state specworkflow.WorkflowState
		round int
		want  string
	}{
		{specworkflow.StateInit, 1, "Initializing workflow"},
		{specworkflow.StateDiscovery, 1, "Running discovery agent"},
		{specworkflow.StateHumanGate1, 1, "Waiting for human gate approval (requirements confirmation)"},
		{specworkflow.StateDrafting, 1, "Drafting specification"},
		{specworkflow.StateHumanGate2, 1, "Waiting for human gate approval (draft review)"},
		{specworkflow.StateReviewing, 2, "Review round 2: dispatching reviewers"},
		{specworkflow.StateRevising, 3, "Review round 3: revising spec to address findings"},
		{specworkflow.StateJudging, 2, "Review round 2: judge evaluating convergence"},
		{specworkflow.StateHumanGateFinal, 1, "Waiting for final human gate approval"},
		{specworkflow.StateFinalized, 1, "Workflow complete: spec finalized"},
		{specworkflow.StateEscalated, 1, "Workflow escalated for human intervention"},
		{specworkflow.StateError, 1, "Workflow encountered an error"},
	}

	for _, tt := range tests {
		got := StatusMessage(tt.state, tt.round)
		if got != tt.want {
			t.Errorf("StatusMessage(%s, %d) = %q, want %q", tt.state, tt.round, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// TestHandleRetryWorkflow
// ---------------------------------------------------------------------------

func TestHandleRetryWorkflow_ClearsState(t *testing.T) {
	manager, dir := setupWorkflowManager(t)

	// Create a workflow-state.json file.
	featureDir := filepath.Join(dir, "specs", "test-feature")
	os.MkdirAll(featureDir, 0o755)
	statePath := filepath.Join(featureDir, "workflow-state.json")
	os.WriteFile(statePath, []byte(`{"state":"ESCALATED"}`), 0o644)

	handler := HandleRetryWorkflow(manager)
	body := `{"feature_name":"test-feature"}`
	req := httptest.NewRequest(http.MethodPost, "/api/workflow/retry", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// State file should be gone.
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Error("expected workflow-state.json to be deleted")
	}
}

func TestHandleRetryWorkflow_MissingFeatureName(t *testing.T) {
	manager, _ := setupWorkflowManager(t)
	handler := HandleRetryWorkflow(manager)

	body := `{}`
	req := httptest.NewRequest(http.MethodPost, "/api/workflow/retry", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleRetryWorkflow_MethodNotAllowed(t *testing.T) {
	manager, _ := setupWorkflowManager(t)
	handler := HandleRetryWorkflow(manager)

	req := httptest.NewRequest(http.MethodGet, "/api/workflow/retry", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// TestHandleResetWorkflow
// ---------------------------------------------------------------------------

func TestHandleResetWorkflow_DeletesDirectory(t *testing.T) {
	manager, dir := setupWorkflowManager(t)

	// Create feature directory with files.
	featureDir := filepath.Join(dir, "specs", "test-feature")
	os.MkdirAll(featureDir, 0o755)
	os.WriteFile(filepath.Join(featureDir, "spec-v1.md"), []byte("# Spec"), 0o644)
	os.WriteFile(filepath.Join(featureDir, "workflow-state.json"), []byte(`{}`), 0o644)

	handler := HandleResetWorkflow(manager)
	body := `{"feature_name":"test-feature"}`
	req := httptest.NewRequest(http.MethodPost, "/api/workflow/reset", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Directory should be gone.
	if _, err := os.Stat(featureDir); !os.IsNotExist(err) {
		t.Error("expected feature directory to be deleted")
	}
}

func TestHandleResetWorkflow_MissingFeatureName(t *testing.T) {
	manager, _ := setupWorkflowManager(t)
	handler := HandleResetWorkflow(manager)

	body := `{}`
	req := httptest.NewRequest(http.MethodPost, "/api/workflow/reset", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleResetWorkflow_NonexistentDir(t *testing.T) {
	manager, _ := setupWorkflowManager(t)
	handler := HandleResetWorkflow(manager)

	body := `{"feature_name":"nonexistent"}`
	req := httptest.NewRequest(http.MethodPost, "/api/workflow/reset", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// RemoveAll on nonexistent path succeeds.
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for nonexistent dir, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// TestHandleListFeatures
// ---------------------------------------------------------------------------

func TestHandleListFeatures_EmptySpecs(t *testing.T) {
	dir := t.TempDir()
	handler := HandleListFeatures(dir)

	req := httptest.NewRequest(http.MethodGet, "/api/workspace/features", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var features []featureInfo
	if err := json.NewDecoder(rec.Body).Decode(&features); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(features) != 0 {
		t.Errorf("expected 0 features, got %d", len(features))
	}
}

func TestHandleListFeatures_WithFeatures(t *testing.T) {
	dir := t.TempDir()
	specsDir := filepath.Join(dir, "specs")
	os.MkdirAll(specsDir, 0o755)

	// Feature 1: has workflow state, spec files, discovery
	f1Dir := filepath.Join(specsDir, "auth-flow")
	os.MkdirAll(f1Dir, 0o755)
	stateJSON := `{
		"state": "FINALIZED",
		"round": 3,
		"feature_name": "auth-flow",
		"started_at": "2026-03-17T05:00:00Z",
		"updated_at": "2026-03-17T06:00:00Z",
		"cumulative_cost_usd": 1.23
	}`
	os.WriteFile(filepath.Join(f1Dir, "workflow-state.json"), []byte(stateJSON), 0o644)
	os.WriteFile(filepath.Join(f1Dir, "spec-v1.md"), []byte("# v1"), 0o644)
	os.WriteFile(filepath.Join(f1Dir, "spec-v2.md"), []byte("# v2"), 0o644)
	os.WriteFile(filepath.Join(f1Dir, "discovery-output.json"), []byte("{}"), 0o644)
	os.WriteFile(filepath.Join(f1Dir, "review-round1.json"), []byte("{}"), 0o644)

	// Feature 2: no workflow state (unknown)
	f2Dir := filepath.Join(specsDir, "b4-slice")
	os.MkdirAll(f2Dir, 0o755)
	os.WriteFile(filepath.Join(f2Dir, "notes.md"), []byte("notes"), 0o644)

	handler := HandleListFeatures(dir)
	req := httptest.NewRequest(http.MethodGet, "/api/workspace/features", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var features []featureInfo
	if err := json.NewDecoder(rec.Body).Decode(&features); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(features) != 2 {
		t.Fatalf("expected 2 features, got %d", len(features))
	}

	// Should be sorted by updated_at (auth-flow has a date, b4-slice does not).
	if features[0].FeatureName != "auth-flow" {
		t.Errorf("expected first feature to be auth-flow, got %s", features[0].FeatureName)
	}

	// Verify auth-flow details.
	af := features[0]
	if af.State != "FINALIZED" {
		t.Errorf("auth-flow state = %q, want FINALIZED", af.State)
	}
	if af.Round != 3 {
		t.Errorf("auth-flow round = %d, want 3", af.Round)
	}
	if af.CostUSD != 1.23 {
		t.Errorf("auth-flow cost = %f, want 1.23", af.CostUSD)
	}
	if af.SpecVersions != 2 {
		t.Errorf("auth-flow spec_versions = %d, want 2", af.SpecVersions)
	}
	if !af.HasDiscovery {
		t.Error("auth-flow has_discovery should be true")
	}
	if !af.HasReviews {
		t.Error("auth-flow has_reviews should be true")
	}
	if !af.IsTerminal {
		t.Error("auth-flow is_terminal should be true for FINALIZED")
	}
	if len(af.Files) != 5 {
		t.Errorf("auth-flow files count = %d, want 5", len(af.Files))
	}

	// Verify b4-slice (unknown state).
	bs := features[1]
	if bs.State != "unknown" {
		t.Errorf("b4-slice state = %q, want unknown", bs.State)
	}
	if !bs.IsTerminal {
		t.Error("b4-slice is_terminal should be true for unknown")
	}
}

func TestHandleListFeatures_MethodNotAllowed(t *testing.T) {
	dir := t.TempDir()
	handler := HandleListFeatures(dir)

	req := httptest.NewRequest(http.MethodPost, "/api/workspace/features", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestHandleListFeatures_SortOrder(t *testing.T) {
	dir := t.TempDir()
	specsDir := filepath.Join(dir, "specs")
	os.MkdirAll(specsDir, 0o755)

	// Feature with older timestamp
	oldDir := filepath.Join(specsDir, "old-feature")
	os.MkdirAll(oldDir, 0o755)
	os.WriteFile(filepath.Join(oldDir, "workflow-state.json"), []byte(`{
		"state": "FINALIZED", "updated_at": "2026-03-01T00:00:00Z"
	}`), 0o644)

	// Feature with newer timestamp
	newDir := filepath.Join(specsDir, "new-feature")
	os.MkdirAll(newDir, 0o755)
	os.WriteFile(filepath.Join(newDir, "workflow-state.json"), []byte(`{
		"state": "ESCALATED", "updated_at": "2026-03-17T00:00:00Z"
	}`), 0o644)

	// Feature with no state (no timestamp) — should come last
	noStateDir := filepath.Join(specsDir, "aaa-no-state")
	os.MkdirAll(noStateDir, 0o755)
	os.WriteFile(filepath.Join(noStateDir, "readme.md"), []byte("hi"), 0o644)

	handler := HandleListFeatures(dir)
	req := httptest.NewRequest(http.MethodGet, "/api/workspace/features", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var features []featureInfo
	json.NewDecoder(rec.Body).Decode(&features)

	if len(features) != 3 {
		t.Fatalf("expected 3 features, got %d", len(features))
	}

	names := make([]string, len(features))
	for i, f := range features {
		names[i] = f.FeatureName
	}

	expected := []string{"new-feature", "old-feature", "aaa-no-state"}
	if !equalStrings(names, expected) {
		t.Errorf("sort order = %v, want %v", names, expected)
	}
}

func TestHandleListFeatures_ActiveWorkflowNotTerminal(t *testing.T) {
	dir := t.TempDir()
	specsDir := filepath.Join(dir, "specs")
	os.MkdirAll(specsDir, 0o755)

	activeDir := filepath.Join(specsDir, "active-feature")
	os.MkdirAll(activeDir, 0o755)
	os.WriteFile(filepath.Join(activeDir, "workflow-state.json"), []byte(`{
		"state": "REVIEWING", "round": 2, "updated_at": "2026-03-17T00:00:00Z"
	}`), 0o644)

	handler := HandleListFeatures(dir)
	req := httptest.NewRequest(http.MethodGet, "/api/workspace/features", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var features []featureInfo
	json.NewDecoder(rec.Body).Decode(&features)

	if len(features) != 1 {
		t.Fatalf("expected 1 feature, got %d", len(features))
	}
	if features[0].IsTerminal {
		t.Error("REVIEWING state should not be terminal")
	}
	if features[0].State != "REVIEWING" {
		t.Errorf("state = %q, want REVIEWING", features[0].State)
	}
}

// ---------------------------------------------------------------------------
// TestHandleGetWorkflowStatus_DiskState
// ---------------------------------------------------------------------------

func TestHandleGetWorkflowStatus_DiskGateState(t *testing.T) {
	manager, dir := setupWorkflowManager(t)

	// Create a gate state on disk.
	featureDir := filepath.Join(dir, "specs", "gate-feature")
	os.MkdirAll(featureDir, 0o755)
	stateJSON := `{
		"state": "HUMAN_GATE_1",
		"round": 1,
		"feature_name": "gate-feature",
		"started_at": "2026-03-17T05:00:00Z",
		"updated_at": "2026-03-17T06:00:00Z"
	}`
	os.WriteFile(filepath.Join(featureDir, "workflow-state.json"), []byte(stateJSON), 0o644)

	handler := HandleGetWorkflowStatus(manager)
	req := httptest.NewRequest(http.MethodGet, "/api/workflow/status", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// Should show the on-disk gate state, not "idle".
	if resp["state"] != "HUMAN_GATE_1" {
		t.Errorf("state = %q, want HUMAN_GATE_1", resp["state"])
	}
	if resp["paused"] != true {
		t.Error("expected paused=true for disk-only gate state")
	}
}

// ---------------------------------------------------------------------------
// TestTryResumeFromDiskState
// ---------------------------------------------------------------------------

func TestTryResumeFromDiskState_NoState(t *testing.T) {
	manager, _ := setupWorkflowManager(t)

	rec := httptest.NewRecorder()
	orch := tryResumeFromDiskState(manager, rec)

	if orch != nil {
		t.Error("expected nil orchestrator when no disk state")
	}
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestTryResumeFromDiskState_NonGateState(t *testing.T) {
	manager, dir := setupWorkflowManager(t)

	// Create a non-gate state on disk (REVIEWING).
	featureDir := filepath.Join(dir, "specs", "reviewing-feature")
	os.MkdirAll(featureDir, 0o755)
	stateJSON := `{
		"state": "REVIEWING",
		"round": 2,
		"feature_name": "reviewing-feature",
		"started_at": "2026-03-17T05:00:00Z",
		"updated_at": "2026-03-17T06:00:00Z"
	}`
	os.WriteFile(filepath.Join(featureDir, "workflow-state.json"), []byte(stateJSON), 0o644)

	rec := httptest.NewRecorder()
	orch := tryResumeFromDiskState(manager, rec)

	if orch != nil {
		t.Error("expected nil orchestrator for non-gate state")
	}
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

// equalStrings compares two string slices for equality.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Multi-Workflow WorkflowManager Tests (460.1)
// ---------------------------------------------------------------------------

// TestWorkflowManagerMapCRUD verifies that WorkflowManager supports
// starting multiple workflows and retrieving them by feature name.
func TestWorkflowManagerMapCRUD(t *testing.T) {
	manager, dir := setupWorkflowManager(t)
	handler := HandleStartWorkflow(manager)

	// Start workflow "alpha".
	body := `{"title":"Alpha","description":"test alpha","feature_name":"alpha"}`
	req := httptest.NewRequest(http.MethodPost, "/api/workflow/start", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("start alpha: expected 202, got %d: %s", rec.Code, rec.Body.String())
	}

	// Start workflow "beta".
	body = `{"title":"Beta","description":"test beta","feature_name":"beta"}`
	req = httptest.NewRequest(http.MethodPost, "/api/workflow/start", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("start beta: expected 202, got %d: %s", rec.Code, rec.Body.String())
	}

	// GetOrchestrator for each feature should return non-nil.
	alphaOrch := manager.GetOrchestrator("alpha")
	if alphaOrch == nil {
		t.Error("expected non-nil orchestrator for alpha")
	}
	betaOrch := manager.GetOrchestrator("beta")
	if betaOrch == nil {
		t.Error("expected non-nil orchestrator for beta")
	}

	// GetAllOrchestrators should return both.
	all := manager.GetAllOrchestrators()
	if len(all) != 2 {
		t.Errorf("expected 2 orchestrators, got %d", len(all))
	}

	// Verify feature directories were created.
	for _, name := range []string{"alpha", "beta"} {
		featureDir := filepath.Join(dir, "specs", name)
		if _, err := os.Stat(featureDir); os.IsNotExist(err) {
			t.Errorf("expected feature directory for %q to exist", name)
		}
	}

	// Wait for background goroutines to finish so TempDir cleanup succeeds.
	time.Sleep(100 * time.Millisecond)
}

// TestWorkflowManagerDuplicateFeatureRejected verifies that starting a
// workflow with a feature name that already has a non-terminal state on
// disk returns HTTP 409.
func TestWorkflowManagerDuplicateFeatureRejected(t *testing.T) {
	manager, dir := setupWorkflowManager(t)
	handler := HandleStartWorkflow(manager)

	// Pre-create an active (non-terminal) workflow state on disk for alpha.
	// This simulates a workflow that's in progress or at a gate state.
	featureDir := filepath.Join(dir, "specs", "alpha")
	os.MkdirAll(featureDir, 0o755)
	stateJSON := `{
		"state": "REVIEWING",
		"round": 2,
		"feature_name": "alpha",
		"started_at": "2026-03-17T05:00:00Z",
		"updated_at": "2026-03-17T06:00:00Z"
	}`
	os.WriteFile(filepath.Join(featureDir, "workflow-state.json"), []byte(stateJSON), 0o644)

	// Try starting "alpha" — should get 409 because disk state is non-terminal.
	body := `{"title":"Alpha","description":"test","feature_name":"alpha"}`
	req := httptest.NewRequest(http.MethodPost, "/api/workflow/start", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("duplicate start: expected 409, got %d: %s", rec.Code, rec.Body.String())
	}

	// But starting "beta" should succeed.
	body = `{"title":"Beta","description":"test","feature_name":"beta"}`
	req = httptest.NewRequest(http.MethodPost, "/api/workflow/start", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Errorf("start beta: expected 202, got %d: %s", rec.Code, rec.Body.String())
	}

	// Wait for background goroutines to finish so TempDir cleanup succeeds.
	time.Sleep(100 * time.Millisecond)
}

// TestWorkflowManagerCancelSpecific verifies that cancelling one workflow
// does not affect others.
func TestWorkflowManagerCancelSpecific(t *testing.T) {
	manager, _ := setupWorkflowManager(t)
	handler := HandleStartWorkflow(manager)

	// Start alpha and beta.
	for _, name := range []string{"alpha", "beta"} {
		body := `{"title":"` + name + `","description":"test","feature_name":"` + name + `"}`
		req := httptest.NewRequest(http.MethodPost, "/api/workflow/start", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("start %s: expected 202, got %d", name, rec.Code)
		}
	}

	// Cancel alpha.
	err := manager.CancelWorkflow("alpha")
	if err != nil {
		t.Fatalf("cancel alpha: %v", err)
	}

	// Alpha orchestrator should be gone.
	if manager.GetOrchestrator("alpha") != nil {
		t.Error("expected alpha orchestrator to be nil after cancel")
	}

	// Beta should still exist.
	betaOrch := manager.GetOrchestrator("beta")
	if betaOrch == nil {
		t.Error("expected beta orchestrator to still exist after cancelling alpha")
	}

	// Cancel non-existent feature should return error.
	err = manager.CancelWorkflow("nonexistent")
	if err == nil {
		t.Error("expected error when cancelling non-existent feature")
	}

	// Wait for background goroutines to finish so TempDir cleanup succeeds.
	time.Sleep(100 * time.Millisecond)
}
