# Feature Specification: Process Tracking — Live Agent Monitoring with Kill Capability

**Created**: 2026-04-05
**Status**: Draft
**Input**: User request — emit subprocess PID in WebSocket event stream, add Running Agents UI tab with SIGTERM/SIGKILL kill capability.

---

## User Stories & Acceptance Criteria

### User Story 1 — Process Start Event Emission (Priority: P0)

Any operator or automated observer watching the WebSocket event stream wants to
know, in real time, when an agent subprocess starts — including which workflow
feature and role spawned it and what its OS PID is — so that they can correlate
log output, metric flows, and resource usage with specific agent invocations.
Currently the PID is only written to the server log; nothing downstream receives
it. This story makes PID a first-class structured field in the event stream.

**Why this priority**: Everything else (UI, kill, persistence) depends on PID
being in the stream. Without this, no other story can be built.

**Independent Test**: Start a workflow, observe the WebSocket stream, and verify
that a `process_started` event containing `feature`, `role`, `pid`, and
`started_at` fields is received within 100 ms of the subprocess launch.

**Acceptance Scenarios**:

1. **Given** a ClaudeRunner is configured for feature "auth-spec" with role "Reviewer", **When** `Run` is called and `cmd.Start()` succeeds, **Then** a `process_started` WebSocket event is broadcast containing `feature: "auth-spec"`, `role: "Reviewer"`, a positive integer `pid`, and an RFC3339 `started_at` timestamp.
2. **Given** a CodexRunner is configured for feature "auth-spec" with role "Drafter", **When** `Run` is called and the subprocess starts, **Then** a `process_started` WebSocket event is broadcast with the same structured fields.
3. **Given** `cmd.Start()` returns an error (subprocess fails to launch), **When** `Run` propagates the error, **Then** no `process_started` event is emitted.

---

### User Story 2 — Process End Event Emission (Priority: P0)

An operator wants to know when an agent subprocess terminates — and why — so
that hung agents are detected, error exits are auditable, and the Running Agents
table can reflect current state without manual refresh. Currently there is no
downstream notification when a subprocess ends.

**Why this priority**: Foundational alongside US-1; the UI table and kill
acknowledgement both depend on end events.

**Independent Test**: Start a workflow, let it complete normally, and verify a
`process_ended` event is received with matching PID, a non-negative `exit_code`,
and `status: "exited"`.

**Acceptance Scenarios**:

1. **Given** a subprocess has started and emitted a `process_started` event, **When** the subprocess exits with code 0, **Then** a `process_ended` WebSocket event is broadcast with the matching `pid`, `exit_code: 0`, `status: "exited"`, and an RFC3339 `ended_at` timestamp.
2. **Given** a subprocess has started, **When** the subprocess exits with a non-zero code, **Then** a `process_ended` event is broadcast with the matching `pid`, the actual `exit_code`, and `status: "exited"`.
3. **Given** a subprocess was sent SIGTERM or SIGKILL by the kill endpoint, **When** the subprocess terminates, **Then** the `process_ended` event carries `status: "killed"` rather than `"exited"`.

---

### User Story 3 — Process Record Persistence (Priority: P1)

A developer restarting the server during an active workflow wants to see the
history of all agent processes that ran — including ones that were active at
shutdown — so that long-running workflow audits are not silently lost on every
server restart. Currently all state is in-memory and lost on restart.

**Why this priority**: Without persistence the kill feature is also unreliable
across restarts. P1 because P0 stories function without it, but the feature is
not production-usable without it.

**Independent Test**: Start the server, trigger one agent subprocess, shut the
server down, restart it, call `GET /api/processes`, and verify the process
record is present with the correct feature, role, PID, and start time.

**Acceptance Scenarios**:

1. **Given** a `process_started` event has been emitted, **When** the record is written to the process store, **Then** a subsequent `GET /api/processes` returns the record even after a server restart.
2. **Given** a process record with `status: "running"` exists in the store, **When** the server starts up, **Then** that record's status is updated to `"lost"` and a `process_lost` WebSocket event is emitted for each such record.
3. **Given** a process record with `status: "exited"` or `"killed"` exists in the store, **When** the server starts up, **Then** that record is not modified.

---

### User Story 4 — Running Agents Sidebar Tab (Priority: P1)

A developer monitoring an active workflow wants a dedicated UI view that lists
every agent process — live and historical — so they can see at a glance what is
running, what has completed, and what might be hung without reading raw logs.
Currently process information is buried in the Messages tab log stream.

**Why this priority**: The primary user-facing surface of the feature. P1 because
the kill capability (US-5) is exposed through this tab.

**Independent Test**: Open the UI with at least one workflow running; click
"Running Agents" in the sidebar; verify the tab displays a table with columns
Feature, Role, PID, Start Time, Status and at least one row for each live
subprocess.

**Acceptance Scenarios**:

1. **Given** the UI is open and at least one agent subprocess is active, **When** the user clicks "Running Agents" in the sidebar, **Then** a table is displayed with columns: Feature, Role, PID, Start Time, Status, and Action.
2. **Given** the Running Agents tab is open, **When** a new `process_started` WebSocket event arrives, **Then** a new row is added to the table without a page reload.
3. **Given** the Running Agents tab is open and a row with `status: "running"` is displayed, **When** a `process_ended` or `process_lost` event arrives for that PID, **Then** the row's Status cell is updated to the new status and the Kill button is removed.
4. **Given** no agent processes have ever run, **When** the Running Agents tab is opened, **Then** an empty-state message is displayed ("No agent processes recorded.").

---

### User Story 5 — Kill a Live Agent Process (Priority: P1)

A developer suspects an agent is hung — consuming time, tokens, and cost with no
progress — and wants to terminate it immediately from the UI without needing
shell access to the server. Currently there is no way to kill a specific agent
subprocess from the UI.

**Why this priority**: Core value proposition of the feature. P1 alongside the
tab that exposes it.

**Independent Test**: Start a long-running agent; click Kill on its table row;
within 15 seconds, the row status changes to "killed" and `ps` on the server no
longer shows the PID.

**Acceptance Scenarios**:

1. **Given** a process row with `status: "running"` is shown in the Running Agents tab, **When** the user clicks Kill and confirms the prompt, **Then** the server sends SIGTERM to the PID, the Kill button becomes disabled with label "Killing…", and the row updates to `status: "killed"` when the `process_ended` event arrives.
2. **Given** a SIGTERM has been sent to a subprocess and the process has not exited within 10 seconds, **When** the escalation timeout fires, **Then** the server sends SIGKILL to the PID.
3. **Given** the user clicks Kill on a PID that has already exited between the click and the server request, **When** the server processes the request, **Then** a `409 Conflict` response is returned, the UI shows a brief "Process already ended" notice, and no signal is sent.
4. **Given** the user clicks Kill on a row, **When** the confirmation prompt is displayed, **Then** clicking Cancel aborts the kill and sends no signal to the server.
5. **Given** the kill endpoint is called for a PID that is not owned by the current server process, **When** the OS rejects the signal with EPERM, **Then** the server returns `403 Forbidden` with a human-readable error and the UI displays it.

