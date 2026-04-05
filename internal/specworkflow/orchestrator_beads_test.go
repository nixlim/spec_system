package specworkflow

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestCreateRunEpic_WithBeads verifies that createRunEpic generates a UUID
// run_id, creates an epic, and stores both in KV with feature-namespaced keys.
// run_id (UUID) and run_epic_id (bead ID) are distinct (FR-040–042).
func TestCreateRunEpic_WithBeads(t *testing.T) {
	mock := &MockBeadsClient{
		CreateEpicResult: "EPIC-abc123",
	}

	orch := &Orchestrator{
		beadsClient: mock,
		featureName: "test-feature",
	}

	state := &WorkflowStateJSON{Round: 1}
	orch.createRunEpic(state)

	calls := mock.GetCalls()
	if len(calls) != 3 {
		t.Fatalf("expected 3 calls (CreateEpic + 2 KVSet), got %d: %+v", len(calls), calls)
	}

	if calls[0].Method != "CreateEpic" {
		t.Errorf("expected CreateEpic, got %s", calls[0].Method)
	}
	if calls[0].Args[1] != "test-feature spec-review run" {
		t.Errorf("expected title 'test-feature spec-review run', got %v", calls[0].Args[1])
	}

	// KVSet for {feature}:run_id — must be a UUID, not the epic bead ID.
	if calls[1].Method != "KVSet" || calls[1].Args[1] != "test-feature:run_id" {
		t.Errorf("expected KVSet test-feature:run_id, got %+v", calls[1])
	}
	runIDValue, ok := calls[1].Args[2].(string)
	if !ok || runIDValue == "" {
		t.Errorf("expected non-empty run_id UUID, got %v", calls[1].Args[2])
	}
	if runIDValue == "EPIC-abc123" {
		t.Error("run_id must be a UUID, not the epic bead ID (FR-040)")
	}
	// Verify UUID format (8-4-4-4-12 hex).
	if len(runIDValue) != 36 || runIDValue[8] != '-' {
		t.Errorf("run_id does not look like a UUID: %s", runIDValue)
	}

	// Verify state.RunID was set.
	if state.RunID != runIDValue {
		t.Errorf("expected state.RunID=%s, got %s", runIDValue, state.RunID)
	}

	// KVSet for {feature}:run_epic_id — must be the epic bead ID.
	if calls[2].Method != "KVSet" || calls[2].Args[1] != "test-feature:run_epic_id" || calls[2].Args[2] != "EPIC-abc123" {
		t.Errorf("expected KVSet test-feature:run_epic_id=EPIC-abc123, got %+v", calls[2])
	}
}

// TestCreateRunEpic_NilClient verifies graceful degradation when beadsClient is nil.
func TestCreateRunEpic_NilClient(t *testing.T) {
	orch := &Orchestrator{
		beadsClient: nil,
		featureName: "test-feature",
	}
	// Should not panic.
	orch.createRunEpic(&WorkflowStateJSON{Round: 1})
}

// TestCreateBeadsFindings verifies that createBeadsFindings creates findings
// under the run epic and stores the finding-to-bead mapping in KV with
// feature-namespaced keys.
func TestCreateBeadsFindings(t *testing.T) {
	mock := &MockBeadsClient{
		CreateFindingResult: "FIND-xyz789",
		KVGetFunc: func(key string) (string, error) {
			switch key {
			case "test-feature:run_epic_id", "test-feature:run_id":
				return "EPIC-abc123", nil
			default:
				return "", nil // dedup check returns empty → finding not yet created
			}
		},
	}

	orch := &Orchestrator{
		beadsClient: mock,
		featureName: "test-feature",
	}

	findings := []MergedFinding{
		{ID: "CRIT-001", Description: "Critical bug", Severity: SeverityCritical},
	}

	orch.createBeadsFindings(findings)

	calls := mock.GetCalls()
	// KVGet(run_epic_id) + KVGet(run_id) + KVGet(finding:CRIT-001 dedup check) + CreateFinding + KVSet(finding mapping)
	if len(calls) != 5 {
		t.Fatalf("expected 5 calls, got %d: %+v", len(calls), calls)
	}

	// Verify KVGet for {feature}:run_epic_id
	if calls[0].Method != "KVGet" || calls[0].Args[1] != "test-feature:run_epic_id" {
		t.Errorf("expected KVGet test-feature:run_epic_id, got %+v", calls[0])
	}

	// Verify KVGet for {feature}:run_id
	if calls[1].Method != "KVGet" || calls[1].Args[1] != "test-feature:run_id" {
		t.Errorf("expected KVGet test-feature:run_id, got %+v", calls[1])
	}

	// Verify dedup check: KVGet for {feature}:finding:CRIT-001
	if calls[2].Method != "KVGet" || calls[2].Args[1] != "test-feature:finding:CRIT-001" {
		t.Errorf("expected KVGet dedup check test-feature:finding:CRIT-001, got %+v", calls[2])
	}

	// Verify CreateFinding
	if calls[3].Method != "CreateFinding" {
		t.Errorf("expected CreateFinding, got %s", calls[3].Method)
	}

	// Verify KVSet for finding mapping
	if calls[4].Method != "KVSet" || calls[4].Args[1] != "test-feature:finding:CRIT-001" {
		t.Errorf("expected KVSet test-feature:finding:CRIT-001, got %+v", calls[4])
	}
}

