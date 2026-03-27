package specworkflow

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// DefaultConfig tests
// ---------------------------------------------------------------------------

func TestConfigDefaultValues(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.MaxRounds != 5 {
		t.Errorf("MaxRounds: got %d, want 5", cfg.MaxRounds)
	}
	if cfg.MinRounds != 2 {
		t.Errorf("MinRounds: got %d, want 2", cfg.MinRounds)
	}
	if cfg.MaxTotalFindings != 200 {
		t.Errorf("MaxTotalFindings: got %d, want 200", cfg.MaxTotalFindings)
	}
	if cfg.StalenessThreshold != 2 {
		t.Errorf("StalenessThreshold: got %d, want 2", cfg.StalenessThreshold)
	}
	if cfg.MaxWallClockMinutes != 60 {
		t.Errorf("MaxWallClockMinutes: got %d, want 60", cfg.MaxWallClockMinutes)
	}
	if cfg.MaxCostUSD != 50.0 {
		t.Errorf("MaxCostUSD: got %f, want 50.0", cfg.MaxCostUSD)
	}
	if cfg.MaxGateCorrections != 3 {
		t.Errorf("MaxGateCorrections: got %d, want 3", cfg.MaxGateCorrections)
	}
	if cfg.MaxGate2Redrafts != 1 {
		t.Errorf("MaxGate2Redrafts: got %d, want 1", cfg.MaxGate2Redrafts)
	}
	if cfg.MaxRetries != 2 {
		t.Errorf("MaxRetries: got %d, want 2", cfg.MaxRetries)
	}
	if cfg.EnableCodexReviewers != true {
		t.Errorf("EnableCodexReviewers: got %v, want true", cfg.EnableCodexReviewers)
	}
	if cfg.CodexModel != "gpt-5.4" {
		t.Errorf("CodexModel: got %q, want gpt-5.4", cfg.CodexModel)
	}
	if cfg.ReviewerTimeoutSeconds != 300 {
		t.Errorf("ReviewerTimeoutSeconds: got %d, want 300", cfg.ReviewerTimeoutSeconds)
	}
	if cfg.HoldoutTimeoutSeconds != 300 {
		t.Errorf("HoldoutTimeoutSeconds: got %d, want 300", cfg.HoldoutTimeoutSeconds)
	}
	if cfg.EnableCodexDiscovery != false {
		t.Errorf("EnableCodexDiscovery: got %v, want false", cfg.EnableCodexDiscovery)
	}
	if cfg.EnableCodexDrafting != false {
		t.Errorf("EnableCodexDrafting: got %v, want false", cfg.EnableCodexDrafting)
	}
	if cfg.AgentTimeoutSeconds != 300 {
		t.Errorf("AgentTimeoutSeconds: got %d, want 300", cfg.AgentTimeoutSeconds)
	}
	if cfg.TaskifyMaxRetries != 3 {
		t.Errorf("TaskifyMaxRetries: got %d, want 3", cfg.TaskifyMaxRetries)
	}
	if cfg.TaskReviewMaxRounds != 3 {
		t.Errorf("TaskReviewMaxRounds: got %d, want 3", cfg.TaskReviewMaxRounds)
	}
	if cfg.SkillPaths.PlanSpec != "" {
		t.Errorf("SkillPaths.PlanSpec: got %q, want empty", cfg.SkillPaths.PlanSpec)
	}
	if cfg.SkillPaths.GrillSpec != "" {
		t.Errorf("SkillPaths.GrillSpec: got %q, want empty", cfg.SkillPaths.GrillSpec)
	}
}

// ---------------------------------------------------------------------------
// ParseConfig tests
// ---------------------------------------------------------------------------

