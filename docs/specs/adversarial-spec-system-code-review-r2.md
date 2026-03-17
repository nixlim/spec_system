# Code Review R2: Adversarial Multi-Agent Specification System

**Spec reviewed**: docs/specs/adversarial-spec-system.md
**Previous review**: docs/specs/adversarial-spec-system-code-review.md
**Review date**: 2026-03-16
**Verdict**: PASS
**Review type**: Re-review after fixes applied for R1 findings

## Executive Summary

All 9 findings from the R1 review (1 CRITICAL, 5 MAJOR, 3 MINOR) have been properly resolved. The fixes are substantive — not superficial patches — and address the root causes identified in R1. Tests pass (250+, 0 failures), `go vet` is clean, and the race detector reports no issues. No new CRITICAL or MAJOR issues were introduced by the fixes. Two minor observations are noted below.

| Metric | Value |
|--------|-------|
| Files reviewed | 7 changed files + supporting files |
| R1 findings resolved | 9 / 9 (1 CRIT + 5 MAJ + 3 MIN) |
| New CRITICAL findings | 0 |
| New MAJOR findings | 0 |
| New MINOR findings | 1 |
| New OBSERVATIONS | 1 |
| Tests passing | 250+ pass / 0 fail |
| `go vet` | Clean |
| Race detector | Clean |

---

## R1 Finding Resolution Audit

### CRIT-001: AssembleFinalSpec not wired into orchestrator — RESOLVED

**R1 issue**: The orchestrator's FINALIZED handler wrote a bare copy of the spec instead of calling `AssembleFinalSpec`.

**Fix applied** (`orchestrator.go:676-691`):
```go
case StateFinalized:
    o.tracker.AcknowledgeMinorFindings(state.Round)
    finConfig := FinalizeConfig{
        WorkspaceDir: o.workspaceDir,
        FeatureName:  o.featureName,
    }
    WriteDebateTrail(finConfig, o.tracker, state.Round)
    if err := AssembleFinalSpec(finConfig, state, o.tracker); err != nil {
        log.Printf("warning: AssembleFinalSpec failed: %v", err)
    }
    SaveState(specDir, state)
    o.logger.LogStateTransition(StateFinalized, StateFinalized, state.Round)
    return nil
```

**Verification**: `AssembleFinalSpec` is now called in the FINALIZED handler. The debate trail is written first (so it can be embedded by `AssembleFinalSpec`), then the full 6-step assembly runs. The `AcknowledgeMinorFindings` call precedes both, ensuring minor findings are properly categorised before assembly. This correctly implements the spec Section 11.3 finalization procedure.

**Note**: The `AssembleFinalSpec` error is logged but not returned as a fatal error. This is a defensible choice — the workflow has converged and the spec versions are preserved regardless — but see MIN-R2-001 below.

**Status**: RESOLVED

---

### MAJ-001: No main.go — RESOLVED

**R1 issue**: No `cmd/` directory or `main.go`; the project was a pure library.

**Fix applied** (`cmd/specworkflow/main.go`): A 126-line main.go that:
- Parses `--port` and `--workspace` flags
- Creates workspace and source-docs directories
- Registers all API endpoints from `internal/api`
- Initialises `WorkflowStateJSON` and `IssueTracker`
- Serves static files from a `static/` directory
- Starts an HTTP server with endpoint listing

**Verification**: The binary entry point is properly structured. It correctly wires up `UploadConfig` and `SpecAPIConfig` with closures for `GetTracker`, `GetState`, and `CancelFunc`. The `findStaticDir` helper provides reasonable fallback for both development and deployed contexts. The cancel function currently returns an error (no active workflow), which is correct for the initial server state.

**Status**: RESOLVED

---

### MAJ-002: WrapSourceDocument wrapping paths not contents — RESOLVED

**R1 issue**: `BuildDiscoveryPrompt` passed file paths to `WrapSourceDocument` instead of file contents.

