package codereview

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const crStateFileName = "workflow-state.json"

// ---------------------------------------------------------------------------
// Error types
// ---------------------------------------------------------------------------

// ErrCRStateNotFound is returned when the code review workflow state file
// does not exist.
type ErrCRStateNotFound struct {
	Path string
}

func (e *ErrCRStateNotFound) Error() string {
	return fmt.Sprintf("code review workflow state file not found: %s", e.Path)
}

// ErrCRStateParseFailed is returned when the workflow state file contains
// invalid JSON.
type ErrCRStateParseFailed struct {
	Path string
	Err  error
}

func (e *ErrCRStateParseFailed) Error() string {
	return fmt.Sprintf("failed to parse code review state file %s: %v", e.Path, e.Err)
}

func (e *ErrCRStateParseFailed) Unwrap() error {
	return e.Err
}

// ---------------------------------------------------------------------------
// SaveState / LoadState
// ---------------------------------------------------------------------------

// CRStateFilePath returns the full path to the code review workflow state
// file within the given directory.
func CRStateFilePath(dir string) string {
	return filepath.Join(dir, crStateFileName)
}

// SaveCRState writes the code review workflow state to
// {dir}/workflow-state.json using an atomic write pattern: data is first
// written to a temporary file in the same directory, then renamed to the
// target path.
func SaveCRState(dir string, state *CodeReviewStateJSON) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal code review state: %w", err)
	}
	data = append(data, '\n')

	target := CRStateFilePath(dir)

	tmp, err := os.CreateTemp(dir, ".cr-workflow-state-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()

	success := false
	defer func() {
		if !success {
			os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	if err := os.Chmod(tmpPath, 0644); err != nil {
		return fmt.Errorf("chmod temp file: %w", err)
	}

	if err := os.Rename(tmpPath, target); err != nil {
		return fmt.Errorf("rename temp file to %s: %w", target, err)
	}

	success = true
	return nil
}

// LoadCRState reads the code review workflow state from
// {dir}/workflow-state.json. Returns *ErrCRStateNotFound if the file does
// not exist and *ErrCRStateParseFailed if the file contains invalid JSON.
func LoadCRState(dir string) (*CodeReviewStateJSON, error) {
	p := CRStateFilePath(dir)

	data, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, &ErrCRStateNotFound{Path: p}
		}
		return nil, fmt.Errorf("read code review state file: %w", err)
	}

	var state CodeReviewStateJSON
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, &ErrCRStateParseFailed{Path: p, Err: err}
	}

	return &state, nil
}

// ---------------------------------------------------------------------------
// Recovery types
// ---------------------------------------------------------------------------

// RecoveryActionType describes the type of recovery action to take.
type RecoveryActionType string

const (
	// RecoveryReDispatchReviewers means some reviewer agents need re-dispatch.
	RecoveryReDispatchReviewers RecoveryActionType = "re_dispatch_reviewers"
	// RecoveryReDispatchFixAgent means the fix agent needs re-dispatch.
	RecoveryReDispatchFixAgent RecoveryActionType = "re_dispatch_fix_agent"
	// RecoveryRouteToGate means the workflow should resume at a human gate.
	RecoveryRouteToGate RecoveryActionType = "route_to_gate"
	// RecoveryResumeAtGate means the workflow was in a gate state — resume there.
	RecoveryResumeAtGate RecoveryActionType = "resume_at_gate"
)

// RecoveryAction describes what the orchestrator should do after crash recovery.
type RecoveryAction struct {
	// Type is the recovery action type.
	Type RecoveryActionType
	// AgentsToReDispatch lists the agent names that need to be re-dispatched
	// (only set when Type is RecoveryReDispatchReviewers).
	AgentsToReDispatch []string
	// Warning is a human-readable warning to surface at the next gate.
	Warning string
	// UncommittedFiles lists files with uncommitted changes (for CR_FIXING recovery).
	UncommittedFiles []string
	// NextState is the target state for routing actions.
	NextState CodeReviewState
}

