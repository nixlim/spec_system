package codedoc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupIncrementalTestDir creates a temp directory with a codebase layout
// and returns the path.
func setupIncrementalTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// Create modules with source files.
	modules := map[string][]string{
		"internal/api":     {"handler.go", "routes.go"},
		"internal/core":    {"engine.go"},
		"internal/storage": {"db.go", "cache.go"},
		"cmd/server":       {"main.go"},
	}
	for mod, files := range modules {
		modDir := filepath.Join(dir, mod)
		os.MkdirAll(modDir, 0o755)
		for _, f := range files {
			os.WriteFile(filepath.Join(modDir, f), []byte("package "+filepath.Base(mod)), 0o644)
		}
	}
	return dir
}

func writeManifest(t *testing.T, codePath, docsDir string, manifest ManifestFile) {
	t.Helper()
	docsPath := filepath.Join(codePath, docsDir)
	os.MkdirAll(docsPath, 0o755)
	data, _ := json.Marshal(manifest)
	os.WriteFile(filepath.Join(docsPath, manifestFileName), data, 0o644)
}

// ---------------------------------------------------------------------------
// LoadManifest tests
// ---------------------------------------------------------------------------

func TestIncremental_LoadManifest_Valid(t *testing.T) {
	dir := t.TempDir()
	manifest := ManifestFile{
		SchemaVersion:   "1.0",
		WorkflowFeature: "test",
		Modules: []ManifestModule{
			{Path: "internal/api", ContentHash: "sha256:abc123"},
		},
	}
	writeManifest(t, dir, "docs", manifest)
	m, warn := LoadManifest(dir, "docs")
	if m == nil {
		t.Fatalf("expected valid manifest, got nil with warning: %s", warn)
	}
	if warn != "" {
		t.Errorf("expected no warning, got: %s", warn)
	}
	if m.SchemaVersion != "1.0" {
		t.Errorf("expected schema_version 1.0, got %s", m.SchemaVersion)
	}
	if len(m.Modules) != 1 {
		t.Errorf("expected 1 module, got %d", len(m.Modules))
	}
}

func TestIncremental_LoadManifest_Missing(t *testing.T) {
	dir := t.TempDir()
	m, warn := LoadManifest(dir, "docs")
	if m != nil {
		t.Error("expected nil manifest when file is missing")
	}
	if !strings.Contains(warn, "falling back to full mode") {
		t.Errorf("expected 'falling back to full mode' in warning, got: %s", warn)
	}
}

func TestIncremental_LoadManifest_Corrupt(t *testing.T) {
	dir := t.TempDir()
	docsDir := filepath.Join(dir, "docs")
	os.MkdirAll(docsDir, 0o755)
	os.WriteFile(filepath.Join(docsDir, manifestFileName), []byte("{invalid json"), 0o644)
	m, warn := LoadManifest(dir, "docs")
	if m != nil {
		t.Error("expected nil manifest when file is corrupt")
	}
	if !strings.Contains(warn, "cannot parse") {
		t.Errorf("expected 'cannot parse' in warning, got: %s", warn)
	}
	if !strings.Contains(warn, "falling back to full mode") {
		t.Errorf("expected 'falling back to full mode' in warning, got: %s", warn)
	}
}

func TestIncremental_LoadManifest_EmptySchemaVersion(t *testing.T) {
	dir := t.TempDir()
	manifest := ManifestFile{WorkflowFeature: "test"}
	writeManifest(t, dir, "docs", manifest)
	m, warn := LoadManifest(dir, "docs")
	if m != nil {
		t.Error("expected nil manifest when schema_version is empty")
	}
	if !strings.Contains(warn, "empty schema_version") {
		t.Errorf("expected 'empty schema_version' in warning, got: %s", warn)
	}
}

// ---------------------------------------------------------------------------
// ComputeModuleHash tests
// ---------------------------------------------------------------------------

