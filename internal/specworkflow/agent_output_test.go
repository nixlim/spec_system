package specworkflow

import (
	"encoding/json"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }

// validDiscoveryOutput returns a minimal valid DiscoveryOutput.
func validDiscoveryOutput() *DiscoveryOutput {
	return &DiscoveryOutput{
		SchemaVersion: "1.0",
		Agent:         "discovery",
		Actors: []Actor{
			{Name: "Admin", Type: "human", Description: "System administrator"},
		},
		ProblemStatement: "Users need authentication",
		Scope: Scope{
			InScope:    []string{"login flow"},
			OutOfScope: []string{"SSO"},
		},
		Constraints: []string{"must use OAuth2"},
		IntegrationPoints: []IntegrationPoint{
			{System: "IDP", Description: "Identity provider", Direction: "outbound"},
		},
		Priorities: []Priority{
			{Item: "login", Priority: "P0", Rationale: "Core feature"},
		},
		Assumptions: []Assumption{
			{Assumption: "Users have email", Confidence: "high"},
		},
		OpenQuestions: []string{"MFA required?"},
	}
}

// validDrafterOutput returns a minimal valid DrafterOutput.
func validDrafterOutput() *DrafterOutput {
	return &DrafterOutput{
		SchemaVersion: "1.0",
		Agent:         "drafter",
		SpecFile:      "spec.md",
		HoldoutFile:   "holdout.json",
		AmbiguityWarnings: []AmbiguityWarning{
			{
				ID:              "AMB-W-001",
				Section:         "FR-1",
				Ambiguity:       "unclear timeout",
				AgentAssumption: "30s default",
				QuestionForUser: "What timeout?",
			},
		},
		StructuralSummary: StructuralSummary{
			UserStoryCount:   3,
			BDDScenarioCount: 5,
			FRCount:          10,
			TestCount:        8,
		},
	}
}

// validReviewerOutput returns a minimal valid ReviewerOutput.
func validReviewerOutput() *ReviewerOutput {
	return &ReviewerOutput{
		SchemaVersion:      "1.0",
		Agent:              "reviewer",
		Round:              1,
		LensesApplied:      []string{"security", "testability"},
		MarkdownReportFile: "review.md",
		Findings: []Finding{
			{
				ID:              "F-001",
				Description:     "Missing input validation",
				Severity:        SeverityCritical,
				Impact:          "SQL injection risk",
				Recommendation:  "Add parameterised queries",
				Lens:            "security",
				AffectedSection: "FR-3",
			},
		},
		StructuralIntegrity: StructuralIntegrity{
			Performed: true,
			Checks: []IntegrityCheck{
				{Check: "BDD coverage", Result: "PASS"},
			},
		},
	}
}

// validRevisionOutput returns a minimal valid RevisionOutput.
func validRevisionOutput() *RevisionOutput {
	return &RevisionOutput{
		SchemaVersion:   "1.0",
		Agent:           "reviser",
		Round:           1,
		RevisedSpecFile: "spec-v2.md",
		Changes: []Change{
			{
				FindingID:        "F-001",
				Action:           "revised",
				Description:      "Added input validation",
				SectionsModified: []string{"FR-3"},
			},
		},
		DismissalRequests: []DismissalRequest{},
	}
}

// validJudgeOutput returns a minimal valid JudgeOutput.
func validJudgeOutput() *JudgeOutput {
	return &JudgeOutput{
		SchemaVersion: "1.0",
		Agent:         "judge",
		Round:         1,
		Verdict:       VerdictPass,
		Rationale:     "All critical findings addressed",
		IssueUpdates: []IssueUpdate{
			{FindingID: "F-001", NewStatus: "verified", Explanation: "Fixed"},
		},
		Downgrades:      []Downgrade{},
		StructuralDelta: StructuralDelta{RegressionsFound: false},
	}
}

// ---------------------------------------------------------------------------
// DiscoveryOutput tests
// ---------------------------------------------------------------------------

func TestAgentOutput_ValidateDiscoveryOutput_Valid(t *testing.T) {
	errs := ValidateDiscoveryOutput(validDiscoveryOutput())
	if len(errs) != 0 {
		t.Errorf("expected no errors, got %d: %v", len(errs), errs)
	}
}

