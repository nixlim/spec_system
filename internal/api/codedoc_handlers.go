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

	"github.com/foundry-zero/adversarial-spec-system/internal/codedoc"
	"github.com/foundry-zero/adversarial-spec-system/internal/specworkflow"
)

// ---------------------------------------------------------------------------
// CodedocManager
// ---------------------------------------------------------------------------

// CodedocManager coordinates the lifecycle of concurrent codedoc workflows.
// It owns a map of CodedocOrchestrators keyed by feature name, protected by
// a sync.RWMutex.
type CodedocManager struct {
	orchestrators map[string]*codedoc.CodedocOrchestrator
	workspaceDir  string
	config        codedoc.CodedocConfig
	runner        specworkflow.AgentRunner
	codexRunner   specworkflow.AgentRunner
	mergeRunner   specworkflow.AgentRunner
	emitter       codedoc.CDEventEmitter
	mu            sync.RWMutex
}

// NewCodedocManager creates a CodedocManager with the given dependencies.
func NewCodedocManager(workspaceDir string, config codedoc.CodedocConfig) *CodedocManager {
	return &CodedocManager{
		orchestrators: make(map[string]*codedoc.CodedocOrchestrator),
		workspaceDir:  workspaceDir,
		config:        config,
	}
}

// SetRunners configures the agent runners used by codedoc orchestrators.
func (m *CodedocManager) SetRunners(runner, codexRunner, mergeRunner specworkflow.AgentRunner) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runner = runner
	m.codexRunner = codexRunner
	m.mergeRunner = mergeRunner
}

// SetEmitter configures the event emitter used by codedoc orchestrators.
func (m *CodedocManager) SetEmitter(emitter codedoc.CDEventEmitter) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.emitter = emitter
}

// CDEmitterAdapter adapts the specworkflow EventEmitter to the codedoc CDEventEmitter interface.
type CDEmitterAdapter struct {
	Emitter specworkflow.EventEmitter
}

// Emit forwards a codedoc event to the specworkflow EventEmitter.
func (a *CDEmitterAdapter) Emit(event codedoc.CDEvent) {
	if a.Emitter == nil {
		return
	}
	_ = a.Emitter.Emit(specworkflow.EventEnvelope{
		Event:       event.Type,
		FeatureName: event.Feature,
		Data:        event,
	})
}

// getOrchestrator returns the orchestrator for the given feature name, or nil.
func (m *CodedocManager) getOrchestrator(featureName string) *codedoc.CodedocOrchestrator {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.orchestrators[featureName]
}

func (m *CodedocManager) loadOrchestratorFromDisk(featureName string) (*codedoc.CodedocOrchestrator, *codedoc.CDStateJSON, error) {
	featureDir := filepath.Join(m.workspaceDir, "codedoc", featureName)
	state, err := codedoc.LoadCDState(featureDir)
	if err != nil {
		return nil, nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if orch := m.orchestrators[featureName]; orch != nil {
		return orch, orch.State(), nil
	}

	orch := codedoc.NewCodedocOrchestrator(codedoc.CodedocOrchestratorConfig{
		WorkspaceDir: m.workspaceDir,
		FeatureName:  featureName,
		CodePath:     state.CodePath,
		Mode:         state.Mode,
		Config:       m.config,
		Runner:       m.runner,
		CodexRunner:  m.codexRunner,
		MergeRunner:  m.mergeRunner,
		Emitter:      m.emitter,
	})
	orch.RestoreFromState(state)
	m.orchestrators[featureName] = orch
	return orch, state, nil
}

// GetAllOrchestrators returns a snapshot of all active orchestrators.
func (m *CodedocManager) GetAllOrchestrators() map[string]*codedoc.CodedocOrchestrator {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]*codedoc.CodedocOrchestrator, len(m.orchestrators))
	for k, v := range m.orchestrators {
		result[k] = v
	}
	return result
}

// ---------------------------------------------------------------------------
// extractCDFeature
// ---------------------------------------------------------------------------

// extractCDFeature extracts the feature name from a URL path like
// /api/codedoc/{feature_name}/status.
func extractCDFeature(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	// Expected: ["api", "codedoc", "{feature}", "{action}"]
	if len(parts) >= 3 && parts[0] == "api" && parts[1] == "codedoc" {
		return parts[2]
	}
	return ""
}

// ---------------------------------------------------------------------------
// Request types
// ---------------------------------------------------------------------------

