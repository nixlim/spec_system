package codedoc

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ---------------------------------------------------------------------------
// Secret pattern definitions
// ---------------------------------------------------------------------------

// SecretPattern defines a regex-based pattern for detecting secrets in
// documentation output, along with its category and redaction placeholder.
type SecretPattern struct {
	// Category is the pattern category used in the redaction placeholder
	// and sanitisation report (e.g., "api_key", "token", "connection_string").
	Category string
	// Regex is the compiled regular expression for detection.
	Regex *regexp.Regexp
	// CanRedact indicates whether the pattern can be safely redacted in-place.
	// When false, the sanitiser flags needs_redraft=true.
	CanRedact bool
}

// defaultPatterns returns the secret detection patterns specified in Section 3d.
func defaultPatterns() []SecretPattern {
	return []SecretPattern{
		// API keys: AKIA prefix (AWS)
		{Category: "api_key", Regex: regexp.MustCompile(`AKIA[0-9A-Za-z]{16,}`), CanRedact: true},
		// API keys: sk- prefix (OpenAI, Stripe, etc.)
		{Category: "api_key", Regex: regexp.MustCompile(`sk-[0-9A-Za-z]{20,}`), CanRedact: true},
		// Tokens: ghp_ prefix (GitHub personal access tokens)
		{Category: "token", Regex: regexp.MustCompile(`ghp_[0-9A-Za-z]{36,}`), CanRedact: true},
		// Tokens: glpat- prefix (GitLab personal access tokens)
		{Category: "token", Regex: regexp.MustCompile(`glpat-[0-9A-Za-z]{20,}`), CanRedact: true},
		// Tokens: JWT strings (eyJ prefix, 3 base64 segments separated by dots)
		{Category: "token", Regex: regexp.MustCompile(`eyJ[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`), CanRedact: true},
		// Connection strings with credentials
		{Category: "connection_string", Regex: regexp.MustCompile(`(?:postgres|mongodb|redis|mysql)://[^\s"'\x60]+:[^\s"'\x60]+@[^\s"'\x60]+`), CanRedact: true},
		// Passwords: key=value patterns
		{Category: "password", Regex: regexp.MustCompile(`(?i)(?:password|passwd|secret)\s*[=:]\s*["']?[^\s"',;]{8,}`), CanRedact: true},
		// Private keys: PEM headers
		{Category: "private_key", Regex: regexp.MustCompile(`-----BEGIN (?:RSA |EC |DSA |OPENSSH )?PRIVATE KEY-----`), CanRedact: true},
		// PII: email addresses
		{Category: "pii_email", Regex: regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`), CanRedact: true},
		// PII: phone numbers (international and US formats)
		{Category: "pii_phone", Regex: regexp.MustCompile(`(?:\+\d{1,3}[\s.-]?)?\(?\d{3}\)?[\s.-]?\d{3}[\s.-]?\d{4}`), CanRedact: true},
	}
}

// ---------------------------------------------------------------------------
// Sanitiser
// ---------------------------------------------------------------------------

// Sanitiser scans documentation files for secret patterns and redacts them.
type Sanitiser struct {
	patterns []SecretPattern
}

// NewSanitiser creates a Sanitiser with the default secret detection patterns
// from Section 3d of the codedoc workflow spec.
func NewSanitiser() *Sanitiser {
	return &Sanitiser{patterns: defaultPatterns()}
}

// ScanResult holds the outcome of scanning a single file.
type ScanResult struct {
	FilePath    string
	Entries     []SanitisationEntry
	NeedsRedraft bool
	NewContent  string
}

// ScanFile scans a single file for secret patterns and returns the scan result.
// The file content is read, each line is checked against all patterns, and
// detected secrets are redacted in-place with [REDACTED: category] placeholders.
func (s *Sanitiser) ScanFile(filePath string) (*ScanResult, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("reading file %s: %w", filePath, err)
	}

	return s.ScanContent(filePath, string(data)), nil
}

// ScanContent scans string content for secret patterns. This is the core
// detection and redaction logic, separated from I/O for testability.
func (s *Sanitiser) ScanContent(filePath, content string) *ScanResult {
	result := &ScanResult{
		FilePath: filePath,
	}

	scanner := bufio.NewScanner(strings.NewReader(content))
	var lines []string
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		modified := line

		for _, p := range s.patterns {
			matches := p.Regex.FindAllStringIndex(modified, -1)
			if len(matches) == 0 {
				continue
			}

			placeholder := fmt.Sprintf("[REDACTED: %s]", p.Category)

			if !p.CanRedact {
				// Record the finding but flag needs_redraft
				for range matches {
					result.Entries = append(result.Entries, SanitisationEntry{
						FilePath:    filePath,
						LineNumber:  lineNum,
						PatternType: p.Category,
						Redacted:    false,
						Confidence:  "high",
					})
				}
				result.NeedsRedraft = true
				continue
			}

			// Replace all occurrences on this line
			replaced := p.Regex.ReplaceAllString(modified, placeholder)
			if replaced != modified {
				matchCount := len(matches)
				for i := 0; i < matchCount; i++ {
					result.Entries = append(result.Entries, SanitisationEntry{
						FilePath:    filePath,
						LineNumber:  lineNum,
						PatternType: p.Category,
						Redacted:    true,
						Confidence:  "high",
					})
				}
				modified = replaced
			}
		}

		lines = append(lines, modified)
	}

	result.NewContent = strings.Join(lines, "\n")
	// Preserve trailing newline if original had one
	if len(content) > 0 && content[len(content)-1] == '\n' {
		result.NewContent += "\n"
	}

	return result
}

// ScanDirectory scans all files in a directory for secrets and produces a
// SanitisationReport. Files are redacted in-place when secrets are found.
func (s *Sanitiser) ScanDirectory(dirPath string) (*SanitisationReport, error) {
	report := &SanitisationReport{
		Safe: true,
	}

	err := filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		// Only scan text-based documentation files
		ext := strings.ToLower(filepath.Ext(path))
		if !isScannable(ext) {
			return nil
		}

		report.ScannedFiles++

		result, scanErr := s.ScanFile(path)
		if scanErr != nil {
			return fmt.Errorf("scanning %s: %w", path, scanErr)
		}

		if len(result.Entries) > 0 {
			report.Entries = append(report.Entries, result.Entries...)
			report.Safe = false

			// Count secrets and redactions
			for _, e := range result.Entries {
				report.SecretsFound++
				if e.Redacted {
					report.SecretsRedacted++
				}
			}

			if result.NeedsRedraft {
				report.NeedsRedraft = true
			}

			// Write redacted content back to file if any redactions were made
			hasRedactions := false
			for _, e := range result.Entries {
				if e.Redacted {
					hasRedactions = true
					break
				}
			}
			if hasRedactions {
				if writeErr := os.WriteFile(path, []byte(result.NewContent), info.Mode()); writeErr != nil {
					return fmt.Errorf("writing redacted file %s: %w", path, writeErr)
				}
			}
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return report, nil
}

// isScannable returns true for file extensions that should be scanned for secrets.
func isScannable(ext string) bool {
	switch ext {
	case ".md", ".json", ".mmd", ".mermaid", ".txt", ".yaml", ".yml":
		return true
	default:
		return false
	}
}
