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
	if cfg.MaxTotalFindings != 60 {
		t.Errorf("MaxTotalFindings: got %d, want 60", cfg.MaxTotalFindings)
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
	if cfg.MaxTotalFindings != 60 {
		t.Errorf("MaxTotalFindings (default): got %d, want 60", cfg.MaxTotalFindings)
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

func TestConfigValidateDefaultsPass(t *testing.T) {
	cfg := DefaultConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("DefaultConfig().Validate() should pass: %v", err)
	}
}
