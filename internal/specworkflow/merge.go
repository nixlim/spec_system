// Package specworkflow defines the core types for the adversarial spec review
// workflow. This file implements the deterministic issue deduplication and
// merge algorithm that combines findings from multiple reviewer agents into a
// single deduplicated ledger.
package specworkflow

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// severityPrefix maps each Severity to its global ID prefix.
var severityPrefix = map[Severity]string{
	SeverityCritical:    "CRIT",
	SeverityMajor:       "MAJ",
	SeverityMinor:       "MIN",
	SeverityObservation: "OBS",
}

// collapseWS matches one or more whitespace characters.
var collapseWS = regexp.MustCompile(`\s+`)

// normalizeSection trims leading/trailing whitespace, collapses all internal
// whitespace runs to a single space, and lowercases the result. This produces
// a canonical form for deduplication comparison.
func normalizeSection(s string) string {
	s = strings.TrimSpace(s)
	s = collapseWS.ReplaceAllString(s, " ")
	return strings.ToLower(s)
}

// principleIsEmpty returns true if the principle pointer is nil or points to
// a string that is empty or whitespace-only.
func principleIsEmpty(p *string) bool {
	return p == nil || strings.TrimSpace(*p) == ""
}

// principlesMatch returns true if two constitution_principle values are
// considered equivalent for deduplication. Two principles match if:
//   - either is null/empty (nil or whitespace-only), OR
//   - both are equal after trimming and lowercasing.
func principlesMatch(a, b *string) bool {
	if principleIsEmpty(a) || principleIsEmpty(b) {
		return true
	}
	return strings.ToLower(strings.TrimSpace(*a)) == strings.ToLower(strings.TrimSpace(*b))
}

// isDuplicate returns true if two findings are considered duplicates.
// Two findings are duplicates if ALL THREE match:
//
//	a. Same affected_section (case-insensitive, whitespace-normalised)
//	b. Same lens (case-insensitive, trimmed)
//	c. Same constitution_principle (or either is null/empty)
func isDuplicate(a, b *Finding) bool {
	if normalizeSection(a.AffectedSection) != normalizeSection(b.AffectedSection) {
		return false
	}
	if strings.ToLower(strings.TrimSpace(a.Lens)) != strings.ToLower(strings.TrimSpace(b.Lens)) {
		return false
	}
	return principlesMatch(a.ConstitutionPrinciple, b.ConstitutionPrinciple)
}

// candidateFinding is an intermediate structure used during the merge process,
// holding a finding with its source attribution before global IDs are assigned.
type candidateFinding struct {
	finding   Finding
	sourceIDs []string
	raisedBy  []string
}

// assignGlobalIDs assigns sequential global IDs to merged findings, grouped
// by severity. Within each severity group, findings are sorted alphabetically
// by their normalised affected_section. IDs follow the pattern PREFIX-NNN
// (e.g. CRIT-001, MAJ-002).
func assignGlobalIDs(findings []MergedFinding) {
	// Sort: primary by severity order, secondary by normalised affected_section.
	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Severity != findings[j].Severity {
			return findings[i].Severity < findings[j].Severity
		}
		return normalizeSection(findings[i].AffectedSection) < normalizeSection(findings[j].AffectedSection)
	})

	// Assign sequential IDs per severity prefix.
	counters := make(map[Severity]int)
	for i := range findings {
		sev := findings[i].Severity
		counters[sev]++
		prefix := severityPrefix[sev]
		findings[i].ID = fmt.Sprintf("%s-%03d", prefix, counters[sev])
	}
}

// findDuplicate searches existing candidates for a duplicate of f.
// Returns the index into candidates if found, or -1 if no duplicate exists.
func findDuplicate(candidates []candidateFinding, f *Finding) int {
	for i := range candidates {
		if isDuplicate(&candidates[i].finding, f) {
			return i
		}
	}
	return -1
}

