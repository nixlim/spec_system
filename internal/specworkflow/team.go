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

// requiredNonReviewerNames lists the non-reviewer agents that must always be
// present in a valid team configuration.
var requiredNonReviewerNames = []string{
	"discovery",
	"drafter",
	"reviser",
	"judge",
}

// requiredClaudeReviewerNames lists the claude reviewer agents that must be
// present in a valid team configuration.
var requiredClaudeReviewerNames = []string{
	"reviewer-clarity-claude",
	"reviewer-consistency-claude",
	"reviewer-security-claude",
	"reviewer-correctness-claude",
}

// reviewerBaseLenses lists the base lens names for reviewers (without provider suffix).
var reviewerBaseLenses = []string{"clarity", "consistency", "security", "correctness"}

// reviewerDescriptions maps base lens names to human-readable descriptions.
var reviewerDescriptions = map[string]string{
	"clarity":      "Reviews the specification for ambiguity and incompleteness using AMB and INC lenses.",
	"consistency":  "Reviews the specification for consistency and feasibility using CON and FEA lenses.",
	"security":     "Reviews the specification for security and operability using SEC and OPS lenses.",
	"correctness":  "Reviews the specification for correctness and complexity using COR and CPX lenses.",
}

// DefaultTeamConfig returns a TeamConfig for the adversarial spec review
// workflow. When enableCodex is false, returns 8 agents with reviewer names
// suffixed with "-claude". When enableCodex is true, returns 12 agents: the
// 8 claude agents plus 4 codex reviewers with "-codex" suffix.
func DefaultTeamConfig(enableCodex bool) TeamConfig {
	agents := []AgentConfig{
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
	}

	// Add claude reviewers with -claude suffix.
	for _, lens := range reviewerBaseLenses {
		agents = append(agents, AgentConfig{
			Name:           fmt.Sprintf("reviewer-%s-claude", lens),
			Provider:       "claude",
			Role:           "reviewer",
			Count:          1,
			Description:    reviewerDescriptions[lens],
			TimeoutSeconds: 120,
		})
	}

	// Add codex reviewers if enabled.
	if enableCodex {
		for _, lens := range reviewerBaseLenses {
			agents = append(agents, AgentConfig{
				Name:           fmt.Sprintf("reviewer-%s-codex", lens),
				Provider:       "codex",
				Role:           "reviewer",
				Count:          1,
				Description:    reviewerDescriptions[lens],
				TimeoutSeconds: 300,
			})
		}
	}

	agents = append(agents, AgentConfig{
		Name:           "judge",
		Provider:       "claude",
		Role:           "judge",
		Count:          1,
		Description:    "Evaluates whether the revised specification adequately addresses findings and renders a verdict.",
		TimeoutSeconds: 120,
	})

	return TeamConfig{Agents: agents}
}

// ValidateTeamConfig validates team configuration constraints:
//   - All required non-reviewer agents (discovery, drafter, reviser, judge) must be present.
//   - At least the 4 claude reviewer agents must be present.
//   - The "codex" provider is only allowed on agents with the "reviewer" or "holdout" role.
//   - Legacy reviewer names (without provider suffix) are accepted for backward compatibility.
func ValidateTeamConfig(config TeamConfig) error {
	nameSet := make(map[string]bool, len(config.Agents))
	reviewerCount := 0

	for _, agent := range config.Agents {
		nameSet[agent.Name] = true
		if agent.Role == "reviewer" {
			reviewerCount++
		}

		// Codex provider is only allowed for reviewer and holdout roles.
		if agent.Provider == "codex" && agent.Role != "reviewer" && agent.Role != "holdout" {
			return fmt.Errorf("agent %q: codex provider only supported for reviewer role", agent.Name)
		}

		// Non-codex agents must use "claude".
		if agent.Provider != "claude" && agent.Provider != "codex" {
			return fmt.Errorf("agent %q: provider must be %q or %q, got %q", agent.Name, "claude", "codex", agent.Provider)
		}
	}

	// Check required non-reviewer agents.
	for _, required := range requiredNonReviewerNames {
		if !nameSet[required] {
			return fmt.Errorf("missing required agent: %q", required)
		}
	}

	// Check required claude reviewers. Accept either suffixed or legacy (unsuffixed) names.
	for _, required := range requiredClaudeReviewerNames {
		if !nameSet[required] {
			// Accept legacy name without -claude suffix.
			legacy := required[:len(required)-len("-claude")]
			if !nameSet[legacy] {
				return fmt.Errorf("missing required agent: %q", required)
			}
		}
	}

	if reviewerCount < 4 {
		return fmt.Errorf("at least 4 reviewer agents required, got %d", reviewerCount)
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
