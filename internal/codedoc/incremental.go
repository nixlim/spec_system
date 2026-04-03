package codedoc

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// manifestFileName is the name of the manifest file in the docs output directory.
const manifestFileName = ".codedoc-manifest.json"

// LoadManifest reads and parses the .codedoc-manifest.json from the target
// docs directory. Returns nil and a warning message if the file is missing
// or corrupt. The caller should fall back to full mode when nil is returned.
func LoadManifest(codePath, docsOutputDir string) (*ManifestFile, string) {
	manifestPath := filepath.Join(codePath, docsOutputDir, manifestFileName)
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "no .codedoc-manifest.json found; falling back to full mode"
		}
		return nil, fmt.Sprintf("cannot read %s: %v; falling back to full mode", manifestPath, err)
	}
	var manifest ManifestFile
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Sprintf("cannot parse %s: %v; falling back to full mode", manifestPath, err)
	}
	if manifest.SchemaVersion == "" {
		return nil, fmt.Sprintf("%s has empty schema_version; falling back to full mode", manifestPath)
	}
	return &manifest, ""
}

// ComputeModuleHash computes a SHA-256 hash of all source files in a module
// directory. Files are sorted by path for deterministic output.
func ComputeModuleHash(modulePath string) (string, error) {
	h := sha256.New()

	var files []string
	err := filepath.WalkDir(modulePath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Skip hidden directories and common non-source directories.
			name := d.Name()
			if strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		// Include source files only.
		if isSourceFile(path) {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("walking module %s: %w", modulePath, err)
	}

	sort.Strings(files)
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return "", fmt.Errorf("reading %s: %w", f, err)
		}
		h.Write([]byte(f))
		h.Write(data)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// isSourceFile returns true for common source file extensions.
func isSourceFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go", ".py", ".js", ".ts", ".tsx", ".jsx", ".java", ".rs", ".rb",
		".c", ".h", ".cpp", ".hpp", ".cs", ".swift", ".kt", ".scala",
		".sh", ".bash", ".zsh", ".yaml", ".yml", ".json", ".toml",
		".md", ".txt", ".html", ".css", ".sql", ".proto", ".graphql":
		return true
	}
	return false
}

// ComputeIncrementalChanges compares the current codebase state against
// a manifest from a previous documentation run and produces an
// IncrementalChanges struct describing what has changed.
//
// codePath is the root of the target repository. The manifest contains
// module paths relative to codePath and their content hashes from the
// previous run.
func ComputeIncrementalChanges(codePath string, manifest *ManifestFile) (*IncrementalChanges, error) {
	if manifest == nil {
		return nil, fmt.Errorf("manifest is nil")
	}

	// Build a lookup of previous module hashes.
	prevModules := make(map[string]string, len(manifest.Modules))
	for _, m := range manifest.Modules {
		prevModules[m.Path] = m.ContentHash
	}

	// Scan current modules by walking codePath for directories containing source files.
	currentModules, err := discoverModulePaths(codePath)
	if err != nil {
		return nil, fmt.Errorf("discovering current modules: %w", err)
	}

	var (
		added    []string
		modified []string
		removed  []string
		addedF   int
		modifiedF int
		removedF int
	)

	// Check current modules against previous.
	for _, modPath := range currentModules {
		fullPath := filepath.Join(codePath, modPath)
		hash, err := ComputeModuleHash(fullPath)
		if err != nil {
			log.Printf("[incremental] warning: cannot hash module %s: %v", modPath, err)
			modified = append(modified, modPath)
			continue
		}
		prevHash, existed := prevModules[modPath]
		if !existed {
			added = append(added, modPath)
			addedF += countSourceFiles(fullPath)
		} else if hash != prevHash {
			modified = append(modified, modPath)
			modifiedF += countSourceFiles(fullPath)
		}
		delete(prevModules, modPath)
	}

	// Remaining entries in prevModules are removed modules.
	for modPath := range prevModules {
		removed = append(removed, modPath)
	}
	sort.Strings(removed)
	removedF = len(removed) // approximate: count modules as files

	// Compute recommendation.
	totalPrev := len(manifest.Modules)
	totalChanged := len(added) + len(modified) + len(removed)
	recommendation := "incremental"
	reason := fmt.Sprintf("%d modules changed out of %d total", totalChanged, totalPrev)

	if totalPrev > 0 {
		changeRatio := float64(totalChanged) / float64(totalPrev)
		if changeRatio >= 0.20 {
			recommendation = "full"
			reason = fmt.Sprintf("%d modules changed out of %d (%.0f%%) — full mode recommended",
				totalChanged, totalPrev, changeRatio*100)
		} else {
			reason = fmt.Sprintf("%d modules changed out of %d (%.0f%%) — incremental is appropriate",
				totalChanged, totalPrev, changeRatio*100)
		}
	} else if totalChanged > 0 {
		recommendation = "full"
		reason = "no previous modules recorded; full mode recommended"
	}

	return &IncrementalChanges{
		AddedModules:    added,
		ModifiedModules: modified,
		RemovedModules:  removed,
		AddedFiles:      addedF,
		ModifiedFiles:   modifiedF,
		RemovedFiles:    removedF,
		Recommendation:  recommendation,
		Reason:          reason,
	}, nil
}

