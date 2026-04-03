package codereview

import (
	"encoding/json"
	"testing"
)

// ---------------------------------------------------------------------------
// CodeReviewState tests
// ---------------------------------------------------------------------------

func TestCodeReviewState_String(t *testing.T) {
	tests := []struct {
		state CodeReviewState
		want  string
	}{
		{CRInit, "CR_INIT"},
		{CRHumanGateScope, "CR_HUMAN_GATE_SCOPE"},
		{CRReviewing, "CR_REVIEWING"},
		{CRFixing, "CR_FIXING"},
		{CRHumanGateFixes, "CR_HUMAN_GATE_FIXES"},
		{CRComplete, "CR_COMPLETE"},
		{CREscalated, "CR_ESCALATED"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("CodeReviewState(%d).String() = %q, want %q", int(tt.state), got, tt.want)
		}
	}
}

func TestCodeReviewState_StringUnknown(t *testing.T) {
	s := CodeReviewState(999)
	got := s.String()
	if got == "" {
		t.Error("expected non-empty string for unknown CodeReviewState")
	}
}

func TestCodeReviewState_JSONRoundTrip(t *testing.T) {
	states := []CodeReviewState{
		CRInit, CRHumanGateScope, CRReviewing, CRFixing,
		CRHumanGateFixes, CRComplete, CREscalated,
	}
	for _, s := range states {
		data, err := json.Marshal(s)
		if err != nil {
			t.Fatalf("Marshal CodeReviewState %v: %v", s, err)
		}
		var got CodeReviewState
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("Unmarshal CodeReviewState %q: %v", data, err)
		}
		if got != s {
			t.Errorf("round-trip: got %v, want %v", got, s)
		}
	}
}

func TestCodeReviewState_UnmarshalCaseInsensitive(t *testing.T) {
	inputs := []string{`"cr_init"`, `"Cr_Init"`, `"CR_INIT"`, `"cr_reviewing"`, `"CR_REVIEWING"`}
	for _, input := range inputs {
		var s CodeReviewState
		if err := json.Unmarshal([]byte(input), &s); err != nil {
			t.Errorf("Unmarshal %s: %v", input, err)
		}
	}
}

func TestCodeReviewState_UnmarshalInvalid(t *testing.T) {
	var s CodeReviewState
	if err := json.Unmarshal([]byte(`"BOGUS"`), &s); err == nil {
		t.Error("expected error for unknown state, got nil")
	}
	if err := json.Unmarshal([]byte(`123`), &s); err == nil {
		t.Error("expected error for non-string, got nil")
	}
}

func TestParseCodeReviewState(t *testing.T) {
	s, err := ParseCodeReviewState("cr_reviewing")
	if err != nil {
		t.Fatalf("ParseCodeReviewState(\"cr_reviewing\"): %v", err)
	}
	if s != CRReviewing {
		t.Errorf("got %v, want CRReviewing", s)
	}
	_, err = ParseCodeReviewState("nope")
	if err == nil {
		t.Error("expected error for unknown state")
	}
}

// ---------------------------------------------------------------------------
// GrillCodeMode tests
// ---------------------------------------------------------------------------

func TestGrillCodeMode_String(t *testing.T) {
	tests := []struct {
		mode GrillCodeMode
		want string
	}{
		{GrillCodeModeCodeOnly, "code-only"},
		{GrillCodeModeSpecOnly, "spec-only"},
		{GrillCodeModeFullContext, "full-context"},
	}
	for _, tt := range tests {
		if got := tt.mode.String(); got != tt.want {
			t.Errorf("GrillCodeMode(%d).String() = %q, want %q", int(tt.mode), got, tt.want)
		}
	}
}

func TestGrillCodeMode_StringUnknown(t *testing.T) {
	m := GrillCodeMode(999)
	got := m.String()
	if got == "" {
		t.Error("expected non-empty string for unknown GrillCodeMode")
	}
}

