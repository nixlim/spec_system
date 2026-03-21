# Adversarial Review: Dual-Provider Review (Codex + Claude)

**Spec reviewed**: `docs/plan/dual-provider-review-spec.md`
**Review date**: 2026-03-20
**Verdict**: REVISE

## Executive Summary

The spec has strong structural coverage with comprehensive BDD scenarios and traceability, but contains critical inconsistencies between the spec's failure thresholds and the existing codebase, ambiguous merge-matching semantics, and a missing concurrency model for shared state during dual-provider dispatch. 4 MAJOR findings require resolution before implementation.

| Severity | Count |
|----------|-------|
| CRITICAL | 0 |
| MAJOR | 4 |
| MINOR | 5 |
| OBSERVATION | 4 |
| **Total** | **13** |

---

## Findings

### MAJOR Findings

#### [MAJ-001] Failure threshold inconsistency: spec says 4, codebase says 2

- **Lens**: Inconsistency / Incorrectness
- **Affected section**: US-3 Acceptance Scenario 3-4, Section 4 Behavioral Contract, `review_dispatch.go` line 110
- **Description**: The spec states the system should "proceed with reduced coverage when 1-3 reviewers fail" and "escalate when 4 or more fail" (out of 8). However, the existing `review_dispatch.go` defines `maxFailuresAllowed = 1` and escalates when `len(failures) > maxFailuresAllowed` (i.e., at 2+ failures out of 4). The spec never acknowledges this existing constant or specifies how it should change. The threshold logic is not a simple doubling — with 8 reviewers, should the escalation threshold be "more than half fail" (>4), "same absolute number" (>1), or the spec's proposed ">3"? The spec asserts ">3" without justifying the choice or reconciling with the existing constant.
- **Impact**: An implementer may change `maxFailuresAllowed` to 3 without understanding the original rationale, or may miss changing it entirely, causing escalation at 2 failures out of 8.
- **Recommendation**: Add an explicit note in Section 10 (Implementation Notes) that `maxFailuresAllowed` in `review_dispatch.go` must be updated from 1 to 3. Provide the rationale: with 8 reviewers, losing up to 3 still guarantees at least one provider covers each lens group in the best case. Also specify: when running in claude-only mode (4 reviewers), should the threshold remain at 1, or also change to 3? The spec is silent on this.

---

#### [MAJ-002] DispatchReviewers signature incompatible with dual-runner dispatch

- **Lens**: Incompleteness / Incorrectness
- **Affected section**: US-3, Section 10 (Files to Modify: `review_dispatch.go`), `review_dispatch.go` current signature
- **Description**: The current `DispatchReviewers` function takes a single `AgentRunner` parameter. The spec says to "accept optional second runner" but never specifies the new function signature, how the dispatch loop maps lens groups to runners, or how the two runners' prompts/output paths are keyed. The existing function iterates over `reviewerLensGroups` (4 items) with a single runner. Doubling to 8 reviewers requires either: (a) two separate dispatch calls, (b) a new signature accepting a map of runner per agent, or (c) a wrapper that dispatches twice. The spec leaves this to the implementer.
- **Impact**: Different implementers will make different architectural choices. The dispatch function's current design (single runner, keyed by lens group name) fundamentally cannot dispatch two runners for the same lens group without a signature change, because the `prompts` and `outputPaths` maps are keyed by lens group.
- **Recommendation**: Specify the dispatch approach explicitly. Recommended: change `DispatchReviewers` to accept `runners map[string]AgentRunner` keyed by agent name (e.g., `"reviewer-clarity-claude"` -> ClaudeRunner, `"reviewer-clarity-codex"` -> CodexRunner), and change `prompts`/`outputPaths` maps to also be keyed by agent name instead of lens group. Add a BDD scenario for the function signature change.

---

#### [MAJ-003] Merge dedup matching semantics undefined for cross-provider findings

