package codedoc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Pattern detection tests
// ---------------------------------------------------------------------------

func TestSanitisationDetectsAKIAKeys(t *testing.T) {
	s := NewSanitiser()
	content := "Here is a key: AKIA1234567890ABCDEF and some text"
	result := s.ScanContent("test.md", content)

	if len(result.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(result.Entries))
	}
	if result.Entries[0].PatternType != "api_key" {
		t.Errorf("expected pattern_type api_key, got %s", result.Entries[0].PatternType)
	}
	if !result.Entries[0].Redacted {
		t.Error("expected entry to be redacted")
	}
	if !strings.Contains(result.NewContent, "[REDACTED: api_key]") {
		t.Errorf("expected redaction placeholder in content, got: %s", result.NewContent)
	}
	if strings.Contains(result.NewContent, "AKIA1234567890ABCDEF") {
		t.Error("original key should not appear in redacted content")
	}
}

func TestSanitisationDetectsSKKeys(t *testing.T) {
	s := NewSanitiser()
	content := "API key: sk-1234567890abcdefABCDEF"
	result := s.ScanContent("test.md", content)

	if len(result.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(result.Entries))
	}
	if result.Entries[0].PatternType != "api_key" {
		t.Errorf("expected pattern_type api_key, got %s", result.Entries[0].PatternType)
	}
	if !strings.Contains(result.NewContent, "[REDACTED: api_key]") {
		t.Errorf("expected redaction placeholder, got: %s", result.NewContent)
	}
}

func TestSanitisationDetectsGitHubTokens(t *testing.T) {
	s := NewSanitiser()
	content := "Token: ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghij"
	result := s.ScanContent("test.md", content)

	if len(result.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(result.Entries))
	}
	if result.Entries[0].PatternType != "token" {
		t.Errorf("expected pattern_type token, got %s", result.Entries[0].PatternType)
	}
	if !strings.Contains(result.NewContent, "[REDACTED: token]") {
		t.Errorf("expected redaction placeholder, got: %s", result.NewContent)
	}
}

func TestSanitisationDetectsGitLabTokens(t *testing.T) {
	s := NewSanitiser()
	content := "Token: glpat-ABCDEFGHIJKLMNOPQRSTu"
	result := s.ScanContent("test.md", content)

	if len(result.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(result.Entries))
	}
	if result.Entries[0].PatternType != "token" {
		t.Errorf("expected pattern_type token, got %s", result.Entries[0].PatternType)
	}
	if !strings.Contains(result.NewContent, "[REDACTED: token]") {
		t.Errorf("expected redaction placeholder, got: %s", result.NewContent)
	}
}

func TestSanitisationDetectsConnectionStrings(t *testing.T) {
	s := NewSanitiser()
	cases := []struct {
		name    string
		content string
	}{
		{"postgres", "DSN: postgres://admin:s3cret@db.example.com:5432/mydb"},
		{"mongodb", "URI: mongodb://root:pass123@mongo.local:27017/app"},
		{"redis", "URL: redis://default:hunter2@redis.local:6379"},
		{"mysql", "Conn: mysql://dbuser:pw@localhost:3306/prod"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := s.ScanContent("test.md", tc.content)
			if len(result.Entries) == 0 {
				t.Fatalf("expected at least 1 entry for %s", tc.name)
			}
			if result.Entries[0].PatternType != "connection_string" {
				t.Errorf("expected pattern_type connection_string, got %s", result.Entries[0].PatternType)
			}
			if !strings.Contains(result.NewContent, "[REDACTED: connection_string]") {
				t.Errorf("expected redaction placeholder, got: %s", result.NewContent)
			}
		})
	}
}

func TestSanitisationDetectsPasswordPatterns(t *testing.T) {
	s := NewSanitiser()
	cases := []struct {
		name    string
		content string
	}{
		{"password=", "config: password=mySecureP@ss123"},
		{"PASSWORD=", "env: PASSWORD=supersecret1"},
		{"secret:", "secret: very-secret-value-here"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := s.ScanContent("test.md", tc.content)
			if len(result.Entries) == 0 {
				t.Fatalf("expected at least 1 entry for %s", tc.name)
			}
			if result.Entries[0].PatternType != "password" {
				t.Errorf("expected pattern_type password, got %s", result.Entries[0].PatternType)
			}
			if !strings.Contains(result.NewContent, "[REDACTED: password]") {
				t.Errorf("expected redaction placeholder, got: %s", result.NewContent)
			}
		})
	}
}

