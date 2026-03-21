# Code Review: Dual-Provider Review (Codex + Claude) -- Iteration 3

**Spec reviewed**: `docs/plan/dual-provider-review-spec.md`
**Task file**: `.tasks/dual-provider-review.task.json`
**Review date**: 2026-03-21
**Iteration**: 3
**Previous fixes verified**: CRIT-001 (nil SchemaBytes), CRIT-002 (holdout unwired), MAJ-001 (single schema), MAJ-001-v2 (claude holdout timeout), MAJ-002 (no escalation on both-fail)
**Verdict**: PASS
**Spec compliance**: 11/11 user stories implemented (100%)

## Executive Summary

The dual-provider review feature is well-implemented across all 11 user stories. All critical and major findings from iterations 1 and 2 have been verified as fixed. This iteration's focused review confirms: holdout dispatch is correctly wired with a separate `codexHoldoutRunner` using `HoldoutOutputSchema`, reviewer dispatch uses `ReviewerOutputSchema`, timeout configuration is correctly plumbed through separate config fields, and escalation behavior matches the spec. All 67 tests pass. Two minor findings remain around test coverage and a workflow handler observation.

| Metric | Value |
|--------|-------|
| Files reviewed | 16 files |
| Functional requirements | 11 implemented / 11 total |
| BDD scenarios with tests | 28 covered / 32 total |
| Tasks genuinely complete | 10 verified / 10 claimed |
| Wiring gaps | 0 stubs, 0 unwired, 0 partial |
| Tests passing | 67 pass / 67 total |

| Severity | Count |
|----------|-------|
| CRITICAL | 0 |
| MAJOR | 0 |
| MINOR | 2 |
| OBSERVATION | 2 |
| **Total** | **4** |

---

## Previous Findings Verification

| Finding | Status | Evidence |
|---------|--------|----------|
| CRIT-001 (nil SchemaBytes) | FIXED | `orchestrator.go:310-311` -- both runners receive schema bytes from `ReviewerOutputSchema()` / `HoldoutOutputSchema()` |
| CRIT-002 (holdout unwired) | FIXED | `orchestrator.go:416` -- `codexHoldoutRunner` stored on struct; `orchestrator_review.go:173` -- used in dispatch |
| MAJ-001 (single schema) | FIXED | Two separate `DefaultCodexRunner` calls at `orchestrator.go:310-311` with distinct schema bytes |
| MAJ-001-v2 (claude holdout timeout) | FIXED | `orchestrator_review.go:142,162` -- uses `o.config.HoldoutTimeoutSeconds` via `o.runner.Run()` directly |
| MAJ-002 (no escalation on both-fail) | FIXED | `orchestrator_review.go:240` -- returns error; caller at line 102-105 escalates |

---

## Spec Compliance Matrix

| Requirement | Status | Evidence |
|-------------|--------|----------|
| US-1: Codex Runner Implementation | IMPLEMENTED | `codex_runner.go:17-161` -- CodexRunner with stdin delivery, --output-schema, SIGTERM/SIGKILL timeout, costUSD=0 |
| US-2: Graceful Degradation | IMPLEMENTED | `orchestrator.go:304-316` -- exec.LookPath check, warning log, nil runner fallback |
| US-3: Dual-Provider Review Dispatch | IMPLEMENTED | `review_dispatch.go:122-262` -- 8 goroutines when codexRunner non-nil, failure tolerance scaling |
| US-4: Provider-Attributed Finding Merge | IMPLEMENTED | `merge.go` + `merge_test.go:564-727` -- raised_by preserves provider-prefixed names, severity escalation |
| US-5: Team Configuration Extension | IMPLEMENTED | `team.go:70-134` -- 12 agents when codex enabled, validation rejects codex on non-reviewer roles |
| US-6: Reviewer Timeout Configuration | IMPLEMENTED | `config.go:64` -- ReviewerTimeoutSeconds (default 300), passed via `orchestrator_review.go:52` |
| US-7: Structured Output Enforcement | IMPLEMENTED | `codex_runner.go:63-73` -- schema temp file created and passed via --output-schema, cleaned up after |
| US-8: Dual-Provider Holdout Generation Dispatch | IMPLEMENTED | `orchestrator_review.go:128-241` -- parallel claude+codex holdout agents, graceful degradation |
| US-9: Holdout Output Merge with Provider Attribution | IMPLEMENTED | `holdout_merge.go:26-46` -- MergeHoldoutMarkdown with provider headers, attribution notes |
| US-10: Codex Holdout Agent Structured Output | IMPLEMENTED | `orchestrator.go:311` -- codexHoldoutRunner uses HoldoutOutputSchema() |
| US-11: Holdout Agent Timeout Configuration | IMPLEMENTED | `config.go:67` -- HoldoutTimeoutSeconds (default 300), used at `orchestrator_review.go:142` |

