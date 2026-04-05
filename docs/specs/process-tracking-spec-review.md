# Adversarial Review: Process Tracking — Live Agent Monitoring with Kill Capability

**Spec reviewed**: docs/specs/process-tracking-spec.md
**Review date**: 2026-04-05
**Verdict**: REVISE

## Executive Summary

The spec is well-structured with strong traceability, a clearly reasoned non-behaviors section, and good edge-case coverage. However, it contains 2 CRITICAL and 8 MAJOR findings that must be addressed before implementation. The critical issues are: (1) the "process started event emitted even when SQLite write fails" behavior in the error flow section directly contradicts the spec's own PID reuse safety guarantee in FR-015, creating a window where kill can be called on a PID that is not in the store; and (2) the spec fails to define what happens when the WebSocket hub itself is nil or unavailable at event emission time — a real startup-ordering scenario in the existing codebase.

| Severity | Count |
|----------|-------|
| CRITICAL | 2 |
| MAJOR | 8 |
| MINOR | 4 |
| OBSERVATION | 3 |
| **Total** | **17** |

---

## Findings

### CRITICAL Findings

#### [CRIT-001] SQLite write failure creates PID-not-in-store state that breaks FR-015 kill safety guarantee

- **Lens**: Inconsistency / Insecurity
- **Affected section**: Behavioral Contract — Error Flows: "When the SQLite write for a process record fails, the event is still broadcast on WebSocket but an error is logged server-side." AND FR-015: "System MUST NOT send any signal to a PID unless it is present in both the process store (with status: 'running') AND the in-memory runtime map."
- **Description**: The spec says that if the SQLite write fails, the `process_started` event is still broadcast and the in-memory runtime map entry is still created (per FR-015's description of the map). This means a PID can be in the runtime map but absent from the store. FR-015 requires BOTH conditions to be satisfied before allowing kill. The kill logic therefore either (a) rejects the kill with `409 Conflict` because the record is not in the store — which silently makes kill impossible for a legitimately running process — or (b) performs the kill based on runtime-map membership alone, defeating the SQLite guard entirely. The spec does not state which of these behaviours occurs, but both are wrong in different ways. No test exercises this failure path for the kill flow.
- **Impact**: If SQLite is temporarily unavailable at process start (disk full, lock contention), the process runs and the user can see it in the WebSocket stream, but clicking Kill either fails silently (if store is required) or bypasses the store safety check (if runtime map alone is used). This is a data-integrity failure with security implications — a kill or a failure to kill during a disk-full incident.
- **Recommendation**: The spec must make a definitive choice and state it explicitly. The correct choice is: if the SQLite write fails, the process record MUST still be written using a retry (with bounded backoff) or the start event MUST be suppressed. "Log and continue" is not an acceptable contract for a system where the store is the kill-authorization gate. Add this to FR-004: "If the SQLite write for a new process record fails after N retries, the process_started event MUST NOT be emitted and the runtime map entry MUST NOT be added. The process is tracked as 'unmanaged' — it runs but cannot be killed via the endpoint." This preserves the safety guarantee at the cost of kill capability for that process.

---

#### [CRIT-002] ProcessTracker injection point into ClaudeRunner is incompatible with existing CloneForAgent/DefaultClaudeRunner construction pattern

- **Lens**: Incorrectness / Infeasibility
- **Affected section**: Assumptions: "ProcessTracker is injected as a constructor parameter into ClaudeRunner and CodexRunner (e.g. `DefaultClaudeRunner(..., tracker ProcessTracker)`)" AND Integration Boundaries — OS Signal Interface; Codebase: `ClaudeRunner.CloneForAgent()`, `ClaudeRunner.WithModel()`, `ClaudeRunner.WithJSONSchema()`, `ClaudeRunner.WithContext()`, `ClaudeRunner.ForJSONOnly()`
- **Description**: `ClaudeRunner` has six copy-constructor methods (`CloneForAgent`, `WithModel`, `WithJSONSchema`, `WithContext`, `ForJSONOnly`, `WithContext`), each of which does a shallow `clone := *r`. If `ProcessTracker` is added as a field, all six methods silently propagate the same tracker reference — which is the correct behaviour, but ONLY if the spec explicitly acknowledges this. More critically, `CloneForAgent` returns `AgentRunner` (not `*ClaudeRunner`), and the orchestrator calls it before passing the runner to the workflow phase. The spec does not address whether the cloned runner's tracker reference produces duplicate event emissions for the same logical agent, since the parent runner and the clone both hold the tracker and `feature`+`role` context. The spec says "feature and role context is already available inside the runner" but `ClaudeRunner.Run` currently takes `outputPath` as a parameter and derives no `feature` or `role` from any field — meaning `feature` and `role` must be added as fields to the struct, which is a larger change than the spec implies.
- **Impact**: Implementation will either (a) add `feature` and `role` as fields to `ClaudeRunner`/`CodexRunner` and discover that all six copy methods need audit, or (b) pass them differently and produce events with empty `feature`/`role` fields. If (b) occurs, the correlations the UI displays will be empty strings. This is a silent correctness failure invisible until the UI is rendered.
- **Recommendation**: Add an explicit section titled "Runner Struct Changes Required" specifying exactly which fields are added to `ClaudeRunner` and `CodexRunner` (`Feature string`, `Role string`, `Tracker ProcessTracker`), which existing copy methods automatically propagate them (all, by shallow copy), and confirm that this is intentional. State explicitly that `CloneForAgent` already propagates the tracker via shallow copy and no additional change is needed there.

---

### MAJOR Findings

#### [MAJ-001] WebSocket hub nil at startup — process_lost events may be silently dropped with no observability

- **Lens**: Incompleteness / Inoperability
- **Affected section**: User Story 3, Acceptance Scenario 2: "When the server starts up, a process_lost WebSocket event is emitted for each such record." AND FR-005: "System MUST update any status: 'running' process records to status: 'lost' during server startup BEFORE accepting HTTP or WebSocket connections."
- **Description**: FR-005 says the startup recovery runs BEFORE accepting connections. If no WebSocket clients can connect before startup recovery runs, then `process_lost` events emitted during that window will be sent to zero subscribers and silently dropped by the hub (per the existing hub behavior: "If no clients are connected, events are silently dropped"). The spec's BDD scenario for this case requires a `process_lost` event to be "broadcast" — but at startup, no client is connected. Any client that connects after startup will never receive the `process_lost` event unless the UI also polls `GET /api/processes` on tab open and renders `lost` status from there.
- **Impact**: A developer opens the UI after a server restart — the Running Agents tab shows the process as "running" (populated from initial `GET /api/processes` fetch) without a subsequent `process_lost` WebSocket event because it was already dropped. The row never updates unless the developer refreshes. The BDD scenario passes in integration tests (no real UI) but fails in practice.
- **Recommendation**: Add explicit behavior: "The Running Agents tab MUST populate its initial state from `GET /api/processes` on tab open, not solely from WebSocket events." This means startup `process_lost` events are informational for live updates, but the ground truth is always the REST endpoint. State this in FR-018/FR-019: "Initial table population MUST use GET /api/processes; WebSocket events MUST update already-rendered rows."

---

#### [MAJ-002] `status: "killed"` in process_ended event requires server-side state mutation that is not specified

- **Lens**: Ambiguity / Incompleteness
- **Affected section**: User Story 2, Acceptance Scenario 3: "the process_ended event carries status: 'killed' rather than 'exited'" AND FR-002 / BDD Scenario "process_ended event carries status 'killed' after user-initiated termination"
- **Description**: The spec requires that when a process is killed, `process_ended.status = "killed"`. But `cmd.Wait()` (or the go routine waiting on process exit) has no native way to know whether the termination was user-initiated or natural — both look like "process exited." The server must maintain state — a flag set by the kill endpoint before the process exits — that the process-end goroutine can read when `cmd.Wait()` returns. The spec names a `ProcessTracker` and `KillService` but never specifies how the `"this pid was kill-requested"` state is maintained between the kill request and the eventual exit. The BDD scenario Given clause is "the kill endpoint was called for it" — the When is "the subprocess terminates" — but the mechanism for transferring the intent is completely unspecified.
- **Impact**: Without this specification, two implementations are plausible: (1) a flag in the runtime map set by KillService; (2) checking the process's exit signal via `cmd.ProcessState`. On POSIX, a SIGTERM/SIGKILL death shows up as a signal in ProcessState, so option 2 is available — but only for signal-induced deaths. The spec should specify which mechanism, because a process could exit naturally at the exact same moment a kill is requested, producing a race between setting the flag and recording the exit.
- **Recommendation**: Add to the spec's Assumptions section: "When the kill endpoint sets a pid as 'kill-requested' in the runtime map before sending SIGTERM, the process-end goroutine MUST check this flag when recording exit status. If the flag is set, status='killed'; otherwise status='exited'. This takes priority over signal inspection. The flag MUST be set under the same mutex that guards the runtime map." Also add a test in Dataset 2 for the race condition: process exits normally at same instant as kill is requested — which status wins?

---

#### [MAJ-003] FR-006 `GET /api/processes` ordering and pagination are underspecified for production use

- **Lens**: Ambiguity / Incompleteness
- **Affected section**: FR-006: "returning a JSON array of all process records, ordered by started_at descending. The endpoint MUST support optional query parameters ?feature=<name> and ?status=<running|exited|killed|lost>"
- **Description**: The spec says "ordered by started_at descending" with no row limit. The Assumptions section says "pagination not needed for MVP — fewer than 50 processes." This creates an undocumented behavioral guarantee: the endpoint works correctly only up to ~50 records. The spec does not state what the maximum response size is or when an operator should be concerned. More practically, the `status` query parameter is defined as accepting `running|exited|killed|lost` but the spec does not specify what happens with an unrecognized status value (e.g., `?status=pending`). Returns 400? Returns all records? Returns empty array?
- **Impact**: An engineer will implement one of three behaviors for unrecognized status values, and the choice will silently affect production behavior. A server that has been running for weeks with 5,000 process records will return a 5,000-element JSON array on every tab open.
- **Recommendation**: (1) Add behavior for invalid `?status=` values: "MUST return `400 Bad Request` with `{"error": "invalid status filter: <value>"}`. (2) Document the row cap assumption explicitly in FR-006: "For MVP, response is unbounded; if process record count exceeds 500, the endpoint returns the 500 most recent records by started_at descending." Add a Dataset 1 row for invalid status query parameter.

---

#### [MAJ-004] Kill endpoint response body on 200 OK is not defined

- **Lens**: Ambiguity
- **Affected section**: Integration Boundaries — HTTP API: "`200 OK` with empty body on kill accepted" AND US-5, Acceptance Scenario 1: "the server sends SIGTERM to the PID"
- **Description**: The spec says `200 OK` with "empty body" when the kill is accepted (SIGTERM sent). However, the acceptance scenario says the Kill button becomes "Killing…" and then the row updates "when the `process_ended` event arrives." This implies the UI must distinguish between "SIGTERM sent, waiting for exit" and "process has exited." The `200 OK` response body being empty means there is no machine-readable confirmation of what actually happened. Moreover, if SIGTERM is sent and the process has not exited yet, is it correct to return 200 before the process is dead? The spec appears to say yes (kill-accepted, not kill-completed), but this is nowhere stated explicitly.
- **Impact**: A frontend developer may implement the Kill button to treat 200 as "process is dead" rather than "SIGTERM was sent." This causes the UI to prematurely mark the row as "killed" before the `process_ended` event arrives, creating a visible flash or race condition.
- **Recommendation**: State explicitly in FR-007/HTTP API section: "`POST /api/processes/{pid}/kill` returns `200 OK` to indicate SIGTERM was accepted and sent — NOT that the process has exited. The response body MUST be `{"status": "kill_accepted"}`. The process end is communicated asynchronously via the `process_ended` WebSocket event." Update Integration Boundaries to match.

---

#### [MAJ-005] Simultaneous kill requests race condition spec is underspecified for the locking mechanism

- **Lens**: Incompleteness / Infeasibility
- **Affected section**: Edge Cases: "If two clients send kill for the same PID simultaneously, only one SIGTERM is sent; the second request receives 409 Conflict." AND BDD Scenario "Simultaneous kill requests for the same PID"
- **Description**: The spec mandates that only one SIGTERM is sent, but never specifies the locking mechanism. Given that this is a Go server, the obvious approach is a mutex per-PID or a CAS operation on the runtime map. Without specifying this, the implementation may use a global mutex (blocking all kill requests during any kill) or a per-PID mutex (correct) or a channel-based approach. The BDD scenario says "the first request receives `200 OK` and the second receives `409 Conflict`" — but if requests arrive at the kernel simultaneously and both find `status=running` in the store before either can update it, both send SIGTERM. The spec does not define at what granularity the state transition is atomic.
- **Impact**: Two simultaneous SIGTERM signals are generally harmless to the target process, but the spec says "only one SIGTERM is sent" — this is a testable contract that the implementation may not satisfy without an explicit locking specification.
- **Recommendation**: Add to the Assumptions section: "The ProcessTracker runtime map is protected by a sync.Mutex. The state transition from 'running' to 'kill-requested' MUST be performed under this mutex atomically: read status, check running, set kill-requested flag, return error if already not running. This ensures the invariant that only one SIGTERM is sent per PID."

---

#### [MAJ-006] `process_lost` event payload schema is inconsistent with the WebSocket event datasets

- **Lens**: Inconsistency
- **Affected section**: Dataset 3, Row 3: "process_lost: Required fields: type, pid (int >0), feature, role" AND BDD Scenario "Running process marked lost on server startup": "a process_lost WebSocket event is broadcast for pid 12349" (no mention of feature/role in the Then clause)
- **Description**: Dataset 3 defines `process_lost` as requiring `feature` and `role` fields. The BDD scenario's Then clause for "Running process marked lost on server startup" only says a `process_lost` event is broadcast with `pid: 12349` — it does not assert `feature` and `role` are present. This inconsistency means a test written from the BDD scenario alone will not verify the required fields, and the Dataset 3 definition will not be tested. Additionally, the `process_started` event in Dataset 3 (Row 1) marks `feature` and `role` as required, but `process_ended` (Row 2) marks them as optional — yet the UI needs feature/role to link the `process_ended` row update back to the correct table row. If `process_ended` arrives with no `feature`/`role`, the UI must look up the row by PID alone, which is fine, but this should be stated as the design intent rather than left as "optional."
- **Impact**: The BDD test for startup recovery will pass without verifying feature/role in the event payload, leaving those fields untested. The UI engineer may assume `process_ended` always contains `feature`/`role` and be surprised when it does not.
- **Recommendation**: (1) Update the BDD scenario "Running process marked lost on server startup" to add: "And the event contains `pid: 12349`, `feature: <original feature>`, and `role: <original role>`". (2) Add a note to Dataset 3 Row 2 explaining why `feature`/`role` are optional on `process_ended` (they can be looked up by PID from the UI's local table state) and confirm this is intentional by adding a test: "TestProcessTracker_EmitsEndedEvent_WithoutFeatureRole — verify UI can correlate by PID alone."

---

#### [MAJ-007] FR-012 and FR-013 both map to the same BDD scenario — traceability matrix conflation

- **Lens**: Inconsistency
- **Affected section**: Traceability Matrix rows FR-012 and FR-013: Both map to BDD scenario "Kill request for already-terminated process returns 409" and both map to `TestProcessHandler_Kill_409` and `TestKillService_AlreadyTerminated` respectively. FR-012 says "404 Not Found for kill requests targeting a PID not present in the process store." FR-013 says "409 Conflict for kill requests targeting a process whose stored status is not 'running'."
- **Description**: These are two distinct error conditions with different HTTP status codes (404 vs 409). The traceability matrix maps both to the same BDD scenario "Kill request for already-terminated process returns 409." FR-012 (PID not in store → 404) has no dedicated BDD scenario and no dedicated test. The scenario outline "Kill endpoint rejects invalid PID values" only covers PID ≤ 0 and non-numeric — it does not cover a valid numeric PID that is simply absent from the store. The 404 case is mentioned in the Behavioral Contract error flows but has no BDD scenario, no test in the TDD plan, and no dataset entry.
- **Impact**: FR-012 will be implemented (it's in the behavioral contract) but never formally verified. A regression that returns 409 instead of 404 for unknown PIDs will not be caught by the test suite.
- **Recommendation**: Add a new BDD scenario: "Kill request for PID not in process store returns 404 — Given no process record exists for pid 99999, When POST /api/processes/99999/kill is called, Then the server returns 404 Not Found with {"error": "process 99999 not found"}, And no signal is sent." Add `TestProcessHandler_Kill_404` and `TestKillService_UnknownPID` to the TDD plan. Add FR-012 to Dataset 1 (a row for a valid-range PID not in store).

---

#### [MAJ-008] SC-005 "within 2 seconds of server startup" startup recovery SLA is not tested

- **Lens**: Infeasibility / Incompleteness
- **Affected section**: SC-005: "Within 2 seconds of server startup, all previously status: 'running' records in the store are updated to status: 'lost'."
- **Description**: No test in the TDD plan measures this 2-second SLA. `TestStartupRecovery_Integration` (test #25) is described as "Write running record, call startup recovery, verify lost" — it verifies correctness but not timing. The 2-second bound is a latency SLA, which requires a timed assertion. Additionally, the SLA is measured from "server startup" — but startup involves SQLite open, schema migration, and recovery scan. If the database has 10,000 stale "running" records, a bulk UPDATE query could take longer than 2 seconds. The spec does not specify whether the 2-second SLA applies only to the normal case (< 50 records) or is an absolute requirement.
- **Impact**: SC-005 will pass acceptance if correctness is verified but timing is never measured. Under load (many stale records), startup may block connection acceptance for several seconds.
- **Recommendation**: Either (a) add a timing assertion to `TestStartupRecovery_Integration` that fails if recovery takes > 2 seconds for a configurable number of stale records (e.g., 1000), or (b) drop the 2-second SLA from SC-005 and replace it with "recovery MUST complete before the HTTP listener is opened" (which is testable structurally without a wall-clock assertion). Also specify what happens if the recovery UPDATE fails (disk full during startup) — does the server refuse to start or proceed with potentially incorrect kill guards?

---

### MINOR Findings

#### [MIN-001] "Killing…" button state has no timeout — can be stuck forever

- **Lens**: Incompleteness
- **Affected section**: FR-022: "Kill button MUST be disabled with label 'Killing…' after the kill request is sent and before the process end event arrives."
- **Description**: If the WebSocket connection drops after the kill request is sent but before `process_ended` arrives, the Kill button remains in "Killing…" state indefinitely. There is no timeout or reconnect behavior specified for this intermediate UI state.
- **Recommendation**: Add to FR-022: "If the WebSocket reconnects and a subsequent GET /api/processes shows the process status as killed/exited/lost, the button state MUST be updated to reflect the stored status. If the reconnect reveals the process is still running, the Kill button MUST revert to active state."

---

#### [MIN-002] Dataset 4 (SIGTERM escalation timing) boundary condition row 2 is incorrect

- **Lens**: Incorrectness
- **Affected section**: Dataset 4, Row 2: "SIGTERM sent at t=0, Process exits at t=10s, Escalation threshold=10s, SIGKILL sent? No"
- **Description**: The escalation fires "after 10 seconds." If the process exits at exactly t=10s, whether SIGKILL is sent depends on whether the timer fires before or after the exit is observed — this is a race condition. The dataset records "No" as a definitive answer but the correct answer is "implementation-defined / race." The spec should either define the tiebreaker or avoid this exact-boundary test case.
- **Recommendation**: Change row 2 to t=9s (safely before threshold) and add a note: "t=10s is ambiguous due to timer/exit race; the implementation MUST cancel the timer on exit using a select with a cancel channel, but which fires first at the exact boundary is OS-scheduler-dependent and untestable reliably."

---

#### [MIN-003] `status: "lost"` not included in FR-013's list of non-running statuses

- **Lens**: Incompleteness
- **Affected section**: FR-013: "System MUST return 409 Conflict for kill requests targeting a process whose stored status is not 'running'." AND Dataset 2, Row 8: "lost | Server restart | lost | Terminated records not modified"
- **Description**: Status `"lost"` is a terminal status. The spec's Dataset 2 and Status Transitions section confirm lost processes should not be modified. FR-013 says "not 'running'" which implicitly covers `lost`, but Dataset 2 row 8 (lost → kill attempt) is not present in Dataset 2. The kill-attempt scenarios in Dataset 2 only cover `exited` and `killed` (rows 9 and 10). A `lost` process should also return 409, but this case is not explicitly tested.
- **Recommendation**: Add Dataset 2 row 11: "lost | Kill endpoint called | 409 Conflict | Kill request for already-terminated process | No signal sent." Add a test case to `TestKillService_AlreadyTerminated` exercising the `lost` status specifically.

---

#### [MIN-004] "Ambiguity Warnings: All ambiguities resolved" is a false assertion

- **Lens**: Ambiguity
- **Affected section**: Ambiguity Warnings section: "All ambiguities resolved. (No open items.)"
- **Description**: The section asserts no ambiguities remain. CRIT-002 (feature/role field injection), MAJ-002 (killed-flag mechanism), MAJ-004 (200 OK response body semantics), and MAJ-005 (locking mechanism) are all genuine ambiguities that were not identified. Asserting completeness where ambiguity remains creates false confidence.
- **Recommendation**: Remove the "All ambiguities resolved" assertion. Replace with a list of the resolved ambiguities (good — the Clarifications section already captures these) and remove the affirmative claim about no open items until after this review is addressed.

---

### Observations

#### [OBS-001] `GET /api/processes` initial load and WebSocket event stream could diverge during tab navigation

- **Lens**: Inoperability
- **Affected section**: FR-018 and FR-019 (real-time table updates via WebSocket)
- **Suggestion**: The spec does not define how the UI handles the case where the user navigates away from the Running Agents tab and back. If WebSocket events arrived while the tab was hidden (DOM detached), the table state when the user returns may be stale. Specify whether the tab re-fetches `GET /api/processes` on every tab activation or maintains a persistent in-memory event log.

---

#### [OBS-002] PID column displayed as string vs. integer in UI table

- **Lens**: Ambiguity
- **Affected section**: FR-017: "columns: Feature, Role, PID, Start Time, Status, Action" AND BDD Scenario "Running Agents tab shows active processes": `"12350"` (string in quote marks)
- **Suggestion**: The BDD scenario quotes the PID as a string (`"12350"`) while FR-017 uses the unquoted label "PID." The WebSocket event spec in Dataset 3 defines `pid` as `int > 0`. Specify whether the UI renders PID as a number (right-aligned, monospace) or as a string. This affects copy-paste usability for operators trying to run `ps -p` from the UI value.

---

#### [OBS-003] No rate limit on POST /api/processes/{pid}/kill

- **Lens**: Insecurity
- **Affected section**: Integration Boundaries — HTTP API
- **Suggestion**: The kill endpoint accepts unlimited requests from any connected client. An operator with UI access could spam kill requests. While FR-015 prevents double-signaling, there is no rate limit preventing a client from hammering 404/409 responses. Consider documenting that the kill endpoint is intentionally rate-limit-free (acceptable for internal tooling) or add a note that rate limiting should be added before any public exposure.

---

## Structural Integrity

| Check | Result | Notes |
|-------|--------|-------|
| Every user story has acceptance scenarios | PASS | All 5 user stories have acceptance scenarios |
| Every acceptance scenario has BDD scenarios | PASS | All acceptance scenarios have corresponding BDD scenarios |
| Every BDD scenario has `Traces to:` reference | PASS | All BDD scenarios carry Traces to references |
| Every BDD scenario has a test in TDD plan | FAIL | No test covers FR-012 (404 for unknown PID) — see MAJ-007 |
| Every FR appears in traceability matrix | FAIL | FR-012 maps to the same scenario as FR-013 with no unique coverage — see MAJ-007 |
| Every BDD scenario in traceability matrix | PASS | All BDD scenarios appear in the matrix |
| Test datasets cover boundaries/edges/errors | FAIL | Dataset 1 missing row for valid-PID not in store (FR-012); Dataset 2 missing lost→kill attempt; Dataset 4 row 2 boundary is incorrect — see MAJ-007, MIN-002, MIN-003 |
| Regression impact addressed | PASS | Regression table present and covers all modified runners |
| Success criteria are measurable | FAIL | SC-005 timing SLA has no corresponding test — see MAJ-008 |

---

## Test Coverage Assessment

### Missing Test Categories

| Category | Gap Description | Affected Scenarios |
|----------|----------------|-------------------|
| 404 Not Found for unknown PID | No BDD scenario, no test, no dataset row | FR-012 kill request for untracked PID |
| `lost` status kill attempt | Dataset 2 row missing; test case absent from TestKillService_AlreadyTerminated | FR-013 for lost processes |
| SQLite write failure during kill flow | No test for kill-attempted-but-no-store-record state | CRIT-001 |
| Startup recovery timing | SC-005 2-second SLA has no timing assertion | TestStartupRecovery_Integration |
| WebSocket reconnect during Killing… state | No test for stuck button after WS disconnect | MIN-001 |
| `process_lost` event payload field verification | BDD scenario does not assert feature/role in event | MAJ-006 |

### Dataset Gaps

| Dataset | Missing Boundary Type | Recommendation |
|---------|----------------------|----------------|
| Dataset 1 | Valid numeric PID not in store → 404 | Add row: PID=99999, Expected=404 Not Found, Traces to=FR-012 |
| Dataset 1 | Invalid status query param → 400 | Add row for `?status=pending` per MAJ-003 |
| Dataset 2 | `lost` status → kill attempt → 409 | Add row 11 per MIN-003 |
| Dataset 4 | Row 2 exact-boundary race | Change to t=9s, annotate t=10s as untestable |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| WebSocket Hub (process events) | ok | risk | risk | risk | ok | ok | T: no integrity check on broadcasted events; R: no audit trail for who received which events; I: process metadata (feature, role, PID) broadcast to ALL connected WS clients without auth check |
| SQLite Process Store | ok | ok | risk | ok | risk | ok | R: no audit log for who requested kill; D: no mention of what happens if disk fills during bulk startup recovery UPDATE |
| POST /api/processes/{pid}/kill | ok | ok | risk | ok | risk | risk | R: kill actions not attributed to a user/session; D: no rate limit; E: any WS client can kill any tracked process — no per-user authorization specified |
| ProcessTracker runtime map | ok | risk | ok | ok | ok | ok | T: flag set between kill-request and process-exit could be read/written by concurrent goroutines without explicit mutex spec (see MAJ-002/MAJ-005) |
| HTTP API GET /api/processes | ok | ok | ok | risk | ok | ok | I: process metadata including PIDs, features, and roles returned to any caller with HTTP access |

**Legend**: risk = identified threat not mitigated in spec, ok = adequately addressed or not applicable

---

## Unasked Questions

1. **Who can call the kill endpoint?** The spec makes no mention of authentication or authorization on `POST /api/processes/{pid}/kill`. Is this endpoint accessible to any user with HTTP access? The system appears to be an internal developer tool, but this should be stated explicitly rather than left implicit.
2. **What happens if the SQLite recovery UPDATE at startup fails?** FR-005 requires all running records to be marked lost before connections are accepted. If this UPDATE fails, does the server refuse to start? Log and proceed? The spec does not specify.
3. **How does the UI populate the Running Agents tab on initial load — REST or WebSocket?** FR-018 says rows are added from WebSocket events, but the startup `process_lost` events are dropped before any client connects. The spec never explicitly says `GET /api/processes` is the initial state source for the tab.
4. **Is the `ProcessTracker` interface a new type or an existing one?** The spec names `ProcessTracker`, `ProcessStore`, `KillService`, and `SignalSender` as distinct types. Are these all new? Do they share a package with the runners or live in `internal/api`? The Integration Boundaries section places `ProcessTracker` in the runner package but `ProcessStore` and `KillService` are unplaced.
5. **What is the HTTP router registration path for the new endpoints?** The spec says `GET /api/processes` and `POST /api/processes/{pid}/kill` but does not state how these are registered in `cmd/specworkflow/main.go` or whether they go through `WorkflowManager` or a new handler type.
6. **What happens to the escalation timer goroutine if the server receives SIGTERM during the 10-second window?** The spec covers server crash mid-kill but not graceful shutdown. If the server shuts down cleanly while a SIGKILL escalation is pending, does it wait for the timer? Cancel it? Send SIGKILL immediately?

---

## Verdict Rationale

CRIT-001 must be resolved before implementation because it creates an undefined behavior at the intersection of the SQLite failure path and the kill safety guarantee. The spec cannot be implemented correctly without knowing what happens when these two behaviors conflict. CRIT-002 must be resolved because the specified injection pattern ("add a constructor parameter") understates the required struct changes and will produce empty feature/role fields in events if not addressed explicitly.

The MAJOR findings are all implementable gaps — missing test coverage for FR-012, unclear kill-status tracking mechanism, undefined 200 response body, missing locking specification — but each one will produce incorrect or untested behavior if not addressed. In aggregate they represent a spec that would ship with observable bugs on first use.

### Recommended Next Actions

- [ ] Resolve kill-safety contract when SQLite write fails — CRIT-001 (FR-004, Behavioral Contract error flows)
- [ ] Specify `Feature` and `Role` as explicit struct fields in ClaudeRunner/CodexRunner — CRIT-002 (Assumptions section)
- [ ] Add "tab initial state from GET /api/processes" requirement — MAJ-001 (FR-018/FR-019)
- [ ] Specify kill-flag/mutex mechanism for status="killed" tracking — MAJ-002 (Assumptions section)
- [ ] Add invalid ?status= param behavior and row cap to FR-006 — MAJ-003
- [ ] Define 200 OK kill response body as {"status": "kill_accepted"} — MAJ-004 (FR-007, Integration Boundaries)
- [ ] Specify per-PID mutex for simultaneous kill prevention — MAJ-005 (Assumptions section)
- [ ] Fix process_lost BDD scenario to assert feature/role fields — MAJ-006 (BDD scenario, Dataset 3)
- [ ] Add FR-012 dedicated BDD scenario and test (TestProcessHandler_Kill_404) — MAJ-007
- [ ] Add timing assertion or drop wall-clock SLA from SC-005 — MAJ-008
