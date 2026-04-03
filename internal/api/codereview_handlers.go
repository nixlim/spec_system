package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/foundry-zero/adversarial-spec-system/internal/codereview"
	"github.com/foundry-zero/adversarial-spec-system/internal/specworkflow"
)

// ---------------------------------------------------------------------------
// CodeReviewManager
// ---------------------------------------------------------------------------

// CodeReviewManager coordinates the lifecycle of concurrent code review
// workflows. It owns a map of CodeReviewOrchestrators keyed by feature name,
// protected by a separate sync.RWMutex (not shared with WorkflowManager).
type CodeReviewManager struct {
	orchestrators map[string]*codereview.CodeReviewOrchestrator
	workspaceDir  string
	config        codereview.CodeReviewConfig
	runner        specworkflow.AgentRunner
	codexRunner   specworkflow.AgentRunner
	fixRunner     specworkflow.AgentRunner
	emitter       specworkflow.EventEmitter
	auditLogger   *codereview.CRAuditLogger
	otelPort      int
	mu            sync.RWMutex
}

// NewCodeReviewManager creates a CodeReviewManager with the given dependencies.
func NewCodeReviewManager(workspaceDir string, config codereview.CodeReviewConfig) *CodeReviewManager {
	return &CodeReviewManager{
		orchestrators: make(map[string]*codereview.CodeReviewOrchestrator),
		workspaceDir:  workspaceDir,
		config:        config,
	}
}

// SetRunners configures the agent runners used by code review orchestrators.
func (m *CodeReviewManager) SetRunners(runner, codexRunner, fixRunner specworkflow.AgentRunner) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runner = runner
	m.codexRunner = codexRunner
	m.fixRunner = fixRunner
}

// SetEmitter configures the event emitter used by code review orchestrators.
func (m *CodeReviewManager) SetEmitter(emitter specworkflow.EventEmitter) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.emitter = emitter
}

// SetAuditLogger configures the JSONL audit logger for code review workflows.
func (m *CodeReviewManager) SetAuditLogger(logger *codereview.CRAuditLogger) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.auditLogger = logger
}

// SetOTELPort configures the OTLP receiver port for child process telemetry.
func (m *CodeReviewManager) SetOTELPort(port int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.otelPort = port
}

// getOrchestrator returns the orchestrator for the given feature name, or nil.
func (m *CodeReviewManager) getOrchestrator(featureName string) *codereview.CodeReviewOrchestrator {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.orchestrators[featureName]
}

// GetAllOrchestrators returns a snapshot of all active orchestrators
// as a map keyed by feature name.
func (m *CodeReviewManager) GetAllOrchestrators() map[string]*codereview.CodeReviewOrchestrator {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]*codereview.CodeReviewOrchestrator, len(m.orchestrators))
	for k, v := range m.orchestrators {
		result[k] = v
	}
	return result
}

// IsFeatureRunning returns true if the given feature has a running orchestrator.
func (m *CodeReviewManager) IsFeatureRunning(featureName string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	orch, ok := m.orchestrators[featureName]
	return ok && orch != nil && orch.IsRunning()
}

// ---------------------------------------------------------------------------
// extractCRFeature
// ---------------------------------------------------------------------------

// extractCRFeature extracts the feature name from a URL path like
// /api/codereview/{feature_name}/gate.
func extractCRFeature(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	// Expected: ["api", "codereview", "{feature}", "{action}"]
	if len(parts) >= 3 && parts[0] == "api" && parts[1] == "codereview" {
		return parts[2]
	}
	return ""
}

// ---------------------------------------------------------------------------
// Request types
// ---------------------------------------------------------------------------

// crStartRequest is the JSON body for POST /api/codereview/start.
type crStartRequest struct {
	CodePath     string `json:"code_path"`
	FeatureName  string `json:"feature_name"`
	SpecPath     string `json:"spec_path,omitempty"`
	TaskListPath string `json:"task_list_path,omitempty"`
	WorkspaceDir string `json:"workspace_dir,omitempty"`
}