**Compliance score**: 11/11 (100%)

---

## Focused Review Areas (Iteration 3)

### 1. Holdout Dispatch Correctness

`dispatchHoldoutGeneration` in `orchestrator_review.go:128-241` is correctly implemented:

- **Dual dispatch**: Claude holdout uses `o.runner.Run()` (line 162), codex holdout uses `o.codexHoldoutRunner.Run()` (line 179) -- separate runner instances.
- **Parallel execution**: Both goroutines launched concurrently via `sync.WaitGroup` (lines 152-194).
- **Result collection**: Channel-based collection with proper close after wg.Wait() (line 195).
- **Graceful degradation**: Either provider can fail without escalation (lines 227-236); only both-fail triggers escalation (line 240).

### 2. Schema Enforcement

- **Reviewer runner**: Created with `ReviewerOutputSchema()` at `orchestrator.go:310`.
- **Holdout runner**: Created with `HoldoutOutputSchema()` at `orchestrator.go:311`.
- **Separation confirmed**: Two distinct `DefaultCodexRunner` calls, each with the correct schema bytes.

### 3. Timeout Configuration

- **Reviewers**: `o.config.ReviewerTimeoutSeconds` passed to `DispatchReviewers` via `dispatchCfg.TimeoutSeconds` at `orchestrator_review.go:51-52`.
- **Holdouts**: `o.config.HoldoutTimeoutSeconds` used directly at `orchestrator_review.go:142` for both claude and codex holdout agents.
- **Correct separation**: Reviewer and holdout timeouts are independent config fields.

### 4. Escalation Behavior

- **Both holdout fail**: Returns error at line 240, caller at line 102-105 calls `o.escalateFrom(StateReviewing)`.
- **Single holdout fail**: Proceeds with available output (lines 227-236), returns nil.
- **Reviewer failure**: `DispatchReviewers` uses `maxFailuresAllowed = totalReviewers/2 - 1` (line 204). For 8 reviewers: 3 allowed. For 4 reviewers: 1 allowed.

### 5. Test Coverage

Tests cover: dual-provider dispatch (8 reviewers), failure tolerance, backward compat (claude-only), cross-provider merge with attribution, holdout merge (both providers, single provider, no dedup, neither provider).

---

## Task Audit

| Task ID | Title | Verified Status | Details |
|---------|-------|----------------|---------|
| add-codex-config-fields | Add codex configuration fields | GENUINELY COMPLETE | All 4 fields present with correct defaults and YAML tags |
| add-team-config-codex-agents | Extend team configuration | GENUINELY COMPLETE | 12 agents when enabled, validation correct |
| implement-codex-runner | Implement CodexRunner | GENUINELY COMPLETE | Full AgentRunner impl with stdin, schema, timeout |
| add-reviewer-output-schema | Create ReviewerOutput JSON schema | GENUINELY COMPLETE | Valid JSON Schema with required finding fields |
| add-holdout-output-schema | Create HoldoutOutput JSON schema | GENUINELY COMPLETE | Valid JSON Schema with required holdout fields |
| add-codex-availability-check | Add codex CLI availability detection | GENUINELY COMPLETE | LookPath check, warning, nil runner fallback |
| extend-review-dispatch-dual-provider | Extend review dispatch | GENUINELY COMPLETE | 8 reviewers, failure tolerance, backward compat |
| extend-merge-provider-attribution | Extend finding merge | GENUINELY COMPLETE | Provider attribution in raised_by and dedup_log |
| implement-holdout-dual-dispatch | Implement dual-provider holdout | GENUINELY COMPLETE | Parallel dispatch, merge, graceful degradation |
| wire-codex-into-orchestrator | Wire codex into orchestrator | GENUINELY COMPLETE | Both runners wired, config passed through |

---

## Wiring & Integration Audit

No wiring gaps detected -- all implemented components are connected end-to-end.

- `CodexRunner` is instantiated in `newOrchestrator` (orchestrator.go:310-311) and stored on the Orchestrator struct.
- `codexRunner` is passed to `DispatchReviewers` at orchestrator_review.go:53.
- `codexHoldoutRunner` is used in `dispatchHoldoutGeneration` at orchestrator_review.go:173.
- `ReviewerOutputSchema()` and `HoldoutOutputSchema()` are both called and their bytes stored in the respective runners.
- `MergeHoldoutMarkdown` is called at orchestrator_review.go:228.
- `DefaultTeamConfig` receives the codex availability flag at orchestrator.go:321.
- Workflow handler passes `manager.config` (which includes all codex fields) to the orchestrator at workflow_handler.go:470.

---

## Code Findings

