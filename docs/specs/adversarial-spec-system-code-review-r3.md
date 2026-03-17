# Code Review: Adversarial Spec System (R3)

**Spec reviewed**: docs/specs/adversarial-spec-system.md
**Review date**: 2026-03-16
**Verdict**: REVISE
**Review round**: R3 (previous rounds: R1, R2)

## Executive Summary

The codebase is substantially complete and well-structured. The R1/R2 wiring gap (orchestrator never called from main.go) has been correctly fixed. All 420 tests pass, go vet is clean, and the build succeeds. However, this R3 review identifies **3 MAJOR** issues (error-swallowing in the orchestrator, dead/unwired code paths, and a file-size violation), **6 MINOR** issues, and **5 OBSERVATIONs**. The MAJOR findings do not block production use but should be addressed before the implementation is considered complete.

| Metric | Value |
|--------|-------|
| Files reviewed | 30 source files + 29 test files |
| Wiring gaps | 0 stubs, 0 unwired, 3 partial |
| Tests passing | 420 pass / 0 fail / 0 skip |

| Severity | Count |
|----------|-------|
| CRITICAL | 0 |
| MAJOR | 3 |
| MINOR | 6 |
| OBSERVATION | 5 |
| **Total** | **14** |

---

## Wiring & Integration Audit

### Stubs Found

No stubs or placeholder functions found. All functions contain real logic.

### Implemented but Unwired

| Package / Component | Has Tests | Called from Binary | Status |
|---------------------|-----------|-------------------|--------|
| `ResumeWorkflow()` | YES (15 tests) | NO — never called from main.go or orchestrator | UNWIRED |
| `DefaultTeamConfig()`, `ValidateTeamConfig()`, `GetReviewerConfigs()` | YES (6 tests) | NO — team.go is entirely unused by the orchestrator | UNWIRED |
| `CheckAllBreakers()`, `CheckStaleness()`, `BreakerConfig` struct | YES (10 tests) | NO — orchestrator uses individual breaker checks directly, never the aggregate `CheckAllBreakers` or `CheckStaleness` | PARTIAL |
| `BestEffortParse()` | YES (4 tests) | NO — `DetermineRecovery` returns `ActionBestEffortParse` but orchestrator never acts on it | UNWIRED |
| `ClientCount()` on `WebSocketHub` | YES (1 test) | NO — never called outside tests | UNWIRED |
| `ParseWorkflowState()`, `ParseSeverity()`, `ParseVerdict()` | YES (tests) | NO — only called from tests | UNWIRED |

### Partial Wiring

| Component | What's Connected | What's Missing |
|-----------|-----------------|----------------|
| `ResumeWorkflow` | Fully implemented with 15 tests | Never called from `HandleStartWorkflow` or `main.go`. A crash-recovery path does not exist end-to-end. |
| `team.go` (TeamConfig, DefaultTeamConfig, ValidateTeamConfig, GetReviewerConfigs) | Fully implemented with tests | Never used by orchestrator — reviewer dispatch hardcodes the 4 lens groups and uses `AgentRunner` directly instead of `AgentConfig`. |
| `CheckStaleness` / `CheckAllBreakers` | Individual breakers (`CheckMaxRounds`, `CheckCost`, etc.) are called from orchestrator | The aggregate `CheckAllBreakers` is never used. `CheckStaleness` is never called — the orchestrator has no issue-history tracking that feeds into staleness detection. |

---

## Code Findings

### MAJOR Findings

#### [MAJ-001] Error swallowing in orchestrator — `os.WriteFile` and `SaveState` errors silently dropped

- **Lens**: Error Handling
- **File**: `internal/specworkflow/orchestrator.go:457-458, 716`
- **Code**:
  ```go
  // Line 457-458: os.WriteFile error dropped
  mergedData, _ := json.MarshalIndent(merged, "", "  ")
  os.WriteFile(mergedPath, mergedData, 0o644)

  // Line 716: SaveState error dropped in FINALIZED state
  SaveState(specDir, state)
  ```
