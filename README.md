# Adversarial Spec System

A multi-agent system that produces high-quality software specifications through adversarial review. Specialised AI agents collaborate and compete — discovering requirements, drafting specs, reviewing through multiple lenses, revising, judging convergence, and decomposing into task graphs — while human gates ensure alignment at critical decision points.

The system supports **dual-provider execution** (Claude + Codex in parallel) across discovery, drafting, and review phases, with intelligent merging of outputs. A separate **code review workflow** provides automated code auditing with fix-review loops.

## How It Works

### Spec Workflow

```
Source Documents
       |
       v
  [ DISCOVERY ]  ──>  Extract actors, scope, constraints, requirements
       |                (dual-provider: Claude + Codex with intelligent merge)
       v
  [ HUMAN GATE 1 ]  ──>  Confirm / correct requirements (up to 3 corrections)
       |
       v
  [ DRAFTING ]  ──>  Produce spec + holdout test dataset from templates
       |              (dual-provider: Claude + Codex with combine agent)
       v
  [ HUMAN GATE 2 ]  ──>  Resolve ambiguity warnings (1 redraft allowed)
       |
       v
  ┌─────────────────────────────────┐
  │  [ REVIEWING ]                  │
  │    4 parallel reviewer agents   │  Adversarial review loop
  │    8 lenses across 4 groups     │  (2-5 rounds, configurable)
  │    + optional Codex reviewers   │
  │            |                    │
  │  [ REVISING ]                   │
  │    Address findings             │
  │            |                    │
  │  [ JUDGING ]                    │
  │    Convergence check            │
  │    Anti-gaming pre-checks       │
  └─────────────┬───────────────────┘
                |
                v
  [ HUMAN GATE FINAL ]  ──>  Only if critical findings remain
                |
                v
          [ FINALIZED ]
                |
                v
         [ TASKIFY ]  ──>  Decompose spec into structured task graph
                |            (validation+retry with schema/DAG checks)
                v
  ┌─────────────────────────────────┐
  │  [ TASK REVIEW ]                │
  │    Dual-provider review of      │  Task review loop
  │    task graph quality           │  (up to 3 rounds)
  │            |                    │
  │  [ TASK REVISION ]              │
  │    Address task findings        │
  └─────────────┬───────────────────┘
                |
                v
  [ TASK HUMAN GATE ]  ──>  Approve / correct / re-decompose tasks
                |
                v
       [ TASKS APPROVED ]
                |
                v
          [ COMPLETE ]
```

The dashboard displays this as a visual pipeline stepper showing all stages, with completed stages in green, the current stage pulsing, and future stages grayed out.

### Smart Discovery Restart

When rewinding to the discovery phase, the system detects existing artefacts and offers three choices:

- **Skip to gate** — jump directly to HUMAN_GATE_1 with the existing merged output
- **Replay merge** — re-run the merge step from existing per-provider outputs without re-dispatching agents
- **Restart fresh** — re-run discovery agents from scratch

### Code Review Workflow

A separate workflow for automated code auditing:

```
Code Path
    |
    v
  [ CR_INIT ]  ──>  [ CR_HUMAN_GATE_SCOPE ]  ──>  Confirm review scope
    |
    v
  ┌─────────────────────────────────┐
  │  [ CR_REVIEWING ]               │
  │    Dual-provider code review    │  Fix-review loop
  │            |                    │  (configurable rounds)
  │  [ CR_FIXING ]                  │
  │    Automated fix application    │
  │            |                    │
  │  [ CR_HUMAN_GATE_FIXES ]        │
  │    Human approval of fixes      │
  └─────────────┬───────────────────┘
                |
                v
  [ CR_COMPLETE ] or [ CR_ESCALATED ]
```

### Agents

| Agent | Role | Lenses |
|-------|------|--------|
| Discovery | Extracts requirements from source documents | -- |
| Discovery Merge | Intelligently merges dual-provider discovery outputs | -- |
| Drafter | Produces specification and holdout test data | -- |
| Drafter Combine | Merges dual-provider drafter outputs | -- |
| Reviewer (Clarity) | Ambiguity, Incompleteness | AMB, INC |
| Reviewer (Consistency) | Consistency, Feasibility | CON, FEA |
| Reviewer (Security) | Security, Operability | SEC, OPS |
| Reviewer (Correctness) | Correctness, Complexity | COR, CPX |
| Reviser | Addresses findings from reviewers | -- |
| Judge | Evaluates convergence, renders PASS/REVISE/BLOCK verdict | -- |
| Taskify | Decomposes finalized spec into structured task graph | -- |
| Task Reviewer | Reviews task graph for quality and completeness | -- |
| Task Reviser | Addresses task review findings | -- |

