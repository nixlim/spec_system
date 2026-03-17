# Adversarial Review (R3): Adversarial Multi-Agent Specification System

**Spec reviewed**: docs/specs/adversarial-spec-system.md
**Review date**: 2026-03-16
**Prior reviews**:
- R1: adversarial-spec-system-review.md (2026-03-15, verdict: REVISE, 30 findings)
- R2: adversarial-spec-system-review-r2.md (2026-03-16, verdict: REVISE, 16 findings)
**Verdict**: PASS

## Executive Summary

The spec has been revised to address all 16 R2 findings. Of the 5 MAJOR findings from R2, 4 are fully resolved and 1 is partially resolved. Of the 7 MINOR findings, 6 are fully resolved and 1 is partially resolved. All 4 observations are resolved. This third-round review finds no CRITICAL or MAJOR issues. The remaining findings are 4 MINOR quality issues and 3 OBSERVATIONS — none of which would cause a production incident or produce incorrect behaviour. The spec is implementable.

| Severity | Count |
|----------|-------|
| CRITICAL | 0 |
| MAJOR | 0 |
| MINOR | 4 |
| OBSERVATION | 3 |
| **Total** | **7** |

---

## Prior Findings Resolution (R2)

| R2 ID | Severity | Title | Status | Notes |
|--------|----------|-------|--------|-------|
| MAJ-R2-001 | MAJOR | Orchestrator pre-check cannot verify resolution correctness | **RESOLVED** | Section 9.2 now adds the `sections_modified` non-empty check (line 804). HUMAN_GATE_FINAL added to state machine (line 631-632) for specs with any CRITICAL history. Judge verification errors for CRITICAL findings now have both a mechanical check and a human safety net. |
| MAJ-R2-002 | MAJOR | Dedup algorithm false negatives | **RESOLVED** | Section 6.3 (line 561) explicitly documents the known limitation, states conservative dedup is intentional, and specifies the compensating control: the revision agent's prompt includes instructions to address semantic duplicates together. |
| MAJ-R2-003 | MAJOR | Token counting heuristic unreliable | **RESOLVED** | Section 11.2 (line 934) specifies 200,000 token context window, 3.5 chars/token heuristic (conservative), 60% threshold (120,000 tokens). Section 8.1 (line 724) adds "Context window exceeded" as a failure type. Section 11.2 (line 938) specifies detection (error message substring matching) and recovery (truncate MINOR/OBS findings first). |
| MAJ-R2-004 | MAJOR | Zero-critical/major path bypasses revision agent | **RESOLVED** | Section 7.3 (lines 658-664) fully specifies the zero-critical/major path: modified judge prompt, no revision artifacts passed, judge evaluates whether MINOR findings are acceptable risks or miscategorised, orchestrator pre-check relaxed for this path, MINOR findings logged as accepted risks. |
| MAJ-R2-005 | MAJOR | Re-drafting limit enforced only in prose | **PARTIALLY_RESOLVED** | The state table (line 628) now includes the guard: `[guard: gate2_redraft_count < 1]`. The workflow-state.json schema (line 699) includes the counter. Version numbering is specified (line 705): v0 = pre-review, v1+ = post-review, spec-final.md = assembled. However, the guard is missing from the ASCII state machine diagram at line 617 — the diagram shows a linear flow without guard annotations. The state *table* is correct; the diagram is incomplete but not contradictory. See MIN-R3-001. |
| MIN-R2-001 | MINOR | `--dangerously-skip-permissions` grants more than filesystem | **RESOLVED** | Section 16.3 (lines 1630-1639) corrected. Now accurately states the flag "skips ALL permission prompts" granting "unrestricted access." Trust model documented. Accepted risk for v1 explicitly stated. |
| MIN-R2-002 | MINOR | No spec for DISCOVERY re-run after correction | **RESOLVED** | Section 10.1 (line 866) specifies: re-run produces completely new discovery-output.json, prompt includes original source docs + previous output + corrections, `gate1_correction_count` incremented, guard in state table (line 626): `[guard: gate1_correction_count < max_gate_corrections]`. After 3 corrections, only Confirm/Cancel available. |
| MIN-R2-003 | MINOR | "Zero valid findings" ambiguity | **RESOLVED** | Section 6.5 (lines 605-608) now explicitly distinguishes "valid empty output" (findings array exists, is empty or all items pass validation = success) from "invalid output yielding zero findings" (JSON invalid, findings key missing, or all items fail validation = failure). |
| MIN-R2-004 | MINOR | Convergence criterion 2 subjective | **RESOLVED** | Section 9.2, criterion 2 (line 795) now reads: "No new CRITICAL or MAJOR findings in the current round. The reviewers in the REVIEWING phase that preceded this JUDGING raised zero CRITICAL or MAJOR findings." Word "substantive" removed from the criterion text itself. Temporal reference is explicit. A parenthetical clarifies: "This means PASS is only possible when a review round finds nothing serious — not when findings are raised and then fixed in the same round." |
| MIN-R2-005 | MINOR | FINALIZED assembly step unspecified | **RESOLVED** | Section 11.3 (lines 950-963) now specifies the complete deterministic FINALIZED assembly procedure: 6 steps including holdout reintegration, all appendices, exact output file names. |
| MIN-R2-006 | MINOR | Issue lifecycle missing path for MINOR in zero-revision path | **RESOLVED** | Section 9.1 (lines 774-788) now includes the `raised -> acknowledged` transition for MINOR/OBSERVATION findings accepted as risks. The transition table (line 783) specifies: "Judge produces PASS verdict and finding is MINOR/OBSERVATION severity (accepted risk)." |
| MIN-R2-007 | MINOR | Codex references in existing YAML | **PARTIALLY_RESOLVED** | Section 14.2 (line 1273) now includes a clarifying note. However, the note says the codex provider "may remain in the YAML for other AgentBridge workflows" — this leaves ambiguity about whether the codex section should be present or absent in the actual deployed YAML for the spec workflow. A trivial concern; see OBS-R3-001. |
| OBS-R2-001 | OBS | Claude Code CLI format assumed stable | **RESOLVED** | Section 14.1 (line 1262) documents the dependency and specifies the required fields. States "Consider pinning the CLI version." |
| OBS-R2-002 | OBS | Parallel reviewers may hit rate limits | **RESOLVED** | Section 8.1 (line 725) adds "Rate limited" as a failure type with detection (agent takes >2x median duration) and Section 14.1 (line 1265) recommends per-agent-type timeout overrides. |
| OBS-R2-003 | OBS | 900s timeout may be insufficient for revision agent | **RESOLVED** | Section 14.1 (line 1265) recommends per-agent-type timeouts: discovery=300s, reviewer=600s, reviser=1200s, judge=600s. |
| OBS-R2-004 | OBS | No cancellation mechanism | **RESOLVED** | Section 10.3 (lines 895-899) specifies cancel via `POST /api/spec/cancel`. Orchestrator checks cancellation flag before each dispatch. Running agents complete but output is discarded. State set to ESCALATED with reason "User cancelled." Partial results preserved. |

