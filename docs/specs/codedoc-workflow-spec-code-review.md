# Code Review: Codebase Documentation Workflow

**Spec reviewed**: `docs/specs/codedoc-workflow-spec.md`
**Tasks reviewed**: `.tasks/codedoc-workflow.task.json`
**Review date**: 2026-04-03
**Verdict**: REVISE

---

## Executive Summary

The codedoc workflow implementation is substantial and largely correct. All 17 task areas have code present, the full build compiles clean, and 267 tests pass with zero failures across `internal/codedoc/`. The orchestrator is fully wired into `cmd/specworkflow/main.go` and the API layer.

Two MAJOR findings must be addressed before this is merge-ready: (1) the revision phase does not create a new draft version directory as required by the spec — revised docs overwrite the current draft in-place, breaking the `draft-v{N+1}/` isolation guarantee; (2) `ValidateReviewerOutput` normalises severity for the gate check but stores the original mixed-case string — LLM agents returning `"CRITICAL"` instead of `"critical"` pass validation but are silently excluded from convergence counts, which could cause the judge to render PASS when open CRITICAL findings exist.

| Metric | Value |
|--------|-------|
| Files reviewed | 22 `internal/codedoc/` source files + 3 API/main files |
| Tasks with code present | 17 / 17 |
| Tasks genuinely complete (acceptance criteria verified) | 15 / 17 |
| Wiring gaps | 0 unwired packages; 1 partial (revision draft versioning) |
| Tests passing | 267 / 267 (codedoc) + all codereview + all api |

| Severity | Count |
|----------|-------|
| CRITICAL | 0 |
| MAJOR | 2 |
| MINOR | 4 |
| OBSERVATION | 3 |
| **Total** | **9** |

---

## Task Audit

All 17 tasks have `no-status` in the task JSON. The review verifies each against its acceptance criteria.

| Task ID | Title | Task Status | Verified Status | Details |
|---------|-------|-------------|----------------|---------|
| cd-config-and-types | Config and domain types | no-status | GENUINELY COMPLETE | All defaults correct; validation messages match spec wording |
| cd-output-schemas | JSON output schemas | no-status | GENUINELY COMPLETE | ValidateDiscoveryOutput, ValidateDrafterOutput, ValidateReviewerOutput present and tested |
| cd-state-machine | State machine | no-status | GENUINELY COMPLETE | All 14 states, full transition table, 5 guards + 2 judging guards |
| cd-discovery-orchestration | Discovery phase | no-status | GENUINELY COMPLETE | Dual-provider, merge, fallback, versioned filenames all present |
| cd-drafting-orchestration | Drafting phase | no-status | INCOMPLETE | Mermaid validation present; combine agent present; but draft artefact files contain placeholder content (MIN-001) |
| cd-secret-sanitisation | Secret sanitisation | no-status | GENUINELY COMPLETE | 10 patterns including PEM, JWT, connection strings; needs_redraft logic correct |
| cd-review-dispatch | Review dispatch | no-status | INCOMPLETE | 4 groups parallel, failure tolerance, dedup key per spec; but severity case-normalisation gap means mixed-case findings are silently dropped (MAJ-002) |
| cd-convergence-and-judge | Convergence + judge | no-status | INCOMPLETE | PASS/REVISE/PASS_WITH_GATE, staleness detection; but mixed-case severity strings bypass countFindings (MAJ-002) |
| cd-revision-agent | Revision agent | no-status | INCOMPLETE | Findings status applied in-memory correctly; but draft files modified in-place instead of written to draft-v{N+1}/ (MAJ-001) |
| cd-writing-phase | Writing phase | no-status | GENUINELY COMPLETE | Staging→backup→rename, lock, stale lock break, drift detection, manual markers, atomic manifest |
| cd-human-gates | Human gate handlers | no-status | GENUINELY COMPLETE | ScopeGate, DraftGate, FinalGate with limits; wired into orchestrator |
| cd-orchestrator | Main orchestrator | no-status | GENUINELY COMPLETE | Full lifecycle, CD_ERROR resume with artefact detection, event emission |
| cd-api-endpoints | API handlers | no-status | GENUINELY COMPLETE | All 7 endpoints (start, status, gate, cancel, resume, reset, rewind); registered in main.go |
| cd-incremental-mode | Incremental mode | no-status | GENUINELY COMPLETE | LoadManifest, ComputeIncrementalChanges, ShouldRegenerateArchitecture all present |
| cd-dashboard-integration | Dashboard | no-status | GENUINELY COMPLETE | CD_PIPELINE_STAGES (12 stages), CD badge, gate actions all present in static/app.js |
| cd-prompts | LLM prompts | no-status | GENUINELY COMPLETE | All 8 prompt builders present; severity rubric verbatim in reviewer prompts; all 7 merge rules and 4 combine rules encoded |
| cd-config-integration | Server config integration | no-status | GENUINELY COMPLETE | codedoc YAML key parsed in main.go; CodedocManager registered; routes mounted |

