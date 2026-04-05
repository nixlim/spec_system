# Adversarial Spec System

A multi-agent system that produces high-quality software specifications through adversarial review. Specialised AI agents collaborate and compete — discovering requirements, drafting specs, reviewing through multiple lenses, revising, judging convergence, and decomposing into task graphs — while human gates ensure alignment at critical decision points.

The system supports **dual-provider execution** (Claude + Codex in parallel) across discovery, drafting, and review phases, with intelligent merging of outputs. A separate **code review workflow** provides automated code auditing with fix-review loops. A **code documentation workflow** auto-generates and maintains code documentation.

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
  │    (with judge block feedback   │
  │     when previous revision      │
  │     was BLOCKed)                │
  │            |                    │
  │  [ JUDGING ]                    │
  │    Convergence check            │
  │    Anti-gaming pre-checks       │
  │    BLOCK → back to REVISING     │
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

### Judge Verdicts

| Verdict | Meaning | What happens |
|---------|---------|-------------|
| `PASS` | All findings adequately addressed | Proceeds to FINALIZED |
| `REVISE` | Minor issues remain | Returns to REVIEWING |
| `BLOCK` | Reviser under-delivered; critical findings not addressed | Returns to REVISING with the judge's full rationale as feedback |

A `BLOCK` does **not** escalate. The reviser receives the judge's output file and must address every flagged finding before the next judging round.

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

---

## Quick Start

### Prerequisites

Install these **before** running the installer:

