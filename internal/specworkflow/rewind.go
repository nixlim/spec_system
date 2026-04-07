package specworkflow

import (
	"fmt"
	"log"
	"time"
)

// RewindableStates lists the workflow states a user can rewind to.
// Gate states are excluded — the user rewinds to the agent state that
// feeds into the gate (e.g. rewind to DISCOVERY, not HUMAN_GATE_1).
var RewindableStates = []WorkflowState{
	StateDiscovery,
	StateDrafting,
	StateReviewing,
	StateRevising,
	StateJudging,
	StateTaskify,
	StateTaskReview,
}



// IsRewindable reports whether the given state is a valid rewind target.
func IsRewindable(state WorkflowState) bool {
	for _, s := range RewindableStates {
		if s == state {
			return true
		}
	}
	return false
}

// RewindResult describes the outcome of a rewind operation.
type RewindResult struct {
	// PreviousState is the state before the rewind.
	PreviousState WorkflowState
	// TargetState is the state after the rewind.
	TargetState WorkflowState
	// Round is the round number after the rewind.
	Round int
	// FilesRemoved is kept for API response compatibility but is always empty.
	// Rewind preserves all artefact files so the workflow can re-run stages
	// using existing documents as input.
	FilesRemoved []string
}

// RewindWorkflow resets a workflow's state to the given target state and round.
// All artefact files are preserved on disk — rewind only changes the workflow
// state so the orchestrator re-runs from the target stage using whatever
// artefacts already exist. Agents will overwrite their output files when they
// run, producing fresh results against the existing inputs.
//
// For example, rewinding to REVIEWING with an existing spec-v1.md will re-run
// the reviewers against that spec, producing new review findings. The spec
// itself is not deleted.
func RewindWorkflow(specDir string, state *WorkflowStateJSON, targetState WorkflowState, targetRound int) (*RewindResult, error) {
	if !IsRewindable(targetState) {
		return nil, fmt.Errorf("cannot rewind to %s — not a rewindable state", targetState)
	}

	if targetRound < 1 {
		targetRound = 1
	}

	result := &RewindResult{
		PreviousState: state.State,
		TargetState:   targetState,
		Round:         targetRound,
	}

	// Update the persisted state — no files are deleted.
	state.State = targetState
	state.Round = targetRound
	state.StartedAt = time.Now().UTC().Format(time.RFC3339)

	// Reset findings summary — it will be recomputed from artefacts on resume.
	state.FindingsSummary = FindingsSummary{}

	// Reset spec version based on target state.
	switch targetState {
	case StateDiscovery:
		state.CurrentSpecVersion = 0
		state.HadCriticalFindings = false
	case StateDrafting:
		state.CurrentSpecVersion = 0
		state.HadCriticalFindings = false
	case StateReviewing:
		state.CurrentSpecVersion = targetRound - 1
	case StateRevising:
		state.CurrentSpecVersion = targetRound - 1
	case StateJudging:
		state.CurrentSpecVersion = targetRound
	case StateTaskify:
		// Rewinding to TASKIFY preserves existing task files and spec version.
		// No counters need resetting — the taskify stage starts a fresh run
		// that may produce different task graphs.
	case StateTaskReview:
		// Rewinding to TASK_REVIEW re-runs task review against the existing
		// task graph. TaskReviewRound is reset so reviewers start fresh.
		state.TaskReviewRound = 0
	}

	if err := SaveState(specDir, state); err != nil {
		return nil, fmt.Errorf("save state: %w", err)
	}

	log.Printf("[rewind] rewound %s to %s round %d (all artefacts preserved)",
		state.FeatureName, targetState, targetRound)

	return result, nil
}