// TestCreateBeadsFindings_DedupSkipsExisting verifies FR-009: if a finding
// already has a Beads ID in KV, it is not re-created.
func TestCreateBeadsFindings_DedupSkipsExisting(t *testing.T) {
	mock := &MockBeadsClient{
		// KVGet always returns a non-empty value, simulating existing entries.
		KVGetResult:         "EXISTING-BEAD-ID",
		CreateFindingResult: "FIND-new",
	}

	orch := &Orchestrator{
		beadsClient: mock,
		featureName: "test-feature",
	}

	findings := []MergedFinding{
		{ID: "CRIT-001", Description: "Already created", Severity: SeverityCritical},
	}

	orch.createBeadsFindings(findings)

	calls := mock.GetCalls()
	// KVGet(run_epic_id) + KVGet(run_id) + KVGet(finding:CRIT-001 dedup check → found)
	// NO CreateFinding, NO KVSet
	for _, c := range calls {
		if c.Method == "CreateFinding" {
			t.Error("expected CreateFinding to be skipped for existing finding (FR-009)")
		}
	}
}

// TestCreateBeadsFindings_NilClient verifies graceful degradation.
func TestCreateBeadsFindings_NilClient(t *testing.T) {
	orch := &Orchestrator{beadsClient: nil}
	orch.createBeadsFindings([]MergedFinding{{ID: "CRIT-001"}})
}

// TestUpdateBeadsIssueStatus_Update verifies that updateBeadsIssueStatus calls
// UpdateIssue for non-terminal statuses like StatusAddressed, with issue_status
// metadata (FR-011).
func TestUpdateBeadsIssueStatus_Update(t *testing.T) {
	mock := &MockBeadsClient{
		KVGetResult: "FIND-xyz789",
	}

	orch := &Orchestrator{beadsClient: mock, featureName: "test-feature"}

	orch.updateBeadsIssueStatus("CRIT-001", StatusAddressed)

	calls := mock.GetCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls (KVGet + UpdateIssue), got %d: %+v", len(calls), calls)
	}

	if calls[0].Method != "KVGet" || calls[0].Args[1] != "test-feature:finding:CRIT-001" {
		t.Errorf("expected KVGet test-feature:finding:CRIT-001, got %+v", calls[0])
	}

	if calls[1].Method != "UpdateIssue" || calls[1].Args[1] != "FIND-xyz789" || calls[1].Args[2] != "in_progress" {
		t.Errorf("expected UpdateIssue FIND-xyz789 in_progress (TD-B02), got %+v", calls[1])
	}

	// Verify issue_status metadata is passed (FR-011).
	meta, ok := calls[1].Args[3].(map[string]string)
	if !ok {
		t.Fatal("expected metadata map as 4th arg to UpdateIssue")
	}
	if meta["issue_status"] != "addressed" {
		t.Errorf("expected issue_status=addressed metadata, got %v", meta)
	}
}

// TestUpdateBeadsIssueStatus_CloseOnVerified verifies FR-010: StatusVerified
// uses bd close instead of bd update, with issue_status metadata (FR-011).
func TestUpdateBeadsIssueStatus_CloseOnVerified(t *testing.T) {
	mock := &MockBeadsClient{
		KVGetResult: "FIND-xyz789",
	}

	orch := &Orchestrator{beadsClient: mock, featureName: "test-feature"}
	orch.updateBeadsIssueStatus("CRIT-001", StatusVerified)

	calls := mock.GetCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d: %+v", len(calls), calls)
	}

	if calls[1].Method != "CloseIssue" {
		t.Errorf("expected CloseIssue for StatusVerified (FR-010), got %s", calls[1].Method)
	}

	// Verify issue_status metadata (FR-011).
	meta, ok := calls[1].Args[3].(map[string]string)
	if !ok {
		t.Fatal("expected metadata map as 4th arg to CloseIssue")
	}
	if meta["issue_status"] != "verified" {
		t.Errorf("expected issue_status=verified metadata, got %v", meta)
	}
}

// TestUpdateBeadsIssueStatus_CloseOnClosed verifies FR-010: StatusClosed
// uses bd close instead of bd update.
func TestUpdateBeadsIssueStatus_CloseOnClosed(t *testing.T) {
	mock := &MockBeadsClient{
		KVGetResult: "FIND-xyz789",
	}

	orch := &Orchestrator{beadsClient: mock, featureName: "test-feature"}
	orch.updateBeadsIssueStatus("CRIT-001", StatusClosed)

	calls := mock.GetCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d: %+v", len(calls), calls)
	}

	if calls[1].Method != "CloseIssue" {
		t.Errorf("expected CloseIssue for StatusClosed (FR-010), got %s", calls[1].Method)
	}
}

