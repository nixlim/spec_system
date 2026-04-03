package codedoc

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/foundry-zero/adversarial-spec-system/internal/specworkflow"
)

// ---------------------------------------------------------------------------
// EventEmitter interface
// ---------------------------------------------------------------------------

// CDEventEmitter broadcasts workflow events to subscribers (e.g., WebSocket hub).
type CDEventEmitter interface {
	Emit(event CDEvent)
}

// CDEvent is a workflow event emitted on state transitions and progress updates.
type CDEvent struct {
	Type      string      `json:"type"`
	Feature   string      `json:"feature"`
	State     string      `json:"state"`
	Round     int         `json:"round,omitempty"`
	Timestamp string      `json:"timestamp"`
	Data      interface{} `json:"data,omitempty"`
}

// NoopEmitter is a CDEventEmitter that discards events.
type NoopEmitter struct{}

func (NoopEmitter) Emit(_ CDEvent) {}

// ---------------------------------------------------------------------------
// CodedocOrchestrator
// ---------------------------------------------------------------------------

// CodedocOrchestratorConfig holds the parameters to construct a CodedocOrchestrator.
type CodedocOrchestratorConfig struct {
	WorkspaceDir string
	FeatureName  string
	CodePath     string
	Mode         string
	Description  string
	Config       CodedocConfig
	Runner       specworkflow.AgentRunner
	CodexRunner  specworkflow.AgentRunner
	MergeRunner  specworkflow.AgentRunner
	Emitter      CDEventEmitter
}

// CodedocOrchestrator drives the codedoc workflow through its lifecycle.
type CodedocOrchestrator struct {
	config      CodedocConfig
	sm          *CDStateMachine
	runner      specworkflow.AgentRunner
	codexRunner specworkflow.AgentRunner
	mergeRunner specworkflow.AgentRunner
	emitter     CDEventEmitter
	featureDir  string
	codePath    string
	mode        string
	featureName string

	startTime time.Time

	// Phase artefacts populated during execution.
	discoveryOutput *DiscoveryOutput
	drafterOutput   *DrafterOutput
	mergedFindings  []ReviewFinding
	roundCounts     []int
}

// NewCodedocOrchestrator creates and initialises a CodedocOrchestrator.
func NewCodedocOrchestrator(cfg CodedocOrchestratorConfig) *CodedocOrchestrator {
	featureDir := filepath.Join(cfg.WorkspaceDir, "codedoc", cfg.FeatureName)

	emitter := cfg.Emitter
	if emitter == nil {
		emitter = NoopEmitter{}
	}

	ws := &CDStateJSON{
		State:       CDInit,
		FeatureName: cfg.FeatureName,
		CodePath:    cfg.CodePath,
		Mode:        cfg.Mode,
		StartedAt:   time.Now().UTC().Format(time.RFC3339),
		UpdatedAt:   time.Now().UTC().Format(time.RFC3339),
	}

	smCfg := CDStateMachineConfigFromConfig(&cfg.Config)

	orch := &CodedocOrchestrator{
		config:      cfg.Config,
		runner:      cfg.Runner,
		codexRunner: cfg.CodexRunner,
		mergeRunner: cfg.MergeRunner,
		emitter:     emitter,
		featureDir:  featureDir,
		codePath:    cfg.CodePath,
		mode:        cfg.Mode,
		featureName: cfg.FeatureName,
		startTime:   time.Now(),
	}

	orch.sm = NewCDStateMachine(ws, smCfg, func(ws *CDStateJSON) error {
		return orch.persistState()
	})

	return orch
}

// State returns the current workflow state snapshot.
func (o *CodedocOrchestrator) State() *CDStateJSON {
	return o.sm.State()
}

// FeatureDir returns the workspace directory for this workflow.
func (o *CodedocOrchestrator) FeatureDir() string {
	return o.featureDir
}

// ---------------------------------------------------------------------------
// RunWorkflow — main orchestration loop
// ---------------------------------------------------------------------------

