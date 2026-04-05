package specworkflow

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

// ---------------------------------------------------------------------------
// CD-88v.1: MockBeadsClient satisfies BeadsClientInterface
// ---------------------------------------------------------------------------

func TestMockBeadsClientInterface(t *testing.T) {
	var _ BeadsClientInterface = (*MockBeadsClient)(nil)
}

func TestBeadsClientInterface(t *testing.T) {
	var _ BeadsClientInterface = (*BeadsClient)(nil)
}

func TestMockBeadsClientRecordsCalls(t *testing.T) {
	mock := &MockBeadsClient{
		CreateEpicResult:    "CD-abc",
		CreateFindingResult: "CD-abc.1",
		KVGetResult:         "some-value",
	}
	ctx := context.Background()

	id, err := mock.CreateEpic(ctx, "orchestrator", "test epic")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "CD-abc" {
		t.Errorf("got %q, want %q", id, "CD-abc")
	}

	finding := MergedFinding{
		ID:              "CRIT-001",
		Severity:        SeverityCritical,
		Lens:            "SEC",
		RoundRaised:     1,
		AffectedSection: "§4.2",
		RaisedBy:        []string{"reviewer-a"},
		Description:     "Missing auth check",
	}
	fid, err := mock.CreateFinding(ctx, "orchestrator", "CD-abc", finding, "run-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fid != "CD-abc.1" {
		t.Errorf("got %q, want %q", fid, "CD-abc.1")
	}

	val, err := mock.KVGet(ctx, "orchestrator", "some-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "some-value" {
		t.Errorf("got %q, want %q", val, "some-value")
	}

	calls := mock.GetCalls()
	if len(calls) != 3 {
		t.Fatalf("expected 3 calls, got %d", len(calls))
	}
	if calls[0].Method != "CreateEpic" {
		t.Errorf("call 0: got %q, want CreateEpic", calls[0].Method)
	}
	if calls[1].Method != "CreateFinding" {
		t.Errorf("call 1: got %q, want CreateFinding", calls[1].Method)
	}
	if calls[2].Method != "KVGet" {
		t.Errorf("call 2: got %q, want KVGet", calls[2].Method)
	}
}

// ---------------------------------------------------------------------------
// CD-88v.4: BeadsClient unit tests
// ---------------------------------------------------------------------------

func TestBeadsClient_ErrBeadsUnavailable(t *testing.T) {
	// Use a PATH that contains no bd binary.
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", t.TempDir())
	defer os.Setenv("PATH", origPath)

	_, err := NewBeadsClient("/tmp")
	if !errors.Is(err, ErrBeadsUnavailable) {
		t.Errorf("expected ErrBeadsUnavailable, got: %v", err)
	}
}

func TestBeadsClient_WrappedStderrOnNonZeroExit(t *testing.T) {
	// Create a fake bd script that exits 1 with stderr.
	tmpDir := t.TempDir()
	fakeBd := filepath.Join(tmpDir, "bd")
	err := os.WriteFile(fakeBd, []byte("#!/bin/sh\necho 'disk quota exceeded' >&2\nexit 1\n"), 0755)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the script is executable.
	if _, err := exec.LookPath(fakeBd); err != nil {
		t.Skipf("cannot exec fake bd: %v", err)
	}

	client := &BeadsClient{
		workspaceDir: tmpDir,
		bdPath:       fakeBd,
	}

	ctx := context.Background()
	_, err = client.CreateEpic(ctx, "orchestrator", "test")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !containsSubstring(err.Error(), "disk quota exceeded") {
		t.Errorf("error should contain stderr, got: %v", err)
	}
	// Must NOT be ErrBeadsUnavailable — bd was found, it just failed.
	if errors.Is(err, ErrBeadsUnavailable) {
		t.Error("should not be ErrBeadsUnavailable for a non-zero exit")
	}
}

func TestBeadsClient_ConcurrentCallsSerialized(t *testing.T) {
	// Create a fake bd that writes to a shared file to detect concurrency.
	tmpDir := t.TempDir()
	lockFile := filepath.Join(tmpDir, "lock")
	fakeBd := filepath.Join(tmpDir, "bd")

	// The script checks for a lock file — if present, concurrency detected.
	script := `#!/bin/sh
if [ -f "` + lockFile + `" ]; then
  echo "CONCURRENT" >&2
  exit 1
fi
touch "` + lockFile + `"
sleep 0.05
rm "` + lockFile + `"
echo "OK"
`
	if err := os.WriteFile(fakeBd, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	client := &BeadsClient{
		workspaceDir: tmpDir,
		bdPath:       fakeBd,
	}

	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make([]error, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, errs[idx] = client.CreateEpic(ctx, "orchestrator", "test")
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d got error (concurrent access detected): %v", i, err)
		}
	}
}

