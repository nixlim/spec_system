package codedoc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fixedNow returns a fixed time for deterministic tests.
func fixedNow() time.Time {
	return time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
}

func newTestWriter(t *testing.T) (*Writer, string) {
	t.Helper()
	tmpDir := t.TempDir()
	codePath := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(filepath.Join(codePath, "docs"), 0755); err != nil {
		t.Fatal(err)
	}
	w := &Writer{
		Config:   DefaultCodedocConfig(),
		CodePath: codePath,
		Feature:  "test-feature",
		Now:      fixedNow,
	}
	return w, codePath
}

// ---------------------------------------------------------------------------
// Staging tests
// ---------------------------------------------------------------------------

func TestWritingStagingThenMove(t *testing.T) {
	w, codePath := newTestWriter(t)

	files := map[string][]byte{
		"report.md": []byte("# Report\nContent here."),
	}
	result, err := w.Write(files)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	// File should be at final location.
	finalPath := filepath.Join(codePath, "docs", "report.md")
	data, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatalf("final file not found: %v", err)
	}
	if !strings.Contains(string(data), "# Report") {
		t.Errorf("final file content unexpected: %s", data)
	}

	// Staging directory should be cleaned up.
	stagingDir := filepath.Join(codePath, "docs", ".codedoc-staging")
	if _, err := os.Stat(stagingDir); !os.IsNotExist(err) {
		t.Errorf("staging directory should be removed after write")
	}

	if len(result.WrittenFiles) == 0 {
		t.Error("WrittenFiles should not be empty")
	}
}

func TestWritingStagingSubdirectories(t *testing.T) {
	w, codePath := newTestWriter(t)

	files := map[string][]byte{
		"architecture/deps.md": []byte("# Deps"),
		"audit/findings.json":  []byte(`{"findings":[]}`),
	}
	result, err := w.Write(files)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	for _, relPath := range result.WrittenFiles {
		finalPath := filepath.Join(codePath, "docs", relPath)
		if _, err := os.Stat(finalPath); err != nil {
			t.Errorf("file %s not found at final location", relPath)
		}
	}
}

func TestWritingStagingValidationRejectsEmpty(t *testing.T) {
	w, _ := newTestWriter(t)

	files := map[string][]byte{
		"report.md": {},
	}

	// Write will create the staging file as empty, then validation should catch it.
	_, err := w.Write(files)
	if err == nil {
		t.Fatal("expected error for empty staged file")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error should mention 'empty': %v", err)
	}
}

// ---------------------------------------------------------------------------
// Backup tests
// ---------------------------------------------------------------------------

func TestWritingBackupCreated(t *testing.T) {
	w, codePath := newTestWriter(t)

	// Create existing file.
	existingPath := filepath.Join(codePath, "docs", "report.md")
	if err := os.WriteFile(existingPath, []byte("old content"), 0644); err != nil {
		t.Fatal(err)
	}

	files := map[string][]byte{
		"report.md": []byte("new content"),
	}
	_, err := w.Write(files)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Check backup exists.
	bakPath := existingPath + ".bak.20260401-120000"
	bakData, err := os.ReadFile(bakPath)
	if err != nil {
		t.Fatalf("backup file not found: %v", err)
	}
	if string(bakData) != "old content" {
		t.Errorf("backup content: got %q, want 'old content'", bakData)
	}

	// Check final file has new content.
	finalData, err := os.ReadFile(existingPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(finalData) != "new content" {
		t.Errorf("final content: got %q, want 'new content'", finalData)
	}
}

// ---------------------------------------------------------------------------
// Lock tests
// ---------------------------------------------------------------------------

func TestWritingLockCreatedAndReleased(t *testing.T) {
	w, codePath := newTestWriter(t)

	files := map[string][]byte{
		"report.md": []byte("content"),
	}
	_, err := w.Write(files)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Lock should be released (file removed).
	lockPath := filepath.Join(codePath, "docs", ".codedoc-write.lock")
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Error("lock file should be removed after successful write")
	}
}

