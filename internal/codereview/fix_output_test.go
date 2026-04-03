package codereview

import (
	"encoding/json"
	"fmt"
	"testing"
)

// ---------------------------------------------------------------------------
// ParseFixOutput tests
// ---------------------------------------------------------------------------

func TestParseFixOutput_ValidAllFixed(t *testing.T) {
	output := FixOutput{
		Round: 1,
		FixesApplied: []FixAction{
			{FindingID: "CRIT-001", Status: FixStatusFixed, FilesModified: []string{"handler.go"}, Description: "Fixed injection"},
			{FindingID: "MAJ-001", Status: FixStatusFixed, FilesModified: []string{"auth.go"}, Description: "Fixed auth"},
		},
		TestResults: &TestResults{Total: 10, Passed: 10, Failed: 0},
		GitDiffStat: " 2 files changed, 15 insertions(+), 7 deletions(-)",
	}
	data, _ := json.Marshal(output)

	result := ParseFixOutput(data)
	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	if result.Output == nil {
		t.Fatal("expected non-nil output")
	}
	if result.Output.Round != 1 {
		t.Errorf("round = %d, want 1", result.Output.Round)
	}
	if len(result.Output.FixesApplied) != 2 {
		t.Errorf("fixes_applied length = %d, want 2", len(result.Output.FixesApplied))
	}
	if len(result.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", result.Warnings)
	}
}

func TestParseFixOutput_ValidMixed(t *testing.T) {
	output := FixOutput{
		Round: 1,
		FixesApplied: []FixAction{
			{FindingID: "CRIT-001", Status: FixStatusFixed, Description: "Fixed"},
			{FindingID: "MAJ-001", Status: FixStatusDeferred, Description: "Deferred"},
			{FindingID: "MAJ-002", Status: FixStatusFailed, Description: "Failed"},
		},
	}
	data, _ := json.Marshal(output)

	result := ParseFixOutput(data)
	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	if len(result.Output.FixesApplied) != 3 {
		t.Errorf("fixes_applied length = %d, want 3", len(result.Output.FixesApplied))
	}
}

func TestParseFixOutput_NilTestResults(t *testing.T) {
	output := FixOutput{
		Round: 1,
		FixesApplied: []FixAction{
			{FindingID: "CRIT-001", Status: FixStatusFixed, Description: "Fixed"},
		},
		TestResults: nil,
	}
	data, _ := json.Marshal(output)

	result := ParseFixOutput(data)
	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	if result.Output.TestResults != nil {
		t.Error("expected nil TestResults")
	}
}

func TestParseFixOutput_InvalidJSON(t *testing.T) {
	result := ParseFixOutput([]byte("not json"))
	if result.Err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if result.Output != nil {
		t.Error("expected nil output on parse error")
	}
}

func TestParseFixOutput_EmptyJSON(t *testing.T) {
	result := ParseFixOutput([]byte("{}"))
	if result.Err == nil {
		t.Fatal("expected error for empty JSON (missing required fields)")
	}
}

func TestParseFixOutput_MissingRound(t *testing.T) {
	data := []byte(`{"fixes_applied": []}`)
	result := ParseFixOutput(data)
	if result.Err == nil {
		t.Fatal("expected error for round=0")
	}
}

func TestParseFixOutput_MissingFixesApplied(t *testing.T) {
	data := []byte(`{"round": 1}`)
	result := ParseFixOutput(data)
	if result.Err == nil {
		t.Fatal("expected error for missing fixes_applied")
	}
}

func TestParseFixOutput_InvalidFixStatus(t *testing.T) {
	output := FixOutput{
		Round: 1,
		FixesApplied: []FixAction{
			{FindingID: "CRIT-001", Status: "invalid_status", Description: "Bad"},
		},
	}
	data, _ := json.Marshal(output)

	result := ParseFixOutput(data)
	if result.Err == nil {
		t.Fatal("expected error for invalid fix status")
	}
}

func TestParseFixOutput_EmptyFindingID(t *testing.T) {
	output := FixOutput{
		Round: 1,
		FixesApplied: []FixAction{
			{FindingID: "", Status: FixStatusFixed, Description: "No ID"},
		},
	}
	data, _ := json.Marshal(output)

	result := ParseFixOutput(data)
	if result.Err == nil {
		t.Fatal("expected error for empty finding_id")
	}
}

func TestParseFixOutput_LargeFilesModifiedWarning(t *testing.T) {
	files := make([]string, 1001)
	for i := range files {
		files[i] = "file.go"
	}
	output := FixOutput{
		Round: 1,
		FixesApplied: []FixAction{
			{FindingID: "CRIT-001", Status: FixStatusFixed, FilesModified: files, Description: "Lots of files"},
		},
	}
	data, _ := json.Marshal(output)

	result := ParseFixOutput(data)
	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	if len(result.Warnings) == 0 {
		t.Error("expected warning for large files_modified")
	}
}

func TestParseFixOutput_EmptyFixesAppliedArray(t *testing.T) {
	output := FixOutput{
		Round:        1,
		FixesApplied: []FixAction{},
	}
	data, _ := json.Marshal(output)

	result := ParseFixOutput(data)
	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	if len(result.Output.FixesApplied) != 0 {
		t.Error("expected empty fixes_applied")
	}
}

// ---------------------------------------------------------------------------
// RouteAfterFix tests
// ---------------------------------------------------------------------------

