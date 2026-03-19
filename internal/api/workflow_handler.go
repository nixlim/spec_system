// Package api provides HTTP handlers for the adversarial spec system.
// This file implements the workflow control endpoints that bridge the HTTP
// API to the Orchestrator: starting workflows, responding to gates, and
// cancelling running workflows.
package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/foundry-zero/adversarial-spec-system/internal/specworkflow"
)

// isTerminalWorkflowState reports whether the given workflow state is terminal
// (FINALIZED or ESCALATED), meaning a new workflow can safely be started.
func isTerminalWorkflowState(s specworkflow.WorkflowState) bool {
	return s == specworkflow.StateFinalized || s == specworkflow.StateEscalated
}

// ---------------------------------------------------------------------------
// WorkflowManager
// ---------------------------------------------------------------------------

// WorkflowManager coordinates the lifecycle of concurrent workflow runs.
// It owns a map of Orchestrators keyed by feature name, a ChannelEmitter,
// and WebSocketHub, and provides HTTP handler factories for the workflow
// control endpoints. All map operations are protected by a sync.RWMutex.
type WorkflowManager struct {
	orchestrators map[string]*specworkflow.Orchestrator
	emitter       *specworkflow.ChannelEmitter
	hub           *WebSocketHub
	workspaceDir  string
	config        specworkflow.SpecWorkflowConfig
	otelPort      int
	metricsStore  *MetricsStore
	otelReceiver  *OTELReceiver
	mu            sync.RWMutex
}

