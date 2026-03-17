// Package specworkflow defines the core types for the adversarial spec review
// workflow. This file implements the main orchestration loop that drives the
// state machine through the full workflow lifecycle: discovery, drafting,
// adversarial review, revision, judging, and finalization.
package specworkflow

import (
	"fmt"
	"log"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// ---------------------------------------------------------------------------
// GoalInput
// ---------------------------------------------------------------------------

// GoalInput describes the user's goal for the adversarial spec workflow,
// including the feature title, a natural-language description, and paths to
// any source documents to be analysed during discovery.
type GoalInput struct {
	// Title is the human-readable feature name.
	Title string
	// Description is a natural-language description of the feature.
	Description string
	// SourceDocPaths lists filesystem paths to source documents.
	SourceDocPaths []string
}

// ---------------------------------------------------------------------------
// GateResponse
// ---------------------------------------------------------------------------

// GateResponse carries a human's response to a gate prompt. Action is one of
// "confirm", "correct", "cancel", "accept", "reject", or "resolve". Data is
// action-specific payload (e.g. corrections map or AmbiguityResolution slice).
type GateResponse struct {
	// Action is the gate action taken by the human.
	Action string
	// Data carries action-specific structured data.
	Data interface{}
}

// ---------------------------------------------------------------------------
// OrchestratorConfig
// ---------------------------------------------------------------------------

// OrchestratorConfig holds the parameters required to construct an Orchestrator.
type OrchestratorConfig struct {
	// WorkspaceDir is the root directory for all workflow artefacts.
	WorkspaceDir string
	// FeatureName is the human-readable feature name used in directory paths.
	FeatureName string
	// SourceDocPaths lists filesystem paths to source documents.
	SourceDocPaths []string
	// Config is the workflow configuration.
	Config SpecWorkflowConfig
	// Runner is the agent execution backend.
	Runner AgentRunner
	// Emitter broadcasts workflow events to subscribers.
	Emitter EventEmitter
}

// ---------------------------------------------------------------------------
// Orchestrator
// ---------------------------------------------------------------------------

// Orchestrator drives the adversarial spec review workflow by coordinating
// the state machine, agent dispatch, issue tracking, convergence checks,
// circuit breakers, and human gates. Only one workflow may be active at a
// time; concurrent calls to RunWorkflow are rejected.
type Orchestrator struct {
	config          SpecWorkflowConfig
	sm              *StateMachine
	tracker         *IssueTracker
	logger          *WorkflowLogger
	emitter         EventEmitter
	promptBuilder   *PromptBuilder
	skills          *SkillCache
	progressTracker *ProgressTracker
	runner          AgentRunner
	cancelled       atomic.Bool
	mu              sync.Mutex
	running         bool
	workspaceDir    string
	featureName     string

	// Gate channel for human gate responses.
	gateCh chan GateResponse

	// Cumulative authority-limit tracking across rounds.
	cumulativeDowngrades int
	cumulativeDismissals int

	// issueHistory tracks per-finding status over rounds for staleness detection.
	// Keys are finding IDs; values are the status recorded at the end of each round.
	issueHistory map[string][]string
}

// NewOrchestrator creates a fully initialised Orchestrator from the given
// configuration. It creates workspace directories, loads skills, and wires
// up all sub-components. Returns an error if directory creation, skill
// loading, or logger initialisation fails.
func NewOrchestrator(cfg OrchestratorConfig) (*Orchestrator, error) {
	return newOrchestrator(cfg)
}

// Tracker returns the orchestrator's issue tracker. Returns nil if the
// orchestrator is not initialised.
func (o *Orchestrator) Tracker() *IssueTracker {
	return o.tracker
}

// State returns the current workflow state snapshot. Returns nil if the
// orchestrator's state machine is not initialised.
func (o *Orchestrator) State() *WorkflowStateJSON {
	if o.sm == nil {
		return nil
	}
	return o.sm.State()
}

// IsRunning reports whether a workflow is currently executing.
func (o *Orchestrator) IsRunning() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.running
}

