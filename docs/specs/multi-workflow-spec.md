# Feature Specification: Multi-Workflow Concurrency

**Created**: 2026-03-18
**Status**: Draft
**Input**: Need to run multiple adversarial spec workflows concurrently, each with its own source documents and isolated telemetry, replacing the current single-workflow architecture.

---

## User Stories & Acceptance Criteria

### User Story 1 — Per-Workflow Source Documents (Priority: P0)

A spec system operator wants to assign specific source documents to each workflow so that different features can be spec'd simultaneously from different reference material, without document cross-contamination between workflows.

Currently all workflows share a single `workspace/source-docs/` directory. If two workflows run concurrently, both see all documents regardless of relevance. Uploading a document for workflow B may overwrite one needed by workflow A.

**Why this priority**: Without per-workflow document isolation, concurrent workflows are fundamentally broken — agents would analyse irrelevant documents, producing incorrect specifications.

**Independent Test**: Upload documents to two different workflows and verify each workflow's discovery agent only sees its assigned documents.

**Acceptance Scenarios**:

1. **Given** a document library with files and two workflows (alpha, beta), **When** the operator assigns `design.md` to alpha and `requirements.md` to beta, **Then** alpha's discovery agent only receives `design.md` as a source document and beta's only receives `requirements.md`.
2. **Given** a workflow with assigned source documents, **When** the workflow is deleted, **Then** the workflow's copy of source documents is preserved in the deleted workflow's directory.
3. **Given** documents in the shared library, **When** a new workflow is started without explicit document assignment, **Then** no documents are automatically assigned (empty discovery is allowed).
4. **Given** a workflow with assigned documents, **When** the workflow resumes after a server restart, **Then** the same documents are used (read from `specs/{feature}/source-docs/`).

---

### User Story 2 — Document Library with Folder Organisation (Priority: P1)

A spec system operator wants a shared document library that supports folder organisation so that reference materials can be categorised and easily found when assigning documents to workflows.

Currently all uploads go to a flat `workspace/source-docs/` directory with no organisation. With multiple workflows, finding and selecting the right documents becomes difficult.

**Why this priority**: The library is the staging area for all documents. Without organisation, the operator cannot efficiently manage documents across multiple concurrent workflows.

**Independent Test**: Create folders in the library, upload documents into them, and verify the folder structure is preserved and browsable via the API and UI.

**Acceptance Scenarios**:

1. **Given** the document library, **When** the operator creates a folder `backend/` and uploads `api-spec.md` into it, **Then** the file is stored at `workspace/source-docs/backend/api-spec.md` and listed with its folder path.
2. **Given** documents in nested folders, **When** the operator lists library contents, **Then** the response includes the full relative path for each file (e.g., `backend/api-spec.md`).
3. **Given** a library folder with documents, **When** the operator assigns a folder to a workflow, **Then** all documents in the folder (recursively) are copied to the workflow's `source-docs/` directory.
4. **Given** a library with documents, **When** the operator assigns individual files to a workflow, **Then** only those specific files are copied.

---

### User Story 3 — Concurrent Workflow Execution (Priority: P0)

A spec system operator wants to run 2-3 spec workflows concurrently so that multiple features can be spec'd in parallel, reducing total wall-clock time.

Currently `WorkflowManager` holds a single `*Orchestrator` and rejects new workflows while one is running. The operator must wait for one workflow to complete before starting another.

**Why this priority**: This is the core concurrency capability. Without it, multi-workflow is sequential only.

**Independent Test**: Start two workflows with different feature names and verify both orchestrators run concurrently, each producing independent outputs.

**Acceptance Scenarios**:

1. **Given** workflow "alpha" is running in REVIEWING state, **When** the operator starts workflow "beta", **Then** beta starts successfully and both run concurrently.
2. **Given** workflows alpha and beta running concurrently, **When** alpha reaches HUMAN_GATE_1, **Then** beta continues running uninterrupted.
3. **Given** workflows alpha and beta running concurrently, **When** the operator cancels alpha, **Then** beta continues running uninterrupted.
4. **Given** 3 workflows running concurrently, **When** the operator starts a 4th, **Then** the system allows it (no hard cap, operator is trusted to manage resource usage).
5. **Given** a workflow "alpha" running, **When** the operator starts another workflow also named "alpha", **Then** the request is rejected with a conflict error.

---

### User Story 4 — Per-Workflow OTEL Telemetry (Priority: P0)

A spec system operator wants each workflow's cost, token usage, and API call metrics tracked independently so that the dashboard shows accurate per-workflow costs and the metrics database records correct data.

Currently the OTEL receiver has global accumulators. If two workflows run concurrently, all telemetry is merged into one set of counters, making per-workflow cost tracking impossible.

