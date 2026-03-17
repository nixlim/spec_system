package specworkflow

import "testing"

func TestDefaultTeamConfig_Has8Agents(t *testing.T) {
	config := DefaultTeamConfig()
	if got := len(config.Agents); got != 8 {
		t.Errorf("expected 8 agents, got %d", got)
	}
}

func TestDefaultTeamConfig_AllUseClaudeProvider(t *testing.T) {
	config := DefaultTeamConfig()
	for _, agent := range config.Agents {
		if agent.Provider != "claude" {
			t.Errorf("agent %q: expected provider %q, got %q", agent.Name, "claude", agent.Provider)
		}
	}
}

func TestGetReviewerConfigs_Returns4Reviewers(t *testing.T) {
	config := DefaultTeamConfig()
	reviewers := GetReviewerConfigs(config)
	if got := len(reviewers); got != 4 {
		t.Errorf("expected 4 reviewers, got %d", got)
	}
	for _, r := range reviewers {
		if r.Role != "reviewer" {
			t.Errorf("expected role %q, got %q for agent %q", "reviewer", r.Role, r.Name)
		}
	}
}

func TestValidateTeamConfig_PassesWithValidConfig(t *testing.T) {
	config := DefaultTeamConfig()
	if err := ValidateTeamConfig(config); err != nil {
		t.Fatalf("expected valid config, got error: %v", err)
	}
}

func TestValidateTeamConfig_FailsWithMissingAgent(t *testing.T) {
	config := DefaultTeamConfig()
	// Remove the last agent (judge).
	config.Agents = config.Agents[:len(config.Agents)-1]

	err := ValidateTeamConfig(config)
	if err == nil {
		t.Fatal("expected error for missing agent, got nil")
	}
}

func TestValidateTeamConfig_FailsWithWrongProvider(t *testing.T) {
	config := DefaultTeamConfig()
	config.Agents[0].Provider = "openai"

	err := ValidateTeamConfig(config)
	if err == nil {
		t.Fatal("expected error for wrong provider, got nil")
	}
}