// Cancel signals the orchestrator to stop at the next safe point. The
// cancellation is checked before every agent dispatch.
func (o *Orchestrator) Cancel() {
	o.cancelled.Store(true)
}

// HandleGateResponse delivers a human gate response to the running workflow.
// Returns an error if no gate is currently waiting for input.
func (o *Orchestrator) HandleGateResponse(response GateResponse) error {
	select {
	case o.gateCh <- response:
		return nil
	default:
		return fmt.Errorf("no gate is currently waiting for a response")
	}
}

// RunWorkflow executes the full adversarial spec review workflow for the
// given goal. It drives the state machine from INIT through FINALIZED or
// ESCALATED. Only one workflow may run at a time; concurrent calls return
// an error. Returns nil on successful completion (FINALIZED or ESCALATED).
func (o *Orchestrator) RunWorkflow(goal GoalInput) error {
	// Reject concurrent workflows.
	o.mu.Lock()
	if o.running {
		o.mu.Unlock()
		return fmt.Errorf("a workflow is already running")
	}
	o.running = true
	o.mu.Unlock()

	defer func() {
		o.mu.Lock()
		o.running = false
		o.mu.Unlock()
		o.logger.Close()
	}()

	state := o.sm.State()
	specDir := filepath.Join(o.workspaceDir, "specs", o.featureName)

	log.Printf("[orchestrator] starting workflow: feature=%s, specDir=%s, sourceDocPaths=%v",
		o.featureName, specDir, goal.SourceDocPaths)
	log.Printf("[orchestrator] config: maxRounds=%d, minRounds=%d, maxCost=$%.2f, maxRetries=%d",
		o.config.MaxRounds, o.config.MinRounds, o.config.MaxCostUSD, o.config.MaxRetries)
	log.Printf("[orchestrator] skills loaded: planSpec=%q, grillSpec=%q",
		o.config.SkillPaths.PlanSpec, o.config.SkillPaths.GrillSpec)

	for {
		if o.cancelled.Load() {
			log.Printf("[orchestrator] workflow cancelled")
			return fmt.Errorf("workflow cancelled")
		}

		current := o.sm.Current()
		log.Printf("[orchestrator] ---- loop iteration: state=%s, round=%d, cost=$%.4f, invocations=%d ----",
			current.String(), state.Round, state.CumulativeCostUSD, state.AgentInvocations)

		// Emit a workflow_status heartbeat at the top of each iteration so
		// the dashboard can track progress in real time.
		var wallClockSec float64
		if startTime, parseErr := time.Parse(time.RFC3339, state.StartedAt); parseErr == nil {
			wallClockSec = time.Since(startTime).Seconds()
		}
		o.emitter.Emit(NewWorkflowStatusEvent(
			current.String(),
			state.Round,
			state.FeatureName,
			state.CumulativeCostUSD,
			wallClockSec,
			state.AgentInvocations,
		))

		switch current {
		case StateInit:
			// Transition to DISCOVERY.
			o.logTransition(StateInit, StateDiscovery)
			if err := o.sm.Transition(StateDiscovery); err != nil {
				return fmt.Errorf("transition INIT -> DISCOVERY: %w", err)
			}

		case StateDiscovery:
			if err := o.handleDiscovery(goal, state, specDir); err != nil {
				return err
			}

		case StateHumanGate1:
			if err := o.handleHumanGate1(state, specDir); err != nil {
				return err
			}

		case StateDrafting:
			if err := o.handleDrafting(state, specDir); err != nil {
				return err
			}

		case StateHumanGate2:
			if err := o.handleHumanGate2(state, specDir); err != nil {
				return err
			}

		case StateReviewing:
			if err := o.handleReviewing(state, specDir); err != nil {
				return err
			}

		case StateRevising:
			if err := o.handleRevising(state, specDir); err != nil {
				return err
			}

		case StateJudging:
			if err := o.handleJudging(state, specDir); err != nil {
				return err
			}

		case StateHumanGateFinal:
			if err := o.handleHumanGateFinal(state, specDir); err != nil {
				return err
			}

		case StateFinalized:
			return o.handleFinalized(state, specDir)

		case StateEscalated:
			return o.handleEscalated(state, specDir)

		case StateError:
			return fmt.Errorf("workflow in ERROR state")

		default:
			return fmt.Errorf("unexpected state: %s", current)
		}
	}
}