---

## Behavioral Contract

**Primary flows:**
- When any agent runner successfully starts a subprocess, the system emits a `process_started` event on the WebSocket with feature, role, PID, and timestamp.
- When any agent runner's subprocess terminates (any cause), the system emits a `process_ended` event with PID, exit code, termination status, and timestamp.
- When a user requests kill via `POST /api/processes/{pid}/kill`, the system sends SIGTERM and, if the process survives 10 seconds, escalates to SIGKILL.
- When the server starts, all process records with `status: "running"` are immediately marked `"lost"`.

**Error flows:**
- When `cmd.Start()` fails, no `process_started` event is emitted and no process record is created.
- When kill is requested for an already-terminated PID, the server returns `409 Conflict` and sends no signal.
- When kill is requested for a PID the server process does not own, the server returns `403 Forbidden`.
- When kill is requested for a PID that was never tracked by this server, the server returns `404 Not Found`.
- When the SQLite write for a new process record fails after retries, the `process_started` event MUST NOT be emitted and the runtime map entry MUST NOT be added. The process runs unmanaged — it cannot be killed via the endpoint. An error is logged server-side.

**Boundary conditions:**
- When a PID of 0 or any negative value is supplied to the kill endpoint, the server returns `400 Bad Request` without sending any signal.
- When multiple subprocesses exist for the same feature (e.g., parallel reviewers), each has its own distinct row in the Running Agents table keyed by PID.

---

## Edge Cases

- **PID reuse**: If a tracked process exits and the OS reuses its PID for an unrelated system process before the kill request arrives, the server must verify the process is still a known child before sending a signal. If it cannot be confirmed as a child, the kill is rejected with `409 Conflict`.
- **Server crash mid-kill**: If the server crashes after sending SIGTERM but before receiving the process exit, the process record remains `status: "running"` at next startup and is marked `"lost"` per the startup recovery rule.
- **Simultaneous kill requests**: If two clients send kill for the same PID simultaneously, only one SIGTERM is sent; the second request receives `409 Conflict`.
- **Process exits during SIGKILL window**: If the process exits after SIGTERM but before the 10-second escalation fires, the escalation timer is cancelled and no SIGKILL is sent.
- **Zero active processes**: The Running Agents tab must show an empty-state message rather than an empty or broken table.
- **Very high PID values**: PIDs up to the platform maximum (`/proc/sys/kernel/pid_max` on Linux, typically 4,194,304) must be handled without integer overflow.
- **Runner exits before subprocess**: If the Go process exits the `Run` method abnormally (panic recovery), the `process_ended` event must still be emitted if a PID was recorded.

---

## Explicit Non-Behaviors

- The system must not send SIGKILL as the first signal because the agent may need time to flush output, close files, and write its result — premature SIGKILL would corrupt workflow output.
- The system must not expose a bulk-kill-all endpoint because accidental misuse could terminate all running agents across all workflows at once.
- The system must not stream subprocess stdout/stderr from the Running Agents tab because that functionality already exists in the Messages tab; duplication would create maintenance burden.
- The system must not allow killing processes with PID ≤ 0 because `kill(0, sig)` sends to the entire process group and `kill(-1, sig)` sends to all processes owned by the user — both are catastrophic.
- The system must not add OTEL SDK dependencies (go.opentelemetry.io/otel, go.opentelemetry.io/sdk) because the user explicitly scoped this feature to WebSocket event stream attachment only.
- The system must not kill processes that are not tracked in the process store because it cannot verify ownership, risking accidental termination of unrelated system processes.

---

## Integration Boundaries

### WebSocket Hub (internal/api/websocket.go)

- **Data in**: `ProcessEvent` structs (started, ended, lost) from the process tracker
- **Data out**: JSON-serialised events broadcast to all connected WebSocket clients
- **Contract**: Existing `BroadcastEvent` pattern; new event types added alongside existing ones
- **On failure**: If no clients are connected, events are silently dropped (existing hub behavior); if broadcast fails for one client, others are unaffected
- **Development**: Real WebSocket hub — no mock needed; existing test helpers cover broadcasting

### SQLite Process Store (metrics.db or co-located process.db)

- **Data in**: ProcessRecord structs (feature, role, pid, status, started_at, ended_at, exit_code)
- **Data out**: Slice of ProcessRecord on query
- **Contract**: SQLite via `modernc.org/sqlite` driver; new table `agent_processes` in the existing database file
- **On failure**: If the write fails (disk full, locked) after retries, the `process_started` event MUST NOT be emitted and the process MUST NOT be added to the runtime map. The process runs but cannot be killed via the endpoint (it is "unmanaged"). Error is logged server-side.
- **Development**: Real SQLite file in temp dir for tests; no mock needed

### OS Signal Interface (os/exec, syscall)

- **Data in**: PID (int), signal (syscall.SIGTERM or syscall.SIGKILL)
- **Data out**: Error (EPERM if not permitted, ESRCH if PID does not exist)
- **Contract**: `syscall.Kill(pid, sig)` — POSIX signal semantics
- **On failure**: EPERM → 403 response; ESRCH (process gone) → treat as already-ended, 409 response
- **Development**: Real OS calls in integration tests; unit tests use a `SignalSender` interface with a mock implementation

### HTTP API (internal/api/workflow_handler.go)

- **Data in**: Path parameter `{pid}` (string, parsed to int) for kill endpoint; no body
- **Data out**: `200 OK` with `{"status": "kill_accepted"}` body when SIGTERM is sent (NOT when the process is dead — process death is communicated asynchronously via `process_ended` WebSocket event); `4xx` on errors; `200 OK` with JSON array for list
- **Note**: The kill endpoint is intentionally rate-limit-free for internal tooling. Rate limiting and authentication MUST be added before any public or multi-tenant exposure.
- **Contract**: RESTful; JSON responses; same router and middleware as existing handlers
- **On failure**: Handler returns appropriate 4xx/5xx with `{"error": "…"}` JSON body
- **Development**: Real HTTP handler tested via `httptest.NewRecorder`

---

## BDD Scenarios

### Feature: Process Tracking

#### Background

- **Given** the server is running with at least one active WebSocket connection

---

#### Scenario: ClaudeRunner emits process_started event on successful subprocess launch

**Traces to**: User Story 1, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a ClaudeRunner configured with feature "auth-spec" and role "Reviewer"
- **When** `Run` is called and `cmd.Start()` succeeds
- **Then** a `process_started` WebSocket event is broadcast
- **And** the event contains `feature: "auth-spec"`, `role: "Reviewer"`, a positive integer `pid`, and a valid RFC3339 `started_at` timestamp

---

#### Scenario: CodexRunner emits process_started event on successful subprocess launch

**Traces to**: User Story 1, Acceptance Scenario 2
**Category**: Happy Path

