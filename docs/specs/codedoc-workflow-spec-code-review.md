# Code Review: Codebase Documentation Workflow

**Spec reviewed**: `docs/specs/codedoc-workflow-spec.md`
**Tasks reviewed**: `.tasks/codedoc-workflow.task.json`
**Review date**: 2026-04-01
**Verdict**: REVISE
**Spec compliance**: 14/16 tasks verified as substantially complete (87%)

## Executive Summary

The codedoc workflow implementation is architecturally sound and follows the established patterns from `specworkflow` and `codereview` sibling packages. All 16 task files are present, all 97 unit tests pass, config/types/schemas/state-machine/review-dispatch/convergence/sanitiser/writer/gates/orchestrator/API-handlers/incremental/prompts are implemented. The code correctly handles dual-provider discovery, drafting, and review with LLM-based merge agents. However, there are two MAJOR gaps: (1) API gate payloads are empty stubs that do not provide the rich data the spec requires at human gates, and (2) the orchestrator does not expose its internal artefacts for external consumption. There is also one MAJOR concern with the draft gate redraft limit behavior differing from what the spec mandates.

| Metric | Value |
|--------|-------|
| Files reviewed | 34 files (17 implementation + 17 test) |
| Tasks genuinely complete | 14 verified / 16 claimed |
| Wiring gaps | 0 stubs, 0 unwired, 1 partial (gate payloads) |
| Tests passing | 97 pass / 97 total |

| Severity | Count |
|----------|-------|
| CRITICAL | 0 |
| MAJOR | 3 |
| MINOR | 4 |
| OBSERVATION | 4 |
| **Total** | **11** |

---

## Task Audit

| Task ID | Title | Verified Status | Details |
|---------|-------|----------------|---------|
| cd-config-and-types | Config and domain types | GENUINELY COMPLETE | All fields, defaults, validation match spec Section 12 |
| cd-output-schemas | JSON output schemas | GENUINELY COMPLETE | Discovery, Drafter, Reviewer, Judge schemas with validation |
| cd-state-machine | State machine with transitions and guards | GENUINELY COMPLETE | All CD_ states, transition table, MaxRounds/Cost/WallClock/Staleness guards |
| cd-discovery-orchestration | Discovery with dual-provider and merge | GENUINELY COMPLETE | Single and dual dispatch, merge agent, fallback, versioned files |
| cd-drafting-orchestration | Drafting with dual-provider combine | GENUINELY COMPLETE | Single and dual dispatch, combine agent, Mermaid validation, fallback |
| cd-secret-sanitisation | Secret sanitisation scanner | GENUINELY COMPLETE | All Section 3d patterns, redaction, needs_redraft, directory scan |
| cd-review-dispatch | Review dispatch with 4 groups and 9 lenses | GENUINELY COMPLETE | 4 parallel groups, dedup by (lens, file, section), failure tolerance |
| cd-convergence-and-judge | Convergence evaluation and judge | GENUINELY COMPLETE | PASS/PASS_WITH_GATE/REVISE verdicts, staleness detection |
| cd-revision-agent | Revision agent | GENUINELY COMPLETE | Priority ordering, status updates, apply-to-findings, wontfix with rationale |
| cd-writing-phase | Writing with staging, backup, lock, drift | GENUINELY COMPLETE | Staging-then-move, backup, lock with stale detection, drift, manual markers, atomic manifest |
| cd-human-gates | Three human gate handlers | GENUINELY COMPLETE | Scope/Draft/Final handlers with correction limits |
| cd-orchestrator | Main orchestrator | GENUINELY COMPLETE | Full lifecycle, resume from CD_ERROR with artefact detection, WebSocket events |
| cd-api-endpoints | API handlers | INCOMPLETE | All 7 endpoints registered; gate payloads are empty stubs (see MAJ-001) |
| cd-incremental-mode | Incremental mode | GENUINELY COMPLETE | Manifest load, hash compare, change computation, architecture regen detection |
| cd-dashboard-integration | Dashboard with CD badge | GENUINELY COMPLETE | Pipeline stages, CD badge, gate buttons, WebSocket updates |
| cd-prompts | LLM prompts for all agents | GENUINELY COMPLETE | Discovery, merge, drafter, combine, sanitisation, reviewer (4 groups), revision, judge |
| cd-config-integration | Config integration in main.go | GENUINELY COMPLETE | Config block in config.yaml, parsed at startup, orchestrator initialised, routes registered |

