// Package api provides HTTP handlers for the adversarial spec system.
// This file implements the workflow control endpoints that bridge the HTTP
// API to the Orchestrator: starting workflows, responding to gates, and
// cancelling running workflows.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/foundry-zero/adversarial-spec-system/internal/codedoc"
	"github.com/foundry-zero/adversarial-spec-system/internal/codereview"
	"github.com/foundry-zero/adversarial-spec-system/internal/process"
	"github.com/foundry-zero/adversarial-spec-system/internal/specworkflow"
)

// newBeadsClientOrNil creates a BeadsClient for the given workspace. If bd is
// not on PATH (ErrBeadsUnavailable), it logs a warning and returns nil so the
// orchestrator runs with Beads integration disabled (graceful degradation).
func newBeadsClientOrNil(workspaceDir string) (specworkflow.BeadsClientInterface, error) {
	client, err := specworkflow.NewBeadsClient(workspaceDir)
	if err != nil {
		if errors.Is(err, specworkflow.ErrBeadsUnavailable) {
			log.Printf("[workflow] WARNING: bd not on PATH — Beads integration disabled")
			return nil, nil
		}
		return nil, fmt.Errorf("beads client: %w", err)
	}
	return client, nil
}

// isTerminalWorkflowState reports whether the given workflow state is terminal
// (COMPLETE or ESCALATED), meaning a new workflow can safely be started.
func isTerminalWorkflowState(s specworkflow.WorkflowState) bool {
	return s == specworkflow.StateComplete || s == specworkflow.StateEscalated
}

// ---------------------------------------------------------------------------
// WorkflowManager
// ---------------------------------------------------------------------------

// WorkflowManager coordinates the lifecycle of concurrent workflow runs.
// It owns a map of Orchestrators keyed by feature name, a ChannelEmitter,
// and WebSocketHub, and provides HTTP handler factories for the workflow
// control endpoints. All map operations are protected by a sync.RWMutex.
type WorkflowManager struct {
	orchestrators      map[string]*specworkflow.Orchestrator
	workspaceOverrides map[string]string // featureName -> overridden workspace dir
	emitter            *specworkflow.ChannelEmitter
	hub                *WebSocketHub
	workspaceDir       string
	config             specworkflow.SpecWorkflowConfig
	otelPort           int
	metricsStore       *MetricsStore
	otelReceiver       *OTELReceiver
	crManager          *CodeReviewManager
	cdManager          *CodedocManager
	processTracker     *process.ProcessTracker
	mu                 sync.RWMutex
}

// NewWorkflowManager creates a WorkflowManager with the given dependencies.
func NewWorkflowManager(
	emitter *specworkflow.ChannelEmitter,
	hub *WebSocketHub,
	workspaceDir string,
	config specworkflow.SpecWorkflowConfig,
) *WorkflowManager {
	return &WorkflowManager{
		orchestrators:      make(map[string]*specworkflow.Orchestrator),
		workspaceOverrides: make(map[string]string),
		emitter:            emitter,
		hub:                hub,
		workspaceDir:       workspaceDir,
		config:             config,
	}
}

// GetWorkspaceForFeature returns the effective workspace directory for a
// feature, checking per-workflow overrides first, then falling back to
// the manager's default workspace.
func (m *WorkflowManager) GetWorkspaceForFeature(featureName string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if ws, ok := m.workspaceOverrides[featureName]; ok {
		return ws
	}
	return m.workspaceDir
}

// SetOTELPort configures the OTEL port used for child process telemetry.
// Must be called before starting any workflows.
func (m *WorkflowManager) SetOTELPort(port int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.otelPort = port
}

// SetMetricsStore configures the SQLite metrics store for persisting
// telemetry data. Must be called before starting any workflows.
func (m *WorkflowManager) SetMetricsStore(store *MetricsStore) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.metricsStore = store
}

// SetProcessTracker configures the process tracker for subprocess lifecycle
// tracking. Must be called before starting any workflows.
func (m *WorkflowManager) SetProcessTracker(tracker *process.ProcessTracker) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.processTracker = tracker
}

// SetOTELReceiver configures the OTEL receiver reference so the status
// endpoint can read in-memory cost data as a fallback.
func (m *WorkflowManager) SetOTELReceiver(recv *OTELReceiver) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.otelReceiver = recv
}

// SetCodeReviewManager configures the code review manager reference so the
// unified status endpoint can include code review workflows.
func (m *WorkflowManager) SetCodeReviewManager(crm *CodeReviewManager) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.crManager = crm
}

// SetCodedocManager configures the codedoc manager reference so the
// unified status endpoint can include codedoc workflows.
func (m *WorkflowManager) SetCodedocManager(cdm *CodedocManager) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cdManager = cdm
}

// GetCurrentFeatureName returns the feature name of a currently running
// workflow. If multiple workflows are running, returns the first one found.
// Falls back to on-disk state so OTEL metrics can still be
// associated with the correct feature after a server restart (when the
// parent Claude Code process continues sending telemetry even though
// no orchestrator is running).
func (m *WorkflowManager) GetCurrentFeatureName() string {
	m.mu.RLock()
	for name, orch := range m.orchestrators {
		if orch != nil && orch.IsRunning() {
			m.mu.RUnlock()
			return name
		}
	}
	m.mu.RUnlock()
	// Fall back to on-disk state.
	if diskState := findLatestDiskState(m.workspaceDir); diskState != nil {
		return diskState.FeatureName
	}
	return ""
}

// GetOrchestrator returns the orchestrator for the given feature name,
// or nil if no workflow is running for that feature. If featureName is
// empty, returns any running orchestrator (for backward compatibility).
func (m *WorkflowManager) GetOrchestrator(featureName ...string) *specworkflow.Orchestrator {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(featureName) > 0 && featureName[0] != "" {
		return m.orchestrators[featureName[0]]
	}
	// Backward compat: return any running orchestrator.
	for _, orch := range m.orchestrators {
		if orch != nil {
			return orch
		}
	}
	return nil
}

// GetAllOrchestrators returns a snapshot of all active orchestrators
// as a map keyed by feature name.
func (m *WorkflowManager) GetAllOrchestrators() map[string]*specworkflow.Orchestrator {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]*specworkflow.Orchestrator, len(m.orchestrators))
	for k, v := range m.orchestrators {
		result[k] = v
	}
	return result
}

// HasRunningWorkflow returns true if any orchestrator is currently running.
func (m *WorkflowManager) HasRunningWorkflow() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, orch := range m.orchestrators {
		if orch != nil && orch.IsRunning() {
			return true
		}
	}
	return false
}

// IsFeatureRunning returns true if the given feature has a running orchestrator.
func (m *WorkflowManager) IsFeatureRunning(featureName string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	orch, ok := m.orchestrators[featureName]
	return ok && orch != nil && orch.IsRunning()
}

// GetTracker returns the issue tracker from the current orchestrator.
// If featureName is provided, returns the tracker for that specific feature.
// If no orchestrator is running, it falls back to loading merged findings
// from disk so the dashboard shows issues after a server restart.
func (m *WorkflowManager) GetTracker(featureName ...string) *specworkflow.IssueTracker {
	var orch *specworkflow.Orchestrator
	var feature string

	m.mu.RLock()
	if len(featureName) > 0 && featureName[0] != "" {
		feature = featureName[0]
		orch = m.orchestrators[feature]
	} else {
		// Backward compat: try any orchestrator.
		for name, o := range m.orchestrators {
			if o != nil {
				orch = o
				feature = name
				break
			}
		}
	}
	m.mu.RUnlock()

	if orch != nil {
		return orch.Tracker()
	}

	// Fall back to disk: find the latest workflow state and load its
	// merged findings into a fresh tracker.
	if feature != "" {
		tracker := specworkflow.NewIssueTracker()
		specDir := filepath.Join(m.workspaceDir, "specs", feature)
		if state, err := specworkflow.LoadState(specDir); err == nil && state != nil {
			specworkflow.ReloadFindings(tracker, specDir, state.Round)
			return tracker
		}
	}
	if diskState := findLatestDiskState(m.workspaceDir); diskState != nil {
		tracker := specworkflow.NewIssueTracker()
		specDir := filepath.Join(m.workspaceDir, "specs", diskState.FeatureName)
		specworkflow.ReloadFindings(tracker, specDir, diskState.Round)
		return tracker
	}
	return specworkflow.NewIssueTracker()
}

// GetState returns the workflow state from the current orchestrator.
// If featureName is provided, returns state for that specific feature.
// If no orchestrator is running, it falls back to the most recent
// on-disk state so that spec endpoints can still serve data after a
// server restart. Returns an empty state only when nothing is found.
func (m *WorkflowManager) GetState(featureName ...string) *specworkflow.WorkflowStateJSON {
	var orch *specworkflow.Orchestrator
	var feature string

	m.mu.RLock()
	if len(featureName) > 0 && featureName[0] != "" {
		feature = featureName[0]
		orch = m.orchestrators[feature]
	} else {
		for name, o := range m.orchestrators {
			if o != nil {
				orch = o
				feature = name
				break
			}
		}
	}
	m.mu.RUnlock()

	if orch != nil {
		return orch.State()
	}

	// Fall back to on-disk state.
	if feature != "" {
		specDir := filepath.Join(m.workspaceDir, "specs", feature)
		if state, err := specworkflow.LoadState(specDir); err == nil && state != nil {
			return state
		}
	}
	if diskState := findLatestDiskState(m.workspaceDir); diskState != nil {
		return diskState
	}
	return &specworkflow.WorkflowStateJSON{}
}

// CancelWorkflow cancels a running workflow by feature name. If featureName
// is provided, cancels that specific workflow. Otherwise cancels any running
// workflow (backward compatibility).
func (m *WorkflowManager) CancelWorkflow(featureName ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(featureName) > 0 && featureName[0] != "" {
		name := featureName[0]
		orch, ok := m.orchestrators[name]
		if !ok || orch == nil {
			return fmt.Errorf("no running workflow for feature: %s", name)
		}
		orch.Cancel()
		delete(m.orchestrators, name)
		return nil
	}

	// Backward compat: cancel any running orchestrator.
	for name, orch := range m.orchestrators {
		if orch != nil {
			orch.Cancel()
			delete(m.orchestrators, name)
			return nil
		}
	}
	return fmt.Errorf("no active workflow to cancel")
}

