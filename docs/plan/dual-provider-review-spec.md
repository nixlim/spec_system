# Dual-Provider Review (Codex + Claude) — Feature Specification

**Feature**: Add Codex as a second reviewer provider for adversarial spec review, and as a second holdout agent provider for dual-provider holdout generation
**Version**: 1.1
**Date**: 2026-03-21
**Status**: Draft

---

## 1. Overview

### Problem Statement

The adversarial spec system currently uses a single AI model (Claude) for all 4 reviewer agents. While the lens-based decomposition (clarity, consistency, security, correctness) provides structural diversity, all reviewers share the same model biases, training data, and reasoning patterns. This creates blind spots where Claude consistently overlooks certain classes of issues.

### Solution

Add Codex (OpenAI) as a second provider for both reviewers and holdout generation. This doubles the review phase to 8 parallel reviewers (4 Claude + 4 Codex) and doubles the holdout generation phase to 2 parallel holdout agents (1 Claude + 1 Codex). Findings from both models are merged and deduplicated together. Holdout outputs from both providers are merged into a single holdout file. This provides genuine multi-model adversarial diversity across both review and evaluation scenario generation.

### Actors

- **Orchestrator**: Automated workflow engine dispatching reviewer and holdout agents
- **Claude CLI agents**: Existing reviewer provider (4 lens groups) + holdout agent
- **Codex CLI agents**: New second provider for reviewers (4 lens groups) + holdout agent
- **Judge**: Remains Claude-only, sees merged findings from both providers
- **Dashboard user**: Observes review progress and findings in the web UI

---

## 2. User Stories

### US-1: Codex Runner Implementation (P0 — Critical)

The orchestrator needs a new `AgentRunner` implementation that invokes the Codex CLI as a subprocess, sends prompts via stdin, enforces structured JSON output via `--output-schema`, and writes results to a specified output file. This is the foundational building block — nothing else works without it.

**Why this priority**: Core dependency for all other stories. No codex reviews without a working runner.

**Independent Test**: Invoke CodexRunner.Run() with a test prompt and verify it produces valid JSON output matching the ReviewerOutput schema.

**Acceptance Scenarios:**

1. **Given** a valid prompt and output path, **When** CodexRunner.Run() is called, **Then** codex CLI is invoked via `codex exec --full-auto -m <model> --output-schema <schema.json> --output-last-message <output.json> --cd <workspace> --ephemeral -` with prompt on stdin.
2. **Given** codex produces valid ReviewerOutput JSON, **When** the process exits with code 0, **Then** Run() returns exitCode=0, the output file contains valid JSON, costUSD=0 (untracked), and durationMS reflects actual execution time.
3. **Given** codex produces invalid JSON or exits non-zero, **When** Run() returns, **Then** the error is captured in stderr, exitCode is non-zero, and costUSD is 0.
4. **Given** codex exceeds the 300s timeout, **When** the timeout fires, **Then** the process is killed (SIGTERM then SIGKILL after 2s), Run() returns a timeout error, and stderr indicates the timeout.
5. **Given** the prompt exceeds typical CLI argument length, **When** Run() is called, **Then** the prompt is delivered via stdin (using `-` positional arg), avoiding ARG_MAX limits.
6. **Given** an `--output-schema` file path, **When** CodexRunner is constructed, **Then** the ReviewerOutput JSON schema is written to a temp file and the path is passed to codex via `--output-schema`. The temp file is cleaned up after execution.

### US-2: Graceful Degradation When Codex Unavailable (P0 — Critical)

When the codex CLI is not installed or not in PATH, the system must detect this at orchestrator creation time and fall back to claude-only reviews (4 reviewers) with a clear warning message. The system must never crash or fail to start due to missing codex.

**Why this priority**: Without this, any environment missing codex would break entirely.

**Independent Test**: Remove codex from PATH, start a workflow, and verify 4 claude-only reviewers run successfully with a warning logged.

**Acceptance Scenarios:**

1. **Given** codex is not in PATH, **When** the orchestrator is created with `enable_codex_reviewers: true`, **Then** a warning is logged: `[WARNING] codex CLI not found — codex reviewers disabled, running claude-only`, and the orchestrator proceeds with claude-only dispatch.
2. **Given** codex is in PATH, **When** the orchestrator is created with `enable_codex_reviewers: true`, **Then** both claude and codex runners are available and no warning is logged.
3. **Given** codex is not in PATH, **When** the orchestrator is created with `enable_codex_reviewers: false`, **Then** no warning is logged and claude-only dispatch is used.
4. **Given** codex was available at startup but becomes unavailable mid-workflow, **When** a codex reviewer fails, **Then** normal retry logic handles it, and if all retries fail, reduced coverage logic applies.

### US-3: Dual-Provider Review Dispatch (P0 — Critical)

The review dispatch system must launch 8 parallel reviewer agents (4 claude + 4 codex) when codex is enabled, each covering all 4 lens groups independently. All 8 run concurrently. Results are collected and passed to the existing merge layer.

**Why this priority**: This is the core behavioral change — doubling review coverage.

**Independent Test**: Trigger a review phase with codex enabled and verify 8 concurrent reviewer goroutines are launched, producing 8 output files with provider-disambiguated names.

**Acceptance Scenarios:**

1. **Given** codex is enabled and available, **When** the orchestrator enters REVIEWING state, **Then** 8 reviewer agents are dispatched in parallel: `reviewer-clarity-claude`, `reviewer-consistency-claude`, `reviewer-security-claude`, `reviewer-correctness-claude`, `reviewer-clarity-codex`, `reviewer-consistency-codex`, `reviewer-security-codex`, `reviewer-correctness-codex`.
2. **Given** 8 reviewers are dispatched, **When** all complete successfully, **Then** 8 output files are written: `review-{letter}-claude-round-{N}.json` and `review-{letter}-codex-round-{N}.json`.
3. **Given** 8 reviewers are dispatched, **When** up to 3 fail (across both providers), **Then** the system proceeds with reduced coverage and logs which lenses/providers were lost.
4. **Given** 8 reviewers are dispatched, **When** 4 or more fail, **Then** the system escalates.
5. **Given** codex is disabled (config or unavailable), **When** the orchestrator enters REVIEWING state, **Then** only 4 claude reviewers are dispatched (current behavior, with `-claude` suffix).

### US-4: Provider-Attributed Finding Merge (P1 — High)

When findings from both providers are merged, the merge layer must preserve provider attribution so the judge can see which model raised each finding and where perspectives converge or diverge.

**Why this priority**: Attribution enables the judge to weigh cross-model consensus higher than single-model findings.

**Independent Test**: Merge findings from claude and codex reviewers for the same lens and verify `raised_by` contains provider-prefixed names.

**Acceptance Scenarios:**

1. **Given** reviewer-clarity-claude raises finding on "Section 3" with lens "AMB" severity MAJOR and reviewer-clarity-codex raises a matching finding on "Section 3" with lens "AMB" severity CRITICAL, **When** merge runs, **Then** the merged finding has `raised_by: ["reviewer-clarity-claude", "reviewer-clarity-codex"]` and severity CRITICAL (higher).
2. **Given** only reviewer-security-codex raises a finding with no claude match, **When** merge runs, **Then** the finding appears with `raised_by: ["reviewer-security-codex"]`.
3. **Given** claude and codex raise findings on the same section with different lenses, **When** merge runs, **Then** they are NOT deduplicated (different lens = different finding).
4. **Given** 8 reviewer outputs, **When** merge runs, **Then** the dedup_log records which findings were merged and from which providers.

### US-5: Team Configuration Extension (P1 — High)

