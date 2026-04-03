package codereview

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/foundry-zero/adversarial-spec-system/internal/specworkflow"
)

// ---------------------------------------------------------------------------
// StartCodeReviewRequest
// ---------------------------------------------------------------------------

// StartCodeReviewRequest contains the parameters for starting a code review.
type StartCodeReviewRequest struct {
	// CodePath is the filesystem path to the target git repository (required).
	CodePath string
	// FeatureName is the kebab-case identifier for this code review (required).
	FeatureName string
	// SpecPath is the optional path to a spec markdown file.
	SpecPath string
	// TaskListPath is the optional path to a task list file.
	TaskListPath string
}

// kebabCaseRegex matches valid kebab-case identifiers.
var kebabCaseRegex = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ---------------------------------------------------------------------------
// CRGateResponse
// ---------------------------------------------------------------------------

// CRGateResponse carries a human's response to a code review gate.
type CRGateResponse struct {
	// Action is the gate action: "confirm", "cancel", "re-review", "accept", "escalate".
	Action string
	// Comment is optional free-text feedback.
	Comment string
}

// ---------------------------------------------------------------------------
// GitInfoProvider
// ---------------------------------------------------------------------------

// GitInfoProvider abstracts git operations for testing.
type GitInfoProvider interface {
	// IsGitRepo returns true if the path is a git repository.
	IsGitRepo(path string) bool
	// GetBranch returns the current branch name.
	GetBranch(path string) (string, error)
	// GetHeadSHA returns the HEAD commit SHA.
	GetHeadSHA(path string) (string, error)
}

// defaultGitInfoProvider uses exec.Command to query git.
type defaultGitInfoProvider struct{}

func (g *defaultGitInfoProvider) IsGitRepo(path string) bool {
	cmd := exec.Command("git", "-C", path, "rev-parse", "--git-dir")
	return cmd.Run() == nil
}