// newOrchestrator contains the full construction logic for NewOrchestrator.
func newOrchestrator(cfg OrchestratorConfig) (*Orchestrator, error) {
	// Validate the agent team configuration to ensure all required agents
	// are defined. This catches misconfiguration at construction time rather
	// than at dispatch time.
	teamCfg := DefaultTeamConfig()
	if err := ValidateTeamConfig(teamCfg); err != nil {
		return nil, fmt.Errorf("invalid team configuration: %w", err)
	}

	// Create workspace directories.
	specDir := filepath.Join(cfg.WorkspaceDir, "specs", cfg.FeatureName)
	sourceDocDir := filepath.Join(cfg.WorkspaceDir, "source-docs")

	if err := mkdirAll(specDir, sourceDocDir); err != nil {
		return nil, err
	}

	// Load skills from config paths.
	skills, err := loadSkillsFromConfig(cfg)
	if err != nil {
		return nil, err
	}

	// Create logger.
	logger, err := NewWorkflowLogger(specDir)
	if err != nil {
		return nil, fmt.Errorf("create logger: %w", err)
	}

	// Try to load existing state from disk for crash/restart recovery.
	// If a persisted state exists, the orchestrator will resume from that
	// state instead of starting fresh.
	var ws *WorkflowStateJSON
	existingState, loadErr := LoadState(specDir)
	if loadErr == nil && existingState != nil {
		ws = existingState
		log.Printf("[orchestrator] restored persisted state: feature=%s, state=%s, round=%d",
			cfg.FeatureName, ws.State, ws.Round)
	} else {
		// Initialise fresh workflow state.
		now := time.Now().UTC().Format(time.RFC3339)
		ws = &WorkflowStateJSON{
			State:          StateInit,
			Round:          1,
			FeatureName:    cfg.FeatureName,
			StartedAt:      now,
			UpdatedAt:      now,
			SkillChecksums: skills.GetChecksums(),
		}
	}

	// Create state machine with persistence callback.
	smConfig := StateMachineConfig{
		MaxGateCorrections: cfg.Config.MaxGateCorrections,
		MaxRounds:          cfg.Config.MaxRounds,
	}

	onTransition := func(state *WorkflowStateJSON) error {
		return SaveState(specDir, state)
	}

	sm := NewStateMachine(ws, smConfig, onTransition)

	// If we loaded an existing state, restore it on the state machine so
	// the main loop starts from the persisted state rather than INIT.
	if existingState != nil {
		sm.RestoreState(ws)
	}

	// Create prompt builder.
	promptBuilder := NewPromptBuilder(skills, cfg.WorkspaceDir, cfg.FeatureName)

	emitter := cfg.Emitter
	if emitter == nil {
		emitter = NewChannelEmitter(64)
	}

	orch := &Orchestrator{
		config:          cfg.Config,
		sm:              sm,
		tracker:         NewIssueTracker(),
		logger:          logger,
		emitter:         emitter,
		promptBuilder:   promptBuilder,
		skills:          skills,
		progressTracker: NewProgressTracker(),
		runner:          cfg.Runner,
		workspaceDir:    cfg.WorkspaceDir,
		featureName:     cfg.FeatureName,
		gateCh:          make(chan GateResponse, 1),
		issueHistory:    make(map[string][]string),
	}

	return orch, nil
}
