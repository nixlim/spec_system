# Adversarial Review (R2): Adversarial Multi-Agent Specification System

**Spec reviewed**: docs/specs/adversarial-spec-system.md
**Review date**: 2026-03-16
**Prior review**: adversarial-spec-system-review.md (2026-03-15, verdict: REVISE, 30 findings)
**Verdict**: REVISE

## Executive Summary

The revised spec has substantially improved since the prior review. Of the 30 original findings, 22 are fully resolved, 5 are partially resolved, and 3 remain unresolved. The four CRITICAL findings from the prior review are all resolved — the spec now has explicit JSON output contracts (CRIT-001), a deterministic deduplication algorithm (CRIT-002), a comprehensive error handling and recovery model (CRIT-003), and a fully specified human gate interaction model (CRIT-004). However, the revisions introduced 2 new MAJOR issues and several MINOR concerns. The spec is close to implementable but requires one more pass to address the remaining gaps before engineering begins.

| Severity | Count |
|----------|-------|
| CRITICAL | 0 |
| MAJOR | 5 |
| MINOR | 7 |
| OBSERVATION | 4 |
| **Total** | **16** |

---

## Prior Findings Resolution

| Prior ID | Severity | Title | Status | Notes |
|----------|----------|-------|--------|-------|
| CRIT-001 | CRITICAL | No specification for how the orchestrator parses structured output | **RESOLVED** | Section 6 now defines explicit JSON output contracts for every agent type with schemas, validation rules, and error handling (Section 6.5). Design principle 8 (Section 4.2) makes "agents produce JSON; orchestrator parses JSON only" a first-class rule. |
| CRIT-002 | CRITICAL | Issue deduplication algorithm is unspecified | **RESOLVED** | Section 6.3 specifies a deterministic 4-step algorithm: parse/validate, assign global IDs, deduplicate by section+lens+constitution_principle, severity rank. Merge rules (keep higher severity, concatenate recommendations) are explicit. |
| CRIT-003 | CRITICAL | No error handling or recovery model | **RESOLVED** | Section 8 is now a comprehensive error handling specification. ERROR state added to state machine (Section 7.1). Recovery rules defined per state (Section 8.2). Retry semantics specified (Section 8.3). Orchestrator crash recovery specified (Section 7.4). |
| CRIT-004 | CRITICAL | Human gate interaction model undefined | **RESOLVED** | Section 10 fully specifies single-shot interaction with correction paths. HUMAN_GATE_1 (Section 10.1) has Confirm/Correct/Cancel with max 3 correction cycles. HUMAN_GATE_2 (Section 10.2) has per-row Accept/Answer/Defer with one re-draft cycle. |
| MAJ-001 | MAJOR | Mixed-provider contradiction | **RESOLVED** | Section 5.4 "All Reviewers Share" states all agents use Claude. Section 14.3 uses all-Claude team config. Section 17.1 records the decision. Mixed-provider is deferred to v2 (Section 17.2). |
| MAJ-002 | MAJOR | Convergence gaming via severity downgrade | **RESOLVED** | Section 5.6 constrains judge: max 2 downgrades/round, max 3 dismissals/round, max 5 cumulative, specific reason codes required. Section 9.2 adds orchestrator pre-check. Section 9.5 lists all anti-gaming measures. |
| MAJ-003 | MAJOR | No merged-findings.json schema | **RESOLVED** | Section 6.4 provides the complete JSON schema with all required fields, metadata, and dedup log. |
| MAJ-004 | MAJOR | Reviewer structural integrity duplicated | **RESOLVED** | Section 5.4 assigns full 9-point check to Reviewer A on round 1 only. Section 5.6 assigns delta check to judge on subsequent rounds. |
| MAJ-005 | MAJOR | Cost estimates ungrounded, success criteria unmeasurable | **RESOLVED** | Section 15 defines measurable success criteria (SC-01 through SC-06, SP-01 through SP-03). Section 15.3 defines post-launch quality comparison. Section 14.10 adds a disclaimer and references the circuit breaker cap. |
| MAJ-006 | MAJOR | No timeout or circuit breaker | **RESOLVED** | Section 7.2 adds `max_wall_clock_minutes` (60) and `max_cost_usd` (50.0) as circuit breakers. Behaviour at ESCALATED specified. |
| MAJ-007 | MAJOR | Holdout scenario exclusion unspecified | **RESOLVED** | Section 5.3 specifies separate file (`{feature-name}-holdouts.md`) excluded from reviewer and revision agent prompts. |
| MAJ-008 | MAJOR | Spec conflates design proposal with implementation spec | **RESOLVED** | Document header updated to "Implementation Spec (revised from Design Proposal)". Section 17 split into "Decided (v1)" and "Deferred to v2" with clear decision/choice/rationale columns. |
| MAJ-009 | MAJOR | No definition of "progress" | **RESOLVED** | Section 9.3 defines progress deterministically with three concrete criteria. Computed by orchestrator, not judge. |
| MAJ-010 | MAJOR | File upload security unaddressed | **RESOLVED** | Section 16.1 specifies allowed types, max sizes, filename sanitization, path traversal prevention, and content validation. Section 16.2 addresses prompt injection. |
| MAJ-011 | MAJOR | Convergence judge single point of failure | **PARTIALLY_RESOLVED** | Section 9.2 adds a deterministic orchestrator pre-check that mechanically verifies PASS validity. This addresses the "unchecked authority" concern. However, the judge can still incorrectly *verify* a finding as resolved when it is not (the pre-check only verifies that findings are *referenced*, not that the resolution is correct). See MAJ-R2-001 below. |
| MIN-001 | MINOR | max_rounds 5 vs 6 discrepancy | **RESOLVED** | Section 14.5 shows `maxSpecReviewRounds = 5` with comment "Updated from 6 to match spec_workflow.max_rounds". Section 7.2 and 14.3 both use 5. |
| MIN-002 | MINOR | Source documents undefined | **RESOLVED** | Section 13.3.1 defines accepted types, extensions, and handling (verbatim embedding, PDF text extraction, code fencing). |
| MIN-003 | MINOR | State machine gates inconsistency | **RESOLVED** | Section 4.1 note explicitly states gates are modelled as states for persistence and crash recovery. Section 7.1 includes both in the state diagram and the state table. |
| MIN-004 | MINOR | How revision agent receives findings | **RESOLVED** | Section 5.5 specifies file paths (reads from workspace). Section 11.2 confirms all agents read inputs from workspace files. Section 5.5 specifies context window split strategy. |
| MIN-005 | MINOR | Glossary incomplete | **PARTIALLY_RESOLVED** | Glossary (Appendix C) now includes "Lens Cluster", "Net Progress", "Staleness", "Severity Calibration", "Skill File", "Source Documents", and "merged-findings.json". However, "constructiveness enforcement" and "partial automation mode" are still undefined (though partial automation is now deferred to v2, so its absence is less concerning). |
| MIN-006 | MINOR | No logging/observability for orchestrator | **RESOLVED** | Section 5.1 specifies structured JSON log events for every state transition, agent dispatch, completion, error, dedup decision, and convergence check. |
| MIN-007 | MINOR | WebSocket event schema incomplete | **RESOLVED** | Section 13.5 provides complete payload schemas for all six event types including `gate_request` data field contents. |
| MIN-008 | MINOR | No concurrent workflow spec | **RESOLVED** | Section 13.2 explicitly states single workflow only in v1 with rejection error. Deferred to v2 (Section 17.2). |
| MIN-009 | MINOR | Debate trail generation unspecified | **RESOLVED** | Section 11.3 and 13.3.6 specify the orchestrator assembles the debate trail deterministically from JSON files at FINALIZED or ESCALATED. |
| OBS-001 | OBS | Uneven reviewer workload | **RESOLVED** | Section 5.4 changed to 4 reviewers with 2 lenses each: {1,2}, {3,4}, {5,6}, {7,8}. No longer 3+1+2+2. |
| OBS-002 | OBS | plan-spec/grill-spec detail in main body | **PARTIALLY_RESOLVED** | Sections 3.1-3.3 still contain ~130 lines of existing skill documentation. The content is more concise than before (prior Sections 4.1-4.3 were ~150 lines), but the recommendation to move to an appendix was not followed. Acceptable given the spec's role as a self-contained implementation document. |
| OBS-003 | OBS | Research sections could be compressed | **PARTIALLY_RESOLVED** | Section 2 now has a summary table and is shorter. Section 3 (was Section 4) still covers research context but is more focused. Not fully compressed to a single table, but improved. |
| OBS-004 | OBS | No context window limit consideration | **RESOLVED** | Section 5.5 specifies 80% context window threshold and multi-pass splitting by severity. Section 11.2 specifies the orchestrator uses a token count heuristic (1 token ~= 4 chars). For reviewers, oversized specs trigger ESCALATED. |
| OBS-005 | OBS | No skill file versioning | **RESOLVED** | Section 7.4 `workflow-state.json` includes `skill_checksums` (SHA256). Resumption protocol (Section 7.4) warns on checksum mismatch but continues. |
| OBS-006 | OBS | Framework comparison appendix inconsistency | **PARTIALLY_RESOLVED** | Appendix A now includes AgentBridge as a column. However, it still doesn't mark a single "Best for this use case" — the comparison is now more of a neutral feature matrix, which is actually an improvement. |