// RunWorkflow drives the workflow from its current state to completion,
// escalation, or a gate that requires human input. It returns nil when
// the workflow reaches a terminal state or a gate state.
func (o *CodedocOrchestrator) RunWorkflow() error {
	_ = os.MkdirAll(o.featureDir, 0o755)

	for {
		current := o.sm.Current()

		// Terminal or gate — stop and wait for external input.
		if current.IsTerminal() || current.IsGate() {
			return nil
		}

		o.emitEvent("state_entered", nil)

		var err error
		switch current {
		case CDInit:
			err = o.handleInit()
		case CDDiscovery:
			err = o.handleDiscovery()
		case CDDrafting:
			err = o.handleDrafting()
		case CDSanitising:
			err = o.handleSanitising()
		case CDReviewing:
			err = o.handleReviewing()
		case CDRevising:
			err = o.handleRevising()
		case CDJudging:
			err = o.handleJudging()
		case CDWriting:
			err = o.handleWriting()
		case CDError:
			return o.handleResume()
		default:
			return fmt.Errorf("unhandled state: %s", current)
		}

		if err != nil {
			log.Printf("[codedoc-orchestrator] error in %s: %v", current, err)
			if transErr := o.sm.Transition(CDError); transErr != nil {
				log.Printf("[codedoc-orchestrator] failed to transition to CD_ERROR: %v", transErr)
				// Force state to CD_ERROR and persist directly to avoid stuck workflows.
				ws := o.sm.State()
				ws.State = CDError
				ws.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
				if persistErr := o.persistState(); persistErr != nil {
					log.Printf("[codedoc-orchestrator] CRITICAL: failed to persist error state: %v", persistErr)
				}
			}
			o.emitEvent("error", map[string]string{"error": err.Error()})
			return err
		}
	}
}

// ---------------------------------------------------------------------------
// Phase handlers
// ---------------------------------------------------------------------------

func (o *CodedocOrchestrator) handleInit() error {
	return o.sm.Transition(CDDiscovery)
}

func (o *CodedocOrchestrator) handleDiscovery() error {
	deps := DiscoveryDeps{
		Runner:      o.runner,
		CodexRunner: o.codexRunner,
		MergeRunner: o.mergeRunner,
		Config:      o.config,
		FeatureDir:  o.featureDir,
		CodePath:    o.codePath,
		Mode:        o.mode,
		Round:       1,
	}

	result, err := RunDiscovery(deps)
	if err != nil {
		return fmt.Errorf("discovery failed: %w", err)
	}

	o.discoveryOutput = result.Output
	o.addCost(result.CostUSD)

	if len(result.Warnings) > 0 {
		ws := o.sm.State()
		ws.DiscoverySource = "single-provider-fallback"
	}

	return o.sm.Transition(CDHumanGateScope)
}

func (o *CodedocOrchestrator) handleDrafting() error {
	ws := o.sm.State()
	if ws.DraftVersion == 0 {
		ws.DraftVersion = 1
	} else {
		ws.DraftVersion++
	}

	discoveryJSON, err := json.Marshal(o.discoveryOutput)
	if err != nil {
		return fmt.Errorf("marshal discovery output for drafter: %w", err)
	}

	deps := DraftingDeps{
		Runner:          o.runner,
		CodexRunner:     o.codexRunner,
		CombineRunner:   o.mergeRunner,
		Config:          o.config,
		FeatureDir:      o.featureDir,
		CodePath:        o.codePath,
		DiscoveryOutput: o.discoveryOutput,
		DiscoveryJSON:   string(discoveryJSON),
		DraftVersion:    ws.DraftVersion,
	}

	result, err := RunDrafting(deps)
	if err != nil {
		return fmt.Errorf("drafting failed: %w", err)
	}

	o.drafterOutput = result.Output
	o.addCost(result.CostUSD)

	if len(result.Warnings) > 0 {
		ws := o.sm.State()
		ws.DraftSource = "single-provider-fallback"
		ws.DraftFailureNotice = result.Warnings[0]
	}

	return o.sm.Transition(CDSanitising)
}

func (o *CodedocOrchestrator) handleSanitising() error {
	ws := o.sm.State()
	draftVersion := ws.DraftVersion
	if draftVersion == 0 {
		draftVersion = 1
	}
	draftDir := filepath.Join(o.featureDir, fmt.Sprintf("draft-v%d", draftVersion))
	sanitiser := NewSanitiser()
	report, err := sanitiser.ScanDirectory(draftDir)
	if err != nil {
		return fmt.Errorf("sanitisation failed: %w", err)
	}

	// Persist sanitisation report
	reportPath := filepath.Join(o.featureDir, fmt.Sprintf("sanitisation-report-v%d.json", draftVersion))
	reportJSON, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal sanitisation report: %w", err)
	}
	if err := os.WriteFile(reportPath, reportJSON, 0644); err != nil {
		return fmt.Errorf("write sanitisation report: %w", err)
	}
	log.Printf("[codedoc-orchestrator] sanitisation report written to %s", reportPath)

	if report.NeedsRedraft {
		log.Printf("[codedoc-orchestrator] sanitisation requires redraft")
		return o.sm.Transition(CDDrafting)
	}

	return o.sm.Transition(CDHumanGateDraft)
}