// cdStartRequest is the JSON body for POST /api/codedoc/start.
type cdStartRequest struct {
	FeatureName  string `json:"feature_name"`
	CodePath     string `json:"code_path"`
	Mode         string `json:"mode"`
	Description  string `json:"description,omitempty"`
	WorkspaceDir string `json:"workspace_dir,omitempty"`
}

// cdGateRequest is the JSON body for POST /api/codedoc/{feature}/gate.
type cdGateRequest struct {
	Action  string `json:"action"`
	Comment string `json:"comment,omitempty"`
}

// cdRewindRequest is the JSON body for POST /api/codedoc/{feature}/rewind.
type cdRewindRequest struct {
	TargetState string `json:"target_state"`
}

// ---------------------------------------------------------------------------
// POST /api/codedoc/start
// ---------------------------------------------------------------------------

// HandleCDStart returns an HTTP handler that starts a new codedoc workflow.
// Returns 202 on success, 400 for invalid input, 409 if already exists.
func HandleCDStart(manager *CodedocManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req cdStartRequest
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
			writeError(w, http.StatusBadRequest, "workspace_dir overrides are not supported for codedoc workflows")
			return
		}

		// Default mode to "full".
		mode := req.Mode
		if mode == "" {
			mode = "full"
		}
		if mode != "full" && mode != "incremental" {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("mode must be 'full' or 'incremental', got %q", mode))
			return
		}

		manager.mu.Lock()
		if _, exists := manager.orchestrators[req.FeatureName]; exists {
			manager.mu.Unlock()
			writeError(w, http.StatusConflict, fmt.Sprintf("codedoc workflow already exists for feature: %s", req.FeatureName))
			return
		}
		if existingState, err := codedoc.LoadCDState(filepath.Join(manager.workspaceDir, "codedoc", req.FeatureName)); err == nil {
			manager.mu.Unlock()
			writeJSON(w, http.StatusConflict, map[string]interface{}{
				"error":        fmt.Sprintf("codedoc workflow already exists on disk for feature: %s", req.FeatureName),
				"feature_name": req.FeatureName,
				"resume_state": existingState.State.String(),
			})
			return
		}

		orch := codedoc.NewCodedocOrchestrator(codedoc.CodedocOrchestratorConfig{
			WorkspaceDir: manager.workspaceDir,
			FeatureName:  req.FeatureName,
			CodePath:     req.CodePath,
			Mode:         mode,
			Description:  req.Description,
			Config:       manager.config,
			Runner:       manager.runner,
			CodexRunner:  manager.codexRunner,
			MergeRunner:  manager.mergeRunner,
			Emitter:      manager.emitter,
		})
		if err := orch.EnsurePersisted(); err != nil {
			manager.mu.Unlock()
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("persist codedoc workflow: %v", err))
			return
		}

		manager.orchestrators[req.FeatureName] = orch
		manager.mu.Unlock()

		// Launch the workflow in the background. RunWorkflow will drive the
		// state machine through CDInit -> CDDiscovery -> CDHumanGateScope,
		// then pause waiting for a gate response.
		go func() {
			if err := orch.RunWorkflow(); err != nil {
				log.Printf("[codedoc] workflow error for %q: %v", req.FeatureName, err)
			}
		}()

		log.Printf("[codedoc] started codedoc workflow for %q (mode=%s)", req.FeatureName, mode)
		writeJSON(w, http.StatusAccepted, map[string]interface{}{
			"status":       "started",
			"feature_name": req.FeatureName,
			"mode":         mode,
		})
	}
}

// ---------------------------------------------------------------------------
// GET /api/codedoc/{feature}/status
// ---------------------------------------------------------------------------

// HandleCDStatus returns an HTTP handler for getting codedoc workflow status.
// Returns 200 with status data, 404 if feature not found.
func HandleCDStatus(manager *CodedocManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		featureName := extractCDFeature(r.URL.Path)
		if featureName == "" {
			writeError(w, http.StatusBadRequest, "feature_name is required in URL path")
			return
		}

		orch := manager.getOrchestrator(featureName)
		if orch != nil {
			resp := buildCDStatusResponse(orch.State())
			// Include gate payload when workflow is at a human gate.
			if gatePayload := buildGatePayload(orch); gatePayload != nil {
				resp["gate_payload"] = gatePayload
			}
			writeJSON(w, http.StatusOK, resp)
			return
		}

		// Try loading from disk.
		featureDir := filepath.Join(manager.workspaceDir, "codedoc", featureName)
		diskState, err := codedoc.LoadCDState(featureDir)
		if err != nil {
			writeError(w, http.StatusNotFound, fmt.Sprintf("no codedoc workflow found for feature: %s", featureName))
			return
		}
		writeJSON(w, http.StatusOK, buildCDStatusResponse(diskState))
	}
}