The team configuration must support both `claude` and `codex` providers for reviewer agents, with 12 agents total (8 original + 4 codex reviewers). Validation must allow `codex` provider for reviewer roles only. A config flag `enable_codex_reviewers` controls whether codex reviewers are included. The codex model is configurable via `codex_model` (default: `gpt-5.4`).

**Why this priority**: Required for the dispatch layer to know which runners to use.

**Independent Test**: Load a config with `enable_codex_reviewers: true`, validate the team, and verify 12 agents are present with correct provider assignments.

**Acceptance Scenarios:**

1. **Given** `enable_codex_reviewers: true` (default), **When** DefaultTeamConfig() is called, **Then** 12 agents are returned: the original 8 (reviewers with `-claude` suffix) plus 4 codex reviewers with `-codex` suffix and provider `codex`.
2. **Given** `enable_codex_reviewers: false`, **When** DefaultTeamConfig() is called, **Then** 8 agents are returned with `-claude` suffix on reviewer names, no codex agents.
3. **Given** a team config with codex reviewer agents, **When** ValidateTeamConfig() is called, **Then** validation passes.
4. **Given** a team config with codex provider on a non-reviewer role (e.g. judge), **When** ValidateTeamConfig() is called, **Then** validation fails with error "codex provider only supported for reviewer role".
5. **Given** `codex_model` set to "o3", **When** CodexRunner is created, **Then** the `-m o3` flag is passed to the codex CLI.
6. **Given** an existing workflow state file referencing old reviewer names (e.g., `reviewer-clarity` without suffix), **When** the state is loaded, **Then** the old names are accepted and mapped to the new format.

### US-6: Reviewer Timeout Configuration (P2 — Medium)

Both claude and codex reviewers must use a 300-second (5-minute) timeout, increased from the current 120 seconds.

**Why this priority**: Increased timeout accommodates both providers' varying response times.

**Independent Test**: Start a review and verify both claude and codex runners are configured with 300s timeout.

**Acceptance Scenarios:**

1. **Given** the default config, **When** reviewers are dispatched, **Then** both claude and codex reviewers use 300s timeout.
2. **Given** a custom config with `reviewer_timeout_seconds: 600`, **When** reviewers are dispatched, **Then** both providers use 600s timeout.

### US-7: Structured Output Enforcement for Codex (P0 — Critical)

Codex must produce output conforming to the ReviewerOutput JSON schema. The system uses codex's `--output-schema` flag for schema enforcement, strict JSON parsing on the output, and retries with clear error messaging back to the agent when output is invalid.

**Why this priority**: Without valid structured output, codex findings can't be merged or used.

**Independent Test**: Send codex a review prompt with --output-schema and verify the output parses as valid ReviewerOutput JSON.

**Acceptance Scenarios:**

1. **Given** a ReviewerOutput JSON schema file, **When** CodexRunner invokes codex, **Then** `--output-schema <path>` is passed as a CLI argument.
2. **Given** codex returns valid JSON matching the schema, **When** the output is parsed, **Then** it produces a valid ReviewerOutput with findings, lenses, and severity.
3. **Given** codex returns invalid JSON, **When** parsing fails, **Then** the retry logic is triggered with a clear error message: "codex output failed schema validation: <details>", and the next attempt includes the error context.
4. **Given** codex returns JSON with missing `recommendation` fields, **When** validation runs, **Then** those findings are rejected, and if zero valid findings remain, a retry is triggered.
5. **Given** all retries produce invalid output, **When** max retries exhausted, **Then** the codex reviewer for that lens is marked as failed and reduced coverage logic applies.

### US-8: Dual-Provider Holdout Generation Dispatch (P0 — Critical)

The holdout generation phase must launch 2 parallel holdout agents (1 Claude + 1 Codex) when codex is enabled, each independently generating evaluation scenarios from the same spec and findings. Both agents run concurrently. Their outputs are merged into a single combined holdout file.

**Why this priority**: This is the core dual-provider holdout capability — matching the review doubling pattern.

**Independent Test**: Trigger a holdout generation phase with codex enabled, verify 2 concurrent holdout agents produce output, and a merged holdout file is written.

**Acceptance Scenarios:**

1. **Given** codex is enabled and available, **When** the orchestrator enters HOLDOUT_GENERATION state, **Then** 2 holdout agents are dispatched in parallel: `holdout-claude` and `holdout-codex`.
2. **Given** 2 holdout agents are dispatched, **When** both complete successfully, **Then** 2 output files are written: `holdout-claude-round-{N}.json` and `holdout-codex-round-{N}.json`, plus 2 markdown files `holdouts-claude-round-{N}.md` and `holdouts-codex-round-{N}.md`.
3. **Given** both holdout agents complete, **When** their markdown outputs are merged, **Then** a combined `holdouts-round-{N}.md` is produced containing scenarios from both providers with attribution.
4. **Given** the codex holdout agent fails after retries, **When** the claude holdout agent succeeds, **Then** the system proceeds with claude-only holdouts (graceful degradation, no escalation).
5. **Given** both holdout agents fail after retries, **When** the error is handled, **Then** the workflow escalates.
6. **Given** codex is disabled or unavailable, **When** the orchestrator enters HOLDOUT_GENERATION, **Then** only the claude holdout agent is dispatched (single provider, same as base holdout-generation-spec behavior).

### US-9: Holdout Output Merge with Provider Attribution (P1 — High)

When holdout scenarios from both providers are merged, the combined holdout file must preserve provider attribution so operators can compare which model generated which scenarios and assess quality differences.

**Why this priority**: Attribution is essential for the learning goal — comparing Claude vs Codex holdout quality.

**Independent Test**: Merge holdout outputs from both providers, verify the combined file attributes each scenario to its source provider.

**Acceptance Scenarios:**

1. **Given** `holdouts-claude-round-1.md` contains 6 scenarios and `holdouts-codex-round-1.md` contains 8 scenarios, **When** the holdout merge runs, **Then** `holdouts-round-1.md` contains all 14 scenarios with provider attribution headers.
2. **Given** both providers generate a scenario for the same edge case, **When** the merge runs, **Then** both scenarios are kept (no deduplication — different perspectives are valuable for holdouts).
3. **Given** only one provider succeeded (the other failed), **When** the merge runs, **Then** `holdouts-round-{N}.md` contains only the successful provider's scenarios with an attribution note.

### US-10: Codex Holdout Agent Structured Output (P0 — Critical)

The codex holdout agent must produce output conforming to the HoldoutOutput JSON schema, using the same `--output-schema` enforcement as codex reviewers. The codex holdout agent also writes a markdown holdout file.

**Why this priority**: Without valid structured output, codex holdout results can't be merged.

**Independent Test**: Invoke codex with a holdout prompt and `--output-schema`, verify the output parses as valid HoldoutOutput JSON.

**Acceptance Scenarios:**

1. **Given** a HoldoutOutput JSON schema file, **When** CodexRunner invokes codex for holdout generation, **Then** `--output-schema <path>` is passed.
2. **Given** codex returns valid JSON matching the schema, **When** the output is parsed, **Then** it produces a valid HoldoutOutput with scenario_count, categories, and holdout_file path.
3. **Given** codex returns invalid JSON for holdout generation, **When** parsing fails, **Then** retry logic fires with error context.
4. **Given** all retries produce invalid output, **When** max retries exhausted, **Then** the codex holdout agent is marked as failed and the claude holdout proceeds alone (graceful degradation).

### US-11: Holdout Agent Timeout Configuration (P2 — Medium)

Both claude and codex holdout agents must use the `HoldoutTimeoutSeconds` config value (default 300s), separate from the reviewer timeout.

**Why this priority**: Holdout generation may require different time budgets than reviewing.

**Independent Test**: Start holdout generation, verify both agents use the configured holdout timeout.

