// Package specworkflow defines the core types for the adversarial spec review
// workflow. This file implements the main orchestration loop that drives the
// state machine through the full workflow lifecycle: discovery, drafting,
// adversarial review, revision, judging, and finalization.
package specworkflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/foundry-zero/adversarial-spec-system/internal/process"
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
	// CodePath is an optional path to the target code repository. When set,
	// the discovery agent will explore the codebase to inform its analysis.
	CodePath string
}

// ---------------------------------------------------------------------------
// Feature name validation
// ---------------------------------------------------------------------------

// featureNamePattern matches valid feature names: alphanumeric, hyphens, underscores.
var featureNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// ValidateFeatureName checks that the feature name contains only alphanumeric
// characters, hyphens, and underscores. It returns an error for empty strings
// or names containing colons, slashes, spaces, or other special characters.
// This validation prevents corruption of the KV namespace which uses colons
// as separators.
func ValidateFeatureName(name string) error {
	if name == "" {
		return fmt.Errorf("feature name must not be empty")
	}
	if !featureNamePattern.MatchString(name) {
		return fmt.Errorf("Invalid feature name %q: must match [a-zA-Z0-9_-]+", name)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Filesystem lock file
// ---------------------------------------------------------------------------

// lockFilePath returns the path for the workflow lock file.
func workflowLockPath(workspaceDir, featureName string) string {
	return filepath.Join(workspaceDir, featureName+".workflow.lock")
}

// acquireLock creates a lock file containing the current PID. If a lock file
// already exists with a running PID, it returns an error. If the PID is stale
// (process not running), the lock file is removed and a new one is created.
func acquireLock(lockPath string) error {
	data, err := os.ReadFile(lockPath)
	if err == nil {
		// Lock file exists — check if the PID is still running.
		pidStr := strings.TrimSpace(string(data))
		pid, parseErr := strconv.Atoi(pidStr)
		if parseErr == nil && pid > 0 {
			if isProcessRunning(pid) {
				return fmt.Errorf("Another orchestrator process may be running for this feature (PID %d) — found lock file at %s. If no process is running, delete the lock file and retry.", pid, lockPath)
			}
			// Stale lock — PID is not running.
			log.Printf("[orchestrator] WARNING: Stale lock file found (PID %d not running) — removing and continuing", pid)
		}
		// Remove stale or unparseable lock file.
		_ = os.Remove(lockPath)
	}

	// Write new lock file with our PID.
	currentPID := os.Getpid()
	return os.WriteFile(lockPath, []byte(strconv.Itoa(currentPID)), 0o644)
}

// releaseLock removes the lock file. Called on clean exit only.
func releaseLock(lockPath string) {
	if err := os.Remove(lockPath); err != nil && !os.IsNotExist(err) {
		log.Printf("[orchestrator] WARNING: failed to remove lock file %s: %v", lockPath, err)
	}
}

// isProcessRunning checks if a process with the given PID exists and is running.
func isProcessRunning(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, sending signal 0 checks if the process exists without
	// actually sending a signal.
	err = proc.Signal(syscall.Signal(0))
	return err == nil
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
	// Comment is optional free-text from the human reviewer. Persisted to
	// human-comments.json and included as context for downstream agents.
	Comment string
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
	// CostProvider supplies authoritative cost data from external telemetry
	// (e.g. OTEL receiver). When set, the orchestrator syncs this cost into
	// workflow state so the dashboard reflects real cost even when the CLI
	// reports zero. Optional — if nil, only CLI-reported cost is used.
	CostProvider CostProvider
	// LookPathFunc is an optional override for exec.LookPath, used for testing.
	LookPathFunc func(file string) (string, error)
	// BeadsClient is an optional Beads integration client. When nil, all Beads
	// operations are silently skipped (graceful degradation).
	BeadsClient BeadsClientInterface
	// ProcessTracker is an optional tracker for subprocess lifecycle events.
	// When set, internally created CodexRunners inherit it so their
	// subprocesses are tracked alongside ClaudeRunner subprocesses.
	ProcessTracker *process.ProcessTracker
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
	codexRunner            AgentRunner
	codexHoldoutRunner     AgentRunner
	codexDiscoveryRunner   AgentRunner
	codexDraftingRunner    AgentRunner
	opencodeRunner          AgentRunner
	opencodeHoldoutRunner   AgentRunner
	opencodeDiscoveryRunner AgentRunner
	opencodeDraftingRunner  AgentRunner
	cancelled       atomic.Bool
	runCtx          context.Context
	runCancel       context.CancelFunc
	mu              sync.Mutex
	running         bool
	workspaceDir    string
	featureName     string
	codePath        string
	lockFilePath    string

	// Gate channel for human gate responses.
	gateCh chan GateResponse

	// costProvider supplies authoritative cost from external telemetry (e.g. OTEL).
	costProvider CostProvider

	// Cumulative authority-limit tracking across rounds.
	cumulativeDowngrades int
	cumulativeDismissals int

	// issueHistory tracks per-finding status over rounds for staleness detection.
	// Keys are finding IDs; values are the status recorded at the end of each round.
	issueHistory map[string][]string

	// beadsClient is the optional Beads integration client. When nil, all
	// Beads operations are silently skipped (graceful degradation).
	beadsClient BeadsClientInterface

	// runEpicID caches the Beads run epic ID for comment posting.
	runEpicID string

	// activeGateBeadID is the Beads bead ID of the currently active gate proxy
	// task, if any. Used by closeOpenGateProxies to clean up on error/escalation.
	activeGateBeadID string
	activeGateName   string

	// activeAgents tracks currently dispatched agents by name with their status.
	activeAgentsMu sync.RWMutex
	activeAgents   map[string]string // agent name -> status ("running", "done", "failed")
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

// Cancel signals the orchestrator to stop immediately. It sets a cancellation
// flag (checked between phases) and cancels the run context, which kills any
// currently-running agent subprocess via exec.CommandContext.
func (o *Orchestrator) Cancel() {
	o.cancelled.Store(true)
	o.mu.Lock()
	cancel := o.runCancel
	o.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// AgentStatus represents a currently tracked agent and its status.
type AgentStatus struct {
	Name   string `json:"name"`
	Status string `json:"status"` // "running", "done", "failed"
}

// SetAgentStatus updates the status of an active agent.
func (o *Orchestrator) SetAgentStatus(name, status string) {
	o.activeAgentsMu.Lock()
	defer o.activeAgentsMu.Unlock()
	if status == "" {
		delete(o.activeAgents, name)
	} else {
		o.activeAgents[name] = status
	}
}

// GetActiveAgents returns a snapshot of agents currently in "running" status.
// Completed agents ("done"/"failed") are excluded so the UI only shows what
// is actually executing right now.
func (o *Orchestrator) GetActiveAgents() []AgentStatus {
	o.activeAgentsMu.RLock()
	defer o.activeAgentsMu.RUnlock()
	agents := make([]AgentStatus, 0, len(o.activeAgents))
	for name, status := range o.activeAgents {
		if status == "running" {
			agents = append(agents, AgentStatus{Name: name, Status: status})
		}
	}
	return agents
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
	runCtx, runCancel := context.WithCancel(context.Background())
	o.runCtx = runCtx
	o.runCancel = runCancel
	o.mu.Unlock()

	defer func() {
		runCancel() // free context resources on any exit path
		o.mu.Lock()
		o.running = false
		o.runCtx = nil
		o.runCancel = nil
		o.mu.Unlock()
		o.logger.Close()
		// Release filesystem lock on clean exit. The lock is intentionally
		// NOT released on SIGTERM/crash so the next startup can detect and
		// clean up the stale lock automatically.
		releaseLock(o.lockFilePath)
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

		// Sync authoritative cost from OTEL telemetry if available.
		// The CLI may report zero cost while OTEL captures real cost, so
		// we use the greater of the two to avoid losing data.
		if o.costProvider != nil {
			if otelCost := o.costProvider.GetCostUSD(); otelCost > state.CumulativeCostUSD {
				state.CumulativeCostUSD = otelCost
			}
		}

		// Compute wall-clock seconds and store in state for persistence
		// and HTTP API access.
		var wallClockSec float64
		if startTime, parseErr := time.Parse(time.RFC3339, state.StartedAt); parseErr == nil {
			wallClockSec = time.Since(startTime).Seconds()
			state.CumulativeWallClockSeconds = wallClockSec
		}

		// Emit a workflow_status heartbeat at the top of each iteration so
		// the dashboard can track progress in real time.
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
			// Create Beads run epic before first state transition.
			o.createRunEpic(state)

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
			if err := o.handleFinalized(state, specDir); err != nil {
				return err
			}

		case StateTaskify:
			if err := o.handleTaskify(state, specDir); err != nil {
				return err
			}

		case StateTaskReview:
			if err := o.handleTaskReview(state, specDir); err != nil {
				return err
			}

		case StateTaskRevision:
			if err := o.handleTaskRevision(state, specDir); err != nil {
				return err
			}

		case StateTaskHumanGate:
			if err := o.handleTaskHumanGate(state, specDir); err != nil {
				return err
			}

		case StateTasksApproved:
			o.logTransition(StateTasksApproved, StateComplete)
			if err := o.sm.Transition(StateComplete); err != nil {
				return fmt.Errorf("transition TASKS_APPROVED -> COMPLETE: %w", err)
			}

		case StateComplete:
			if err := SaveState(specDir, state); err != nil {
				log.Printf("warning: failed to save complete state: %v", err)
			}
			log.Printf("[orchestrator] workflow completed successfully")
			return nil

		case StateEscalated:
			return o.handleEscalated(state, specDir)

		case StateError:
			return fmt.Errorf("workflow in ERROR state")

		default:
			return fmt.Errorf("unexpected state: %s", current)
		}
	}
}

// runnerFor returns an AgentRunner for the given role with per-role model
// overrides, role tagging, and cancellation context applied. Works with any
// AgentRunner implementation (Claude, OpenCode, etc.) via optional interfaces.
func (o *Orchestrator) runnerFor(role string) AgentRunner {
	runner := o.runner

	// Clone first for telemetry attribution — this must happen before
	// model/context injection so those values aren't lost by the clone.
	if tagger, ok := runner.(AgentTagger); ok {
		runner = tagger.CloneForAgent(role)
	}

	// Apply per-role model override via the ModelOverrider interface.
	if model := o.modelForRole(role); model != "" {
		if mo, ok := runner.(ModelOverrider); ok {
			runner = mo.WithModelOverride(model)
		}
	}

	// Inject cancellation context via the ContextInjector interface.
	o.mu.Lock()
	ctx := o.runCtx
	o.mu.Unlock()
	if ctx != nil {
		if ci, ok := runner.(ContextInjector); ok {
			runner = ci.WithContext(ctx)
		}
	}
	return runner
}

// modelForRole returns the model string for a given role, consulting
// the appropriate config based on the primary provider.
func (o *Orchestrator) modelForRole(role string) string {
	if o.config.PrimaryProvider == "opencode" {
		return o.config.OpenCodeModels.For(role)
	}
	return o.config.ClaudeModels.For(role)
}

// primaryProviderName returns the label used in versioned filenames for
// the primary provider.
func (o *Orchestrator) primaryProviderName() string {
	return normalizePrimaryProvider(o.config.PrimaryProvider)
}

// normalizePrimaryProvider returns "opencode" when the value is "opencode",
// and "claude" for any other value (including empty string).
func normalizePrimaryProvider(p string) string {
	if p == "opencode" {
		return "opencode"
	}
	return "claude"
}

// newOrchestrator contains the full construction logic for NewOrchestrator.
func newOrchestrator(cfg OrchestratorConfig) (*Orchestrator, error) {
	// Validate feature name before any Beads, KV, or filesystem operations.
	if err := ValidateFeatureName(cfg.FeatureName); err != nil {
		return nil, err
	}

	// Acquire filesystem lock to prevent concurrent orchestrator processes.
	lockPath := workflowLockPath(cfg.WorkspaceDir, cfg.FeatureName)
	if err := acquireLock(lockPath); err != nil {
		return nil, err
	}

	// Wire Beads integration (graceful degradation — Beads is optional).
	var beadsClient BeadsClientInterface
	if cfg.BeadsClient != nil {
		ctx := context.Background()
		if err := cfg.BeadsClient.EnsureCustomTypesConfigured(ctx); err != nil {
			if errors.Is(err, ErrBeadsUnavailable) {
				log.Printf("[orchestrator] WARNING: bd not available — Beads integration disabled")
			} else {
				log.Printf("[orchestrator] WARNING: Beads custom type setup failed — Beads integration disabled: %v", err)
			}
		} else {
			beadsClient = cfg.BeadsClient
			log.Printf("[orchestrator] Beads integration enabled")
		}
	}

	// Detect codex CLI availability when codex reviewers are enabled.
	var codexRunner AgentRunner
	var codexHoldoutRunner AgentRunner
	if cfg.Config.EnableCodexReviewers {
		lookPath := cfg.LookPathFunc
		if lookPath == nil {
			lookPath = exec.LookPath
		}
		if _, err := lookPath("codex"); err == nil {
			cr := DefaultCodexRunner(cfg.Config.CodexModel, cfg.WorkspaceDir, ReviewerOutputSchema())
			cr.Tracker = cfg.ProcessTracker
			cr.Feature = cfg.FeatureName
			cr.Role = "reviewer"
			codexRunner = cr
			ch := DefaultCodexRunner(cfg.Config.CodexModel, cfg.WorkspaceDir, HoldoutOutputSchema())
			ch.Tracker = cfg.ProcessTracker
			ch.Feature = cfg.FeatureName
			ch.Role = "holdout"
			codexHoldoutRunner = ch
			log.Printf("[orchestrator] codex CLI detected — dual-provider review enabled")
		} else {
			log.Printf("[orchestrator] WARNING: codex CLI not found — codex reviewers disabled, running claude-only")
		}
	}

	// Detect codex CLI availability for drafting when enabled.
	var codexDraftingRunner AgentRunner
	if cfg.Config.EnableCodexDrafting {
		lookPathDraft := cfg.LookPathFunc
		if lookPathDraft == nil {
			lookPathDraft = exec.LookPath
		}
		if _, err := lookPathDraft("codex"); err == nil {
			cdr := DefaultCodexRunner(cfg.Config.CodexModel, cfg.WorkspaceDir, DrafterOutputSchema())
			cdr.Tracker = cfg.ProcessTracker
			cdr.Feature = cfg.FeatureName
			cdr.Role = "drafter"
			codexDraftingRunner = cdr
			log.Printf("[orchestrator] codex CLI detected — dual-provider drafting enabled")
		} else {
			log.Printf("[orchestrator] Codex unavailable, falling back to Claude-only drafting")
		}
	}

	// Detect codex CLI availability for discovery when enabled.
	var codexDiscoveryRunner AgentRunner
	if cfg.Config.EnableCodexDiscovery {
		lookPath2 := cfg.LookPathFunc
		if lookPath2 == nil {
			lookPath2 = exec.LookPath
		}
		if _, err := lookPath2("codex"); err == nil {
			cdis := DefaultCodexRunner(cfg.Config.CodexModel, cfg.WorkspaceDir, DiscoveryOutputSchema())
			cdis.Tracker = cfg.ProcessTracker
			cdis.Feature = cfg.FeatureName
			cdis.Role = "discovery"
			codexDiscoveryRunner = cdis
			log.Printf("[orchestrator] codex CLI detected — dual-provider discovery enabled")
		} else {
			log.Printf("[orchestrator] Codex CLI not available, falling back to Claude-only discovery")
		}
	}

	// Detect opencode CLI availability when opencode reviewers are enabled.
	var opencodeRunner AgentRunner
	var opencodeHoldoutRunner AgentRunner
	if cfg.Config.EnableOpenCodeReviewers {
		lookPathOC := cfg.LookPathFunc
		if lookPathOC == nil {
			lookPathOC = exec.LookPath
		}
		if _, err := lookPathOC("opencode"); err == nil {
			ocr := DefaultOpenCodeRunner(cfg.Config.OpenCodeModels.For("reviewer"), cfg.WorkspaceDir, ReviewerOutputSchema())
			ocr.Tracker = cfg.ProcessTracker
			ocr.Feature = cfg.FeatureName
			ocr.Role = "reviewer"
			opencodeRunner = ocr
			och := DefaultOpenCodeRunner(cfg.Config.OpenCodeModels.For("holdout"), cfg.WorkspaceDir, HoldoutOutputSchema())
			och.Tracker = cfg.ProcessTracker
			och.Feature = cfg.FeatureName
			och.Role = "holdout"
			opencodeHoldoutRunner = och
			log.Printf("[orchestrator] opencode CLI detected — triple-provider review enabled")
		} else {
			log.Printf("[orchestrator] WARNING: opencode CLI not found — opencode reviewers disabled")
		}
	}

	// Detect opencode CLI availability for drafting when enabled.
	var opencodeDraftingRunner AgentRunner
	if cfg.Config.EnableOpenCodeDrafting {
		lookPathOCD := cfg.LookPathFunc
		if lookPathOCD == nil {
			lookPathOCD = exec.LookPath
		}
		if _, err := lookPathOCD("opencode"); err == nil {
			ocdr := DefaultOpenCodeRunner(cfg.Config.OpenCodeModels.For("drafter"), cfg.WorkspaceDir, DrafterOutputSchema())
			ocdr.Tracker = cfg.ProcessTracker
			ocdr.Feature = cfg.FeatureName
			ocdr.Role = "drafter"
			opencodeDraftingRunner = ocdr
			log.Printf("[orchestrator] opencode CLI detected — triple-provider drafting enabled")
		} else {
			log.Printf("[orchestrator] opencode CLI not available for drafting")
		}
	}

	// Detect opencode CLI availability for discovery when enabled.
	var opencodeDiscoveryRunner AgentRunner
	if cfg.Config.EnableOpenCodeDiscovery {
		lookPathOCDis := cfg.LookPathFunc
		if lookPathOCDis == nil {
			lookPathOCDis = exec.LookPath
		}
		if _, err := lookPathOCDis("opencode"); err == nil {
			ocdis := DefaultOpenCodeRunner(cfg.Config.OpenCodeModels.For("discovery"), cfg.WorkspaceDir, DiscoveryOutputSchema())
			ocdis.Tracker = cfg.ProcessTracker
			ocdis.Feature = cfg.FeatureName
			ocdis.Role = "discovery"
			opencodeDiscoveryRunner = ocdis
			log.Printf("[orchestrator] opencode CLI detected — triple-provider discovery enabled")
		} else {
			log.Printf("[orchestrator] opencode CLI not available for discovery")
		}
	}

	// Validate the agent team configuration to ensure all required agents
	// are defined. This catches misconfiguration at construction time rather
	// than at dispatch time.
	teamCfg := DefaultTeamConfig(codexRunner != nil)
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
			State:           StateInit,
			Round:           1,
			FeatureName:     cfg.FeatureName,
			StartedAt:       now,
			UpdatedAt:       now,
			SkillChecksums:  skills.GetChecksums(),
			PrimaryProvider: normalizePrimaryProvider(cfg.Config.PrimaryProvider),
		}
	}

	// On resume, reject provider mismatch to prevent broken workflows.
	if existingState != nil {
		persisted := normalizePrimaryProvider(existingState.PrimaryProvider)
		configured := normalizePrimaryProvider(cfg.Config.PrimaryProvider)
		if persisted != configured {
			return nil, fmt.Errorf(
				"cannot resume workflow: started with primary_provider=%q but config now specifies %q",
				persisted, configured,
			)
		}
	}

	// Create state machine with persistence callback.
	smConfig := StateMachineConfig{
		MaxGateCorrections: cfg.Config.MaxGateCorrections,
		MaxGate2Redrafts:   cfg.Config.MaxGate2Redrafts,
		MaxRounds:          cfg.Config.MaxRounds,
	}

	// orchPtr is captured by the onTransition closure and set after
	// construction. This allows the persistence callback to invoke Beads
	// KV writes without a circular dependency.
	var orchPtr *Orchestrator
	onTransition := func(state *WorkflowStateJSON) error {
		if err := SaveState(specDir, state); err != nil {
			return err
		}
		// Write state snapshot and convergence round to Beads KV.
		if orchPtr != nil {
			orchPtr.writeBeadsStateSnapshot(state)
		}
		return nil
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

	// Build issue tracker — if resuming, reload findings from disk so the
	// dashboard and downstream agents have the full issue history.
	tracker := NewIssueTracker()
	if existingState != nil {
		ReloadFindings(tracker, specDir, existingState.Round)
		// Sync the findings summary into the persisted state so the
		// dashboard API reflects the reloaded findings immediately.
		ws.FindingsSummary = tracker.GetFindingSummary()
	}

	// Use cfg.Runner as-is — the caller (API layer via DefaultPrimaryRunner)
	// is responsible for constructing the correct runner based on PrimaryProvider.
	primaryRunner := cfg.Runner
	log.Printf("[orchestrator] primary provider: %s", normalizePrimaryProvider(cfg.Config.PrimaryProvider))

	orch := &Orchestrator{
		config:          cfg.Config,
		sm:              sm,
		tracker:         tracker,
		logger:          logger,
		emitter:         emitter,
		promptBuilder:   promptBuilder,
		skills:          skills,
		progressTracker: NewProgressTracker(),
		runner:          primaryRunner,
		codexRunner:            codexRunner,
		codexHoldoutRunner:     codexHoldoutRunner,
		codexDiscoveryRunner:   codexDiscoveryRunner,
		codexDraftingRunner:    codexDraftingRunner,
		opencodeRunner:          opencodeRunner,
		opencodeHoldoutRunner:   opencodeHoldoutRunner,
		opencodeDiscoveryRunner: opencodeDiscoveryRunner,
		opencodeDraftingRunner:  opencodeDraftingRunner,
		beadsClient:     beadsClient,
		costProvider:    cfg.CostProvider,
		workspaceDir:    cfg.WorkspaceDir,
		featureName:     cfg.FeatureName,
		lockFilePath:    lockPath,
		gateCh:          make(chan GateResponse, 1),
		issueHistory:    make(map[string][]string),
		activeAgents:    make(map[string]string),
	}

	// Set the orchPtr so the onTransition persistence callback can invoke
	// Beads KV writes now that the orchestrator is fully constructed.
	orchPtr = orch

	// Wire Beads status sync callback on the issue tracker so every
	// TransitionIssue call propagates to Beads automatically.
	if beadsClient != nil {
		tracker.OnTransition = orch.updateBeadsIssueStatus

		// On crash recovery, rebuild finding-to-bead-ID mappings from Beads
		// so that status updates continue to propagate correctly (CD-88v.17).
		if existingState != nil {
			orch.rebuildIssueTrackerFromBeads(tracker, existingState.RunID)
		}
	}

	// On crash recovery into REVIEWING, clean stale reviewer outputs that
	// may be incomplete from the interrupted round (FR-028/MAJ-004).
	if existingState != nil && existingState.State == StateReviewing {
		cleanStaleReviewerOutputs(specDir, existingState.Round)
	}

	return orch, nil
}

// ReloadFindings loads merged findings from all rounds up to maxRound back
// into the IssueTracker, then replays revision and judge outputs to restore
// the full lifecycle state (raised → addressed → verified → closed). This is
// needed when resuming a workflow so the dashboard and downstream agents have
// accurate issue status. It is also called by the API layer to populate the
// tracker from disk when no orchestrator is running.
func ReloadFindings(tracker *IssueTracker, specDir string, maxRound int) {
	for round := 1; round <= maxRound; round++ {
		// Step 1: Load merged findings for this round.
		mergedPath := filepath.Join(specDir, fmt.Sprintf("merged-findings-round-%d.json", round))
		data, err := os.ReadFile(mergedPath)
		if err != nil {
			continue // file doesn't exist for this round
		}

		var merged MergedFindings
		if err := json.Unmarshal(data, &merged); err != nil {
			log.Printf("[orchestrator] warning: failed to parse %s: %v", mergedPath, err)
			continue
		}

		tracker.AddFindings(merged.Findings)
		log.Printf("[orchestrator] reloaded %d findings from round %d", len(merged.Findings), round)

		// Step 2: Replay revision output (raised → addressed).
		revisionPath := filepath.Join(specDir, fmt.Sprintf("revision-round-%d.json", round))
		if revData, err := os.ReadFile(revisionPath); err == nil {
			var revision RevisionOutput
			if err := json.Unmarshal(revData, &revision); err == nil {
				if warnings, err := tracker.ApplyRevisionChanges(&revision, round); err != nil {
					log.Printf("[orchestrator] warning: replay revision round %d: %v", round, err)
				} else {
					for _, w := range warnings {
						log.Printf("[orchestrator] replay revision round %d: %s", round, w)
					}
				}
			}
		}

		// Step 3: Replay judge output (addressed → verified, etc.).
		// During replay, transition errors on already-terminal findings are
		// expected (e.g. a round-2 judge referencing a finding that was
		// closed after round 1). These are logged as warnings, not errors.
		judgePath := filepath.Join(specDir, fmt.Sprintf("judge-round-%d.json", round))
		if judgeData, err := os.ReadFile(judgePath); err == nil {
			var judge JudgeOutput
			if err := json.Unmarshal(judgeData, &judge); err == nil {
				if warnings, err := tracker.ApplyJudgeUpdates(&judge, round); err != nil {
					// Downgrade transition errors to warnings during replay.
					log.Printf("[orchestrator] warning: replay judge round %d: %v", round, err)
				} else {
					for _, w := range warnings {
						log.Printf("[orchestrator] replay judge round %d: %s", round, w)
					}
				}
				tracker.CloseVerifiedFindings(round)
				// A PASS verdict implicitly verifies all addressed findings —
				// mirror the same logic that handleJudging applies at runtime
				// so the replayed tracker matches the live state.
				if judge.Verdict == VerdictPass {
					tracker.AutoVerifyAddressedFindings(round)
				}
			}
		}
	}

	summary := tracker.GetFindingSummary()
	log.Printf("[orchestrator] issue tracker restored: %d raised, %d closed, %d open critical, %d open major",
		summary.Raised, summary.Closed, summary.OpenCritical, summary.OpenMajor)
}