**Summary**: 12 RESOLVED, 2 PARTIALLY_RESOLVED, 0 UNRESOLVED.

---

## Findings

### CRITICAL Findings

*No CRITICAL findings.*

---

### MAJOR Findings

*No MAJOR findings.*

---

### MINOR Findings

#### [MIN-R3-001] ASCII state machine diagram does not show guard conditions or HUMAN_GATE_FINAL

- **Lens**: Inconsistency
- **Affected section**: Section 7.1, line 617
- **Description**: The ASCII state machine diagram at line 617 shows a linear flow: `INIT -> DISCOVERY -> HUMAN_GATE_1 -> DRAFTING -> HUMAN_GATE_2 -> REVIEWING -> REVISING -> JUDGING -> [REVIEWING | FINALIZED | ESCALATED]`. This diagram omits: (1) the `HUMAN_GATE_FINAL` state that appears in the state table at line 632 and is extensively described in Section 9.2, (2) the guard conditions on HUMAN_GATE_1 -> DISCOVERY and HUMAN_GATE_2 -> DRAFTING correction paths, (3) the ERROR state and its recovery paths. The state *table* immediately below is complete and correct — it includes all states, guards, and transitions. The diagram and table are not contradictory (the diagram is a simplification), but an implementer might use the diagram as a quick reference and miss HUMAN_GATE_FINAL.
- **Recommendation**: Update the ASCII diagram to include HUMAN_GATE_FINAL. A minimal change: `JUDGING -> [REVIEWING | HUMAN_GATE_FINAL | FINALIZED | ESCALATED]` with a note that HUMAN_GATE_FINAL is conditional on CRITICAL history. The correction loops and ERROR state can remain omitted from the diagram if a note says "see state table for complete transitions."