// crGateRequest is the JSON body for POST /api/codereview/{feature}/gate.
type crGateRequest struct {
	Action  string `json:"action"`
	Comment string `json:"comment,omitempty"`
}

// ---------------------------------------------------------------------------
// POST /api/codereview/start
// ---------------------------------------------------------------------------

// HandleCRStart returns an HTTP handler that starts a new code review workflow.
// Returns 200 on success, 400 for invalid input, 409 if already exists.
func HandleCRStart(manager *CodeReviewManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req crStartRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
			return
		}

		if req.CodePath == "" {
			writeError(w, http.StatusBadRequest, "code_path is required")
			return
		}
		if req.FeatureName == "" {
			writeError(w, http.StatusBadRequest, "feature_name is required")
			return
		}
		if err := ValidateFeatureName(req.FeatureName); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid feature_name: %v", err))
			return
		}
		if err := validateExistingDirectory("code_path", req.CodePath); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if req.WorkspaceDir != "" {
			writeError(w, http.StatusBadRequest, "workspace_dir overrides are not supported for code review workflows")
			return
		}

		// Check for duplicate feature name.
		manager.mu.Lock()
		if _, exists := manager.orchestrators[req.FeatureName]; exists {
			manager.mu.Unlock()
			writeError(w, http.StatusConflict, fmt.Sprintf("code review already exists for feature: %s", req.FeatureName))
			return
		}

		// Create orchestrator with runners and emitter.
		orch := codereview.NewCodeReviewOrchestrator(codereview.CROrchestratorConfig{
			WorkspaceDir: manager.workspaceDir,
			Config:       manager.config,
			Runner:       manager.runner,
			CodexRunner:  manager.codexRunner,
			FixRunner:    manager.fixRunner,
			Emitter:      manager.emitter,
			AuditLogger:  manager.auditLogger,
			OTELPort:     manager.otelPort,
		})

		// Start the workflow (validates inputs, creates workspace, snapshots git).
		err := orch.Start(codereview.StartCodeReviewRequest{
			CodePath:     req.CodePath,
			FeatureName:  req.FeatureName,
			SpecPath:     req.SpecPath,
			TaskListPath: req.TaskListPath,
		})
		if err != nil {
			manager.mu.Unlock()
			writeError(w, http.StatusBadRequest, fmt.Sprintf("start failed: %v", err))
			return
		}

		manager.orchestrators[req.FeatureName] = orch
		manager.mu.Unlock()

		log.Printf("[codereview] started code review for %q", req.FeatureName)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":       "started",
			"feature_name": req.FeatureName,
			"state":        orch.StateMachine().Current().String(),
		})
	}
}

// ---------------------------------------------------------------------------
// POST /api/codereview/{feature}/gate
// ---------------------------------------------------------------------------

// HandleCRGate returns an HTTP handler for responding to human gates.
// Returns 200 on success, 400 for invalid action, 404 if feature not found,
// 409 if workflow is not in a gate state.
func HandleCRGate(manager *CodeReviewManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		featureName := extractCRFeature(r.URL.Path)
		if featureName == "" {
			writeError(w, http.StatusBadRequest, "feature_name is required in URL path")
			return
		}

		orch := manager.getOrchestrator(featureName)
		if orch == nil {
			writeError(w, http.StatusNotFound, fmt.Sprintf("no code review found for feature: %s", featureName))
			return
		}

		var req crGateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
			return
		}

		sm := orch.StateMachine()
		if sm == nil {
			writeError(w, http.StatusConflict, "orchestrator not initialized")
			return
		}

		currentState := sm.Current()
		gateResp := codereview.CRGateResponse{
			Action:  req.Action,
			Comment: req.Comment,
		}

		var err error
		switch currentState {
		case codereview.CRHumanGateScope:
			err = orch.HandleScopeGate(gateResp)
		case codereview.CRHumanGateFixes:
			err = orch.HandleFixesGate(gateResp)
		default:
			writeError(w, http.StatusConflict, fmt.Sprintf("workflow is not in a gate state (current state: %s)", currentState))
			return
		}

		if err != nil {
			// Distinguish between invalid action (400) and transition failures (409).
			if strings.Contains(err.Error(), "invalid gate action") {
				writeError(w, http.StatusBadRequest, err.Error())
			} else {
				writeError(w, http.StatusConflict, err.Error())
			}
			return
		}

		newState := sm.Current()

		// If the gate response moves the workflow to an actionable state
		// (CR_REVIEWING, CR_FIXING), launch Run() in a goroutine to drive
		// the workflow forward automatically.
		if newState == codereview.CRReviewing || newState == codereview.CRFixing {
			go func() {
				if err := orch.Run(); err != nil {
					log.Printf("[codereview] workflow run error for %q: %v", featureName, err)
				}
			}()
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":    "transitioned",
			"new_state": newState.String(),
		})
	}
}

