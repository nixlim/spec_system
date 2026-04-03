package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/foundry-zero/adversarial-spec-system/internal/codedoc"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// noopAgentRunner is a stub runner that returns empty output without error.
type noopAgentRunner struct{}

func (noopAgentRunner) Run(prompt, outputPath string, timeoutSeconds int) (int, string, float64, int64, error) {
	// Write a minimal valid JSON output to satisfy parsers.
	_ = os.WriteFile(outputPath, []byte("{}"), 0o644)
	return 0, "{}", 0.0, 0, nil
}

func newTestCDManager(t *testing.T) (*CodedocManager, string) {
	t.Helper()
	workspaceDir := t.TempDir()
	cfg := codedoc.DefaultCodedocConfig()
	mgr := NewCodedocManager(workspaceDir, cfg)
	// Set noop runners to prevent nil-pointer panics from background goroutines.
	mgr.SetRunners(noopAgentRunner{}, noopAgentRunner{}, noopAgentRunner{})
	return mgr, workspaceDir
}

// setupCDWithOrchestrator creates a manager with an orchestrator at a given state.
func setupCDWithOrchestrator(t *testing.T, featureName string, targetState codedoc.CDState) (*CodedocManager, string) {
	t.Helper()
	manager, workspaceDir := newTestCDManager(t)

	codePath := t.TempDir()
	noop := noopAgentRunner{}
	orch := codedoc.NewCodedocOrchestrator(codedoc.CodedocOrchestratorConfig{
		WorkspaceDir: workspaceDir,
		FeatureName:  featureName,
		CodePath:     codePath,
		Mode:         "full",
		Config:       manager.config,
		Runner:       noop,
		CodexRunner:  noop,
		MergeRunner:  noop,
	})

	// Ensure the feature directory exists so state persistence succeeds.
	featureDir := filepath.Join(workspaceDir, "codedoc", featureName)
	os.MkdirAll(featureDir, 0o755)

	// Drive state machine to target state if not CDInit.
	if targetState != codedoc.CDInit {
		sm := orch.StateMachine()
		// Walk through transitions to reach the desired state.
		transitions := statePathTo(targetState)
		for _, t := range transitions {
			if err := sm.Transition(t); err != nil {
				// For states that require guards to pass, force via RestoreState.
				ws := sm.State()
				ws.State = targetState
				sm.RestoreState(ws)
				break
			}
		}
		// Final fallback: force the state directly.
		if sm.Current() != targetState {
			ws := sm.State()
			ws.State = targetState
			sm.RestoreState(ws)
		}
	}

	manager.orchestrators[featureName] = orch
	return manager, workspaceDir
}

// statePathTo returns the sequence of transitions to reach a target state
// from CDInit. Only covers common paths used in tests.
func statePathTo(target codedoc.CDState) []codedoc.CDState {
	switch target {
	case codedoc.CDDiscovery:
		return []codedoc.CDState{codedoc.CDDiscovery}
	case codedoc.CDHumanGateScope:
		return []codedoc.CDState{codedoc.CDDiscovery, codedoc.CDHumanGateScope}
	case codedoc.CDDrafting:
		return []codedoc.CDState{codedoc.CDDiscovery, codedoc.CDHumanGateScope, codedoc.CDDrafting}
	case codedoc.CDHumanGateDraft:
		return []codedoc.CDState{codedoc.CDDiscovery, codedoc.CDHumanGateScope, codedoc.CDDrafting, codedoc.CDSanitising, codedoc.CDHumanGateDraft}
	case codedoc.CDHumanGateFinal:
		return []codedoc.CDState{codedoc.CDDiscovery, codedoc.CDHumanGateScope, codedoc.CDDrafting, codedoc.CDSanitising, codedoc.CDHumanGateDraft}
	case codedoc.CDEscalated:
		return []codedoc.CDState{codedoc.CDDiscovery, codedoc.CDEscalated}
	case codedoc.CDComplete:
		return []codedoc.CDState{codedoc.CDDiscovery, codedoc.CDHumanGateScope, codedoc.CDDrafting, codedoc.CDSanitising, codedoc.CDHumanGateDraft}
	case codedoc.CDError:
		return []codedoc.CDState{codedoc.CDDiscovery, codedoc.CDError}
	}
	return nil
}