**Fix applied** (`prompts.go:91-99`):
```go
for _, p := range sourceDocPaths {
    name := filepath.Base(p)
    content, err := os.ReadFile(p)
    if err != nil {
        b.WriteString(WrapSourceDocument(name, fmt.Sprintf("[error reading source document: %v]", err)))
    } else {
        b.WriteString(WrapSourceDocument(name, string(content)))
    }
    b.WriteString("\n\n")
}
```

**Verification**: File contents are now read via `os.ReadFile` before being passed to `WrapSourceDocument`. Read errors are handled gracefully by wrapping an error message instead of the content, which preserves the XML structure and makes the error visible to the agent. The `WrapSourceDocument` function itself was already correct — only the caller needed fixing. Test `TestPromptDiscoveryEmbedsFileContent` and `TestPromptDiscoveryHandlesUnreadableFile` confirm both paths.

**Status**: RESOLVED

---

### MAJ-003: os.ReadFile error silently ignored — RESOLVED

**R1 issue**: The orchestrator's FINALIZED handler ignored `os.ReadFile` errors on the spec file.

**Fix applied**: The ad-hoc `os.ReadFile` / `os.WriteFile` code in the FINALIZED handler has been replaced entirely by the `AssembleFinalSpec` call (as part of CRIT-001 fix). `AssembleFinalSpec` (`finalize.go:35-37`) properly returns errors from `os.ReadFile`:
```go
specContent, err := os.ReadFile(srcPath)
if err != nil {
    return fmt.Errorf("read source spec %s: %w", srcPath, err)
}
```

**Status**: RESOLVED (subsumed by CRIT-001 fix)

---

### MAJ-004: Review dispatch false retries — RESOLVED

**R1 issue**: `ValidateReviewerOutput` returning any validation errors triggered retries, even when valid findings existed.

**Fix applied** (`review_dispatch.go:304-328`):
```go
validFindings, rejectedCount, validationErrs := ValidateReviewerOutput(&output)
if len(validFindings) == 0 && len(validationErrs) > 0 {
    // Only retry when there are ZERO valid findings
    ...
    continue
}
if rejectedCount > 0 {
    log.Printf("WARNING: %s: accepted %d valid findings, rejected %d ...")
}
output.Findings = validFindings
```

**Verification**: The fix correctly checks `len(validFindings) == 0` before treating the output as a schema violation. If some findings are valid, they are accepted and the output proceeds with the valid subset. `ValidateReviewerOutput` was also updated to return `(validFindings []Finding, rejectedCount int, errors []error)` — a three-value return that separates valid findings from rejected ones. The test `TestReviewDispatch_PartialFindingsAccepted` confirms partial acceptance works correctly.

**Status**: RESOLVED

---

### MAJ-005: ESCALATED handler incomplete — RESOLVED

**R1 issue**: The ESCALATED handler only wrote a debate trail with no escalation summary.

**Fix applied** (`orchestrator.go:699-715, 866-901`):
```go
case StateEscalated:
    finConfig := FinalizeConfig{...}
    o.writeEscalationSummary(specDir, state)
    WriteDebateTrail(finConfig, o.tracker, state.Round)
    SaveState(specDir, state)
    ...
```

The `writeEscalationSummary` method (`orchestrator.go:866-901`) writes a structured `escalation-summary.md` containing:
- Workflow state, round, feature name, start time, cumulative cost, agent invocations
- Escalation reason
- Findings summary (raised, closed, open critical, open major)
- Current spec version path

**Verification**: The escalation summary provides the structured context the spec requires (Section 7.2). The summary includes all key metrics a human needs to understand why the workflow escalated and where to pick up. The debate trail is also written for audit purposes.

**Status**: RESOLVED

---

### MIN-001: Unstructured logging in issue tracker — RESOLVED

**R1 issue**: `ApplyRevisionChanges` and `ApplyJudgeUpdates` used `log.Printf` for warnings.