func TestRouteAfterFix_AllFixed(t *testing.T) {
	output := &FixOutput{
		Round: 1,
		FixesApplied: []FixAction{
			{FindingID: "CRIT-001", Status: FixStatusFixed},
			{FindingID: "MAJ-001", Status: FixStatusFixed},
		},
		TestResults: &TestResults{Total: 10, Passed: 10, Failed: 0},
	}
	critMajor := map[string]bool{"CRIT-001": true, "MAJ-001": true}

	decision := RouteAfterFix(output, critMajor)
	if decision.NextState != CRReviewing {
		t.Errorf("next state = %s, want CR_REVIEWING", decision.NextState)
	}
}

func TestRouteAfterFix_MixedFixedDeferred(t *testing.T) {
	output := &FixOutput{
		Round: 1,
		FixesApplied: []FixAction{
			{FindingID: "CRIT-001", Status: FixStatusFixed},
			{FindingID: "MAJ-001", Status: FixStatusDeferred},
		},
	}
	critMajor := map[string]bool{"CRIT-001": true, "MAJ-001": true}

	decision := RouteAfterFix(output, critMajor)
	if decision.NextState != CRHumanGateFixes {
		t.Errorf("next state = %s, want CR_HUMAN_GATE_FIXES", decision.NextState)
	}
}

func TestRouteAfterFix_AllDeferred(t *testing.T) {
	output := &FixOutput{
		Round: 1,
		FixesApplied: []FixAction{
			{FindingID: "CRIT-001", Status: FixStatusDeferred},
			{FindingID: "MAJ-001", Status: FixStatusDeferred},
		},
	}
	critMajor := map[string]bool{"CRIT-001": true, "MAJ-001": true}

	decision := RouteAfterFix(output, critMajor)
	if decision.NextState != CRHumanGateFixes {
		t.Errorf("next state = %s, want CR_HUMAN_GATE_FIXES", decision.NextState)
	}
}

func TestRouteAfterFix_AllFailed(t *testing.T) {
	output := &FixOutput{
		Round: 1,
		FixesApplied: []FixAction{
			{FindingID: "CRIT-001", Status: FixStatusFailed},
		},
	}
	critMajor := map[string]bool{"CRIT-001": true}

	decision := RouteAfterFix(output, critMajor)
	if decision.NextState != CRHumanGateFixes {
		t.Errorf("next state = %s, want CR_HUMAN_GATE_FIXES", decision.NextState)
	}
}

func TestRouteAfterFix_TestFailures(t *testing.T) {
	output := &FixOutput{
		Round: 1,
		FixesApplied: []FixAction{
			{FindingID: "CRIT-001", Status: FixStatusFixed},
		},
		TestResults: &TestResults{Total: 10, Passed: 8, Failed: 2, Failures: []string{"TestA", "TestB"}},
	}
	critMajor := map[string]bool{"CRIT-001": true}

	decision := RouteAfterFix(output, critMajor)
	if decision.NextState != CRHumanGateFixes {
		t.Errorf("next state = %s, want CR_HUMAN_GATE_FIXES (test failures)", decision.NextState)
	}
}

func TestRouteAfterFix_MissingFinding(t *testing.T) {
	// Fix output doesn't mention MAJ-001 at all — treat as failed.
	output := &FixOutput{
		Round: 1,
		FixesApplied: []FixAction{
			{FindingID: "CRIT-001", Status: FixStatusFixed},
		},
	}
	critMajor := map[string]bool{"CRIT-001": true, "MAJ-001": true}

	decision := RouteAfterFix(output, critMajor)
	if decision.NextState != CRHumanGateFixes {
		t.Errorf("next state = %s, want CR_HUMAN_GATE_FIXES (missing finding)", decision.NextState)
	}
}

func TestRouteAfterFix_NoCriticalMajor(t *testing.T) {
	// Only MINOR findings fixed — no CRIT/MAJ to check.
	output := &FixOutput{
		Round: 1,
		FixesApplied: []FixAction{
			{FindingID: "MIN-001", Status: FixStatusFixed},
		},
	}
	critMajor := map[string]bool{} // empty = no crit/major

	decision := RouteAfterFix(output, critMajor)
	if decision.NextState != CRReviewing {
		t.Errorf("next state = %s, want CR_REVIEWING (no crit/major to fail)", decision.NextState)
	}
}

func TestRouteAfterFix_NilTestResults(t *testing.T) {
	output := &FixOutput{
		Round: 1,
		FixesApplied: []FixAction{
			{FindingID: "CRIT-001", Status: FixStatusFixed},
		},
		TestResults: nil,
	}
	critMajor := map[string]bool{"CRIT-001": true}

	decision := RouteAfterFix(output, critMajor)
	if decision.NextState != CRReviewing {
		t.Errorf("next state = %s, want CR_REVIEWING (nil test results is OK)", decision.NextState)
	}
}

// ---------------------------------------------------------------------------
// RouteAfterFixParseError tests
// ---------------------------------------------------------------------------

func TestRouteAfterFixParseError(t *testing.T) {
	decision := RouteAfterFixParseError(fmt.Errorf("bad json"))
	if decision.NextState != CRHumanGateFixes {
		t.Errorf("next state = %s, want CR_HUMAN_GATE_FIXES", decision.NextState)
	}
	if len(decision.Warnings) == 0 {
		t.Error("expected warnings for parse error")
	}
	if decision.Reason != "fix output parse error" {
		t.Errorf("reason = %q, want 'fix output parse error'", decision.Reason)
	}
}
