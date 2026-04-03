# Codebase Documentation Workflow — Feature Specification

**Feature**: Codebase Documentation ("code-doc") Workflow
**Version**: 1.1
**Status**: Revised (addressing grill-spec review findings)
**Date**: 2026-04-01

---

## 1. Overview

### Problem Statement

Codebases drift from their documentation. README files become stale, architecture diagrams reflect designs from months ago, dead code accumulates without anyone noticing, and TODO stubs never get tracked. There is no automated way to produce a comprehensive, accurate "as-implemented" report that covers architecture, dead code, stubs, and comment quality — all verified through adversarial review to ensure accuracy.

### Solution

A new dual-provider workflow type ("code-doc") that uses the proven adversarial Review-Revise-Judge loop to produce comprehensive, verified codebase documentation. The workflow takes a code path as input and produces:

1. A full "as-implemented" report describing what the codebase actually does
2. Updated/expanded existing documentation files in the repository
3. Architecture diagrams (Mermaid) in thematically named separate files
4. A structured audit of comments, stubs, non-wired-in components, and suspected dead code

The workflow supports dual-provider execution (Claude + Codex in parallel) with LLM-based merge agents, and human gates at key decision points. All documentation output is scanned for secrets before writing. The output is in-situ current-state-of-the-codebase documentation written to the target repository's `docs/` directory.

### Actors

| Actor | Type | Description |
|-------|------|-------------|
| Developer / Tech Lead | Human | Initiates workflow, provides code path, reviews outputs at gates, approves final documentation |
| Claude Discovery Agent | System | Analyses codebase structure, modules, dependencies, and existing docs |
| Codex Discovery Agent | System | Parallel analysis for dual-provider merge during discovery |
| Documentation Drafter (Claude) | System | Produces documentation artefacts from discovery output |
| Documentation Drafter (Codex) | System | Parallel documentation production for dual-provider merge |
| Discovery Merge Agent | System | LLM-based agent that merges dual-provider discovery outputs (see Section 3a) |
| Drafter Combine Agent | System | LLM-based agent that merges dual-provider drafter outputs (see Section 3b) |
| Reviewer Agents (4 groups) | System | Adversarial review of documentation accuracy, completeness, architecture, audit quality, and security |
| Reviser Agent | System | Addresses review findings by correcting documentation |
| Judge Agent | System | Evaluates convergence, renders PASS/REVISE/BLOCK verdict |

### Architectural Relationship

The code-doc workflow is a **sibling workflow** to the existing `specworkflow` and `codereview` packages:

| Component | Reused from | Reimplemented | Notes |
|-----------|-------------|---------------|-------|
| State machine | `specworkflow/statemachine.go` pattern | CD-prefixed states | Same universal ERROR transition rules |
| Review dispatch | `specworkflow/review_dispatch.go` | Adapted lens groups | Same parallel dispatch + failure tolerance pattern |
| Findings merge | `specworkflow/merge.go` | Adapted dedup key | Same `MergeReviewerOutputs` with codedoc-specific `DedupKeyFunc` |
| Convergence | `codereview/convergence.go` pattern | Adapted for doc lenses | Severity-based verdict, same staleness detection |
| Discovery merge | `specworkflow/orchestrator_discovery.go` | Adapted for codedoc schema | Same LLM-based merge agent pattern |
| Claude/Codex runners | `specworkflow/claude_runner.go` | Shared directly | Same `AgentRunner` interface |
| Human gates | `specworkflow/orchestrator.go` | Adapted gate payloads | Same gate handler pattern |
| Writing phase | N/A | New | Staging directory + atomic move; unique to codedoc |
| Secret sanitisation | N/A | New | Pre-write scanning; unique to codedoc |

**Package location**: `internal/codedoc/` — parallel to `internal/specworkflow/` and `internal/codereview/`.

---

## 2. Workflow States

```
Code Path + Mode (full | incremental)
       |
       v
  [ CD_INIT ]
       |
       v
  [ CD_DISCOVERY ]  ──>  Analyse codebase: modules, deps, entry points,
       |                  test coverage, existing docs, language/framework
       |                  (dual-provider: Claude + Codex with merge)
       v
  [ CD_HUMAN_GATE_SCOPE ]  ──>  Confirm/adjust scope, select focus areas
       |
       v
  [ CD_DRAFTING ]  ──>  Produce documentation artefacts:
       |                 - As-implemented report
       |                 - Architecture diagrams (Mermaid)
       |                 - Comment/stub/dead-code audit (JSON + findings)
       |                 - Updated existing doc files
       |                 (dual-provider: Claude + Codex with combine)
       v
  [ CD_SANITISING ]  ──>  Scan all documentation output for secrets/credentials
       |
       v
  [ CD_HUMAN_GATE_DRAFT ]  ──>  Review draft docs, approve/correct direction
       |
       v
  ┌─────────────────────────────────────┐
  │  [ CD_REVIEWING ]                   │
  │    4 parallel reviewer groups       │  Adversarial review loop
  │    9 lenses across 4 groups         │  (configurable rounds)
  │    + optional Codex reviewers       │
  │              |                      │
  │  [ CD_REVISING ]                    │
  │    Address findings, update docs    │
  │              |                      │
  │  [ CD_JUDGING ]                     │
  │    Convergence + accuracy check     │
  └──────────────┬──────────────────────┘
                 |
                 v
  [ CD_HUMAN_GATE_FINAL ]  ──>  (only when unresolved CRITICAL or MAJOR findings remain)
                 |
                 v
  [ CD_WRITING ]  ──>  Write approved docs to target repo's docs/ directory
                 |     (staging → atomic move, with drift check)
                 v
          [ CD_COMPLETE ]
```

### State Definitions

| State | Type | Description |
|-------|------|-------------|
| `CD_INIT` | Init | Workflow created, not yet started |
| `CD_DISCOVERY` | Agent | Analyse codebase structure, inventory modules, detect patterns |
| `CD_HUMAN_GATE_SCOPE` | Gate | Human confirms/adjusts discovery scope before drafting |
| `CD_DRAFTING` | Agent | Produce all documentation artefacts from confirmed scope |
| `CD_SANITISING` | Agent | Scan all documentation output for secrets, credentials, and PII patterns |
| `CD_HUMAN_GATE_DRAFT` | Gate | Human reviews draft direction before adversarial review |
| `CD_REVIEWING` | Agent | 4 reviewer groups apply 9 lenses to verify documentation |
| `CD_REVISING` | Agent | Address review findings, update documentation |
| `CD_JUDGING` | Agent | Evaluate convergence, render verdict |
| `CD_HUMAN_GATE_FINAL` | Gate | Human approves final documentation when unresolved CRITICAL or MAJOR findings remain |
| `CD_WRITING` | Agent | Write approved documentation files to target repository via staging directory |
| `CD_COMPLETE` | Terminal | Workflow completed successfully |
| `CD_ESCALATED` | Terminal | Workflow halted by circuit breaker or human rejection |
| `CD_ERROR` | Terminal | Unrecoverable error (reachable from any non-terminal state) |

### State Transition Table

```
CD_INIT              -> CD_DISCOVERY
CD_DISCOVERY         -> CD_HUMAN_GATE_SCOPE, CD_ESCALATED
CD_HUMAN_GATE_SCOPE  -> CD_DRAFTING, CD_DISCOVERY, CD_ESCALATED
CD_DRAFTING          -> CD_SANITISING, CD_ESCALATED
CD_SANITISING        -> CD_HUMAN_GATE_DRAFT, CD_DRAFTING (if unredacted secrets found)
CD_HUMAN_GATE_DRAFT  -> CD_REVIEWING, CD_DRAFTING, CD_ESCALATED
CD_REVIEWING         -> CD_REVISING, CD_JUDGING, CD_ESCALATED
CD_REVISING          -> CD_JUDGING, CD_ESCALATED
CD_JUDGING           -> CD_REVIEWING, CD_HUMAN_GATE_FINAL, CD_WRITING, CD_ESCALATED
CD_HUMAN_GATE_FINAL  -> CD_WRITING, CD_ESCALATED
CD_WRITING           -> CD_COMPLETE, CD_ESCALATED

# Universal error transitions (implemented as statemachine rules, not per-state edges):
Any state except (CD_COMPLETE, CD_ESCALATED) -> CD_ERROR    on unrecoverable failure
CD_ERROR -> CD_ERROR                                        BLOCKED (prevents loops)
```