func TestConfigPartialYAMLOverride(t *testing.T) {
	yaml := []byte(`
max_rounds: 10
max_cost_usd: 25.0
`)
	cfg, err := ParseConfig(yaml)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}

	// Overridden fields.
	if cfg.MaxRounds != 10 {
		t.Errorf("MaxRounds: got %d, want 10", cfg.MaxRounds)
	}
	if cfg.MaxCostUSD != 25.0 {
		t.Errorf("MaxCostUSD: got %f, want 25.0", cfg.MaxCostUSD)
	}

	// Fields not in YAML should retain defaults.
	if cfg.MinRounds != 2 {
		t.Errorf("MinRounds (default): got %d, want 2", cfg.MinRounds)
	}
	if cfg.MaxTotalFindings != 200 {
		t.Errorf("MaxTotalFindings (default): got %d, want 200", cfg.MaxTotalFindings)
	}
	if cfg.StalenessThreshold != 2 {
		t.Errorf("StalenessThreshold (default): got %d, want 2", cfg.StalenessThreshold)
	}
	if cfg.MaxWallClockMinutes != 60 {
		t.Errorf("MaxWallClockMinutes (default): got %d, want 60", cfg.MaxWallClockMinutes)
	}
	if cfg.MaxGateCorrections != 3 {
		t.Errorf("MaxGateCorrections (default): got %d, want 3", cfg.MaxGateCorrections)
	}
	if cfg.MaxGate2Redrafts != 1 {
		t.Errorf("MaxGate2Redrafts (default): got %d, want 1", cfg.MaxGate2Redrafts)
	}
	if cfg.MaxRetries != 2 {
		t.Errorf("MaxRetries (default): got %d, want 2", cfg.MaxRetries)
	}
	if cfg.EnableCodexReviewers != true {
		t.Errorf("EnableCodexReviewers (default): got %v, want true", cfg.EnableCodexReviewers)
	}
	if cfg.CodexModel != "gpt-5.4" {
		t.Errorf("CodexModel (default): got %q, want gpt-5.4", cfg.CodexModel)
	}
	if cfg.ReviewerTimeoutSeconds != 300 {
		t.Errorf("ReviewerTimeoutSeconds (default): got %d, want 300", cfg.ReviewerTimeoutSeconds)
	}
	if cfg.HoldoutTimeoutSeconds != 300 {
		t.Errorf("HoldoutTimeoutSeconds (default): got %d, want 300", cfg.HoldoutTimeoutSeconds)
	}
	if cfg.EnableCodexDiscovery != false {
		t.Errorf("EnableCodexDiscovery (default): got %v, want false", cfg.EnableCodexDiscovery)
	}
	if cfg.EnableCodexDrafting != false {
		t.Errorf("EnableCodexDrafting (default): got %v, want false", cfg.EnableCodexDrafting)
	}
	if cfg.AgentTimeoutSeconds != 300 {
		t.Errorf("AgentTimeoutSeconds (default): got %d, want 300", cfg.AgentTimeoutSeconds)
	}
	if cfg.TaskifyMaxRetries != 3 {
		t.Errorf("TaskifyMaxRetries (default): got %d, want 3", cfg.TaskifyMaxRetries)
	}
	if cfg.TaskReviewMaxRounds != 3 {
		t.Errorf("TaskReviewMaxRounds (default): got %d, want 3", cfg.TaskReviewMaxRounds)
	}
}