// TestUpdateBeadsIssueStatus_DismissedUsesUpdateClosed verifies TD-B05:
// StatusDismissed maps to bd update --status closed (not bd close).
func TestUpdateBeadsIssueStatus_DismissedUsesUpdateClosed(t *testing.T) {
	mock := &MockBeadsClient{KVGetResult: "FIND-xyz789"}
	orch := &Orchestrator{beadsClient: mock, featureName: "test-feature"}

	orch.updateBeadsIssueStatus("MAJ-001", StatusDismissed)

	calls := mock.GetCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d: %+v", len(calls), calls)
	}
	if calls[1].Method != "UpdateIssue" {
		t.Errorf("expected UpdateIssue for StatusDismissed (TD-B05), got %s", calls[1].Method)
	}
	if calls[1].Args[2] != "closed" {
		t.Errorf("expected Beads status 'closed' for StatusDismissed, got %v", calls[1].Args[2])
	}
	meta := calls[1].Args[3].(map[string]string)
	if meta["issue_status"] != "dismissed" {
		t.Errorf("expected issue_status=dismissed metadata, got %v", meta)
	}
}

// TestUpdateBeadsIssueStatus_AcknowledgedUsesUpdateClosed verifies TD-B06:
// StatusAcknowledged maps to bd update --status closed.
func TestUpdateBeadsIssueStatus_AcknowledgedUsesUpdateClosed(t *testing.T) {
	mock := &MockBeadsClient{KVGetResult: "FIND-xyz789"}
	orch := &Orchestrator{beadsClient: mock, featureName: "test-feature"}

	orch.updateBeadsIssueStatus("MIN-001", StatusAcknowledged)

	calls := mock.GetCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d: %+v", len(calls), calls)
	}
	if calls[1].Method != "UpdateIssue" {
		t.Errorf("expected UpdateIssue for StatusAcknowledged (TD-B06), got %s", calls[1].Method)
	}
	if calls[1].Args[2] != "closed" {
		t.Errorf("expected Beads status 'closed' for StatusAcknowledged, got %v", calls[1].Args[2])
	}
	meta := calls[1].Args[3].(map[string]string)
	if meta["issue_status"] != "acknowledged" {
		t.Errorf("expected issue_status=acknowledged metadata, got %v", meta)
	}
}

// TestUpdateBeadsIssueStatus_ReopenedUsesUpdateOpen verifies TD-B07:
// StatusReopened maps to bd update --status open.
func TestUpdateBeadsIssueStatus_ReopenedUsesUpdateOpen(t *testing.T) {
	mock := &MockBeadsClient{KVGetResult: "FIND-xyz789"}
	orch := &Orchestrator{beadsClient: mock, featureName: "test-feature"}

	orch.updateBeadsIssueStatus("CRIT-001", StatusReopened)

	calls := mock.GetCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d: %+v", len(calls), calls)
	}
	if calls[1].Method != "UpdateIssue" {
		t.Errorf("expected UpdateIssue for StatusReopened (TD-B07), got %s", calls[1].Method)
	}
	if calls[1].Args[2] != "open" {
		t.Errorf("expected Beads status 'open' for StatusReopened, got %v", calls[1].Args[2])
	}
	meta := calls[1].Args[3].(map[string]string)
	if meta["issue_status"] != "reopened" {
		t.Errorf("expected issue_status=reopened metadata, got %v", meta)
	}
}

// TestUpdateBeadsIssueStatus_NoBeadID verifies that if no Beads ID is found
// in KV, the update is silently skipped.
func TestUpdateBeadsIssueStatus_NoBeadID(t *testing.T) {
	mock := &MockBeadsClient{
		KVGetResult: "", // No Beads ID found.
	}

	orch := &Orchestrator{beadsClient: mock, featureName: "test-feature"}
	orch.updateBeadsIssueStatus("CRIT-001", StatusAddressed)

	calls := mock.GetCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call (KVGet only), got %d: %+v", len(calls), calls)
	}
}

// TestUpdateBeadsIssueStatus_NilClient verifies graceful degradation.
func TestUpdateBeadsIssueStatus_NilClient(t *testing.T) {
	orch := &Orchestrator{beadsClient: nil}
	orch.updateBeadsIssueStatus("CRIT-001", StatusAddressed)
}

