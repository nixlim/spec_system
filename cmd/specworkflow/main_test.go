package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/foundry-zero/adversarial-spec-system/internal/specworkflow"
)

func TestResolveStartupSpecConfigUsesDetectedSkillPaths(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tempDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tempDir, ".agents", "skills", "plan-spec"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tempDir, ".agents", "skills", "grill-spec"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tempDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)

	cfg := specworkflow.DefaultConfig()
	resolved, err := resolveStartupSpecConfig(cfg)
	if err != nil {
		t.Fatalf("resolveStartupSpecConfig: %v", err)
	}
	if resolved.SkillPaths.PlanSpec != filepath.Join(".agents", "skills", "plan-spec") {
		t.Fatalf("unexpected plan-spec path: %s", resolved.SkillPaths.PlanSpec)
	}
	if resolved.SkillPaths.GrillSpec != filepath.Join(".agents", "skills", "grill-spec") {
		t.Fatalf("unexpected grill-spec path: %s", resolved.SkillPaths.GrillSpec)
	}
}