func TestConfigFullYAMLParsing(t *testing.T) {
	yaml := []byte(`
max_rounds: 8
min_rounds: 3
max_total_findings: 100
staleness_threshold: 4
max_wall_clock_minutes: 120
max_cost_usd: 75.5
max_gate_corrections: 5
max_gate2_redrafts: 3
max_retries: 4
enable_codex_reviewers: false
codex_model: o3
reviewer_timeout_seconds: 600
holdout_timeout_seconds: 120
enable_codex_discovery: true
enable_codex_drafting: true
agent_timeout_seconds: 600
taskify_max_retries: 5
task_review_max_rounds: 4
skill_paths:
  plan_spec: /tmp
  grill_spec: /tmp
`)
	cfg, err := ParseConfig(yaml)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}

	if cfg.MaxRounds != 8 {
		t.Errorf("MaxRounds: got %d, want 8", cfg.MaxRounds)
	}
	if cfg.MinRounds != 3 {
		t.Errorf("MinRounds: got %d, want 3", cfg.MinRounds)
	}
	if cfg.MaxTotalFindings != 100 {
		t.Errorf("MaxTotalFindings: got %d, want 100", cfg.MaxTotalFindings)
	}
	if cfg.StalenessThreshold != 4 {
		t.Errorf("StalenessThreshold: got %d, want 4", cfg.StalenessThreshold)
	}
	if cfg.MaxWallClockMinutes != 120 {
		t.Errorf("MaxWallClockMinutes: got %d, want 120", cfg.MaxWallClockMinutes)
	}
	if cfg.MaxCostUSD != 75.5 {
		t.Errorf("MaxCostUSD: got %f, want 75.5", cfg.MaxCostUSD)
	}
	if cfg.MaxGateCorrections != 5 {
		t.Errorf("MaxGateCorrections: got %d, want 5", cfg.MaxGateCorrections)
	}
	if cfg.MaxGate2Redrafts != 3 {
		t.Errorf("MaxGate2Redrafts: got %d, want 3", cfg.MaxGate2Redrafts)
	}
	if cfg.MaxRetries != 4 {
		t.Errorf("MaxRetries: got %d, want 4", cfg.MaxRetries)
	}
	if cfg.EnableCodexReviewers != false {
		t.Errorf("EnableCodexReviewers: got %v, want false", cfg.EnableCodexReviewers)
	}
	if cfg.CodexModel != "o3" {
		t.Errorf("CodexModel: got %q, want o3", cfg.CodexModel)
	}
	if cfg.ReviewerTimeoutSeconds != 600 {
		t.Errorf("ReviewerTimeoutSeconds: got %d, want 600", cfg.ReviewerTimeoutSeconds)
	}
	if cfg.HoldoutTimeoutSeconds != 120 {
		t.Errorf("HoldoutTimeoutSeconds: got %d, want 120", cfg.HoldoutTimeoutSeconds)
	}
	if cfg.EnableCodexDiscovery != true {
		t.Errorf("EnableCodexDiscovery: got %v, want true", cfg.EnableCodexDiscovery)
	}
	if cfg.EnableCodexDrafting != true {
		t.Errorf("EnableCodexDrafting: got %v, want true", cfg.EnableCodexDrafting)
	}
	if cfg.AgentTimeoutSeconds != 600 {
		t.Errorf("AgentTimeoutSeconds: got %d, want 600", cfg.AgentTimeoutSeconds)
	}
	if cfg.TaskifyMaxRetries != 5 {
		t.Errorf("TaskifyMaxRetries: got %d, want 5", cfg.TaskifyMaxRetries)
	}
	if cfg.TaskReviewMaxRounds != 4 {
		t.Errorf("TaskReviewMaxRounds: got %d, want 4", cfg.TaskReviewMaxRounds)
	}
	if cfg.SkillPaths.PlanSpec != "/tmp" {
		t.Errorf("SkillPaths.PlanSpec: got %q, want /tmp", cfg.SkillPaths.PlanSpec)
	}
	if cfg.SkillPaths.GrillSpec != "/tmp" {
		t.Errorf("SkillPaths.GrillSpec: got %q, want /tmp", cfg.SkillPaths.GrillSpec)
	}
}

// ---------------------------------------------------------------------------
// Validation tests
// ---------------------------------------------------------------------------

func TestConfigValidateMinExceedsMax(t *testing.T) {
	yaml := []byte(`
min_rounds: 10
max_rounds: 3
`)
	_, err := ParseConfig(yaml)
	if err == nil {
		t.Fatal("expected error when min_rounds > max_rounds, got nil")
	}
	if !strings.Contains(err.Error(), "min_rounds") {
		t.Errorf("error should mention min_rounds: %v", err)
	}
}

func TestConfigValidateMaxCostZero(t *testing.T) {
	yaml := []byte(`max_cost_usd: 0`)
	_, err := ParseConfig(yaml)
	if err == nil {
		t.Fatal("expected error when max_cost_usd is 0, got nil")
	}
	if !strings.Contains(err.Error(), "max_cost_usd") {
		t.Errorf("error should mention max_cost_usd: %v", err)
	}
}

func TestConfigValidateMaxCostNegative(t *testing.T) {
	yaml := []byte(`max_cost_usd: -5.0`)
	_, err := ParseConfig(yaml)
	if err == nil {
		t.Fatal("expected error when max_cost_usd is negative, got nil")
	}
	if !strings.Contains(err.Error(), "max_cost_usd") {
		t.Errorf("error should mention max_cost_usd: %v", err)
	}
}

func TestConfigValidateMaxRoundsZero(t *testing.T) {
	yaml := []byte(`max_rounds: 0`)
	_, err := ParseConfig(yaml)
	if err == nil {
		t.Fatal("expected error when max_rounds is 0, got nil")
	}
	if !strings.Contains(err.Error(), "max_rounds") {
		t.Errorf("error should mention max_rounds: %v", err)
	}
}

func TestConfigValidateMaxTotalFindingsZero(t *testing.T) {
	yaml := []byte(`max_total_findings: 0`)
	_, err := ParseConfig(yaml)
	if err == nil {
		t.Fatal("expected error when max_total_findings is 0, got nil")
	}
	if !strings.Contains(err.Error(), "max_total_findings") {
		t.Errorf("error should mention max_total_findings: %v", err)
	}
}

