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
	"strings"
	"sync"

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

// WorkflowManager coordinates the lifecycle of a single workflow run.
// It owns the Orchestrator, ChannelEmitter, and WebSocketHub, and provides
// HTTP handler factories for the workflow control endpoints.
type WorkflowManager struct {
	orchestrator *specworkflow.Orchestrator
	emitter      *specworkflow.ChannelEmitter
	hub          *WebSocketHub
	workspaceDir string
	config       specworkflow.SpecWorkflowConfig
	otelPort     int
	mu           sync.Mutex
}

// NewWorkflowManager creates a WorkflowManager with the given dependencies.
func NewWorkflowManager(
	emitter *specworkflow.ChannelEmitter,
	hub *WebSocketHub,
	workspaceDir string,
	config specworkflow.SpecWorkflowConfig,
) *WorkflowManager {
	return &WorkflowManager{
		emitter:      emitter,
		hub:          hub,
		workspaceDir: workspaceDir,
		config:       config,
	}
}

// SetOTELPort configures the OTEL port used for child process telemetry.
// Must be called before starting any workflows.
func (m *WorkflowManager) SetOTELPort(port int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.otelPort = port
}

// GetOrchestrator returns the current orchestrator, or nil if no workflow
// has been started.
func (m *WorkflowManager) GetOrchestrator() *specworkflow.Orchestrator {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.orchestrator
}

// GetTracker returns the issue tracker from the current orchestrator,
// or a new empty tracker if no workflow is running.
func (m *WorkflowManager) GetTracker() *specworkflow.IssueTracker {
	m.mu.Lock()
	orch := m.orchestrator
	m.mu.Unlock()

	if orch != nil {
		return orch.Tracker()
	}
	return specworkflow.NewIssueTracker()
}

// GetState returns the workflow state from the current orchestrator,
// or a default state if no workflow is running.
func (m *WorkflowManager) GetState() *specworkflow.WorkflowStateJSON {
	m.mu.Lock()
	orch := m.orchestrator
	m.mu.Unlock()

	if orch != nil {
		return orch.State()
	}
	return &specworkflow.WorkflowStateJSON{}
}

// CancelWorkflow cancels the running workflow, if any.
func (m *WorkflowManager) CancelWorkflow() error {
	m.mu.Lock()
	orch := m.orchestrator
	m.mu.Unlock()

	if orch == nil {
		return fmt.Errorf("no active workflow to cancel")
	}
	orch.Cancel()
	return nil
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
		if manager.orchestrator != nil && manager.orchestrator.IsRunning() {
			manager.mu.Unlock()
			writeError(w, http.StatusConflict, "a workflow is already running")
			return
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
			Runner:         specworkflow.DefaultClaudeRunner(manager.workspaceDir, manager.otelPort),
			Emitter:        manager.emitter,
		}

		orch, err := specworkflow.NewOrchestrator(orchConfig)
		if err != nil {
			manager.mu.Unlock()
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to create orchestrator: %v", err))
			return
		}

		manager.orchestrator = orch
		manager.mu.Unlock()

		// Run workflow in background.
		goal := specworkflow.GoalInput{
			Title:          req.Title,
			Description:    req.Description,
			SourceDocPaths: sourcePaths,
		}

		go func() {
			if err := orch.RunWorkflow(goal); err != nil {
				log.Printf("workflow completed with error: %v", err)
			} else {
				log.Printf("workflow completed successfully")
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
func HandleGateApprove(manager *WorkflowManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		orch := manager.GetOrchestrator()
		if orch == nil {
			writeError(w, http.StatusNotFound, "no active workflow")
			return
		}

		var req gateApproveRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
			return
		}

		// Determine the gate response based on the request content.
		var gateResp specworkflow.GateResponse
		switch req.Action {
		case "confirm":
			gateResp = specworkflow.GateResponse{Action: "confirm"}
		case "correct":
			gateResp = specworkflow.GateResponse{
				Action: "correct",
				Data:   req.Corrections,
			}
		case "accept":
			gateResp = specworkflow.GateResponse{Action: "accept"}
		default:
			// For gate 2 resolutions (no explicit action field).
			if len(req.Resolutions) > 0 || req.Action == "" {
				if hasAnswerResolution(req.Resolutions) {
					gateResp = specworkflow.GateResponse{
						Action: "resolve",
						Data:   req.Resolutions,
					}
				} else {
					gateResp = specworkflow.GateResponse{
						Action: "confirm",
					}
				}
			} else {
				gateResp = specworkflow.GateResponse{Action: req.Action}
			}
		}

		if err := orch.HandleGateResponse(gateResp); err != nil {
			writeError(w, http.StatusConflict, fmt.Sprintf("gate response failed: %v", err))
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "accepted"})
	}
}

// HandleGateReject returns an HTTP handler for POST /api/tasks/{id}/reject.
// It delivers a cancellation/rejection gate response to the orchestrator.
func HandleGateReject(manager *WorkflowManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		orch := manager.GetOrchestrator()
		if orch == nil {
			writeError(w, http.StatusNotFound, "no active workflow")
			return
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
// the running workflow via the WorkflowManager.
func HandleCancelWorkflowAPI(manager *WorkflowManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		if err := manager.CancelWorkflow(); err != nil {
			writeError(w, http.StatusConflict, fmt.Sprintf("cancel failed: %v", err))
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
	}
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

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"state":              state.State.String(),
			"round":              state.Round,
			"feature_name":       state.FeatureName,
			"cost_usd":           state.CumulativeCostUSD,
			"wall_clock_seconds": state.CumulativeWallClockSeconds,
			"agent_invocations":  state.AgentInvocations,
			"message":            StatusMessage(state.State, state.Round),
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

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

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
