package specworkflow

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// handleFinalized acknowledges minor findings, writes the debate trail,
// assembles the final spec, and saves the terminal state.
func (o *Orchestrator) handleFinalized(state *WorkflowStateJSON, specDir string) error {
	// Acknowledge minor findings.
	o.tracker.AcknowledgeMinorFindings(state.Round)

	// Write debate trail.
	finConfig := FinalizeConfig{
		WorkspaceDir: o.workspaceDir,
		FeatureName:  o.featureName,
	}
	WriteDebateTrail(finConfig, o.tracker, state.Round)

	// Assemble full final spec (spec content + holdouts + convergence
	// summary + accepted risks + debate trail).
	if err := AssembleFinalSpec(finConfig, state, o.tracker); err != nil {
		log.Printf("warning: AssembleFinalSpec failed: %v", err)
	}

	// Save state before transitioning to taskify.
	if err := SaveState(specDir, state); err != nil {
		log.Printf("warning: failed to save finalized state: %v", err)
	}

	// FINALIZED is no longer terminal — advance to TASKIFY.
	o.logTransition(StateFinalized, StateTaskify)
	if err := o.sm.Transition(StateTaskify); err != nil {
		return fmt.Errorf("transition FINALIZED -> TASKIFY: %w", err)
	}
	return nil
}

// handleEscalated writes an escalation summary, debate trail, and saves
// the terminal state.
func (o *Orchestrator) handleEscalated(state *WorkflowStateJSON, specDir string) error {
	finConfig := FinalizeConfig{
		WorkspaceDir: o.workspaceDir,
		FeatureName:  o.featureName,
	}

	// Write escalation summary.
	o.writeEscalationSummary(specDir, state)

	// Write debate trail to preserve state.
	WriteDebateTrail(finConfig, o.tracker, state.Round)

	// Save escalated state.
	if err := SaveState(specDir, state); err != nil {
		log.Printf("warning: failed to save escalated state: %v", err)
	}

	o.logger.LogStateTransition(StateEscalated, StateEscalated, state.Round)
	return nil
}

// writeEscalationSummary writes an escalation-summary.md to the spec directory
// capturing the current state, reason for escalation, findings summary, and
// the current spec version path for human review.
func (o *Orchestrator) writeEscalationSummary(specDir string, state *WorkflowStateJSON) {
	summary := o.tracker.GetFindingSummary()
	specPath := o.currentSpecPath(state)

	var b strings.Builder
	b.WriteString("# Escalation Summary\n\n")

	// State and round info.
	b.WriteString("## Workflow State\n\n")
	b.WriteString(fmt.Sprintf("- **State:** %s\n", state.State))
	b.WriteString(fmt.Sprintf("- **Round:** %d\n", state.Round))
	b.WriteString(fmt.Sprintf("- **Feature:** %s\n", state.FeatureName))
	b.WriteString(fmt.Sprintf("- **Started at:** %s\n", state.StartedAt))
	b.WriteString(fmt.Sprintf("- **Cumulative cost:** $%.4f\n", state.CumulativeCostUSD))
	b.WriteString(fmt.Sprintf("- **Agent invocations:** %d\n", state.AgentInvocations))

	// Reason for escalation.
	b.WriteString("\n## Escalation Reason\n\n")
	b.WriteString("User cancelled or workflow could not converge.\n")

	// Findings summary.
	b.WriteString("\n## Findings Summary\n\n")
	b.WriteString(fmt.Sprintf("- **Total raised:** %d\n", summary.Raised))
	b.WriteString(fmt.Sprintf("- **Total closed:** %d\n", summary.Closed))
	b.WriteString(fmt.Sprintf("- **Open critical:** %d\n", summary.OpenCritical))
	b.WriteString(fmt.Sprintf("- **Open major:** %d\n", summary.OpenMajor))

	// Current spec version.
	b.WriteString("\n## Current Spec Version\n\n")
	b.WriteString(fmt.Sprintf("The latest spec version is preserved at: `%s`\n", specPath))

	summaryPath := filepath.Join(specDir, "escalation-summary.md")
	if err := os.WriteFile(summaryPath, []byte(b.String()), 0o644); err != nil {
		log.Printf("warning: failed to write escalation summary: %v", err)
	}
}
