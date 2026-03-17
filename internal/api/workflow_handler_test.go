package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

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