All JSON-producing agents use a **validation+retry loop**: after dispatch, the output is validated against the expected schema. If invalid, validation errors are fed back into the prompt and the agent is re-dispatched (up to `max_retries` attempts).

### Convergence Protocol

The judge's PASS verdict is subject to deterministic anti-gaming checks:

- All CRITICAL findings must be closed or dismissed
- Revision change logs must reference every CRITICAL and MAJOR finding
- Minimum round count must be met
- Authority limits per round: max 2 severity downgrades, max 3 dismissals
- Cumulative escalation: total downgrades + dismissals > 5 triggers escalation

### Circuit Breakers

The workflow halts automatically when any limit is exceeded:

- **Max rounds** -- round count exceeds configured maximum (default: 5)
- **Max findings** -- cumulative finding count exceeds threshold (default: 60)
- **Staleness** -- CRITICAL/MAJOR findings stuck for N consecutive rounds (default: 2)
- **Wall clock** -- elapsed time exceeds budget (default: 60 minutes)
- **Cost** -- cumulative API cost exceeds budget (default: $50)

## Quick Start

### Prerequisites

- Go 1.21+ (built with 1.25)
- [Claude CLI](https://docs.anthropic.com/en/docs/claude-code) installed and authenticated
- Optional: [Codex CLI](https://github.com/openai/codex) for dual-provider mode
- `plan-spec` and `grill-spec` skill directories (see [Configuration](#configuration))

### Build

```bash
go build -o specworkflow ./cmd/specworkflow
```

### Run

```bash
./specworkflow --config config.yaml --workspace ./workspace
```

Open `http://localhost:8080` for the dashboard.

### CLI Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--port` | `8080` | HTTP listen port |
| `--workspace` | `./workspace` | Directory for spec files, uploads, and metrics |
| `--config` | *(none)* | Path to YAML configuration file |
| `--otel-port` | `4317` | gRPC OTLP receiver port for Claude Code telemetry (0 to disable) |

## Configuration

Create a `config.yaml`:

```yaml
# Required: paths to skill directories
skill_paths:
  plan_spec: "/path/to/.claude/skills/plan-spec"
  grill_spec: "/path/to/.claude/skills/grill-spec"

# Workflow limits (defaults shown)
max_rounds: 5              # Maximum review/revise iterations
min_rounds: 2              # Minimum iterations before acceptance
max_total_findings: 60     # Upper bound on cumulative findings
staleness_threshold: 2     # Consecutive stale rounds before halt
max_wall_clock_minutes: 60 # Time budget
max_cost_usd: 50.0         # Cost budget
max_gate_corrections: 3    # Max human corrections at Gate 1
max_gate2_redrafts: 1      # Max redrafts at Gate 2
max_retries: 2             # Agent retry attempts (used for validation+retry)

# Dual-provider configuration
enable_codex_reviewers: true    # Claude + Codex for review/holdout
enable_codex_discovery: false   # Claude + Codex for discovery
enable_codex_drafting: false    # Claude + Codex for drafting
codex_model: "gpt-5.4"         # Model ID for Codex CLI

# Agent timeouts
agent_timeout_seconds: 300       # Discovery, drafting, taskify agents
reviewer_timeout_seconds: 300    # Reviewer agents (Claude + Codex)
holdout_timeout_seconds: 300     # Holdout generation agents

# Task decomposition
taskify_max_retries: 3           # Max retries for task graph validation
task_review_max_rounds: 3        # Max task review/revision rounds

# Code review (optional)
code_review:
  max_rounds: 3
  max_cost_usd: 50.0
  max_wall_clock_minutes: 120
  fixer_timeout_seconds: 600
  commit_mode: branch_per_round
  staleness_threshold: 2
  max_retries: 2
  reviewer_timeout_seconds: 300
```

### Skill Directories

The system requires two Claude skill directories containing the templates that govern spec structure and review criteria:

- **plan-spec**: Must contain `spec-template.md`, `bdd-template.md`, `test-dataset-template.md`
- **grill-spec**: Must contain `review-constitution.md`, `report-template.md`

## Dashboard

The web dashboard provides real-time visibility into workflow execution. Multiple workflows can run concurrently, each tracked independently. Both spec workflows and code review workflows are managed from the same interface.

### Tabs

- **Controls** -- Active workflow list, start new workflows (spec or code review), upload source documents, assign documents to workflows, manage workspace
- **Spec** -- View and diff spec versions as they evolve through rounds
- **Issues** -- Track findings with severity/status/lens filtering and lifecycle management
- **Convergence** -- Monitor review/revision convergence metrics and round history
- **Messages** -- Filtered workflow log (OTEL, Orchestrator, Claude Runner, Agent Events, State Transitions)

### Workflow Status Panel

A persistent top panel shows aggregate metrics updated in real-time via SSE:

- **Pipeline stepper** -- visual chain of all workflow stages with progress indication
- Feature name, round number, workflow state badge, workflow type badge (SPEC/CR)
- Cost (from OTEL telemetry), elapsed wall clock time
- Token usage (input, output, cache read), API call count, agent cost
- Activity feed of individual tool and API events
- Source document list per workflow

### Multi-Workflow Support

Multiple workflows can execute concurrently, each processing a different feature:

- The active workflow list shows all running workflows with state badges
- Notification badges appear on workflows needing attention (at gate states)
- Click a workflow to switch context -- the status panel, gates, and all tabs update
- Each workflow has isolated source documents, state, and artefacts

### Human Gates

Gate panels appear when the workflow requires human input:

- **Gate 1** -- Review discovery output (with side-by-side dual-provider view when applicable), answer open questions, provide corrections (editable inline fields), add reviewer comments
- **Gate 2** -- Resolve ambiguity warnings (accept/answer/defer per warning), add reviewer comments
- **Gate Final** -- Approve or reject when critical findings persist after convergence
- **Task Gate** -- Review task graph, approve or request re-decomposition
- **Discovery Resume** -- Choose how to proceed when existing artefacts are found (skip to gate, replay merge, restart fresh)

### Workflow Rewind and Replay

Workflows can be rewound to any previous agent stage (DISCOVERY, DRAFTING, REVIEWING, REVISING, JUDGING, TASKIFY) while preserving all artefact files. The workflow resumes from the target stage, overwriting outputs when agents re-run.

Individual phases can be **replayed** without re-dispatching agents:
- **Discovery merge** -- re-run the intelligent merge from existing per-provider outputs
- **Drafting combine** -- re-run the combine agent from existing per-provider drafts
- **Review merge** -- re-run findings dedup from existing reviewer outputs
- **Task review merge** -- re-run task findings dedup from existing task reviewer outputs

## API Reference

### Spec Workflow Lifecycle

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/workflow/start` | Start new workflow with feature name and source docs |
| POST | `/api/workflow/cancel` | Cancel running workflow |
| GET | `/api/workflow/status` | Poll workflow status (single or all) |
| POST | `/api/workflow/resume` | Resume from ESCALATED/ERROR/paused state |
| POST | `/api/workflow/rewind` | Rewind to target state and round |
| POST | `/api/workflow/replay` | Replay a specific phase (merge/combine/dedup) |
| POST | `/api/workflow/finalize` | Force transition to FINALIZED |
| POST | `/api/workflow/reset` | Delete feature directory entirely |
| POST | `/api/workflow/restart` | Stop, delete, and restart workflow |
| POST | `/api/workflow/retry` | Clear stale state file |
| GET | `/api/workflow/agents` | List active agents for current workflow |

### Code Review Workflow

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/codereview/start` | Start new code review workflow |
| GET | `/api/codereview/{feature}/status` | Poll code review status |
| POST | `/api/codereview/{feature}/gate` | Submit gate decision (approve/reject) |
| POST | `/api/codereview/{feature}/cancel` | Cancel running code review |
| POST | `/api/codereview/{feature}/resume` | Resume from ERROR state |
| POST | `/api/codereview/{feature}/reset` | Delete code review feature directory |

### Source Documents

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/upload` | Upload source documents to global library |
| GET | `/api/uploads` | List uploaded files |
| POST | `/api/workflow/{feature}/source-docs` | Assign documents to a workflow |
| GET | `/api/workflow/{feature}/source-docs` | List documents assigned to a workflow |

### Gates

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/tasks/{id}/approve` | Approve gate (with corrections/resolutions) |
| POST | `/api/tasks/{id}/reject` | Reject gate (cancel workflow) |

### Data Access

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/workspace/features` | List all features with metadata |
| GET | `/api/workspace/features/{name}/discovery` | Feature discovery output |
| GET | `/api/workspace/features/{name}/state` | Feature workflow state |
| GET | `/api/workspace/features/{name}/files/{f}` | Specific feature file |
| GET | `/api/spec/*` | Spec versions, diffs, issues, convergence |
| GET | `/api/metrics` | Persisted OTEL telemetry |
| GET | `/api/messages` | Workflow log messages |
| GET | `/api/logs/server` | Server log ring buffer |

### Real-Time

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/ws` | WebSocket event stream |

## Architecture

```
cmd/specworkflow/main.go          CLI entry point, HTTP routing

internal/api/
  workflow_handler.go             HTTP handlers, WorkflowManager (concurrent map)
  codereview_handlers.go          HTTP handlers for code review workflow
  otel_receiver.go                OTLP gRPC receiver for Claude telemetry
  metrics_store.go                SQLite persistence for telemetry
  websocket.go                    WebSocket hub and broadcasting
  spec_endpoints.go               Spec/issue/convergence REST endpoints
  upload.go                       Source document upload
  log_stream.go                   Server log and message streaming

internal/specworkflow/
  orchestrator.go                 Main workflow loop and state coordination
  orchestrator_discovery.go       Discovery phase + Gate 1 + smart restart
  orchestrator_drafting.go        Drafting phase + Gate 2 + dual-provider combine
  orchestrator_review.go          Review dispatch + revision + judging
  orchestrator_finalize.go        Finalization and output assembly
  orchestrator_taskify.go         Task graph decomposition + validation loop
  orchestrator_task_review.go     Task review/revision loop
  orchestrator_helpers.go         Agent dispatch, error handling utilities
  statemachine.go                 State machine with guarded transitions
  claude_runner.go                Claude CLI subprocess execution
  codex_runner.go                 Codex CLI subprocess execution
  review_dispatch.go              Parallel reviewer dispatch with retry
  prompts.go                      Prompt construction for all agents
  json_validation.go              Validation+retry loop for JSON-producing agents
  convergence.go                  Anti-gaming pre-checks and convergence
  breakers.go                     Circuit breaker evaluation
  issues.go                       Issue tracker with lifecycle transitions
  skills.go                       Skill template loading and caching
  persistence.go                  Atomic state persistence
  recovery.go                     Agent failure detection and retry
  resume.go                       Crash/restart recovery
  rewind.go                       Workflow rewind to previous stages
  replay.go                       Phase replay (merge/combine without re-dispatch)
  merge.go                        Mechanical merge of discovery outputs
  holdout_merge.go                Holdout test dataset merging
  debate_trail.go                 Decision history through review rounds
  progress.go                     Workflow progress tracking
  security.go                     Prompt injection mitigation
  config.go                       Configuration parsing and validation
  types.go                        Core type definitions and workflow states
  events.go                       Event system (14+ event types)
  team.go                         Agent team definition
  gate_requirements.go            Gate 1 handler (requirements confirmation)
  gate_ambiguity.go               Gate 2 handler (ambiguity resolution)
  discovery_output_schema.go      JSON schema for discovery output
  drafter_output_schema.go        JSON schema for drafter output
  holdout_output_schema.go        JSON schema for holdout test dataset
  taskgraph_validation.go         Task graph DAG and schema validation
  agent_output.go                 Agent output types and validation

internal/codereview/
  orchestrator.go                 Code review workflow loop
  orchestrator_review.go          Code review dispatch
  orchestrator_fix.go             Automated fix application
  orchestrator_gates.go           Human gate handling for code review
  statemachine.go                 Code review state machine (CR_ states)
  types.go                        Code review types (CRState, findings, fixes)
  config.go                       Code review configuration
  convergence.go                  Severity-based convergence routing
  persistence.go                  Code review state persistence
  recovery.go                     Crash recovery for code review
  events.go                       Code review event types
  prompts.go                      Code review prompt construction
  dedup.go                        Finding deduplication
  fix_output.go                   Fix output parsing
  audit.go                        Audit trail logging

static/
  index.html                      Dashboard HTML
  app.js                          Dashboard JavaScript (SPA)
  style.css                       Dashboard styles
```

## Persistence

### Workflow State

State is persisted to `workspace/specs/{feature}/workflow-state.json` via atomic write (temp file + rename). On server restart, the system resumes from the persisted state:

- **Gate states** -- Restored automatically; gate panels reappear in the dashboard
- **Agent states** -- If agent output exists on disk, the step is skipped (crash recovery); otherwise the agent is re-dispatched

### Telemetry Metrics

OTEL telemetry from Claude Code is persisted to `workspace/metrics.db` (SQLite, WAL mode):

- **Aggregate counters** per feature: tokens, cost, API calls -- upserted on every OTEL update
- **Individual events**: tool invocations and API calls with duration, cost, timestamp
- **90-day retention** with automatic cleanup on startup
- **Survives browser refresh and server restart** -- in-memory accumulators restored from SQLite

### Workspace Layout

```
workspace/
  metrics.db                       SQLite telemetry database
  source-docs/                     Uploaded reference documents (global library)
  specs/{feature}/
    source-docs/                   Per-workflow document copies
    workflow-state.json            Persisted workflow state
    workflow-log.jsonl             Structured workflow log

    # Discovery phase
    discovery-output.json          Canonical discovery output
    discovery-output-{N}.json      Versioned discovery output (per correction round)
    discovery-output-claude-v{N}.json   Per-provider Claude output (dual-provider)
    discovery-output-codex-v{N}.json    Per-provider Codex output (dual-provider)
    discovery-output-merged-v{N}.json   Agent-merged output (dual-provider)

    # Human gate artefacts
    gate1-corrections.json         Latest human corrections
    gate1-corrections-{N}.json     Versioned corrections (per round)
    user-answers.json              Human answers to open questions
    human-comments.json            Free-text reviewer comments (appended)

    # Drafting phase
    drafter-output.json            Canonical drafter output
    drafter-output-claude-v{N}.json    Per-provider Claude draft (dual-provider)
    drafter-output-codex-v{N}.json     Per-provider Codex draft (dual-provider)
    drafter-output-combined-v{N}.json  Combined draft output (dual-provider)
    spec-v0.md                     Initial spec draft
    spec-v{N}.md                   Revised spec (per round)
    {feature}-holdouts.md          Holdout test dataset

    # Review/revise/judge loop
    review-{a,b,c,d}-round-{N}.json   Reviewer outputs per round per lens group
    merged-findings-round-{N}.json     Merged findings per round
    revision-round-{N}.json        Revision output per round
    judge-round-{N}.json           Judge output per round

    # Gate 2
    gate2-resolutions.json         Ambiguity resolutions

    # Finalized output
    spec-final.md                  Finalized specification

  .tasks/
    {feature}.task.json            Structured task graph
    task-review-claude-v{N}.json   Task reviewer output (Claude)
    task-review-codex-v{N}.json    Task reviewer output (Codex)
    task-findings-round-{N}.json   Merged task findings per round
```

## Testing

```bash
go test ./...
```

Test files cover all major components including the state machine, orchestrator, convergence protocol, circuit breakers, issue lifecycle, agent output validation, prompt construction, persistence, recovery, resume, rewind, replay, security, configuration, JSON validation+retry, discovery resume, code review state machine, and all HTTP/WebSocket handlers.

## Development

### Project Structure

- `internal/specworkflow/` -- Core spec workflow engine (pure Go, no HTTP dependencies)
- `internal/codereview/` -- Code review workflow engine (pure Go, no HTTP dependencies)
- `internal/api/` -- HTTP/WebSocket/gRPC layer (depends on specworkflow and codereview)
- `cmd/specworkflow/` -- CLI entry point (depends on api)
- `static/` -- Dashboard frontend (vanilla JS, no build step)

### Adding a Review Lens

1. Add the lens code to `lensGroupMap` in `prompts.go`
2. Add the lens description to `review-constitution.md` in the grill-spec skill
3. Assign the lens to a reviewer group (or create a new reviewer in `team.go`)

### WebSocket Events

The dashboard receives real-time updates via WebSocket event types:

`spec_version`, `issue_update`, `convergence_update`, `gate_request`, `gate_response`, `circuit_breaker`, `agent_error`, `state_transition`, `agent_dispatch`, `agent_complete`, `workflow_status`, `agent_metrics`, `agent_tool_event`, `agent_api_event`

## License

Proprietary. All rights reserved.