**Acceptance Scenarios:**

1. **Given** the default config, **When** holdout agents are dispatched, **Then** both claude and codex holdout agents use 300s timeout.
2. **Given** a custom config with `holdout_timeout_seconds: 600`, **When** holdout agents are dispatched, **Then** both providers use 600s timeout.

---

## 3. Edge Cases

| # | Edge Case | Expected Behavior |
|---|-----------|-------------------|
| E1 | Codex CLI installed but authentication expired/invalid | Codex process exits non-zero with auth error in stderr. Retry logic fires. After max retries, marked as failed reviewer. Warning logged. |
| E2 | Codex returns valid JSON but with 0 findings | Accepted as valid output (no findings = clean review for that lens). Not retried. |
| E3 | Both claude and codex find the same issue with different severities | Merge keeps the higher severity, concatenates recommendations with provider attribution. |
| E4 | Codex returns findings with lenses outside its assigned group | Findings with wrong lens codes are filtered out during validation. If no valid findings remain, retry triggered. |
| E5 | Network partition during codex API call | Process hangs until timeout (300s), then killed. Retry logic applies. |
| E6 | Codex output file written but empty (0 bytes) | Treated as missing output. Retry triggered. |
| E7 | Both providers fail for the same lens group | That lens has zero coverage. If total failures ≥4, escalate. |
| E8 | Config has `enable_codex_reviewers: true` but codex missing | Degradation: warn and run claude-only. |
| E9 | Very large spec (>100k tokens) sent to codex | Codex may hit context limits. Process exits with error. Retry logic applies. |
| E10 | Concurrent codex processes exhaust API rate limits | Some codex reviewers fail with rate limit errors. Retry with backoff handles this. |
| E11 | Codex holdout agent produces valid JSON but empty scenario list | Accepted as valid output (0 scenarios). Merged holdout file contains only claude scenarios. |
| E12 | Both holdout agents produce scenarios for the same edge case | Both kept in merged file — no deduplication for holdouts (different perspectives are valuable). |
| E13 | Codex holdout agent times out | Retry logic fires. After max retries, claude-only holdouts used. No escalation. |
| E14 | 4 codex reviewers + codex holdout agent all rate-limited simultaneously | Reviewers retry independently from holdout agent. Each has its own retry budget. |
| E15 | Codex holdout succeeds but codex reviewers all fail | Holdout uses both providers' output. Review uses claude-only (reduced coverage). Independent failure domains. |

---

## 4. Behavioral Contract & Boundaries

### Behavioral Contract

- When codex is enabled and available, the system dispatches 8 parallel reviewers (4 claude + 4 codex).
- When codex is enabled but unavailable, the system logs a warning and dispatches 4 claude-only reviewers.
- When codex is disabled via config, the system dispatches 4 claude-only reviewers with no warning.
- When a codex reviewer produces invalid output, the system retries with error context up to max retries.
- When findings from both providers are merged, provider attribution is preserved in `raised_by`.
- When both providers find the same issue, the merge keeps the higher severity and concatenates recommendations.
- When reviewer failures reach 4+ (out of 8), the system escalates.
- When reviewer failures are ≤3 (out of 8), the system proceeds with reduced coverage.
- When codex cost data is unavailable, the dashboard shows $0 for codex reviewers (no OTEL).
- When a workflow state references old reviewer names (without provider suffix), the system accepts them.
- When codex is enabled and available, the system dispatches 2 parallel holdout agents (1 claude + 1 codex) during HOLDOUT_GENERATION.
- When codex is enabled but unavailable, the system dispatches 1 claude-only holdout agent during HOLDOUT_GENERATION.
- When both holdout agents complete, their scenarios are merged into a single `holdouts-round-{N}.md` with provider attribution.
- When only one holdout agent succeeds, the merged file contains only that provider's scenarios.
- When the codex holdout agent fails, the system proceeds with claude-only holdouts (no escalation).
- When both holdout agents fail, the system escalates.

### Explicit Non-Behaviors

- The system must NOT use codex for non-reviewer roles (judge, drafter, reviser, discovery) because those roles require Claude-specific features (OTEL telemetry, tool use patterns).
- The system must NOT crash or refuse to start when codex is unavailable because the system must remain functional in claude-only environments.
- The system must NOT retry indefinitely on codex failures because this would block the review phase; max retries apply equally to both providers.
- The system must NOT fabricate cost data for codex because inaccurate cost data is worse than no cost data.
- The system must NOT change the merge deduplication algorithm because the existing algorithm handles N inputs generically.
- The system must NOT add provider suffix to non-reviewer, non-holdout agents because they only have one provider.
- The system must NOT deduplicate holdout scenarios across providers because different perspectives are valuable for evaluation.
- The system must NOT escalate when only the codex holdout agent fails because holdout generation has softer failure semantics than review — one provider's output is sufficient.

### Integration Boundaries

**Codex CLI (External System)**
- **Data in**: Review prompt (via stdin), JSON schema (via `--output-schema` file), workspace path (via `--cd`), model selection (via `-m`)
- **Data out**: ReviewerOutput JSON (via `--output-last-message` file), exit code, stderr
- **Contract**: `codex exec --full-auto -m <model> --output-schema <schema.json> --output-last-message <output.json> --cd <workspace> --ephemeral -` with prompt on stdin
- **Failure behavior**: Non-zero exit → retry. Timeout → SIGTERM/SIGKILL → retry. Auth failure → retry then fail. Rate limit → retry with backoff.
- **Development approach**: Real codex CLI. Unit tests use mock AgentRunner interface.

---

## 5. BDD Scenarios

### Feature: Codex Runner Execution

```gherkin
Scenario: Successful codex reviewer invocation
  Category: Happy Path
  Traces to: US-1, Acceptance Scenario 1, 2

  Given a CodexRunner configured with command "codex", model "gpt-5.4", and timeout 300s
  And a valid reviewer prompt for lens group "clarity"
  And an output path "specs/feature/review-a-codex-round-1.json"
  When Run() is called with the prompt and output path
  Then the process is invoked as "codex exec --full-auto -m gpt-5.4 --output-schema <schema> --output-last-message <output> --cd <workspace> --ephemeral -"
  And the prompt is written to the process stdin
  And the process exits with code 0
  And the output file contains valid ReviewerOutput JSON
  And durationMS is greater than 0
  And costUSD is 0

Scenario: Codex process timeout
  Category: Error Path
  Traces to: US-1, Acceptance Scenario 4

  Given a CodexRunner configured with timeout 300s
  And a prompt that causes codex to hang
  When Run() is called
  And 300 seconds elapse
  Then SIGTERM is sent to the process
  And if the process does not exit within 2 seconds, SIGKILL is sent
  And Run() returns exitCode != 0
  And stderr contains "timeout" or "killed"

Scenario: Codex produces invalid JSON output
  Category: Error Path
  Traces to: US-7, Acceptance Scenario 3

  Given a CodexRunner and a prompt
  When codex writes non-JSON content to the output file
  Then Run() returns exitCode 0 but the output file fails JSON parsing
  And the dispatch retry logic is triggered
  And the retry error message includes "codex output failed schema validation"

Scenario: Codex output file missing after execution
  Category: Error Path
  Traces to: US-7, Acceptance Scenario 5

  Given a CodexRunner and a valid prompt
  When codex exits with code 0 but does not write to the output path
  Then the dispatch layer detects missing output
  And retry logic is triggered

Scenario: Prompt delivered via stdin
  Category: Happy Path
  Traces to: US-1, Acceptance Scenario 5

  Given a CodexRunner and a prompt of 500,000 characters
  When Run() is called
  Then the prompt is delivered via stdin (not as a CLI argument)
  And codex receives the full prompt without truncation

Scenario: Schema file lifecycle
  Category: Happy Path
  Traces to: US-1, Acceptance Scenario 6

  Given a CodexRunner
  When Run() is called
  Then a JSON schema file is created in a temp directory
  And the file contains the ReviewerOutput schema definition
  And --output-schema points to this file
  And after execution completes, the temp file is cleaned up

Scenario: Configurable model selection
  Category: Happy Path
  Traces to: US-5, Acceptance Scenario 5

  Given config has codex_model: "o3"
  When CodexRunner is created
  Then the command includes "-m o3"
  And the model flag is passed to every codex invocation
```