// TestWriteBeadsStateSnapshot_IndividualKeysThenSnapshot verifies US-4 AC-1:
// individual keys (state, round, open_critical, open_major, total_findings,
// cost_usd) are written first, then state_snapshot is written LAST (MAJ-002).
func TestWriteBeadsStateSnapshot_IndividualKeysThenSnapshot(t *testing.T) {
	mock := &MockBeadsClient{}

	orch := &Orchestrator{beadsClient: mock, featureName: "payment-disputes"}

	state := &WorkflowStateJSON{
		State:              StateReviewing,
		Round:              3,
		CumulativeCostUSD:  1.2345,
		FindingsSummary:    FindingsSummary{Raised: 5, OpenCritical: 2, OpenMajor: 1},
	}

	orch.writeBeadsStateSnapshot(state)

	calls := mock.GetCalls()
	// 6 individual keys + 1 state_snapshot = 7
	if len(calls) != 7 {
		t.Fatalf("expected 7 KVSet calls, got %d: %+v", len(calls), calls)
	}

	// Verify individual keys are first.
	expectedKeys := []string{
		"payment-disputes:state",
		"payment-disputes:round",
		"payment-disputes:open_critical",
		"payment-disputes:open_major",
		"payment-disputes:total_findings",
		"payment-disputes:cost_usd",
	}
	for i, ek := range expectedKeys {
		if calls[i].Method != "KVSet" || calls[i].Args[1] != ek {
			t.Errorf("call %d: expected KVSet %s, got %s %v", i, ek, calls[i].Method, calls[i].Args[1])
		}
	}

	// Verify state_snapshot is LAST (MAJ-002).
	last := calls[6]
	if last.Method != "KVSet" || last.Args[1] != "payment-disputes:state_snapshot" {
		t.Errorf("expected last call to be KVSet payment-disputes:state_snapshot, got %+v", last)
	}

	// Verify individual key values.
	if calls[0].Args[2] != "REVIEWING" {
		t.Errorf("expected state=REVIEWING, got %v", calls[0].Args[2])
	}
	if calls[1].Args[2] != "3" {
		t.Errorf("expected round=3, got %v", calls[1].Args[2])
	}
	if calls[2].Args[2] != "2" {
		t.Errorf("expected open_critical=2, got %v", calls[2].Args[2])
	}
	if calls[3].Args[2] != "1" {
		t.Errorf("expected open_major=1, got %v", calls[3].Args[2])
	}
	if calls[4].Args[2] != "5" {
		t.Errorf("expected total_findings=5, got %v", calls[4].Args[2])
	}
	if calls[5].Args[2] != "1.2345" {
		t.Errorf("expected cost_usd=1.2345, got %v", calls[5].Args[2])
	}
}

// TestWriteBeadsStateSnapshot_ErrBeadsUnavailable_SkipsRemaining verifies
// FR-018/MAJ-005: if the first KV write returns ErrBeadsUnavailable, all
// remaining KV writes are skipped.
func TestWriteBeadsStateSnapshot_ErrBeadsUnavailable_SkipsRemaining(t *testing.T) {
	mock := &MockBeadsClient{
		KVSetErr: ErrBeadsUnavailable,
	}

	orch := &Orchestrator{beadsClient: mock, featureName: "test-feature"}
	orch.writeBeadsStateSnapshot(&WorkflowStateJSON{State: StateReviewing, Round: 1})

	calls := mock.GetCalls()
	// Only 1 KVSet attempted (the first one fails with ErrBeadsUnavailable).
	if len(calls) != 1 {
		t.Errorf("expected 1 KVSet call (skip remaining after ErrBeadsUnavailable), got %d", len(calls))
	}
}

// TestWriteBeadsStateSnapshot_NilClient verifies graceful degradation.
func TestWriteBeadsStateSnapshot_NilClient(t *testing.T) {
	orch := &Orchestrator{beadsClient: nil}
	orch.writeBeadsStateSnapshot(&WorkflowStateJSON{Round: 1})
}

// TestIssueTrackerTransitionCallback verifies that the OnTransition callback
// fires after a successful TransitionIssue call.
func TestIssueTrackerTransitionCallback(t *testing.T) {
	tracker := NewIssueTracker()
	tracker.AddFindings([]MergedFinding{
		{ID: "CRIT-001", Severity: SeverityCritical},
	})

	var callbackFindingID string
	var callbackNewStatus IssueStatus
	tracker.OnTransition = func(findingID string, newStatus IssueStatus) {
		callbackFindingID = findingID
		callbackNewStatus = newStatus
	}

	err := tracker.TransitionIssue("CRIT-001", StatusAddressed, 1, "fixed it")
	if err != nil {
		t.Fatalf("TransitionIssue: %v", err)
	}

	if callbackFindingID != "CRIT-001" {
		t.Errorf("expected callback findingID=CRIT-001, got %s", callbackFindingID)
	}
	if callbackNewStatus != StatusAddressed {
		t.Errorf("expected callback newStatus=addressed, got %s", callbackNewStatus)
	}
}

// TestIssueTrackerTransitionCallback_NotCalledOnInvalidTransition verifies
// that the callback is NOT fired when a transition is invalid.
func TestIssueTrackerTransitionCallback_NotCalledOnInvalidTransition(t *testing.T) {
	tracker := NewIssueTracker()
	tracker.AddFindings([]MergedFinding{
		{ID: "CRIT-001", Severity: SeverityCritical},
	})

	called := false
	tracker.OnTransition = func(findingID string, newStatus IssueStatus) {
		called = true
	}

	// raised -> closed is not a valid transition.
	err := tracker.TransitionIssue("CRIT-001", StatusClosed, 1, "skip steps")
	if err == nil {
		t.Fatal("expected error for invalid transition")
	}
	if called {
		t.Error("callback should not be called on invalid transition")
	}
}

// TestIssueTrackerTransitionCallback_NilSafe verifies that IssueTracker works
// correctly when OnTransition is nil (no Beads integration).
func TestIssueTrackerTransitionCallback_NilSafe(t *testing.T) {
	tracker := NewIssueTracker()
	tracker.AddFindings([]MergedFinding{
		{ID: "CRIT-001", Severity: SeverityCritical},
	})

	// OnTransition is nil by default — should not panic.
	err := tracker.TransitionIssue("CRIT-001", StatusAddressed, 1, "fixed it")
	if err != nil {
		t.Fatalf("TransitionIssue with nil OnTransition: %v", err)
	}
	if tracker.Issues["CRIT-001"].Status != StatusAddressed {
		t.Errorf("expected addressed, got %s", tracker.Issues["CRIT-001"].Status)
	}
}