func (o *CodedocOrchestrator) handleReviewing() error {
	ws := o.sm.State()
	round := ws.Round
	if round == 0 {
		round = 1
		ws.Round = round
	}

	draftVersion := ws.DraftVersion
	if draftVersion == 0 {
		draftVersion = 1
	}
	draftDir := filepath.Join(o.featureDir, fmt.Sprintf("draft-v%d", draftVersion))

	// Build prompts and output paths for all 4 reviewer groups.
	prompts := make(map[string]string)
	outputPaths := make(map[string]string)
	for _, group := range CodedocLensGroups {
		prompts[group] = BuildReviewerPrompt(group, draftDir, round)
		letter := ReviewerGroupLetter(group)
		outputPaths[group] = filepath.Join(o.featureDir, fmt.Sprintf("review-%s-round-%d.json", letter, round))
	}

	var codexReviewRunner *cdAgentRunnerAdapter
	if o.config.EnableCodexReviewers && o.codexRunner != nil {
		codexReviewRunner = &cdAgentRunnerAdapter{o.codexRunner}
	}

	dispatchResult, err := DispatchCodedocReviewers(
		&cdAgentRunnerAdapter{o.runner}, codexReviewRunner,
		CodedocLensGroups, prompts, outputPaths, nil,
		CDReviewDispatchConfig{MaxRetries: o.config.MaxRetries, TimeoutSeconds: o.config.ReviewerTimeoutSeconds},
		nil, nil,
	)
	if err != nil {
		return fmt.Errorf("review dispatch failed: %w", err)
	}

	o.addCost(dispatchResult.TotalCostUSD)

	// Collect successful outputs and merge.
	var outputs []*ReviewerOutput
	for _, r := range dispatchResult.Results {
		if r.Output != nil {
			outputs = append(outputs, r.Output)
		}
	}

	if len(outputs) == 0 {
		return fmt.Errorf("no reviewer outputs to merge")
	}

	merged, mergeErr := MergeCodedocReviewerOutputs(outputs, round)
	if mergeErr != nil {
		return fmt.Errorf("merge failed: %w", mergeErr)
	}

	o.mergedFindings = merged.Findings
	o.roundCounts = append(o.roundCounts, CountOpenCriticalMajor(merged.Findings))

	// Save merged findings — required for resume to detect review progress.
	mergedData, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal merged findings: %w", err)
	}
	mergedPath := filepath.Join(o.featureDir, fmt.Sprintf("merged-findings-round-%d.json", round))
	if err := os.WriteFile(mergedPath, mergedData, 0o644); err != nil {
		return fmt.Errorf("write merged findings: %w", err)
	}

	// Skip revision when there are no findings to address.
	if len(merged.Findings) == 0 {
		log.Printf("[codedoc-orchestrator] no findings in round %d — skipping revision, going to judge", round)
		return o.sm.Transition(CDJudging)
	}

	return o.sm.Transition(CDRevising)
}

func (o *CodedocOrchestrator) handleRevising() error {
	ws := o.sm.State()
	round := ws.Round

	findingsJSON, err := json.Marshal(o.mergedFindings)
	if err != nil {
		return fmt.Errorf("marshal findings for revision: %w", err)
	}

	// Determine the source draft directory (current version).
	srcVersion := ws.DraftVersion
	if srcVersion == 0 {
		srcVersion = 1
	}
	srcDraftDir := filepath.Join(o.featureDir, fmt.Sprintf("draft-v%d", srcVersion))

	// Each revision round writes to a new versioned directory to preserve the
	// artefact trail and enable crash recovery per round.
	nextVersion := srcVersion + 1
	draftDir := filepath.Join(o.featureDir, fmt.Sprintf("draft-v%d", nextVersion))
	if err := copyDir(srcDraftDir, draftDir); err != nil {
		return fmt.Errorf("copy draft-v%d to draft-v%d: %w", srcVersion, nextVersion, err)
	}
	ws.DraftVersion = nextVersion

	prompt := BuildRevisionPrompt(string(findingsJSON), draftDir, round)
	outPath := filepath.Join(o.featureDir, fmt.Sprintf("revision-round-%d.json", round))

	_, _, cost, _, runErr := o.runner.Run(prompt, outPath, o.config.AgentTimeoutSeconds)
	o.addCost(cost)

	if runErr != nil {
		return fmt.Errorf("revision agent failed: %w", runErr)
	}

	// Read and apply revision.
	data, readErr := os.ReadFile(outPath)
	if readErr != nil {
		return fmt.Errorf("read revision output: %w", readErr)
	}
	var revision RevisionOutput
	if err := json.Unmarshal(data, &revision); err != nil {
		log.Printf("[codedoc-orchestrator] WARNING: revision output parse error (round %d): %v", round, err)
	} else {
		o.mergedFindings = ApplyRevisionToFindings(o.mergedFindings, &revision)
	}

	return o.sm.Transition(CDJudging)
}

