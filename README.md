# Adversarial Spec System

A multi-agent system that produces high-quality software specifications through adversarial review. Eight specialised Claude agents collaborate and compete — drafting, reviewing through multiple lenses, revising, and judging convergence — while human gates ensure alignment at critical decision points.

The system automates the manual loop of running `plan-spec` then `grill-spec` repeatedly, grounded in research showing adversarial multi-agent review overcomes the "Degeneration-of-Thought" problem inherent in single-agent self-reflection.

## How It Works

```
Source Documents
       |
       v
  [ DISCOVERY ]  ──>  Extract actors, scope, constraints, requirements
       |
       v
  [ HUMAN GATE 1 ]  ──>  Confirm / correct requirements (up to 3 corrections)
       |
       v
  [ DRAFTING ]  ──>  Produce spec + holdout test dataset from templates
       |
       v
  [ HUMAN GATE 2 ]  ──>  Resolve ambiguity warnings (1 redraft allowed)
       |
       v
  ┌─────────────────────────────────┐
  │  [ REVIEWING ]                  │
  │    4 parallel reviewer agents   │  Adversarial review loop
  │    8 lenses across 4 groups     │  (2-5 rounds, configurable)
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
```

### Agents

| Agent | Role | Lenses |
|-------|------|--------|
| Discovery | Extracts requirements from source documents | — |
| Drafter | Produces specification and holdout test data | — |
| Reviewer (Clarity) | Ambiguity, Incompleteness | AMB, INC |
| Reviewer (Consistency) | Consistency, Feasibility | CON, FEA |
| Reviewer (Security) | Security, Operability | SEC, OPS |
| Reviewer (Correctness) | Correctness, Complexity | COR, CPX |
| Reviser | Addresses findings from reviewers | — |
| Judge | Evaluates convergence, renders PASS/REVISE/BLOCK verdict | — |

### Convergence Protocol

The judge's PASS verdict is subject to deterministic anti-gaming checks:

- All CRITICAL findings must be closed or dismissed
- Revision change logs must reference every CRITICAL and MAJOR finding
- Minimum round count must be met
- Authority limits per round: max 2 severity downgrades, max 3 dismissals
- Cumulative escalation: total downgrades + dismissals > 5 triggers escalation

### Circuit Breakers

The workflow halts automatically when any limit is exceeded:

- **Max rounds** — round count exceeds configured maximum (default: 5)
- **Max findings** — cumulative finding count exceeds threshold (default: 60)
- **Staleness** — CRITICAL/MAJOR findings stuck for N consecutive rounds (default: 2)
- **Wall clock** — elapsed time exceeds budget (default: 60 minutes)
- **Cost** — cumulative API cost exceeds budget (default: $50)

## Quick Start

### Prerequisites