func TestAgentOutput_ValidateDiscoveryOutput_EmptyFields(t *testing.T) {
	o := &DiscoveryOutput{} // everything empty/zero
	errs := ValidateDiscoveryOutput(o)
	if len(errs) == 0 {
		t.Fatal("expected errors for empty DiscoveryOutput")
	}
	// Should report at least: schema_version, agent, problem_statement, actors, scope.in_scope
	wantSubstrings := []string{"schema_version", "agent", "problem_statement", "actors", "scope.in_scope"}
	for _, w := range wantSubstrings {
		found := false
		for _, e := range errs {
			if strings.Contains(e.Error(), w) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected error mentioning %q, not found in %v", w, errs)
		}
	}
}

func TestAgentOutput_ValidateDiscoveryOutput_BadActorType(t *testing.T) {
	o := validDiscoveryOutput()
	o.Actors[0].Type = "robot"
	errs := ValidateDiscoveryOutput(o)
	if len(errs) == 0 {
		t.Fatal("expected error for bad actor type")
	}
	if !strings.Contains(errs[0].Error(), "human|system|external") {
		t.Errorf("expected actor type error, got: %v", errs[0])
	}
}

func TestAgentOutput_ValidateDiscoveryOutput_BadDirection(t *testing.T) {
	o := validDiscoveryOutput()
	o.IntegrationPoints[0].Direction = "both"
	errs := ValidateDiscoveryOutput(o)
	if len(errs) == 0 {
		t.Fatal("expected error for bad direction")
	}
	if !strings.Contains(errs[0].Error(), "inbound|outbound|bidirectional") {
		t.Errorf("expected direction error, got: %v", errs[0])
	}
}

func TestAgentOutput_ValidateDiscoveryOutput_BadPriority(t *testing.T) {
	o := validDiscoveryOutput()
	o.Priorities[0].Priority = "P5"
	errs := ValidateDiscoveryOutput(o)
	if len(errs) == 0 {
		t.Fatal("expected error for bad priority")
	}
	if !strings.Contains(errs[0].Error(), "P0-P4") {
		t.Errorf("expected priority error, got: %v", errs[0])
	}
}

func TestAgentOutput_ValidateDiscoveryOutput_BadConfidence(t *testing.T) {
	o := validDiscoveryOutput()
	o.Assumptions[0].Confidence = "very high"
	errs := ValidateDiscoveryOutput(o)
	if len(errs) == 0 {
		t.Fatal("expected error for bad confidence")
	}
	if !strings.Contains(errs[0].Error(), "high|medium|low") {
		t.Errorf("expected confidence error, got: %v", errs[0])
	}
}

func TestAgentOutput_ValidateDiscoveryOutput_MultipleErrors(t *testing.T) {
	o := &DiscoveryOutput{
		Actors: []Actor{
			{Name: "", Type: "robot", Description: ""},
		},
		Assumptions: []Assumption{
			{Confidence: "ultra"},
		},
	}
	errs := ValidateDiscoveryOutput(o)
	// Should have multiple errors reported (not just first)
	if len(errs) < 5 {
		t.Errorf("expected >= 5 errors, got %d: %v", len(errs), errs)
	}
}

// ---------------------------------------------------------------------------
// DrafterOutput tests
// ---------------------------------------------------------------------------

func TestAgentOutput_ValidateDrafterOutput_Valid(t *testing.T) {
	errs := ValidateDrafterOutput(validDrafterOutput())
	if len(errs) != 0 {
		t.Errorf("expected no errors, got %d: %v", len(errs), errs)
	}
}

func TestAgentOutput_ValidateDrafterOutput_EmptyFields(t *testing.T) {
	o := &DrafterOutput{}
	errs := ValidateDrafterOutput(o)
	if len(errs) == 0 {
		t.Fatal("expected errors for empty DrafterOutput")
	}
	wantSubstrings := []string{"schema_version", "agent", "spec_file", "holdout_file"}
	for _, w := range wantSubstrings {
		found := false
		for _, e := range errs {
			if strings.Contains(e.Error(), w) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected error mentioning %q, not found in %v", w, errs)
		}
	}
}

func TestAgentOutput_ValidateDrafterOutput_BadAmbiguityID(t *testing.T) {
	o := validDrafterOutput()
	o.AmbiguityWarnings[0].ID = "AMB-001"
	errs := ValidateDrafterOutput(o)
	if len(errs) == 0 {
		t.Fatal("expected error for bad ambiguity ID")
	}
	if !strings.Contains(errs[0].Error(), "AMB-W-NNN") {
		t.Errorf("expected AMB-W-NNN pattern error, got: %v", errs[0])
	}
}

