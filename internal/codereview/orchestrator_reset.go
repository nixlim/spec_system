package codereview

import (
	"fmt"
	"os"
)

// ---------------------------------------------------------------------------
// ResetWorkspace
// ---------------------------------------------------------------------------

// ResetWorkspace deletes the workspace directory for a completed or escalated
// code review. It returns an error if the orchestrator is not in a terminal
// state (CR_COMPLETE or CR_ESCALATED).
//
// IMPORTANT: This function only deletes workspace artefacts under
// {workspaceDir}/code-reviews/{featureName}/. It does NOT delete any branches
// (cr-fix-round-*) in the target repository.
func (o *CodeReviewOrchestrator) ResetWorkspace() error {
	if o.sm == nil {
		return fmt.Errorf("orchestrator not started")
	}
	if !o.sm.IsTerminal() {
		return fmt.Errorf("cannot reset running workflow (state: %s)", o.sm.Current())
	}
	if o.featureDir == "" {
		return fmt.Errorf("no workspace directory to reset")
	}

	if err := os.RemoveAll(o.featureDir); err != nil {
		return fmt.Errorf("delete workspace: %w", err)
	}
	return nil
}

// ResetWorkspaceFromDisk deletes the workspace directory for a code review
// that is only known from persisted state on disk. It loads the state to
// verify terminal status, then deletes the directory.
//
// Returns ErrCRStateNotFound if no persisted state exists, or an error if the
// workflow is not in a terminal state.
func ResetWorkspaceFromDisk(workspaceDir, featureName string) error {
	featureDir := CRFeatureDir(workspaceDir, featureName)

	state, err := LoadCRState(featureDir)
	if err != nil {
		return err
	}

	if state.State != CRComplete && state.State != CREscalated {
		return fmt.Errorf("cannot reset running workflow (state: %s)", state.State)
	}

	if err := os.RemoveAll(featureDir); err != nil {
		return fmt.Errorf("delete workspace: %w", err)
	}
	return nil
}

// CRFeatureDir returns the workspace path for a code review feature.
func CRFeatureDir(workspaceDir, featureName string) string {
	return workspaceDir + "/code-reviews/" + featureName
}
