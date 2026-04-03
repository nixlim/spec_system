---
name: investigate-logs
description: >
  Investigate workflow issues using the system's log files. Produces a structured
  diagnostic report with root cause analysis, timeline reconstruction, and
  relevant log excerpts. Fire this skill when the user asks "why did it
  escalate", "what happened to workflow X", "check the logs", "why did it fail",
  "what went wrong", "trace the error", "investigate the issue", "debug the
  workflow", "show me the logs for X", "why is it stuck", or any question about
  a past or current workflow run that requires reading log data to answer. Also
  fire when the user pastes log output and asks for an explanation. Output is a
  Markdown diagnostic report with timeline, root cause, and evidence sections.
argument-hint: "[feature-name or keyword to investigate]"
---

# Log Investigation Skill

You diagnose issues with the adversarial spec system and code review workflows
by reading the system's log files. Your job is to find what happened, why, and
present the evidence clearly.

## Log Sources

This system produces three complementary log streams. Always check all three —
they capture different facets of the same events.

### 1. Server Log (full text, persistent)

- **Path**: `{workspace}/server.log`
- **Format**: Go `log.Printf` output — timestamped text lines with `[tag]` prefixes
- **Contains**: Everything — agent dispatch/completion, state transitions, errors,
  circuit breakers, escalation reasons, JSON parse failures, prompt lengths, costs,
  durations, PID info, HTTP routing
- **Tags to know**:
  - `[orchestrator]` — state machine loop iterations, verdict routing
  - `[claude-runner]` — agent process lifecycle (start, exit, output size, cost)
  - `[codex-runner]` — Codex agent lifecycle
  - `[otel]` — cumulative cost/token metrics per feature
  - `[workflow]` — high-level workflow start/complete/cancel
  - `[codereview]` — code review workflow events
  - `[main]` — server startup, config loading
  - `[routing]` — HTTP request dispatch
- **How to read**: Use `Grep` with the feature name or a keyword to find relevant
  sections, then `Read` surrounding context. For chronological reconstruction,
  grep for the feature name and read the matching lines in order.

### 2. Workflow Log (structured JSONL, per-feature)

- **Path**: `{workspace}/specs/{feature-name}/workflow-log.jsonl`
- **Format**: One JSON object per line, each with a `timestamp` (RFC3339 UTC)
  and `event` field
- **Event types**:
  - `state_transition` — fields: `from`, `to`, `round`
  - `agent_dispatch` — fields: `agent`, `task_id`, `round`
  - `agent_complete` — fields: `agent`, `task_id`, `duration_ms`, `cost_usd`, `success`
  - `agent_error` — fields: `agent`, `error_type`, `detail`
  - `convergence_check` — fields: `round`, `open_critical`, `open_major`, `verdict`, `progress`
  - `dedup_merge` — fields: `kept_id`, `merged_id`, `reason`
- **Best for**: Cost accounting, timing analysis, convergence trend analysis,
  finding which agent failed

### 3. Code Review Audit Log (structured JSONL, workspace-level)

- **Path**: `{workspace}/codereview-audit.jsonl`
- **Format**: One JSON object per line, each with `timestamp` and `event`
- **Event types**:
  - `codereview_start` — fields: `feature_name`, `code_path`, `spec_path`, `task_list_path`, `grill_code_mode`
  - `codereview_gate` — fields: `feature_name`, `gate`, `action`, `comment`
  - `codereview_cancel` — fields: `feature_name`, `reason`
  - `codereview_reset` — fields: `feature_name`
  - `state_transition` — fields: `feature_name`, `from`, `to`, `round`
  - `agent_dispatch` — fields: `feature_name`, `agent_name`, `lens`, `provider`, `round`
  - `agent_complete` — fields: `feature_name`, `agent_name`, `success`, `duration_ms`, `cost_usd`
  - `fix_phase` — fields: `feature_name`, `round`, `cost_usd`, `duration_ms`, `next_state`, `reason`
- **Best for**: Code review workflow investigations

## Investigation Principles

1. **Start with the server log.** It has everything in chronological order. The
   structured JSONL logs are useful for aggregation but the server log captures
   the unstructured context (error messages, reasons, thresholds) that JSONL
   events omit.

2. **Identify the feature name first.** Every investigation starts by knowing
   which feature workspace to look in. If the user provided log output, extract
   the feature name from it. If not, ask or check recent entries.

3. **Reconstruct the timeline before diagnosing.** Don't jump to conclusions
   from a single log line. Read the full sequence of events for the relevant
   workflow run to understand the progression.

4. **Cross-reference structured and text logs.** The workflow-log.jsonl tells
   you *what* happened (agent failed, state changed). The server.log tells you
   *why* (parse error detail, escalation reason, circuit breaker threshold).

5. **Look for the proximate cause, then the root cause.** "It escalated" is the
   symptom. The proximate cause might be "two consecutive rounds with no
   progress." The root cause might be "the reviser kept reintroducing the same
   critical finding the judge had already dismissed."

## How to Find the Workspace

The workspace path is set via the `--workspace` flag at server startup (default:
`./workspace`). Look for it in:
- The startup banner in server.log: `Workspace: {path}`
- The config.yaml file in the project root: check for `workspace_dir`
- Default: `./workspace` relative to where the server was started

If the project is at `/Users/nixlim/Sync/PROJECTS/foundry_zero/myagentsgigs`,
the workspace is typically `.workspace` in that directory.

## Investigation Procedure

Given a feature name or symptom:

1. **Grep server.log** for the feature name to get all related entries:
   ```
   Grep pattern="{feature-name}" path="{workspace}/server.log"
   ```

2. **Read the workflow JSONL** for the structured event timeline:
   ```
   Read file_path="{workspace}/specs/{feature-name}/workflow-log.jsonl"
   ```

