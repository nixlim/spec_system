package codedoc

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Drift detection
// ---------------------------------------------------------------------------

// DetectDrift checks if the codebase has changed significantly since discovery
// and returns a warning string if drift exceeds the threshold. This should be
// called at HUMAN_GATE_FINAL so the user can abort before files are written.
func (w *Writer) DetectDrift() string {
	if w.DiscoveryOutput == nil || len(w.DiscoveryOutput.Modules) == 0 {
		return ""
	}

	total := 0
	changed := 0
	for _, mod := range w.DiscoveryOutput.Modules {
		if mod.ContentHash == "" {
			continue
		}
		total++
		// Compute current hash of the module directory.
		currentHash := computeDirHash(filepath.Join(w.CodePath, mod.Path))
		if currentHash != mod.ContentHash {
			changed++
		}
	}

	if total == 0 {
		return ""
	}

	fraction := float64(changed) / float64(total)
	if fraction > w.Config.DriftWarningThreshold {
		return fmt.Sprintf("Codebase has changed significantly since discovery. %d of %d modules modified (%.0f%%). Consider re-running discovery.", changed, total, fraction*100)
	}
	return ""
}

// computeDirHash computes a SHA-256 hash of all regular files in a directory tree.
func computeDirHash(dir string) string {
	h := sha256.New()
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		h.Write([]byte(rel))
		h.Write(data)
		return nil
	})
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// ---------------------------------------------------------------------------
// Manual marker preservation
// ---------------------------------------------------------------------------

const (
	manualOpen  = "<!-- manual -->"
	manualClose = "<!-- /manual -->"
)

// preserveManualMarkers extracts manual blocks from an existing file and
// re-inserts them into the new content at matching structural positions.
func (w *Writer) preserveManualMarkers(existingPath string, newContent []byte) []byte {
	existingData, err := os.ReadFile(existingPath)
	if err != nil {
		return newContent
	}

	blocks := extractManualBlocks(string(existingData))
	if len(blocks) == 0 {
		return newContent
	}

	return insertManualBlocks(string(newContent), blocks)
}

// manualBlock represents an extracted manual block with its context.
type manualBlock struct {
	Content       string // full content including markers
	ParentHeading string // nearest heading above the block
}

// extractManualBlocks finds all <!-- manual --> / <!-- /manual --> blocks.
func extractManualBlocks(content string) []manualBlock {
	var blocks []manualBlock
	lines := strings.Split(content, "\n")

	currentHeading := ""
	inBlock := false
	var blockLines []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Track headings.
		if strings.HasPrefix(trimmed, "#") && !inBlock {
			currentHeading = trimmed
		}

		if trimmed == manualOpen {
			inBlock = true
			blockLines = []string{line}
			continue
		}

		if inBlock {
			blockLines = append(blockLines, line)
			if trimmed == manualClose {
				blocks = append(blocks, manualBlock{
					Content:       strings.Join(blockLines, "\n"),
					ParentHeading: currentHeading,
				})
				inBlock = false
				blockLines = nil
			}
		}
	}

	return blocks
}

// insertManualBlocks places manual blocks back into new content at their
// structural positions, or appends with a warning if the heading is gone.
func insertManualBlocks(newContent string, blocks []manualBlock) []byte {
	result := newContent

	for _, block := range blocks {
		if block.ParentHeading != "" && strings.Contains(result, block.ParentHeading) {
			// Find the heading and insert the block after the heading's section content.
			idx := strings.Index(result, block.ParentHeading)
			insertPoint := idx + len(block.ParentHeading)

			// Find the end of the current line.
			nlIdx := strings.Index(result[insertPoint:], "\n")
			if nlIdx >= 0 {
				insertPoint += nlIdx + 1
			}

			result = result[:insertPoint] + "\n" + block.Content + "\n" + result[insertPoint:]
		} else {
			// Section removed — append with warning.
			result += "\n\n<!-- WARNING: original section removed, manual content preserved -->\n" + block.Content + "\n"
		}
	}

	return []byte(result)
}

// ---------------------------------------------------------------------------
// Manifest
// ---------------------------------------------------------------------------

func (w *Writer) buildManifest(writtenFiles []string) ManifestFile {
	docsDir := w.docsDir()

	var modules []ManifestModule
	if w.DiscoveryOutput != nil {
		for _, mod := range w.DiscoveryOutput.Modules {
			modules = append(modules, ManifestModule{
				Path:        mod.Path,
				ContentHash: mod.ContentHash,
			})
		}
	}

	var filesDoc []ManifestDoc
	for _, relPath := range writtenFiles {
		absPath := filepath.Join(docsDir, relPath)
		data, err := os.ReadFile(absPath)
		hash := ""
		if err == nil {
			h := sha256.Sum256(data)
			hash = "sha256:" + hex.EncodeToString(h[:])
		}
		filesDoc = append(filesDoc, ManifestDoc{
			Path:        filepath.Join(w.Config.DocsOutputDir, relPath),
			ContentHash: hash,
		})
	}

	return ManifestFile{
		SchemaVersion:   "1.0",
		WorkflowFeature: w.Feature,
		GeneratedAt:     w.now().UTC().Format(time.RFC3339),
		Modules:         modules,
		FilesDocumented: filesDoc,
	}
}

func (w *Writer) writeManifestAtomic(manifest ManifestFile) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling manifest: %w", err)
	}

	manifestPath := filepath.Join(w.docsDir(), ".codedoc-manifest.json")
	tmpPath := manifestPath + ".tmp"

	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("writing temp manifest: %w", err)
	}

	if err := os.Rename(tmpPath, manifestPath); err != nil {
		return fmt.Errorf("renaming temp manifest: %w", err)
	}

	return nil
}

// computeFileHash returns the sha256 hash of a file in the format "sha256:hex".
func computeFileHash(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(h[:])
}

// ---------------------------------------------------------------------------
// Lock file helpers (exported for testing)
// ---------------------------------------------------------------------------

// ParseLockFile reads the lock file at the given path and returns the lock info.
func ParseLockFile(path string) (*lockInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var info lockInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// FormatLockPID returns the PID from a lock file for testing.
func FormatLockPID(path string) (int, error) {
	info, err := ParseLockFile(path)
	if err != nil {
		return 0, err
	}
	return info.PID, nil
}

// IsProcessAlive is exported for testing.
func IsProcessAlive(pid int) bool {
	return isProcessAlive(pid)
}

// WriteStaleLock writes a lock file with a dead PID for testing.
func WriteStaleLock(path, feature string) error {
	// Use PID 99999999 which is almost certainly not running.
	info := lockInfo{Feature: feature, PID: 99999999}
	data, _ := json.Marshal(info)
	return os.WriteFile(path, data, 0644)
}

// ReadLockFeature reads the feature name from a lock file.
func ReadLockFeature(path string) (string, error) {
	info, err := ParseLockFile(path)
	if err != nil {
		return "", err
	}
	return info.Feature, nil
}

// pidFromString converts a string PID to int. Exported for testing.
func pidFromString(s string) int {
	pid, _ := strconv.Atoi(s)
	return pid
}