func (o *CodedocOrchestrator) handleJudging() error {
	ws := o.sm.State()
	round := ws.Round

	// Run LLM judge agent per spec.
	findingsJSON, err := json.Marshal(o.mergedFindings)
	if err != nil {
		return fmt.Errorf("marshal findings for judge: %w", err)
	}
	prompt := BuildJudgePrompt(string(findingsJSON), round)
	outPath := filepath.Join(o.featureDir, fmt.Sprintf("judge-round-%d.json", round))

	_, _, cost, _, runErr := o.runner.Run(prompt, outPath, o.config.AgentTimeoutSeconds)
	o.addCost(cost)

	var verdict string
	if runErr == nil {
		data, readErr := os.ReadFile(outPath)
		if readErr == nil {
			var judgeOut JudgeOutput
			if json.Unmarshal(data, &judgeOut) == nil && judgeOut.Verdict != "" {
				verdict = judgeOut.Verdict
			}
		}
	}

	// Fall back to deterministic convergence if LLM judge failed or returned empty.
	if verdict == "" {
		log.Printf("[codedoc-orchestrator] judge agent failed or returned empty verdict, using deterministic convergence")
		convergence := EvaluateConvergence(o.mergedFindings, round, o.config.MinRounds, o.roundCounts, o.config.StalenessThreshold)
		verdict = convergence.Verdict
	}

	ws.HadCriticalFindings = CountOpenCriticalMajor(o.mergedFindings) > 0

	// Check for staleness regardless of how verdict was obtained.
	if DetectStaleness(o.roundCounts, o.config.StalenessThreshold) {
		log.Printf("[codedoc-orchestrator] staleness detected after %d rounds — escalating to human gate", round)
		verdict = VerdictPassWithGate
	}

	// If judge says PASS but open CRITICAL/MAJOR findings remain, escalate to
	// human gate instead of letting the state machine guard block the transition.
	if verdict == VerdictPass && ws.HadCriticalFindings {
		log.Printf("[codedoc-orchestrator] judge returned PASS but open CRITICAL/MAJOR findings remain — routing to HUMAN_GATE_FINAL")
		verdict = VerdictPassWithGate
	}

	switch verdict {
	case VerdictPass:
		return o.sm.Transition(CDWriting)
	case VerdictPassWithGate:
		return o.sm.Transition(CDHumanGateFinal)
	case VerdictRevise:
		ws.Round++
		return o.sm.Transition(CDReviewing)
	case "BLOCK":
		return o.sm.Transition(CDEscalated)
	default:
		return fmt.Errorf("unexpected judge verdict: %s", verdict)
	}
}

func (o *CodedocOrchestrator) handleWriting() error {
	writer := &Writer{
		Config:          o.config,
		CodePath:        o.codePath,
		Feature:         o.featureName,
		DiscoveryOutput: o.discoveryOutput,
	}

	ws := o.sm.State()
	draftVersion := ws.DraftVersion
	if draftVersion == 0 {
		draftVersion = 1
	}
	draftDir := filepath.Join(o.featureDir, fmt.Sprintf("draft-v%d", draftVersion))
	approvedFiles, err := collectDraftFiles(draftDir)
	if err != nil {
		return fmt.Errorf("collecting draft files: %w", err)
	}

	_, writeErr := writer.Write(approvedFiles)
	if writeErr != nil {
		return fmt.Errorf("writing failed: %w", writeErr)
	}

	return o.sm.Transition(CDComplete)
}

