package specworkflow

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPersistenceRoundTrip(t *testing.T) {
	dir := t.TempDir()

	orig := &WorkflowStateJSON{
		State:                      StateReviewing,
		Round:                      3,
		FeatureName:                "widget-factory",
		StartedAt:                  "2025-06-01T10:00:00Z",
		UpdatedAt:                  "2025-06-01T12:30:00Z",
		CumulativeCostUSD:          1.45,
		CumulativeWallClockSeconds: 9000,
		AgentInvocations:           12,
		FindingsSummary: FindingsSummary{
			Raised:       8,
			Closed:       5,
			OpenCritical: 1,
			OpenMajor:    2,
		},
		HadCriticalFindings:  true,
		Gate1CorrectionCount: 2,
		Gate2RedraftCount:    1,
		CurrentSpecVersion:   4,
		SkillChecksums: map[string]string{
			"skills/drafter.md":  "abc123",
			"skills/reviewer.md": "def456",
		},
	}

	if err := SaveState(dir, orig); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	loaded, err := LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}

	// Compare all fields.
	if loaded.State != orig.State {
		t.Errorf("State: got %v, want %v", loaded.State, orig.State)
	}
	if loaded.Round != orig.Round {
		t.Errorf("Round: got %d, want %d", loaded.Round, orig.Round)
	}
	if loaded.FeatureName != orig.FeatureName {
		t.Errorf("FeatureName: got %q, want %q", loaded.FeatureName, orig.FeatureName)
	}
	if loaded.StartedAt != orig.StartedAt {
		t.Errorf("StartedAt: got %q, want %q", loaded.StartedAt, orig.StartedAt)
	}
	if loaded.UpdatedAt != orig.UpdatedAt {
		t.Errorf("UpdatedAt: got %q, want %q", loaded.UpdatedAt, orig.UpdatedAt)
	}
	if loaded.CumulativeCostUSD != orig.CumulativeCostUSD {
		t.Errorf("CumulativeCostUSD: got %f, want %f", loaded.CumulativeCostUSD, orig.CumulativeCostUSD)
	}
	if loaded.CumulativeWallClockSeconds != orig.CumulativeWallClockSeconds {
		t.Errorf("CumulativeWallClockSeconds: got %f, want %f", loaded.CumulativeWallClockSeconds, orig.CumulativeWallClockSeconds)
	}
	if loaded.AgentInvocations != orig.AgentInvocations {
		t.Errorf("AgentInvocations: got %d, want %d", loaded.AgentInvocations, orig.AgentInvocations)
	}
	if loaded.FindingsSummary != orig.FindingsSummary {
		t.Errorf("FindingsSummary: got %+v, want %+v", loaded.FindingsSummary, orig.FindingsSummary)
	}
	if loaded.HadCriticalFindings != orig.HadCriticalFindings {
		t.Errorf("HadCriticalFindings: got %v, want %v", loaded.HadCriticalFindings, orig.HadCriticalFindings)
	}
	if loaded.Gate1CorrectionCount != orig.Gate1CorrectionCount {
		t.Errorf("Gate1CorrectionCount: got %d, want %d", loaded.Gate1CorrectionCount, orig.Gate1CorrectionCount)
	}
	if loaded.Gate2RedraftCount != orig.Gate2RedraftCount {
		t.Errorf("Gate2RedraftCount: got %d, want %d", loaded.Gate2RedraftCount, orig.Gate2RedraftCount)
	}
	if loaded.CurrentSpecVersion != orig.CurrentSpecVersion {
		t.Errorf("CurrentSpecVersion: got %d, want %d", loaded.CurrentSpecVersion, orig.CurrentSpecVersion)
	}
	if len(loaded.SkillChecksums) != len(orig.SkillChecksums) {
		t.Errorf("SkillChecksums length: got %d, want %d", len(loaded.SkillChecksums), len(orig.SkillChecksums))
	}
	for k, v := range orig.SkillChecksums {
		if loaded.SkillChecksums[k] != v {
			t.Errorf("SkillChecksums[%q]: got %q, want %q", k, loaded.SkillChecksums[k], v)
		}
	}
}

