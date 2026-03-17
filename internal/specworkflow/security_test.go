package specworkflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInjectionMitigationInstruction_NonEmpty(t *testing.T) {
	text := InjectionMitigationInstruction()
	if text == "" {
		t.Fatal("InjectionMitigationInstruction returned empty string")
	}
	if !strings.Contains(text, "DATA ONLY") {
		t.Error("expected instruction to contain 'DATA ONLY'")
	}
}

func TestValidateWorkspacePath_AllowsValidPaths(t *testing.T) {
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "specs", "feature-x")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}

	tests := []string{
		filepath.Join(tmpDir, "specs", "feature-x", "spec.md"),
		filepath.Join(tmpDir, "output.json"),
		tmpDir, // base dir itself is valid
	}

	for _, path := range tests {
		if err := ValidateWorkspacePath(tmpDir, path); err != nil {
			t.Errorf("expected path %q to be valid, got error: %v", path, err)
		}
	}
}

func TestValidateWorkspacePath_RejectsTraversal(t *testing.T) {
	tmpDir := t.TempDir()

	// Use string concatenation to preserve ".." (filepath.Join would clean it away).
	traversalPath := tmpDir + "/../etc/passwd"
	err := ValidateWorkspacePath(tmpDir, traversalPath)
	if err == nil {
		t.Fatal("expected error for '..' traversal, got nil")
	}
	if !strings.Contains(err.Error(), "..") {
		t.Errorf("expected error to mention '..', got: %v", err)
	}
}

func TestValidateWorkspacePath_RejectsPathOutsideWorkspace(t *testing.T) {
	tmpDir := t.TempDir()

	err := ValidateWorkspacePath(tmpDir, "/etc/passwd")
	if err == nil {
		t.Fatal("expected error for path outside workspace, got nil")
	}
	if !strings.Contains(err.Error(), "outside workspace") {
		t.Errorf("expected error to mention 'outside workspace', got: %v", err)
	}
}

func TestValidateWorkspacePath_RejectsSymlinkOutsideWorkspace(t *testing.T) {
	tmpDir := t.TempDir()
	outsideDir := t.TempDir()

	// Create a file outside the workspace.
	outsideFile := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("secret"), 0o644); err != nil {
		t.Fatalf("failed to create outside file: %v", err)
	}

	// Create a symlink inside the workspace pointing outside.
	symlinkPath := filepath.Join(tmpDir, "link.txt")
	if err := os.Symlink(outsideFile, symlinkPath); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	err := ValidateWorkspacePath(tmpDir, symlinkPath)
	if err == nil {
		t.Fatal("expected error for symlink resolving outside workspace, got nil")
	}
	if !strings.Contains(err.Error(), "outside workspace") {
		t.Errorf("expected error to mention 'outside workspace', got: %v", err)
	}
}
