package codedoc

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// CodedocConfig holds tunable parameters that govern the code documentation
// workflow, including round limits, cost budgets, timeout values, and
// dual-provider settings. It is intended to be loaded from a YAML
// configuration file under the `codedoc` key and merged over sensible
// defaults via ParseCodedocConfig.
type CodedocConfig struct {
	// MaxRounds is the maximum number of review/revise iterations.
	// Default: 3
	MaxRounds int `yaml:"max_rounds"`

	// MinRounds is the minimum number of review/revise iterations before acceptance.
	// Default: 1
	MinRounds int `yaml:"min_rounds"`

	// MaxCostUSD is the cost budget in USD. Workflow escalates when exceeded.
	// Default: 50.0
	MaxCostUSD float64 `yaml:"max_cost_usd"`

	// MaxWallClockMinutes is the maximum wall-clock time in minutes.
	// Default: 90
	MaxWallClockMinutes int `yaml:"max_wall_clock_minutes"`

	// MaxRetries is the maximum retry attempts for transient agent failures.
	// Default: 2
	MaxRetries int `yaml:"max_retries"`

	// MaxGateCorrections is the maximum corrections at the scope gate.
	// Default: 3
	MaxGateCorrections int `yaml:"max_gate_corrections"`

	// MaxGateDraftRedrafts is the maximum re-drafts at the draft gate.
	// Default: 2
	MaxGateDraftRedrafts int `yaml:"max_gate_draft_redrafts"`

	// StalenessThreshold is the number of consecutive stale rounds before halt.
	// Default: 2
	StalenessThreshold int `yaml:"staleness_threshold"`

	// AgentTimeoutSeconds is the per-agent timeout for drafting, review, etc.
	// Default: 600
	AgentTimeoutSeconds int `yaml:"agent_timeout_seconds"`

	// DiscoveryTimeoutSeconds is the discovery-specific timeout (large codebases need more).
	// Default: 1200
	DiscoveryTimeoutSeconds int `yaml:"discovery_timeout_seconds"`

	// ReviewerTimeoutSeconds is the per-reviewer timeout.
	// Default: 300
	ReviewerTimeoutSeconds int `yaml:"reviewer_timeout_seconds"`

	// EnableCodexCodedocDiscovery enables dual-provider for discovery.
	// Default: false
	EnableCodexCodedocDiscovery bool `yaml:"enable_codex_codedoc_discovery"`

	// EnableCodexCodedocDrafting enables dual-provider for drafting.
	// Default: false
	EnableCodexCodedocDrafting bool `yaml:"enable_codex_codedoc_drafting"`

	// EnableCodexReviewers enables dual-provider for review.
	// Default: true
	EnableCodexReviewers bool `yaml:"enable_codex_reviewers"`

	// CodexModel is the model identifier for Codex agents.
	// Default: "gpt-5.4"
	CodexModel string `yaml:"codex_model"`

	// DefaultMode is the default workflow mode: "full" or "incremental".
	// Default: "full"
	DefaultMode string `yaml:"default_mode"`

	// BackupBeforeWrite creates timestamped .bak files before overwriting.
	// Default: true
	BackupBeforeWrite bool `yaml:"backup_before_write"`

	// DocsOutputDir is the output directory relative to the code path.
	// Default: "docs"
	DocsOutputDir string `yaml:"docs_output_dir"`

	// DriftWarningThreshold is the fraction of files changed to trigger drift warning (0.0-1.0).
	// Default: 0.20
	DriftWarningThreshold float64 `yaml:"drift_warning_threshold"`

	// WriteLockTimeoutSeconds is the max wait for docs/ write lock before escalating.
	// Default: 300
	WriteLockTimeoutSeconds int `yaml:"write_lock_timeout_seconds"`
}

// validModes is the set of allowed DefaultMode values.
var validModes = map[string]bool{
	"full":        true,
	"incremental": true,
}