- **Issue**: The merged findings write to disk on line 458 drops the error from `os.WriteFile`. If this write fails (permissions, disk full), the orchestrator continues without the merged findings file, and subsequent agents that need to read it will fail with a confusing error. Similarly, `SaveState` on line 716 in the FINALIZED handler drops the error — if state persistence fails, there's no record of finalization.
- **Impact**: A disk-full or permissions error during a real workflow run will produce a misleading downstream failure rather than a clear error at the point of failure. The workflow may appear to complete successfully when state was not actually persisted.
- **Fix**:
  ```go
  mergedData, err := json.MarshalIndent(merged, "", "  ")
  if err != nil {
      return fmt.Errorf("marshal merged findings: %w", err)
  }
  if err := os.WriteFile(mergedPath, mergedData, 0o644); err != nil {
      return fmt.Errorf("write merged findings: %w", err)
  }

  // Line 716:
  if err := SaveState(specDir, state); err != nil {
      log.Printf("warning: failed to save finalized state: %v", err)
  }
  ```

---

#### [MAJ-002] `ResumeWorkflow`, `team.go`, `CheckStaleness`, and `BestEffortParse` are dead code — implemented, tested, but never called from production paths

- **Lens**: Correctness + Wiring
- **File**: `internal/specworkflow/resume.go`, `internal/specworkflow/team.go`, `internal/specworkflow/breakers.go:89`, `internal/specworkflow/recovery.go:226`
- **Issue**: These represent significant implemented functionality that is entirely disconnected from the running system:

  1. **`ResumeWorkflow()`**: The spec (Section 4.1, 7.3) requires crash recovery by persisting state at gates and resuming. `ResumeWorkflow` implements this probing logic but is never called. `HandleStartWorkflow` always creates a new orchestrator — it never checks for an existing workflow to resume.

  2. **`team.go`**: Contains `DefaultTeamConfig()`, `ValidateTeamConfig()`, `GetReviewerConfigs()` — a complete agent team configuration system. The orchestrator never uses it. The 4 reviewer lens groups are hardcoded in `orchestrator.go:413` and `review_dispatch.go:96`.

  3. **`CheckStaleness()`**: The spec (Section 9.3) requires staleness detection — "if a CRITICAL or MAJOR finding remains in the same status for N consecutive rounds, trigger escalation." `CheckStaleness` implements this but is never called because the orchestrator doesn't build the issue-history map that `CheckStaleness` requires.

  4. **`BestEffortParse()`**: `DetermineRecovery` returns `ActionBestEffortParse` for schema violations, but `handleAgentError` in the orchestrator never checks for this action and never calls `BestEffortParse`. The recovery path is incomplete.

- **Impact**: Crash recovery does not work. Staleness detection is not enforced. Best-effort parsing is not used. Team configuration is ignored. These are spec requirements that appear implemented but are actually inert.
- **Fix**: Wire these into the production path:
  1. Add resume logic to `HandleStartWorkflow` before creating a new orchestrator
  2. Either use `TeamConfig` to drive reviewer dispatch or remove it as dead code
  3. Build issue-status-per-round history in the orchestrator and call `CheckStaleness`
  4. Handle `ActionBestEffortParse` in `handleAgentError`

---

#### [MAJ-003] `orchestrator.go` exceeds the 500-line hard limit at 923 lines

- **Lens**: Overcomplexity
- **File**: `internal/specworkflow/orchestrator.go` (923 lines)
- **Issue**: The Go standards reference (standards-golang.md) specifies "No production file exceeds 300 lines (500 = hard limit)". `orchestrator.go` is 923 lines, nearly double the hard limit. The file contains the `RunWorkflow` main loop (250+ lines), all state-case handlers, agent dispatch, error handling, breaker checks, escalation logic, and the escalation summary writer.
- **Impact**: Difficult to navigate, review, and maintain. The 250-line `RunWorkflow` switch statement is a monolith that handles 12 different states.
- **Fix**: Extract per-state handlers into separate methods or files. For example:
  - `orchestrator_discovery.go` — StateDiscovery, StateHumanGate1
  - `orchestrator_review.go` — StateReviewing, StateRevising, StateJudging
  - `orchestrator_finalize.go` — StateFinalized, StateEscalated

