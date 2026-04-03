package codedoc

import "github.com/foundry-zero/adversarial-spec-system/internal/specworkflow"

// ---------------------------------------------------------------------------
// Severity constants
// ---------------------------------------------------------------------------

const (
	SeverityCritical    = "critical"
	SeverityMajor       = "major"
	SeverityMinor       = "minor"
	SeverityObservation = "observation"
)

// ---------------------------------------------------------------------------
// Discovery Output types
// ---------------------------------------------------------------------------

// DiscoveryOutput represents the structured output of the discovery agent,
// conforming to the Discovery Output Schema (Section 9).
type DiscoveryOutput struct {
	SchemaVersion    string              `json:"schema_version"`
	Agent            string              `json:"agent"`
	Mode             string              `json:"mode"`
	CompletionStatus CompletionStatus    `json:"completion_status"`
	ToolsUsed        []ToolUsed          `json:"tools_used"`
	Languages        []LanguageInfo      `json:"languages"`
	Frameworks       []string            `json:"frameworks"`
	Modules          []ModuleInfo        `json:"modules"`
	EntryPoints      []EntryPoint        `json:"entry_points"`
	DependencyGraph  DependencyGraph     `json:"dependency_graph"`
	ExistingDocs     []ExistingDoc       `json:"existing_docs"`
	TestCoverage     TestCoverageOverview `json:"test_coverage_overview"`
	SuggestedScope   SuggestedScope      `json:"suggested_scope"`
	IncrementalChanges *IncrementalChanges `json:"incremental_changes"`
	MergeLog         *MergeLog           `json:"merge_log"`
}

// CompletionStatus indicates whether the discovery inventory is complete or partial.
type CompletionStatus struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

// ToolUsed records a static analysis tool and its status during discovery.
type ToolUsed struct {
	Tool    string `json:"tool"`
	Version string `json:"version"`
	Status  string `json:"status"`
}

// LanguageInfo records language statistics discovered in the codebase.
type LanguageInfo struct {
	Language  string `json:"language"`
	FileCount int    `json:"file_count"`
	LineCount int    `json:"line_count"`
}

