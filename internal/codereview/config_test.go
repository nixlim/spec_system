package codereview

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// DefaultCodeReviewConfig tests
// ---------------------------------------------------------------------------

func TestDefaultCodeReviewConfigValues(t *testing.T) {
	cfg := DefaultCodeReviewConfig()

	if cfg.MaxRounds != 3 {
		t.Errorf("MaxRounds: got %d, want 3", cfg.MaxRounds)
	}
	if cfg.MaxCostUSD != 50.0 {
		t.Errorf("MaxCostUSD: got %f, want 50.0", cfg.MaxCostUSD)
	}
	if cfg.MaxWallClockMinutes != 120 {
		t.Errorf("MaxWallClockMinutes: got %d, want 120", cfg.MaxWallClockMinutes)
	}
	if cfg.FixerTimeoutSeconds != 600 {
		t.Errorf("FixerTimeoutSeconds: got %d, want 600", cfg.FixerTimeoutSeconds)
	}
	if cfg.CommitMode != "branch_per_round" {
		t.Errorf("CommitMode: got %q, want branch_per_round", cfg.CommitMode)
	}
	if cfg.StalenessThreshold != 2 {
		t.Errorf("StalenessThreshold: got %d, want 2", cfg.StalenessThreshold)
	}
	if cfg.MaxRetries != 2 {
		t.Errorf("MaxRetries: got %d, want 2", cfg.MaxRetries)
	}
	if cfg.ReviewerTimeoutSeconds != 300 {
		t.Errorf("ReviewerTimeoutSeconds: got %d, want 300", cfg.ReviewerTimeoutSeconds)
	}
}

func TestDefaultCodeReviewConfigValidates(t *testing.T) {
	cfg := DefaultCodeReviewConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("DefaultCodeReviewConfig().Validate() should pass: %v", err)
	}
}

// ---------------------------------------------------------------------------
// ParseCodeReviewConfig tests
// ---------------------------------------------------------------------------

func TestCodeReviewConfigPartialYAMLOverride(t *testing.T) {
	yaml := []byte(`
max_rounds: 5
max_cost_usd: 100.0
`)
	cfg, err := ParseCodeReviewConfig(yaml)
	if err != nil {
		t.Fatalf("ParseCodeReviewConfig: %v", err)
	}

	// Overridden fields.
	if cfg.MaxRounds != 5 {
		t.Errorf("MaxRounds: got %d, want 5", cfg.MaxRounds)
	}
	if cfg.MaxCostUSD != 100.0 {
		t.Errorf("MaxCostUSD: got %f, want 100.0", cfg.MaxCostUSD)
	}

	// Fields not in YAML should retain defaults.
	if cfg.MaxWallClockMinutes != 120 {
		t.Errorf("MaxWallClockMinutes (default): got %d, want 120", cfg.MaxWallClockMinutes)
	}
	if cfg.FixerTimeoutSeconds != 600 {
		t.Errorf("FixerTimeoutSeconds (default): got %d, want 600", cfg.FixerTimeoutSeconds)
	}
	if cfg.CommitMode != "branch_per_round" {
		t.Errorf("CommitMode (default): got %q, want branch_per_round", cfg.CommitMode)
	}
	if cfg.StalenessThreshold != 2 {
		t.Errorf("StalenessThreshold (default): got %d, want 2", cfg.StalenessThreshold)
	}
	if cfg.MaxRetries != 2 {
		t.Errorf("MaxRetries (default): got %d, want 2", cfg.MaxRetries)
	}
	if cfg.ReviewerTimeoutSeconds != 300 {
		t.Errorf("ReviewerTimeoutSeconds (default): got %d, want 300", cfg.ReviewerTimeoutSeconds)
	}
}

func TestCodeReviewConfigFullYAMLParsing(t *testing.T) {
	yaml := []byte(`
max_rounds: 10
max_cost_usd: 200.0
max_wall_clock_minutes: 240
fixer_timeout_seconds: 1200
commit_mode: direct_commit
staleness_threshold: 3
max_retries: 5
reviewer_timeout_seconds: 600
`)
	cfg, err := ParseCodeReviewConfig(yaml)
	if err != nil {
		t.Fatalf("ParseCodeReviewConfig: %v", err)
	}

	if cfg.MaxRounds != 10 {
		t.Errorf("MaxRounds: got %d, want 10", cfg.MaxRounds)
	}
	if cfg.MaxCostUSD != 200.0 {
		t.Errorf("MaxCostUSD: got %f, want 200.0", cfg.MaxCostUSD)
	}
	if cfg.MaxWallClockMinutes != 240 {
		t.Errorf("MaxWallClockMinutes: got %d, want 240", cfg.MaxWallClockMinutes)
	}
	if cfg.FixerTimeoutSeconds != 1200 {
		t.Errorf("FixerTimeoutSeconds: got %d, want 1200", cfg.FixerTimeoutSeconds)
	}
	if cfg.CommitMode != "direct_commit" {
		t.Errorf("CommitMode: got %q, want direct_commit", cfg.CommitMode)
	}
	if cfg.StalenessThreshold != 3 {
		t.Errorf("StalenessThreshold: got %d, want 3", cfg.StalenessThreshold)
	}
	if cfg.MaxRetries != 5 {
		t.Errorf("MaxRetries: got %d, want 5", cfg.MaxRetries)
	}
	if cfg.ReviewerTimeoutSeconds != 600 {
		t.Errorf("ReviewerTimeoutSeconds: got %d, want 600", cfg.ReviewerTimeoutSeconds)
	}
}