// ---------------------------------------------------------------------------
// ReviewerOutput tests
// ---------------------------------------------------------------------------

func TestAgentOutput_ValidateReviewerOutput_Valid(t *testing.T) {
	valid, rejected, errs := ValidateReviewerOutput(validReviewerOutput())
	if len(errs) != 0 {
		t.Errorf("expected no errors, got %d: %v", len(errs), errs)
	}
	if rejected != 0 {
		t.Errorf("expected 0 rejected, got %d", rejected)
	}
	if len(valid) != 1 {
		t.Errorf("expected 1 valid finding, got %d", len(valid))
	}
}

func TestAgentOutput_ValidateReviewerOutput_RejectsMissingRecommendation(t *testing.T) {
	o := validReviewerOutput()
	o.Findings = append(o.Findings, Finding{
		ID:              "F-002",
		Description:     "Missing error handling",
		Severity:        SeverityMajor,
		Impact:          "Silent failures",
		Recommendation:  "", // MISSING
		Lens:            "reliability",
		AffectedSection: "FR-5",
	})
	valid, rejected, errs := ValidateReviewerOutput(o)
	if rejected != 1 {
		t.Errorf("expected 1 rejected, got %d", rejected)
	}
	if len(valid) != 1 {
		t.Errorf("expected 1 valid finding, got %d", len(valid))
	}
	// Should have an error mentioning rejection
	foundRejection := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "rejected") && strings.Contains(e.Error(), "recommendation") {
			foundRejection = true
			break
		}
	}
	if !foundRejection {
		t.Errorf("expected rejection error for missing recommendation, got: %v", errs)
	}
}

func TestAgentOutput_ValidateReviewerOutput_EmptyLenses(t *testing.T) {
	o := validReviewerOutput()
	o.LensesApplied = nil
	_, _, errs := ValidateReviewerOutput(o)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "lenses_applied") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error for empty lenses_applied, got: %v", errs)
	}
}

func TestAgentOutput_ValidateReviewerOutput_BadIntegrityResult(t *testing.T) {
	o := validReviewerOutput()
	o.StructuralIntegrity.Checks[0].Result = "MAYBE"
	_, _, errs := ValidateReviewerOutput(o)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "PASS|FAIL") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error for bad integrity result, got: %v", errs)
	}
}

func TestAgentOutput_ValidateReviewerOutput_SeverityNormalization(t *testing.T) {
	// Severity normalisation happens at the JSON unmarshal level.
	// Verify that a lowercase severity in JSON is normalised to uppercase
	// and survives validation.
	raw := `{
		"schema_version": "1.0",
		"agent": "reviewer",
		"round": 1,
		"lenses_applied": ["security"],
		"findings": [{
			"id": "F-001",
			"description": "test",
			"severity": "critical",
			"impact": "high",
			"recommendation": "fix it",
			"lens": "security",
			"affected_section": "FR-1"
		}],
		"structural_integrity": {"performed": true, "checks": []},
		"markdown_report_file": "report.md"
	}`
	var o ReviewerOutput
	if err := json.Unmarshal([]byte(raw), &o); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	valid, rejected, errs := ValidateReviewerOutput(&o)
	if len(errs) != 0 {
		t.Errorf("expected no errors, got: %v", errs)
	}
	if rejected != 0 {
		t.Errorf("expected 0 rejected, got %d", rejected)
	}
	if len(valid) != 1 {
		t.Fatalf("expected 1 valid finding, got %d", len(valid))
	}
	if valid[0].Severity != SeverityCritical {
		t.Errorf("severity not normalised: got %v, want CRITICAL", valid[0].Severity)
	}
	// Verify it marshals back to uppercase
	data, _ := json.Marshal(valid[0].Severity)
	if string(data) != `"CRITICAL"` {
		t.Errorf("marshalled severity = %s, want %q", data, "CRITICAL")
	}
}

// ---------------------------------------------------------------------------
// RevisionOutput tests
// ---------------------------------------------------------------------------

func TestAgentOutput_ValidateRevisionOutput_Valid(t *testing.T) {
	errs := ValidateRevisionOutput(validRevisionOutput())
	if len(errs) != 0 {
		t.Errorf("expected no errors, got %d: %v", len(errs), errs)
	}
}

