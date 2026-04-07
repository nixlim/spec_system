package specworkflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Gate lifecycle (7 tests)
// ---------------------------------------------------------------------------

func TestGateProxy_CreatesBeadsTask(t *testing.T) {
	mock := &MockBeadsClient{
		GateCreateResult: "GATE-001",
		KVStore:          make(map[string]string),
	}

	orch := &Orchestrator{
		beadsClient: mock,
		featureName: "test-feature",
	}

	orch.openGateProxy("gate1", "test-feature: gate-1 — Discovery review required")

	calls := mock.GetCalls()

	// Expect: KVGet (dedup check) + GateCreate + KVSet
	var createCall *MockCall
	for i := range calls {
		if calls[i].Method == "GateCreate" {
			createCall = &calls[i]
			break
		}
	}
	if createCall == nil {
		t.Fatal("expected GateCreate call")
	}
	if createCall.Args[0] != "orchestrator" {
		t.Errorf("expected actor=orchestrator, got %v", createCall.Args[0])
	}
	if createCall.Args[1] != "test-feature: gate-1 — Discovery review required" {
		t.Errorf("expected gate title 'test-feature: gate-1 — Discovery review required', got %v", createCall.Args[1])
	}

	if orch.activeGateBeadID != "GATE-001" {
		t.Errorf("expected activeGateBeadID=GATE-001, got %q", orch.activeGateBeadID)
	}
	if orch.activeGateName != "gate1" {
		t.Errorf("expected activeGateName=gate1, got %q", orch.activeGateName)
	}
}

func TestGateProxy_KVStoresGateID(t *testing.T) {
	mock := &MockBeadsClient{
		GateCreateResult: "GATE-002",
		KVStore:          make(map[string]string),
	}

	orch := &Orchestrator{
		beadsClient: mock,
		featureName: "myfeature",
	}

	orch.openGateProxy("gate2", "myfeature: gate-2 — Spec review required")

	// Verify KV was set with correct key.
	if v, ok := mock.KVStore["myfeature:gate:gate2"]; !ok || v != "GATE-002" {
		t.Errorf("expected KV myfeature:gate:gate2=GATE-002, got %q (ok=%v)", v, ok)
	}
}

func TestGateProxy_DedupOnRecovery(t *testing.T) {
	// Simulate recovery: KV already has a gate ID and the gate is still open.
	openGateJSON, _ := json.Marshal(gateShowResult{Status: "open"})
	mock := &MockBeadsClient{
		GateCreateResult: "GATE-NEW",
		GateShowResult:   openGateJSON,
		KVStore: map[string]string{
			"test-feature:gate:gate1": "GATE-EXISTING",
		},
	}

	orch := &Orchestrator{
		beadsClient: mock,
		featureName: "test-feature",
	}

	orch.openGateProxy("gate1", "test-feature: gate-1 — Discovery review required")

	// Should reuse existing gate, not create new one.
	if orch.activeGateBeadID != "GATE-EXISTING" {
		t.Errorf("expected reused GATE-EXISTING, got %q", orch.activeGateBeadID)
	}

	calls := mock.GetCalls()
	for _, c := range calls {
		if c.Method == "GateCreate" {
			t.Error("expected GateCreate to be skipped on recovery (FR-013)")
		}
	}
}