// DefaultCodedocConfig returns a CodedocConfig populated with sensible
// default values matching Section 12 of the codedoc workflow spec.
func DefaultCodedocConfig() CodedocConfig {
	return CodedocConfig{
		MaxRounds:               3,
		MinRounds:               1,
		MaxCostUSD:              50.0,
		MaxWallClockMinutes:     90,
		MaxRetries:              2,
		MaxGateCorrections:      3,
		MaxGateDraftRedrafts:    2,
		StalenessThreshold:      2,
		AgentTimeoutSeconds:     600,
		DiscoveryTimeoutSeconds: 1200,
		ReviewerTimeoutSeconds:  300,
		EnableCodexReviewers:    true,
		CodexModel:              "gpt-5.4",
		DefaultMode:             "full",
		BackupBeforeWrite:       true,
		DocsOutputDir:           "docs",
		DriftWarningThreshold:   0.20,
		WriteLockTimeoutSeconds: 300,
	}
}

// ParseCodedocConfig parses YAML configuration data and returns a validated
// CodedocConfig. It starts from DefaultCodedocConfig and overlays only the
// fields present in data, so callers may provide partial YAML to override
// specific defaults. Returns the first validation error encountered, if any.
func ParseCodedocConfig(data []byte) (*CodedocConfig, error) {
	cfg := DefaultCodedocConfig()
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing codedoc config YAML: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Validate checks the CodedocConfig for internal consistency and returns
// an error describing the first violated constraint.
func (c *CodedocConfig) Validate() error {
	if c.MaxRounds < 1 {
		return fmt.Errorf("max_rounds must be >= 1, got %d", c.MaxRounds)
	}
	if c.MinRounds < 1 {
		return fmt.Errorf("min_rounds must be >= 1, got %d", c.MinRounds)
	}
	if c.MinRounds > c.MaxRounds {
		return fmt.Errorf("min_rounds must be <= max_rounds, got min_rounds=%d max_rounds=%d", c.MinRounds, c.MaxRounds)
	}
	if c.MaxCostUSD <= 0 {
		return fmt.Errorf("max_cost_usd must be > 0, got %g", c.MaxCostUSD)
	}
	if c.MaxWallClockMinutes <= 0 {
		return fmt.Errorf("max_wall_clock_minutes must be > 0, got %d", c.MaxWallClockMinutes)
	}
	if c.StalenessThreshold < 0 {
		return fmt.Errorf("staleness_threshold must be >= 0, got %d", c.StalenessThreshold)
	}
	if c.MaxGateCorrections < 1 {
		return fmt.Errorf("max_gate_corrections must be >= 1, got %d", c.MaxGateCorrections)
	}
	if c.MaxGateDraftRedrafts < 1 {
		return fmt.Errorf("max_gate_draft_redrafts must be >= 1, got %d", c.MaxGateDraftRedrafts)
	}
	if c.MaxRetries < 0 {
		return fmt.Errorf("max_retries must be >= 0, got %d", c.MaxRetries)
	}
	if c.AgentTimeoutSeconds <= 0 {
		return fmt.Errorf("agent_timeout_seconds must be > 0, got %d", c.AgentTimeoutSeconds)
	}
	if c.DiscoveryTimeoutSeconds <= 0 {
		return fmt.Errorf("discovery_timeout_seconds must be > 0, got %d", c.DiscoveryTimeoutSeconds)
	}
	if c.ReviewerTimeoutSeconds <= 0 {
		return fmt.Errorf("reviewer_timeout_seconds must be > 0, got %d", c.ReviewerTimeoutSeconds)
	}
	if c.DriftWarningThreshold < 0.0 || c.DriftWarningThreshold > 1.0 {
		return fmt.Errorf("drift_warning_threshold must be >= 0.0 and <= 1.0, got %g", c.DriftWarningThreshold)
	}
	if c.WriteLockTimeoutSeconds <= 0 {
		return fmt.Errorf("write_lock_timeout_seconds must be > 0, got %d", c.WriteLockTimeoutSeconds)
	}
	if !validModes[c.DefaultMode] {
		return fmt.Errorf("default_mode must be one of %q or %q, got %q", "full", "incremental", c.DefaultMode)
	}
	return nil
}