func TestAgentOutput_ValidateRevisionOutput_EmptyFields(t *testing.T) {
	o := &RevisionOutput{}
	errs := ValidateRevisionOutput(o)
	if len(errs) == 0 {
		t.Fatal("expected errors for empty RevisionOutput")
	}
	wantSubstrings := []string{"schema_version", "agent", "round", "revised_spec_file"}
	for _, w := range wantSubstrings {
		found := false
		for _, e := range errs {
			if strings.Contains(e.Error(), w) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected error mentioning %q, not found in %v", w, errs)
		}
	}
}

func TestAgentOutput_ValidateRevisionOutput_BadAction(t *testing.T) {
	o := validRevisionOutput()
	o.Changes[0].Action = "ignored"
	errs := ValidateRevisionOutput(o)
	if len(errs) == 0 {
		t.Fatal("expected error for bad action")
	}
	if !strings.Contains(errs[0].Error(), "revised|dismissed") {
		t.Errorf("expected action error, got: %v", errs[0])
	}
}

func TestAgentOutput_ValidateRevisionOutput_EmptySectionsModified(t *testing.T) {
	o := validRevisionOutput()
	o.Changes[0].SectionsModified = nil
	errs := ValidateRevisionOutput(o)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "sections_modified") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error for empty sections_modified, got: %v", errs)
	}
}

// ---------------------------------------------------------------------------
// JudgeOutput tests
// ---------------------------------------------------------------------------

func TestAgentOutput_ValidateJudgeOutput_Valid(t *testing.T) {
	errs := ValidateJudgeOutput(validJudgeOutput())
	if len(errs) != 0 {
		t.Errorf("expected no errors, got %d: %v", len(errs), errs)
	}
}

func TestAgentOutput_ValidateJudgeOutput_EmptyFields(t *testing.T) {
	o := &JudgeOutput{}
	errs := ValidateJudgeOutput(o)
	if len(errs) == 0 {
		t.Fatal("expected errors for empty JudgeOutput")
	}
	wantSubstrings := []string{"schema_version", "agent", "round", "rationale"}
	for _, w := range wantSubstrings {
		found := false
		for _, e := range errs {
			if strings.Contains(e.Error(), w) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected error mentioning %q, not found in %v", w, errs)
		}
	}
}

func TestAgentOutput_ValidateJudgeOutput_BadIssueStatus(t *testing.T) {
	o := validJudgeOutput()
	o.IssueUpdates[0].NewStatus = "closed"
	errs := ValidateJudgeOutput(o)
	if len(errs) == 0 {
		t.Fatal("expected error for bad status")
	}
	if !strings.Contains(errs[0].Error(), "verified|reopened|dismissed") {
		t.Errorf("expected status error, got: %v", errs[0])
	}
}

func TestAgentOutput_ValidateJudgeOutput_BadDowngradeReason(t *testing.T) {
	o := validJudgeOutput()
	o.Downgrades = []Downgrade{
		{
			FindingID:    "F-001",
			FromSeverity: SeverityCritical,
			ToSeverity:   SeverityMinor,
			ReasonCode:   "INVALID_REASON",
			ReasonDetail: "test",
		},
	}
	errs := ValidateJudgeOutput(o)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "reason_code") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error for bad reason_code, got: %v", errs)
	}
}

func TestAgentOutput_ValidateJudgeOutput_ValidDowngradeReasons(t *testing.T) {
	reasons := []string{"DUPLICATE_OF", "OUT_OF_SCOPE", "CONTRADICTED_BY_REQUIREMENT", "REVIEWER_ERROR"}
	for _, reason := range reasons {
		o := validJudgeOutput()
		o.Downgrades = []Downgrade{
			{
				FindingID:    "F-001",
				FromSeverity: SeverityCritical,
				ToSeverity:   SeverityMinor,
				ReasonCode:   reason,
				ReasonDetail: "test detail",
			},
		}
		errs := ValidateJudgeOutput(o)
		if len(errs) != 0 {
			t.Errorf("reason_code %q: expected no errors, got %v", reason, errs)
		}
	}
}

// ---------------------------------------------------------------------------
// JSON marshal/unmarshal round-trip tests
// ---------------------------------------------------------------------------

