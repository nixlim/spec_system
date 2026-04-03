package codedoc

import (
	"encoding/json"
	"testing"
)

// ---------------------------------------------------------------------------
// Severity constant tests
// ---------------------------------------------------------------------------

func TestTypesSeverityConstants(t *testing.T) {
	checks := []struct {
		name string
		got  string
		want string
	}{
		{"SeverityCritical", SeverityCritical, "critical"},
		{"SeverityMajor", SeverityMajor, "major"},
		{"SeverityMinor", SeverityMinor, "minor"},
		{"SeverityObservation", SeverityObservation, "observation"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, c.got, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// CodeAuditFinding JSON round-trip
// ---------------------------------------------------------------------------

func TestTypesCodeAuditFindingJSONTags(t *testing.T) {
	f := CodeAuditFinding{
		ID:          "AUDIT-001",
		Type:        "dead_code",
		Severity:    SeverityMajor,
		FilePath:    "internal/legacy/old.go",
		LineNumber:  42,
		Symbol:      "handleLegacy",
		Description: "Function has no callers",
		Evidence:    "grep returns only definition",
	}

	data, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("Unmarshal to map: %v", err)
	}

	expectedKeys := []string{"id", "type", "severity", "file_path", "line_number", "symbol", "description", "evidence"}
	for _, key := range expectedKeys {
		if _, ok := m[key]; !ok {
			t.Errorf("missing JSON key %q in CodeAuditFinding", key)
		}
	}

	// Round-trip back to struct.
	var f2 CodeAuditFinding
	if err := json.Unmarshal(data, &f2); err != nil {
		t.Fatalf("Unmarshal to struct: %v", err)
	}
	if f2.ID != f.ID || f2.Type != f.Type || f2.Severity != f.Severity ||
		f2.FilePath != f.FilePath || f2.LineNumber != f.LineNumber ||
		f2.Symbol != f.Symbol || f2.Description != f.Description ||
		f2.Evidence != f.Evidence {
		t.Errorf("round-trip mismatch: got %+v, want %+v", f2, f)
	}
}

// ---------------------------------------------------------------------------
// ManifestFile JSON round-trip
// ---------------------------------------------------------------------------

func TestTypesManifestFileJSONTags(t *testing.T) {
	mf := ManifestFile{
		SchemaVersion:   "1.0",
		WorkflowFeature: "my-project-docs",
		GeneratedAt:     "2026-04-01T12:00:00Z",
		Modules: []ManifestModule{
			{
				Path:        "internal/api",
				ContentHash: "sha256:abc123",
				DocFiles:    []string{"docs/report.md"},
			},
		},
		FilesDocumented: []ManifestDoc{
			{
				Path:        "docs/report.md",
				ContentHash: "sha256:def456",
			},
		},
	}

	data, err := json.Marshal(mf)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("Unmarshal to map: %v", err)
	}

	expectedKeys := []string{"schema_version", "workflow_feature", "generated_at", "modules", "files_documented"}
	for _, key := range expectedKeys {
		if _, ok := m[key]; !ok {
			t.Errorf("missing JSON key %q in ManifestFile", key)
		}
	}

	// Round-trip back to struct.
	var mf2 ManifestFile
	if err := json.Unmarshal(data, &mf2); err != nil {
		t.Fatalf("Unmarshal to struct: %v", err)
	}
	if mf2.SchemaVersion != mf.SchemaVersion ||
		mf2.WorkflowFeature != mf.WorkflowFeature ||
		mf2.GeneratedAt != mf.GeneratedAt {
		t.Errorf("round-trip mismatch on top-level fields")
	}
	if len(mf2.Modules) != 1 || mf2.Modules[0].Path != "internal/api" {
		t.Errorf("Modules round-trip failed: got %+v", mf2.Modules)
	}
	if len(mf2.FilesDocumented) != 1 || mf2.FilesDocumented[0].Path != "docs/report.md" {
		t.Errorf("FilesDocumented round-trip failed: got %+v", mf2.FilesDocumented)
	}
}

// ---------------------------------------------------------------------------
// DiscoveryOutput JSON round-trip
// ---------------------------------------------------------------------------

func TestTypesDiscoveryOutputRoundTrip(t *testing.T) {
	d := DiscoveryOutput{
		SchemaVersion: "1.0",
		Agent:         "codedoc-discovery",
		Mode:          "full",
		CompletionStatus: CompletionStatus{
			Status: "complete",
			Reason: "Inventory complete",
		},
		Modules: []ModuleInfo{
			{
				Path:        "internal/api",
				Name:        "api",
				Description: "HTTP handlers",
				Files:       8,
				Lines:       2400,
			},
		},
	}

	data, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var d2 DiscoveryOutput
	if err := json.Unmarshal(data, &d2); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if d2.SchemaVersion != "1.0" || d2.Agent != "codedoc-discovery" || d2.Mode != "full" {
		t.Errorf("top-level fields mismatch: %+v", d2)
	}
	if d2.CompletionStatus.Status != "complete" {
		t.Errorf("CompletionStatus.Status: got %q, want complete", d2.CompletionStatus.Status)
	}
	if len(d2.Modules) != 1 || d2.Modules[0].Path != "internal/api" {
		t.Errorf("Modules round-trip failed")
	}
}

// ---------------------------------------------------------------------------
// DrafterOutput JSON round-trip
// ---------------------------------------------------------------------------

func TestTypesDrafterOutputRoundTrip(t *testing.T) {
	d := DrafterOutput{
		SchemaVersion: "1.0",
		Agent:         "codedoc-drafter",
		AsImplementedReport: ReportRef{
			FilePath: "docs/report.md",
			Sections: []string{"overview", "modules"},
		},
		CodeAudit: CodeAudit{
			Findings: []CodeAuditFinding{
				{ID: "AUDIT-001", Type: "dead_code", Severity: SeverityMajor},
			},
			Summary: AuditSummary{DeadCode: 1},
		},
	}

	data, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var d2 DrafterOutput
	if err := json.Unmarshal(data, &d2); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if d2.SchemaVersion != "1.0" || d2.Agent != "codedoc-drafter" {
		t.Errorf("top-level fields mismatch")
	}
	if len(d2.CodeAudit.Findings) != 1 || d2.CodeAudit.Findings[0].ID != "AUDIT-001" {
		t.Errorf("CodeAudit.Findings round-trip failed")
	}
	if d2.CodeAudit.Summary.DeadCode != 1 {
		t.Errorf("CodeAudit.Summary.DeadCode: got %d, want 1", d2.CodeAudit.Summary.DeadCode)
	}
}

// ---------------------------------------------------------------------------
// SanitisationReport JSON round-trip
// ---------------------------------------------------------------------------

func TestTypesSanitisationReportRoundTrip(t *testing.T) {
	sr := SanitisationReport{
		ScannedFiles:    10,
		SecretsFound:    2,
		SecretsRedacted: 2,
		Entries: []SanitisationEntry{
			{
				FilePath:    "docs/report.md",
				LineNumber:  15,
				PatternType: "aws_key",
				Redacted:    true,
				Confidence:  "high",
			},
		},
		Safe: true,
	}

	data, err := json.Marshal(sr)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var sr2 SanitisationReport
	if err := json.Unmarshal(data, &sr2); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if sr2.ScannedFiles != 10 || sr2.SecretsFound != 2 || sr2.SecretsRedacted != 2 || !sr2.Safe {
		t.Errorf("SanitisationReport fields mismatch: %+v", sr2)
	}
	if len(sr2.Entries) != 1 || sr2.Entries[0].PatternType != "aws_key" {
		t.Errorf("SanitisationReport.Entries round-trip failed")
	}
}

// ---------------------------------------------------------------------------
// AuditSummary JSON keys
// ---------------------------------------------------------------------------

func TestTypesAuditSummaryJSONKeys(t *testing.T) {
	s := AuditSummary{
		DeadCode:     3,
		Stubs:        1,
		Todos:        12,
		Fixmes:       2,
		EmptyCatches: 0,
		NonWired:     1,
	}

	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	expectedKeys := []string{"dead_code", "stubs", "todos", "fixmes", "empty_catches", "non_wired"}
	for _, key := range expectedKeys {
		if _, ok := m[key]; !ok {
			t.Errorf("missing JSON key %q in AuditSummary", key)
		}
	}
}
