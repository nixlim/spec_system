package codereview

import (
	"fmt"
	"strings"

	"github.com/foundry-zero/adversarial-spec-system/internal/specworkflow"
)

// CodeReviewDedupKey produces a dedup key for code review findings using the
// tuple (file_path, line_range, lens, severity). This differs from the spec
// workflow key because severity is part of the key — two findings at the same
// location with different severities are considered distinct.
//
// The affected_section field for code review findings contains file paths with
// optional line ranges (e.g., "internal/api/handler.go:45-62"). The file_path
// is extracted by splitting on ":" and the line_range is everything after it.
func CodeReviewDedupKey(f *specworkflow.Finding) string {
	filePath, lineRange := splitAffectedSection(f.AffectedSection)
	lens := strings.ToLower(strings.TrimSpace(f.Lens))
	severity := strings.ToLower(f.Severity.String())
	return filePath + "|" + lineRange + "|" + lens + "|" + severity
}

// splitAffectedSection extracts the file path and optional line range from
// an affected_section string. The format is "path/to/file.go:45-62" or
// just "path/to/file.go" if no line range is present.
func splitAffectedSection(s string) (filePath, lineRange string) {
	s = strings.TrimSpace(s)
	s = strings.ToLower(s)

	idx := strings.LastIndex(s, ":")
	if idx < 0 {
		return s, ""
	}

	// Check if the part after ":" looks like a line range (digits, dashes).
	after := s[idx+1:]
	if isLineRange(after) {
		return s[:idx], after
	}

	// If it doesn't look like a line range (e.g., Windows drive letter),
	// treat the whole string as the file path.
	return s, ""
}

// isLineRange returns true if s looks like a line range (e.g., "45", "45-62").
func isLineRange(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c != '-' && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// CodeReviewDedupKeyFunc returns a DedupKeyFunc suitable for use with
// MergeReviewerOutputs in code review workflows.
func CodeReviewDedupKeyFunc() specworkflow.DedupKeyFunc {
	return func(f *specworkflow.Finding) string {
		return CodeReviewDedupKey(f)
	}
}

// FormatCodeReviewDedupReason produces a human-readable reason string for
// the dedup log when two code review findings are considered duplicates.
func FormatCodeReviewDedupReason(f *specworkflow.Finding) string {
	filePath, lineRange := splitAffectedSection(f.AffectedSection)
	return fmt.Sprintf("duplicate: same file_path=%q, line_range=%q, lens=%q, severity=%q",
		filePath, lineRange,
		strings.ToLower(strings.TrimSpace(f.Lens)),
		strings.ToLower(f.Severity.String()))
}
