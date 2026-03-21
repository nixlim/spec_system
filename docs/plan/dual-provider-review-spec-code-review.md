# Code Review: Dual-Provider Review (Codex + Claude)

**Spec reviewed**: docs/plan/dual-provider-review-spec.md
**Review date**: 2026-03-21
**Verdict**: REVISE
**Spec compliance**: 8/11 user stories substantially implemented (73%)

## Executive Summary

The dual-provider reviewer dispatch, merge, team configuration, config, codex runner, and output schema tasks are well-implemented with solid test coverage. However, the codex runner is wired with `nil` schema bytes in production (a critical bug), the holdout dual-dispatch is entirely unimplemented (US-8, US-9, US-10 missing), and the orchestrator integration tests disable codex entirely, leaving the end-to-end wiring untested. Tests pass (48 tests, 0 failures) but they do not cover the wiring gap.

| Metric | Value |
|--------|-------|
| Files reviewed | 19 files |
| Tasks genuinely complete | 8 verified / 11 total |
| Wiring gaps | 1 critical, 1 unwired, 0 partial |
| Tests passing | 48 pass / 48 total |

| Severity | Count |
|----------|-------|
| CRITICAL | 2 |
| MAJOR | 3 |
| MINOR | 2 |
| OBSERVATION | 2 |
| **Total** | **9** |

---

## Task Audit

| Task ID | Title | Claimed Status | Verified Status | Details |
|---------|-------|---------------|----------------|---------|
| add-codex-config-fields | Add codex config fields | open | GENUINELY COMPLETE | All 4 fields present, defaults correct, validation passes |
| add-team-config-codex-agents | Extend team with codex agents | open | GENUINELY COMPLETE | 12 agents with codex, 8 without, validation rejects codex on non-reviewer |
| implement-codex-runner | Implement CodexRunner | open | GENUINELY COMPLETE | Implements AgentRunner, stdin delivery, SIGTERM/SIGKILL, costUSD=0 |
| add-reviewer-output-schema | Reviewer output schema | open | GENUINELY COMPLETE | Valid JSON Schema with required fields |
| add-holdout-output-schema | Holdout output schema | open | GENUINELY COMPLETE | Valid JSON Schema with required fields |
| add-codex-availability-check | Codex availability check | open | GENUINELY COMPLETE | LookPath check, warning logged, graceful degradation |
| extend-review-dispatch-dual-provider | Dual-provider review dispatch | open | GENUINELY COMPLETE | 8 reviewers, failure tolerance, backward compat |
| extend-merge-provider-attribution | Merge with provider attribution | open | GENUINELY COMPLETE | Cross-provider dedup, raised_by preserved, dedup_log attribution |
| implement-holdout-dual-dispatch | Dual-provider holdout dispatch | open | NOT STARTED | Only MergeHoldoutMarkdown exists; no dispatch logic wired |
| wire-codex-into-orchestrator | Wire codex into orchestrator & API | open | INCOMPLETE | Reviewer wiring done; holdout wiring missing; SchemaBytes=nil |
| add-regression-and-backward-compat-tests | Regression tests | open | GENUINELY COMPLETE | 3 backward-compat tests present and passing |

### Incomplete Task Details

#### Task wire-codex-into-orchestrator: Wire codex into orchestrator & API

**Acceptance criteria from task:**
1. "Orchestrator.handleReviewing passes codexRunner to the review dispatch function" -- VERIFIED at `orchestrator_review.go:52`
2. "Orchestrator.handleHoldoutGeneration passes codexRunner to the holdout dispatch function" -- NOT MET: No handleHoldoutGeneration function exists; no reference to holdout dispatch in orchestrator
3. "WorkflowHandler passes EnableCodexReviewers, CodexModel, ReviewerTimeoutSeconds, and HoldoutTimeoutSeconds from request/config to the orchestrator" -- PARTIAL: Config is passed through `manager.config` which includes codex fields, but no explicit mapping verified
4. "When codexRunner is nil, dispatch functions receive nil and operate in claude-only mode" -- VERIFIED at `orchestrator_review.go:52` (nil is passed)
5. "Integration test: full orchestrator review phase with mock runners dispatches 8 reviewers when codex runner is non-nil" -- NOT MET: orchestrator tests set `EnableCodexReviewers = false`
6. "Integration test: full orchestrator review phase with nil codex runner dispatches 4 reviewers" -- PARTIAL: Tests run claude-only but don't explicitly verify count