- **Given** a CodexRunner configured with feature "auth-spec" and role "Drafter"
- **When** `Run` is called and the subprocess starts successfully
- **Then** a `process_started` WebSocket event is broadcast
- **And** the event contains `feature: "auth-spec"`, `role: "Drafter"`, a positive integer `pid`, and a valid RFC3339 `started_at` timestamp

---

#### Scenario: No process_started event emitted when subprocess fails to launch

**Traces to**: User Story 1, Acceptance Scenario 3
**Category**: Error Path

- **Given** a ClaudeRunner configured with a binary path that does not exist
- **When** `Run` is called and `cmd.Start()` returns an error
- **Then** no `process_started` WebSocket event is broadcast
- **And** no process record is written to the store

---

#### Scenario: process_ended event emitted on normal subprocess exit

**Traces to**: User Story 2, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a subprocess has started and emitted a `process_started` event with pid 12345
- **When** the subprocess exits with code 0
- **Then** a `process_ended` WebSocket event is broadcast
- **And** the event contains `pid: 12345`, `exit_code: 0`, `status: "exited"`, and a valid RFC3339 `ended_at` timestamp

---

#### Scenario: process_ended event emitted on non-zero subprocess exit

**Traces to**: User Story 2, Acceptance Scenario 2
**Category**: Alternate Path

- **Given** a subprocess has started and emitted a `process_started` event with pid 12346
- **When** the subprocess exits with code 1
- **Then** a `process_ended` WebSocket event is broadcast
- **And** the event contains `pid: 12346`, `exit_code: 1`, and `status: "exited"`

---

#### Scenario: process_ended event carries status "killed" after user-initiated termination

**Traces to**: User Story 2, Acceptance Scenario 3
**Category**: Alternate Path

- **Given** a subprocess with pid 12347 is running and the kill endpoint was called for it
- **When** the subprocess terminates in response to the signal
- **Then** the `process_ended` WebSocket event contains `pid: 12347` and `status: "killed"`

---

#### Scenario: Process record survives server restart

**Traces to**: User Story 3, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a `process_started` event was emitted for pid 12348 and the record was written to the store
- **And** the process subsequently exited normally
- **When** the server is restarted
- **Then** `GET /api/processes` returns a record with `pid: 12348` and `status: "exited"`

---

#### Scenario: Running process marked lost on server startup

**Traces to**: User Story 3, Acceptance Scenario 2
**Category**: Happy Path

- **Given** a process record with `pid: 12349` and `status: "running"` exists in the store
- **When** the server starts up
- **Then** the record's status is updated to `"lost"` in the store
- **And** a `process_lost` WebSocket event is broadcast containing `pid: 12349`, `feature: <original feature>`, and `role: <original role>`

---

#### Scenario: Terminated process records not modified on server startup

**Traces to**: User Story 3, Acceptance Scenario 3
**Category**: Alternate Path

- **Given** process records with status `"exited"` and `"killed"` exist in the store
- **When** the server starts up
- **Then** those records retain their original status values unchanged

---

#### Scenario: Running Agents tab shows active processes

**Traces to**: User Story 4, Acceptance Scenario 1
**Category**: Happy Path

- **Given** at least one agent subprocess is active with feature "auth-spec", role "Reviewer", pid 12350
- **When** the user clicks "Running Agents" in the sidebar
- **Then** a table is displayed with columns: Feature, Role, PID, Start Time, Status, Action
- **And** a row shows "auth-spec", "Reviewer", "12350", a formatted start time, "running", and a Kill button

---

#### Scenario: New process row added to table in real time

**Traces to**: User Story 4, Acceptance Scenario 2
**Category**: Happy Path

- **Given** the Running Agents tab is open and showing zero running rows
- **When** a `process_started` WebSocket event arrives for pid 12351
- **Then** a new row for pid 12351 is appended to the table without a page reload

---

#### Scenario: Table row status updated when process ends

**Traces to**: User Story 4, Acceptance Scenario 3
**Category**: Happy Path

- **Given** the Running Agents tab is open and shows a row for pid 12352 with status "running"
- **When** a `process_ended` WebSocket event arrives for pid 12352 with status "exited"
- **Then** the row's Status cell is updated to "exited"
- **And** the Kill button in that row is removed

---

#### Scenario: Empty state shown when no processes recorded

**Traces to**: User Story 4, Acceptance Scenario 4
**Category**: Edge Case

- **Given** the process store contains no records
- **When** the user opens the Running Agents tab
- **Then** no table rows are displayed
- **And** the message "No agent processes recorded." is shown

---

#### Scenario: User successfully kills a running process via SIGTERM

**Traces to**: User Story 5, Acceptance Scenario 1
**Category**: Happy Path

- **Given** the Running Agents tab shows a row for pid 12353 with status "running" and a Kill button
- **When** the user clicks Kill and confirms the prompt
- **Then** a `POST /api/processes/12353/kill` request is sent
- **And** the Kill button label changes to "Killing…" and the button is disabled
- **And** when the `process_ended` event arrives, the row status updates to "killed"

---

#### Scenario: SIGKILL escalation fires when process ignores SIGTERM

**Traces to**: User Story 5, Acceptance Scenario 2
**Category**: Alternate Path

- **Given** a subprocess with pid 12354 is running and `POST /api/processes/12354/kill` is called
- **And** the subprocess is ignoring SIGTERM
- **When** 10 seconds elapse without the process exiting
- **Then** the server sends SIGKILL to pid 12354
- **And** a `process_ended` event with `status: "killed"` is subsequently broadcast

---

#### Scenario: Kill request for PID not in process store returns 404

**Traces to**: User Story 5, Acceptance Scenario 3 (safety boundary)
**Category**: Error Path

- **Given** no process record exists in the store for pid 99999
- **When** `POST /api/processes/99999/kill` is called
- **Then** the server returns `404 Not Found` with `{"error": "process 99999 not found"}`
- **And** no signal is sent

---

#### Scenario: Kill request for already-terminated process returns 409

**Traces to**: User Story 5, Acceptance Scenario 3
**Category**: Error Path

- **Given** a process with pid 12355 has already exited and its record has `status: "exited"`
- **When** `POST /api/processes/12355/kill` is called
- **Then** the server returns `409 Conflict`
- **And** no signal is sent
- **And** the UI displays a brief "Process already ended" notice

---

#### Scenario: User cancels kill confirmation prompt

**Traces to**: User Story 5, Acceptance Scenario 4
**Category**: Alternate Path

- **Given** the user clicks Kill on a row for pid 12356
- **And** a confirmation prompt is displayed
- **When** the user clicks Cancel
- **Then** no HTTP request is sent to the kill endpoint
- **And** the row remains unchanged with status "running"

---

#### Scenario: Kill rejected when process is not owned by the server

**Traces to**: User Story 5, Acceptance Scenario 5
**Category**: Error Path