// MergeReviewerOutputs takes the outputs from multiple reviewer agents and
// produces a single deduplicated MergedFindings ledger. The algorithm is
// entirely deterministic — no LLM calls are made.
//
// The merge proceeds in these steps:
//  1. Parse and validate all reviewer outputs, tracking rejected findings.
//  2. Collect all valid findings with source attribution.
//  3. Deduplicate by (affected_section, lens, constitution_principle).
//  4. Assign global IDs in severity order (CRIT, MAJ, MIN, OBS).
//  5. Sort alphabetically by affected_section within each severity group.
//  6. Record each merge in the dedup_log.
//  7. Build the MergedFindings struct with all metadata.
func MergeReviewerOutputs(outputs []*ReviewerOutput, round int) (*MergedFindings, error) {
	if len(outputs) == 0 {
		return nil, fmt.Errorf("MergeReviewerOutputs: at least one ReviewerOutput is required")
	}

	// Step 1 & 2: Validate and collect findings with source attribution.
	var totalRejected int
	var totalRaw int
	var candidates []candidateFinding
	var dedupLog []DedupEntry

	for _, output := range outputs {
		validFindings, rejected, _ := ValidateReviewerOutput(output)
		totalRejected += rejected
		totalRaw += len(output.Findings)

		reviewer := output.Agent

		for _, f := range validFindings {
			f := f // capture loop variable
			idx := findDuplicate(candidates, &f)

			if idx >= 0 {
				// Duplicate found — merge into existing candidate.
				existing := &candidates[idx]

				// Keep higher severity (lower numeric value = higher severity).
				if f.Severity < existing.finding.Severity {
					existing.finding.Severity = f.Severity
				}

				// Concatenate recommendations with attribution.
				existingRec := existing.finding.Recommendation
				newRec := fmt.Sprintf("From %s: %s", reviewer, f.Recommendation)
				// Re-attribute the existing recommendation if not already attributed.
				if !strings.HasPrefix(existingRec, "From ") {
					existingRec = fmt.Sprintf("From %s: %s", existing.raisedBy[0], existingRec)
				}
				existing.finding.Recommendation = existingRec + "\n" + newRec

				// Merge source_ids and raised_by.
				existing.sourceIDs = append(existing.sourceIDs, f.ID)
				existing.raisedBy = append(existing.raisedBy, reviewer)

				// Record in dedup log.
				dedupLog = append(dedupLog, DedupEntry{
					KeptID:   existing.sourceIDs[0],
					MergedID: f.ID,
					Reason: fmt.Sprintf("duplicate: same affected_section=%q, lens=%q, constitution_principle=%s",
						normalizeSection(f.AffectedSection),
						strings.ToLower(strings.TrimSpace(f.Lens)),
						formatPrinciple(f.ConstitutionPrinciple)),
				})
			} else {
				// New unique finding.
				candidates = append(candidates, candidateFinding{
					finding:   f,
					sourceIDs: []string{f.ID},
					raisedBy:  []string{reviewer},
				})
			}
		}
	}

	// Step 3-5: Build MergedFinding slice, assign global IDs.
	merged := make([]MergedFinding, len(candidates))
	for i, c := range candidates {
		merged[i] = MergedFinding{
			SourceIDs:             c.sourceIDs,
			RaisedBy:              c.raisedBy,
			Description:           c.finding.Description,
			Severity:              c.finding.Severity,
			Impact:                c.finding.Impact,
			Recommendation:        c.finding.Recommendation,
			Lens:                  c.finding.Lens,
			AffectedSection:       c.finding.AffectedSection,
			ConstitutionPrinciple: c.finding.ConstitutionPrinciple,
			Status:                "open",
			RoundRaised:           round,
		}
	}

	// Sort and assign IDs.
	assignGlobalIDs(merged)

	// Build the final struct.
	duplicatesMerged := totalRaw - totalRejected - len(candidates)
	if duplicatesMerged < 0 {
		duplicatesMerged = 0
	}

	result := &MergedFindings{
		SchemaVersion:    "1.0",
		Round:            round,
		Timestamp:        time.Now().UTC().Format(time.RFC3339),
		TotalFindings:    totalRaw,
		TotalAfterDedup:  len(merged),
		DuplicatesMerged: duplicatesMerged,
		FindingsRejected: totalRejected,
		Findings:         merged,
		DedupLog:         dedupLog,
	}

	return result, nil
}

// formatPrinciple returns a display string for a constitution principle pointer.
func formatPrinciple(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%q", *p)
}
