package codedoc

import (
	"encoding/json"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// DiscoveryOutput JSON schema conformance
// ---------------------------------------------------------------------------

func TestSchemasDiscoveryOutputJSONMatchesSection9(t *testing.T) {
	d := DiscoveryOutput{
		SchemaVersion: "1.0",
		Agent:         "codedoc-discovery",
		Mode:          "full",
		CompletionStatus: CompletionStatus{
			Status: "complete",
			Reason: "Inventory complete",
		},
		ToolsUsed: []ToolUsed{
			{Tool: "staticcheck", Version: "2024.1", Status: "success"},
		},
		Languages: []LanguageInfo{
			{Language: "Go", FileCount: 45, LineCount: 12000},
		},
		Frameworks: []string{"net/http", "gRPC"},
		Modules: []ModuleInfo{
			{
				Path:         "internal/api",
				Name:         "api",
				Description:  "HTTP handlers and WebSocket hub",
				Files:        8,
				Lines:        2400,
				Exports:      []string{"HandleStart", "HandleStatus"},
				Dependencies: []string{"internal/specworkflow"},
				HasTests:     true,
				TestFiles:    2,
				ContentHash:  "sha256:abc123",
			},
		},
		EntryPoints: []EntryPoint{
			{Path: "cmd/specworkflow/main.go", Type: "cli", Description: "Main CLI entry point"},
		},
		DependencyGraph: DependencyGraph{
			Edges: []DependencyEdge{
				{From: "cmd/specworkflow", To: "internal/api"},
			},
		},
		ExistingDocs: []ExistingDoc{
			{Path: "README.md", LastModified: "2026-03-15", EstimatedStaleness: "high", Topics: []string{"overview"}},
		},
		TestCoverage: TestCoverageOverview{
			TestFileCount: 32, PackagesWithTests: 3, PackagesWithoutTests: 1, TestFrameworks: []string{"testing"},
		},
		SuggestedScope: SuggestedScope{
			Include:    []string{"internal/", "cmd/"},
			Exclude:    []string{"vendor/"},
			FocusAreas: []string{"internal/specworkflow"},
		},
	}

	data, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	// All Section 9 top-level fields must be present.
	requiredKeys := []string{
		"schema_version", "agent", "mode", "completion_status",
		"tools_used", "languages", "frameworks", "modules",
		"entry_points", "dependency_graph", "existing_docs",
		"test_coverage_overview", "suggested_scope",
		"incremental_changes", "merge_log",
	}
	for _, key := range requiredKeys {
		if _, ok := m[key]; !ok {
			t.Errorf("missing JSON key %q in DiscoveryOutput", key)
		}
	}
}

// ---------------------------------------------------------------------------
// DrafterOutput JSON schema conformance
// ---------------------------------------------------------------------------

func TestSchemasDrafterOutputJSONMatchesSection10(t *testing.T) {
	d := DrafterOutput{
		SchemaVersion: "1.0",
		Agent:         "codedoc-drafter",
		AsImplementedReport: ReportRef{
			FilePath: "docs/as-implemented-report.md",
			Sections: []string{"overview", "modules"},
		},
		ArchitectureDiagrams: []DiagramRef{
			{
				FilePath:       "docs/architecture/module-dependencies.md",
				DiagramType:    "module_dependency",
				MermaidContent: "graph TD; A-->B",
				MermaidValid:   true,
			},
		},
		CodeAudit: CodeAudit{
			JSONFilePath:   "docs/audit/code-audit.json",
			ReportFilePath: "docs/audit/code-audit-report.md",
			Findings: []CodeAuditFinding{
				{
					ID: "AUDIT-001", Type: "dead_code", Severity: SeverityMajor,
					FilePath: "internal/legacy/old.go", LineNumber: 42,
					Symbol: "handleLegacy", Description: "No callers", Evidence: "grep",
				},
			},
			Summary: AuditSummary{DeadCode: 1},
		},
		DocUpdates: []DocUpdate{
			{FilePath: "README.md", Action: "update", SectionsChanged: []string{"Architecture"}, Reason: "Stale"},
		},
		StructuralSummary: StructuralSummary{
			ReportSections: 5, DiagramCount: 3, AuditFindingCount: 19,
			DocUpdates: 2, ModulesDocumented: 15, EntryPointsDocumented: 2,
		},
	}

	data, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	requiredKeys := []string{
		"schema_version", "agent", "as_implemented_report",
		"architecture_diagrams", "code_audit", "doc_updates",
		"structural_summary",
	}
	for _, key := range requiredKeys {
		if _, ok := m[key]; !ok {
			t.Errorf("missing JSON key %q in DrafterOutput", key)
		}
	}
}

// ---------------------------------------------------------------------------
// ReviewerOutput JSON schema conformance
// ---------------------------------------------------------------------------

func TestSchemasReviewerOutputJSONMatchesSection11(t *testing.T) {
	r := ReviewerOutput{
		SchemaVersion: "1.0",
		Agent:         "codedoc-reviewer",
		Round:         1,
		LensesApplied: []string{"ACC", "CUR"},
		Findings: []ReviewFinding{
			{
				ID:              "ACC-001",
				Description:     "Module description is wrong",
				Severity:        SeverityCritical,
				Status:          "open",
				Impact:          "Misleading docs",
				Recommendation:  "Update description",
				Lens:            "ACC",
				AffectedSection: "as-implemented-report.md#modules",
				AffectedFile:    "docs/as-implemented-report.md",
			},
		},
		StructuralIntegrity: StructuralIntegrity{
			Performed: true,
			Checks: []StructuralCheck{
				{Name: "mermaid_syntax", Passed: true, Details: "All diagrams parse"},
			},
		},
	}

	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	requiredKeys := []string{
		"schema_version", "agent", "round", "lenses_applied",
		"findings", "structural_integrity",
	}
	for _, key := range requiredKeys {
		if _, ok := m[key]; !ok {
			t.Errorf("missing JSON key %q in ReviewerOutput", key)
		}
	}

	// Check finding has required fields.
	findings := m["findings"].([]interface{})
	f := findings[0].(map[string]interface{})
	findingKeys := []string{
		"id", "description", "severity", "status", "impact",
		"recommendation", "lens", "affected_section", "affected_file",
	}
	for _, key := range findingKeys {
		if _, ok := f[key]; !ok {
			t.Errorf("missing JSON key %q in ReviewFinding", key)
		}
	}
}

// ---------------------------------------------------------------------------
// ValidateDiscoveryOutput
// ---------------------------------------------------------------------------

func TestSchemasValidateDiscoveryOutput_Valid(t *testing.T) {
	d := &DiscoveryOutput{
		SchemaVersion:    "1.0",
		Agent:            "codedoc-discovery",
		Mode:             "full",
		CompletionStatus: CompletionStatus{Status: "complete", Reason: "done"},
	}
	errs := ValidateDiscoveryOutput(d)
	if len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestSchemasValidateDiscoveryOutput_MissingSchemaVersion(t *testing.T) {
	d := &DiscoveryOutput{
		Agent:            "codedoc-discovery",
		Mode:             "full",
		CompletionStatus: CompletionStatus{Status: "complete", Reason: "done"},
	}
	errs := ValidateDiscoveryOutput(d)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "schema_version") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error about schema_version, got %v", errs)
	}
}

