package specworkflow

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
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
	// FilesRemoved lists the artefact files that were deleted.
	FilesRemoved []string
}

// RewindWorkflow resets a workflow to the given target state and round by
// deleting artefacts that come after the target stage while preserving all
// context that feeds into it. The workflow state file is updated on disk.
//
// The round parameter controls which round's artefacts to preserve:
//   - For REVIEWING: keeps everything up to spec-v{round-1}, deletes reviews/revisions/judge for round+
//   - For REVISING: keeps reviews and merged findings for the round, deletes revision/judge
//   - For JUDGING: keeps revision for the round, deletes judge
//   - For DISCOVERY: deletes everything except workflow-state.json
//   - For DRAFTING: keeps discovery output and gate1 artefacts
//
// Returns the list of files removed and an error if the operation fails.
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

	// Determine which files to remove based on target state.
	removed, err := cleanArtefactsForRewind(specDir, targetState, targetRound)
	if err != nil {
		return nil, fmt.Errorf("clean artefacts: %w", err)
	}
	result.FilesRemoved = removed

	// Update the persisted state.
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
	}

	if err := SaveState(specDir, state); err != nil {
		return nil, fmt.Errorf("save state: %w", err)
	}

	log.Printf("[rewind] rewound %s to %s round %d, removed %d files",
		state.FeatureName, targetState, targetRound, len(removed))

	return result, nil
}

// cleanArtefactsForRewind removes files that come after the target state in
// the workflow pipeline. Returns the list of removed file names.
func cleanArtefactsForRewind(specDir string, targetState WorkflowState, targetRound int) ([]string, error) {
	entries, err := os.ReadDir(specDir)
	if err != nil {
		return nil, err
	}

	// Build list of files to keep based on target state.
	// Always keep: workflow-state.json, workflow-log.jsonl, human-comments.json
	alwaysKeep := map[string]bool{
		"workflow-state.json": true,
		"workflow-log.jsonl":  true,
		"human-comments.json": true,
	}

	keep := make(map[string]bool)
	for k := range alwaysKeep {
		keep[k] = true
	}

	switch targetState {
	case StateDiscovery:
		// Keep nothing else — start fresh from discovery.

	case StateDrafting:
		// Keep discovery output and gate1 artefacts.
		keep["discovery-output.json"] = true
		keep["gate1-corrections.json"] = true

	case StateReviewing:
		// Keep everything through the draft: discovery, gates, drafter, spec up to current.
		keep["discovery-output.json"] = true
		keep["gate1-corrections.json"] = true
		keep["drafter-output.json"] = true
		keep["gate2-resolutions.json"] = true
		keepSpecVersions(keep, 0, targetRound-1) // spec-v0 through spec-v{round-1}
		keepHoldouts(keep, entries)
		// Keep review artefacts for rounds BEFORE targetRound.
		keepRoundArtefacts(keep, entries, "review", 1, targetRound-1)
		keepRoundArtefacts(keep, entries, "merged-findings", 1, targetRound-1)
		keepRoundArtefacts(keep, entries, "revision", 1, targetRound-1)
		keepRoundArtefacts(keep, entries, "judge", 1, targetRound-1)
		// Keep debate trail from prior rounds.
		keep["debate-trail.md"] = true

	case StateRevising:
		// Keep everything through reviews: discovery, gates, drafter, spec, reviews, merged.
		keep["discovery-output.json"] = true
		keep["gate1-corrections.json"] = true
		keep["drafter-output.json"] = true
		keep["gate2-resolutions.json"] = true
		keepSpecVersions(keep, 0, targetRound-1)
		keepHoldouts(keep, entries)
		keep["debate-trail.md"] = true
		// Keep all artefacts for prior rounds.
		keepRoundArtefacts(keep, entries, "review", 1, targetRound-1)
		keepRoundArtefacts(keep, entries, "merged-findings", 1, targetRound-1)
		keepRoundArtefacts(keep, entries, "revision", 1, targetRound-1)
		keepRoundArtefacts(keep, entries, "judge", 1, targetRound-1)
		// Keep this round's reviews and merged findings.
		keepRoundArtefacts(keep, entries, "review", targetRound, targetRound)
		keepRoundArtefacts(keep, entries, "merged-findings", targetRound, targetRound)

	case StateJudging:
		// Keep everything through revision.
		keep["discovery-output.json"] = true
		keep["gate1-corrections.json"] = true
		keep["drafter-output.json"] = true
		keep["gate2-resolutions.json"] = true
		keepSpecVersions(keep, 0, targetRound)
		keepHoldouts(keep, entries)
		keep["debate-trail.md"] = true
		// Keep all artefacts for prior rounds.
		keepRoundArtefacts(keep, entries, "review", 1, targetRound-1)
		keepRoundArtefacts(keep, entries, "merged-findings", 1, targetRound-1)
		keepRoundArtefacts(keep, entries, "revision", 1, targetRound-1)
		keepRoundArtefacts(keep, entries, "judge", 1, targetRound-1)
		// Keep this round's reviews, merged, and revision.
		keepRoundArtefacts(keep, entries, "review", targetRound, targetRound)
		keepRoundArtefacts(keep, entries, "merged-findings", targetRound, targetRound)
		keepRoundArtefacts(keep, entries, "revision", targetRound, targetRound)
	}

	// Delete everything not in the keep set.
	var removed []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if keep[name] {
			continue
		}
		path := filepath.Join(specDir, name)
		if err := os.Remove(path); err != nil {
			return removed, fmt.Errorf("remove %s: %w", name, err)
		}
		removed = append(removed, name)
	}

	return removed, nil
}

// keepSpecVersions marks spec-v{from}.md through spec-v{to}.md for keeping.
func keepSpecVersions(keep map[string]bool, from, to int) {
	for v := from; v <= to; v++ {
		keep[fmt.Sprintf("spec-v%d.md", v)] = true
	}
}

// keepHoldouts marks holdout files (e.g. *-holdouts.md) for keeping.
func keepHoldouts(keep map[string]bool, entries []os.DirEntry) {
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), "-holdouts.md") {
			keep[e.Name()] = true
		}
	}
}

// keepRoundArtefacts marks files matching a prefix pattern for rounds fromRound..toRound.
// Matches patterns like "review-a-round-1.json", "review-a-round-1.md",
// "merged-findings-round-1.json", "revision-round-1.json", "judge-round-1.json".
func keepRoundArtefacts(keep map[string]bool, entries []os.DirEntry, prefix string, fromRound, toRound int) {
	for _, e := range entries {
		name := e.Name()
		for r := fromRound; r <= toRound; r++ {
			roundStr := fmt.Sprintf("round-%d", r)
			if strings.HasPrefix(name, prefix) && strings.Contains(name, roundStr) {
				keep[name] = true
			}
		}
	}
}