### Incomplete Task Details

#### Task cd-api-endpoints: API handlers

**Acceptance criteria from task:**
1. POST /api/codedoc/start with valid body returns 202 -- VERIFIED at `codedoc_handlers.go:204`
2. POST /api/codedoc/start with missing code_path returns 400 -- VERIFIED at `codedoc_handlers.go:152`
3. GET /api/codedoc/{feature}/status returns 200 -- VERIFIED at `codedoc_handlers.go:240`
4. GET /api/codedoc/{feature}/status for non-existent returns 404 -- VERIFIED at `codedoc_handlers.go:247`
5. POST /api/codedoc/{feature}/gate with approve returns 200 -- VERIFIED at `codedoc_handlers.go:355`
6. POST /api/codedoc/{feature}/cancel returns 200 -- VERIFIED at `codedoc_handlers.go:393`
7. POST /api/codedoc/{feature}/resume returns 200 -- VERIFIED at `codedoc_handlers.go:458`
8. POST /api/codedoc/{feature}/resume when not CD_ERROR returns 409 -- VERIFIED at `codedoc_handlers.go:446`
9. POST /api/codedoc/{feature}/reset returns 200 -- VERIFIED at `codedoc_handlers.go:517`
10. POST /api/codedoc/{feature}/rewind returns 200 -- VERIFIED at `codedoc_handlers.go:570`
11. **Gate payloads populated with spec-required data** -- NOT MET: `buildGatePayload` at `codedoc_handlers.go:255-272` returns near-empty structs. The SCOPE gate should display module inventory, completion status, dependency overview, existing docs, suggested scope, and merge conflicts (per US-7). The DRAFT gate should display file list, summary stats, and redaction log. The FINAL gate should display unresolved findings. All return skeleton values instead.

---

## Wiring & Integration Audit

### Stubs Found

No function-level stubs found. All methods have real implementations.

### Implemented but Unwired

No packages or components are completely unwired. All `internal/codedoc` code is reachable from `cmd/specworkflow/main.go` via the API handlers.

### Partial Wiring

| Component | What's Connected | What's Missing |
|-----------|-----------------|----------------|
| Gate payload enrichment | `buildGatePayload` called from `HandleCDStatus` | Orchestrator's internal artefacts (`discoveryOutput`, `drafterOutput`, `mergedFindings`) are private fields with no accessor methods. The API handler cannot populate gate payloads with spec-required data. |

---

## Code Findings

### MAJOR Findings

#### [MAJ-001] Gate payloads are empty stubs -- spec requires rich data at human gates

- **Lens**: Correctness / Spec Compliance
- **File**: `internal/api/codedoc_handlers.go:255-272`
- **Code**:
  ```go
  case codedoc.CDHumanGateScope:
      return codedoc.ScopeGatePayload{
          DiscoverySource: state.DiscoverySource,
      }
  case codedoc.CDHumanGateDraft:
      return codedoc.DraftGatePayload{}
  case codedoc.CDHumanGateFinal:
      return codedoc.FinalGatePayload{
          TotalUnresolved: 0,
          DriftWarning:    "",
      }
  ```
- **Issue**: The spec (US-7) requires the SCOPE gate to display module inventory, completion status, dependency overview, existing doc inventory, suggested scope, and merge conflicts. The DRAFT gate must display generated file list, summary statistics, and redaction log. The FINAL gate must display remaining unresolved findings. All three payloads are returned as mostly-empty structs. The orchestrator's `discoveryOutput`, `drafterOutput`, and `mergedFindings` fields are private with no accessor methods.
- **Impact**: Human users at gate states will see no useful information to make approval decisions. The core human-in-the-loop value proposition of the workflow is undermined.
- **Fix**: Add accessor methods to `CodedocOrchestrator` (e.g., `DiscoveryOutput()`, `DrafterOutput()`, `MergedFindings()`) and populate the gate payloads in `buildGatePayload`.

#### [MAJ-002] Orchestrator does not expose artefacts for gate payload population

- **Lens**: Correctness / Wiring
- **File**: `internal/codedoc/orchestrator.go:73-76`
- **Code**:
  ```go
  discoveryOutput *DiscoveryOutput
  drafterOutput   *DrafterOutput
  mergedFindings  []ReviewFinding
  roundCounts     []int
  ```