// buildGatePayload returns a gate-specific payload when the orchestrator is
// at a human gate, or nil if not at a gate.
func buildGatePayload(orch *codedoc.CodedocOrchestrator) interface{} {
	state := orch.State()
	switch state.State {
	case codedoc.CDHumanGateScope:
		return codedoc.ScopeGatePayload{
			DiscoverySource: state.DiscoverySource,
		}
	case codedoc.CDHumanGateDraft:
		return codedoc.DraftGatePayload{}
	case codedoc.CDHumanGateFinal:
		return orch.FinalGatePayload()
	default:
		return nil
	}
}

// buildCDStatusResponse constructs the status response from workflow state.
func buildCDStatusResponse(state *codedoc.CDStateJSON) map[string]interface{} {
	return map[string]interface{}{
		"state":                 state.State.String(),
		"round":                 state.Round,
		"mode":                  state.Mode,
		"cost_usd":              state.CumulativeCostUSD,
		"wall_clock_seconds":    state.CumulativeWallClockSeconds,
		"agent_invocations":     state.AgentInvocations,
		"had_critical_findings": state.HadCriticalFindings,
	}
}

// ---------------------------------------------------------------------------
// POST /api/codedoc/{feature}/gate
// ---------------------------------------------------------------------------

// HandleCDGate returns an HTTP handler for responding to human gates.
// Returns 200 on success, 400 for invalid action, 404 if feature not found,
// 409 if workflow is not in a gate state.
func HandleCDGate(manager *CodedocManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		featureName := extractCDFeature(r.URL.Path)
		if featureName == "" {
			writeError(w, http.StatusBadRequest, "feature_name is required in URL path")
			return
		}

		orch := manager.getOrchestrator(featureName)
		if orch == nil {
			var err error
			orch, _, err = manager.loadOrchestratorFromDisk(featureName)
			if err != nil {
				writeError(w, http.StatusNotFound, fmt.Sprintf("no codedoc workflow found for feature: %s", featureName))
				return
			}
		}

		var req cdGateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
			return
		}

		currentState := orch.State().State
		var err error

		switch currentState {
		case codedoc.CDHumanGateScope:
			err = orch.HandleScopeGate(req.Action)
		case codedoc.CDHumanGateDraft:
			err = orch.HandleDraftGate(req.Action)
		case codedoc.CDHumanGateFinal:
			err = orch.HandleFinalGate(req.Action)
		default:
			writeError(w, http.StatusConflict, fmt.Sprintf("workflow is not in a gate state (current state: %s)", currentState))
			return
		}

		if err != nil {
			if strings.Contains(err.Error(), "unknown") {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid gate action: %v", err))
			} else {
				writeError(w, http.StatusConflict, err.Error())
			}
			return
		}

		newState := orch.State().State

		// If the gate moved the workflow to an actionable (non-gate, non-terminal) state,
		// launch RunWorkflow in a goroutine to drive the workflow forward.
		if !newState.IsTerminal() && !newState.IsGate() {
			go func() {
				if err := orch.RunWorkflow(); err != nil {
					log.Printf("[codedoc] workflow run error for %q: %v", featureName, err)
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
// POST /api/codedoc/{feature}/cancel
// ---------------------------------------------------------------------------

// HandleCDCancel returns an HTTP handler for cancelling a codedoc workflow.
// Returns 200 on success, 404 if feature not found.
func HandleCDCancel(manager *CodedocManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		featureName := extractCDFeature(r.URL.Path)
		if featureName == "" {
			writeError(w, http.StatusBadRequest, "feature_name is required in URL path")
			return
		}

		orch := manager.getOrchestrator(featureName)
		if orch == nil {
			writeError(w, http.StatusNotFound, fmt.Sprintf("no codedoc workflow found for feature: %s", featureName))
			return
		}

		if err := orch.Cancel(); err != nil {
			log.Printf("[codedoc] cancel warning for %q: %v", featureName, err)
		}

		log.Printf("[codedoc] cancelled codedoc workflow for %q", featureName)
		writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
	}
}

// ---------------------------------------------------------------------------
// POST /api/codedoc/{feature}/resume
// ---------------------------------------------------------------------------

// HandleCDResume returns an HTTP handler for resuming a codedoc workflow from
// CD_ERROR state. Returns 200 on success, 404 if not found, 409 if not in
// CD_ERROR state.
func HandleCDResume(manager *CodedocManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		featureName := extractCDFeature(r.URL.Path)
		if featureName == "" {
			writeError(w, http.StatusBadRequest, "feature_name is required in URL path")
			return
		}

		orch := manager.getOrchestrator(featureName)
		if orch == nil {
			var err error
			orch, _, err = manager.loadOrchestratorFromDisk(featureName)
			if err != nil {
				writeError(w, http.StatusNotFound, fmt.Sprintf("no codedoc workflow found for feature: %s", featureName))
				return
			}
		}

		currentState := orch.State().State
		if currentState != codedoc.CDError {
			writeError(w, http.StatusConflict, fmt.Sprintf("workflow is not in CD_ERROR state (current state: %s)", currentState))
			return
		}

		// Launch resume in background.
		go func() {
			if err := orch.Resume(); err != nil {
				log.Printf("[codedoc] resume error for %q: %v", featureName, err)
			}
		}()

		log.Printf("[codedoc] resuming codedoc workflow for %q", featureName)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":       "resumed",
			"feature_name": featureName,
			"state":        currentState.String(),
		})
	}
}