// ResumeFromGate re-creates the orchestrator and resumes the workflow when the
// server has restarted while in a gate state. It loads the persisted state from
// disk, verifies it is a gate state, creates a new orchestrator (which will
// restore the persisted state), and runs the workflow in a background goroutine.
// The workflow loop will enter the gate handler and wait on gateCh, ready to
// receive the gate response from the HTTP handler.
//
// Returns the resumed orchestrator, or an error if resumption is not possible.
func (m *WorkflowManager) ResumeFromGate(featureName string) (*specworkflow.Orchestrator, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// If there's already a running orchestrator for this feature, return it directly.
	if orch, ok := m.orchestrators[featureName]; ok && orch != nil && orch.IsRunning() {
		return orch, nil
	}

	// Load state from disk to verify it's a gate state.
	absWorkspace, _ := filepath.Abs(m.workspaceDir)
	state, err := specworkflow.LoadState(filepath.Join(absWorkspace, "specs", featureName))
	if err != nil {
		return nil, fmt.Errorf("load state: %w", err)
	}

	if !specworkflow.IsGateState(state.State) {
		return nil, fmt.Errorf("workflow is in %s, not a gate state", state.State)
	}

	// Create orchestrator — it will restore the persisted state from disk.
	// Wrap the shared emitter so events carry this workflow's feature name.
	// Use per-workflow source docs for isolation.
	sourcePaths := discoverWorkflowSourceDocs(m.workspaceDir, featureName)
	beadsClient, beadsErr := newBeadsClientOrNil(m.workspaceDir)
	if beadsErr != nil {
		return nil, fmt.Errorf("create orchestrator: %w", beadsErr)
	}
	runner := specworkflow.DefaultClaudeRunner(m.workspaceDir, m.otelPort, featureName, m.config.ClaudeModels.Default)
	runner.Tracker = m.processTracker
	runner.Feature = featureName
	orchConfig := specworkflow.OrchestratorConfig{
		WorkspaceDir:   m.workspaceDir,
		FeatureName:    featureName,
		SourceDocPaths: sourcePaths,
		Config:         m.config,
		Runner:         runner,
		Emitter:        specworkflow.NewFeatureEmitter(m.emitter, featureName),
		BeadsClient:    beadsClient,
	}
	// Wire cost provider if metrics store is available.
	if m.metricsStore != nil {
		orchConfig.CostProvider = NewMetricsCostProvider(m.metricsStore, featureName)
	}

	orch, err := specworkflow.NewOrchestrator(orchConfig)
	if err != nil {
		return nil, fmt.Errorf("create orchestrator: %w", err)
	}

	m.orchestrators[featureName] = orch

	// Run workflow in background — it will resume from the gate state and
	// block waiting on gateCh for the human response.
	go func() {
		goal := specworkflow.GoalInput{
			Title:          featureName,
			Description:    "Resumed from gate after server restart",
			SourceDocPaths: sourcePaths,
		}
		if err := orch.RunWorkflow(goal); err != nil {
			log.Printf("[workflow] resumed workflow completed with error: %v", err)
		} else {
			log.Printf("[workflow] resumed workflow completed successfully")
		}
	}()

	// Brief pause to let the goroutine start and reach the gate wait point.
	time.Sleep(500 * time.Millisecond)

	return orch, nil
}

// ---------------------------------------------------------------------------
// Request / Response types
// ---------------------------------------------------------------------------

// startWorkflowRequest is the JSON body for POST /api/workflow/start.
type startWorkflowRequest struct {
	Title          string   `json:"title"`
	Description    string   `json:"description"`
	FeatureName    string   `json:"feature_name"`
	SourceDocPaths []string `json:"source_doc_paths"`
	CodePath       string   `json:"code_path,omitempty"`
	WorkspaceDir   string   `json:"workspace_dir,omitempty"`
}

// assignSourceDocsRequest is the JSON body for POST /api/workflow/{feature}/source-docs.
type assignSourceDocsRequest struct {
	SourceDocPaths []string `json:"source_doc_paths"`
}

// gateApproveRequest is the JSON body for POST /api/tasks/{id}/approve.
type gateApproveRequest struct {
	Action      string                             `json:"action"`
	Corrections map[string]string                  `json:"corrections,omitempty"`
	Resolutions []specworkflow.AmbiguityResolution `json:"resolutions,omitempty"`
	UserAnswers map[string]interface{}             `json:"user_answers,omitempty"`
	Comment     string                             `json:"comment,omitempty"`
}

// ---------------------------------------------------------------------------
// HTTP Handlers
// ---------------------------------------------------------------------------

// HandleStartWorkflow returns an HTTP handler that starts a new workflow.
// It creates an Orchestrator with a ClaudeRunner and runs RunWorkflow in a
// background goroutine. Returns 202 on success, 409 if a workflow is already
// running, 400 for invalid input.
func HandleStartWorkflow(manager *WorkflowManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req startWorkflowRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
			return
		}

		if req.FeatureName == "" {
			// Default feature name from title.
			req.FeatureName = sanitizeFeatureName(req.Title)
		}
		if req.FeatureName == "" {
			writeError(w, http.StatusBadRequest, "feature_name or title is required")
			return
		}

		if err := ValidateFeatureName(req.FeatureName); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid feature_name: %v", err))
			return
		}

		wsDir := manager.workspaceDir
		if req.WorkspaceDir != "" {
			if err := validateExistingDirectory("workspace_dir", req.WorkspaceDir); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			wsDir = req.WorkspaceDir
		}

		// Check for an existing workflow state on disk before creating a new one.
		absWorkspace, _ := filepath.Abs(wsDir)
		resumeResult, resumeErr := specworkflow.ResumeWorkflow(absWorkspace, req.FeatureName, nil)
		if resumeErr == nil && resumeResult.Found {
			if !isTerminalWorkflowState(resumeResult.State.State) {
				// Non-terminal state: a workflow is already in progress for this feature.
				writeJSON(w, http.StatusConflict, map[string]interface{}{
					"error":        "a workflow is already in progress for this feature",
					"status":       "in_progress",
					"state":        resumeResult.State.State.String(),
					"round":        resumeResult.State.Round,
					"feature_name": req.FeatureName,
					"is_gate":      resumeResult.IsGateState,
				})
				return
			}
			// Terminal state (FINALIZED/ESCALATED): allow starting a new workflow.
		}

		manager.mu.Lock()
		// Check if a workflow is already running for this feature.
		if existingOrch, ok := manager.orchestrators[req.FeatureName]; ok && existingOrch != nil {
			if existingOrch.IsRunning() {
				manager.mu.Unlock()
				writeError(w, http.StatusConflict, fmt.Sprintf("workflow already running for feature: %s", req.FeatureName))
				return
			}
			// Orchestrator exists but is not running — clean it up.
			delete(manager.orchestrators, req.FeatureName)
		}
		if req.WorkspaceDir != "" {
			manager.workspaceOverrides[req.FeatureName] = wsDir
		} else {
			delete(manager.workspaceOverrides, req.FeatureName)
		}

		// Resolve source doc paths — if none provided, scan source-docs directory.
		// These are paths in the global library that will be copied below.
		globalPaths := req.SourceDocPaths
		if len(globalPaths) == 0 {
			globalPaths = discoverSourceDocs(wsDir)
		}

		// Copy source docs into the per-workflow directory for isolation.
		var sourcePaths []string
		if len(globalPaths) > 0 {
			copied, copyErr := copySourceDocsToWorkflow(wsDir, req.FeatureName, globalPaths)
			if copyErr != nil {
				manager.mu.Unlock()
				writeError(w, http.StatusBadRequest, fmt.Sprintf("failed to copy source docs: %v", copyErr))
				return
			}
			sourcePaths = copied
		}

		// Create orchestrator config.
		// Wrap the shared emitter so events from this workflow carry its
		// feature name — enabling client-side filtering when multiple
		// workflows run concurrently.
		beadsClient, beadsErr := newBeadsClientOrNil(wsDir)
		if beadsErr != nil {
			manager.mu.Unlock()
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to create beads client: %v", beadsErr))
			return
		}
		startRunner := specworkflow.DefaultClaudeRunner(wsDir, manager.otelPort, req.FeatureName, manager.config.ClaudeModels.Default)
		startRunner.Tracker = manager.processTracker
		startRunner.Feature = req.FeatureName
		orchConfig := specworkflow.OrchestratorConfig{
			WorkspaceDir:   wsDir,
			FeatureName:    req.FeatureName,
			SourceDocPaths: sourcePaths,
			Config:         manager.config,
			Runner:         startRunner,
			Emitter:        specworkflow.NewFeatureEmitter(manager.emitter, req.FeatureName),
			BeadsClient:    beadsClient,
		}

		// Wire cost provider if metrics store is available.
		if manager.metricsStore != nil {
			orchConfig.CostProvider = NewMetricsCostProvider(manager.metricsStore, req.FeatureName)
			// Reset persisted metrics for a fresh workflow run.
			if err := manager.metricsStore.ResetForFeature(req.FeatureName); err != nil {
				log.Printf("[workflow] warning: failed to reset metrics for %q: %v", req.FeatureName, err)
			}
		}

		orch, err := specworkflow.NewOrchestrator(orchConfig)
		if err != nil {
			manager.mu.Unlock()
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to create orchestrator: %v", err))
			return
		}

		manager.orchestrators[req.FeatureName] = orch
		manager.mu.Unlock()

		// Run workflow in background.
		goal := specworkflow.GoalInput{
			Title:          req.Title,
			Description:    req.Description,
			SourceDocPaths: sourcePaths,
			CodePath:       req.CodePath,
		}

		go func() {
			if err := orch.RunWorkflow(goal); err != nil {
				log.Printf("[workflow] %s completed with error: %v", req.FeatureName, err)
			} else {
				log.Printf("[workflow] %s completed successfully", req.FeatureName)
			}
		}()

		writeJSON(w, http.StatusAccepted, map[string]interface{}{
			"status":       "started",
			"feature_name": req.FeatureName,
			"state":        "INIT",
			"round":        1,
		})
	}
}

