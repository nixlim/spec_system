package specworkflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const stateFileName = "workflow-state.json"

const commentsFileName = "human-comments.json"

// CommentEntry represents a single human comment persisted at a gate action.
type CommentEntry struct {
	Gate      string `json:"gate"`
	Action    string `json:"action"`
	Comment   string `json:"comment"`
	Timestamp string `json:"timestamp"`
}

// ErrCommentsCorrupted is returned when human-comments.json contains invalid JSON.
type ErrCommentsCorrupted struct {
	Path string
	Err  error
}

func (e *ErrCommentsCorrupted) Error() string {
	return fmt.Sprintf("human-comments.json is corrupted: %s: %v", e.Path, e.Err)
}

func (e *ErrCommentsCorrupted) Unwrap() error {
	return e.Err
}

// CommentsFilePath returns the full path to human-comments.json within the
// given directory.
func CommentsFilePath(dir string) string {
	return filepath.Join(dir, commentsFileName)
}

// LoadComments reads the comment history from human-comments.json. It returns
// an empty slice (not an error) if the file does not exist, and
// *ErrCommentsCorrupted if the file contains invalid JSON.
func LoadComments(dir string) ([]CommentEntry, error) {
	p := CommentsFilePath(dir)
	data, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read human-comments.json: %w", err)
	}

	var comments []CommentEntry
	if err := json.Unmarshal(data, &comments); err != nil {
		return nil, &ErrCommentsCorrupted{Path: p, Err: err}
	}
	return comments, nil
}

// AppendComment appends a single comment entry to human-comments.json in
// append-only fashion. It returns *ErrCommentsCorrupted if the existing file
// contains invalid JSON. An empty comment is a no-op.
func AppendComment(dir string, entry CommentEntry) error {
	if entry.Comment == "" {
		return nil
	}

	comments, err := LoadComments(dir)
	if err != nil {
		return err
	}

	comments = append(comments, entry)

	data, err := json.MarshalIndent(comments, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal human-comments.json: %w", err)
	}
	data = append(data, '\n')

	p := CommentsFilePath(dir)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		return fmt.Errorf("write human-comments.json: %w", err)
	}
	return nil
}

// ErrStateNotFound is returned when the workflow state file does not exist.
type ErrStateNotFound struct {
	Path string
}

func (e *ErrStateNotFound) Error() string {
	return fmt.Sprintf("workflow state file not found: %s", e.Path)
}

// ErrStateParseFailed is returned when the workflow state file contains invalid JSON.
type ErrStateParseFailed struct {
	Path string
	Err  error
}

func (e *ErrStateParseFailed) Error() string {
	return fmt.Sprintf("failed to parse workflow state file %s: %v", e.Path, e.Err)
}

// Unwrap returns the underlying parse error.
func (e *ErrStateParseFailed) Unwrap() error {
	return e.Err
}

// StateFilePath returns the full path to the workflow state file within the
// given directory.
func StateFilePath(dir string) string {
	return filepath.Join(dir, stateFileName)
}

// SaveState writes the workflow state to {dir}/workflow-state.json using an
// atomic write pattern: data is first written to a temporary file in the same
// directory, then renamed to the target path. The JSON is indented for human
// readability.
func SaveState(dir string, state *WorkflowStateJSON) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal workflow state: %w", err)
	}
	data = append(data, '\n')

	target := StateFilePath(dir)

	// Write to a temp file in the same directory so os.Rename is atomic
	// (same filesystem).
	tmp, err := os.CreateTemp(dir, ".workflow-state-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()

	// Clean up the temp file on any failure path.
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

// LoadState reads the workflow state from {dir}/workflow-state.json. It returns
// *ErrStateNotFound if the file does not exist and *ErrStateParseFailed if the
// file contains invalid JSON.
func LoadState(dir string) (*WorkflowStateJSON, error) {
	p := StateFilePath(dir)

	data, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, &ErrStateNotFound{Path: p}
		}
		return nil, fmt.Errorf("read workflow state file: %w", err)
	}

	var state WorkflowStateJSON
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, &ErrStateParseFailed{Path: p, Err: err}
	}

	return &state, nil
}
