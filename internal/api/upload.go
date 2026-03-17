// Package api provides HTTP handlers for the adversarial spec system.
package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"gopkg.in/yaml.v3"
)

// UploadConfig holds configuration for file upload handling.
type UploadConfig struct {
	// WorkspaceDir is the base workspace directory under which uploads are stored.
	WorkspaceDir string
	// MaxFileSize is the maximum allowed size for a single uploaded file in bytes.
	// Defaults to 10MB if zero.
	MaxFileSize int64
	// MaxTotalSize is the maximum total upload size for a workflow in bytes.
	// Defaults to 50MB if zero.
	MaxTotalSize int64
	// MaxFileCount is the maximum number of files allowed in a workflow.
	// Defaults to 20 if zero.
	MaxFileCount int
}

const (
	defaultMaxFileSize  = 10 * 1024 * 1024 // 10MB
	defaultMaxTotalSize = 50 * 1024 * 1024 // 50MB
	defaultMaxFileCount = 20
)

func (c UploadConfig) maxFileSize() int64 {
	if c.MaxFileSize <= 0 {
		return defaultMaxFileSize
	}
	return c.MaxFileSize
}

func (c UploadConfig) maxTotalSize() int64 {
	if c.MaxTotalSize <= 0 {
		return defaultMaxTotalSize
	}
	return c.MaxTotalSize
}

func (c UploadConfig) maxFileCount() int {
	if c.MaxFileCount <= 0 {
		return defaultMaxFileCount
	}
	return c.MaxFileCount
}

func (c UploadConfig) sourceDocsDir() string {
	return filepath.Join(c.WorkspaceDir, "source-docs")
}

// AllowedExtensions is the set of file extensions permitted for upload.
var AllowedExtensions = map[string]bool{
	".md":   true,
	".txt":  true,
	".pdf":  true,
	".go":   true,
	".ts":   true,
	".py":   true,
	".js":   true,
	".yaml": true,
	".json": true,
}

// ContentTypeMap maps file extensions to acceptable content-type prefixes.
var ContentTypeMap = map[string]string{
	".md":   "text/",
	".txt":  "text/",
	".pdf":  "application/pdf",
	".go":   "text/",
	".ts":   "text/",
	".py":   "text/",
	".js":   "text/",
	".yaml": "text/",
	".json": "application/json",
}

// SanitizeFilename sanitizes a filename for safe storage on the filesystem.
// It strips path separators, rejects null bytes and ".." sequences, and normalises
// the result to ASCII alphanumeric characters, hyphens, and dots. Returns an error
// if the sanitized result is empty.
func SanitizeFilename(name string) (string, error) {
	// Reject null bytes
	if strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("filename contains null bytes")
	}

	// Reject ".." sequences
	if strings.Contains(name, "..") {
		return "", fmt.Errorf("filename contains '..' sequence")
	}

	// Normalise path separators to forward slash so filepath.Base works
	// correctly on all platforms (macOS treats backslash as literal).
	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base(name)
	// Belt-and-suspenders: strip any remaining separators
	name = strings.ReplaceAll(name, "/", "")
	name = strings.ReplaceAll(name, "\\", "")

	// Normalise to ASCII alphanumeric + hyphens + dots
	var b strings.Builder
	for _, r := range name {
		if unicode.IsLetter(r) && r <= 127 {
			b.WriteRune(r)
		} else if unicode.IsDigit(r) && r <= 127 {
			b.WriteRune(r)
		} else if r == '-' || r == '.' {
			b.WriteRune(r)
		}
		// Drop everything else
	}

	result := b.String()
	if result == "" || result == "." {
		return "", fmt.Errorf("filename is empty after sanitization")
	}

	return result, nil
}

// ValidateUploadPath checks that resolvedPath is safely contained within baseDir.
// It resolves symlinks and rejects any path that escapes the workspace.
func ValidateUploadPath(baseDir, resolvedPath string) error {
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return fmt.Errorf("cannot resolve base directory: %w", err)
	}

	absPath, err := filepath.Abs(resolvedPath)
	if err != nil {
		return fmt.Errorf("cannot resolve target path: %w", err)
	}

	// Attempt to resolve symlinks for the base directory (must exist)
	evalBase, err := filepath.EvalSymlinks(absBase)
	if err != nil {
		return fmt.Errorf("cannot evaluate symlinks for base: %w", err)
	}

	// For the target path, resolve as much as exists
	evalPath, err := evalSymlinksPartial(absPath)
	if err != nil {
		return fmt.Errorf("cannot evaluate symlinks for path: %w", err)
	}

	// Ensure the resolved path is within the base directory
	if !strings.HasPrefix(evalPath, evalBase+string(filepath.Separator)) && evalPath != evalBase {
		return fmt.Errorf("path escapes workspace: %s is not within %s", evalPath, evalBase)
	}

	return nil
}

// evalSymlinksPartial resolves symlinks for the portion of the path that exists,
// then appends any remaining non-existent components.
func evalSymlinksPartial(path string) (string, error) {
	// Try resolving the full path first
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}

	// Walk up until we find a path that exists
	dir := filepath.Dir(path)
	base := filepath.Base(path)

	if dir == path {
		// We've reached the root and it doesn't exist — unusual
		return path, nil
	}

	resolvedDir, err := evalSymlinksPartial(dir)
	if err != nil {
		return "", err
	}

	return filepath.Join(resolvedDir, base), nil
}