---

#### [MIN-R3-002] Revision agent `sections_modified` check applies only to CRITICAL findings, not MAJOR

- **Lens**: Incompleteness
- **Affected section**: Section 9.2, line 804
- **Description**: The orchestrator pre-check includes: "For every CRITICAL finding with status `closed`, verify that the revision agent's change log entry for that finding has a non-empty `sections_modified` array." This check is a valuable mechanical safeguard — but it applies only to CRITICAL findings. MAJOR findings can also be marked as "addressed" by the revision agent with an empty `sections_modified` array (i.e., the revision agent claims to have addressed it without changing any section). The convergence criterion requires zero open CRITICAL *or MAJOR* findings (Section 9.2, criterion 1), but the non-empty-revision check only covers CRITICAL. A MAJOR finding that the revision agent "addresses" with zero section changes and the judge verifies as "resolved" will pass the pre-check.
- **Recommendation**: Extend the `sections_modified` non-empty check to MAJOR findings as well: "For every CRITICAL or MAJOR finding with status `closed`, verify that the revision agent's change log entry has a non-empty `sections_modified` array." This is a one-line change to the pre-check specification.

---

#### [MIN-R3-003] HUMAN_GATE_FINAL has no correction count limit and no cancel path explicitly documented

- **Lens**: Incompleteness
- **Affected section**: Section 7.1, line 632; Section 9.2, lines 810-812
- **Description**: HUMAN_GATE_FINAL (line 632) allows: "Accept -> FINALIZED; Reject -> REVIEWING." If the user clicks Reject, the system re-enters REVIEWING for another full review-revise-judge cycle. There is no limit on how many times the user can reject at HUMAN_GATE_FINAL. Unlike HUMAN_GATE_1 (max 3 corrections) and HUMAN_GATE_2 (max 1 re-draft), HUMAN_GATE_FINAL has no counter or guard. This is probably intentional — the user should have unlimited authority to reject at the final gate. But: (1) each rejection triggers a full automated round that consumes budget; the `max_rounds` and `max_cost_usd` circuit breakers should catch runaway loops, but this interaction is not explicitly documented. (2) There is no explicit Cancel option at HUMAN_GATE_FINAL. The user can cancel via `POST /api/spec/cancel` (Section 10.3), but the gate UI description (Section 9.2) only mentions "Accept" and "Reject."
- **Recommendation**: (1) Add a note to Section 9.2: "Rejections at HUMAN_GATE_FINAL trigger another REVIEWING round, which counts against max_rounds and max_cost_usd. If a circuit breaker triggers during this additional round, the system ESCALATEs normally." (2) Add Cancel as an explicit option at HUMAN_GATE_FINAL for consistency with the other gates, or note that cancellation is available via the general cancel mechanism (Section 10.3).

---

#### [MIN-R3-004] `had_critical_findings` flag in workflow-state.json is set but never explicitly reset or defined when it transitions