#### Task implement-holdout-dual-dispatch: Dual-provider holdout dispatch

**Acceptance criteria from task:**
1. "When codex_runner is non-nil, 2 holdout goroutines are launched concurrently" -- NOT MET: No dispatch code exists
2. "When codex_runner is nil, only holdout-claude is launched" -- NOT MET: No dispatch code exists
3. "Output files follow naming: holdout-{provider}-round-{N}.json" -- NOT MET
4. "Merged holdouts-round-{N}.md contains sections attributed to each provider" -- PARTIAL: MergeHoldoutMarkdown() exists but is never called from orchestrator
5. "When codex holdout fails and claude succeeds, workflow proceeds with claude-only holdouts" -- NOT MET
6. "When both holdout agents fail, the workflow escalates" -- NOT MET
7. "No deduplication is applied to holdout scenarios across providers" -- VERIFIED in MergeHoldoutMarkdown (concatenates, no dedup)
8. "When only one provider succeeds, merged file contains only that provider's scenarios with attribution note" -- VERIFIED in MergeHoldoutMarkdown

---

## Wiring & Integration Audit

### Stubs Found

| File | Function/Method | Stub Pattern | Severity |
|------|----------------|--------------|----------|
| None found | | | |

### Implemented but Unwired

| Package / Component | Has Tests | Called from Binary | Status |
|---------------------|-----------|-------------------|--------|
| `holdout_merge.go` MergeHoldoutMarkdown | YES (5 tests) | NO -- never called from orchestrator or any handler | UNWIRED |
| `holdout_merge.go` HoldoutDispatchResult | NO | NO -- struct defined but never instantiated | UNWIRED |
| `holdout_output_schema.go` HoldoutOutputSchema() | YES (2 tests) | NO -- never called from orchestrator or codex runner | UNWIRED |

### Partial Wiring

| Component | What's Connected | What's Missing |
|-----------|-----------------|----------------|
| CodexRunner in Orchestrator | Created via `DefaultCodexRunner(model, workspace, nil)` at `orchestrator.go:308` | **SchemaBytes is nil** -- the runner will write 0 bytes to the schema temp file, making `--output-schema` point to an empty file. Should be `ReviewerOutputSchema()` |
| Orchestrator codex for holdout | codexRunner field is set | No holdout dispatch function calls codexRunner for holdout generation |

---

## Code Findings

### CRITICAL Findings

#### [CRIT-001] CodexRunner created with nil SchemaBytes in production

- **Lens**: Correctness
- **File**: `internal/specworkflow/orchestrator.go:308`
- **Code**:
  ```go
  codexRunner = DefaultCodexRunner(cfg.Config.CodexModel, cfg.WorkspaceDir, nil)
  ```
- **Issue**: The orchestrator passes `nil` as `schemaBytes` to `DefaultCodexRunner`. When `Run()` is called, it writes `nil` bytes to the temp schema file (line 69 of codex_runner.go: `schemaFile.Write(r.SchemaBytes)` writes 0 bytes). The `--output-schema` flag then points to an empty file. This means codex will have no schema enforcement in production, defeating the purpose of US-7 (Structured Output Enforcement).
- **Impact**: Codex will produce unstructured output in production, likely failing JSON parsing and causing all codex reviewers to fail after retries. The entire dual-provider feature is effectively broken.
- **Fix**: Pass the appropriate schema bytes:
  ```go
  codexRunner = DefaultCodexRunner(cfg.Config.CodexModel, cfg.WorkspaceDir, ReviewerOutputSchema())
  ```
  Note: This only handles reviewer schema. For holdout, the CodexRunner would need a separate instance or the schema would need to be swapped per-invocation, which is a design gap.

#### [CRIT-002] Holdout dual-dispatch entirely unimplemented