func TestWritingLockContainsFeatureAndPID(t *testing.T) {
	w, codePath := newTestWriter(t)

	// Manually acquire lock to inspect contents.
	if err := w.tryAcquireLock(); err != nil {
		t.Fatalf("tryAcquireLock: %v", err)
	}
	defer w.releaseLock()

	lockPath := filepath.Join(codePath, "docs", ".codedoc-write.lock")
	info, err := ParseLockFile(lockPath)
	if err != nil {
		t.Fatalf("ParseLockFile: %v", err)
	}
	if info.Feature != "test-feature" {
		t.Errorf("lock feature: got %q, want test-feature", info.Feature)
	}
	if info.PID != os.Getpid() {
		t.Errorf("lock PID: got %d, want %d", info.PID, os.Getpid())
	}
}

func TestWritingStaleLockBroken(t *testing.T) {
	w, codePath := newTestWriter(t)

	lockPath := filepath.Join(codePath, "docs", ".codedoc-write.lock")
	if err := WriteStaleLock(lockPath, "old-feature"); err != nil {
		t.Fatal(err)
	}

	// Should succeed because the PID 99999999 is dead.
	files := map[string][]byte{
		"report.md": []byte("content"),
	}
	_, err := w.Write(files)
	if err != nil {
		t.Fatalf("Write should succeed after breaking stale lock: %v", err)
	}
}

func TestWritingLockHeldByLiveProcess(t *testing.T) {
	w, codePath := newTestWriter(t)
	w.Config.WriteLockTimeoutSeconds = 1 // short timeout for test

	// Write a lock with our own PID (alive).
	lockPath := filepath.Join(codePath, "docs", ".codedoc-write.lock")
	info := lockInfo{Feature: "other-feature", PID: os.Getpid()}
	data, _ := json.Marshal(info)
	if err := os.WriteFile(lockPath, data, 0644); err != nil {
		t.Fatal(err)
	}

	files := map[string][]byte{
		"report.md": []byte("content"),
	}
	_, err := w.Write(files)
	if err == nil {
		t.Fatal("expected error when lock held by live process")
	}
	if !strings.Contains(err.Error(), "lock") {
		t.Errorf("error should mention lock: %v", err)
	}
}

func TestWritingLockReleasedOnError(t *testing.T) {
	w, codePath := newTestWriter(t)

	// Provide empty file to trigger validation error.
	files := map[string][]byte{
		"report.md": {},
	}
	_, _ = w.Write(files)

	// Lock should still be released.
	lockPath := filepath.Join(codePath, "docs", ".codedoc-write.lock")
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Error("lock file should be removed even on error")
	}
}

// ---------------------------------------------------------------------------
// Drift detection tests
// ---------------------------------------------------------------------------

func TestWritingDriftDetection(t *testing.T) {
	w, codePath := newTestWriter(t)

	// Create some module directories with content.
	modPath := filepath.Join(codePath, "internal", "api")
	if err := os.MkdirAll(modPath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modPath, "handler.go"), []byte("package api"), 0644); err != nil {
		t.Fatal(err)
	}

	// Compute hash before modification.
	hash := computeDirHash(modPath)

	// Set up discovery output with the hash.
	w.DiscoveryOutput = &DiscoveryOutput{
		Modules: []ModuleInfo{
			{Path: "internal/api", ContentHash: hash},
		},
	}

	// No drift — warning should be empty.
	warning := w.DetectDrift()
	if warning != "" {
		t.Errorf("expected no drift warning, got: %s", warning)
	}

	// Modify the file to cause drift.
	if err := os.WriteFile(filepath.Join(modPath, "handler.go"), []byte("package api // changed"), 0644); err != nil {
		t.Fatal(err)
	}

	// With threshold 0.20 and 1/1 modules changed = 100%, should warn.
	warning = w.DetectDrift()
	if warning == "" {
		t.Error("expected drift warning after modification")
	}
	if !strings.Contains(warning, "changed significantly") {
		t.Errorf("drift warning should mention 'changed significantly': %s", warning)
	}
}

