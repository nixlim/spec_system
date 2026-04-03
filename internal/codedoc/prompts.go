package codedoc

import (
	"fmt"
	"strings"
)

// ---------------------------------------------------------------------------
// Severity rubric (shared across all reviewer prompts)
// ---------------------------------------------------------------------------

const severityRubric = `## Severity Classification Rubric

You MUST classify each finding using this rubric:

| Severity | Definition | Examples |
|----------|------------|---------|
| CRITICAL | Documentation states the opposite of what the code does, or contains leaked secrets/credentials | Wrong data flow direction in diagram; API signature with incorrect parameters; hardcoded API key in code example |
| MAJOR | Documentation omits a significant component, contains misleading descriptions, or has incorrect architectural relationships | Missing module in dependency diagram; module description attributes wrong responsibility; audit false positive on a widely-used function |
| MINOR | Documentation has style issues, unclear phrasing, or minor inaccuracies that don't mislead | Inconsistent capitalisation of module names; unclear sentence that could be reworded; minor version number wrong |
| OBSERVATION | Suggestions for improved documentation structure or additional content | "Consider adding a sequence diagram for the auth flow"; "The config section could list defaults" |
`

// ---------------------------------------------------------------------------
// Reviewer lens groups
// ---------------------------------------------------------------------------

// CodedocLensGroups defines the 4 reviewer groups for codedoc workflows.
var CodedocLensGroups = []string{"accuracy", "completeness", "architecture", "audit_security"}

// codedocLensGroupMap maps each lens group to its review lens codes.
var codedocLensGroupMap = map[string][]string{
	"accuracy":       {"ACC", "CUR"},
	"completeness":   {"CMP", "CLA"},
	"architecture":   {"ARC", "STR"},
	"audit_security": {"AUD", "CON", "SEC"},
}

// codedocReviewerGroupLetter maps a lens group to the suffix letter used in
// output file names (e.g. review-a-round-1.json).
var codedocReviewerGroupLetter = map[string]string{
	"accuracy":       "a",
	"completeness":   "b",
	"architecture":   "c",
	"audit_security": "d",
}

// ReviewerGroupLetter returns the file suffix letter for a given lens group.
func ReviewerGroupLetter(group string) string {
	return codedocReviewerGroupLetter[group]
}

// LensCodesForGroup returns the lens codes for a given reviewer group.
func LensCodesForGroup(group string) []string {
	return codedocLensGroupMap[group]
}

// ---------------------------------------------------------------------------
// Discovery prompt
// ---------------------------------------------------------------------------

// BuildDiscoveryPrompt constructs the prompt for the codedoc discovery agent.
// codePath is the path to the repository to analyse.
func BuildDiscoveryPrompt(codePath, mode string) string {
	var b strings.Builder

	b.WriteString("You are a codebase discovery agent. Analyse the codebase and produce a structured inventory.\n\n")
	b.WriteString(fmt.Sprintf("## Target\n\nCode path: %s\nMode: %s\n\n", codePath, mode))

	b.WriteString("## Instructions\n\n")
	b.WriteString("1. Identify all packages/modules, their responsibilities, public exports, and dependencies.\n")
	b.WriteString("2. Identify entry points (CLI, HTTP servers, etc.).\n")
	b.WriteString("3. Build a dependency graph showing module relationships.\n")
	b.WriteString("4. Inventory existing documentation files and assess their staleness.\n")
	b.WriteString("5. Detect languages, frameworks, and test coverage.\n")
	b.WriteString("6. If optional static analysis tools are available (go vet, staticcheck, etc.), run them and include results.\n")
	b.WriteString("7. Compute a SHA-256 content_hash for each module's source files.\n")
	b.WriteString("8. Suggest a scope for documentation (include/exclude paths, focus areas).\n\n")

	if mode == "incremental" {
		b.WriteString("## Incremental Mode\n\n")
		b.WriteString("Compare the current codebase against the .codedoc-manifest.json in docs/.\n")
		b.WriteString("Identify added, modified, and removed modules since the last run.\n")
		b.WriteString("Populate the incremental_changes field.\n")
		b.WriteString("If the manifest is missing or corrupt, fall back to full mode and set mode to 'full'.\n\n")
	}

	b.WriteString("## Completion Status\n\n")
	b.WriteString("Set completion_status.status to 'complete' if the full inventory was produced.\n")
	b.WriteString("Set it to 'partial' with a reason if discovery timed out or was truncated.\n\n")

	b.WriteString("## Output Format\n\n")
	b.WriteString("Produce a single JSON object conforming to the Discovery Output Schema:\n\n")
	b.WriteString("Required fields: schema_version, agent, mode, completion_status, tools_used, languages, frameworks, modules, entry_points, dependency_graph, existing_docs, test_coverage_overview, suggested_scope.\n")
	b.WriteString("Optional fields: incremental_changes (only for incremental mode), merge_log (only after dual-provider merge).\n")

	return b.String()
}