// HandleGateApprove returns an HTTP handler for POST /api/tasks/{id}/approve.
// It delivers a gate approval response to the running orchestrator. The
// request body may contain corrections (gate 1) or resolutions (gate 2).
//
// If no orchestrator is running but on-disk state shows a gate state (e.g.
// after a server restart), the handler automatically resumes the workflow
// before delivering the gate response.
func HandleGateApprove(manager *WorkflowManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			log.Printf("[gate-approve] rejected %s %s (expected POST)", r.Method, r.URL.Path)
			w.Header().Set("Allow", "POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Extract feature name from URL: /api/tasks/{feature}/approve
		featureName := extractFeatureFromTaskPath(r.URL.Path)

		orch := manager.GetOrchestrator(featureName)
		if orch == nil {
			// No orchestrator running — try to resume from on-disk gate state.
			orch = tryResumeFromDiskState(manager, w)
			if orch == nil {
				return // error already written to response
			}
		}

		var req gateApproveRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
			return
		}

		// ---------------------------------------------------------------
		// PERSIST FIRST: Save all gate data to disk BEFORE signaling
		// the gate channel. This ensures data is never lost even if the
		// gate signal fails, the orchestrator has moved on, or the
		// server crashes between signal and orchestrator persistence.
		// ---------------------------------------------------------------
		if featureName != "" {
			specDir := filepath.Join(manager.workspaceDir, "specs", featureName)
			os.MkdirAll(specDir, 0o755) // ensure dir exists

			// Determine current gate state to route persistence correctly.
			currentState := orch.State().State

			switch req.Action {
			case "confirm":
				// Save user answers.
				if req.UserAnswers != nil {
					persistGateData(specDir, "user-answers.json", req.UserAnswers)
				}
			case "correct":
				// Only write gate1-corrections files for HUMAN_GATE_1.
				// TASK_HUMAN_GATE corrections are handled by the orchestrator.
				if currentState == specworkflow.StateHumanGate1 {
					corrData := map[string]interface{}{
						"action":      "correct",
						"corrections": req.Corrections,
					}
					if req.UserAnswers != nil {
						corrData["user_answers"] = req.UserAnswers
					}
					if req.Comment != "" {
						corrData["reviewer_comment"] = req.Comment
					}
					corrRound := nextGate1CorrectionRound(specDir)
					corrFilename := fmt.Sprintf("gate1-corrections-%d.json", corrRound)
					persistGateData(specDir, corrFilename, corrData)
					persistGateData(specDir, "gate1-corrections.json", corrData)
				}
			default:
				// Gate 2 resolutions.
				if len(req.Resolutions) > 0 {
					persistGateData(specDir, "gate2-resolutions.json", map[string]interface{}{
						"resolutions": req.Resolutions,
					})
				}
			}

			// Persist comment to human-comments.json (append).
			// Skip for TASK_HUMAN_GATE — the orchestrator handles comment persistence there.
			if req.Comment != "" && currentState != specworkflow.StateTaskHumanGate {
				persistHumanComment(specDir, req.Action, req.Comment)
			}

			log.Printf("[gate-approve] persisted gate data: feature=%s action=%s corrections=%d user_answers=%v comment=%d chars",
				featureName, req.Action, len(req.Corrections), req.UserAnswers != nil, len(req.Comment))
		}

		// ---------------------------------------------------------------
		// SIGNAL: Now send the gate response through the channel.
		// The orchestrator will read persisted data from disk.
		// ---------------------------------------------------------------
		gateResp := specworkflow.GateResponse{
			Action:  req.Action,
			Comment: req.Comment,
		}
		// For actions that don't have an explicit action field (gate 2
		// resolutions), infer the action.
		if gateResp.Action == "" {
			if len(req.Resolutions) > 0 && hasAnswerResolution(req.Resolutions) {
				gateResp.Action = "resolve"
			} else {
				gateResp.Action = "confirm"
			}
		}

		if err := orch.HandleGateResponse(gateResp); err != nil {
			// Data is already persisted — the orchestrator will pick it
			// up on the next gate interaction or resume.
			writeError(w, http.StatusConflict, fmt.Sprintf("gate response failed (data saved to disk): %v", err))
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "accepted"})
	}
}

// extractFeatureFromTaskPath extracts the feature name from a URL path
// like /api/tasks/{feature}/approve.
func extractFeatureFromTaskPath(path string) string {
	// path: /api/tasks/{feature}/approve
	parts := strings.Split(strings.Trim(path, "/"), "/")
	// Expected: ["api", "tasks", "{feature}", "approve"]
	if len(parts) >= 4 && parts[0] == "api" && parts[1] == "tasks" {
		return parts[2]
	}
	return ""
}

// extractFeatureFromWorkflowPath extracts the feature name from a URL path
// like /api/workflow/{feature}/source-docs.
func extractFeatureFromWorkflowPath(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	// Expected: ["api", "workflow", "{feature}", "source-docs"]
	if len(parts) >= 3 && parts[0] == "api" && parts[1] == "workflow" {
		return parts[2]
	}
	return ""
}

// nextGate1CorrectionRound returns the next sequential round number for
// gate1-corrections-{N}.json files by counting existing files.
func nextGate1CorrectionRound(specDir string) int {
	round := 1
	for {
		path := filepath.Join(specDir, fmt.Sprintf("gate1-corrections-%d.json", round))
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return round
		}
		round++
	}
}

// persistGateData writes gate data to a JSON file in the spec directory.
func persistGateData(specDir, filename string, data interface{}) {
	path := filepath.Join(specDir, filename)
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		log.Printf("[gate-approve] failed to marshal %s: %v", filename, err)
		return
	}
	if err := os.WriteFile(path, jsonData, 0o644); err != nil {
		log.Printf("[gate-approve] failed to write %s: %v", path, err)
	} else {
		log.Printf("[gate-approve] persisted %s (%d bytes)", path, len(jsonData))
	}
}

// persistHumanComment appends a reviewer comment to human-comments.json.
func persistHumanComment(specDir, action, comment string) {
	type commentEntry struct {
		Gate      string `json:"gate"`
		Action    string `json:"action"`
		Comment   string `json:"comment"`
		Timestamp string `json:"timestamp"`
	}

	commentsPath := filepath.Join(specDir, "human-comments.json")
	var comments []commentEntry
	if data, err := os.ReadFile(commentsPath); err == nil {
		json.Unmarshal(data, &comments)
	}

	comments = append(comments, commentEntry{
		Gate:      "HTTP_GATE_APPROVE",
		Action:    action,
		Comment:   comment,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})

	data, err := json.MarshalIndent(comments, "", "  ")
	if err != nil {
		log.Printf("[gate-approve] failed to marshal comments: %v", err)
		return
	}
	if err := os.WriteFile(commentsPath, data, 0o644); err != nil {
		log.Printf("[gate-approve] failed to write comments: %v", err)
	}
}

// HandleGateReject returns an HTTP handler for POST /api/tasks/{id}/reject.
// It delivers a cancellation/rejection gate response to the orchestrator.
//
// If no orchestrator is running but on-disk state shows a gate state (e.g.
// after a server restart), the handler automatically resumes the workflow
// before delivering the rejection.
func HandleGateReject(manager *WorkflowManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Extract feature name from URL: /api/tasks/{feature}/reject
		featureName := extractFeatureFromTaskPath(r.URL.Path)

		orch := manager.GetOrchestrator(featureName)
		if orch == nil {
			// No orchestrator running — try to resume from on-disk gate state.
			orch = tryResumeFromDiskState(manager, w)
			if orch == nil {
				return // error already written to response
			}
		}

		gateResp := specworkflow.GateResponse{Action: "cancel"}
		if err := orch.HandleGateResponse(gateResp); err != nil {
			writeError(w, http.StatusConflict, fmt.Sprintf("gate reject failed: %v", err))
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "rejected"})
	}
}

// HandleCancelWorkflowAPI returns an HTTP handler for POST /api/workflow/cancel
// (and also POST /api/spec/cancel for backwards compatibility). It cancels
// the running workflow via the WorkflowManager. If the request body contains
// a feature_name, cancels that specific workflow; otherwise cancels any
// running workflow.
func HandleCancelWorkflowAPI(manager *WorkflowManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Try to read feature_name from body (optional).
		var req struct {
			FeatureName string `json:"feature_name"`
		}
		// Body may be empty for backward compat.
		json.NewDecoder(r.Body).Decode(&req)

		if req.FeatureName != "" {
			if err := ValidateFeatureName(req.FeatureName); err != nil {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid feature_name: %v", err))
				return
			}
		}

		var err error
		if req.FeatureName != "" {
			err = manager.CancelWorkflow(req.FeatureName)
		} else {
			err = manager.CancelWorkflow()
		}
		if err != nil {
			// If a specific feature was requested and not found, return 404.
			// Otherwise (no feature specified, nothing running) return 409.
			if req.FeatureName != "" {
				writeError(w, http.StatusNotFound, fmt.Sprintf("cancel failed: %v", err))
			} else {
				writeError(w, http.StatusConflict, fmt.Sprintf("cancel failed: %v", err))
			}
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
	}
}

// bestCostUSD returns the highest cost value from all available sources:
// orchestrator state, OTEL receiver (in-memory), and SQLite metrics store.
func (m *WorkflowManager) bestCostUSD(stateCost float64, featureName string) float64 {
	best := stateCost
	if m.otelReceiver != nil {
		if otelCost := m.otelReceiver.GetCostUSD(featureName); otelCost > best {
			best = otelCost
		}
	}
	if m.metricsStore != nil && featureName != "" {
		if dbCost := m.metricsStore.GetCurrentCostUSD(featureName); dbCost > best {
			best = dbCost
		}
	}
	return best
}

// HandleGetWorkflowStatus returns an HTTP handler for GET /api/workflow/status.
//
// Behavior varies by query parameter:
//   - GET /api/workflow/status (no params) → JSON array of ALL workflow statuses
//   - GET /api/workflow/status?feature=alpha → single JSON object for alpha, or 404
//
// Each status object contains: feature_name, state, round, cost_usd,
// wall_clock_seconds, agent_invocations, message.
func HandleGetWorkflowStatus(manager *WorkflowManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		featureParam := r.URL.Query().Get("feature")

		if featureParam != "" {
			// Single-feature query.
			handleSingleWorkflowStatus(w, manager, featureParam)
			return
		}

		// No feature param — return array of all workflow statuses.
		handleAllWorkflowStatuses(w, manager)
	}
}

// HandleGetWorkflowAgents returns the currently active agents for a workflow.
// GET /api/workflow/agents?feature=<name>
func HandleGetWorkflowAgents(manager *WorkflowManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		featureName := r.URL.Query().Get("feature")
		if featureName == "" {
			http.Error(w, "feature parameter required", http.StatusBadRequest)
			return
		}
		orch := manager.GetOrchestrator(featureName)
		if orch == nil {
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"agents": []specworkflow.AgentStatus{},
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"agents": orch.GetActiveAgents(),
		})
	}
}

