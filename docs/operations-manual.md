# Operations Manual

This guide covers day-to-day operation of the Adversarial Spec System: starting workflows, responding to gates, handling errors, and managing the workspace.

## Starting the Server

```bash
# Build
go build -o specworkflow ./cmd/specworkflow

# Run with config
./specworkflow --config config.yaml --workspace ./workspace

# Custom port, OTEL disabled
./specworkflow --config config.yaml --workspace ./workspace --port 9090 --otel-port 0
```

The server starts on `http://localhost:8080` (default). The dashboard opens in any browser.

On startup, the server:
1. Loads configuration from the YAML file
2. Initialises the SQLite metrics database (`workspace/metrics.db`)
3. Starts the OTEL gRPC receiver (if enabled) for Claude Code telemetry
4. Scans `workspace/specs/` for any persisted workflow states and resumes them
5. Detects Codex CLI availability for dual-provider mode (if configured)
6. Serves the dashboard and API

## Running a Spec Workflow

### 1. Upload Source Documents

Upload reference documents (PRDs, design docs, prior specs) via the **Controls** tab:

- Click "Choose Files" in the upload area, or drag and drop
- Documents are stored in `workspace/source-docs/` (the global library)
- Supported formats: any text-based format (Markdown, PDF, plain text)
- Organise with folders if desired -- the upload preserves directory structure

### 2. Start a Workflow

From the **Controls** tab:

1. Select "Spec Workflow" as the workflow type
2. Enter a feature name (alphanumeric, hyphens, underscores -- no spaces or path separators)
3. Write a goal description (what the spec should cover)
4. Optionally provide a code path for codebase-aware discovery
5. Select source documents from the document picker
6. Click "Start Workflow"

The workflow begins in DISCOVERY and the pipeline stepper appears in the status panel.

### 3. Respond to Gates

When the workflow reaches a human gate, the status panel shows a pulsing purple indicator and a gate panel appears at the top of the page.

**Gate 1 (Post-Discovery):**
- Review the extracted requirements, actors, constraints, and open questions
- In dual-provider mode, you see a side-by-side comparison of Claude and Codex outputs alongside the merged result
- Answer any open questions the discovery agent raised
- Correct any misunderstandings by editing the inline fields
- Add free-text reviewer comments for context the agent missed
- Click "Approve" to proceed to drafting, or provide corrections (up to 3 rounds)

**Gate 2 (Post-Draft):**
- Review ambiguity warnings flagged by the drafter
- For each warning, choose: Accept (acknowledge), Answer (provide clarification), or Defer (skip for now)
- Add reviewer comments if needed
- Click "Approve" to proceed to review, or request a redraft (1 redraft allowed)

**Gate Final (Critical Findings):**
- This gate only appears if critical findings were raised during review
- Review the remaining open findings and the judge's assessment
- Click "Approve" to finalize despite findings, or "Reject" to escalate

**Task Gate (Post-Task Decomposition):**
- Review the structured task graph decomposed from the finalized spec
- Verify task dependencies form a valid DAG
- Check acceptance criteria quality and goal specificity
- Click "Approve" to accept, "Correct" to provide feedback and re-decompose, or "Complete" to finish the workflow

### 4. Monitor Progress

While agents are working:

- The **pipeline stepper** shows which stage is active and which are complete
- The **activity feed** logs individual agent dispatches, completions, and costs
- The **Spec** tab updates with new versions as revisions happen
- The **Issues** tab shows findings as reviewers submit them
- The **Convergence** tab tracks round-over-round progress

### 5. Retrieve the Final Spec and Tasks

When the workflow reaches COMPLETE:

- The finalized spec is at `workspace/specs/{feature}/spec-final.md`
- The holdout test dataset is at `workspace/specs/{feature}/{feature}-holdouts.md`
- The structured task graph is at `workspace/.tasks/{feature}.task.json`
- All review artefacts (findings, revisions, judge reports) are preserved alongside

## Running a Code Review Workflow

### 1. Start a Code Review

From the **Controls** tab:

1. Select "Code Review" as the workflow type
2. Enter a feature name
3. Provide the path to the code to review
4. Click "Start Workflow"

### 2. Respond to Code Review Gates

**Scope Gate (CR_HUMAN_GATE_SCOPE):**
- Review the proposed review scope
- Confirm or adjust before agents begin reviewing

**Fixes Gate (CR_HUMAN_GATE_FIXES):**
- Review automated fix proposals from the fixer agent
- Accept, reject, or modify individual fixes
- The loop continues until convergence or max rounds

### 3. Code Review Completion

Code reviews end in either CR_COMPLETE (all findings resolved) or CR_ESCALATED (max rounds, cost limit, or human rejection).

## Running Multiple Workflows

Multiple workflows can run concurrently on different features:

- Start each workflow with a unique feature name
- The **Active Workflows** list on the Controls tab shows all running workflows with type badges (SPEC/CR)
- Click a workflow to switch the dashboard context to it
- Notification badges appear on workflows waiting at gates
- Each workflow has its own isolated source documents, state, and artefacts
- OTEL telemetry is partitioned by feature name