**Why this priority**: Without isolated telemetry, cost tracking (a circuit breaker input) is incorrect, potentially causing premature halts or budget overruns.

**Independent Test**: Run two workflows concurrently, verify each workflow's cost and token counts are independent in both the dashboard and SQLite.

**Acceptance Scenarios**:

1. **Given** workflows alpha ($5 cost) and beta ($3 cost), **When** the operator views the dashboard, **Then** alpha shows $5 and beta shows $3 (not $8 each).
2. **Given** workflow alpha running, **When** workflow beta is restarted (metrics reset), **Then** alpha's metrics are unaffected.
3. **Given** workflows alpha and beta, **When** child Claude processes send OTEL telemetry, **Then** each metric data point is attributed to the correct workflow via the `workflow.feature` resource attribute.
4. **Given** the server restarts with two workflows in progress, **When** the OTEL receiver starts, **Then** it restores per-workflow accumulators from SQLite independently.

---

### User Story 5 — WebSocket Event Multiplexing (Priority: P1)

A spec system operator wants WebSocket events tagged with their source workflow so that the frontend can filter and display events for the selected workflow, with notification badges for activity on other workflows.

Currently `EventEnvelope` has no workflow identifier. All events from all workflows are broadcast to all clients with no way to distinguish origin.

**Why this priority**: Without event multiplexing, the dashboard would show a confusing mix of events from multiple workflows with no way to separate them.

**Independent Test**: Run two workflows, verify WebSocket events carry the correct feature name, and verify the frontend can filter by selected workflow.

**Acceptance Scenarios**:

1. **Given** workflows alpha and beta both emitting events, **When** the frontend selects alpha, **Then** only alpha's events appear in the activity feed and status panel.
2. **Given** workflow beta emits a gate_request event while alpha is selected, **Then** a notification badge appears on the workflow list indicating beta needs attention.
3. **Given** the operator switches from alpha to beta, **Then** beta's current state, metrics, and recent activity feed are displayed immediately (from persisted data + WebSocket events).
4. **Given** all workflows are idle, **When** no events are being emitted, **Then** no notification badges are shown.

---

### User Story 6 — Multi-Workflow Dashboard UI (Priority: P1)

A spec system operator wants a dashboard that can display and manage multiple concurrent workflows so that they can monitor progress, respond to gates, and manage resources across all running workflows.

Currently the dashboard has a single workflow status panel, a single activity feed, and single-workflow spec/issues/convergence tabs.

**Why this priority**: The backend supports concurrency but the frontend needs to present it coherently. Without this, the operator cannot effectively use concurrent workflows.

**Independent Test**: Start two workflows, verify the dashboard shows both in the workflow list, verify selecting each switches the status panel and activity feed, verify gate panels appear for the correct workflow.

**Acceptance Scenarios**:

1. **Given** workflows alpha and beta running, **When** the operator views the Controls tab, **Then** both appear in the Workflows list with live status badges, cost, and time.
2. **Given** alpha selected in the dashboard, **When** the operator clicks "View" on beta, **Then** the status panel, activity feed, spec tab, issues tab, and convergence tab all switch to beta's data.
3. **Given** beta is in HUMAN_GATE_1, **When** the operator selects beta, **Then** the gate panel appears with beta's discovery output and the operator can respond.
4. **Given** alpha and beta running, **When** alpha reaches a gate state, **Then** a notification badge appears on alpha in the workflow list even if beta is currently selected.

---

### User Story 7 — Multi-Workflow API Changes (Priority: P0)

A spec system operator (or automated client) wants API endpoints that support multi-workflow operations so that workflows can be started, stopped, queried, and managed independently via HTTP.

Currently several endpoints assume a single workflow: `GET /api/workflow/status` returns one status, `POST /api/workflow/cancel` cancels the one workflow, spec endpoints serve one workflow's data.

**Why this priority**: The API is the contract between frontend and backend. All other stories depend on correct API semantics.

**Independent Test**: Start two workflows via API, query status for each independently, cancel one without affecting the other.

**Acceptance Scenarios**:

1. **Given** workflows alpha and beta, **When** `GET /api/workflow/status` is called without parameters, **Then** it returns an array of all workflow statuses.
2. **Given** workflows alpha and beta, **When** `GET /api/workflow/status?feature=alpha` is called, **Then** it returns only alpha's status.
3. **Given** workflow alpha running, **When** `POST /api/workflow/cancel` is called with `{"feature_name":"alpha"}`, **Then** alpha is cancelled and beta is unaffected.
4. **Given** no workflows running, **When** `POST /api/workflow/start` is called twice with different feature names, **Then** both start successfully.
5. **Given** workflow alpha running, **When** `POST /api/upload` is called with `feature_name=alpha`, **Then** the file is stored in `specs/alpha/source-docs/`.
6. **Given** documents in the library, **When** `POST /api/workflow/start` is called with `source_doc_paths`, **Then** those specific files are copied from the library to the workflow's source-docs.