func TestWritingDriftBelowThreshold(t *testing.T) {
	w, codePath := newTestWriter(t)

	// Create 5 module directories. Only change 1 (20% = at threshold, not above).
	var modules []ModuleInfo
	for i := 0; i < 5; i++ {
		modName := filepath.Join("internal", string(rune('a'+i)))
		modPath := filepath.Join(codePath, modName)
		if err := os.MkdirAll(modPath, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(modPath, "main.go"), []byte("package "+string(rune('a'+i))), 0644); err != nil {
			t.Fatal(err)
		}
		hash := computeDirHash(modPath)
		modules = append(modules, ModuleInfo{Path: modName, ContentHash: hash})
	}

	w.DiscoveryOutput = &DiscoveryOutput{Modules: modules}

	// Change exactly 1 of 5 modules (20% = at threshold, not above it).
	changedPath := filepath.Join(codePath, "internal", "a", "main.go")
	if err := os.WriteFile(changedPath, []byte("package a // changed"), 0644); err != nil {
		t.Fatal(err)
	}

	warning := w.DetectDrift()
	if warning != "" {
		t.Errorf("20%% drift (at threshold) should not warn, got: %s", warning)
	}
}

// ---------------------------------------------------------------------------
// Manual marker preservation tests
// ---------------------------------------------------------------------------

func TestWritingManualMarkerPreserved(t *testing.T) {
	w, codePath := newTestWriter(t)

	// Create existing file with manual markers.
	existingContent := `# Report
## Overview
Generated overview.
## Custom Notes
<!-- manual -->
These are my hand-written notes.
<!-- /manual -->
## Architecture
Generated architecture.
`
	existingPath := filepath.Join(codePath, "docs", "report.md")
	if err := os.WriteFile(existingPath, []byte(existingContent), 0644); err != nil {
		t.Fatal(err)
	}

	// New content replaces everything but should preserve manual block.
	newContent := `# Report
## Overview
Updated overview.
## Custom Notes
New generated notes.
## Architecture
Updated architecture.
`

	files := map[string][]byte{
		"report.md": []byte(newContent),
	}
	_, err := w.Write(files)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	finalData, err := os.ReadFile(existingPath)
	if err != nil {
		t.Fatal(err)
	}
	finalStr := string(finalData)

	if !strings.Contains(finalStr, "<!-- manual -->") {
		t.Error("manual marker should be preserved")
	}
	if !strings.Contains(finalStr, "These are my hand-written notes.") {
		t.Error("manual content should be preserved")
	}
}

func TestWritingManualMarkerSectionRemoved(t *testing.T) {
	w, codePath := newTestWriter(t)

	// Existing file has manual markers under a heading that new content removes.
	existingContent := `# Report
## Deprecated Section
<!-- manual -->
Important manual note.
<!-- /manual -->
`
	existingPath := filepath.Join(codePath, "docs", "report.md")
	if err := os.WriteFile(existingPath, []byte(existingContent), 0644); err != nil {
		t.Fatal(err)
	}

	// New content doesn't have "Deprecated Section".
	newContent := `# Report
## Overview
New content.
`

	files := map[string][]byte{
		"report.md": []byte(newContent),
	}
	_, err := w.Write(files)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	finalData, err := os.ReadFile(existingPath)
	if err != nil {
		t.Fatal(err)
	}
	finalStr := string(finalData)

	if !strings.Contains(finalStr, "Important manual note.") {
		t.Error("manual content should be preserved even when section is removed")
	}
	if !strings.Contains(finalStr, "WARNING: original section removed, manual content preserved") {
		t.Error("should include warning comment when section is removed")
	}
}

// ---------------------------------------------------------------------------
// Manifest tests
// ---------------------------------------------------------------------------

func TestWritingManifestWrittenAtomically(t *testing.T) {
	w, codePath := newTestWriter(t)

	files := map[string][]byte{
		"report.md": []byte("content"),
	}
	result, err := w.Write(files)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Check manifest exists.
	manifestPath := filepath.Join(codePath, "docs", ".codedoc-manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("manifest file not found: %v", err)
	}

	var manifest ManifestFile
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("manifest JSON invalid: %v", err)
	}

	if manifest.SchemaVersion != "1.0" {
		t.Errorf("SchemaVersion: got %q, want 1.0", manifest.SchemaVersion)
	}
	if manifest.WorkflowFeature != "test-feature" {
		t.Errorf("WorkflowFeature: got %q, want test-feature", manifest.WorkflowFeature)
	}
	if manifest.GeneratedAt != "2026-04-01T12:00:00Z" {
		t.Errorf("GeneratedAt: got %q, want 2026-04-01T12:00:00Z", manifest.GeneratedAt)
	}

	// Temp file should not exist.
	tmpPath := manifestPath + ".tmp"
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Error("temp manifest file should not exist after atomic write")
	}

	// Check result manifest matches.
	if result.Manifest.SchemaVersion != "1.0" {
		t.Errorf("result manifest SchemaVersion: got %q, want 1.0", result.Manifest.SchemaVersion)
	}
}