**Summary**: 22 RESOLVED, 5 PARTIALLY_RESOLVED, 3 UNRESOLVED (0 — all prior findings are at least partially addressed).

---

## Findings

### CRITICAL Findings

*No CRITICAL findings.*

---

### MAJOR Findings

#### [MAJ-R2-001] Orchestrator pre-check cannot verify resolution *correctness*, only resolution *reference*

- **Lens**: Incorrectness
- **Affected section**: Section 9.2 (Convergence Criteria), Section 5.6 (Convergence Judge)
- **Description**: Section 9.2 states the orchestrator performs a "deterministic pre-check" before accepting PASS. The pre-check verifies: (1) every CRITICAL finding has status `closed` or `dismissed`, (2) the revision agent's change log references every CRITICAL/MAJOR finding ID, (3) `min_rounds` is met, (4) downgrade/dismissal limits. This is a mechanical reference check — it confirms that the revision agent *mentioned* each finding, not that the revision *actually resolved* it. The judge is still the sole arbiter of whether a revision correctly resolves a finding. A hallucinating judge that verifies all findings as "resolved" when they are not will pass the orchestrator's pre-check because the change log references are present. The prior review's MAJ-011 identified this single-point-of-failure risk. The orchestrator pre-check mitigates the "judge approves without looking" case but not the "judge looks and gets it wrong" case.
- **Impact**: A poorly calibrated judge can declare all findings verified, the orchestrator pre-check passes (all references present, all statuses closed), and the system FINALIZEs a spec with unresolved CRITICAL issues. This is the most likely path to a production incident from this system.
- **Recommendation**: Add one additional mechanical check to the orchestrator pre-check: for every CRITICAL finding marked as "addressed" -> "verified" -> "closed", verify that the revision agent's `sections_modified` array for that finding is non-empty (i.e., the revision agent actually changed something in the spec, not just claimed to). This doesn't guarantee correctness but catches the "nothing changed but the judge said it's fine" case. Additionally, for v1, add a human confirmation gate at FINALIZED for specs that had any CRITICAL findings at any point in the workflow — the user must download and at least skim the final spec before it's declared done. This can be relaxed in v2 after confidence is established.