// ---------------------------------------------------------------------------
// POST /api/codedoc/start
// ---------------------------------------------------------------------------

func TestCodedocHandlers_Start_ValidRequest(t *testing.T) {
	// Use os.MkdirTemp to avoid t.TempDir() cleanup issues with background goroutines.
	workspaceDir, err := os.MkdirTemp("", "codedoc-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(workspaceDir)

	cfg := codedoc.DefaultCodedocConfig()
	manager := NewCodedocManager(workspaceDir, cfg)
	manager.SetRunners(noopAgentRunner{}, noopAgentRunner{}, noopAgentRunner{})

	codePath, err := os.MkdirTemp("", "codedoc-code-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(codePath)

	handler := HandleCDStart(manager)
	rr := postJSON(handler, "/api/codedoc/start", cdStartRequest{
		FeatureName: "test-docs",
		CodePath:    codePath,
		Mode:        "full",
	})

	if rr.Code != http.StatusAccepted {
		t.Errorf("expected 202, got %d: %s", rr.Code, rr.Body.String())
	}

	body := decodeBody(t, rr)
	if body["status"] != "started" {
		t.Errorf("expected status 'started', got %v", body["status"])
	}
	if body["feature_name"] != "test-docs" {
		t.Errorf("expected feature_name 'test-docs', got %v", body["feature_name"])
	}
	if body["mode"] != "full" {
		t.Errorf("expected mode 'full', got %v", body["mode"])
	}
}

func TestCodedocHandlers_Start_MissingCodePath(t *testing.T) {
	manager, _ := newTestCDManager(t)
	handler := HandleCDStart(manager)
	rr := postJSON(handler, "/api/codedoc/start", cdStartRequest{
		FeatureName: "test-docs",
	})

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
	body := decodeBody(t, rr)
	if !strings.Contains(body["error"].(string), "code_path is required") {
		t.Errorf("expected error about code_path, got: %v", body["error"])
	}
}

func TestCodedocHandlers_Start_MissingFeatureName(t *testing.T) {
	manager, _ := newTestCDManager(t)
	handler := HandleCDStart(manager)
	rr := postJSON(handler, "/api/codedoc/start", cdStartRequest{
		CodePath: "/tmp/test",
	})

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestCodedocHandlers_Start_InvalidFeatureName(t *testing.T) {
	manager, _ := newTestCDManager(t)
	handler := HandleCDStart(manager)
	rr := postJSON(handler, "/api/codedoc/start", cdStartRequest{
		FeatureName: "../escape",
		CodePath:    t.TempDir(),
	})

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
	if len(manager.GetAllOrchestrators()) != 0 {
		t.Fatal("unexpected codedoc orchestrator created for invalid feature name")
	}
	if _, err := os.Stat(filepath.Join(manager.workspaceDir, "codedoc")); !os.IsNotExist(err) {
		t.Fatal("unexpected codedoc workspace created for invalid feature name")
	}
}

func TestCodedocHandlers_Start_FileCodePath(t *testing.T) {
	manager, _ := newTestCDManager(t)
	handler := HandleCDStart(manager)
	filePath := filepath.Join(t.TempDir(), "code.txt")
	if err := os.WriteFile(filePath, []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	rr := postJSON(handler, "/api/codedoc/start", cdStartRequest{
		FeatureName: "test-docs",
		CodePath:    filePath,
	})

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestCodedocHandlers_Start_RejectsWorkspaceOverride(t *testing.T) {
	manager, _ := newTestCDManager(t)
	handler := HandleCDStart(manager)
	rr := postJSON(handler, "/api/codedoc/start", cdStartRequest{
		FeatureName:  "test-docs",
		CodePath:     t.TempDir(),
		WorkspaceDir: t.TempDir(),
	})

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
	body := decodeBody(t, rr)
	if !strings.Contains(body["error"].(string), "workspace_dir overrides are not supported") {
		t.Fatalf("unexpected error: %v", body["error"])
	}
}

func TestCodedocHandlers_Start_DuplicateFeature(t *testing.T) {
	manager, _ := setupCDWithOrchestrator(t, "existing", codedoc.CDInit)
	handler := HandleCDStart(manager)
	rr := postJSON(handler, "/api/codedoc/start", cdStartRequest{
		FeatureName: "existing",
		CodePath:    t.TempDir(),
		Mode:        "full",
	})

	if rr.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d", rr.Code)
	}
}

func TestCodedocHandlers_Start_PersistsStateBeforeReturn(t *testing.T) {
	workspaceDir, err := os.MkdirTemp("", "codedoc-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(workspaceDir)

	cfg := codedoc.DefaultCodedocConfig()
	manager := NewCodedocManager(workspaceDir, cfg)
	manager.SetRunners(noopAgentRunner{}, noopAgentRunner{}, noopAgentRunner{})

	codePath := t.TempDir()
	handler := HandleCDStart(manager)
	rr := postJSON(handler, "/api/codedoc/start", cdStartRequest{
		FeatureName: "durable-start",
		CodePath:    codePath,
		Mode:        "full",
	})

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rr.Code, rr.Body.String())
	}

	statePath := filepath.Join(workspaceDir, "codedoc", "durable-start", "workflow-state.json")
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("expected persisted workflow state: %v", err)
	}
}

func TestCodedocHandlers_Start_ExistingPersistedStateConflicts(t *testing.T) {
	manager, workspaceDir := newTestCDManager(t)
	featureDir := filepath.Join(workspaceDir, "codedoc", "existing")
	if err := os.MkdirAll(featureDir, 0o755); err != nil {
		t.Fatal(err)
	}
	state := &codedoc.CDStateJSON{
		State:       codedoc.CDHumanGateScope,
		FeatureName: "existing",
		CodePath:    t.TempDir(),
		Mode:        "full",
	}
	data, _ := json.MarshalIndent(state, "", "  ")
	if err := os.WriteFile(filepath.Join(featureDir, "workflow-state.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	handler := HandleCDStart(manager)
	rr := postJSON(handler, "/api/codedoc/start", cdStartRequest{
		FeatureName: "existing",
		CodePath:    t.TempDir(),
		Mode:        "full",
	})

	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rr.Code, rr.Body.String())
	}
	body := decodeBody(t, rr)
	if body["resume_state"] != "CD_HUMAN_GATE_SCOPE" {
		t.Fatalf("expected resume_state CD_HUMAN_GATE_SCOPE, got %v", body["resume_state"])
	}
}

func TestCodedocHandlers_Start_DefaultsToFullMode(t *testing.T) {
	// Use os.MkdirTemp to avoid t.TempDir() cleanup issues with background goroutines.
	workspaceDir, err := os.MkdirTemp("", "codedoc-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(workspaceDir)

	cfg := codedoc.DefaultCodedocConfig()
	manager := NewCodedocManager(workspaceDir, cfg)
	manager.SetRunners(noopAgentRunner{}, noopAgentRunner{}, noopAgentRunner{})

	codePath, err := os.MkdirTemp("", "codedoc-code-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(codePath)

	handler := HandleCDStart(manager)
	rr := postJSON(handler, "/api/codedoc/start", cdStartRequest{
		FeatureName: "test-docs",
		CodePath:    codePath,
	})

	if rr.Code != http.StatusAccepted {
		t.Errorf("expected 202, got %d: %s", rr.Code, rr.Body.String())
	}
	body := decodeBody(t, rr)
	if body["mode"] != "full" {
		t.Errorf("expected mode 'full' by default, got %v", body["mode"])
	}
}

func TestCodedocHandlers_Start_InvalidMode(t *testing.T) {
	manager, _ := newTestCDManager(t)
	handler := HandleCDStart(manager)
	rr := postJSON(handler, "/api/codedoc/start", cdStartRequest{
		FeatureName: "test-docs",
		CodePath:    t.TempDir(),
		Mode:        "partial",
	})

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// GET /api/codedoc/{feature}/status
// ---------------------------------------------------------------------------

func TestCodedocHandlers_Status_ReturnsData(t *testing.T) {
	manager, _ := setupCDWithOrchestrator(t, "status-feature", codedoc.CDHumanGateScope)
	handler := HandleCDStatus(manager)
	rr := getJSON(handler, "/api/codedoc/status-feature/status")

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	body := decodeBody(t, rr)
	if body["state"] == nil {
		t.Error("expected state in response")
	}
	if body["round"] == nil {
		t.Error("expected round in response")
	}
	if body["mode"] == nil {
		t.Error("expected mode in response")
	}
	if body["cost_usd"] == nil {
		t.Error("expected cost_usd in response")
	}
}

func TestCodedocHandlers_Status_NotFound(t *testing.T) {
	manager, _ := newTestCDManager(t)
	handler := HandleCDStatus(manager)
	rr := getJSON(handler, "/api/codedoc/nonexistent/status")

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rr.Code)
	}
}

func TestCodedocHandlers_Status_FromDisk(t *testing.T) {
	manager, workspaceDir := newTestCDManager(t)

	// Write a persisted state to disk.
	featureDir := filepath.Join(workspaceDir, "codedoc", "disk-feature")
	os.MkdirAll(featureDir, 0o755)
	state := &codedoc.CDStateJSON{
		State:       codedoc.CDHumanGateScope,
		FeatureName: "disk-feature",
		Mode:        "full",
		Round:       1,
	}
	data, _ := json.MarshalIndent(state, "", "  ")
	os.WriteFile(filepath.Join(featureDir, "workflow-state.json"), data, 0o644)

	handler := HandleCDStatus(manager)
	rr := getJSON(handler, "/api/codedoc/disk-feature/status")

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	body := decodeBody(t, rr)
	if body["state"] != "CD_HUMAN_GATE_SCOPE" {
		t.Errorf("expected CD_HUMAN_GATE_SCOPE, got %v", body["state"])
	}
}

// ---------------------------------------------------------------------------
// POST /api/codedoc/{feature}/gate
// ---------------------------------------------------------------------------

func TestCodedocHandlers_Gate_ScopeConfirm(t *testing.T) {
	manager, _ := setupCDWithOrchestrator(t, "gate-feature", codedoc.CDHumanGateScope)
	handler := HandleCDGate(manager)
	rr := postJSON(handler, "/api/codedoc/gate-feature/gate", cdGateRequest{
		Action: "confirm",
	})

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	body := decodeBody(t, rr)
	if body["status"] != "transitioned" {
		t.Errorf("expected status 'transitioned', got %v", body["status"])
	}
	if body["new_state"] != "CD_DRAFTING" {
		t.Errorf("expected new_state CD_DRAFTING, got %v", body["new_state"])
	}
	time.Sleep(100 * time.Millisecond)
}

func TestCodedocHandlers_Gate_NotInGateState(t *testing.T) {
	manager, _ := setupCDWithOrchestrator(t, "gate-feature", codedoc.CDDiscovery)
	handler := HandleCDGate(manager)
	rr := postJSON(handler, "/api/codedoc/gate-feature/gate", cdGateRequest{
		Action: "confirm",
	})

	if rr.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestCodedocHandlers_Gate_UnknownFeature(t *testing.T) {
	manager, _ := newTestCDManager(t)
	handler := HandleCDGate(manager)
	rr := postJSON(handler, "/api/codedoc/unknown/gate", cdGateRequest{
		Action: "confirm",
	})

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rr.Code)
	}
}

func TestCodedocHandlers_Gate_DraftApprove(t *testing.T) {
	manager, _ := setupCDWithOrchestrator(t, "draft-gate", codedoc.CDHumanGateDraft)

	// Set round to 1 so the reviewing transition works.
	orch := manager.getOrchestrator("draft-gate")
	orch.State().Round = 1

	handler := HandleCDGate(manager)
	rr := postJSON(handler, "/api/codedoc/draft-gate/gate", cdGateRequest{
		Action: "approve",
	})

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	body := decodeBody(t, rr)
	if body["new_state"] != "CD_REVIEWING" {
		t.Errorf("expected CD_REVIEWING, got %v", body["new_state"])
	}
	time.Sleep(100 * time.Millisecond)
}

func TestCodedocHandlers_Gate_ScopeAction_FromDisk(t *testing.T) {
	manager, workspaceDir := newTestCDManager(t)
	featureDir := filepath.Join(workspaceDir, "codedoc", "disk-scope")
	if err := os.MkdirAll(featureDir, 0o755); err != nil {
		t.Fatal(err)
	}
	state := &codedoc.CDStateJSON{
		State:       codedoc.CDHumanGateScope,
		FeatureName: "disk-scope",
		CodePath:    t.TempDir(),
		Mode:        "full",
	}
	data, _ := json.MarshalIndent(state, "", "  ")
	if err := os.WriteFile(filepath.Join(featureDir, "workflow-state.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	handler := HandleCDGate(manager)
	rr := postJSON(handler, "/api/codedoc/disk-scope/gate", cdGateRequest{Action: "cancel"})

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	body := decodeBody(t, rr)
	if body["new_state"] != "CD_ESCALATED" {
		t.Fatalf("expected CD_ESCALATED, got %v", body["new_state"])
	}
	if manager.getOrchestrator("disk-scope") == nil {
		t.Fatal("expected gate handler to restore orchestrator from disk")
	}
}

func TestCodedocHandlers_Gate_FinalAction_FromDisk(t *testing.T) {
	manager, workspaceDir := newTestCDManager(t)
	featureDir := filepath.Join(workspaceDir, "codedoc", "disk-final")
	if err := os.MkdirAll(featureDir, 0o755); err != nil {
		t.Fatal(err)
	}
	state := &codedoc.CDStateJSON{
		State:       codedoc.CDHumanGateFinal,
		FeatureName: "disk-final",
		CodePath:    t.TempDir(),
		Mode:        "full",
		Round:       1,
	}
	data, _ := json.MarshalIndent(state, "", "  ")
	if err := os.WriteFile(filepath.Join(featureDir, "workflow-state.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	handler := HandleCDGate(manager)
	rr := postJSON(handler, "/api/codedoc/disk-final/gate", cdGateRequest{Action: "reject"})

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	body := decodeBody(t, rr)
	if body["new_state"] != "CD_ESCALATED" {
		t.Fatalf("expected CD_ESCALATED, got %v", body["new_state"])
	}
	if manager.getOrchestrator("disk-final") == nil {
		t.Fatal("expected gate handler to restore final-gate orchestrator from disk")
	}
}

// ---------------------------------------------------------------------------
// POST /api/codedoc/{feature}/cancel
// ---------------------------------------------------------------------------

func TestCodedocHandlers_Cancel_RunningWorkflow(t *testing.T) {
	manager, _ := setupCDWithOrchestrator(t, "cancel-feature", codedoc.CDDiscovery)
	handler := HandleCDCancel(manager)
	rr := postJSON(handler, "/api/codedoc/cancel-feature/cancel", nil)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	orch := manager.getOrchestrator("cancel-feature")
	if orch.StateMachine().Current() != codedoc.CDEscalated {
		t.Errorf("expected CD_ESCALATED, got %s", orch.StateMachine().Current())
	}
}

func TestCodedocHandlers_Cancel_UnknownFeature(t *testing.T) {
	manager, _ := newTestCDManager(t)
	handler := HandleCDCancel(manager)
	rr := postJSON(handler, "/api/codedoc/unknown/cancel", nil)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// POST /api/codedoc/{feature}/resume
// ---------------------------------------------------------------------------

func TestCodedocHandlers_Resume_FromError(t *testing.T) {
	manager, _ := setupCDWithOrchestrator(t, "resume-feature", codedoc.CDError)
	handler := HandleCDResume(manager)
	rr := postJSON(handler, "/api/codedoc/resume-feature/resume", nil)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	body := decodeBody(t, rr)
	if body["status"] != "resumed" {
		t.Errorf("expected status 'resumed', got %v", body["status"])
	}
}

func TestCodedocHandlers_Resume_NotInError(t *testing.T) {
	manager, _ := setupCDWithOrchestrator(t, "resume-feature", codedoc.CDHumanGateScope)
	handler := HandleCDResume(manager)
	rr := postJSON(handler, "/api/codedoc/resume-feature/resume", nil)

	if rr.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestCodedocHandlers_Resume_UnknownFeature(t *testing.T) {
	manager, _ := newTestCDManager(t)
	handler := HandleCDResume(manager)
	rr := postJSON(handler, "/api/codedoc/unknown/resume", nil)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// POST /api/codedoc/{feature}/reset
// ---------------------------------------------------------------------------

func TestCodedocHandlers_Reset_TerminalWorkflow(t *testing.T) {
	manager, workspaceDir := setupCDWithOrchestrator(t, "reset-feature", codedoc.CDComplete)

	// Write persisted state to disk.
	featureDir := filepath.Join(workspaceDir, "codedoc", "reset-feature")
	os.MkdirAll(featureDir, 0o755)
	state := &codedoc.CDStateJSON{State: codedoc.CDComplete, FeatureName: "reset-feature"}
	data, _ := json.MarshalIndent(state, "", "  ")
	os.WriteFile(filepath.Join(featureDir, "workflow-state.json"), data, 0o644)

	handler := HandleCDReset(manager)
	rr := postJSON(handler, "/api/codedoc/reset-feature/reset", nil)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// Verify directory was deleted.
	if _, err := os.Stat(featureDir); !os.IsNotExist(err) {
		t.Error("expected feature directory to be deleted")
	}
}

func TestCodedocHandlers_Reset_StillRunning(t *testing.T) {
	manager, _ := setupCDWithOrchestrator(t, "running-feature", codedoc.CDDiscovery)
	handler := HandleCDReset(manager)
	rr := postJSON(handler, "/api/codedoc/running-feature/reset", nil)

	if rr.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestCodedocHandlers_Reset_NotFound(t *testing.T) {
	manager, _ := newTestCDManager(t)
	handler := HandleCDReset(manager)
	rr := postJSON(handler, "/api/codedoc/unknown/reset", nil)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// POST /api/codedoc/{feature}/rewind
// ---------------------------------------------------------------------------

func TestCodedocHandlers_Rewind_ValidState(t *testing.T) {
	manager, workspaceDir := setupCDWithOrchestrator(t, "rewind-feature", codedoc.CDReviewing)

	// Ensure feature directory exists for persist.
	featureDir := filepath.Join(workspaceDir, "codedoc", "rewind-feature")
	os.MkdirAll(featureDir, 0o755)

	handler := HandleCDRewind(manager)
	rr := postJSON(handler, "/api/codedoc/rewind-feature/rewind", cdRewindRequest{
		TargetState: "CD_DRAFTING",
	})

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	body := decodeBody(t, rr)
	if body["new_state"] != "CD_DRAFTING" {
		t.Errorf("expected CD_DRAFTING, got %v", body["new_state"])
	}
}

func TestCodedocHandlers_Rewind_UnknownState(t *testing.T) {
	manager, _ := setupCDWithOrchestrator(t, "rewind-feature", codedoc.CDReviewing)
	handler := HandleCDRewind(manager)
	rr := postJSON(handler, "/api/codedoc/rewind-feature/rewind", cdRewindRequest{
		TargetState: "INVALID_STATE",
	})

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestCodedocHandlers_Rewind_MissingState(t *testing.T) {
	manager, _ := setupCDWithOrchestrator(t, "rewind-feature", codedoc.CDReviewing)
	handler := HandleCDRewind(manager)
	rr := postJSON(handler, "/api/codedoc/rewind-feature/rewind", cdRewindRequest{})

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestCodedocHandlers_Rewind_NotFound(t *testing.T) {
	manager, _ := newTestCDManager(t)
	handler := HandleCDRewind(manager)
	rr := postJSON(handler, "/api/codedoc/unknown/rewind", cdRewindRequest{
		TargetState: "CD_DRAFTING",
	})

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// extractCDFeature
// ---------------------------------------------------------------------------

func TestCodedocHandlers_ExtractFeature(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"/api/codedoc/my-feature/status", "my-feature"},
		{"/api/codedoc/my-feature/gate", "my-feature"},
		{"/api/codedoc/my-feature/cancel", "my-feature"},
		{"/api/codedoc/", ""},
		{"/api/other/my-feature/status", ""},
	}

	for _, tt := range tests {
		got := extractCDFeature(tt.path)
		if got != tt.expected {
			t.Errorf("extractCDFeature(%q) = %q, want %q", tt.path, got, tt.expected)
		}
	}
}