- **Lens**: Ambiguity
- **Affected section**: US-4 Acceptance Scenario 1, Section 4 Behavioral Contract bullet 6, TD-4 row 5
- **Description**: The existing `isDuplicate` function in `merge.go` matches on `(affected_section, lens, constitution_principle)`. The spec says "both providers find the same issue" should be deduplicated, but never defines what "same issue" means for cross-provider findings. Two different LLMs will produce different `affected_section` text, different `constitution_principle` values, and different `description` text for the same underlying issue. TD-4 row 5 hints at case-insensitive matching ("sec 3" vs "Sec 3"), but this is already handled by `normalizeSection`. The real problem is semantic equivalence: Claude might say `"Section 3: User Stories"` while Codex says `"US-3 Dual-Provider Review Dispatch"` for the same section. These will NOT match under the current algorithm.
- **Impact**: Cross-provider dedup will rarely trigger because two independent models will almost never produce identical `affected_section` and `lens` strings. The spec's core value proposition — "genuine multi-model adversarial diversity" with merged findings — will result in near-zero deduplication, doubling the judge's workload without the promised "cross-model consensus" signal.
- **Recommendation**: Either (1) acknowledge that cross-provider dedup will be rare and adjust expectations — the value is in diversity of findings, not consensus detection; or (2) specify a fuzzy matching strategy (e.g., normalize section references to a canonical form, or use embedding similarity with a threshold). Option 1 is simpler and more honest. If choosing option 1, update US-4 acceptance scenarios and TD-4 to reflect realistic dedup rates.

---

#### [MAJ-004] Claude-only mode failure threshold regression

- **Lens**: Incompleteness
- **Affected section**: US-3 Acceptance Scenario 5, Section 6 Regression Test Requirements
- **Description**: The spec states that when codex is disabled, "only 4 claude reviewers are dispatched (current behavior, with `-claude` suffix)". But the current behavior uses names like `reviewer-clarity` (no suffix), not `reviewer-clarity-claude`. The spec requires renaming existing reviewer agents to add `-claude` suffix (US-5 Acceptance Scenario 2), but never specifies whether the failure threshold in claude-only mode should remain at `maxFailuresAllowed = 1` (current) or change to 3 (the new dual-provider threshold). If the threshold is globally changed to 3, then in claude-only mode with 4 reviewers, the system would allow 3 failures and proceed with just 1 reviewer — a severe coverage degradation.
- **Impact**: Either claude-only mode is broken by the new threshold (3 of 4 failures allowed = near-useless review), or dual-provider mode uses the old threshold (escalate at 2 failures = overly aggressive). The spec does not address this.
- **Recommendation**: Specify that `maxFailuresAllowed` should be computed dynamically based on total reviewer count: e.g., `maxFailuresAllowed = totalReviewers / 2 - 1` (so 1 for 4 reviewers, 3 for 8 reviewers). Add a BDD scenario and test dataset row for the claude-only threshold.

---

### MINOR Findings

#### [MIN-001] Reviewer timeout change not addressed in regression analysis

- **Lens**: Incompleteness
- **Affected section**: US-6, Section 6 Regression Test Requirements
- **Description**: US-6 increases the reviewer timeout from 120s to 300s. The `orchestrator_review.go` currently hardcodes `TimeoutSeconds: 120`. The spec mentions this change but does not list it in the regression test requirements section, nor does it specify whether the timeout increase applies only to new workflows or also to in-progress workflows that are resumed.
- **Recommendation**: Add a regression note: "reviewer timeout changes from 120s to 300s; existing `TimeoutSeconds: 120` in `orchestrator_review.go` line 40 must be updated." Add a test case for resumed workflows with the old timeout value in state.

---

#### [MIN-002] Codex `--ephemeral` flag purpose not documented

- **Lens**: Ambiguity
- **Affected section**: US-1 Acceptance Scenario 1, Section 4 Integration Boundaries
- **Description**: The `--ephemeral` flag is specified in the codex CLI command but its purpose is never explained. Is it required for correctness (e.g., prevents sandbox pollution between reviewers) or optional (e.g., performance optimization)? If required, what happens if a future codex version removes it?
- **Recommendation**: Add a note in Section 8 (Assumptions) explaining what `--ephemeral` does and why it is required.

---

#### [MIN-003] `ReviewerResult.LensGroup` field insufficient for dual-provider tracking

- **Lens**: Incompleteness
- **Affected section**: US-3, `review_dispatch.go` ReviewerResult struct
- **Description**: The existing `ReviewerResult` struct has a `LensGroup` field but no `Provider` or `AgentName` field. With dual-provider dispatch, two results will have the same `LensGroup` value (e.g., both "clarity"). The spec never specifies how to distinguish them in the result type. The `CoverageLoss` field in `ReviewDispatchResult` also tracks only lens group names, not provider-qualified names.
- **Recommendation**: Specify that `ReviewerResult` gains an `AgentName string` field (e.g., "reviewer-clarity-codex") and that `CoverageLoss` uses agent names instead of bare lens group names.

---

#### [MIN-004] Agent name construction in `runReviewerWithRetries` hardcoded