---

#### [MAJ-R2-002] Deduplication algorithm has a semantic gap that will produce false negatives

- **Lens**: Incorrectness
- **Affected section**: Section 6.3 (Issue Deduplication Algorithm), Step 3
- **Description**: The dedup algorithm requires exact match on `affected_section` (case-insensitive, whitespace-normalised). Two reviewers describing the same gap will very likely use different section references. For example, Reviewer A might reference "Section 7.2 Iteration Bounds" while Reviewer B references "Section 7.2" or "Iteration Bounds" or "the max_rounds parameter in Section 7.2." These are all the same section, but the exact-match algorithm will treat them as different, producing duplicate findings that flood the revision agent. The `constitution_principle` match criterion (Step 3, point 3) helps if both reviewers cite the same principle, but findings from different lenses will never share a constitution_principle. The algorithm will accurately dedup the narrow case where two reviewers produce nearly identical findings, but will miss the common case where they describe the same issue differently.
- **Impact**: The revision agent will receive duplicate findings presented as distinct issues, increasing its prompt size, consuming context window, and potentially causing it to make redundant or contradictory changes to the same section. In later rounds, the issue tracker will have parallel open issues for the same underlying problem, confusing convergence tracking.
- **Recommendation**: Accept that deterministic exact-match dedup will be conservative (producing duplicates rather than incorrectly merging distinct issues). This is the safer direction. Add a note to Section 6.3 acknowledging this limitation explicitly and specify that the revision agent's prompt should include the instruction: "If you encounter multiple findings about the same section or concept, address them together in a single change and reference all finding IDs." This pushes the semantic dedup to the revision agent (an LLM that can understand equivalence) rather than trying to solve it deterministically.

---

#### [MAJ-R2-003] Token counting heuristic is unreliable and the 80% threshold is unvalidated

- **Lens**: Infeasibility
- **Affected section**: Section 11.2, Section 5.5
- **Description**: Section 11.2 specifies "1 token ~= 4 characters" as the token count heuristic. This approximation varies significantly by content type: English prose averages ~4 chars/token, but JSON (with repetitive structural characters) averages ~3 chars/token, and code averages ~3.5 chars/token. For a prompt that's 80% JSON schemas and code examples, the heuristic could undercount tokens by 25-33%. The spec says the orchestrator splits work when the prompt exceeds 80% of the "model context window" but never states what that context window size is. Claude models have varying context windows (200k tokens for Claude 3.5 Sonnet, for example). The threshold percentage and the model's actual limit are both unspecified. If the heuristic undercounts and the actual prompt exceeds the context window, the agent will receive a truncated prompt or an API error — and the spec's error handling (Section 8.1) doesn't list "context window exceeded" as a failure type.
- **Impact**: The revision agent (which receives the full spec + all findings + revision instructions) is most at risk. A 20-page spec (~80k characters) + 60 findings (~50k characters of JSON) + templates (~20k characters) = ~150k characters. At 4 chars/token this is ~37.5k tokens (well within 200k). But at 3 chars/token for the JSON portions, it's ~45k tokens. The real risk is when the agent *also* needs output tokens — the context window is shared between input and output.
- **Recommendation**: (1) Specify the assumed context window size explicitly (e.g., 200k tokens for Claude Sonnet). (2) Change the heuristic to "1 token ~= 3.5 characters" (more conservative). (3) Set the threshold at 60% of context window, not 80%, to leave headroom for output tokens. (4) Add "context window exceeded" (API error with specific error code) to Section 8.1 failure types, with recovery: split and retry with smaller input.