3. **For escalation questions**, grep server.log for these specific patterns:
   - `"escalating from"` — progress-stall or regression escalation (includes reason)
   - `"staleness_breaker"` — a finding stuck open too long
   - `"circuit_breaker"` — cost or invocation limit hit
   - `"holdout generation escalation"` — holdout phase failure
   - `"best-effort parse failed"` — agent output unparseable, exhausted retries

4. **For cost questions**, grep for `[otel] metrics` lines which show cumulative
   `in`, `out`, `cache` tokens and `cost` per feature.

5. **For agent failures**, grep for `"process exited"` and check the exit
   status, or grep for `"is_error=true"` in claude-runner output.

6. **For convergence questions**, read the `convergence_check` events from the
   workflow JSONL to see the round-by-round trend of `open_critical`,
   `open_major`, `verdict`, and `progress`.

## Escalation Decision Tree

When the user asks "why did it escalate?", these are the possible causes,
checked in this order during the JUDGING phase:

| Priority | Trigger | Log signature |
|----------|---------|---------------|
| 1 | Staleness — a CRITICAL/MAJOR finding stuck open N rounds | `staleness_breaker` in workflow-log.jsonl `agent_error` event |
| 2 | Circuit breaker — cost or invocations exceeded limit | `circuit_breaker` event in workflow-log.jsonl OR server.log |
| 3 | Progress stall — 2 consecutive rounds with no progress | `"escalating from JUDGING: progress stalled: two consecutive rounds with no progress"` |
| 4 | Regression — open CRITICAL+MAJOR increased 2 rounds running | `"escalating from JUDGING: progress stalled: open CRITICAL+MAJOR increased"` |
| 5 | Authority-based — convergence verdict says escalate (cumulative downgrades/dismissals too high) | No specific log line; the `ProcessVerdict` returned `ShouldEscalate=true` |
| 6 | Agent error — judge or reviewer output unparseable after retries | `"best-effort parse failed for {agent}, escalating"` |

For cause #5 (authority-based), check the convergence_check events: if the
verdict was not PASS but also no progress/staleness/breaker message appears,
it was the cumulative downgrade/dismissal threshold.

## Edge Cases

- **Workspace path unknown**: Check config.yaml in the project root, or ask the
  user. Do NOT guess paths.
- **server.log doesn't exist**: The feature was added recently. Fall back to the
  ring buffer endpoint (`/api/logs/server`) if the server is running, or check
  stderr output if the user captured it. Explain that server.log requires the
  latest build.
- **Feature name unknown**: List feature directories with
  `Glob pattern="specs/*" path="{workspace}"` and ask the user which one.
- **Log file is very large**: Use `Grep` to narrow down to the relevant time
  window or feature, then `Read` with offset/limit for context around matches.
  Never try to read a multi-MB server.log in one shot.
- **Multiple workflow runs for same feature**: The logs append across runs. Look
  for `[workflow] started workflow` or `[workflow] resumed workflow` entries to
  find run boundaries.
- **Code review vs spec workflow**: If the issue is about a code review, check
  `codereview-audit.jsonl` instead of the per-feature workflow-log.jsonl.

## Output Format

Present findings as a structured diagnostic report:

```markdown
## Log Investigation: {feature-name or topic}

### Timeline
{Chronological sequence of key events, with timestamps. Include state
transitions, agent dispatches/completions, and the triggering event.}

### Root Cause
{One-paragraph explanation of why the observed behavior occurred. Distinguish
proximate cause from root cause if they differ.}

### Evidence
{Relevant log excerpts — quote directly from the logs. Include file path and
line context for each excerpt so the user can verify.}

### Recommendations (if applicable)
{Only if the root cause suggests a config change, bug fix, or operational
action. Omit this section if the behavior was expected.}
```

## Example Output

```markdown
## Log Investigation: b7-spec escalation at round 4

### Timeline
- 04:28:12 — Round 3 judge completed: open_critical=2, open_major=3, verdict=REVISE, progress=false
- 04:31:05 — Round 3 revision completed ($0.42, 173s)
- 04:34:42 — Round 4 judge dispatched (PID 79315)
- 04:38:46 — Round 4 judge completed: open_critical=2, open_major=3, verdict=REVISE, progress=false
- 04:38:47 — Escalated from JUDGING

### Root Cause
**Progress stall**: Two consecutive rounds (3 and 4) showed no reduction in
open CRITICAL or MAJOR findings (stuck at 2C/3M). The `ProgressTracker`
detected this pattern and triggered escalation.

The deeper issue is that the reviser was unable to resolve findings CRIT-002
and CRIT-005 — both relate to ambiguous requirements in the source document
that the reviser cannot resolve without human clarification.

### Evidence
From `server.log`:
> 04:38:47 escalating from JUDGING: progress stalled: two consecutive rounds with no progress

From `workflow-log.jsonl`:
> {"event":"convergence_check","round":3,"open_critical":2,"open_major":3,"verdict":"REVISE","progress":false,...}
> {"event":"convergence_check","round":4,"open_critical":2,"open_major":3,"verdict":"REVISE","progress":false,...}

### Recommendations
Review findings CRIT-002 and CRIT-005 in the judge output at
`specs/b7-spec/judge-round-4.json`. These likely need human input to resolve
the ambiguity before restarting the workflow.
```

## Quality Criteria

**Good output**:
- Answers the user's actual question directly in the first sentence of Root Cause
- Includes exact timestamps and log excerpts as evidence
- Distinguishes between what the logs prove vs. what is inferred
- Identifies the specific escalation trigger from the decision tree above

**Adequate but insufficient**:
- Lists possible causes without determining which one fired
- Summarizes without quoting log evidence
- Describes the code path without reading the actual log data