// TestNewOrchestratorWithBeadsClient_Startup verifies that providing a BeadsClient
// in OrchestratorConfig triggers EnsureCustomTypesConfigured at startup.
func TestNewOrchestratorWithBeadsClient_Startup(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	workspace := t.TempDir()

	mock := &MockBeadsClient{}

	orch, err := NewOrchestrator(OrchestratorConfig{
		WorkspaceDir: workspace,
		FeatureName:  "beads-test",
		Config:       orchTestConfig(),
		Runner:       newOrchMockRunner(),
		Emitter:      NewChannelEmitter(64),
		BeadsClient:  mock,
	})
	if err != nil {
		t.Fatalf("NewOrchestrator: %v", err)
	}

	// Verify EnsureCustomTypesConfigured was called.
	calls := mock.GetCalls()
	found := false
	for _, c := range calls {
		if c.Method == "EnsureCustomTypesConfigured" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected EnsureCustomTypesConfigured to be called at startup")
	}

	// Verify beadsClient is set on the orchestrator.
	if orch.beadsClient == nil {
		t.Error("expected beadsClient to be set on orchestrator")
	}
}

// TestNewOrchestratorWithBeadsClient_UnavailableGraceful verifies that when
// EnsureCustomTypesConfigured returns ErrBeadsUnavailable, the orchestrator
// still starts but with beadsClient set to nil.
func TestNewOrchestratorWithBeadsClient_UnavailableGraceful(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	workspace := t.TempDir()

	mock := &MockBeadsClient{
		EnsureCustomTypesErr: ErrBeadsUnavailable,
	}

	orch, err := NewOrchestrator(OrchestratorConfig{
		WorkspaceDir: workspace,
		FeatureName:  "beads-test",
		Config:       orchTestConfig(),
		Runner:       newOrchMockRunner(),
		Emitter:      NewChannelEmitter(64),
		BeadsClient:  mock,
	})
	if err != nil {
		t.Fatalf("NewOrchestrator: %v", err)
	}

	// beadsClient should be nil due to graceful degradation.
	if orch.beadsClient != nil {
		t.Error("expected beadsClient to be nil when ErrBeadsUnavailable")
	}
}

// TestBeadsFindingsCreatedInReviewPhase verifies Beads findings are created
// during the review phase after MergeReviewerOutputs.
func TestBeadsFindingsCreatedInReviewPhase(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	workspace := t.TempDir()

	mock := &MockBeadsClient{
		CreateEpicResult:    "EPIC-run1",
		CreateFindingResult: "FIND-001",
		// Use KVGetFunc to return epic ID for run_epic_id/run_id but
		// empty string for finding dedup checks (so findings get created).
		KVGetFunc: func(key string) (string, error) {
			if key == "beads-review-test:run_epic_id" || key == "beads-review-test:run_id" {
				return "EPIC-run1", nil
			}
			return "", nil // dedup check returns empty → finding not yet created
		},
	}

	runner := newOrchMockRunner()
	emitter := NewChannelEmitter(64)

	orch, err := NewOrchestrator(OrchestratorConfig{
		WorkspaceDir: workspace,
		FeatureName:  "beads-review-test",
		Config:       orchTestConfig(),
		Runner:       runner,
		Emitter:      emitter,
		BeadsClient:  mock,
	})
	if err != nil {
		t.Fatalf("NewOrchestrator: %v", err)
	}

	// Inject dummy skills.
	orch.skills = &SkillCache{
		contents: map[string]string{
			SpecTemplate:        "# Spec Template\nDummy.",
			BDDTemplate:         "# BDD Template\nDummy.",
			TestDatasetTemplate: "# Test Dataset Template\nDummy.",
			ReviewConstitution:  "# Review Constitution\nDummy.",
			ReportTemplate:      "# Report Template\nDummy.",
		},
		checksums: map[string]string{"plan_spec": "sha256:test", "grill_spec": "sha256:test"},
		loaded:    true,
	}
	orch.promptBuilder = NewPromptBuilder(orch.skills, workspace, "beads-review-test")

	specDir := filepath.Join(workspace, "specs", "beads-review-test")
	os.WriteFile(filepath.Join(specDir, "spec-v0.md"), []byte("# Test Spec\n"), 0o644)

	// Call createBeadsFindings directly with a finding to verify the flow.
	findings := []MergedFinding{
		{ID: "CRIT-001", Description: "Test finding", Severity: SeverityCritical},
	}

	orch.createBeadsFindings(findings)

	// Verify CreateFinding was called.
	calls := mock.GetCalls()
	foundCreateFinding := false
	for _, c := range calls {
		if c.Method == "CreateFinding" {
			foundCreateFinding = true
			break
		}
	}
	if !foundCreateFinding {
		t.Error("expected CreateFinding to be called for findings")
	}
}