func TestSanitisationDetectsPEMHeaders(t *testing.T) {
	s := NewSanitiser()
	content := "Key:\n-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQ...\n-----END RSA PRIVATE KEY-----"
	result := s.ScanContent("test.md", content)

	if len(result.Entries) == 0 {
		t.Fatal("expected at least 1 entry for PEM header")
	}
	if result.Entries[0].PatternType != "private_key" {
		t.Errorf("expected pattern_type private_key, got %s", result.Entries[0].PatternType)
	}
	if !strings.Contains(result.NewContent, "[REDACTED: private_key]") {
		t.Errorf("expected redaction placeholder, got: %s", result.NewContent)
	}
}

func TestSanitisationDetectsJWTTokens(t *testing.T) {
	s := NewSanitiser()
	// A realistic-looking JWT (header.payload.signature, each base64url)
	jwt := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
	content := "Bearer " + jwt
	result := s.ScanContent("test.md", content)

	if len(result.Entries) == 0 {
		t.Fatal("expected at least 1 entry for JWT")
	}
	if result.Entries[0].PatternType != "token" {
		t.Errorf("expected pattern_type token, got %s", result.Entries[0].PatternType)
	}
	if !strings.Contains(result.NewContent, "[REDACTED: token]") {
		t.Errorf("expected redaction placeholder, got: %s", result.NewContent)
	}
}

// ---------------------------------------------------------------------------
// Clean content test
// ---------------------------------------------------------------------------

func TestSanitisationNoSecretsProducesCleanReport(t *testing.T) {
	s := NewSanitiser()
	content := "# Module Overview\n\nThis module handles HTTP requests.\nIt uses net/http and returns JSON responses.\n"
	result := s.ScanContent("clean.md", content)

	if len(result.Entries) != 0 {
		t.Errorf("expected 0 entries for clean content, got %d", len(result.Entries))
	}
	if result.NeedsRedraft {
		t.Error("expected needs_redraft=false for clean content")
	}
}

// ---------------------------------------------------------------------------
// Report accuracy tests
// ---------------------------------------------------------------------------

func TestSanitisationReportLineNumbers(t *testing.T) {
	s := NewSanitiser()
	content := "line 1 is clean\nline 2 has AKIA1234567890ABCDEF\nline 3 is clean\nline 4 has sk-abcdefghijklmnopqrstuvwx"
	result := s.ScanContent("test.md", content)

	if len(result.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(result.Entries))
	}

	if result.Entries[0].LineNumber != 2 {
		t.Errorf("first entry: expected line 2, got %d", result.Entries[0].LineNumber)
	}
	if result.Entries[0].FilePath != "test.md" {
		t.Errorf("first entry: expected file_path test.md, got %s", result.Entries[0].FilePath)
	}

	if result.Entries[1].LineNumber != 4 {
		t.Errorf("second entry: expected line 4, got %d", result.Entries[1].LineNumber)
	}
}

func TestSanitisationMultipleSecretsOnSameLine(t *testing.T) {
	s := NewSanitiser()
	content := "keys: AKIA1234567890ABCDEF and AKIA9876543210ZYXWVU"
	result := s.ScanContent("test.md", content)

	if len(result.Entries) != 2 {
		t.Fatalf("expected 2 entries for two keys on same line, got %d", len(result.Entries))
	}
	// Both should be on line 1
	for i, e := range result.Entries {
		if e.LineNumber != 1 {
			t.Errorf("entry %d: expected line 1, got %d", i, e.LineNumber)
		}
	}
}

// ---------------------------------------------------------------------------
// Directory scanning tests
// ---------------------------------------------------------------------------