### Error Transitions and Resume Semantics

The CD_ERROR state follows the same universal transition pattern as the existing spec workflow's `statemachine.go`:

1. **Any non-terminal state can transition to CD_ERROR** on unrecoverable failure (agent crash, system error, unrecoverable validation failure after retries exhausted).
2. **CD_ERROR -> CD_ERROR is blocked** to prevent error loops.
3. **Resume from CD_ERROR** is triggered via the `POST .../resume` endpoint and uses artefact detection to determine the appropriate restart state:

| Artefacts Present | Resume Target | Rationale |
|-------------------|---------------|-----------|
| No discovery output | CD_DISCOVERY | Fresh start — nothing to salvage |
| Discovery output exists, no drafter output | CD_HUMAN_GATE_SCOPE | Re-confirm scope before re-drafting |
| Drafter output exists, no review outputs | CD_HUMAN_GATE_DRAFT | Re-confirm draft before review |
| Review outputs exist | CD_REVIEWING | Resume review loop from current round |
| Staging directory exists (`docs/.codedoc-staging/`) | CD_HUMAN_GATE_FINAL | Crash during writing — offer human choice: complete write or re-review |

**Crash during CD_WRITING**: The staging directory pattern (Section 3c) ensures original files are untouched if the process crashes mid-write. On resume:
- If `docs/.codedoc-staging/` exists and is complete (all expected files present), the human gate offers: "Complete write from staging" or "Re-enter review."
- If staging is incomplete, the system discards it and resumes from CD_HUMAN_GATE_FINAL for re-approval, then re-enters CD_WRITING.

---

## 3. Review Lenses

9 lenses across 4 reviewer groups, adapted for codebase documentation:

### Group 1: Accuracy (Lenses: ACC, CUR)

| Lens | Code | Description |
|------|------|-------------|
| Accuracy | ACC | Does the documentation match the actual code? Are function signatures, module responsibilities, and data flows described correctly? |
| Currency | CUR | Is the documentation current with the actual codebase state? Are deprecated features still documented? Are new features missing? |

### Group 2: Completeness (Lenses: CMP, CLA)

| Lens | Code | Description |
|------|------|-------------|
| Completeness | CMP | Are all public APIs, modules, and execution flows documented? Are entry points, configuration options, and error handling covered? |
| Clarity | CLA | Is the documentation clear and unambiguous? Can a new developer understand the system from the docs alone? Are terms defined consistently? |

### Group 3: Architecture (Lenses: ARC, STR)

| Lens | Code | Description |
|------|------|-------------|
| Architecture Correctness | ARC | Are the architecture diagrams correct? Do they accurately represent module boundaries, dependency directions, and data flow? ARC findings on diagrams MUST include the corrected Mermaid snippet, not just a prose description. |
| Structure | STR | Is the documentation well-organized? Are files named logically? Is the hierarchy navigable? Do cross-references work? |

### Group 4: Audit & Security (Lenses: AUD, CON, SEC)

| Lens | Code | Description |
|------|------|-------------|
| Audit Quality | AUD | Are dead code, stub, and non-wired-in component findings real (not false positives)? Are TODO/FIXME flagging and empty catch blocks correctly identified? |
| Consistency | CON | Is the documentation internally consistent? Do different sections agree on naming, component responsibilities, and data flow descriptions? |
| Sensitive Data | SEC | Does any documentation output contain secrets, credentials, API keys, tokens, connection strings, passwords, or PII from source code? Does any code example embed real configuration values? |

### Severity Classification Rubric

All reviewer agents MUST classify findings using this rubric. The judge uses these severities for convergence evaluation.

| Severity | Definition | Examples |
|----------|------------|---------|
| **CRITICAL** | Documentation states the opposite of what the code does, or contains leaked secrets/credentials | Wrong data flow direction in diagram; API signature with incorrect parameters; hardcoded API key in code example |
| **MAJOR** | Documentation omits a significant component, contains misleading descriptions, or has incorrect architectural relationships | Missing module in dependency diagram; module description attributes wrong responsibility; audit false positive on a widely-used function |
| **MINOR** | Documentation has style issues, unclear phrasing, or minor inaccuracies that don't mislead | Inconsistent capitalisation of module names; unclear sentence that could be reworded; minor version number wrong |
| **OBSERVATION** | Suggestions for improved documentation structure or additional content | "Consider adding a sequence diagram for the auth flow"; "The config section could list defaults" |

---

## 3a. Discovery Merge Agent Contract

When dual-provider discovery is enabled, both Claude and Codex produce independent discovery outputs. The Discovery Merge Agent combines them into a single canonical output.

**Agent type**: LLM invocation (same `AgentRunner` interface as other agents)

**Input**: Two complete discovery output JSONs (Claude + Codex), both conforming to the Discovery Output Schema (Section 9).

**Output**: A single merged discovery output JSON conforming to the same schema, with a `merge_log` field appended.

