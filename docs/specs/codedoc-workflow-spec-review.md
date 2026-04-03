# Adversarial Review: Codebase Documentation Workflow

**Spec reviewed**: docs/specs/codedoc-workflow-spec.md
**Review date**: 2026-04-01
**Verdict**: REVISE

## Executive Summary

The code-doc workflow specification is structurally comprehensive with well-defined states, schemas, and a traceability matrix, but contains 2 critical gaps (missing error-state transitions in the state machine, and no sanitisation of source code content flowing into documentation outputs), 7 major findings spanning ambiguity in merge semantics, missing concurrency/timeout handling, and an overcomplicated incremental mode, plus several minor and observational items. The spec cannot proceed to implementation until the critical and major findings are addressed.

| Severity | Count |
|----------|-------|
| CRITICAL | 2 |
| MAJOR | 7 |
| MINOR | 5 |
| OBSERVATION | 4 |
| **Total** | **18** |

---

## Findings

### CRITICAL Findings

#### [CRIT-001] State machine has no CD_ERROR transitions defined

- **Lens**: Incompleteness
- **Affected section**: Section 2, State Transition Table
- **Description**: The state `CD_ERROR` is listed in the State Definitions table as a terminal state, but it appears in zero rows of the State Transition Table. No state has a defined transition TO `CD_ERROR`. The spec states "Unrecoverable error" as the description but never specifies which states can transition to `CD_ERROR` or under what conditions. The existing spec workflow codebase handles ERROR as a special "any state -> ERROR" rule in `statemachine.go`, but the spec must explicitly declare this intent rather than leaving it implicit. The spec also references "resume from ERROR state" in the API (Section 13, `POST .../resume`) but never describes what resume-from-error means for this workflow — which state does it resume to? What artefacts are preserved?
- **Impact**: An implementer could omit error handling transitions entirely, or implement them inconsistently. The resume endpoint would be implemented without defined semantics, making crash recovery unpredictable. If the server crashes during `CD_WRITING` (the most dangerous state, since it writes files), the workflow has no defined recovery path.
- **Recommendation**: Add explicit transitions: "Any state except CD_COMPLETE -> CD_ERROR on unrecoverable failure. CD_ERROR -> CD_DISCOVERY, CD_DRAFTING, CD_REVIEWING via resume (with artefact detection, same as spec workflow rewind)." Add a subsection under State Definitions specifying resume semantics: which artefacts are checked, which state is selected, and what happens to partially written files during a CD_WRITING crash.

---

#### [CRIT-002] No sanitisation or redaction specification for source code content in documentation output

- **Lens**: Insecurity
- **Affected section**: Section 7, Explicit Non-Behaviors; FR-015
- **Description**: Section 7 states "The system must NOT include credentials, secrets, or sensitive configuration values in documentation output." However, there is no specification of HOW this is enforced. The discovery and drafting agents read source files that may contain hardcoded secrets, API keys, connection strings, or PII in comments. The agents produce documentation that describes and quotes code. There is no requirement for a sanitisation pass, no pattern-matching for secrets, and no reviewer lens that specifically checks for leaked secrets in the documentation output. The existing 8 review lenses (Section 3) have no security-focused lens — there is no equivalent of the STRIDE lens from the spec workflow.
- **Impact**: An LLM agent producing documentation will cheerfully quote a hardcoded API key from a config file, embed a database connection string found in a comment, or reproduce PII from test fixtures — and this documentation gets written directly to the repository. The adversarial review loop has no lens designed to catch this. Secrets committed to `docs/` are a production security incident.
- **Recommendation**: (1) Add a mandatory post-drafting sanitisation step that scans all documentation output for patterns matching secrets (API keys, tokens, connection strings, passwords, PII patterns). (2) Add a dedicated Security lens to the reviewer groups, or extend the Audit Quality group to include a "sensitive data leakage" check. (3) Add FR-024: "System MUST scan all documentation output for secret patterns (API keys, tokens, passwords, connection strings) and redact them before writing. The writing phase MUST NOT proceed if unredacted secrets are detected."