// handleSingleWorkflowStatus returns a single JSON status object for the
// requested feature, or 404 if it doesn't exist.
func handleSingleWorkflowStatus(w http.ResponseWriter, manager *WorkflowManager, featureName string) {
	// First check in-memory orchestrators.
	orch := manager.GetOrchestrator(featureName)
	if orch != nil {
		state := orch.State()
		if state != nil {
			writeJSON(w, http.StatusOK, buildStatusObject(manager, state, false))
			return
		}
	}

	// Fall back to on-disk state.
	specDir := filepath.Join(manager.workspaceDir, "specs", featureName)
	diskState, err := specworkflow.LoadState(specDir)
	if err != nil || diskState == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("no workflow found for feature: %s", featureName))
		return
	}

	// Determine if paused: non-terminal, no running orchestrator.
	paused := !isTerminalWorkflowState(diskState.State) && !manager.IsFeatureRunning(featureName)
	writeJSON(w, http.StatusOK, buildStatusObject(manager, diskState, paused))
}

// handleAllWorkflowStatuses returns a JSON array of status objects for every
// known workflow, merging in-memory orchestrator state with on-disk state.
func handleAllWorkflowStatuses(w http.ResponseWriter, manager *WorkflowManager) {
	// Gather all known features from disk.
	allStates := findAllDiskStates(manager.workspaceDir)
	if allStates == nil {
		allStates = make(map[string]*specworkflow.WorkflowStateJSON)
	}

	// Override with live orchestrator state where available.
	for name, orch := range manager.GetAllOrchestrators() {
		if orch == nil {
			continue
		}
		if state := orch.State(); state != nil {
			allStates[name] = state
		}
	}

	// Build status objects and sort by feature name for stable output.
	var names []string
	for name := range allStates {
		names = append(names, name)
	}
	sort.Strings(names)

	result := make([]map[string]interface{}, 0, len(names))
	for _, name := range names {
		state := allStates[name]
		paused := !isTerminalWorkflowState(state.State) && !manager.IsFeatureRunning(name)
		obj := buildStatusObject(manager, state, paused)
		result = append(result, obj)
	}

	// Gather code review workflows if a CR manager is wired.
	manager.mu.RLock()
	crMgr := manager.crManager
	manager.mu.RUnlock()

	if crMgr != nil {
		// Disk-based CR states.
		crDiskStates := findAllCRDiskStates(manager.workspaceDir)
		// Override with live orchestrator state.
		for name, orch := range crMgr.GetAllOrchestrators() {
			if orch == nil {
				continue
			}
			if sm := orch.StateMachine(); sm != nil {
				crDiskStates[name] = sm.State()
			}
		}

		var crNames []string
		for name := range crDiskStates {
			crNames = append(crNames, name)
		}
		sort.Strings(crNames)

		for _, name := range crNames {
			state := crDiskStates[name]
			paused := !state.State.IsTerminal() && !crMgr.IsFeatureRunning(name)
			obj := buildCRUnifiedStatusObject(state, paused)
			result = append(result, obj)
		}
	}

	// Gather codedoc workflows if a CD manager is wired.
	manager.mu.RLock()
	cdMgr := manager.cdManager
	manager.mu.RUnlock()

	if cdMgr != nil {
		cdDiskStates := findAllCDDiskStates(manager.workspaceDir)
		for name, orch := range cdMgr.GetAllOrchestrators() {
			if orch == nil {
				continue
			}
			cdDiskStates[name] = orch.State()
		}

		var cdNames []string
		for name := range cdDiskStates {
			cdNames = append(cdNames, name)
		}
		sort.Strings(cdNames)

		for _, name := range cdNames {
			state := cdDiskStates[name]
			paused := !state.State.IsTerminal() && !state.State.IsGate() && cdMgr.getOrchestrator(name) == nil
			obj := buildCDUnifiedStatusObject(state, paused)
			result = append(result, obj)
		}
	}

	writeJSON(w, http.StatusOK, result)
}

// buildStatusObject constructs the standard status response fields from a
// workflow state. If paused is true, a "(server restarted — workflow paused)"
// suffix is added to the message.
func buildStatusObject(manager *WorkflowManager, state *specworkflow.WorkflowStateJSON, paused bool) map[string]interface{} {
	// Compute wall clock from StartedAt. For terminal or paused workflows,
	// use UpdatedAt as the end time so the clock stops ticking.
	var wallClockSec float64
	if startTime, parseErr := time.Parse(time.RFC3339, state.StartedAt); parseErr == nil {
		if paused || isTerminalWorkflowState(state.State) {
			if endTime, endErr := time.Parse(time.RFC3339, state.UpdatedAt); endErr == nil {
				if d := endTime.Sub(startTime).Seconds(); d >= 0 {
					wallClockSec = d
				} else {
					wallClockSec = time.Since(startTime).Seconds()
				}
			} else {
				wallClockSec = time.Since(startTime).Seconds()
			}
		} else {
			wallClockSec = time.Since(startTime).Seconds()
		}
	}

	// Best cost from all sources (state, OTEL in-memory, SQLite).
	costUSD := manager.bestCostUSD(state.CumulativeCostUSD, state.FeatureName)

	msg := StatusMessage(state.State, state.Round)
	if paused {
		msg += " (server restarted — workflow paused)"
	}

	isRunning := manager.IsFeatureRunning(state.FeatureName)

	obj := map[string]interface{}{
		"state":              state.State.String(),
		"round":              state.Round,
		"feature_name":       state.FeatureName,
		"cost_usd":           costUSD,
		"wall_clock_seconds": wallClockSec,
		"agent_invocations":  state.AgentInvocations,
		"message":            msg,
		"is_running":         isRunning,
		"workflow_type":      "spec",
	}
	if paused {
		obj["paused"] = true
	}
	return obj
}

// HandleGetMetrics returns an HTTP handler for GET /api/metrics.
// It serves persisted OTEL metrics and events from SQLite so the dashboard
// can restore data after browser refresh or server restart.
// Query parameters:
//   - feature: the feature name to query metrics for (required)
func HandleGetMetrics(store *MetricsStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		if store == nil {
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"metrics": nil,
				"events":  []MetricEvent{},
			})
			return
		}

		feature := r.URL.Query().Get("feature")
		if feature == "" {
			writeError(w, http.StatusBadRequest, "feature query parameter is required")
			return
		}

		metrics, err := store.GetWorkflowMetrics(feature)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("get metrics: %v", err))
			return
		}

		events, err := store.GetEvents(feature)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("get events: %v", err))
			return
		}

		if events == nil {
			events = []MetricEvent{}
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"metrics": metrics,
			"events":  events,
		})
	}
}

// StatusMessage returns a human-readable description of what the workflow is
// doing in the given state and round.
func StatusMessage(state specworkflow.WorkflowState, round int) string {
	switch state {
	case specworkflow.StateInit:
		return "Initializing workflow"
	case specworkflow.StateDiscovery:
		return "Running discovery agent"
	case specworkflow.StateHumanGate1:
		return "Waiting for human gate approval (requirements confirmation)"
	case specworkflow.StateDrafting:
		return "Drafting specification"
	case specworkflow.StateHumanGate2:
		return "Waiting for human gate approval (draft review)"
	case specworkflow.StateReviewing:
		return fmt.Sprintf("Review round %d: dispatching reviewers", round)
	case specworkflow.StateRevising:
		return fmt.Sprintf("Review round %d: revising spec to address findings", round)
	case specworkflow.StateJudging:
		return fmt.Sprintf("Review round %d: judge evaluating convergence", round)
	case specworkflow.StateHumanGateFinal:
		return "Waiting for final human gate approval"
	case specworkflow.StateFinalized:
		return "Spec finalized, advancing to task decomposition"
	case specworkflow.StateTaskify:
		return "Decomposing spec into task graph"
	case specworkflow.StateTaskReview:
		return "Reviewing task graph with dual providers"
	case specworkflow.StateTaskRevision:
		return "Revising task graph to address review findings"
	case specworkflow.StateTaskHumanGate:
		return "Waiting for human approval of task graph"
	case specworkflow.StateTasksApproved:
		return "Tasks approved, creating Beads issues"
	case specworkflow.StateComplete:
		return "Workflow complete"
	case specworkflow.StateEscalated:
		return "Workflow escalated for human intervention"
	case specworkflow.StateError:
		return "Workflow encountered an error"
	default:
		return fmt.Sprintf("Unknown state: %s", state.String())
	}
}