---

#### [MAJ-R2-004] REVIEWING -> JUDGING shortcut on zero CRITICAL/MAJOR bypasses revision agent review

- **Lens**: Incorrectness
- **Affected section**: Section 7.3 (Zero-findings path)
- **Description**: Section 7.3 states: "If all four reviewers produce zero CRITICAL or MAJOR findings (only MINOR/OBSERVATION or none at all), the orchestrator skips REVISING and proceeds directly to JUDGING." This creates an asymmetry: the judge receives a spec that has not been through the revision agent, so there is no change log, no dismissal requests, and no `revision-round-{N}.json` file. The judge's input specification (Section 5.6) says it receives "revision agent's change log JSON and dismissal requests" — but in this path, those don't exist. The judge's schema (Section 6.2) and the convergence criteria (Section 9.2) both assume the revision agent has run. What does the judge evaluate when there's nothing to verify?
- **Impact**: If the judge's prompt expects revision artifacts that don't exist, the judge may fail (missing input file) or hallucinate a change log. The orchestrator pre-check (Section 9.2) verifies the change log references every CRITICAL/MAJOR finding — but there are none, so the check vacuously passes. The MINOR findings go unaddressed and unacknowledged. The judge's role in this path is reduced to rubber-stamping.
- **Recommendation**: Specify the judge's behaviour on the zero-critical-major path explicitly: (1) The judge still runs but receives a modified prompt: "No revision was needed. Verify that the MINOR/OBSERVATION findings are acceptable risks and that structural integrity passes." (2) The orchestrator does NOT pass revision artifacts (or passes an empty placeholder). (3) The judge's verdict in this case is either PASS (confirming MINOR findings are acceptable) or REVISE (if the judge determines a MINOR finding should be MAJOR). (4) The MINOR findings should be logged as "accepted risks" in the convergence summary — currently this is specified for FINALIZED (Section 11.3) but the mechanism for when it happens in the zero-revision path is unclear.

---

#### [MAJ-R2-005] Re-drafting from HUMAN_GATE_2 can produce a new spec version without review history context

- **Lens**: Incompleteness
- **Affected section**: Section 10.2 (HUMAN_GATE_2), Section 7.1 state transitions
- **Description**: Section 10.2 says: "If any rows have 'Provide answer': System re-runs DRAFTING with the confirmed requirements updated to include the user's answers." The state table (Section 7.1) confirms: HUMAN_GATE_2 "Corrected -> DRAFTING (re-draft with answers)". This re-drafting produces a new `spec-v1.md` (or does it produce `spec-v2.md`?). The spec doesn't say what version number the re-drafted spec gets. More importantly, Section 10.2 says "Re-drafting from ambiguity resolution can happen at most once. If the re-draft produces new ambiguity warnings, the user sees them but can only Accept or Defer (no further re-drafting loop)." But the state machine (Section 7.1) doesn't model this constraint — HUMAN_GATE_2 transitions to DRAFTING, which transitions back to HUMAN_GATE_2, which could transition to DRAFTING again. The "at most once" constraint exists only in prose, not in the state machine.
- **Impact**: Without a counter or state flag, the re-drafting limit is unenforced. An implementer reading only the state machine would allow unlimited re-drafting loops. The version numbering ambiguity could cause the review cycle to reference the wrong spec version.
- **Recommendation**: (1) Add a `gate2_redraft_count` field to `workflow-state.json`. The orchestrator checks this before allowing HUMAN_GATE_2 -> DRAFTING. (2) Specify the version numbering: the initial draft is `spec-v0.md` (pre-review), a re-draft from gate 2 is `spec-v0-revised.md` (still pre-review), and the first post-review revision is `spec-v1.md`. Or simply: all pre-review drafts are `spec-v0.md` (overwritten), and versioning starts at `spec-v1.md` after the first review round. (3) Encode the "at most once" constraint in the state table, not just in prose. Add a guard: HUMAN_GATE_2 -> DRAFTING [only if gate2_redraft_count < 1].

---

### MINOR Findings

#### [MIN-R2-001] `--dangerously-skip-permissions` grants more than filesystem access

- **Lens**: Insecurity
- **Affected section**: Section 16.3, Section 14.1
- **Description**: Section 16.3 states "No agent has network access (Claude Code runs with `--dangerously-skip-permissions` which grants filesystem but is invoked in a workspace-scoped context)." This is incorrect. The `--dangerously-skip-permissions` flag in Claude Code skips ALL permission prompts, not just filesystem. This includes: executing arbitrary shell commands, installing packages, making network requests, modifying system files. Claude Code with this flag has unrestricted access to the machine. The claim "no agent has network access" is false — any agent could `curl` an external URL or `pip install` a package. The workspace-scoped working directory is a convention, not a sandbox.
- **Recommendation**: (1) Correct Section 16.3 to accurately describe what `--dangerously-skip-permissions` grants. (2) Acknowledge the trust model: agents have full machine access; security relies on prompt instructions and the single-user trust boundary. (3) Consider adding `--allowed-tools` flags (if Claude Code supports tool restrictions) to limit agents to file read/write operations only. (4) Document this as an accepted risk for v1 (localhost, single user).