---

### MAJOR Findings

#### [MAJ-001] "Intelligent merge" semantics are undefined

- **Lens**: Ambiguity
- **Affected section**: US-4 Scenario 4; FR-003; Section 1 Overview
- **Description**: The spec repeatedly uses the phrase "intelligent merge" and "intelligently merged" (Overview, US-4 Scenario 4, FR-003) without defining what this means. US-4 Scenario 4 says "the output preserves the most detailed description from each provider, deduplicates sections, and resolves conflicts" — but does not specify: (a) How "most detailed" is determined — by token count? by information density? by a merge agent's judgement? (b) How conflicts are resolved — does one provider take priority? Is there a voting mechanism? Does it flag conflicts for human review? (c) What constitutes a "duplicate" section — exact match? semantic similarity? same module path? The existing spec workflow's merge logic in `merge.go` uses an LLM-based merge agent, but the spec does not state whether the same approach applies here.
- **Impact**: Two implementers would build fundamentally different merge strategies. One might use a simple concatenation-with-dedup, another might build an LLM-based merge agent. The lack of conflict resolution semantics means that when Claude says module X does A and Codex says module X does B, the output is unpredictable.
- **Recommendation**: Define merge semantics explicitly: "The merge agent is an LLM invocation that receives both provider outputs and produces a unified output. The merge prompt instructs the agent to: (1) prefer the more detailed description when both cover the same module, (2) flag irreconcilable conflicts as findings for the human gate, (3) deduplicate by module path, not by content similarity." Add a merge output schema or reference the existing merge pattern from the spec workflow.

---

#### [MAJ-002] No timeout specified for discovery phase against large codebases