### Feature: Graceful Degradation

```gherkin
Scenario: Codex unavailable at startup with codex enabled
  Category: Alternate Path
  Traces to: US-2, Acceptance Scenario 1

  Given codex is not in PATH
  And config has enable_codex_reviewers: true
  When the orchestrator is created
  Then a warning is logged containing "codex CLI not found — codex reviewers disabled, running claude-only"
  And the orchestrator is created successfully
  And codex reviewers are disabled for this orchestrator instance

Scenario: Codex available at startup
  Category: Happy Path
  Traces to: US-2, Acceptance Scenario 2

  Given codex is in PATH
  And config has enable_codex_reviewers: true
  When the orchestrator is created
  Then both claude and codex runners are configured
  And no warning is logged about codex availability

Scenario: Codex disabled via config
  Category: Alternate Path
  Traces to: US-2, Acceptance Scenario 3

  Given config has enable_codex_reviewers: false
  When the orchestrator is created
  Then only claude runner is configured
  And no codex availability check is performed
  And no warning is logged

Scenario: Codex fails mid-workflow
  Category: Error Path
  Traces to: US-2, Acceptance Scenario 4

  Given codex was available at startup
  And a review phase is in progress
  When a codex reviewer process fails (e.g. auth expired)
  Then normal retry logic applies
  And if all retries fail, the codex reviewer is marked as failed
  And reduced coverage logic proceeds
```

### Feature: Dual-Provider Dispatch

```gherkin
Scenario: Eight reviewers dispatched in parallel
  Category: Happy Path
  Traces to: US-3, Acceptance Scenario 1, 2

  Given codex is enabled and available
  And the workflow is in REVIEWING state with round 1
  When handleReviewing() executes
  Then 8 reviewer goroutines are launched concurrently
  And 4 use ClaudeRunner with names reviewer-{lens}-claude
  And 4 use CodexRunner with names reviewer-{lens}-codex
  And 8 output files are written:
    | review-a-claude-round-1.json |
    | review-b-claude-round-1.json |
    | review-c-claude-round-1.json |
    | review-d-claude-round-1.json |
    | review-a-codex-round-1.json  |
    | review-b-codex-round-1.json  |
    | review-c-codex-round-1.json  |
    | review-d-codex-round-1.json  |

Scenario: Reduced coverage with up to 3 failures
  Category: Alternate Path
  Traces to: US-3, Acceptance Scenario 3

  Given 8 reviewers are dispatched
  When reviewer-clarity-codex, reviewer-security-claude, and reviewer-correctness-codex all fail after retries
  Then the system proceeds with 5 successful reviewers
  And reduced_coverage is true
  And coverage_loss includes the 3 failed lens/provider combinations

Scenario: Escalation at 4 or more failures
  Category: Error Path
  Traces to: US-3, Acceptance Scenario 4

  Given 8 reviewers are dispatched
  When 4 or more reviewers fail after retries
  Then the system returns an escalation error
  And the workflow transitions to ESCALATED state

Scenario Outline: Claude-only fallback dispatch
  Category: Alternate Path
  Traces to: US-3, Acceptance Scenario 5

  Given codex is <availability>
  And config has enable_codex_reviewers: <config_value>
  When the orchestrator enters REVIEWING state
  Then <reviewer_count> reviewers are dispatched

  Examples:
    | availability | config_value | reviewer_count |
    | unavailable  | true         | 4              |
    | available    | false        | 4              |
    | available    | true         | 8              |
```

### Feature: Provider-Attributed Merge

```gherkin
Scenario: Cross-provider duplicate merged with attribution
  Category: Happy Path
  Traces to: US-4, Acceptance Scenario 1

  Given reviewer-clarity-claude raises finding on "Section 3" with lens "AMB" severity MAJOR
  And reviewer-clarity-codex raises finding on "Section 3" with lens "AMB" severity CRITICAL
  When MergeReviewerOutputs() runs
  Then one merged finding exists for "Section 3" / "AMB"
  And severity is CRITICAL (higher of the two)
  And raised_by contains ["reviewer-clarity-claude", "reviewer-clarity-codex"]
  And recommendation contains "From reviewer-clarity-claude:" and "From reviewer-clarity-codex:"

Scenario: Single-provider finding preserved
  Category: Happy Path
  Traces to: US-4, Acceptance Scenario 2

  Given only reviewer-security-codex raises a finding on "Section 7" with lens "SEC"
  And no other reviewer raises a matching finding
  When MergeReviewerOutputs() runs
  Then the finding appears with raised_by: ["reviewer-security-codex"]

Scenario: Same section different lens not deduplicated
  Category: Edge Case
  Traces to: US-4, Acceptance Scenario 3

  Given reviewer-clarity-claude raises finding on "Section 3" with lens "AMB"
  And reviewer-consistency-codex raises finding on "Section 3" with lens "CON"
  When MergeReviewerOutputs() runs
  Then two separate findings exist (different lenses)

Scenario: Dedup log records cross-provider merges
  Category: Happy Path
  Traces to: US-4, Acceptance Scenario 4

  Given findings from 8 reviewers with 3 cross-provider duplicates
  When MergeReviewerOutputs() runs
  Then dedup_log contains 3 entries
  And each entry records the source providers for both sides of the merge
```

### Feature: Team Configuration

```gherkin
Scenario: Default team with codex enabled
  Category: Happy Path
  Traces to: US-5, Acceptance Scenario 1

  Given enable_codex_reviewers is true
  When DefaultTeamConfig() is called
  Then 12 agents are returned
  And agents include reviewer-clarity-claude, reviewer-clarity-codex (and 6 more reviewer pairs)
  And non-reviewer agents (discovery, drafter, reviser, judge) retain original names with no suffix
  And codex reviewer agents have provider "codex"
  And claude reviewer agents have provider "claude"

Scenario: Default team with codex disabled
  Category: Alternate Path
  Traces to: US-5, Acceptance Scenario 2

  Given enable_codex_reviewers is false
  When DefaultTeamConfig() is called
  Then 8 agents are returned
  And reviewer agents are named reviewer-clarity-claude, reviewer-consistency-claude, etc.
  And non-reviewer agents retain original names

Scenario: Validation accepts codex reviewer
  Category: Happy Path
  Traces to: US-5, Acceptance Scenario 3

  Given a team config with reviewer-clarity-codex having provider "codex"
  When ValidateTeamConfig() is called
  Then validation passes

Scenario: Validation rejects codex non-reviewer
  Category: Error Path
  Traces to: US-5, Acceptance Scenario 4

  Given a team config with judge having provider "codex"
  When ValidateTeamConfig() is called
  Then validation fails with error containing "codex provider only supported for reviewer role"

Scenario: Backward-compatible reviewer name loading
  Category: Alternate Path
  Traces to: US-5, Acceptance Scenario 6

  Given a workflow state file with raised_by: ["reviewer-clarity"]
  When the state is loaded
  Then the old name is accepted without error
  And the finding is attributed to the legacy reviewer
```

### Feature: Structured Output Enforcement

