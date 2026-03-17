package specworkflow

import (
	"encoding/json"
	"testing"
)

// ---------------------------------------------------------------------------
// WorkflowState tests
// ---------------------------------------------------------------------------

func TestWorkflowState_String(t *testing.T) {
	tests := []struct {
		state WorkflowState
		want  string
	}{
		{StateInit, "INIT"},
		{StateDiscovery, "DISCOVERY"},
		{StateHumanGate1, "HUMAN_GATE_1"},
		{StateDrafting, "DRAFTING"},
		{StateHumanGate2, "HUMAN_GATE_2"},
		{StateReviewing, "REVIEWING"},
		{StateRevising, "REVISING"},
		{StateJudging, "JUDGING"},
		{StateHumanGateFinal, "HUMAN_GATE_FINAL"},
		{StateFinalized, "FINALIZED"},
		{StateEscalated, "ESCALATED"},
		{StateError, "ERROR"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("WorkflowState(%d).String() = %q, want %q", int(tt.state), got, tt.want)
		}
	}
}

func TestWorkflowState_StringUnknown(t *testing.T) {
	s := WorkflowState(999)
	got := s.String()
	if got == "" {
		t.Error("expected non-empty string for unknown WorkflowState")
	}
}

func TestWorkflowState_JSONRoundTrip(t *testing.T) {
	states := []WorkflowState{
		StateInit, StateDiscovery, StateHumanGate1, StateDrafting,
		StateHumanGate2, StateReviewing, StateRevising, StateJudging,
		StateHumanGateFinal, StateFinalized, StateEscalated, StateError,
	}
	for _, s := range states {
		data, err := json.Marshal(s)
		if err != nil {
			t.Fatalf("Marshal WorkflowState %v: %v", s, err)
		}
		var got WorkflowState
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("Unmarshal WorkflowState %q: %v", data, err)
		}
		if got != s {
			t.Errorf("round-trip: got %v, want %v", got, s)
		}
	}
}

func TestWorkflowState_UnmarshalCaseInsensitive(t *testing.T) {
	inputs := []string{`"init"`, `"Init"`, `"INIT"`, `"discovery"`, `"Discovery"`}
	for _, input := range inputs {
		var s WorkflowState
		if err := json.Unmarshal([]byte(input), &s); err != nil {
			t.Errorf("Unmarshal %s: %v", input, err)
		}
	}
}

func TestWorkflowState_UnmarshalInvalid(t *testing.T) {
	var s WorkflowState
	if err := json.Unmarshal([]byte(`"BOGUS"`), &s); err == nil {
		t.Error("expected error for unknown state, got nil")
	}
	if err := json.Unmarshal([]byte(`123`), &s); err == nil {
		t.Error("expected error for non-string, got nil")
	}
}

func TestParseWorkflowState(t *testing.T) {
	s, err := ParseWorkflowState("reviewing")
	if err != nil {
		t.Fatalf("ParseWorkflowState(\"reviewing\"): %v", err)
	}
	if s != StateReviewing {
		t.Errorf("got %v, want StateReviewing", s)
	}
	_, err = ParseWorkflowState("nope")
	if err == nil {
		t.Error("expected error for unknown state")
	}
}

// ---------------------------------------------------------------------------
// Severity tests
// ---------------------------------------------------------------------------

func TestSeverity_String(t *testing.T) {
	tests := []struct {
		sev  Severity
		want string
	}{
		{SeverityCritical, "CRITICAL"},
		{SeverityMajor, "MAJOR"},
		{SeverityMinor, "MINOR"},
		{SeverityObservation, "OBSERVATION"},
	}
	for _, tt := range tests {
		if got := tt.sev.String(); got != tt.want {
			t.Errorf("Severity(%d).String() = %q, want %q", int(tt.sev), got, tt.want)
		}
	}
}

