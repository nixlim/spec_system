package codedoc

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// DefaultCodedocConfig tests
// ---------------------------------------------------------------------------

func TestConfigDefaultValues(t *testing.T) {
	cfg := DefaultCodedocConfig()

	checks := []struct {
		name string
		got  interface{}
		want interface{}
	}{
		{"MaxRounds", cfg.MaxRounds, 3},
		{"MinRounds", cfg.MinRounds, 1},
		{"MaxCostUSD", cfg.MaxCostUSD, 50.0},
		{"MaxWallClockMinutes", cfg.MaxWallClockMinutes, 90},
		{"MaxRetries", cfg.MaxRetries, 2},
		{"MaxGateCorrections", cfg.MaxGateCorrections, 3},
		{"MaxGateDraftRedrafts", cfg.MaxGateDraftRedrafts, 2},
		{"StalenessThreshold", cfg.StalenessThreshold, 2},
		{"AgentTimeoutSeconds", cfg.AgentTimeoutSeconds, 600},
		{"DiscoveryTimeoutSeconds", cfg.DiscoveryTimeoutSeconds, 1200},
		{"ReviewerTimeoutSeconds", cfg.ReviewerTimeoutSeconds, 300},
		{"DefaultMode", cfg.DefaultMode, "full"},
		{"BackupBeforeWrite", cfg.BackupBeforeWrite, true},
		{"DocsOutputDir", cfg.DocsOutputDir, "docs"},
		{"DriftWarningThreshold", cfg.DriftWarningThreshold, 0.20},
		{"WriteLockTimeoutSeconds", cfg.WriteLockTimeoutSeconds, 300},
	}

	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestConfigDefaultValidates(t *testing.T) {
	cfg := DefaultCodedocConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("DefaultCodedocConfig().Validate() should pass: %v", err)
	}
}

// ---------------------------------------------------------------------------
// ParseCodedocConfig tests
// ---------------------------------------------------------------------------

func TestConfigPartialYAMLOverride(t *testing.T) {
	yamlData := []byte(`max_rounds: 5`)
	cfg, err := ParseCodedocConfig(yamlData)
	if err != nil {
		t.Fatalf("ParseCodedocConfig: %v", err)
	}
	if cfg.MaxRounds != 5 {
		t.Errorf("MaxRounds: got %d, want 5", cfg.MaxRounds)
	}
	// Non-overridden fields retain defaults.
	if cfg.MinRounds != 1 {
		t.Errorf("MinRounds (default): got %d, want 1", cfg.MinRounds)
	}
	if cfg.MaxCostUSD != 50.0 {
		t.Errorf("MaxCostUSD (default): got %f, want 50.0", cfg.MaxCostUSD)
	}
	if cfg.MaxWallClockMinutes != 90 {
		t.Errorf("MaxWallClockMinutes (default): got %d, want 90", cfg.MaxWallClockMinutes)
	}
}

