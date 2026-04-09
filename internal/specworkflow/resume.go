package specworkflow

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

// ---------------------------------------------------------------------------
// ResumeResult
// ---------------------------------------------------------------------------

// ResumeResult describes the outcome of probing the workspace for a resumable
// workflow. It is used by the orchestrator after a crash (or restart) to decide
// whether to continue an in-flight workflow and, if so, what work remains.
type ResumeResult struct {
	// State is the deserialized workflow state, or nil if no workflow exists.
	State *WorkflowStateJSON
	// Found is true when a workflow-state.json file was present on disk.
	Found bool
	// SkillsChanged is true when the current skill checksums differ from the
	// checksums recorded in the persisted state. The orchestrator should log a
	// warning but continue — mid-workflow skill changes are not blocking.
	SkillsChanged bool
	// NeedsAgentRedispatch is true when the state indicates an agent was
	// running but its expected output file is missing, meaning the agent must
	// be re-invoked. A re-dispatch counts against the retry budget.
	NeedsAgentRedispatch bool
	// MissingOutputFile is the path of the output file that was expected but
	// absent. Empty when NeedsAgentRedispatch is false.
	MissingOutputFile string
	// IsGateState is true when the persisted state is one of the human gates
	// (HUMAN_GATE_1, HUMAN_GATE_2, HUMAN_GATE_FINAL).
	IsGateState bool
}

// ---------------------------------------------------------------------------
// Gate and agent state classification
// ---------------------------------------------------------------------------

// gateStates is the set of WorkflowState values that represent human gates.
var gateStates = map[WorkflowState]bool{
	StateHumanGate1:    true,
	StateHumanGate2:    true,
	StateHumanGateFinal: true,
	StateTaskHumanGate: true,
}

// IsGateState reports whether the given state is a human gate state
// (HUMAN_GATE_1, HUMAN_GATE_2, HUMAN_GATE_FINAL, or TASK_HUMAN_GATE).
func IsGateState(s WorkflowState) bool {
	return gateStates[s]
}

// agentStates is the set of WorkflowState values that represent active agent
// phases — phases where an agent is expected to produce an output file.
var agentStates = map[WorkflowState]bool{
	StateDiscovery:    true,
	StateDrafting:     true,
	StateReviewing:    true,
	StateRevising:     true,
	StateJudging:      true,
	StateTaskify:      true,
	StateTaskReview:   true,
	StateTaskRevision: true,
}

// ---------------------------------------------------------------------------
// ExpectedOutputFile
// ---------------------------------------------------------------------------

// ExpectedOutputFile returns the expected output file path (relative to the
// spec directory) for the given agent state. For REVIEWING, the first review
// file ("review-a-round-{round}.json") is returned as a representative — the
// caller should use reviewOutputExists for a more thorough check. Returns an
// empty string for non-agent states.
func ExpectedOutputFile(state WorkflowState, featureName string, round int) string {
	specDir := filepath.Join("specs", featureName)
	switch state {
	case StateDiscovery:
		return filepath.Join(specDir, "discovery-output.json")
	case StateDrafting:
		return filepath.Join(specDir, "drafter-output.json")
	case StateReviewing:
		return filepath.Join(specDir, fmt.Sprintf("review-a-round-%d.json", round))
	case StateRevising:
		return filepath.Join(specDir, fmt.Sprintf("revision-round-%d.json", round))
	case StateJudging:
		return filepath.Join(specDir, fmt.Sprintf("judge-round-%d.json", round))
	default:
		return ""
	}
}

// reviewerLetters are the suffix letters for the reviewer groups. Keep in
// sync with reviewerGroupLetter in prompts.go: a=clarity, b=consistency,
// c=security, d=correctness, e=coverage.
var reviewerLetters = []string{"a", "b", "c", "d", "e"}

// reviewOutputExists returns true if at least one reviewer output file for the
// given round exists in the workspace.
func reviewOutputExists(workspaceDir, featureName string, round int) bool {
	specDir := filepath.Join(workspaceDir, "specs", featureName)
	for _, letter := range reviewerLetters {
		p := filepath.Join(specDir, fmt.Sprintf("review-%s-round-%d.json", letter, round))
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// ResumeWorkflow
// ---------------------------------------------------------------------------

// ResumeWorkflow probes the workspace for an existing workflow and returns a
// ResumeResult describing what was found. The orchestrator uses this after a
// crash or restart to decide whether to resume, wait for a human gate, or
// re-dispatch an agent.
//
// The currentChecksums parameter should contain the checksums from the
// currently-loaded SkillCache (via SkillCache.GetChecksums). If the persisted
// state records different checksums, SkillsChanged is set to true and a
// warning is logged.
//
// When the persisted state is an agent state and the expected output file is
// missing, NeedsAgentRedispatch is set to true. A re-dispatch counts against
// the retry budget.
func ResumeWorkflow(workspaceDir, featureName string, currentChecksums map[string]string) (*ResumeResult, error) {
	specDir := filepath.Join(workspaceDir, "specs", featureName)

	state, err := LoadState(specDir)
	if err != nil {
		var notFound *ErrStateNotFound
		if errors.As(err, &notFound) {
			return &ResumeResult{Found: false}, nil
		}
		return nil, fmt.Errorf("resume: load state: %w", err)
	}

	result := &ResumeResult{
		State: state,
		Found: true,
	}

	// Check if this is a gate state.
	if gateStates[state.State] {
		result.IsGateState = true
	}

	// For agent states, check whether the expected output file exists.
	if agentStates[state.State] {
		if state.State == StateReviewing {
			// For REVIEWING, check if any reviewer output exists.
			if !reviewOutputExists(workspaceDir, featureName, state.Round) {
				result.NeedsAgentRedispatch = true
				result.MissingOutputFile = ExpectedOutputFile(state.State, featureName, state.Round)
			}
		} else {
			expected := ExpectedOutputFile(state.State, featureName, state.Round)
			absPath := filepath.Join(workspaceDir, expected)
			if _, statErr := os.Stat(absPath); os.IsNotExist(statErr) {
				result.NeedsAgentRedispatch = true
				result.MissingOutputFile = expected
			}
		}
	}

	// Compare skill checksums.
	if state.SkillChecksums != nil && currentChecksums != nil {
		result.SkillsChanged = !checksumMapsEqual(state.SkillChecksums, currentChecksums)
		if result.SkillsChanged {
			log.Printf("WARNING: skill checksums have changed since workflow was persisted (feature: %s, state: %s)",
				featureName, state.State)
		}
	}

	return result, nil
}

// checksumMapsEqual returns true if the two maps contain exactly the same
// key-value pairs.
func checksumMapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