- Go 1.21+ (built with 1.25)
- [Claude CLI](https://docs.anthropic.com/en/docs/claude-code) installed and authenticated
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

# Optional: workflow limits (defaults shown)
max_rounds: 5              # Maximum review/revise iterations
min_rounds: 2              # Minimum iterations before acceptance
max_total_findings: 60     # Upper bound on cumulative findings
staleness_threshold: 2     # Consecutive stale rounds before halt
max_wall_clock_minutes: 60 # Time budget
max_cost_usd: 50.0         # Cost budget
max_gate_corrections: 3    # Max human corrections at Gate 1
max_retries: 2             # Agent retry attempts for transient failures
```

### Skill Directories

The system requires two Claude skill directories containing the templates that govern spec structure and review criteria:

- **plan-spec**: Must contain `spec-template.md`, `bdd-template.md`, `test-dataset-template.md`
- **grill-spec**: Must contain `review-constitution.md`, `report-template.md`

## Dashboard

The web dashboard provides real-time visibility into the workflow:

- **Controls** — Start new workflows, upload source documents, manage workspace
- **Spec** — View and diff spec versions as they evolve through rounds
- **Issues** — Track findings with severity filtering and status lifecycle
- **Convergence** — Monitor review/revision convergence metrics
- **Messages** — Raw WebSocket event stream and server logs

### Workflow Status Panel

The top panel shows aggregate metrics updated in real-time via WebSocket:

- Feature name, round number, workflow state
- Cost (from OTEL telemetry), elapsed wall clock time
- Token usage (input, output, cache read), API call count, agent cost
- Activity feed of individual tool and API events

### Human Gates

Gate panels appear when the workflow requires human input:

- **Gate 1** — Review discovery output, answer open questions, provide corrections (editable inline fields), add reviewer comments
- **Gate 2** — Resolve ambiguity warnings (accept/answer/defer per warning), add reviewer comments
- **Gate Final** — Approve or reject when critical findings persist after convergence

## Architecture

```
cmd/specworkflow/main.go          CLI entry point, HTTP routing
internal/api/
  workflow_handler.go             HTTP handlers, WorkflowManager
  otel_receiver.go                OTLP gRPC receiver for Claude telemetry
  metrics_store.go                SQLite persistence for telemetry
  websocket.go                    WebSocket hub and broadcasting
  spec_endpoints.go               Spec/issue/convergence REST endpoints
  upload.go                       Source document upload
  log_stream.go                   Server log and message streaming
internal/specworkflow/
  orchestrator.go                 Main workflow loop and state coordination
  statemachine.go                 State machine with guarded transitions
  orchestrator_discovery.go       Discovery phase + Gate 1 handling
  orchestrator_drafting.go        Drafting phase + Gate 2 handling
  orchestrator_review.go          Review dispatch + revision + judging
  orchestrator_finalize.go        Finalization and output assembly
  claude_runner.go                Claude CLI subprocess execution
  review_dispatch.go              Parallel reviewer dispatch with retry
  prompts.go                      Prompt construction for all agents
  convergence.go                  Anti-gaming pre-checks and convergence
  breakers.go                     Circuit breaker evaluation
  issues.go                       Issue tracker with lifecycle transitions
  skills.go                       Skill template loading and caching
  persistence.go                  Atomic state persistence
  recovery.go                     Agent failure detection and retry
  resume.go                       Crash/restart recovery
  security.go                     Prompt injection mitigation
  config.go                       Configuration parsing and validation
  types.go                        Core type definitions
  events.go                       Event system (12 event types)
  team.go                         Agent team definition
static/
  index.html                      Dashboard HTML
  app.js                          Dashboard JavaScript (SPA)
  style.css                       Dashboard styles
```

## Persistence

### Workflow State

State is persisted to `workspace/specs/{feature}/workflow-state.json` via atomic write (temp file + rename). On server restart, the system resumes from the persisted state:

- **Gate states** — Restored automatically; gate panels reappear in the dashboard
- **Agent states** — If agent output exists on disk, the step is skipped (crash recovery); otherwise the agent is re-dispatched

### Telemetry Metrics

OTEL telemetry from Claude Code is persisted to `workspace/metrics.db` (SQLite, WAL mode):

- **Aggregate counters** per feature: tokens, cost, API calls — upserted on every OTEL update
- **Individual events**: tool invocations and API calls with duration, cost, timestamp
- **90-day retention** with automatic cleanup on startup
- **Survives browser refresh and server restart** — in-memory accumulators restored from SQLite

### Workspace Layout

```
workspace/
  metrics.db                       SQLite telemetry database
  source-docs/                     Uploaded reference documents
  specs/{feature}/
    workflow-state.json            Persisted workflow state
    workflow-log.jsonl             Structured workflow log
    discovery-output.json          Discovery agent output
    gate1-corrections.json         Human corrections (if any)
    user-answers.json              Human answers to open questions
    human-comments.json            Free-text reviewer comments
    drafter-output.json            Drafter agent output
    spec-v0.md                     Initial spec draft
    spec-v{N}.md                   Revised spec (per round)
    {feature}-holdouts.md          Holdout test dataset
    review-{a,b,c,d}-round-{N}.json  Reviewer outputs per round
    merged-findings-round-{N}.json    Merged findings per round
    revision-round-{N}.json        Revision output per round
    judge-round-{N}.json           Judge output per round
```

## Testing

```bash
go test ./...
```

32 test files cover all major components including the state machine, orchestrator, convergence protocol, circuit breakers, issue lifecycle, agent output validation, prompt construction, persistence, recovery, resume, security, configuration, and all HTTP/WebSocket handlers.

## Development

### Project Structure

- `internal/specworkflow/` — Core workflow engine (pure Go, no HTTP dependencies)
- `internal/api/` — HTTP/WebSocket/gRPC layer (depends on specworkflow)
- `cmd/specworkflow/` — CLI entry point (depends on api)
- `static/` — Dashboard frontend (vanilla JS, no build step)

### Adding a Review Lens

1. Add the lens code to `lensGroupMap` in `prompts.go`
2. Add the lens description to `review-constitution.md` in the grill-spec skill
3. Assign the lens to a reviewer group (or create a new reviewer in `team.go`)

### WebSocket Events

The dashboard receives real-time updates via 14 WebSocket event types:

`spec_version`, `issue_update`, `convergence_update`, `gate_request`, `gate_response`, `circuit_breaker`, `agent_error`, `state_transition`, `agent_dispatch`, `agent_complete`, `workflow_status`, `agent_metrics`, `agent_tool_event`, `agent_api_event`

## License

Proprietary. All rights reserved.
