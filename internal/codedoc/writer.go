package codedoc

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// ---------------------------------------------------------------------------
// WriteResult
// ---------------------------------------------------------------------------

// WriteResult holds the outcome of the writing phase.
type WriteResult struct {
	WrittenFiles []string     `json:"written_files"`
	Manifest     ManifestFile `json:"manifest"`
	DriftWarning string       `json:"drift_warning,omitempty"`
}

// ---------------------------------------------------------------------------
// Writer
// ---------------------------------------------------------------------------

// Writer implements the CD_WRITING phase: staging, backup, atomic move,
// lock management, drift detection, and manual marker preservation.
type Writer struct {
	Config          CodedocConfig
	CodePath        string
	Feature         string
	DiscoveryOutput *DiscoveryOutput
	Now             func() time.Time // for testability
}

// docsDir returns the absolute path to the docs output directory.
func (w *Writer) docsDir() string {
	return filepath.Join(w.CodePath, w.Config.DocsOutputDir)
}

// stagingDir returns the path to the staging directory.
func (w *Writer) stagingDir() string {
	return filepath.Join(w.docsDir(), ".codedoc-staging")
}

// lockPath returns the path to the write lock file.
func (w *Writer) lockPath() string {
	return filepath.Join(w.docsDir(), ".codedoc-write.lock")
}

func (w *Writer) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

// ---------------------------------------------------------------------------
// Write — main entry point
// ---------------------------------------------------------------------------

// Write executes the full writing phase for the given approved files.
// approvedFiles maps relative paths (within docs/) to their content.
func (w *Writer) Write(approvedFiles map[string][]byte) (result *WriteResult, err error) {
	docsDir := w.docsDir()
	if err := os.MkdirAll(docsDir, 0755); err != nil {
		return nil, fmt.Errorf("creating docs dir: %w", err)
	}

	// Acquire lock.
	if err := w.acquireLock(); err != nil {
		return nil, fmt.Errorf("acquiring write lock: %w", err)
	}
	defer func() {
		if unlockErr := w.releaseLock(); unlockErr != nil {
			log.Printf("WARNING: failed to release write lock: %v", unlockErr)
		}
	}()

	// Drift detection (also available via DetectDrift() for pre-gate checks).
	driftWarning := w.DetectDrift()

	// Stage files.
	stagingDir := w.stagingDir()
	_ = os.RemoveAll(stagingDir) // clean any previous staging
	if err := os.MkdirAll(stagingDir, 0755); err != nil {
		return nil, fmt.Errorf("creating staging dir: %w", err)
	}

	for relPath, content := range approvedFiles {
		// Preserve manual markers from existing files.
		finalPath := filepath.Join(docsDir, relPath)
		content = w.preserveManualMarkers(finalPath, content)

		stagePath := filepath.Join(stagingDir, relPath)
		if err := os.MkdirAll(filepath.Dir(stagePath), 0755); err != nil {
			return nil, fmt.Errorf("creating staging subdir for %s: %w", relPath, err)
		}
		if err := os.WriteFile(stagePath, content, 0644); err != nil {
			return nil, fmt.Errorf("writing staged file %s: %w", relPath, err)
		}
	}

	// Validate staging — all expected files present and non-empty.
	for relPath := range approvedFiles {
		stagePath := filepath.Join(stagingDir, relPath)
		info, err := os.Stat(stagePath)
		if err != nil {
			return nil, fmt.Errorf("staged file missing: %s: %w", relPath, err)
		}
		if info.Size() == 0 {
			return nil, fmt.Errorf("staged file is empty: %s", relPath)
		}
	}

	// Backup existing files that will be overwritten (when enabled).
	ts := w.now().Format("20060102-150405")
	if w.Config.BackupBeforeWrite {
		for relPath := range approvedFiles {
			finalPath := filepath.Join(docsDir, relPath)
			if _, err := os.Stat(finalPath); err == nil {
				bakPath := finalPath + ".bak." + ts
				data, err := os.ReadFile(finalPath)
				if err != nil {
					return nil, fmt.Errorf("reading existing file for backup %s: %w", relPath, err)
				}
				if err := os.WriteFile(bakPath, data, 0644); err != nil {
					return nil, fmt.Errorf("creating backup %s: %w", bakPath, err)
				}
			}
		}
	}

	// Move files from staging to final location.
	var writtenFiles []string
	for relPath := range approvedFiles {
		stagePath := filepath.Join(stagingDir, relPath)
		finalPath := filepath.Join(docsDir, relPath)
		if err := os.MkdirAll(filepath.Dir(finalPath), 0755); err != nil {
			return nil, fmt.Errorf("creating final subdir for %s: %w", relPath, err)
		}
		if err := os.Rename(stagePath, finalPath); err != nil {
			return nil, fmt.Errorf("moving staged file %s: %w", relPath, err)
		}
		writtenFiles = append(writtenFiles, relPath)
	}

	// Cleanup staging.
	_ = os.RemoveAll(stagingDir)

	// Write manifest atomically.
	manifest := w.buildManifest(writtenFiles)
	if err := w.writeManifestAtomic(manifest); err != nil {
		return nil, fmt.Errorf("writing manifest: %w", err)
	}

	return &WriteResult{
		WrittenFiles: writtenFiles,
		Manifest:     manifest,
		DriftWarning: driftWarning,
	}, nil
}

// ---------------------------------------------------------------------------
// Lock management
// ---------------------------------------------------------------------------

type lockInfo struct {
	Feature string `json:"feature"`
	PID     int    `json:"pid"`
}

func (w *Writer) acquireLock() error {
	// Use real wall-clock for timeout (not the testable w.now() which is for timestamps).
	deadline := time.Now().Add(time.Duration(w.Config.WriteLockTimeoutSeconds) * time.Second)
	backoff := 500 * time.Millisecond

	for {
		err := w.tryAcquireLock()
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("write lock timeout after %ds: %w", w.Config.WriteLockTimeoutSeconds, err)
		}
		time.Sleep(backoff)
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (w *Writer) tryAcquireLock() error {
	lockPath := w.lockPath()

	// Try atomic create first (O_CREATE|O_EXCL prevents TOCTOU race).
	info := lockInfo{
		Feature: w.Feature,
		PID:     os.Getpid(),
	}
	lockData, _ := json.Marshal(info)

	f, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err == nil {
		// Successfully created — we hold the lock.
		_, writeErr := f.Write(lockData)
		f.Close()
		return writeErr
	}

	// Lock file exists — check if PID is alive.
	data, readErr := os.ReadFile(lockPath)
	if readErr != nil {
		return fmt.Errorf("lock file unreadable: %w", readErr)
	}
	var existing lockInfo
	if jsonErr := json.Unmarshal(data, &existing); jsonErr == nil {
		if isProcessAlive(existing.PID) {
			return fmt.Errorf("lock held by feature %q (PID %d)", existing.Feature, existing.PID)
		}
		// Stale lock — PID is dead, break it.
		log.Printf("breaking stale write lock (feature=%s, pid=%d)", existing.Feature, existing.PID)
	}
	// Remove stale/corrupt lock and retry atomic create.
	_ = os.Remove(lockPath)

	f, err = os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return fmt.Errorf("failed to acquire lock after breaking stale: %w", err)
	}
	_, writeErr := f.Write(lockData)
	f.Close()
	return writeErr
}

func (w *Writer) releaseLock() error {
	return os.Remove(w.lockPath())
}

// isProcessAlive checks if a process with the given PID exists.
func isProcessAlive(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, FindProcess always succeeds. Send signal 0 to check.
	err = process.Signal(syscall.Signal(0))
	return err == nil
}