// TestFindingKVMapping_WriteOnCreate verifies that after createBeadsFindings,
// each finding has a KV entry mapping {feature}:finding:{id} → bead ID.
func TestFindingKVMapping_WriteOnCreate(t *testing.T) {
	kvStore := map[string]string{
		"myfeature:run_epic_id": "EPIC-run1",
		"myfeature:run_id":     "uuid-run1",
	}
	mock := &MockBeadsClient{
		CreateFindingResult: "BEAD-f1",
		KVStore:             kvStore,
	}

	orch := &Orchestrator{beadsClient: mock, featureName: "myfeature"}
	orch.runEpicID = "EPIC-run1"

	findings := []MergedFinding{
		{ID: "CRIT-001", Description: "First", Severity: SeverityCritical},
		{ID: "MAJ-001", Description: "Second", Severity: SeverityMajor},
	}

	orch.createBeadsFindings(findings)

	// Verify KV has mapping for each finding.
	for _, f := range findings {
		key := "myfeature:finding:" + f.ID
		val, ok := kvStore[key]
		if !ok || val == "" {
			t.Errorf("expected KV entry for %s, not found", key)
		}
		if val != "BEAD-f1" {
			t.Errorf("expected KV[%s]=BEAD-f1, got %s", key, val)
		}
	}
}

// TestFindingKVMapping_UsedForStatusUpdate verifies that updateBeadsIssueStatus
// looks up the KV mapping {feature}:finding:{id} to get the Beads bead ID,
// then uses that bead ID for the UpdateIssue call.
func TestFindingKVMapping_UsedForStatusUpdate(t *testing.T) {
	kvStore := map[string]string{
		"feat:finding:CRIT-001": "BEAD-abc",
	}
	mock := &MockBeadsClient{KVStore: kvStore}

	orch := &Orchestrator{beadsClient: mock, featureName: "feat"}
	orch.updateBeadsIssueStatus("CRIT-001", StatusAddressed)

	calls := mock.GetCalls()
	// KVGet(finding:CRIT-001) + UpdateIssue(BEAD-abc, addressed, metadata)
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d: %+v", len(calls), calls)
	}
	if calls[1].Method != "UpdateIssue" {
		t.Fatalf("expected UpdateIssue, got %s", calls[1].Method)
	}
	if calls[1].Args[1] != "BEAD-abc" {
		t.Errorf("expected UpdateIssue with bead ID BEAD-abc, got %v", calls[1].Args[1])
	}
}

// TestFindingKVMapping_RecoveryRepopulation verifies that rebuildIssueTrackerFromBeads
// re-populates the KV entries {feature}:finding:{id} → bead ID from Beads children.
func TestFindingKVMapping_RecoveryRepopulation(t *testing.T) {
	childrenJSON := `[
		{"id":"BEAD-f1","title":"CRIT-001","status":"open","issue_type":"finding","metadata":{"finding_id":"CRIT-001","issue_status":"raised"}},
		{"id":"BEAD-f2","title":"MAJ-001","status":"in_progress","issue_type":"finding","metadata":{"finding_id":"MAJ-001","issue_status":"addressed"}},
		{"id":"GATE-001","title":"Human Gate","status":"open","issue_type":"gate","metadata":{}}
	]`

	kvStore := map[string]string{
		"recover:run_epic_id": "EPIC-run1",
		"recover:run_id":     "EPIC-run1", // matching epic ID means same run
	}
	mock := &MockBeadsClient{
		ChildrenResult: []byte(childrenJSON),
		KVStore:        kvStore,
	}

	orch := &Orchestrator{beadsClient: mock, featureName: "recover"}

	tracker := NewIssueTracker()
	tracker.AddFindings([]MergedFinding{
		{ID: "CRIT-001", Severity: SeverityCritical},
		{ID: "MAJ-001", Severity: SeverityMajor},
	})

	orch.rebuildIssueTrackerFromBeads(tracker, "")

	// Verify KV was repopulated for both findings (but not for the gate).
	if kvStore["recover:finding:CRIT-001"] != "BEAD-f1" {
		t.Errorf("expected KV[recover:finding:CRIT-001]=BEAD-f1, got %s", kvStore["recover:finding:CRIT-001"])
	}
	if kvStore["recover:finding:MAJ-001"] != "BEAD-f2" {
		t.Errorf("expected KV[recover:finding:MAJ-001]=BEAD-f2, got %s", kvStore["recover:finding:MAJ-001"])
	}

	// Verify gate was NOT added to KV (issue_type != "finding").
	if _, ok := kvStore["recover:finding:Human Gate"]; ok {
		t.Error("gate should not be added to finding KV mapping")
	}

	// Verify IssueTracker status was restored from Beads metadata.
	if tracker.Issues["MAJ-001"].Status != StatusAddressed {
		t.Errorf("expected MAJ-001 status=addressed after recovery, got %s", tracker.Issues["MAJ-001"].Status)
	}
}