### Incomplete Task Details

#### Task cd-revision-agent: Draft files not versioned after revision

**Acceptance criteria from task:**
- "Revised documentation files are written to a new draft version directory" — NOT MET

`handleRevising` at `internal/codedoc/orchestrator.go:387-415` passes the current `draft-v{DraftVersion}` directory to the revision agent and never increments `ws.DraftVersion`. The next review round reads the same `draft-v{N}/` directory, breaking the per-round artefact isolation the spec requires.

#### Task cd-review-dispatch / cd-convergence-and-judge: Severity case normalisation gap

**Acceptance criteria from task (cd-convergence-and-judge):**
- "DetectStaleness returns true after staleness_threshold consecutive rounds without CRIT+MAJ decrease" — CORRECT
- "Zero open findings returns PASS verdict" — CORRECT (when all findings are lowercase)
- Silent failure: if any LLM returns `"CRITICAL"` (uppercase), countFindings at `convergence.go:83` falls through all switch cases and the finding is not counted — NOT MET

---

## Wiring & Integration Audit

### Stubs Found

None. All phase handlers contain real logic.

### Implemented but Unwired

No packages are excluded from the binary. `internal/codedoc` is imported in `cmd/specworkflow/main.go` and `internal/api/codedoc_handlers.go`. All routes are mounted.

### Partial Wiring

| Component | What's Connected | What's Missing |
|-----------|-----------------|----------------|
| Revision draft versioning | `handleRevising` runs the agent against `draft-v{DraftVersion}` | `DraftVersion` never incremented; `draft-v{N+1}/` never created; writing phase reads the same `draft-v{N}/` regardless of revision round |

---

## Code Findings

### MAJOR Findings

#### MAJ-001: Revision phase does not create a new draft version directory

- **Lens**: Correctness
- **File**: `internal/codedoc/orchestrator.go:387-415`
- **Code**:
  ```go
  draftVersion := ws.DraftVersion
  if draftVersion == 0 {
      draftVersion = 1
  }
  draftDir := filepath.Join(o.featureDir, fmt.Sprintf("draft-v%d", draftVersion))
  prompt := BuildRevisionPrompt(string(findingsJSON), draftDir, round)
  // ... ws.DraftVersion is never incremented ...
  return o.sm.Transition(CDJudging)
  ```
- **Issue**: The spec (task cd-revision-agent) requires revised documentation to be written to `draft-v{N+1}/`. `handleRevising` points the agent at the current `draft-v{DraftVersion}` and never increments `DraftVersion`. After revision, the next `handleReviewing` call reads the same version directory. If the agent modifies docs in `draft-v1/` in-place, round 2 reviewers see the modified content but there is no pre-revision snapshot. If the crash-recovery logic later resumes at `CDReviewing`, there is no way to know what docs were in what state.
- **Impact**: No per-round artefact trail; crash recovery cannot distinguish round 1 vs round 2 draft state; the writing phase uses the same `DraftVersion` so it will write `draft-v1/` content regardless of how many revision rounds occurred.
- **Fix**: At the start of `handleRevising`, increment `ws.DraftVersion` and use the new value as both the source (copy from current) and target. The revision agent should be given the new directory path as its write target.

---

#### MAJ-002: Severity strings are not normalised — mixed-case LLM output causes silent finding loss