func TestPersistenceAtomicWrite(t *testing.T) {
	dir := t.TempDir()

	state := &WorkflowStateJSON{
		State:       StateInit,
		FeatureName: "atomic-test",
	}

	if err := SaveState(dir, state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	// After SaveState, there should be exactly one file: workflow-state.json.
	// No temp files should remain.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}

	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}

	if len(names) != 1 {
		t.Fatalf("expected 1 file, got %d: %v", len(names), names)
	}
	if names[0] != "workflow-state.json" {
		t.Errorf("expected workflow-state.json, got %s", names[0])
	}

	// Verify the file is valid JSON (not a partial write).
	data, err := os.ReadFile(filepath.Join(dir, "workflow-state.json"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !json.Valid(data) {
		t.Error("written file is not valid JSON")
	}
}

func TestPersistenceLoadStateNotFound(t *testing.T) {
	dir := t.TempDir()

	_, err := LoadState(dir)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var notFound *ErrStateNotFound
	if !errors.As(err, &notFound) {
		t.Fatalf("expected *ErrStateNotFound, got %T: %v", err, err)
	}

	expectedPath := StateFilePath(dir)
	if notFound.Path != expectedPath {
		t.Errorf("ErrStateNotFound.Path: got %q, want %q", notFound.Path, expectedPath)
	}
}

func TestPersistenceLoadStateParseFailed(t *testing.T) {
	dir := t.TempDir()
	p := StateFilePath(dir)

	if err := os.WriteFile(p, []byte("{invalid json!!!"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := LoadState(dir)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var parseFailed *ErrStateParseFailed
	if !errors.As(err, &parseFailed) {
		t.Fatalf("expected *ErrStateParseFailed, got %T: %v", err, err)
	}

	if parseFailed.Path != p {
		t.Errorf("ErrStateParseFailed.Path: got %q, want %q", parseFailed.Path, p)
	}

	// Unwrap should return the underlying JSON parse error.
	if parseFailed.Unwrap() == nil {
		t.Error("ErrStateParseFailed.Unwrap() returned nil")
	}
}

func TestPersistenceLoadStateFromHandwrittenJSON(t *testing.T) {
	dir := t.TempDir()
	p := StateFilePath(dir)

	handwritten := `{
  "state": "JUDGING",
  "round": 2,
  "feature_name": "payment-gateway",
  "started_at": "2025-07-15T08:00:00Z",
  "updated_at": "2025-07-15T14:22:33Z",
  "cumulative_cost_usd": 3.21,
  "cumulative_wall_clock_seconds": 23000.5,
  "agent_invocations": 25,
  "findings_summary": {
    "raised": 12,
    "closed": 10,
    "open_critical": 0,
    "open_major": 2
  },
  "had_critical_findings": true,
  "gate1_correction_count": 1,
  "gate2_redraft_count": 0,
  "current_spec_version": 5,
  "skill_checksums": {
    "skills/judge.md": "aaabbb"
  }
}`

	if err := os.WriteFile(p, []byte(handwritten), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	loaded, err := LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}

	if loaded.State != StateJudging {
		t.Errorf("State: got %v, want JUDGING", loaded.State)
	}
	if loaded.Round != 2 {
		t.Errorf("Round: got %d, want 2", loaded.Round)
	}
	if loaded.FeatureName != "payment-gateway" {
		t.Errorf("FeatureName: got %q, want %q", loaded.FeatureName, "payment-gateway")
	}
	if loaded.StartedAt != "2025-07-15T08:00:00Z" {
		t.Errorf("StartedAt: got %q", loaded.StartedAt)
	}
	if loaded.UpdatedAt != "2025-07-15T14:22:33Z" {
		t.Errorf("UpdatedAt: got %q", loaded.UpdatedAt)
	}
	if loaded.CumulativeCostUSD != 3.21 {
		t.Errorf("CumulativeCostUSD: got %f, want 3.21", loaded.CumulativeCostUSD)
	}
	if loaded.CumulativeWallClockSeconds != 23000.5 {
		t.Errorf("CumulativeWallClockSeconds: got %f, want 23000.5", loaded.CumulativeWallClockSeconds)
	}
	if loaded.AgentInvocations != 25 {
		t.Errorf("AgentInvocations: got %d, want 25", loaded.AgentInvocations)
	}
	if loaded.FindingsSummary.Raised != 12 {
		t.Errorf("Raised: got %d, want 12", loaded.FindingsSummary.Raised)
	}
	if loaded.FindingsSummary.Closed != 10 {
		t.Errorf("Closed: got %d, want 10", loaded.FindingsSummary.Closed)
	}
	if loaded.FindingsSummary.OpenCritical != 0 {
		t.Errorf("OpenCritical: got %d, want 0", loaded.FindingsSummary.OpenCritical)
	}
	if loaded.FindingsSummary.OpenMajor != 2 {
		t.Errorf("OpenMajor: got %d, want 2", loaded.FindingsSummary.OpenMajor)
	}
	if !loaded.HadCriticalFindings {
		t.Error("HadCriticalFindings: got false, want true")
	}
	if loaded.Gate1CorrectionCount != 1 {
		t.Errorf("Gate1CorrectionCount: got %d, want 1", loaded.Gate1CorrectionCount)
	}
	if loaded.Gate2RedraftCount != 0 {
		t.Errorf("Gate2RedraftCount: got %d, want 0", loaded.Gate2RedraftCount)
	}
	if loaded.CurrentSpecVersion != 5 {
		t.Errorf("CurrentSpecVersion: got %d, want 5", loaded.CurrentSpecVersion)
	}
	if loaded.SkillChecksums["skills/judge.md"] != "aaabbb" {
		t.Errorf("SkillChecksums[skills/judge.md]: got %q, want %q", loaded.SkillChecksums["skills/judge.md"], "aaabbb")
	}
}

func TestPersistenceErrorInterfaces(t *testing.T) {
	// ErrStateNotFound implements error.
	var _ error = &ErrStateNotFound{Path: "/tmp/test"}
	notFound := &ErrStateNotFound{Path: "/some/path/workflow-state.json"}
	if !strings.Contains(notFound.Error(), "/some/path/workflow-state.json") {
		t.Errorf("ErrStateNotFound.Error() should contain path, got: %s", notFound.Error())
	}

	// ErrStateParseFailed implements error and Unwrap.
	inner := errors.New("unexpected character")
	var _ error = &ErrStateParseFailed{Path: "/tmp/test", Err: inner}
	parseFailed := &ErrStateParseFailed{Path: "/some/path/workflow-state.json", Err: inner}
	if !strings.Contains(parseFailed.Error(), "/some/path/workflow-state.json") {
		t.Errorf("ErrStateParseFailed.Error() should contain path, got: %s", parseFailed.Error())
	}
	if !strings.Contains(parseFailed.Error(), "unexpected character") {
		t.Errorf("ErrStateParseFailed.Error() should contain inner error, got: %s", parseFailed.Error())
	}
	if !errors.Is(parseFailed, inner) {
		t.Error("errors.Is(parseFailed, inner) should be true")
	}
	if parseFailed.Unwrap() != inner {
		t.Error("Unwrap() should return the inner error")
	}
}

func TestPersistenceStateFilePath(t *testing.T) {
	got := StateFilePath("/foo/bar")
	want := filepath.Join("/foo/bar", "workflow-state.json")
	if got != want {
		t.Errorf("StateFilePath: got %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// Comment protocol tests
// ---------------------------------------------------------------------------

func TestCommentPersistence_AppendOnly(t *testing.T) {
	dir := t.TempDir()

	// Append first comment.
	err := AppendComment(dir, CommentEntry{
		Gate:      "HUMAN_GATE_1",
		Action:    "correct",
		Comment:   "Fix the actor list",
		Timestamp: "2026-03-25T10:00:00Z",
	})
	if err != nil {
		t.Fatalf("AppendComment (1): %v", err)
	}

	// Append second comment.
	err = AppendComment(dir, CommentEntry{
		Gate:      "HUMAN_GATE_2",
		Action:    "confirm",
		Comment:   "Looks good",
		Timestamp: "2026-03-25T11:00:00Z",
	})
	if err != nil {
		t.Fatalf("AppendComment (2): %v", err)
	}

	// Load and verify both comments exist (append-only).
	comments, err := LoadComments(dir)
	if err != nil {
		t.Fatalf("LoadComments: %v", err)
	}
	if len(comments) != 2 {
		t.Fatalf("expected 2 comments, got %d", len(comments))
	}
	if comments[0].Gate != "HUMAN_GATE_1" {
		t.Errorf("comment[0].Gate = %q, want HUMAN_GATE_1", comments[0].Gate)
	}
	if comments[0].Comment != "Fix the actor list" {
		t.Errorf("comment[0].Comment = %q, want 'Fix the actor list'", comments[0].Comment)
	}
	if comments[1].Gate != "HUMAN_GATE_2" {
		t.Errorf("comment[1].Gate = %q, want HUMAN_GATE_2", comments[1].Gate)
	}
}

func TestCommentPersistence_TaskHumanGate(t *testing.T) {
	dir := t.TempDir()

	err := AppendComment(dir, CommentEntry{
		Gate:      "TASK_HUMAN_GATE",
		Action:    "approve",
		Comment:   "Looks good, ship it",
		Timestamp: "2026-03-25T12:00:00Z",
	})
	if err != nil {
		t.Fatalf("AppendComment: %v", err)
	}

	comments, err := LoadComments(dir)
	if err != nil {
		t.Fatalf("LoadComments: %v", err)
	}
	if len(comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(comments))
	}
	if comments[0].Gate != "TASK_HUMAN_GATE" {
		t.Errorf("Gate = %q, want TASK_HUMAN_GATE", comments[0].Gate)
	}
	if comments[0].Action != "approve" {
		t.Errorf("Action = %q, want approve", comments[0].Action)
	}
}

func TestCommentPersistence_SurvivesRewind(t *testing.T) {
	dir := t.TempDir()

	// Append a comment.
	err := AppendComment(dir, CommentEntry{
		Gate:      "HUMAN_GATE_1",
		Action:    "correct",
		Comment:   "Pre-rewind comment",
		Timestamp: "2026-03-25T10:00:00Z",
	})
	if err != nil {
		t.Fatalf("AppendComment: %v", err)
	}

	// Simulate rewind: save a new workflow state (rewind doesn't touch comments).
	state := &WorkflowStateJSON{
		State:       StateDiscovery,
		Round:       1,
		FeatureName: "test-feature",
	}
	if err := SaveState(dir, state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	// Comments must survive the rewind.
	comments, err := LoadComments(dir)
	if err != nil {
		t.Fatalf("LoadComments after rewind: %v", err)
	}
	if len(comments) != 1 {
		t.Fatalf("expected 1 comment after rewind, got %d", len(comments))
	}
	if comments[0].Comment != "Pre-rewind comment" {
		t.Errorf("comment = %q, want 'Pre-rewind comment'", comments[0].Comment)
	}
}

func TestCommentPersistence_CorruptedFileEscalates(t *testing.T) {
	dir := t.TempDir()

	// Write corrupted JSON to human-comments.json.
	p := CommentsFilePath(dir)
	if err := os.WriteFile(p, []byte("{not valid json!!!"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// LoadComments should return ErrCommentsCorrupted.
	_, err := LoadComments(dir)
	if err == nil {
		t.Fatal("expected error for corrupted file, got nil")
	}
	var corrupted *ErrCommentsCorrupted
	if !errors.As(err, &corrupted) {
		t.Fatalf("expected *ErrCommentsCorrupted, got %T: %v", err, err)
	}
	if corrupted.Path != p {
		t.Errorf("ErrCommentsCorrupted.Path = %q, want %q", corrupted.Path, p)
	}
	if corrupted.Unwrap() == nil {
		t.Error("Unwrap() should return the underlying JSON error")
	}

	// AppendComment should also fail on corrupted file.
	err = AppendComment(dir, CommentEntry{
		Gate:      "HUMAN_GATE_1",
		Action:    "confirm",
		Comment:   "This should fail",
		Timestamp: "2026-03-25T10:00:00Z",
	})
	if err == nil {
		t.Fatal("expected error from AppendComment on corrupted file, got nil")
	}
	if !errors.As(err, &corrupted) {
		t.Fatalf("expected *ErrCommentsCorrupted from AppendComment, got %T: %v", err, err)
	}
}

func TestCommentPersistence_EmptyCommentIsNoop(t *testing.T) {
	dir := t.TempDir()

	err := AppendComment(dir, CommentEntry{
		Gate:      "HUMAN_GATE_1",
		Action:    "confirm",
		Comment:   "",
		Timestamp: "2026-03-25T10:00:00Z",
	})
	if err != nil {
		t.Fatalf("AppendComment with empty comment: %v", err)
	}

	// File should not exist (no-op).
	if _, err := os.Stat(CommentsFilePath(dir)); !os.IsNotExist(err) {
		t.Error("expected no file for empty comment, but file exists")
	}
}

func TestCommentPersistence_FileNotExistReturnsEmpty(t *testing.T) {
	dir := t.TempDir()

	comments, err := LoadComments(dir)
	if err != nil {
		t.Fatalf("LoadComments on non-existent file: %v", err)
	}
	if comments != nil {
		t.Errorf("expected nil, got %v", comments)
	}
}

func TestCommentPersistence_JSONRoundTrip(t *testing.T) {
	dir := t.TempDir()

	entries := []CommentEntry{
		{Gate: "HUMAN_GATE_1", Action: "correct", Comment: "Fix actors", Timestamp: "2026-03-25T10:00:00Z"},
		{Gate: "HUMAN_GATE_2", Action: "confirm", Comment: "LGTM", Timestamp: "2026-03-25T11:00:00Z"},
		{Gate: "HUMAN_GATE_FINAL", Action: "approve", Comment: "Ship it", Timestamp: "2026-03-25T12:00:00Z"},
		{Gate: "TASK_HUMAN_GATE", Action: "approve", Comment: "Tasks look good", Timestamp: "2026-03-25T13:00:00Z"},
	}

	for _, e := range entries {
		if err := AppendComment(dir, e); err != nil {
			t.Fatalf("AppendComment(%s): %v", e.Gate, err)
		}
	}

	loaded, err := LoadComments(dir)
	if err != nil {
		t.Fatalf("LoadComments: %v", err)
	}
	if len(loaded) != len(entries) {
		t.Fatalf("expected %d comments, got %d", len(entries), len(loaded))
	}
	for i, want := range entries {
		got := loaded[i]
		if got.Gate != want.Gate || got.Action != want.Action || got.Comment != want.Comment || got.Timestamp != want.Timestamp {
			t.Errorf("comment[%d]: got %+v, want %+v", i, got, want)
		}
	}

	// Verify the file is valid JSON.
	data, err := os.ReadFile(CommentsFilePath(dir))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !json.Valid(data) {
		t.Error("human-comments.json is not valid JSON")
	}
}

func TestCommentsFilePath(t *testing.T) {
	got := CommentsFilePath("/foo/bar")
	want := filepath.Join("/foo/bar", "human-comments.json")
	if got != want {
		t.Errorf("CommentsFilePath: got %q, want %q", got, want)
	}
}
