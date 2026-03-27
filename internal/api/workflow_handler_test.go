package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
// TestValidateFeatureName
// ---------------------------------------------------------------------------

func TestValidateFeatureName(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantErr   bool
		errSubstr string
	}{
		{name: "simple alpha", input: "alpha", wantErr: false},
		{name: "hyphenated", input: "my-feature", wantErr: false},
		{name: "empty string", input: "", wantErr: true, errSubstr: "empty"},
		{name: "forward slash", input: "feat/sub", wantErr: true, errSubstr: "path separator"},
		{name: "backslash", input: "feat\\sub", wantErr: true, errSubstr: "path separator"},
		{name: "double dot mid", input: "feat..sub", wantErr: true, errSubstr: "traversal"},
		{name: "parent traversal", input: "../escape", wantErr: true, errSubstr: "traversal"},
		{name: "alphanumeric", input: "feature123", wantErr: false},
		{name: "underscores", input: "my_feature", wantErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateFeatureName(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ValidateFeatureName(%q) = nil, want error containing %q", tt.input, tt.errSubstr)
				}
				if !strings.Contains(err.Error(), tt.errSubstr) {
					t.Fatalf("ValidateFeatureName(%q) error = %q, want substring %q", tt.input, err.Error(), tt.errSubstr)
				}
			} else {
				if err != nil {
					t.Fatalf("ValidateFeatureName(%q) = %v, want nil", tt.input, err)
				}
			}
		})
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

	// With no workflows at all, should return an empty JSON array.
	var resp []map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp) != 0 {
		t.Errorf("expected empty array, got %d items", len(resp))
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
		{specworkflow.StateFinalized, 1, "Spec finalized, advancing to task decomposition"},
		{specworkflow.StateTaskify, 1, "Decomposing spec into task graph"},
		{specworkflow.StateTaskReview, 1, "Reviewing task graph with dual providers"},
		{specworkflow.StateTaskRevision, 1, "Revising task graph to address review findings"},
		{specworkflow.StateTaskHumanGate, 1, "Waiting for human approval of task graph"},
		{specworkflow.StateTasksApproved, 1, "Tasks approved, creating Beads issues"},
		{specworkflow.StateComplete, 1, "Workflow complete"},
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
	if af.IsTerminal {
		t.Error("auth-flow is_terminal should be false for FINALIZED (no longer terminal)")
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

	// Query with ?feature= to get single object.
	req := httptest.NewRequest(http.MethodGet, "/api/workflow/status?feature=gate-feature", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// Should show the on-disk gate state.
	if resp["state"] != "HUMAN_GATE_1" {
		t.Errorf("state = %q, want HUMAN_GATE_1", resp["state"])
	}
	if resp["paused"] != true {
		t.Error("expected paused=true for disk-only gate state")
	}

	// Also verify the list endpoint includes it.
	req2 := httptest.NewRequest(http.MethodGet, "/api/workflow/status", nil)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)

	var arr []map[string]interface{}
	if err := json.NewDecoder(rec2.Body).Decode(&arr); err != nil {
		t.Fatalf("decode array response: %v", err)
	}
	if len(arr) != 1 {
		t.Fatalf("expected 1 item in array, got %d", len(arr))
	}
	if arr[0]["state"] != "HUMAN_GATE_1" {
		t.Errorf("array[0].state = %q, want HUMAN_GATE_1", arr[0]["state"])
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

// ---------------------------------------------------------------------------
// Multi-Workflow Status Endpoint Tests (460.5)
// ---------------------------------------------------------------------------

// TestStatusAllWorkflows verifies that GET /api/workflow/status with no
// params returns a JSON array containing all workflow statuses from disk.
func TestStatusAllWorkflows(t *testing.T) {
	manager, dir := setupWorkflowManager(t)

	// Create disk states for alpha and beta.
	for _, feat := range []struct {
		name  string
		state string
		round int
	}{
		{"alpha", "REVIEWING", 2},
		{"beta", "DRAFTING", 1},
	} {
		featureDir := filepath.Join(dir, "specs", feat.name)
		os.MkdirAll(featureDir, 0o755)
		stateJSON := fmt.Sprintf(`{
			"state": %q,
			"round": %d,
			"feature_name": %q,
			"started_at": "2026-03-17T05:00:00Z",
			"updated_at": "2026-03-17T06:00:00Z",
			"cumulative_cost_usd": 0.5,
			"agent_invocations": 3
		}`, feat.state, feat.round, feat.name)
		os.WriteFile(filepath.Join(featureDir, "workflow-state.json"), []byte(stateJSON), 0o644)
	}

	handler := HandleGetWorkflowStatus(manager)
	req := httptest.NewRequest(http.MethodGet, "/api/workflow/status", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp []map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if len(resp) != 2 {
		t.Fatalf("expected 2 statuses, got %d: %v", len(resp), resp)
	}

	// Array should be sorted by feature name: alpha, beta.
	if resp[0]["feature_name"] != "alpha" {
		t.Errorf("first feature_name = %q, want alpha", resp[0]["feature_name"])
	}
	if resp[1]["feature_name"] != "beta" {
		t.Errorf("second feature_name = %q, want beta", resp[1]["feature_name"])
	}

	// Verify required fields exist on each entry.
	for i, entry := range resp {
		for _, field := range []string{"feature_name", "state", "round", "cost_usd", "wall_clock_seconds", "agent_invocations", "message"} {
			if _, ok := entry[field]; !ok {
				t.Errorf("resp[%d] missing field %q", i, field)
			}
		}
	}

	// Verify specific values.
	if resp[0]["state"] != "REVIEWING" {
		t.Errorf("alpha state = %q, want REVIEWING", resp[0]["state"])
	}
	if resp[1]["state"] != "DRAFTING" {
		t.Errorf("beta state = %q, want DRAFTING", resp[1]["state"])
	}
}

// TestStatusSingleWorkflow verifies that GET /api/workflow/status?feature=alpha
// returns a single JSON object for that feature.
func TestStatusSingleWorkflow(t *testing.T) {
	manager, dir := setupWorkflowManager(t)

	// Create disk state for alpha.
	featureDir := filepath.Join(dir, "specs", "alpha")
	os.MkdirAll(featureDir, 0o755)
	stateJSON := `{
		"state": "REVIEWING",
		"round": 2,
		"feature_name": "alpha",
		"started_at": "2026-03-17T05:00:00Z",
		"updated_at": "2026-03-17T06:00:00Z",
		"cumulative_cost_usd": 1.25,
		"agent_invocations": 5
	}`
	os.WriteFile(filepath.Join(featureDir, "workflow-state.json"), []byte(stateJSON), 0o644)

	handler := HandleGetWorkflowStatus(manager)
	req := httptest.NewRequest(http.MethodGet, "/api/workflow/status?feature=alpha", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// Verify it's a single object (not an array).
	if resp["feature_name"] != "alpha" {
		t.Errorf("feature_name = %q, want alpha", resp["feature_name"])
	}
	if resp["state"] != "REVIEWING" {
		t.Errorf("state = %q, want REVIEWING", resp["state"])
	}
	if resp["round"] != float64(2) {
		t.Errorf("round = %v, want 2", resp["round"])
	}

	// Verify required fields.
	for _, field := range []string{"feature_name", "state", "round", "cost_usd", "wall_clock_seconds", "agent_invocations", "message"} {
		if _, ok := resp[field]; !ok {
			t.Errorf("missing field %q", field)
		}
	}
}

// TestStatusNonexistentFeature verifies that GET /api/workflow/status?feature=nonexistent
// returns HTTP 404.
func TestStatusNonexistentFeature(t *testing.T) {
	manager, _ := setupWorkflowManager(t)

	handler := HandleGetWorkflowStatus(manager)
	req := httptest.NewRequest(http.MethodGet, "/api/workflow/status?feature=nonexistent", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, ok := resp["error"]; !ok {
		t.Error("expected error field in 404 response")
	}
}

// ---------------------------------------------------------------------------
// Per-Workflow Source Document Isolation Tests (460.8)
// ---------------------------------------------------------------------------

// TestCopySourceDocsToWorkflow verifies that files are copied from the global
// source-docs library into specs/{feature}/source-docs/.
func TestCopySourceDocsToWorkflow(t *testing.T) {
	dir := t.TempDir()

	// Create global source-docs with test files.
	globalDir := filepath.Join(dir, "source-docs")
	os.MkdirAll(globalDir, 0o755)
	os.WriteFile(filepath.Join(globalDir, "design.md"), []byte("# Design Doc"), 0o644)
	os.WriteFile(filepath.Join(globalDir, "requirements.txt"), []byte("requirement 1"), 0o644)

	// Copy specific files.
	sourcePaths := []string{
		filepath.Join(globalDir, "design.md"),
	}
	newPaths, err := copySourceDocsToWorkflow(dir, "alpha", sourcePaths)
	if err != nil {
		t.Fatalf("copySourceDocsToWorkflow: %v", err)
	}

	if len(newPaths) != 1 {
		t.Fatalf("expected 1 new path, got %d", len(newPaths))
	}

	expectedPath := filepath.Join(dir, "specs", "alpha", "source-docs", "design.md")
	if newPaths[0] != expectedPath {
		t.Errorf("new path = %q, want %q", newPaths[0], expectedPath)
	}

	// Verify the file was actually copied with correct content.
	content, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("read copied file: %v", err)
	}
	if string(content) != "# Design Doc" {
		t.Errorf("copied content = %q, want %q", string(content), "# Design Doc")
	}
}

// TestCopySourceDocsToWorkflow_AllFiles verifies that when all global source
// docs are passed, they are all copied to the workflow directory.
func TestCopySourceDocsToWorkflow_AllFiles(t *testing.T) {
	dir := t.TempDir()

	globalDir := filepath.Join(dir, "source-docs")
	os.MkdirAll(globalDir, 0o755)
	os.WriteFile(filepath.Join(globalDir, "a.md"), []byte("aaa"), 0o644)
	os.WriteFile(filepath.Join(globalDir, "b.txt"), []byte("bbb"), 0o644)
	os.WriteFile(filepath.Join(globalDir, "c.pdf"), []byte("ccc"), 0o644)

	allPaths := discoverSourceDocs(dir)
	if len(allPaths) != 3 {
		t.Fatalf("expected 3 global docs, got %d", len(allPaths))
	}

	newPaths, err := copySourceDocsToWorkflow(dir, "beta", allPaths)
	if err != nil {
		t.Fatalf("copySourceDocsToWorkflow: %v", err)
	}

	if len(newPaths) != 3 {
		t.Fatalf("expected 3 new paths, got %d", len(newPaths))
	}

	// Verify all files exist in the workflow directory.
	workflowDir := filepath.Join(dir, "specs", "beta", "source-docs")
	for _, name := range []string{"a.md", "b.txt", "c.pdf"} {
		if _, err := os.Stat(filepath.Join(workflowDir, name)); os.IsNotExist(err) {
			t.Errorf("expected %s to exist in workflow source-docs", name)
		}
	}
}

// TestCopySourceDocsToWorkflow_FilesAreCopiesNotSymlinks verifies that the
// copied files are true copies — modifying the original does not affect the
// workflow copy.
func TestCopySourceDocsToWorkflow_FilesAreCopiesNotSymlinks(t *testing.T) {
	dir := t.TempDir()

	globalDir := filepath.Join(dir, "source-docs")
	os.MkdirAll(globalDir, 0o755)
	origPath := filepath.Join(globalDir, "doc.md")
	os.WriteFile(origPath, []byte("original"), 0o644)

	newPaths, err := copySourceDocsToWorkflow(dir, "gamma", []string{origPath})
	if err != nil {
		t.Fatalf("copySourceDocsToWorkflow: %v", err)
	}

	// Modify the original file.
	os.WriteFile(origPath, []byte("modified"), 0o644)

	// The workflow copy should still have the original content.
	copiedContent, err := os.ReadFile(newPaths[0])
	if err != nil {
		t.Fatalf("read copied file: %v", err)
	}
	if string(copiedContent) != "original" {
		t.Errorf("copied file was modified when original changed — got %q, want %q", string(copiedContent), "original")
	}

	// Verify it's not a symlink.
	fi, err := os.Lstat(newPaths[0])
	if err != nil {
		t.Fatalf("lstat: %v", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Error("copied file is a symlink, expected a regular file")
	}
}

// TestCopySourceDocsToWorkflow_PathTraversalRejected verifies that source
// paths containing path traversal are rejected.
func TestCopySourceDocsToWorkflow_PathTraversalRejected(t *testing.T) {
	dir := t.TempDir()

	globalDir := filepath.Join(dir, "source-docs")
	os.MkdirAll(globalDir, 0o755)
	os.WriteFile(filepath.Join(globalDir, "legit.md"), []byte("ok"), 0o644)

	// Also create a file outside the source-docs directory.
	os.WriteFile(filepath.Join(dir, "secret.env"), []byte("SECRET=123"), 0o644)

	tests := []struct {
		name string
		path string
	}{
		{"parent traversal", filepath.Join(globalDir, "..", "secret.env")},
		{"absolute outside", filepath.Join(dir, "secret.env")},
		{"different directory", "/etc/passwd"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := copySourceDocsToWorkflow(dir, "evil", []string{tt.path})
			if err == nil {
				t.Errorf("expected error for path traversal with %q, got nil", tt.path)
			}
			if err != nil && !strings.Contains(err.Error(), "outside the global source-docs directory") {
				t.Errorf("unexpected error message: %v", err)
			}
		})
	}
}

// TestCopySourceDocsToWorkflow_CreatesDirectory verifies that the target
// directory is created on demand.
func TestCopySourceDocsToWorkflow_CreatesDirectory(t *testing.T) {
	dir := t.TempDir()

	globalDir := filepath.Join(dir, "source-docs")
	os.MkdirAll(globalDir, 0o755)
	os.WriteFile(filepath.Join(globalDir, "doc.md"), []byte("hello"), 0o644)

	// Verify specs/newfeature/source-docs doesn't exist yet.
	targetDir := filepath.Join(dir, "specs", "newfeature", "source-docs")
	if _, err := os.Stat(targetDir); !os.IsNotExist(err) {
		t.Fatal("expected target dir to not exist before copy")
	}

	_, err := copySourceDocsToWorkflow(dir, "newfeature", []string{filepath.Join(globalDir, "doc.md")})
	if err != nil {
		t.Fatalf("copySourceDocsToWorkflow: %v", err)
	}

	// Directory should now exist.
	if _, err := os.Stat(targetDir); os.IsNotExist(err) {
		t.Error("expected target directory to be created")
	}
}

// TestDiscoverWorkflowSourceDocs verifies that discoverWorkflowSourceDocs
// returns files from the per-workflow source-docs directory.
func TestDiscoverWorkflowSourceDocs(t *testing.T) {
	dir := t.TempDir()

	// Create per-workflow source docs.
	workflowDocsDir := filepath.Join(dir, "specs", "alpha", "source-docs")
	os.MkdirAll(workflowDocsDir, 0o755)
	os.WriteFile(filepath.Join(workflowDocsDir, "design.md"), []byte("# Design"), 0o644)
	os.WriteFile(filepath.Join(workflowDocsDir, "reqs.txt"), []byte("req"), 0o644)

	paths := discoverWorkflowSourceDocs(dir, "alpha")
	if len(paths) != 2 {
		t.Fatalf("expected 2 paths, got %d: %v", len(paths), paths)
	}

	// Should all be under the workflow directory.
	for _, p := range paths {
		if !strings.Contains(p, filepath.Join("specs", "alpha", "source-docs")) {
			t.Errorf("path %q is not under workflow source-docs", p)
		}
	}
}

// TestDiscoverWorkflowSourceDocs_NoDir verifies that missing workflow
// source-docs returns empty.
func TestDiscoverWorkflowSourceDocs_NoDir(t *testing.T) {
	paths := discoverWorkflowSourceDocs("/nonexistent", "alpha")
	if len(paths) != 0 {
		t.Errorf("expected 0 paths for missing dir, got %d", len(paths))
	}
}

// TestStartWorkflow_CopiesSourceDocs verifies that HandleStartWorkflow copies
// source docs from the global library to the per-workflow directory.
func TestStartWorkflow_CopiesSourceDocs(t *testing.T) {
	manager, dir := setupWorkflowManager(t)

	// Create global source docs.
	globalDir := filepath.Join(dir, "source-docs")
	os.MkdirAll(globalDir, 0o755)
	os.WriteFile(filepath.Join(globalDir, "design.md"), []byte("# Design"), 0o644)
	os.WriteFile(filepath.Join(globalDir, "reqs.txt"), []byte("requirements"), 0o644)

	handler := HandleStartWorkflow(manager)

	// Start with specific source_doc_paths.
	body := fmt.Sprintf(`{"title":"Test","feature_name":"doc-test","source_doc_paths":[%q]}`,
		filepath.Join(globalDir, "design.md"))
	req := httptest.NewRequest(http.MethodPost, "/api/workflow/start", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify the file was copied to specs/doc-test/source-docs/.
	copiedPath := filepath.Join(dir, "specs", "doc-test", "source-docs", "design.md")
	content, err := os.ReadFile(copiedPath)
	if err != nil {
		t.Fatalf("read copied file: %v", err)
	}
	if string(content) != "# Design" {
		t.Errorf("copied content = %q, want %q", string(content), "# Design")
	}

	// reqs.txt should NOT be copied (we only specified design.md).
	reqsPath := filepath.Join(dir, "specs", "doc-test", "source-docs", "reqs.txt")
	if _, err := os.Stat(reqsPath); !os.IsNotExist(err) {
		t.Error("expected reqs.txt to NOT be copied when only design.md specified")
	}

	time.Sleep(100 * time.Millisecond)
}

// TestStartWorkflow_CopiesAllDocsWhenNoneSpecified verifies that starting a
// workflow with no source_doc_paths copies all global docs.
func TestStartWorkflow_CopiesAllDocsWhenNoneSpecified(t *testing.T) {
	manager, dir := setupWorkflowManager(t)

	// Create global source docs.
	globalDir := filepath.Join(dir, "source-docs")
	os.MkdirAll(globalDir, 0o755)
	os.WriteFile(filepath.Join(globalDir, "a.md"), []byte("aaa"), 0o644)
	os.WriteFile(filepath.Join(globalDir, "b.txt"), []byte("bbb"), 0o644)

	handler := HandleStartWorkflow(manager)

	body := `{"title":"All Docs","feature_name":"all-docs-test"}`
	req := httptest.NewRequest(http.MethodPost, "/api/workflow/start", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}

	// Both files should be copied.
	workflowDocsDir := filepath.Join(dir, "specs", "all-docs-test", "source-docs")
	for _, name := range []string{"a.md", "b.txt"} {
		path := filepath.Join(workflowDocsDir, name)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("expected %s to be copied to workflow source-docs", name)
		}
	}

	time.Sleep(100 * time.Millisecond)
}

// TestStartWorkflow_PathTraversalInSourceDocPaths verifies that path traversal
// in source_doc_paths is rejected at the HTTP level.
func TestStartWorkflow_PathTraversalInSourceDocPaths(t *testing.T) {
	manager, dir := setupWorkflowManager(t)

	// Create global source-docs dir (empty is fine, the traversal is the point).
	os.MkdirAll(filepath.Join(dir, "source-docs"), 0o755)

	handler := HandleStartWorkflow(manager)

	body := `{"title":"Evil","feature_name":"evil-test","source_doc_paths":["/etc/passwd"]}`
	req := httptest.NewRequest(http.MethodPost, "/api/workflow/start", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for path traversal, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestCancelWorkflowAPI_NonexistentFeature_Returns404 verifies that
// POST /api/workflow/cancel with a non-existent feature_name returns 404.
func TestCancelWorkflowAPI_NonexistentFeature_Returns404(t *testing.T) {
	manager, _ := setupWorkflowManager(t)
	handler := HandleCancelWorkflowAPI(manager)

	body := `{"feature_name":"nonexistent"}`
	req := httptest.NewRequest(http.MethodPost, "/api/workflow/cancel", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for non-existent feature, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ===========================================================================
// Regression Tests: Single-Workflow Backward Compatibility (460.9)
// ===========================================================================

// TestSingleWorkflowStillWorks verifies that the existing single-workflow
// path continues to work correctly after the multi-workflow refactor.
// It starts a single workflow via HandleStartWorkflow, checks the 202
// response, then verifies that GetOrchestrator() (no feature name) and
// GetState() return correct values for backward compatibility.
func TestSingleWorkflowStillWorks(t *testing.T) {
	manager, _ := setupWorkflowManager(t)
	handler := HandleStartWorkflow(manager)

	// Start a single workflow.
	body := `{"title":"Single Feature","description":"regression test","feature_name":"single-compat"}`
	req := httptest.NewRequest(http.MethodPost, "/api/workflow/start", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// 1. Verify 202 Accepted.
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["feature_name"] != "single-compat" {
		t.Errorf("feature_name = %q, want single-compat", resp["feature_name"])
	}
	if resp["status"] != "started" {
		t.Errorf("status = %q, want started", resp["status"])
	}

	// 2. GetOrchestrator() with no feature name (backward compat) returns non-nil.
	orch := manager.GetOrchestrator()
	if orch == nil {
		t.Fatal("GetOrchestrator() with no feature name returned nil; expected non-nil for single workflow")
	}

	// 3. GetOrchestrator("single-compat") also returns the same orchestrator.
	orchByName := manager.GetOrchestrator("single-compat")
	if orchByName == nil {
		t.Fatal("GetOrchestrator(\"single-compat\") returned nil")
	}
	if orch != orchByName {
		t.Error("GetOrchestrator() and GetOrchestrator(\"single-compat\") returned different orchestrators")
	}

	// 4. GetState() with no feature name returns state with correct feature name.
	state := manager.GetState()
	if state == nil {
		t.Fatal("GetState() returned nil")
	}
	if state.FeatureName != "single-compat" {
		t.Errorf("GetState().FeatureName = %q, want single-compat", state.FeatureName)
	}

	// 5. HasRunningWorkflow should be true while the orchestrator goroutine is alive.
	// (The workflow will fail quickly since claude CLI isn't available, but
	// the orchestrator is registered in the map.)
	if manager.GetAllOrchestrators() == nil || len(manager.GetAllOrchestrators()) != 1 {
		t.Errorf("expected exactly 1 orchestrator in map, got %d", len(manager.GetAllOrchestrators()))
	}

	// Wait for background goroutine to finish so TempDir cleanup succeeds.
	time.Sleep(200 * time.Millisecond)
}

// TestStatusEndpointArrayFormat verifies that GET /api/workflow/status (no
// params) returns a JSON array even when there is only one workflow. This
// is the expected response format after the multi-workflow refactor — older
// clients that expected an object must be updated. Each entry in the array
// must contain all required status fields.
func TestStatusEndpointArrayFormat(t *testing.T) {
	manager, dir := setupWorkflowManager(t)

	// Create a single workflow state on disk.
	featureDir := filepath.Join(dir, "specs", "solo-feature")
	os.MkdirAll(featureDir, 0o755)
	stateJSON := `{
		"state": "REVIEWING",
		"round": 2,
		"feature_name": "solo-feature",
		"started_at": "2026-03-17T05:00:00Z",
		"updated_at": "2026-03-17T06:00:00Z",
		"cumulative_cost_usd": 0.42,
		"agent_invocations": 7
	}`
	os.WriteFile(filepath.Join(featureDir, "workflow-state.json"), []byte(stateJSON), 0o644)

	handler := HandleGetWorkflowStatus(manager)
	req := httptest.NewRequest(http.MethodGet, "/api/workflow/status", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// The response body must be a JSON array, not a JSON object.
	raw := rec.Body.Bytes()
	if len(raw) == 0 {
		t.Fatal("empty response body")
	}
	// First non-whitespace character must be '['.
	for _, b := range raw {
		if b == ' ' || b == '\t' || b == '\n' || b == '\r' {
			continue
		}
		if b != '[' {
			t.Fatalf("expected JSON array (first char '['), got %q", string(b))
		}
		break
	}

	var arr []map[string]interface{}
	if err := json.Unmarshal(raw, &arr); err != nil {
		t.Fatalf("failed to decode as JSON array: %v", err)
	}

	// Exactly one entry.
	if len(arr) != 1 {
		t.Fatalf("expected 1 entry in array, got %d", len(arr))
	}

	entry := arr[0]

	// Verify all required fields.
	requiredFields := []string{
		"feature_name", "state", "round",
		"cost_usd", "wall_clock_seconds", "agent_invocations", "message",
	}
	for _, field := range requiredFields {
		if _, ok := entry[field]; !ok {
			t.Errorf("missing required field %q in status entry", field)
		}
	}

	// Verify values.
	if entry["feature_name"] != "solo-feature" {
		t.Errorf("feature_name = %q, want solo-feature", entry["feature_name"])
	}
	if entry["state"] != "REVIEWING" {
		t.Errorf("state = %q, want REVIEWING", entry["state"])
	}
	if entry["round"] != float64(2) {
		t.Errorf("round = %v, want 2", entry["round"])
	}
}

// ===========================================================================
// Concurrent Multi-Workflow Integration Tests (460.12)
// ===========================================================================

// TestStartConcurrentWorkflows verifies that two workflows started concurrently
// via HandleStartWorkflow both return 202, register in the orchestrator map,
// and create independent feature directories.
func TestStartConcurrentWorkflows(t *testing.T) {
	manager, dir := setupWorkflowManager(t)
	handler := HandleStartWorkflow(manager)

	// Start "alpha" and "beta" workflows.
	for _, name := range []string{"alpha", "beta"} {
		body := fmt.Sprintf(`{"title":"%s","description":"concurrent test","feature_name":"%s"}`, name, name)
		req := httptest.NewRequest(http.MethodPost, "/api/workflow/start", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusAccepted {
			t.Fatalf("start %s: expected 202, got %d: %s", name, rec.Code, rec.Body.String())
		}
	}

	// Verify GetAllOrchestrators returns 2 entries.
	all := manager.GetAllOrchestrators()
	if len(all) != 2 {
		t.Errorf("expected 2 orchestrators, got %d", len(all))
	}

	// Brief sleep for background goroutines.
	time.Sleep(100 * time.Millisecond)

	// Verify feature directories exist for both.
	for _, name := range []string{"alpha", "beta"} {
		featureDir := filepath.Join(dir, "specs", name)
		if _, err := os.Stat(featureDir); os.IsNotExist(err) {
			t.Errorf("expected feature directory for %q to exist", name)
		}
	}
}

// TestConcurrentWorkflowIsolation verifies that two workflows with different
// disk states report independent state and round values through the status
// endpoint. The list endpoint returns both, and per-feature queries return
// only the queried workflow's data.
func TestConcurrentWorkflowIsolation(t *testing.T) {
	manager, dir := setupWorkflowManager(t)

	// Create disk states: alpha at REVIEWING round 2, beta at DISCOVERY round 1.
	for _, feat := range []struct {
		name  string
		state string
		round int
	}{
		{"alpha", "REVIEWING", 2},
		{"beta", "DISCOVERY", 1},
	} {
		featureDir := filepath.Join(dir, "specs", feat.name)
		os.MkdirAll(featureDir, 0o755)
		stateJSON := fmt.Sprintf(`{
			"state": %q,
			"round": %d,
			"feature_name": %q,
			"started_at": "2026-03-17T05:00:00Z",
			"updated_at": "2026-03-17T06:00:00Z"
		}`, feat.state, feat.round, feat.name)
		os.WriteFile(filepath.Join(featureDir, "workflow-state.json"), []byte(stateJSON), 0o644)
	}

	handler := HandleGetWorkflowStatus(manager)

	// 1. GET /api/workflow/status (no params) returns array with both.
	req := httptest.NewRequest(http.MethodGet, "/api/workflow/status", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var arr []map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&arr); err != nil {
		t.Fatalf("decode array: %v", err)
	}
	if len(arr) != 2 {
		t.Fatalf("expected 2 statuses, got %d", len(arr))
	}

	// 2. GET ?feature=alpha returns alpha's state.
	req = httptest.NewRequest(http.MethodGet, "/api/workflow/status?feature=alpha", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("alpha: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var alphaResp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&alphaResp); err != nil {
		t.Fatalf("decode alpha: %v", err)
	}
	if alphaResp["state"] != "REVIEWING" {
		t.Errorf("alpha state = %q, want REVIEWING", alphaResp["state"])
	}
	if alphaResp["round"] != float64(2) {
		t.Errorf("alpha round = %v, want 2", alphaResp["round"])
	}

	// 3. GET ?feature=beta returns beta's state.
	req = httptest.NewRequest(http.MethodGet, "/api/workflow/status?feature=beta", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("beta: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var betaResp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&betaResp); err != nil {
		t.Fatalf("decode beta: %v", err)
	}
	if betaResp["state"] != "DISCOVERY" {
		t.Errorf("beta state = %q, want DISCOVERY", betaResp["state"])
	}
	if betaResp["round"] != float64(1) {
		t.Errorf("beta round = %v, want 1", betaResp["round"])
	}

	// 4. Verify they are truly independent: alpha's state != beta's state.
	if alphaResp["state"] == betaResp["state"] {
		t.Error("alpha and beta should have different states, but both are the same")
	}
	if alphaResp["round"] == betaResp["round"] {
		t.Error("alpha and beta should have different rounds, but both are the same")
	}
}

// TestGateDoesNotBlockOtherWorkflow verifies that one workflow paused at a
// gate state does not prevent another workflow from being in an active state.
// Both workflows are independent — alpha at HUMAN_GATE_1 and beta at DISCOVERY.
func TestGateDoesNotBlockOtherWorkflow(t *testing.T) {
	manager, dir := setupWorkflowManager(t)

	// Create disk states: alpha at HUMAN_GATE_1, beta at DISCOVERY.
	for _, feat := range []struct {
		name  string
		state string
		round int
	}{
		{"alpha", "HUMAN_GATE_1", 1},
		{"beta", "DISCOVERY", 1},
	} {
		featureDir := filepath.Join(dir, "specs", feat.name)
		os.MkdirAll(featureDir, 0o755)
		stateJSON := fmt.Sprintf(`{
			"state": %q,
			"round": %d,
			"feature_name": %q,
			"started_at": "2026-03-17T05:00:00Z",
			"updated_at": "2026-03-17T06:00:00Z"
		}`, feat.state, feat.round, feat.name)
		os.WriteFile(filepath.Join(featureDir, "workflow-state.json"), []byte(stateJSON), 0o644)
	}

	handler := HandleGetWorkflowStatus(manager)

	// GET /api/workflow/status — both should appear.
	req := httptest.NewRequest(http.MethodGet, "/api/workflow/status", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var arr []map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&arr); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(arr) != 2 {
		t.Fatalf("expected 2 statuses, got %d", len(arr))
	}

	// Build a lookup by feature_name.
	byName := make(map[string]map[string]interface{})
	for _, entry := range arr {
		name, _ := entry["feature_name"].(string)
		byName[name] = entry
	}

	// Alpha should show HUMAN_GATE_1.
	alphaEntry, ok := byName["alpha"]
	if !ok {
		t.Fatal("alpha not found in status array")
	}
	if alphaEntry["state"] != "HUMAN_GATE_1" {
		t.Errorf("alpha state = %q, want HUMAN_GATE_1", alphaEntry["state"])
	}

	// Beta should show DISCOVERY — unaffected by alpha's gate.
	betaEntry, ok := byName["beta"]
	if !ok {
		t.Fatal("beta not found in status array")
	}
	if betaEntry["state"] != "DISCOVERY" {
		t.Errorf("beta state = %q, want DISCOVERY", betaEntry["state"])
	}

	// Verify independence: alpha being at a gate does not add any gate-related
	// fields to beta's status.
	if _, hasPaused := betaEntry["paused"]; hasPaused {
		// Beta could be marked as paused (no running orchestrator for it),
		// but its state must remain DISCOVERY regardless of alpha's gate.
		if betaEntry["state"] != "DISCOVERY" {
			t.Errorf("beta state changed from DISCOVERY despite alpha being at a gate")
		}
	}
}

// TestCancelOneWorkflowOtherContinues verifies that cancelling one workflow
// does not affect the other. After cancelling "alpha", "beta" should still
// be accessible via GetOrchestrator.
func TestCancelOneWorkflowOtherContinues(t *testing.T) {
	manager, _ := setupWorkflowManager(t)
	handler := HandleStartWorkflow(manager)

	// Start alpha and beta via HandleStartWorkflow.
	for _, name := range []string{"alpha", "beta"} {
		body := fmt.Sprintf(`{"title":"%s","description":"cancel test","feature_name":"%s"}`, name, name)
		req := httptest.NewRequest(http.MethodPost, "/api/workflow/start", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("start %s: expected 202, got %d", name, rec.Code)
		}
	}

	// Cancel alpha via CancelWorkflow("alpha").
	if err := manager.CancelWorkflow("alpha"); err != nil {
		t.Fatalf("cancel alpha: %v", err)
	}

	// Verify GetOrchestrator("alpha") is nil (cancelled).
	if manager.GetOrchestrator("alpha") != nil {
		t.Error("expected alpha orchestrator to be nil after cancel")
	}

	// Verify GetOrchestrator("beta") is still non-nil.
	if manager.GetOrchestrator("beta") == nil {
		t.Error("expected beta orchestrator to still exist after cancelling alpha")
	}

	// Brief sleep for goroutine cleanup.
	time.Sleep(100 * time.Millisecond)
}

// TestBackwardCompatCancelNoFeatureName verifies that POST /api/workflow/cancel
// with an empty body (no feature_name) cancels a running workflow. This is the
// backward-compatible behavior for clients that do not yet send feature_name.
func TestBackwardCompatCancelNoFeatureName(t *testing.T) {
	manager, _ := setupWorkflowManager(t)

	// Start a workflow so there is something to cancel.
	startHandler := HandleStartWorkflow(manager)
	body := `{"title":"Cancel Me","description":"test","feature_name":"cancel-compat"}`
	req := httptest.NewRequest(http.MethodPost, "/api/workflow/start", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	startHandler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("start: expected 202, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify the orchestrator exists.
	if manager.GetOrchestrator() == nil {
		t.Fatal("expected non-nil orchestrator after starting workflow")
	}

	// Cancel with empty body (backward compat: no feature_name).
	cancelHandler := HandleCancelWorkflowAPI(manager)
	cancelReq := httptest.NewRequest(http.MethodPost, "/api/workflow/cancel", bytes.NewBufferString("{}"))
	cancelReq.Header.Set("Content-Type", "application/json")
	cancelRec := httptest.NewRecorder()
	cancelHandler.ServeHTTP(cancelRec, cancelReq)

	if cancelRec.Code != http.StatusOK {
		t.Fatalf("cancel: expected 200, got %d: %s", cancelRec.Code, cancelRec.Body.String())
	}

	var cancelResp map[string]interface{}
	if err := json.NewDecoder(cancelRec.Body).Decode(&cancelResp); err != nil {
		t.Fatalf("decode cancel response: %v", err)
	}
	if cancelResp["status"] != "cancelled" {
		t.Errorf("cancel status = %q, want cancelled", cancelResp["status"])
	}

	// After cancellation, GetOrchestrator() should return nil.
	if manager.GetOrchestrator() != nil {
		t.Error("expected nil orchestrator after cancel, but got non-nil")
	}

	// Also verify that GetAllOrchestrators map is empty.
	all := manager.GetAllOrchestrators()
	if len(all) != 0 {
		t.Errorf("expected 0 orchestrators after cancel, got %d", len(all))
	}

	// Wait for background goroutine to finish so TempDir cleanup succeeds.
	time.Sleep(200 * time.Millisecond)
}

// ---------------------------------------------------------------------------
// TestExtractFeatureFromWorkflowPath
// ---------------------------------------------------------------------------

func TestExtractFeatureFromWorkflowPath(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/api/workflow/alpha/source-docs", "alpha"},
		{"/api/workflow/my-feature/source-docs", "my-feature"},
		{"/api/workflow/", ""},
		{"/api/workflow", ""},
		{"/api/other/alpha/source-docs", ""},
		{"/api/workflow/alpha/source-docs/", "alpha"},
	}

	for _, tt := range tests {
		got := extractFeatureFromWorkflowPath(tt.path)
		if got != tt.want {
			t.Errorf("extractFeatureFromWorkflowPath(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// TestAssignSourceDocs
// ---------------------------------------------------------------------------

func TestAssignSourceDocs(t *testing.T) {
	manager, dir := setupWorkflowManager(t)

	// Set up global source-docs library with test files.
	globalDocsDir := filepath.Join(dir, "source-docs")
	os.MkdirAll(globalDocsDir, 0o755)
	os.WriteFile(filepath.Join(globalDocsDir, "design.md"), []byte("# Design Doc"), 0o644)
	os.WriteFile(filepath.Join(globalDocsDir, "api-spec.md"), []byte("# API Spec"), 0o644)

	handler := HandleAssignSourceDocs(manager)

	body := `{"source_doc_paths": ["design.md", "api-spec.md"]}`
	req := httptest.NewRequest(http.MethodPost, "/api/workflow/alpha/source-docs", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp["feature"] != "alpha" {
		t.Errorf("feature = %q, want %q", resp["feature"], "alpha")
	}
	count, ok := resp["count"].(float64)
	if !ok || int(count) != 2 {
		t.Errorf("count = %v, want 2", resp["count"])
	}

	// Verify files were actually copied.
	targetDir := filepath.Join(dir, "specs", "alpha", "source-docs")
	for _, name := range []string{"design.md", "api-spec.md"} {
		path := filepath.Join(targetDir, name)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected file %q to exist, got error: %v", path, err)
		}
	}
}

// ---------------------------------------------------------------------------
// TestAssignSourceDocs_FileNotFound
// ---------------------------------------------------------------------------

func TestAssignSourceDocs_FileNotFound(t *testing.T) {
	manager, dir := setupWorkflowManager(t)

	// Set up global source-docs library (empty).
	globalDocsDir := filepath.Join(dir, "source-docs")
	os.MkdirAll(globalDocsDir, 0o755)

	handler := HandleAssignSourceDocs(manager)

	body := `{"source_doc_paths": ["nonexistent.md"]}`
	req := httptest.NewRequest(http.MethodPost, "/api/workflow/alpha/source-docs", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// TestAssignSourceDocs_InvalidFeatureName
// ---------------------------------------------------------------------------

func TestAssignSourceDocs_InvalidFeatureName(t *testing.T) {
	manager, _ := setupWorkflowManager(t)
	handler := HandleAssignSourceDocs(manager)

	body := `{"source_doc_paths": ["design.md"]}`
	req := httptest.NewRequest(http.MethodPost, "/api/workflow/../escape/source-docs", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify the error message mentions the validation failure.
	var resp map[string]string
	json.NewDecoder(rec.Body).Decode(&resp)
	if !strings.Contains(resp["error"], "traversal") && !strings.Contains(resp["error"], "invalid") {
		t.Errorf("expected error about traversal/invalid, got: %s", resp["error"])
	}
}

// ---------------------------------------------------------------------------
// TestAssignSourceDocs_EmptyPaths
// ---------------------------------------------------------------------------

func TestAssignSourceDocs_EmptyPaths(t *testing.T) {
	manager, _ := setupWorkflowManager(t)
	handler := HandleAssignSourceDocs(manager)

	body := `{"source_doc_paths": []}`
	req := httptest.NewRequest(http.MethodPost, "/api/workflow/alpha/source-docs", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// TestAssignSourceDocs_PathTraversalInDoc
// ---------------------------------------------------------------------------

func TestAssignSourceDocs_PathTraversalInDoc(t *testing.T) {
	manager, _ := setupWorkflowManager(t)
	handler := HandleAssignSourceDocs(manager)

	body := `{"source_doc_paths": ["../../etc/passwd"]}`
	req := httptest.NewRequest(http.MethodPost, "/api/workflow/alpha/source-docs", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// TestGetWorkflowSourceDocs
// ---------------------------------------------------------------------------

func TestGetWorkflowSourceDocs(t *testing.T) {
	manager, dir := setupWorkflowManager(t)

	// Create some files in specs/alpha/source-docs/.
	docsDir := filepath.Join(dir, "specs", "alpha", "source-docs")
	os.MkdirAll(docsDir, 0o755)
	os.WriteFile(filepath.Join(docsDir, "design.md"), []byte("# Design"), 0o644)
	os.WriteFile(filepath.Join(docsDir, "api-spec.md"), []byte("# API"), 0o644)

	handler := HandleGetWorkflowSourceDocs(manager)

	req := httptest.NewRequest(http.MethodGet, "/api/workflow/alpha/source-docs", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var docs []sourceDocInfo
	if err := json.NewDecoder(rec.Body).Decode(&docs); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if len(docs) != 2 {
		t.Fatalf("expected 2 docs, got %d", len(docs))
	}

	// Should be sorted by name.
	if docs[0].Name != "api-spec.md" {
		t.Errorf("docs[0].Name = %q, want %q", docs[0].Name, "api-spec.md")
	}
	if docs[1].Name != "design.md" {
		t.Errorf("docs[1].Name = %q, want %q", docs[1].Name, "design.md")
	}

	// Check that sizes are reasonable.
	if docs[0].Size == 0 {
		t.Error("expected non-zero size for api-spec.md")
	}
	if docs[1].Size == 0 {
		t.Error("expected non-zero size for design.md")
	}

	// Check that ModifiedAt is populated.
	for _, d := range docs {
		if d.ModifiedAt.IsZero() {
			t.Errorf("expected non-zero ModifiedAt for %q", d.Name)
		}
	}
}

// ---------------------------------------------------------------------------
// TestGetWorkflowSourceDocs_NoFeature
// ---------------------------------------------------------------------------

func TestGetWorkflowSourceDocs_NoFeature(t *testing.T) {
	manager, _ := setupWorkflowManager(t)
	handler := HandleGetWorkflowSourceDocs(manager)

	// Request for a feature that doesn't exist.
	req := httptest.NewRequest(http.MethodGet, "/api/workflow/nonexistent/source-docs", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var docs []sourceDocInfo
	if err := json.NewDecoder(rec.Body).Decode(&docs); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if len(docs) != 0 {
		t.Errorf("expected empty array, got %d docs", len(docs))
	}
}