func TestSchemasValidateDiscoveryOutput_InvalidCompletionStatus(t *testing.T) {
	d := &DiscoveryOutput{
		SchemaVersion:    "1.0",
		Agent:            "codedoc-discovery",
		Mode:             "full",
		CompletionStatus: CompletionStatus{Status: "invalid", Reason: "bad"},
	}
	errs := ValidateDiscoveryOutput(d)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "completion_status.status") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error about completion_status.status, got %v", errs)
	}
}

func TestSchemasValidateDiscoveryOutput_InvalidMode(t *testing.T) {
	d := &DiscoveryOutput{
		SchemaVersion:    "1.0",
		Agent:            "codedoc-discovery",
		Mode:             "partial",
		CompletionStatus: CompletionStatus{Status: "complete", Reason: "done"},
	}
	errs := ValidateDiscoveryOutput(d)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "mode") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error about mode, got %v", errs)
	}
}

func TestSchemasValidateDiscoveryOutput_EmptyModulePath(t *testing.T) {
	d := &DiscoveryOutput{
		SchemaVersion:    "1.0",
		Agent:            "codedoc-discovery",
		Mode:             "full",
		CompletionStatus: CompletionStatus{Status: "complete", Reason: "done"},
		Modules:          []ModuleInfo{{Path: ""}},
	}
	errs := ValidateDiscoveryOutput(d)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "modules[0].path") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error about modules[0].path, got %v", errs)
	}
}