- **Lens**: Correctness
- **File**: `internal/codedoc/schemas.go:221-226` (gate), `internal/codedoc/convergence.go:83-93` (consumer)
- **Code**:
  ```go
  // schemas.go — validates with lowercase normalisation but DOES NOT store normalised value:
  if !validSeverities[strings.ToLower(strings.TrimSpace(f.Severity))] {
      rejected++
      continue
  }
  // finding f.Severity is stored as-is (e.g. "CRITICAL")
  valid = append(valid, f)
  ```
  ```go
  // convergence.go — consumes without normalisation:
  switch f.Severity {
  case SeverityCritical:   // "critical" — will NOT match "CRITICAL"
      s.OpenCritical++
  case SeverityMajor:      // "major"
      s.OpenMajor++
  // ...
  }
  ```
- **Issue**: `ValidateReviewerOutput` accepts findings with severity `"CRITICAL"` (or `"Major"`, etc.) because it normalises for the gate check, but stores the original casing in the `ReviewFinding`. `countFindings` in `convergence.go` uses a switch-case against lowercase constants. An uppercase severity falls through all cases — the finding is counted as neither open critical, major, minor, nor observation. `CountOpenCriticalMajor` at `convergence.go:106` has the same flaw. Real-world LLMs commonly return uppercase severity labels.
- **Impact**: A run with any LLM returning uppercase severities would produce convergence results suggesting zero critical/major findings when there are unresolved ones. The judge would render PASS and documentation would be written to the repository unchecked.
- **Fix**: After the severity gate check passes in `ValidateReviewerOutput`, normalise: `f.Severity = strings.ToLower(strings.TrimSpace(f.Severity))`. Add a test that passes `"CRITICAL"` through the full `ValidateReviewerOutput → MergeCodedocReviewerOutputs → EvaluateConvergence` pipeline and asserts it is counted.

---

### MINOR Findings

#### MIN-001: `writeDraftFiles` writes placeholder content instead of actual drafter output

- **Lens**: Correctness
- **File**: `internal/codedoc/orchestrator_drafting.go:376-381`
- **Code**:
  ```go
  reportPath := filepath.Join(draftDir, "as-implemented-report.md")
  if err := os.WriteFile(reportPath, []byte("# As-Implemented Report\n\n(Generated by codedoc workflow)\n"), 0644); err != nil {
  ```
- **Issue**: The as-implemented report and code audit report are written as static stub text. Reviewers in `CD_REVIEWING` will read placeholder content. The Mermaid diagrams and audit JSON are written correctly from `DrafterOutput` data, but the main markdown report is a stub.
- **Fix**: The drafter agent is responsible for writing actual content. `writeDraftFiles` should either (a) write the `as_implemented_report.file_path` content from the `DrafterOutput` struct if it contains the content, or (b) copy the file written by the agent at `output.AsImplementedReport.FilePath` to the expected draft location.

---

#### MIN-002: `isProcessAlive` is Unix-only, no build constraint

- **Lens**: Correctness
- **File**: `internal/codedoc/writer.go:243-251`
- **Code**:
  ```go
  err = process.Signal(syscall.Signal(0))
  return err == nil
  ```
- **Issue**: `syscall.Signal(0)` works only on Unix/Linux/macOS. On Windows, `Signal` returns an error for any signal, so `isProcessAlive` always returns `false`, disabling stale-lock detection. The server is likely Linux-only, but this is not documented.
- **Fix**: Add `// This function is Unix-only; on Windows stale lock detection is disabled` comment, or add a `//go:build !windows` constraint with a Windows stub returning `false`.

---

#### MIN-003: Write lock backoff has no jitter

- **Lens**: Correctness
- **File**: `internal/codedoc/writer.go:188-192`
- **Issue**: Pure exponential backoff without jitter causes lock contention from concurrent workflows retrying in lockstep. Minor quality issue.
- **Fix**: Add ±25% random jitter to `backoff` before sleeping.

---

#### MIN-004: `ScanContent` normalises CRLF to LF without documentation

- **Lens**: Correctness
- **File**: `internal/codedoc/sanitiser.go:149`
- **Code**:
  ```go
  result.NewContent = strings.Join(lines, "\n")
  ```
- **Issue**: `bufio.Scanner` strips line endings; rejoining with `"\n"` silently converts CRLF files, changing content hashes. Not a functional problem in a Unix deployment but worth documenting.
- **Fix**: Document that the sanitiser normalises line endings to LF, or track original endings and preserve them.