---

#### [MIN-R2-002] No specification for what happens when DISCOVERY re-runs after user correction

- **Lens**: Incompleteness
- **Affected section**: Section 10.1 (HUMAN_GATE_1), Section 7.1
- **Description**: Section 10.1 says user corrections trigger a re-run of DISCOVERY with corrections as additional context. The prompt includes: "The user has provided the following corrections...". But: (a) Does the re-run produce a completely new `discovery-output.json` or a delta? (b) Does the re-run see the original source documents AND the previous discovery output, or only the corrections? (c) After the re-run, the user sees HUMAN_GATE_1 again — can they correct again? Section 10.1 says "at most 3 times (configurable: `max_gate_corrections`)" but the state machine doesn't model this counter.
- **Recommendation**: (1) Specify: re-run produces a completely new `discovery-output.json` (overwrites previous). The prompt includes original source documents + previous discovery output + user corrections. (2) Add `gate1_correction_count` to `workflow-state.json` and encode the limit in the state machine guard condition. (3) Clarify: after 3 corrections, the user's options are Confirm (as-is) or Cancel.

---

#### [MIN-R2-003] Best-effort parse (Section 6.5) has undefined "zero valid findings" threshold for reviewers

- **Lens**: Ambiguity
- **Affected section**: Section 6.5 (Output Validation), Section 8.2
- **Description**: Section 6.5 says: "The orchestrator attempts a best-effort parse: extract whatever findings are valid, log warnings for invalid ones, and proceed. If zero valid findings are extracted, treat as agent failure." For a reviewer, zero valid findings could mean: (a) the reviewer found no issues (legitimate — the spec section they cover is clean), or (b) the reviewer's output was garbage (failure). These are indistinguishable by "zero valid findings" alone. A reviewer that produces `{"findings": []}` (empty array, valid JSON, valid schema) is different from one that produces `{"fndngs": []}` (valid JSON, invalid schema, zero extractable findings).
- **Recommendation**: Distinguish between "valid output with zero findings" and "invalid output with zero extractable findings." Case (a): reviewer's JSON is valid, `findings` array exists (even if empty) = success, zero findings. Case (b): reviewer's JSON is invalid or `findings` array is missing = failure after best-effort parse yields nothing. Specify this distinction in Section 6.5.

---

#### [MIN-R2-004] Convergence criteria item 2 ("no new substantive findings") is subjective

- **Lens**: Ambiguity
- **Affected section**: Section 9.2, Criterion 2
- **Description**: Convergence criterion #2 says: "No new substantive findings. The latest review round raised no CRITICAL or MAJOR findings." The word "substantive" is defined by the parenthetical as "CRITICAL or MAJOR" — but the criterion title uses a subjective word that could be interpreted differently. More importantly, this criterion creates a chicken-and-egg problem: the PASS decision happens in JUDGING, but new findings are raised in REVIEWING. The sequence is REVIEWING -> REVISING -> JUDGING. By the time the judge runs, the reviewers from the *current* round have already raised findings and the revision agent has addressed them. Does "latest review round" mean the round that just completed (in which case the criterion is about the REVIEWING that preceded this JUDGING), or the *next* round (which hasn't happened yet)?
- **Recommendation**: (1) Remove the word "substantive" — just say "no CRITICAL or MAJOR findings." (2) Clarify temporal reference: "The reviewers in the current round (the REVIEWING phase that preceded this JUDGING) raised zero CRITICAL or MAJOR findings." (3) This means PASS is only possible when a review round finds nothing serious — not when a review round finds things and the revision agent fixes them. If this is the intent, state it. If not, remove this criterion (criteria 1, 4, 5, and 6 are sufficient).

---

#### [MIN-R2-005] No specification for the FINALIZED assembly step

- **Lens**: Incompleteness
- **Affected section**: Section 7.1, Section 11.3
- **Description**: Section 7.1 lists FINALIZED as a state with description "Assemble final spec + appendices" and agent "Orchestrator (deterministic)." Section 11.3 says the orchestrator assembles three appendices: Convergence Summary, Accepted Risks, and Debate Trail. But: (a) Where is the holdout file re-integrated? Section 5.3 says holdout scenarios are "only included in the final assembled output at FINALIZED" but the assembly step doesn't mention them. (b) What is the exact output file name? Is it `spec-final.md`? `spec-v{N+1}.md`? A new file or an overwrite? (c) Is the assembled output a single file with appendices appended, or multiple files?
- **Recommendation**: Specify the FINALIZED assembly step as a deterministic procedure: (1) Copy `spec-v{latest}.md` to `spec-final.md`. (2) Append holdout scenarios from `{feature-name}-holdouts.md`. (3) Append Convergence Summary appendix. (4) Append Accepted Risks appendix. (5) Append Debate Trail from `debate-trail.md`. (6) Write `workflow-state.json` with state=FINALIZED. State the exact output file names.