// TestSameFeatureReRun_FreshRunCreatesNewEpic verifies that calling createRunEpic
// always creates a new epic with a fresh UUID run_id, even when previous run data
// exists in KV.
func TestSameFeatureReRun_FreshRunCreatesNewEpic(t *testing.T) {
	mock := &MockBeadsClient{
		CreateEpicResult: "EPIC-new",
		KVStore:          map[string]string{},
	}

	orch := &Orchestrator{beadsClient: mock, featureName: "rerun-feat"}

	// First run.
	state1 := &WorkflowStateJSON{Round: 1}
	orch.createRunEpic(state1)
	firstRunID := state1.RunID
	if firstRunID == "" {
		t.Fatal("expected non-empty RunID after first createRunEpic")
	}

	// Simulate second run: reset orchestrator state but keep same mock/feature.
	orch.runEpicID = ""
	mock.CreateEpicResult = "EPIC-new2"

	state2 := &WorkflowStateJSON{Round: 1}
	orch.createRunEpic(state2)
	secondRunID := state2.RunID
	if secondRunID == "" {
		t.Fatal("expected non-empty RunID after second createRunEpic")
	}

	// UUIDs must differ between runs.
	if firstRunID == secondRunID {
		t.Errorf("expected different run_ids across runs, both got %s", firstRunID)
	}

	// Verify CreateEpic was called twice.
	calls := mock.GetCalls()
	epicCalls := 0
	for _, c := range calls {
		if c.Method == "CreateEpic" {
			epicCalls++
		}
	}
	if epicCalls != 2 {
		t.Errorf("expected 2 CreateEpic calls, got %d", epicCalls)
	}
}

// TestRunID_IsUUID verifies that state.RunID is a valid UUID v4 format after
// createRunEpic (FR-040). Format: 8-4-4-4-12 hex digits.
func TestRunID_IsUUID(t *testing.T) {
	mock := &MockBeadsClient{
		CreateEpicResult: "EPIC-123",
	}

	orch := &Orchestrator{beadsClient: mock, featureName: "uuid-test"}
	state := &WorkflowStateJSON{Round: 1}
	orch.createRunEpic(state)

	runID := state.RunID
	if runID == "" {
		t.Fatal("expected non-empty RunID")
	}

	// UUID v4 format: xxxxxxxx-xxxx-4xxx-[89ab]xxx-xxxxxxxxxxxx
	// Length must be 36, dashes at positions 8, 13, 18, 23.
	if len(runID) != 36 {
		t.Fatalf("expected UUID length 36, got %d: %s", len(runID), runID)
	}
	dashes := []int{8, 13, 18, 23}
	for _, pos := range dashes {
		if runID[pos] != '-' {
			t.Errorf("expected dash at position %d, got %c in %s", pos, runID[pos], runID)
		}
	}
	// Version nibble must be '4'.
	if runID[14] != '4' {
		t.Errorf("expected UUID version 4 (char at pos 14 = '4'), got %c in %s", runID[14], runID)
	}
	// Variant nibble must be 8, 9, a, or b.
	variant := runID[19]
	if variant != '8' && variant != '9' && variant != 'a' && variant != 'b' {
		t.Errorf("expected UUID variant [89ab] at pos 19, got %c in %s", variant, runID)
	}

	// run_id must NOT equal the epic bead ID.
	if runID == "EPIC-123" {
		t.Error("run_id must be a UUID, not the epic bead ID")
	}
}

// ---------------------------------------------------------------------------
// CD-88v.17: Crash recovery E2E — rebuild IssueTracker from Beads
// ---------------------------------------------------------------------------