// ---------------------------------------------------------------------------
// GitStatusChecker
// ---------------------------------------------------------------------------

// GitStatusChecker abstracts checking for uncommitted changes in a git repo.
// This allows testing without real git operations.
type GitStatusChecker interface {
	// HasUncommittedChanges returns true if the repo at codePath has staged
	// or unstaged changes, and returns the list of changed file paths.
	HasUncommittedChanges(codePath string) (bool, []string, error)
}

// defaultGitStatusChecker implements GitStatusChecker using exec.Command.
type defaultGitStatusChecker struct{}

func (g *defaultGitStatusChecker) HasUncommittedChanges(codePath string) (bool, []string, error) {
	cmd := exec.Command("git", "-C", codePath, "status", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return false, nil, fmt.Errorf("git status: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var files []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			// Porcelain format: XY filename
			if len(line) > 3 {
				files = append(files, strings.TrimSpace(line[3:]))
			} else {
				files = append(files, line)
			}
		}
	}
	return len(files) > 0, files, nil
}

// ---------------------------------------------------------------------------
// RecoverFromCrash
// ---------------------------------------------------------------------------

// reviewOutputFileName returns the expected output file name for a reviewer
// agent in a given round.
func reviewOutputFileName(provider, lens string, round int) string {
	return fmt.Sprintf("review-%s-%s-round-%d.json", lens, provider, round)
}

// expectedReviewerAgents returns the list of expected reviewer agent names
// and their output file paths for the given round and workspace directory.
// When codexAvailable is true, 12 agents are expected (6 lenses × 2 providers);
// otherwise 6 (Claude only).
func expectedReviewerAgents(workspaceDir string, round int, codexAvailable bool) map[string]string {
	agents := make(map[string]string)
	providers := []string{"claude"}
	if codexAvailable {
		providers = append(providers, "codex")
	}
	for _, provider := range providers {
		for _, lens := range CodeReviewLensGroups {
			agentName := fmt.Sprintf("reviewer-%s-%s", lens, provider)
			fileName := reviewOutputFileName(provider, lens, round)
			agents[agentName] = filepath.Join(workspaceDir, fileName)
		}
	}
	return agents
}

// RecoverFromCrash examines the persisted state and filesystem to determine
// what recovery action should be taken after a crash or server restart.
//
// Recovery logic per state:
//   - CR_REVIEWING: check for expected output files. Valid output files for the
//     current round are kept; corrupt (invalid JSON) files are deleted. Agents
//     without valid output files are added to the re-dispatch list. Files from
//     previous rounds are ignored.
//   - CR_FIXING: check git status for uncommitted changes. If changes exist,
//     route to CR_HUMAN_GATE_FIXES with a partial fix warning. If no changes
//     and no FixOutput file, re-dispatch the fix agent.
//   - Gate states (CR_HUMAN_GATE_SCOPE, CR_HUMAN_GATE_FIXES): resume at gate.
//   - Terminal states: no recovery needed.
func RecoverFromCrash(
	workspaceDir string,
	state *CodeReviewStateJSON,
	codexAvailable bool,
	gitChecker GitStatusChecker,
) (*RecoveryAction, error) {
	switch state.State {
	case CRReviewing:
		return recoverReviewing(workspaceDir, state.Round, codexAvailable)

	case CRFixing:
		return recoverFixing(workspaceDir, state.CodePath, state.Round, gitChecker)

	case CRHumanGateScope, CRHumanGateFixes:
		return &RecoveryAction{
			Type:      RecoveryResumeAtGate,
			NextState: state.State,
		}, nil

	case CRComplete, CREscalated:
		return nil, nil // terminal, no recovery needed

	default:
		return &RecoveryAction{
			Type:      RecoveryResumeAtGate,
			NextState: state.State,
			Warning:   fmt.Sprintf("unexpected state %s during recovery, resuming as gate", state.State),
		}, nil
	}
}