- **Given** a process record with pid 12357 exists but the OS rejects the signal with EPERM
- **When** `POST /api/processes/12357/kill` is called
- **Then** the server returns `403 Forbidden` with `{"error": "permission denied: cannot signal process 12357"}`
- **And** the UI displays the error message

---

#### Scenario Outline: Kill endpoint rejects invalid PID values

**Traces to**: User Story 5, Acceptance Scenario 3 (safety boundary)
**Category**: Edge Case

- **Given** the kill endpoint receives a PID value of `<pid>`
- **When** `POST /api/processes/<pid>/kill` is called
- **Then** the server returns `<status>` with a human-readable error

**Examples**:

| pid  | status            | notes                          |
|------|-------------------|--------------------------------|
| 0    | 400 Bad Request   | Would signal entire process group |
| -1   | 400 Bad Request   | Would signal all owned processes  |
| -999 | 400 Bad Request   | Negative PID                   |
| abc  | 400 Bad Request   | Non-numeric path param         |

---

#### Scenario: SIGKILL escalation cancelled when process exits before timeout

**Traces to**: User Story 5, Acceptance Scenario 2
**Category**: Edge Case

- **Given** SIGTERM has been sent to pid 12358 and a 10-second escalation timer is running
- **When** the process exits at second 4
- **Then** the escalation timer is cancelled
- **And** no SIGKILL is sent
- **And** a `process_ended` event with `status: "killed"` is broadcast

---

#### Scenario: Simultaneous kill requests for the same PID

**Traces to**: User Story 5, Acceptance Scenario 3
**Category**: Edge Case

- **Given** two clients simultaneously send `POST /api/processes/12359/kill`
- **When** both requests are processed
- **Then** exactly one SIGTERM is sent to pid 12359
- **And** the first request receives `200 OK`
- **And** the second request receives `409 Conflict`

---

#### Scenario: PID reuse safety check prevents killing unrelated process

**Traces to**: User Story 5, Acceptance Scenario 3
**Category**: Edge Case

- **Given** a tracked process with pid 12360 has exited and the OS has reassigned pid 12360 to an unrelated process
- **When** `POST /api/processes/12360/kill` is called
- **Then** the server detects that pid 12360 is no longer a tracked child and returns `409 Conflict`
- **And** no signal is sent to the unrelated process

---

## Test-Driven Development Plan

### Test Hierarchy

| Level       | Scope                                     | Purpose                                                  |
|-------------|-------------------------------------------|----------------------------------------------------------|
| Unit        | ProcessTracker, ProcessStore, KillHandler | Validates logic in isolation with mocks for OS/WS/DB     |
| Integration | Runner + Tracker, API handlers + Store    | Validates components interact correctly with real SQLite  |
| E2E         | Full workflow → UI table → kill flow      | Validates complete feature from browser perspective      |

### Test Implementation Order

| Order | Test Name | Level | Traces to BDD Scenario | Description |
|-------|-----------|-------|------------------------|-------------|
| 1 | `TestProcessStore_SaveAndLoad` | Unit | Process record survives server restart | Save a record, re-open DB, verify retrieval |
| 2 | `TestProcessStore_MarkLostOnStartup` | Unit | Running process marked lost on server startup | Records with status=running are updated to lost on Open |
| 3 | `TestProcessStore_TerminatedRecordsUnchanged` | Unit | Terminated process records not modified on startup | Exited/killed records unchanged on Open |
| 4 | `TestProcessTracker_EmitsStartedEvent` | Unit | ClaudeRunner emits process_started event | Tracker.RecordStart broadcasts correct event payload |
| 5 | `TestProcessTracker_EmitsEndedEvent` | Unit | process_ended event emitted on normal subprocess exit | Tracker.RecordEnd broadcasts correct event payload |
| 6 | `TestProcessTracker_EmitsKilledStatus` | Unit | process_ended event carries status "killed" | RecordEnd with killed=true sets status:"killed" |
| 7 | `TestKillService_SendsSIGTERM` | Unit | User successfully kills a running process via SIGTERM | KillService calls SignalSender with SIGTERM |
| 8 | `TestKillService_EscalatesToSIGKILL` | Unit | SIGKILL escalation fires when process ignores SIGTERM | After 10s timeout, KillService calls SignalSender with SIGKILL |
| 9 | `TestKillService_CancelsEscalation` | Unit | SIGKILL escalation cancelled when process exits before timeout | Process exit before timeout cancels the escalation timer |
| 10 | `TestKillService_AlreadyTerminated` | Unit | Kill request for already-terminated process returns 409 | Returns ErrAlreadyTerminated for non-running status |
| 11 | `TestKillService_InvalidPID` | Unit | Kill endpoint rejects invalid PID values | PID ≤ 0 returns ErrInvalidPID without calling SignalSender |
| 12 | `TestKillService_EPERM` | Unit | Kill rejected when process is not owned by server | EPERM from SignalSender surfaces as ErrPermissionDenied |
| 13 | `TestKillService_SimultaneousRequests` | Unit | Simultaneous kill requests for the same PID | Second concurrent call returns ErrAlreadyTerminated |
| 14 | `TestProcessHandler_List` | Unit | Running Agents tab shows active processes | GET /api/processes returns correct JSON array |
| 14a | `TestProcessHandler_List_InvalidStatusParam` | Unit | Kill endpoint rejects invalid PID values | GET with ?status=pending returns 400 Bad Request |
| 15 | `TestProcessHandler_Kill_OK` | Unit | User successfully kills a running process | POST /api/processes/{pid}/kill returns 200 with {"status":"kill_accepted"} |
| 16 | `TestProcessHandler_Kill_404` | Unit | Kill request for PID not in process store returns 404 | POST returns 404 with error body for unknown PID |
| 16a | `TestKillService_UnknownPID` | Unit | Kill request for PID not in process store returns 404 | KillService returns ErrNotFound for PID absent from store |
| 17 | `TestProcessHandler_Kill_409` | Unit | Kill request for already-terminated process | POST returns 409 with error body |
| 18 | `TestProcessHandler_Kill_403` | Unit | Kill rejected when process not owned | POST returns 403 with error body |
| 19 | `TestProcessHandler_Kill_InvalidPID` | Unit | Kill endpoint rejects invalid PID values | POST with pid≤0 or non-numeric returns 400 |
| 19a | `TestProcessTracker_EmitsEndedEvent_WithoutFeatureRole` | Unit | process_ended event emitted on normal subprocess exit | Verify UI can correlate process_ended by PID alone when feature/role absent |
| 19b | `TestKillService_KillFlagRace` | Unit | process_ended event carries status "killed" | Process exits naturally at same instant kill is requested — killed flag wins |
| 20 | `TestClaudeRunner_EmitsProcessEvents` | Integration | ClaudeRunner emits process_started event | Real ClaudeRunner + ProcessTracker; verify events via channel |
| 21 | `TestCodexRunner_EmitsProcessEvents` | Integration | CodexRunner emits process_started event | Real CodexRunner + ProcessTracker; verify events via channel |
| 22 | `TestNoEventOnStartFailure` | Integration | No process_started event emitted when subprocess fails | Bad binary path; verify no event on WS |
| 23 | `TestProcessStore_Integration` | Integration | Process record survives server restart | Write record, close DB, reopen, query |
| 24 | `TestKillFlow_Integration` | Integration | Full SIGTERM→exit flow | Real subprocess, kill endpoint, verify dead |
| 25 | `TestKillFlow_EscalationIntegration` | Integration | SIGKILL escalation fires | Real subprocess ignoring SIGTERM, verify SIGKILL fires |
| 26 | `TestStartupRecovery_Integration` | Integration | Running process marked lost on startup | Write running record, call startup recovery, verify lost; verify startup refuses if recovery UPDATE fails |
| 27 | `TestRunningAgentsTab_ShowsProcesses` | E2E | Running Agents tab shows active processes | Playwright: start workflow, open tab, verify table row |
| 28 | `TestRunningAgentsTab_RealTimeUpdate` | E2E | New process row added to table in real time | Playwright: open tab, trigger agent, verify row appears |
| 29 | `TestRunningAgentsTab_KillFlow` | E2E | User successfully kills a running process via SIGTERM | Playwright: click Kill, confirm, verify status → killed |
| 30 | `TestRunningAgentsTab_EmptyState` | E2E | Empty state shown when no processes recorded | Playwright: open tab with empty store, verify message |