---

## Behavioral Contract

Primary flows:
- When the operator starts a workflow with a unique feature name, the system creates an orchestrator and begins the workflow loop.
- When the operator assigns library documents to a workflow, the system copies them to `specs/{feature}/source-docs/`.
- When the operator selects a workflow in the dashboard, the system displays that workflow's status, metrics, activity, and spec data.
- When a child Claude process emits OTEL telemetry with a `workflow.feature` attribute, the system attributes it to the correct workflow's accumulators.
- When the operator responds to a gate, the system persists data to the correct workflow's spec directory and signals the correct orchestrator.

Error flows:
- When the operator starts a workflow with a feature name that already has a running orchestrator, the system returns HTTP 409 Conflict.
- When the operator cancels a workflow that doesn't exist, the system returns HTTP 404 Not Found.
- When OTEL telemetry arrives without a `workflow.feature` attribute, the system drops it silently (it's from an external Claude instance).
- When the operator queries status for a non-existent feature, the system returns HTTP 404.

Boundary conditions:
- When 3 workflows run concurrently and a 4th is started, the system allows it (no artificial cap).
- When all workflows are idle/terminal, `GET /api/workflow/status` returns an empty array.
- When a workflow is deleted, its source-docs copy is preserved on disk (within the deleted directory).
- When the server restarts with multiple workflows in progress, the system restores per-workflow OTEL accumulators from SQLite.

---

## Edge Cases

- What happens when two workflows are started with the same feature name simultaneously? Expected: one succeeds, the other gets 409 (mutex serialisation).
- What happens when the operator uploads a file to a workflow that doesn't exist yet? Expected: the spec directory is created on demand.
- What happens when the OTEL receiver gets telemetry for a workflow that has been deleted? Expected: telemetry is persisted to SQLite (the feature name exists in the data), but in-memory accumulators are not created for deleted workflows.
- What happens when the operator selects a workflow in the UI and it completes/errors while selected? Expected: the status panel updates to show the terminal state, no stale gate panels.
- What happens when library documents are modified after being copied to a workflow? Expected: the workflow's copy is unaffected (it's a copy, not a symlink).
- What happens when two workflows both have gate panels active? Expected: only the selected workflow's gate panel is shown; the other workflow gets a notification badge.

---

## Explicit Non-Behaviors

- The system must not impose a hard cap on concurrent workflows because the operator is trusted to manage system resources.
- The system must not share OTEL accumulators between workflows because this makes cost tracking incorrect and breaks circuit breakers.
- The system must not auto-assign library documents to new workflows because the operator should explicitly choose which documents each workflow uses.
- The system must not delete a workflow's source-docs when the workflow finishes because the operator may need to reference them.
- The system must not merge events from different workflows into a single activity feed by default because this creates confusion.
- The system must not queue workflows behind each other because the purpose is true concurrency, not sequential processing.

---

## Integration Boundaries

### Claude CLI (Child Processes)

- **Data in**: Prompt string, environment variables (OTEL config, workspace dir)
- **Data out**: JSON output (result, cost, tokens), OTEL telemetry (metrics, logs)
- **Contract**: `claude -p <prompt> --output-format json --verbose`, OTEL via gRPC to configured port
- **On failure**: Retry up to `max_retries`, then escalate. Telemetry loss is tolerable (metrics restored from SQLite on restart).
- **Development**: Real Claude CLI (no mock for integration tests; unit tests mock the `AgentRunner` interface)

### OTEL gRPC Receiver

- **Data in**: `ExportMetricsServiceRequest`, `ExportLogsServiceRequest` with resource attributes
- **Data out**: Per-workflow accumulators, SQLite persistence, WebSocket broadcasts
- **Contract**: OTLP gRPC on configured port. Resource attribute `service.name=adversarial-spec-system` for filtering. Resource attribute `workflow.feature=<name>` for per-workflow routing.
- **On failure**: If receiver is down, child processes buffer and retry (OTEL SDK default). If SQLite write fails, in-memory data is retained.
- **Development**: Real gRPC receiver.

### SQLite Metrics Store

- **Data in**: `UpsertWorkflowMetrics`, `RecordEvent` per-workflow
- **Data out**: `GetWorkflowMetrics(feature)`, `GetEvents(feature)`
- **Contract**: Pure-Go SQLite (modernc.org/sqlite), WAL mode, keyed by `feature_name`
- **On failure**: Log warning, continue with in-memory data only
- **Development**: Real SQLite (temp directory in tests)

