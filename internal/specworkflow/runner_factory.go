package specworkflow

import "github.com/foundry-zero/adversarial-spec-system/internal/process"

// DefaultPrimaryRunner constructs an AgentRunner for the configured primary
// provider. When PrimaryProvider is "opencode", it returns an OpenCodeRunner;
// otherwise it returns a ClaudeRunner with OTEL environment wiring.
func DefaultPrimaryRunner(cfg SpecWorkflowConfig, workspaceDir string, otelPort int, featureName string, tracker *process.ProcessTracker) AgentRunner {
	switch cfg.PrimaryProvider {
	case "opencode":
		r := DefaultOpenCodeRunner(cfg.OpenCodeModels.Default, workspaceDir, nil)
		r.Tracker = tracker
		r.Feature = featureName
		return r
	default:
		r := DefaultClaudeRunner(workspaceDir, otelPort, featureName, cfg.ClaudeModels.Default)
		r.Tracker = tracker
		r.Feature = featureName
		return r
	}
}