func TestConfigFullYAMLParsing(t *testing.T) {
	yamlData := []byte(`
max_rounds: 10
min_rounds: 3
max_cost_usd: 200.0
max_wall_clock_minutes: 180
max_retries: 5
max_gate_corrections: 10
max_gate_draft_redrafts: 5
staleness_threshold: 4
agent_timeout_seconds: 1200
discovery_timeout_seconds: 2400
reviewer_timeout_seconds: 600
enable_codex_codedoc_discovery: true
enable_codex_codedoc_drafting: true
enable_codex_reviewers: false
codex_model: "gpt-6"
default_mode: "incremental"
backup_before_write: false
docs_output_dir: "documentation"
drift_warning_threshold: 0.50
write_lock_timeout_seconds: 600
`)
	cfg, err := ParseCodedocConfig(yamlData)
	if err != nil {
		t.Fatalf("ParseCodedocConfig: %v", err)
	}

	if cfg.MaxRounds != 10 {
		t.Errorf("MaxRounds: got %d, want 10", cfg.MaxRounds)
	}
	if cfg.MinRounds != 3 {
		t.Errorf("MinRounds: got %d, want 3", cfg.MinRounds)
	}
	if cfg.MaxCostUSD != 200.0 {
		t.Errorf("MaxCostUSD: got %f, want 200.0", cfg.MaxCostUSD)
	}
	if cfg.MaxWallClockMinutes != 180 {
		t.Errorf("MaxWallClockMinutes: got %d, want 180", cfg.MaxWallClockMinutes)
	}
	if cfg.MaxRetries != 5 {
		t.Errorf("MaxRetries: got %d, want 5", cfg.MaxRetries)
	}
	if cfg.MaxGateCorrections != 10 {
		t.Errorf("MaxGateCorrections: got %d, want 10", cfg.MaxGateCorrections)
	}
	if cfg.MaxGateDraftRedrafts != 5 {
		t.Errorf("MaxGateDraftRedrafts: got %d, want 5", cfg.MaxGateDraftRedrafts)
	}
	if cfg.StalenessThreshold != 4 {
		t.Errorf("StalenessThreshold: got %d, want 4", cfg.StalenessThreshold)
	}
	if cfg.AgentTimeoutSeconds != 1200 {
		t.Errorf("AgentTimeoutSeconds: got %d, want 1200", cfg.AgentTimeoutSeconds)
	}
	if cfg.DiscoveryTimeoutSeconds != 2400 {
		t.Errorf("DiscoveryTimeoutSeconds: got %d, want 2400", cfg.DiscoveryTimeoutSeconds)
	}
	if cfg.ReviewerTimeoutSeconds != 600 {
		t.Errorf("ReviewerTimeoutSeconds: got %d, want 600", cfg.ReviewerTimeoutSeconds)
	}
	if !cfg.EnableCodexCodedocDiscovery {
		t.Error("EnableCodexCodedocDiscovery: got false, want true")
	}
	if !cfg.EnableCodexCodedocDrafting {
		t.Error("EnableCodexCodedocDrafting: got false, want true")
	}
	if cfg.EnableCodexReviewers {
		t.Error("EnableCodexReviewers: got true, want false")
	}
	if cfg.CodexModel != "gpt-6" {
		t.Errorf("CodexModel: got %q, want gpt-6", cfg.CodexModel)
	}
	if cfg.DefaultMode != "incremental" {
		t.Errorf("DefaultMode: got %q, want incremental", cfg.DefaultMode)
	}
	if cfg.BackupBeforeWrite {
		t.Error("BackupBeforeWrite: got true, want false")
	}
	if cfg.DocsOutputDir != "documentation" {
		t.Errorf("DocsOutputDir: got %q, want documentation", cfg.DocsOutputDir)
	}
	if cfg.DriftWarningThreshold != 0.50 {
		t.Errorf("DriftWarningThreshold: got %f, want 0.50", cfg.DriftWarningThreshold)
	}
	if cfg.WriteLockTimeoutSeconds != 600 {
		t.Errorf("WriteLockTimeoutSeconds: got %d, want 600", cfg.WriteLockTimeoutSeconds)
	}
}

func TestConfigInvalidYAML(t *testing.T) {
	yamlData := []byte(`{invalid yaml:::`)
	_, err := ParseCodedocConfig(yamlData)
	if err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
}

// ---------------------------------------------------------------------------
// Validation tests
// ---------------------------------------------------------------------------

func TestConfigValidateMaxRoundsZero(t *testing.T) {
	_, err := ParseCodedocConfig([]byte(`max_rounds: 0`))
	if err == nil {
		t.Fatal("expected error for max_rounds=0")
	}
	if !strings.Contains(err.Error(), "must be >= 1") {
		t.Errorf("error should contain 'must be >= 1': %v", err)
	}
}

func TestConfigValidateMinRoundsGreaterThanMaxRounds(t *testing.T) {
	_, err := ParseCodedocConfig([]byte("min_rounds: 5\nmax_rounds: 3"))
	if err == nil {
		t.Fatal("expected error for min_rounds > max_rounds")
	}
	if !strings.Contains(err.Error(), "must be <= max_rounds") {
		t.Errorf("error should contain 'must be <= max_rounds': %v", err)
	}
}