func TestIncremental_ComputeModuleHash_Deterministic(t *testing.T) {
	dir := setupIncrementalTestDir(t)
	modPath := filepath.Join(dir, "internal/api")

	hash1, err := ComputeModuleHash(modPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	hash2, err := ComputeModuleHash(modPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hash1 != hash2 {
		t.Errorf("expected deterministic hash, got %s and %s", hash1, hash2)
	}
	if !strings.HasPrefix(hash1, "sha256:") {
		t.Errorf("expected sha256: prefix, got %s", hash1)
	}
}

func TestIncremental_ComputeModuleHash_ChangesOnFileEdit(t *testing.T) {
	dir := setupIncrementalTestDir(t)
	modPath := filepath.Join(dir, "internal/api")

	hash1, _ := ComputeModuleHash(modPath)
	os.WriteFile(filepath.Join(modPath, "handler.go"), []byte("package api // modified"), 0o644)
	hash2, _ := ComputeModuleHash(modPath)

	if hash1 == hash2 {
		t.Error("expected hash to change after file edit")
	}
}

// ---------------------------------------------------------------------------
// ComputeIncrementalChanges tests
// ---------------------------------------------------------------------------

func TestIncremental_ComputeChanges_NoChanges(t *testing.T) {
	dir := setupIncrementalTestDir(t)

	// Compute hashes for all modules.
	modules := []ManifestModule{
		{Path: "internal/api"},
		{Path: "internal/core"},
		{Path: "internal/storage"},
		{Path: "cmd/server"},
	}
	for i := range modules {
		hash, _ := ComputeModuleHash(filepath.Join(dir, modules[i].Path))
		modules[i].ContentHash = hash
	}

	manifest := ManifestFile{
		SchemaVersion: "1.0",
		Modules:       modules,
	}

	changes, err := ComputeIncrementalChanges(dir, &manifest)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(changes.AddedModules) != 0 {
		t.Errorf("expected no added modules, got %v", changes.AddedModules)
	}
	if len(changes.ModifiedModules) != 0 {
		t.Errorf("expected no modified modules, got %v", changes.ModifiedModules)
	}
	if len(changes.RemovedModules) != 0 {
		t.Errorf("expected no removed modules, got %v", changes.RemovedModules)
	}
	if changes.Recommendation != "incremental" {
		t.Errorf("expected 'incremental' recommendation, got %s", changes.Recommendation)
	}
}

func TestIncremental_ComputeChanges_ModifiedModule(t *testing.T) {
	dir := setupIncrementalTestDir(t)

	// Compute initial hashes.
	modules := []ManifestModule{
		{Path: "internal/api"},
		{Path: "internal/core"},
		{Path: "internal/storage"},
		{Path: "cmd/server"},
	}
	for i := range modules {
		hash, _ := ComputeModuleHash(filepath.Join(dir, modules[i].Path))
		modules[i].ContentHash = hash
	}
	manifest := ManifestFile{SchemaVersion: "1.0", Modules: modules}

	// Modify one module.
	os.WriteFile(filepath.Join(dir, "internal/api/handler.go"), []byte("package api // changed"), 0o644)

	changes, err := ComputeIncrementalChanges(dir, &manifest)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(changes.ModifiedModules) != 1 || changes.ModifiedModules[0] != "internal/api" {
		t.Errorf("expected 1 modified module (internal/api), got %v", changes.ModifiedModules)
	}
	if len(changes.AddedModules) != 0 {
		t.Errorf("expected no added modules, got %v", changes.AddedModules)
	}
}

func TestIncremental_ComputeChanges_AddedModule(t *testing.T) {
	dir := setupIncrementalTestDir(t)

	// Manifest only knows about 2 modules.
	modules := []ManifestModule{
		{Path: "internal/api", ContentHash: "sha256:old"},
		{Path: "internal/core", ContentHash: "sha256:old"},
	}
	manifest := ManifestFile{SchemaVersion: "1.0", Modules: modules}

	changes, err := ComputeIncrementalChanges(dir, &manifest)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// internal/storage and cmd/server are new.
	addedSet := make(map[string]bool)
	for _, m := range changes.AddedModules {
		addedSet[m] = true
	}
	if !addedSet["internal/storage"] {
		t.Error("expected internal/storage in added modules")
	}
	if !addedSet["cmd/server"] {
		t.Error("expected cmd/server in added modules")
	}
}

func TestIncremental_ComputeChanges_RemovedModule(t *testing.T) {
	dir := setupIncrementalTestDir(t)

	// Manifest includes a module that no longer exists.
	apiHash, _ := ComputeModuleHash(filepath.Join(dir, "internal/api"))
	modules := []ManifestModule{
		{Path: "internal/api", ContentHash: apiHash},
		{Path: "internal/legacy", ContentHash: "sha256:gone"},
	}
	manifest := ManifestFile{SchemaVersion: "1.0", Modules: modules}

	changes, err := ComputeIncrementalChanges(dir, &manifest)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	removedSet := make(map[string]bool)
	for _, m := range changes.RemovedModules {
		removedSet[m] = true
	}
	if !removedSet["internal/legacy"] {
		t.Errorf("expected internal/legacy in removed modules, got %v", changes.RemovedModules)
	}
}

func TestIncremental_ComputeChanges_RecommendsFull_Over20Percent(t *testing.T) {
	dir := setupIncrementalTestDir(t)

	// 5 modules in manifest, 2 changed = 40% > 20%.
	modules := []ManifestModule{
		{Path: "internal/api", ContentHash: "sha256:old1"},
		{Path: "internal/core", ContentHash: "sha256:old2"},
		{Path: "internal/storage", ContentHash: "sha256:old3"},
		{Path: "cmd/server", ContentHash: "sha256:old4"},
		{Path: "internal/legacy", ContentHash: "sha256:old5"},
	}
	manifest := ManifestFile{SchemaVersion: "1.0", Modules: modules}

	changes, err := ComputeIncrementalChanges(dir, &manifest)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// All modules have different hashes + one removed = many changes.
	if changes.Recommendation != "full" {
		t.Errorf("expected 'full' recommendation for >20%% changes, got %s", changes.Recommendation)
	}
}

func TestIncremental_ComputeChanges_RecommendsIncremental_Under20Percent(t *testing.T) {
	// Create 10 real modules, modify 1 = 10% < 20%.
	dir2 := t.TempDir()
	mods2 := make([]ManifestModule, 0, 10)
	for i := 0; i < 10; i++ {
		modName := filepath.Join("pkg", string(rune('a'+i)))
		modDir := filepath.Join(dir2, modName)
		os.MkdirAll(modDir, 0o755)
		content := []byte("package " + string(rune('a'+i)))
		os.WriteFile(filepath.Join(modDir, "main.go"), content, 0o644)
		hash, _ := ComputeModuleHash(modDir)
		mods2 = append(mods2, ManifestModule{Path: modName, ContentHash: hash})
	}
	manifest2 := ManifestFile{SchemaVersion: "1.0", Modules: mods2}

	// Modify 1 out of 10 = 10%.
	os.WriteFile(filepath.Join(dir2, "pkg/a/main.go"), []byte("package a // changed"), 0o644)

	changes2, err := ComputeIncrementalChanges(dir2, &manifest2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changes2.Recommendation != "incremental" {
		t.Errorf("expected 'incremental' for <20%% changes, got %s (reason: %s)",
			changes2.Recommendation, changes2.Reason)
	}
}

func TestIncremental_ComputeChanges_NilManifest(t *testing.T) {
	_, err := ComputeIncrementalChanges("/tmp", nil)
	if err == nil {
		t.Error("expected error for nil manifest")
	}
}

// ---------------------------------------------------------------------------
// ShouldRegenerateArchitecture tests
// ---------------------------------------------------------------------------

func TestIncremental_ShouldRegenerateArchitecture_NoChange(t *testing.T) {
	edges := []DependencyEdge{{From: "a", To: "b"}, {From: "b", To: "c"}}
	prev := &DiscoveryOutput{DependencyGraph: DependencyGraph{Edges: edges}}
	curr := &DiscoveryOutput{DependencyGraph: DependencyGraph{Edges: edges}}
	if ShouldRegenerateArchitecture(prev, curr) {
		t.Error("expected no regeneration when edges are the same")
	}
}

func TestIncremental_ShouldRegenerateArchitecture_EdgeAdded(t *testing.T) {
	prev := &DiscoveryOutput{DependencyGraph: DependencyGraph{
		Edges: []DependencyEdge{{From: "a", To: "b"}},
	}}
	curr := &DiscoveryOutput{DependencyGraph: DependencyGraph{
		Edges: []DependencyEdge{{From: "a", To: "b"}, {From: "b", To: "c"}},
	}}
	if !ShouldRegenerateArchitecture(prev, curr) {
		t.Error("expected regeneration when edge is added")
	}
}

func TestIncremental_ShouldRegenerateArchitecture_EdgeRemoved(t *testing.T) {
	prev := &DiscoveryOutput{DependencyGraph: DependencyGraph{
		Edges: []DependencyEdge{{From: "a", To: "b"}, {From: "b", To: "c"}},
	}}
	curr := &DiscoveryOutput{DependencyGraph: DependencyGraph{
		Edges: []DependencyEdge{{From: "a", To: "b"}},
	}}
	if !ShouldRegenerateArchitecture(prev, curr) {
		t.Error("expected regeneration when edge is removed")
	}
}

func TestIncremental_ShouldRegenerateArchitecture_NilPrev(t *testing.T) {
	curr := &DiscoveryOutput{}
	if !ShouldRegenerateArchitecture(nil, curr) {
		t.Error("expected regeneration when prev is nil")
	}
}

// ---------------------------------------------------------------------------
// FilterChangedModules tests
// ---------------------------------------------------------------------------

func TestIncremental_FilterChangedModules(t *testing.T) {
	modules := []ModuleInfo{
		{Path: "internal/api"},
		{Path: "internal/core"},
		{Path: "internal/storage"},
	}
	changes := &IncrementalChanges{
		AddedModules:    []string{"internal/storage"},
		ModifiedModules: []string{"internal/api"},
	}
	filtered := FilterChangedModules(modules, changes)
	if len(filtered) != 2 {
		t.Fatalf("expected 2 filtered modules, got %d", len(filtered))
	}
	paths := make(map[string]bool)
	for _, m := range filtered {
		paths[m.Path] = true
	}
	if !paths["internal/api"] || !paths["internal/storage"] {
		t.Errorf("expected api and storage, got %v", paths)
	}
}

func TestIncremental_FilterChangedModules_NilChanges(t *testing.T) {
	modules := []ModuleInfo{{Path: "internal/api"}}
	filtered := FilterChangedModules(modules, nil)
	if len(filtered) != 1 {
		t.Error("expected all modules returned when changes is nil")
	}
}

// ---------------------------------------------------------------------------
// UpdateManifest tests
// ---------------------------------------------------------------------------

func TestIncremental_UpdateManifest_PreservesUnchanged(t *testing.T) {
	prev := &ManifestFile{
		SchemaVersion: "1.0",
		Modules: []ManifestModule{
			{Path: "internal/api", ContentHash: "sha256:aaa"},
			{Path: "internal/core", ContentHash: "sha256:bbb"},
			{Path: "internal/storage", ContentHash: "sha256:ccc"},
		},
	}
	changes := &IncrementalChanges{
		ModifiedModules: []string{"internal/api"},
	}
	currentModules := []ModuleInfo{
		{Path: "internal/api", ContentHash: "sha256:aaa_new"},
	}
	result := UpdateManifest(prev, changes, currentModules, "/tmp", "test")

	if len(result.Modules) != 3 {
		t.Fatalf("expected 3 modules, got %d", len(result.Modules))
	}
	// Check that internal/api was updated.
	for _, m := range result.Modules {
		if m.Path == "internal/api" && m.ContentHash != "sha256:aaa_new" {
			t.Errorf("expected updated hash for internal/api, got %s", m.ContentHash)
		}
		if m.Path == "internal/core" && m.ContentHash != "sha256:bbb" {
			t.Errorf("expected preserved hash for internal/core, got %s", m.ContentHash)
		}
	}
}

func TestIncremental_UpdateManifest_RemovesDeleted(t *testing.T) {
	prev := &ManifestFile{
		SchemaVersion: "1.0",
		Modules: []ManifestModule{
			{Path: "internal/api", ContentHash: "sha256:aaa"},
			{Path: "internal/legacy", ContentHash: "sha256:old"},
		},
	}
	changes := &IncrementalChanges{
		RemovedModules: []string{"internal/legacy"},
	}
	result := UpdateManifest(prev, changes, nil, "/tmp", "test")
	if len(result.Modules) != 1 {
		t.Fatalf("expected 1 module after removal, got %d", len(result.Modules))
	}
	if result.Modules[0].Path != "internal/api" {
		t.Errorf("expected internal/api preserved, got %s", result.Modules[0].Path)
	}
}

func TestIncremental_UpdateManifest_SortedOutput(t *testing.T) {
	prev := &ManifestFile{
		SchemaVersion: "1.0",
		Modules: []ManifestModule{
			{Path: "z/mod", ContentHash: "sha256:z"},
			{Path: "a/mod", ContentHash: "sha256:a"},
		},
	}
	result := UpdateManifest(prev, nil, nil, "/tmp", "test")
	if result.Modules[0].Path != "a/mod" {
		t.Errorf("expected sorted output, first module is %s", result.Modules[0].Path)
	}
}