func TestBeadsClient_SuccessfulCommand(t *testing.T) {
	tmpDir := t.TempDir()
	fakeBd := filepath.Join(tmpDir, "bd")
	err := os.WriteFile(fakeBd, []byte("#!/bin/sh\necho 'CD-abc'\n"), 0755)
	if err != nil {
		t.Fatal(err)
	}

	client := &BeadsClient{
		workspaceDir: tmpDir,
		bdPath:       fakeBd,
	}

	ctx := context.Background()
	id, err := client.CreateEpic(ctx, "orchestrator", "my epic")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "CD-abc" {
		t.Errorf("got %q, want %q", id, "CD-abc")
	}
}

// ---------------------------------------------------------------------------
// CD-88v.6: EnsureCustomTypesConfigured tests
// ---------------------------------------------------------------------------

func TestBeadsClientCustomTypesConfigured_AppendsNotReplaces(t *testing.T) {
	tmpDir := t.TempDir()
	stateFile := filepath.Join(tmpDir, "types_state")

	// Fake bd: "config get" returns "task,epic" from state file,
	// "config set" writes the new value to state file.
	fakeBd := filepath.Join(tmpDir, "bd")
	script := `#!/bin/sh
if [ "$1" = "config" ] && [ "$2" = "get" ]; then
  if [ -f "` + stateFile + `" ]; then
    cat "` + stateFile + `"
  else
    echo "task,epic"
  fi
  exit 0
fi
if [ "$1" = "config" ] && [ "$2" = "set" ]; then
  printf '%s' "$4" > "` + stateFile + `"
  exit 0
fi
echo "unknown command" >&2
exit 1
`
	if err := os.WriteFile(fakeBd, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	client := &BeadsClient{
		workspaceDir: tmpDir,
		bdPath:       fakeBd,
	}

	ctx := context.Background()
	if err := client.EnsureCustomTypesConfigured(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the state file was written with appended value.
	data, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatalf("state file not written: %v", err)
	}
	got := string(data)
	if got != "task,epic,finding" {
		t.Errorf("expected 'task,epic,finding', got %q", got)
	}
}

func TestBeadsClientCustomTypesConfigured_SkipsIfAlreadyPresent(t *testing.T) {
	tmpDir := t.TempDir()
	stateFile := filepath.Join(tmpDir, "types_state")

	// Fake bd: "config get" returns value that already contains "finding".
	fakeBd := filepath.Join(tmpDir, "bd")
	script := `#!/bin/sh
if [ "$1" = "config" ] && [ "$2" = "get" ]; then
  echo "task,finding,epic"
  exit 0
fi
if [ "$1" = "config" ] && [ "$2" = "set" ]; then
  printf '%s' "$4" > "` + stateFile + `"
  exit 0
fi
echo "unknown command" >&2
exit 1
`
	if err := os.WriteFile(fakeBd, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	client := &BeadsClient{
		workspaceDir: tmpDir,
		bdPath:       fakeBd,
	}

	ctx := context.Background()
	if err := client.EnsureCustomTypesConfigured(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// State file should NOT have been written (config set was not called).
	if _, err := os.Stat(stateFile); err == nil {
		t.Error("config set should not have been called when finding already present")
	}
}

func TestBeadsClientCustomTypesConfigured_SetsWhenEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	stateFile := filepath.Join(tmpDir, "types_state")

	// Fake bd: "config get" returns empty string.
	fakeBd := filepath.Join(tmpDir, "bd")
	script := `#!/bin/sh
if [ "$1" = "config" ] && [ "$2" = "get" ]; then
  echo ""
  exit 0
fi
if [ "$1" = "config" ] && [ "$2" = "set" ]; then
  printf '%s' "$4" > "` + stateFile + `"
  exit 0
fi
echo "unknown command" >&2
exit 1
`
	if err := os.WriteFile(fakeBd, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	client := &BeadsClient{
		workspaceDir: tmpDir,
		bdPath:       fakeBd,
	}

	ctx := context.Background()
	if err := client.EnsureCustomTypesConfigured(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatalf("state file not written: %v", err)
	}
	got := string(data)
	if got != "finding" {
		t.Errorf("expected 'finding', got %q", got)
	}
}

// ---------------------------------------------------------------------------
// FR-004: CreateFinding uses --silent flag
// ---------------------------------------------------------------------------

func TestBeadsClientCreateFinding_UsesSilentFlag(t *testing.T) {
	tmpDir := t.TempDir()
	argsFile := filepath.Join(tmpDir, "args")
	fakeBd := filepath.Join(tmpDir, "bd")
	// Script dumps all args to a file and prints a bead ID.
	script := `#!/bin/sh
printf '%s\n' "$@" > "` + argsFile + `"
echo "CD-test.1"
`
	if err := os.WriteFile(fakeBd, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	client := &BeadsClient{workspaceDir: tmpDir, bdPath: fakeBd}
	ctx := context.Background()

	finding := MergedFinding{
		ID: "CRIT-001", Severity: SeverityCritical, Lens: "SEC",
		RoundRaised: 1, AffectedSection: "§4", RaisedBy: []string{"a"},
		Description: "test finding",
	}
	id, err := client.CreateFinding(ctx, "orchestrator", "CD-epic", finding, "run-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "CD-test.1" {
		t.Errorf("got %q, want %q", id, "CD-test.1")
	}

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	args := string(data)
	if !containsSubstring(args, "--silent") {
		t.Errorf("expected --silent in args, got:\n%s", args)
	}
}

// ---------------------------------------------------------------------------
// MIN-003: Comment with special characters passed as single arg
// ---------------------------------------------------------------------------

func TestBeadsClientComment_SpecialCharacters(t *testing.T) {
	tmpDir := t.TempDir()
	argsFile := filepath.Join(tmpDir, "args")
	fakeBd := filepath.Join(tmpDir, "bd")
	// Script writes each arg on its own line using NUL separator for safety.
	script := `#!/bin/sh
for arg in "$@"; do
  printf '%s\0' "$arg"
done > "` + argsFile + `"
`
	if err := os.WriteFile(fakeBd, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	client := &BeadsClient{workspaceDir: tmpDir, bdPath: fakeBd}
	ctx := context.Background()

	commentText := "Judge says: \"verified\" — $cost was §4.2\nnewline here"
	err := client.Comment(ctx, "judge", "CD-abc.1", commentText)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	// The comment should appear as a single NUL-delimited arg containing the full text.
	if !containsSubstring(string(data), commentText) {
		t.Errorf("comment text not found as single arg in output:\n%q", string(data))
	}
}

// ---------------------------------------------------------------------------
// US-5: Comment passes --actor=judge
// ---------------------------------------------------------------------------

func TestBeadsClientComment_ActorFlag(t *testing.T) {
	tmpDir := t.TempDir()
	argsFile := filepath.Join(tmpDir, "args")
	fakeBd := filepath.Join(tmpDir, "bd")
	script := `#!/bin/sh
printf '%s\n' "$@" > "` + argsFile + `"
`
	if err := os.WriteFile(fakeBd, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	client := &BeadsClient{workspaceDir: tmpDir, bdPath: fakeBd}
	ctx := context.Background()

	if err := client.Comment(ctx, "judge", "CD-abc.1", "verified"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSubstring(string(data), "--actor=judge") {
		t.Errorf("expected --actor=judge in args, got:\n%s", string(data))
	}
}

// ---------------------------------------------------------------------------
// EC-02: ID parsing — newline trimmed from silent output
// ---------------------------------------------------------------------------

func TestBeadsClientIDParsing_SilentOutput(t *testing.T) {
	tmpDir := t.TempDir()
	fakeBd := filepath.Join(tmpDir, "bd")
	// Output includes trailing newline (normal shell echo behavior).
	if err := os.WriteFile(fakeBd, []byte("#!/bin/sh\necho 'CD-abc123'\n"), 0755); err != nil {
		t.Fatal(err)
	}

	client := &BeadsClient{workspaceDir: tmpDir, bdPath: fakeBd}
	ctx := context.Background()

	id, err := client.CreateEpic(ctx, "orchestrator", "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "CD-abc123" {
		t.Errorf("expected trimmed ID %q, got %q", "CD-abc123", id)
	}
}

// ---------------------------------------------------------------------------
// EC-02: ID parsing — empty stdout on exit 0
// ---------------------------------------------------------------------------

func TestBeadsClientIDParsing_EmptyOutput(t *testing.T) {
	tmpDir := t.TempDir()
	fakeBd := filepath.Join(tmpDir, "bd")
	// Exit 0 but no output — should error after one retry per EC-02.
	if err := os.WriteFile(fakeBd, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}

	client := &BeadsClient{workspaceDir: tmpDir, bdPath: fakeBd}
	ctx := context.Background()

	_, err := client.CreateEpic(ctx, "orchestrator", "test")
	if err == nil {
		t.Fatal("expected error for empty bead ID, got nil")
	}
	if !containsSubstring(err.Error(), "empty bead ID") {
		t.Errorf("expected 'empty bead ID' in error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// FR-014: GateShow JSON parsing
// ---------------------------------------------------------------------------

func TestBeadsClientGateShow_OpenGate(t *testing.T) {
	tmpDir := t.TempDir()
	fakeBd := filepath.Join(tmpDir, "bd")
	// Fake bd returns JSON with status "open" and no close_reason.
	script := `#!/bin/sh
echo '{"id": "GATE-001", "status": "open", "close_reason": ""}'
`
	if err := os.WriteFile(fakeBd, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	client := &BeadsClient{workspaceDir: tmpDir, bdPath: fakeBd}
	ctx := context.Background()

	data, err := client.GateShow(ctx, "orchestrator", "GATE-001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Parse the returned JSON the same way the orchestrator does.
	var result struct {
		ID          string `json:"id"`
		Status      string `json:"status"`
		CloseReason string `json:"close_reason"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("failed to parse GateShow output: %v", err)
	}
	if result.Status != "open" {
		t.Errorf("status = %q, want %q", result.Status, "open")
	}
	if result.CloseReason != "" {
		t.Errorf("close_reason = %q, want empty", result.CloseReason)
	}
}

func TestBeadsClientGateShow_ClosedWithAccept(t *testing.T) {
	tmpDir := t.TempDir()
	fakeBd := filepath.Join(tmpDir, "bd")
	script := `#!/bin/sh
echo '{"id": "GATE-002", "status": "closed", "close_reason": "ACCEPT: lgtm"}'
`
	if err := os.WriteFile(fakeBd, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	client := &BeadsClient{workspaceDir: tmpDir, bdPath: fakeBd}
	ctx := context.Background()

	data, err := client.GateShow(ctx, "orchestrator", "GATE-002")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result struct {
		ID          string `json:"id"`
		Status      string `json:"status"`
		CloseReason string `json:"close_reason"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("failed to parse GateShow output: %v", err)
	}
	if result.Status != "closed" {
		t.Errorf("status = %q, want %q", result.Status, "closed")
	}
	if result.CloseReason != "ACCEPT: lgtm" {
		t.Errorf("close_reason = %q, want %q", result.CloseReason, "ACCEPT: lgtm")
	}

	// Verify the orchestrator's parseCloseReason correctly extracts the action.
	action, comment, ok := parseCloseReason(result.CloseReason, "confirm", "correct")
	if !ok {
		t.Error("parseCloseReason should recognize ACCEPT: prefix")
	}
	if action != "confirm" {
		t.Errorf("action = %q, want %q", action, "confirm")
	}
	if comment != "lgtm" {
		t.Errorf("comment = %q, want %q", comment, "lgtm")
	}
}

func TestBeadsClientGateShow_MissingCloseReason(t *testing.T) {
	tmpDir := t.TempDir()
	fakeBd := filepath.Join(tmpDir, "bd")
	// JSON has no close_reason field at all.
	script := `#!/bin/sh
echo '{"id": "GATE-003", "status": "closed"}'
`
	if err := os.WriteFile(fakeBd, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	client := &BeadsClient{workspaceDir: tmpDir, bdPath: fakeBd}
	ctx := context.Background()

	data, err := client.GateShow(ctx, "orchestrator", "GATE-003")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result struct {
		ID          string `json:"id"`
		Status      string `json:"status"`
		CloseReason string `json:"close_reason"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("failed to parse GateShow output: %v", err)
	}
	if result.Status != "closed" {
		t.Errorf("status = %q, want %q", result.Status, "closed")
	}
	// Missing field should unmarshal as zero value (empty string), not panic.
	if result.CloseReason != "" {
		t.Errorf("close_reason = %q, want empty string for missing field", result.CloseReason)
	}

	// Verify parseCloseReason treats empty as unrecognized.
	_, _, ok := parseCloseReason(result.CloseReason, "confirm", "correct")
	if ok {
		t.Error("parseCloseReason should not recognize empty close_reason")
	}
}

func TestBeadsClientCustomTypesConfigured_SetsWhenConfigGetFails(t *testing.T) {
	tmpDir := t.TempDir()
	stateFile := filepath.Join(tmpDir, "types_state")

	// Fake bd: "config get" fails with non-zero exit (key absent).
	fakeBd := filepath.Join(tmpDir, "bd")
	script := `#!/bin/sh
if [ "$1" = "config" ] && [ "$2" = "get" ]; then
  echo "key not found" >&2
  exit 1
fi
if [ "$1" = "config" ] && [ "$2" = "set" ]; then
  printf '%s' "$4" > "` + stateFile + `"
  exit 0
fi
echo "unknown command" >&2
exit 1
`
	if err := os.WriteFile(fakeBd, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	client := &BeadsClient{
		workspaceDir: tmpDir,
		bdPath:       fakeBd,
	}

	ctx := context.Background()
	if err := client.EnsureCustomTypesConfigured(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatalf("state file not written: %v", err)
	}
	got := string(data)
	if got != "finding" {
		t.Errorf("expected 'finding', got %q", got)
	}
}