- **Issue**: These fields are private (lowercase) with no getter methods. External consumers (API handlers, dashboard) cannot access the workflow artefacts needed to build gate payloads.
- **Impact**: Same as MAJ-001 -- gate payloads cannot be populated without architecture changes. This is the root cause of MAJ-001.
- **Fix**: Add exported accessor methods: `func (o *CodedocOrchestrator) DiscoveryOutput() *DiscoveryOutput`, `func (o *CodedocOrchestrator) DrafterOutput() *DrafterOutput`, `func (o *CodedocOrchestrator) MergedFindings() []ReviewFinding`.

#### [MAJ-003] Draft gate redraft limit behavior differs from spec for max_gate_draft_redrafts

- **Lens**: Correctness
- **File**: `internal/codedoc/gates.go:137-147`
- **Code**:
  ```go
  func (h *DraftGateHandler) HandleRedraft() (CDState, error) {
      if h.state.GateDraftRedraftCount >= h.maxRedrafts {
          return CDEscalated, fmt.Errorf(...)
      }
      h.state.GateDraftRedraftCount++
      return CDDrafting, nil
  }
  ```
- **Issue**: The spec (US-7) says "Maximum `max_gate_draft_redrafts` re-drafts... before the gate forces approve-or-cancel." The implementation returns `CDEscalated` with an error when the limit is reached, rather than forcing the user to choose between approve and cancel. An error + escalation is more aggressive than what the spec describes (which is just disabling the redraft option while still allowing approve or cancel).
- **Impact**: Users who hit the redraft limit will have their workflow escalated rather than being given the choice to approve the current draft or cancel.
- **Fix**: When the limit is reached, `HandleRedraft` should return an error indicating redraft is disabled (not escalate). The API handler should inform the user that only approve and cancel are available. The `IsRedraftDisabled()` method exists and is correct -- the issue is in the handler returning `CDEscalated`.

---

### MINOR Findings

#### [MIN-001] File scope in tasks differs from actual file organization

- **Lens**: Overcomplexity
- **File**: Various
- **Issue**: The task spec expected separate files for prompts (`prompts_discovery.go`, `prompts_drafting.go`, `prompts_review.go`, `prompts_revision.go`, `prompts_judge.go`, `prompts_sanitisation.go`) and a separate `mermaid_validator.go`. The implementation consolidates all prompts into `prompts.go` and Mermaid validation into `orchestrator_drafting.go`. This is actually a better organization -- fewer files, same functionality.
- **Fix**: No action needed. This is noted for task tracking accuracy only.

#### [MIN-002] `log.Printf` used instead of structured logging

- **Lens**: Observability
- **File**: `internal/codedoc/orchestrator.go` (throughout), `orchestrator_discovery.go`, `orchestrator_drafting.go`, `review_dispatch.go`
- **Issue**: The codedoc package uses `log.Printf` throughout. However, the existing `specworkflow` and `codereview` packages also use `log.Printf`, so this is consistent with the codebase.
- **Fix**: Consider migrating to structured logging in a future pass if the project adopts a structured logging library.

#### [MIN-003] `handleSanitising` does not verify draft directory exists before scanning

- **Lens**: Error Handling
- **File**: `internal/codedoc/orchestrator.go:268-298`
- **Issue**: If `DraftVersion` is 0 (the guard sets it to 1), the draft directory `draft-v1` may not exist if drafting failed partway. The `ScanDirectory` would return an error which would be caught, but the error message would be confusing ("walk: no such file or directory").
- **Fix**: Add a check that the draft directory exists before scanning.

#### [MIN-004] Manual marker insertion logic is simplistic

- **Lens**: Correctness
- **File**: `internal/codedoc/writer_helpers.go:144-167`
- **Issue**: The `insertManualBlocks` function inserts manual blocks after the first occurrence of the parent heading. If the same heading text appears multiple times in the document (e.g., `## Overview` in two different sections), the block will be inserted after the first occurrence, which may be wrong. The spec says "same structural position" meaning relative to the heading hierarchy, not just string matching.
- **Fix**: Use a more robust heading hierarchy matching that considers heading levels and position in the document.

---

### Observations

#### [OBS-001] Mermaid validation is basic