### WebSocket Hub

- **Data in**: `EventEnvelope` with `FeatureName` field
- **Data out**: JSON broadcast to all connected clients
- **Contract**: All events include `feature_name`. Clients filter client-side.
- **On failure**: Disconnected clients reconnect automatically (existing logic). Missed events recovered from persisted state.
- **Development**: Real WebSocket.

---

## BDD Scenarios

### Feature: Per-Workflow Source Documents

#### Scenario: Documents assigned to workflow are isolated

**Traces to**: User Story 1, Acceptance Scenario 1
**Category**: Happy Path

- **Given** library files `design.md` and `requirements.md`
- **And** workflow "alpha" with `design.md` assigned
- **And** workflow "beta" with `requirements.md` assigned
- **When** alpha's discovery agent runs
- **Then** its prompt only references `/workspace/specs/alpha/source-docs/design.md`
- **And** beta's discovery agent only references `/workspace/specs/beta/source-docs/requirements.md`

#### Scenario: Workflow source docs preserved on deletion

**Traces to**: User Story 1, Acceptance Scenario 2
**Category**: Alternate Path

- **Given** workflow "alpha" with assigned source documents
- **When** alpha is deleted via `POST /api/workflow/reset`
- **Then** the directory `workspace/specs/alpha/` still exists on disk with source-docs intact

#### Scenario: New workflow with no documents assigned

**Traces to**: User Story 1, Acceptance Scenario 3
**Category**: Edge Case

- **Given** documents exist in the shared library
- **When** workflow "gamma" is started without specifying source documents
- **Then** gamma starts successfully with no source documents
- **And** the discovery agent runs with an empty source document list

#### Scenario: Documents survive server restart

**Traces to**: User Story 1, Acceptance Scenario 4
**Category**: Alternate Path

- **Given** workflow "alpha" with assigned source documents
- **When** the server restarts
- **And** alpha resumes
- **Then** the same documents from `specs/alpha/source-docs/` are used

---

### Feature: Document Library with Folders

#### Scenario: Create folder and upload document

**Traces to**: User Story 2, Acceptance Scenario 1
**Category**: Happy Path

- **Given** an empty document library
- **When** the operator uploads `api-spec.md` to folder `backend/`
- **Then** the file exists at `workspace/source-docs/backend/api-spec.md`
- **And** `GET /api/uploads` returns the file with path `backend/api-spec.md`

#### Scenario: List library contents with folder paths

**Traces to**: User Story 2, Acceptance Scenario 2
**Category**: Happy Path

- **Given** library files at `backend/api.md`, `backend/models.md`, and `frontend/ui.md`
- **When** `GET /api/uploads` is called
- **Then** the response includes all three files with their relative folder paths

#### Scenario: Assign folder to workflow

**Traces to**: User Story 2, Acceptance Scenario 3
**Category**: Happy Path

- **Given** library folder `backend/` containing `api.md` and `models.md`
- **When** the operator assigns folder `backend/` to workflow "alpha"
- **Then** `specs/alpha/source-docs/api.md` and `specs/alpha/source-docs/models.md` exist

#### Scenario: Assign individual files to workflow

**Traces to**: User Story 2, Acceptance Scenario 4
**Category**: Alternate Path

- **Given** library files `backend/api.md`, `backend/models.md`, and `frontend/ui.md`
- **When** the operator assigns only `backend/api.md` to workflow "alpha"
- **Then** `specs/alpha/source-docs/api.md` exists
- **But** `specs/alpha/source-docs/models.md` does not exist

---

### Feature: Concurrent Workflow Execution

#### Scenario: Start second workflow while first is running

**Traces to**: User Story 3, Acceptance Scenario 1
**Category**: Happy Path

- **Given** workflow "alpha" is running in REVIEWING state
- **When** `POST /api/workflow/start` is called with `feature_name: "beta"`
- **Then** HTTP 200 is returned
- **And** both alpha and beta orchestrators are running concurrently

#### Scenario: Gate on one workflow doesn't block another

**Traces to**: User Story 3, Acceptance Scenario 2
**Category**: Happy Path

- **Given** workflows alpha (REVIEWING) and beta (DISCOVERY) running
- **When** alpha transitions to HUMAN_GATE_1
- **Then** beta continues executing without interruption
- **And** alpha's status shows HUMAN_GATE_1 while beta's shows its current state

#### Scenario: Cancel one workflow, other continues

**Traces to**: User Story 3, Acceptance Scenario 3
**Category**: Alternate Path

- **Given** workflows alpha and beta running concurrently
- **When** `POST /api/workflow/cancel` is called with `{"feature_name":"alpha"}`
- **Then** alpha transitions to ESCALATED
- **And** beta continues running uninterrupted