// ---------------------------------------------------------------------------
// GET /api/codereview/{feature}/status
// ---------------------------------------------------------------------------

// HandleCRStatus returns an HTTP handler for getting code review status.
// Returns 200 with status data, 404 if feature not found.
func HandleCRStatus(manager *CodeReviewManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		featureName := extractCRFeature(r.URL.Path)
		if featureName == "" {
			writeError(w, http.StatusBadRequest, "feature_name is required in URL path")
			return
		}

		orch := manager.getOrchestrator(featureName)
		if orch == nil {
			// Try loading from disk.
			featureDir := filepath.Join(manager.workspaceDir, "code-reviews", featureName)
			diskState, err := codereview.LoadCRState(featureDir)
			if err != nil {
				writeError(w, http.StatusNotFound, fmt.Sprintf("no code review found for feature: %s", featureName))
				return
			}
			writeJSON(w, http.StatusOK, buildCRStatusResponse(diskState))
			return
		}

		sm := orch.StateMachine()
		if sm == nil {
			writeError(w, http.StatusNotFound, fmt.Sprintf("no code review found for feature: %s", featureName))
			return
		}

		writeJSON(w, http.StatusOK, buildCRStatusResponse(sm.State()))
	}
}

// buildCRStatusResponse constructs the status response from workflow state.
func buildCRStatusResponse(state *codereview.CodeReviewStateJSON) map[string]interface{} {
	return map[string]interface{}{
		"state":              state.State.String(),
		"round":              state.Round,
		"cost_usd":           state.CumulativeCostUSD,
		"wall_clock_minutes": state.CumulativeWallClockSeconds / 60.0,
		"findings_summary":   state.FindingsSummary,
		"active_agents":      []string{}, // populated by orchestrator when running
	}
}

// ---------------------------------------------------------------------------
// POST /api/codereview/{feature}/cancel
// ---------------------------------------------------------------------------

// HandleCRCancel returns an HTTP handler for cancelling a code review.
// Returns 200 on success, 404 if feature not found.
func HandleCRCancel(manager *CodeReviewManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		featureName := extractCRFeature(r.URL.Path)
		if featureName == "" {
			writeError(w, http.StatusBadRequest, "feature_name is required in URL path")
			return
		}

		orch := manager.getOrchestrator(featureName)
		if orch == nil {
			writeError(w, http.StatusNotFound, fmt.Sprintf("no code review found for feature: %s", featureName))
			return
		}

		// Use Cancel() to set the atomic cancellation flag and terminate agents.
		if err := orch.Cancel(); err != nil {
			log.Printf("[codereview] cancel warning for %q: %v", featureName, err)
		}

		log.Printf("[codereview] cancelled code review for %q", featureName)
		writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
	}
}

// ---------------------------------------------------------------------------
// POST /api/codereview/{feature}/resume
// ---------------------------------------------------------------------------