// ShouldRegenerateArchitecture returns true if the dependency graph has
// changed between the previous and current discovery outputs, requiring
// full regeneration of architecture diagrams.
func ShouldRegenerateArchitecture(prev, current *DiscoveryOutput) bool {
	if prev == nil || current == nil {
		return true
	}
	prevEdges := edgeSet(prev.DependencyGraph.Edges)
	currEdges := edgeSet(current.DependencyGraph.Edges)
	if len(prevEdges) != len(currEdges) {
		return true
	}
	for e := range currEdges {
		if !prevEdges[e] {
			return true
		}
	}
	return false
}

// edgeSet converts a slice of DependencyEdge to a set of "from->to" strings.
func edgeSet(edges []DependencyEdge) map[string]bool {
	m := make(map[string]bool, len(edges))
	for _, e := range edges {
		m[e.From+"->"+e.To] = true
	}
	return m
}

// FilterChangedModules returns only the modules from a discovery output
// that appear in the incremental changes (added or modified).
func FilterChangedModules(modules []ModuleInfo, changes *IncrementalChanges) []ModuleInfo {
	if changes == nil {
		return modules
	}
	changedSet := make(map[string]bool)
	for _, m := range changes.AddedModules {
		changedSet[m] = true
	}
	for _, m := range changes.ModifiedModules {
		changedSet[m] = true
	}
	var result []ModuleInfo
	for _, m := range modules {
		if changedSet[m.Path] {
			result = append(result, m)
		}
	}
	return result
}

// UpdateManifest returns a new manifest with updated entries for changed
// modules while preserving unchanged entries from the previous manifest.
func UpdateManifest(prev *ManifestFile, changes *IncrementalChanges, currentModules []ModuleInfo, codePath, featureName string) ManifestFile {
	// Build lookup of current module hashes.
	currentHashes := make(map[string]string)
	for _, m := range currentModules {
		currentHashes[m.Path] = m.ContentHash
	}

	// Build set of removed modules.
	removedSet := make(map[string]bool)
	if changes != nil {
		for _, m := range changes.RemovedModules {
			removedSet[m] = true
		}
	}

	// Start with previous modules, update changed ones, remove deleted ones.
	moduleMap := make(map[string]ManifestModule)
	if prev != nil {
		for _, m := range prev.Modules {
			if !removedSet[m.Path] {
				moduleMap[m.Path] = m
			}
		}
	}

	// Update/add changed modules.
	if changes != nil {
		for _, path := range append(changes.AddedModules, changes.ModifiedModules...) {
			hash := currentHashes[path]
			moduleMap[path] = ManifestModule{
				Path:        path,
				ContentHash: hash,
			}
		}
	}

	// Collect into sorted slice.
	modules := make([]ManifestModule, 0, len(moduleMap))
	for _, m := range moduleMap {
		modules = append(modules, m)
	}
	sort.Slice(modules, func(i, j int) bool {
		return modules[i].Path < modules[j].Path
	})

	return ManifestFile{
		SchemaVersion:   "1.0",
		WorkflowFeature: featureName,
		Mode:            "incremental",
		Modules:         modules,
	}
}

// discoverModulePaths walks codePath and returns relative paths of directories
// that contain at least one source file. Only top-level meaningful directories
// are returned (e.g. "internal/api", "cmd/server").
func discoverModulePaths(codePath string) ([]string, error) {
	var modules []string
	seen := make(map[string]bool)

	err := filepath.WalkDir(codePath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !isSourceFile(path) {
			return nil
		}
		dir := filepath.Dir(path)
		rel, err := filepath.Rel(codePath, dir)
		if err != nil {
			return nil
		}
		if rel == "." {
			return nil // skip root-level files
		}
		if !seen[rel] {
			seen[rel] = true
			modules = append(modules, rel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(modules)
	return modules, nil
}

// countSourceFiles counts source files in a directory (non-recursive for speed).
func countSourceFiles(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	count := 0
	for _, e := range entries {
		if !e.IsDir() && isSourceFile(e.Name()) {
			count++
		}
	}
	return count
}