#### Scenario: Duplicate feature name rejected

**Traces to**: User Story 3, Acceptance Scenario 5
**Category**: Error Path

- **Given** workflow "alpha" is running
- **When** `POST /api/workflow/start` is called with `feature_name: "alpha"`
- **Then** HTTP 409 Conflict is returned
- **And** the existing alpha workflow is unaffected

---

### Feature: Per-Workflow OTEL Telemetry

#### Scenario: Metrics attributed to correct workflow

**Traces to**: User Story 4, Acceptance Scenario 1
**Category**: Happy Path

- **Given** workflows alpha and beta running concurrently
- **And** alpha's child process has `workflow.feature=alpha` attribute
- **And** beta's child process has `workflow.feature=beta` attribute
- **When** both emit OTEL metrics
- **Then** alpha's dashboard shows only alpha's cost
- **And** beta's dashboard shows only beta's cost

#### Scenario: Resetting one workflow's metrics doesn't affect another

**Traces to**: User Story 4, Acceptance Scenario 2
**Category**: Alternate Path

- **Given** workflow alpha with $5 cost and workflow beta with $3 cost
- **When** beta is restarted (metrics reset)
- **Then** alpha's cost remains $5
- **And** beta's cost resets to $0

#### Scenario: Per-workflow OTEL accumulators restored on restart

**Traces to**: User Story 4, Acceptance Scenario 4
**Category**: Alternate Path

- **Given** workflows alpha ($5 cost) and beta ($3 cost) persisted in SQLite
- **When** the server restarts
- **Then** alpha's OTEL accumulator is restored to $5
- **And** beta's OTEL accumulator is restored to $3

---

### Feature: WebSocket Event Multiplexing

#### Scenario: Events filtered by selected workflow

**Traces to**: User Story 5, Acceptance Scenario 1
**Category**: Happy Path

- **Given** workflows alpha and beta emitting events
- **When** the operator selects alpha in the dashboard
- **Then** only events with `feature_name: "alpha"` appear in the activity feed

#### Scenario: Notification badge for unselected workflow gate

**Traces to**: User Story 5, Acceptance Scenario 2
**Category**: Happy Path

- **Given** alpha is selected in the dashboard
- **When** beta reaches HUMAN_GATE_1
- **Then** a notification badge appears on beta in the workflow list
- **And** alpha's activity feed is not interrupted

#### Scenario: Switching selected workflow loads data immediately

**Traces to**: User Story 5, Acceptance Scenario 3
**Category**: Happy Path

- **Given** alpha is selected, beta has accumulated 50 events
- **When** the operator switches to beta
- **Then** beta's status, metrics, and recent events are displayed immediately
- **And** the display reflects beta's persisted state from SQLite plus any buffered WebSocket events

---

### Feature: Multi-Workflow API

#### Scenario: Status endpoint returns all workflows

**Traces to**: User Story 7, Acceptance Scenario 1
**Category**: Happy Path

- **Given** workflows alpha (DRAFTING) and beta (REVIEWING)
- **When** `GET /api/workflow/status` is called
- **Then** the response is a JSON array with two entries
- **And** each entry contains `feature_name`, `state`, `round`, `cost_usd`, `wall_clock_seconds`

#### Scenario: Status endpoint filtered by feature

**Traces to**: User Story 7, Acceptance Scenario 2
**Category**: Happy Path

- **Given** workflows alpha and beta
- **When** `GET /api/workflow/status?feature=alpha` is called
- **Then** only alpha's status is returned (single object, not array)

#### Scenario: Cancel specific workflow

**Traces to**: User Story 7, Acceptance Scenario 3
**Category**: Happy Path

- **Given** workflows alpha and beta running
- **When** `POST /api/workflow/cancel` with `{"feature_name":"alpha"}`
- **Then** alpha is cancelled
- **And** beta is unaffected

#### Scenario: Upload document to specific workflow

**Traces to**: User Story 7, Acceptance Scenario 5
**Category**: Happy Path

- **Given** workflow "alpha" exists
- **When** `POST /api/upload` is called with `feature_name=alpha` and file `spec.md`
- **Then** the file is stored at `workspace/specs/alpha/source-docs/spec.md`

#### Scenario Outline: Start workflow with library documents

**Traces to**: User Story 7, Acceptance Scenario 6
**Category**: Happy Path

- **Given** library files at `<library_path>`
- **When** `POST /api/workflow/start` with `source_doc_paths: ["<library_path>"]` and `feature_name: "<feature>"`
- **Then** the file exists at `workspace/specs/<feature>/source-docs/<filename>`

**Examples**:

| library_path | feature | filename |
|---|---|---|
| `backend/api.md` | alpha | `api.md` |
| `design.md` | beta | `design.md` |