// HandleUpload returns an HTTP handler that accepts multipart file uploads.
// Files are stored under {WorkspaceDir}/source-docs/. The handler validates
// file extensions, content types, file sizes, and content integrity for JSON
// and YAML files. Returns 201 on success with a JSON response containing
// filename, size, and path.
func HandleUpload(config UploadConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Parse multipart form with a reasonable memory limit
		if err := r.ParseMultipartForm(config.maxFileSize()); err != nil {
			http.Error(w, "failed to parse multipart form", http.StatusBadRequest)
			return
		}

		file, header, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "missing file field", http.StatusBadRequest)
			return
		}
		defer file.Close()

		// Validate extension
		ext := strings.ToLower(filepath.Ext(header.Filename))
		if !AllowedExtensions[ext] {
			http.Error(w, fmt.Sprintf("file type %q not allowed", ext), http.StatusBadRequest)
			return
		}

		// Validate content-type
		contentType := header.Header.Get("Content-Type")
		expectedPrefix, ok := ContentTypeMap[ext]
		if ok && !strings.HasPrefix(contentType, expectedPrefix) {
			// Allow application/octet-stream as a fallback for text types
			if contentType != "application/octet-stream" {
				http.Error(w, fmt.Sprintf("content-type %q does not match expected %q for %s", contentType, expectedPrefix, ext), http.StatusBadRequest)
				return
			}
		}

		// Check individual file size
		if header.Size > config.maxFileSize() {
			http.Error(w, fmt.Sprintf("file size %d exceeds maximum %d", header.Size, config.maxFileSize()), http.StatusRequestEntityTooLarge)
			return
		}

		// Ensure source-docs directory exists
		docsDir := config.sourceDocsDir()
		if err := os.MkdirAll(docsDir, 0o755); err != nil {
			http.Error(w, "failed to create upload directory", http.StatusInternalServerError)
			return
		}

		// Check file count limit
		entries, err := os.ReadDir(docsDir)
		if err != nil {
			http.Error(w, "failed to read upload directory", http.StatusInternalServerError)
			return
		}
		if len(entries) >= config.maxFileCount() {
			http.Error(w, fmt.Sprintf("file count limit %d reached", config.maxFileCount()), http.StatusBadRequest)
			return
		}

		// Check total size limit
		var totalSize int64
		for _, entry := range entries {
			info, err := entry.Info()
			if err != nil {
				continue
			}
			totalSize += info.Size()
		}
		if totalSize+header.Size > config.maxTotalSize() {
			http.Error(w, fmt.Sprintf("total upload size would exceed limit of %d bytes", config.maxTotalSize()), http.StatusRequestEntityTooLarge)
			return
		}

		// Sanitize filename
		sanitized, err := SanitizeFilename(header.Filename)
		if err != nil {
			http.Error(w, fmt.Sprintf("invalid filename: %v", err), http.StatusBadRequest)
			return
		}

		// Read file content (bounded by MaxFileSize)
		content, err := io.ReadAll(io.LimitReader(file, config.maxFileSize()+1))
		if err != nil {
			http.Error(w, "failed to read file", http.StatusInternalServerError)
			return
		}
		if int64(len(content)) > config.maxFileSize() {
			http.Error(w, "file size exceeds maximum", http.StatusRequestEntityTooLarge)
			return
		}

		// Content validation for structured formats
		switch ext {
		case ".json":
			if !json.Valid(content) {
				http.Error(w, "invalid JSON content", http.StatusBadRequest)
				return
			}
		case ".yaml":
			var parsed interface{}
			if err := yaml.Unmarshal(content, &parsed); err != nil {
				http.Error(w, fmt.Sprintf("invalid YAML content: %v", err), http.StatusBadRequest)
				return
			}
		}

		// Build final path and validate it's within workspace
		destPath := filepath.Join(docsDir, sanitized)
		if err := ValidateUploadPath(config.WorkspaceDir, destPath); err != nil {
			http.Error(w, fmt.Sprintf("path validation failed: %v", err), http.StatusBadRequest)
			return
		}

		// Write file
		if err := os.WriteFile(destPath, content, 0o644); err != nil {
			http.Error(w, "failed to write file", http.StatusInternalServerError)
			return
		}

		// Return success response
		resp := map[string]interface{}{
			"filename": sanitized,
			"size":     len(content),
			"path":     filepath.Join("source-docs", sanitized),
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(resp)
	}
}

// HandleListUploads returns an HTTP handler that lists all uploaded files
// in the source-docs directory. Returns a JSON array of objects containing
// name, size, and modified_at fields.
func HandleListUploads(config UploadConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		docsDir := config.sourceDocsDir()

		entries, err := os.ReadDir(docsDir)
		if err != nil {
			if os.IsNotExist(err) {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode([]interface{}{})
				return
			}
			http.Error(w, "failed to read upload directory", http.StatusInternalServerError)
			return
		}

		type fileInfo struct {
			Name       string    `json:"name"`
			Size       int64     `json:"size"`
			ModifiedAt time.Time `json:"modified_at"`
		}

		files := make([]fileInfo, 0, len(entries))
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			files = append(files, fileInfo{
				Name:       entry.Name(),
				Size:       info.Size(),
				ModifiedAt: info.ModTime(),
			})
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(files)
	}
}