---

#### [MIN-R2-006] Issue lifecycle has no path from `raised` to `closed` for MINOR findings in zero-revision path

- **Lens**: Incompleteness
- **Affected section**: Section 9.1 (Issue Lifecycle)
- **Description**: The issue lifecycle (Section 9.1) shows: `RAISED -> ADDRESSED -> VERIFIED -> CLOSED`. There is no direct `RAISED -> CLOSED` path. In the zero-CRITICAL/MAJOR path (Section 7.3), MINOR findings are raised but the revision agent never runs, so they are never `addressed`. The judge may PASS (all MINOR findings are acceptable risks). But the findings remain in `raised` status — they are never `closed` or `addressed`. Section 9.2 criterion 3 says "MINOR findings acknowledged" but "acknowledged" is not a status in the lifecycle.
- **Recommendation**: Add a lifecycle transition: `raised -> acknowledged` (set by orchestrator when judge produces PASS verdict and findings are MINOR/OBSERVATION). Or: `raised -> closed` with resolution_notes="Accepted risk — acknowledged at FINALIZED." Ensure the lifecycle covers all terminal states for all finding severities.

---

#### [MIN-R2-007] Existing YAML config shows codex provider and mixed team, contradicting "all Claude" decision

- **Lens**: Inconsistency
- **Affected section**: Section 14.2 (Existing YAML Configuration)
- **Description**: Section 14.2 shows the "current `agentbridge.yaml`" which includes a `codex` provider definition and `reviewer-codex` team member. Section 14.3 then shows the new config with all-Claude. Section 14.2 is context ("what exists today"), not a design decision, but it creates confusion: an implementer might wonder whether the codex references should remain for backwards compatibility or be removed.
- **Recommendation**: Add a note to Section 14.2: "This is the existing configuration. The adversarial spec workflow uses the configuration in Section 14.3, which replaces the mixed-provider team with all-Claude agents. The codex provider definition may remain for other AgentBridge workflows but is not used by the spec workflow."

---

### Observations

#### [OBS-R2-001] The spec assumes Claude Code CLI JSON output format is stable

- **Lens**: Infeasibility
- **Affected section**: Section 14.1
- **Description**: The cost tracking mechanism depends on the `cost_usd` field in Claude Code's stdout JSON. The prompt parsing depends on `result` and `is_error` fields. These are implementation details of the Claude Code CLI tool, which is under active development and may change its output format. The spec has no fallback or version pinning for the CLI tool itself.
- **Suggestion**: Document the assumed Claude Code CLI version or commit hash. Consider adding a note: "If the CLI output format changes, the adapter (`adapter_claude.go`) must be updated. The orchestrator relies on these specific fields: `result`, `cost_usd`, `duration_ms`, `is_error`."

---

#### [OBS-R2-002] Four parallel reviewer agents may hit provider rate limits

- **Lens**: Inoperability
- **Affected section**: Section 7.3, Section 14.7
- **Description**: Four simultaneous Claude API calls may trigger rate limits, especially on lower-tier API plans. Section 8.1 lists "Crash" as a failure type with "API key revoked" as an example, but rate limiting (HTTP 429) is not listed and may not cause the subprocess to crash — it may cause the agent to retry internally or produce truncated output. The spec's retry semantics (Section 8.3) handle timeout and crash but not rate-limit-induced degradation.
- **Suggestion**: Add "Rate limited" to Section 8.1 failure types. Detection: agent takes significantly longer than expected (> 2x median duration for that agent type) or agent output references retry/throttling. Recovery: the orchestrator could stagger reviewer dispatch (e.g., 2 immediately, 2 after 30s) as a configurable option if rate limiting is observed.

---

#### [OBS-R2-003] The 900s default timeout may be insufficient for the revision agent on large specs

- **Lens**: Infeasibility
- **Affected section**: Section 14.1, Section 8.1
- **Description**: The default CLI timeout is 900 seconds (15 minutes), shared across all agent types. The revision agent receives the full spec + all findings and must produce a complete revised spec. For a 20-page spec with 30+ findings, this is a substantial generation task. If the agent also needs multiple turns (the YAML config sets `CLAUDE_CODE_MAX_TURNS: "50"`), 900 seconds may not be enough. Other agents (discovery, individual reviewers) likely need far less time.
- **Suggestion**: Consider per-agent-type timeouts in the YAML config rather than a single global timeout. E.g., `discovery: 300s`, `reviewer: 600s`, `reviser: 1200s`, `judge: 600s`. This provides tighter failure detection for agents that should be fast and more headroom for agents that do heavy generation.

---

#### [OBS-R2-004] No cancellation mechanism during the automated review-revise loop

