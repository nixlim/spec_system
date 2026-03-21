package specworkflow

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// makeReviewerOutput builds a ReviewerOutput with the given agent name and findings.
func makeReviewerOutput(agent string, findings []Finding) *ReviewerOutput {
	return &ReviewerOutput{
		SchemaVersion:      "1.0",
		Agent:              agent,
		Round:              1,
		LensesApplied:      []string{"security"},
		MarkdownReportFile: "review.md",
		Findings:           findings,
		StructuralIntegrity: StructuralIntegrity{
			Performed: true,
			Checks:    []IntegrityCheck{{Check: "BDD coverage", Result: "PASS"}},
		},
	}
}

// makeTestFinding builds a Finding with the given parameters.
func makeTestFinding(id, section, lens string, sev Severity, principle *string) Finding {
	return Finding{
		ID:                    id,
		Description:           "Description for " + id,
		Severity:              sev,
		Impact:                "Impact for " + id,
		Recommendation:        "Recommendation for " + id,
		Lens:                  lens,
		AffectedSection:       section,
		ConstitutionPrinciple: principle,
	}
}

// ---------------------------------------------------------------------------
// normalizeSection tests
// ---------------------------------------------------------------------------

func TestMerge_NormalizeSection(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"simple", "FR-1", "fr-1"},
		{"leading trailing spaces", "  FR-1  ", "fr-1"},
		{"internal whitespace", "FR   1", "fr 1"},
		{"tabs and newlines", " FR\t\n  1 ", "fr 1"},
		{"mixed case", "Section A.1", "section a.1"},
		{"empty", "", ""},
		{"only whitespace", "   ", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeSection(tc.in)
			if got != tc.want {
				t.Errorf("normalizeSection(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// MergeReviewerOutputs tests
// ---------------------------------------------------------------------------

func TestMerge_NoDuplicates(t *testing.T) {
	// 4 reviewers, each with a unique finding.
	outputs := []*ReviewerOutput{
		makeReviewerOutput("reviewer-alpha", []Finding{
			makeTestFinding("F-001", "Section A", "security", SeverityCritical, strPtr("P1")),
		}),
		makeReviewerOutput("reviewer-beta", []Finding{
			makeTestFinding("F-001", "Section B", "testability", SeverityMajor, strPtr("P2")),
		}),
		makeReviewerOutput("reviewer-gamma", []Finding{
			makeTestFinding("F-001", "Section C", "clarity", SeverityMinor, strPtr("P3")),
		}),
		makeReviewerOutput("reviewer-delta", []Finding{
			makeTestFinding("F-001", "Section D", "completeness", SeverityObservation, strPtr("P4")),
		}),
	}

	result, err := MergeReviewerOutputs(outputs, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.TotalFindings != 4 {
		t.Errorf("TotalFindings = %d, want 4", result.TotalFindings)
	}
	if result.TotalAfterDedup != 4 {
		t.Errorf("TotalAfterDedup = %d, want 4", result.TotalAfterDedup)
	}
	if result.DuplicatesMerged != 0 {
		t.Errorf("DuplicatesMerged = %d, want 0", result.DuplicatesMerged)
	}

	// Check global IDs: 1 CRIT, 1 MAJ, 1 MIN, 1 OBS.
	wantIDs := map[string]bool{
		"CRIT-001": false,
		"MAJ-001":  false,
		"MIN-001":  false,
		"OBS-001":  false,
	}
	for _, f := range result.Findings {
		if _, ok := wantIDs[f.ID]; !ok {
			t.Errorf("unexpected finding ID: %s", f.ID)
		} else {
			wantIDs[f.ID] = true
		}
	}
	for id, found := range wantIDs {
		if !found {
			t.Errorf("expected finding ID %s not present", id)
		}
	}
}

func TestMerge_DuplicatesMergeHigherSeverity(t *testing.T) {
	// Two reviewers raise findings on the same section+lens+principle.
	// Reviewer A: MAJOR, Reviewer B: CRITICAL -> result should be CRITICAL.
	principle := strPtr("consistency")
	outputs := []*ReviewerOutput{
		makeReviewerOutput("reviewer-A", []Finding{
			makeTestFinding("F-001", "Section X", "security", SeverityMajor, principle),
		}),
		makeReviewerOutput("reviewer-B", []Finding{
			makeTestFinding("F-010", "Section X", "security", SeverityCritical, principle),
		}),
	}

	result, err := MergeReviewerOutputs(outputs, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.TotalAfterDedup != 1 {
		t.Fatalf("TotalAfterDedup = %d, want 1", result.TotalAfterDedup)
	}
	if result.DuplicatesMerged != 1 {
		t.Errorf("DuplicatesMerged = %d, want 1", result.DuplicatesMerged)
	}

	merged := result.Findings[0]
	if merged.Severity != SeverityCritical {
		t.Errorf("Severity = %v, want CRITICAL", merged.Severity)
	}
	if len(merged.SourceIDs) != 2 {
		t.Errorf("len(SourceIDs) = %d, want 2", len(merged.SourceIDs))
	}
	if len(merged.RaisedBy) != 2 {
		t.Errorf("len(RaisedBy) = %d, want 2", len(merged.RaisedBy))
	}
}

func TestMerge_RecommendationConcatenation(t *testing.T) {
	principle := strPtr("clarity")
	outputs := []*ReviewerOutput{
		makeReviewerOutput("reviewer-alpha", []Finding{
			makeTestFinding("F-001", "Section Y", "clarity", SeverityMinor, principle),
		}),
		makeReviewerOutput("reviewer-beta", []Finding{
			makeTestFinding("F-002", "Section Y", "clarity", SeverityMinor, principle),
		}),
	}

	result, err := MergeReviewerOutputs(outputs, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.TotalAfterDedup != 1 {
		t.Fatalf("TotalAfterDedup = %d, want 1", result.TotalAfterDedup)
	}

	rec := result.Findings[0].Recommendation
	if !strings.Contains(rec, "From reviewer-alpha:") {
		t.Errorf("recommendation missing reviewer-alpha attribution: %s", rec)
	}
	if !strings.Contains(rec, "From reviewer-beta:") {
		t.Errorf("recommendation missing reviewer-beta attribution: %s", rec)
	}
}

func TestMerge_CaseInsensitiveSectionMatch(t *testing.T) {
	// "SECTION A" and "section a" should be treated as duplicates.
	outputs := []*ReviewerOutput{
		makeReviewerOutput("reviewer-1", []Finding{
			makeTestFinding("F-001", "SECTION A", "security", SeverityMinor, nil),
		}),
		makeReviewerOutput("reviewer-2", []Finding{
			makeTestFinding("F-002", "section a", "security", SeverityMinor, nil),
		}),
	}

	result, err := MergeReviewerOutputs(outputs, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.TotalAfterDedup != 1 {
		t.Errorf("TotalAfterDedup = %d, want 1 (case-insensitive match failed)", result.TotalAfterDedup)
	}
	if result.DuplicatesMerged != 1 {
		t.Errorf("DuplicatesMerged = %d, want 1", result.DuplicatesMerged)
	}
}

func TestMerge_WhitespaceNormalization(t *testing.T) {
	// "Section   A" and "Section A" should be treated as duplicates.
	outputs := []*ReviewerOutput{
		makeReviewerOutput("reviewer-1", []Finding{
			makeTestFinding("F-001", "  Section   A  ", "security", SeverityMajor, nil),
		}),
		makeReviewerOutput("reviewer-2", []Finding{
			makeTestFinding("F-002", "Section A", "security", SeverityMajor, nil),
		}),
	}

	result, err := MergeReviewerOutputs(outputs, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.TotalAfterDedup != 1 {
		t.Errorf("TotalAfterDedup = %d, want 1 (whitespace normalisation failed)", result.TotalAfterDedup)
	}
}

func TestMerge_NullConstitutionPrincipleMatchesAnything(t *testing.T) {
	// Finding with nil principle and finding with a principle on the same
	// section+lens should be considered duplicates (nil matches anything).
	outputs := []*ReviewerOutput{
		makeReviewerOutput("reviewer-1", []Finding{
			makeTestFinding("F-001", "Section Z", "security", SeverityMinor, nil),
		}),
		makeReviewerOutput("reviewer-2", []Finding{
			makeTestFinding("F-002", "Section Z", "security", SeverityMinor, strPtr("principle-X")),
		}),
	}

	result, err := MergeReviewerOutputs(outputs, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Per spec: "same constitution_principle (or either is null/empty)" -> duplicates.
	if result.TotalAfterDedup != 1 {
		t.Errorf("TotalAfterDedup = %d, want 1 (null principle should match anything)", result.TotalAfterDedup)
	}
}

func TestMerge_SeverityOrdering(t *testing.T) {
	// Findings of each severity. The output should be ordered CRIT, MAJ, MIN, OBS.
	outputs := []*ReviewerOutput{
		makeReviewerOutput("reviewer-1", []Finding{
			makeTestFinding("F-OBS", "Section D", "security", SeverityObservation, strPtr("p1")),
			makeTestFinding("F-MAJ", "Section B", "clarity", SeverityMajor, strPtr("p2")),
			makeTestFinding("F-CRIT", "Section A", "testability", SeverityCritical, strPtr("p3")),
			makeTestFinding("F-MIN", "Section C", "completeness", SeverityMinor, strPtr("p4")),
		}),
	}

	result, err := MergeReviewerOutputs(outputs, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Findings) != 4 {
		t.Fatalf("expected 4 findings, got %d", len(result.Findings))
	}

	expectedOrder := []Severity{SeverityCritical, SeverityMajor, SeverityMinor, SeverityObservation}
	for i, f := range result.Findings {
		if f.Severity != expectedOrder[i] {
			t.Errorf("findings[%d].Severity = %v, want %v", i, f.Severity, expectedOrder[i])
		}
	}
}

func TestMerge_AlphabeticalWithinSeverity(t *testing.T) {
	// Multiple findings at the same severity should be sorted alphabetically
	// by affected_section (normalised).
	outputs := []*ReviewerOutput{
		makeReviewerOutput("reviewer-1", []Finding{
			makeTestFinding("F-001", "Zeta Section", "security", SeverityMajor, strPtr("p1")),
			makeTestFinding("F-002", "Alpha Section", "clarity", SeverityMajor, strPtr("p2")),
			makeTestFinding("F-003", "Middle Section", "testability", SeverityMajor, strPtr("p3")),
		}),
	}

	result, err := MergeReviewerOutputs(outputs, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedSections := []string{"Alpha Section", "Middle Section", "Zeta Section"}
	for i, f := range result.Findings {
		if normalizeSection(f.AffectedSection) != normalizeSection(expectedSections[i]) {
			t.Errorf("findings[%d].AffectedSection = %q, want %q",
				i, f.AffectedSection, expectedSections[i])
		}
	}

	// IDs should be MAJ-001, MAJ-002, MAJ-003.
	for i, f := range result.Findings {
		wantID := "MAJ-" + []string{"001", "002", "003"}[i]
		if f.ID != wantID {
			t.Errorf("findings[%d].ID = %q, want %q", i, f.ID, wantID)
		}
	}
}

func TestMerge_DedupLogRecordsMerges(t *testing.T) {
	principle := strPtr("P1")
	outputs := []*ReviewerOutput{
		makeReviewerOutput("reviewer-A", []Finding{
			makeTestFinding("F-001", "Section X", "security", SeverityMajor, principle),
		}),
		makeReviewerOutput("reviewer-B", []Finding{
			makeTestFinding("F-010", "Section X", "security", SeverityMajor, principle),
		}),
		makeReviewerOutput("reviewer-C", []Finding{
			makeTestFinding("F-020", "Section X", "security", SeverityMajor, principle),
		}),
	}

	result, err := MergeReviewerOutputs(outputs, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Two merges should be logged (F-010 -> F-001, F-020 -> F-001).
	if len(result.DedupLog) != 2 {
		t.Fatalf("len(DedupLog) = %d, want 2", len(result.DedupLog))
	}

	for _, entry := range result.DedupLog {
		if entry.KeptID != "F-001" {
			t.Errorf("DedupLog entry KeptID = %q, want %q", entry.KeptID, "F-001")
		}
		if entry.Reason == "" {
			t.Error("DedupLog entry Reason is empty")
		}
	}
}

func TestMerge_RejectedFindingsCounted(t *testing.T) {
	// A finding with an empty recommendation should be rejected.
	outputs := []*ReviewerOutput{
		makeReviewerOutput("reviewer-1", []Finding{
			{
				ID:              "F-001",
				Description:     "Valid finding",
				Severity:        SeverityMajor,
				Impact:          "Some impact",
				Recommendation:  "Fix it",
				Lens:            "security",
				AffectedSection: "Section A",
			},
			{
				ID:              "F-002",
				Description:     "Invalid finding",
				Severity:        SeverityMinor,
				Impact:          "Some impact",
				Recommendation:  "", // Missing recommendation -> rejected.
				Lens:            "clarity",
				AffectedSection: "Section B",
			},
		}),
	}

	result, err := MergeReviewerOutputs(outputs, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.FindingsRejected != 1 {
		t.Errorf("FindingsRejected = %d, want 1", result.FindingsRejected)
	}
	if result.TotalFindings != 2 {
		t.Errorf("TotalFindings = %d, want 2", result.TotalFindings)
	}
	if result.TotalAfterDedup != 1 {
		t.Errorf("TotalAfterDedup = %d, want 1", result.TotalAfterDedup)
	}
}

func TestMerge_Determinism(t *testing.T) {
	// Running the merge twice with the same inputs must produce the same output
	// (ignoring timestamp).
	principle := strPtr("P1")
	buildOutputs := func() []*ReviewerOutput {
		return []*ReviewerOutput{
			makeReviewerOutput("reviewer-A", []Finding{
				makeTestFinding("F-001", "Section B", "security", SeverityMajor, principle),
				makeTestFinding("F-002", "Section A", "clarity", SeverityCritical, strPtr("P2")),
			}),
			makeReviewerOutput("reviewer-B", []Finding{
				makeTestFinding("F-010", "Section B", "security", SeverityMinor, principle),
				makeTestFinding("F-011", "Section C", "testability", SeverityObservation, strPtr("P3")),
			}),
		}
	}

	result1, err := MergeReviewerOutputs(buildOutputs(), 1)
	if err != nil {
		t.Fatalf("run 1 error: %v", err)
	}
	result2, err := MergeReviewerOutputs(buildOutputs(), 1)
	if err != nil {
		t.Fatalf("run 2 error: %v", err)
	}

	if len(result1.Findings) != len(result2.Findings) {
		t.Fatalf("different finding counts: %d vs %d", len(result1.Findings), len(result2.Findings))
	}

	for i := range result1.Findings {
		f1, f2 := result1.Findings[i], result2.Findings[i]
		if f1.ID != f2.ID {
			t.Errorf("findings[%d].ID: %q vs %q", i, f1.ID, f2.ID)
		}
		if f1.Severity != f2.Severity {
			t.Errorf("findings[%d].Severity: %v vs %v", i, f1.Severity, f2.Severity)
		}
		if f1.AffectedSection != f2.AffectedSection {
			t.Errorf("findings[%d].AffectedSection: %q vs %q", i, f1.AffectedSection, f2.AffectedSection)
		}
		if f1.Recommendation != f2.Recommendation {
			t.Errorf("findings[%d].Recommendation differs", i)
		}
	}

	if len(result1.DedupLog) != len(result2.DedupLog) {
		t.Errorf("DedupLog lengths differ: %d vs %d", len(result1.DedupLog), len(result2.DedupLog))
	}
}

func TestMerge_SingleReviewer(t *testing.T) {
	outputs := []*ReviewerOutput{
		makeReviewerOutput("only-reviewer", []Finding{
			makeTestFinding("F-001", "Section A", "security", SeverityCritical, nil),
			makeTestFinding("F-002", "Section B", "clarity", SeverityMinor, nil),
		}),
	}

	result, err := MergeReviewerOutputs(outputs, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.TotalFindings != 2 {
		t.Errorf("TotalFindings = %d, want 2", result.TotalFindings)
	}
	if result.TotalAfterDedup != 2 {
		t.Errorf("TotalAfterDedup = %d, want 2", result.TotalAfterDedup)
	}
	if result.DuplicatesMerged != 0 {
		t.Errorf("DuplicatesMerged = %d, want 0", result.DuplicatesMerged)
	}

	// Verify source attribution.
	for _, f := range result.Findings {
		if len(f.RaisedBy) != 1 || f.RaisedBy[0] != "only-reviewer" {
			t.Errorf("RaisedBy = %v, want [only-reviewer]", f.RaisedBy)
		}
	}
}

func TestMerge_EmptyFindings(t *testing.T) {
	outputs := []*ReviewerOutput{
		makeReviewerOutput("reviewer-1", []Finding{}),
		makeReviewerOutput("reviewer-2", []Finding{}),
	}

	result, err := MergeReviewerOutputs(outputs, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.TotalFindings != 0 {
		t.Errorf("TotalFindings = %d, want 0", result.TotalFindings)
	}
	if result.TotalAfterDedup != 0 {
		t.Errorf("TotalAfterDedup = %d, want 0", result.TotalAfterDedup)
	}
	if len(result.Findings) != 0 {
		t.Errorf("len(Findings) = %d, want 0", len(result.Findings))
	}
}

func TestMerge_NoOutputsReturnsError(t *testing.T) {
	_, err := MergeReviewerOutputs(nil, 1)
	if err == nil {
		t.Error("expected error for nil outputs, got nil")
	}

	_, err = MergeReviewerOutputs([]*ReviewerOutput{}, 1)
	if err == nil {
		t.Error("expected error for empty outputs, got nil")
	}
}

func TestMerge_RoundMetadata(t *testing.T) {
	outputs := []*ReviewerOutput{
		makeReviewerOutput("reviewer-1", []Finding{
			makeTestFinding("F-001", "Section A", "security", SeverityMajor, nil),
		}),
	}

	result, err := MergeReviewerOutputs(outputs, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Round != 3 {
		t.Errorf("Round = %d, want 3", result.Round)
	}
	for _, f := range result.Findings {
		if f.RoundRaised != 3 {
			t.Errorf("RoundRaised = %d, want 3", f.RoundRaised)
		}
		if f.Status != "open" {
			t.Errorf("Status = %q, want %q", f.Status, "open")
		}
	}
}

func TestMerge_EmptyConstitutionPrincipleMatchesEmpty(t *testing.T) {
	// Both findings have empty string principle — should match.
	empty := ""
	outputs := []*ReviewerOutput{
		makeReviewerOutput("reviewer-1", []Finding{
			makeTestFinding("F-001", "Section A", "security", SeverityMajor, &empty),
		}),
		makeReviewerOutput("reviewer-2", []Finding{
			makeTestFinding("F-002", "Section A", "security", SeverityMajor, &empty),
		}),
	}

	result, err := MergeReviewerOutputs(outputs, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.TotalAfterDedup != 1 {
		t.Errorf("TotalAfterDedup = %d, want 1 (empty string principles should match)", result.TotalAfterDedup)
	}
}

// ---------------------------------------------------------------------------
// Dual-provider merge tests
// ---------------------------------------------------------------------------

func TestMerge_CrossProviderDuplicate(t *testing.T) {
	// Claude reviewer finds (Sec3, AMB, MAJOR), codex reviewer finds (Sec3, AMB, CRITICAL).
	// Result: one finding, raised_by=[both], severity=CRITICAL (higher wins).
	principle := strPtr("P1")
	outputs := []*ReviewerOutput{
		makeReviewerOutput("reviewer-clarity-claude", []Finding{
			makeTestFinding("F-001", "Section 3", "AMB", SeverityMajor, principle),
		}),
		makeReviewerOutput("reviewer-clarity-codex", []Finding{
			makeTestFinding("F-002", "Section 3", "AMB", SeverityCritical, principle),
		}),
	}

	result, err := MergeReviewerOutputs(outputs, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.TotalAfterDedup != 1 {
		t.Fatalf("TotalAfterDedup = %d, want 1", result.TotalAfterDedup)
	}

	f := result.Findings[0]
	if f.Severity != SeverityCritical {
		t.Errorf("Severity = %v, want CRITICAL", f.Severity)
	}
	if len(f.RaisedBy) != 2 {
		t.Errorf("len(RaisedBy) = %d, want 2", len(f.RaisedBy))
	}
	// Verify both providers represented.
	hasClaud, hasCodex := false, false
	for _, r := range f.RaisedBy {
		if r == "reviewer-clarity-claude" {
			hasClaud = true
		}
		if r == "reviewer-clarity-codex" {
			hasCodex = true
		}
	}
	if !hasClaud || !hasCodex {
		t.Errorf("RaisedBy = %v, want both claude and codex reviewers", f.RaisedBy)
	}
}

func TestMerge_SingleProviderFinding(t *testing.T) {
	// Only codex reviewer raises a finding.
	outputs := []*ReviewerOutput{
		makeReviewerOutput("reviewer-security-claude", []Finding{}),
		makeReviewerOutput("reviewer-security-codex", []Finding{
			makeTestFinding("F-001", "Section A", "SEC", SeverityMajor, nil),
		}),
	}

	result, err := MergeReviewerOutputs(outputs, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.TotalAfterDedup != 1 {
		t.Fatalf("TotalAfterDedup = %d, want 1", result.TotalAfterDedup)
	}
	if len(result.Findings[0].RaisedBy) != 1 {
		t.Errorf("len(RaisedBy) = %d, want 1", len(result.Findings[0].RaisedBy))
	}
	if result.Findings[0].RaisedBy[0] != "reviewer-security-codex" {
		t.Errorf("RaisedBy[0] = %q, want %q", result.Findings[0].RaisedBy[0], "reviewer-security-codex")
	}
}

func TestMerge_SameSectionDifferentLens(t *testing.T) {
	// Same section but different lens = NOT deduplicated.
	outputs := []*ReviewerOutput{
		makeReviewerOutput("reviewer-clarity-claude", []Finding{
			makeTestFinding("F-001", "Section A", "AMB", SeverityMajor, nil),
		}),
		makeReviewerOutput("reviewer-security-codex", []Finding{
			makeTestFinding("F-002", "Section A", "SEC", SeverityMajor, nil),
		}),
	}

	result, err := MergeReviewerOutputs(outputs, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.TotalAfterDedup != 2 {
		t.Errorf("TotalAfterDedup = %d, want 2 (different lenses should not deduplicate)", result.TotalAfterDedup)
	}
	if result.DuplicatesMerged != 0 {
		t.Errorf("DuplicatesMerged = %d, want 0", result.DuplicatesMerged)
	}
}

func TestMerge_EightInputs(t *testing.T) {
	// 8 reviewer outputs (simulating dual-provider team) merged without error.
	lenses := []string{"clarity", "consistency", "security", "correctness"}
	var outputs []*ReviewerOutput
	for _, lens := range lenses {
		outputs = append(outputs,
			makeReviewerOutput("reviewer-"+lens+"-claude", []Finding{
				makeTestFinding("F-"+lens+"-claude", "Section "+lens, lens, SeverityMajor, nil),
			}),
			makeReviewerOutput("reviewer-"+lens+"-codex", []Finding{
				makeTestFinding("F-"+lens+"-codex", "Section "+lens, lens, SeverityCritical, nil),
			}),
		)
	}

	result, err := MergeReviewerOutputs(outputs, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Each lens pair should deduplicate to 1 finding = 4 total.
	if result.TotalAfterDedup != 4 {
		t.Errorf("TotalAfterDedup = %d, want 4", result.TotalAfterDedup)
	}
	if result.DuplicatesMerged != 4 {
		t.Errorf("DuplicatesMerged = %d, want 4", result.DuplicatesMerged)
	}

	// All should be CRITICAL (higher severity wins).
	for _, f := range result.Findings {
		if f.Severity != SeverityCritical {
			t.Errorf("finding %q: Severity = %v, want CRITICAL", f.ID, f.Severity)
		}
		if len(f.RaisedBy) != 2 {
			t.Errorf("finding %q: len(RaisedBy) = %d, want 2", f.ID, len(f.RaisedBy))
		}
	}
}

func TestMerge_DedupLogProviderAttribution(t *testing.T) {
	// Dedup log records both provider-suffixed reviewer names.
	principle := strPtr("P1")
	outputs := []*ReviewerOutput{
		makeReviewerOutput("reviewer-clarity-claude", []Finding{
			makeTestFinding("F-001", "Section X", "AMB", SeverityMajor, principle),
		}),
		makeReviewerOutput("reviewer-clarity-codex", []Finding{
			makeTestFinding("F-002", "Section X", "AMB", SeverityCritical, principle),
		}),
	}

	result, err := MergeReviewerOutputs(outputs, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.DedupLog) != 1 {
		t.Fatalf("len(DedupLog) = %d, want 1", len(result.DedupLog))
	}

	entry := result.DedupLog[0]
	if entry.KeptID != "F-001" {
		t.Errorf("DedupLog KeptID = %q, want %q", entry.KeptID, "F-001")
	}
	if entry.MergedID != "F-002" {
		t.Errorf("DedupLog MergedID = %q, want %q", entry.MergedID, "F-002")
	}
	if entry.Reason == "" {
		t.Error("DedupLog Reason is empty")
	}
}

// ---------------------------------------------------------------------------
// Original merge tests
// ---------------------------------------------------------------------------

func TestMerge_ThreeWayDuplicate(t *testing.T) {
	// Three reviewers raise the same finding. Should merge into one
	// with 3 source_ids and 3 raised_by entries.
	outputs := []*ReviewerOutput{
		makeReviewerOutput("reviewer-A", []Finding{
			makeTestFinding("F-A1", "Section X", "security", SeverityMinor, nil),
		}),
		makeReviewerOutput("reviewer-B", []Finding{
			makeTestFinding("F-B1", "Section X", "security", SeverityMajor, nil),
		}),
		makeReviewerOutput("reviewer-C", []Finding{
			makeTestFinding("F-C1", "Section X", "security", SeverityCritical, nil),
		}),
	}

	result, err := MergeReviewerOutputs(outputs, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.TotalAfterDedup != 1 {
		t.Fatalf("TotalAfterDedup = %d, want 1", result.TotalAfterDedup)
	}

	f := result.Findings[0]
	if f.Severity != SeverityCritical {
		t.Errorf("Severity = %v, want CRITICAL (highest of three)", f.Severity)
	}
	if len(f.SourceIDs) != 3 {
		t.Errorf("len(SourceIDs) = %d, want 3", len(f.SourceIDs))
	}
	if len(f.RaisedBy) != 3 {
		t.Errorf("len(RaisedBy) = %d, want 3", len(f.RaisedBy))
	}
	if result.DuplicatesMerged != 2 {
		t.Errorf("DuplicatesMerged = %d, want 2", result.DuplicatesMerged)
	}
	if len(result.DedupLog) != 2 {
		t.Errorf("len(DedupLog) = %d, want 2", len(result.DedupLog))
	}
}