// HandleRetryWorkflow returns an HTTP handler for POST /api/workflow/retry.
// It clears the stale workflow-state.json for the given feature so a new
// workflow can be started.
func HandleRetryWorkflow(manager *WorkflowManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			FeatureName string `json:"feature_name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
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

		// Remove the workflow-state.json file.
		statePath := filepath.Join(manager.workspaceDir, "specs", req.FeatureName, "workflow-state.json")
		if err := os.Remove(statePath); err != nil && !os.IsNotExist(err) {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to remove state file: %v", err))
			return
		}

		log.Printf("[workflow] cleared workflow state for feature %q", req.FeatureName)
		writeJSON(w, http.StatusOK, map[string]string{
			"status":  "cleared",
			"message": "Ready to start a new workflow",
		})
	}
}

// HandleResumeWorkflow returns an HTTP handler for POST /api/workflow/resume.
// It resumes a workflow from ESCALATED, ERROR, or any paused state by
// determining the best state to resume from based on existing artefacts,
// resetting the wall clock timer, and starting a new orchestrator.
func HandleResumeWorkflow(manager *WorkflowManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			FeatureName string `json:"feature_name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
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

		manager.mu.Lock()
		// Cancel existing orchestrator for this feature if any.
		if orch, ok := manager.orchestrators[req.FeatureName]; ok && orch != nil && orch.IsRunning() {
			orch.Cancel()
			delete(manager.orchestrators, req.FeatureName)
			time.Sleep(500 * time.Millisecond)
		}
		manager.mu.Unlock()

		// Load persisted state.
		specDir := filepath.Join(manager.workspaceDir, "specs", req.FeatureName)
		state, err := specworkflow.LoadState(specDir)
		if err != nil {
			writeError(w, http.StatusNotFound, fmt.Sprintf("no workflow state found for %q", req.FeatureName))
			return
		}

		// Determine the best state to resume from based on artefacts on disk.
		resumeState := determineResumeState(state, manager.workspaceDir, req.FeatureName)
		previousState := state.State

		// Update the persisted state: reset timer, set resumable state.
		state.State = resumeState
		state.StartedAt = time.Now().UTC().Format(time.RFC3339)
		if err := specworkflow.SaveState(specDir, state); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to save state: %v", err))
			return
		}

		log.Printf("[workflow] resuming %q from %s (was %s, started_at reset)", req.FeatureName, resumeState, previousState)

		// Create and start a new orchestrator using per-workflow source docs.
		sourcePaths := discoverWorkflowSourceDocs(manager.workspaceDir, req.FeatureName)

		// Wrap the shared emitter so events carry this workflow's feature name.
		beadsClient, beadsErr := newBeadsClientOrNil(manager.workspaceDir)
		if beadsErr != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to create beads client: %v", beadsErr))
			return
		}
		resumeRunner := specworkflow.DefaultClaudeRunner(manager.workspaceDir, manager.otelPort, req.FeatureName, manager.config.ClaudeModels.Default)
		resumeRunner.Tracker = manager.processTracker
		resumeRunner.Feature = req.FeatureName
		orchConfig := specworkflow.OrchestratorConfig{
			WorkspaceDir:   manager.workspaceDir,
			FeatureName:    req.FeatureName,
			SourceDocPaths: sourcePaths,
			Config:         manager.config,
			Runner:         resumeRunner,
			Emitter:        specworkflow.NewFeatureEmitter(manager.emitter, req.FeatureName),
			BeadsClient:    beadsClient,
		}
		if manager.metricsStore != nil {
			orchConfig.CostProvider = NewMetricsCostProvider(manager.metricsStore, req.FeatureName)
		}

		orch, err := specworkflow.NewOrchestrator(orchConfig)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to create orchestrator: %v", err))
			return
		}

		featureName := req.FeatureName // capture for goroutine
		manager.mu.Lock()
		manager.orchestrators[featureName] = orch
		manager.mu.Unlock()

		go func() {
			goal := specworkflow.GoalInput{
				Title:          featureName,
				Description:    "Resumed workflow",
				SourceDocPaths: sourcePaths,
			}
			if err := orch.RunWorkflow(goal); err != nil {
				log.Printf("[workflow] resumed workflow %q completed with error: %v", featureName, err)
			} else {
				log.Printf("[workflow] resumed workflow %q completed successfully", featureName)
			}
		}()

		writeJSON(w, http.StatusOK, map[string]string{
			"status":       "resumed",
			"resume_state": resumeState.String(),
			"message":      fmt.Sprintf("Workflow resumed from %s", resumeState),
		})
	}
}

// determineResumeState figures out the best state to resume a workflow from
// based on what artefacts exist on disk. It works backward from the most
// advanced state that has its prerequisites met, checking the current round
// from the persisted state.
func determineResumeState(state *specworkflow.WorkflowStateJSON, workspaceDir, featureName string) specworkflow.WorkflowState {
	specDir := filepath.Join(workspaceDir, "specs", featureName)
	round := state.Round
	if round < 1 {
		round = 1
	}

	// If the persisted state is a non-terminal, non-idle active workflow state,
	// trust it — the user or a manual rewind has set it deliberately.
	// Artefact-based inference is only a fallback for ESCALATED/COMPLETE/unknown.
	switch specworkflow.WorkflowState(state.State) {
	case specworkflow.StateRevising,
		specworkflow.StateReviewing,
		specworkflow.StateJudging,
		specworkflow.StateDrafting,
		specworkflow.StateDiscovery,
		specworkflow.StateHumanGate1,
		specworkflow.StateHumanGate2,
		specworkflow.StateHumanGateFinal,
		specworkflow.StateFinalized,
		specworkflow.StateTaskify,
		specworkflow.StateTaskReview,
		specworkflow.StateTaskRevision,
		specworkflow.StateTaskHumanGate,
		specworkflow.StateTasksApproved:
		return specworkflow.WorkflowState(state.State)
	}

	// If drafter output exists, we're past drafting. Check how far into
	// the review cycle we got by inspecting artefacts in reverse order.
	drafterPath := filepath.Join(specDir, "drafter-output.json")
	if _, err := os.Stat(drafterPath); err == nil {
		// Drafter output exists. Check if spec exists too.
		specPath := filepath.Join(specDir, "spec-v0.md")
		if _, err := os.Stat(specPath); err != nil {
			// Drafter output but no spec — resume into HUMAN_GATE_2.
			return specworkflow.StateHumanGate2
		}

		// Spec exists — check how far into the review/revise/judge cycle.
		// Work backward: judge -> revising -> reviewing.

		// Check for judge output for this round.
		judgePath := filepath.Join(specDir, fmt.Sprintf("judge-round-%d.json", round))
		if _, err := os.Stat(judgePath); err == nil {
			// Judge completed — the orchestrator loop will evaluate the
			// verdict and decide the next action. Resume into JUDGING so
			// the orchestrator can process the existing output.
			return specworkflow.StateJudging
		}

		// Check for revision output for this round (reviser completed).
		revisionPath := filepath.Join(specDir, fmt.Sprintf("revision-round-%d.json", round))
		if _, err := os.Stat(revisionPath); err == nil {
			// Revision exists — move on to judging.
			return specworkflow.StateJudging
		}

		// Check for merged findings (reviews completed, ready for revising).
		mergedPath := filepath.Join(specDir, fmt.Sprintf("merged-findings-round-%d.json", round))
		if _, err := os.Stat(mergedPath); err == nil {
			// Reviews completed and merged — resume into REVISING.
			return specworkflow.StateRevising
		}

		// Check if any individual review outputs exist for this round.
		if hasReviewOutputs(specDir, round) {
			// Some reviews exist but not merged yet — resume into REVIEWING
			// so the orchestrator can re-run/complete the review dispatch.
			return specworkflow.StateReviewing
		}

		// No review artefacts — start reviewing from scratch.
		return specworkflow.StateReviewing
	}

	// If discovery output exists, we can go to DRAFTING (or HUMAN_GATE_1).
	discoveryPath := filepath.Join(specDir, "discovery-output.json")
	if _, err := os.Stat(discoveryPath); err == nil {
		return specworkflow.StateDrafting
	}

	// Nothing useful on disk — start from discovery.
	return specworkflow.StateDiscovery
}

// hasReviewOutputs checks whether any reviewer output files exist for the
// given round in the spec directory.
func hasReviewOutputs(specDir string, round int) bool {
	for _, letter := range []string{"a", "b", "c", "d"} {
		p := filepath.Join(specDir, fmt.Sprintf("review-%s-round-%d.json", letter, round))
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// HandleRestartWorkflow returns an HTTP handler for POST /api/workflow/restart.
// It stops any running workflow, deletes the feature's spec directory so the
// workflow starts completely fresh, resets persisted metrics, and starts a
// new workflow for the same feature.
func HandleRestartWorkflow(manager *WorkflowManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			FeatureName string `json:"feature_name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
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

		// 1. Cancel running workflow for this feature if any.
		manager.mu.Lock()
		if orch, ok := manager.orchestrators[req.FeatureName]; ok && orch != nil {
			orch.Cancel()
			delete(manager.orchestrators, req.FeatureName)
		}
		manager.mu.Unlock()

		// Give the orchestrator goroutine a moment to exit cleanly.
		time.Sleep(500 * time.Millisecond)

		// 2. Delete the feature's spec directory.
		featureDir := filepath.Join(manager.workspaceDir, "specs", req.FeatureName)
		if err := os.RemoveAll(featureDir); err != nil {
			log.Printf("[workflow] restart: failed to remove %s: %v", featureDir, err)
		}

		// 3. Reset persisted metrics.
		if manager.metricsStore != nil {
			if err := manager.metricsStore.ResetForFeature(req.FeatureName); err != nil {
				log.Printf("[workflow] restart: failed to reset metrics: %v", err)
			}
		}

		// 4. Reset OTEL receiver in-memory accumulators for this feature.
		if manager.otelReceiver != nil {
			manager.otelReceiver.ResetMetrics(req.FeatureName)
		}

		log.Printf("[workflow] restarted feature %q — cancelled, deleted state, reset metrics", req.FeatureName)
		writeJSON(w, http.StatusOK, map[string]string{
			"status":  "restarted",
			"message": fmt.Sprintf("Workflow %q stopped and state cleared. Start a new workflow to begin fresh.", req.FeatureName),
		})
	}
}

// HandleRewindWorkflow returns an HTTP handler for POST /api/workflow/rewind.
// It rewinds a workflow to a previous stage by resetting the workflow state.
// All artefact files are preserved so the workflow re-runs stages using
// existing documents as input. After rewinding, use Resume to continue.
func HandleRewindWorkflow(manager *WorkflowManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			FeatureName string `json:"feature_name"`
			TargetState string `json:"target_state"`
			Round       int    `json:"round"`
			Comment     string `json:"comment,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
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
		if req.TargetState == "" {
			writeError(w, http.StatusBadRequest, "target_state is required")
			return
		}

		// Parse target state.
		targetState, err := specworkflow.ParseWorkflowState(req.TargetState)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid target_state: %v", err))
			return
		}
		if !specworkflow.IsRewindable(targetState) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("cannot rewind to %s", req.TargetState))
			return
		}

		if req.Round < 1 {
			req.Round = 1
		}

		// Cancel any running orchestrator for this feature.
		manager.mu.Lock()
		if orch, ok := manager.orchestrators[req.FeatureName]; ok && orch != nil {
			orch.Cancel()
			delete(manager.orchestrators, req.FeatureName)
			time.Sleep(500 * time.Millisecond)
		}
		manager.mu.Unlock()

		// Load current state from disk.
		specDir := filepath.Join(manager.workspaceDir, "specs", req.FeatureName)
		state, loadErr := specworkflow.LoadState(specDir)
		if loadErr != nil {
			writeError(w, http.StatusNotFound, fmt.Sprintf("no workflow state found for %q", req.FeatureName))
			return
		}

		// Persist comment before rewind (comment must be saved before state change).
		if req.Comment != "" {
			persistHumanComment(specDir, "rewind_to_"+req.TargetState, req.Comment)
		}

		// Perform the rewind.
		result, err := specworkflow.RewindWorkflow(specDir, state, targetState, req.Round)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("rewind failed: %v", err))
			return
		}

		log.Printf("[workflow] rewound %q from %s to %s round %d (artefacts preserved)",
			req.FeatureName, result.PreviousState, result.TargetState, result.Round)

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":         "rewound",
			"previous_state": result.PreviousState.String(),
			"target_state":   result.TargetState.String(),
			"round":          result.Round,
			"files_removed":  0,
			"message":        fmt.Sprintf("Workflow rewound to %s round %d. All artefacts preserved. Use Resume to continue.", result.TargetState, result.Round),
		})
	}
}