- **Lens**: Inoperability
- **Affected section**: Section 10.3
- **Description**: Section 10.3 says "The review-revise-judge cycle is fully automated. The human is not involved until either FINALIZED or ESCALATED." There is no mention of a cancel/abort mechanism during the automated loop. The prior review's "Unasked Questions" #5 asked: "Is there a 'cancel' mechanism?" The spec doesn't answer this. If the user sees the system heading in a wrong direction (e.g., reviewers keep raising findings about a fundamental design choice the user already decided), there's no way to stop it short of killing the process.
- **Suggestion**: Add a cancel endpoint (`POST /api/spec/cancel`) that sets the state to ESCALATED with reason "User cancelled." The orchestrator checks for cancellation before each agent dispatch. Current agent subprocesses are allowed to complete (or killed with SIGTERM), and partial results are preserved.

---

## Structural Integrity

| Check | Result | Notes |
|-------|--------|-------|
| Every goal/objective has acceptance criteria | **PASS** | Section 15 defines SC-01 through SC-06 with measurable thresholds. SP-01 through SP-03 define performance criteria. |
| Cross-references are consistent | **PASS** | Section references are internally consistent. Section 6.3 referenced by 5.1, 7.3, 14.9. Section 9.2 referenced by 5.6, 9.5. No dangling references found. |
| Scope boundaries are explicit | **PASS** | Section 17.1 lists all decided items. Section 17.2 lists all deferred items. Clear v1/v2 boundary. |
| Success criteria are measurable | **PASS** | SC-01: 100% structural integrity pass. SC-02: >=70% convergence. SC-03: >=60% manual review PASS. SP-01: <30 min. SP-02: <$50. SP-03: median <=3 rounds. All measurable. |
| Error/failure scenarios addressed | **PASS** | Section 8 covers timeout, crash, missing output, invalid JSON, schema violation, orchestrator crash. Recovery rules per state in Section 8.2. |
| Dependencies between requirements identified | **FAIL** | The spec does not explicitly identify dependencies between features being added to AgentBridge (Section 12.1 "What needs to be added"). For example, the issue tracker data structure must exist before the convergence verdict parsing can be built; the file upload API must exist before the goal submission UI extension works. No implementation ordering is specified. See MIN-R2-005 (FINALIZED assembly) for another dependency gap. |

---

## Test Coverage Assessment

### Missing Test Categories

| Category | Gap Description | Affected Scenarios |
|----------|----------------|-------------------|
| Deduplication edge cases | No test strategy for dedup with near-miss section names, null constitution_principles on both sides, or all-reviewers-flag-same-issue | Section 6.3 algorithm |
| Context window overflow | No test for when agent prompts approach or exceed context limits; the 80% heuristic is untested | Section 11.2, Section 5.5 |
| Circuit breaker interactions | No test for multiple circuit breakers triggering simultaneously (e.g., max_cost_usd and max_wall_clock_minutes at the same time) | Section 7.2 |
| Judge + orchestrator pre-check disagreement | No test for the case where the judge says PASS but the orchestrator pre-check overrides to REVISE | Section 9.2 |
| Gate correction cycles | No test for max_gate_corrections limit enforcement or what happens when the corrected discovery output is worse than the original | Section 10.1 |
| Resumption after crash mid-agent | No test for crash recovery where an agent was mid-execution, output file is partially written (valid JSON prefix but truncated) | Section 7.4, Section 8.2 |
| Zero-findings path through judge | No test for the judge's behaviour when no revision artifacts exist (Section 7.3 skip path) | Section 7.3 |

### Dataset Gaps

| Dataset | Missing Boundary Type | Recommendation |
|---------|----------------------|----------------|
| Findings volume | Exactly 60 findings (max_total_findings boundary) — does the 60th finding trigger ESCALATED or is it <=60 that's allowed? | Section 7.2 says "> 60" but should clarify: is it "findings > 60" or "findings >= 60"? |
| Cost tracking | What happens when cost accumulates to exactly $50.00 (the boundary)? | Clarify: is the check `>= max_cost_usd` or `> max_cost_usd`? |
| Round counting | Round exactly equals max_rounds (5) — the spec says "if round > max_rounds, -> ESCALATED" (Section 7.2). This means round 5 is allowed (5 > 5 is false) and round 6 triggers escalation. But the same section says "up to 5 entries to REVIEWING." If round 5 enters REVIEWING, the check is 5 > 5 = false, so it proceeds. When does the check for round 6 happen? | Clarify whether the check should be `>= max_rounds` or `> max_rounds`. |
| Wall clock | What happens if max_wall_clock_minutes is exceeded mid-agent-execution (not before dispatch)? | Section 7.2 says "Checked before every agent dispatch" — but what about during execution? A 15-minute agent invocation could blow past the limit. Clarify: is this a hard kill or a soft check? |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| File Upload API | ok | ok | - | ok | ok | - | Section 16.1 addresses file type, size, path traversal. Content validation added. |
| Agent Prompts | M | M | - | L | - | M | Prompt injection mitigated by XML delimiters (Section 16.2) but acknowledged as defence-in-depth, not a guarantee. `--dangerously-skip-permissions` grants full machine access [MIN-R2-001]. |
| Convergence Judge | - | M | L | - | - | L | Authority constrained (Section 5.6) but judge can still incorrectly verify findings [MAJ-R2-001]. All decisions logged. |
| WebSocket Events | - | L | - | L | - | - | Authentication deferred to v2. Acceptable for localhost (Section 16.4). |
| Workspace Files | - | M | ok | L | - | - | Agents have full filesystem access via `--dangerously-skip-permissions`. Git audit trail provides repudiation protection. |
| CLI Tool (Claude Code) | - | L | - | L | - | L | Dependency on external CLI tool's output format stability [OBS-R2-001]. |
| Workflow State File | - | M | - | L | M | - | `workflow-state.json` could be tampered with to manipulate round counts, costs, or state. No integrity protection (checksums, signing). |