// ---------------------------------------------------------------------------
// Discovery merge prompt
// ---------------------------------------------------------------------------

// BuildDiscoveryMergePrompt constructs the prompt for the dual-provider
// discovery merge agent, encoding all 7 merge rules from Section 3a.
func BuildDiscoveryMergePrompt(claudeJSON, codexJSON string) string {
	var b strings.Builder

	b.WriteString("You are a Discovery Merge Agent. You will receive two discovery outputs (one from Claude, one from Codex) and must merge them into a single canonical output.\n\n")

	b.WriteString("## Merge Rules\n\n")
	b.WriteString("Apply these rules in order:\n\n")
	b.WriteString("1. **Modules**: Union by module path. When both providers describe the same module path, prefer the description with more detail (more fields populated, longer description). Backfill empty fields from the other provider's entry.\n")
	b.WriteString("2. **Entry points**: Union by path. Deduplicate exact matches.\n")
	b.WriteString("3. **Dependency graph edges**: Union of all edges. Deduplicate by (from, to) pair.\n")
	b.WriteString("4. **Existing docs inventory**: Union by path. When both assess staleness differently, use the more pessimistic (higher staleness) assessment.\n")
	b.WriteString("5. **Languages/frameworks**: Union, deduplicate by name (case-insensitive).\n")
	b.WriteString("6. **Suggested scope**: Union include and exclude lists. Union focus_areas, deduplicate by module path prefix.\n")
	b.WriteString("7. **Conflicts**: When providers produce irreconcilable descriptions of the same module, flag the conflict in the merge_log and include both descriptions with attribution: \"[Claude]: ... | [Codex]: ...\". The human gate displays flagged conflicts.\n\n")

	b.WriteString("## Claude Discovery Output\n\n")
	b.WriteString("<claude_discovery>\n")
	b.WriteString(claudeJSON)
	b.WriteString("\n</claude_discovery>\n\n")

	b.WriteString("## Codex Discovery Output\n\n")
	b.WriteString("<codex_discovery>\n")
	b.WriteString(codexJSON)
	b.WriteString("\n</codex_discovery>\n\n")

	b.WriteString("## Output Format\n\n")
	b.WriteString("Produce a single merged JSON object conforming to the Discovery Output Schema, with merge_log populated.\n")
	b.WriteString("The merge_log must include: claude_modules, codex_modules, merged_modules, conflicts (array), and dedup_count.\n")

	return b.String()
}

// ---------------------------------------------------------------------------
// Drafter prompt
// ---------------------------------------------------------------------------

// BuildDrafterPrompt constructs the prompt for the codedoc drafter agent.
func BuildDrafterPrompt(discoveryJSON, codePath string) string {
	var b strings.Builder

	b.WriteString("You are a codebase documentation drafter. Based on the discovery output, produce comprehensive documentation artefacts.\n\n")
	b.WriteString(fmt.Sprintf("Code path: %s\n\n", codePath))

	b.WriteString("## Discovery Output\n\n")
	b.WriteString("<discovery>\n")
	b.WriteString(discoveryJSON)
	b.WriteString("\n</discovery>\n\n")

	b.WriteString("## Required Artefacts\n\n")
	b.WriteString("Produce ALL FOUR artefact types:\n\n")
	b.WriteString("1. **As-implemented report** (docs/as-implemented-report.md): Comprehensive description of what the codebase actually does. Cover all modules, entry points, configuration, and error handling. Write ONLY as-implemented behaviour, NOT intended/planned features.\n\n")
	b.WriteString("2. **Architecture diagrams** (Mermaid format, each in a separate file):\n")
	b.WriteString("   - Module dependency graph (docs/architecture/module-dependencies.md)\n")
	b.WriteString("   - Call flow diagrams showing cross-module interactions (docs/architecture/call-flows.md)\n")
	b.WriteString("   - Data flow diagrams showing how data moves between components (docs/architecture/data-flows.md)\n")
	b.WriteString("   Validate Mermaid syntax before including. Set mermaid_valid to true only if syntax is correct.\n\n")
	b.WriteString("3. **Code audit** (docs/audit/code-audit.json + docs/audit/code-audit-report.md):\n")
	b.WriteString("   - Dead code (functions with no callers, excluding test helpers)\n")
	b.WriteString("   - Stubs and TODO/FIXME comments\n")
	b.WriteString("   - Empty catch/error blocks\n")
	b.WriteString("   - Non-wired-in components (defined but never used from any entry point)\n")
	b.WriteString("   Each finding must include: id, type, severity, file_path, line_number, symbol, description, evidence.\n\n")
	b.WriteString("4. **Doc updates**: Identify existing documentation files that need updating and specify which sections changed.\n\n")

	b.WriteString("## Output Format\n\n")
	b.WriteString("Produce a single JSON object conforming to the Drafter Output Schema:\n")
	b.WriteString("Required fields: schema_version, agent, as_implemented_report, architecture_diagrams, code_audit, doc_updates, structural_summary.\n")

	return b.String()
}