---

### Test Datasets

#### Dataset 1: Kill Endpoint PID Input Validation

| # | Input PID | Boundary Type | Expected HTTP Status | Traces to | Notes |
|---|-----------|---------------|----------------------|-----------|-------|
| 1 | 1 | Min valid (init process) | 403 Forbidden | Kill endpoint rejects invalid PID values | Not a child process |
| 2 | 12345 | Happy path | 200 OK | User successfully kills running process | Valid tracked PID |
| 3 | 0 | Zero | 400 Bad Request | Kill endpoint rejects invalid PID values | Would signal process group |
| 4 | -1 | Negative | 400 Bad Request | Kill endpoint rejects invalid PID values | Would signal all owned processes |
| 5 | -999 | Large negative | 400 Bad Request | Kill endpoint rejects invalid PID values | Same reason as -1 |
| 6 | 4194304 | Platform max (Linux) | 404 Not Found | Kill endpoint rejects invalid PID values | Valid range but not tracked |
| 7 | 4194305 | Above platform max | 400 Bad Request | Kill endpoint rejects invalid PID values | Exceeds OS limit |
| 8 | "abc" | Non-numeric | 400 Bad Request | Kill endpoint rejects invalid PID values | Path param parse failure |
| 9 | "" | Empty | 400 Bad Request | Kill endpoint rejects invalid PID values | Missing param |
| 10 | 99999 | Valid numeric, not in store | 404 Not Found | Kill request for PID not in process store returns 404 | Traces to FR-012 |
| 11 | n/a (?status=pending) | Invalid status query param | 400 Bad Request | Kill endpoint rejects invalid PID values | GET /api/processes?status=pending; traces to FR-006 |

#### Dataset 2: Process Record Status Transitions

| # | Initial Status | Trigger | Expected New Status | Traces to | Notes |
|---|----------------|---------|---------------------|-----------|-------|
| 1 | (none) | cmd.Start() succeeds | running | ClaudeRunner emits process_started event | Record created on start |
| 2 | running | Process exits code 0 | exited | process_ended event emitted on normal exit | Normal termination |
| 3 | running | Process exits code 1 | exited | process_ended event on non-zero exit | Error termination |
| 4 | running | Kill endpoint called | killed | process_ended event carries status killed | User-initiated |
| 5 | running | Server restart | lost | Running process marked lost on startup | Unrecoverable state |
| 6 | exited | Server restart | exited | Terminated process records not modified | No change |
| 7 | killed | Server restart | killed | Terminated process records not modified | No change |
| 8 | lost | Server restart | lost | Terminated process records not modified | No change |
| 9 | exited | Kill endpoint called | 409 Conflict | Kill request for already-terminated process | No signal sent |
| 10 | killed | Kill endpoint called | 409 Conflict | Kill request for already-terminated process | No signal sent |
| 11 | lost | Kill endpoint called | 409 Conflict | Kill request for already-terminated process | No signal sent; lost is terminal |

#### Dataset 3: WebSocket Event Payload Validation

| # | Event Type | Required Fields | Optional Fields | Traces to | Notes |
|---|------------|-----------------|-----------------|-----------|-------|
| 1 | process_started | type, feature, role, pid (int >0), started_at (RFC3339) | — | ClaudeRunner emits process_started event | All required |
| 2 | process_ended | type, pid (int >0), exit_code (int), status ("exited"\|"killed"), ended_at (RFC3339) | feature, role | process_ended event emitted on normal exit | feature/role are optional: the UI can correlate by PID alone from its local table state (see TestProcessTracker_EmitsEndedEvent_WithoutFeatureRole) |
| 3 | process_lost | type, pid (int >0), feature, role | — | Running process marked lost on startup | All four fields required; BDD scenario asserts feature and role in payload |

#### Dataset 4: SIGTERM Escalation Timing

| # | SIGTERM sent at | Process exits at | Escalation threshold | SIGKILL sent? | Traces to | Notes |
|---|-----------------|------------------|----------------------|---------------|-----------|-------|
| 1 | t=0 | t=2s | 10s | No | SIGKILL escalation cancelled | Process exits before timeout |
| 2 | t=0 | t=9s | 10s | No | SIGKILL escalation cancelled | Exit safely before threshold; t=10s is untestable (timer/exit race — OS-scheduler-dependent; implementation MUST use select with cancel channel) |
| 3 | t=0 | never | 10s | Yes at t=10s | SIGKILL escalation fires | Process ignores SIGTERM |
| 4 | t=0 | t=15s (after SIGKILL) | 10s | Yes at t=10s | SIGKILL escalation fires | SIGKILL eventually kills it |

---

### Regression Test Requirements

This feature modifies ClaudeRunner and CodexRunner by adding process event emission calls. Existing runner tests must continue to pass.

| Existing Behaviour | Existing Test | New Regression Test Needed | Notes |
|-------------------|---------------|---------------------------|-------|
| ClaudeRunner.Run returns parsed output on success | `TestClaudeRunner_Run_*` | No — verify existing tests still pass | ProcessTracker injection must not alter return values |
| ClaudeRunner.Run returns error on start failure | `TestClaudeRunner_StartFailure` | No | Event emission path must be bypassed on error |
| CodexRunner.Run executes subprocess and returns | `TestCodexRunner_*` | No — verify existing tests still pass | Same tracker injection concern |
| DefaultClaudeRunner sets OTEL env vars | `TestDefaultClaudeRunner_WithOTELPort` | No | ProcessTracker is additional dep, not replacing anything |

Integration seams protected by existing tests: `TestDefaultClaudeRunner`, `TestBuildCommand_BasicArgs`, `TestParseOutput_Success`.