- **Lens**: Correctness (Wiring)
- **File**: `internal/specworkflow/orchestrator_review.go` (missing), `internal/specworkflow/orchestrator.go` (no holdout references)
- **Issue**: US-8 (Dual-Provider Holdout Dispatch), US-9 (Holdout Merge with Attribution), and US-10 (Codex Holdout Structured Output) have no orchestrator wiring. The `MergeHoldoutMarkdown` function and `HoldoutDispatchResult` struct exist in `holdout_merge.go` but are never called. The `HoldoutOutputSchema()` function exists but is never passed to any runner. There is no `handleHoldoutGeneration` or equivalent function. The orchestrator's main loop has no `StateHoldoutGeneration` case.
- **Impact**: Holdout generation runs claude-only even when codex is available. Three user stories (US-8, US-9, US-10) and one task (`implement-holdout-dual-dispatch`) are not implemented.
- **Fix**: Implement the holdout dispatch handler that: (1) launches parallel holdout agents when codexRunner is non-nil, (2) creates a CodexRunner with `HoldoutOutputSchema()` for holdout invocations, (3) calls `MergeHoldoutMarkdown()`, (4) handles graceful degradation when codex fails.

---

### MAJOR Findings

#### [MAJ-001] CodexRunner uses a single SchemaBytes for all invocations

- **Lens**: Correctness
- **File**: `internal/specworkflow/codex_runner.go:26`
- **Issue**: The `CodexRunner` struct stores `SchemaBytes` as a fixed field set at construction time. This means a single runner instance can only use one schema. For reviewer dispatch this works (all reviewers use `ReviewerOutputSchema`), but for holdout dispatch, a different schema (`HoldoutOutputSchema`) is needed. The current design requires creating separate `CodexRunner` instances per schema, which is not reflected in the orchestrator's single `codexRunner` field.
- **Impact**: When holdout dispatch is implemented, the orchestrator will need either two CodexRunner instances or a way to pass schema per-invocation. The current architecture doesn't support this.
- **Fix**: Either (a) create separate `codexReviewerRunner` and `codexHoldoutRunner` fields on the orchestrator, or (b) change the `Run` method to accept schema bytes as a parameter.

#### [MAJ-002] Orchestrator integration tests skip codex entirely

- **Lens**: Testing Quality
- **File**: `internal/specworkflow/orchestrator_test.go:120`
- **Code**:
  ```go
  cfg.EnableCodexReviewers = false // Disable codex in tests to avoid real CLI dependency
  ```
- **Issue**: All orchestrator tests disable codex. The task `wire-codex-into-orchestrator` requires integration tests verifying "full orchestrator review phase with mock runners dispatches 8 reviewers when codex runner is non-nil" and "full orchestrator review phase with nil codex runner dispatches 4 reviewers". Neither exists. The `LookPathFunc` override was added specifically for testability but is never used in any test.
- **Impact**: The end-to-end wiring from orchestrator -> dispatch with codex runner is untested. CRIT-001 (nil SchemaBytes) was not caught because of this gap.
- **Fix**: Add orchestrator-level tests that: (1) set `LookPathFunc` to return success, (2) provide a mock codex runner, (3) verify 8 review files are produced, (4) verify output paths include `-codex-` suffix.

#### [MAJ-003] Codex reviewer timeout hardcoded in team config, not from workflow config

- **Lens**: Correctness
- **File**: `internal/specworkflow/team.go:119`
- **Code**:
  ```go
  TimeoutSeconds: 300,
  ```
- **Issue**: Codex reviewer agents in `DefaultTeamConfig` have `TimeoutSeconds: 300` hardcoded, while claude reviewers have `TimeoutSeconds: 120`. However, the actual dispatch in `orchestrator_review.go:50` uses `o.config.ReviewerTimeoutSeconds` for the dispatch config, which applies to all reviewers equally. The `TimeoutSeconds` field on `AgentConfig` in the team config is never read by the dispatch logic. This is dead configuration that misleads readers.
- **Impact**: The timeout on team config agents is informational only and could diverge from the actual timeout used. If someone reads the team config to understand timeouts, they'll get wrong information (claude shows 120s, codex shows 300s, but both actually use `ReviewerTimeoutSeconds` which defaults to 300s).
- **Fix**: Either (a) remove `TimeoutSeconds` from `AgentConfig` since it's not used by dispatch, or (b) have the dispatch read the per-agent timeout from team config instead of the global config value.

---

### MINOR Findings