---

### MINOR Findings

#### [MIN-001] `_ = reason` discards progress-based escalation reason

- **Lens**: Observability
- **File**: `internal/specworkflow/orchestrator.go:641`
- **Code**:
  ```go
  if shouldEscalate, reason := o.progressTracker.ShouldEscalate(); shouldEscalate {
      _ = reason
      o.escalateFrom(StateJudging)
      continue
  }
  ```
- **Issue**: The escalation reason is computed but explicitly discarded. The escalation summary written by `writeEscalationSummary` says only "User cancelled or workflow could not converge" — it does not include the actual reason (e.g., "two consecutive rounds with no progress" or "open CRITICAL+MAJOR increased for two consecutive rounds").
- **Fix**: Pass `reason` to `writeEscalationSummary` or log it.

---

#### [MIN-002] `_ = nextState` discards return value from gate cancel handler

- **Lens**: Correctness
- **File**: `internal/specworkflow/orchestrator.go:330-332`
- **Code**:
  ```go
  case "cancel":
      nextState, _ := gate1.HandleCancel()
      o.escalateFrom(StateHumanGate1)
      _ = nextState
  ```
- **Issue**: `HandleCancel()` returns `StateEscalated`, but the code ignores it and calls `escalateFrom` directly. The same pattern appears for gate 2 cancel. While functionally correct (both paths lead to escalation), the discarded return suggests the code was hastily wired.
- **Fix**: Either use the returned state or simplify to not call `HandleCancel()` at all since the escalation is hardcoded.

---

#### [MIN-003] `_ = needsRedraft` discards redraft signal from gate 2

- **Lens**: Correctness
- **File**: `internal/specworkflow/orchestrator.go:385-386`
- **Code**:
  ```go
  needsRedraft, nextState, err := gate2.HandleResolutions(resolutions)
  _ = needsRedraft
  ```
- **Issue**: The `needsRedraft` flag indicates whether the drafter needs to re-run with user answers incorporated. The orchestrator ignores it and just transitions to whatever `nextState` the gate handler returns. This works because the gate handler already sets `nextState = StateDrafting` when redraft is needed, but the explicit discard suggests the orchestrator was meant to do something additional with this information (e.g., pass user answers to the drafter prompt).
- **Fix**: Either use `needsRedraft` to incorporate user answers into the drafter re-invocation, or document why it's intentionally unused.

---

#### [MIN-004] `json.Unmarshal` errors silently ignored in gate state handlers

- **Lens**: Error Handling
- **File**: `internal/specworkflow/orchestrator.go:302-304, 370-372`
- **Code**:
  ```go
  dData, _ := os.ReadFile(discoveryPath)
  var disc DiscoveryOutput
  json.Unmarshal(dData, &disc)
  ```
- **Issue**: In HUMAN_GATE_1 and HUMAN_GATE_2, the discovery/drafter output files are read and parsed with errors silently dropped. If the file is missing or corrupt, `gate1.EnterGate(&disc)` receives a zero-value struct, causing the UI to display an empty gate prompt with no data.
- **Fix**: Handle errors from `os.ReadFile` and `json.Unmarshal`, returning an error or emitting a diagnostic event.

---

#### [MIN-005] `escalateFrom` swallows transition errors

- **Lens**: Error Handling
- **File**: `internal/specworkflow/orchestrator.go:872-883`
- **Code**:
  ```go
  func (o *Orchestrator) escalateFrom(from WorkflowState) {
      if isValidTransition(from, StateEscalated) {
          o.logTransition(from, StateEscalated)
          _ = o.sm.Transition(StateEscalated)
      } else {
          o.logTransition(from, StateError)
          _ = o.sm.Transition(StateError)
          o.logTransition(StateError, StateEscalated)
          _ = o.sm.Transition(StateEscalated)
      }
  }
  ```