---

## Test-Driven Development Plan

### Test Hierarchy

| Level | Scope | Purpose |
|---|---|---|
| Unit | WorkflowManager map ops, OTEL accumulator partitioning, EventEnvelope feature field, document copy logic | Validates individual components in isolation |
| Integration | Multi-orchestrator lifecycle, per-workflow OTEL flow, WebSocket event filtering | Validates components work together |
| E2E | Start two workflows, respond to gates on each, verify independent outputs | Validates complete multi-workflow experience |

### Test Implementation Order

| Order | Test Name | Level | Traces to BDD Scenario | Description |
|---|---|---|---|---|
| 1 | TestWorkflowManagerMapCRUD | Unit | Start second workflow | Add/get/remove orchestrators by feature name |
| 2 | TestWorkflowManagerDuplicateFeatureRejected | Unit | Duplicate feature name rejected | Verify 409 for duplicate feature |
| 3 | TestWorkflowManagerCancelSpecific | Unit | Cancel one workflow, other continues | Cancel by feature name, verify other untouched |
| 4 | TestEventEnvelopeCarriesFeatureName | Unit | Events filtered by selected workflow | Verify feature name set on all event types |
| 5 | TestOTELAccumulatorPartitioning | Unit | Metrics attributed to correct workflow | Per-workflow accumulator map, independent increments |
| 6 | TestOTELAccumulatorResetIsolation | Unit | Resetting one doesn't affect another | Reset one feature, verify other unchanged |
| 7 | TestDocumentCopyToWorkflow | Unit | Documents assigned are isolated | Copy files from library to specs/{feature}/source-docs/ |
| 8 | TestDocumentCopyFolder | Unit | Assign folder to workflow | Recursive folder copy |
| 9 | TestLibraryFolderListing | Unit | List library contents with folder paths | ReadDir recursive with relative paths |
| 10 | TestUploadToWorkflow | Integration | Upload document to specific workflow | HTTP upload with feature_name param |
| 11 | TestStartConcurrentWorkflows | Integration | Start second workflow while first running | Two orchestrators running simultaneously |
| 12 | TestStatusAllWorkflows | Integration | Status endpoint returns all workflows | GET /api/workflow/status returns array |
| 13 | TestStatusSingleWorkflow | Integration | Status endpoint filtered by feature | GET with ?feature= returns single object |
| 14 | TestOTELAttributeRouting | Integration | Metrics attributed to correct workflow | OTEL export with workflow.feature attribute routes correctly |
| 15 | TestPerWorkflowSQLitePersistence | Integration | Per-workflow accumulators restored | Upsert two features, restart, verify both restored |

### Test Datasets

#### Dataset: Workflow Feature Names

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | `""` | Empty | Error: feature_name required | Duplicate feature name rejected | Empty string |
| 2 | `"a"` | Min | Success | Start second workflow | Single char |
| 3 | `"my-very-long-feature-name-with-many-words"` | Long | Success | Start second workflow | Long but valid |
| 4 | `"alpha"` (duplicate) | Duplicate | Error: 409 | Duplicate feature name rejected | Already running |
| 5 | `"ALPHA"` vs `"alpha"` | Case | Both succeed (case-sensitive) | Start second workflow | Different features |
| 6 | `"feat/sub"` | Slash | Error: invalid (path traversal risk) | Start second workflow | Contains path separator |
| 7 | `"feat..sub"` | Dot-dot | Error: invalid (path traversal risk) | Start second workflow | Contains traversal pattern |

#### Dataset: Library File Paths

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | `"design.md"` | Root file | Stored at `source-docs/design.md` | Create folder and upload | No folder |
| 2 | `"backend/api.md"` | One level | Stored at `source-docs/backend/api.md` | Create folder and upload | Single folder |
| 3 | `"a/b/c/deep.md"` | Deep nesting | Stored at `source-docs/a/b/c/deep.md` | List library contents | Three levels |
| 4 | `"../escape.md"` | Path traversal | Error: rejected | Create folder and upload | Security boundary |
| 5 | `""` | Empty path | Error: invalid | Create folder and upload | No file specified |

#### Dataset: OTEL Resource Attributes

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | `service.name=adversarial-spec-system, workflow.feature=alpha` | Both present | Attributed to alpha | Metrics attributed to correct workflow | Normal case |
| 2 | `service.name=adversarial-spec-system` (no workflow.feature) | Missing feature | Dropped silently | Metrics attributed to correct workflow | Can't route without feature |
| 3 | `service.name=other-service, workflow.feature=alpha` | Wrong service | Dropped silently | Metrics attributed to correct workflow | Not our process |
| 4 | No resource attributes | Missing all | Dropped silently | Metrics attributed to correct workflow | External process |
| 5 | `service.name=adversarial-spec-system, workflow.feature=""` | Empty feature | Dropped silently | Metrics attributed to correct workflow | Invalid feature |

