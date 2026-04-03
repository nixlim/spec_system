# Code Review: Repository Runtime Audit

**Spec reviewed**: none; repo-wide code audit
**Review date**: 2026-04-03
**Verdict**: BLOCK

## Executive Summary

Repo-wide audit of the server entrypoint, HTTP/API layer, workflow managers, state machines, orchestrators, and dashboard wiring found one critical defect and five major defects that can break real workflows. The highest-risk problems are a path-traversal bug in codedoc feature names, workspace override wiring that is only partially implemented, a broken default no-config startup path for the main spec workflow, and restart/gate flows that strand code-review and codedoc runs.

The binary wiring is broadly present from [`cmd/specworkflow/main.go`](/Users/nixlim/Sync/PROJECTS/foundry_zero/adversarial_spec_system/cmd/specworkflow/main.go), and the codebase has substantial unit coverage, but the integration edges between HTTP, persistence, and the UI are not reliable enough to pass a production review.

| Metric | Value |
|--------|-------|
| Files reviewed | 21 key runtime/test files |
| Wiring gaps | 4 partial/dead wiring paths |
| Tests passing | 3 packages pass / 1 package fails |

| Severity | Count |
|----------|-------|
| CRITICAL | 1 |
| MAJOR | 5 |
| MINOR | 1 |
| OBSERVATION | 1 |
| **Total** | **8** |

## Wiring & Integration Audit

### Stubs Found

No package-level placeholder implementations were found in the runtime path reviewed.

### Implemented but Unwired

No fully unwired internal packages were found from the binary entrypoint; [`main.go`](/Users/nixlim/Sync/PROJECTS/foundry_zero/adversarial_spec_system/cmd/specworkflow/main.go) wires the spec, code-review, and codedoc managers into the HTTP server.

### Partial Wiring