- **Issue**: All three `Transition()` calls have their errors discarded. If the transition fails (e.g., due to a guard or persistence callback failure), the state machine remains in its previous state but the orchestrator proceeds as if escalation succeeded. This could leave the workflow in an inconsistent state.
- **Fix**: At minimum, log the transition errors. Ideally, return an error from `escalateFrom`.

---

#### [MIN-006] WebSocket handshake errors discarded during upgrade

- **Lens**: Error Handling
- **File**: `internal/api/websocket.go:246-247`
- **Code**:
  ```go
  _, _ = bufrw.WriteString(resp)
  _ = bufrw.Flush()
  ```
- **Issue**: After hijacking the HTTP connection for WebSocket upgrade, the write and flush of the 101 response both discard errors. If the client disconnects during the handshake, the server registers a dead connection in the hub. The `readFrames` goroutine will eventually clean it up, but the initial dead registration is unnecessary.
- **Fix**: Check the error from `bufrw.Flush()` and skip `addClient` if the handshake write failed.

---

### Observations

#### [OBS-001] `ChannelEmitter` is never closed

- **Lens**: Resource Leaks
- **File**: `internal/specworkflow/events.go:176-178`, `cmd/specworkflow/main.go:67`
- **Suggestion**: `NewChannelEmitter(64)` is created in main.go but `Close()` is never called. The `StartBroadcasting` goroutine uses `for event := range emitter.Events()` which only terminates when the channel is closed. Since the server runs until process exit, this is acceptable, but if the design ever supports graceful shutdown, the emitter should be closed to terminate the broadcast goroutine.

---

#### [OBS-002] Spec version files are looked up in workspace root, not in `specs/{feature-name}/`

- **Lens**: Correctness
- **File**: `internal/api/spec_endpoints.go:49-51`
- **Code**:
  ```go
  func specFilePath(workspaceDir string, version int) string {
      return filepath.Join(workspaceDir, fmt.Sprintf("spec-v%d.md", version))
  }
  ```
- **Suggestion**: The spec endpoint looks for `spec-v{N}.md` directly under `workspaceDir`, but the orchestrator writes spec files to `workspaceDir/specs/{featureName}/spec-v{N}.md`. This means `HandleGetCurrentSpec`, `HandleGetSpecVersion`, and `HandleGetSpecDiff` will always return 404 during a real workflow run because the files are in a different directory. This is a latent bug that won't manifest until someone actually tries to use the spec viewing endpoints during a live workflow. The `SpecAPIConfig.FeatureName` field exists but is not used in the path resolution.

---

#### [OBS-003] `InjectionMitigationInstruction()` is defined but not embedded in prompts

- **Lens**: Security
- **File**: `internal/specworkflow/security.go:17-23`, `internal/specworkflow/prompts.go:88-89`
- **Suggestion**: `InjectionMitigationInstruction()` in security.go returns a mitigation string, but the discovery prompt in `prompts.go:88-89` hardcodes its own similar-but-different inline text. The dedicated function is never called from any prompt builder. Consider using the shared function for consistency.

---

#### [OBS-004] `HandleListUploads` does not set status code 200 explicitly

- **Lens**: Correctness
- **File**: `internal/api/upload.go:378-379`
- **Suggestion**: The successful path of `HandleListUploads` writes `Content-Type` and encodes JSON but never calls `w.WriteHeader(http.StatusOK)`. Go defaults to 200, so this works, but it's inconsistent with the pattern used in `HandleUpload` (which explicitly writes 201).

---

#### [OBS-005] `readAll` in `claude_runner.go` always returns nil error

