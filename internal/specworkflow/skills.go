package specworkflow

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Skill file name constants identify the expected file within each skill
// directory.
const (
	// SpecTemplate is the plan-spec skill's specification template file.
	SpecTemplate = "spec-template.md"
	// BDDTemplate is the plan-spec skill's BDD scenario template file.
	BDDTemplate = "bdd-template.md"
	// TestDatasetTemplate is the plan-spec skill's test dataset template file.
	TestDatasetTemplate = "test-dataset-template.md"
	// ReviewConstitution is the grill-spec skill's review constitution file.
	ReviewConstitution = "review-constitution.md"
	// ReportTemplate is the grill-spec skill's report template file.
	ReportTemplate = "report-template.md"
)

// planSpecFiles lists the skill files expected in the plan-spec directory.
var planSpecFiles = []string{SpecTemplate, BDDTemplate, TestDatasetTemplate}

// grillSpecFiles lists the skill files expected in the grill-spec directory.
var grillSpecFiles = []string{ReviewConstitution, ReportTemplate}

// SkillCache holds cached skill file contents and directory checksums. Once
// populated by LoadSkills, the contents are served from memory without further
// filesystem access.
type SkillCache struct {
	contents  map[string]string // skill file name -> content
	checksums map[string]string // "plan_spec" | "grill_spec" -> "sha256:hex"
	loaded    bool
}

// LoadSkills reads all required skill files from the given plan-spec and
// grill-spec directories, computes SHA-256 checksums for each directory, and
// returns a populated SkillCache. If any expected file is missing or unreadable,
// an error identifying the problematic path is returned.
func LoadSkills(planSpecDir, grillSpecDir string) (*SkillCache, error) {
	contents := make(map[string]string, len(planSpecFiles)+len(grillSpecFiles))

	// Load plan-spec files.
	for _, name := range planSpecFiles {
		p := filepath.Join(planSpecDir, name)
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("missing skill file: %s", p)
		}
		contents[name] = string(data)
	}

	// Load grill-spec files.
	for _, name := range grillSpecFiles {
		p := filepath.Join(grillSpecDir, name)
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("missing skill file: %s", p)
		}
		contents[name] = string(data)
	}

	// Compute directory checksums.
	planSum, err := computeDirChecksum(planSpecDir, planSpecFiles)
	if err != nil {
		return nil, fmt.Errorf("checksum plan-spec dir: %w", err)
	}
	grillSum, err := computeDirChecksum(grillSpecDir, grillSpecFiles)
	if err != nil {
		return nil, fmt.Errorf("checksum grill-spec dir: %w", err)
	}

	return &SkillCache{
		contents: contents,
		checksums: map[string]string{
			"plan_spec":  planSum,
			"grill_spec": grillSum,
		},
		loaded: true,
	}, nil
}

// GetSkillContent returns the cached content for the named skill file. An
// error is returned if the name does not correspond to a loaded skill file.
func (sc *SkillCache) GetSkillContent(name string) (string, error) {
	content, ok := sc.contents[name]
	if !ok {
		return "", fmt.Errorf("unknown skill file: %q", name)
	}
	return content, nil
}

// GetChecksums returns a map of directory name ("plan_spec", "grill_spec") to
// its SHA-256 checksum in the format "sha256:<hex>".
func (sc *SkillCache) GetChecksums() map[string]string {
	out := make(map[string]string, len(sc.checksums))
	for k, v := range sc.checksums {
		out[k] = v
	}
	return out
}

// computeDirChecksum computes a deterministic SHA-256 checksum over the given
// files within dir. Files are processed in sorted order; each file's name and
// contents are fed into the hash so that renames are also detected.
func computeDirChecksum(dir string, files []string) (string, error) {
	sorted := make([]string, len(files))
	copy(sorted, files)
	sort.Strings(sorted)

	h := sha256.New()
	for _, name := range sorted {
		p := filepath.Join(dir, name)
		data, err := os.ReadFile(p)
		if err != nil {
			return "", fmt.Errorf("reading %s: %w", p, err)
		}
		// Include the file name in the hash so identical content under
		// different names produces a different checksum.
		fmt.Fprintf(h, "%s\n", name)
		h.Write(data)
	}
	return fmt.Sprintf("sha256:%x", h.Sum(nil)), nil
}
