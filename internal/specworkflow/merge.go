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

// DedupKeyFunc produces a string key for a finding used to determine
// duplicates. Two findings with the same key are considered duplicates.
type DedupKeyFunc func(f *Finding) string

// SpecDedupKey is the default dedup key function for spec workflows.
// It uses the tuple (affected_section, lens, constitution_principle).
func SpecDedupKey(f *Finding) string {
	section := normalizeSection(f.AffectedSection)
	lens := strings.ToLower(strings.TrimSpace(f.Lens))
	target := normalizeFindingTarget(f.Target)
	principle := ""
	if f.ConstitutionPrinciple != nil {
		principle = strings.ToLower(strings.TrimSpace(*f.ConstitutionPrinciple))
	}
	return section + "|" + lens + "|" + target + "|" + principle
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
	if normalizeFindingTarget(a.Target) != normalizeFindingTarget(b.Target) {
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

// findDuplicate searches existing candidates for a duplicate of f using
// the provided key function. When keyFn is nil, falls back to isDuplicate.
// Returns the index into candidates if found, or -1 if no duplicate exists.
func findDuplicate(candidates []candidateFinding, f *Finding, keyFn DedupKeyFunc) int {
	if keyFn != nil {
		key := keyFn(f)
		for i := range candidates {
			if keyFn(&candidates[i].finding) == key {
				return i
			}
		}
		return -1
	}
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
// dedupKeyFn controls how duplicates are detected. When nil, the default
// spec workflow dedup (affected_section, lens, constitution_principle) is used.
//
// promoteSeverity controls whether the higher severity is kept when merging
// duplicates. Spec workflows set this to true; code review workflows set it
// to false because severity is part of the dedup key.
//
// The merge proceeds in these steps:
//  1. Parse and validate all reviewer outputs, tracking rejected findings.
//  2. Collect all valid findings with source attribution.
//  3. Deduplicate using the provided key function.
//  4. Assign global IDs in severity order (CRIT, MAJ, MIN, OBS).
//  5. Sort alphabetically by affected_section within each severity group.
//  6. Record each merge in the dedup_log.
//  7. Build the MergedFindings struct with all metadata.
func MergeReviewerOutputs(outputs []*ReviewerOutput, round int, dedupKeyFn DedupKeyFunc, promoteSeverity bool) (*MergedFindings, error) {
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
			idx := findDuplicate(candidates, &f, dedupKeyFn)

			if idx >= 0 {
				// Duplicate found — merge into existing candidate.
				existing := &candidates[idx]

				// Keep higher severity (lower numeric value = higher severity)
				// only when promoteSeverity is enabled.
				if promoteSeverity && f.Severity < existing.finding.Severity {
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
			Target:                normalizeFindingTarget(c.finding.Target),
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

// ---------------------------------------------------------------------------
// Discovery output merge
// ---------------------------------------------------------------------------

// MergeDiscoveryOutputs combines two DiscoveryOutput structs using union-unique
// logic for all collection fields. Deduplication is case-insensitive.
// Conflict resolution: prefer Claude's version; if Claude's field is empty,
// use Codex's. When problem_statements differ, both are included with provider
// attribution. When either input is nil, the other is returned as-is.
func MergeDiscoveryOutputs(claude, codex *DiscoveryOutput) *DiscoveryOutput {
	if claude == nil && codex == nil {
		return &DiscoveryOutput{}
	}
	if claude == nil {
		return codex
	}
	if codex == nil {
		return claude
	}

	merged := &DiscoveryOutput{
		SchemaVersion: claude.SchemaVersion,
		Agent:         "merged",
	}

	// Merge actors by name (case-insensitive).
	merged.Actors = unionActorsByName(claude.Actors, codex.Actors)

	// Merge problem statement.
	merged.ProblemStatement = mergeProblemStatements(claude.ProblemStatement, codex.ProblemStatement)

	// Merge scope (union-unique for both in_scope and out_of_scope).
	merged.Scope = Scope{
		InScope:    unionStringsCaseInsensitive(claude.Scope.InScope, codex.Scope.InScope),
		OutOfScope: unionStringsCaseInsensitive(claude.Scope.OutOfScope, codex.Scope.OutOfScope),
	}

	// Merge constraints (union by text, case-insensitive).
	merged.Constraints = unionStringsCaseInsensitive(claude.Constraints, codex.Constraints)

	// Merge integration points by system name (case-insensitive).
	merged.IntegrationPoints = unionIntegrationPointsBySystem(claude.IntegrationPoints, codex.IntegrationPoints)

	// Merge priorities by item (case-insensitive).
	merged.Priorities = unionPrioritiesByItem(claude.Priorities, codex.Priorities)

	// Merge assumptions by assumption text (case-insensitive).
	merged.Assumptions = unionAssumptionsByText(claude.Assumptions, codex.Assumptions)

	// Merge open questions (union by text, case-insensitive).
	merged.OpenQuestions = unionStringsCaseInsensitive(claude.OpenQuestions, codex.OpenQuestions)

	return merged
}

// mergeProblemStatements returns a single problem statement when both are
// identical (case-insensitive, whitespace-normalised), or both with provider
// attribution when they differ materially (both non-empty and different).
// When one is empty, the non-empty one is used directly without attribution.
func mergeProblemStatements(claude, codex string) string {
	claudeTrimmed := strings.TrimSpace(claude)
	codexTrimmed := strings.TrimSpace(codex)

	// If either is empty, use the non-empty one (prefer Claude).
	if claudeTrimmed == "" && codexTrimmed == "" {
		return ""
	}
	if codexTrimmed == "" {
		return claude
	}
	if claudeTrimmed == "" {
		return codex
	}

	// Both non-empty: check if identical.
	if normalizeSection(claude) == normalizeSection(codex) {
		return claude // prefer Claude's casing
	}

	// Both non-empty and different — include both with attribution.
	return fmt.Sprintf("[Claude]: %s\n\n[Codex]: %s", claude, codex)
}

// unionStringsCaseInsensitive returns the union of two string slices,
// deduplicating by case-insensitive comparison. Claude's items come first;
// Codex items are appended only if not already present.
func unionStringsCaseInsensitive(claude, codex []string) []string {
	seen := make(map[string]bool, len(claude))
	var result []string
	for _, s := range claude {
		key := strings.ToLower(strings.TrimSpace(s))
		if key == "" {
			continue
		}
		if !seen[key] {
			seen[key] = true
			result = append(result, s)
		}
	}
	for _, s := range codex {
		key := strings.ToLower(strings.TrimSpace(s))
		if key == "" {
			continue
		}
		if !seen[key] {
			seen[key] = true
			result = append(result, s)
		}
	}
	return result
}

// unionActorsByName merges actors by name (case-insensitive). When both
// providers have an actor with the same name, Claude's version is preferred.
// If Claude's description is empty, Codex's description is used.
func unionActorsByName(claude, codex []Actor) []Actor {
	seen := make(map[string]int, len(claude)) // key -> index in result
	var result []Actor
	for _, a := range claude {
		key := strings.ToLower(strings.TrimSpace(a.Name))
		if _, ok := seen[key]; !ok {
			seen[key] = len(result)
			result = append(result, a)
		}
	}
	for _, a := range codex {
		key := strings.ToLower(strings.TrimSpace(a.Name))
		if idx, ok := seen[key]; ok {
			// Actor already present from Claude — backfill empty description.
			if strings.TrimSpace(result[idx].Description) == "" && strings.TrimSpace(a.Description) != "" {
				result[idx].Description = a.Description
			}
		} else {
			seen[key] = len(result)
			result = append(result, a)
		}
	}
	return result
}

// unionIntegrationPointsBySystem merges integration points by system name
// (case-insensitive). Conflict resolution: prefer Claude; backfill empty
// description from Codex.
func unionIntegrationPointsBySystem(claude, codex []IntegrationPoint) []IntegrationPoint {
	seen := make(map[string]int, len(claude))
	var result []IntegrationPoint
	for _, ip := range claude {
		key := strings.ToLower(strings.TrimSpace(ip.System))
		if _, ok := seen[key]; !ok {
			seen[key] = len(result)
			result = append(result, ip)
		}
	}
	for _, ip := range codex {
		key := strings.ToLower(strings.TrimSpace(ip.System))
		if idx, ok := seen[key]; ok {
			if strings.TrimSpace(result[idx].Description) == "" && strings.TrimSpace(ip.Description) != "" {
				result[idx].Description = ip.Description
			}
		} else {
			seen[key] = len(result)
			result = append(result, ip)
		}
	}
	return result
}

// unionPrioritiesByItem merges priorities by item text (case-insensitive).
// Conflict resolution: prefer Claude; backfill empty rationale from Codex.
func unionPrioritiesByItem(claude, codex []Priority) []Priority {
	seen := make(map[string]int, len(claude))
	var result []Priority
	for _, p := range claude {
		key := strings.ToLower(strings.TrimSpace(p.Item))
		if _, ok := seen[key]; !ok {
			seen[key] = len(result)
			result = append(result, p)
		}
	}
	for _, p := range codex {
		key := strings.ToLower(strings.TrimSpace(p.Item))
		if idx, ok := seen[key]; ok {
			if strings.TrimSpace(result[idx].Rationale) == "" && strings.TrimSpace(p.Rationale) != "" {
				result[idx].Rationale = p.Rationale
			}
		} else {
			seen[key] = len(result)
			result = append(result, p)
		}
	}
	return result
}

// unionAssumptionsByText merges assumptions by assumption text
// (case-insensitive). Conflict resolution: prefer Claude; backfill empty
// Confidence from Codex.
func unionAssumptionsByText(claude, codex []Assumption) []Assumption {
	seen := make(map[string]int, len(claude))
	var result []Assumption
	for _, a := range claude {
		key := strings.ToLower(strings.TrimSpace(a.Assumption))
		if _, ok := seen[key]; !ok {
			seen[key] = len(result)
			result = append(result, a)
		}
	}
	for _, a := range codex {
		key := strings.ToLower(strings.TrimSpace(a.Assumption))
		if idx, ok := seen[key]; ok {
			if strings.TrimSpace(result[idx].Confidence) == "" && strings.TrimSpace(a.Confidence) != "" {
				result[idx].Confidence = a.Confidence
			}
		} else {
			seen[key] = len(result)
			result = append(result, a)
		}
	}
	return result
}
