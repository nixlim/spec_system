package specworkflow

import (
	"fmt"
	"os"
	"time"

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
// ClaudeModelConfig
// ---------------------------------------------------------------------------

// ClaudeModelConfig holds per-role model overrides for Claude agent invocations.
// Each field names the model passed via --model to the claude CLI. An empty
// field falls back to Default; an empty Default means the CLI picks its own default.
type ClaudeModelConfig struct {
	// Default is the fallback model for any role not explicitly overridden.
	Default string `yaml:"default"`
	// Reviewer is the model for review-lens agents.
	Reviewer string `yaml:"reviewer"`
	// Holdout is the model for the holdout adversarial agent.
	Holdout string `yaml:"holdout"`
	// Reviser is the model for the spec revision agent.
	Reviser string `yaml:"reviser"`
	// Judge is the model for the convergence judge agent.
	Judge string `yaml:"judge"`
	// Discovery is the model for the discovery-phase agent.
	Discovery string `yaml:"discovery"`
	// Drafter is the model for the spec drafting agent.
	Drafter string `yaml:"drafter"`
	// Taskify is the model for the task-graph decomposition agent.
	Taskify string `yaml:"taskify"`
	// TaskReviewer is the model for the task-graph review agent.
	TaskReviewer string `yaml:"task_reviewer"`
	// TaskReviser is the model for the task-graph revision agent.
	TaskReviser string `yaml:"task_reviser"`
}

// For returns the model configured for role, falling back to Default when
// the role-specific field is empty. Returns "" if neither is set.
func (m ClaudeModelConfig) For(role string) string {
	var specific string
	switch role {
	case "reviewer":
		specific = m.Reviewer
	case "holdout":
		specific = m.Holdout
	case "reviser":
		specific = m.Reviser
	case "judge":
		specific = m.Judge
	case "discovery":
		specific = m.Discovery
	case "drafter":
		specific = m.Drafter
	case "taskify":
		specific = m.Taskify
	case "task_reviewer":
		specific = m.TaskReviewer
	case "task_reviser":
		specific = m.TaskReviser
	}
	if specific != "" {
		return specific
	}
	return m.Default
}

// ---------------------------------------------------------------------------
// OpenCodeModelConfig
// ---------------------------------------------------------------------------

// OpenCodeModelConfig holds per-role model overrides for OpenCode agent
// invocations. Each field is a "provider/model" string (e.g.
// "anthropic/claude-sonnet-4-5", "google/gemini-2.5-pro").
type OpenCodeModelConfig struct {
	Default   string `yaml:"default"`
	Reviewer  string `yaml:"reviewer"`
	Holdout   string `yaml:"holdout"`
	Discovery string `yaml:"discovery"`
	Drafter   string `yaml:"drafter"`
}

// For returns the model configured for role, falling back to Default.
func (m OpenCodeModelConfig) For(role string) string {
	var specific string
	switch role {
	case "reviewer":
		specific = m.Reviewer
	case "holdout":
		specific = m.Holdout
	case "discovery":
		specific = m.Discovery
	case "drafter":
		specific = m.Drafter
	}
	if specific != "" {
		return specific
	}
	return m.Default
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
	// MaxGateCorrections is the maximum number of Gate 1 (post-discovery)
	// correction rounds before the workflow proceeds to drafting.
	MaxGateCorrections int `yaml:"max_gate_corrections"`
	// MaxGate2Redrafts is the maximum number of Gate 2 (post-draft)
	// redraft rounds allowed when the human provides answers that
	// require a redraft. Default: 1.
	MaxGate2Redrafts int `yaml:"max_gate2_redrafts"`
	// MaxRetries is the maximum number of retry attempts for transient
	// failures in agent invocations.
	MaxRetries int `yaml:"max_retries"`
	// EnableCodexReviewers controls whether codex CLI agents are used
	// alongside claude for review and holdout generation. Default: true.
	EnableCodexReviewers bool `yaml:"enable_codex_reviewers"`
	// CodexModel is the model passed to codex CLI via -m flag.
	CodexModel string `yaml:"codex_model"`
	// ClaudeModels holds per-role model overrides for Claude agent invocations.
	// Each role field names the --model value; an empty field falls back to Default.
	ClaudeModels ClaudeModelConfig `yaml:"claude_models"`
	// ReviewerTimeoutSeconds is the timeout for all reviewer agents (both
	// claude and codex) in seconds.
	ReviewerTimeoutSeconds int `yaml:"reviewer_timeout_seconds"`
	// HoldoutTimeoutSeconds is the timeout for holdout generation agents
	// in seconds.
	HoldoutTimeoutSeconds int `yaml:"holdout_timeout_seconds"`
	// EnableCodexDiscovery controls whether Codex CLI is used alongside
	// Claude for the discovery phase. Default: false.
	EnableCodexDiscovery bool `yaml:"enable_codex_discovery"`
	// EnableCodexDrafting controls whether Codex CLI is used alongside
	// Claude for the drafting phase. Default: false.
	EnableCodexDrafting bool `yaml:"enable_codex_drafting"`
	// AgentTimeoutSeconds is the per-agent timeout in seconds for
	// dual-provider agent invocations (discovery, drafting, taskify).
	AgentTimeoutSeconds int `yaml:"agent_timeout_seconds"`
	// TaskifyMaxRetries is the maximum number of retry attempts for
	// taskify when output fails schema/DAG validation.
	TaskifyMaxRetries int `yaml:"taskify_max_retries"`
	// EnableOpenCodeReviewers controls whether OpenCode CLI agents are used
	// alongside claude and codex for review and holdout generation.
	EnableOpenCodeReviewers bool `yaml:"enable_opencode_reviewers"`
	// EnableOpenCodeDiscovery controls whether OpenCode CLI is used alongside
	// Claude for the discovery phase.
	EnableOpenCodeDiscovery bool `yaml:"enable_opencode_discovery"`
	// EnableOpenCodeDrafting controls whether OpenCode CLI is used alongside
	// Claude for the drafting phase.
	EnableOpenCodeDrafting bool `yaml:"enable_opencode_drafting"`
	// OpenCodeModels holds per-role model overrides for OpenCode agent invocations.
	OpenCodeModels OpenCodeModelConfig `yaml:"opencode_models"`
	// TaskReviewMaxRounds is the maximum number of task review/revision
	// rounds before findings pass to the human gate regardless of severity.
	TaskReviewMaxRounds int `yaml:"task_review_max_rounds"`
	// BeadsGatePollInterval is how often the orchestrator polls Beads for
	// gate closure. Zero uses the default (5s).
	BeadsGatePollInterval time.Duration `yaml:"beads_gate_poll_interval"`
	// BeadsGateTimeout is how long a gate may remain open before the
	// orchestrator logs a warning. Zero uses the default (24h).
	// The warning is advisory only — polling continues (US-3 AC-8).
	BeadsGateTimeout time.Duration `yaml:"beads_gate_timeout"`
	// KillEscalationTimeoutSeconds is the duration in seconds to wait after
	// SIGTERM before escalating to SIGKILL. Default: 10.
	KillEscalationTimeoutSeconds int `yaml:"kill_escalation_timeout_seconds"`
	// SkillPaths holds the filesystem paths to skill directories.
	SkillPaths SkillPaths `yaml:"skill_paths"`
}

// DefaultConfig returns a SpecWorkflowConfig populated with sensible default
// values suitable for most workflows.
func DefaultConfig() SpecWorkflowConfig {
	return SpecWorkflowConfig{
		MaxRounds:           5,
		MinRounds:           2,
		MaxTotalFindings:    200,
		StalenessThreshold:  2,
		MaxWallClockMinutes: 60,
		MaxCostUSD:          50.0,
		MaxGateCorrections:  3,
		MaxGate2Redrafts:    1,
		MaxRetries:          2,
		EnableCodexReviewers:   true,
		CodexModel:             "gpt-5.4",
		ReviewerTimeoutSeconds: 300,
		HoldoutTimeoutSeconds:  300,
		EnableCodexDiscovery:   false,
		EnableCodexDrafting:    false,
		AgentTimeoutSeconds:    300,
		TaskifyMaxRetries:      3,
		TaskReviewMaxRounds:    3,
		KillEscalationTimeoutSeconds: 10,
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
	if c.MaxGateCorrections <= 0 {
		return fmt.Errorf("max_gate_corrections must be > 0, got %d", c.MaxGateCorrections)
	}
	if c.MaxGate2Redrafts <= 0 {
		return fmt.Errorf("max_gate2_redrafts must be > 0, got %d", c.MaxGate2Redrafts)
	}
	if c.ReviewerTimeoutSeconds <= 0 {
		return fmt.Errorf("reviewer_timeout_seconds must be > 0, got %d", c.ReviewerTimeoutSeconds)
	}
	if c.HoldoutTimeoutSeconds <= 0 {
		return fmt.Errorf("holdout_timeout_seconds must be > 0, got %d", c.HoldoutTimeoutSeconds)
	}
	if c.AgentTimeoutSeconds <= 0 {
		return fmt.Errorf("agent_timeout_seconds must be > 0, got %d", c.AgentTimeoutSeconds)
	}
	if c.TaskifyMaxRetries <= 0 {
		return fmt.Errorf("taskify_max_retries must be > 0, got %d", c.TaskifyMaxRetries)
	}
	if c.TaskReviewMaxRounds <= 0 {
		return fmt.Errorf("task_review_max_rounds must be > 0, got %d", c.TaskReviewMaxRounds)
	}
	if c.KillEscalationTimeoutSeconds <= 0 {
		return fmt.Errorf("kill_escalation_timeout_seconds must be > 0, got %d", c.KillEscalationTimeoutSeconds)
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