```gherkin
Scenario: Output schema file passed to codex
  Category: Happy Path
  Traces to: US-7, Acceptance Scenario 1

  Given a CodexRunner with a ReviewerOutput JSON schema
  When Run() builds the command
  Then --output-schema argument points to a valid JSON schema file
  And the schema file contains the ReviewerOutput type definition

Scenario: Valid structured output from codex
  Category: Happy Path
  Traces to: US-7, Acceptance Scenario 2

  Given codex returns JSON matching the ReviewerOutput schema
  When the output is parsed
  Then all findings have id, description, severity, lens, recommendation
  And lenses_applied matches the assigned lens group

Scenario: Retry with error context on invalid output
  Category: Error Path
  Traces to: US-7, Acceptance Scenario 3, 4

  Given codex returns JSON with findings missing recommendation
  When validation rejects all findings
  Then retry is triggered
  And the retry error message includes details of the validation failure

Scenario: Zero findings accepted without retry
  Category: Edge Case
  Traces to: Edge Case E2

  Given codex returns valid ReviewerOutput JSON with empty findings array
  When the output is parsed
  Then it is accepted as a valid clean review
  And no retry is triggered
```

### Feature: Dual-Provider Holdout Dispatch

```gherkin
Scenario: Two holdout agents dispatched in parallel
  Category: Happy Path
  Traces to: US-8, Acceptance Scenario 1, 2

  Given codex is enabled and available
  And the workflow is in HOLDOUT_GENERATION state round 1
  When handleHoldoutGeneration() executes
  Then 2 holdout agent goroutines are launched concurrently
  And 1 uses ClaudeRunner with name holdout-claude
  And 1 uses CodexRunner with name holdout-codex
  And 4 output files are written:
    | holdout-claude-round-1.json |
    | holdout-codex-round-1.json  |
    | holdouts-claude-round-1.md  |
    | holdouts-codex-round-1.md   |

Scenario: Holdout outputs merged with attribution
  Category: Happy Path
  Traces to: US-8 Acceptance Scenario 3, US-9 Acceptance Scenario 1

  Given holdouts-claude-round-1.md contains 6 scenarios
  And holdouts-codex-round-1.md contains 8 scenarios
  When the holdout merge runs
  Then holdouts-round-1.md contains all 14 scenarios
  And each scenario section is attributed to its source provider
  And no deduplication is applied

Scenario: Codex holdout fails, claude-only proceeds
  Category: Alternate Path
  Traces to: US-8 Acceptance Scenario 4

  Given 2 holdout agents are dispatched
  And holdout-codex fails after max retries
  And holdout-claude succeeds
  When the dispatch result is processed
  Then holdouts-round-{N}.md contains only claude scenarios
  And the workflow proceeds (no escalation)
  And a warning is logged about codex holdout failure

Scenario: Both holdout agents fail
  Category: Error Path
  Traces to: US-8 Acceptance Scenario 5

  Given 2 holdout agents are dispatched
  And both fail after max retries
  When the dispatch result is processed
  Then the workflow escalates from HOLDOUT_GENERATION

Scenario: Claude-only holdout when codex unavailable
  Category: Alternate Path
  Traces to: US-8 Acceptance Scenario 6

  Given codex is not available (disabled or not in PATH)
  When the orchestrator enters HOLDOUT_GENERATION
  Then only holdout-claude is dispatched
  And holdout output files use the base naming (holdout-round-{N}.json, holdouts-round-{N}.md)

Scenario: Duplicate edge case scenarios from both providers kept
  Category: Edge Case
  Traces to: US-9 Acceptance Scenario 2, Edge Case E12

  Given both holdout agents generate a scenario testing "concurrent user cancellation"
  When the holdout merge runs
  Then both scenarios appear in the merged file
  And each is attributed to its provider
```

### Feature: Codex Holdout Structured Output

```gherkin
Scenario: Codex holdout output schema enforcement
  Category: Happy Path
  Traces to: US-10 Acceptance Scenario 1, 2

  Given a HoldoutOutput JSON schema file
  When CodexRunner invokes codex for holdout generation
  Then --output-schema points to the HoldoutOutput schema
  And the output file contains valid HoldoutOutput JSON

Scenario: Codex holdout invalid output retry
  Category: Error Path
  Traces to: US-10 Acceptance Scenario 3

  Given codex returns invalid JSON for holdout generation
  When parsing fails
  Then retry logic fires with error context
  And the retry includes the validation error details

Scenario: Codex holdout all retries exhausted
  Category: Error Path
  Traces to: US-10 Acceptance Scenario 4

  Given codex holdout produces invalid output on every attempt
  When max retries are exhausted
  Then the codex holdout agent is marked as failed
  And the claude holdout proceeds alone
  And the workflow does not escalate
```

---

## 6. Test-Driven Development Plan

### Test Implementation Order