- **Lens**: Incompleteness
- **Affected section**: `review_dispatch.go` line 249
- **Description**: The current code constructs agent names as `"reviewer-" + lensGroup` (e.g., "reviewer-clarity"). The spec requires provider-suffixed names ("reviewer-clarity-claude", "reviewer-clarity-codex") but never specifies where this naming logic lives. The `AgentError.Agent` field will need the full name for attribution in merge.
- **Recommendation**: Specify that the agent name is passed into the dispatch function (not constructed internally from lens group), so the caller controls the naming convention.

---

#### [MIN-005] Test TD-4 row 5 (case-insensitive match) is a false positive

- **Lens**: Incorrectness
- **Affected section**: Section 6, TD-4 row 5
- **Description**: TD-4 row 5 tests `("sec 3", AMB, MAJOR)` vs `("Sec 3", amb, CRIT)` as a duplicate match. The existing `normalizeSection` and lens comparison already handle this case-insensitively. This test adds no new coverage — it tests existing behavior, not new dual-provider behavior.
- **Recommendation**: Replace with a test that exercises the actual cross-provider scenario: two findings with semantically equivalent but textually different section references (e.g., "Section 3" vs "§3" or "US-3"), demonstrating whether they are or are not deduplicated.

---

### Observations

#### [OBS-001] OTEL telemetry gap for codex cost is permanent, not temporary

- **Lens**: Inoperability
- **Affected section**: FR-013, Section 4 Behavioral Contract
- **Description**: The spec states codex cost is "$0 (untracked)" and "costUSD=0 (untracked)". This means the dashboard's cost tracking will become increasingly inaccurate as codex usage grows. There is no plan to ever track codex costs. The `CostProvider` interface in the orchestrator could potentially be extended to aggregate codex costs from the OpenAI billing API, but this is never mentioned.
- **Suggestion**: Add a note acknowledging this as a known limitation and file a follow-up task for codex cost tracking if the feature proves valuable.

---

#### [OBS-002] Default `enable_codex_reviewers: true` is aggressive

- **Lens**: Overcomplexity
- **Affected section**: US-5 Acceptance Scenario 1, FR-005
- **Description**: The spec defaults `enable_codex_reviewers` to `true`. This means every user who upgrades gets dual-provider review by default, even if they have not installed codex. While graceful degradation handles this, it means every single user without codex will see a warning message on every workflow. This is noisy for the majority of users who may never use codex.
- **Suggestion**: Consider defaulting to `false` and requiring explicit opt-in. This follows the principle of least surprise and avoids warning-fatigue.

---

#### [OBS-003] 31 tests is a large TDD plan for the scope

- **Lens**: Overcomplexity
- **Affected section**: Section 6, TDD Plan
- **Description**: The TDD plan specifies 31 tests including 2 integration and 1 E2E test. Several tests overlap in coverage (e.g., TestCodexRunner_BuildCommand and TestCodexRunner_BuildCommand_SchemaFile both verify command construction). The overhead of maintaining 31 tests may exceed the complexity of the feature itself.
- **Suggestion**: Consider consolidating related command-construction tests into table-driven tests to reduce the number of test functions while maintaining coverage.

---

#### [OBS-004] Schema file written to temp directory on every invocation

- **Lens**: Overcomplexity
- **Affected section**: US-1 Acceptance Scenario 6, BDD: Schema file lifecycle
- **Description**: The spec requires writing a JSON schema file to a temp directory on every codex invocation and cleaning it up after. If the schema is static (the `ReviewerOutput` type does not change between invocations), this file could be written once at CodexRunner construction time and reused, avoiding filesystem churn on every invocation.
- **Suggestion**: Write the schema file once in the CodexRunner constructor and reuse it. Clean up in a finalizer or when the orchestrator is destroyed.

---

## Structural Integrity

| Check | Result | Notes |
|-------|--------|-------|
| Every user story has acceptance scenarios | PASS | All 7 user stories have acceptance scenarios |
| Every acceptance scenario has BDD scenarios | PASS | All acceptance scenarios are covered |
| Every BDD scenario has `Traces to:` reference | PASS | All BDD scenarios have traces |
| Every BDD scenario has a test in TDD plan | PASS | All BDD scenarios mapped to tests |
| Every FR appears in traceability matrix | PASS | All 17 FRs appear |
| Every BDD scenario in traceability matrix | PASS | Coverage verified |
| Test datasets cover boundaries/edges/errors | PASS | TD-1 through TD-4 cover boundaries |
| Regression impact addressed | FAIL | Timeout change (120->300s) not listed in regression section; claude-only threshold behavior unspecified |
| Success criteria are measurable | PASS | SC-001 through SC-007 are measurable |