| Component | What's Connected | What's Missing |
|-----------|------------------|----------------|
| `workspace_dir` for codedoc | UI sends a `workspace_dir` field | [`cdStartRequest`](/Users/nixlim/Sync/PROJECTS/foundry_zero/adversarial_spec_system/internal/api/codedoc_handlers.go#L114) has no such field, so the override is silently dropped |
| `workspace_dir` for spec/code review | APIs accept overrides and store some state under the override | source-doc copying and child runner working directories still use the manager default workspace, mixing two workspaces in one run |
| codedoc gate comments | UI renders comment boxes and API struct has `comment` | the browser never sends the comment and the handler never uses it |
| persisted CR/CD gate state | status endpoints can read gate state from disk | gate endpoints require an in-memory orchestrator, so restarted workflows are not actually resumable from the dashboard |

## Code Findings

### CRITICAL Findings

#### [CRIT-001] Codedoc start allows path traversal through `feature_name`

- **Lens**: Security
- **Files**:
  - [`codedoc_handlers.go`](/Users/nixlim/Sync/PROJECTS/foundry_zero/adversarial_spec_system/internal/api/codedoc_handlers.go#L146)
  - [`orchestrator.go`](/Users/nixlim/Sync/PROJECTS/foundry_zero/adversarial_spec_system/internal/codedoc/orchestrator.go#L79)
  - [`orchestrator_helpers.go`](/Users/nixlim/Sync/PROJECTS/foundry_zero/adversarial_spec_system/internal/codedoc/orchestrator_helpers.go#L205)
- **Issue**: `HandleCDStart` only checks that `feature_name` is non-empty; it never validates or sanitizes it. `NewCodedocOrchestrator` then builds `featureDir := filepath.Join(cfg.WorkspaceDir, "codedoc", cfg.FeatureName)`, and `persistState()` writes `workflow-state.json` there. A request body with `feature_name: "../../tmp/pwn"` can escape the intended `workspace/codedoc/` subtree.
- **Impact**: A caller can create or overwrite files outside the codedoc workspace namespace. Follow-up operations such as reset/rewind then act on that escaped directory, which is both a correctness bug and a filesystem safety bug.
- **Fix**: Apply the same feature-name validation used by the spec workflow and code-review workflow before constructing paths.

### MAJOR Findings

#### [MAJ-001] Workspace overrides are only half-wired and can mix artefacts across workspaces

- **Lens**: Correctness
- **Files**:
  - [`workflow_handler.go`](/Users/nixlim/Sync/PROJECTS/foundry_zero/adversarial_spec_system/internal/api/workflow_handler.go#L479)
  - [`workflow_handler.go`](/Users/nixlim/Sync/PROJECTS/foundry_zero/adversarial_spec_system/internal/api/workflow_handler.go#L498)
  - [`workflow_handler.go`](/Users/nixlim/Sync/PROJECTS/foundry_zero/adversarial_spec_system/internal/api/workflow_handler.go#L520)
  - [`main.go`](/Users/nixlim/Sync/PROJECTS/foundry_zero/adversarial_spec_system/cmd/specworkflow/main.go#L200)
  - [`main.go`](/Users/nixlim/Sync/PROJECTS/foundry_zero/adversarial_spec_system/cmd/specworkflow/main.go#L239)
  - [`codedoc_handlers.go`](/Users/nixlim/Sync/PROJECTS/foundry_zero/adversarial_spec_system/internal/api/codedoc_handlers.go#L114)
  - [`app.js`](/Users/nixlim/Sync/PROJECTS/foundry_zero/adversarial_spec_system/static/app.js#L2269)
- **Issue**: The UI exposes a generic workspace override, but the back end wires it inconsistently. Spec workflows resolve/copy source docs from `manager.workspaceDir` before applying the override, then still create the Claude runner with `manager.workspaceDir` instead of the chosen `wsDir`. Code review accepts `workspace_dir` for artefacts, but the runners were already constructed in `main` against the default workspace. Codedoc is worse: the UI sends `workspace_dir`, but `cdStartRequest` has no `WorkspaceDir` field at all.
- **Impact**: Overridden runs can read source docs from one workspace, persist state in another, and execute child agents from a third working directory. This breaks isolation and makes resume/debugging unreliable.
- **Fix**: Thread the effective workspace through request structs, source-doc discovery/copy, runner construction, and disk-state lookup consistently for all three workflows.

#### [MAJ-002] The advertised default startup path is broken without `config.yaml`

- **Lens**: Correctness
- **Files**:
  - [`main.go`](/Users/nixlim/Sync/PROJECTS/foundry_zero/adversarial_spec_system/cmd/specworkflow/main.go#L38)
  - [`config.go`](/Users/nixlim/Sync/PROJECTS/foundry_zero/adversarial_spec_system/internal/specworkflow/config.go#L89)
  - [`orchestrator_helpers.go`](/Users/nixlim/Sync/PROJECTS/foundry_zero/adversarial_spec_system/internal/specworkflow/orchestrator_helpers.go#L69)
  - [`prompts.go`](/Users/nixlim/Sync/PROJECTS/foundry_zero/adversarial_spec_system/internal/specworkflow/prompts.go#L79)
- **Issue**: `main` documents `--config` as optional, and `DefaultConfig()` leaves `SkillPaths` empty. `loadSkillsFromConfig()` then returns an empty `SkillCache`, but prompt generation immediately requires `spec-template.md` and other skill files via `GetSkillContent()`. In practice the workflow starts and then fails in discovery with `unknown skill file: "spec-template.md"`.
- **Impact**: A default server boot looks healthy, but the first spec workflow fails at runtime unless an external config has already wired skill directories. That is a broken out-of-box path, not just a test harness issue.
- **Fix**: Either make `--config` truly required at process startup, or derive sane default skill paths and validate them before accepting workflow requests.

#### [MAJ-003] Code-review and codedoc workflows are not resumable from human gates after restart

- **Lens**: Correctness
- **Files**:
  - [`codereview_handlers.go`](/Users/nixlim/Sync/PROJECTS/foundry_zero/adversarial_spec_system/internal/api/codereview_handlers.go#L242)
  - [`codereview_handlers.go`](/Users/nixlim/Sync/PROJECTS/foundry_zero/adversarial_spec_system/internal/api/codereview_handlers.go#L326)
  - [`codedoc_handlers.go`](/Users/nixlim/Sync/PROJECTS/foundry_zero/adversarial_spec_system/internal/api/codedoc_handlers.go#L231)
  - [`codedoc_handlers.go`](/Users/nixlim/Sync/PROJECTS/foundry_zero/adversarial_spec_system/internal/api/codedoc_handlers.go#L307)
  - [`app.js`](/Users/nixlim/Sync/PROJECTS/foundry_zero/adversarial_spec_system/static/app.js#L759)
  - [`app.js`](/Users/nixlim/Sync/PROJECTS/foundry_zero/adversarial_spec_system/static/app.js#L3303)
  - [`app.js`](/Users/nixlim/Sync/PROJECTS/foundry_zero/adversarial_spec_system/static/app.js#L3442)
- **Issue**: Both status endpoints can reconstruct CR/CD state from disk when there is no in-memory orchestrator, so the dashboard continues showing gate panels after restart. But the corresponding gate POST handlers immediately 404 if `manager.getOrchestrator(featureName)` returns `nil`. The browser only posts to `/gate`; it never calls the CR/CD `/resume` endpoints.
- **Impact**: After a restart, a code review or codedoc run can appear actionable in the UI but be impossible to advance. That strands workflows at scope/fixes/draft/final gates until someone uses ad hoc API calls or restarts the run.
- **Fix**: Mirror the spec workflow’s auto-resume-from-disk behavior in CR/CD gate handlers, and add explicit frontend resume handling for CR/CD workflows.

#### [MAJ-004] Codedoc start returns success before any durable state exists

- **Lens**: Error Handling
- **Files**:
  - [`codedoc_handlers.go`](/Users/nixlim/Sync/PROJECTS/foundry_zero/adversarial_spec_system/internal/api/codedoc_handlers.go#L191)
  - [`orchestrator.go`](/Users/nixlim/Sync/PROJECTS/foundry_zero/adversarial_spec_system/internal/codedoc/orchestrator.go#L87)
  - [`orchestrator.go`](/Users/nixlim/Sync/PROJECTS/foundry_zero/adversarial_spec_system/internal/codedoc/orchestrator.go#L135)
  - [`orchestrator_helpers.go`](/Users/nixlim/Sync/PROJECTS/foundry_zero/adversarial_spec_system/internal/codedoc/orchestrator_helpers.go#L205)
- **Issue**: `HandleCDStart` inserts the orchestrator into memory, launches `RunWorkflow()` in a goroutine, and returns `202 Accepted` immediately. `NewCodedocOrchestrator` does not persist the initial state, so there is no durable `workflow-state.json` until the background goroutine gets far enough to transition and persist. The current test suite already exposes this timing hole.
- **Impact**: A process crash or fast follow-up request can observe a “started” workflow with no recoverable state on disk. This also makes cleanup/race behavior flaky, as shown by the failing API test run.
- **Fix**: Persist the initial codedoc state synchronously before returning success, or block until the first durable transition has completed.

#### [MAJ-005] CR/CD start handlers do not protect persisted workflows from duplicate starts after restart

- **Lens**: Correctness
- **Files**:
  - [`codereview_handlers.go`](/Users/nixlim/Sync/PROJECTS/foundry_zero/adversarial_spec_system/internal/api/codereview_handlers.go#L165)
  - [`codedoc_handlers.go`](/Users/nixlim/Sync/PROJECTS/foundry_zero/adversarial_spec_system/internal/api/codedoc_handlers.go#L171)
  - [`workflow_handler.go`](/Users/nixlim/Sync/PROJECTS/foundry_zero/adversarial_spec_system/internal/api/workflow_handler.go#L448)
- **Issue**: Code-review and codedoc starts only reject duplicate feature names that are still present in the in-memory manager map. They do not check on-disk persisted state the way spec workflows do. After a server restart, starting the same feature again can recreate the orchestrator and overwrite or fork existing artefacts.
- **Impact**: Operators can accidentally clobber a paused review/documentation run instead of resuming it, leading to lost audit history and ambiguous workspace contents.
- **Fix**: Add the same persisted-state conflict detection/resume behavior used by `HandleStartWorkflow`.

### MINOR Findings

#### [MIN-001] Codedoc gate comments are dead UI/API plumbing

- **Lens**: Overcomplexity
- **Files**:
  - [`app.js`](/Users/nixlim/Sync/PROJECTS/foundry_zero/adversarial_spec_system/static/app.js#L3335)
  - [`app.js`](/Users/nixlim/Sync/PROJECTS/foundry_zero/adversarial_spec_system/static/app.js#L3375)
  - [`app.js`](/Users/nixlim/Sync/PROJECTS/foundry_zero/adversarial_spec_system/static/app.js#L3417)
  - [`app.js`](/Users/nixlim/Sync/PROJECTS/foundry_zero/adversarial_spec_system/static/app.js#L3442)
  - [`codedoc_handlers.go`](/Users/nixlim/Sync/PROJECTS/foundry_zero/adversarial_spec_system/internal/api/codedoc_handlers.go#L122)
  - [`codedoc_handlers.go`](/Users/nixlim/Sync/PROJECTS/foundry_zero/adversarial_spec_system/internal/api/codedoc_handlers.go#L313)
- **Issue**: The dashboard renders three codedoc comment textareas, and the request struct includes `comment`, but `submitCDGate()` only sends `{action}` and the handler never propagates `req.Comment` to the orchestrator or persistence.
- **Impact**: Reviewers are shown a comment box that has no effect. That is dead wiring and misleading UX.
- **Fix**: Either send/persist the comment end-to-end or remove the unused UI/control surface.

### Observations

#### [OBS-001] The binary entrypoint is otherwise wired coherently

- **Lens**: Correctness
- **Files**:
  - [`main.go`](/Users/nixlim/Sync/PROJECTS/foundry_zero/adversarial_spec_system/cmd/specworkflow/main.go#L158)
  - [`main.go`](/Users/nixlim/Sync/PROJECTS/foundry_zero/adversarial_spec_system/cmd/specworkflow/main.go#L199)
  - [`main.go`](/Users/nixlim/Sync/PROJECTS/foundry_zero/adversarial_spec_system/cmd/specworkflow/main.go#L238)
- **Suggestion**: The server does successfully wire the three workflow families, WebSocket streaming, logs, metrics, and spec endpoints from a single binary. The main problems are integration edges and restart semantics, not missing top-level registration.

## Test Results

Command run:

```bash
go test ./internal/api/... ./internal/codereview/... ./internal/codedoc/... ./internal/specworkflow/...
```

Summary:

| Status | Count |
|--------|-------|
| PASS | 3 packages |
| FAIL | 1 package |
| SKIP | 0 observed |

### Failing Tests

| Test | File | Failure Reason |
|------|------|----------------|
| `TestCodedocHandlers_Gate_ScopeConfirm` | [`codedoc_handlers_test.go`](/Users/nixlim/Sync/PROJECTS/foundry_zero/adversarial_spec_system/internal/api/codedoc_handlers_test.go#L313) | `go test` fails in `internal/api` with codedoc background-workflow cleanup races and missing early persistence (`TempDir RemoveAll cleanup: ... directory not empty`) |

Package results:

- `FAIL github.com/foundry-zero/adversarial-spec-system/internal/api`
- `ok github.com/foundry-zero/adversarial-spec-system/internal/codereview`
- `ok github.com/foundry-zero/adversarial-spec-system/internal/codedoc`
- `ok github.com/foundry-zero/adversarial-spec-system/internal/specworkflow`

## Verdict Rationale

The repository is blocked by correctness issues at workflow boundaries, not by isolated implementation mistakes. The codedoc start path has a real filesystem-safety bug, the restart/gate paths for CR/CD are not actually operable from the dashboard, and workspace overrides are not wired consistently enough to trust isolation or persistence.

The system also advertises execution modes that do not work as described: default no-config startup fails at runtime for spec workflows, and codedoc comments/workspace overrides are exposed in the UI without an end-to-end implementation behind them. These issues can cause workflows to fail, become stranded, or write artefacts in the wrong place.

### Recommended Next Actions

- [ ] Fix `CRIT-001` by validating/sanitizing codedoc `feature_name` before any path construction.
- [ ] Fix `MAJ-001` by threading the effective workspace through source-doc discovery, disk lookups, and runner creation for spec, code-review, and codedoc flows.
- [ ] Fix `MAJ-002` by making skill-path configuration mandatory or providing working defaults.
- [ ] Fix `MAJ-003` by auto-resuming CR/CD gate workflows from persisted state and wiring the frontend to use those paths.
- [ ] Fix `MAJ-004` by persisting codedoc state synchronously before returning `202 Accepted`.
- [ ] Fix `MAJ-005` by checking persisted CR/CD state before allowing a new start for the same feature.
- [ ] Fix `MIN-001` by either removing codedoc gate comments or actually persisting them.

After fixing, re-run: `/grill-code /Users/nixlim/Sync/PROJECTS/foundry_zero/adversarial_spec_system`