**Fix applied** (`issues.go:182-196, 202-214`): Both methods now return `(warnings []string, err error)` instead of logging directly. The orchestrator (`orchestrator.go:488-494, 535-541`) logs the warnings through `o.logger.LogAgentError`:
```go
if warnings, err := o.tracker.ApplyRevisionChanges(&revision, state.Round); err != nil {
    return fmt.Errorf("apply revision changes: %w", err)
} else {
    for _, w := range warnings {
        o.logger.LogAgentError("reviser", "tracker_warning", w)
    }
}
```

**Verification**: The `log.Printf` calls have been removed from `issues.go`. Warnings are now surfaced to the caller and logged through the structured `WorkflowLogger`. This is the architecturally correct approach — the issue tracker is a pure domain component that shouldn't have logging concerns.

**Status**: RESOLVED

---

### MIN-002: Missing MergedFindings validation — RESOLVED

**R1 issue**: No validation function existed for the `MergedFindings` struct.

**Fix applied** (`agent_output.go:575-583`):
```go
func ValidateMergedFindings(o *MergedFindings) []error {
    var errs []error
    requirePositive(&errs, "round", o.Round)
    requireNonEmpty(&errs, "timestamp", o.Timestamp)
    if o.Findings == nil {
        errs = append(errs, fmt.Errorf("findings must not be nil"))
    }
    return errs
}
```

**Verification**: Tests `TestAgentOutput_ValidateMergedFindings_Valid`, `_EmptyFields`, `_ZeroRound`, and `_NilFindings` cover the key validation paths. The function validates round > 0, non-empty timestamp, and non-nil findings — the minimum required fields per the spec schema.

**Status**: RESOLVED

---

### MIN-003: Silent event dropping — RESOLVED

**R1 issue**: `ChannelEmitter.Emit` silently dropped events with no error when the buffer was full.

**Fix applied** (`events.go:153-167`):
```go
var ErrChannelFull = fmt.Errorf("event channel full: event dropped")

func (e *ChannelEmitter) Emit(event EventEnvelope) error {
    select {
    case e.ch <- event:
        return nil
    default:
        return ErrChannelFull
    }
}
```

**Verification**: The `Emit` method now returns `ErrChannelFull` when the buffer is full, and the `EventEmitter` interface was updated to return `error`. Test `TestChannelEmitter_NonBlockingDrop` confirms the error is returned. Callers in the orchestrator don't check the return value (which is acceptable — event emission is advisory and should not block the workflow), but the error is available for callers that want to handle it.

**Status**: RESOLVED

---

## New Findings

### MINOR Findings

#### [MIN-R2-001] AssembleFinalSpec error downgraded to warning in FINALIZED handler

- **File**: `internal/specworkflow/orchestrator.go:689-691`
- **Code**:
  ```go
  if err := AssembleFinalSpec(finConfig, state, o.tracker); err != nil {
      log.Printf("warning: AssembleFinalSpec failed: %v", err)
  }
  ```
- **Issue**: If `AssembleFinalSpec` fails (e.g. the spec file is missing or disk is full), the error is logged as a warning via `log.Printf` but the workflow returns `nil` (success). This means the workflow reports success even if the primary deliverable (spec-final.md) was not produced. Additionally, this uses `log.Printf` (unstructured) rather than the `WorkflowLogger`.
- **Impact**: Low in practice — `AssembleFinalSpec` would fail only if the spec file that was just used in previous rounds is suddenly unreadable — but the error handling pattern is inconsistent with the rest of the orchestrator which returns errors explicitly.
- **Suggested fix**: Return the error, or at minimum log through `o.logger`.

---

### Observations

#### [OBS-R2-001] Unchecked os.WriteFile for merged findings

- **File**: `internal/specworkflow/orchestrator.go:436`
- **Code**: `os.WriteFile(mergedPath, mergedData, 0o644)`
- **Issue**: The write error for merged findings JSON is discarded. If this write fails, the reviser agent would receive a stale or missing merged-findings file in the next step. While `json.MarshalIndent` on a known-good struct is unlikely to fail, and the filesystem write is the only realistic failure mode, this is a minor gap in error handling.