func TestAgentOutput_DiscoveryOutput_JSONRoundTrip(t *testing.T) {
	original := validDiscoveryOutput()
	original.Assumptions[0].QuestionForUser = strPtr("Do all users have email?")

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded DiscoveryOutput
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.SchemaVersion != original.SchemaVersion {
		t.Errorf("SchemaVersion: got %q, want %q", decoded.SchemaVersion, original.SchemaVersion)
	}
	if decoded.Agent != original.Agent {
		t.Errorf("Agent: got %q, want %q", decoded.Agent, original.Agent)
	}
	if decoded.ProblemStatement != original.ProblemStatement {
		t.Errorf("ProblemStatement: got %q, want %q", decoded.ProblemStatement, original.ProblemStatement)
	}
	if len(decoded.Actors) != len(original.Actors) {
		t.Fatalf("Actors length: got %d, want %d", len(decoded.Actors), len(original.Actors))
	}
	if decoded.Actors[0].Type != "human" {
		t.Errorf("Actors[0].Type: got %q, want %q", decoded.Actors[0].Type, "human")
	}
	if decoded.Assumptions[0].QuestionForUser == nil || *decoded.Assumptions[0].QuestionForUser != "Do all users have email?" {
		t.Errorf("Assumptions[0].QuestionForUser not preserved")
	}
	if len(decoded.Scope.InScope) != 1 || decoded.Scope.InScope[0] != "login flow" {
		t.Errorf("Scope.InScope not preserved")
	}
	if len(decoded.IntegrationPoints) != 1 || decoded.IntegrationPoints[0].Direction != "outbound" {
		t.Errorf("IntegrationPoints not preserved")
	}
}

func TestAgentOutput_DrafterOutput_JSONRoundTrip(t *testing.T) {
	original := validDrafterOutput()

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded DrafterOutput
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.SpecFile != original.SpecFile {
		t.Errorf("SpecFile: got %q, want %q", decoded.SpecFile, original.SpecFile)
	}
	if decoded.HoldoutFile != original.HoldoutFile {
		t.Errorf("HoldoutFile: got %q, want %q", decoded.HoldoutFile, original.HoldoutFile)
	}
	if len(decoded.AmbiguityWarnings) != 1 {
		t.Fatalf("AmbiguityWarnings length: got %d, want 1", len(decoded.AmbiguityWarnings))
	}
	if decoded.AmbiguityWarnings[0].ID != "AMB-W-001" {
		t.Errorf("AmbiguityWarnings[0].ID: got %q, want %q", decoded.AmbiguityWarnings[0].ID, "AMB-W-001")
	}
	if decoded.StructuralSummary.BDDScenarioCount != 5 {
		t.Errorf("StructuralSummary.BDDScenarioCount: got %d, want 5", decoded.StructuralSummary.BDDScenarioCount)
	}
}

func TestAgentOutput_ReviewerOutput_JSONRoundTrip(t *testing.T) {
	original := validReviewerOutput()
	original.Findings[0].ConstitutionPrinciple = strPtr("PRINCIPLE-SEC-01")

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded ReviewerOutput
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.Round != 1 {
		t.Errorf("Round: got %d, want 1", decoded.Round)
	}
	if len(decoded.Findings) != 1 {
		t.Fatalf("Findings length: got %d, want 1", len(decoded.Findings))
	}
	if decoded.Findings[0].Severity != SeverityCritical {
		t.Errorf("Severity: got %v, want CRITICAL", decoded.Findings[0].Severity)
	}
	if decoded.Findings[0].ConstitutionPrinciple == nil || *decoded.Findings[0].ConstitutionPrinciple != "PRINCIPLE-SEC-01" {
		t.Errorf("ConstitutionPrinciple not preserved")
	}
	if !decoded.StructuralIntegrity.Performed {
		t.Errorf("StructuralIntegrity.Performed: got false, want true")
	}
}

