// team.go defines the agent team configuration for the adversarial spec review
// workflow. Currently the standard 8-agent team is hardcoded with the "claude"
// provider. ValidateTeamConfig is called during orchestrator construction to
// enforce the invariant that all required agents are present. GetReviewerConfigs
// and per-agent TimeoutSeconds are reserved for future multi-provider support
// where different agents may use different LLM providers or timeout budgets.
package specworkflow

import "fmt"

// ---------------------------------------------------------------------------
// Agent and team configuration
// ---------------------------------------------------------------------------

// AgentConfig describes a single agent in the adversarial spec review team.
type AgentConfig struct {
	// Name is the unique identifier for the agent (e.g. "discovery", "reviewer-clarity").
	Name string `json:"name" yaml:"name"`
	// Provider is the LLM provider to use (e.g. "claude").
	Provider string `json:"provider" yaml:"provider"`
	// Role is the functional role: one of "discovery", "drafter", "reviser", "reviewer", or "judge".
	Role string `json:"role" yaml:"role"`
	// Count is the number of instances of this agent to run (typically 1).
	Count int `json:"count" yaml:"count"`
	// Description is a human-readable summary of what this agent does.
	Description string `json:"description" yaml:"description"`
	// TimeoutSeconds is the maximum time allowed for the agent to complete, in seconds.
	TimeoutSeconds int `json:"timeout_seconds" yaml:"timeout_seconds"`
}

// TeamConfig holds the full team of agents for the adversarial spec review workflow.
type TeamConfig struct {
	// Agents is the ordered list of agent configurations.
	Agents []AgentConfig `json:"agents" yaml:"agents"`
}

// requiredAgentNames is the canonical set of agent names that must be present
// in a valid team configuration.
var requiredAgentNames = []string{
	"discovery",
	"drafter",
	"reviser",
	"reviewer-clarity",
	"reviewer-consistency",
	"reviewer-security",
	"reviewer-correctness",
	"judge",
}

// DefaultTeamConfig returns a TeamConfig with the standard 8-agent team for
// the adversarial spec review workflow. All agents use the "claude" provider.
func DefaultTeamConfig() TeamConfig {
	return TeamConfig{
		Agents: []AgentConfig{
			{
				Name:           "discovery",
				Provider:       "claude",
				Role:           "discovery",
				Count:          1,
				Description:    "Analyses source documents to extract actors, scope, constraints, and requirements.",
				TimeoutSeconds: 120,
			},
			{
				Name:           "drafter",
				Provider:       "claude",
				Role:           "drafter",
				Count:          1,
				Description:    "Produces a complete specification document and holdout test dataset from confirmed requirements.",
				TimeoutSeconds: 180,
			},
			{
				Name:           "reviser",
				Provider:       "claude",
				Role:           "reviser",
				Count:          1,
				Description:    "Revises the specification to address findings from the review round.",
				TimeoutSeconds: 180,
			},
			{
				Name:           "reviewer-clarity",
				Provider:       "claude",
				Role:           "reviewer",
				Count:          1,
				Description:    "Reviews the specification for ambiguity and incompleteness using AMB and INC lenses.",
				TimeoutSeconds: 120,
			},
			{
				Name:           "reviewer-consistency",
				Provider:       "claude",
				Role:           "reviewer",
				Count:          1,
				Description:    "Reviews the specification for consistency and feasibility using CON and FEA lenses.",
				TimeoutSeconds: 120,
			},
			{
				Name:           "reviewer-security",
				Provider:       "claude",
				Role:           "reviewer",
				Count:          1,
				Description:    "Reviews the specification for security and operability using SEC and OPS lenses.",
				TimeoutSeconds: 120,
			},
			{
				Name:           "reviewer-correctness",
				Provider:       "claude",
				Role:           "reviewer",
				Count:          1,
				Description:    "Reviews the specification for correctness and complexity using COR and CPX lenses.",
				TimeoutSeconds: 120,
			},
			{
				Name:           "judge",
				Provider:       "claude",
				Role:           "judge",
				Count:          1,
				Description:    "Evaluates whether the revised specification adequately addresses findings and renders a verdict.",
				TimeoutSeconds: 120,
			},
		},
	}
}

// ValidateTeamConfig validates that all 8 required agents are present in the
// configuration and that all agents use the "claude" provider. Returns an
// error describing the first violated constraint.
func ValidateTeamConfig(config TeamConfig) error {
	nameSet := make(map[string]bool, len(config.Agents))
	for _, agent := range config.Agents {
		nameSet[agent.Name] = true
		if agent.Provider != "claude" {
			return fmt.Errorf("agent %q: provider must be %q, got %q", agent.Name, "claude", agent.Provider)
		}
	}

	for _, required := range requiredAgentNames {
		if !nameSet[required] {
			return fmt.Errorf("missing required agent: %q", required)
		}
	}

	return nil
}

// GetReviewerConfigs returns the subset of agents with role "reviewer" from
// the given team configuration.
func GetReviewerConfigs(config TeamConfig) []AgentConfig {
	var reviewers []AgentConfig
	for _, agent := range config.Agents {
		if agent.Role == "reviewer" {
			reviewers = append(reviewers, agent)
		}
	}
	return reviewers
}
