package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func setupTestConfig(t *testing.T) UploadConfig {
	t.Helper()
	dir := t.TempDir()
	return UploadConfig{
		WorkspaceDir: dir,
		MaxFileSize:  1024 * 1024, // 1MB for tests
		MaxTotalSize: 5 * 1024 * 1024,
		MaxFileCount: 20,
	}
}

func createMultipartRequest(t *testing.T, filename string, content []byte, contentType string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	h := make(map[string][]string)
	h["Content-Disposition"] = []string{fmt.Sprintf(`form-data; name="file"; filename="%s"`, filename)}
	if contentType != "" {
		h["Content-Type"] = []string{contentType}
	} else {
		h["Content-Type"] = []string{"application/octet-stream"}
	}

	part, err := writer.CreatePart(h)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/upload", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

func TestUploadValidFile(t *testing.T) {
	config := setupTestConfig(t)
	handler := HandleUpload(config)

	content := []byte("# Hello World\nThis is a test document.")
	req := createMultipartRequest(t, "test.md", content, "text/markdown")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["filename"] != "test.md" {
		t.Errorf("expected filename 'test.md', got %q", resp["filename"])
	}

	// Verify file exists on disk
	destPath := filepath.Join(config.WorkspaceDir, "source-docs", "test.md")
	data, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("file not found on disk: %v", err)
	}
	if !bytes.Equal(data, content) {
		t.Errorf("file content mismatch")
	}
}

func TestUploadRejectDisallowedExtension(t *testing.T) {
	config := setupTestConfig(t)
	handler := HandleUpload(config)

	content := []byte("malicious content")
	req := createMultipartRequest(t, "malware.exe", content, "application/octet-stream")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	if !bytes.Contains([]byte(body), []byte("not allowed")) {
		t.Errorf("expected 'not allowed' in response, got %q", body)
	}
}