func TestConfigValidateSkillPathNonExistent(t *testing.T) {
	yaml := []byte(`
skill_paths:
  plan_spec: /nonexistent/path/that/does/not/exist
`)
	_, err := ParseConfig(yaml)
	if err == nil {
		t.Fatal("expected error for non-existent skill path, got nil")
	}
	if !strings.Contains(err.Error(), "skill_paths.plan_spec") {
		t.Errorf("error should mention skill_paths.plan_spec: %v", err)
	}
}

func TestConfigValidateGrillSpecPathNonExistent(t *testing.T) {
	yaml := []byte(`
skill_paths:
  grill_spec: /nonexistent/path/that/does/not/exist
`)
	_, err := ParseConfig(yaml)
	if err == nil {
		t.Fatal("expected error for non-existent grill_spec path, got nil")
	}
	if !strings.Contains(err.Error(), "skill_paths.grill_spec") {
		t.Errorf("error should mention skill_paths.grill_spec: %v", err)
	}
}

func TestConfigCodexDisabled(t *testing.T) {
	yaml := []byte(`enable_codex_reviewers: false`)
	cfg, err := ParseConfig(yaml)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.EnableCodexReviewers != false {
		t.Errorf("EnableCodexReviewers: got %v, want false", cfg.EnableCodexReviewers)
	}
}

func TestConfigValidateReviewerTimeoutZero(t *testing.T) {
	yaml := []byte(`reviewer_timeout_seconds: 0`)
	_, err := ParseConfig(yaml)
	if err == nil {
		t.Fatal("expected error when reviewer_timeout_seconds is 0, got nil")
	}
	if !strings.Contains(err.Error(), "reviewer_timeout_seconds") {
		t.Errorf("error should mention reviewer_timeout_seconds: %v", err)
	}
}

func TestConfigValidateAgentTimeoutZero(t *testing.T) {
	yaml := []byte(`agent_timeout_seconds: 0`)
	_, err := ParseConfig(yaml)
	if err == nil {
		t.Fatal("expected error when agent_timeout_seconds is 0, got nil")
	}
	if !strings.Contains(err.Error(), "agent_timeout_seconds") {
		t.Errorf("error should mention agent_timeout_seconds: %v", err)
	}
}

func TestConfigValidateAgentTimeoutNegative(t *testing.T) {
	yaml := []byte(`agent_timeout_seconds: -10`)
	_, err := ParseConfig(yaml)
	if err == nil {
		t.Fatal("expected error when agent_timeout_seconds is negative, got nil")
	}
	if !strings.Contains(err.Error(), "agent_timeout_seconds") {
		t.Errorf("error should mention agent_timeout_seconds: %v", err)
	}
}

func TestConfigValidateTaskifyMaxRetriesZero(t *testing.T) {
	yaml := []byte(`taskify_max_retries: 0`)
	_, err := ParseConfig(yaml)
	if err == nil {
		t.Fatal("expected error when taskify_max_retries is 0, got nil")
	}
	if !strings.Contains(err.Error(), "taskify_max_retries") {
		t.Errorf("error should mention taskify_max_retries: %v", err)
	}
}

func TestConfigValidateTaskifyMaxRetriesNegative(t *testing.T) {
	yaml := []byte(`taskify_max_retries: -1`)
	_, err := ParseConfig(yaml)
	if err == nil {
		t.Fatal("expected error when taskify_max_retries is negative, got nil")
	}
	if !strings.Contains(err.Error(), "taskify_max_retries") {
		t.Errorf("error should mention taskify_max_retries: %v", err)
	}
}

func TestConfigValidateTaskReviewMaxRoundsZero(t *testing.T) {
	yaml := []byte(`task_review_max_rounds: 0`)
	_, err := ParseConfig(yaml)
	if err == nil {
		t.Fatal("expected error when task_review_max_rounds is 0, got nil")
	}
	if !strings.Contains(err.Error(), "task_review_max_rounds") {
		t.Errorf("error should mention task_review_max_rounds: %v", err)
	}
}

func TestConfigValidateTaskReviewMaxRoundsNegative(t *testing.T) {
	yaml := []byte(`task_review_max_rounds: -2`)
	_, err := ParseConfig(yaml)
	if err == nil {
		t.Fatal("expected error when task_review_max_rounds is negative, got nil")
	}
	if !strings.Contains(err.Error(), "task_review_max_rounds") {
		t.Errorf("error should mention task_review_max_rounds: %v", err)
	}
}

func TestConfigValidateDefaultsPass(t *testing.T) {
	cfg := DefaultConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("DefaultConfig().Validate() should pass: %v", err)
	}
}