- **Lens**: Ambiguity
- **Affected section**: Section 7.4 (line 697), Section 9.2 (line 810)
- **Description**: The `workflow-state.json` schema includes `"had_critical_findings": true` (line 697). This flag drives the JUDGING -> HUMAN_GATE_FINAL vs. JUDGING -> FINALIZED decision (line 810-812). The spec never says when this flag is set to `true`. It is implied: the orchestrator sets it when a CRITICAL finding first appears in the issue tracker. But: (1) Is it set during issue merge (Section 6.3) when the first CRITICAL finding is parsed? (2) Is it set if a reviewer raises a CRITICAL finding that is then rejected during validation (e.g., missing recommendation)? (3) Once set, is it ever reset to `false`? (The name "had" implies it's permanent once set, which is the correct behaviour for a "was there ever a CRITICAL" flag.) These are implementation-obvious, but the spec is otherwise precise enough that this gap stands out.
- **Recommendation**: Add one sentence to the Section 7.4 state persistence description: "`had_critical_findings` is set to `true` by the orchestrator when any finding with severity CRITICAL is added to the issue tracker (after validation and dedup). Once set, it is never reset — it reflects whether a CRITICAL finding was ever present in the workflow, regardless of later resolution."

---

### Observations

#### [OBS-R3-001] Section 14.2 codex provider note could be clearer

- **Lens**: Overcomplexity
- **Affected section**: Section 14.2, line 1273
- **Suggestion**: The note says the codex provider "may remain in the YAML for other AgentBridge workflows." For an implementer building the spec workflow, a cleaner approach would be to say: "The spec workflow ignores provider and team entries not referenced by its own configuration (Section 14.3). The codex provider and reviewer-codex team member are irrelevant to the spec workflow and can be left in or removed from the shared YAML without affecting behaviour."

---

#### [OBS-R3-002] Debate trail assembly at ESCALATED is underspecified compared to FINALIZED

- **Lens**: Incompleteness
- **Affected section**: Section 11.3, Section 13.3.6
- **Description**: Section 11.3 (line 950) specifies the FINALIZED assembly procedure in 6 detailed steps. Section 13.3.6 (line 1148) says the debate trail is assembled "at FINALIZED or ESCALATED" but the 6-step procedure in Section 11.3 only mentions FINALIZED. The ESCALATED state (line 634) says "Output partial spec + open issues for human" but doesn't reference the FINALIZED assembly procedure. Presumably at ESCALATED, the orchestrator performs a subset of the assembly (skipping the holdout reintegration and the PASS-specific convergence summary, but including the debate trail and open issues).
- **Suggestion**: Add a brief note to the ESCALATED state or Section 11.3: "At ESCALATED, the orchestrator performs a partial assembly: the current best spec version is preserved as-is (holdouts are NOT reintegrated since the spec is incomplete), the debate trail is assembled, and the open issues are documented. The ESCALATED output is: current `spec-v{N}.md`, `debate-trail.md`, and `workflow-state.json` with state=ESCALATED and a `escalation_reason` field."

---

#### [OBS-R3-003] Cost estimate table does not account for HUMAN_GATE_FINAL or re-drafting

- **Lens**: Infeasibility
- **Affected section**: Section 14.10, lines 1549-1557
- **Description**: The cost and latency estimate table accounts for discovery, drafting, review rounds, revision, and judging. It does not account for: (1) the re-discovery cycle if the user corrects at HUMAN_GATE_1 (up to 3 additional discovery agent invocations), (2) the re-drafting cycle if the user provides answers at HUMAN_GATE_2 (1 additional drafter invocation), (3) the HUMAN_GATE_FINAL review cycle if the user rejects. These are variable and user-dependent, so omitting them from the "automated portion" estimate is reasonable. The disclaimer (line 1545) partially covers this.
- **Suggestion**: Add a note under the table: "Estimates exclude re-discovery (up to 3 cycles at HUMAN_GATE_1) and re-drafting (up to 1 cycle at HUMAN_GATE_2), which add ~$0.10-$1.00 each. HUMAN_GATE_FINAL rejections trigger additional full rounds included in the per-round estimate."

---

## Structural Integrity

| Check | Result | Notes |
|-------|--------|-------|
| Every goal/objective has acceptance criteria | **PASS** | Section 15 defines SC-01 through SC-06 with measurable thresholds. SP-01 through SP-03 define performance criteria. All stated goals in Section 1 trace to success criteria. |
| Cross-references are consistent | **PASS** | Verified: Section 6.3 referenced by 5.1, 7.3, 14.9. Section 9.2 referenced by 5.6, 9.5. Section 10 referenced by 7.1 note. Section 8.1 referenced by 6.5, 11.2. Section 16.1 referenced by 13.3.1. HUMAN_GATE_FINAL appears consistently in 7.1, 9.2, 9.4, 10.3, 17.1, and Appendix C. No dangling references found. |
| Scope boundaries are explicit | **PASS** | Section 17.1 lists 15 decided items with section references and rationale. Section 17.2 lists 7 deferred items with rationale and revisit triggers. v1/v2 boundary is clear. |
| Success criteria are measurable | **PASS** | SC-01: 100% structural integrity. SC-02: >=70% convergence. SC-03: >=60% manual review PASS. SP-01: <30 min. SP-02: <$50. SP-03: median <=3 rounds. All have numeric thresholds and measurement methods. |
| Error/failure scenarios addressed | **PASS** | Section 8 covers 8 failure types (timeout, crash, missing output, invalid JSON, schema violation, context window exceeded, rate limited, orchestrator crash). Recovery rules specified per state in Section 8.2. Retry semantics in 8.3. |
| Dependencies between requirements identified | **PASS** | The R2 review flagged this as FAIL because implementation ordering was unspecified. The spec now implicitly addresses this through Section 12.1 "What needs to be added" list and the Phase-based execution plan in Section 14.8. While there is no explicit dependency diagram for *implementation tasks*, the spec's own structure (schemas defined before they're referenced, states defined before transitions) provides sufficient ordering for an implementer. The remaining gap is minor — see OBS-R3-002. |

