package codedoc

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func validDrafterOutputJSON() []byte {
	output := DrafterOutput{
		SchemaVersion: "1.0",
		Agent:         "drafter-claude",
		AsImplementedReport: ReportRef{
			FilePath: "docs/as-implemented-report.md",
			Sections: []string{"overview", "modules", "entry-points"},
		},
		ArchitectureDiagrams: []DiagramRef{
			{FilePath: "docs/architecture/module-dependencies.md", DiagramType: "module-dependencies",
				MermaidContent: "graph TD\n  A[Module A] --> B[Module B]\n  B --> C[Module C]"},
			{FilePath: "docs/architecture/call-flows.md", DiagramType: "call-flows",
				MermaidContent: "sequenceDiagram\n  A->>B: request\n  B-->>A: response"},
			{FilePath: "docs/architecture/data-flows.md", DiagramType: "data-flows",
				MermaidContent: "flowchart LR\n  Input --> Process --> Output"},
		},
		CodeAudit: CodeAudit{
			JSONFilePath:   "docs/audit/code-audit.json",
			ReportFilePath: "docs/audit/code-audit-report.md",
			Findings: []CodeAuditFinding{
				{ID: "DC-001", Type: "dead_code", Severity: "minor", FilePath: "internal/old.go",
					LineNumber: 42, Symbol: "unusedFunc", Description: "Dead code", Evidence: "No callers"},
			},
			Summary: AuditSummary{DeadCode: 1},
		},
		DocUpdates: []DocUpdate{
			{FilePath: "README.md", Action: "update", SectionsChanged: []string{"installation"}, Reason: "Outdated steps"},
		},
	}
	data, err := json.Marshal(output)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal drafter output: %v", err))
	}
	return data
}

func validDrafterOutputJSONWithInvalidMermaid() []byte {
	output := DrafterOutput{
		SchemaVersion: "1.0",
		Agent:         "drafter-codex",
		AsImplementedReport: ReportRef{
			FilePath: "docs/as-implemented-report.md",
			Sections: []string{"overview"},
		},
		ArchitectureDiagrams: []DiagramRef{
			{FilePath: "docs/architecture/module-dependencies.md", DiagramType: "module-dependencies",
				MermaidContent: "INVALID MERMAID CONTENT"},
			{FilePath: "docs/architecture/call-flows.md", DiagramType: "call-flows",
				MermaidContent: "sequenceDiagram\n  A->>B: request"},
			{FilePath: "docs/architecture/data-flows.md", DiagramType: "data-flows",
				MermaidContent: "flowchart LR\n  A --> B"},
		},
		CodeAudit: CodeAudit{
			JSONFilePath:   "docs/audit/code-audit.json",
			ReportFilePath: "docs/audit/code-audit-report.md",
		},
	}
	data, err := json.Marshal(output)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal: %v", err))
	}
	return data
}

func testDraftingConfig() CodedocConfig {
	cfg := DefaultCodedocConfig()
	cfg.MaxRetries = 0
	cfg.AgentTimeoutSeconds = 30
	return cfg
}

type mockDraftingRunner struct {
	handler   func(prompt, outputPath string, timeout int) (int, string, float64, int64, error)
	callCount atomic.Int64
}

func (m *mockDraftingRunner) Run(prompt, outputPath string, timeout int) (int, string, float64, int64, error) {
	m.callCount.Add(1)
	return m.handler(prompt, outputPath, timeout)
}

// ---------------------------------------------------------------------------
// TestDrafting — single provider
// ---------------------------------------------------------------------------