**Merge rules** (encoded in the merge agent's prompt):

1. **Modules**: Union by module path. When both providers describe the same module path, prefer the description with more detail (more fields populated, longer description). Backfill empty fields from the other provider's entry.
2. **Entry points**: Union by path. Deduplicate exact matches.
3. **Dependency graph edges**: Union of all edges. Deduplicate by `(from, to)` pair.
4. **Existing docs inventory**: Union by path. When both assess staleness differently, use the more pessimistic (higher staleness) assessment.
5. **Languages/frameworks**: Union, deduplicate by name (case-insensitive).
6. **Suggested scope**: Union `include` and `exclude` lists. Union `focus_areas`, deduplicate by module path prefix.
7. **Conflicts**: When providers produce irreconcilable descriptions of the same module (e.g., one says "handles auth" and the other says "handles logging"), the merge agent flags the conflict in the `merge_log` and includes both descriptions with attribution: `"[Claude]: handles auth | [Codex]: handles logging"`. The human gate at SCOPE displays flagged conflicts for resolution.

**Failure mode**: If the merge agent fails after retries, fall back to the Claude-only discovery output and log a warning.

**Timeout**: Uses `discovery_timeout_seconds` from config.

**Merge log schema** (appended to discovery output):

```json
{
  "merge_log": {
    "claude_modules": 15,
    "codex_modules": 12,
    "merged_modules": 18,
    "conflicts": [
      {
        "module_path": "internal/api",
        "field": "description",
        "claude_value": "HTTP handlers for workflow management",
        "codex_value": "REST API layer with WebSocket support",
        "resolution": "combined"
      }
    ],
    "dedup_count": 9
  }
}
```

---

## 3b. Drafter Combine Agent Contract

When dual-provider drafting is enabled, both Claude and Codex produce independent documentation sets. The Drafter Combine Agent merges them into a single canonical set.

**Agent type**: LLM invocation

**Input**: Two complete drafter output JSONs (Claude + Codex), both conforming to the Drafter Output Schema (Section 10), plus their associated draft artefact files.

**Output**: A single combined drafter output JSON conforming to the same schema, with combined artefact files.

**Combine rules**:

1. **As-implemented report**: The combine agent receives both reports and produces a unified report. For each module section, it selects the more detailed description and augments with unique details from the other. Contradictions are resolved by cross-referencing the discovery output (source of truth for what the code does).
2. **Architecture diagrams (Mermaid)**: For each diagram type (module dependency, call flow, data flow), the combine agent selects the more complete diagram as the base (more nodes/edges) and augments it with missing nodes/edges from the other provider. **Line-level merge of Mermaid syntax is NOT attempted.** After combining, a Mermaid syntax validation step confirms the output parses correctly. Invalid diagrams are replaced with the base diagram.
3. **Code audit**: Union of findings by `(file_path, line_number, type)` tuple. Deduplicate exact matches. When both flag the same location with different descriptions, prefer the more specific description.
4. **Doc updates**: Union by file path. When both suggest updating the same file, merge the `sections_changed` lists.

**Failure mode**: If the combine agent fails after retries, fall back to the Claude-only drafter output and log a warning.

**Timeout**: Uses `agent_timeout_seconds` from config.

---

## 3c. Writing Phase Specification

The writing phase uses a staging-then-move pattern for atomicity, with concurrency locking and drift detection.

### Atomic Writing via Staging Directory

1. **Stage**: All documentation files are written to `docs/.codedoc-staging/` first, mirroring the final directory structure.
2. **Validate**: Verify all expected files are present in staging and non-empty.
3. **Backup**: For each file that would be overwritten, create a timestamped backup: `{filename}.bak.{YYYYMMDD-HHMMSS}`.
4. **Move**: Atomically move each file from staging to its final location (`os.Rename` — atomic on same filesystem).
5. **Cleanup**: Remove the staging directory on success.

If the process crashes between steps 3 and 4, the staging directory contains the intended state and originals remain untouched. See "Error Transitions and Resume Semantics" in Section 2 for recovery.

### Concurrency: Docs Directory Lock

Multiple code-doc workflows may run concurrently against the same code path (different feature names, same repo). Discovery, drafting, and review phases run independently. The writing phase acquires an **exclusive lock** on the target `docs/` directory:

- Lock is implemented as a lock file: `docs/.codedoc-write.lock` containing the workflow feature name and PID.
- If the lock is held by another workflow, the current workflow queues at CD_WRITING and retries with exponential backoff (max wait: 5 minutes).
- If the lock file exists but the PID is dead (stale lock), the lock is broken and acquired.
- The lock is released after writing completes or on error.

### Drift Detection at Writing

Before writing, the system re-checks key file hashes from the discovery output against the current codebase state:

1. Compute hashes for all files that were analysed during discovery.
2. Compare against the hashes recorded in the discovery output.
3. If more than 20% of files have changed since discovery, **warn at HUMAN_GATE_FINAL** (or the equivalent point before writing): "Codebase has changed significantly since discovery. N files modified. Proceed with writing or re-run?"
4. If fewer than 20% changed, proceed without warning. Documentation is always a point-in-time snapshot.

### Manual Marker Preservation

When updating existing documentation files that contain `<!-- manual -->` / `<!-- /manual -->` markers:

1. **Extract**: Parse the file and extract all `<!-- manual -->...<!-- /manual -->` blocks with their byte offsets relative to section boundaries.
2. **Regenerate**: Produce new content for all non-manual sections.
3. **Re-insert**: Place extracted manual blocks at the same structural positions in the regenerated content. "Structural position" means: same parent heading level, same relative order among siblings.
4. **Conflict**: If the regenerated structure removes the section that contained a manual block, the manual block is appended to the end of the file with a warning comment: `<!-- WARNING: original section removed, manual content preserved -->`.

---

## 3d. Secret Sanitisation Specification

A mandatory post-drafting step that scans all documentation output for leaked secrets before any human review or writing occurs.

### Scan Patterns

The sanitisation agent scans all text content (markdown, JSON, Mermaid) for:

| Pattern Category | Examples | Detection Method |
|------------------|----------|------------------|
| API keys | `AKIA...`, `sk-...`, `ghp_...`, `glpat-...` | Regex: known prefix patterns |
| Tokens | Bearer tokens, JWT strings, OAuth tokens | Regex: base64 blocks > 40 chars, `eyJ` prefix |
| Connection strings | `postgres://`, `mongodb://`, `redis://`, `mysql://` | Regex: protocol prefixes with credentials |
| Passwords | `password=`, `passwd:`, `secret:` in config examples | Regex: key-value patterns |
| Private keys | `-----BEGIN RSA PRIVATE KEY-----` | Regex: PEM headers |
| PII patterns | Email addresses, phone numbers in code examples | Regex: common PII formats |

### Sanitisation Flow

1. After drafting completes (CD_DRAFTING → CD_SANITISING), scan all generated documentation files.
2. If **no secrets detected**: transition to CD_HUMAN_GATE_DRAFT.
3. If **secrets detected**:
   a. Redact each detected secret with `[REDACTED: <pattern_category>]` placeholder.
   b. Log each redaction with file path, line number, and pattern category.
   c. If the sanitisation agent is confident all secrets are redacted, transition to CD_HUMAN_GATE_DRAFT with the redaction log visible in the gate payload.
   d. If the sanitisation agent detects patterns it cannot safely redact (e.g., a secret woven into prose), transition back to CD_DRAFTING with instructions to regenerate the affected section without the secret.

### SEC Review Lens

The SEC lens in Group 4 provides a second layer of defence during adversarial review. It specifically checks for:
- Secrets that survived the automated sanitisation pass
- Real configuration values used in documentation examples (should use placeholder values)
- Source code comments containing sensitive information that were quoted verbatim

---

## 4. User Stories

### US-1: Full Codebase Documentation (P0 — Critical)

As a **tech lead**, I want to run a single command/button that analyses my entire codebase and produces comprehensive as-implemented documentation, so that new team members can onboard quickly and the team has an accurate reference of what the system actually does.

**Why P0**: This is the core capability — without it, the workflow has no purpose.

**Independent Test**: Start a code-doc workflow against a non-trivial codebase, let it complete through all stages, and verify the output docs directory contains an as-implemented report, architecture diagrams, and audit findings that accurately reflect the code.

**Acceptance Scenarios:**

1. **Given** a code path pointing to a Go project with 50+ source files, **When** I start a code-doc workflow in full mode, **Then** the discovery phase produces a structured inventory of all modules, entry points, dependencies, and existing documentation, with a `completion_status` indicating whether the inventory is complete or partial.

2. **Given** a confirmed discovery scope, **When** the drafting phase completes, **Then** the output includes: (a) an as-implemented report markdown file, (b) at least one architecture diagram in Mermaid format, (c) a code audit JSON file, and (d) a list of existing docs that need updating.

3. **Given** draft documentation that has passed secret sanitisation, **When** the adversarial review loop completes with a PASS verdict, **Then** all CRITICAL and MAJOR accuracy findings have been addressed and the documentation matches the code.

4. **Given** approved final documentation, **When** the writing phase executes, **Then** files are written to the target repo's `docs/` directory via the staging directory, existing stale docs are updated in place, and timestamped backups are created for overwritten files.

### US-2: Architecture Diagrams (P0 — Critical)

As a **developer**, I want automatically generated architecture diagrams that show module dependencies, call flows, and data flows, so that I can understand the system structure without reading every file.

**Why P0**: Architecture diagrams are a primary deliverable — visual understanding of the system is the most requested documentation artifact.

**Independent Test**: After workflow completion, verify that the `docs/` directory contains Mermaid diagrams covering module dependencies, at least one call flow, and at least one data flow, and that they render correctly.

**Acceptance Scenarios:**

1. **Given** a codebase with multiple packages/modules, **When** drafting completes, **Then** a module dependency diagram is produced showing all packages and their import relationships.

2. **Given** a codebase with HTTP handlers or CLI entry points, **When** drafting completes, **Then** at least one call flow diagram traces a request from entry point through to response.

3. **Given** a codebase with database access or external API calls, **When** drafting completes, **Then** a data flow diagram shows how data moves between components and external systems.

4. **Given** generated diagrams, **When** the Architecture Correctness (ARC) reviewer checks them, **Then** any diagram that misrepresents a dependency direction or omits a critical module is flagged as a MAJOR finding. ARC findings on diagrams MUST include the corrected Mermaid snippet.

5. **Given** dual-provider drafting produces two independent diagram sets, **When** the combine agent merges them, **Then** the merged diagrams are validated for Mermaid syntax correctness before proceeding to review.

### US-3: Dead Code and Stub Audit (P1 — High)

As a **tech lead**, I want an automated audit that identifies dead code, stubs, TODO/FIXME comments, empty catch blocks, and functions with no callers, so that I can prioritize cleanup and track technical debt.

**Why P1**: High value for maintainability, but the workflow could function without it.

**Independent Test**: Run the workflow against a codebase with known dead code, TODO comments, and empty catch blocks. Verify the audit output identifies them with correct file paths and line numbers.

**Acceptance Scenarios:**

1. **Given** a codebase with functions that have no callers (not exported, not referenced), **When** the audit phase runs, **Then** each unreachable function is listed with its file path, line number, and a "suspected dead code" classification.

2. **Given** a codebase with TODO and FIXME comments, **When** the audit runs, **Then** each is extracted with file path, line number, and the comment text.

3. **Given** a codebase with empty catch/error blocks (e.g. `catch {}`, `if err != nil { }` with no body), **When** the audit runs, **Then** each is flagged with file path and line number.

4. **Given** a codebase with exported types/functions that are defined but never used from any entry point, **When** the audit runs, **Then** they are flagged as "non-wired-in components".

5. **Given** audit findings, **When** the AUD reviewer verifies them, **Then** false positives are identified and removed, and the final audit JSON reflects only confirmed findings.

6. **Given** the audit output, **Then** it is available as both structured JSON (for tooling) and as a findings list with severities (for human review).

### US-4: Dual-Provider Execution (P1 — High)

As a **user**, I want discovery, drafting, and review to run with both Claude and Codex in parallel, so that the documentation benefits from two independent perspectives and the merge produces more comprehensive output.

**Why P1**: Follows the established dual-provider pattern; the system should work single-provider too.

**Independent Test**: Start a workflow with Codex enabled, verify both providers run in parallel, and the merged output is richer than either individual output.

**Acceptance Scenarios:**

1. **Given** `enable_codex_codedoc_discovery: true` in config and Codex CLI available, **When** discovery runs, **Then** both Claude and Codex analyse the codebase in parallel and the Discovery Merge Agent (Section 3a) combines outputs using LLM-based merge with conflict flagging.

2. **Given** one provider fails during discovery, **When** the other succeeds, **Then** the workflow continues with single-provider output and logs a warning.

3. **Given** both providers fail during discovery, **Then** the workflow escalates.

4. **Given** `enable_codex_codedoc_drafting: true` and dual-provider drafting produces two independent documentation sets, **When** the Drafter Combine Agent (Section 3b) runs, **Then** the output preserves the more detailed description from each provider for each module, deduplicates by module path, and flags irreconcilable conflicts in the merge log. Architecture diagrams are merged by selecting the more complete diagram as base and augmenting with missing elements, with Mermaid syntax validation after merge.

### US-5: Incremental Mode (P2 — Medium)

As a **developer**, I want to re-run the documentation workflow in incremental mode to update only what has changed since the last run, so that I don't wait for a full analysis every time.

**Why P2**: Nice to have; full mode is always correct, incremental is an optimisation.

**Independent Test**: Run full mode, make a change to one module, run incremental mode, verify only the changed module's documentation is regenerated.

**Acceptance Scenarios:**

1. **Given** a previous full documentation run exists in `docs/` with a valid `.codedoc-manifest.json`, **When** I start a code-doc workflow with `mode: incremental`, **Then** the discovery phase compares the current codebase state against the manifest and identifies only changed/added/removed modules.

2. **Given** incremental discovery identifies 3 changed modules out of 20 total, **When** drafting runs, **Then** only documentation for those 3 modules is regenerated; other docs are preserved unchanged.

3. **Given** incremental mode, **When** a structural change affects the module dependency graph, **Then** the architecture diagrams are regenerated in full (since they depend on the whole codebase).

4. **Given** a corrupted or unparseable `.codedoc-manifest.json`, **When** incremental mode is requested, **Then** the system falls back to full mode and logs a warning with the parse error.

### US-6: In-Situ Documentation Writing (P1 — High)

As a **developer**, I want the final approved documentation written directly to my repository's `docs/` directory, updating stale existing files and creating new ones, so that the documentation lives alongside the code.

**Why P1**: The core value proposition is in-situ documentation, not a separate report.

**Independent Test**: After workflow completion, verify that `docs/` in the target repo contains the full documentation set and any previously stale files have been updated.

**Acceptance Scenarios:**

1. **Given** a target repo with an existing `docs/README.md` that is stale and contains sections wrapped in `<!-- manual -->` / `<!-- /manual -->` markers, **When** the writing phase executes, **Then** `docs/README.md` is updated with current information while preserving content between the manual markers at their structural positions (see Section 3c).

2. **Given** no existing `docs/` directory, **When** the writing phase executes, **Then** the directory is created with the full documentation set.

3. **Given** approved documentation, **When** writing completes, **Then** the following files exist in `docs/`:
   - `as-implemented-report.md` — full system description
   - `architecture/module-dependencies.md` — with embedded Mermaid diagram
   - `architecture/call-flows.md` — with embedded Mermaid diagrams
   - `architecture/data-flows.md` — with embedded Mermaid diagram
   - `audit/code-audit.json` — structured audit findings
   - `audit/code-audit-report.md` — human-readable audit report

4. **Given** the writing phase, **When** it encounters a file it would overwrite, **Then** it creates a timestamped backup at `{filename}.bak.{YYYYMMDD-HHMMSS}` before overwriting.

5. **Given** the writing phase, **When** another code-doc workflow is already writing to the same `docs/` directory, **Then** the current workflow queues and retries with exponential backoff until the lock is released (max wait 5 minutes before escalating).

### US-7: Human Gates (P0 — Critical)

As a **tech lead**, I want human review gates at scope confirmation, draft review, and final approval, so that I maintain control over what documentation is produced and written to my repo.

**Why P0**: Without human gates, automated documentation could write incorrect or unwanted content to the repository.

**Independent Test**: Start a workflow, verify it pauses at each gate, responds to user input, and does not proceed without explicit approval.

**Acceptance Scenarios:**

1. **Given** discovery completes, **When** HUMAN_GATE_SCOPE is reached, **Then** the gate displays: module inventory with completion status (complete/partial), dependency overview, existing doc inventory, suggested scope, and any merge conflicts from dual-provider discovery. The user can confirm, adjust scope (include/exclude modules), or cancel.

2. **Given** drafting and sanitisation complete, **When** HUMAN_GATE_DRAFT is reached, **Then** the gate displays: list of generated files, summary statistics (module count, diagram count, audit finding count), any redaction log from sanitisation, and the user can approve, request re-draft with free-text feedback, or cancel. Re-draft requests pass the user's comment to the drafter as additional context alongside the previous draft. Maximum `max_gate_draft_redrafts` re-drafts (default 2) before the gate forces approve-or-cancel.

3. **Given** the review loop completes, **When** the judge verdict is PASS but merged findings contain one or more unresolved findings with severity CRITICAL or MAJOR, **Then** CD_HUMAN_GATE_FINAL is reached. The gate displays remaining findings and the user can approve (accept docs with known issues), reject (escalate), or request another review round. When all findings are MINOR or resolved, the judge transitions directly to CD_WRITING.

### US-8: Dashboard Integration (P1 — High)

As a **user**, I want to see code-doc workflows alongside spec and code review workflows in the dashboard, so that I can track progress across all workflow types.

**Why P1**: Dashboard is the primary user interface; workflows not visible in the dashboard are effectively invisible.

**Independent Test**: Start a code-doc workflow and verify it appears in the dashboard with correct pipeline stages, progress indicators, and workflow type badge.

**Acceptance Scenarios:**

1. **Given** a running code-doc workflow, **When** I view the dashboard, **Then** it appears in the workflow list with a `CD` type badge alongside `SPEC` and `CR` badges.

2. **Given** a code-doc workflow at any state, **When** I view its detail page, **Then** the pipeline visualisation shows the correct stages with the current state highlighted.

---

## 5. Edge Cases

| Edge Case | Expected Behaviour |
|-----------|-------------------|
| Empty codebase (no source files) | Discovery reports empty inventory, workflow escalates with message "no source files found" |
| Binary-only project (no readable source) | Discovery reports zero analysable files, escalates |
| Monorepo with 1000+ packages | Discovery runs with `discovery_timeout_seconds` and reports `completion_status: "partial"` with reason if inventory is incomplete. Scope gate displays the partial status prominently. |
| Codebase with no existing docs | Full mode only — incremental mode falls back to full |
| Codebase with extensive existing docs | Existing docs are inventoried, compared against code, and updated in place |
| Generated code (protobuf, swagger) | Flagged as "generated" in inventory, excluded from dead code audit, included in architecture diagrams |
| Circular dependencies | Flagged as a MAJOR architecture finding, diagram shows the cycle |
| Mixed-language project | Discovery identifies all languages, documentation covers each; diagrams show cross-language calls |
| Target docs/ directory is read-only | Writing phase fails gracefully, escalates with error message |
| Previous documentation run exists but code has diverged significantly | Full mode treats it as a fresh run; incremental mode may detect too many changes and suggest full mode instead |
| Corrupted `.codedoc-manifest.json` | Incremental mode falls back to full mode, logs warning with parse error |
| Concurrent workflows on same code path | Discovery/drafting/review run independently. Writing phase acquires exclusive lock on docs/ directory; second workflow queues. |
| Codebase changes during long workflow | Drift detection at writing phase warns if >20% of files changed since discovery |
| Source code contains hardcoded secrets | Sanitisation step detects and redacts before human review; SEC lens provides second layer |
| Stale lock file from crashed process | Lock file PID is checked; stale locks are broken and re-acquired |
| Discovery timeout on large codebase | Discovery reports `completion_status: "partial"` with reason; human gate shows partial status |
| Config with zero/negative timeouts | Validation at startup rejects invalid config values |

---

## 6. Behavioral Contract

### Primary Flows

- When a code path is provided and mode is "full", the system analyses the entire codebase and produces complete documentation.
- When mode is "incremental" and a valid `.codedoc-manifest.json` exists, the system analyses only changes and updates affected documentation.
- When dual-provider is enabled, both providers run in parallel and outputs are merged by LLM-based merge agents with conflict flagging.
- When a human confirms scope at the gate, drafting proceeds with the confirmed scope.
- When drafting completes, all output is scanned for secrets before human review.
- When the review loop produces a PASS verdict with no unresolved CRITICAL/MAJOR findings, documentation proceeds directly to writing.
- When the writing phase runs, files are staged first, then moved atomically.

### Error Flows

- When both providers fail during any phase, the workflow escalates.
- When one provider fails, the survivor's output is used with a warning.
- When the merge/combine agent fails, the Claude-only output is used with a warning.
- When the judge renders BLOCK, the workflow escalates to human gate.
- When a circuit breaker fires (cost, time, rounds), the workflow escalates.
- When the target directory is unwritable, the writing phase fails and escalates.
- When an unrecoverable error occurs in any non-terminal state, the workflow transitions to CD_ERROR.
- When resuming from CD_ERROR, the system detects existing artefacts and resumes from the appropriate state.
- When the sanitisation step detects un-redactable secrets, the workflow returns to drafting.
- When the `.codedoc-manifest.json` is corrupted, incremental mode falls back to full mode.

### Boundary Flows

- When incremental mode detects no changes, the workflow completes immediately with "no changes detected".
- When the codebase exceeds context limits, discovery reports partial completion status and suggests scope reduction at the gate.
- When a correction loop at scope gate reaches max corrections, the workflow proceeds with the last confirmed scope.
- When a re-draft loop at draft gate reaches `max_gate_draft_redrafts`, the gate forces approve-or-cancel.
- When another workflow holds the docs/ write lock, the current workflow queues at CD_WRITING.
- When codebase drift exceeds 20% at writing time, the system warns at the final gate.

---

## 7. Explicit Non-Behaviors

- The system must NOT modify source code files — only documentation files in `docs/`. **Reason**: The workflow's purpose is documentation, not code modification. Source changes are out of scope and dangerous.
- The system must NOT delete existing documentation without creating a timestamped backup first. **Reason**: Prevents accidental data loss.
- The system must NOT execute code, run tests, or invoke build tools in the target repository. **Reason**: Security boundary — the workflow reads and documents, it does not execute.
- The system must NOT include credentials, secrets, or sensitive configuration values in documentation output. **Enforcement**: Automated sanitisation step (Section 3d) plus SEC review lens. **Reason**: Secrets committed to `docs/` are a production security incident.
- The system must NOT auto-approve documentation without human gate confirmation. **Reason**: Human oversight is a core design principle.
- The system must NOT produce documentation that describes intended/planned features — only as-implemented behavior. **Reason**: The purpose is accurate "as-is" documentation.
- The system must NOT write files outside the target repo's `docs/` directory (and existing doc files at their current paths). **Reason**: Blast radius containment.
- The system must NOT require authentication for API access in v1. **Reason**: The server runs locally on the user's machine, same as existing spec and code review workflows. Network access control is the user's responsibility. Authentication is deferred to a future version if the server is exposed to a network.

---

## 8. Integration Boundaries

### Claude CLI

- **Data in**: Prompt (codebase analysis instructions + context)
- **Data out**: Structured JSON output (discovery, draft, review, revision, judge outputs)
- **Contract**: `claude -p <prompt> --output-format json --verbose [--json-schema <schema>]`
- **Failure**: Timeout, exit code != 0, invalid JSON → retry with validation errors → escalate
- **Dev approach**: Real CLI

### Codex CLI

- **Data in**: Prompt via stdin
- **Data out**: Structured output to file via `--output-last-message`
- **Contract**: `echo <prompt> | codex -m <model> --output-last-message <path>`
- **Failure**: Same as Claude — retry → escalate; single-provider fallback
- **Dev approach**: Real CLI, auto-detected on startup

### Target Repository Filesystem

- **Data in**: Source files read during discovery and drafting
- **Data out**: Documentation files written to `docs/` directory via staging
- **Contract**: Standard filesystem read/write; exclusive lock during writing
- **Failure**: Permission denied, disk full, lock contention → escalate with error
- **Dev approach**: Real filesystem

### Optional Static Analysis Tools

- **Data in**: Source files in the target repository
- **Data out**: Analysis results (dead code, unused exports, type information)
- **Contract**: Auto-detected at discovery time. If available, invoked as subprocesses. Results augment (not replace) LLM analysis.
- **Supported tools** (optional, not required):
  - Go: `go vet`, `staticcheck`, `gopls` (for call graph)
  - Python: `pylint`, `pyflakes`
  - JavaScript/TypeScript: `eslint`, `tsc --noEmit`
- **Failure**: Tool not found → skip silently, proceed LLM-only. Tool exits with error → log warning, proceed LLM-only.
- **Dev approach**: Real tools when available; LLM-only baseline always works

---

## 9. Discovery Output Schema

```json
{
  "schema_version": "1.0",
  "agent": "codedoc-discovery",
  "mode": "full|incremental",
  "completion_status": {
    "status": "complete|partial",
    "reason": "Inventory complete"
  },
  "tools_used": [
    { "tool": "staticcheck", "version": "2024.1", "status": "success|failed|not_found" }
  ],
  "languages": [
    { "language": "Go", "file_count": 45, "line_count": 12000 }
  ],
  "frameworks": ["net/http", "gRPC"],
  "modules": [
    {
      "path": "internal/api",
      "name": "api",
      "description": "HTTP handlers and WebSocket hub",
      "files": 8,
      "lines": 2400,
      "exports": ["HandleStart", "HandleStatus"],
      "dependencies": ["internal/specworkflow"],
      "has_tests": true,
      "test_files": 2,
      "content_hash": "sha256:abc123..."
    }
  ],
  "entry_points": [
    {
      "path": "cmd/specworkflow/main.go",
      "type": "cli",
      "description": "Main CLI entry point"
    }
  ],
  "dependency_graph": {
    "edges": [
      { "from": "cmd/specworkflow", "to": "internal/api" },
      { "from": "internal/api", "to": "internal/specworkflow" }
    ]
  },
  "existing_docs": [
    {
      "path": "README.md",
      "last_modified": "2026-03-15",
      "estimated_staleness": "high|medium|low|current",
      "topics": ["overview", "quickstart"]
    }
  ],
  "test_coverage_overview": {
    "test_file_count": 32,
    "packages_with_tests": 3,
    "packages_without_tests": 1,
    "test_frameworks": ["testing"]
  },
  "suggested_scope": {
    "include": ["internal/", "cmd/"],
    "exclude": ["vendor/", ".git/", "node_modules/"],
    "focus_areas": ["internal/specworkflow — core workflow engine"]
  },
  "incremental_changes": null,
  "merge_log": null
}
```

The `completion_status` field is displayed prominently at HUMAN_GATE_SCOPE so the human knows whether the inventory is complete or truncated. When `status` is `"partial"`, the `reason` field explains why (e.g., "Discovery timed out after 1200s; 85 of ~120 modules inventoried").

The `content_hash` field on each module records a SHA-256 hash of the module's source files at discovery time. This is used for: (a) incremental mode delta computation, (b) drift detection at writing time (Section 3c).

The `tools_used` field records which optional static analysis tools were available and their status. This is informational — displayed at the human gate to indicate analysis confidence.

### Incremental Changes (when mode = "incremental")

```json
{
  "incremental_changes": {
    "added_modules": ["internal/newpkg"],
    "modified_modules": ["internal/api"],
    "removed_modules": [],
    "added_files": 3,
    "modified_files": 7,
    "removed_files": 1,
    "recommendation": "incremental|full",
    "reason": "7 files modified across 2 modules — incremental is appropriate"
  }
}
```

---

## 10. Drafter Output Schema

```json
{
  "schema_version": "1.0",
  "agent": "codedoc-drafter",
  "as_implemented_report": {
    "file_path": "docs/as-implemented-report.md",
    "sections": ["overview", "modules", "entry_points", "configuration", "error_handling"]
  },
  "architecture_diagrams": [
    {
      "file_path": "docs/architecture/module-dependencies.md",
      "diagram_type": "module_dependency",
      "mermaid_content": "graph TD; ...",
      "mermaid_valid": true
    },
    {
      "file_path": "docs/architecture/call-flows.md",
      "diagram_type": "call_flow",
      "mermaid_content": "sequenceDiagram; ...",
      "mermaid_valid": true
    },
    {
      "file_path": "docs/architecture/data-flows.md",
      "diagram_type": "data_flow",
      "mermaid_content": "flowchart LR; ...",
      "mermaid_valid": true
    }
  ],
  "code_audit": {
    "json_file_path": "docs/audit/code-audit.json",
    "report_file_path": "docs/audit/code-audit-report.md",
    "findings": [
      {
        "id": "AUDIT-001",
        "type": "dead_code|stub|todo|fixme|empty_catch|non_wired",
        "severity": "major|minor|observation",
        "file_path": "internal/legacy/old_handler.go",
        "line_number": 42,
        "symbol": "handleLegacyRequest",
        "description": "Function has no callers and is not exported",
        "evidence": "grep for 'handleLegacyRequest' returns only the definition"
      }
    ],
    "summary": {
      "dead_code": 3,
      "stubs": 1,
      "todos": 12,
      "fixmes": 2,
      "empty_catches": 0,
      "non_wired": 1
    }
  },
  "doc_updates": [
    {
      "file_path": "README.md",
      "action": "update",
      "sections_changed": ["Architecture", "Quick Start"],
      "reason": "Architecture section references removed package"
    }
  ],
  "structural_summary": {
    "report_sections": 5,
    "diagram_count": 3,
    "audit_finding_count": 19,
    "doc_updates": 2,
    "modules_documented": 15,
    "entry_points_documented": 2
  }
}
```

The `mermaid_valid` field indicates whether the Mermaid content passed syntax validation. After dual-provider combine, this field is re-validated on the merged diagrams.

---

## 11. Reviewer Output Schema

Same structure as the spec workflow's `ReviewerOutput`, with adapted lens codes:

```json
{
  "schema_version": "1.0",
  "agent": "codedoc-reviewer",
  "round": 1,
  "lenses_applied": ["ACC", "CUR"],
  "findings": [
    {
      "id": "ACC-001",
      "description": "Module description for internal/api says 'handles authentication' but the package contains no auth logic",
      "severity": "critical|major|minor|observation",
      "status": "open",
      "impact": "Misleading documentation could cause developers to look for auth code in the wrong package",
      "recommendation": "Update module description to 'HTTP handlers and WebSocket hub for workflow management'",
      "lens": "ACC",
      "affected_section": "as-implemented-report.md#modules",
      "affected_file": "docs/as-implemented-report.md"
    }
  ],
  "structural_integrity": {
    "performed": true,
    "checks": [
      { "name": "mermaid_syntax", "passed": true, "details": "All 3 diagrams parse successfully" },
      { "name": "cross_references", "passed": false, "details": "docs/architecture/call-flows.md references non-existent module 'internal/auth'" },
      { "name": "audit_json_schema", "passed": true, "details": "code-audit.json validates against schema" }
    ]
  }
}
```

### Finding Status Field

The `status` field tracks finding lifecycle across review rounds:

| Status | Meaning |
|--------|---------|
| `open` | Finding is new or unaddressed |
| `resolved` | The revision agent addressed this finding |
| `wontfix` | Finding acknowledged but deliberately not addressed (with rationale) |

- New findings are created with `status: "open"`.
- The revision agent sets `status: "resolved"` for findings it addressed, copying the finding with the updated status.
- The judge uses `status` to compute convergence: only `open` findings of severity CRITICAL or MAJOR block a PASS verdict.

---

## 12. Configuration

```yaml
# Code documentation workflow config
codedoc:
  max_rounds: 3                            # Maximum review/revise iterations
  min_rounds: 1                            # Minimum iterations before acceptance
  max_cost_usd: 50.0                       # Cost budget
  max_wall_clock_minutes: 90               # Time budget
  max_retries: 2                           # Agent retry attempts (validation+retry)
  max_gate_corrections: 3                  # Max corrections at scope gate
  max_gate_draft_redrafts: 2               # Max re-drafts at draft gate
  staleness_threshold: 2                   # Consecutive stale rounds before halt
  agent_timeout_seconds: 600               # Per-agent timeout (drafting, review, etc.)
  discovery_timeout_seconds: 1200          # Discovery-specific timeout (large codebases need more)
  reviewer_timeout_seconds: 300            # Per-reviewer timeout
  enable_codex_codedoc_discovery: false    # Dual-provider for discovery
  enable_codex_codedoc_drafting: false     # Dual-provider for drafting
  enable_codex_reviewers: true             # Dual-provider for review
  codex_model: "gpt-5.4"                  # Codex model
  default_mode: "full"                     # Default mode: full or incremental
  backup_before_write: true                # Create timestamped .bak files before overwriting
  docs_output_dir: "docs"                  # Output directory relative to code path
  drift_warning_threshold: 0.20            # Fraction of files changed to trigger drift warning (0.0-1.0)
  write_lock_timeout_seconds: 300          # Max wait for docs/ write lock before escalating
```

### Configuration Validation

At startup, the system validates config values and rejects invalid configurations:

| Field | Validation |
|-------|-----------|
| `max_rounds` | Must be >= 1 |
| `min_rounds` | Must be >= 1 and <= `max_rounds` |
| `max_cost_usd` | Must be > 0 |
| `max_wall_clock_minutes` | Must be > 0 |
| `max_retries` | Must be >= 0 |
| `agent_timeout_seconds` | Must be > 0 |
| `discovery_timeout_seconds` | Must be > 0 |
| `reviewer_timeout_seconds` | Must be > 0 |
| `drift_warning_threshold` | Must be >= 0.0 and <= 1.0 |
| `write_lock_timeout_seconds` | Must be > 0 |

---

## 13. API Endpoints

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/codedoc/start` | Start code-doc workflow with code path and mode |
| GET | `/api/codedoc/{feature}/status` | Poll workflow status |
| POST | `/api/codedoc/{feature}/gate` | Submit gate decision (approve/correct/cancel) |
| POST | `/api/codedoc/{feature}/cancel` | Cancel running workflow |
| POST | `/api/codedoc/{feature}/resume` | Resume from ERROR state (artefact-detected restart) |
| POST | `/api/codedoc/{feature}/reset` | Delete feature directory |
| POST | `/api/codedoc/{feature}/rewind` | Rewind to target state |

**Authentication**: None in v1. See Explicit Non-Behaviors (Section 7) for rationale.

### Start Request

```json
{
  "feature_name": "my-project-docs",
  "code_path": "/path/to/repository",
  "mode": "full",
  "description": "Document the adversarial spec system"
}
```

---

## 14. Dashboard Integration

### Pipeline Stages

```javascript
var CD_PIPELINE_STAGES = [
    { state: "CD_INIT", label: "Init", gate: false },
    { state: "CD_DISCOVERY", label: "Discover", gate: false },
    { state: "CD_HUMAN_GATE_SCOPE", label: "Scope", gate: true },
    { state: "CD_DRAFTING", label: "Draft", gate: false },
    { state: "CD_SANITISING", label: "Sanitise", gate: false },
    { state: "CD_HUMAN_GATE_DRAFT", label: "Review Draft", gate: true },
    { state: "CD_REVIEWING", label: "Review", gate: false },
    { state: "CD_REVISING", label: "Revise", gate: false },
    { state: "CD_JUDGING", label: "Judge", gate: false },
    { state: "CD_HUMAN_GATE_FINAL", label: "Final", gate: true },
    { state: "CD_WRITING", label: "Write", gate: false },
    { state: "CD_COMPLETE", label: "Done", gate: false }
];
```

### Workflow Type

Type badge: `CD` (displayed alongside SPEC and CR badges in the workflow list).

---

## 15. Workspace Layout

```
workspace/codedoc/{feature}/
  workflow-state.json                    Persisted workflow state
  workflow-log.jsonl                     Structured workflow log

  # Discovery
  discovery-output.json                  Canonical discovery output
  discovery-output-claude-v{N}.json      Per-provider (dual-provider)
  discovery-output-codex-v{N}.json       Per-provider (dual-provider)
  discovery-output-merged-v{N}.json      Agent-merged (dual-provider)

  # Gates
  gate-scope-corrections.json            Scope corrections from human
  gate-scope-corrections-{N}.json        Versioned corrections
  gate-draft-redraft-{N}.json            Re-draft request with user comment
  human-comments.json                    Free-text reviewer comments

  # Drafting
  drafter-output.json                    Canonical drafter output
  drafter-output-claude-v{N}.json        Per-provider (dual-provider)
  drafter-output-codex-v{N}.json         Per-provider (dual-provider)
  drafter-output-combined-v{N}.json      Combined (dual-provider)
  draft-v{N}/                            Draft artefact files per version
    as-implemented-report.md
    architecture/module-dependencies.md
    architecture/call-flows.md
    architecture/data-flows.md
    audit/code-audit.json
    audit/code-audit-report.md

  # Sanitisation
  sanitisation-report-v{N}.json          Redaction log per draft version

  # Review loop
  review-{a,b,c,d}-round-{N}.json       Reviewer outputs per round
  merged-findings-round-{N}.json         Merged findings per round
  revision-round-{N}.json               Revision output per round
  judge-round-{N}.json                  Judge output per round
```

---

## 16. Functional Requirements

| ID | Requirement |
|----|-------------|
| FR-001 | System MUST accept a code path and mode (full/incremental) to start a code-doc workflow |
| FR-002 | System MUST analyse the codebase and produce a structured discovery output containing module inventory, dependency graph, entry points, test coverage overview, existing doc inventory, language/framework detection, and completion status |
| FR-003 | System MUST support dual-provider discovery (Claude + Codex) with LLM-based merge agent (Section 3a) and single-provider fallback |
| FR-004 | System MUST present discovery output at HUMAN_GATE_SCOPE for human confirmation before drafting, including completion status and any merge conflicts |
| FR-005 | System MUST produce an as-implemented report describing all documented modules, their responsibilities, public APIs, and interactions |
| FR-006 | System MUST produce Mermaid architecture diagrams: module dependency graph, call flow diagrams, and data flow diagrams, each in a thematically named separate file, with syntax validation |
| FR-007 | System MUST produce a code audit with findings for dead code, stubs, TODO/FIXME comments, empty catch blocks, and non-wired-in components, available as both structured JSON and human-readable report |
| FR-008 | System MUST identify and update existing stale documentation files in the target repository |
| FR-009 | System MUST run an adversarial review loop with 4 reviewer groups (9 lenses) checking accuracy, completeness, architecture correctness, audit quality, and security |
| FR-010 | System MUST support dual-provider review (Claude + Codex) with findings merge and deduplication |
| FR-011 | System MUST apply the validation+retry pattern to all JSON-producing agents |
| FR-012 | System MUST enforce convergence via judge verdicts (PASS/REVISE/BLOCK) with anti-gaming checks |
| FR-013 | System MUST present final documentation at HUMAN_GATE_FINAL when unresolved CRITICAL or MAJOR findings remain in the merged findings |
| FR-014 | System MUST write approved documentation to the target repo's `docs/` directory via staging directory, creating timestamped backups before overwriting |
| FR-015 | System MUST NOT modify source code files — only documentation |
| FR-016 | System MUST support incremental mode that analyses only changed modules since the last documentation run, using `.codedoc-manifest.json` for delta computation |
| FR-017 | System MUST integrate with the existing dashboard alongside spec and code review workflows |
| FR-018 | System MUST implement circuit breakers for cost, time, round count, and staleness |
| FR-019 | System MUST support workflow rewind to any agent state |
| FR-020 | System MUST support smart restart with artefact detection when rewinding to discovery |
| FR-021 | System MUST persist workflow state for crash recovery |
| FR-022 | System MUST emit WebSocket events for real-time dashboard updates |
| FR-023 | System SHOULD produce a draft review gate (HUMAN_GATE_DRAFT) before entering the adversarial review loop |
| FR-024 | System MUST scan all documentation output for secret patterns (API keys, tokens, passwords, connection strings, PII) and redact them before human review. The writing phase MUST NOT proceed if unredacted secrets are detected. |
| FR-025 | System MUST support CD_ERROR transitions from any non-terminal state, and resume from CD_ERROR via artefact detection (Section 2) |
| FR-026 | System MUST write documentation via staging directory with atomic move, ensuring partial writes do not corrupt the docs/ directory |
| FR-027 | System MUST acquire an exclusive lock on the docs/ directory during the writing phase to prevent concurrent write conflicts |
| FR-028 | System MUST classify review findings using the severity rubric (Section 3) — CRITICAL, MAJOR, MINOR, OBSERVATION — with documented definitions |
| FR-029 | System MUST support dual-provider drafting with LLM-based combine agent (Section 3b) and single-provider fallback |
| FR-030 | System MUST validate Mermaid diagram syntax after generation and after dual-provider combine, replacing invalid diagrams with the base provider's diagram |
| FR-031 | System MUST detect codebase drift at writing time by comparing file hashes against discovery-time hashes, warning when drift exceeds `drift_warning_threshold` |
| FR-032 | System MUST validate configuration at startup and reject invalid values (Section 12) |
| FR-033 | System SHOULD auto-detect and use optional static analysis tools to augment LLM-based discovery when available |

---

## 17. Success Criteria

| ID | Criterion |
|----|-----------|
| SC-001 | A full code-doc workflow completes against the adversarial_spec_system codebase (50+ Go files) in under 90 minutes |
| SC-002 | The as-implemented report covers at least 90% of packages in the target codebase |
| SC-003 | Architecture diagrams are valid Mermaid that renders without syntax errors |
| SC-004 | The code audit achieves at least 80% precision (no more than 20% false positives) and at least 60% recall against a manually curated list of 10 known dead functions in a test codebase. Measured as: precision = true positives / (true positives + false positives); recall = true positives / total known dead functions. |
| SC-005 | The adversarial review loop produces at least 1 finding that improves documentation accuracy (verified by human) |
| SC-006 | Dual-provider mode produces output with higher module coverage (measured as: modules documented / total modules) and more diagram nodes than single-provider mode when run against the same codebase |
| SC-007 | Incremental mode completes in under 50% of the time of full mode when fewer than 20% of modules changed |
| SC-008 | All documentation files are written to `docs/` in the target repo via staging directory with timestamped backups of overwritten files |
| SC-009 | Workflow survives server restart and resumes from persisted state via artefact detection |
| SC-010 | Dashboard shows the code-doc workflow alongside spec and code review workflows with correct pipeline stages |
| SC-011 | No documentation output contains detectable secrets (API keys, tokens, passwords, connection strings) after the sanitisation step |

---

## 18. Traceability Matrix

| Requirement | User Story | BDD Scenarios | Test Coverage |
|-------------|-----------|---------------|---------------|
| FR-001 | US-1 | US-1 Scenario 1 | Config parsing, start handler |
| FR-002 | US-1 | US-1 Scenario 1 | Discovery output validation, completion_status |
| FR-003 | US-4 | US-4 Scenarios 1-3 | Dual dispatch, merge agent, fallback |
| FR-004 | US-7 | US-7 Scenario 1 | Gate handler, event emission, merge conflict display |
| FR-005 | US-1 | US-1 Scenario 2 | Drafter output validation |
| FR-006 | US-2 | US-2 Scenarios 1-3, 5 | Diagram generation, Mermaid validation |
| FR-007 | US-3 | US-3 Scenarios 1-6 | Audit output, JSON + report |
| FR-008 | US-6 | US-6 Scenarios 1, 3 | Doc update detection, writing |
| FR-009 | US-1 | US-1 Scenario 3 | Review dispatch, merge, convergence |
| FR-010 | US-4 | US-4 Scenarios 1-3 | Dual review dispatch |
| FR-011 | US-1 | US-1 Scenarios 2-3 | Validation+retry tests |
| FR-012 | US-1 | US-1 Scenario 3 | Judge output, convergence checks |
| FR-013 | US-7 | US-7 Scenario 3 | Gate handler, severity-based condition |
| FR-014 | US-6 | US-6 Scenarios 2-4 | File writing, timestamped backup, staging |
| FR-015 | US-6 | (non-behavior) | Negative test: no source mods |
| FR-016 | US-5 | US-5 Scenarios 1-4 | Incremental discovery, delta, manifest fallback |
| FR-017 | US-8 | US-8 Scenarios 1-2 | Dashboard integration, pipeline stages, CD badge |
| FR-018 | US-1 | Edge cases | Circuit breaker tests |
| FR-019 | US-1 | (inherited from spec wf) | Rewind tests |
| FR-020 | US-1 | (inherited from spec wf) | Smart restart tests |
| FR-021 | US-1 | (inherited from spec wf) | Persistence tests |
| FR-022 | US-8 | US-8 Scenarios 1-2 | Event emission tests |
| FR-023 | US-7 | US-7 Scenario 2 | Gate handler, re-draft with feedback |
| FR-024 | US-1 | US-1 Scenario 3 | Sanitisation scan, redaction, SEC lens |
| FR-025 | US-1 | Error transitions | CD_ERROR transitions, resume artefact detection |
| FR-026 | US-6 | US-6 Scenario 4 | Staging directory, atomic move, crash recovery |
| FR-027 | US-6 | US-6 Scenario 5 | Write lock acquisition, queue, stale lock handling |
| FR-028 | US-1 | US-1 Scenario 3 | Severity rubric enforcement in reviewer prompts |
| FR-029 | US-4 | US-4 Scenario 4 | Combine agent, merge rules, fallback |
| FR-030 | US-2 | US-2 Scenarios 4-5 | Mermaid validation, invalid diagram replacement |
| FR-031 | US-6 | US-6 Scenario 1 | Drift detection, hash comparison, threshold warning |
| FR-032 | US-1 | Edge cases | Config validation at startup |
| FR-033 | US-3 | US-3 Scenarios 1, 4 | Tool detection, augmented analysis |

---

## 19. Resolved Design Decisions

| # | Decision | Resolution |
|---|----------|------------|
| 1 | Manual section preservation | Use `<!-- manual -->` / `<!-- /manual -->` comment markers in documentation files. Content between these markers is preserved verbatim during updates. The writing phase extracts all manual blocks with their structural positions, regenerates surrounding content, and re-inserts manual blocks at the same positions relative to their parent heading level. If the section containing a manual block is removed, the block is appended to the end of the file with a warning comment. |
| 2 | Call flow diagram depth | Cross-module boundaries only. Diagrams show how modules interact (function calls that cross package boundaries), not internal implementation details. This keeps diagrams readable and maintainable. |
| 3 | Dead code audit scope | Test files (`*_test.go`, `test_*.py`, etc.) are excluded from dead code analysis. Only production code functions/types with no callers from any production entry point are flagged. Test helpers called only from test files are not flagged. |
| 4 | Staleness detection for existing docs | Content comparison: the discovery agent checks whether symbols, APIs, module names, and function signatures referenced in existing documentation still exist in the codebase. A doc is "stale" when it references entities that have been renamed, removed, or significantly changed. File modification time is not used. |
| 5 | Incremental mode detection | A `.codedoc-manifest.json` file is written to `docs/` during CD_WRITING. See Section 19a for schema. Incremental mode diffs current module hashes against the manifest to identify changes. If no manifest exists or it cannot be parsed, incremental mode falls back to full mode with a logged warning. |
| 6 | Merge strategy for dual-provider outputs | LLM-based merge agents (not deterministic dedup). Discovery uses a Discovery Merge Agent (Section 3a); drafting uses a Drafter Combine Agent (Section 3b). Both follow explicit merge rules in their prompts. Conflicts are flagged, not silently resolved. Fallback: Claude-only output on merge failure. |
| 7 | Diagram conflict resolution | For dual-provider diagrams, the combine agent selects the more complete diagram (more nodes/edges) as the base and augments it with missing elements. Line-level Mermaid merge is not attempted. Post-merge Mermaid syntax validation is mandatory. |
| 8 | Writing atomicity | Staging directory pattern: write to `docs/.codedoc-staging/`, validate, backup originals with timestamps, atomically move. See Section 3c. |
| 9 | Documentation versioning | Git is the versioning system. The workflow writes files; the user commits. Timestamped `.bak` files are a crash-recovery safety net, not a versioning mechanism. No cross-run comparison built into the workflow. |
| 10 | Codebase drift handling | Re-check file hashes at writing time. Warn if drift exceeds configurable threshold. Documentation is a point-in-time snapshot; the next run corrects drift. |
| 11 | API authentication | No auth in v1. Server is local-only, same as spec and code review workflows. Auth deferred to future version if network exposure is needed. |
| 12 | Static analysis tools | Optional augmentation. Discovery auto-detects available tools and uses them to improve accuracy (especially dead code detection). LLM-only baseline always works. Tool availability is reported in discovery output. |

---

## 19a. Codedoc Manifest Schema

The `.codedoc-manifest.json` file is written to the `docs/` directory during CD_WRITING. It enables incremental mode by recording the state at documentation time.

```json
{
  "schema_version": "1.0",
  "workflow_feature": "my-project-docs",
  "generated_at": "2026-04-01T12:00:00Z",
  "mode": "full",
  "modules": [
    {
      "path": "internal/api",
      "content_hash": "sha256:abc123...",
      "doc_files": [
        "docs/as-implemented-report.md",
        "docs/architecture/module-dependencies.md"
      ]
    }
  ],
  "files_documented": [
    {
      "path": "docs/as-implemented-report.md",
      "content_hash": "sha256:def456..."
    }
  ],
  "discovery_completion_status": "complete"
}
```

### Manifest Lifecycle

1. **Creation**: Written atomically during CD_WRITING (write to `.codedoc-manifest.json.tmp`, then `os.Rename`).
2. **Reading**: Read at the start of incremental mode discovery. If the file does not exist, fall back to full mode.
3. **Corruption handling**: If the file exists but cannot be parsed as valid JSON, or the `schema_version` is unrecognised, fall back to full mode and log a warning including the parse error.
4. **Stale references**: If the manifest references modules that no longer exist in the codebase, incremental mode treats them as "removed modules" in the `incremental_changes` output.
5. **Overwrite**: Each full mode run overwrites the manifest. Incremental runs update only the changed module entries and the `generated_at` timestamp.

---

## 20. Holdout Evaluation Scenarios

**These scenarios are for post-implementation verification only. Do NOT reference in TDD plan.**

1. **Happy path**: Run full mode against a 100-file Go project. Verify the `docs/` output contains at least: 1 report, 3 diagrams, 1 audit JSON, and covers all packages listed by `go list ./...`.

2. **Happy path**: Run against a project with known dead code (manually planted function with no callers). Verify the audit JSON contains the planted function.

3. **Happy path**: Run against a project with a stale README. Verify the README is updated and a timestamped `.bak` backup exists.

4. **Error path**: Run against a path that doesn't exist. Verify the workflow fails gracefully with a clear error message and never enters DISCOVERY.

5. **Error path**: Run against a read-only `docs/` directory. Verify the writing phase escalates with a permission error and no partial writes exist.

6. **Edge case**: Run incremental mode on a project with no previous documentation. Verify it falls back to full mode automatically.

7. **Edge case**: Run against a project where Codex CLI is not installed but `enable_codex_codedoc_discovery: true`. Verify the workflow proceeds with Claude-only and logs a warning.

8. **Security**: Run against a project with a hardcoded API key in a config file (`AKIA...`). Verify the documentation output does not contain the key after sanitisation.

9. **Recovery**: Kill the server process during CD_WRITING. Restart. Verify the staging directory is detected and resume offers the correct options.

---

## 21. Assumptions

| # | Assumption | Confidence | Notes |
|---|-----------|------------|-------|
| 1 | The target repository is on a local filesystem accessible to the server process | High | Network filesystems may introduce latency but should work |
| 2 | Git is available in the target repo for versioning documentation changes | High | The workflow writes files but does not commit; user handles git |
| 3 | Mermaid syntax validation can be performed without rendering (syntax parsing only) | High | Multiple Go/JS libraries exist for Mermaid syntax validation |
| 4 | Optional static analysis tools produce machine-parseable output | Medium | Tool output formats vary; may need per-tool parsers |
| 5 | LLM agents can reliably identify dead code through source text analysis | Medium | False positive rate depends on codebase complexity; tools augment when available |