### MINOR Findings

#### [MIN-001] Workflow handler does not expose codex config overrides in start request

- **Lens**: Correctness
- **File**: `internal/api/workflow_handler.go:356-361`
- **Issue**: The `startWorkflowRequest` struct has no fields for `enable_codex_reviewers`, `codex_model`, `reviewer_timeout_seconds`, or `holdout_timeout_seconds`. The handler uses `manager.config` directly (line 470), meaning per-workflow codex config overrides are not possible via the API. The spec (US-5, US-6, US-11) implies these should be configurable, but the spec also states the config comes from YAML, so this may be intentional.
- **Fix**: If per-workflow overrides are desired, add optional fields to `startWorkflowRequest` and merge them over `manager.config`. Otherwise, document that codex config is server-level only.

#### [MIN-002] Missing integration test for dispatchHoldoutGeneration with mock runners

- **Lens**: Testing Quality
- **File**: `internal/specworkflow/orchestrator_test.go`
- **Issue**: The orchestrator tests (`setupOrch`) set `EnableCodexReviewers = false` (line 120), meaning no integration test exercises the dual-provider holdout path through the full orchestrator. While `holdout_merge_test.go` covers `MergeHoldoutMarkdown` and `review_dispatch_test.go` covers dual-provider dispatch, there is no test that verifies `dispatchHoldoutGeneration` is called with a non-nil `codexHoldoutRunner` through `handleReviewing`. The unit tests for the individual components are solid, but an integration test would increase confidence.
- **Fix**: Add a test variant of `setupOrch` that sets `EnableCodexReviewers = true` with a mock `LookPathFunc` and verifies holdout dispatch produces merged output from both providers.

---

### Observations

#### [OBS-001] Claude holdout uses o.runner.Run() directly (correct)

- **Lens**: Correctness
- **File**: `internal/specworkflow/orchestrator_review.go:162`
- **Observation**: The claude holdout agent calls `o.runner.Run(holdoutPrompt, claudeJSONPath, timeout)` directly rather than using `dispatchAgent`. This is correct -- `dispatchAgent` hardcodes 120s timeout, while this path uses the configurable `HoldoutTimeoutSeconds`. This was the fix from iteration 2 MAJ-001.

#### [OBS-002] Codex holdout cost correctly discarded

- **Lens**: Correctness
- **File**: `internal/specworkflow/orchestrator_review.go:179`
- **Observation**: The codex holdout result discards the cost return value (`_, _, _, _, runErr := o.codexHoldoutRunner.Run(...)`). The `holdoutResult.cost` is set to 0 (line 189). This is correct per spec: "costUSD is always 0 (codex cost not tracked)".

---

## Test Results

```
ok  github.com/foundry-zero/adversarial-spec-system/internal/specworkflow  2.382s
```

All 67 tests pass, 0 failures, 0 skips.

Key dual-provider tests verified:
- TestDispatch_DualProvider_8Reviewers: 8 goroutines, correct agent names
- TestDispatch_DualProvider_FailureTolerance: 4/8 codex failures triggers escalation
- TestDispatch_BackwardCompat_ClaudeOnly: 4 claude-only results
- TestMerge_CrossProviderDuplicate: severity escalation, dual raised_by
- TestMerge_EightInputs: 8 inputs merge correctly
- TestMerge_DedupLogProviderAttribution: dedup log records providers
- TestHoldoutMerge_BothProviders: merged output with attribution
- TestHoldoutMerge_SingleProvider: claude-only with note
- TestHoldoutMerge_NoDeduplicate: no dedup across providers
- TestTeamConfig_WithCodex: 12 agents, correct providers

| Status | Count |
|--------|-------|
| PASS | 67 |
| FAIL | 0 |
| SKIP | 0 |

---

## Verdict Rationale

The implementation is spec-compliant across all 11 user stories. All five focus areas from this iteration check out:

1. **Holdout dispatch** uses the correct separate runners and runs them in parallel.
2. **Schema enforcement** correctly passes ReviewerOutputSchema to reviewer runners and HoldoutOutputSchema to holdout runners.
3. **Timeout configuration** correctly separates ReviewerTimeoutSeconds and HoldoutTimeoutSeconds.
4. **Escalation behavior** correctly handles both-fail (escalate) and single-fail (proceed) cases.
5. **Test coverage** is comprehensive for unit tests; integration coverage has a minor gap (MIN-002).

The two minor findings are quality improvements rather than correctness issues. The implementation is ready for merge.

### Recommended Next Actions

- [ ] Consider adding an integration test for dual-provider holdout dispatch through the orchestrator (MIN-002)
- [ ] Document whether codex config is server-level or per-workflow (MIN-001)

Implementation verified. Ready for merge.