func TestAgentOutput_RevisionOutput_JSONRoundTrip(t *testing.T) {
	original := validRevisionOutput()
	original.DismissalRequests = []DismissalRequest{
		{FindingID: "F-002", Rationale: "Out of scope"},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded RevisionOutput
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.RevisedSpecFile != "spec-v2.md" {
		t.Errorf("RevisedSpecFile: got %q, want %q", decoded.RevisedSpecFile, "spec-v2.md")
	}
	if len(decoded.Changes) != 1 || decoded.Changes[0].Action != "revised" {
		t.Errorf("Changes not preserved")
	}
	if len(decoded.DismissalRequests) != 1 || decoded.DismissalRequests[0].FindingID != "F-002" {
		t.Errorf("DismissalRequests not preserved")
	}
}

func TestAgentOutput_JudgeOutput_JSONRoundTrip(t *testing.T) {
	original := validJudgeOutput()
	original.Downgrades = []Downgrade{
		{
			FindingID:    "F-001",
			FromSeverity: SeverityCritical,
			ToSeverity:   SeverityMinor,
			ReasonCode:   "DUPLICATE_OF",
			ReasonDetail: "Same as F-003",
		},
	}
	original.StructuralDelta = StructuralDelta{
		RegressionsFound: true,
		Details:          []string{"Lost BDD scenario for edge case"},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded JudgeOutput
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.Verdict != VerdictPass {
		t.Errorf("Verdict: got %v, want PASS", decoded.Verdict)
	}
	if decoded.Rationale != original.Rationale {
		t.Errorf("Rationale: got %q, want %q", decoded.Rationale, original.Rationale)
	}
	if len(decoded.Downgrades) != 1 {
		t.Fatalf("Downgrades length: got %d, want 1", len(decoded.Downgrades))
	}
	if decoded.Downgrades[0].FromSeverity != SeverityCritical {
		t.Errorf("FromSeverity: got %v, want CRITICAL", decoded.Downgrades[0].FromSeverity)
	}
	if decoded.Downgrades[0].ToSeverity != SeverityMinor {
		t.Errorf("ToSeverity: got %v, want MINOR", decoded.Downgrades[0].ToSeverity)
	}
	if !decoded.StructuralDelta.RegressionsFound {
		t.Errorf("RegressionsFound: got false, want true")
	}
}

func TestAgentOutput_MergedFindings_JSONRoundTrip(t *testing.T) {
	original := MergedFindings{
		SchemaVersion:    "1.0",
		Round:            2,
		Timestamp:        "2025-01-15T12:00:00Z",
		TotalFindings:    10,
		TotalAfterDedup:  8,
		DuplicatesMerged: 2,
		FindingsRejected: 1,
		Findings: []MergedFinding{
			{
				ID:                 "MF-001",
				SourceIDs:          []string{"F-001", "F-005"},
				RaisedBy:           []string{"reviewer-sec", "reviewer-test"},
				Description:        "Missing input validation",
				Severity:           SeverityCritical,
				Impact:             "SQL injection",
				Recommendation:     "Add parameterised queries",
				Lens:               "security",
				AffectedSection:    "FR-3",
				Status:             "open",
				RoundRaised:        1,
				RoundClosed:        intPtr(2),
				ResolutionNotes:    strPtr("Fixed in v2"),
				DismissalRationale: nil,
			},
		},
		DedupLog: []DedupEntry{
			{KeptID: "MF-001", MergedID: "F-005", Reason: "Same root cause"},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded MergedFindings
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.TotalFindings != 10 {
		t.Errorf("TotalFindings: got %d, want 10", decoded.TotalFindings)
	}
	if decoded.TotalAfterDedup != 8 {
		t.Errorf("TotalAfterDedup: got %d, want 8", decoded.TotalAfterDedup)
	}
	if decoded.DuplicatesMerged != 2 {
		t.Errorf("DuplicatesMerged: got %d, want 2", decoded.DuplicatesMerged)
	}
	if len(decoded.Findings) != 1 {
		t.Fatalf("Findings length: got %d, want 1", len(decoded.Findings))
	}
	mf := decoded.Findings[0]
	if mf.ID != "MF-001" {
		t.Errorf("MergedFinding.ID: got %q, want %q", mf.ID, "MF-001")
	}
	if len(mf.SourceIDs) != 2 {
		t.Errorf("SourceIDs length: got %d, want 2", len(mf.SourceIDs))
	}
	if mf.Severity != SeverityCritical {
		t.Errorf("Severity: got %v, want CRITICAL", mf.Severity)
	}
	if mf.RoundClosed == nil || *mf.RoundClosed != 2 {
		t.Errorf("RoundClosed not preserved")
	}
	if mf.ResolutionNotes == nil || *mf.ResolutionNotes != "Fixed in v2" {
		t.Errorf("ResolutionNotes not preserved")
	}
	if len(decoded.DedupLog) != 1 || decoded.DedupLog[0].KeptID != "MF-001" {
		t.Errorf("DedupLog not preserved")
	}
}

// ---------------------------------------------------------------------------
// JSON field name verification
// ---------------------------------------------------------------------------

func TestAgentOutput_DiscoveryOutput_JSONFieldNames(t *testing.T) {
	o := validDiscoveryOutput()
	data, err := json.Marshal(o)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal to map: %v", err)
	}
	requiredKeys := []string{
		"schema_version", "agent", "actors", "problem_statement",
		"scope", "constraints", "integration_points", "priorities",
		"assumptions", "open_questions",
	}
	for _, key := range requiredKeys {
		if _, ok := raw[key]; !ok {
			t.Errorf("missing JSON key %q", key)
		}
	}
}

func TestAgentOutput_FindingJSONFieldNames(t *testing.T) {
	f := Finding{
		ID:              "F-001",
		Description:     "test",
		Severity:        SeverityMinor,
		Impact:          "low",
		Recommendation:  "fix",
		Lens:            "security",
		AffectedSection: "FR-1",
	}
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal to map: %v", err)
	}
	requiredKeys := []string{
		"id", "description", "severity", "impact",
		"recommendation", "lens", "affected_section",
	}
	for _, key := range requiredKeys {
		if _, ok := raw[key]; !ok {
			t.Errorf("missing JSON key %q", key)
		}
	}
	// constitution_principle should be omitted when nil
	if _, ok := raw["constitution_principle"]; ok {
		t.Errorf("constitution_principle should be omitted when nil")
	}
}

// ---------------------------------------------------------------------------
// Validation collects ALL errors (not just first)
// ---------------------------------------------------------------------------

func TestAgentOutput_ValidationCollectsAllErrors(t *testing.T) {
	// JudgeOutput with multiple errors: empty schema_version, agent,
	// rationale, round=0, bad issue status, bad downgrade reason.
	o := &JudgeOutput{
		IssueUpdates: []IssueUpdate{
			{FindingID: "", NewStatus: "invalid", Explanation: ""},
		},
		Downgrades: []Downgrade{
			{FindingID: "", ReasonCode: "BOGUS", ReasonDetail: ""},
		},
	}
	errs := ValidateJudgeOutput(o)
	// Should have at least 7 errors:
	//   schema_version, agent, round, rationale,
	//   issue_updates[0].finding_id, issue_updates[0].new_status, issue_updates[0].explanation,
	//   downgrades[0].finding_id, downgrades[0].reason_code, downgrades[0].reason_detail
	if len(errs) < 7 {
		t.Errorf("expected >= 7 errors, got %d: %v", len(errs), errs)
	}
}

// ---------------------------------------------------------------------------
// MergedFindings validation tests
// ---------------------------------------------------------------------------

func TestAgentOutput_ValidateMergedFindings_Valid(t *testing.T) {
	o := &MergedFindings{
		Round:     1,
		Timestamp: "2025-01-15T12:00:00Z",
		Findings:  []MergedFinding{},
	}
	errs := ValidateMergedFindings(o)
	if len(errs) != 0 {
		t.Errorf("expected no errors, got %d: %v", len(errs), errs)
	}
}

func TestAgentOutput_ValidateMergedFindings_EmptyFields(t *testing.T) {
	o := &MergedFindings{}
	errs := ValidateMergedFindings(o)
	if len(errs) == 0 {
		t.Fatal("expected errors for empty MergedFindings")
	}
	wantSubstrings := []string{"round", "timestamp", "findings"}
	for _, w := range wantSubstrings {
		found := false
		for _, e := range errs {
			if strings.Contains(e.Error(), w) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected error mentioning %q, not found in %v", w, errs)
		}
	}
}

func TestAgentOutput_ValidateMergedFindings_ZeroRound(t *testing.T) {
	o := &MergedFindings{
		Round:     0,
		Timestamp: "2025-01-15T12:00:00Z",
		Findings:  []MergedFinding{},
	}
	errs := ValidateMergedFindings(o)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error for zero round, got %d: %v", len(errs), errs)
	}
	if !strings.Contains(errs[0].Error(), "round") {
		t.Errorf("expected round error, got: %v", errs[0])
	}
}

func TestAgentOutput_ValidateMergedFindings_NilFindings(t *testing.T) {
	o := &MergedFindings{
		Round:     1,
		Timestamp: "2025-01-15T12:00:00Z",
		Findings:  nil,
	}
	errs := ValidateMergedFindings(o)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error for nil findings, got %d: %v", len(errs), errs)
	}
	if !strings.Contains(errs[0].Error(), "findings") {
		t.Errorf("expected findings error, got: %v", errs[0])
	}
}