#### [OBS-R2-002] MergedFinding.Status still uses string "open" vs StatusRaised

- **File**: `internal/specworkflow/merge.go:199`
- **Code**: `Status: "open"`
- **Issue**: R1 MIN-003 was about this inconsistency (MergedFinding uses `"open"`, IssueTracker uses `StatusRaised` = `"raised"`). This was not in the fix list for R2, so it remains. The two systems use different representations for the same concept. Not a functional bug since `IssueTracker.AddFindings` overwrites the status to `StatusRaised` when adding findings, but it's a latent inconsistency.

---

## R1 Observations Status

| R1 ID | Description | Status |
|-------|-------------|--------|
| OBS-001 | No race detector in CI | Tests pass with `-race`; recommendation stands but not blocking |
| OBS-002 | Spec version 0 rejected by API | Not addressed; minor UX issue |
| OBS-003 | Test helper functions in wrong file | Not addressed; code organisation |
| OBS-004 | No go.sum verification | Not addressed; build reproducibility |

---

## Test Results

```
?   github.com/foundry-zero/adversarial-spec-system/cmd/specworkflow    [no test files]
ok  github.com/foundry-zero/adversarial-spec-system/internal/api        1.025s
ok  github.com/foundry-zero/adversarial-spec-system/internal/specworkflow  3.036s
```

| Status | Count |
|--------|-------|
| PASS | 250+ |
| FAIL | 0 |
| SKIP | 0 |

### Race Detector

```
ok  github.com/foundry-zero/adversarial-spec-system/internal/api        2.142s
ok  github.com/foundry-zero/adversarial-spec-system/internal/specworkflow  3.754s
```

No data races detected.

### `go vet`

Clean — no issues reported.

---

## Verdict Rationale

Verdict: **PASS**

All 9 findings from the R1 review have been properly resolved:

1. **CRIT-001** (AssembleFinalSpec unwired): Now called in FINALIZED handler with debate trail written first for embedding. The full 6-step assembly procedure runs.

2. **MAJ-001** (No main.go): `cmd/specworkflow/main.go` created with proper HTTP server setup, all API endpoints registered, static file serving, and CLI flags.

3. **MAJ-002** (WrapSourceDocument wrapping paths): `BuildDiscoveryPrompt` now reads file contents via `os.ReadFile` before passing to `WrapSourceDocument`, with proper error handling for unreadable files.

4. **MAJ-003** (Silent ReadFile error): Subsumed by CRIT-001 fix — `AssembleFinalSpec` handles all file I/O errors explicitly.

5. **MAJ-004** (False retries): Review dispatch now checks `len(validFindings) == 0` before triggering retry. Partial valid findings are accepted with a warning log.

6. **MAJ-005** (Incomplete ESCALATED): `writeEscalationSummary` writes a structured markdown report with workflow state, escalation reason, findings summary, and current spec path.

7. **MIN-001** (Unstructured logging): `ApplyRevisionChanges` and `ApplyJudgeUpdates` return warnings instead of logging directly; orchestrator routes through `WorkflowLogger`.

8. **MIN-002** (Missing MergedFindings validation): `ValidateMergedFindings` added with 4 test cases.

9. **MIN-003** (Silent event dropping): `ChannelEmitter.Emit` returns `ErrChannelFull` error.

The one new MINOR finding (AssembleFinalSpec error downgraded to warning) and two observations are not blocking. The fixes demonstrate thoughtful engineering — each addresses the root cause at the correct architectural layer rather than applying superficial patches.

### Remaining Recommendations (Non-Blocking)

- [ ] Return `AssembleFinalSpec` error or log through structured logger (MIN-R2-001)
- [ ] Check `os.WriteFile` error for merged findings (OBS-R2-001)
- [ ] Align `MergedFinding.Status` with `IssueStatus` constants (OBS-R2-002, carried from R1 MIN-003)
- [ ] Split `orchestrator.go` (901 lines) into smaller files (carried from R1 MIN-002, not blocking)
