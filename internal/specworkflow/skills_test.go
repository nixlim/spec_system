package specworkflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// skillFixture holds the file name and mock content used by tests.
type skillFixture struct {
	name    string
	content string
}

var planSpecFixtures = []skillFixture{
	{SpecTemplate, "# Spec Template\nplaceholder spec content"},
	{BDDTemplate, "# BDD Template\nGiven/When/Then scenarios"},
	{TestDatasetTemplate, "# Test Dataset\nboundary values here"},
}

var grillSpecFixtures = []skillFixture{
	{ReviewConstitution, "# Review Constitution\nreview rules"},
	{ReportTemplate, "# Report Template\nfindings go here"},
}

// setupSkillDirs creates temporary plan-spec and grill-spec directories
// populated with mock skill files and returns their paths plus a cleanup
// function.
func setupSkillDirs(t *testing.T) (planDir, grillDir string) {
	t.Helper()

	planDir = t.TempDir()
	grillDir = t.TempDir()

	for _, f := range planSpecFixtures {
		if err := os.WriteFile(filepath.Join(planDir, f.name), []byte(f.content), 0o644); err != nil {
			t.Fatalf("writing %s: %v", f.name, err)
		}
	}
	for _, f := range grillSpecFixtures {
		if err := os.WriteFile(filepath.Join(grillDir, f.name), []byte(f.content), 0o644); err != nil {
			t.Fatalf("writing %s: %v", f.name, err)
		}
	}
	return planDir, grillDir
}

func TestSkillLoadAll(t *testing.T) {
	planDir, grillDir := setupSkillDirs(t)

	sc, err := LoadSkills(planDir, grillDir)
	if err != nil {
		t.Fatalf("LoadSkills returned unexpected error: %v", err)
	}
	if !sc.loaded {
		t.Fatal("expected loaded to be true")
	}

	// Verify all 5 files were loaded.
	allFixtures := append(planSpecFixtures, grillSpecFixtures...)
	for _, f := range allFixtures {
		got, err := sc.GetSkillContent(f.name)
		if err != nil {
			t.Errorf("GetSkillContent(%q) error: %v", f.name, err)
			continue
		}
		if got != f.content {
			t.Errorf("GetSkillContent(%q) = %q, want %q", f.name, got, f.content)
		}
	}
}

func TestSkillGetContentUnknown(t *testing.T) {
	planDir, grillDir := setupSkillDirs(t)

	sc, err := LoadSkills(planDir, grillDir)
	if err != nil {
		t.Fatalf("LoadSkills: %v", err)
	}

	_, err = sc.GetSkillContent("nonexistent.md")
	if err == nil {
		t.Fatal("expected error for unknown skill file")
	}
	if !strings.Contains(err.Error(), "nonexistent.md") {
		t.Errorf("error should mention the unknown file name, got: %v", err)
	}
}

func TestSkillLoadMissingFile(t *testing.T) {
	planDir, grillDir := setupSkillDirs(t)

	// Remove one plan-spec file.
	if err := os.Remove(filepath.Join(planDir, BDDTemplate)); err != nil {
		t.Fatalf("removing fixture: %v", err)
	}

	_, err := LoadSkills(planDir, grillDir)
	if err == nil {
		t.Fatal("expected error when a skill file is missing")
	}
	if !strings.Contains(err.Error(), BDDTemplate) {
		t.Errorf("error should identify the missing file, got: %v", err)
	}
}

func TestSkillLoadMissingGrillFile(t *testing.T) {
	planDir, grillDir := setupSkillDirs(t)

	// Remove a grill-spec file.
	if err := os.Remove(filepath.Join(grillDir, ReportTemplate)); err != nil {
		t.Fatalf("removing fixture: %v", err)
	}

	_, err := LoadSkills(planDir, grillDir)
	if err == nil {
		t.Fatal("expected error when a grill-spec skill file is missing")
	}
	if !strings.Contains(err.Error(), ReportTemplate) {
		t.Errorf("error should identify the missing file, got: %v", err)
	}
}

