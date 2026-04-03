package specworkflow

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// FinalizeConfig holds the parameters needed to assemble the final spec.
type FinalizeConfig struct {
	// WorkspaceDir is the root workspace directory.
	WorkspaceDir string
	// FeatureName is the name of the feature being specified.
	FeatureName string
}

// specDir returns the directory under WorkspaceDir where spec artefacts for
// the configured feature are stored.
func specDir(config FinalizeConfig) string {
	return filepath.Join(config.WorkspaceDir, "specs", config.FeatureName)
}

// AssembleFinalSpec builds the final specification document by combining the
// current spec version with holdout scenarios, a convergence summary, accepted
// risks, and the debate trail. All assembly is deterministic code operating on
// structured data -- no LLM-generated content is introduced.
func AssembleFinalSpec(config FinalizeConfig, state *WorkflowStateJSON, tracker *IssueTracker) error {
	dir := specDir(config)

	// 1. Copy spec-v{current_spec_version}.md to spec-final.md.
	srcName := fmt.Sprintf("spec-v%d.md", state.CurrentSpecVersion)
	srcPath := filepath.Join(dir, srcName)
	specContent, err := os.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("read source spec %s: %w", srcPath, err)
	}

	var final strings.Builder
	final.Write(specContent)

	// Ensure trailing newline before appending sections.
	if len(specContent) > 0 && specContent[len(specContent)-1] != '\n' {
		final.WriteByte('\n')
	}

	// 2. Read holdout file and append holdout evaluation scenarios.
	holdoutPath := resolveFinalizeHoldoutPath(config, state)
	holdoutContent, err := os.ReadFile(holdoutPath)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("warning: holdout file %s not found, skipping holdout section", holdoutPath)
		} else {
			return fmt.Errorf("read holdout file %s: %w", holdoutPath, err)
		}
	}
	if len(holdoutContent) > 0 {
		final.WriteString("\n## Holdout Evaluation Scenarios\n\n")
		final.Write(holdoutContent)
		if holdoutContent[len(holdoutContent)-1] != '\n' {
			final.WriteByte('\n')
		}
	}

	// 3. Append convergence summary.
	summary := tracker.GetFindingSummary()
	acknowledged := len(tracker.GetFindingsByStatus(StatusAcknowledged))

	final.WriteString("\n## Convergence Summary\n\n")
	final.WriteString(fmt.Sprintf("- **Rounds completed:** %d\n", state.Round))
	final.WriteString(fmt.Sprintf("- **Total findings raised:** %d\n", summary.Raised))
	final.WriteString(fmt.Sprintf("- **Total findings closed:** %d\n", summary.Closed))
	final.WriteString(fmt.Sprintf("- **Findings acknowledged:** %d\n", acknowledged))
	final.WriteString("- **Final verdict:** PASS\n")
	final.WriteString(fmt.Sprintf("- **Cumulative cost:** $%.4f\n", state.CumulativeCostUSD))
	final.WriteString(fmt.Sprintf("- **Wall clock time:** %.1fs\n", state.CumulativeWallClockSeconds))

	// 4. Append accepted risks (acknowledged + dismissed findings).
	acceptedRisks := collectAcceptedRisks(tracker)
	final.WriteString("\n## Accepted Risks\n\n")
	if len(acceptedRisks) == 0 {
		final.WriteString("No accepted risks.\n")
	} else {
		for _, ar := range acceptedRisks {
			final.WriteString(fmt.Sprintf("### %s [%s] (%s)\n\n", ar.ID, ar.Severity, ar.Status))
			final.WriteString(fmt.Sprintf("**Description:** %s\n\n", ar.Description))
			final.WriteString(fmt.Sprintf("**Rationale:** %s\n\n", ar.Rationale))
		}
	}

	// 5. Append debate trail.
	debateTrailPath := filepath.Join(dir, "debate-trail.md")
	debateContent, err := os.ReadFile(debateTrailPath)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("warning: debate trail file %s not found, skipping debate trail section", debateTrailPath)
		} else {
			return fmt.Errorf("read debate trail %s: %w", debateTrailPath, err)
		}
	}
	if len(debateContent) > 0 {
		final.WriteString("\n## Debate Trail\n\n")
		final.Write(debateContent)
		if debateContent[len(debateContent)-1] != '\n' {
			final.WriteByte('\n')
		}
	}

	// Write spec-final.md.
	finalPath := filepath.Join(dir, "spec-final.md")
	if err := os.WriteFile(finalPath, []byte(final.String()), 0644); err != nil {
		return fmt.Errorf("write final spec %s: %w", finalPath, err)
	}

	// 6. Update workflow state to FINALIZED.
	state.State = StateFinalized
	if err := SaveState(dir, state); err != nil {
		return fmt.Errorf("save finalized state: %w", err)
	}

	return nil
}

func resolveFinalizeHoldoutPath(config FinalizeConfig, state *WorkflowStateJSON) string {
	dir := specDir(config)
	if state != nil && state.Round > 0 {
		roundPath := filepath.Join(dir, fmt.Sprintf("holdouts-round-%d.md", state.Round))
		if _, err := os.Stat(roundPath); err == nil {
			return roundPath
		}
	}
	return filepath.Join(dir, fmt.Sprintf("%s-holdouts.md", config.FeatureName))
}

// acceptedRisk is a helper type for accepted risk entries.
type acceptedRisk struct {
	ID          string
	Severity    string
	Status      string
	Description string
	Rationale   string
}

// collectAcceptedRisks gathers all findings with status "acknowledged" or
// "dismissed", sorted by finding ID for deterministic output.
func collectAcceptedRisks(tracker *IssueTracker) []acceptedRisk {
	ack := tracker.GetFindingsByStatus(StatusAcknowledged)
	dis := tracker.GetFindingsByStatus(StatusDismissed)

	var risks []acceptedRisk
	for _, ti := range ack {
		rationale := "Acknowledged without revision"
		if ti.Finding.ResolutionNotes != nil && *ti.Finding.ResolutionNotes != "" {
			rationale = *ti.Finding.ResolutionNotes
		}
		risks = append(risks, acceptedRisk{
			ID:          ti.Finding.ID,
			Severity:    ti.Finding.Severity.String(),
			Status:      string(ti.Status),
			Description: ti.Finding.Description,
			Rationale:   rationale,
		})
	}
	for _, ti := range dis {
		rationale := "Dismissed"
		if ti.Finding.DismissalRationale != nil && *ti.Finding.DismissalRationale != "" {
			rationale = *ti.Finding.DismissalRationale
		}
		risks = append(risks, acceptedRisk{
			ID:          ti.Finding.ID,
			Severity:    ti.Finding.Severity.String(),
			Status:      string(ti.Status),
			Description: ti.Finding.Description,
			Rationale:   rationale,
		})
	}

	// Sort by ID for deterministic output. Both sub-slices are already
	// sorted by GetFindingsByStatus, but the merged list needs a full sort.
	sortAcceptedRisks(risks)
	return risks
}

// sortAcceptedRisks sorts accepted risk entries by ID.
func sortAcceptedRisks(risks []acceptedRisk) {
	for i := 1; i < len(risks); i++ {
		for j := i; j > 0 && risks[j].ID < risks[j-1].ID; j-- {
			risks[j], risks[j-1] = risks[j-1], risks[j]
		}
	}
}