### Regression Test Requirements

**Modifying existing functionality:**

| Existing Behaviour | Existing Test | New Regression Test Needed | Notes |
|---|---|---|---|
| Single workflow start/run/cancel | TestHandleStartWorkflow, TestHandleCancelWorkflow | Yes: TestSingleWorkflowStillWorks | Verify existing single-workflow path isn't broken |
| OTEL metrics accumulation | TestOTELExportMetrics | Yes: TestOTELSingleWorkflowBackwardCompat | Verify single-workflow OTEL still works without workflow.feature attribute |
| Gate approve/reject | TestHandleGateApprove | No | Already feature-scoped via URL path |
| Metrics store CRUD | TestUpsertAndGetWorkflowMetrics | No | Already keyed by feature name |
| Workflow status endpoint | TestHandleGetWorkflowStatus | Yes: TestStatusEndpointArrayFormat | Verify new array format doesn't break existing clients |

---

## Functional Requirements

- **FR-001**: System MUST support 2+ concurrent orchestrators, each identified by a unique feature name.
- **FR-002**: System MUST store source documents per-workflow at `workspace/specs/{feature}/source-docs/`.
- **FR-003**: System MUST provide a shared document library at `workspace/source-docs/` with folder support.
- **FR-004**: System MUST copy (not symlink) documents from the library to workflow source-docs on assignment.
- **FR-005**: System MUST partition OTEL telemetry accumulators by workflow feature name using the `workflow.feature` resource attribute.
- **FR-006**: System MUST drop OTEL telemetry that lacks a valid `workflow.feature` attribute (except for backward compatibility with single-workflow mode during migration).
- **FR-007**: System MUST include `feature_name` in every `EventEnvelope` broadcast via WebSocket.
- **FR-008**: System MUST return all workflow statuses as a JSON array from `GET /api/workflow/status` when no `feature` query parameter is specified.
- **FR-009**: System MUST accept `feature_name` on `POST /api/workflow/cancel`, `POST /api/workflow/restart`, and `POST /api/upload`.
- **FR-010**: System MUST preserve workflow source-docs on deletion (delete only workflow state, not source documents).
- **FR-011**: System MUST reject workflow start requests with a feature name that already has a running orchestrator (HTTP 409).
- **FR-012**: System MUST allow empty discovery (starting a workflow with no source documents assigned).
- **FR-013**: System SHOULD display notification badges in the UI for workflows that need attention (gate states) while another workflow is selected.
- **FR-014**: System MUST restore per-workflow OTEL accumulators from SQLite on server restart.
- **FR-015**: System MUST reject feature names containing path traversal patterns (`..`, `/`, `\`).

---

## Success Criteria

- **SC-001**: Two workflows started concurrently each produce independent specification documents with no cross-contamination of source documents, findings, or metrics.
- **SC-002**: Per-workflow cost shown in the dashboard matches the sum of OTEL-reported API costs for that workflow's child processes only, with <1% variance.
- **SC-003**: Cancelling or restarting one workflow has zero impact on other running workflows (no state change, no metric reset, no event loss).
- **SC-004**: After server restart with two workflows persisted, both resume with correct per-workflow metrics from SQLite.
- **SC-005**: Switching between workflows in the dashboard displays the correct workflow's data within 500ms (persisted state + WebSocket events).

---

## Traceability Matrix

| Requirement | User Story | BDD Scenario(s) | Test Name(s) |
|---|---|---|---|
| FR-001 | US-3 | Start second workflow, Gate doesn't block | TestWorkflowManagerMapCRUD, TestStartConcurrentWorkflows |
| FR-002 | US-1 | Documents assigned are isolated, Documents survive restart | TestDocumentCopyToWorkflow |
| FR-003 | US-2 | Create folder and upload, List contents | TestLibraryFolderListing |
| FR-004 | US-2 | Assign folder to workflow, Assign individual files | TestDocumentCopyFolder |
| FR-005 | US-4 | Metrics attributed to correct workflow | TestOTELAccumulatorPartitioning, TestOTELAttributeRouting |
| FR-006 | US-4 | Metrics attributed to correct workflow | TestOTELAttributeRouting |
| FR-007 | US-5 | Events filtered by selected workflow | TestEventEnvelopeCarriesFeatureName |
| FR-008 | US-7 | Status returns all workflows | TestStatusAllWorkflows |
| FR-009 | US-7 | Cancel specific workflow, Upload to workflow | TestWorkflowManagerCancelSpecific, TestUploadToWorkflow |
| FR-010 | US-1 | Source docs preserved on deletion | TestDocumentCopyToWorkflow |
| FR-011 | US-3 | Duplicate feature name rejected | TestWorkflowManagerDuplicateFeatureRejected |
| FR-012 | US-1 | New workflow with no documents | TestStartConcurrentWorkflows |
| FR-013 | US-5, US-6 | Notification badge for gate | (Frontend test — manual) |
| FR-014 | US-4 | Accumulators restored on restart | TestPerWorkflowSQLitePersistence |
| FR-015 | US-3 | Duplicate feature name rejected | TestWorkflowManagerDuplicateFeatureRejected |

---

## Ambiguity Warnings

| # | What's Ambiguous | Likely Agent Assumption | Question to Resolve |
|---|---|---|---|
| 1 | How should `GET /api/workflow/status` behave for backward compatibility? Currently returns a single object; changing to array breaks existing clients. | Return array always, add `?feature=X` for single-object response | Resolved: use array for all, single-object with ?feature= |
| 2 | Should the document library support deleting folders recursively? | Yes, with confirmation | Accepted as assumption |
| 3 | When assigning a folder to a workflow, should the folder structure be preserved in the workflow's source-docs? | Flatten — copy files without preserving folder structure | Need user confirmation |
| 4 | Should the dashboard support viewing two workflows side-by-side? | No — single selected workflow with fast switching | Accepted as assumption |
| 5 | What happens when a workflow's child process doesn't set the `workflow.feature` attribute (old binary)? | Drop the telemetry | Accepted — migration note in README |

---

## Evaluation Scenarios (Holdout)

> **Note**: These scenarios are for post-implementation evaluation only.

### Scenario: Cross-Contamination Smoke Test
- **Setup**: Start workflows "auth" and "payments" with completely different source documents.
- **Action**: Let both run through discovery and drafting.
- **Expected outcome**: auth's spec contains no payment-related content; payment's spec contains no auth-related content.
- **Category**: Happy Path

### Scenario: Cost Isolation Under Load
- **Setup**: Start workflow "expensive" with large source docs and "cheap" with a small doc. Let both run through one full review cycle.
- **Action**: Check each workflow's cost in the dashboard and SQLite.
- **Expected outcome**: "expensive" shows significantly higher cost than "cheap". Sum of both equals total OTEL-reported cost.
- **Category**: Happy Path

### Scenario: Restart One, Other Unaffected
- **Setup**: Start workflows "stable" and "flaky". Let both reach REVIEWING.
- **Action**: Restart "flaky" while "stable" is mid-review.
- **Expected outcome**: "stable" continues without any state change, event loss, or metric disruption. "flaky" starts fresh.
- **Category**: Error

### Scenario: Server Crash Recovery with Multiple Workflows
- **Setup**: Start two workflows. Kill the server process ungracefully (SIGKILL).
- **Action**: Restart the server.
- **Expected outcome**: Both workflows' metrics are restored from SQLite. Gate states are resumable. No data loss.
- **Category**: Edge Case

### Scenario: Dashboard Switching Under Event Storm
- **Setup**: Both workflows are in active REVIEWING state, generating many events per second.
- **Action**: Switch the dashboard between workflows rapidly (5 times in 3 seconds).
- **Expected outcome**: The dashboard shows the correct workflow's data after each switch, with no event mixing or stale data. Notification badges update correctly.
- **Category**: Edge Case

---

## Assumptions

- The operator runs at most 2-3 concurrent workflows. The system does not need job queuing, worker pools, or resource scheduling.
- Claude CLI can handle multiple concurrent instances on the same machine (each agent process is independent).
- The operator's machine has sufficient resources (CPU, memory, API rate limits) for concurrent workflows.
- All workflows share the same `config.yaml` settings (max_rounds, cost limits, skill paths). Per-workflow config overrides are out of scope.
- The OTEL gRPC port is shared across all workflows (one receiver, partitioned by attribute).
- WebSocket event volume from 2-3 concurrent workflows is manageable for browser rendering (no server-side filtering needed).

## Clarifications

### 2026-03-18

- Q: When a workflow finishes or is deleted, should its source-docs copy be cleaned up? -> A: Preserved for reference.
- Q: Should the library support folders? -> A: Yes, folders should be supported.
- Q: Should unselected workflow events be silently dropped or show badges? -> A: Notification badges for activity on other workflows.
- Q: Should starting a workflow with no docs be an error? -> A: Empty discovery is fine, should be allowed.
- Q: How many concurrent workflows? -> A: 2-3 concurrent. No queue needed.
- Q: Document management model? -> A: Library + assign to workflow. Upload to library or directly to workflow.
- Q: OTEL partitioning approach? -> A: Resource attributes (one receiver, partition by `workflow.feature`).