// HandleCRResume returns an HTTP handler for resuming a code review from
// persisted state. Returns 200 on success, 404 if no persisted state found.
func HandleCRResume(manager *CodeReviewManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		featureName := extractCRFeature(r.URL.Path)
		if featureName == "" {
			writeError(w, http.StatusBadRequest, "feature_name is required in URL path")
			return
		}

		// Load persisted state from disk.
		featureDir := filepath.Join(manager.workspaceDir, "code-reviews", featureName)
		state, err := codereview.LoadCRState(featureDir)
		if err != nil {
			writeError(w, http.StatusNotFound, fmt.Sprintf("no persisted state found for feature: %s", featureName))
			return
		}

		manager.mu.Lock()
		// Create a new orchestrator and restore persisted state into it.
		orch := codereview.NewCodeReviewOrchestrator(codereview.CROrchestratorConfig{
			WorkspaceDir: manager.workspaceDir,
			Config:       manager.config,
			Runner:       manager.runner,
			CodexRunner:  manager.codexRunner,
			FixRunner:    manager.fixRunner,
			Emitter:      manager.emitter,
			AuditLogger:  manager.auditLogger,
			OTELPort:     manager.otelPort,
		})
		orch.RestoreFromState(state)
		manager.orchestrators[featureName] = orch
		manager.mu.Unlock()

		// Invoke crash recovery to determine what action to take.
		recoveryAction, recoveryErr := codereview.RecoverFromCrash(
			orch.FeatureDir(), state,
			manager.codexRunner != nil,
			nil, // use default git status checker
		)
		if recoveryErr != nil {
			log.Printf("[codereview] recovery error for %q: %v", featureName, recoveryErr)
		}

		// If recovery suggests continuing (e.g., re-dispatch agents), launch Run().
		if recoveryAction != nil && !state.State.IsTerminal() && !state.State.IsGate() {
			go func() {
				if err := orch.Run(); err != nil {
					log.Printf("[codereview] resumed workflow run error for %q: %v", featureName, err)
				}
			}()
		}

		log.Printf("[codereview] resumed code review for %q from state %s", featureName, state.State)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":       "resumed",
			"feature_name": featureName,
			"state":        state.State.String(),
		})
	}
}

// ---------------------------------------------------------------------------
// POST /api/codereview/{feature}/reset
// ---------------------------------------------------------------------------

// HandleCRReset returns an HTTP handler for resetting (deleting workspace of)
// a completed code review. Returns 200 on success, 404 if not found, 409 if
// the workflow is still running (non-terminal state).
func HandleCRReset(manager *CodeReviewManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		featureName := extractCRFeature(r.URL.Path)
		if featureName == "" {
			writeError(w, http.StatusBadRequest, "feature_name is required in URL path")
			return
		}

		manager.mu.Lock()
		defer manager.mu.Unlock()

		// Check in-memory orchestrator.
		if orch, exists := manager.orchestrators[featureName]; exists && orch != nil {
			sm := orch.StateMachine()
			if sm != nil && !sm.IsTerminal() {
				writeError(w, http.StatusConflict, fmt.Sprintf("code review for %s is still running (state: %s)", featureName, sm.Current()))
				return
			}
			delete(manager.orchestrators, featureName)
		}

		// Check on-disk state.
		featureDir := filepath.Join(manager.workspaceDir, "code-reviews", featureName)
		state, err := codereview.LoadCRState(featureDir)
		if err != nil {
			writeError(w, http.StatusNotFound, fmt.Sprintf("no code review found for feature: %s", featureName))
			return
		}

		// Only allow reset for terminal states.
		if state.State != codereview.CRComplete && state.State != codereview.CREscalated {
			writeError(w, http.StatusConflict, fmt.Sprintf("code review for %s is not in a terminal state (state: %s)", featureName, state.State))
			return
		}

		// Delete workspace directory.
		if err := os.RemoveAll(featureDir); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to delete workspace: %v", err))
			return
		}

		if manager.auditLogger != nil {
			manager.auditLogger.LogCodeReviewReset(featureName)
		}
		log.Printf("[codereview] reset code review workspace for %q", featureName)
		writeJSON(w, http.StatusOK, map[string]string{"status": "reset"})
	}
}