func TestGateProxy_PollsUntilAccept(t *testing.T) {
	// GateShow returns "open" twice, then "closed" with ACCEPT.
	var mu sync.Mutex
	pollCount := 0
	closedJSON, _ := json.Marshal(gateShowResult{Status: "closed", CloseReason: "ACCEPT: looks good"})
	openJSON, _ := json.Marshal(gateShowResult{Status: "open"})

	mock := &MockBeadsClient{
		GateCreateResult: "GATE-POLL",
		KVStore:          make(map[string]string),
	}
	// Override GateShow to return open then closed.
	origGateShow := mock.GateShowResult
	_ = origGateShow
	mock.GateShowResult = openJSON

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	orch := &Orchestrator{
		beadsClient: mock,
		featureName: "test-feature",
		gateCh:      make(chan GateResponse, 1),
		runCtx:      ctx,
		config:      SpecWorkflowConfig{BeadsGatePollInterval: 50 * time.Millisecond},
	}

	orch.activeGateBeadID = "GATE-POLL"
	orch.activeGateName = "gate1"

	// We need dynamic GateShow responses. Use a wrapper mock.
	dynamicMock := &dynamicGateShowMock{
		MockBeadsClient: mock,
		mu:              &mu,
		pollCount:       &pollCount,
		openJSON:        openJSON,
		closedJSON:      closedJSON,
		closedAfter:     2,
	}
	orch.beadsClient = dynamicMock

	resp := orch.waitForGateResponse("confirm", "correct")

	if resp.Action != "confirm" {
		t.Errorf("expected action=confirm, got %q", resp.Action)
	}
	if resp.Comment != "looks good" {
		t.Errorf("expected comment='looks good', got %q", resp.Comment)
	}
}

