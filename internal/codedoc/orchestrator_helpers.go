package codedoc

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/foundry-zero/adversarial-spec-system/internal/specworkflow"
)

// ---------------------------------------------------------------------------
// Resume from CD_ERROR
// ---------------------------------------------------------------------------

// handleResume detects available artefacts and resumes the workflow from the
// appropriate phase based on what was completed before the error.
func (o *CodedocOrchestrator) handleResume() error {
	// Check artefacts in reverse order (most progress → least progress).

	// 1. Staging directory exists → resume at CD_HUMAN_GATE_FINAL (require human approval)
	docsDir := filepath.Join(o.codePath, o.config.DocsOutputDir)
	stagingDir := filepath.Join(docsDir, ".codedoc-staging")
	if _, err := os.Stat(stagingDir); err == nil {
		return o.sm.Transition(CDHumanGateFinal)
	}

	// 2. Review outputs exist → resume at CD_REVIEWING
	reviewPath := filepath.Join(o.featureDir, "merged-findings-round-1.json")
	if _, err := os.Stat(reviewPath); err == nil {
		o.loadMergedFindings(reviewPath)
		ws := o.sm.State()
		if ws.Round == 0 {
			ws.Round = 1
		}
		return o.sm.Transition(CDReviewing)
	}

	// 3. Drafter output exists → resume at CD_HUMAN_GATE_DRAFT
	drafterPath := filepath.Join(o.featureDir, "drafter-output.json")
	if _, err := os.Stat(drafterPath); err == nil {
		o.loadDrafterOutput(drafterPath)
		return o.sm.Transition(CDHumanGateDraft)
	}

	// 4. Discovery output exists → resume at CD_HUMAN_GATE_SCOPE
	discoveryPath := filepath.Join(o.featureDir, "discovery-output.json")
	if _, err := os.Stat(discoveryPath); err == nil {
		o.loadDiscoveryOutput(discoveryPath)
		return o.sm.Transition(CDHumanGateScope)
	}

	// 5. Nothing found → restart at CD_DISCOVERY
	return o.sm.Transition(CDDiscovery)
}

// ---------------------------------------------------------------------------
// Gate handlers (called by API handlers)
// ---------------------------------------------------------------------------

// HandleScopeGate processes a scope gate decision.
func (o *CodedocOrchestrator) HandleScopeGate(action string) error {
	handler := NewScopeGateHandler(o.sm.State(), o.config.MaxGateCorrections)

	var nextState CDState
	var err error
	switch action {
	case "confirm":
		nextState, err = handler.HandleConfirm()
	case "correct":
		nextState, err = handler.HandleCorrect()
	case "cancel":
		nextState, err = handler.HandleCancel()
	default:
		return fmt.Errorf("unknown scope gate action: %s", action)
	}

	if err != nil {
		return err
	}
	return o.sm.Transition(nextState)
}

// HandleDraftGate processes a draft gate decision.
func (o *CodedocOrchestrator) HandleDraftGate(action string) error {
	handler := NewDraftGateHandler(o.sm.State(), o.config.MaxGateDraftRedrafts)

	var nextState CDState
	var err error
	switch action {
	case "approve":
		nextState, err = handler.HandleApprove()
	case "redraft":
		nextState, err = handler.HandleRedraft()
	case "cancel":
		nextState, err = handler.HandleCancel()
	default:
		return fmt.Errorf("unknown draft gate action: %s", action)
	}

	if err != nil {
		return err
	}
	return o.sm.Transition(nextState)
}

// HandleFinalGate processes a final gate decision.
func (o *CodedocOrchestrator) HandleFinalGate(action string) error {
	handler := NewFinalGateHandler(o.sm.State())

	var nextState CDState
	var err error
	switch action {
	case "accept":
		nextState, err = handler.HandleAccept()
	case "reject":
		nextState, err = handler.HandleReject()
	case "request_review":
		nextState, err = handler.HandleRequestReview()
	default:
		return fmt.Errorf("unknown final gate action: %s", action)
	}

	if err != nil {
		return err
	}
	return o.sm.Transition(nextState)
}

// StateMachine returns the underlying state machine.
func (o *CodedocOrchestrator) StateMachine() *CDStateMachine {
	return o.sm
}

// Cancel transitions the workflow to CD_ESCALATED.
func (o *CodedocOrchestrator) Cancel() error {
	current := o.sm.Current()
	if current.IsTerminal() {
		return nil // already terminal, nothing to do
	}
	return o.sm.Transition(CDEscalated)
}

// Resume attempts to resume a workflow from CD_ERROR state by invoking
// the artefact-based resume logic.
func (o *CodedocOrchestrator) Resume() error {
	if o.sm.Current() != CDError {
		return fmt.Errorf("cannot resume: workflow is in %s, not CD_ERROR", o.sm.Current())
	}
	return o.RunWorkflow()
}

// IsRunning returns true if the workflow is in a non-terminal, non-gate state.
func (o *CodedocOrchestrator) IsRunning() bool {
	current := o.sm.Current()
	return !current.IsTerminal() && !current.IsGate() && current != CDError
}