// ---------------------------------------------------------------------------
// ValidateDrafterOutput
// ---------------------------------------------------------------------------

func TestSchemasValidateDrafterOutput_Valid(t *testing.T) {
	d := &DrafterOutput{
		SchemaVersion:       "1.0",
		Agent:               "codedoc-drafter",
		AsImplementedReport: ReportRef{FilePath: "docs/report.md", Sections: []string{"overview"}},
	}
	errs := ValidateDrafterOutput(d)
	if len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestSchemasValidateDrafterOutput_MissingReport(t *testing.T) {
	d := &DrafterOutput{
		SchemaVersion:       "1.0",
		Agent:               "codedoc-drafter",
		AsImplementedReport: ReportRef{},
	}
	errs := ValidateDrafterOutput(d)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "as_implemented_report") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error about as_implemented_report, got %v", errs)
	}
}

func TestSchemasValidateDrafterOutput_InvalidAuditSeverity(t *testing.T) {
	d := &DrafterOutput{
		SchemaVersion:       "1.0",
		Agent:               "codedoc-drafter",
		AsImplementedReport: ReportRef{FilePath: "docs/report.md"},
		CodeAudit: CodeAudit{
			Findings: []CodeAuditFinding{
				{ID: "AUDIT-001", Type: "dead_code", Severity: "invalid"},
			},
		},
	}
	errs := ValidateDrafterOutput(d)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "severity") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error about severity, got %v", errs)
	}
}

// ---------------------------------------------------------------------------
// ValidateReviewerOutput
// ---------------------------------------------------------------------------

func TestSchemasValidateReviewerOutput_Valid(t *testing.T) {
	r := &ReviewerOutput{
		SchemaVersion: "1.0",
		Agent:         "codedoc-reviewer",
		Round:         1,
		LensesApplied: []string{"ACC"},
		Findings: []ReviewFinding{
			{
				ID: "ACC-001", Description: "Wrong", Severity: SeverityCritical,
				Status: "open", Impact: "Bad", Recommendation: "Fix it",
				Lens: "ACC", AffectedSection: "report.md#overview",
				AffectedFile: "docs/report.md",
			},
		},
	}
	valid, rejected, errs := ValidateReviewerOutput(r)
	if len(valid) != 1 {
		t.Errorf("expected 1 valid finding, got %d", len(valid))
	}
	if rejected != 0 {
		t.Errorf("expected 0 rejected, got %d", rejected)
	}
	// Structural errors only (none expected here).
	for _, e := range errs {
		t.Errorf("unexpected error: %v", e)
	}
}

func TestSchemasValidateReviewerOutput_EmptySeverity(t *testing.T) {
	r := &ReviewerOutput{
		SchemaVersion: "1.0",
		Agent:         "codedoc-reviewer",
		Round:         1,
		LensesApplied: []string{"ACC"},
		Findings: []ReviewFinding{
			{
				ID: "ACC-001", Description: "Wrong", Severity: "",
				Impact: "Bad", Recommendation: "Fix",
				Lens: "ACC", AffectedSection: "report.md",
			},
		},
	}
	valid, rejected, errs := ValidateReviewerOutput(r)
	if len(valid) != 0 {
		t.Errorf("expected 0 valid findings, got %d", len(valid))
	}
	if rejected != 1 {
		t.Errorf("expected 1 rejected, got %d", rejected)
	}
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "empty severity") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error about empty severity, got %v", errs)
	}
}