| Order | Test Name | Level | Traces to BDD Scenario | Description |
|-------|-----------|-------|------------------------|-------------|
| 1 | TestCodexRunner_BuildCommand | Unit | Successful codex invocation | Verify command construction: args, stdin, schema path, output path, model flag, working dir |
| 2 | TestCodexRunner_BuildCommand_SchemaFile | Unit | Schema file lifecycle | Verify JSON schema file is created, path passed via --output-schema, cleaned up after |
| 3 | TestCodexRunner_BuildCommand_ModelFlag | Unit | Configurable model selection | Verify -m flag with configured model |
| 4 | TestCodexRunner_ParseOutput_ValidJSON | Unit | Valid structured output | Parse valid ReviewerOutput JSON from output file |
| 5 | TestCodexRunner_ParseOutput_InvalidJSON | Unit | Codex produces invalid JSON | Verify error returned when output file contains non-JSON |
| 6 | TestCodexRunner_ParseOutput_EmptyFile | Unit | Output file missing | Verify error returned for empty/missing output file |
| 7 | TestCodexRunner_Timeout | Unit | Codex process timeout | Verify timeout kills process and returns error |
| 8 | TestCodexRunner_StdinDelivery | Unit | Prompt delivered via stdin | Verify large prompt delivered via stdin, not CLI arg |
| 9 | TestCodexRunner_CostAlwaysZero | Unit | Successful codex invocation | Verify costUSD is always 0 (untracked) |
| 10 | TestCodexAvailability_Available | Unit | Codex available at startup | exec.LookPath("codex") succeeds, no warning |
| 11 | TestCodexAvailability_Unavailable | Unit | Codex unavailable at startup | exec.LookPath fails, warning logged, runner nil |
| 12 | TestCodexAvailability_ConfigDisabled | Unit | Codex disabled via config | No availability check, no warning |
| 13 | TestTeamConfig_WithCodex | Unit | Default team with codex enabled | 12 agents, correct names and providers |
| 14 | TestTeamConfig_WithoutCodex | Unit | Default team with codex disabled | 8 agents, -claude suffix on reviewers |
| 15 | TestTeamConfig_ValidateCodexReviewer | Unit | Validation accepts codex reviewer | codex provider on reviewer role passes |
| 16 | TestTeamConfig_ValidateCodexNonReviewer | Unit | Validation rejects codex non-reviewer | codex on judge/drafter fails validation |
| 17 | TestTeamConfig_BackwardCompatNames | Unit | Backward-compatible name loading | Old reviewer names accepted |
| 18 | TestDispatch_DualProvider_AllSucceed | Unit | Eight reviewers dispatched | 8 goroutines, 8 results, correct file names |
| 19 | TestDispatch_DualProvider_ThreeFailures | Unit | Reduced coverage 3 failures | ≤3 failures → proceed with reduced coverage |
| 20 | TestDispatch_DualProvider_FourFailures | Unit | Escalation at 4+ failures | ≥4 failures → escalation error |
| 21 | TestDispatch_ClaudeOnlyFallback | Unit | Claude-only fallback dispatch | 4 reviewers when codex disabled/unavailable |
| 22 | TestDispatch_DualProvider_OutputFileNaming | Unit | Eight reviewers dispatched | Files named review-{letter}-{provider}-round-{N}.json |
| 23 | TestMerge_CrossProviderDuplicate | Unit | Cross-provider duplicate merged | Same finding from both providers merged with attribution |
| 24 | TestMerge_SingleProviderFinding | Unit | Single-provider finding preserved | Unmatched finding keeps single raised_by |
| 25 | TestMerge_SameSectionDifferentLens | Unit | Same section different lens | Different lens = not deduplicated |
| 26 | TestMerge_DedupLogProviderAttribution | Unit | Dedup log records cross-provider | Dedup log entries include provider info |
| 27 | TestMerge_EightReviewerInputs | Unit | Eight reviewers dispatched | Merge handles 8 inputs correctly |
| 28 | TestMerge_ZeroFindingsAccepted | Unit | Zero findings accepted | Empty findings array is valid |
| 29 | TestHandleReviewing_DualProvider | Integration | Eight reviewers dispatched | Full orchestrator review phase with mock runners |
| 30 | TestHandleReviewing_CodexFallback | Integration | Claude-only fallback dispatch | Orchestrator with nil codex runner dispatches 4 only |
| 31 | TestCodexRunner_E2E_RealInvocation | E2E | Successful codex invocation | Real codex CLI invocation with simple prompt (skip if codex unavailable) |
| 32 | TestHoldoutDispatch_DualProvider_AllSucceed | Unit | Two holdout agents dispatched | 2 goroutines, 4 output files (2 JSON + 2 MD), correct naming |
| 33 | TestHoldoutDispatch_DualProvider_CodexFails | Unit | Codex holdout fails, claude proceeds | Claude-only holdouts used, no escalation |
| 34 | TestHoldoutDispatch_DualProvider_BothFail | Unit | Both holdout agents fail | Escalation triggered |
| 35 | TestHoldoutDispatch_ClaudeOnlyFallback | Unit | Claude-only holdout dispatch | Single agent when codex disabled/unavailable |
| 36 | TestHoldoutMerge_BothProviders | Unit | Holdout outputs merged | Combined file with all scenarios, provider attribution |
| 37 | TestHoldoutMerge_SingleProvider | Unit | Codex holdout fails | Merged file contains only claude scenarios |
| 38 | TestHoldoutMerge_NoDeduplicate | Unit | Duplicate scenarios kept | Both providers' scenarios for same edge case preserved |
| 39 | TestCodexRunner_HoldoutSchemaFile | Unit | Codex holdout schema enforcement | HoldoutOutput schema passed via --output-schema |
| 40 | TestHoldoutDispatch_DualProvider_OutputFileNaming | Unit | Two holdout agents dispatched | Files named holdout-{provider}-round-{N}.json and holdouts-{provider}-round-{N}.md |
| 41 | TestHandleHoldoutGeneration_DualProvider | Integration | Two holdout agents dispatched | Full handler with mock runners, verify dual dispatch and merge |
| 42 | TestHandleHoldoutGeneration_CodexFallback | Integration | Claude-only holdout dispatch | Handler with nil codex runner dispatches 1 only |

### Test Datasets

**TD-1: CodexRunner Command Construction**

| # | Input | Expected Behavior | Traces to |
|---|-------|-------------------|-----------|
| 1 | prompt="Review this spec", output="/tmp/out.json", timeout=300, model="gpt-5.4" | Command: `codex exec --full-auto -m gpt-5.4 --output-schema /tmp/schema.json --output-last-message /tmp/out.json --cd /workspace --ephemeral -` | BDD: Successful codex invocation |
| 2 | prompt="" (empty) | Error: "empty prompt" | BDD: Codex produces invalid JSON |
| 3 | prompt=string(500000 chars) | Delivered via stdin, no truncation | BDD: Prompt delivered via stdin |
| 4 | outputPath="" (empty) | Error: "empty output path" | BDD: Output file missing |
| 5 | timeout=0 | Error: "timeout must be positive" | BDD: Codex process timeout |
| 6 | model="" (empty) | No -m flag passed (use codex default) | BDD: Configurable model selection |
| 7 | model="o3" | Command includes `-m o3` | BDD: Configurable model selection |

**TD-2: Team Configuration**

| # | enable_codex | codex_available | Expected Count | Reviewer Names | Traces to |
|---|-------------|-----------------|----------------|----------------|-----------|
| 1 | true | true | 12 | *-claude + *-codex | BDD: Default team codex enabled |
| 2 | true | false | 8 | *-claude only | BDD: Codex unavailable |
| 3 | false | true | 8 | *-claude only | BDD: Codex disabled |
| 4 | false | false | 8 | *-claude only | BDD: Codex disabled |

**TD-3: Dispatch Failure Thresholds (8 reviewers)**

| # | Claude Fails | Codex Fails | Total | Expected | Traces to |
|---|-------------|-------------|-------|----------|-----------|
| 1 | 0 | 0 | 0 | Full coverage | BDD: 8 dispatched |
| 2 | 1 | 0 | 1 | Reduced, proceed | BDD: Reduced coverage |
| 3 | 0 | 2 | 2 | Reduced, proceed | BDD: Reduced coverage |
| 4 | 1 | 2 | 3 | Reduced, proceed | BDD: Reduced coverage |
| 5 | 2 | 2 | 4 | Escalation | BDD: Escalation |
| 6 | 0 | 4 | 4 | Escalation | BDD: Escalation |
| 7 | 4 | 0 | 4 | Escalation | BDD: Escalation |
| 8 | 4 | 4 | 8 | Escalation | BDD: Escalation |

**TD-4: Merge Cross-Provider Attribution**

| # | Claude Finding | Codex Finding | Match? | raised_by | Severity | Traces to |
|---|---------------|---------------|--------|-----------|----------|-----------|
| 1 | (Sec3, AMB, MAJOR) | (Sec3, AMB, CRIT) | Yes | [claude, codex] | CRITICAL | BDD: Cross-provider dup |
| 2 | (Sec3, AMB, MAJOR) | none | No | [claude] | MAJOR | BDD: Single-provider |
| 3 | none | (Sec7, SEC, MINOR) | No | [codex] | MINOR | BDD: Single-provider |
| 4 | (Sec3, AMB, MAJOR) | (Sec3, CON, MAJOR) | No | Separate | MAJOR each | BDD: Different lens |
| 5 | (sec 3, AMB, MAJOR) | (Sec 3, amb, CRIT) | Yes | [claude, codex] | CRITICAL | BDD: Case-insensitive |

**TD-5: Holdout Dispatch Failure Modes (2 agents)**

| # | Claude Holdout | Codex Holdout | Expected | Traces to |
|---|---------------|--------------|----------|-----------|
| 1 | Succeeds | Succeeds | Merge both, proceed | BDD: Two holdout agents dispatched |
| 2 | Succeeds | Fails | Claude-only holdouts, proceed | BDD: Codex holdout fails |
| 3 | Fails | Succeeds | Codex-only holdouts, proceed | BDD: (implicit — symmetric) |
| 4 | Fails | Fails | Escalation | BDD: Both holdout agents fail |

**TD-6: Holdout Merge Attribution**