// Rewind transitions the workflow to the target state. This is an admin
// operation that bypasses the normal transition table, used for debugging
// or manual correction.
func (o *CodedocOrchestrator) Rewind(target CDState) error {
	ws := o.sm.State()
	ws.State = target
	ws.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	return o.persistState()
}

// RestoreFromState restores a CodedocOrchestrator from persisted state.
func (o *CodedocOrchestrator) RestoreFromState(state *CDStateJSON) {
	o.sm.RestoreState(state)
	o.featureName = state.FeatureName
	o.codePath = state.CodePath
	o.mode = state.Mode
	o.loadArtifactsForState(state)
}

// ResetWorkspace deletes the feature directory from disk.
func (o *CodedocOrchestrator) ResetWorkspace() error {
	return os.RemoveAll(o.featureDir)
}

// ---------------------------------------------------------------------------
// LoadCDState reads a persisted workflow-state.json from the feature directory.
// ---------------------------------------------------------------------------

// LoadCDState reads and parses a CDStateJSON from the given feature directory.
func LoadCDState(featureDir string) (*CDStateJSON, error) {
	path := filepath.Join(featureDir, "workflow-state.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading codedoc state: %w", err)
	}
	var state CDStateJSON
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parsing codedoc state: %w", err)
	}
	return &state, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (o *CodedocOrchestrator) persistState() error {
	data, err := json.MarshalIndent(o.sm.State(), "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling workflow state: %w", err)
	}
	if err := os.MkdirAll(o.featureDir, 0o755); err != nil {
		return fmt.Errorf("creating feature dir: %w", err)
	}
	path := filepath.Join(o.featureDir, "workflow-state.json")
	return os.WriteFile(path, data, 0o644)
}

// EnsurePersisted creates the feature directory and writes the current
// workflow state to disk. API handlers use this before launching background
// execution so restarts can recover the workflow immediately.
func (o *CodedocOrchestrator) EnsurePersisted() error {
	return o.persistState()
}

func (o *CodedocOrchestrator) emitEvent(eventType string, data interface{}) {
	o.emitter.Emit(CDEvent{
		Type:      eventType,
		Feature:   o.featureName,
		State:     string(o.sm.Current()),
		Round:     o.sm.State().Round,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Data:      data,
	})
}

func (o *CodedocOrchestrator) addCost(cost float64) {
	ws := o.sm.State()
	ws.CumulativeCostUSD += cost
	ws.AgentInvocations++
	ws.CumulativeWallClockSeconds = time.Since(o.startTime).Seconds()
}

func (o *CodedocOrchestrator) loadDiscoveryOutput(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var output DiscoveryOutput
	if json.Unmarshal(data, &output) == nil {
		o.discoveryOutput = &output
	}
}

func (o *CodedocOrchestrator) loadDrafterOutput(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var output DrafterOutput
	if json.Unmarshal(data, &output) == nil {
		o.drafterOutput = &output
	}
}

func (o *CodedocOrchestrator) loadMergedFindings(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var merged MergedCodedocFindings
	if json.Unmarshal(data, &merged) == nil {
		o.mergedFindings = merged.Findings
	}
}

func (o *CodedocOrchestrator) loadArtifactsForState(state *CDStateJSON) {
	if state == nil {
		return
	}

	discoveryPath := filepath.Join(o.featureDir, "discovery-output.json")
	drafterPath := filepath.Join(o.featureDir, "drafter-output.json")
	mergedPath := filepath.Join(o.featureDir, fmt.Sprintf("merged-findings-round-%d.json", maxInt(state.Round, 1)))

	switch state.State {
	case CDHumanGateScope, CDDrafting, CDSanitising, CDHumanGateDraft, CDReviewing, CDRevising, CDJudging, CDHumanGateFinal, CDWriting, CDComplete, CDError:
		o.loadDiscoveryOutput(discoveryPath)
	}

	switch state.State {
	case CDHumanGateDraft, CDReviewing, CDRevising, CDJudging, CDHumanGateFinal, CDWriting, CDComplete, CDError:
		o.loadDrafterOutput(drafterPath)
	}

	switch state.State {
	case CDReviewing, CDRevising, CDJudging, CDHumanGateFinal, CDWriting, CDComplete, CDError:
		o.loadMergedFindings(mergedPath)
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// collectDraftFiles reads all files from the draft directory into a map.
func collectDraftFiles(draftDir string) (map[string][]byte, error) {
	files := make(map[string][]byte)
	err := filepath.Walk(draftDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		relPath, _ := filepath.Rel(draftDir, path)
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		files[relPath] = data
		return nil
	})
	return files, err
}

// ---------------------------------------------------------------------------
// CDAgentRunner adapter (wraps specworkflow.AgentRunner for codedoc dispatch)
// ---------------------------------------------------------------------------

// cdAgentRunnerAdapter wraps a specworkflow.AgentRunner to satisfy CDAgentRunner.
type cdAgentRunnerAdapter struct {
	inner specworkflow.AgentRunner
}

func (a *cdAgentRunnerAdapter) Run(prompt string, outputPath string, timeoutSeconds int) (int, string, float64, int64, error) {
	return a.inner.Run(prompt, outputPath, timeoutSeconds)
}