func TestSchemasValidateReviewerOutput_MissingRecommendation(t *testing.T) {
	r := &ReviewerOutput{
		SchemaVersion: "1.0",
		Agent:         "codedoc-reviewer",
		Round:         1,
		LensesApplied: []string{"ACC"},
		Findings: []ReviewFinding{
			{
				ID: "ACC-001", Description: "Wrong", Severity: SeverityMajor,
				Impact: "Bad", Recommendation: "",
				Lens: "ACC", AffectedSection: "report.md",
			},
		},
	}
	valid, rejected, _ := ValidateReviewerOutput(r)
	if len(valid) != 0 {
		t.Errorf("expected 0 valid findings, got %d", len(valid))
	}
	if rejected != 1 {
		t.Errorf("expected 1 rejected, got %d", rejected)
	}
}

func TestSchemasValidateReviewerOutput_InvalidSeverity(t *testing.T) {
	r := &ReviewerOutput{
		SchemaVersion: "1.0",
		Agent:         "codedoc-reviewer",
		Round:         1,
		LensesApplied: []string{"ACC"},
		Findings: []ReviewFinding{
			{
				ID: "ACC-001", Description: "Wrong", Severity: "INVALID",
				Impact: "Bad", Recommendation: "Fix",
				Lens: "ACC", AffectedSection: "report.md",
			},
		},
	}
	valid, rejected, _ := ValidateReviewerOutput(r)
	if len(valid) != 0 {
		t.Errorf("expected 0 valid findings, got %d", len(valid))
	}
	if rejected != 1 {
		t.Errorf("expected 1 rejected, got %d", rejected)
	}
}

func TestSchemasValidateReviewerOutput_EmptyLenses(t *testing.T) {
	r := &ReviewerOutput{
		SchemaVersion: "1.0",
		Agent:         "codedoc-reviewer",
		Round:         1,
		LensesApplied: []string{},
	}
	_, _, errs := ValidateReviewerOutput(r)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "lenses_applied") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error about lenses_applied, got %v", errs)
	}
}

func TestSchemasValidateReviewerOutput_MixedValidAndInvalid(t *testing.T) {
	r := &ReviewerOutput{
		SchemaVersion: "1.0",
		Agent:         "codedoc-reviewer",
		Round:         1,
		LensesApplied: []string{"ACC", "CUR"},
		Findings: []ReviewFinding{
			{
				ID: "ACC-001", Description: "Valid", Severity: SeverityCritical,
				Impact: "Bad", Recommendation: "Fix it",
				Lens: "ACC", AffectedSection: "report.md",
			},
			{
				ID: "CUR-001", Description: "Missing rec", Severity: SeverityMinor,
				Impact: "Minor", Recommendation: "",
				Lens: "CUR", AffectedSection: "report.md",
			},
			{
				ID: "ACC-002", Description: "Empty sev", Severity: "",
				Impact: "Bad", Recommendation: "Fix",
				Lens: "ACC", AffectedSection: "report.md",
			},
		},
	}
	valid, rejected, _ := ValidateReviewerOutput(r)
	if len(valid) != 1 {
		t.Errorf("expected 1 valid finding, got %d", len(valid))
	}
	if rejected != 2 {
		t.Errorf("expected 2 rejected, got %d", rejected)
	}
}

// ---------------------------------------------------------------------------
// ValidateJudgeOutput
// ---------------------------------------------------------------------------

func TestSchemasValidateJudgeOutput_Valid(t *testing.T) {
	j := &JudgeOutput{
		SchemaVersion: "1.0",
		Agent:         "codedoc-judge",
		Round:         1,
		Verdict:       "PASS",
		Rationale:     "All findings resolved",
	}
	errs := ValidateJudgeOutput(j)
	if len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestSchemasValidateJudgeOutput_InvalidVerdict(t *testing.T) {
	j := &JudgeOutput{
		SchemaVersion: "1.0",
		Agent:         "codedoc-judge",
		Round:         1,
		Verdict:       "INVALID",
		Rationale:     "reason",
	}
	errs := ValidateJudgeOutput(j)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "verdict") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error about verdict, got %v", errs)
	}
}

