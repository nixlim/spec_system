package codedoc

// FinalGatePayload returns the current final-gate payload derived from the
// orchestrator's in-memory findings and discovery snapshot.
func (o *CodedocOrchestrator) FinalGatePayload() FinalGatePayload {
	unresolved := make([]UnresolvedFinding, 0, len(o.mergedFindings))
	for _, f := range FilterOpenFindings(o.mergedFindings) {
		if f.Severity != SeverityCritical && f.Severity != SeverityMajor {
			continue
		}
		unresolved = append(unresolved, UnresolvedFinding{
			ID:          f.ID,
			Description: f.Description,
			Severity:    f.Severity,
			Lens:        f.Lens,
			Section:     f.AffectedSection,
		})
	}

	writer := Writer{
		Config:          o.config,
		CodePath:        o.codePath,
		Feature:         o.featureName,
		DiscoveryOutput: o.discoveryOutput,
	}

	return FinalGatePayload{
		UnresolvedFindings: unresolved,
		TotalUnresolved:    len(unresolved),
		DriftWarning:       writer.DetectDrift(),
	}
}