---

## Test Coverage Assessment

### Missing Test Categories

| Category | Gap Description | Affected Scenarios |
|----------|----------------|-------------------|
| Concurrency (shared state) | No test for race conditions when 8 goroutines write to shared `results`/`failures` slices simultaneously | BDD: Eight reviewers dispatched |
| Threshold regression | No test for claude-only mode using the new threshold constant | BDD: Claude-only fallback dispatch |
| Timeout change | No test that reviewer timeout is 300s (not 120s) | US-6 scenarios |
| Schema file cleanup on crash | No test that temp schema file is cleaned up if codex process crashes or is killed | BDD: Schema file lifecycle |

### Dataset Gaps

| Dataset | Missing Boundary Type | Recommendation |
|---------|----------------------|----------------|
| TD-3 (Dispatch Failures) | Claude-only with new threshold | Add rows for 4-reviewer dispatch with 0, 1, 2, 3, 4 failures |
| TD-4 (Merge Attribution) | Semantic near-miss | Add a row where two providers reference the same issue with different section text to demonstrate non-dedup |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| Codex CLI subprocess | ok | risk | ok | risk | risk | ok | T: prompt/output transit via filesystem (temp files). I: stderr may leak API keys or internal errors. D: 4 concurrent codex processes could exhaust API rate limits. |
| JSON schema temp file | ok | risk | ok | ok | ok | ok | T: malicious process could modify schema file between write and codex read (TOCTOU). |
| Reviewer output files | ok | ok | ok | ok | ok | ok | Written to workspace dir with 0644 perms |
| Codex authentication | risk | ok | ok | risk | ok | ok | S: codex auth token could be expired/stolen. I: auth errors in stderr logged verbatim. |

**Legend**: risk = identified threat not mitigated in spec, ok = adequately addressed or not applicable

---

## Unasked Questions

1. **What is the escalation threshold in claude-only mode?** The spec changes the threshold for 8 reviewers but never specifies the 4-reviewer threshold. Should it remain at 1 (current), or scale proportionally?

2. **How are codex reviewer prompts differentiated from claude prompts?** The spec says codex reviewers cover "all 4 lens groups independently" with the same prompts, but different models may need different prompt engineering. Are the prompts identical?

3. **What happens if codex and claude produce contradictory findings?** E.g., codex says "Section 3 is correct" (zero findings) while claude raises a CRITICAL on Section 3. The spec handles the merge mechanics but not the semantic contradiction.

4. **How does the dashboard display 8 reviewers?** The current UI presumably shows 4 reviewer slots. Doubling to 8 requires UI changes not mentioned in the spec.

5. **What is the retry backoff strategy for codex rate limit errors (E10)?** The spec says "retry with backoff" but does not specify whether this uses the existing `RetryDelay` exponential backoff or a rate-limit-specific strategy (e.g., respecting `Retry-After` headers).

6. **Should codex reviewers use the same `--cd <workspace>` as claude?** Claude uses `WorkspaceDir` for file access. Codex presumably needs the same, but the spec does not confirm whether codex needs read access to the same files and whether `--cd` provides this.

7. **What minimum codex CLI version is required?** Section 8 says ">=0.114.0" but does not specify a version check. Should the availability check (`exec.LookPath`) also verify the version?

---

## Verdict Rationale

The spec is well-structured and thorough in its BDD coverage, but has a critical gap in reconciling the new dual-provider dispatch model with the existing codebase's failure thresholds and function signatures. MAJ-001 and MAJ-004 together create a situation where the implementer must make undocumented architectural decisions about failure thresholds that affect both dual-provider and claude-only modes. MAJ-002 identifies that the core dispatch function's signature is fundamentally incompatible with the spec's requirements without changes the spec does not describe. MAJ-003 questions whether the spec's central value proposition (cross-model consensus detection) will actually work given the deterministic dedup algorithm.

None of these are blocking — they are addressable with clarifications — but implementing without resolving them will produce inconsistent behavior.

### Recommended Next Actions

- [ ] Resolve dynamic failure threshold for both 4-reviewer and 8-reviewer modes (MAJ-001, MAJ-004)
- [ ] Specify the `DispatchReviewers` signature change or architectural approach for dual-runner dispatch (MAJ-002)
- [ ] Clarify cross-provider dedup expectations and update test datasets accordingly (MAJ-003)
- [ ] Add `ReviewerResult.AgentName` field to the spec (MIN-003, MIN-004)
- [ ] Add timeout change to regression test requirements (MIN-001)