func TestSchemasValidateJudgeOutput_MissingRationale(t *testing.T) {
	j := &JudgeOutput{
		SchemaVersion: "1.0",
		Agent:         "codedoc-judge",
		Round:         1,
		Verdict:       "REVISE",
		Rationale:     "",
	}
	errs := ValidateJudgeOutput(j)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "rationale") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error about rationale, got %v", errs)
	}
}

// ---------------------------------------------------------------------------
// ReviewerOutput JSON round-trip
// ---------------------------------------------------------------------------

func TestSchemasReviewerOutputRoundTrip(t *testing.T) {
	r := ReviewerOutput{
		SchemaVersion: "1.0",
		Agent:         "codedoc-reviewer",
		Round:         2,
		LensesApplied: []string{"ARC", "STR"},
		Findings: []ReviewFinding{
			{
				ID: "ARC-001", Description: "Diagram wrong", Severity: SeverityMajor,
				Status: "open", Impact: "Wrong dep direction",
				Recommendation: "Reverse arrow", Lens: "ARC",
				AffectedSection: "architecture/module-dependencies.md",
				AffectedFile: "docs/architecture/module-dependencies.md",
			},
		},
		StructuralIntegrity: StructuralIntegrity{
			Performed: true,
			Checks: []StructuralCheck{
				{Name: "mermaid_syntax", Passed: true, Details: "OK"},
				{Name: "cross_references", Passed: false, Details: "broken ref"},
			},
		},
	}

	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var r2 ReviewerOutput
	if err := json.Unmarshal(data, &r2); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if r2.SchemaVersion != "1.0" || r2.Agent != "codedoc-reviewer" || r2.Round != 2 {
		t.Errorf("top-level fields mismatch: %+v", r2)
	}
	if len(r2.LensesApplied) != 2 {
		t.Errorf("LensesApplied: got %d, want 2", len(r2.LensesApplied))
	}
	if len(r2.Findings) != 1 || r2.Findings[0].ID != "ARC-001" {
		t.Errorf("Findings round-trip failed")
	}
	if !r2.StructuralIntegrity.Performed || len(r2.StructuralIntegrity.Checks) != 2 {
		t.Errorf("StructuralIntegrity round-trip failed")
	}
}

// ---------------------------------------------------------------------------
// JudgeOutput JSON round-trip
// ---------------------------------------------------------------------------

func TestSchemasJudgeOutputRoundTrip(t *testing.T) {
	j := JudgeOutput{
		SchemaVersion: "1.0",
		Agent:         "codedoc-judge",
		Round:         1,
		Verdict:       "REVISE",
		Rationale:     "Unresolved critical findings",
		IssueUpdates: []IssueUpdate{
			{FindingID: "ACC-001", NewStatus: "resolved", Reason: "Addressed in revision"},
		},
		Downgrades: []Downgrade{
			{FindingID: "CMP-001", OldSeverity: SeverityMajor, NewSeverity: SeverityMinor, ReasonCode: "OUT_OF_SCOPE"},
		},
	}

	data, err := json.Marshal(j)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var j2 JudgeOutput
	if err := json.Unmarshal(data, &j2); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if j2.Verdict != "REVISE" || j2.Rationale != "Unresolved critical findings" {
		t.Errorf("top-level fields mismatch: %+v", j2)
	}
	if len(j2.IssueUpdates) != 1 || j2.IssueUpdates[0].FindingID != "ACC-001" {
		t.Errorf("IssueUpdates round-trip failed")
	}
	if len(j2.Downgrades) != 1 || j2.Downgrades[0].ReasonCode != "OUT_OF_SCOPE" {
		t.Errorf("Downgrades round-trip failed")
	}
}