| # | Claude Scenarios | Codex Scenarios | Expected Merged Count | Attribution | Traces to |
|---|-----------------|----------------|----------------------|-------------|-----------|
| 1 | 6 | 8 | 14 | Both providers labeled | BDD: Holdout outputs merged |
| 2 | 6 | 0 (empty output) | 6 | Claude only | BDD: Codex holdout fails |
| 3 | 0 (empty output) | 8 | 8 | Codex only | BDD: (symmetric) |
| 4 | 3 (same edge case) | 3 (same edge case) | 6 | Both kept, no dedup | BDD: Duplicate scenarios kept |

### Regression Test Requirements

This feature **modifies existing functionality**:

1. **Behaviours that MUST be preserved:**
   - Claude-only dispatch (4 reviewers) must work identically when codex disabled
   - Merge algorithm produces deterministic results regardless of input count
   - Retry logic, exponential backoff, and failure thresholds work correctly
   - Output file validation (missing recommendation rejection) unchanged
   - OTEL telemetry for claude reviewers unaffected

2. **Existing tests that MUST continue to pass:**
   - `TestDispatchReviewers_AllSucceed`
   - `TestDispatchReviewers_OneFailsThenSucceeds`
   - `TestDispatchReviewers_OneFailsAfterRetries`
   - `TestDispatchReviewers_TwoFail`
   - `TestMerge_*` (all existing merge tests)
   - `TestClaudeRunner_*` (all existing runner tests)

3. **NEW regression tests needed:**
   - `TestDispatch_BackwardCompat_ClaudeOnly` — 4-reviewer dispatch still works
   - `TestMerge_BackwardCompat_FourInputs` — merge with exactly 4 inputs
   - `TestTeamConfig_BackwardCompat_NoCodex` — 8-agent team when codex disabled

---

## 7. Requirements & Success Criteria

### Functional Requirements

- **FR-001**: System MUST implement a `CodexRunner` that satisfies the `AgentRunner` interface, invoking `codex exec --full-auto -m <model>` with prompt via stdin, structured output via `--output-schema` and `--output-last-message`, and `--ephemeral` flag.
- **FR-002**: System MUST dispatch 8 parallel reviewer agents (4 claude + 4 codex) when `enable_codex_reviewers` is true and codex is available.
- **FR-003**: System MUST fall back to 4 claude-only reviewers with a logged warning when codex is enabled but unavailable.
- **FR-004**: System MUST preserve provider attribution in merged findings via `raised_by` field containing provider-prefixed reviewer names.
- **FR-005**: System MUST support `enable_codex_reviewers` config flag, defaulting to `true`, with documentation on how to disable.
- **FR-006**: System MUST validate that `codex` provider is only used for `reviewer` role agents.
- **FR-007**: System MUST use 300-second timeout for both claude and codex reviewers (configurable via `reviewer_timeout_seconds`).
- **FR-008**: System MUST enforce structured output from codex using `--output-schema` with the ReviewerOutput JSON schema.
- **FR-009**: System MUST retry codex reviewers on invalid output with error context describing the validation failure.
- **FR-010**: System MUST escalate when 4 or more reviewers (across both providers) fail after retries.
- **FR-011**: System SHOULD proceed with reduced coverage when 1-3 reviewers fail, logging which lenses and providers were lost.
- **FR-012**: System MUST name output files with provider disambiguation: `review-{letter}-{provider}-round-{N}.json`.
- **FR-013**: System MUST report codex reviewer cost as $0 (untracked) without fabricating data.
- **FR-014**: System MUST name reviewer agents with provider suffix: `reviewer-{lens}-claude` and `reviewer-{lens}-codex`.
- **FR-015**: System MUST support configurable codex model via `codex_model` config field (default: `gpt-5.4`), passed as `-m <model>` to codex CLI.
- **FR-016**: System MUST accept legacy reviewer names (without provider suffix) when loading existing workflow state files.
- **FR-017**: System MUST NOT add provider suffix to non-reviewer, non-holdout agents (discovery, drafter, reviser, judge).
- **FR-018**: System MUST dispatch 2 parallel holdout agents (1 claude + 1 codex) during HOLDOUT_GENERATION when `enable_codex_reviewers` is true and codex is available.
- **FR-019**: System MUST fall back to 1 claude-only holdout agent when codex is enabled but unavailable.
- **FR-020**: System MUST merge holdout outputs from both providers into a single `holdouts-round-{N}.md` with provider attribution.
- **FR-021**: System MUST NOT deduplicate holdout scenarios across providers.
- **FR-022**: System MUST NOT escalate when only the codex holdout agent fails (proceed with claude-only holdouts).
- **FR-023**: System MUST escalate when both holdout agents fail after retries.
- **FR-024**: System MUST name holdout output files with provider disambiguation: `holdout-{provider}-round-{N}.json` and `holdouts-{provider}-round-{N}.md`.
- **FR-025**: System MUST enforce structured output for codex holdout agent using `--output-schema` with the HoldoutOutput JSON schema.
- **FR-026**: System MUST use `HoldoutTimeoutSeconds` (default 300) for both holdout agent providers.

### Success Criteria

- **SC-001**: All 42 tests in the TDD plan pass.
- **SC-002**: When codex is enabled and available, exactly 8 reviewer goroutines execute concurrently during the REVIEWING state.
- **SC-003**: When codex is unavailable, the system starts successfully and completes a full review cycle with 4 claude-only reviewers.
- **SC-004**: Merged findings from a dual-provider review contain provider-attributed `raised_by` arrays.
- **SC-005**: All existing tests (review_dispatch_test, merge_test, claude_runner_test) continue to pass.
- **SC-006**: Codex reviewer output files are valid ReviewerOutput JSON in ≥80% of invocations (retries allowed).
- **SC-007**: No OTEL/telemetry regressions — claude reviewer metrics still appear in the dashboard.
- **SC-008**: When codex is enabled, HOLDOUT_GENERATION dispatches 2 holdout agents concurrently producing 4 output files.
- **SC-009**: Merged holdout file `holdouts-round-{N}.md` contains scenarios from both providers with attribution headers.
- **SC-010**: When codex holdout fails, the workflow proceeds with claude-only holdouts without escalation.

### Traceability Matrix