// ---------------------------------------------------------------------------
// Drafter combine prompt
// ---------------------------------------------------------------------------

// BuildDrafterCombinePrompt constructs the prompt for the dual-provider
// drafter combine agent, encoding all 4 combine rules from Section 3b.
func BuildDrafterCombinePrompt(claudeJSON, codexJSON, discoveryJSON string) string {
	var b strings.Builder

	b.WriteString("You are a Drafter Combine Agent. You will receive two drafter outputs (one from Claude, one from Codex) and must combine them into a single canonical documentation set.\n\n")

	b.WriteString("## Combine Rules\n\n")
	b.WriteString("1. **As-implemented report**: For each module section, select the more detailed description and augment with unique details from the other. Resolve contradictions by cross-referencing the discovery output (source of truth).\n")
	b.WriteString("2. **Architecture diagrams (Mermaid)**: For each diagram type, select the more complete diagram (more nodes/edges) as the base and augment with missing nodes/edges. Do NOT attempt line-level merge of Mermaid syntax. After combining, validate Mermaid syntax. Replace invalid diagrams with the base diagram.\n")
	b.WriteString("3. **Code audit**: Union of findings by (file_path, line_number, type) tuple. Deduplicate exact matches. When both flag the same location with different descriptions, prefer the more specific description.\n")
	b.WriteString("4. **Doc updates**: Union by file path. When both suggest updating the same file, merge the sections_changed lists.\n\n")

	b.WriteString("## Claude Drafter Output\n\n")
	b.WriteString("<claude_drafter>\n")
	b.WriteString(claudeJSON)
	b.WriteString("\n</claude_drafter>\n\n")

	b.WriteString("## Codex Drafter Output\n\n")
	b.WriteString("<codex_drafter>\n")
	b.WriteString(codexJSON)
	b.WriteString("\n</codex_drafter>\n\n")

	b.WriteString("## Discovery Output (source of truth)\n\n")
	b.WriteString("<discovery>\n")
	b.WriteString(discoveryJSON)
	b.WriteString("\n</discovery>\n\n")

	b.WriteString("## Output Format\n\n")
	b.WriteString("Produce a single combined JSON object conforming to the Drafter Output Schema.\n")

	return b.String()
}

// ---------------------------------------------------------------------------
// Sanitisation prompt
// ---------------------------------------------------------------------------

// BuildSanitisationPrompt constructs the prompt for the secret sanitisation
// scanner, listing all pattern categories from Section 3d.
func BuildSanitisationPrompt(draftDir string) string {
	var b strings.Builder

	b.WriteString("You are a secret sanitisation scanner. Scan all documentation output files for leaked secrets and credentials.\n\n")
	b.WriteString(fmt.Sprintf("Draft directory: %s\n\n", draftDir))

	b.WriteString("## Pattern Categories\n\n")
	b.WriteString("Scan all text content (markdown, JSON, Mermaid) for:\n\n")
	b.WriteString("| Category | Examples | Detection Method |\n")
	b.WriteString("|----------|----------|------------------|\n")
	b.WriteString("| api_key | AKIA..., sk-..., ghp_..., glpat-... | Known prefix patterns |\n")
	b.WriteString("| token | Bearer tokens, JWT strings (eyJ prefix), OAuth tokens | Base64 blocks > 40 chars, eyJ prefix |\n")
	b.WriteString("| connection_string | postgres://, mongodb://, redis://, mysql:// | Protocol prefixes with credentials |\n")
	b.WriteString("| password | password=, passwd:, secret: in config examples | Key-value patterns |\n")
	b.WriteString("| private_key | -----BEGIN RSA PRIVATE KEY----- | PEM headers |\n")
	b.WriteString("| pii | Email addresses, phone numbers in code examples | Common PII formats |\n\n")

	b.WriteString("## Instructions\n\n")
	b.WriteString("1. Scan every file in the draft directory.\n")
	b.WriteString("2. For each detected secret, redact it with [REDACTED: <category>].\n")
	b.WriteString("3. If a secret is woven into prose and cannot be safely redacted without breaking the text, set needs_redraft to true.\n")
	b.WriteString("4. Report each detection with file_path, line_number, and pattern_category.\n\n")

	b.WriteString("## Output Format\n\n")
	b.WriteString("Produce a SanitisationReport JSON with: scanned_files, secrets_found, secrets_redacted, entries[], safe (boolean), needs_redraft (boolean).\n")

	return b.String()
}