---

## Functional Requirements

- **FR-001**: System MUST emit a `process_started` WebSocket event immediately after any AgentRunner's subprocess starts, containing `feature`, `role`, `pid`, and `started_at` fields.
- **FR-002**: System MUST emit a `process_ended` WebSocket event when any AgentRunner's subprocess terminates, containing `pid`, `exit_code`, `status` ("exited" or "killed"), and `ended_at` fields.
- **FR-003**: System MUST emit a `process_lost` WebSocket event at startup for each process record with `status: "running"` found in the store.
- **FR-004**: System MUST persist every process start and end event to a SQLite store, including feature, role, pid, status, started_at, ended_at, and exit_code. If the SQLite write for a new process record fails after bounded retries (maximum 3, with exponential backoff capped at 500ms), the `process_started` event MUST NOT be emitted and the runtime map entry MUST NOT be added. The process is tracked as "unmanaged" — it runs but cannot be killed via the endpoint.
- **FR-005**: System MUST update any `status: "running"` process records to `status: "lost"` during server startup before accepting HTTP or WebSocket connections. If the recovery UPDATE fails (e.g., disk full, SQLite locked), the server MUST refuse to start and exit with a fatal error — proceeding with potentially incorrect kill guards is not acceptable.
- **FR-006**: System MUST expose `GET /api/processes` returning a JSON array of all process records, ordered by started_at descending. The endpoint MUST support optional query parameters `?feature=<name>` and `?status=<running|exited|killed|lost>` to filter results server-side; parameters may be combined. An unrecognised `?status=` value (anything other than `running`, `exited`, `killed`, or `lost`) MUST return `400 Bad Request` with `{"error": "invalid status filter: <value>"}`. For MVP, if the process record count exceeds 500, the endpoint MUST return the 500 most recent records by `started_at` descending.
- **FR-007**: System MUST expose `POST /api/processes/{pid}/kill` to request termination of a tracked running process. On success, the response MUST be `200 OK` with body `{"status": "kill_accepted"}`. This response indicates SIGTERM was sent — NOT that the process has exited. Process death is communicated asynchronously via the `process_ended` WebSocket event.
- **FR-008**: System MUST send SIGTERM as the first signal when a kill is requested.
- **FR-009**: System MUST escalate to SIGKILL if the target process has not exited within the configured kill escalation timeout (default: 10 seconds). On graceful server shutdown (SIGTERM received by the server), all pending SIGKILL escalation timers MUST be cancelled and SIGKILL MUST be sent immediately to all processes that are in the "SIGTERM sent, awaiting exit" state.
- **FR-010**: System MUST cancel the SIGKILL escalation timer if the process exits before the timeout fires.
- **FR-011**: System MUST return `400 Bad Request` for kill requests with PID ≤ 0 or non-numeric PID without sending any signal.
- **FR-012**: System MUST return `404 Not Found` for kill requests targeting a PID not present in the process store.
- **FR-013**: System MUST return `409 Conflict` for kill requests targeting a process whose stored status is not "running".
- **FR-014**: System MUST return `403 Forbidden` when the OS rejects the signal with EPERM.
- **FR-015**: System MUST NOT send any signal to a PID unless it is present in both the process store (with `status: "running"`) AND the in-memory runtime map maintained by ProcessTracker for this server instance. Any PID that fails either check returns `409 Conflict`. This prevents signalling PIDs started before a restart (stored as "lost", absent from runtime map) and PIDs reused by the OS after a tracked process exited (removed from runtime map on exit, already marked "exited" in store).
- **FR-016**: System MUST include a "Running Agents" tab in the sidebar, positioned between the Controls tab and the Spec tab.
- **FR-017**: The Running Agents tab MUST display a table with columns: Feature, Role, PID, Start Time, Status, Action. Rows with `status: "running"` MUST be pinned to the top of the table. Within each status group, rows MUST be ordered by started_at descending (newest first). The PID column MUST render the PID as an integer (right-aligned, monospace font) — not as a quoted string — to support copy-paste into `ps -p`.
- **FR-018**: The Running Agents tab MUST populate its initial state from `GET /api/processes` on every tab open or tab activation (including after navigating away and back). WebSocket events then provide live updates to already-rendered rows. The tab MUST NOT rely solely on WebSocket events for initial state, because startup `process_lost` events are emitted before any client can connect and are therefore silently dropped. When a `process_started` event arrives, a new row MUST be added in real time without requiring a page reload.
- **FR-019**: The Running Agents tab MUST update existing rows in real time when `process_ended` or `process_lost` events arrive.
- **FR-020**: The Running Agents tab MUST display a Kill button only on rows with `status: "running"`.
- **FR-021**: The Kill button MUST display a confirmation prompt before sending the kill request.
- **FR-022**: The Kill button MUST be disabled with label "Killing…" after the kill request is sent and before the process end event arrives. If the WebSocket connection drops while the button is in "Killing…" state, the tab MUST re-fetch `GET /api/processes` on reconnect. If the re-fetched record shows `status: "killed"`, `"exited"`, or `"lost"`, the row MUST be updated accordingly. If the record still shows `status: "running"`, the Kill button MUST revert to its active enabled state.
- **FR-023**: The Running Agents tab MUST display an empty-state message when no process records exist.
- **FR-024**: System SHOULD make the kill escalation timeout configurable via the existing YAML config file (default: 10 seconds).
- **FR-025**: The kill endpoint (`POST /api/processes/{pid}/kill`) is intentionally rate-limit-free for internal tooling. The spec MUST document this explicitly. Rate limiting and per-user authentication MUST be added before any public or multi-tenant exposure of this endpoint.
- **FR-026**: New types `ProcessTracker`, `ProcessStore`, `KillService`, and `SignalSender` MUST be placed in a new `internal/process` package to avoid circular imports between the runner package and the API handler package. Both `internal/specworkflow` (runners) and `internal/api` (handlers) import `internal/process` — the process package imports neither. New HTTP handler types for `GET /api/processes` and `POST /api/processes/{pid}/kill` are registered directly in `cmd/specworkflow/main.go` on the existing router, following the same pattern as other handler registrations.

---

## Success Criteria

- **SC-001**: A `process_started` WebSocket event is received within 100 ms of `cmd.Start()` returning success, measured in integration tests with a real subprocess.
- **SC-002**: `POST /api/processes/{pid}/kill` with a running PID results in the process being dead (not present in `ps`) within 15 seconds under normal conditions and within 1 second when SIGKILL escalation fires.
- **SC-003**: The Running Agents table reflects a new process within one WebSocket message cycle of the `process_started` event arriving.
- **SC-004**: `GET /api/processes?feature=X` returns all process records for feature X, including records created before a server restart.
- **SC-005**: Startup recovery MUST complete (all `status: "running"` records updated to `status: "lost"`) before the HTTP listener is opened. This is verified structurally in `TestStartupRecovery_Integration` by confirming the recovery function returns before server start is called — no wall-clock SLA is imposed, because recovery time is proportional to stale record count and is not bounded in the MVP (fewer than 50 records assumed).
- **SC-006**: All existing ClaudeRunner and CodexRunner tests pass without modification after ProcessTracker injection is added.
- **SC-007**: Kill endpoint returns `400 Bad Request` for all PID ≤ 0 inputs and non-numeric inputs without any signal being sent (verified by unit tests with mock SignalSender that asserts zero calls).