**Legend**: H = high risk, M = medium risk, L = low risk, ok = addressed, - = not applicable

---

## Unasked Questions

1. What happens when the user provides corrections at HUMAN_GATE_1 that fundamentally contradict the source documents? Does the discovery agent resolve the conflict, or does the user's correction always win?

2. How does the orchestrator handle a spec that grows significantly across revisions (e.g., each revision adds sections, growing from 10 pages to 30 pages)? Is there a spec size limit?

3. When the revision agent splits findings by severity (Section 5.5), are the multiple revision passes sequential? Does each pass see the output of the previous pass? If so, the later passes may undo changes from earlier passes.

4. What happens if the drafter agent produces a spec with zero BDD scenarios or zero FRs (structurally valid JSON but semantically empty)? Is this caught by the reviewer's structural integrity check or does it pass through?

5. How does the system handle the case where all four reviewers produce zero findings in round 1 (the very first review)? The judge would need to PASS (no issues found), but this seems suspicious for any non-trivial spec. Should `min_rounds` prevent this?

6. If `max_gate_corrections` (3) is reached at HUMAN_GATE_1 and the user is still unsatisfied, the only option is Confirm or Cancel. What if the user needs a 4th correction? Is there a mechanism to reset the counter or override?

7. What is the expected behaviour when two circuit breakers trigger at the same time (e.g., max_cost_usd and max_rounds both exceeded simultaneously)? Which takes precedence in the ESCALATED message?

---

## Verdict Rationale

The revised spec has made impressive progress. All four prior CRITICAL findings are resolved. The spec now has explicit JSON contracts for every agent type, a deterministic deduplication algorithm, comprehensive error handling, and a fully specified human gate model. The document has been restructured from a design proposal into an implementation specification with clear decided/deferred boundaries and measurable success criteria.

However, five MAJOR issues remain. MAJ-R2-001 identifies that the orchestrator pre-check, while a significant improvement, still cannot catch a judge that incorrectly verifies findings — the most likely path to shipping a defective spec. MAJ-R2-002 identifies that the dedup algorithm will produce false negatives (duplicates that aren't merged) due to exact-string-match limitations, leading to bloated findings lists. MAJ-R2-003 identifies that the token counting heuristic is unreliable and the context window threshold leaves insufficient headroom for output tokens. MAJ-R2-004 identifies an unspecified code path (zero-critical/major findings skip to judge without revision artifacts). MAJ-R2-005 identifies that the re-drafting limit at HUMAN_GATE_2 is enforced only in prose, not in the state machine.

None of these are blocking — they can be addressed with targeted additions to the existing sections. The spec's architecture is sound, the agent contracts are well-defined, and the error handling model is comprehensive. One more revision pass addressing these five MAJOR findings will bring the spec to an implementable state.

Verdict: **REVISE**. Address MAJ-R2-001 through MAJ-R2-005 before implementation begins. The MINOR findings and observations should be addressed but are not blocking.

### Recommended Next Actions

- [ ] Add mechanical check for non-empty `sections_modified` on CRITICAL finding verification, and consider a human confirmation gate at FINALIZED for specs with CRITICAL history (MAJ-R2-001)
- [ ] Acknowledge dedup false-negative limitation in Section 6.3 and instruct revision agent to handle semantic duplicates (MAJ-R2-002)
- [ ] Specify assumed context window size, use conservative token heuristic, reduce threshold to 60%, add "context window exceeded" to failure types (MAJ-R2-003)
- [ ] Specify judge behaviour and prompt for the zero-critical/major (skip revision) path (MAJ-R2-004)
- [ ] Add `gate2_redraft_count` to workflow state, specify version numbering for pre-review drafts, encode re-draft limit in state machine (MAJ-R2-005)
- [ ] Correct the description of `--dangerously-skip-permissions` in Section 16.3 (MIN-R2-001)
- [ ] Add `gate1_correction_count` to workflow state and encode limit in state machine guard (MIN-R2-002)
- [ ] Distinguish "valid empty findings" from "invalid zero extractable findings" in Section 6.5 (MIN-R2-003)
- [ ] Clarify temporal reference and remove "substantive" in convergence criterion 2 (MIN-R2-004)
- [ ] Specify FINALIZED assembly procedure with holdout reintegration and file names (MIN-R2-005)
- [ ] Add lifecycle path for MINOR findings in zero-revision path (MIN-R2-006)
- [ ] Add clarifying note to Section 14.2 about codex config (MIN-R2-007)