| Dependency | Required | Install |
|------------|----------|---------|
| **Claude CLI** | Yes — runs all AI agents | [claude.ai/install.sh](https://claude.ai/install.sh) |
| **Codex CLI** | No — enables dual-provider mode | [github.com/openai/codex](https://github.com/openai/codex) |

```bash
# Verify Claude is installed and authenticated
claude --version
claude auth login   # if not already done
```

The installer handles everything else (server binary, bd, taskval, skills).

### Install

```bash
curl -fsSL https://raw.githubusercontent.com/nixlim/spec_system/main/install.sh | bash
```

The script:
- Downloads a pre-built binary (no Go required), or builds from source if Go is available
- Installs **bd** (Beads issue tracking) and **taskval** (task graph validation)
- Copies the bundled `plan-spec` and `grill-spec` skills to `~/.claude/skills/`
- Writes a default `config.yaml` and creates the workspace directory

```bash
# Options
./install.sh --help           # All flags
./install.sh --skip-beads     # Skip bd installation
./install.sh --skip-taskval   # Skip taskval installation
./install.sh --dir ~/bin      # Custom binary location
./install.sh --dry-run        # Preview without making changes
```

If `bd` or `taskval` are not on your PATH, those features are silently disabled and the workflow continues without them.

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

---

## Configuration

### Skill Directories

The system requires two Claude skill directories containing the templates that govern spec structure and review criteria:

**plan-spec** must contain:
- `spec-template.md` — Specification format and section structure
- `bdd-template.md` — BDD scenario format
- `test-dataset-template.md` — Test dataset format

**grill-spec** must contain:
- `review-constitution.md` — Review lenses and scoring criteria
- `report-template.md` — Report format for the judge

The server searches for skills in these locations in order:
1. Path given in `config.yaml` under `skill_paths`
2. `~/.claude/skills/`
3. `~/.codex/skills/`
4. `.claude/skills/` (relative to working directory)
5. `.agents/skills/`

### config.yaml Reference

Create a `config.yaml` in your working directory. All fields are optional — sensible defaults apply.

```yaml
# ─────────────────────────────────────────────
# Skill paths (required if not auto-discovered)
# ─────────────────────────────────────────────
skill_paths:
  plan_spec: "/path/to/.claude/skills/plan-spec"
  grill_spec: "/path/to/.claude/skills/grill-spec"

# ─────────────────────────────────────────────
# Review loop limits
# ─────────────────────────────────────────────
max_rounds: 5              # Maximum review/revise/judge iterations (default: 5)
min_rounds: 2              # Minimum iterations required before PASS is accepted (default: 2)
max_total_findings: 60     # Cumulative findings before escalation (default: 60)
staleness_threshold: 2     # Consecutive rounds with no CRIT/MAJ progress before halt (default: 2)

# ─────────────────────────────────────────────
# Budget limits
# ─────────────────────────────────────────────
max_wall_clock_minutes: 60  # Elapsed time budget per workflow (default: 60)
max_cost_usd: 50.0          # API cost budget per workflow (default: 50.0)

# ─────────────────────────────────────────────
# Human gate configuration
# ─────────────────────────────────────────────
max_gate_corrections: 3    # Max correction rounds at Gate 1 (post-discovery) (default: 3)
max_gate2_redrafts: 1      # Max redraft rounds at Gate 2 (post-draft) (default: 1)

# ─────────────────────────────────────────────
# Agent reliability
# ─────────────────────────────────────────────
max_retries: 2             # Retry attempts per agent on validation failure (default: 2)

# ─────────────────────────────────────────────
# Agent timeouts (seconds)
# ─────────────────────────────────────────────
agent_timeout_seconds: 300      # Discovery, drafting, taskify agents (default: 300)
reviewer_timeout_seconds: 300   # Reviewer agents — Claude and Codex (default: 300)
holdout_timeout_seconds: 300    # Holdout generation agents (default: 300)

# ─────────────────────────────────────────────
# Model selection
# ─────────────────────────────────────────────
# Empty string means the Claude CLI picks its default model.
# Set per-role to use different models for different tasks.
claude_models:
  default: ""              # Fallback for any role not explicitly set
  reviewer: ""             # All 4 reviewer agents
  holdout: ""              # Holdout generation agents
  reviser: ""              # Reviser agent
  judge: ""                # Judge agent
  discovery: ""            # Discovery agent
  drafter: ""              # Drafter agent
  taskify: ""              # Taskify agent
  task_reviewer: ""        # Task reviewer agent
  task_reviser: ""         # Task reviser agent

# ─────────────────────────────────────────────
# Dual-provider (Codex CLI) — requires codex on PATH
# ─────────────────────────────────────────────
enable_codex_reviewers: true    # Parallel Codex reviewers + holdout (default: true)
enable_codex_discovery: false   # Parallel Codex discovery agent (default: false)
enable_codex_drafting: false    # Parallel Codex drafting agent (default: false)
codex_model: "gpt-5.4"         # Model ID passed to the Codex CLI (default: "gpt-5.4")

# ─────────────────────────────────────────────
# Task decomposition
# ─────────────────────────────────────────────
taskify_max_retries: 3         # Max validation+retry attempts for task graph (default: 3)
task_review_max_rounds: 3      # Max task review/revision rounds (default: 3)

# ─────────────────────────────────────────────
# Beads issue tracking — requires bd on PATH
# ─────────────────────────────────────────────
# If bd is not installed, these settings have no effect.
beads_gate_poll_interval: 5s   # How often to poll gate task status in Beads (default: 5s)
beads_gate_timeout: 24h        # Advisory warning threshold for gates left open (default: 24h)

# ─────────────────────────────────────────────
# Code review workflow
# ─────────────────────────────────────────────
code_review:
  max_rounds: 3                  # Fix-review iterations (default: 3)
  max_cost_usd: 50.0             # Cost budget (default: 50.0)
  max_wall_clock_minutes: 120    # Time budget (default: 120)
  fixer_timeout_seconds: 600     # Fixer agent timeout (default: 600)
  commit_mode: branch_per_round  # "branch_per_round" or "direct_commit"
  staleness_threshold: 2
  max_retries: 2
  reviewer_timeout_seconds: 300
  claude_models:
    default: ""
    reviewer: ""
    fixer: ""
```

### Minimal Config (Fastest Start)

```yaml
skill_paths:
  plan_spec: "~/.claude/skills/plan-spec"
  grill_spec: "~/.claude/skills/grill-spec"
```

Everything else uses defaults. This gets you a single-provider Claude-only workflow with sensible round limits and budgets.

---

## Dashboard

The web dashboard provides real-time visibility into workflow execution. Multiple workflows can run concurrently, each tracked independently.

### Tabs

- **Controls** — Active workflow list, start new workflows (spec, code review, codedoc), upload source documents, assign documents to workflows, manage workspace
- **Spec** — View and diff spec versions as they evolve through rounds
- **Issues** — Track findings with severity/status/lens filtering; shows round raised and round closed for each finding
- **Convergence** — Monitor review/revision convergence metrics and round history
- **Messages** — Filtered workflow log (OTEL, Orchestrator, Claude Runner, Agent Events, State Transitions)

### Workflow Status Panel

A persistent top panel shows aggregate metrics updated in real-time via WebSocket:

- **Pipeline stepper** — visual chain of all workflow stages with progress indication
- Feature name, round number, workflow state badge, workflow type badge (SPEC/CR)
- Cost (from OTEL telemetry), elapsed wall clock time
- Token usage (input, output, cache read), API call count, agent cost
- Activity feed of individual tool and API events
- Source document list per workflow

### Human Gates

Gate panels appear when the workflow requires human input:

- **Gate 1** — Review discovery output, answer open questions, provide corrections
- **Gate 2** — Resolve ambiguity warnings (accept/answer/defer per warning)
- **Gate Final** — Approve or reject when critical findings persist
- **Task Gate** — Review task graph, approve or request re-decomposition

### Workflow Rewind and Replay

Workflows can be rewound to any previous stage from the UI. Setting the `state` field in `workflow-state.json` directly is also honoured — the system respects an explicit non-terminal state rather than re-deriving it from artefacts on disk.

Individual phases can be **replayed** without re-dispatching agents:
- **Discovery merge** — re-run the intelligent merge from existing per-provider outputs
- **Drafting combine** — re-run the combine agent from existing per-provider drafts
- **Review merge** — re-run findings dedup from existing reviewer outputs
- **Task review merge** — re-run task findings dedup from existing task reviewer outputs

---

## Beads Integration

When `bd` (Beads) is installed and a `.beads/` workspace exists in the working directory, the system automatically enables issue tracking integration.

### What Gets Tracked

| Item | Beads Artefact | Content |
|------|---------------|---------|
| Each workflow run | Epic issue | Feature name, run ID, start time |
| Each reviewer finding | Child issue (type: `finding`) | ID, severity, lens, affected section, round, agent |
| Human gate points | Task issue (gate proxy) | Gate name, feature, accept/reject instructions |
| Review round | Molecule | Steps: reviewing → revising → judging |
| State snapshots | KV store | Current state, round, run ID, step IDs |

### Gate Proxies

When the workflow reaches a human gate (Gate 1, Gate 2, Gate Final, Task Gate), it creates a Beads task issue. The orchestrator polls `bd show <id>` every `beads_gate_poll_interval` (default 5s). Close the task with reason `ACCEPT: <comment>` or `REJECT: <comment>` to advance the workflow.

This mirrors the gate panel in the dashboard — both mechanisms work and either one unblocks the workflow.

### Crash Recovery with Beads

If the server restarts mid-workflow, the orchestrator reads the run epic's children from Beads to rebuild the in-memory issue tracker. All finding statuses are restored from Beads state.

### Graceful Degradation

If `bd` is not on your PATH, Beads integration is silently disabled. The message `[orchestrator] Beads integration disabled: bd not found` appears in the server log. The workflow runs identically without it.

---

## API Reference

### Spec Workflow

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/workflow/start` | Start new workflow |
| POST | `/api/workflow/cancel` | Cancel running workflow |
| GET | `/api/workflow/status` | Poll workflow status |
| POST | `/api/workflow/resume` | Resume from ESCALATED/ERROR state |
| POST | `/api/workflow/rewind` | Rewind to target state and round |
| POST | `/api/workflow/replay` | Replay a specific phase |
| POST | `/api/workflow/finalize` | Force transition to FINALIZED |
| POST | `/api/workflow/reset` | Delete feature directory |
| POST | `/api/workflow/restart` | Stop, delete, and restart |
| POST | `/api/workflow/retry` | Clear stale state file |
| GET | `/api/workflow/agents` | List active agents |

### Code Review

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/codereview/start` | Start new code review |
| GET | `/api/codereview/{feature}/status` | Poll code review status |
| POST | `/api/codereview/{feature}/gate` | Submit gate decision |
| POST | `/api/codereview/{feature}/cancel` | Cancel running code review |
| POST | `/api/codereview/{feature}/resume` | Resume from ERROR state |
| POST | `/api/codereview/{feature}/reset` | Delete code review feature directory |

### Source Documents

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/upload` | Upload documents to global library |
| GET | `/api/uploads` | List uploaded files |
| POST | `/api/workflow/{feature}/source-docs` | Assign documents to a workflow |
| GET | `/api/workflow/{feature}/source-docs` | List assigned documents |

### Gates

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/tasks/{id}/approve` | Approve gate (with corrections/resolutions) |
| POST | `/api/tasks/{id}/reject` | Reject gate (cancel workflow) |

### Data Access

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/workspace/features` | List all features with metadata |
| GET | `/api/workspace/features/{name}/state` | Feature workflow state |
| GET | `/api/workspace/features/{name}/files/{f}` | Specific feature file |
| GET | `/api/spec/*` | Spec versions, diffs, issues, convergence |
| GET | `/api/metrics` | Persisted OTEL telemetry |
| GET | `/api/messages` | Workflow log messages |
| GET | `/api/logs/server` | Server log ring buffer |
| GET | `/ws` | WebSocket event stream |

---

## Architecture

```
cmd/specworkflow/main.go          CLI entry point, HTTP routing

internal/api/
  workflow_handler.go             HTTP handlers, WorkflowManager
  codereview_handlers.go          Code review HTTP handlers
  codedoc_handlers.go             Code documentation HTTP handlers
  otel_receiver.go                OTLP gRPC receiver for Claude telemetry
  metrics_store.go                SQLite persistence for telemetry
  websocket.go                    WebSocket hub and broadcasting
  spec_endpoints.go               Spec/issue/convergence REST endpoints

internal/specworkflow/
  orchestrator.go                 Main workflow loop and state coordination
  orchestrator_discovery.go       Discovery phase + Gate 1
  orchestrator_drafting.go        Drafting phase + Gate 2
  orchestrator_review.go          Review dispatch + revision + judging
  orchestrator_beads.go           Beads integration: epics, findings, gates, molecules
  orchestrator_taskify.go         Task graph decomposition + validation loop
  orchestrator_task_review.go     Task review/revision loop
  statemachine.go                 State machine with guarded transitions
  claude_runner.go                Claude CLI subprocess execution
  codex_runner.go                 Codex CLI subprocess execution
  beads_client.go                 Beads CLI client (BeadsClientInterface + BeadsClient)
  beads_client_mock.go            Mock Beads client for tests
  issues.go                       Issue tracker with lifecycle transitions + ExportLiveState
  prompts.go                      Prompt construction for all agents
  convergence.go                  Anti-gaming pre-checks and convergence
  breakers.go                     Circuit breaker evaluation
  config.go                       Configuration parsing and validation
  types.go                        Core type definitions and workflow states
  recovery.go                     Agent failure detection and retry

static/
  index.html                      Dashboard HTML
  app.js                          Dashboard JavaScript (SPA)
  style.css                       Dashboard styles
```

---

## Persistence

### Workspace Layout

```
workspace/
  metrics.db                       SQLite telemetry database
  source-docs/                     Uploaded reference documents
  specs/{feature}/
    source-docs/                   Per-workflow document copies
    workflow-state.json            Persisted workflow state (edit to rewind)
    workflow-log.jsonl             Structured workflow log

    # Discovery phase
    discovery-output.json
    discovery-output-claude-v{N}.json
    discovery-output-codex-v{N}.json
    discovery-output-merged-v{N}.json

    # Drafting phase
    spec-v0.md                     Initial draft
    spec-v{N}.md                   Revised spec per round
    {feature}-holdouts.md

    # Review/revise/judge loop
    review-{a,b,c,d}-round-{N}.json
    merged-findings-round-{N}.json   Frozen findings snapshot (all statuses = open)
    issue-tracker-round-{N}.json     Live tracker state (accurate statuses) — used by judge
    revision-round-{N}.json
    judge-round-{N}.json

    # Finalized output
    spec-final.md

  .tasks/
    {feature}.task.json            Structured task graph
```

**Rewinding manually:** Edit `workflow-state.json` and change `"state"` to the desired active state (e.g. `"REVISING"`). The server respects this on resume and will not override it with artefact-based inference.

### Telemetry

OTEL telemetry from Claude Code is persisted to `workspace/metrics.db`:

- Aggregate token/cost counters per feature (upserted on every OTEL update)
- Individual tool invocations and API calls with duration and cost
- 90-day retention with automatic cleanup on startup

---

## Testing

```bash
go test ./...
```

Test coverage includes: state machine, orchestrator, convergence, circuit breakers, issue lifecycle, agent output validation, prompt construction, persistence, recovery, resume, rewind, replay, security, configuration, JSON validation+retry, Beads client and integration, discovery resume, code review state machine, and HTTP/WebSocket handlers.

---

## Development

### Project Structure

- `internal/specworkflow/` — Core spec workflow engine (pure Go, no HTTP dependencies)
- `internal/codereview/` — Code review workflow engine
- `internal/codedoc/` — Code documentation workflow engine
- `internal/api/` — HTTP/WebSocket/gRPC layer
- `cmd/specworkflow/` — CLI entry point
- `static/` — Dashboard frontend (vanilla JS, no build step)

### WebSocket Event Types

`spec_version`, `issue_update`, `convergence_update`, `gate_request`, `gate_response`, `circuit_breaker`, `agent_error`, `state_transition`, `agent_dispatch`, `agent_complete`, `workflow_status`, `agent_metrics`, `agent_tool_event`, `agent_api_event`

---

## License

Proprietary. All rights reserved.
