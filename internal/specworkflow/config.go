package specworkflow

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// SkillPaths
// ---------------------------------------------------------------------------

// SkillPaths holds filesystem paths to the skill directories used by the
// workflow orchestrator for spec drafting and adversarial review.
type SkillPaths struct {
	// PlanSpec is the path to the plan-spec skill directory.
	PlanSpec string `yaml:"plan_spec"`
	// GrillSpec is the path to the grill-spec skill directory.
	GrillSpec string `yaml:"grill_spec"`
}

// ---------------------------------------------------------------------------
// SpecWorkflowConfig
// ---------------------------------------------------------------------------

// SpecWorkflowConfig holds tunable parameters that govern the adversarial
// spec review workflow, including round limits, cost budgets, and skill
// directory locations. It is intended to be loaded from a YAML configuration
// file and merged over sensible defaults via ParseConfig.
type SpecWorkflowConfig struct {
	// MaxRounds is the maximum number of review/revise iterations allowed.
	MaxRounds int `yaml:"max_rounds"`
	// MinRounds is the minimum number of review/revise iterations required
	// before the workflow may accept a spec.
	MinRounds int `yaml:"min_rounds"`
	// MaxTotalFindings is the upper bound on total findings that can be
	// raised before the workflow escalates.
	MaxTotalFindings int `yaml:"max_total_findings"`
	// StalenessThreshold is the number of consecutive rounds with no new
	// findings before the workflow considers the review stale.
	StalenessThreshold int `yaml:"staleness_threshold"`
	// MaxWallClockMinutes is the maximum wall-clock time budget in minutes.
	MaxWallClockMinutes int `yaml:"max_wall_clock_minutes"`
	// MaxCostUSD is the maximum estimated cost in USD for the entire workflow.
	MaxCostUSD float64 `yaml:"max_cost_usd"`
	// MaxGateCorrections is the maximum number of human-gate corrections
	// before the workflow escalates.
	MaxGateCorrections int `yaml:"max_gate_corrections"`
	// MaxRetries is the maximum number of retry attempts for transient
	// failures in agent invocations.
	MaxRetries int `yaml:"max_retries"`
	// SkillPaths holds the filesystem paths to skill directories.
	SkillPaths SkillPaths `yaml:"skill_paths"`
}

// DefaultConfig returns a SpecWorkflowConfig populated with sensible default
// values suitable for most workflows.
func DefaultConfig() SpecWorkflowConfig {
	return SpecWorkflowConfig{
		MaxRounds:           5,
		MinRounds:           2,
		MaxTotalFindings:    60,
		StalenessThreshold:  2,
		MaxWallClockMinutes: 60,
		MaxCostUSD:          50.0,
		MaxGateCorrections:  3,
		MaxRetries:          2,
	}
}

// ParseConfig parses YAML configuration data and returns a validated
// SpecWorkflowConfig. It starts from DefaultConfig and overlays only the
// fields present in data, so callers may provide partial YAML to override
// specific defaults. Returns the first validation error encountered, if any.
func ParseConfig(data []byte) (*SpecWorkflowConfig, error) {
	cfg := DefaultConfig()
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config YAML: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Validate checks the SpecWorkflowConfig for internal consistency and
// returns an error describing the first violated constraint.
func (c *SpecWorkflowConfig) Validate() error {
	if c.MaxRounds <= 0 {
		return fmt.Errorf("max_rounds must be > 0, got %d", c.MaxRounds)
	}
	if c.MinRounds > c.MaxRounds {
		return fmt.Errorf("min_rounds (%d) must not exceed max_rounds (%d)", c.MinRounds, c.MaxRounds)
	}
	if c.MaxCostUSD <= 0 {
		return fmt.Errorf("max_cost_usd must be > 0, got %g", c.MaxCostUSD)
	}
	if c.MaxTotalFindings <= 0 {
		return fmt.Errorf("max_total_findings must be > 0, got %d", c.MaxTotalFindings)
	}
	if c.SkillPaths.PlanSpec != "" {
		if _, err := os.Stat(c.SkillPaths.PlanSpec); err != nil {
			return fmt.Errorf("skill_paths.plan_spec: %w", err)
		}
	}
	if c.SkillPaths.GrillSpec != "" {
		if _, err := os.Stat(c.SkillPaths.GrillSpec); err != nil {
			return fmt.Errorf("skill_paths.grill_spec: %w", err)
		}
	}
	return nil
}