func TestConfigValidateDriftWarningThresholdTooHigh(t *testing.T) {
	_, err := ParseCodedocConfig([]byte(`drift_warning_threshold: 1.5`))
	if err == nil {
		t.Fatal("expected error for drift_warning_threshold=1.5")
	}
	if !strings.Contains(err.Error(), "must be >= 0.0 and <= 1.0") {
		t.Errorf("error should contain 'must be >= 0.0 and <= 1.0': %v", err)
	}
}

func TestConfigValidateAgentTimeoutZero(t *testing.T) {
	_, err := ParseCodedocConfig([]byte(`agent_timeout_seconds: 0`))
	if err == nil {
		t.Fatal("expected error for agent_timeout_seconds=0")
	}
	if !strings.Contains(err.Error(), "must be > 0") {
		t.Errorf("error should contain 'must be > 0': %v", err)
	}
}

func TestConfigValidateDiscoveryTimeoutZero(t *testing.T) {
	_, err := ParseCodedocConfig([]byte(`discovery_timeout_seconds: 0`))
	if err == nil {
		t.Fatal("expected error for discovery_timeout_seconds=0")
	}
	if !strings.Contains(err.Error(), "must be > 0") {
		t.Errorf("error should contain 'must be > 0': %v", err)
	}
}

func TestConfigValidateReviewerTimeoutZero(t *testing.T) {
	_, err := ParseCodedocConfig([]byte(`reviewer_timeout_seconds: 0`))
	if err == nil {
		t.Fatal("expected error for reviewer_timeout_seconds=0")
	}
	if !strings.Contains(err.Error(), "must be > 0") {
		t.Errorf("error should contain 'must be > 0': %v", err)
	}
}

func TestConfigValidateMaxCostZero(t *testing.T) {
	_, err := ParseCodedocConfig([]byte(`max_cost_usd: 0`))
	if err == nil {
		t.Fatal("expected error for max_cost_usd=0")
	}
	if !strings.Contains(err.Error(), "must be > 0") {
		t.Errorf("error should contain 'must be > 0': %v", err)
	}
}

func TestConfigValidateMaxWallClockZero(t *testing.T) {
	_, err := ParseCodedocConfig([]byte(`max_wall_clock_minutes: 0`))
	if err == nil {
		t.Fatal("expected error for max_wall_clock_minutes=0")
	}
	if !strings.Contains(err.Error(), "must be > 0") {
		t.Errorf("error should contain 'must be > 0': %v", err)
	}
}

func TestConfigValidateWriteLockTimeoutZero(t *testing.T) {
	_, err := ParseCodedocConfig([]byte(`write_lock_timeout_seconds: 0`))
	if err == nil {
		t.Fatal("expected error for write_lock_timeout_seconds=0")
	}
	if !strings.Contains(err.Error(), "must be > 0") {
		t.Errorf("error should contain 'must be > 0': %v", err)
	}
}

func TestConfigValidateMaxRetriesNegative(t *testing.T) {
	_, err := ParseCodedocConfig([]byte(`max_retries: -1`))
	if err == nil {
		t.Fatal("expected error for max_retries=-1")
	}
	if !strings.Contains(err.Error(), "max_retries") {
		t.Errorf("error should mention max_retries: %v", err)
	}
}

func TestConfigValidateInvalidMode(t *testing.T) {
	_, err := ParseCodedocConfig([]byte(`default_mode: "partial"`))
	if err == nil {
		t.Fatal("expected error for invalid default_mode")
	}
	if !strings.Contains(err.Error(), "default_mode") {
		t.Errorf("error should mention default_mode: %v", err)
	}
}

func TestConfigValidateDriftThresholdNegative(t *testing.T) {
	_, err := ParseCodedocConfig([]byte(`drift_warning_threshold: -0.1`))
	if err == nil {
		t.Fatal("expected error for negative drift_warning_threshold")
	}
	if !strings.Contains(err.Error(), "drift_warning_threshold") {
		t.Errorf("error should mention drift_warning_threshold: %v", err)
	}
}
