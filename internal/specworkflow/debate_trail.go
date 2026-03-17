package specworkflow

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// AssembleDebateTrail builds a markdown document that records the full
// lifecycle of every finding tracked by the IssueTracker. Findings are
// rendered in ID order, with chronological status-change entries per finding.
// All content is assembled deterministically from structured data.
func AssembleDebateTrail(config FinalizeConfig, tracker *IssueTracker, rounds int) (string, error) {
	// Collect all findings sorted by ID.
	ids := make([]string, 0, len(tracker.Issues))
	for id := range tracker.Issues {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var b strings.Builder
	b.WriteString("# Debate Trail\n\n")

	for _, id := range ids {
		issue := tracker.Issues[id]
		finding := issue.Finding

		// Heading: ### {finding_id}: {affected_section} [{severity}]
		b.WriteString(fmt.Sprintf("### %s: %s [%s]\n\n", finding.ID, finding.AffectedSection, finding.Severity))

		// Who raised it.
		if len(finding.RaisedBy) > 0 {
			b.WriteString(fmt.Sprintf("**Raised by:** %s\n\n", strings.Join(finding.RaisedBy, ", ")))
		}

		b.WriteString(fmt.Sprintf("**Description:** %s\n\n", finding.Description))

		// Lifecycle history from status changes.
		history := tracker.History[id]
		if len(history) > 0 {
			b.WriteString("**Lifecycle:**\n\n")
			for _, change := range history {
				b.WriteString(fmt.Sprintf("- Round %d: %s -> %s", change.Round, change.From, change.To))
				if change.Reason != "" {
					b.WriteString(fmt.Sprintf(" (%s)", change.Reason))
				}
				b.WriteString("\n")
			}
			b.WriteByte('\n')
		}

		// Current status.
		b.WriteString(fmt.Sprintf("**Current status:** %s\n\n", issue.Status))
	}

	return b.String(), nil
}

// WriteDebateTrail assembles the debate trail markdown and writes it to
// debate-trail.md in the spec directory for the configured feature.
func WriteDebateTrail(config FinalizeConfig, tracker *IssueTracker, rounds int) error {
	content, err := AssembleDebateTrail(config, tracker, rounds)
	if err != nil {
		return fmt.Errorf("assemble debate trail: %w", err)
	}

	dir := specDir(config)
	path := filepath.Join(dir, "debate-trail.md")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("write debate trail to %s: %w", path, err)
	}

	return nil
}