// NewWorkflowManager creates a WorkflowManager with the given dependencies.
func NewWorkflowManager(
	emitter *specworkflow.ChannelEmitter,
	hub *WebSocketHub,
	workspaceDir string,
	config specworkflow.SpecWorkflowConfig,
) *WorkflowManager {
	return &WorkflowManager{
		orchestrators: make(map[string]*specworkflow.Orchestrator),
		emitter:       emitter,
		hub:           hub,
		workspaceDir:  workspaceDir,
		config:        config,
	}
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

// SetOTELReceiver configures the OTEL receiver reference so the status
// endpoint can read in-memory cost data as a fallback.
func (m *WorkflowManager) SetOTELReceiver(recv *OTELReceiver) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.otelReceiver = recv
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
	sourcePaths := discoverSourceDocs(m.workspaceDir)
	orchConfig := specworkflow.OrchestratorConfig{
		WorkspaceDir:   m.workspaceDir,
		FeatureName:    featureName,
		SourceDocPaths: sourcePaths,
		Config:         m.config,
		Runner:         specworkflow.DefaultClaudeRunner(m.workspaceDir, m.otelPort, featureName),
		Emitter:        m.emitter,
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

		// Check for an existing workflow state on disk before creating a new one.
		absWorkspace, _ := filepath.Abs(manager.workspaceDir)
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

		// Resolve source doc paths — if none provided, scan source-docs directory.
		sourcePaths := req.SourceDocPaths
		if len(sourcePaths) == 0 {
			sourcePaths = discoverSourceDocs(manager.workspaceDir)
		}

		// Create orchestrator config.
		orchConfig := specworkflow.OrchestratorConfig{
			WorkspaceDir:   manager.workspaceDir,
			FeatureName:    req.FeatureName,
			SourceDocPaths: sourcePaths,
			Config:         manager.config,
			Runner:         specworkflow.DefaultClaudeRunner(manager.workspaceDir, manager.otelPort, req.FeatureName),
			Emitter:        manager.emitter,
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

		orch := manager.GetOrchestrator()
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

			switch req.Action {
			case "confirm":
				// Save user answers.
				if req.UserAnswers != nil {
					persistGateData(specDir, "user-answers.json", req.UserAnswers)
				}
			case "correct":
				// Save corrections + user answers + reviewer comment.
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
				persistGateData(specDir, "gate1-corrections.json", corrData)
			default:
				// Gate 2 resolutions.
				if len(req.Resolutions) > 0 {
					persistGateData(specDir, "gate2-resolutions.json", map[string]interface{}{
						"resolutions": req.Resolutions,
					})
				}
			}

			// Always persist comment to human-comments.json (append).
			if req.Comment != "" {
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

		orch := manager.GetOrchestrator()
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
			writeError(w, http.StatusConflict, fmt.Sprintf("cancel failed: %v", err))
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
// It returns the current workflow state as a JSON object with a human-readable
// message describing what the workflow is doing.
func HandleGetWorkflowStatus(manager *WorkflowManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		orch := manager.GetOrchestrator()
		if orch == nil {
			// No orchestrator running — check for on-disk workflow state
			// so that gate states survive server restarts.
			diskState := findLatestDiskState(manager.workspaceDir)
			if diskState != nil && !isTerminalWorkflowState(diskState.State) {
				// Compute wall clock dynamically from StartedAt.
				var wallClockSec float64
				if startTime, parseErr := time.Parse(time.RFC3339, diskState.StartedAt); parseErr == nil {
					wallClockSec = time.Since(startTime).Seconds()
				}

				// Best cost from all sources (state, OTEL in-memory, SQLite).
				costUSD := manager.bestCostUSD(diskState.CumulativeCostUSD, diskState.FeatureName)

				writeJSON(w, http.StatusOK, map[string]interface{}{
					"state":              diskState.State.String(),
					"round":              diskState.Round,
					"feature_name":       diskState.FeatureName,
					"cost_usd":           costUSD,
					"wall_clock_seconds": wallClockSec,
					"agent_invocations":  diskState.AgentInvocations,
					"message":            StatusMessage(diskState.State, diskState.Round) + " (server restarted — workflow paused)",
					"paused":             true,
				})
				return
			}
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"state":   "idle",
				"message": "No workflow running",
			})
			return
		}

		// Check state even if the goroutine hasn't set running=true yet,
		// to avoid a race where the UI polls before RunWorkflow enters its loop.
		state := orch.State()
		if state == nil || (!orch.IsRunning() && isTerminalWorkflowState(state.State)) {
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"state":   "idle",
				"message": "No workflow running",
			})
			return
		}

		// Compute wall clock dynamically from StartedAt so the HTTP
		// endpoint returns real elapsed time (not the stale struct field).
		var wallClockSec float64
		if startTime, parseErr := time.Parse(time.RFC3339, state.StartedAt); parseErr == nil {
			wallClockSec = time.Since(startTime).Seconds()
		}

		// Best cost from all sources (state, OTEL in-memory, SQLite).
		costUSD := manager.bestCostUSD(state.CumulativeCostUSD, state.FeatureName)

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"state":              state.State.String(),
			"round":              state.Round,
			"feature_name":       state.FeatureName,
			"cost_usd":           costUSD,
			"wall_clock_seconds": wallClockSec,
			"agent_invocations":  state.AgentInvocations,
			"message":            StatusMessage(state.State, state.Round),
		})
	}
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
		return "Workflow complete: spec finalized"
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

		// Update the persisted state: reset timer, set resumable state.
		state.State = resumeState
		state.StartedAt = time.Now().UTC().Format(time.RFC3339)
		if err := specworkflow.SaveState(specDir, state); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to save state: %v", err))
			return
		}

		log.Printf("[workflow] resuming %q from %s (was %s, started_at reset)", req.FeatureName, resumeState, state.State)

		// Create and start a new orchestrator.
		sourcePaths := discoverSourceDocs(manager.workspaceDir)
		// Also check per-workflow source docs.
		featureSourceDir := filepath.Join(specDir, "source-docs")
		if entries, err := os.ReadDir(featureSourceDir); err == nil {
			for _, e := range entries {
				if !e.IsDir() {
					sourcePaths = append(sourcePaths, filepath.Join(featureSourceDir, e.Name()))
				}
			}
		}

		orchConfig := specworkflow.OrchestratorConfig{
			WorkspaceDir:   manager.workspaceDir,
			FeatureName:    req.FeatureName,
			SourceDocPaths: sourcePaths,
			Config:         manager.config,
			Runner:         specworkflow.DefaultClaudeRunner(manager.workspaceDir, manager.otelPort, req.FeatureName),
			Emitter:        manager.emitter,
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
// It rewinds a workflow to a previous stage, preserving all accumulated context
// that feeds into the target stage while removing artefacts that come after it.
// After rewinding, the workflow can be resumed from the target state.
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

		// Perform the rewind.
		result, err := specworkflow.RewindWorkflow(specDir, state, targetState, req.Round)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("rewind failed: %v", err))
			return
		}

		log.Printf("[workflow] rewound %q from %s to %s round %d, removed %d files",
			req.FeatureName, result.PreviousState, result.TargetState, result.Round, len(result.FilesRemoved))

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":         "rewound",
			"previous_state": result.PreviousState.String(),
			"target_state":   result.TargetState.String(),
			"round":          result.Round,
			"files_removed":  len(result.FilesRemoved),
			"message":        fmt.Sprintf("Workflow rewound to %s round %d. Use Resume to continue.", result.TargetState, result.Round),
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
	Files        []string `json:"files"`
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

				// Detect paused: state is an active agent state but no
				// orchestrator is running (e.g. after server restart).
				if !fi.IsTerminal && !specworkflow.IsGateState(state.State) {
					orchestratorRunning := false
					if len(manager) > 0 && manager[0] != nil {
						orchestratorRunning = manager[0].IsFeatureRunning(featureName)
					}
					if !orchestratorRunning {
						fi.IsPaused = true
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
func HandleFeatureFiles(workspaceDir string) http.HandlerFunc {
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

		specDir := filepath.Join(workspaceDir, "specs", featureName)

		switch {
		case subPath == "discovery":
			serveJSONFile(w, filepath.Join(specDir, "discovery-output.json"))
		case subPath == "state":
			serveJSONFile(w, filepath.Join(specDir, "workflow-state.json"))
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

// discoverSourceDocs scans the workspace's source-docs directory and returns
// absolute paths to all files found there.
func discoverSourceDocs(workspaceDir string) []string {
	docsDir := filepath.Join(workspaceDir, "source-docs")
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
