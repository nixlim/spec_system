# Operations Manual

This guide covers day-to-day operation of the Adversarial Spec System: installation, configuration, running workflows, responding to gates, and troubleshooting.

---

## Installation

### 1. Install Required Dependencies

#### Go

The server is a Go binary. You need Go 1.21 or later.

```bash
# Check your version
go version
# Expected: go version go1.21+ ...
```

Download from [golang.org/dl](https://golang.org/dl/) if needed.

#### Claude CLI

All AI agent work runs through the Claude CLI. Install it and authenticate:

```bash
npm install -g @anthropic-ai/claude-code
claude --version    # Verify installation
claude auth login   # Authenticate (if not already done)
```

The server spawns `claude` as a subprocess. If `claude` is not on your PATH or authentication has expired, agents will fail.

#### Skill Directories

The system needs two skill directories that contain the spec templates and review rules. These are typically found in your Claude skills directory (`~/.claude/skills/`). The server auto-discovers them there.

If you need to install them manually, each directory must contain:

**plan-spec/**
- `spec-template.md` — Specification document format
- `bdd-template.md` — BDD scenario format (Given/When/Then)
- `test-dataset-template.md` — Test dataset format

**grill-spec/**
- `review-constitution.md` — Review lenses and quality criteria
- `report-template.md` — Judge report format

Point to them explicitly in `config.yaml` if they are not in a standard location:

```yaml
skill_paths:
  plan_spec: "/path/to/plan-spec"
  grill_spec: "/path/to/grill-spec"
```

### 2. Install Optional Dependencies

These are optional. The system detects them at startup and silently skips their features if missing.

#### Codex CLI (dual-provider mode)

Runs GPT models in parallel with Claude for richer, more diverse outputs.

```bash
# Install per OpenAI documentation
codex --version    # Verify
```

When detected, the server logs: `[orchestrator] codex CLI detected — dual-provider review enabled`

Enable dual-provider phases in `config.yaml`:

```yaml
enable_codex_reviewers: true   # Adds Codex reviewers + holdout generation
enable_codex_discovery: false  # Adds Codex discovery (heavier, optional)
enable_codex_drafting: false   # Adds Codex drafting (heavier, optional)
codex_model: "gpt-5.4"        # Which GPT model to use
```

#### bd (Beads) — Issue Tracking

Beads is an issue tracking system backed by Dolt. When installed, the orchestrator creates epics, finding issues, and gate proxy tasks automatically.

```bash
# Install per Beads documentation
bd --version       # Verify
bd ready           # Initialise a .beads workspace in the current directory
```

When detected, the server logs: `[orchestrator] Beads integration enabled`

When not detected: `[orchestrator] Beads integration disabled: bd not found` — the workflow continues normally without any Beads features.

See [Beads Integration](#beads-integration) for details on what gets tracked.

#### taskval — Task Graph Validation

Validates the task JSON graph produced by the taskify agent against schema and DAG rules.

```bash
taskval --version  # Verify
```

When installed, the orchestrator runs `taskval --mode=graph <path>` after each taskify attempt. Validation errors are fed back into the prompt for the next retry.

### 3. Build the Server

```bash
git clone <repo>
cd adversarial-spec-system
go build -o specworkflow ./cmd/specworkflow
```

### 4. Create a Config File

Copy the example below and adjust to your environment. A minimal config only needs skill paths:

```yaml
skill_paths:
  plan_spec: "~/.claude/skills/plan-spec"
  grill_spec: "~/.claude/skills/grill-spec"
```

See the [full config reference](#configuration-reference) for all options.

### 5. Start the Server

```bash
./specworkflow --config config.yaml --workspace ./workspace
```

Open `http://localhost:8080` in your browser.

**Startup output you should see:**

```
[server] listening on :8080
[orchestrator] Beads integration enabled         ← if bd is installed
[orchestrator] codex CLI detected — dual-provider review enabled  ← if codex installed
[server] skills loaded: planSpec="..." grillSpec="..."
```

---

## Running a Spec Workflow

### Step 1 — Upload Source Documents

Upload the documents that describe the system you want to spec (PRDs, design docs, prior specs, architecture notes):

1. Open the **Controls** tab
2. Drag and drop files into the upload area, or click "Choose Files"
3. Documents are saved to `workspace/source-docs/`

Supported formats: Markdown, plain text, PDF, or any text-based format.

### Step 2 — Start the Workflow

1. In the **Controls** tab, select **Spec Workflow**
2. Enter a **feature name** — alphanumeric, hyphens, underscores, no spaces (e.g. `user-auth`, `payment_flow`)
3. Enter a **goal** — a short description of what the spec should cover
4. Optionally provide a **code path** — a local repository path for codebase-aware discovery
5. Select which source documents to include
6. Click **Start Workflow**

The pipeline stepper appears and DISCOVERY begins.

### Step 3 — Wait for Gate 1

Discovery agents read your source documents and extract actors, scope, constraints, assumptions, and open questions. When complete, the workflow pauses at **Gate 1** and a gate panel appears at the top of the page.

**At Gate 1:**
- Review the extracted requirements, actors, constraints, and open questions
- In dual-provider mode, you see per-provider outputs side-by-side with the merged result
- Answer open questions the discovery agent raised
- Correct any misunderstandings using the inline edit fields
- Add free-text comments for context the agent missed
- Click **Approve** to proceed, or provide corrections and click **Re-run Discovery** (up to 3 correction rounds)

### Step 4 — Wait for Gate 2

After you approve Gate 1, the drafter produces a full specification document and holdout test dataset. The workflow pauses at **Gate 2**.

**At Gate 2:**
- Review ambiguity warnings flagged by the drafter
- For each warning: **Accept** (acknowledge the assumption), **Answer** (provide a clarification), or **Defer** (skip for now)
- Click **Approve** to begin the adversarial review loop

### Step 5 — Monitor the Review Loop

The system runs multiple review/revise/judge iterations:

- **REVIEWING**: 4 reviewer agents run in parallel (+ Codex reviewers if enabled), each examining the spec through specific lenses
- **REVISING**: A reviser agent addresses findings. If the previous judge BLOCKed the revision, the reviser receives the judge's full rationale and must address every flagged issue
- **JUDGING**: The judge reads the live issue tracker (with current statuses — not a stale snapshot) and renders PASS, REVISE, or BLOCK

Watch progress in the **Convergence** tab. The **Issues** tab shows each finding with its current status, the round it was raised, and the round it was closed.

The loop ends when:
- Judge renders PASS (all CRIT/MAJ resolved, convergence criteria met)
- A circuit breaker trips (max rounds, cost, staleness, etc.)

### Step 6 — Gate Final (if triggered)

This gate appears only if critical findings remain when the judge would otherwise PASS. Review the open findings and either approve (proceed to finalized despite findings) or reject.

### Step 7 — Task Decomposition

After finalization, the taskify agent decomposes the spec into a structured task graph. This uses validation+retry (up to `taskify_max_retries` attempts) to ensure the output is a valid DAG with correct schema.

If `taskval` is installed, each attempt is also validated by `taskval --mode=graph`. Errors are fed back into the agent's next prompt.

### Step 8 — Task Gate

Review the task graph and either approve or request re-decomposition with corrections.

### Step 9 — Retrieve Outputs

When the workflow reaches COMPLETE:

| Output | Path |
|--------|------|
| Finalized specification | `workspace/specs/{feature}/spec-final.md` |
| Holdout test scenarios | `workspace/specs/{feature}/{feature}-holdouts.md` |
| Task graph | `workspace/.tasks/{feature}.task.json` |
| All review artefacts | `workspace/specs/{feature}/` |

---

## Running a Code Review Workflow

### Start

1. In **Controls**, select **Code Review**
2. Enter a feature name and the path to the code being reviewed
3. Click **Start Workflow**

### Gates

**Scope Gate**: Review the proposed review scope and confirm before agents begin.

**Fixes Gate**: Review automated fix proposals. Accept, reject, or modify individual fixes. The loop continues until convergence or max rounds.

### Completion

Code reviews end in CR_COMPLETE (all findings resolved) or CR_ESCALATED (max rounds, cost, or human rejection).

---

## Beads Integration

When `bd` is installed and `bd ready` has been run in the working directory, all workflows automatically integrate with Beads.

### What Gets Tracked

| Item | Beads Artefact | Details |
|------|---------------|---------|
| Each workflow run | Epic | Feature name, run ID, start time |
| Each reviewer finding | Child issue (`finding` type) | ID, severity, lens, affected section, round, agent name |
| Human gate | Task issue (gate proxy) | Gate name, feature, close-with-ACCEPT/REJECT instructions |
| Review round | Molecule | Steps: `reviewing` → `revising` → `judging` |
| Workflow state | KV store | Current state, round, run ID, Beads step IDs |
| Judge verdict | Comment | Per-finding verdict explanation posted to each finding issue |

### Using Beads as a Gate Interface

Every human gate creates a Beads task issue. You can approve or reject the gate from the dashboard **or** by closing the Beads task:

```bash
# Approve a gate
bd close <gate-task-id> --reason "ACCEPT: looks good, proceed"

# Reject a gate
bd close <gate-task-id> --reason "REJECT: discovery missed the payments module"
```

The orchestrator polls the task every 5 seconds (configurable via `beads_gate_poll_interval`). Once closed, the workflow immediately continues.

### Checking Workflow State in Beads

```bash
bd show <run-epic-id>           # Overview of the run
bd children <run-epic-id>       # List all findings and gate tasks
bd list --filter status=open    # Open findings across all runs
```

### Finding Statuses in Beads

| Beads Status | Meaning in Workflow |
|-------------|---------------------|
| `open` | Finding is raised, not yet addressed |
| `in-progress` | Reviser is actively addressing |
| `addressed` | Reviser has addressed (pending judge verification) |
| `closed` | Judge verified as resolved |
| `dismissed` | Dismissed by reviser or judge |

### After a Crash or Restart

If the server restarts, the orchestrator rebuilds the in-memory issue tracker from Beads by reading the run epic's children. All finding statuses are restored from Beads. No data is lost.

---

## Multiple Concurrent Workflows

Multiple workflows can run at the same time on different features:

- Start each workflow with a unique feature name
- The **Active Workflows** list on the Controls tab shows all running workflows with type badges (SPEC/CR)
- Click a workflow to switch the dashboard context to it
- Notification badges appear on workflows waiting at gates
- OTEL telemetry is partitioned by feature name

---

## Dual-Provider Mode

When Codex CLI is detected and the relevant config flags are enabled, the system runs Claude and Codex agents in parallel:

**Discovery** (enable via `enable_codex_discovery: true`): Both providers analyse source documents independently. An intelligent merge agent combines outputs. Gate 1 shows per-provider outputs side-by-side with the merged result.

**Drafting** (enable via `enable_codex_drafting: true`): Both produce independent spec drafts. A combine agent merges them into one cohesive specification.

**Review** (enable via `enable_codex_reviewers: true`): Four Claude reviewers run in parallel across lens groups; Codex generates holdout review data. Findings from both are merged and deduplicated.

**Fallback**: If one provider fails, the system uses single-provider output. If the merge/combine agent fails, a mechanical merge is used. The workflow never blocks on a provider outage.

---

## Handling Errors

### Workflow in ERROR State

1. Check the **Messages** tab for error details
2. Click **Resume** to retry from the failed state
3. If the error persists, use **Rewind** to go back to a known-good stage
4. Last resort: **Reset** deletes the workflow directory and starts fresh

### Rewinding a Workflow

**Via the dashboard**: Use the Rewind button and select the target stage and round.

**Manually** (useful for precise control):

```bash
# Edit workflow-state.json directly
vim workspace/specs/{feature}/workflow-state.json
# Change "state" to the desired stage, e.g. "REVISING"
# Save the file, then click Resume in the dashboard
```

The server respects the explicit state in `workflow-state.json` on resume and will not override it with artefact-based inference.

| Target State | What happens on resume |
|--------------|----------------------|
| `REVISING` | Re-runs the reviser. If `judge-round-N.json` has a BLOCK verdict, reviser receives it as feedback |
| `JUDGING` | Re-runs the judge using the live issue tracker (accurate statuses) |
| `REVIEWING` | Re-runs all 4 reviewer groups against the current spec |
| `DRAFTING` | Re-runs drafter(s) from existing discovery output |
| `DISCOVERY` | Re-runs discovery (or prompts smart restart choices if artefacts exist) |
| `TASKIFY` | Re-runs task decomposition from the finalized spec |

### Circuit Breaker Trips → ESCALATED

| Breaker | Cause | Action |
|---------|-------|--------|
| Max rounds | Review loop exceeded `max_rounds` | Increase limit, or Finalize/Rewind |
| Max findings | Too many cumulative findings | Spec may need structural rethink; Reset |
| Staleness | CRIT/MAJ findings not moving across rounds | Rewind to REVISING and intervene |
| Wall clock | Elapsed time exceeded `max_wall_clock_minutes` | Increase limit, or Finalize |
| Cost | API cost exceeded `max_cost_usd` | Increase limit, or Finalize |

For an escalated workflow, your options are:
- **Force Finalize** — Accept the spec as-is with open findings (use the Finalize button)
- **Rewind** — Go back to an earlier stage and try again with adjusted config
- **Reset** — Delete and start over

### Agent Failures

The system automatically retries agent failures up to `max_retries` times, feeding validation errors back into the prompt. If all retries are exhausted:

- The workflow transitions to ERROR or ESCALATED
- The activity feed shows which agent failed, attempt count, and validation errors
- Resume re-dispatches the failed agent

### Server Crash Recovery

If the server crashes or is restarted:

1. All workflow states are automatically restored from `workflow-state.json` files on startup
2. Gate panels reappear in the dashboard for any workflows in gate states
3. If an agent was mid-execution, it will be re-dispatched (agent output files checked for existence)
4. Beads issue tracker is rebuilt from the run epic's children
5. OTEL metrics are restored from SQLite
6. The dashboard reconnects via WebSocket automatically

---

## Workflow Replay

Replay re-runs a sub-phase without re-dispatching the main agents. Useful when a merge or combine step produced bad output but individual agent outputs are fine.

```bash
# Via API
curl -X POST http://localhost:8080/api/workflow/replay \
  -H "Content-Type: application/json" \
  -d '{"feature_name": "my-feature", "phase": "review_merge"}'
```

| Phase | What it re-runs | When to use |
|-------|----------------|-------------|
| `discovery_merge` | Merge agent on existing Claude + Codex discovery outputs | Merge failed, individual outputs are good |
| `drafting_combine` | Combine agent on existing Claude + Codex drafts | Combine failed, individual drafts are good |
| `review_merge` | Findings dedup on existing reviewer outputs | Want different dedup or thresholds |
| `task_review_merge` | Task findings dedup on existing task reviewer outputs | Same, for task review |

You can also edit input files on disk before replaying — for example, fix a malformed per-provider output, then replay to produce a clean merged result.

---

## Configuration Reference

All fields are optional unless noted. Defaults are shown.

### Spec Workflow

```yaml
# Skill directories (required if not auto-discovered)
skill_paths:
  plan_spec: ""    # Path to plan-spec skill directory
  grill_spec: ""   # Path to grill-spec skill directory

# Review loop limits
max_rounds: 5              # Max review/revise/judge iterations
min_rounds: 2              # Min iterations before PASS is accepted
max_total_findings: 60     # Cumulative findings before escalation
staleness_threshold: 2     # Consecutive rounds with no progress before halt

# Budgets
max_wall_clock_minutes: 60
max_cost_usd: 50.0

# Human gates
max_gate_corrections: 3    # Max correction rounds at Gate 1
max_gate2_redrafts: 1      # Max redraft rounds at Gate 2

# Agent reliability
max_retries: 2             # Validation+retry attempts per agent

# Timeouts (seconds)
agent_timeout_seconds: 300      # Discovery, drafting, taskify
reviewer_timeout_seconds: 300   # Reviewer agents
holdout_timeout_seconds: 300    # Holdout generation

# Claude model overrides (empty = CLI default)
claude_models:
  default: ""
  reviewer: ""
  holdout: ""
  reviser: ""
  judge: ""
  discovery: ""
  drafter: ""
  taskify: ""
  task_reviewer: ""
  task_reviser: ""

# Dual-provider (requires codex on PATH)
enable_codex_reviewers: true
enable_codex_discovery: false
enable_codex_drafting: false
codex_model: "gpt-5.4"

# Task decomposition
taskify_max_retries: 3
task_review_max_rounds: 3

# Beads integration (requires bd on PATH)
beads_gate_poll_interval: 5s   # How often to poll gate task status
beads_gate_timeout: 24h        # Warning threshold for open gates
```

### Code Review

```yaml
code_review:
  max_rounds: 3
  max_cost_usd: 50.0
  max_wall_clock_minutes: 120
  fixer_timeout_seconds: 600
  commit_mode: branch_per_round   # or "direct_commit"
  staleness_threshold: 2
  max_retries: 2
  reviewer_timeout_seconds: 300
  claude_models:
    default: ""
    reviewer: ""
    fixer: ""
```

### Presets

**High quality (slower, more expensive):**
```yaml
max_rounds: 7
min_rounds: 3
staleness_threshold: 3
max_cost_usd: 200.0
max_wall_clock_minutes: 180
enable_codex_reviewers: true
enable_codex_discovery: true
enable_codex_drafting: true
```

**Fast iteration (cheaper, fewer rounds):**
```yaml
max_rounds: 3
min_rounds: 1
max_wall_clock_minutes: 20
max_cost_usd: 15.0
enable_codex_reviewers: false
enable_codex_discovery: false
enable_codex_drafting: false
```

**Cost cap (fixed budget):**
```yaml
max_cost_usd: 10.0
max_rounds: 3
enable_codex_reviewers: false
```

---

## Monitoring

### Real-Time

- **Pipeline stepper**: Visual progress through all workflow stages
- **Activity feed**: Timestamped log of agent dispatches, completions, costs, retries
- **Convergence tab**: Round-over-round progress (findings opened/closed)
- **Issues tab**: All findings with severity, status, round raised, round closed

### Telemetry

OTEL telemetry is collected via the gRPC receiver and stored in `workspace/metrics.db`:

- Per-feature aggregate: total tokens (input/output/cache), cost, API call count
- Individual events: tool invocations, API calls, duration, cost
- 90-day auto-retention

To disable telemetry: `--otel-port 0`

### Logs

- **Dashboard Messages tab**: Filterable real-time log by category (OTEL, Orchestrator, Agent Events, etc.)
- **Workflow log**: Structured JSONL at `workspace/specs/{feature}/workflow-log.jsonl`
- **Server log**: Ring buffer at `GET /api/logs/server`

---

## Troubleshooting

| Symptom | Likely Cause | Fix |
|---------|-------------|-----|
| Workflow skips REVISING and jumps to JUDGING on resume | `determineResumeState` sees revision artefact | Edit `workflow-state.json` and set `"state": "REVISING"` directly, then Resume |
| Judge reports 63 open findings, UI shows 10 | Old behaviour (pre-fix); judge was reading frozen merged-findings file | Upgrade to latest build; judge now reads `issue-tracker-round-N.json` with live statuses |
| Active agents panel shows running badges after ESCALATED | Stale restore on page load | Fixed in latest build; terminal states no longer trigger `restoreActiveAgents` |
| Workflow stuck at gate, panel missing | Dashboard lost WebSocket connection | Refresh browser; gate state is persisted and panel will reappear |
| Agent timeout | Claude CLI not responding or auth expired | Run `claude --version`; re-authenticate; Resume |
| "409 conflict" on start | Workflow already running for this feature name | Cancel or wait for the existing workflow to complete |
| No OTEL data in dashboard | OTEL receiver not running | Check `--otel-port` flag; ensure Claude Code telemetry is enabled |
| Missing spec versions after restart | Server restarted mid-agent | Resume — agent will re-run and regenerate output |
| Gate panel disappeared | Browser refresh during gate | Refresh again — gate state is persisted |
| `bd not found` in logs | Beads not installed | Install `bd` and run `bd ready`, or ignore if you don't need issue tracking |
| Codex agents not running | Codex CLI not on PATH | Install Codex CLI; server auto-detects on startup |
| Task graph validation fails repeatedly | DAG cycles or missing required fields | Check Messages tab for `taskval` errors; manually edit task JSON if needed |
| JSON validation failures | Agent wrapping JSON in markdown | System auto-extracts JSON; retry with error feedback happens automatically |
| Discovery re-runs instead of reusing output | Correction loop (Gate 1 corrections) | Expected — correction loops always re-dispatch with human feedback incorporated |
| Skill paths not found | Skills in non-standard location | Set `skill_paths` in `config.yaml` explicitly |

---

## Workspace Management

### Document Library

- Upload via the Controls tab drag-drop area
- Or place files directly in `workspace/source-docs/`
- Assign specific documents to a workflow when starting it

### Cleanup

```bash
# Delete a single workflow (removes workspace/specs/{feature}/)
# Use the delete button on the workflow card in the dashboard, or:
rm -rf workspace/specs/my-feature

# Clear all task artefacts
rm workspace/.tasks/my-feature.task.json

# SQLite metrics are auto-pruned after 90 days
```

### Disk Usage

| Directory | Typical Size | Notes |
|-----------|-------------|-------|
| `workspace/source-docs/` | Varies | Your uploaded documents |
| `workspace/specs/{feature}/` | 1–10 MB | Grows with rounds; spec + all artefacts |
| `workspace/.tasks/` | < 1 MB | Small JSON task graphs |
| `workspace/metrics.db` | < 50 MB | Auto-pruned; grows slowly |