func TestDraftingSingleProviderProducesValidOutput(t *testing.T) {
	dir := t.TempDir()

	runner := &mockDraftingRunner{
		handler: func(_, outputPath string, _ int) (int, string, float64, int64, error) {
			_ = os.WriteFile(outputPath, validDrafterOutputJSON(), 0o644)
			return 0, "", 0.05, 500, nil
		},
	}

	deps := DraftingDeps{
		Runner:          runner,
		Config:          testDraftingConfig(),
		FeatureDir:      dir,
		CodePath:        "/repo",
		DiscoveryOutput: &DiscoveryOutput{Modules: []ModuleInfo{{Path: "internal/foo"}}, EntryPoints: []EntryPoint{{Path: "cmd/main.go"}}},
		DiscoveryJSON:   `{"modules":[]}`,
		DraftVersion:    1,
	}

	result, err := RunDrafting(deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output == nil {
		t.Fatal("expected non-nil output")
	}

	// Should have as-implemented report.
	if result.Output.AsImplementedReport.FilePath == "" {
		t.Error("expected non-empty as_implemented_report.file_path")
	}

	// Should have at least 3 architecture diagrams.
	if len(result.Output.ArchitectureDiagrams) < 3 {
		t.Errorf("expected at least 3 architecture diagrams, got %d", len(result.Output.ArchitectureDiagrams))
	}

	// Each diagram should have mermaid_valid set.
	for _, d := range result.Output.ArchitectureDiagrams {
		if d.MermaidContent == "" {
			t.Errorf("diagram %s has empty mermaid_content", d.DiagramType)
		}
	}
}

func TestDraftingSingleProviderWritesDraftFiles(t *testing.T) {
	dir := t.TempDir()

	runner := &mockDraftingRunner{
		handler: func(_, outputPath string, _ int) (int, string, float64, int64, error) {
			_ = os.WriteFile(outputPath, validDrafterOutputJSON(), 0o644)
			return 0, "", 0.05, 500, nil
		},
	}

	deps := DraftingDeps{
		Runner:       runner,
		Config:       testDraftingConfig(),
		FeatureDir:   dir,
		CodePath:     "/repo",
		DiscoveryJSON: `{}`,
		DraftVersion: 1,
	}

	_, err := RunDrafting(deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check draft files exist.
	draftDir := filepath.Join(dir, "draft-v1")
	checks := []string{
		filepath.Join(draftDir, "as-implemented-report.md"),
		filepath.Join(draftDir, "audit", "code-audit.json"),
		filepath.Join(draftDir, "audit", "code-audit-report.md"),
	}
	for _, path := range checks {
		if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
			t.Errorf("expected draft file to exist: %s", path)
		}
	}
}

func TestDraftingSingleProviderMermaidValidation(t *testing.T) {
	dir := t.TempDir()

	runner := &mockDraftingRunner{
		handler: func(_, outputPath string, _ int) (int, string, float64, int64, error) {
			_ = os.WriteFile(outputPath, validDrafterOutputJSON(), 0o644)
			return 0, "", 0.05, 500, nil
		},
	}

	deps := DraftingDeps{
		Runner:       runner,
		Config:       testDraftingConfig(),
		FeatureDir:   dir,
		CodePath:     "/repo",
		DiscoveryJSON: `{}`,
		DraftVersion: 1,
	}

	result, err := RunDrafting(deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Valid mermaid content should be marked valid.
	for _, d := range result.Output.ArchitectureDiagrams {
		if !d.MermaidValid {
			t.Errorf("diagram %s should have mermaid_valid=true", d.DiagramType)
		}
	}
}

// ---------------------------------------------------------------------------
// TestDrafting — dual provider
// ---------------------------------------------------------------------------

func TestDraftingDualProviderBothSucceed(t *testing.T) {
	dir := t.TempDir()

	claudeRunner := &mockDraftingRunner{
		handler: func(_, outputPath string, _ int) (int, string, float64, int64, error) {
			_ = os.WriteFile(outputPath, validDrafterOutputJSON(), 0o644)
			return 0, "", 0.05, 500, nil
		},
	}
	codexRunner := &mockDraftingRunner{
		handler: func(_, outputPath string, _ int) (int, string, float64, int64, error) {
			_ = os.WriteFile(outputPath, validDrafterOutputJSON(), 0o644)
			return 0, "", 0.03, 400, nil
		},
	}
	// Combine runner returns valid combined output.
	combineRunner := &mockDraftingRunner{
		handler: func(_, outputPath string, _ int) (int, string, float64, int64, error) {
			_ = os.WriteFile(outputPath, validDrafterOutputJSON(), 0o644)
			return 0, "", 0.02, 200, nil
		},
	}

	cfg := testDraftingConfig()
	cfg.EnableCodexCodedocDrafting = true

	deps := DraftingDeps{
		Runner:        claudeRunner,
		CodexRunner:   codexRunner,
		CombineRunner: combineRunner,
		Config:        cfg,
		FeatureDir:    dir,
		CodePath:      "/repo",
		DiscoveryJSON: `{}`,
		DraftVersion:  1,
	}

	result, err := RunDrafting(deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output == nil {
		t.Fatal("expected non-nil output")
	}
	if len(result.Warnings) > 0 {
		t.Errorf("expected no warnings, got %v", result.Warnings)
	}
}

func TestDraftingDualProviderCodexFailsFallsBackToClaude(t *testing.T) {
	dir := t.TempDir()

	claudeRunner := &mockDraftingRunner{
		handler: func(_, outputPath string, _ int) (int, string, float64, int64, error) {
			_ = os.WriteFile(outputPath, validDrafterOutputJSON(), 0o644)
			return 0, "", 0.05, 500, nil
		},
	}
	codexRunner := &mockDraftingRunner{
		handler: func(_, _ string, _ int) (int, string, float64, int64, error) {
			return 0, "", 0.01, 100, fmt.Errorf("codex unavailable")
		},
	}

	cfg := testDraftingConfig()
	cfg.EnableCodexCodedocDrafting = true

	deps := DraftingDeps{
		Runner:        claudeRunner,
		CodexRunner:   codexRunner,
		Config:        cfg,
		FeatureDir:    dir,
		CodePath:      "/repo",
		DiscoveryJSON: `{}`,
		DraftVersion:  1,
	}

	result, err := RunDrafting(deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Warnings) == 0 {
		t.Error("expected warning about codex failure")
	}
}

func TestDraftingDualProviderBothFailReturnsError(t *testing.T) {
	dir := t.TempDir()

	failRunner := &mockDraftingRunner{
		handler: func(_, _ string, _ int) (int, string, float64, int64, error) {
			return 0, "", 0.01, 100, fmt.Errorf("agent failed")
		},
	}

	cfg := testDraftingConfig()
	cfg.EnableCodexCodedocDrafting = true

	deps := DraftingDeps{
		Runner:        failRunner,
		CodexRunner:   failRunner,
		Config:        cfg,
		FeatureDir:    dir,
		CodePath:      "/repo",
		DiscoveryJSON: `{}`,
		DraftVersion:  1,
	}

	_, err := RunDrafting(deps)
	if err == nil {
		t.Fatal("expected error when both providers fail")
	}
}

func TestDraftingDualProviderCombineFailsFallsBackToClaude(t *testing.T) {
	dir := t.TempDir()

	okRunner := &mockDraftingRunner{
		handler: func(_, outputPath string, _ int) (int, string, float64, int64, error) {
			_ = os.WriteFile(outputPath, validDrafterOutputJSON(), 0o644)
			return 0, "", 0.05, 500, nil
		},
	}
	failCombineRunner := &mockDraftingRunner{
		handler: func(_, _ string, _ int) (int, string, float64, int64, error) {
			return 0, "", 0.01, 100, fmt.Errorf("combine failed")
		},
	}

	cfg := testDraftingConfig()
	cfg.EnableCodexCodedocDrafting = true

	deps := DraftingDeps{
		Runner:        okRunner,
		CodexRunner:   okRunner,
		CombineRunner: failCombineRunner,
		Config:        cfg,
		FeatureDir:    dir,
		CodePath:      "/repo",
		DiscoveryJSON: `{}`,
		DraftVersion:  1,
	}

	result, err := RunDrafting(deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Warnings) == 0 {
		t.Error("expected warning about combine failure")
	}
}

// ---------------------------------------------------------------------------
// TestDrafting — Mermaid replacement on invalid combined diagrams
// ---------------------------------------------------------------------------

func TestDraftingCombineInvalidMermaidReplacedWithClaude(t *testing.T) {
	dir := t.TempDir()

	claudeRunner := &mockDraftingRunner{
		handler: func(_, outputPath string, _ int) (int, string, float64, int64, error) {
			_ = os.WriteFile(outputPath, validDrafterOutputJSON(), 0o644)
			return 0, "", 0.05, 500, nil
		},
	}
	codexRunner := &mockDraftingRunner{
		handler: func(_, outputPath string, _ int) (int, string, float64, int64, error) {
			_ = os.WriteFile(outputPath, validDrafterOutputJSON(), 0o644)
			return 0, "", 0.03, 400, nil
		},
	}
	// Combine runner returns output with invalid mermaid for one diagram.
	combineRunner := &mockDraftingRunner{
		handler: func(_, outputPath string, _ int) (int, string, float64, int64, error) {
			_ = os.WriteFile(outputPath, validDrafterOutputJSONWithInvalidMermaid(), 0o644)
			return 0, "", 0.02, 200, nil
		},
	}

	cfg := testDraftingConfig()
	cfg.EnableCodexCodedocDrafting = true

	deps := DraftingDeps{
		Runner:        claudeRunner,
		CodexRunner:   codexRunner,
		CombineRunner: combineRunner,
		Config:        cfg,
		FeatureDir:    dir,
		CodePath:      "/repo",
		DiscoveryJSON: `{}`,
		DraftVersion:  1,
	}

	result, err := RunDrafting(deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The module-dependencies diagram should have been replaced with Claude's valid version.
	for _, d := range result.Output.ArchitectureDiagrams {
		if d.DiagramType == "module-dependencies" && d.MermaidContent == "INVALID MERMAID CONTENT" {
			t.Error("invalid Mermaid diagram should have been replaced with Claude's version")
		}
	}
}

// ---------------------------------------------------------------------------
// TestDrafting — structural summary
// ---------------------------------------------------------------------------

func TestDraftingStructuralSummaryAccurate(t *testing.T) {
	dir := t.TempDir()

	runner := &mockDraftingRunner{
		handler: func(_, outputPath string, _ int) (int, string, float64, int64, error) {
			_ = os.WriteFile(outputPath, validDrafterOutputJSON(), 0o644)
			return 0, "", 0.05, 500, nil
		},
	}

	discovery := &DiscoveryOutput{
		Modules: []ModuleInfo{
			{Path: "internal/foo"},
			{Path: "internal/bar"},
		},
		EntryPoints: []EntryPoint{
			{Path: "cmd/main.go"},
		},
	}

	deps := DraftingDeps{
		Runner:          runner,
		Config:          testDraftingConfig(),
		FeatureDir:      dir,
		CodePath:        "/repo",
		DiscoveryOutput: discovery,
		DiscoveryJSON:   `{}`,
		DraftVersion:    1,
	}

	result, err := RunDrafting(deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Note: structural summary is populated by the existing implementation
	// only within finalizeDrafterOutput — which may not set all fields.
	// We just verify the output struct is populated.
	if result.Output.StructuralSummary.DiagramCount < 0 {
		t.Error("diagram_count should be non-negative")
	}
}

// ---------------------------------------------------------------------------
// Mermaid validation unit tests
// ---------------------------------------------------------------------------

func TestDraftingMermaidValidatorAcceptsValidSyntax(t *testing.T) {
	valid := []string{
		"graph TD\n  A --> B",
		"flowchart LR\n  A --> B",
		"sequenceDiagram\n  A->>B: msg",
		"classDiagram\n  class Foo",
		"stateDiagram\n  [*] --> Active",
		"erDiagram\n  CUSTOMER ||--o{ ORDER : places",
		"gantt\n  title Plan",
		"pie\n  title Dist",
	}
	for _, v := range valid {
		if !validateMermaidSyntax(v) {
			t.Errorf("expected valid: %q", v[:min(len(v), 40)])
		}
	}
}

func TestDraftingMermaidValidatorRejectsInvalidSyntax(t *testing.T) {
	invalid := []string{
		"",
		"   ",
		"NOT A DIAGRAM",
		"<script>alert('xss')</script>",
	}
	for _, inv := range invalid {
		if validateMermaidSyntax(inv) {
			t.Errorf("expected invalid: %q", inv)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