- **Lens**: Correctness
- **File**: `internal/specworkflow/claude_runner.go:164-177`
- **Code**:
  ```go
  func readAll(r interface{ Read([]byte) (int, error) }) ([]byte, error) {
      ...
      return buf, nil  // always nil error
  }
  ```
- **Suggestion**: The function signature returns an error but always returns `nil`. This masks io errors — if the pipe returns a real error (not just `io.EOF`), it's silently swallowed. On line 113, the caller also discards the error: `stderrBuf, _ = readAll(stderrPipe)`. Consider using `io.ReadAll` from the standard library.

---

## Test Results

| Status | Count |
|--------|-------|
| PASS | 420 |
| FAIL | 0 |
| SKIP | 0 |

All 420 tests pass across 2 packages. No failing or skipped tests.

### Test Quality Assessment

**Strengths:**
- Orchestrator tests (`TestOrchestratorHappyPath2Rounds`, `TestOrchestratorCancellation`, `TestOrchestratorCircuitBreakerMaxRounds`, etc.) use a mock `AgentRunner` and exercise the full state machine loop — they are genuine integration tests of the orchestrator.
- Review dispatch tests cover concurrent execution, retry logic, partial failures, schema violations, and cost accumulation.
- Issue tracker tests cover the full lifecycle state machine with valid and invalid transitions.
- Convergence tests cover pre-check logic, authority limits, verdict overrides, and cumulative thresholds.

**Weaknesses:**
- No tests exercise the full end-to-end path from `HandleStartWorkflow` HTTP handler through orchestrator to WebSocket events reaching a client. The handler tests verify HTTP status codes but use a mock that doesn't actually run `RunWorkflow`.
- Resume tests are comprehensive but test dead code (see MAJ-002).
- No test verifies that spec API endpoints (`HandleGetCurrentSpec`, etc.) return correct data from a running orchestrator — they use fake `GetState`/`GetTracker` callbacks.

---

## Verdict Rationale

The implementation is well-crafted with thorough unit and integration testing (420 tests, all passing). The core workflow loop is correctly wired end-to-end from `main.go` through HTTP handlers to the orchestrator. The R1/R2 wiring gap has been properly fixed.

However, three MAJOR findings prevent a PASS verdict:

1. **Error swallowing** (MAJ-001): `os.WriteFile` and `SaveState` errors silently dropped in the orchestrator will cause confusing downstream failures in production. This violates the spec's "Fail explicitly" principle (Section 4.2.9).

2. **Dead code paths** (MAJ-002): Four significant subsystems (`ResumeWorkflow`, `team.go`, `CheckStaleness`, `BestEffortParse`) are fully implemented and tested but never called from production code. This represents spec requirements (crash recovery, staleness detection) that appear complete but are actually inert. This is the same class of issue as the R1/R2 wiring gap.

3. **File size violation** (MAJ-003): `orchestrator.go` at 923 lines exceeds the 500-line hard limit.

None of these are blocking for a localhost development tool, but they represent incomplete wiring and code quality issues that should be addressed.

### Recommended Next Actions

- [ ] Fix MAJ-001: Handle `os.WriteFile` and `SaveState` errors in `orchestrator.go:457-458, 716`
- [ ] Fix MAJ-002: Wire `ResumeWorkflow` into `HandleStartWorkflow`, wire `CheckStaleness` into the orchestrator loop, handle `ActionBestEffortParse` in error recovery, and either wire or remove `team.go`
- [ ] Fix MAJ-003: Split `orchestrator.go` into smaller files (per-state handlers)
- [ ] Fix MIN-001: Log or use the escalation `reason` from `ShouldEscalate()`
- [ ] Fix MIN-004: Handle `os.ReadFile`/`json.Unmarshal` errors in gate state handlers
- [ ] Fix MIN-005: Log transition errors in `escalateFrom()`
- [ ] Investigate OBS-002: Spec endpoint path mismatch — `spec-v{N}.md` looked up in wrong directory

After fixing, re-run: `/grill-code`