// HandleFinalizeWorkflow returns an HTTP handler for POST /api/workflow/finalize.
// It force-transitions a workflow to FINALIZED state, cancelling any running
// orchestrator. This is useful when a workflow is stuck in a non-terminal state
// (e.g. JUDGING) and the user wants to mark it as done without deleting it.
func HandleFinalizeWorkflow(manager *WorkflowManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			FeatureName string `json:"feature_name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
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

		// Cancel running orchestrator if any.
		manager.mu.Lock()
		if orch, ok := manager.orchestrators[req.FeatureName]; ok && orch != nil {
			orch.Cancel()
			delete(manager.orchestrators, req.FeatureName)
		}
		manager.mu.Unlock()

		// Load state from disk and update to FINALIZED.
		featureDir := filepath.Join(manager.workspaceDir, "specs", req.FeatureName)
		state, err := specworkflow.LoadState(featureDir)
		if err != nil {
			writeError(w, http.StatusNotFound, fmt.Sprintf("no workflow state found for feature: %s", req.FeatureName))
			return
		}

		if isTerminalWorkflowState(state.State) {
			writeJSON(w, http.StatusOK, map[string]string{
				"status":  "already_terminal",
				"message": fmt.Sprintf("Workflow %q is already in %s state", req.FeatureName, state.State),
			})
			return
		}

		state.State = specworkflow.StateComplete
		state.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		if err := specworkflow.SaveState(featureDir, state); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to save state: %v", err))
			return
		}

		log.Printf("[workflow] force-completed feature %q from previous state", req.FeatureName)
		writeJSON(w, http.StatusOK, map[string]string{
			"status":  "completed",
			"message": fmt.Sprintf("Workflow %q has been completed", req.FeatureName),
		})
	}
}

// HandleResetWorkflow returns an HTTP handler for POST /api/workflow/reset.
// It deletes the entire workspace/specs/{feature}/ directory so the feature
// can be started completely fresh.
func HandleResetWorkflow(manager *WorkflowManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			FeatureName string `json:"feature_name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
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

		// Cancel and clear the orchestrator if it's running this feature.
		manager.mu.Lock()
		if orch, ok := manager.orchestrators[req.FeatureName]; ok && orch != nil {
			orch.Cancel()
			delete(manager.orchestrators, req.FeatureName)
		}
		manager.mu.Unlock()

		featureDir := filepath.Join(manager.workspaceDir, "specs", req.FeatureName)
		if err := os.RemoveAll(featureDir); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to remove feature directory: %v", err))
			return
		}

		log.Printf("[workflow] reset feature %q — cancelled orchestrator, deleted %s", req.FeatureName, featureDir)
		writeJSON(w, http.StatusOK, map[string]string{
			"status":  "reset",
			"message": "Feature directory deleted",
		})
	}
}

// ---------------------------------------------------------------------------
// Workspace Browser — List Features
// ---------------------------------------------------------------------------

// featureInfo represents a single feature directory in the workspace browser.
type featureInfo struct {
	FeatureName  string   `json:"feature_name"`
	State        string   `json:"state"`
	Round        int      `json:"round"`
	StartedAt    string   `json:"started_at"`
	UpdatedAt    string   `json:"updated_at"`
	CostUSD      float64  `json:"cost_usd"`
	SpecVersions int      `json:"spec_versions"`
	HasDiscovery bool     `json:"has_discovery"`
	HasReviews   bool     `json:"has_reviews"`
	IsTerminal   bool     `json:"is_terminal"`
	IsPaused     bool     `json:"is_paused"`
	IsRunning    bool     `json:"is_running"`
	Files        []string `json:"files"`
	SourceDocs   []string `json:"source_docs"`
}

// HandleListFeatures returns an HTTP handler for GET /api/workspace/features.
// It scans the workspace/specs/ directory for feature subdirectories and returns
// a JSON array of feature info objects sorted by most recently updated.
// The optional manager parameter is used to detect whether the orchestrator is
// actually running, so orphaned agent states can be marked as paused.
func HandleListFeatures(workspaceDir string, manager ...*WorkflowManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		specsDir := filepath.Join(workspaceDir, "specs")
		entries, err := os.ReadDir(specsDir)
		if err != nil {
			if os.IsNotExist(err) {
				writeJSON(w, http.StatusOK, []featureInfo{})
				return
			}
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to read specs directory: %v", err))
			return
		}

		var features []featureInfo
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}

			featureName := entry.Name()
			featureDir := filepath.Join(specsDir, featureName)
			fi := featureInfo{
				FeatureName: featureName,
				State:       "unknown",
				IsTerminal:  true,
			}

			// Read workflow-state.json if it exists.
			state, stateErr := specworkflow.LoadState(featureDir)
			if stateErr == nil && state != nil {
				fi.State = state.State.String()
				fi.Round = state.Round
				fi.StartedAt = state.StartedAt
				fi.UpdatedAt = state.UpdatedAt
				fi.CostUSD = state.CumulativeCostUSD
				fi.IsTerminal = isTerminalWorkflowState(state.State) || state.State == specworkflow.StateError

				// Check if the orchestrator is actively running for this feature.
				// If so, use its live in-memory state so the card doesn't show a
				// stale disk state (e.g. FINALIZED when actually in TASKIFY).
				orchestratorRunning := false
				if len(manager) > 0 && manager[0] != nil {
					orchestratorRunning = manager[0].IsFeatureRunning(featureName)
					if orch := manager[0].GetOrchestrator(featureName); orch != nil {
						if liveState := orch.State(); liveState != nil {
							fi.State = liveState.State.String()
							fi.Round = liveState.Round
							fi.UpdatedAt = liveState.UpdatedAt
							fi.CostUSD = liveState.CumulativeCostUSD
							fi.IsTerminal = isTerminalWorkflowState(liveState.State) || liveState.State == specworkflow.StateError
						}
					}
				}
				fi.IsRunning = orchestratorRunning

				// Detect paused: state is an active agent state but no
				// orchestrator is running (e.g. after server restart).
				if !fi.IsTerminal && !specworkflow.IsGateState(state.State) {
					if !orchestratorRunning {
						fi.IsPaused = true
					}
				}
			}

			// Scan source-docs subdirectory.
			sourceDocsDir := filepath.Join(featureDir, "source-docs")
			if sdEntries, sdErr := os.ReadDir(sourceDocsDir); sdErr == nil {
				for _, sd := range sdEntries {
					if !sd.IsDir() {
						fi.SourceDocs = append(fi.SourceDocs, sd.Name())
					}
				}
			}

			// Scan files in the feature directory.
			fileEntries, _ := os.ReadDir(featureDir)
			for _, f := range fileEntries {
				if f.IsDir() {
					continue
				}
				name := f.Name()
				fi.Files = append(fi.Files, name)

				// Count spec-v*.md files.
				if strings.HasPrefix(name, "spec-v") && strings.HasSuffix(name, ".md") {
					fi.SpecVersions++
				}
				// Check for discovery output.
				if name == "discovery-output.json" {
					fi.HasDiscovery = true
				}
				// Check for review files.
				if strings.HasPrefix(name, "review-") && strings.HasSuffix(name, ".json") {
					fi.HasReviews = true
				}
			}

			features = append(features, fi)
		}

		// Sort by most recently updated (newest first), falling back to name.
		sort.Slice(features, func(i, j int) bool {
			if features[i].UpdatedAt != "" && features[j].UpdatedAt != "" {
				return features[i].UpdatedAt > features[j].UpdatedAt
			}
			if features[i].UpdatedAt != "" {
				return true
			}
			if features[j].UpdatedAt != "" {
				return false
			}
			return features[i].FeatureName < features[j].FeatureName
		})

		writeJSON(w, http.StatusOK, features)
	}
}

// ---------------------------------------------------------------------------
// Feature File Endpoints
// ---------------------------------------------------------------------------

// HandleFeatureFiles returns an HTTP handler that serves files from a feature's
// spec directory. It routes based on the URL path suffix:
//   - /api/workspace/features/{name}/discovery → discovery-output.json
//   - /api/workspace/features/{name}/state → workflow-state.json
//   - /api/workspace/features/{name}/files/{filename} → any file in the spec dir
func HandleFeatureFiles(workspaceDir string, manager ...*WorkflowManager) http.HandlerFunc {
	// If a manager is provided, use it to resolve per-feature workspace overrides.
	var mgr *WorkflowManager
	if len(manager) > 0 {
		mgr = manager[0]
	}
	_ = mgr // used below
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Parse path: /api/workspace/features/{name}/...
		const prefix = "/api/workspace/features/"
		path := r.URL.Path
		if !strings.HasPrefix(path, prefix) {
			http.NotFound(w, r)
			return
		}
		rest := path[len(prefix):]
		// rest is e.g. "my-feature/discovery" or "my-feature/state" or "my-feature/files/foo.json"

		// Split into feature name and sub-path.
		slashIdx := strings.Index(rest, "/")
		if slashIdx < 0 {
			http.NotFound(w, r)
			return
		}
		featureName := rest[:slashIdx]
		subPath := rest[slashIdx+1:]

		if featureName == "" {
			http.NotFound(w, r)
			return
		}

		// Resolve effective workspace (per-feature override or global default).
		effectiveWS := workspaceDir
		if mgr != nil {
			effectiveWS = mgr.GetWorkspaceForFeature(featureName)
		}
		specDir := filepath.Join(effectiveWS, "specs", featureName)

		switch {
		case subPath == "discovery":
			serveJSONFile(w, filepath.Join(specDir, "discovery-output.json"))
		case subPath == "state":
			serveJSONFile(w, filepath.Join(specDir, "workflow-state.json"))
		case subPath == "files":
			serveFeatureFileList(w, specDir)
		case strings.HasPrefix(subPath, "files/"):
			filename := subPath[len("files/"):]
			if filename == "" || strings.Contains(filename, "/") || strings.Contains(filename, "..") {
				http.NotFound(w, r)
				return
			}
			serveJSONFile(w, filepath.Join(specDir, filename))
		default:
			http.NotFound(w, r)
		}
	}
}

// serveJSONFile reads a file from disk and writes it as a JSON response.
func serveJSONFile(w http.ResponseWriter, filePath string) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "file not found")
			return
		}
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to read file: %v", err))
		return
	}

	// Validate it's valid JSON; if not, wrap it as a string.
	var js json.RawMessage
	if json.Unmarshal(data, &js) == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(data)
	} else {
		// Not valid JSON — return as a content wrapper.
		writeJSON(w, http.StatusOK, map[string]string{"content": string(data)})
	}
}

