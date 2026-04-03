package codedoc

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// RevisionOutput is the structured output produced by the codedoc revision agent.
type RevisionOutput struct {
	SchemaVersion string           `json:"schema_version"`
	Agent         string           `json:"agent"`
	Round         int              `json:"round"`
	Addressed     []RevisedFinding `json:"addressed"`
	WontFix       []RevisedFinding `json:"wont_fix"`
	Remaining     []RevisedFinding `json:"remaining"`
	Summary       RevisionSummary  `json:"summary"`
}

// RevisedFinding records the disposition of a single review finding by the
// revision agent.
type RevisedFinding struct {
	FindingID   string `json:"finding_id"`
	Status      string `json:"status"` // "resolved", "wontfix", "open"
	Rationale   string `json:"rationale,omitempty"`
	FileChanged string `json:"file_changed,omitempty"`
}

// RevisionSummary provides aggregate counts for the revision output.
type RevisionSummary struct {
	AddressedCount int `json:"addressed_count"`
	WontFixCount   int `json:"wont_fix_count"`
	RemainingCount int `json:"remaining_count"`
}

// ValidateRevisionOutput checks a RevisionOutput for required fields and
// returns errors for any violations. Does not reject the output — callers
// decide severity.
func ValidateRevisionOutput(o *RevisionOutput) []error {
	var errs []error
	if o.SchemaVersion == "" {
		errs = append(errs, fmt.Errorf("schema_version must not be empty"))
	}
	if o.Agent == "" {
		errs = append(errs, fmt.Errorf("agent must not be empty"))
	}
	if o.Round <= 0 {
		errs = append(errs, fmt.Errorf("round must be > 0, got %d", o.Round))
	}
	for i, f := range o.Addressed {
		if f.FindingID == "" {
			errs = append(errs, fmt.Errorf("addressed[%d].finding_id must not be empty", i))
		}
		if f.Status != "resolved" {
			errs = append(errs, fmt.Errorf("addressed[%d].status must be 'resolved', got %q", i, f.Status))
		}
	}
	for i, f := range o.WontFix {
		if f.FindingID == "" {
			errs = append(errs, fmt.Errorf("wont_fix[%d].finding_id must not be empty", i))
		}
		if f.Status != "wontfix" {
			errs = append(errs, fmt.Errorf("wont_fix[%d].status must be 'wontfix', got %q", i, f.Status))
		}
		if strings.TrimSpace(f.Rationale) == "" {
			errs = append(errs, fmt.Errorf("wont_fix[%d].rationale must not be empty (finding %s)", i, f.FindingID))
		}
	}
	return errs
}

// PrioritizeFindings sorts review findings so that CRITICAL findings come
// first, then MAJOR, then MINOR, then OBSERVATION. Within each severity
// group, findings are sorted by ID for determinism.
func PrioritizeFindings(findings []ReviewFinding) []ReviewFinding {
	result := make([]ReviewFinding, len(findings))
	copy(result, findings)
	sort.SliceStable(result, func(i, j int) bool {
		si := severityOrder(result[i].Severity)
		sj := severityOrder(result[j].Severity)
		if si != sj {
			return si < sj
		}
		return result[i].ID < result[j].ID
	})
	return result
}

func severityOrder(s string) int {
	switch strings.ToLower(s) {
	case SeverityCritical:
		return 0
	case SeverityMajor:
		return 1
	case SeverityMinor:
		return 2
	case SeverityObservation:
		return 3
	default:
		return 4
	}
}

// ApplyRevisionToFindings takes the current findings and a revision output,
// and returns updated findings with statuses changed according to the revision.
func ApplyRevisionToFindings(findings []ReviewFinding, revision *RevisionOutput) []ReviewFinding {
	if revision == nil {
		return findings
	}

	// Build a map of finding ID -> new status.
	statusMap := make(map[string]string)
	for _, f := range revision.Addressed {
		statusMap[f.FindingID] = "resolved"
	}
	for _, f := range revision.WontFix {
		statusMap[f.FindingID] = "wontfix"
	}

	result := make([]ReviewFinding, len(findings))
	copy(result, findings)
	for i := range result {
		if newStatus, ok := statusMap[result[i].ID]; ok {
			result[i].Status = newStatus
		}
	}
	return result
}

// FilterOpenFindings returns only findings with status "open".
func FilterOpenFindings(findings []ReviewFinding) []ReviewFinding {
	var result []ReviewFinding
	for _, f := range findings {
		if f.Status == "open" {
			result = append(result, f)
		}
	}
	return result
}

// BuildRevisionSummary computes a RevisionSummary from a RevisionOutput.
func BuildRevisionSummary(revision *RevisionOutput) RevisionSummary {
	return RevisionSummary{
		AddressedCount: len(revision.Addressed),
		WontFixCount:   len(revision.WontFix),
		RemainingCount: len(revision.Remaining),
	}
}

// ReadMergedFindings reads and parses the merged findings file for a given round.
// The file contains a MergedCodedocFindings wrapper with a Findings array.
func ReadMergedFindings(codedocDir string, round int) ([]ReviewFinding, error) {
	path := filepath.Join(codedocDir, fmt.Sprintf("merged-findings-round-%d.json", round))
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read merged findings: %w", err)
	}
	var merged MergedCodedocFindings
	if err := json.Unmarshal(data, &merged); err != nil {
		// Fall back to raw array for backwards compatibility.
		var findings []ReviewFinding
		if err2 := json.Unmarshal(data, &findings); err2 != nil {
			return nil, fmt.Errorf("parse merged findings: %w", err)
		}
		return findings, nil
	}
	return merged.Findings, nil
}

// WriteRevisionOutput writes a RevisionOutput to the workspace as
// revision-round-{N}.json.
func WriteRevisionOutput(codedocDir string, round int, revision *RevisionOutput) error {
	path := filepath.Join(codedocDir, fmt.Sprintf("revision-round-%d.json", round))
	data, err := json.MarshalIndent(revision, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal revision output: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}