func TestGrillCodeMode_JSONRoundTrip(t *testing.T) {
	modes := []GrillCodeMode{GrillCodeModeCodeOnly, GrillCodeModeSpecOnly, GrillCodeModeFullContext}
	for _, m := range modes {
		data, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("Marshal GrillCodeMode %v: %v", m, err)
		}
		var got GrillCodeMode
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("Unmarshal GrillCodeMode %q: %v", data, err)
		}
		if got != m {
			t.Errorf("round-trip: got %v, want %v", got, m)
		}
	}
}

func TestGrillCodeMode_UnmarshalCaseInsensitive(t *testing.T) {
	inputs := []struct {
		input string
		want  GrillCodeMode
	}{
		{`"code-only"`, GrillCodeModeCodeOnly},
		{`"Code-Only"`, GrillCodeModeCodeOnly},
		{`"spec-only"`, GrillCodeModeSpecOnly},
		{`"full-context"`, GrillCodeModeFullContext},
		{`"FULL-CONTEXT"`, GrillCodeModeFullContext},
	}
	for _, tc := range inputs {
		var m GrillCodeMode
		if err := json.Unmarshal([]byte(tc.input), &m); err != nil {
			t.Errorf("Unmarshal %s: %v", tc.input, err)
			continue
		}
		if m != tc.want {
			t.Errorf("Unmarshal %s: got %v, want %v", tc.input, m, tc.want)
		}
	}
}

func TestGrillCodeMode_UnmarshalInvalid(t *testing.T) {
	var m GrillCodeMode
	if err := json.Unmarshal([]byte(`"unknown-mode"`), &m); err == nil {
		t.Error("expected error for unknown mode, got nil")
	}
}

func TestParseGrillCodeMode(t *testing.T) {
	m, err := ParseGrillCodeMode("spec-only")
	if err != nil {
		t.Fatalf("ParseGrillCodeMode(\"spec-only\"): %v", err)
	}
	if m != GrillCodeModeSpecOnly {
		t.Errorf("got %v, want GrillCodeModeSpecOnly", m)
	}
	_, err = ParseGrillCodeMode("nope")
	if err == nil {
		t.Error("expected error for unknown mode")
	}
}

