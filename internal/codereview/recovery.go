package codereview

import (
	"fmt"
	"os"
	"path/filepath"
)

// ---------------------------------------------------------------------------
// LoadPersistedWorkflows
// ---------------------------------------------------------------------------

// PersistedWorkflow pairs a loaded workflow state with its workspace directory.
type PersistedWorkflow struct {
	// State is the deserialized workflow state.
	State *CodeReviewStateJSON
	// WorkspaceDir is the feature-specific workspace directory containing the
	// workflow-state.json file.
	WorkspaceDir string
}

// LoadPersistedWorkflows scans {codeReviewsRoot}/*/ for workflow-state.json
// files and returns the parsed state for each discovered workflow. Workflows
// with corrupt or unreadable state files are skipped and reported in the
// returned errors slice. Terminal-state workflows (CR_COMPLETE, CR_ESCALATED)
// are included in the results — callers decide whether to skip them.
func LoadPersistedWorkflows(codeReviewsRoot string) ([]PersistedWorkflow, []error) {
	entries, err := os.ReadDir(codeReviewsRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []error{fmt.Errorf("read code-reviews directory: %w", err)}
	}

	var workflows []PersistedWorkflow
	var errs []error

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		featureDir := filepath.Join(codeReviewsRoot, entry.Name())
		state, loadErr := LoadCRState(featureDir)
		if loadErr != nil {
			// Skip workflows with missing or corrupt state files.
			if _, ok := loadErr.(*ErrCRStateNotFound); ok {
				continue // directory exists but no state file — not a workflow
			}
			errs = append(errs, fmt.Errorf("load workflow %s: %w", entry.Name(), loadErr))
			continue
		}

		workflows = append(workflows, PersistedWorkflow{
			State:        state,
			WorkspaceDir: featureDir,
		})
	}

	return workflows, errs
}

// ---------------------------------------------------------------------------
// RecoverWorkflow
// ---------------------------------------------------------------------------

// RecoverWorkflow loads the persisted state for a single feature and determines
// the recovery action. It combines LoadCRState and RecoverFromCrash into a
// single call. The codexAvailable flag controls whether Codex agents are
// expected during CR_REVIEWING recovery.
func RecoverWorkflow(
	featureDir string,
	codexAvailable bool,
	gitChecker GitStatusChecker,
) (*CodeReviewStateJSON, *RecoveryAction, error) {
	state, err := LoadCRState(featureDir)
	if err != nil {
		return nil, nil, fmt.Errorf("load state for recovery: %w", err)
	}

	action, err := RecoverFromCrash(featureDir, state, codexAvailable, gitChecker)
	if err != nil {
		return state, nil, fmt.Errorf("determine recovery action: %w", err)
	}

	return state, action, nil
}