---

### Observations

#### OBS-001: Skip-revision shortcut duplicates convergence logic

- **File**: `internal/codedoc/orchestrator.go:371-375`
- **Suggestion**: `if len(merged.Findings) == 0 { return sm.Transition(CDJudging) }` is correct but duplicates what `EvaluateConvergence` would catch. If convergence logic changes, this shortcut may diverge. Consider removing it and always routing through `CDRevising → CDJudging`.

---

#### OBS-002: `ValidateReviewerOutput` accepts findings with empty `affected_section`

- **File**: `internal/codedoc/schemas.go:236-239`
- **Suggestion**: `requireNonEmpty` for `affected_section` appends to `errs` but the finding is already in the `valid` slice. Empty `affected_section` means all such findings dedup together under the same key `lens||`. Consider adding `affected_section` emptiness to the early-rejection path alongside `severity` and `recommendation`.

---

#### OBS-003: No structured workflow-log.jsonl despite task constraint

- **File**: `internal/codedoc/orchestrator.go` (all phase handlers)
- **Suggestion**: The `cd-orchestrator` task specifies "Structured logging to workflow-log.jsonl" as a constraint. All current logging uses `log.Printf` to stdout. No `workflow-log.jsonl` is written to `featureDir`. The specworkflow uses a `CRAuditLogger` pattern for this. Not blocking (not in acceptance criteria), but noted for operational completeness.

---

## Codereview Refactor Verification

The changes to `internal/codereview/` consolidate type definitions from `events.go`, `fix_output.go`, `orchestrator.go`, `orchestrator_fix.go`, `orchestrator_gates.go`, and `statemachine.go` into `types.go`. This is a pure reorganisation: no logic changed. All codereview tests pass (`ok internal/codereview 9.391s`). The refactor is clean and correct.

---

## Test Results

```
ok  github.com/foundry-zero/adversarial-spec-system/internal/codedoc     2.796s
ok  github.com/foundry-zero/adversarial-spec-system/internal/api         1.670s
ok  github.com/foundry-zero/adversarial-spec-system/internal/codereview  9.391s
```

| Status | Count |
|--------|-------|
| PASS | 267 (codedoc) + full api + full codereview pass |
| FAIL | 0 |
| SKIP | 0 |

**Notable test gap**: No tests pass mixed-case severity strings (`"CRITICAL"`, `"Major"`) through the validation+convergence pipeline. MAJ-002 is not caught by the existing test suite.

---

## Verdict Rationale

The implementation is architecturally sound: all 17 task areas are present, wired, and the build is clean. The orchestrator, state machine, gate handlers, writing phase, and API layer all work correctly. The codereview type consolidation is a clean refactor with no regressions.

Two issues prevent PASS. MAJ-001 (revision draft versioning) is a spec compliance failure: the revision agent must write to `draft-v{N+1}/` but the current code never increments `DraftVersion`, breaking per-round artefact isolation. MAJ-002 (severity case normalisation) is a silent correctness bug: the normalisation happens for validation but the original casing is stored, causing uppercase severity strings from LLMs to bypass all convergence counting — the judge could render PASS with unresolved CRITICAL findings present.

### Recommended Next Actions

- [ ] Fix MAJ-001: Increment `ws.DraftVersion` at the start of `handleRevising`, copy current draft to new version dir, pass new path to agent — `internal/codedoc/orchestrator.go:379`
- [ ] Fix MAJ-002: Normalise `f.Severity = strings.ToLower(strings.TrimSpace(f.Severity))` after severity gate passes in `ValidateReviewerOutput` — `internal/codedoc/schemas.go:226`
- [ ] Add test for MAJ-002: uppercase severities through full validation+convergence pipeline
- [ ] Fix MIN-001: Replace placeholder report content with actual DrafterOutput content — `internal/codedoc/orchestrator_drafting.go:376`
- [ ] Address OBS-002: Add `affected_section` emptiness to early-reject path in `ValidateReviewerOutput` — `internal/codedoc/schemas.go:236`

After fixing, re-run: `/grill-code docs/specs/codedoc-workflow-spec.md`