func TestDetermineGrillCodeMode(t *testing.T) {
	tests := []struct {
		specPath     string
		taskListPath string
		want         GrillCodeMode
	}{
		{"", "", GrillCodeModeCodeOnly},
		{"", "/tasks.json", GrillCodeModeCodeOnly},
		{"/spec.md", "", GrillCodeModeSpecOnly},
		{"/spec.md", "/tasks.json", GrillCodeModeFullContext},
	}
	for _, tt := range tests {
		got := DetermineGrillCodeMode(tt.specPath, tt.taskListPath)
		if got != tt.want {
			t.Errorf("DetermineGrillCodeMode(%q, %q) = %v, want %v",
				tt.specPath, tt.taskListPath, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// CodeReviewVerdict tests
// ---------------------------------------------------------------------------

func TestCodeReviewVerdict_String(t *testing.T) {
	tests := []struct {
		v    CodeReviewVerdict
		want string
	}{
		{CodeReviewVerdictPass, "PASS"},
		{CodeReviewVerdictPassWithObservations, "PASS_WITH_OBSERVATIONS"},
		{CodeReviewVerdictRevise, "REVISE"},
	}
	for _, tt := range tests {
		if got := tt.v.String(); got != tt.want {
			t.Errorf("CodeReviewVerdict(%d).String() = %q, want %q", int(tt.v), got, tt.want)
		}
	}
}

func TestCodeReviewVerdict_JSONRoundTrip(t *testing.T) {
	verdicts := []CodeReviewVerdict{
		CodeReviewVerdictPass, CodeReviewVerdictPassWithObservations, CodeReviewVerdictRevise,
	}
	for _, v := range verdicts {
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("Marshal CodeReviewVerdict %v: %v", v, err)
		}
		var got CodeReviewVerdict
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("Unmarshal CodeReviewVerdict %q: %v", data, err)
		}
		if got != v {
			t.Errorf("round-trip: got %v, want %v", got, v)
		}
	}
}

func TestCodeReviewVerdict_UnmarshalCaseInsensitive(t *testing.T) {
	var v CodeReviewVerdict
	if err := json.Unmarshal([]byte(`"pass_with_observations"`), &v); err != nil {
		t.Fatalf("Unmarshal \"pass_with_observations\": %v", err)
	}
	if v != CodeReviewVerdictPassWithObservations {
		t.Errorf("got %v, want CodeReviewVerdictPassWithObservations", v)
	}
}

func TestCodeReviewVerdict_UnmarshalInvalid(t *testing.T) {
	var v CodeReviewVerdict
	if err := json.Unmarshal([]byte(`"REJECT"`), &v); err == nil {
		t.Error("expected error for unknown verdict, got nil")
	}
}

func TestParseCodeReviewVerdict(t *testing.T) {
	v, err := ParseCodeReviewVerdict("revise")
	if err != nil {
		t.Fatalf("ParseCodeReviewVerdict(\"revise\"): %v", err)
	}
	if v != CodeReviewVerdictRevise {
		t.Errorf("got %v, want CodeReviewVerdictRevise", v)
	}
	_, err = ParseCodeReviewVerdict("nope")
	if err == nil {
		t.Error("expected error for unknown verdict")
	}
}

// ---------------------------------------------------------------------------
// FixOutput / FixAction / FixStatus tests
// ---------------------------------------------------------------------------

func TestFixStatus_Constants(t *testing.T) {
	if FixStatusFixed != "fixed" {
		t.Errorf("FixStatusFixed = %q, want \"fixed\"", FixStatusFixed)
	}
	if FixStatusDeferred != "deferred" {
		t.Errorf("FixStatusDeferred = %q, want \"deferred\"", FixStatusDeferred)
	}
	if FixStatusFailed != "failed" {
		t.Errorf("FixStatusFailed = %q, want \"failed\"", FixStatusFailed)
	}
}

func TestValidFixStatuses(t *testing.T) {
	for _, s := range []FixStatus{FixStatusFixed, FixStatusDeferred, FixStatusFailed} {
		if !ValidFixStatuses[s] {
			t.Errorf("ValidFixStatuses[%q] should be true", s)
		}
	}
	if ValidFixStatuses["unknown"] {
		t.Error("ValidFixStatuses[\"unknown\"] should be false")
	}
}

func TestFixOutput_JSONRoundTrip(t *testing.T) {
	original := FixOutput{
		Round: 2,
		FixesApplied: []FixAction{
			{
				FindingID:     "CRIT-001",
				Status:        FixStatusFixed,
				FilesModified: []string{"main.go", "handler.go"},
				Description:   "Fixed nil pointer dereference",
			},
			{
				FindingID:     "MAJ-003",
				Status:        FixStatusDeferred,
				FilesModified: nil,
				Description:   "Requires upstream API change",
			},
		},
		TestResults: &TestResults{
			Total:    42,
			Passed:   40,
			Failed:   2,
			Failures: []string{"TestFoo", "TestBar"},
		},
		GitDiffStat: " 2 files changed, 10 insertions(+), 3 deletions(-)",
	}

	data, err := json.MarshalIndent(original, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}

	var decoded FixOutput
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.Round != original.Round {
		t.Errorf("Round: got %d, want %d", decoded.Round, original.Round)
	}
	if len(decoded.FixesApplied) != len(original.FixesApplied) {
		t.Fatalf("FixesApplied length: got %d, want %d", len(decoded.FixesApplied), len(original.FixesApplied))
	}
	if decoded.FixesApplied[0].FindingID != "CRIT-001" {
		t.Errorf("FixesApplied[0].FindingID: got %q, want CRIT-001", decoded.FixesApplied[0].FindingID)
	}
	if decoded.FixesApplied[0].Status != FixStatusFixed {
		t.Errorf("FixesApplied[0].Status: got %q, want fixed", decoded.FixesApplied[0].Status)
	}
	if decoded.FixesApplied[1].Status != FixStatusDeferred {
		t.Errorf("FixesApplied[1].Status: got %q, want deferred", decoded.FixesApplied[1].Status)
	}
	if decoded.TestResults == nil {
		t.Fatal("TestResults should not be nil")
	}
	if decoded.TestResults.Total != 42 {
		t.Errorf("TestResults.Total: got %d, want 42", decoded.TestResults.Total)
	}
	if decoded.TestResults.Passed != 40 {
		t.Errorf("TestResults.Passed: got %d, want 40", decoded.TestResults.Passed)
	}
	if decoded.TestResults.Failed != 2 {
		t.Errorf("TestResults.Failed: got %d, want 2", decoded.TestResults.Failed)
	}
	if len(decoded.TestResults.Failures) != 2 {
		t.Fatalf("TestResults.Failures length: got %d, want 2", len(decoded.TestResults.Failures))
	}
	if decoded.GitDiffStat != original.GitDiffStat {
		t.Errorf("GitDiffStat: got %q, want %q", decoded.GitDiffStat, original.GitDiffStat)
	}
}

func TestFixOutput_NilTestResults(t *testing.T) {
	original := FixOutput{
		Round:        1,
		FixesApplied: []FixAction{},
		TestResults:  nil,
		GitDiffStat:  "",
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded FixOutput
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.TestResults != nil {
		t.Error("TestResults should be nil")
	}
}

// ---------------------------------------------------------------------------
// CodeReviewStateJSON round-trip
// ---------------------------------------------------------------------------

func TestCodeReviewStateJSON_RoundTrip(t *testing.T) {
	original := CodeReviewStateJSON{
		State:          CRReviewing,
		Round:          2,
		FeatureName:    "auth-refactor",
		CodePath:       "/home/user/repo",
		SpecPath:       "/home/user/spec.md",
		TaskListPath:   "/home/user/tasks.json",
		GrillCodeMode:  GrillCodeModeFullContext,
		GitBranch:      "feature/auth",
		GitHeadSHA:     "abc123def456",
		CommitMode:     "branch_per_round",
		StartedAt:      "2026-03-28T10:00:00Z",
		UpdatedAt:      "2026-03-28T11:30:00Z",
		CumulativeCostUSD:          5.25,
		CumulativeWallClockSeconds: 5400.0,
		AgentInvocations:           12,
		FindingsSummary: CodeReviewFindingsSummary{
			OpenCritical:    1,
			OpenMajor:       3,
			OpenMinor:       2,
			OpenObservation: 5,
			Fixed:           4,
			Deferred:        1,
			Failed:          0,
		},
		Verdict:          CodeReviewVerdictRevise,
		EscalationReason: "",
		Warnings:         []string{"reduced_coverage"},
	}

	data, err := json.MarshalIndent(original, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}

	var decoded CodeReviewStateJSON
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.State != original.State {
		t.Errorf("State: got %v, want %v", decoded.State, original.State)
	}
	if decoded.Round != original.Round {
		t.Errorf("Round: got %d, want %d", decoded.Round, original.Round)
	}
	if decoded.FeatureName != original.FeatureName {
		t.Errorf("FeatureName: got %q, want %q", decoded.FeatureName, original.FeatureName)
	}
	if decoded.CodePath != original.CodePath {
		t.Errorf("CodePath: got %q, want %q", decoded.CodePath, original.CodePath)
	}
	if decoded.SpecPath != original.SpecPath {
		t.Errorf("SpecPath: got %q, want %q", decoded.SpecPath, original.SpecPath)
	}
	if decoded.TaskListPath != original.TaskListPath {
		t.Errorf("TaskListPath: got %q, want %q", decoded.TaskListPath, original.TaskListPath)
	}
	if decoded.GrillCodeMode != original.GrillCodeMode {
		t.Errorf("GrillCodeMode: got %v, want %v", decoded.GrillCodeMode, original.GrillCodeMode)
	}
	if decoded.GitBranch != original.GitBranch {
		t.Errorf("GitBranch: got %q, want %q", decoded.GitBranch, original.GitBranch)
	}
	if decoded.GitHeadSHA != original.GitHeadSHA {
		t.Errorf("GitHeadSHA: got %q, want %q", decoded.GitHeadSHA, original.GitHeadSHA)
	}
	if decoded.CommitMode != original.CommitMode {
		t.Errorf("CommitMode: got %q, want %q", decoded.CommitMode, original.CommitMode)
	}
	if decoded.CumulativeCostUSD != original.CumulativeCostUSD {
		t.Errorf("CumulativeCostUSD: got %f, want %f", decoded.CumulativeCostUSD, original.CumulativeCostUSD)
	}
	if decoded.CumulativeWallClockSeconds != original.CumulativeWallClockSeconds {
		t.Errorf("CumulativeWallClockSeconds: got %f, want %f", decoded.CumulativeWallClockSeconds, original.CumulativeWallClockSeconds)
	}
	if decoded.AgentInvocations != original.AgentInvocations {
		t.Errorf("AgentInvocations: got %d, want %d", decoded.AgentInvocations, original.AgentInvocations)
	}
	if decoded.FindingsSummary.OpenCritical != 1 {
		t.Errorf("FindingsSummary.OpenCritical: got %d, want 1", decoded.FindingsSummary.OpenCritical)
	}
	if decoded.FindingsSummary.OpenMajor != 3 {
		t.Errorf("FindingsSummary.OpenMajor: got %d, want 3", decoded.FindingsSummary.OpenMajor)
	}
	if decoded.FindingsSummary.Fixed != 4 {
		t.Errorf("FindingsSummary.Fixed: got %d, want 4", decoded.FindingsSummary.Fixed)
	}
	if decoded.Verdict != CodeReviewVerdictRevise {
		t.Errorf("Verdict: got %v, want REVISE", decoded.Verdict)
	}
	if len(decoded.Warnings) != 1 || decoded.Warnings[0] != "reduced_coverage" {
		t.Errorf("Warnings: got %v, want [reduced_coverage]", decoded.Warnings)
	}
}

func TestCodeReviewStateJSON_JSONFieldNames(t *testing.T) {
	ws := CodeReviewStateJSON{
		State: CRInit,
	}
	data, err := json.Marshal(ws)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal to map: %v", err)
	}

	requiredKeys := []string{
		"state", "round", "feature_name", "code_path",
		"grill_code_mode", "git_branch", "git_head_sha",
		"commit_mode", "started_at", "updated_at",
		"cumulative_cost_usd", "cumulative_wall_clock_seconds",
		"agent_invocations", "findings_summary",
	}
	for _, key := range requiredKeys {
		if _, ok := raw[key]; !ok {
			t.Errorf("missing JSON key %q in serialized output", key)
		}
	}
}

// ---------------------------------------------------------------------------
// Lens groups
// ---------------------------------------------------------------------------

func TestCodeReviewLensGroups(t *testing.T) {
	expected := []string{
		"correctness", "security", "testing",
		"error-handling", "observability", "overcomplexity",
	}
	if len(CodeReviewLensGroups) != len(expected) {
		t.Fatalf("CodeReviewLensGroups length: got %d, want %d", len(CodeReviewLensGroups), len(expected))
	}
	for i, want := range expected {
		if CodeReviewLensGroups[i] != want {
			t.Errorf("CodeReviewLensGroups[%d] = %q, want %q", i, CodeReviewLensGroups[i], want)
		}
	}
}