---

## Traceability Matrix

| Requirement | User Story | BDD Scenario(s) | Test Name(s) |
|-------------|-----------|-----------------|--------------|
| FR-001 | US-1 | ClaudeRunner emits process_started event; CodexRunner emits process_started event | TestProcessTracker_EmitsStartedEvent; TestClaudeRunner_EmitsProcessEvents; TestCodexRunner_EmitsProcessEvents |
| FR-002 | US-2 | process_ended event emitted on normal exit; process_ended on non-zero exit; process_ended carries status "killed" | TestProcessTracker_EmitsEndedEvent; TestProcessTracker_EmitsKilledStatus |
| FR-003 | US-3 | Running process marked lost on startup | TestProcessStore_MarkLostOnStartup; TestStartupRecovery_Integration |
| FR-004 | US-3 | Process record survives server restart | TestProcessStore_SaveAndLoad; TestProcessStore_Integration |
| FR-005 | US-3 | Running process marked lost; Terminated records not modified | TestProcessStore_MarkLostOnStartup; TestProcessStore_TerminatedRecordsUnchanged |
| FR-006 | US-4 | Running Agents tab shows active processes | TestProcessHandler_List |
| FR-007 | US-5 | User successfully kills a running process | TestProcessHandler_Kill_OK |
| FR-008 | US-5 | User successfully kills a running process | TestKillService_SendsSIGTERM; TestKillFlow_Integration |
| FR-009 | US-5 | SIGKILL escalation fires when process ignores SIGTERM | TestKillService_EscalatesToSIGKILL; TestKillFlow_EscalationIntegration |
| FR-010 | US-5 | SIGKILL escalation cancelled when process exits before timeout | TestKillService_CancelsEscalation |
| FR-011 | US-5 | Kill endpoint rejects invalid PID values | TestKillService_InvalidPID; TestProcessHandler_Kill_InvalidPID |
| FR-012 | US-5 | Kill request for PID not in process store returns 404 | TestProcessHandler_Kill_404; TestKillService_UnknownPID |
| FR-013 | US-5 | Kill request for already-terminated process returns 409 | TestKillService_AlreadyTerminated |
| FR-014 | US-5 | Kill rejected when process not owned | TestKillService_EPERM; TestProcessHandler_Kill_403 |
| FR-015 | US-5 | PID reuse safety check prevents killing unrelated process | TestKillService_AlreadyTerminated (status=exited covers this) |
| FR-016 | US-4 | Running Agents tab shows active processes | TestRunningAgentsTab_ShowsProcesses |
| FR-017 | US-4 | Running Agents tab shows active processes | TestRunningAgentsTab_ShowsProcesses |
| FR-018 | US-4 | New process row added to table in real time | TestRunningAgentsTab_RealTimeUpdate |
| FR-019 | US-4 | Table row status updated when process ends; Running Agents tab initial state from GET /api/processes | TestRunningAgentsTab_RealTimeUpdate; TestRunningAgentsTab_ShowsProcesses |
| FR-020 | US-4, US-5 | User successfully kills; Table row updated when process ends | TestRunningAgentsTab_KillFlow |
| FR-021 | US-5 | User cancels kill confirmation prompt | TestRunningAgentsTab_KillFlow |
| FR-022 | US-5 | User successfully kills a running process | TestRunningAgentsTab_KillFlow |
| FR-023 | US-4 | Empty state shown when no processes recorded | TestRunningAgentsTab_EmptyState |
| FR-024 | US-5 | SIGKILL escalation fires | TestKillService_EscalatesToSIGKILL |
| FR-025 | US-5 | (No BDD scenario — documented constraint, not testable behavior) | (No test — documented in spec as architectural constraint) |
| FR-026 | US-1, US-2, US-3, US-5 | (Package structure — all runner and handler tests verify indirectly) | TestClaudeRunner_EmitsProcessEvents; TestCodexRunner_EmitsProcessEvents; TestProcessHandler_Kill_OK |

---

## Ambiguity Warnings

All known ambiguities have been resolved. The following items were resolved through the review cycle and are documented here for traceability:

| Item | Resolution |
|------|-----------|
| SQLite write failure vs. kill safety guarantee | If write fails after retries, process is "unmanaged" — event suppressed, runtime map entry not added (FR-004) |
| `Feature` / `Role` fields on runners | Explicit struct fields on `ClaudeRunner` and `CodexRunner`; all clone methods propagate via shallow copy (intentional) — see Assumptions |
| `status: "killed"` tracking mechanism | Kill-request flag set in runtime map under mutex before sending SIGTERM; process-end goroutine checks flag — see Assumptions |
| Simultaneous kill locking | Per-PID atomic CAS on runtime map for running→kill-requested transition — see Assumptions |
| Kill endpoint auth | Internal tool, no auth required; must add before public exposure (FR-025) |
| Initial tab state vs. WebSocket | Tab fetches GET /api/processes on every open/activation; WS provides live updates only (FR-018) |
| ProcessTracker package placement | New `internal/process` package (FR-026) |
| Graceful shutdown with pending escalation | Cancel timers; send SIGKILL immediately (FR-009) |
| Kill endpoint response body semantics | `{"status": "kill_accepted"}` — means SIGTERM sent, not process dead (FR-007) |
| Startup recovery failure | Server refuses to start with fatal error (FR-005) |

---

## Evaluation Scenarios (Holdout)

> **Note**: These scenarios are for post-implementation evaluation only.
> They must NOT be visible to the implementing agent during development.
> Do not reference these in the TDD plan or traceability matrix.

### Scenario: Live process appears in Running Agents tab within one second

- **Setup**: Server running, no prior processes. WebSocket connected.
- **Action**: Start a workflow. Navigate to Running Agents tab immediately.
- **Expected outcome**: Within 1 second of the agent process launching, a row appears in the table with the correct feature name, role, and a positive integer PID. Status is "running".
- **Category**: Happy Path

### Scenario: Kill button terminates agent and row updates without refresh

- **Setup**: A long-running agent (e.g., sleep 60 subprocess) is running and visible in the Running Agents tab.
- **Action**: Click Kill on the agent's row. Confirm the prompt.
- **Expected outcome**: The Kill button becomes disabled. Within 15 seconds, the row status changes to "killed" without reloading the page. Running `ps -p <PID>` on the server returns no results.
- **Category**: Happy Path

### Scenario: Process records visible after server restart

- **Setup**: Start a workflow, let one agent run to completion, then stop the server and restart it.
- **Action**: Open the Running Agents tab immediately after restart.
- **Expected outcome**: The previously completed agent appears in the table with status "exited" and correct start/end times. No data is missing.
- **Category**: Happy Path