// recoverReviewing checks reviewer output files and builds a re-dispatch list.
func recoverReviewing(workspaceDir string, round int, codexAvailable bool) (*RecoveryAction, error) {
	expected := expectedReviewerAgents(workspaceDir, round, codexAvailable)

	var toReDispatch []string
	for agentName, filePath := range expected {
		if !isValidOutputFile(filePath) {
			toReDispatch = append(toReDispatch, agentName)
		}
	}

	if len(toReDispatch) == 0 {
		// All outputs are present and valid — proceed to merge.
		return &RecoveryAction{
			Type: RecoveryReDispatchReviewers,
		}, nil
	}

	return &RecoveryAction{
		Type:               RecoveryReDispatchReviewers,
		AgentsToReDispatch: toReDispatch,
	}, nil
}

// isValidOutputFile checks if a file exists and contains valid JSON.
// If the file exists but contains invalid JSON, it is deleted.
func isValidOutputFile(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	if !json.Valid(data) {
		// Corrupt file — delete it so the agent can be re-dispatched.
		os.Remove(path)
		return false
	}
	return true
}

// recoverFixing checks git status and determines recovery action.
func recoverFixing(workspaceDir, codePath string, round int, gitChecker GitStatusChecker) (*RecoveryAction, error) {
	if gitChecker == nil {
		gitChecker = &defaultGitStatusChecker{}
	}
	hasChanges, files, err := gitChecker.HasUncommittedChanges(codePath)
	if err != nil {
		return nil, fmt.Errorf("check git status during recovery: %w", err)
	}

	if hasChanges {
		return &RecoveryAction{
			Type:             RecoveryRouteToGate,
			NextState:        CRHumanGateFixes,
			Warning:          "partial fix — server crashed during fix phase",
			UncommittedFiles: files,
		}, nil
	}

	// No uncommitted changes. Check for FixOutput file.
	fixOutputPath := filepath.Join(workspaceDir, fmt.Sprintf("fix-output-round-%d.json", round))
	if _, err := os.Stat(fixOutputPath); err == nil {
		// FixOutput exists — route to evaluation.
		return &RecoveryAction{
			Type:      RecoveryRouteToGate,
			NextState: CRHumanGateFixes,
		}, nil
	}

	// No changes and no output — re-dispatch the fix agent.
	return &RecoveryAction{
		Type: RecoveryReDispatchFixAgent,
	}, nil
}

// ---------------------------------------------------------------------------
// Human comments persistence
// ---------------------------------------------------------------------------

const crCommentsFileName = "human-comments.json"

// CRCommentEntry represents a single human comment persisted at a gate action.
type CRCommentEntry struct {
	Gate      string `json:"gate"`
	Action    string `json:"action"`
	Comment   string `json:"comment"`
	Timestamp string `json:"timestamp"`
}

// CRCommentsFilePath returns the full path to human-comments.json within the
// given directory.
func CRCommentsFilePath(dir string) string {
	return filepath.Join(dir, crCommentsFileName)
}

// AppendCRComment appends a single comment entry to human-comments.json.
func AppendCRComment(dir string, entry CRCommentEntry) error {
	if strings.TrimSpace(entry.Comment) == "" {
		return nil
	}

	p := CRCommentsFilePath(dir)
	var comments []CRCommentEntry

	data, err := os.ReadFile(p)
	if err == nil {
		if jsonErr := json.Unmarshal(data, &comments); jsonErr != nil {
			return fmt.Errorf("corrupt human-comments.json: %w", jsonErr)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read human-comments.json: %w", err)
	}

	comments = append(comments, entry)

	out, err := json.MarshalIndent(comments, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal human-comments.json: %w", err)
	}
	out = append(out, '\n')

	// Atomic write: temp file + rename.
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, out, 0644); err != nil {
		return fmt.Errorf("write temp human-comments.json: %w", err)
	}
	return os.Rename(tmp, p)
}
