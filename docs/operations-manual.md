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
5. Serves the dashboard and API

## Running a Workflow

### 1. Upload Source Documents

Upload reference documents (PRDs, design docs, prior specs) via the **Controls** tab:

- Click "Choose Files" in the upload area, or drag and drop
- Documents are stored in `workspace/source-docs/` (the global library)
- Supported formats: any text-based format (Markdown, PDF, plain text)
- Organise with folders if desired — the upload preserves directory structure

### 2. Start a Workflow

From the **Controls** tab:

1. Enter a feature name (alphanumeric, hyphens, underscores — no spaces or path separators)
2. Write a goal description (what the spec should cover)
3. Select source documents from the document picker
4. Click "Start Workflow"

The workflow begins in DISCOVERY and the pipeline stepper appears in the status panel.

### 3. Respond to Gates

When the workflow reaches a human gate, the status panel shows a pulsing purple indicator and a gate panel appears at the top of the page.

**Gate 1 (Post-Discovery):**
- Review the extracted requirements, actors, constraints, and open questions
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

### 4. Monitor Progress

While agents are working:

- The **pipeline stepper** shows which stage is active and which are complete
- The **activity feed** logs individual agent dispatches and completions
- The **Spec** tab updates with new versions as revisions happen
- The **Issues** tab shows findings as reviewers submit them
- The **Convergence** tab tracks round-over-round progress

### 5. Retrieve the Final Spec

When the workflow reaches FINALIZED:

- The final spec is at `workspace/specs/{feature}/spec-v{N}.md`
- The holdout test dataset is at `workspace/specs/{feature}/{feature}-holdouts.md`
- All review artefacts (findings, revisions, judge reports) are preserved alongside

## Running Multiple Workflows

Multiple workflows can run concurrently on different features:

- Start each workflow with a unique feature name
- The **Active Workflows** list on the Controls tab shows all running workflows
- Click a workflow to switch the dashboard context to it
- Notification badges appear on workflows waiting at gates
- Each workflow has its own isolated source documents, state, and artefacts
- OTEL telemetry is partitioned by feature name

## Handling Errors

### Workflow Stuck or Errored

If a workflow enters ERROR state:

1. Check the **Messages** tab for the error details
2. Use **Resume** to retry from the failed state
3. If the error persists, use **Rewind** to go back to a known-good stage
4. As a last resort, use **Reset** to delete the workflow and start fresh

### Agent Failures

The system automatically retries transient agent failures (up to `max_retries`, default 2). If all retries are exhausted:

- The workflow transitions to ERROR
- The activity feed shows which agent failed and why
- Resume will re-dispatch the failed agent

### Circuit Breaker Trips

When a circuit breaker fires, the workflow transitions to ESCALATED:

- **Max rounds**: The review loop exceeded the configured maximum
- **Max findings**: Too many cumulative findings (spec may be fundamentally flawed)
- **Staleness**: Critical/major findings not being resolved across rounds
- **Wall clock**: Time budget exceeded
- **Cost**: API cost budget exceeded

For escalated workflows, review the state and decide whether to:
- **Finalize** — Accept the spec as-is (use the Force Finalize button)
- **Rewind** — Go back to an earlier stage and try again
- **Reset** — Start over from scratch

### Server Crash Recovery

If the server crashes or is restarted:

- All workflow states are automatically restored from `workflow-state.json` files
- Gate states resume — gate panels reappear in the dashboard
- If an agent was mid-execution, it will be re-dispatched (idempotent)
- OTEL metrics are restored from SQLite
- The dashboard reconnects via WebSocket automatically

## Workflow Rewind

To rewind a workflow to an earlier stage:

1. Find the workflow in the Active Workflows list
2. Select the target stage from the rewind dropdown (DISCOVERY, DRAFTING, REVIEWING, REVISING, JUDGING)
3. Enter the target round number (for review-loop stages)
4. Click "Rewind"

The system preserves prerequisite artefacts and deletes everything downstream:

| Target | Preserved | Deleted |
|--------|-----------|---------|
| DISCOVERY | State files only | Everything else |
| DRAFTING | Discovery output, Gate 1 corrections | Draft, reviews, revisions |
| REVIEWING | Specs through v{round-1}, prior round reviews | Current round reviews onward |
| REVISING | Current round reviews + merged findings | Revision, judge output |
| JUDGING | Current round revision | Judge output |

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
- **SQLite metrics** in `workspace/metrics.db` (grows slowly, auto-pruned)

## Configuration Tuning

### Cost Control

Reduce API costs by tightening limits:

```yaml
max_rounds: 3          # Fewer review cycles
max_cost_usd: 20.0     # Lower cost cap
max_wall_clock_minutes: 30
```

### Quality vs Speed

For higher quality specs at the cost of more time and money:

```yaml
max_rounds: 5          # Allow more review cycles
min_rounds: 3          # Require at least 3 rounds
staleness_threshold: 3  # More patience for finding resolution
```

For faster iteration with acceptable quality:

```yaml
max_rounds: 3
min_rounds: 1
max_wall_clock_minutes: 20
max_cost_usd: 15.0
```

### Gate Corrections

If discovery frequently needs correction:

```yaml
max_gate_corrections: 5  # Allow more back-and-forth at Gate 1
```

## Monitoring

### Real-Time

- **Dashboard pipeline stepper**: Visual progress through workflow stages
- **Activity feed**: Timestamped log of agent dispatches, completions, state transitions
- **WebSocket events**: 14 event types for programmatic monitoring

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
| Missing spec versions | Server restarted mid-agent | Resume workflow — agent will re-run |
| Gate panel disappeared | Browser refresh during gate | Refresh again — gate state is persisted and will restore |
| Pipeline shows ERROR | Agent failed after retries | Check Messages tab for details; Resume or Rewind |
