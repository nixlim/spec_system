package specworkflow

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// DefaultTeamConfig tests
// ---------------------------------------------------------------------------

func TestTeamConfig_WithoutCodex(t *testing.T) {
	config := DefaultTeamConfig(false)
	if got := len(config.Agents); got != 8 {
		t.Errorf("expected 8 agents, got %d", got)
	}

	// Verify reviewer names have -claude suffix.
	expectedReviewers := map[string]bool{
		"reviewer-clarity-claude":      false,
		"reviewer-consistency-claude":  false,
		"reviewer-security-claude":     false,
		"reviewer-correctness-claude":  false,
	}
	for _, agent := range config.Agents {
		if agent.Role == "reviewer" {
			if _, ok := expectedReviewers[agent.Name]; !ok {
				t.Errorf("unexpected reviewer name: %q", agent.Name)
			} else {
				expectedReviewers[agent.Name] = true
			}
			if agent.Provider != "claude" {
				t.Errorf("reviewer %q: expected provider %q, got %q", agent.Name, "claude", agent.Provider)
			}
		}
	}
	for name, found := range expectedReviewers {
		if !found {
			t.Errorf("expected reviewer %q not found", name)
		}
	}
}

func TestTeamConfig_WithCodex(t *testing.T) {
	config := DefaultTeamConfig(true)
	if got := len(config.Agents); got != 12 {
		t.Errorf("expected 12 agents, got %d", got)
	}

	// Verify all reviewer names and providers.
	reviewerProviders := make(map[string]string)
	for _, agent := range config.Agents {
		if agent.Role == "reviewer" {
			reviewerProviders[agent.Name] = agent.Provider
		}
	}

	expectedReviewers := map[string]string{
		"reviewer-clarity-claude":      "claude",
		"reviewer-consistency-claude":  "claude",
		"reviewer-security-claude":     "claude",
		"reviewer-correctness-claude":  "claude",
		"reviewer-clarity-codex":       "codex",
		"reviewer-consistency-codex":   "codex",
		"reviewer-security-codex":      "codex",
		"reviewer-correctness-codex":   "codex",
	}

	if len(reviewerProviders) != len(expectedReviewers) {
		t.Errorf("expected %d reviewers, got %d", len(expectedReviewers), len(reviewerProviders))
	}
	for name, wantProvider := range expectedReviewers {
		gotProvider, ok := reviewerProviders[name]
		if !ok {
			t.Errorf("missing reviewer %q", name)
		} else if gotProvider != wantProvider {
			t.Errorf("reviewer %q: expected provider %q, got %q", name, wantProvider, gotProvider)
		}
	}

	// Verify non-reviewer agents unchanged.
	nonReviewers := make(map[string]string)
	for _, agent := range config.Agents {
		if agent.Role != "reviewer" {
			nonReviewers[agent.Name] = agent.Provider
		}
	}
	for _, name := range []string{"discovery", "drafter", "reviser", "judge"} {
		provider, ok := nonReviewers[name]
		if !ok {
			t.Errorf("missing non-reviewer agent %q", name)
		} else if provider != "claude" {
			t.Errorf("non-reviewer %q: expected provider %q, got %q", name, "claude", provider)
		}
	}
}

func TestTeamConfig_AllClaudeProviderWithoutCodex(t *testing.T) {
	config := DefaultTeamConfig(false)
	for _, agent := range config.Agents {
		if agent.Provider != "claude" {
			t.Errorf("agent %q: expected provider %q, got %q", agent.Name, "claude", agent.Provider)
		}
	}
}

func TestGetReviewerConfigs_Returns4ReviewersWithoutCodex(t *testing.T) {
	config := DefaultTeamConfig(false)
	reviewers := GetReviewerConfigs(config)
	if got := len(reviewers); got != 4 {
		t.Errorf("expected 4 reviewers, got %d", got)
	}
}

func TestGetReviewerConfigs_Returns8ReviewersWithCodex(t *testing.T) {
	config := DefaultTeamConfig(true)
	reviewers := GetReviewerConfigs(config)
	if got := len(reviewers); got != 8 {
		t.Errorf("expected 8 reviewers, got %d", got)
	}
}

// ---------------------------------------------------------------------------
// ValidateTeamConfig tests
// ---------------------------------------------------------------------------

func TestValidateTeamConfig_PassesWithValidConfig(t *testing.T) {
	config := DefaultTeamConfig(false)
	if err := ValidateTeamConfig(config); err != nil {
		t.Fatalf("expected valid config, got error: %v", err)
	}
}

func TestValidateTeamConfig_PassesWithCodexConfig(t *testing.T) {
	config := DefaultTeamConfig(true)
	if err := ValidateTeamConfig(config); err != nil {
		t.Fatalf("expected valid config with codex, got error: %v", err)
	}
}

func TestTeamConfig_ValidateCodexReviewer(t *testing.T) {
	// Codex provider on reviewer role should pass.
	config := DefaultTeamConfig(false)
	config.Agents = append(config.Agents, AgentConfig{
		Name:     "reviewer-extra-codex",
		Provider: "codex",
		Role:     "reviewer",
		Count:    1,
	})
	if err := ValidateTeamConfig(config); err != nil {
		t.Fatalf("codex reviewer should be valid, got error: %v", err)
	}
}

func TestTeamConfig_ValidateCodexNonReviewer(t *testing.T) {
	// Codex provider on judge role should fail.
	config := DefaultTeamConfig(false)
	// Change judge to codex.
	for i := range config.Agents {
		if config.Agents[i].Name == "judge" {
			config.Agents[i].Provider = "codex"
			break
		}
	}
	err := ValidateTeamConfig(config)
	if err == nil {
		t.Fatal("expected error for codex on judge role, got nil")
	}
	if !strings.Contains(err.Error(), "codex provider only supported for reviewer role") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidateTeamConfig_FailsWithMissingAgent(t *testing.T) {
	config := DefaultTeamConfig(false)
	// Remove the last agent (judge).
	config.Agents = config.Agents[:len(config.Agents)-1]

	err := ValidateTeamConfig(config)
	if err == nil {
		t.Fatal("expected error for missing agent, got nil")
	}
}

func TestValidateTeamConfig_FailsWithWrongProvider(t *testing.T) {
	config := DefaultTeamConfig(false)
	config.Agents[0].Provider = "openai"

	err := ValidateTeamConfig(config)
	if err == nil {
		t.Fatal("expected error for wrong provider, got nil")
	}
}

func TestTeamConfig_BackwardCompatNames(t *testing.T) {
	// Old names without suffix should be accepted by validation
	// (for loading legacy workflow state).
	config := TeamConfig{
		Agents: []AgentConfig{
			{Name: "discovery", Provider: "claude", Role: "discovery", Count: 1},
			{Name: "drafter", Provider: "claude", Role: "drafter", Count: 1},
			{Name: "reviser", Provider: "claude", Role: "reviser", Count: 1},
			{Name: "reviewer-clarity", Provider: "claude", Role: "reviewer", Count: 1},
			{Name: "reviewer-consistency", Provider: "claude", Role: "reviewer", Count: 1},
			{Name: "reviewer-security", Provider: "claude", Role: "reviewer", Count: 1},
			{Name: "reviewer-correctness", Provider: "claude", Role: "reviewer", Count: 1},
			{Name: "judge", Provider: "claude", Role: "judge", Count: 1},
		},
	}
	if err := ValidateTeamConfig(config); err != nil {
		t.Fatalf("legacy names should be accepted, got error: %v", err)
	}
}