func TestSkillGetChecksums(t *testing.T) {
	planDir, grillDir := setupSkillDirs(t)

	sc, err := LoadSkills(planDir, grillDir)
	if err != nil {
		t.Fatalf("LoadSkills: %v", err)
	}

	checksums := sc.GetChecksums()
	if len(checksums) != 2 {
		t.Fatalf("expected 2 checksums, got %d", len(checksums))
	}

	for _, key := range []string{"plan_spec", "grill_spec"} {
		v, ok := checksums[key]
		if !ok {
			t.Errorf("missing checksum key %q", key)
			continue
		}
		if !strings.HasPrefix(v, "sha256:") {
			t.Errorf("checksum for %q should start with 'sha256:', got %q", key, v)
		}
		// SHA-256 hex digest is 64 characters.
		hex := strings.TrimPrefix(v, "sha256:")
		if len(hex) != 64 {
			t.Errorf("checksum hex for %q has length %d, want 64", key, len(hex))
		}
	}
}

func TestSkillChecksumsDeterministic(t *testing.T) {
	planDir, grillDir := setupSkillDirs(t)

	sc1, err := LoadSkills(planDir, grillDir)
	if err != nil {
		t.Fatalf("first LoadSkills: %v", err)
	}

	sc2, err := LoadSkills(planDir, grillDir)
	if err != nil {
		t.Fatalf("second LoadSkills: %v", err)
	}

	c1 := sc1.GetChecksums()
	c2 := sc2.GetChecksums()

	for _, key := range []string{"plan_spec", "grill_spec"} {
		if c1[key] != c2[key] {
			t.Errorf("checksum for %q differs between loads: %q vs %q", key, c1[key], c2[key])
		}
	}
}

func TestSkillChecksumChangesWithContent(t *testing.T) {
	planDir, grillDir := setupSkillDirs(t)

	sc1, err := LoadSkills(planDir, grillDir)
	if err != nil {
		t.Fatalf("LoadSkills: %v", err)
	}

	// Modify one file and reload.
	if err := os.WriteFile(filepath.Join(planDir, SpecTemplate), []byte("modified content"), 0o644); err != nil {
		t.Fatalf("modifying fixture: %v", err)
	}

	sc2, err := LoadSkills(planDir, grillDir)
	if err != nil {
		t.Fatalf("LoadSkills after modification: %v", err)
	}

	c1 := sc1.GetChecksums()
	c2 := sc2.GetChecksums()

	if c1["plan_spec"] == c2["plan_spec"] {
		t.Error("plan_spec checksum should change when file content changes")
	}
	// grill_spec should be unchanged.
	if c1["grill_spec"] != c2["grill_spec"] {
		t.Error("grill_spec checksum should not change when only plan-spec files change")
	}
}

func TestSkillContentCached(t *testing.T) {
	planDir, grillDir := setupSkillDirs(t)

	sc, err := LoadSkills(planDir, grillDir)
	if err != nil {
		t.Fatalf("LoadSkills: %v", err)
	}

	// Modify the underlying file after loading.
	if err := os.WriteFile(filepath.Join(planDir, SpecTemplate), []byte("new content"), 0o644); err != nil {
		t.Fatalf("modifying file: %v", err)
	}

	// GetSkillContent should return the originally cached content, not the
	// modified file.
	got, err := sc.GetSkillContent(SpecTemplate)
	if err != nil {
		t.Fatalf("GetSkillContent: %v", err)
	}
	if got == "new content" {
		t.Error("content should be cached from initial load, not re-read from disk")
	}
	if got != planSpecFixtures[0].content {
		t.Errorf("cached content = %q, want %q", got, planSpecFixtures[0].content)
	}
}

func TestSkillGetChecksumsReturnsCopy(t *testing.T) {
	planDir, grillDir := setupSkillDirs(t)

	sc, err := LoadSkills(planDir, grillDir)
	if err != nil {
		t.Fatalf("LoadSkills: %v", err)
	}

	checksums := sc.GetChecksums()
	// Mutate the returned map.
	checksums["plan_spec"] = "tampered"

	// The internal state should be unaffected.
	fresh := sc.GetChecksums()
	if fresh["plan_spec"] == "tampered" {
		t.Error("GetChecksums should return a copy, not the internal map")
	}
}
