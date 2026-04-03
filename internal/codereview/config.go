package codereview

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// CodeReviewConfig holds tunable parameters that govern the automated code
// review workflow, including round limits, cost budgets, and timeout values.
// It is intended to be loaded from a YAML configuration file under the
// `code_review` key and merged over sensible defaults via ParseCodeReviewConfig.
type CodeReviewConfig struct {
	// MaxRounds is the maximum number of re-review rounds after the initial review.
	// Guard blocks when round > MaxRounds. 0 = no re-reviews allowed.
	// Default: 3
	MaxRounds int `yaml:"max_rounds"`

	// MaxCostUSD is the cumulative cost budget in USD. Workflow escalates when exceeded.
	// Default: 50.0
	MaxCostUSD float64 `yaml:"max_cost_usd"`

	// MaxWallClockMinutes is the maximum wall-clock time in minutes.
	// Default: 120
	MaxWallClockMinutes int `yaml:"max_wall_clock_minutes"`

	// FixerTimeoutSeconds is the timeout for the fix agent subprocess.
	// After this duration: SIGTERM, wait 5s, SIGKILL.
	// Default: 600
	FixerTimeoutSeconds int `yaml:"fixer_timeout_seconds"`

	// CommitMode controls how fix branches are created.
	// Valid values: "branch_per_round" (default), "direct_commit".
	CommitMode string `yaml:"commit_mode"`

	// StalenessThreshold is the number of consecutive rounds with no improvement
	// (open CRITICAL+MAJOR count >= previous round) before staleness is triggered.
	// Default: 2
	StalenessThreshold int `yaml:"staleness_threshold"`

	// MaxRetries is the maximum number of retries for reviewer agent failures.
	// Default: 2
	MaxRetries int `yaml:"max_retries"`

	// ReviewerTimeoutSeconds is the timeout for each reviewer agent subprocess.
	// Default: 300
	ReviewerTimeoutSeconds int `yaml:"reviewer_timeout_seconds"`
}

// validCommitModes is the set of allowed CommitMode values.
var validCommitModes = map[string]bool{
	"branch_per_round": true,
	"direct_commit":    true,
}

// DefaultCodeReviewConfig returns a CodeReviewConfig populated with sensible
// default values suitable for most code review workflows.
func DefaultCodeReviewConfig() CodeReviewConfig {
	return CodeReviewConfig{
		MaxRounds:              3,
		MaxCostUSD:             50.0,
		MaxWallClockMinutes:    120,
		FixerTimeoutSeconds:    600,
		CommitMode:             "branch_per_round",
		StalenessThreshold:     2,
		MaxRetries:             2,
		ReviewerTimeoutSeconds: 300,
	}
}

// ParseCodeReviewConfig parses YAML configuration data and returns a validated
// CodeReviewConfig. It starts from DefaultCodeReviewConfig and overlays only
// the fields present in data, so callers may provide partial YAML to override
// specific defaults. Returns the first validation error encountered, if any.
func ParseCodeReviewConfig(data []byte) (*CodeReviewConfig, error) {
	cfg := DefaultCodeReviewConfig()
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing code review config YAML: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Validate checks the CodeReviewConfig for internal consistency and returns
// an error describing the first violated constraint.
func (c *CodeReviewConfig) Validate() error {
	if c.MaxRounds < 0 {
		return fmt.Errorf("max_rounds must be >= 0, got %d", c.MaxRounds)
	}
	if c.MaxCostUSD < 0 {
		return fmt.Errorf("max_cost_usd must be >= 0, got %g", c.MaxCostUSD)
	}
	if c.MaxWallClockMinutes < 0 {
		return fmt.Errorf("max_wall_clock_minutes must be >= 0, got %d", c.MaxWallClockMinutes)
	}
	if c.FixerTimeoutSeconds < 0 {
		return fmt.Errorf("fixer_timeout_seconds must be >= 0, got %d", c.FixerTimeoutSeconds)
	}
	if !validCommitModes[c.CommitMode] {
		return fmt.Errorf("commit_mode must be one of %q or %q, got %q", "branch_per_round", "direct_commit", c.CommitMode)
	}
	if c.StalenessThreshold < 0 {
		return fmt.Errorf("staleness_threshold must be >= 0, got %d", c.StalenessThreshold)
	}
	if c.MaxRetries < 0 {
		return fmt.Errorf("max_retries must be >= 0, got %d", c.MaxRetries)
	}
	if c.ReviewerTimeoutSeconds < 0 {
		return fmt.Errorf("reviewer_timeout_seconds must be >= 0, got %d", c.ReviewerTimeoutSeconds)
	}
	return nil
}