// ---------------------------------------------------------------------------
// POST /api/codedoc/{feature}/reset
// ---------------------------------------------------------------------------

// HandleCDReset returns an HTTP handler for deleting a codedoc feature
// workspace. Returns 200 on success, 404 if not found, 409 if still running.
func HandleCDReset(manager *CodedocManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		featureName := extractCDFeature(r.URL.Path)
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
				writeError(w, http.StatusConflict, fmt.Sprintf("codedoc workflow for %s is still running (state: %s)", featureName, sm.Current()))
				return
			}
			delete(manager.orchestrators, featureName)
		}

		// Check on-disk state.
		featureDir := filepath.Join(manager.workspaceDir, "codedoc", featureName)
		state, err := codedoc.LoadCDState(featureDir)
		if err != nil {
			writeError(w, http.StatusNotFound, fmt.Sprintf("no codedoc workflow found for feature: %s", featureName))
			return
		}

		if !state.State.IsTerminal() {
			writeError(w, http.StatusConflict, fmt.Sprintf("codedoc workflow for %s is not in a terminal state (state: %s)", featureName, state.State))
			return
		}

		if err := os.RemoveAll(featureDir); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to delete workspace: %v", err))
			return
		}

		log.Printf("[codedoc] reset codedoc workspace for %q", featureName)
		writeJSON(w, http.StatusOK, map[string]string{"status": "reset"})
	}
}

// ---------------------------------------------------------------------------
// POST /api/codedoc/{feature}/rewind
// ---------------------------------------------------------------------------

// HandleCDRewind returns an HTTP handler for rewinding a codedoc workflow to
// a target state. Returns 200 on success, 400 for invalid state, 404 if not found.
func HandleCDRewind(manager *CodedocManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		featureName := extractCDFeature(r.URL.Path)
		if featureName == "" {
			writeError(w, http.StatusBadRequest, "feature_name is required in URL path")
			return
		}

		orch := manager.getOrchestrator(featureName)
		if orch == nil {
			writeError(w, http.StatusNotFound, fmt.Sprintf("no codedoc workflow found for feature: %s", featureName))
			return
		}

		var req cdRewindRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
			return
		}

		if req.TargetState == "" {
			writeError(w, http.StatusBadRequest, "target_state is required")
			return
		}

		// Validate the target state is a known CDState.
		targetState := codedoc.CDState(req.TargetState)
		if !isKnownCDState(targetState) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown target_state: %s", req.TargetState))
			return
		}

		if err := orch.Rewind(targetState); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("rewind failed: %v", err))
			return
		}

		log.Printf("[codedoc] rewound codedoc workflow for %q to %s", featureName, targetState)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":    "rewound",
			"new_state": targetState.String(),
		})
	}
}

// isKnownCDState checks whether the given state is a recognised CDState value.
func isKnownCDState(s codedoc.CDState) bool {
	switch s {
	case codedoc.CDInit, codedoc.CDDiscovery, codedoc.CDHumanGateScope,
		codedoc.CDDrafting, codedoc.CDSanitising, codedoc.CDHumanGateDraft,
		codedoc.CDReviewing, codedoc.CDRevising, codedoc.CDJudging,
		codedoc.CDHumanGateFinal, codedoc.CDWriting, codedoc.CDComplete,
		codedoc.CDEscalated, codedoc.CDError:
		return true
	}
	return false
}