func TestUploadFileSizeLimit(t *testing.T) {
	config := setupTestConfig(t)
	config.MaxFileSize = 100 // 100 bytes
	handler := HandleUpload(config)

	content := make([]byte, 200) // exceeds 100 bytes
	for i := range content {
		content[i] = 'A'
	}

	req := createMultipartRequest(t, "big.txt", content, "text/plain")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected status 413, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUploadFilenameSanitization(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
		wantErr  bool
	}{
		{
			name:     "strips path separators",
			input:    "path/to/file.txt",
			expected: "file.txt",
		},
		{
			name:     "strips backslash separators",
			input:    "path\\to\\file.txt",
			expected: "file.txt",
		},
		{
			name:    "rejects null bytes",
			input:   "file\x00.txt",
			wantErr: true,
		},
		{
			name:    "rejects dot-dot sequences",
			input:   "../etc/passwd",
			wantErr: true,
		},
		{
			name:     "normalises to ASCII",
			input:    "héllo wörld.txt",
			expected: "hllowrld.txt",
		},
		{
			name:    "empty after sanitization",
			input:   "!!!",
			wantErr: true,
		},
		{
			name:     "preserves hyphens and dots",
			input:    "my-file.2024.txt",
			expected: "my-file.2024.txt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := SanitizeFilename(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got result %q", result)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestUploadPathTraversal(t *testing.T) {
	// Test that SanitizeFilename directly rejects ".." sequences
	_, err := SanitizeFilename("../../etc/passwd.txt")
	if err == nil {
		t.Error("expected SanitizeFilename to reject '..' sequence")
	}

	// Test that the handler rejects a filename with ".." when passed directly.
	// Note: Go's multipart library strips directory paths from filenames,
	// so we also verify ValidateUploadPath catches escapes independently.
	config := setupTestConfig(t)
	handler := HandleUpload(config)

	content := []byte("pwned")
	req := createMultipartRequest(t, "../../etc/passwd.txt", content, "text/plain")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Go's multipart strips the path to "passwd.txt", so upload succeeds
	// but the file is safely stored inside source-docs/
	if rec.Code == http.StatusCreated {
		// Verify the file landed inside the workspace, not in /etc/
		destPath := filepath.Join(config.WorkspaceDir, "source-docs", "passwd.txt")
		if _, err := os.Stat(destPath); err != nil {
			t.Fatalf("file should exist in source-docs: %v", err)
		}
		// Verify /etc/passwd was NOT written
		if _, err := os.Stat("/etc/passwd.txt"); err == nil {
			t.Fatal("path traversal succeeded — file written outside workspace")
		}
	}
	// If status is 400, that's also acceptable (SanitizeFilename caught it)
}

func TestUploadPathTraversalValidation(t *testing.T) {
	baseDir := t.TempDir()

	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{
			name:    "valid path within base",
			path:    filepath.Join(baseDir, "source-docs", "test.md"),
			wantErr: false,
		},
		{
			name:    "path escapes base",
			path:    filepath.Join(baseDir, "..", "etc", "passwd"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateUploadPath(baseDir, tt.path)
			if tt.wantErr && err == nil {
				t.Error("expected error for path traversal, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestUploadInvalidJSON(t *testing.T) {
	config := setupTestConfig(t)
	handler := HandleUpload(config)

	content := []byte(`{"broken: json no closing`)
	req := createMultipartRequest(t, "config.json", content, "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 for invalid JSON, got %d: %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	if !bytes.Contains([]byte(body), []byte("invalid JSON")) {
		t.Errorf("expected 'invalid JSON' in response, got %q", body)
	}
}

func TestUploadValidJSON(t *testing.T) {
	config := setupTestConfig(t)
	handler := HandleUpload(config)

	content := []byte(`{"key": "value", "count": 42}`)
	req := createMultipartRequest(t, "config.json", content, "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUploadInvalidYAML(t *testing.T) {
	config := setupTestConfig(t)
	handler := HandleUpload(config)

	content := []byte("key: value\n  bad_indent: broken\n\ttabs: mixed")
	req := createMultipartRequest(t, "config.yaml", content, "text/yaml")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 for invalid YAML, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUploadValidYAML(t *testing.T) {
	config := setupTestConfig(t)
	handler := HandleUpload(config)

	content := []byte("key: value\ncount: 42\nitems:\n  - one\n  - two\n")
	req := createMultipartRequest(t, "config.yaml", content, "text/yaml")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUploadFileCountLimit(t *testing.T) {
	config := setupTestConfig(t)
	config.MaxFileCount = 3
	handler := HandleUpload(config)

	// Upload 3 files (should succeed)
	for i := 0; i < 3; i++ {
		filename := fmt.Sprintf("file%d.txt", i)
		content := []byte(fmt.Sprintf("content %d", i))
		req := createMultipartRequest(t, filename, content, "text/plain")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("upload %d: expected 201, got %d: %s", i, rec.Code, rec.Body.String())
		}
	}

	// 4th file should fail
	content := []byte("one too many")
	req := createMultipartRequest(t, "extra.txt", content, "text/plain")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 for file count limit, got %d: %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	if !bytes.Contains([]byte(body), []byte("file count limit")) {
		t.Errorf("expected 'file count limit' in response, got %q", body)
	}
}

func TestUploadListUploads(t *testing.T) {
	config := setupTestConfig(t)
	uploadHandler := HandleUpload(config)
	listHandler := HandleListUploads(config)

	// Upload a couple of files
	files := []struct {
		name    string
		content []byte
	}{
		{"doc1.md", []byte("# Document 1")},
		{"doc2.txt", []byte("Plain text document")},
	}

	for _, f := range files {
		req := createMultipartRequest(t, f.name, f.content, "text/plain")
		rec := httptest.NewRecorder()
		uploadHandler.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("upload %s: expected 201, got %d: %s", f.name, rec.Code, rec.Body.String())
		}
	}

	// List uploads
	req := httptest.NewRequest(http.MethodGet, "/uploads", nil)
	rec := httptest.NewRecorder()
	listHandler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var listed []map[string]interface{}
	body, _ := io.ReadAll(rec.Body)
	if err := json.Unmarshal(body, &listed); err != nil {
		t.Fatalf("failed to decode list response: %v\nbody: %s", err, string(body))
	}

	if len(listed) != 2 {
		t.Fatalf("expected 2 files, got %d", len(listed))
	}

	// Check that expected fields exist
	for _, item := range listed {
		if _, ok := item["name"]; !ok {
			t.Error("missing 'name' field")
		}
		if _, ok := item["size"]; !ok {
			t.Error("missing 'size' field")
		}
		if _, ok := item["modified_at"]; !ok {
			t.Error("missing 'modified_at' field")
		}
	}
}

func TestUploadListEmpty(t *testing.T) {
	config := setupTestConfig(t)
	handler := HandleListUploads(config)

	req := httptest.NewRequest(http.MethodGet, "/uploads", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var listed []interface{}
	if err := json.NewDecoder(rec.Body).Decode(&listed); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if len(listed) != 0 {
		t.Errorf("expected empty list, got %d items", len(listed))
	}
}