// ---------------------------------------------------------------------------
// Reviewer prompts
// ---------------------------------------------------------------------------

// BuildReviewerPrompt constructs the prompt for a codedoc reviewer agent.
// lensGroup is one of: "accuracy", "completeness", "architecture", "audit_security".
// draftDir is the path to the draft documentation files.
// round is the review round number.
func BuildReviewerPrompt(lensGroup, draftDir string, round int) string {
	var b strings.Builder

	lenses := codedocLensGroupMap[lensGroup]
	b.WriteString(fmt.Sprintf("You are a codedoc reviewer agent for Group: %s. Apply lenses: %s.\n\n",
		lensGroup, strings.Join(lenses, ", ")))
	b.WriteString(fmt.Sprintf("Review round: %d\n", round))
	b.WriteString(fmt.Sprintf("Draft directory: %s\n\n", draftDir))

	// Severity rubric (required in all reviewer prompts).
	b.WriteString(severityRubric)
	b.WriteString("\n")

	// Lens-specific instructions.
	switch lensGroup {
	case "accuracy":
		b.WriteString("## Lens Instructions\n\n")
		b.WriteString("**ACC (Accuracy)**: Does the documentation match the actual code? Are function signatures, module responsibilities, and data flows described correctly?\n\n")
		b.WriteString("**CUR (Currency)**: Is the documentation current with the actual codebase state? Are deprecated features still documented? Are new features missing?\n\n")

	case "completeness":
		b.WriteString("## Lens Instructions\n\n")
		b.WriteString("**CMP (Completeness)**: Are all public APIs, modules, and execution flows documented? Are entry points, configuration options, and error handling covered?\n\n")
		b.WriteString("**CLA (Clarity)**: Is the documentation clear and unambiguous? Can a new developer understand the system from the docs alone? Are terms defined consistently?\n\n")

	case "architecture":
		b.WriteString("## Lens Instructions\n\n")
		b.WriteString("**ARC (Architecture Correctness)**: Are the architecture diagrams correct? Do they accurately represent module boundaries, dependency directions, and data flow? **ARC findings on diagrams MUST include the corrected Mermaid snippet, not just a prose description.**\n\n")
		b.WriteString("**STR (Structure)**: Is the documentation well-organized? Are files named logically? Is the hierarchy navigable? Do cross-references work?\n\n")

	case "audit_security":
		b.WriteString("## Lens Instructions\n\n")
		b.WriteString("**AUD (Audit Quality)**: Are dead code, stub, and non-wired-in component findings real (not false positives)? Are TODO/FIXME flagging and empty catch blocks correctly identified?\n\n")
		b.WriteString("**CON (Consistency)**: Is the documentation internally consistent? Do different sections agree on naming, component responsibilities, and data flow descriptions?\n\n")
		b.WriteString("**SEC (Sensitive Data)**: Does any documentation output contain secrets, credentials, API keys, tokens, connection strings, passwords, or PII from source code? Does any code example embed real configuration values? This is a second layer of defence after the automated sanitisation step.\n\n")
	}

	b.WriteString("## Output Format\n\n")
	b.WriteString("Produce a ReviewerOutput JSON with:\n")
	b.WriteString("- schema_version, agent, round, lenses_applied\n")
	b.WriteString("- findings[]: each with id, description, severity, status (\"open\"), impact, recommendation, lens, affected_section, affected_file\n")
	b.WriteString("- structural_integrity: performed (bool), checks[] with name, passed (bool), details\n\n")
	b.WriteString("Finding IDs should use the format: <LENS>-<NNN> (e.g., ACC-001, ARC-002).\n")
	b.WriteString("Set status to \"open\" for all new findings.\n")

	return b.String()
}