## Dual-Provider Mode

When Codex CLI is available and the relevant config flags are enabled, the system runs agents from both providers in parallel:

### Discovery (dual-provider)

Both Claude and Codex analyse the source documents independently. An intelligent merge agent combines their outputs, preferring richer descriptions and deduplicating actors, priorities, and open questions. The human gate shows per-provider outputs alongside the merged result.

### Drafting (dual-provider)

Both providers produce independent spec drafts. A combine agent merges them into a single cohesive specification, preserving BDD scenarios and test datasets from both.

### Review (dual-provider)

Four Claude reviewer agents run in parallel across lens groups, while Codex generates holdout review data. Findings from both providers are merged and deduplicated before entering the revise/judge loop.

### Fallback Behaviour

If one provider fails, the system falls back to single-provider output. If the merge/combine agent fails, a mechanical merge is used as fallback. This ensures the workflow never blocks on a provider outage.

## JSON Validation and Retry

All JSON-producing agents use a validation+retry loop borrowed from the taskval pattern:

1. Agent is dispatched with the prompt
2. Output is validated against the expected JSON schema
3. If invalid: validation errors are appended to the prompt and the agent is re-dispatched
4. This repeats up to `max_retries` attempts (default: 2)
5. If all attempts fail: the workflow escalates or falls back (depending on the phase)

This applies to: discovery, discovery merge, reviser, judge, taskify, and task reviewer agents.

If an agent wraps JSON in markdown code fences or commentary, the system automatically extracts the JSON object before validation.

## Smart Discovery Restart

When rewinding to the discovery phase and existing artefacts are found, the system presents a choice instead of blindly re-dispatching agents:

- **Skip to gate** -- jump directly to HUMAN_GATE_1 with the existing merged output (useful when you just want to re-review)
- **Replay merge** -- re-run the intelligent merge from existing per-provider outputs without re-dispatching discovery agents (useful when the merge failed but individual outputs are good)
- **Restart fresh** -- re-run discovery agents from scratch (the default when you need new analysis)

This gate is skipped during normal correction loops (when the user provides corrections at Gate 1 and the system re-runs discovery with feedback).

## Workflow Replay

Individual sub-phases can be replayed without re-dispatching the main agents:

| Phase | What it does | When to use |
|-------|-------------|-------------|
| `discovery_merge` | Re-runs the merge agent on existing Claude + Codex discovery outputs | Merge failed but individual outputs are good |
| `drafting_combine` | Re-runs the combine agent on existing Claude + Codex drafts | Combine failed but individual drafts are good |
| `review_merge` | Re-runs findings dedup on existing reviewer outputs | Want different dedup strategy or thresholds |
| `task_review_merge` | Re-runs task findings dedup on existing task reviewer outputs | Same as above, for task review |

Use the replay API: `POST /api/workflow/replay` with `{"feature_name": "...", "phase": "discovery_merge"}`.

You can also edit the input files on disk before replaying -- for example, fix a malformed per-provider output, then replay the merge to produce a clean merged result.

## Handling Errors

### Workflow Stuck or Errored

If a workflow enters ERROR state:

1. Check the **Messages** tab for the error details
2. Use **Resume** to retry from the failed state
3. If the error persists, use **Rewind** to go back to a known-good stage
4. As a last resort, use **Reset** to delete the workflow and start fresh

### Agent Failures

The system automatically retries agent failures with validation error feedback (up to `max_retries`, default 2). If all retries are exhausted:

- The workflow transitions to ERROR or ESCALATED
- The activity feed shows which agent failed, the attempt count, and the validation errors
- Resume will re-dispatch the failed agent

### Circuit Breaker Trips

When a circuit breaker fires, the workflow transitions to ESCALATED:

- **Max rounds**: The review loop exceeded the configured maximum
- **Max findings**: Too many cumulative findings (spec may be fundamentally flawed)
- **Staleness**: Critical/major findings not being resolved across rounds
- **Wall clock**: Time budget exceeded
- **Cost**: API cost budget exceeded

For escalated workflows, review the state and decide whether to:
- **Finalize** -- Accept the spec as-is (use the Force Finalize button)
- **Rewind** -- Go back to an earlier stage and try again
- **Reset** -- Start over from scratch

### Server Crash Recovery

If the server crashes or is restarted:

- All workflow states are automatically restored from `workflow-state.json` files
- Gate states resume -- gate panels reappear in the dashboard
- If an agent was mid-execution, it will be re-dispatched (idempotent)
- OTEL metrics are restored from SQLite
- The dashboard reconnects via WebSocket automatically

## Workflow Rewind

To rewind a workflow to an earlier stage:

1. Find the workflow in the Active Workflows list
2. Select the target stage from the rewind dropdown (DISCOVERY, DRAFTING, REVIEWING, REVISING, JUDGING, TASKIFY)
3. Enter the target round number (for review-loop stages)
4. Click "Rewind"

All artefact files are preserved on disk -- rewind only changes the workflow state so the orchestrator re-runs from the target stage. Agents overwrite their output files when they run, producing fresh results against existing inputs.