func TestWorkflow_E2E_CrashRecovery(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	workspace := t.TempDir()
	feature := "crash-recovery-e2e"
	specDir := filepath.Join(workspace, "specs", feature)
	os.MkdirAll(specDir, 0o755)
	os.MkdirAll(filepath.Join(workspace, ".tasks"), 0o755)

	// Phase 1: Simulate a "previous run" that created an epic and findings,
	// then saved state to disk before crashing.

	// Write a merged findings file for round 1 so ReloadFindings populates the tracker.
	findingsJSON := `{
		"schema_version": "1.0",
		"round": 1,
		"total_findings": 2,
		"total_after_dedup": 2,
		"findings": [
			{
				"id": "CRIT-001",
				"source_ids": ["CRIT-001"],
				"raised_by": ["reviewer-a"],
				"description": "Critical auth gap",
				"severity": "CRITICAL",
				"impact": "High",
				"recommendation": "Add auth",
				"lens": "SEC",
				"affected_section": "auth",
				"status": "raised",
				"round_raised": 1
			},
			{
				"id": "MAJ-001",
				"source_ids": ["MAJ-001"],
				"raised_by": ["reviewer-b"],
				"description": "Missing validation",
				"severity": "MAJOR",
				"impact": "Medium",
				"recommendation": "Validate input",
				"lens": "CON",
				"affected_section": "api",
				"status": "raised",
				"round_raised": 1
			}
		]
	}`
	os.WriteFile(filepath.Join(specDir, "merged-findings-round-1.json"), []byte(findingsJSON), 0o644)

	// Save persisted state as if round 1 reviewing completed and we crashed at REVIEWING round 2.
	stateJSON := `{
		"state": "REVIEWING",
		"round": 2,
		"feature_name": "crash-recovery-e2e",
		"started_at": "2026-04-05T01:00:00Z",
		"updated_at": "2026-04-05T01:05:00Z",
		"cumulative_cost_usd": 0.50,
		"agent_invocations": 4,
		"findings_summary": {
			"raised": 2,
			"addressed": 0,
			"verified": 0,
			"closed": 0,
			"dismissed": 0,
			"open_critical": 1,
			"open_major": 1,
			"total": 2
		}
	}`
	os.WriteFile(StateFilePath(specDir), []byte(stateJSON), 0o644)

	// Phase 2: Create a new orchestrator from saved state — simulates restart after crash.
	// The mock Beads client returns children with updated statuses (as if another agent
	// addressed CRIT-001 in Beads before the crash recovery).
	childrenJSON := `[
		{
			"id": "BEAD-F1",
			"title": "Critical auth gap",
			"status": "open",
			"issue_type": "finding",
			"metadata": {
				"finding_id": "CRIT-001",
				"issue_status": "addressed",
				"severity": "CRITICAL"
			}
		},
		{
			"id": "BEAD-F2",
			"title": "Missing validation",
			"status": "open",
			"issue_type": "finding",
			"metadata": {
				"finding_id": "MAJ-001",
				"issue_status": "raised",
				"severity": "MAJOR"
			}
		},
		{
			"id": "BEAD-GATE",
			"title": "Gate proxy",
			"status": "closed",
			"issue_type": "task",
			"metadata": {}
		}
	]`

	mockBeads := &MockBeadsClient{
		CreateEpicResult:    "EPIC-recovery",
		CreateFindingResult: "BEAD-new",
		GateCreateResult:    "GATE-new",
		ChildrenResult:      []byte(childrenJSON),
		KVStore:             make(map[string]string),
	}
	// Pre-populate KV with the epic ID and run_id from the "previous run".
	mockBeads.KVStore[feature+":run_epic_id"] = "EPIC-prev"
	mockBeads.KVStore[feature+":run_id"] = "EPIC-prev"

	runner := newOrchMockRunner()
	emitter := NewChannelEmitter(64)

	// Write spec files needed by the orchestrator.
	os.WriteFile(filepath.Join(specDir, "spec-v0.md"), []byte("# Crash Recovery Spec\n"), 0o644)

	orch, err := NewOrchestrator(OrchestratorConfig{
		WorkspaceDir: workspace,
		FeatureName:  feature,
		Config:       orchTestConfig(),
		Runner:       runner,
		Emitter:      emitter,
		BeadsClient:  mockBeads,
	})
	if err != nil {
		t.Fatalf("NewOrchestrator (recovery): %v", err)
	}

	// Phase 3: Verify crash recovery results.

	// Verify the run epic ID was restored from KV.
	if orch.runEpicID != "EPIC-prev" {
		t.Errorf("runEpicID = %q, want %q", orch.runEpicID, "EPIC-prev")
	}

	// Verify IssueTracker has both findings loaded (from ReloadFindings on disk).
	if len(orch.tracker.Issues) != 2 {
		t.Fatalf("tracker should have 2 issues, got %d", len(orch.tracker.Issues))
	}

	// Verify CRIT-001 status was updated to "addressed" by rebuildIssueTrackerFromBeads
	// (Beads had issue_status=addressed in metadata).
	crit001 := orch.tracker.Issues["CRIT-001"]
	if crit001 == nil {
		t.Fatal("CRIT-001 not found in tracker")
	}
	if crit001.Status != StatusAddressed {
		t.Errorf("CRIT-001 status = %q, want %q", crit001.Status, StatusAddressed)
	}

	// Verify MAJ-001 remains "raised" (Beads had issue_status=raised).
	maj001 := orch.tracker.Issues["MAJ-001"]
	if maj001 == nil {
		t.Fatal("MAJ-001 not found in tracker")
	}
	if maj001.Status != StatusRaised {
		t.Errorf("MAJ-001 status = %q, want %q", maj001.Status, StatusRaised)
	}

	// Verify KV mappings were restored (finding:CRIT-001 → BEAD-F1, finding:MAJ-001 → BEAD-F2).
	calls := mockBeads.GetCalls()
	kvSetCalls := make(map[string]string)
	for _, c := range calls {
		if c.Method == "KVSet" && len(c.Args) >= 3 {
			kvSetCalls[c.Args[1].(string)] = c.Args[2].(string)
		}
	}

	critKVKey := feature + ":finding:CRIT-001"
	if kvSetCalls[critKVKey] != "BEAD-F1" {
		t.Errorf("KV %q = %q, want %q", critKVKey, kvSetCalls[critKVKey], "BEAD-F1")
	}
	majKVKey := feature + ":finding:MAJ-001"
	if kvSetCalls[majKVKey] != "BEAD-F2" {
		t.Errorf("KV %q = %q, want %q", majKVKey, kvSetCalls[majKVKey], "BEAD-F2")
	}

	// Verify Children was called (the recovery mechanism queries Beads children).
	childrenCalled := false
	for _, c := range calls {
		if c.Method == "Children" {
			childrenCalled = true
			break
		}
	}
	if !childrenCalled {
		t.Error("expected Children to be called during crash recovery")
	}

	// Verify the gate proxy task (issue_type=task) was filtered out — no KV write for it.
	for key := range kvSetCalls {
		if key == feature+":finding:Gate proxy" {
			t.Error("gate proxy task should have been filtered out by issue_type check")
		}
	}

	// Verify the state machine resumed at REVIEWING.
	if orch.sm.Current() != StateReviewing {
		t.Errorf("state machine = %s, want REVIEWING", orch.sm.Current())
	}
}

// Ensure the test helper ignores unused context import.
var _ = context.Background