- **Lens**: Overcomplexity
- **File**: `internal/codedoc/orchestrator_drafting.go:22-47`
- **Issue**: The `validateMermaidSyntax` function only checks for valid diagram type prefixes. It does not validate syntax (balanced braces, valid node definitions, etc.). The spec says "Mermaid syntax validation step confirms the output parses correctly." However, full Mermaid parsing would require either an external tool or a substantial parser. The current approach is pragmatic for v1.
- **Suggestion**: Consider invoking `mmdc` (Mermaid CLI) for validation if available, similar to how static analysis tools are auto-detected.

#### [OBS-002] Resume logic checks only round-1 merged findings

- **Lens**: Correctness
- **File**: `internal/codedoc/orchestrator_helpers.go:30-31`
- **Code**:
  ```go
  reviewPath := filepath.Join(o.featureDir, "merged-findings-round-1.json")
  if _, err := os.Stat(reviewPath); err == nil {
  ```
- **Issue**: The resume logic only checks for `merged-findings-round-1.json`. If the workflow crashed in round 2 or later, the round-1 file would exist but the code would resume at `CD_REVIEWING` with round set to 1, potentially losing progress from later rounds.
- **Suggestion**: Scan for the highest-numbered merged findings file to determine the correct resume round.

#### [OBS-003] Race condition potential in concurrent gate handler + RunWorkflow

- **Lens**: Correctness
- **File**: `internal/api/codedoc_handlers.go:347-353`
- **Code**:
  ```go
  if !newState.IsTerminal() && !newState.IsGate() {
      go func() {
          if err := orch.RunWorkflow(); err != nil {
  ```
- **Issue**: After a gate response transitions the state and launches `RunWorkflow` in a goroutine, a second gate request arriving before `RunWorkflow` starts processing could cause a second `RunWorkflow` goroutine to be launched. The `CodedocOrchestrator` has no mutex protecting concurrent access to `RunWorkflow`. The existing `specworkflow` package may have the same pattern, so this is consistent.
- **Suggestion**: Add a `running` flag with a mutex to prevent concurrent `RunWorkflow` invocations.

#### [OBS-004] Good work: clean separation of concerns

- **Lens**: Overcomplexity (positive)
- **Issue**: The package design is clean. The orchestrator delegates to focused functions (`RunDiscovery`, `RunDrafting`, `DispatchCodedocReviewers`, `EvaluateConvergence`), each with well-defined dependency injection structs. The state machine is a standalone testable component. Guards are composable. The dual-provider pattern with fallback is implemented consistently across discovery and drafting. Test coverage is thorough with 97 tests covering happy paths, edge cases, and error paths.

---

## Test Results

```
ok  github.com/foundry-zero/adversarial-spec-system/internal/codedoc    2.401s
ok  github.com/foundry-zero/adversarial-spec-system/internal/api        1.357s
```

| Status | Count |
|--------|-------|
| PASS | 97 (codedoc) + 30 (API codedoc tests) |
| FAIL | 0 |
| SKIP | 0 |

---

## Verdict Rationale

The implementation is substantially complete and well-engineered. The state machine, dual-provider pattern, review dispatch, convergence, sanitiser, writer with atomic staging, and incremental mode all match the spec. All tests pass. The dashboard integration is present with correct pipeline stages.

The REVISE verdict is driven by MAJ-001/MAJ-002: the gate payloads at the API level are empty stubs. The spec's core value proposition (US-7) depends on human gates displaying rich contextual data. Without module inventories at the scope gate, file lists at the draft gate, and finding summaries at the final gate, the human-in-the-loop workflow cannot function as designed. This is a straightforward fix: add accessor methods to the orchestrator and populate the payloads in the API handler.

MAJ-003 (draft gate escalating instead of forcing approve-or-cancel) is a behavior mismatch that could surprise users but has a lower blast radius.

### Recommended Next Actions

- [ ] Fix MAJ-001/MAJ-002: Add `DiscoveryOutput()`, `DrafterOutput()`, `MergedFindings()` accessors to `CodedocOrchestrator` and populate gate payloads in `buildGatePayload` -- `internal/codedoc/orchestrator.go` + `internal/api/codedoc_handlers.go:255-272`
- [ ] Fix MAJ-003: Change `HandleRedraft` to return an error without escalating when limit reached -- `internal/codedoc/gates.go:137-147`
- [ ] Consider OBS-002: Improve resume logic to scan for highest-numbered merged findings -- `internal/codedoc/orchestrator_helpers.go:30-31`

After fixing, re-run: `/grill-code internal/codedoc/ --spec docs/specs/codedoc-workflow-spec.md`