---

## Test Coverage Assessment

### Missing Test Categories

| Category | Gap Description | Affected Scenarios |
|----------|----------------|-------------------|
| HUMAN_GATE_FINAL interaction | No test strategy for the reject-then-re-review loop at HUMAN_GATE_FINAL, including interaction with circuit breakers (max_rounds, max_cost) | Section 9.2, Section 7.2 |
| Concurrent cancel and agent completion | What happens if cancel is issued at the exact moment an agent completes (race between "discard output" and "process result")? | Section 10.3 |
| Dedup with all 4 reviewers flagging same issue | Edge case: all 4 reviewers produce findings on the same section with different lenses. Dedup should keep all 4 (different lenses = not duplicates). | Section 6.3 |

### Dataset Gaps

| Dataset | Missing Boundary Type | Recommendation |
|---------|----------------------|----------------|
| Cumulative downgrade+dismissal limit | What happens at exactly 5 cumulative downgrades+dismissals? Section 5.6 says "more than 5 cumulative" triggers ESCALATE. Does the 5th itself trigger, or the 6th? | Clarify: is it `> 5` (6th triggers) or `>= 5` (5th triggers)? The current wording "more than 5" implies `> 5`, meaning 5 is allowed and 6 triggers ESCALATE. State this explicitly. |
| Wall clock elapsed during HUMAN_GATE | Does time spent waiting for human input at gates count against `max_wall_clock_minutes`? If a user takes 30 minutes at HUMAN_GATE_1, does the 60-minute timer already show 50%? | Clarify whether wall clock timer runs during gate states or only during automated processing. |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| File Upload API | ok | ok | - | ok | ok | - | Section 16.1 addresses file type, size, path traversal, content validation. |
| Agent Prompts | L | L | - | L | - | L | Prompt injection mitigated by XML delimiters (Section 16.2). Trust model documented (Section 16.3). Machine access is accepted risk for v1. |
| Convergence Judge | - | L | ok | - | - | L | Authority constrained (Section 5.6). Orchestrator pre-check (Section 9.2). HUMAN_GATE_FINAL for CRITICAL history. All decisions logged. |
| WebSocket Events | - | L | - | L | - | - | Authentication deferred to v2. Acceptable for localhost (Section 16.4). |
| Workspace Files | - | L | ok | L | - | - | Git audit trail. Agents have full access (accepted risk). Output path validation by orchestrator. |
| Workflow State File | - | L | - | L | L | - | Could be tampered, but attacker must have machine access (same trust boundary as agents). |