func (g *defaultGitInfoProvider) GetBranch(path string) (string, error) {
	cmd := exec.Command("git", "-C", path, "rev-parse", "--abbrev-ref", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("get git branch: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (g *defaultGitInfoProvider) GetHeadSHA(path string) (string, error) {
	cmd := exec.Command("git", "-C", path, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("get git HEAD SHA: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// ---------------------------------------------------------------------------
// CodeReviewOrchestrator
// ---------------------------------------------------------------------------

// CROrchestratorConfig holds the parameters required to construct a
// CodeReviewOrchestrator.
type CROrchestratorConfig struct {
	// WorkspaceDir is the root directory for all workflow artefacts.
	WorkspaceDir string
	// Config is the code review configuration.
	Config CodeReviewConfig
	// GitProvider abstracts git operations (nil = default).
	GitProvider GitInfoProvider
	// Runner is the primary agent runner (Claude).
	Runner specworkflow.AgentRunner
	// CodexRunner is the optional Codex agent runner (nil = Claude-only).
	CodexRunner specworkflow.AgentRunner
	// FixRunner is the runner for the fix agent, constructed with
	// --allowedTools "Read,Write,Bash" (no --dangerously-skip-permissions).
	// If nil, falls back to Runner.
	FixRunner specworkflow.AgentRunner
	// Emitter broadcasts workflow events via WebSocket.
	Emitter specworkflow.EventEmitter
	// AuditLogger writes structured events to JSONL audit log (nil = no logging).
	AuditLogger *CRAuditLogger
	// OTELPort is the gRPC OTLP receiver port for telemetry (0 = disabled).
	OTELPort int
}

// CodeReviewOrchestrator drives a single code review workflow through its
// lifecycle.
type CodeReviewOrchestrator struct {
	workspaceDir string
	featureDir   string
	config       CodeReviewConfig
	sm           *CRStateMachine
	gitProvider  GitInfoProvider
	runner       specworkflow.AgentRunner
	codexRunner  specworkflow.AgentRunner
	fixRunner    specworkflow.AgentRunner
	emitter      specworkflow.EventEmitter
	// roundCounts tracks CRITICAL+MAJOR count per completed round for staleness.
	roundCounts []int
	// lastFixOutput holds the most recent fix agent output for gate display.
	lastFixOutput *FixOutput
	// cancelled is set by Cancel() and checked before agent dispatch.
	cancelled atomic.Bool
	// running is set to 1 in Run() entry and 0 on exit.
	running atomic.Int32
	// crEmitter wraps the base emitter with code-review-specific methods.
	crEmitter *CREventEmitter
	// auditLogger writes structured events to the JSONL audit log.
	auditLogger *CRAuditLogger
	// otelPort is the OTLP receiver port for child process telemetry.
	otelPort int
}

// NewCodeReviewOrchestrator creates a new orchestrator instance.
func NewCodeReviewOrchestrator(cfg CROrchestratorConfig) *CodeReviewOrchestrator {
	gp := cfg.GitProvider
	if gp == nil {
		gp = &defaultGitInfoProvider{}
	}
	return &CodeReviewOrchestrator{
		workspaceDir: cfg.WorkspaceDir,
		config:       cfg.Config,
		gitProvider:  gp,
		runner:       cfg.Runner,
		codexRunner:  cfg.CodexRunner,
		fixRunner:    cfg.FixRunner,
		emitter:      cfg.Emitter,
		auditLogger:  cfg.AuditLogger,
		otelPort:     cfg.OTELPort,
	}
}

// Start validates inputs, creates the workspace, snapshots git state, and
// transitions to CR_HUMAN_GATE_SCOPE.
func (o *CodeReviewOrchestrator) Start(req StartCodeReviewRequest) error {
	// Validate feature_name.
	if !kebabCaseRegex.MatchString(req.FeatureName) {
		return fmt.Errorf("feature_name must match ^[a-z0-9]+(-[a-z0-9]+)*$ (kebab-case), got %q", req.FeatureName)
	}

	// Validate code_path: no path traversal.
	if strings.Contains(req.CodePath, "..") {
		return fmt.Errorf("code_path must not contain path traversal segments (..), got %q", req.CodePath)
	}

	// Validate code_path: exists.
	info, err := os.Stat(req.CodePath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("code_path does not exist: %s", req.CodePath)
		}
		return fmt.Errorf("code_path stat error: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("code_path is not a directory: %s", req.CodePath)
	}

	// Validate code_path: is a git repository.
	if !o.gitProvider.IsGitRepo(req.CodePath) {
		return fmt.Errorf("code_path is not a git repository: %s", req.CodePath)
	}

	// Validate spec_path if provided.
	if req.SpecPath != "" {
		if _, err := os.Stat(req.SpecPath); err != nil {
			return fmt.Errorf("spec file not found: %s", req.SpecPath)
		}
	}

	// Create workspace directory.
	o.featureDir = filepath.Join(o.workspaceDir, "code-reviews", req.FeatureName)
	if err := os.MkdirAll(o.featureDir, 0755); err != nil {
		return fmt.Errorf("create workspace directory: %w", err)
	}

	// Snapshot git state.
	branch, err := o.gitProvider.GetBranch(req.CodePath)
	if err != nil {
		return fmt.Errorf("snapshot git state: %w", err)
	}
	headSHA, err := o.gitProvider.GetHeadSHA(req.CodePath)
	if err != nil {
		return fmt.Errorf("snapshot git state: %w", err)
	}

	// Detect grill-code mode.
	mode := DetermineGrillCodeMode(req.SpecPath, req.TaskListPath)

	// Build initial state.
	now := time.Now().UTC().Format(time.RFC3339)
	ws := &CodeReviewStateJSON{
		State:         CRInit,
		Round:         1,
		FeatureName:   req.FeatureName,
		CodePath:      req.CodePath,
		SpecPath:      req.SpecPath,
		TaskListPath:  req.TaskListPath,
		GrillCodeMode: mode,
		GitBranch:     branch,
		GitHeadSHA:    headSHA,
		CommitMode:    o.config.CommitMode,
		StartedAt:     now,
		UpdatedAt:     now,
	}

	// Create state machine.
	smCfg := CRStateMachineConfigFromConfig(&o.config)
	o.sm = NewCRStateMachine(ws, smCfg, func(ws *CodeReviewStateJSON) error {
		return SaveCRState(o.featureDir, ws)
	})

	// Create event emitter.
	o.crEmitter = NewCREventEmitter(o.emitter, req.FeatureName)

	// Transition to CR_HUMAN_GATE_SCOPE.
	if err := o.sm.Transition(CRHumanGateScope); err != nil {
		return fmt.Errorf("transition to scope gate: %w", err)
	}

	// Emit scope gate request event.
	o.crEmitter.EmitGateRequest("scope", []string{"confirm", "cancel"})

	// Audit log.
	if o.auditLogger != nil {
		o.auditLogger.LogCodeReviewStart(req.FeatureName, req.CodePath, req.SpecPath, req.TaskListPath, mode)
	}

	return nil
}

// HandleScopeGate processes the human response at the scope gate.
func (o *CodeReviewOrchestrator) HandleScopeGate(resp CRGateResponse) error {
	if o.sm == nil {
		return fmt.Errorf("orchestrator not started")
	}
	if o.sm.Current() != CRHumanGateScope {
		return fmt.Errorf("workflow is not at scope gate (current state: %s)", o.sm.Current())
	}

	switch resp.Action {
	case "confirm":
		if err := o.sm.Transition(CRReviewing); err != nil {
			return fmt.Errorf("transition to reviewing: %w", err)
		}
	case "cancel":
		o.sm.State().EscalationReason = "operator cancelled at scope gate"
		if err := o.sm.Transition(CREscalated); err != nil {
			return fmt.Errorf("transition to escalated: %w", err)
		}
	default:
		return fmt.Errorf("invalid gate action %q, must be one of: confirm, cancel", resp.Action)
	}

	// Persist comment if provided.
	if resp.Comment != "" {
		entry := CRCommentEntry{
			Gate:      "CR_HUMAN_GATE_SCOPE",
			Action:    resp.Action,
			Comment:   resp.Comment,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}
		if err := AppendCRComment(o.featureDir, entry); err != nil {
			return fmt.Errorf("persist comment: %w", err)
		}
	}

	// Audit log.
	if o.auditLogger != nil {
		o.auditLogger.LogCodeReviewGate(o.sm.State().FeatureName, "CR_HUMAN_GATE_SCOPE", resp.Action, resp.Comment)
	}

	return nil
}

// StateMachine returns the underlying state machine for inspection.
func (o *CodeReviewOrchestrator) StateMachine() *CRStateMachine {
	return o.sm
}

// FeatureDir returns the workspace directory for this code review.
func (o *CodeReviewOrchestrator) FeatureDir() string {
	return o.featureDir
}

// RestoreFromState rebuilds the orchestrator's internal state from a previously
// persisted CodeReviewStateJSON. This is used by the resume handler to bring
// back an orchestrator after a server restart.
func (o *CodeReviewOrchestrator) RestoreFromState(state *CodeReviewStateJSON) {
	o.featureDir = filepath.Join(o.workspaceDir, "code-reviews", state.FeatureName)

	smCfg := CRStateMachineConfigFromConfig(&o.config)
	o.sm = NewCRStateMachine(state, smCfg, func(ws *CodeReviewStateJSON) error {
		return SaveCRState(o.featureDir, ws)
	})

	// Recreate event emitter.
	o.crEmitter = NewCREventEmitter(o.emitter, state.FeatureName)
}

// IsRunning returns true if the orchestrator's Run loop is currently active.
func (o *CodeReviewOrchestrator) IsRunning() bool {
	return o.running.Load() == 1
}

// Run drives the code review workflow through its lifecycle, dispatching
// agents and transitioning the state machine until a terminal or gate state
// is reached. It blocks until the workflow reaches a gate (needing human
// input) or terminal state. Call Run again after a gate response to continue.
func (o *CodeReviewOrchestrator) Run() error {
	o.running.Store(1)
	defer o.running.Store(0)

	if o.sm == nil {
		return fmt.Errorf("orchestrator not started")
	}

	for {
		if o.cancelled.Load() {
			log.Printf("[codereview] workflow cancelled")
			return fmt.Errorf("workflow cancelled")
		}

		current := o.sm.Current()
		state := o.sm.State()

		log.Printf("[codereview] loop: state=%s, round=%d, cost=$%.2f",
			current, state.Round, state.CumulativeCostUSD)

		// Emit workflow status event.
		if o.crEmitter != nil {
			var wallClockSec float64
			if startTime, err := time.Parse(time.RFC3339, state.StartedAt); err == nil {
				wallClockSec = time.Since(startTime).Seconds()
				state.CumulativeWallClockSeconds = wallClockSec
			}
			o.crEmitter.EmitWorkflowStatus(current.String(), state.Round, state.CumulativeCostUSD, wallClockSec)
		}

		switch current {
		case CRInit:
			// Should not reach here — Start() transitions past INIT.
			return fmt.Errorf("unexpected state: CR_INIT (call Start first)")

		case CRHumanGateScope, CRHumanGateFixes:
			// Gate states pause the loop — caller must handle gate response
			// and call Run() again.
			if o.crEmitter != nil {
				gateType := "scope"
				actions := []string{"confirm", "cancel"}
				if current == CRHumanGateFixes {
					gateType = "fixes"
					actions = []string{"re-review", "accept", "escalate"}
				}
				o.crEmitter.EmitGateRequest(gateType, actions)
			}
			return nil

		case CRReviewing:
			if err := o.HandleReviewing(); err != nil {
				return err
			}

		case CRFixing:
			if err := o.HandleFixing(); err != nil {
				return err
			}

		case CRComplete, CREscalated:
			log.Printf("[codereview] workflow reached terminal state: %s", current)
			return nil
		}
	}
}