- **Lens**: Incompleteness
- **Affected section**: Section 12, Configuration; Section 5, Edge Cases ("Monorepo with 1000+ packages")
- **Description**: The config section defines `agent_timeout_seconds: 600` for general agents, but the discovery phase must read and analyse the entire codebase. For a monorepo with 1000+ packages (acknowledged in the edge cases table), 600 seconds is likely insufficient. The edge case says "warns about context limits; suggests scope reduction at gate" but does not specify: (a) What constitutes a context limit breach — token count? file count? (b) What happens when the discovery agent times out — is the partial output usable? (c) Is there a separate, higher timeout for discovery? The existing codebase config has `agent_timeout_seconds: 900` which is already different from the spec's stated 600.
- **Impact**: Discovery against large codebases will timeout silently or produce incomplete inventories. The workflow will either fail or proceed with a partial inventory that the human at the gate cannot know is incomplete (since the spec doesn't require the discovery output to indicate whether it completed fully).
- **Recommendation**: (1) Add a `discovery_timeout_seconds` config parameter separate from the general agent timeout, with a default of at least 1200 seconds. (2) Add a `completion_status` field to the discovery output schema (Section 9): `"complete" | "partial"` with a `reason` field when partial. (3) Require HUMAN_GATE_SCOPE to display the completion status prominently so the human knows whether the inventory is full or truncated.

---

#### [MAJ-003] Combine agent for dual-provider drafting has no schema or contract

- **Lens**: Incompleteness
- **Affected section**: Section 1 (Actors table, "Combine Agent"); Section 10 (Drafter Output Schema)
- **Description**: The Actors table lists a "Combine Agent" that "merges dual-provider drafter outputs intelligently." However, there is no schema for the combine agent's input or output, no prompt contract, no retry/validation specification, and no failure mode. The Drafter Output Schema (Section 10) shows a single drafter output, not a combined output. Questions unanswered: Does the combine agent receive two full drafter output JSONs? Does it produce a new drafter output JSON in the same schema? What happens when it fails — fallback to one provider? What if the two drafters produced conflicting architecture diagrams?
- **Impact**: The combine agent is a black box in the specification. An implementer would have to invent the entire contract, making the spec incomplete for this core dual-provider feature.
- **Recommendation**: Add a "Combine Agent Contract" subsection after Section 10: specify input (two drafter output JSONs), output (single drafter output JSON in the same schema), conflict resolution rules (same as MAJ-001), failure mode (fallback to Claude-only output with warning), and timeout.

---

#### [MAJ-004] Writing phase has no atomicity or rollback specification

- **Lens**: Incompleteness
- **Affected section**: US-6; FR-014; Section 2, CD_WRITING state
- **Description**: The writing phase writes multiple files to the target repo's `docs/` directory. The spec says backups are created (`.bak` files) and FR-014 requires this. However: (a) There is no atomicity guarantee — if the process crashes after writing 3 of 6 files, the `docs/` directory is in a half-updated state with some new docs and some stale docs. (b) There is no rollback procedure — if the human discovers the written docs are wrong, there is no "undo" beyond manually restoring `.bak` files. (c) The backup strategy of `{filename}.bak` overwrites any previous backup — running the workflow twice destroys the original backup.
- **Impact**: A crash during writing leaves the repository in an inconsistent state. Users who run the workflow multiple times lose their safety net. The `.bak` approach is fragile and not documented as a limitation.
- **Recommendation**: (1) Specify that the writing phase first writes all files to a staging directory (`docs/.codedoc-staging/`), then atomically moves them into place (rename is atomic on most filesystems). (2) Specify that `.bak` files include a timestamp: `{filename}.bak.{YYYYMMDD-HHMMSS}`. (3) Add a rollback procedure: "If CD_WRITING fails partway, the staging directory contains the intended state and the original files remain untouched."

---

#### [MAJ-005] `.codedoc-manifest.json` lifecycle and corruption handling unspecified

- **Lens**: Incompleteness
- **Affected section**: Section 19, Resolved Design Decision #5 (Incremental mode detection)
- **Description**: Design Decision #5 introduces `.codedoc-manifest.json` as the mechanism for incremental mode. However: (a) The manifest format is described in prose but has no JSON schema. (b) There is no specification for what happens when the manifest is corrupted, truncated, or contains invalid JSON. (c) There is no specification for what happens when the manifest references modules that no longer exist (deleted between runs). (d) The manifest is written during CD_WRITING — if CD_WRITING crashes (see MAJ-004), the manifest may not be written, making the next incremental run fall back to full mode without explanation.
- **Impact**: A corrupted manifest could cause incremental mode to crash or produce nonsensical scope. Missing error handling for manifest read failures means the workflow could fail in confusing ways instead of gracefully falling back to full mode.
- **Recommendation**: (1) Add a JSON schema for `.codedoc-manifest.json` alongside the other schemas in Sections 9-11. (2) Add error handling: "If the manifest cannot be parsed, incremental mode MUST fall back to full mode and log a warning with the parse error." (3) Specify that the manifest is written atomically (write to `.codedoc-manifest.json.tmp`, then rename).

---

#### [MAJ-006] Review lenses lack specificity on what constitutes a finding severity

- **Lens**: Ambiguity
- **Affected section**: Section 3, Review Lenses; Section 11, Reviewer Output Schema
- **Description**: The 8 review lenses (ACC, CUR, CMP, CLA, ARC, STR, AUD, CON) describe WHAT to check but not HOW to classify finding severity. The reviewer output schema (Section 11) includes a `severity` field with values `major` and implicitly others, but there is no severity classification rubric specific to documentation review. For the spec workflow, severity is well-defined (CRITICAL = production incident, MAJOR = incorrect behaviour). For documentation, what constitutes CRITICAL vs. MAJOR vs. MINOR? Is an incorrect function signature in a diagram CRITICAL (because it misleads developers) or MAJOR? Is a missing module in the documentation CRITICAL or MAJOR?
- **Impact**: Reviewers (LLM agents) will classify findings inconsistently across rounds and across the four groups. The judge's convergence check depends on finding counts by severity — inconsistent classification makes convergence detection unreliable.
- **Recommendation**: Add a severity classification rubric specific to code-doc review: "CRITICAL: documentation states the opposite of what the code does (e.g. wrong data flow direction, wrong API signature). MAJOR: documentation omits a significant component or contains misleading descriptions. MINOR: documentation has style issues, unclear phrasing, or minor inaccuracies. OBSERVATION: suggestions for improved documentation structure or additional content."

---

#### [MAJ-007] No specification for how the Combine Agent and reviewer outputs handle Mermaid diagram conflicts

- **Lens**: Ambiguity
- **Affected section**: US-2; Section 3, Group 3 (Architecture lenses); Section 10, Drafter Output Schema
- **Description**: When dual-provider drafting produces two independent sets of Mermaid diagrams, the spec does not specify how conflicting diagrams are merged. Mermaid diagrams are structured text with specific syntax — merging two `graph TD` diagrams is not the same as merging prose. If Claude produces a module dependency diagram with 15 nodes and Codex produces one with 12 nodes (different subset), the merge semantics are undefined. Similarly, when the ARC reviewer finds a diagram error, the spec does not specify whether the reviser regenerates the diagram from scratch or attempts to patch specific edges/nodes.
- **Impact**: Diagram merging is a fundamentally different problem from prose merging. A naive merge could produce invalid Mermaid syntax. A reviewer finding "edge A->B should be A->C" cannot be applied as a text patch — the reviser needs to understand Mermaid syntax.
- **Recommendation**: (1) Specify that for dual-provider diagrams, the merge agent selects the more complete diagram as the base and augments it with missing nodes/edges from the other, rather than attempting a line-level merge. (2) Specify that ARC reviewer findings on diagrams MUST include the corrected Mermaid snippet, not just a prose description of the error. (3) Add a Mermaid syntax validation step post-merge (before human gate).

---

### MINOR Findings

#### [MIN-001] Inconsistent config key naming between spec and existing codebase

- **Lens**: Inconsistency
- **Affected section**: Section 12, Configuration
- **Description**: The spec defines `enable_codex_codedoc` for dual-provider discovery+drafting. The existing `config.yaml` in the codebase uses `enable_codex_discovery` and `enable_codex_drafting` as separate flags. The spec combines them into one flag, which is inconsistent with the established pattern and reduces granularity (a user might want Codex for discovery but not drafting, or vice versa).
- **Recommendation**: Split `enable_codex_codedoc` into `enable_codex_codedoc_discovery` and `enable_codex_codedoc_drafting` to match the existing codebase's pattern of separate flags per phase. Alternatively, document the deliberate divergence with a rationale.

---

#### [MIN-002] `CD_HUMAN_GATE_FINAL` conditionally reached but condition is vague

- **Lens**: Ambiguity
- **Affected section**: Section 2, State Definitions (CD_HUMAN_GATE_FINAL: "only if critical findings remain")
- **Description**: The state definition says HUMAN_GATE_FINAL is reached "only if critical findings remain." The transition table shows `CD_JUDGING -> CD_HUMAN_GATE_FINAL` as one of several options. But the spec does not define: (a) What "critical findings remain" means precisely — is it `severity == CRITICAL` findings with `status != resolved`? (b) Who determines this — the judge agent? (c) What threshold — one remaining critical finding? Any remaining findings above MINOR?
- **Recommendation**: Specify: "The judge transitions to CD_HUMAN_GATE_FINAL when its verdict is PASS but the merged findings contain one or more unresolved findings with severity CRITICAL or MAJOR. When all findings are MINOR or resolved, the judge transitions directly to CD_WRITING."

---

#### [MIN-003] Reviewer output schema missing `status` field for tracking finding resolution

- **Lens**: Incompleteness
- **Affected section**: Section 11, Reviewer Output Schema
- **Description**: The reviewer output schema shows findings with `id`, `description`, `severity`, etc., but no `status` field (e.g., `open`, `resolved`, `wontfix`). Across multiple review rounds, findings from round 1 may be addressed in the revision. Without a status field, the judge has no structured way to determine which findings have been resolved — it would have to compare findings across rounds by description text.
- **Recommendation**: Add `"status": "open|resolved|wontfix"` to the finding schema. The revision agent sets status to `resolved` for findings it addressed. The judge uses status to compute convergence.

---

#### [MIN-004] No specification for what happens when HUMAN_GATE_DRAFT user requests re-draft

- **Lens**: Incompleteness
- **Affected section**: Section 2, State Transition Table; US-7 Scenario 2
- **Description**: The transition table shows `CD_HUMAN_GATE_DRAFT -> CD_DRAFTING` (re-draft). US-7 Scenario 2 says the user can "request re-draft." However, the spec does not specify: what input the user provides to guide the re-draft (just "re-draft" or specific instructions?), whether the drafter receives the previous draft plus the user's feedback, and whether there is a limit on re-draft loops (analogous to `max_gate_corrections` for scope gate).
- **Recommendation**: Add `max_gate_draft_redrafts` config parameter (default 2). Specify that the re-draft request includes a free-text comment field that is passed to the drafter as additional context alongside the previous draft.

---

#### [MIN-005] Traceability matrix maps FR-017 to US-7 but US-7 is about human gates, not dashboard integration

- **Lens**: Inconsistency
- **Affected section**: Section 18, Traceability Matrix, row FR-017
- **Description**: FR-017 says "System MUST integrate with the existing dashboard alongside spec and code review workflows." The traceability matrix maps it to US-7 (Human Gates). US-7 is about human review gates, not dashboard integration. There is no user story specifically covering dashboard integration.
- **Recommendation**: Either add a US-8 for dashboard integration and update the traceability row, or re-map FR-017 to US-1 (the closest match, as the full workflow must be visible in the dashboard to be usable).

---

### Observations

#### [OBS-001] Overcomplexity: Dual-provider for documentation drafting may not justify the merge complexity

- **Lens**: Overcomplexity
- **Affected section**: US-4; FR-003; FR-010
- **Description**: The spec requires dual-provider execution for discovery, drafting, AND review. For the spec workflow, dual-provider review makes sense because two LLMs may catch different issues. However, dual-provider documentation drafting requires a complex merge/combine agent (see MAJ-001, MAJ-003, MAJ-007) to reconcile two complete documentation sets. The merge problem for structured documentation (Mermaid diagrams, JSON audit files) is significantly harder than merging prose review findings. The spec's P1 priority for US-4 means this complexity ships early.
- **Suggestion**: Consider whether dual-provider discovery + single-provider drafting + dual-provider review achieves 90% of the benefit at 50% of the merge complexity. If dual-provider drafting is retained, the merge specification gaps (MAJ-001, MAJ-003, MAJ-007) must be fully addressed.

---

#### [OBS-002] Incremental mode (US-5) introduces manifest management complexity for a P2 feature

- **Lens**: Overcomplexity
- **Affected section**: US-5; Design Decision #5
- **Description**: Incremental mode requires: a manifest schema, hash computation for every module, manifest lifecycle management (MAJ-005), delta computation logic, and a fallback-to-full-mode decision tree. This is substantial new infrastructure for a P2 "nice to have." The spec could ship full mode as the only mode for v1 and add incremental in a later version, which would eliminate Design Decision #5, the manifest, and the conditional logic in discovery.
- **Suggestion**: Consider deferring incremental mode entirely from the v1 spec. If retained, add the manifest schema and error handling specified in MAJ-005.

---

#### [OBS-003] Success criterion SC-004 (80% dead code detection) may be unmeasurable

- **Lens**: Infeasibility
- **Affected section**: Section 17, SC-004
- **Description**: SC-004 says "The code audit correctly identifies at least 80% of functions with zero callers (verified by manual grep)." Computing the ground truth (all functions with zero callers) requires exhaustive static analysis — "manual grep" is not sufficient for this, especially with indirect calls, reflection, and interface dispatch in Go. The success criterion is effectively: verify that the LLM-based audit finds 80% of what a perfect static analyser would find, but the spec does not require running a static analyser.
- **Suggestion**: Reframe SC-004 as a precision+recall test against a manually curated list of known dead code in a test codebase, rather than claiming 80% recall against an unknown total.

---

#### [OBS-004] The spec does not reference the existing code review workflow pattern

- **Lens**: Incompleteness
- **Affected section**: General
- **Description**: The codebase already has `internal/codereview/` implementing a separate workflow type with its own orchestrator, gates, and state machine. The spec does not mention the code review workflow at all, nor specify how the code-doc workflow relates to it architecturally. Is it a sibling (separate package like `internal/codedoc/`)? Does it share the orchestrator interface? Does it reuse the review dispatch, convergence, or merge infrastructure?
- **Suggestion**: Add a brief "Architectural Relationship" section specifying where the code-doc workflow code lives relative to existing `specworkflow` and `codereview` packages, and which components (state machine, review dispatch, merge, convergence) are reused vs. reimplemented.

---

## Structural Integrity

| Check | Result | Notes |
|-------|--------|-------|
| Every goal/objective has acceptance criteria | PASS | All 7 user stories have acceptance scenarios |
| Cross-references are consistent | FAIL | FR-017 traced to US-7 but US-7 is about gates, not dashboard (MIN-005). FR-015 listed as "non-behavior" without a negative test scenario. |
| Scope boundaries are explicit | PASS | Section 7 (Explicit Non-Behaviors) clearly defines what the system must NOT do |
| Success criteria are measurable | FAIL | SC-004 requires measuring recall against an unknown ground truth (OBS-003). SC-006 ("measurably more comprehensive") has no metric definition. |
| Error/failure scenarios addressed | FAIL | CD_ERROR has no transitions defined (CRIT-001). Writing phase has no atomicity (MAJ-004). Manifest corruption unhandled (MAJ-005). |
| Dependencies between requirements identified | FAIL | FR-016 (incremental) depends on the manifest from Design Decision #5, but neither FR-016 nor the decision references a manifest schema. FR-003 (dual-provider merge) has no specification for the merge/combine agent contract (MAJ-003). |

---

## Test Coverage Assessment

### Missing Test Categories

| Category | Gap Description | Affected Scenarios |
|----------|----------------|-------------------|
| Concurrency | No specification for concurrent workflow instances against the same code path | US-1 — what if two users start code-doc for the same repo simultaneously? |
| Secret detection | No tests for secret/credential leakage in documentation output | US-1 Scenario 4 (writing phase), US-6 |
| Partial failure recovery | No tests for crash during CD_WRITING and subsequent resume | US-1, FR-021 |
| Manifest corruption | No tests for corrupted/truncated `.codedoc-manifest.json` | US-5 |
| Large codebase timeout | No tests for discovery timeout on codebases exceeding context limits | Edge case: Monorepo with 1000+ packages |
| Mermaid validation | No tests for invalid Mermaid syntax in merged diagrams | US-2, US-4 |

### Dataset Gaps

| Dataset | Missing Boundary Type | Recommendation |
|---------|----------------------|----------------|
| Code path input | Path with spaces, unicode characters, symlinks | Add test cases for `"/path/with spaces/repo"`, symlinked directories, and paths containing unicode |
| Discovery output | Empty modules list, single-file project | Add test case where discovery finds 0 modules (all code in root with no packages) |
| Audit findings | False positive: test helper flagged as dead code | Add test case with a test helper function that should NOT be in the audit output |
| Config values | Zero and negative values for timeouts, rounds | Add test cases for `max_rounds: 0`, `agent_timeout_seconds: -1`, `max_cost_usd: 0` |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| Discovery Agent (reads source files) | ok | ok | ok | **risk** | ok | ok | Source code content flows into discovery output — no sanitisation spec (CRIT-002) |
| Drafter Agent (produces docs) | ok | ok | ok | **risk** | ok | ok | Documentation output may contain secrets from source code (CRIT-002) |
| Writing Phase (writes to filesystem) | ok | **risk** | ok | ok | ok | ok | No atomicity — partial writes can corrupt docs/ (MAJ-004) |
| API Endpoints (Section 13) | ok | ok | **risk** | ok | **risk** | ok | No auth specified on endpoints; no rate limiting on start endpoint |
| Manifest File (.codedoc-manifest.json) | ok | **risk** | ok | ok | ok | ok | No integrity check — corrupted manifest causes unpredictable behaviour (MAJ-005) |
| Human Gate Endpoints | ok | ok | ok | ok | ok | **risk** | No authorization specified — anyone with API access can approve gates |

**Legend**: risk = identified threat not mitigated in spec, ok = adequately addressed or not applicable

---

## Unasked Questions

1. **What happens when two users start a code-doc workflow against the same code path simultaneously?** The workspace layout uses `{feature}` as the directory key, but two different feature names pointing at the same code path would read the same source files and write to the same `docs/` directory.

2. **How are architecture diagrams versioned across workflow runs?** If the writing phase overwrites `docs/architecture/module-dependencies.md` with a new diagram, is the previous diagram only preserved in the `.bak` file? Is there any mechanism to compare diagrams across runs to show architectural drift?

3. **What is the maximum codebase size the workflow is designed to handle?** The edge cases mention 1000+ packages, but there is no stated upper bound. At what point should the system refuse to proceed rather than produce unreliable output?

4. **Does the discovery agent execute any code analysis tools (e.g., `go vet`, `staticcheck`, `gopls`) or does it rely entirely on LLM analysis of source text?** If LLM-only, the dead code detection will have significant false-positive/negative rates. If tool-assisted, the tool dependencies need to be documented.

5. **What happens to in-progress documentation when the target codebase changes during a long-running workflow?** A 90-minute workflow against an active repo could be documenting code that has already changed by the time it reaches CD_WRITING.

6. **How does the `<!-- manual -->` marker preservation work when the manual section is in the middle of a section that the workflow wants to rewrite?** Does the workflow split the section around the markers, or does it leave the entire containing section untouched?

7. **Who authenticates to the API endpoints in Section 13?** There is no mention of authentication or authorization for any endpoint. Can any network-adjacent user start a workflow, approve a gate, or cancel a running workflow?

---

## Verdict Rationale

The specification is thorough in its coverage of the happy path and provides good structural scaffolding (schemas, state machine, traceability). However, two critical gaps prevent implementation: the state machine's complete omission of CD_ERROR transitions and resume semantics (CRIT-001) means crash recovery is undefined, and the absence of any secret sanitisation mechanism (CRIT-002) creates a direct security risk when documenting codebases that contain credentials. The 7 major findings collectively indicate that the dual-provider merge semantics, writing phase atomicity, and reviewer severity classification are insufficiently specified for an implementer to build reliably.

The spec should be revised to address all CRITICAL and MAJOR findings before proceeding to task decomposition. The MINOR findings and observations can be addressed during revision or deferred to a v1.1 iteration.

### Recommended Next Actions

- [ ] Address CRIT-001: Define CD_ERROR transitions and resume semantics in the state transition table
- [ ] Address CRIT-002: Add secret sanitisation requirement and a security-focused review lens
- [ ] Address MAJ-001, MAJ-003, MAJ-007: Define merge/combine agent contracts with explicit conflict resolution rules, especially for Mermaid diagrams
- [ ] Address MAJ-002: Add discovery timeout configuration and completion status to discovery output schema
- [ ] Address MAJ-004: Specify atomic writing via staging directory and timestamped backups
- [ ] Address MAJ-005: Add `.codedoc-manifest.json` schema and corruption handling
- [ ] Address MAJ-006: Add severity classification rubric for documentation review findings
- [ ] Address MIN-005: Fix FR-017 traceability mapping