// serveFeatureFileList reads a directory and returns a JSON array of file
// metadata objects: [{name, size, modified}]. Subdirectories are omitted.
func serveFeatureFileList(w http.ResponseWriter, specDir string) {
	entries, err := os.ReadDir(specDir)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusOK, []interface{}{})
			return
		}
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to read directory: %v", err))
		return
	}

	type fileEntry struct {
		Name     string `json:"name"`
		Size     int64  `json:"size"`
		Modified string `json:"modified"`
	}

	files := make([]fileEntry, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, fileEntry{
			Name:     e.Name(),
			Size:     info.Size(),
			Modified: info.ModTime().UTC().Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, files)
}

// ---------------------------------------------------------------------------
// HandleReplayPhase
// ---------------------------------------------------------------------------

// HandleReplayPhase re-runs a specific sub-phase of a workflow using existing
// output files on disk, without re-dispatching agents. This is useful when
// a merge/combine step fails or produces a fixable output — the user can fix
// the input file on disk and replay just the merge step.
func HandleReplayPhase(manager *WorkflowManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			FeatureName string `json:"feature_name"`
			Phase       string `json:"phase"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
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
		if req.Phase == "" {
			writeError(w, http.StatusBadRequest, "phase is required")
			return
		}

		// Validate phase name.
		validPhases := map[string]bool{
			"discovery_merge":   true,
			"drafting_combine":  true,
			"review_merge":      true,
			"task_review_merge": true,
		}
		if !validPhases[req.Phase] {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid phase %q: must be one of discovery_merge, drafting_combine, review_merge, task_review_merge", req.Phase))
			return
		}

		// Check the feature is not currently running.
		if manager.IsFeatureRunning(req.FeatureName) {
			writeError(w, http.StatusConflict, "workflow is currently running — pause or wait for a gate before replaying")
			return
		}

		// Load workflow state from disk.
		workspace := manager.GetWorkspaceForFeature(req.FeatureName)
		specDir := filepath.Join(workspace, "specs", req.FeatureName)
		state, err := specworkflow.LoadState(specDir)
		if err != nil {
			writeError(w, http.StatusNotFound, fmt.Sprintf("no workflow state found for %q", req.FeatureName))
			return
		}

		// Determine the round/version for the replay.
		var message string
		var replayErr error

		switch req.Phase {
		case "discovery_merge":
			round := state.Gate1CorrectionCount + 1
			// Discovery merge requires an agent runner.
			runner := specworkflow.DefaultClaudeRunner(workspace, manager.otelPort, req.FeatureName, manager.config.ClaudeModels.Default)
			runner.Tracker = manager.processTracker
			runner.Feature = req.FeatureName
			runner.Role = "discovery-merge"
			timeout := manager.config.AgentTimeoutSeconds
			if timeout == 0 {
				timeout = 120
			}
			message, replayErr = specworkflow.ReplayDiscoveryMerge(runner, specDir, round, timeout)

		case "drafting_combine":
			version := state.Gate2RedraftCount + 1
			runner := specworkflow.DefaultClaudeRunner(workspace, manager.otelPort, req.FeatureName, manager.config.ClaudeModels.Default)
			runner.Tracker = manager.processTracker
			runner.Feature = req.FeatureName
			runner.Role = "drafting-combine"
			timeout := manager.config.AgentTimeoutSeconds
			if timeout == 0 {
				timeout = 120
			}
			message, replayErr = specworkflow.ReplayDraftingCombine(runner, specDir, version, timeout)

		case "review_merge":
			round := state.Round
			message, replayErr = specworkflow.ReplayReviewMerge(specDir, round)

		case "task_review_merge":
			round := state.TaskReviewRound
			if round == 0 {
				round = 1
			}
			message, replayErr = specworkflow.ReplayTaskReviewMerge(specDir, round)
		}

		if replayErr != nil {
			writeError(w, http.StatusNotFound, fmt.Sprintf("replay failed: %v", replayErr))
			return
		}

		log.Printf("[replay] %s phase=%s feature=%s: %s", r.URL.Path, req.Phase, req.FeatureName, message)

		writeJSON(w, http.StatusOK, map[string]string{
			"status":       "replayed",
			"phase":        req.Phase,
			"feature_name": req.FeatureName,
			"message":      message,
		})
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// ValidateFeatureName rejects feature names that contain path traversal
// patterns or path separators. It returns a descriptive error for invalid
// names and nil for valid ones. This must be called at the HTTP handler
// layer before any business logic uses the name.
func ValidateFeatureName(name string) error {
	if name == "" {
		return fmt.Errorf("feature name must not be empty")
	}
	if strings.Contains(name, "..") {
		return fmt.Errorf("feature name must not contain traversal sequence")
	}
	if strings.Contains(name, "/") || strings.Contains(name, "\\") {
		return fmt.Errorf("feature name must not contain path separator")
	}
	return nil
}

func validateExistingDirectory(fieldName, dir string) error {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("%s is not a valid directory: %s", fieldName, dir)
	}
	return nil
}

// sanitizeFeatureName converts a human title to a URL-safe feature name
// by lowercasing, replacing spaces/special characters with hyphens, and
// trimming.
func sanitizeFeatureName(title string) string {
	if title == "" {
		return ""
	}
	name := strings.ToLower(strings.TrimSpace(title))
	// Replace non-alphanumeric characters with hyphens.
	var b strings.Builder
	prevHyphen := false
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevHyphen = false
		} else if !prevHyphen {
			b.WriteRune('-')
			prevHyphen = true
		}
	}
	result := strings.Trim(b.String(), "-")
	if len(result) > 64 {
		result = result[:64]
	}
	return result
}

// discoverSourceDocs scans the workspace's global source-docs directory
// recursively and returns absolute paths to all files found there. This is
// used only as the source library for copying into per-workflow directories.
func discoverSourceDocs(workspaceDir string) []string {
	docsDir := filepath.Join(workspaceDir, "source-docs")
	var paths []string
	filepath.WalkDir(docsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			paths = append(paths, path)
		}
		return nil
	})
	return paths
}

// discoverWorkflowSourceDocs scans the per-workflow source-docs directory
// (specs/{feature}/source-docs/) and returns absolute paths to all files.
// This is the authoritative source of documents for a running workflow.
func discoverWorkflowSourceDocs(workspaceDir, featureName string) []string {
	docsDir := filepath.Join(workspaceDir, "specs", featureName, "source-docs")
	entries, err := os.ReadDir(docsDir)
	if err != nil {
		return nil
	}

	var paths []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		paths = append(paths, filepath.Join(docsDir, e.Name()))
	}
	return paths
}

// copySourceDocsToWorkflow copies source documents from the global
// source-docs library into the per-workflow directory at
// specs/{feature}/source-docs/. It creates the target directory on demand.
//
// sourcePaths can be:
//   - Absolute paths to individual files in the global source-docs directory
//   - Absolute paths to subdirectories in the global source-docs directory
//     (all files are copied recursively)
//
// Files are always copied FLAT into the workflow source-docs directory
// (folder hierarchy from the library is NOT preserved). Path traversal in
// source paths is rejected.
//
// Returns the new paths (under specs/{feature}/source-docs/) or an error.
func copySourceDocsToWorkflow(workspaceDir, featureName string, sourcePaths []string) ([]string, error) {
	targetDir := filepath.Join(workspaceDir, "specs", featureName, "source-docs")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return nil, fmt.Errorf("create workflow source-docs dir: %w", err)
	}

	// Resolve the canonical global source-docs directory for validation.
	globalDocsDir := filepath.Join(workspaceDir, "source-docs")
	absGlobalDir, err := filepath.Abs(globalDocsDir)
	if err != nil {
		return nil, fmt.Errorf("resolve global source-docs dir: %w", err)
	}

	var newPaths []string
	for _, srcPath := range sourcePaths {
		// If the path is relative, resolve it against the global source-docs directory.
		resolved := srcPath
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(globalDocsDir, resolved)
		}

		// Validate: reject path traversal.
		absSrc, err := filepath.Abs(resolved)
		if err != nil {
			return nil, fmt.Errorf("resolve source path %q: %w", srcPath, err)
		}
		if !strings.HasPrefix(absSrc, absGlobalDir+string(filepath.Separator)) && absSrc != absGlobalDir {
			return nil, fmt.Errorf("source path %q is outside the global source-docs directory", srcPath)
		}

		// Check if the source path is a directory (folder assignment).
		info, statErr := os.Stat(absSrc)
		if statErr != nil {
			return nil, fmt.Errorf("stat source path %q: %w", srcPath, statErr)
		}

		if info.IsDir() {
			// Recursively copy all files from the folder, flattened.
			copied, walkErr := copyDirFlat(absSrc, targetDir)
			if walkErr != nil {
				return nil, fmt.Errorf("copy folder %q: %w", srcPath, walkErr)
			}
			newPaths = append(newPaths, copied...)
			continue
		}

		// Individual file: extract filename — reject any traversals.
		baseName := filepath.Base(absSrc)
		if baseName == "." || baseName == ".." || baseName == "" {
			return nil, fmt.Errorf("invalid source filename: %q", srcPath)
		}

		// Copy the file using io.Copy.
		dstPath := filepath.Join(targetDir, baseName)
		if err := copyFile(absSrc, dstPath); err != nil {
			return nil, fmt.Errorf("copy %q to %q: %w", srcPath, dstPath, err)
		}
		newPaths = append(newPaths, dstPath)
	}

	return newPaths, nil
}

// copyDirFlat recursively copies all files from srcDir into dstDir without
// preserving the source directory hierarchy (flat copy). Returns the list of
// destination paths created.
func copyDirFlat(srcDir, dstDir string) ([]string, error) {
	var copied []string
	err := filepath.WalkDir(srcDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if d.IsDir() {
			return nil
		}
		baseName := filepath.Base(path)
		dstPath := filepath.Join(dstDir, baseName)
		if err := copyFile(path, dstPath); err != nil {
			return fmt.Errorf("copy %q: %w", path, err)
		}
		copied = append(copied, dstPath)
		return nil
	})
	return copied, err
}

// copyFile copies a single file from src to dst using io.Copy.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

// hasAnswerResolution checks if any resolution has the "answer" action.
func hasAnswerResolution(resolutions []specworkflow.AmbiguityResolution) bool {
	for _, r := range resolutions {
		if r.Action == "answer" {
			return true
		}
	}
	return false
}

// tryResumeFromDiskState attempts to find a gate-state workflow on disk and
// resume it. If successful, it returns the resumed orchestrator. If not (no
// gate state found, or resume fails), it writes an error to the response and
// returns nil.
func tryResumeFromDiskState(manager *WorkflowManager, w http.ResponseWriter) *specworkflow.Orchestrator {
	diskState := findLatestDiskState(manager.workspaceDir)
	if diskState == nil || !specworkflow.IsGateState(diskState.State) {
		writeError(w, http.StatusNotFound, "no active workflow")
		return nil
	}

	featureName := diskState.FeatureName
	if featureName == "" {
		writeError(w, http.StatusNotFound, "no active workflow")
		return nil
	}

	orch, err := manager.ResumeFromGate(featureName)
	if err != nil {
		writeError(w, http.StatusConflict, fmt.Sprintf("failed to resume workflow: %v", err))
		return nil
	}

	return orch
}

// findLatestDiskState scans workspace/specs/ for the most recently updated
// non-terminal workflow-state.json. This allows the status endpoint to show
// gate states even after a server restart when no orchestrator is running.
func findLatestDiskState(workspaceDir string) *specworkflow.WorkflowStateJSON {
	specsDir := filepath.Join(workspaceDir, "specs")
	entries, err := os.ReadDir(specsDir)
	if err != nil {
		return nil
	}

	var latest *specworkflow.WorkflowStateJSON
	var latestTime string

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		state, err := specworkflow.LoadState(filepath.Join(specsDir, e.Name()))
		if err != nil {
			continue
		}
		if state.UpdatedAt > latestTime {
			latestTime = state.UpdatedAt
			latest = state
		}
	}
	return latest
}

// ---------------------------------------------------------------------------
// Source-doc assignment handlers
// ---------------------------------------------------------------------------

// HandleAssignSourceDocs returns an HTTP handler for POST /api/workflow/{feature}/source-docs.
// It copies the specified source documents from the global library into the
// workflow's per-feature source-docs directory. This allows assigning docs
// to a running workflow after it has been started.
func HandleAssignSourceDocs(manager *WorkflowManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		featureName := extractFeatureFromWorkflowPath(r.URL.Path)
		if featureName == "" {
			writeError(w, http.StatusBadRequest, "missing feature name in URL")
			return
		}

		if err := ValidateFeatureName(featureName); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid feature name: %v", err))
			return
		}

		var req assignSourceDocsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
			return
		}

		if len(req.SourceDocPaths) == 0 {
			writeError(w, http.StatusBadRequest, "source_doc_paths must not be empty")
			return
		}

		// Resolve relative names to absolute paths within the global source-docs library.
		globalDocsDir := filepath.Join(manager.workspaceDir, "source-docs")
		var absPaths []string
		for _, relPath := range req.SourceDocPaths {
			// Reject path traversal in individual entries.
			if strings.Contains(relPath, "..") {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("path traversal not allowed: %q", relPath))
				return
			}

			absPath := filepath.Join(globalDocsDir, relPath)
			if _, err := os.Stat(absPath); err != nil {
				writeError(w, http.StatusNotFound, fmt.Sprintf("source document not found: %q", relPath))
				return
			}
			absPaths = append(absPaths, absPath)
		}

		// Copy files into the per-workflow source-docs directory.
		copiedPaths, err := copySourceDocsToWorkflow(manager.workspaceDir, featureName, absPaths)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to copy source docs: %v", err))
			return
		}

		// Build response with just the base filenames.
		copiedNames := make([]string, len(copiedPaths))
		for i, p := range copiedPaths {
			copiedNames[i] = filepath.Base(p)
		}

		log.Printf("[source-docs] assigned %d docs to feature %q: %v", len(copiedNames), featureName, copiedNames)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"feature":      featureName,
			"copied_files": copiedNames,
			"count":        len(copiedNames),
		})
	}
}

// sourceDocInfo represents metadata about a source document assigned to a workflow.
type sourceDocInfo struct {
	Name       string    `json:"name"`
	Size       int64     `json:"size"`
	ModifiedAt time.Time `json:"modified_at"`
}

// HandleGetWorkflowSourceDocs returns an HTTP handler for GET /api/workflow/{feature}/source-docs.
// It lists all source documents currently assigned to the workflow.
func HandleGetWorkflowSourceDocs(manager *WorkflowManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		featureName := extractFeatureFromWorkflowPath(r.URL.Path)
		if featureName == "" {
			writeError(w, http.StatusBadRequest, "missing feature name in URL")
			return
		}

		if err := ValidateFeatureName(featureName); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid feature name: %v", err))
			return
		}

		docsDir := filepath.Join(manager.workspaceDir, "specs", featureName, "source-docs")
		entries, err := os.ReadDir(docsDir)
		if err != nil {
			// Directory doesn't exist — return empty array, not an error.
			writeJSON(w, http.StatusOK, []sourceDocInfo{})
			return
		}

		var docs []sourceDocInfo
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			docs = append(docs, sourceDocInfo{
				Name:       e.Name(),
				Size:       info.Size(),
				ModifiedAt: info.ModTime().UTC(),
			})
		}

		// Sort by name for deterministic output.
		sort.Slice(docs, func(i, j int) bool {
			return docs[i].Name < docs[j].Name
		})

		writeJSON(w, http.StatusOK, docs)
	}
}

// findAllDiskStates scans workspace/specs/ and returns all workflow states
// found on disk, keyed by feature name. This is used by the status endpoint
// to return a complete list of all known workflows.
func findAllDiskStates(workspaceDir string) map[string]*specworkflow.WorkflowStateJSON {
	specsDir := filepath.Join(workspaceDir, "specs")
	entries, err := os.ReadDir(specsDir)
	if err != nil {
		return nil
	}

	result := make(map[string]*specworkflow.WorkflowStateJSON)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		state, err := specworkflow.LoadState(filepath.Join(specsDir, e.Name()))
		if err != nil {
			continue
		}
		name := e.Name()
		if state.FeatureName != "" {
			name = state.FeatureName
		}
		result[name] = state
	}
	return result
}

// findAllCRDiskStates scans the code-reviews/ directory for persisted code
// review workflow states and returns them keyed by feature name.
func findAllCRDiskStates(workspaceDir string) map[string]*codereview.CodeReviewStateJSON {
	crDir := filepath.Join(workspaceDir, "code-reviews")
	entries, err := os.ReadDir(crDir)
	if err != nil {
		return make(map[string]*codereview.CodeReviewStateJSON)
	}

	result := make(map[string]*codereview.CodeReviewStateJSON)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		state, err := codereview.LoadCRState(filepath.Join(crDir, e.Name()))
		if err != nil {
			continue
		}
		name := e.Name()
		if state.FeatureName != "" {
			name = state.FeatureName
		}
		result[name] = state
	}
	return result
}

// buildCRUnifiedStatusObject maps a code review state into the same shape as
// the spec workflow status objects, with workflow_type set to "code_review".
func buildCRUnifiedStatusObject(state *codereview.CodeReviewStateJSON, paused bool) map[string]interface{} {
	var wallClockSec float64
	if startTime, parseErr := time.Parse(time.RFC3339, state.StartedAt); parseErr == nil {
		if paused || state.State.IsTerminal() {
			if endTime, endErr := time.Parse(time.RFC3339, state.UpdatedAt); endErr == nil {
				if d := endTime.Sub(startTime).Seconds(); d >= 0 {
					wallClockSec = d
				} else {
					wallClockSec = time.Since(startTime).Seconds()
				}
			} else {
				wallClockSec = time.Since(startTime).Seconds()
			}
		} else {
			wallClockSec = time.Since(startTime).Seconds()
		}
	}

	msg := state.State.String()
	if paused {
		msg += " (server restarted — workflow paused)"
	}

	obj := map[string]interface{}{
		"state":              state.State.String(),
		"round":              state.Round,
		"feature_name":       state.FeatureName,
		"cost_usd":           state.CumulativeCostUSD,
		"wall_clock_seconds": wallClockSec,
		"agent_invocations":  state.AgentInvocations,
		"message":            msg,
		"is_running":         !paused && !state.State.IsTerminal(),
		"workflow_type":      "code_review",
	}
	if paused {
		obj["paused"] = true
	}
	return obj
}

// findAllCDDiskStates scans the workspace/codedoc/ directory for persisted
// codedoc workflow states.
func findAllCDDiskStates(workspaceDir string) map[string]*codedoc.CDStateJSON {
	cdDir := filepath.Join(workspaceDir, "codedoc")
	entries, err := os.ReadDir(cdDir)
	if err != nil {
		return make(map[string]*codedoc.CDStateJSON)
	}

	result := make(map[string]*codedoc.CDStateJSON)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		state, err := codedoc.LoadCDState(filepath.Join(cdDir, e.Name()))
		if err != nil {
			continue
		}
		name := e.Name()
		if state.FeatureName != "" {
			name = state.FeatureName
		}
		result[name] = state
	}
	return result
}

// buildCDUnifiedStatusObject maps a codedoc state into the same shape as
// the spec workflow status objects, with workflow_type set to "codedoc".
func buildCDUnifiedStatusObject(state *codedoc.CDStateJSON, paused bool) map[string]interface{} {
	var wallClockSec float64
	if startTime, parseErr := time.Parse(time.RFC3339, state.StartedAt); parseErr == nil {
		if paused || state.State.IsTerminal() {
			if endTime, endErr := time.Parse(time.RFC3339, state.UpdatedAt); endErr == nil {
				if d := endTime.Sub(startTime).Seconds(); d >= 0 {
					wallClockSec = d
				} else {
					wallClockSec = time.Since(startTime).Seconds()
				}
			} else {
				wallClockSec = time.Since(startTime).Seconds()
			}
		} else {
			wallClockSec = time.Since(startTime).Seconds()
		}
	}

	msg := state.State.String()
	if paused {
		msg += " (server restarted — workflow paused)"
	}

	obj := map[string]interface{}{
		"state":              state.State.String(),
		"round":              state.Round,
		"feature_name":       state.FeatureName,
		"cost_usd":           state.CumulativeCostUSD,
		"wall_clock_seconds": wallClockSec,
		"agent_invocations":  state.AgentInvocations,
		"message":            msg,
		"is_running":         !paused && !state.State.IsTerminal() && !state.State.IsGate(),
		"workflow_type":      "codedoc",
	}
	if paused {
		obj["paused"] = true
	}
	return obj
}