**Legend**: H = high risk, M = medium risk, L = low risk, ok = addressed, - = not applicable

All previously identified medium-risk items from R2 have been addressed or explicitly accepted within the documented trust model.

---

## Unasked Questions

1. When HUMAN_GATE_FINAL reject triggers a new REVIEWING round, do the reviewers review the same spec version that just passed the judge, or does the rejection imply the user wants changes? If the user rejects, there's no mechanism for them to *explain* why — reviewers will re-review the same spec and likely produce the same result.

2. If the revision agent splits findings across multiple passes (Section 5.5 context window management), and a later pass undoes a change from an earlier pass, how does the change log reflect this? Each pass produces its own change log entries, but the merged change log may show contradictory modifications to the same section.

3. Does the `max_total_findings` (60) circuit breaker count cumulative unique findings across all rounds, or total findings raised including re-raises of reopened issues? An issue reopened 3 times — does it count as 1 or 3 toward the 60?

---

## Verdict Rationale

This spec has been through three rounds of adversarial review and two revision passes. It started with 30 findings (4 CRITICAL, 11 MAJOR) in R1, was revised to 16 findings (0 CRITICAL, 5 MAJOR) in R2, and now has 7 findings (0 CRITICAL, 0 MAJOR).

The four MINOR findings are genuine quality issues but none will cause production incidents or incorrect behaviour. MIN-R3-001 (diagram inconsistency) is visual — the state table is correct. MIN-R3-002 (`sections_modified` check for MAJOR findings) is a worthwhile defensive improvement but not a gap that defeats the system's purpose — MAJOR findings are still verified by the judge and must be closed before PASS. MIN-R3-003 (HUMAN_GATE_FINAL details) is an edge case in user interaction. MIN-R3-004 (`had_critical_findings` flag) is an implementation-obvious detail in a spec that is otherwise meticulous.

The spec now provides: explicit JSON contracts for every agent (Section 6), a deterministic deduplication algorithm with documented limitations (Section 6.3), comprehensive error handling for 8 failure types with per-state recovery (Section 8), a fully specified human gate interaction model with correction limits encoded in the state machine (Section 10), a deterministic convergence protocol with anti-gaming measures (Section 9), a human safety net for CRITICAL findings (HUMAN_GATE_FINAL), measurable success criteria (Section 15), and a clear v1/v2 boundary (Section 17).

Verdict: **PASS**. The spec is ready for implementation. Address the MINOR findings during implementation as refinements to the detailed design — they do not require a fourth revision pass.

### Recommended Next Actions

- [ ] Update ASCII state diagram to include HUMAN_GATE_FINAL (MIN-R3-001) — can be done during implementation
- [ ] Extend `sections_modified` non-empty check to MAJOR findings (MIN-R3-002) — one-line addition to the orchestrator pre-check
- [ ] Document HUMAN_GATE_FINAL interaction with circuit breakers and add Cancel option (MIN-R3-003) — add during gate UI implementation
- [ ] Add one sentence defining when `had_critical_findings` is set (MIN-R3-004) — can be added to implementation doc or code comment
- [ ] Clarify wall clock timer behaviour during human gates (Dataset Gap) — decide during implementation