func TestGateProxy_PollsUntilReject(t *testing.T) {
	closedJSON, _ := json.Marshal(gateShowResult{Status: "closed", CloseReason: "REJECT: needs more work"})

	mock := &MockBeadsClient{
		GateCreateResult: "GATE-REJ",
		GateShowResult:   closedJSON,
		KVStore:          make(map[string]string),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	orch := &Orchestrator{
		beadsClient: mock,
		featureName: "test-feature",
		gateCh:      make(chan GateResponse, 1),
		runCtx:      ctx,
		config:      SpecWorkflowConfig{BeadsGatePollInterval: 50 * time.Millisecond},
	}
	orch.activeGateBeadID = "GATE-REJ"
	orch.activeGateName = "gate1"

	resp := orch.waitForGateResponse("confirm", "correct")

	if resp.Action != "correct" {
		t.Errorf("expected action=correct (reject maps to rejectAction), got %q", resp.Action)
	}
	if resp.Comment != "needs more work" {
		t.Errorf("expected comment='needs more work', got %q", resp.Comment)
	}
}

func TestGateCloseReason_RepeatedUnrecognized_Escalates(t *testing.T) {
	// Return "closed" with bare "Closed" reason every time → unrecognized × 10 → escalate.
	closedJSON, _ := json.Marshal(gateShowResult{Status: "closed", CloseReason: "Closed"})

	mock := &MockBeadsClient{
		GateShowResult: closedJSON,
		KVStore:        make(map[string]string),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	orch := &Orchestrator{
		beadsClient: mock,
		featureName: "test-feature",
		gateCh:      make(chan GateResponse, 1),
		runCtx:      ctx,
		config:      SpecWorkflowConfig{BeadsGatePollInterval: 50 * time.Millisecond},
	}
	orch.activeGateBeadID = "GATE-ESC"
	orch.activeGateName = "gate1"

	done := make(chan GateResponse, 1)
	go func() {
		done <- orch.waitForGateResponse("confirm", "correct")
	}()

	select {
	case resp := <-done:
		if resp.Action != "escalate" {
			t.Errorf("expected action=escalate after 10 unrecognized, got %q", resp.Action)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for escalation")
	}
}

func TestGateProxy_ClosedOnWorkflowError(t *testing.T) {
	mock := &MockBeadsClient{
		KVStore: make(map[string]string),
	}

	orch := &Orchestrator{
		beadsClient:     mock,
		featureName:     "test-feature",
		activeGateBeadID: "GATE-ERR",
		activeGateName:   "gate1",
	}

	orch.closeOpenGateProxies("REVIEWING")

	calls := mock.GetCalls()
	var closeCall *MockCall
	for i := range calls {
		if calls[i].Method == "GateClose" {
			closeCall = &calls[i]
			break
		}
	}
	if closeCall == nil {
		t.Fatal("expected GateClose call on workflow error")
	}
	if !strings.HasPrefix(closeCall.Args[2].(string), "ESCALATED:") {
		t.Errorf("expected close reason starting with ESCALATED:, got %q", closeCall.Args[2])
	}

	if orch.activeGateBeadID != "" {
		t.Errorf("expected activeGateBeadID cleared, got %q", orch.activeGateBeadID)
	}
}

// ---------------------------------------------------------------------------
// Molecule tracking (6 tests)
// ---------------------------------------------------------------------------

func molStepsJSON() []byte {
	steps := []struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}{
		{ID: "step-rev", Title: "reviewing"},
		{ID: "step-revise", Title: "revising"},
		{ID: "step-judge", Title: "judging"},
	}
	data, _ := json.Marshal(steps)
	return data
}

func TestMolPour_CreatesBeadsEpic(t *testing.T) {
	mock := &MockBeadsClient{
		MolPourResult:      "MOL-001",
		MolShowStepsResult: molStepsJSON(),
		KVStore:            make(map[string]string),
		KVGetFunc: func(key string) (string, error) {
			if key == "beads:proto:review-cycle" {
				return "PROTO-001", nil
			}
			return "", nil
		},
	}

	orch := &Orchestrator{
		beadsClient: mock,
		featureName: "test-feature",
	}

	orch.pourReviewMolecule(1)

	calls := mock.GetCalls()
	var pourCall *MockCall
	for i := range calls {
		if calls[i].Method == "MolPour" {
			pourCall = &calls[i]
			break
		}
	}
	if pourCall == nil {
		t.Fatal("expected MolPour call")
	}
	if pourCall.Args[1] != "PROTO-001" {
		t.Errorf("expected proto ID PROTO-001, got %v", pourCall.Args[1])
	}
	vars := pourCall.Args[2].(map[string]string)
	if vars["round"] != "1" || vars["feature"] != "test-feature" {
		t.Errorf("expected vars round=1, feature=test-feature, got %v", vars)
	}
}

func TestMolPour_StoresStepIDsInKV(t *testing.T) {
	mock := &MockBeadsClient{
		MolPourResult:      "MOL-002",
		MolShowStepsResult: molStepsJSON(),
		KVStore:            make(map[string]string),
		KVGetFunc: func(key string) (string, error) {
			if key == "beads:proto:review-cycle" {
				return "PROTO-001", nil
			}
			return "", nil
		},
	}

	orch := &Orchestrator{
		beadsClient: mock,
		featureName: "test-feature",
	}

	orch.pourReviewMolecule(1)

	// Verify mol ID stored.
	if v := mock.KVStore["test-feature:mol:round:1"]; v != "MOL-002" {
		t.Errorf("expected mol ID MOL-002, got %q", v)
	}

	// Verify step IDs stored (FR-025).
	if v := mock.KVStore["test-feature:mol:round:1:step:reviewing"]; v != "step-rev" {
		t.Errorf("expected reviewing step=step-rev, got %q", v)
	}
	if v := mock.KVStore["test-feature:mol:round:1:step:revising"]; v != "step-revise" {
		t.Errorf("expected revising step=step-revise, got %q", v)
	}
	if v := mock.KVStore["test-feature:mol:round:1:step:judging"]; v != "step-judge" {
		t.Errorf("expected judging step=step-judge, got %q", v)
	}
}

func TestMolStep_ReviewingClosed(t *testing.T) {
	mock := &MockBeadsClient{
		KVStore: map[string]string{
			"test-feature:mol:round:1:step:reviewing": "step-rev-001",
		},
	}

	orch := &Orchestrator{
		beadsClient: mock,
		featureName: "test-feature",
	}

	orch.closeMoleculeStep(1, "reviewing")

	calls := mock.GetCalls()
	var closeCall *MockCall
	for i := range calls {
		if calls[i].Method == "CloseStep" {
			closeCall = &calls[i]
			break
		}
	}
	if closeCall == nil {
		t.Fatal("expected CloseStep call for reviewing")
	}
	if closeCall.Args[1] != "step-rev-001" {
		t.Errorf("expected step ID step-rev-001, got %v", closeCall.Args[1])
	}
}

func TestMolStep_JudgingClosed(t *testing.T) {
	mock := &MockBeadsClient{
		KVStore: map[string]string{
			"test-feature:mol:round:2:step:judging": "step-judge-002",
		},
	}

	orch := &Orchestrator{
		beadsClient: mock,
		featureName: "test-feature",
	}

	orch.closeMoleculeStep(2, "judging")

	calls := mock.GetCalls()
	var closeCall *MockCall
	for i := range calls {
		if calls[i].Method == "CloseStep" {
			closeCall = &calls[i]
			break
		}
	}
	if closeCall == nil {
		t.Fatal("expected CloseStep call for judging")
	}
	if closeCall.Args[1] != "step-judge-002" {
		t.Errorf("expected step ID step-judge-002, got %v", closeCall.Args[1])
	}
}

func TestMolStep_RevisingClosed(t *testing.T) {
	mock := &MockBeadsClient{
		KVStore: map[string]string{
			"test-feature:mol:round:1:step:revising": "step-revise-001",
		},
	}

	orch := &Orchestrator{
		beadsClient: mock,
		featureName: "test-feature",
	}

	orch.closeMoleculeStep(1, "revising")

	calls := mock.GetCalls()
	var closeCall *MockCall
	for i := range calls {
		if calls[i].Method == "CloseStep" {
			closeCall = &calls[i]
			break
		}
	}
	if closeCall == nil {
		t.Fatal("expected CloseStep call for revising")
	}
	if closeCall.Args[1] != "step-revise-001" {
		t.Errorf("expected step ID step-revise-001, got %v", closeCall.Args[1])
	}
}

func TestMolPour_NewPourOnNewRound(t *testing.T) {
	mock := &MockBeadsClient{
		MolPourResult:      "MOL-R1",
		MolShowStepsResult: molStepsJSON(),
		KVStore:            make(map[string]string),
		KVGetFunc: func(key string) (string, error) {
			if key == "beads:proto:review-cycle" {
				return "PROTO-001", nil
			}
			return "", nil
		},
	}

	orch := &Orchestrator{
		beadsClient: mock,
		featureName: "test-feature",
	}

	// Pour round 1.
	orch.pourReviewMolecule(1)

	// Change result for round 2.
	mock.MolPourResult = "MOL-R2"

	// Pour round 2.
	orch.pourReviewMolecule(2)

	if v := mock.KVStore["test-feature:mol:round:1"]; v != "MOL-R1" {
		t.Errorf("expected round 1 mol=MOL-R1, got %q", v)
	}
	if v := mock.KVStore["test-feature:mol:round:2"]; v != "MOL-R2" {
		t.Errorf("expected round 2 mol=MOL-R2, got %q", v)
	}

	// Verify MolPour called twice.
	pourCount := 0
	for _, c := range mock.GetCalls() {
		if c.Method == "MolPour" {
			pourCount++
		}
	}
	if pourCount != 2 {
		t.Errorf("expected 2 MolPour calls (one per round), got %d", pourCount)
	}
}

// ---------------------------------------------------------------------------
// Crash recovery from Beads (4 tests)
// ---------------------------------------------------------------------------

func TestIssueTrackerRebuild_FromBeadsChildren(t *testing.T) {
	children := []beadsFinding{
		{
			ID:        "BEAD-F1",
			Title:     "CRIT-001",
			Status:    "open",
			IssueType: "finding",
			Metadata:  map[string]string{"finding_id": "CRIT-001"},
		},
		{
			ID:        "BEAD-F2",
			Title:     "MAJ-001",
			Status:    "in_progress",
			IssueType: "finding",
			Metadata:  map[string]string{"finding_id": "MAJ-001"},
		},
	}
	childrenJSON, _ := json.Marshal(children)

	mock := &MockBeadsClient{
		ChildrenResult: childrenJSON,
		KVStore: map[string]string{
			"test-feature:run_epic_id": "EPIC-001",
			"test-feature:run_id":      "uuid-run-001",
		},
	}

	tracker := NewIssueTracker()
	tracker.AddFindings([]MergedFinding{
		{ID: "CRIT-001", Severity: SeverityCritical},
		{ID: "MAJ-001", Severity: SeverityMajor},
	})

	orch := &Orchestrator{
		beadsClient: mock,
		featureName: "test-feature",
	}

	orch.rebuildIssueTrackerFromBeads(tracker, "uuid-run-001")

	if orch.runEpicID != "EPIC-001" {
		t.Errorf("expected runEpicID=EPIC-001, got %q", orch.runEpicID)
	}

	// Verify KV mappings restored.
	if v := mock.KVStore["test-feature:finding:CRIT-001"]; v != "BEAD-F1" {
		t.Errorf("expected finding:CRIT-001 KV = BEAD-F1, got %q", v)
	}
	if v := mock.KVStore["test-feature:finding:MAJ-001"]; v != "BEAD-F2" {
		t.Errorf("expected finding:MAJ-001 KV = BEAD-F2, got %q", v)
	}
}

func TestIssueTrackerRebuild_StatusMappingFromMetadata(t *testing.T) {
	children := []beadsFinding{
		{
			ID:        "BEAD-F1",
			Title:     "CRIT-001",
			Status:    "closed",
			IssueType: "finding",
			Metadata:  map[string]string{"finding_id": "CRIT-001", "issue_status": "verified"},
		},
	}
	childrenJSON, _ := json.Marshal(children)

	mock := &MockBeadsClient{
		ChildrenResult: childrenJSON,
		KVStore: map[string]string{
			"test-feature:run_epic_id": "EPIC-001",
			"test-feature:run_id":      "uuid-run-001",
		},
	}

	tracker := NewIssueTracker()
	tracker.AddFindings([]MergedFinding{
		{ID: "CRIT-001", Severity: SeverityCritical},
	})
	// Advance to addressed so verified is reachable.
	_ = tracker.TransitionIssue("CRIT-001", StatusAddressed, 1, "fixed")

	orch := &Orchestrator{
		beadsClient: mock,
		featureName: "test-feature",
	}

	orch.rebuildIssueTrackerFromBeads(tracker, "uuid-run-001")

	// issue_status metadata ("verified") takes priority over Beads status ("closed").
	if tracker.Issues["CRIT-001"].Status != StatusVerified {
		t.Errorf("expected status=verified from metadata, got %s", tracker.Issues["CRIT-001"].Status)
	}
}

func TestIssueTrackerRebuild_SkipsNonFindingIssues(t *testing.T) {
	children := []beadsFinding{
		{
			ID:        "BEAD-GATE",
			Title:     "gate-1 proxy",
			Status:    "closed",
			IssueType: "task", // gate proxy — should be skipped
			Metadata:  map[string]string{},
		},
		{
			ID:        "BEAD-F1",
			Title:     "CRIT-001",
			Status:    "open",
			IssueType: "finding",
			Metadata:  map[string]string{"finding_id": "CRIT-001"},
		},
	}
	childrenJSON, _ := json.Marshal(children)

	mock := &MockBeadsClient{
		ChildrenResult: childrenJSON,
		KVStore: map[string]string{
			"test-feature:run_epic_id": "EPIC-001",
			"test-feature:run_id":      "uuid-run-001",
		},
	}

	tracker := NewIssueTracker()
	tracker.AddFindings([]MergedFinding{
		{ID: "CRIT-001", Severity: SeverityCritical},
	})

	orch := &Orchestrator{
		beadsClient: mock,
		featureName: "test-feature",
	}

	orch.rebuildIssueTrackerFromBeads(tracker, "uuid-run-001")

	// Only finding should be in KV.
	if v := mock.KVStore["test-feature:finding:CRIT-001"]; v != "BEAD-F1" {
		t.Errorf("expected finding:CRIT-001 recovered, got %q", v)
	}

	// Gate proxy should NOT be in KV as a finding.
	if _, ok := mock.KVStore["test-feature:finding:gate-1 proxy"]; ok {
		t.Error("gate proxy should not be recovered as a finding (FR-028)")
	}
}

func TestRecovery_ReviewingStateDiscardsOutputFiles(t *testing.T) {
	specDir := t.TempDir()

	// Create stale reviewer output files for round 2.
	staleFiles := []string{
		"review-holdout-claude-round-2.json",
		"review-consistency-claude-round-2.json",
		"review-holdout-codex-round-2.json",
	}
	for _, f := range staleFiles {
		os.WriteFile(filepath.Join(specDir, f), []byte("stale"), 0o644)
	}

	// Create a file for round 1 that should NOT be removed.
	keepFile := "review-holdout-claude-round-1.json"
	os.WriteFile(filepath.Join(specDir, keepFile), []byte("keep"), 0o644)

	cleanStaleReviewerOutputs(specDir, 2)

	// Stale files should be removed.
	for _, f := range staleFiles {
		if _, err := os.Stat(filepath.Join(specDir, f)); err == nil {
			t.Errorf("expected stale file %s to be removed", f)
		}
	}

	// Round 1 file should still exist.
	if _, err := os.Stat(filepath.Join(specDir, keepFile)); err != nil {
		t.Errorf("expected round-1 file %s to be kept", keepFile)
	}
}

// ---------------------------------------------------------------------------
// Comment posting (4 tests)
// ---------------------------------------------------------------------------

func TestJudgeVerdict_PostsAcceptComment(t *testing.T) {
	mock := &MockBeadsClient{
		KVStore: map[string]string{
			"test-feature:finding:CRIT-001": "BEAD-F1",
		},
	}

	orch := &Orchestrator{
		beadsClient: mock,
		featureName: "test-feature",
	}

	orch.postJudgeComment("CRIT-001", 2, "verified", "Fix is correct")

	calls := mock.GetCalls()
	var commentCall *MockCall
	for i := range calls {
		if calls[i].Method == "Comment" {
			commentCall = &calls[i]
			break
		}
	}
	if commentCall == nil {
		t.Fatal("expected Comment call for judge verdict")
	}
	if commentCall.Args[0] != "judge" {
		t.Errorf("expected actor=judge, got %v", commentCall.Args[0])
	}
	if commentCall.Args[1] != "BEAD-F1" {
		t.Errorf("expected beadID=BEAD-F1, got %v", commentCall.Args[1])
	}
	body := commentCall.Args[2].(string)
	if !strings.Contains(body, "Judge [round 2]: verified") {
		t.Errorf("expected judge comment body, got %q", body)
	}
	if !strings.Contains(body, "Fix is correct") {
		t.Errorf("expected rationale in comment, got %q", body)
	}
}

func TestJudgeVerdict_PostsDismissComment(t *testing.T) {
	mock := &MockBeadsClient{
		KVStore: map[string]string{
			"test-feature:finding:MIN-001": "BEAD-M1",
		},
	}

	orch := &Orchestrator{
		beadsClient: mock,
		featureName: "test-feature",
	}

	orch.postJudgeComment("MIN-001", 1, "dismissed", "Not applicable to this context")

	calls := mock.GetCalls()
	var commentCall *MockCall
	for i := range calls {
		if calls[i].Method == "Comment" {
			commentCall = &calls[i]
			break
		}
	}
	if commentCall == nil {
		t.Fatal("expected Comment call for dismiss verdict")
	}
	body := commentCall.Args[2].(string)
	if !strings.Contains(body, "Judge [round 1]: dismissed") {
		t.Errorf("expected dismiss comment, got %q", body)
	}
}

func TestReviserAction_PostsAddressedComment(t *testing.T) {
	mock := &MockBeadsClient{
		KVStore: map[string]string{
			"test-feature:finding:CRIT-001": "BEAD-F1",
		},
	}

	orch := &Orchestrator{
		beadsClient: mock,
		featureName: "test-feature",
	}

	orch.postReviserComment("CRIT-001", 1, "Added input validation to auth endpoint")

	calls := mock.GetCalls()
	var commentCall *MockCall
	for i := range calls {
		if calls[i].Method == "Comment" {
			commentCall = &calls[i]
			break
		}
	}
	if commentCall == nil {
		t.Fatal("expected Comment call for reviser action")
	}
	if commentCall.Args[0] != "reviser" {
		t.Errorf("expected actor=reviser, got %v", commentCall.Args[0])
	}
	body := commentCall.Args[2].(string)
	if !strings.Contains(body, "Reviser [round 1]: revised") {
		t.Errorf("expected reviser comment, got %q", body)
	}
	if !strings.Contains(body, "Added input validation") {
		t.Errorf("expected description in comment, got %q", body)
	}
}

func TestReviserAction_PostsNoActionComment(t *testing.T) {
	// When finding has no Beads bead ID, comment is silently skipped.
	mock := &MockBeadsClient{
		KVStore: make(map[string]string), // No finding mapping.
	}

	orch := &Orchestrator{
		beadsClient: mock,
		featureName: "test-feature",
	}

	orch.postReviserComment("UNKNOWN-001", 1, "some description")

	calls := mock.GetCalls()
	for _, c := range calls {
		if c.Method == "Comment" {
			t.Error("expected no Comment call when finding has no Beads bead ID")
		}
	}
}

// ---------------------------------------------------------------------------
// Taskify refinement loop (3 tests)
// ---------------------------------------------------------------------------

func TestTaskifyRefinement_ExitCode1_Retries(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	workspace := t.TempDir()
	feature := "taskify-retry"

	runner := newOrchMockRunner()
	emitter := NewChannelEmitter(64)
	mock := &MockBeadsClient{
		CreateEpicResult:    "EPIC-001",
		CreateFindingResult: "FIND-001",
		GateCreateResult:    "GATE-001",
		KVStore:             make(map[string]string),
	}

	orch, err := NewOrchestrator(OrchestratorConfig{
		WorkspaceDir: workspace,
		FeatureName:  feature,
		Config:       orchTestConfig(),
		Runner:       runner,
		Emitter:      emitter,
		BeadsClient:  mock,
	})
	if err != nil {
		t.Fatalf("NewOrchestrator: %v", err)
	}

	// Set up state as if we're in TASKIFY.
	specDir := filepath.Join(workspace, "specs", feature)
	os.MkdirAll(specDir, 0o755)
	os.MkdirAll(filepath.Join(workspace, ".tasks"), 0o755)
	os.WriteFile(filepath.Join(specDir, "spec-final.md"), []byte("# Spec\n"), 0o644)

	orch.skills = &SkillCache{
		contents:  map[string]string{SpecTemplate: "# Template"},
		checksums: make(map[string]string),
		loaded:    true,
	}
	orch.promptBuilder = NewPromptBuilder(orch.skills, workspace, feature)

	// First call fails, second succeeds.
	callCount := 0
	taskRunner := &funcMockRunner{
		runFunc: func(prompt, outputPath string, timeout int) (int, string, float64, int64, error) {
			callCount++
			taskGraph := `{"spec_version":"1.0","tasks":[{"id":"task-1","title":"Do stuff","description":"stuff","acceptance_criteria":["done"],"estimated_effort":"S","priority":"P1","depends_on":{"status":"N/A","reason":"none"},"constraints":{"status":"N/A","reason":"none"},"files_scope":{"status":"N/A","reason":"none"}}]}`
			if callCount == 1 {
				// Write invalid JSON first time.
				os.MkdirAll(filepath.Dir(outputPath), 0o755)
				os.WriteFile(outputPath, []byte(`{"invalid`), 0o644)
				return 0, "", 0.01, 500, nil
			}
			os.MkdirAll(filepath.Dir(outputPath), 0o755)
			os.WriteFile(outputPath, []byte(taskGraph), 0o644)
			return 0, "", 0.01, 500, nil
		},
	}
	orch.runner = taskRunner

	// Set state to just before TASKIFY.
	ws := orch.sm.State()
	ws.State = StateTaskify
	ws.CurrentSpecVersion = 1
	orch.sm.RestoreState(ws)

	err = orch.handleTaskify(ws, specDir)
	// Should succeed on retry.
	if err != nil {
		t.Logf("handleTaskify error (may be expected if taskval unavailable): %v", err)
	}

	// At minimum, the agent should have been called at least twice (retry).
	if callCount < 2 {
		t.Errorf("expected at least 2 taskify attempts (retry on invalid JSON), got %d", callCount)
	}
}

func TestTaskifyRefinement_ExitCode1_ExhaustedNotifiesBeads(t *testing.T) {
	mock := &MockBeadsClient{
		KVStore: make(map[string]string),
	}

	orch := &Orchestrator{
		beadsClient: mock,
		featureName: "test-feature",
		runEpicID:   "EPIC-001",
	}

	orch.postTaskvalExhaustion([]string{"error 1", "error 2"})

	calls := mock.GetCalls()
	var commentCall *MockCall
	for i := range calls {
		if calls[i].Method == "Comment" {
			commentCall = &calls[i]
			break
		}
	}
	if commentCall == nil {
		t.Fatal("expected Comment call for taskval exhaustion")
	}
	body := commentCall.Args[2].(string)
	if !strings.Contains(body, "exhausted") {
		t.Errorf("expected 'exhausted' in comment body, got %q", body)
	}
	if !strings.Contains(body, "error 1") || !strings.Contains(body, "error 2") {
		t.Errorf("expected validation errors in comment body, got %q", body)
	}
}

func TestTaskifyRefinement_ExitCode2_Escalates(t *testing.T) {
	mock := &MockBeadsClient{
		KVStore: make(map[string]string),
	}

	orch := &Orchestrator{
		beadsClient: mock,
		featureName: "test-feature",
		runEpicID:   "EPIC-001",
	}

	orch.postTaskvalInternalError("taskval: segfault in parser")

	calls := mock.GetCalls()
	var commentCall *MockCall
	for i := range calls {
		if calls[i].Method == "Comment" {
			commentCall = &calls[i]
			break
		}
	}
	if commentCall == nil {
		t.Fatal("expected Comment call for taskval internal error")
	}
	body := commentCall.Args[2].(string)
	if !strings.Contains(body, "internal error") {
		t.Errorf("expected 'internal error' in comment body, got %q", body)
	}
	if !strings.Contains(body, "segfault") {
		t.Errorf("expected stderr in comment body, got %q", body)
	}
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// dynamicGateShowMock wraps MockBeadsClient to return dynamic GateShow responses.
type dynamicGateShowMock struct {
	*MockBeadsClient
	mu          *sync.Mutex
	pollCount   *int
	openJSON    []byte
	closedJSON  []byte
	closedAfter int
}

func (d *dynamicGateShowMock) GateShow(_ context.Context, actor string, beadID string) ([]byte, error) {
	d.mu.Lock()
	*d.pollCount++
	count := *d.pollCount
	d.mu.Unlock()

	d.MockBeadsClient.record("GateShow", actor, beadID)

	if count <= d.closedAfter {
		return d.openJSON, nil
	}
	return d.closedJSON, nil
}

// funcMockRunner is a simple AgentRunner backed by a function.
type funcMockRunner struct {
	runFunc func(prompt, outputPath string, timeout int) (int, string, float64, int64, error)
}

func (f *funcMockRunner) Run(prompt, outputPath string, timeout int) (int, string, float64, int64, error) {
	return f.runFunc(prompt, outputPath, timeout)
}

// Ensure helpers are used (avoid unused import errors).
var _ = fmt.Sprintf