func TestSanitisationScanDirectory(t *testing.T) {
	dir := t.TempDir()

	// Write a clean file
	cleanPath := filepath.Join(dir, "clean.md")
	if err := os.WriteFile(cleanPath, []byte("# Clean\nNo secrets here.\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Write a file with a secret
	secretPath := filepath.Join(dir, "secret.md")
	secretContent := "# Config\nDB: postgres://admin:hunter2@db.example.com:5432/app\n"
	if err := os.WriteFile(secretPath, []byte(secretContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Write a non-scannable file (should be skipped)
	binPath := filepath.Join(dir, "image.png")
	if err := os.WriteFile(binPath, []byte{0x89, 0x50, 0x4e, 0x47}, 0644); err != nil {
		t.Fatal(err)
	}

	s := NewSanitiser()
	report, err := s.ScanDirectory(dir)
	if err != nil {
		t.Fatalf("ScanDirectory failed: %v", err)
	}

	if report.ScannedFiles != 2 {
		t.Errorf("expected 2 scanned files, got %d", report.ScannedFiles)
	}
	if report.SecretsFound != 1 {
		t.Errorf("expected 1 secret found, got %d", report.SecretsFound)
	}
	if report.SecretsRedacted != 1 {
		t.Errorf("expected 1 secret redacted, got %d", report.SecretsRedacted)
	}
	if report.Safe {
		t.Error("expected report.Safe=false when secrets found")
	}

	// Verify the file was redacted in place
	redacted, err := os.ReadFile(secretPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(redacted), "[REDACTED: connection_string]") {
		t.Errorf("expected redacted file to contain placeholder, got: %s", string(redacted))
	}
	if strings.Contains(string(redacted), "hunter2") {
		t.Error("original password should not appear in redacted file")
	}
}

func TestSanitisationScanDirectoryAllClean(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "readme.md"), []byte("# Hello\nNo secrets.\n"), 0644); err != nil {
		t.Fatal(err)
	}

	s := NewSanitiser()
	report, err := s.ScanDirectory(dir)
	if err != nil {
		t.Fatalf("ScanDirectory failed: %v", err)
	}

	if !report.Safe {
		t.Error("expected report.Safe=true for clean directory")
	}
	if report.NeedsRedraft {
		t.Error("expected needs_redraft=false for clean directory")
	}
	if report.SecretsFound != 0 {
		t.Errorf("expected 0 secrets, got %d", report.SecretsFound)
	}
	if len(report.Entries) != 0 {
		t.Errorf("expected empty entries, got %d", len(report.Entries))
	}
}

// ---------------------------------------------------------------------------
// Report JSON serialisation test
// ---------------------------------------------------------------------------

func TestSanitisationReportJSON(t *testing.T) {
	report := &SanitisationReport{
		ScannedFiles:    3,
		SecretsFound:    1,
		SecretsRedacted: 1,
		Entries: []SanitisationEntry{
			{
				FilePath:    "docs/config.md",
				LineNumber:  15,
				PatternType: "connection_string",
				Redacted:    true,
				Confidence:  "high",
			},
		},
		Safe:         false,
		NeedsRedraft: false,
	}

	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("failed to marshal report: %v", err)
	}

	var decoded SanitisationReport
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal report: %v", err)
	}

	if decoded.ScannedFiles != 3 {
		t.Errorf("expected scanned_files=3, got %d", decoded.ScannedFiles)
	}
	if decoded.NeedsRedraft {
		t.Error("expected needs_redraft=false after round-trip")
	}
	if len(decoded.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(decoded.Entries))
	}
	if decoded.Entries[0].PatternType != "connection_string" {
		t.Errorf("expected pattern_type connection_string, got %s", decoded.Entries[0].PatternType)
	}
}

// ---------------------------------------------------------------------------
// Nested directory test
// ---------------------------------------------------------------------------

func TestSanitisationScanNestedDirectory(t *testing.T) {
	dir := t.TempDir()
	subDir := filepath.Join(dir, "architecture")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}

	content := "# Architecture\nAPI key for diagram service: sk-abcdefghijklmnopqrstuvwx\n"
	if err := os.WriteFile(filepath.Join(subDir, "diagrams.md"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	s := NewSanitiser()
	report, err := s.ScanDirectory(dir)
	if err != nil {
		t.Fatalf("ScanDirectory failed: %v", err)
	}

	if report.SecretsFound != 1 {
		t.Errorf("expected 1 secret in nested dir, got %d", report.SecretsFound)
	}
}

// ---------------------------------------------------------------------------
// Trailing newline preservation test
// ---------------------------------------------------------------------------

func TestSanitisationPreservesTrailingNewline(t *testing.T) {
	s := NewSanitiser()

	withNewline := "line with AKIA1234567890ABCDEF\n"
	result := s.ScanContent("test.md", withNewline)
	if !strings.HasSuffix(result.NewContent, "\n") {
		t.Error("expected trailing newline to be preserved")
	}

	withoutNewline := "line with AKIA1234567890ABCDEF"
	result2 := s.ScanContent("test.md", withoutNewline)
	if strings.HasSuffix(result2.NewContent, "\n") {
		t.Error("expected no trailing newline when original had none")
	}
}