#### [MIN-001] Schema file cleanup could fail silently on temp file creation error paths

- **Lens**: Error Handling
- **File**: `internal/specworkflow/codex_runner.go:63-73`
- **Issue**: If `schemaFile.Write()` fails (line 69-72), the function returns but `os.Remove(schemaFile.Name())` is still deferred from line 67. This is fine (deferred cleanup still runs), but the early return after `schemaFile.Close()` doesn't check the Close error.
- **Fix**: Check `schemaFile.Close()` error before returning on the write error path.

#### [MIN-002] HoldoutOutput struct defined in holdout_output_schema.go alongside the schema

- **Lens**: Overcomplexity
- **File**: `internal/specworkflow/holdout_output_schema.go:5-18`
- **Issue**: The `HoldoutOutput` struct is defined in `holdout_output_schema.go` rather than in a types file. This couples the struct definition to the schema utility. The `ReviewerOutput` struct is presumably defined elsewhere (not in `reviewer_output_schema.go`). Inconsistent placement.
- **Fix**: Move `HoldoutOutput` struct to the appropriate types file where `ReviewerOutput` is defined.

---

### Observations

#### [OBS-001] CodexRunner uses log.Printf instead of structured logging

- **Lens**: Observability
- **File**: `internal/specworkflow/codex_runner.go:83-96`
- **Suggestion**: The codex runner uses `log.Printf` with `[codex-runner]` prefixes while the rest of the codebase appears to use similar patterns. This is consistent but not structured (no correlation IDs, no JSON logging). Consider adopting zerolog or the project's structured logging pattern if one exists.

#### [OBS-002] Test for schema file lifecycle is weak

- **Lens**: Testing Quality
- **File**: `internal/specworkflow/codex_runner_test.go:128-157`
- **Suggestion**: `TestCodexRunner_SchemaFileLifecycle` attempts to verify cleanup but acknowledges it can't reliably do so in concurrent test environments and assigns the glob result to `_`. The test effectively only verifies that `SchemaBytes` is preserved on the runner, which is trivial. Consider testing schema file creation/content instead of cleanup (e.g., intercept the command to verify the schema file exists and contains expected content before Run completes).

---

## Test Results

```
ok  	github.com/foundry-zero/adversarial-spec-system/internal/specworkflow	2.538s
```

| Status | Count |
|--------|-------|
| PASS | 48 |
| FAIL | 0 |
| SKIP | 0 |

### Failing Tests

None.

---

## Verdict Rationale

The implementation is solid for the reviewer dispatch path: config fields, team configuration, codex runner, review dispatch, and merge all work correctly with good test coverage. However, two critical issues prevent a PASS verdict:

1. **CRIT-001**: The codex runner is created with `nil` schema bytes in the orchestrator, which means the `--output-schema` flag will point to an empty file in production. This effectively breaks all codex reviewers at runtime. This was not caught because orchestrator tests disable codex entirely (MAJ-002).

2. **CRIT-002**: The holdout dual-dispatch (US-8, US-9, US-10) is completely unimplemented at the orchestrator level. The supporting functions (`MergeHoldoutMarkdown`, `HoldoutOutputSchema`, `HoldoutDispatchResult`) exist but are orphaned -- never called from any handler or entry point.

Additionally, MAJ-001 reveals an architectural concern: the single `codexRunner` field with fixed `SchemaBytes` cannot serve both reviewer and holdout schemas, requiring design work before holdout dispatch can be wired.

### Recommended Next Actions

- [ ] Fix CRIT-001: Pass `ReviewerOutputSchema()` to `DefaultCodexRunner` at `orchestrator.go:308`
- [ ] Fix CRIT-002: Implement holdout dual-dispatch handler and wire into orchestrator
- [ ] Fix MAJ-001: Create separate codex runner instances for reviewer vs holdout (different schemas), or refactor `Run()` to accept schema per-call
- [ ] Fix MAJ-002: Add orchestrator integration tests with `LookPathFunc` override and mock codex runner verifying 8-reviewer dispatch
- [ ] Fix MAJ-003: Align team config `TimeoutSeconds` with actual dispatch behavior or remove unused field

After fixing, re-run: `/grill-code docs/plan/dual-provider-review-spec.md`