// ---------------------------------------------------------------------------
// Revision prompt
// ---------------------------------------------------------------------------

// BuildRevisionPrompt constructs the prompt for the revision agent.
// findingsJSON is the merged findings from the current round.
// draftDir is the path to the draft documentation files.
func BuildRevisionPrompt(findingsJSON, draftDir string, round int) string {
	var b strings.Builder

	b.WriteString("You are a codedoc revision agent. Address review findings by correcting the documentation.\n\n")
	b.WriteString(fmt.Sprintf("Revision round: %d\n", round))
	b.WriteString(fmt.Sprintf("Draft directory: %s\n\n", draftDir))

	b.WriteString("## Review Findings\n\n")
	b.WriteString("<findings>\n")
	b.WriteString(findingsJSON)
	b.WriteString("\n</findings>\n\n")

	b.WriteString("## Instructions\n\n")
	b.WriteString("1. Address CRITICAL findings first, then MAJOR, then MINOR. Observations are optional.\n")
	b.WriteString("2. For each finding you address, update the documentation files accordingly.\n")
	b.WriteString("3. For findings you cannot address (out of scope, contradictory), mark as status: \"wontfix\" with rationale.\n")
	b.WriteString("4. Do NOT introduce new content beyond what is needed to fix the findings.\n")
	b.WriteString("5. When fixing ARC findings with corrected Mermaid snippets, use the provided snippet.\n\n")

	b.WriteString("## Output Format\n\n")
	b.WriteString("Produce a JSON object with:\n")
	b.WriteString("- schema_version, agent, round\n")
	b.WriteString("- addressed[]: each with finding_id, status (\"resolved\"), action taken\n")
	b.WriteString("- wont_fix[]: each with finding_id, status (\"wontfix\"), rationale\n")
	b.WriteString("- remaining[]: findings not yet addressed, with finding_id and status\n")
	b.WriteString("- summary: { addressed_count, wontfix_count, remaining_count }\n")

	return b.String()
}

// ---------------------------------------------------------------------------
// Judge prompt
// ---------------------------------------------------------------------------

// BuildJudgePrompt constructs the prompt for the codedoc judge agent.
// findingsJSON is the merged findings after revision.
// round is the current review round.
func BuildJudgePrompt(findingsJSON string, round int) string {
	var b strings.Builder

	b.WriteString("You are a codedoc judge agent. Evaluate the review findings and render a verdict.\n\n")
	b.WriteString(fmt.Sprintf("Round: %d\n\n", round))

	b.WriteString("## Current Findings\n\n")
	b.WriteString("<findings>\n")
	b.WriteString(findingsJSON)
	b.WriteString("\n</findings>\n\n")

	b.WriteString("## Verdict Rules\n\n")
	b.WriteString("Render one of these verdicts:\n\n")
	b.WriteString("- **PASS**: All CRITICAL and MAJOR findings are resolved or absent. Documentation is ready for writing.\n")
	b.WriteString("- **REVISE**: Unresolved CRITICAL or MAJOR findings remain. Another review/revision round is needed.\n")
	b.WriteString("- **BLOCK**: The documentation has fundamental issues that cannot be resolved through iteration. Escalate to human.\n\n")

	b.WriteString("## Authority Limits\n\n")
	b.WriteString("- You may downgrade at most 2 findings per round. Valid reason codes: DUPLICATE_OF, OUT_OF_SCOPE, CONTRADICTED_BY_REQUIREMENT, REVIEWER_ERROR.\n")
	b.WriteString("- You may dismiss at most 3 findings per round.\n")
	b.WriteString("- If cumulative downgrades + dismissals exceed 5 across all rounds, the workflow escalates.\n\n")

	b.WriteString("## Output Format\n\n")
	b.WriteString("Produce a JudgeOutput JSON with:\n")
	b.WriteString("- schema_version, agent, round\n")
	b.WriteString("- verdict (PASS, REVISE, or BLOCK)\n")
	b.WriteString("- rationale: explanation of the verdict\n")
	b.WriteString("- issue_updates[]: each with finding_id, new_status, reason\n")
	b.WriteString("- downgrades[]: each with finding_id, old_severity, new_severity, reason_code\n")

	return b.String()
}