| Target | What happens |
|--------|-------------|
| DISCOVERY | Re-runs discovery agents (or offers smart restart choices if artefacts exist) |
| DRAFTING | Re-runs drafter(s) using existing discovery output |
| REVIEWING | Re-runs all 4 reviewer groups against the current spec version |
| REVISING | Re-runs the reviser against current round findings |
| JUDGING | Re-runs the judge against current round revision |
| TASKIFY | Re-runs task decomposition from the finalized spec |

## Managing the Workspace

### Document Library

- Upload documents via the Controls tab or place them directly in `workspace/source-docs/`
- Documents can be organised into folders
- Assign specific documents to workflows via the document picker when starting a workflow, or via the per-workflow source docs API

### Cleaning Up

- **Delete a workflow**: Click the delete button on the workflow card (removes the entire `workspace/specs/{feature}/` directory)
- **Reset a workflow**: Use the Reset button to delete and optionally restart
- **Metrics cleanup**: The system automatically purges telemetry events older than 90 days

### Disk Space

The main consumers of disk space are:
- **Source documents** in `workspace/source-docs/`
- **Spec artefacts** in `workspace/specs/{feature}/` (typically 1-5 MB per feature)
- **Task artefacts** in `workspace/.tasks/` (small JSON files)
- **SQLite metrics** in `workspace/metrics.db` (grows slowly, auto-pruned)

## Configuration Tuning

### Cost Control

Reduce API costs by tightening limits:

```yaml
max_rounds: 3          # Fewer review cycles
max_cost_usd: 20.0     # Lower cost cap
max_wall_clock_minutes: 30
enable_codex_reviewers: false  # Single-provider only
```

### Quality vs Speed

For higher quality specs at the cost of more time and money:

```yaml
max_rounds: 5          # Allow more review cycles
min_rounds: 3          # Require at least 3 rounds
staleness_threshold: 3  # More patience for finding resolution
enable_codex_discovery: true   # Dual-provider discovery
enable_codex_drafting: true    # Dual-provider drafting
```

For faster iteration with acceptable quality:

```yaml
max_rounds: 3
min_rounds: 1
max_wall_clock_minutes: 20
max_cost_usd: 15.0
enable_codex_reviewers: false
```

### Gate Attempts

If discovery frequently needs correction, or if drafts need more revision:

```yaml
max_gate_corrections: 5  # Allow more back-and-forth at Gate 1 (default: 3)
max_gate2_redrafts: 2    # Allow more redrafts at Gate 2 (default: 1)
```

### Agent Timeouts

For long-running agents or large codebases:

```yaml
agent_timeout_seconds: 600       # Discovery, drafting, taskify (default: 300)
reviewer_timeout_seconds: 600    # Review agents (default: 300)
```

### Task Decomposition

For task graph quality:

```yaml
taskify_max_retries: 5      # More attempts for valid task output (default: 3)
task_review_max_rounds: 5   # More review rounds for tasks (default: 3)
```

## Monitoring

### Real-Time

- **Dashboard pipeline stepper**: Visual progress through workflow stages
- **Activity feed**: Timestamped log of agent dispatches, completions, costs, and validation retries
- **WebSocket events**: Event types for programmatic monitoring

### Telemetry

- **Cost tracking**: Per-feature and per-agent cost from OTEL telemetry
- **Token usage**: Input, output, and cache-read token counts
- **API calls**: Count and duration of Claude API calls
- **Wall clock time**: Elapsed real time per workflow

### Logs

- **Workflow log**: Structured JSONL at `workspace/specs/{feature}/workflow-log.jsonl`
- **Server log**: Ring buffer accessible via `GET /api/logs/server`
- **Messages tab**: Filterable real-time log in the dashboard

## Troubleshooting

| Symptom | Likely Cause | Fix |
|---------|-------------|-----|
| Workflow stuck at gate | Dashboard not showing gate panel | Refresh browser; check WebSocket connection |
| Agent timeout | Claude CLI not responding | Check Claude CLI auth (`claude --version`); resume workflow |
| "409 conflict" on start | Workflow already running for this feature | Cancel or wait for the existing workflow |
| No OTEL data | OTEL receiver not running | Check `--otel-port` flag; ensure Claude Code has telemetry enabled |
| Missing spec versions | Server restarted mid-agent | Resume workflow -- agent will re-run |
| Gate panel disappeared | Browser refresh during gate | Refresh again -- gate state is persisted and will restore |
| Pipeline shows ERROR | Agent failed after retries | Check Messages tab for details; Resume or Rewind |
| Codex agents not running | Codex CLI not installed | Install Codex CLI; system auto-detects on startup |
| Discovery re-dispatches unnecessarily | Correction loop, not rewind | Expected -- correction loops always re-dispatch with feedback |
| JSON validation failures | Agent producing commentary around JSON | System auto-extracts JSON; retry with error feedback happens automatically |
| Task graph validation fails | DAG cycles or missing fields | System retries up to `taskify_max_retries`; check Messages for specific errors |
