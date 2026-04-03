package codedoc

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFinalGatePayloadReflectsFindingsAndDrift(t *testing.T) {
	workspaceDir := t.TempDir()
	codePath := t.TempDir()
	moduleDir := filepath.Join(codePath, "internal", "foo")
	if err := os.MkdirAll(moduleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	moduleFile := filepath.Join(moduleDir, "doc.go")
	if err := os.WriteFile(moduleFile, []byte("package foo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := DefaultCodedocConfig()
	cfg.DriftWarningThreshold = 0
	orch := NewCodedocOrchestrator(CodedocOrchestratorConfig{
		WorkspaceDir: workspaceDir,
		FeatureName:  "docs-feature",
		CodePath:     codePath,
		Mode:         "full",
		Config:       cfg,
	})
	orch.mergedFindings = []ReviewFinding{
		{ID: "CRIT-1", Description: "critical", Severity: SeverityCritical, Status: "open", Lens: "ACC", AffectedSection: "intro"},
		{ID: "MAJ-1", Description: "major", Severity: SeverityMajor, Status: "open", Lens: "CMP", AffectedSection: "api"},
		{ID: "MIN-1", Description: "minor", Severity: SeverityMinor, Status: "open", Lens: "ACC", AffectedSection: "misc"},
		{ID: "RES-1", Description: "resolved", Severity: SeverityCritical, Status: "resolved", Lens: "ACC", AffectedSection: "done"},
	}
	orch.discoveryOutput = &DiscoveryOutput{
		Modules: []ModuleInfo{{
			Path:        filepath.Join("internal", "foo"),
			ContentHash: computeDirHash(moduleDir),
		}},
	}

	if err := os.WriteFile(moduleFile, []byte("package foo\n// changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	payload := orch.FinalGatePayload()
	if payload.TotalUnresolved != 2 {
		t.Fatalf("expected 2 unresolved findings, got %d", payload.TotalUnresolved)
	}
	if len(payload.UnresolvedFindings) != 2 {
		t.Fatalf("expected 2 unresolved finding payloads, got %d", len(payload.UnresolvedFindings))
	}
	if payload.DriftWarning == "" {
		t.Fatal("expected non-empty drift warning")
	}
}