| Requirement | User Story | BDD Scenario(s) | Test Name(s) |
|-------------|-----------|------------------|---------------|
| FR-001 | US-1 | Successful codex invocation, Prompt via stdin, Schema file lifecycle | TestCodexRunner_BuildCommand, TestCodexRunner_StdinDelivery, TestCodexRunner_BuildCommand_SchemaFile |
| FR-002 | US-3 | Eight reviewers dispatched | TestDispatch_DualProvider_AllSucceed |
| FR-003 | US-2 | Codex unavailable at startup | TestCodexAvailability_Unavailable, TestDispatch_ClaudeOnlyFallback |
| FR-004 | US-4 | Cross-provider duplicate merged, Single-provider finding | TestMerge_CrossProviderDuplicate, TestMerge_SingleProviderFinding |
| FR-005 | US-5 | Default team codex enabled/disabled | TestTeamConfig_WithCodex, TestTeamConfig_WithoutCodex |
| FR-006 | US-5 | Validation rejects codex non-reviewer | TestTeamConfig_ValidateCodexNonReviewer |
| FR-007 | US-6 | (implicit in all dispatch scenarios) | TestCodexRunner_BuildCommand (timeout field) |
| FR-008 | US-7 | Output schema file passed to codex | TestCodexRunner_BuildCommand_SchemaFile |
| FR-009 | US-7 | Retry with error context | TestCodexRunner_ParseOutput_InvalidJSON |
| FR-010 | US-3 | Escalation at 4+ failures | TestDispatch_DualProvider_FourFailures |
| FR-011 | US-3 | Reduced coverage 3 failures | TestDispatch_DualProvider_ThreeFailures |
| FR-012 | US-3 | Eight reviewers dispatched (file names) | TestDispatch_DualProvider_OutputFileNaming |
| FR-013 | US-1 | Successful codex invocation (cost=0) | TestCodexRunner_CostAlwaysZero |
| FR-014 | US-5 | Default team codex enabled | TestTeamConfig_WithCodex |
| FR-015 | US-5 | Configurable model selection | TestCodexRunner_BuildCommand_ModelFlag |
| FR-016 | US-5 | Backward-compatible name loading | TestTeamConfig_BackwardCompatNames |
| FR-017 | US-5 | Default team codex enabled (non-reviewer names) | TestTeamConfig_WithCodex |
| FR-018 | US-8 | Two holdout agents dispatched | TestHoldoutDispatch_DualProvider_AllSucceed, TestHandleHoldoutGeneration_DualProvider |
| FR-019 | US-8 | Claude-only holdout dispatch | TestHoldoutDispatch_ClaudeOnlyFallback, TestHandleHoldoutGeneration_CodexFallback |
| FR-020 | US-9 | Holdout outputs merged | TestHoldoutMerge_BothProviders |
| FR-021 | US-9 | Duplicate scenarios kept | TestHoldoutMerge_NoDeduplicate |
| FR-022 | US-8 | Codex holdout fails, claude proceeds | TestHoldoutDispatch_DualProvider_CodexFails |
| FR-023 | US-8 | Both holdout agents fail | TestHoldoutDispatch_DualProvider_BothFail |
| FR-024 | US-8 | Two holdout agents dispatched (file names) | TestHoldoutDispatch_DualProvider_OutputFileNaming |
| FR-025 | US-10 | Codex holdout schema enforcement | TestCodexRunner_HoldoutSchemaFile |
| FR-026 | US-11 | (implicit in dispatch config) | TestHoldoutDispatch_DualProvider_AllSucceed |

---

## 8. Assumptions

- Codex CLI version ≥0.114.0 (supports `--output-schema`, `--output-last-message`, `--ephemeral`)
- Codex authentication is pre-configured (via `codex login`) before workflow starts
- The `gpt-5.4` model is available to the user's codex account
- Codex can read files in the workspace directory to review the spec and generate holdouts
- Codex respects the `--output-schema` constraint and produces conforming JSON in most cases (for both ReviewerOutput and HoldoutOutput schemas)
- The same `enable_codex_reviewers` flag controls both codex reviewers and codex holdout agent (single toggle)
- Holdout generation failure is less critical than review failure — single-provider holdouts are acceptable

---

## 9. Holdout Evaluation Scenarios

> **These are for post-implementation verification only. NOT referenced in TDD plan or traceability matrix.**

**HE-1 (Happy Path)**: Start a fresh workflow with `enable_codex_reviewers: true` and codex installed. Let it run through REVIEWING state. Verify 8 output files appear in `specs/<feature>/` with correct naming pattern. Check `merged-findings-round-1.json` contains `raised_by` arrays with both `-claude` and `-codex` entries.

**HE-2 (Happy Path)**: Run a full review-revise-judge cycle with dual providers. Verify the judge's input includes findings attributed to both providers. Verify convergence tab in dashboard shows correct finding counts.

**HE-3 (Happy Path)**: Check the dashboard during a dual-provider review. Verify agent dispatch events show 8 reviewer names. Verify cost tracking shows claude reviewer costs (via OTEL) and $0 for codex reviewers.

**HE-4 (Error Path)**: Temporarily rename the `codex` binary. Start a workflow with `enable_codex_reviewers: true`. Verify warning appears in server logs. Verify exactly 4 claude reviewers run. Verify the review completes normally.

**HE-5 (Error Path)**: Set `codex_model` to an invalid model name (e.g., `nonexistent-model`). Start a review. Verify codex reviewers fail, retry logic fires, and eventually fall back to reduced coverage. Verify claude reviewers are unaffected.

**HE-6 (Edge Case)**: Run a review where both claude and codex find the exact same critical issue on the same section. Verify merge produces a single finding with both providers in `raised_by` and the higher severity retained.

**HE-7 (Edge Case)**: Set `enable_codex_reviewers: false`. Run a review. Verify only 4 claude reviewers run (named `reviewer-*-claude`). Verify no codex-related warnings or errors.

**HE-8 (Happy Path)**: With codex enabled, let a workflow reach HOLDOUT_GENERATION. Verify 2 holdout agents dispatch (holdout-claude and holdout-codex). Verify 4 output files appear. Verify `holdouts-round-1.md` contains attributed scenarios from both providers.

**HE-9 (Error Path)**: Kill the codex process during holdout generation. Verify holdout-claude completes normally. Verify `holdouts-round-1.md` contains only claude scenarios. Verify the workflow proceeds to REVISING/JUDGING without escalation.

**HE-10 (Edge Case)**: Compare scenarios from holdout-claude and holdout-codex for the same spec. Observe whether the two models generate meaningfully different evaluation scenarios — this validates the diversity hypothesis for holdout generation.

---

## 10. Implementation Notes

### Files to Create
- `internal/specworkflow/codex_runner.go` — CodexRunner implementing AgentRunner
- `internal/specworkflow/codex_runner_test.go` — Unit tests
- `internal/specworkflow/reviewer_output_schema.go` — JSON schema for ReviewerOutput (used by --output-schema)
- `internal/specworkflow/holdout_output_schema.go` — JSON schema for HoldoutOutput (used by --output-schema for holdout agent)
- `internal/specworkflow/holdout_merge.go` — Merge holdout outputs from multiple providers with attribution
- `internal/specworkflow/holdout_merge_test.go` — Holdout merge tests

### Files to Modify
- `internal/specworkflow/review_dispatch.go` — Accept optional second runner, dispatch 8 reviewers
- `internal/specworkflow/review_dispatch_test.go` — Add dual-provider test cases
- `internal/specworkflow/orchestrator_review.go` — Pass codex runner to dispatch, handle file naming
- `internal/specworkflow/orchestrator_holdout.go` — Dual-dispatch holdout agents (claude + codex), merge outputs
- `internal/specworkflow/team.go` — Add codex agents (reviewers + holdout), relax validation, backward compat
- `internal/specworkflow/team_test.go` — New validation tests
- `internal/specworkflow/config.go` — Add `EnableCodexReviewers`, `CodexModel`, `ReviewerTimeoutSeconds`, `HoldoutTimeoutSeconds`
- `internal/specworkflow/orchestrator.go` — Check codex availability, create CodexRunner, pass to holdout handler
- `internal/api/workflow_handler.go` — Pass codex config to orchestrator
- `internal/specworkflow/merge.go` — No algorithm changes needed (already handles N inputs)
- `internal/specworkflow/merge_test.go` — Add 8-input and cross-provider attribution tests

### Config Addition (YAML)
```yaml
# Dual-provider: set to false to use claude-only reviewers and holdout agents
enable_codex_reviewers: true
# Model for codex agents (reviewers + holdout, passed as -m flag)
codex_model: "gpt-5.4"
# Timeout for all reviewer agents (seconds)
reviewer_timeout_seconds: 300
# Timeout for holdout agents (seconds)
holdout_timeout_seconds: 300
```

### Holdout Merge Format

The merged holdout file (`holdouts-round-{N}.md`) uses provider attribution headers:

```markdown
# Holdout Evaluation Scenarios — Round 1

## Claude-Generated Scenarios

> Source: holdout-claude

### Scenario 1: [title]
...

### Scenario 2: [title]
...

## Codex-Generated Scenarios

> Source: holdout-codex

### Scenario 1: [title]
...

### Scenario 2: [title]
...
```

When only one provider succeeds, the file contains only that provider's section with an attribution note explaining the other provider's failure.