### Scenario: Process running at shutdown appears as "lost" after restart

- **Setup**: Start a long-running agent. Without waiting for it to finish, stop the server.
- **Action**: Restart the server and open the Running Agents tab.
- **Expected outcome**: The agent that was running at shutdown appears in the table with status "lost". No Kill button is shown for it.
- **Category**: Error

### Scenario: Kill button absent for exited and lost processes

- **Setup**: Server running with a mix of "running", "exited", and "lost" records in the table.
- **Action**: Inspect all rows in the Running Agents table.
- **Expected outcome**: Kill buttons are present only on rows with status "running". Rows with status "exited", "killed", or "lost" have no Kill button or action control.
- **Category**: Edge Case

### Scenario: SIGKILL fires when agent ignores SIGTERM

- **Setup**: A subprocess that explicitly ignores SIGTERM (traps the signal) is visible in the Running Agents tab.
- **Action**: Click Kill on its row and confirm.
- **Expected outcome**: After approximately 10 seconds, the process is forcibly terminated. The row status changes to "killed". The process is not visible in `ps` output on the server.
- **Category**: Error

### Scenario: Cancel kill does not terminate process

- **Setup**: A running agent is visible in the Running Agents tab.
- **Action**: Click Kill on its row. When the confirmation prompt appears, click Cancel.
- **Expected outcome**: The row remains unchanged with status "running". The agent continues executing. No "Killing…" state appears.
- **Category**: Edge Case

---

## Assumptions

- The server process has permission to send signals to all subprocesses it spawns (they share the same OS user).
- The existing `metrics.db` SQLite file is the correct location for the new `agent_processes` table; no separate database file is needed.
- The kill escalation timeout of 10 seconds is a reasonable default for the agent use case; it will be made configurable but not tunable per-agent-role.
- "Confirmed as child process" (FR-015) is satisfied by the constraint that kill is only permitted for PIDs present in the process store — PIDs are only added to the store by this server, so a PID in the store was started by this server.
- The platform PID maximum is 2^22 (4,194,304) for Linux; on macOS it is 99,998. The implementation should use the platform's actual limit rather than a hardcoded constant.
- The Running Agents tab does not need pagination for MVP; workflows typically run fewer than 50 agent processes in total.
- ProcessTracker is injected as a constructor parameter into ClaudeRunner and CodexRunner (e.g. `DefaultClaudeRunner(..., tracker ProcessTracker)`). A wrapper satisfying AgentRunner is not used because feature and role context required for event emission is already available inside the runner, not at the call site.

### Runner Struct Changes Required

`ClaudeRunner` and `CodexRunner` structs MUST each gain three new fields:

```go
Feature string        // workflow feature name, set at construction time
Role    string        // agent role, set at construction time
Tracker ProcessTracker // injected dependency; nil-safe (no-op if nil)
```

`ClaudeRunner` has six copy-constructor methods (`CloneForAgent`, `WithModel`, `WithJSONSchema`, `WithContext`, `ForJSONOnly`, and a second `WithContext` variant), each of which does a shallow `clone := *r`. All three new fields are automatically propagated by this shallow copy — this is intentional and correct. No additional changes to these methods are needed. `CloneForAgent` returns `AgentRunner` (not `*ClaudeRunner`), so the tracker propagates correctly through the returned interface value.

### Kill-Request Flag and Mutex

When the kill endpoint marks a PID as "kill-requested":
1. The ProcessTracker runtime map is protected by a `sync.Mutex`.
2. The state transition from `running` to `kill-requested` MUST be performed atomically under this mutex: read status → check running → set kill-requested flag → return error if already not running.
3. The process-end goroutine MUST check the kill-requested flag (under the same mutex) when `cmd.Wait()` returns: if flag is set, status is `"killed"`; otherwise status is `"exited"`. The flag takes priority over OS signal inspection.
4. If a process exits naturally at the exact same instant a kill is requested (race), the kill-requested flag wins — the exit is recorded as `status: "killed"`. This is acceptable and simplifies the implementation.
5. This invariant ensures only one SIGTERM is sent per PID (simultaneous kill requests: the second finds the flag already set and returns `409 Conflict`).
- The in-memory runtime map (`pid → *os.Process`) is owned by ProcessTracker. An entry is added after `cmd.Start()` succeeds and removed when the process-end goroutine observes termination. Kill requires presence in this map; this prevents both PID reuse attacks and killing "lost" processes after restart without any platform-specific OS verification.
- `started_at` is recorded as `time.Now()` immediately after `cmd.Start()` returns — within microseconds of actual process creation. `ended_at` is `time.Now()` immediately after `cmd.Wait()` returns. True OS process creation time requires platform-specific code (`/proc/{pid}/stat` on Linux, `libproc` on macOS) that is not available in Go stdlib; Go observation time is the correct pragmatic choice and is documented here as a known approximation.
- `GET /api/processes` supports `?feature=X` and `?status=Y` server-side query params (resolved: server-side filtering preferred over client-side to keep payload bounded for long-running servers).

---

## Clarifications

### 2026-04-05

- Q: OTEL SDK or WebSocket event attachment? -> A: No new OTEL SDK. PID is attached as a structured field to the existing WebSocket event stream.
- Q: Which runners are in scope? -> A: All AgentRunner implementations (ClaudeRunner, CodexRunner, and any future runners).
- Q: Should kill be read-only or include signal capability? -> A: Kill via SIGTERM with SIGKILL escalation after 10 seconds.
- Q: Correlation key? -> A: workflow.feature + agent.role + PID is sufficient.
- Q: Persist across restarts? -> A: Yes, using SQLite.
- Q: UI placement? -> A: New "Running Agents" sidebar tab between Controls and Spec.
- Q: Escalation path? -> A: SIGTERM first; SIGKILL after configured timeout (default 10s).
- Q: How is ProcessTracker injected? -> A: Constructor parameter in ClaudeRunner and CodexRunner — not a wrapper pattern. Architecturally correct because feature/role context is already available inside the runner.
- Q: Should GET /api/processes support server-side filtering? -> A: Yes — `?feature=X` and `?status=Y` query params added to FR-006.
- Q: How is PID reuse / child verification handled? -> A: ProcessTracker maintains an in-memory runtime map of `pid → *os.Process` for all processes started by this server instance. Kill requires presence in both the store (status=running) and the runtime map. Empirical verification confirmed `os.ProcessState` exposes only CPU times; no cross-platform stdlib API for process parentage exists.
- Q: Table sort order? -> A: Running rows pinned to top; within each status group, sorted by started_at descending.
- Q: OS time vs. Go observation time for timestamps? -> A: Go observation time (`time.Now()` after `cmd.Start()` / `cmd.Wait()`). OS process creation time requires `/proc` on Linux or `libproc` on macOS — neither in stdlib. Approximation is within microseconds and is documented.