// ModuleInfo describes a single module/package discovered in the codebase.
type ModuleInfo struct {
	Path         string   `json:"path"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Files        int      `json:"files"`
	Lines        int      `json:"lines"`
	Exports      []string `json:"exports"`
	Dependencies []string `json:"dependencies"`
	HasTests     bool     `json:"has_tests"`
	TestFiles    int      `json:"test_files"`
	ContentHash  string   `json:"content_hash"`
}

// EntryPoint describes a codebase entry point (CLI, HTTP server, etc.).
type EntryPoint struct {
	Path        string `json:"path"`
	Type        string `json:"type"`
	Description string `json:"description"`
}

// DependencyGraph holds the edges of the module dependency graph.
type DependencyGraph struct {
	Edges []DependencyEdge `json:"edges"`
}

// DependencyEdge represents a directed dependency between two modules.
type DependencyEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// ExistingDoc describes a documentation file already present in the codebase.
type ExistingDoc struct {
	Path              string   `json:"path"`
	LastModified      string   `json:"last_modified"`
	EstimatedStaleness string  `json:"estimated_staleness"`
	Topics            []string `json:"topics"`
}

// TestCoverageOverview summarises test presence across the codebase.
type TestCoverageOverview struct {
	TestFileCount       int      `json:"test_file_count"`
	PackagesWithTests   int      `json:"packages_with_tests"`
	PackagesWithoutTests int     `json:"packages_without_tests"`
	TestFrameworks      []string `json:"test_frameworks"`
}

// SuggestedScope describes the recommended scope for documentation.
type SuggestedScope struct {
	Include    []string `json:"include"`
	Exclude    []string `json:"exclude"`
	FocusAreas []string `json:"focus_areas"`
}

// IncrementalChanges describes what changed since the last documentation run.
type IncrementalChanges struct {
	AddedModules    []string `json:"added_modules"`
	ModifiedModules []string `json:"modified_modules"`
	RemovedModules  []string `json:"removed_modules"`
	AddedFiles      int      `json:"added_files"`
	ModifiedFiles   int      `json:"modified_files"`
	RemovedFiles    int      `json:"removed_files"`
	Recommendation  string   `json:"recommendation"`
	Reason          string   `json:"reason"`
}

// MergeConflict records a single disagreement between providers during merge.
type MergeConflict struct {
	ModulePath  string `json:"module_path"`
	Field       string `json:"field"`
	ClaudeValue string `json:"claude_value"`
	CodexValue  string `json:"codex_value"`
	Resolution  string `json:"resolution"`
}

// MergeLog records the outcome of dual-provider discovery merge.
type MergeLog struct {
	ClaudeModules int             `json:"claude_modules"`
	CodexModules  int             `json:"codex_modules"`
	MergedModules int             `json:"merged_modules"`
	Conflicts     []MergeConflict `json:"conflicts"`
	DedupCount    int             `json:"dedup_count"`
	Strategy      string          `json:"strategy"`
}

// ---------------------------------------------------------------------------
// Drafter Output types
// ---------------------------------------------------------------------------

// DrafterOutput represents the structured output of the drafter agent,
// conforming to the Drafter Output Schema (Section 10).
type DrafterOutput struct {
	SchemaVersion      string              `json:"schema_version"`
	Agent              string              `json:"agent"`
	AsImplementedReport ReportRef          `json:"as_implemented_report"`
	ArchitectureDiagrams []DiagramRef     `json:"architecture_diagrams"`
	CodeAudit          CodeAudit           `json:"code_audit"`
	DocUpdates         []DocUpdate         `json:"doc_updates"`
	StructuralSummary  StructuralSummary   `json:"structural_summary"`
}

// ReportRef points to the as-implemented report file and its sections.
type ReportRef struct {
	FilePath string   `json:"file_path"`
	Sections []string `json:"sections"`
}

// DiagramRef describes a single architecture diagram artefact.
type DiagramRef struct {
	FilePath       string `json:"file_path"`
	DiagramType    string `json:"diagram_type"`
	MermaidContent string `json:"mermaid_content"`
	MermaidValid   bool   `json:"mermaid_valid"`
}

// CodeAudit holds the code audit findings and summary.
type CodeAudit struct {
	JSONFilePath   string            `json:"json_file_path"`
	ReportFilePath string            `json:"report_file_path"`
	Findings       []CodeAuditFinding `json:"findings"`
	Summary        AuditSummary      `json:"summary"`
}

// CodeAuditFinding represents a single finding from the code audit.
type CodeAuditFinding struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Severity    string `json:"severity"`
	FilePath    string `json:"file_path"`
	LineNumber  int    `json:"line_number"`
	Symbol      string `json:"symbol"`
	Description string `json:"description"`
	Evidence    string `json:"evidence"`
}

// AuditSummary provides counts of each finding type.
type AuditSummary struct {
	DeadCode    int `json:"dead_code"`
	Stubs       int `json:"stubs"`
	Todos       int `json:"todos"`
	Fixmes      int `json:"fixmes"`
	EmptyCatches int `json:"empty_catches"`
	NonWired    int `json:"non_wired"`
}

// DocUpdate describes a change to an existing documentation file.
type DocUpdate struct {
	FilePath        string   `json:"file_path"`
	Action          string   `json:"action"`
	SectionsChanged []string `json:"sections_changed"`
	Reason          string   `json:"reason"`
}

// StructuralSummary provides an overview of the drafter output.
type StructuralSummary struct {
	ReportSections         int `json:"report_sections"`
	DiagramCount           int `json:"diagram_count"`
	AuditFindingCount      int `json:"audit_finding_count"`
	DocUpdates             int `json:"doc_updates"`
	ModulesDocumented      int `json:"modules_documented"`
	EntryPointsDocumented  int `json:"entry_points_documented"`
}

// ---------------------------------------------------------------------------
// Sanitisation types
// ---------------------------------------------------------------------------

// SanitisationReport records the outcome of secret scanning on documentation output.
type SanitisationReport struct {
	ScannedFiles    int                 `json:"scanned_files"`
	SecretsFound    int                 `json:"secrets_found"`
	SecretsRedacted int                 `json:"secrets_redacted"`
	Entries         []SanitisationEntry `json:"entries"`
	Safe            bool                `json:"safe"`
	NeedsRedraft    bool                `json:"needs_redraft"`
}

// SanitisationEntry describes a single secret detection in a scanned file.
type SanitisationEntry struct {
	FilePath    string `json:"file_path"`
	LineNumber  int    `json:"line_number"`
	PatternType string `json:"pattern_type"`
	Redacted    bool   `json:"redacted"`
	Confidence  string `json:"confidence"`
}

// ---------------------------------------------------------------------------
// Manifest types
// ---------------------------------------------------------------------------

// ManifestFile represents the .codedoc-manifest.json written during CD_WRITING,
// conforming to the Manifest Schema (Section 19a).
type ManifestFile struct {
	SchemaVersion           string           `json:"schema_version"`
	WorkflowFeature         string           `json:"workflow_feature"`
	GeneratedAt             string           `json:"generated_at"`
	Mode                    string           `json:"mode,omitempty"`
	Modules                 []ManifestModule `json:"modules"`
	FilesDocumented         []ManifestDoc    `json:"files_documented"`
	DiscoveryCompletionStatus string         `json:"discovery_completion_status,omitempty"`
}

// ManifestModule records a module's state at documentation time.
type ManifestModule struct {
	Path        string   `json:"path"`
	ContentHash string   `json:"content_hash"`
	DocFiles    []string `json:"doc_files"`
}

// ManifestDoc records a documented file and its content hash.
type ManifestDoc struct {
	Path        string `json:"path"`
	ContentHash string `json:"content_hash"`
}

// ---------------------------------------------------------------------------
// Gate payload types
// ---------------------------------------------------------------------------

// ScopeGatePayload carries the data presented to the human at CD_HUMAN_GATE_SCOPE.
type ScopeGatePayload struct {
	// Modules is the discovered module inventory.
	Modules []ModuleInfo `json:"modules"`
	// CompletionStatus indicates whether the inventory is complete or partial.
	CompletionStatus CompletionStatus `json:"completion_status"`
	// DependencyGraph holds the dependency edges.
	DependencyGraph DependencyGraph `json:"dependency_graph"`
	// ExistingDocs lists documentation files already in the codebase.
	ExistingDocs []ExistingDoc `json:"existing_docs"`
	// SuggestedScope is the recommended scope for documentation.
	SuggestedScope SuggestedScope `json:"suggested_scope"`
	// MergeConflicts lists any conflicts from dual-provider discovery merge.
	MergeConflicts []MergeConflict `json:"merge_conflicts,omitempty"`
	// DualProvider indicates whether dual-provider discovery was used.
	DualProvider bool `json:"dual_provider"`
	// DiscoverySource indicates how the discovery was produced ("combined", "single_survivor", "single_provider").
	DiscoverySource string `json:"discovery_source,omitempty"`
}

// DraftGatePayload carries the data presented at CD_HUMAN_GATE_DRAFT.
type DraftGatePayload struct {
	// GeneratedFiles lists the files produced by the drafter.
	GeneratedFiles []string `json:"generated_files"`
	// Summary holds aggregate statistics about the draft.
	Summary StructuralSummary `json:"summary"`
	// RedactionLog is the sanitisation report (nil if no secrets detected).
	RedactionLog *SanitisationReport `json:"redaction_log,omitempty"`
	// RedraftCount is the number of re-drafts already performed.
	RedraftCount int `json:"redraft_count"`
	// MaxRedrafts is the configured maximum re-drafts.
	MaxRedrafts int `json:"max_redrafts"`
	// RedraftDisabled is true when the redraft limit has been exhausted.
	RedraftDisabled bool `json:"redraft_disabled"`
}

// FinalGatePayload carries the data presented at CD_HUMAN_GATE_FINAL.
type FinalGatePayload struct {
	// UnresolvedFindings lists CRITICAL and MAJOR findings that remain open.
	UnresolvedFindings []UnresolvedFinding `json:"unresolved_findings"`
	// TotalUnresolved is the count of unresolved CRITICAL + MAJOR findings.
	TotalUnresolved int `json:"total_unresolved"`
	// DriftWarning is set if the codebase has changed significantly since discovery.
	DriftWarning string `json:"drift_warning,omitempty"`
}

// UnresolvedFinding is a simplified finding for display at the final gate.
type UnresolvedFinding struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Severity    string `json:"severity"`
	Lens        string `json:"lens"`
	Section     string `json:"affected_section"`
}

// ---------------------------------------------------------------------------
// Convergence types
// ---------------------------------------------------------------------------

// Verdict constants for codedoc convergence decisions.
const (
	// VerdictPass means all findings are resolved or MINOR/OBSERVATION only.
	// Routes to CD_WRITING.
	VerdictPass = "PASS"
	// VerdictPassWithGate means unresolved CRITICAL or MAJOR remain but
	// min_rounds is met. Routes to CD_HUMAN_GATE_FINAL for human decision.
	VerdictPassWithGate = "PASS_WITH_GATE"
	// VerdictRevise means unresolved CRITICAL or MAJOR remain and min_rounds
	// not yet met. Routes to CD_REVIEWING for another round.
	VerdictRevise = "REVISE"
)

// CDState routing constants for next-state strings returned by convergence evaluation.
const (
	CDStateReviewing      = "CD_REVIEWING"
	CDStateHumanGateFinal = "CD_HUMAN_GATE_FINAL"
	CDStateWriting        = "CD_WRITING"
)

// FindingSummary holds per-severity counts for the judge output.
type FindingSummary struct {
	OpenCritical    int `json:"open_critical"`
	OpenMajor       int `json:"open_major"`
	OpenMinor       int `json:"open_minor"`
	OpenObservation int `json:"open_observation"`
	Resolved        int `json:"resolved"`
	WontFix         int `json:"wontfix"`
}

// ConvergenceResult is the outcome of evaluating convergence for a codedoc
// review round.
type ConvergenceResult struct {
	Verdict        string         `json:"verdict"`
	NextState      string         `json:"next_state"`
	Reasoning      string         `json:"reasoning"`
	FindingSummary FindingSummary `json:"finding_summary"`
	StaleDetected  bool           `json:"stale_detected"`
}

// ---------------------------------------------------------------------------
// Discovery phase types
// ---------------------------------------------------------------------------

// DiscoveryDeps holds the dependencies needed by RunDiscovery. This avoids
// coupling to a full orchestrator struct and makes testing straightforward.
type DiscoveryDeps struct {
	// Runner is the Claude agent runner.
	Runner specworkflow.AgentRunner
	// CodexRunner is the Codex agent runner (nil when dual-provider is disabled).
	CodexRunner specworkflow.AgentRunner
	// MergeRunner is the agent runner used for the merge agent (typically Claude).
	MergeRunner specworkflow.AgentRunner
	// Config is the validated codedoc configuration.
	Config CodedocConfig
	// FeatureDir is the workspace directory for this feature
	// (e.g., workspace/codedoc/{feature}/).
	FeatureDir string
	// CodePath is the filesystem path to the target repository.
	CodePath string
	// Mode is "full" or "incremental".
	Mode string
	// Round is the discovery round number (1-indexed).
	Round int
}

// DiscoveryResult holds the outputs of a successful discovery phase.
type DiscoveryResult struct {
	// Output is the canonical (possibly merged) discovery output.
	Output *DiscoveryOutput
	// CostUSD is the total cost across all agent invocations.
	CostUSD float64
	// DurationMS is the wall-clock duration of the longest agent.
	DurationMS int64
	// Warnings accumulated during discovery (e.g., single-provider fallback).
	Warnings []string
}