func TestWritingManifestFilesDocumented(t *testing.T) {
	w, _ := newTestWriter(t)

	files := map[string][]byte{
		"report.md":              []byte("# Report"),
		"architecture/deps.md":  []byte("# Deps"),
	}
	result, err := w.Write(files)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	if len(result.Manifest.FilesDocumented) != 2 {
		t.Errorf("FilesDocumented count: got %d, want 2", len(result.Manifest.FilesDocumented))
	}

	// Each documented file should have a content hash.
	for _, fd := range result.Manifest.FilesDocumented {
		if fd.ContentHash == "" {
			t.Errorf("FilesDocumented %q has no content hash", fd.Path)
		}
		if !strings.HasPrefix(fd.ContentHash, "sha256:") {
			t.Errorf("FilesDocumented %q hash should start with sha256: got %q", fd.Path, fd.ContentHash)
		}
	}
}

// ---------------------------------------------------------------------------
// Full integration test
// ---------------------------------------------------------------------------

func TestWritingFullIntegration(t *testing.T) {
	w, codePath := newTestWriter(t)

	// Set up discovery output.
	modPath := filepath.Join(codePath, "internal", "api")
	if err := os.MkdirAll(modPath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modPath, "handler.go"), []byte("package api"), 0644); err != nil {
		t.Fatal(err)
	}
	hash := computeDirHash(modPath)
	w.DiscoveryOutput = &DiscoveryOutput{
		SchemaVersion: "1.0",
		Modules: []ModuleInfo{
			{Path: "internal/api", ContentHash: hash},
		},
	}

	// Create existing file to test backup.
	existingPath := filepath.Join(codePath, "docs", "README.md")
	if err := os.WriteFile(existingPath, []byte("old readme"), 0644); err != nil {
		t.Fatal(err)
	}

	files := map[string][]byte{
		"README.md":                     []byte("# Updated README"),
		"as-implemented-report.md":      []byte("# As-Implemented Report\nContent."),
		"architecture/module-deps.md":   []byte("# Module Dependencies\n```mermaid\ngraph TD;\n```"),
	}

	result, err := w.Write(files)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Verify all files written.
	if len(result.WrittenFiles) != 3 {
		t.Errorf("WrittenFiles: got %d, want 3", len(result.WrittenFiles))
	}

	// Verify backup exists for README.md.
	bakPath := existingPath + ".bak.20260401-120000"
	if _, err := os.Stat(bakPath); err != nil {
		t.Errorf("backup not created for README.md: %v", err)
	}

	// Verify manifest.
	if result.Manifest.SchemaVersion != "1.0" {
		t.Error("manifest schema version mismatch")
	}
	if len(result.Manifest.Modules) != 1 {
		t.Errorf("manifest modules: got %d, want 1", len(result.Manifest.Modules))
	}
	if result.Manifest.Modules[0].ContentHash != hash {
		t.Error("manifest module hash should match discovery output")
	}

	// No drift since we haven't changed the source.
	if result.DriftWarning != "" {
		t.Errorf("unexpected drift warning: %s", result.DriftWarning)
	}

	// Staging cleaned up.
	stagingDir := filepath.Join(codePath, "docs", ".codedoc-staging")
	if _, err := os.Stat(stagingDir); !os.IsNotExist(err) {
		t.Error("staging directory should be removed")
	}

	// Lock cleaned up.
	lockPath := filepath.Join(codePath, "docs", ".codedoc-write.lock")
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Error("lock file should be removed")
	}
}