func TestParseSeverity_CaseInsensitive(t *testing.T) {
	cases := []struct {
		input string
		want  Severity
	}{
		{"critical", SeverityCritical},
		{"Critical", SeverityCritical},
		{"CRITICAL", SeverityCritical},
		{"major", SeverityMajor},
		{"Major", SeverityMajor},
		{"MAJOR", SeverityMajor},
		{"minor", SeverityMinor},
		{"Minor", SeverityMinor},
		{"MINOR", SeverityMinor},
		{"observation", SeverityObservation},
		{"Observation", SeverityObservation},
		{"OBSERVATION", SeverityObservation},
	}
	for _, tc := range cases {
		got, err := ParseSeverity(tc.input)
		if err != nil {
			t.Errorf("ParseSeverity(%q): unexpected error: %v", tc.input, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseSeverity(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestParseSeverity_Invalid(t *testing.T) {
	_, err := ParseSeverity("blocker")
	if err == nil {
		t.Error("expected error for unknown severity, got nil")
	}
}

func TestSeverity_JSONRoundTrip(t *testing.T) {
	sevs := []Severity{SeverityCritical, SeverityMajor, SeverityMinor, SeverityObservation}
	for _, s := range sevs {
		data, err := json.Marshal(s)
		if err != nil {
			t.Fatalf("Marshal Severity %v: %v", s, err)
		}
		var got Severity
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("Unmarshal Severity %q: %v", data, err)
		}
		if got != s {
			t.Errorf("round-trip: got %v, want %v", got, s)
		}
	}
}

func TestSeverity_UnmarshalCaseInsensitive(t *testing.T) {
	var s Severity
	if err := json.Unmarshal([]byte(`"minor"`), &s); err != nil {
		t.Fatalf("Unmarshal \"minor\": %v", err)
	}
	if s != SeverityMinor {
		t.Errorf("got %v, want SeverityMinor", s)
	}
}

// ---------------------------------------------------------------------------
// Verdict tests
// ---------------------------------------------------------------------------

func TestVerdict_String(t *testing.T) {
	tests := []struct {
		v    Verdict
		want string
	}{
		{VerdictPass, "PASS"},
		{VerdictRevise, "REVISE"},
		{VerdictBlock, "BLOCK"},
	}
	for _, tt := range tests {
		if got := tt.v.String(); got != tt.want {
			t.Errorf("Verdict(%d).String() = %q, want %q", int(tt.v), got, tt.want)
		}
	}
}

func TestVerdict_JSONRoundTrip(t *testing.T) {
	verdicts := []Verdict{VerdictPass, VerdictRevise, VerdictBlock}
	for _, v := range verdicts {
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("Marshal Verdict %v: %v", v, err)
		}
		var got Verdict
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("Unmarshal Verdict %q: %v", data, err)
		}
		if got != v {
			t.Errorf("round-trip: got %v, want %v", got, v)
		}
	}
}

func TestVerdict_UnmarshalCaseInsensitive(t *testing.T) {
	var v Verdict
	if err := json.Unmarshal([]byte(`"revise"`), &v); err != nil {
		t.Fatalf("Unmarshal \"revise\": %v", err)
	}
	if v != VerdictRevise {
		t.Errorf("got %v, want VerdictRevise", v)
	}
}

func TestVerdict_UnmarshalInvalid(t *testing.T) {
	var v Verdict
	if err := json.Unmarshal([]byte(`"REJECT"`), &v); err == nil {
		t.Error("expected error for unknown verdict, got nil")
	}
}

func TestParseVerdict(t *testing.T) {
	v, err := ParseVerdict("block")
	if err != nil {
		t.Fatalf("ParseVerdict(\"block\"): %v", err)
	}
	if v != VerdictBlock {
		t.Errorf("got %v, want VerdictBlock", v)
	}
	_, err = ParseVerdict("nope")
	if err == nil {
		t.Error("expected error for unknown verdict")
	}
}

// ---------------------------------------------------------------------------
// WorkflowStateJSON round-trip
// ---------------------------------------------------------------------------

func TestWorkflowStateJSON_RoundTrip(t *testing.T) {
	original := WorkflowStateJSON{
		State:                      StateReviewing,
		Round:                      2,
		FeatureName:                "user-auth-flow",
		StartedAt:                  "2025-01-15T10:30:00Z",
		UpdatedAt:                  "2025-01-15T11:45:30Z",
		CumulativeCostUSD:          1.2345,
		CumulativeWallClockSeconds: 4530.5,
		AgentInvocations:           7,
		FindingsSummary: FindingsSummary{
			Raised:       12,
			Closed:       8,
			OpenCritical: 1,
			OpenMajor:    3,
		},
		HadCriticalFindings:  true,
		Gate1CorrectionCount: 2,
		Gate2RedraftCount:    1,
		CurrentSpecVersion:   3,
		SkillChecksums: map[string]string{
			"grill-spec": "abc123def456",
			"plan-spec":  "789ghi012jkl",
		},
	}

	data, err := json.MarshalIndent(original, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}

	var decoded WorkflowStateJSON
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	// Verify every field.
	if decoded.State != original.State {
		t.Errorf("State: got %v, want %v", decoded.State, original.State)
	}
	if decoded.Round != original.Round {
		t.Errorf("Round: got %d, want %d", decoded.Round, original.Round)
	}
	if decoded.FeatureName != original.FeatureName {
		t.Errorf("FeatureName: got %q, want %q", decoded.FeatureName, original.FeatureName)
	}
	if decoded.StartedAt != original.StartedAt {
		t.Errorf("StartedAt: got %q, want %q", decoded.StartedAt, original.StartedAt)
	}
	if decoded.UpdatedAt != original.UpdatedAt {
		t.Errorf("UpdatedAt: got %q, want %q", decoded.UpdatedAt, original.UpdatedAt)
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
	if decoded.FindingsSummary.Raised != original.FindingsSummary.Raised {
		t.Errorf("FindingsSummary.Raised: got %d, want %d", decoded.FindingsSummary.Raised, original.FindingsSummary.Raised)
	}
	if decoded.FindingsSummary.Closed != original.FindingsSummary.Closed {
		t.Errorf("FindingsSummary.Closed: got %d, want %d", decoded.FindingsSummary.Closed, original.FindingsSummary.Closed)
	}
	if decoded.FindingsSummary.OpenCritical != original.FindingsSummary.OpenCritical {
		t.Errorf("FindingsSummary.OpenCritical: got %d, want %d", decoded.FindingsSummary.OpenCritical, original.FindingsSummary.OpenCritical)
	}
	if decoded.FindingsSummary.OpenMajor != original.FindingsSummary.OpenMajor {
		t.Errorf("FindingsSummary.OpenMajor: got %d, want %d", decoded.FindingsSummary.OpenMajor, original.FindingsSummary.OpenMajor)
	}
	if decoded.HadCriticalFindings != original.HadCriticalFindings {
		t.Errorf("HadCriticalFindings: got %v, want %v", decoded.HadCriticalFindings, original.HadCriticalFindings)
	}
	if decoded.Gate1CorrectionCount != original.Gate1CorrectionCount {
		t.Errorf("Gate1CorrectionCount: got %d, want %d", decoded.Gate1CorrectionCount, original.Gate1CorrectionCount)
	}
	if decoded.Gate2RedraftCount != original.Gate2RedraftCount {
		t.Errorf("Gate2RedraftCount: got %d, want %d", decoded.Gate2RedraftCount, original.Gate2RedraftCount)
	}
	if decoded.CurrentSpecVersion != original.CurrentSpecVersion {
		t.Errorf("CurrentSpecVersion: got %d, want %d", decoded.CurrentSpecVersion, original.CurrentSpecVersion)
	}
	if len(decoded.SkillChecksums) != len(original.SkillChecksums) {
		t.Fatalf("SkillChecksums length: got %d, want %d", len(decoded.SkillChecksums), len(original.SkillChecksums))
	}
	for k, v := range original.SkillChecksums {
		if decoded.SkillChecksums[k] != v {
			t.Errorf("SkillChecksums[%q]: got %q, want %q", k, decoded.SkillChecksums[k], v)
		}
	}
}

func TestWorkflowStateJSON_JSONFieldNames(t *testing.T) {
	ws := WorkflowStateJSON{
		State:          StateInit,
		SkillChecksums: map[string]string{},
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
		"state", "round", "feature_name", "started_at", "updated_at",
		"cumulative_cost_usd", "cumulative_wall_clock_seconds",
		"agent_invocations", "findings_summary", "had_critical_findings",
		"gate1_correction_count", "gate2_redraft_count",
		"current_spec_version", "skill_checksums",
	}
	for _, key := range requiredKeys {
		if _, ok := raw[key]; !ok {
			t.Errorf("missing JSON key %q in serialized output", key)
		}
	}
}