func TestCodeReviewConfigMaxRoundsZeroAllowed(t *testing.T) {
	yaml := []byte(`max_rounds: 0`)
	cfg, err := ParseCodeReviewConfig(yaml)
	if err != nil {
		t.Fatalf("max_rounds=0 should be valid: %v", err)
	}
	if cfg.MaxRounds != 0 {
		t.Errorf("MaxRounds: got %d, want 0", cfg.MaxRounds)
	}
}

// ---------------------------------------------------------------------------
// Validation tests
// ---------------------------------------------------------------------------

func TestCodeReviewConfigValidateMaxRoundsNegative(t *testing.T) {
	yaml := []byte(`max_rounds: -1`)
	_, err := ParseCodeReviewConfig(yaml)
	if err == nil {
		t.Fatal("expected error when max_rounds is negative, got nil")
	}
	if !strings.Contains(err.Error(), "max_rounds") {
		t.Errorf("error should mention max_rounds: %v", err)
	}
}

func TestCodeReviewConfigValidateMaxCostZero(t *testing.T) {
	yaml := []byte(`max_cost_usd: 0`)
	_, err := ParseCodeReviewConfig(yaml)
	if err != nil {
		t.Fatalf("max_cost_usd=0 should be valid per spec (>= 0), got error: %v", err)
	}
}

func TestCodeReviewConfigValidateMaxCostNegative(t *testing.T) {
	yaml := []byte(`max_cost_usd: -5.0`)
	_, err := ParseCodeReviewConfig(yaml)
	if err == nil {
		t.Fatal("expected error when max_cost_usd is negative, got nil")
	}
	if !strings.Contains(err.Error(), "max_cost_usd") {
		t.Errorf("error should mention max_cost_usd: %v", err)
	}
}

func TestCodeReviewConfigValidateMaxWallClockZero(t *testing.T) {
	yaml := []byte(`max_wall_clock_minutes: 0`)
	_, err := ParseCodeReviewConfig(yaml)
	if err != nil {
		t.Fatalf("max_wall_clock_minutes=0 should be valid per spec (>= 0), got error: %v", err)
	}
}

func TestCodeReviewConfigValidateFixerTimeoutZero(t *testing.T) {
	yaml := []byte(`fixer_timeout_seconds: 0`)
	_, err := ParseCodeReviewConfig(yaml)
	if err != nil {
		t.Fatalf("fixer_timeout_seconds=0 should be valid per spec (>= 0), got error: %v", err)
	}
}

func TestCodeReviewConfigValidateFixerTimeoutNegative(t *testing.T) {
	yaml := []byte(`fixer_timeout_seconds: -10`)
	_, err := ParseCodeReviewConfig(yaml)
	if err == nil {
		t.Fatal("expected error when fixer_timeout_seconds is negative, got nil")
	}
	if !strings.Contains(err.Error(), "fixer_timeout_seconds") {
		t.Errorf("error should mention fixer_timeout_seconds: %v", err)
	}
}

func TestCodeReviewConfigValidateInvalidCommitMode(t *testing.T) {
	yaml := []byte(`commit_mode: squash`)
	_, err := ParseCodeReviewConfig(yaml)
	if err == nil {
		t.Fatal("expected error for invalid commit_mode, got nil")
	}
	if !strings.Contains(err.Error(), "commit_mode") {
		t.Errorf("error should mention commit_mode: %v", err)
	}
}

func TestCodeReviewConfigValidateCommitModeDirectCommit(t *testing.T) {
	yaml := []byte(`commit_mode: direct_commit`)
	cfg, err := ParseCodeReviewConfig(yaml)
	if err != nil {
		t.Fatalf("direct_commit should be valid: %v", err)
	}
	if cfg.CommitMode != "direct_commit" {
		t.Errorf("CommitMode: got %q, want direct_commit", cfg.CommitMode)
	}
}

func TestCodeReviewConfigValidateStalenessNegative(t *testing.T) {
	yaml := []byte(`staleness_threshold: -1`)
	_, err := ParseCodeReviewConfig(yaml)
	if err == nil {
		t.Fatal("expected error when staleness_threshold is negative, got nil")
	}
	if !strings.Contains(err.Error(), "staleness_threshold") {
		t.Errorf("error should mention staleness_threshold: %v", err)
	}
}

func TestCodeReviewConfigValidateMaxRetriesNegative(t *testing.T) {
	yaml := []byte(`max_retries: -1`)
	_, err := ParseCodeReviewConfig(yaml)
	if err == nil {
		t.Fatal("expected error when max_retries is negative, got nil")
	}
	if !strings.Contains(err.Error(), "max_retries") {
		t.Errorf("error should mention max_retries: %v", err)
	}
}

func TestCodeReviewConfigValidateReviewerTimeoutZero(t *testing.T) {
	yaml := []byte(`reviewer_timeout_seconds: 0`)
	_, err := ParseCodeReviewConfig(yaml)
	if err != nil {
		t.Fatalf("reviewer_timeout_seconds=0 should be valid per spec (>= 0), got error: %v", err)
	}
}

func TestCodeReviewConfigValidateReviewerTimeoutNegative(t *testing.T) {
	yaml := []byte(`reviewer_timeout_seconds: -10`)
	_, err := ParseCodeReviewConfig(yaml)
	if err == nil {
		t.Fatal("expected error when reviewer_timeout_seconds is negative, got nil")
	}
	if !strings.Contains(err.Error(), "reviewer_timeout_seconds") {
		t.Errorf("error should mention reviewer_timeout_seconds: %v", err)
	}
}

func TestCodeReviewConfigInvalidYAML(t *testing.T) {
	yaml := []byte(`{invalid yaml:::`)
	_, err := ParseCodeReviewConfig(yaml)
	if err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
}
