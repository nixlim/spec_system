# Holdout Test Generation Stage

## Overview

Add a dedicated holdout test generation stage to the adversarial spec review
workflow. A new `HOLDOUT_GENERATION` state is inserted between REVIEWING and
REVISING/JUDGING. A holdout agent generates and refines secret evaluation
scenarios each round, informed by the spec and review findings. Holdouts are
reviewed by all four lens groups but hidden from the reviser to prevent
"teaching to the test."

**Pipeline change:**

```
Before: Reviewing → Revising/Judging
After:  Reviewing → HoldoutGeneration → Revising/Judging
```

## Assumptions

- The holdout agent uses the same `AgentRunner` interface as other agents.
- Reviewer findings about holdouts use the standard severity scheme (CRIT-xxx, MAJ-xxx, etc.).
- Findings gain a structured `target` field (`"spec"` or `"holdout"`) for filtering.
- The drafter continues generating initial holdouts separately; these are never fed to the holdout agent.
- The holdout agent produces JSON output (like other agents) plus a separate markdown file.
- Holdout agent timeout is 5 minutes (300s), configured via `HoldoutTimeoutSeconds`.
- No cap on holdout scenario count — the agent determines appropriate coverage.

---

## User Stories

### US-1: Holdout Generation — Round 1 (P0)

The holdout agent generates an initial set of evaluation scenarios from the
freshly reviewed spec. After reviewers complete their first pass and findings
are merged, the holdout agent reads the current spec, the merged findings, and
source/technical documents to produce holdout tests that probe gaps, edge
cases, and subtle failure modes the implementation might miss. Output is written
as both `holdout-round-1.json` (metadata) and `holdouts-round-1.md` (scenarios).

**Why this priority**: Without round-1 generation, the entire feature has no
foundation.

**Independent Test**: Start a workflow, let it reach REVIEWING round 1, verify
holdout agent is dispatched after findings merge and produces valid output files.

**Acceptance Scenarios**:

1. **Given** reviewers have completed round 1 and merged findings exist, **When** the state machine transitions from REVIEWING, **Then** it enters HOLDOUT_GENERATION (not REVISING or JUDGING directly).
2. **Given** the workflow is in HOLDOUT_GENERATION round 1, **When** the holdout agent is dispatched, **Then** it receives the current spec path, merged findings path, and source doc paths.
3. **Given** the holdout agent completes successfully, **When** its output is written, **Then** both `holdout-round-1.json` and `holdouts-round-1.md` exist in the spec directory.
4. **Given** holdout generation completes in round 1 with CRITICAL/MAJOR findings present, **When** the transition occurs, **Then** the state moves to REVISING.
5. **Given** holdout generation completes in round 1 with zero CRITICAL/MAJOR findings, **When** the transition occurs, **Then** the state moves to JUDGING (zero-critical path).
6. **Given** the holdout agent fails after max retries, **When** the error is handled, **Then** the workflow escalates (same error handling pattern as other agents).

---

### US-2: Holdout Refinement — Round 2+ (P0)

In subsequent rounds, the holdout agent refines its evaluation scenarios based
on how the spec has evolved. It receives the current spec, this round's merged
findings, source docs, and its own previous round's holdouts. It produces an
updated `holdouts-round-{N}.md` that incorporates lessons from findings and
spec changes.

**Why this priority**: Without refinement, holdouts become stale across rounds.

**Independent Test**: Run a workflow through round 2, verify the holdout agent
receives `holdouts-round-1.md` as input and produces `holdouts-round-2.md`.

**Acceptance Scenarios**:

1. **Given** the workflow enters HOLDOUT_GENERATION in round 2, **When** the holdout agent is dispatched, **Then** it receives the previous round's holdout file path (`holdouts-round-1.md`) in addition to spec, findings, and source docs.
2. **Given** the workflow enters HOLDOUT_GENERATION in round N (N > 2), **When** the holdout agent is dispatched, **Then** it receives `holdouts-round-{N-1}.md` as the previous holdout baseline.
3. **Given** `holdouts-round-{N-1}.md` does not exist (e.g., first round after rewind), **When** the holdout agent is dispatched, **Then** it runs without previous holdouts (same as round 1 behavior).
4. **Given** the holdout agent completes in round 2+, **When** its output is written, **Then** `holdouts-round-{N}.md` exists and `holdouts-round-{N-1}.md` is preserved (not overwritten).

---

### US-3: Reviewer Holdout Coverage (P0)

All four reviewer lens groups (clarity, consistency, security, correctness)
review the holdout tests alongside the spec. Findings about holdouts use the
same severity scheme and merge naturally into the merged findings for the round.
Each finding carries a `target` field set to either `"spec"` or `"holdout"`.

**Why this priority**: If holdouts aren't reviewed, they may contain
ambiguities, gaps, or errors that undermine their value.

**Independent Test**: Run a round-2 review, verify reviewer prompts include the
holdout file path and that findings can reference holdout sections.

**Acceptance Scenarios**:

1. **Given** the workflow enters REVIEWING in round 2+, **When** reviewer prompts are built, **Then** each reviewer receives the latest holdout file path as additional review input.
2. **Given** the workflow enters REVIEWING in round 1, **When** reviewer prompts are built, **Then** no holdout file is included (holdouts don't exist yet in round 1).
3. **Given** a reviewer identifies an issue in a holdout scenario, **When** it produces a finding, **Then** the finding has `target: "holdout"` and uses the standard severity scheme.
4. **Given** multiple reviewers flag the same holdout issue, **When** findings are merged, **Then** deduplication works normally (same logic as spec findings).

---

### US-4: Reviser Information Isolation (P1)

The reviser must not see holdout files. It receives the spec and merged findings
but holdout-targeted findings (those with `target: "holdout"`) are filtered out
before being passed to the reviser. The reviser addresses only spec-side
findings.

**Why this priority**: Information isolation prevents the reviser from "teaching
to the test."

**Independent Test**: Inspect the reviser prompt in round 2+ and verify no
holdout file path is included and holdout-targeted findings are filtered out.

**Acceptance Scenarios**:

1. **Given** the workflow enters REVISING in any round, **When** the reviser prompt is built, **Then** no holdout file path is included in the prompt context.
2. **Given** merged findings contain findings with `target: "holdout"`, **When** the reviser prompt is built, **Then** those findings are excluded from the reviser's input.
3. **Given** merged findings contain findings with `target: "spec"`, **When** the reviser prompt is built, **Then** those findings are included normally.

---

### US-5: State Machine Integration (P0)

The workflow state machine gains a new `HOLDOUT_GENERATION` state positioned
between REVIEWING and REVISING/JUDGING.

**Transitions:**

- REVIEWING → HOLDOUT_GENERATION (always)
- HOLDOUT_GENERATION → REVISING (CRITICAL/MAJOR findings exist)
- HOLDOUT_GENERATION → JUDGING (zero-critical path)
- HOLDOUT_GENERATION → ESCALATED (on error/circuit breaker)
- HOLDOUT_GENERATION → ERROR (on agent failure)

**Why this priority**: Without state machine changes, the holdout step cannot
execute.

**Independent Test**: Verify all valid transitions from/to HOLDOUT_GENERATION
succeed and invalid transitions are rejected.

**Acceptance Scenarios**:

1. **Given** the state machine is in REVIEWING, **When** a transition to HOLDOUT_GENERATION is requested, **Then** it succeeds.
2. **Given** the state machine is in HOLDOUT_GENERATION with open CRITICAL/MAJOR findings, **When** a transition to REVISING is requested, **Then** it succeeds.
3. **Given** the state machine is in HOLDOUT_GENERATION with zero CRITICAL/MAJOR findings, **When** a transition to JUDGING is requested, **Then** it succeeds.
4. **Given** the state machine is in HOLDOUT_GENERATION, **When** a transition to REVIEWING is requested, **Then** it is rejected (invalid transition).
5. **Given** the state machine is in DRAFTING, **When** a transition to HOLDOUT_GENERATION is requested, **Then** it is rejected (only reachable from REVIEWING).

---

### US-6: Pipeline UI Stepper (P2)

The dashboard pipeline stepper shows the new HOLDOUT_GENERATION stage between
Review and Revise. It highlights correctly as the workflow progresses through it.

**Why this priority**: Operational visibility. Lower priority than core logic.

**Independent Test**: Load the dashboard during a workflow at HOLDOUT_GENERATION
state, verify the stepper shows the stage highlighted.

**Acceptance Scenarios**:

1. **Given** the workflow is in HOLDOUT_GENERATION, **When** the dashboard renders the pipeline stepper, **Then** a "Holdout" stage appears between "Review" and "Revise" and is highlighted as active.
2. **Given** the workflow has passed HOLDOUT_GENERATION, **When** the dashboard renders the stepper, **Then** the "Holdout" stage appears as completed.

---

### US-7: Finalization with Round Holdouts (P1)

When the workflow finalizes, the final spec assembly uses the latest
`holdouts-round-{N}.md` for the holdout section. The drafter-generated
`{feature}-holdouts.md` is preserved separately for comparison.

**Why this priority**: Without this, finalization uses stale drafter holdouts.

**Independent Test**: Finalize a workflow that completed 2 rounds, verify
`spec-final.md` contains content from `holdouts-round-2.md`.

**Acceptance Scenarios**:

1. **Given** a workflow finalizes after round N, **When** the final spec is assembled, **Then** the holdout section uses content from `holdouts-round-{N}.md`.
2. **Given** a workflow finalizes but no `holdouts-round-{N}.md` exists (legacy workflow), **When** the final spec is assembled, **Then** it falls back to `{feature}-holdouts.md`.
3. **Given** both drafter holdouts and round holdouts exist, **When** the final spec is assembled, **Then** the drafter holdouts file is not deleted.

---

### US-8: Resume and Rewind Support (P1)

The new state is rewindable and resumable. Rewind preserves all artefacts.
Resume correctly detects the state and expected output file.

**Acceptance Scenarios**:

1. **Given** a workflow in any state after HOLDOUT_GENERATION, **When** a rewind to HOLDOUT_GENERATION is requested, **Then** all artefacts are preserved and state is set to HOLDOUT_GENERATION.
2. **Given** a workflow in HOLDOUT_GENERATION after a crash, **When** resume probes the workspace, **Then** it detects the expected output file as `holdout-round-{N}.json`.
3. **Given** HOLDOUT_GENERATION is in the rewindable states list, **When** IsRewindable is called, **Then** it returns true.

---

## Edge Cases

- **Holdout agent produces empty output**: Treat as agent failure, trigger retry.
- **Previous holdout file referenced but missing**: Agent runs without previous holdouts (graceful degradation, same as round 1).
- **All findings are about holdouts, none about spec**: Reviser receives zero findings (after filtering on `target`). Holdout agent addresses all findings in next round. Spec is unchanged.
- **Source docs unavailable**: Holdout agent runs with spec + findings only (source docs are optional context).
- **Rewind to REVIEWING then resume**: Round re-runs REVIEWING → HOLDOUT_GENERATION → REVISING normally. Existing holdout files from this round are overwritten by new generation.

---

## Behavioral Contract

- When reviewers complete a round, the system transitions to HOLDOUT_GENERATION (not directly to REVISING or JUDGING).
- When the holdout agent is dispatched in round 1, it receives spec + findings + source docs and produces `holdout-round-1.json` and `holdouts-round-1.md`.
- When the holdout agent is dispatched in round N > 1, it additionally receives `holdouts-round-{N-1}.md`.
- When holdout generation completes and CRITICAL/MAJOR findings exist, the system transitions to REVISING.
- When holdout generation completes and zero CRITICAL/MAJOR findings exist, the system transitions to JUDGING.
- When the holdout agent fails after retries, the system escalates.
- When reviewers run in round 2+, they receive the latest holdout file as additional review input.
- When the reviser runs, it never receives holdout file paths or holdout-targeted findings.
- When the workflow finalizes, the holdout section uses the latest `holdouts-round-{N}.md`.

## Explicit Non-Behaviors

- The system must not give holdout files to the reviser, because the reviser would tailor the spec to pass holdout scenarios.
- The system must not give drafter holdouts to the holdout agent, because we want independent generation for comparison.
- The system must not delete drafter-generated holdouts during finalization, because they are preserved for quality comparison.
- The system must not skip holdout generation on the zero-critical path, because holdouts should be generated/updated regardless of finding severity.
- The holdout agent must not modify the spec file, because it is a holdout generator, not a reviser.

## Integration Boundaries

**AgentRunner (existing)**:
- Data in: Prompt string, output file path
- Data out: JSON output file, cost, duration
- Failure: Retry up to MaxRetries, then escalate
- Development: Uses existing ClaudeRunner — no new runner needed

**PromptBuilder (existing)**:
- Data in: File paths for spec, findings, holdouts, source docs
- Data out: Assembled prompt string
- Failure: Returns error if required files are unreadable
- Development: New method `BuildHoldoutPrompt` added to existing builder

---

## BDD Scenarios

### Feature: Holdout Test Generation

#### Scenario: Round 1 holdout generation dispatches after review

```gherkin
Scenario: Round 1 holdout generation
  Given the workflow has completed REVIEWING in round 1
  And merged findings "merged-findings-round-1.json" exist
  And the current spec is "spec-v0.md"
  When the state machine transitions from REVIEWING
  Then the state becomes HOLDOUT_GENERATION
  And the holdout agent is dispatched with:
    | input          | path                              |
    | spec           | spec-v0.md                        |
    | findings       | merged-findings-round-1.json      |
    | source_docs    | <source doc paths from goal>      |
  And the holdout agent output is written to "holdout-round-1.json"
  And "holdouts-round-1.md" is written by the agent

  Traces to: US-1 AS-1, AS-2, AS-3
  Category: Happy Path
```

#### Scenario: Round 2 holdout generation includes previous holdouts

```gherkin
Scenario: Round 2 holdout refinement
  Given the workflow has completed REVIEWING in round 2
  And merged findings "merged-findings-round-2.json" exist
  And "holdouts-round-1.md" exists from the previous round
  And the current spec is "spec-v1.md"
  When the holdout agent is dispatched
  Then it receives the previous holdout path "holdouts-round-1.md"
  And it produces "holdout-round-2.json" and "holdouts-round-2.md"
  And "holdouts-round-1.md" is preserved unchanged

  Traces to: US-2 AS-1, AS-4
  Category: Happy Path
```

#### Scenario: Missing previous holdouts treated as round 1

```gherkin
Scenario: Missing previous holdouts graceful degradation
  Given the workflow is in HOLDOUT_GENERATION round 3
  And "holdouts-round-2.md" does not exist
  When the holdout agent is dispatched
  Then it runs without a previous holdout path
  And it produces "holdout-round-3.json" and "holdouts-round-3.md"

  Traces to: US-2 AS-3
  Category: Edge Case
```

#### Scenario: Zero-critical path still generates holdouts

```gherkin
Scenario: Holdout generation on zero-critical path
  Given the workflow has completed REVIEWING with zero CRITICAL and zero MAJOR findings
  When the state transitions from REVIEWING
  Then it enters HOLDOUT_GENERATION (not JUDGING)
  And the holdout agent runs and produces holdouts
  And the state transitions to JUDGING after holdout generation completes

  Traces to: US-1 AS-5, US-5 AS-3
  Category: Alternate Path
```

#### Scenario: Post-holdout transition with findings

```gherkin
Scenario: Transition to REVISING when findings exist
  Given the workflow is in HOLDOUT_GENERATION
  And the findings summary has open_critical > 0
  When holdout generation completes
  Then the state transitions to REVISING

  Traces to: US-1 AS-4, US-5 AS-2
  Category: Happy Path
```

#### Scenario: Holdout agent failure escalates

```gherkin
Scenario: Agent failure after retries
  Given the workflow is in HOLDOUT_GENERATION
  And the holdout agent fails on dispatch
  When max retries are exhausted
  Then the workflow escalates from HOLDOUT_GENERATION

  Traces to: US-1 AS-6
  Category: Error Path
```

### Feature: Reviewer Holdout Integration

#### Scenario: Reviewers receive holdout file in round 2+

```gherkin
Scenario: Reviewer prompt includes holdouts
  Given the workflow enters REVIEWING in round 2
  And "holdouts-round-1.md" exists
  When reviewer prompts are built
  Then each of the 4 reviewer prompts includes the holdout file path

  Traces to: US-3 AS-1
  Category: Happy Path
```

#### Scenario: No holdouts available for round 1 review

```gherkin
Scenario: Round 1 reviewers have no holdouts
  Given the workflow enters REVIEWING in round 1
  And no holdout round files exist
  When reviewer prompts are built
  Then no holdout file path is included in the prompts

  Traces to: US-3 AS-2
  Category: Alternate Path
```

#### Scenario: Holdout findings use target field

```gherkin
Scenario: Holdout findings carry target field
  Given reviewer A produces a MAJOR finding about "Holdout scenario 3"
  When the finding is created
  Then the finding has target: "holdout"
  And the finding uses the standard severity ID scheme (MAJ-xxx)

  Traces to: US-3 AS-3
  Category: Happy Path
```

#### Scenario: Holdout findings merge normally

```gherkin
Scenario: Deduplication across holdout findings
  Given reviewer A produces a MAJOR finding about "Holdout scenario 3" with target "holdout"
  And reviewer C produces the same finding about "Holdout scenario 3" with target "holdout"
  When findings are merged
  Then deduplication applies normally based on (affected_section, lens, principle)

  Traces to: US-3 AS-4
  Category: Happy Path
```

### Feature: Reviser Information Isolation

#### Scenario: Reviser prompt excludes holdout file

```gherkin
Scenario: Reviser never sees holdouts
  Given the workflow enters REVISING in round 2
  And "holdouts-round-1.md" exists
  When the reviser prompt is built
  Then the holdout file path is not included in the prompt

  Traces to: US-4 AS-1
  Category: Happy Path
```

#### Scenario: Holdout-targeted findings filtered for reviser

```gherkin
Scenario: Reviser receives only spec-targeted findings
  Given merged findings contain:
    | finding_id | target  |
    | MAJ-001    | spec    |
    | MAJ-002    | holdout |
    | MIN-001    | spec    |
    | MIN-002    | holdout |
  When the reviser prompt is built
  Then only MAJ-001 and MIN-001 are included in the reviser's findings input

  Traces to: US-4 AS-2, AS-3
  Category: Happy Path
```

### Feature: State Machine Transitions

#### Scenario Outline: Valid and invalid HOLDOUT_GENERATION transitions

```gherkin
Scenario Outline: HOLDOUT_GENERATION transitions
  Given the state machine is in <from_state>
  When a transition to <to_state> is requested
  Then the transition <result>

  Examples:
    | from_state           | to_state             | result   |
    | REVIEWING            | HOLDOUT_GENERATION   | succeeds |
    | HOLDOUT_GENERATION   | REVISING             | succeeds |
    | HOLDOUT_GENERATION   | JUDGING              | succeeds |
    | HOLDOUT_GENERATION   | ESCALATED            | succeeds |
    | HOLDOUT_GENERATION   | ERROR                | succeeds |
    | DRAFTING             | HOLDOUT_GENERATION   | fails    |
    | HOLDOUT_GENERATION   | REVIEWING            | fails    |
    | JUDGING              | HOLDOUT_GENERATION   | fails    |
    | INIT                 | HOLDOUT_GENERATION   | fails    |
    | HOLDOUT_GENERATION   | DRAFTING             | fails    |

  Traces to: US-5 AS-1 through AS-5
  Category: Happy Path, Error Path
```

### Feature: Finalization

#### Scenario: Final spec uses round holdouts

```gherkin
Scenario: Finalization with round holdouts
  Given the workflow completed 2 rounds
  And "holdouts-round-2.md" exists
  And "b6-spec-holdouts.md" exists (drafter version)
  When the final spec is assembled
  Then the holdout section contains content from "holdouts-round-2.md"
  And "b6-spec-holdouts.md" is not deleted

  Traces to: US-7 AS-1, AS-3
  Category: Happy Path
```

#### Scenario: Finalization fallback to drafter holdouts

```gherkin
Scenario: No round holdouts available
  Given the workflow completed but no "holdouts-round-*.md" files exist
  And "b6-spec-holdouts.md" exists (drafter version)
  When the final spec is assembled
  Then the holdout section contains content from "b6-spec-holdouts.md"

  Traces to: US-7 AS-2
  Category: Alternate Path
```

### Feature: Resume and Rewind

#### Scenario: Rewind to HOLDOUT_GENERATION

```gherkin
Scenario: Rewind preserves artefacts
  Given a workflow in JUDGING round 1
  When a rewind to HOLDOUT_GENERATION round 1 is requested
  Then the state becomes HOLDOUT_GENERATION
  And all artefact files are preserved

  Traces to: US-8 AS-1, AS-3
  Category: Happy Path
```

#### Scenario: Resume from HOLDOUT_GENERATION

```gherkin
Scenario: Resume detects missing holdout output
  Given a workflow-state.json with state HOLDOUT_GENERATION round 1
  And "holdout-round-1.json" does not exist
  When ResumeWorkflow probes the workspace
  Then NeedsAgentRedispatch is true
  And MissingOutputFile is "specs/{feature}/holdout-round-1.json"

  Traces to: US-8 AS-2
  Category: Happy Path
```

---

## Test-Driven Development Plan

### Test Implementation Order

| Order | Test Name | Level | Traces to BDD | Description |
|-------|-----------|-------|---------------|-------------|
| 1 | TestStateHoldoutGenerationConstant | Unit | US-5 | HOLDOUT_GENERATION state constant exists with correct string |
| 2 | TestHoldoutGenerationTransitions | Unit | US-5 AS-1..5 | All valid/invalid transitions to/from HOLDOUT_GENERATION |
| 3 | TestIsRewindableIncludesHoldoutGeneration | Unit | US-8 AS-3 | HOLDOUT_GENERATION in RewindableStates |
| 4 | TestExpectedOutputFileHoldoutGeneration | Unit | US-8 AS-2 | ExpectedOutputFile returns correct path |
| 5 | TestBuildHoldoutPromptRound1 | Unit | US-1 AS-2 | Prompt includes spec, findings, source docs; no previous holdouts |
| 6 | TestBuildHoldoutPromptRound2 | Unit | US-2 AS-1 | Prompt includes previous holdout file path |
| 7 | TestBuildHoldoutPromptMissingPreviousHoldouts | Unit | US-2 AS-3 | Prompt builds successfully without previous holdouts |
| 8 | TestBuildReviewerPromptIncludesHoldoutsRound2 | Unit | US-3 AS-1 | Reviewer prompt includes holdout path in round 2+ |
| 9 | TestBuildReviewerPromptExcludesHoldoutsRound1 | Unit | US-3 AS-2 | Reviewer prompt omits holdout path in round 1 |
| 10 | TestBuildReviserPromptExcludesHoldouts | Unit | US-4 AS-1 | Reviser prompt never includes holdout file path |
| 11 | TestFilterFindingsByTarget | Unit | US-4 AS-2,3 | Holdout-targeted findings filtered from reviser input |
| 12 | TestMergedFindingTargetField | Unit | US-3 AS-3 | MergedFinding struct has Target field, defaults to "spec" |
| 13 | TestHoldoutOutputStruct | Unit | US-1 AS-3 | HoldoutOutput JSON schema validates correctly |
| 14 | TestHandleHoldoutGenerationRound1 | Integration | US-1 AS-1..5 | Full handler: mock agent, verify dispatch inputs and transition |
| 15 | TestHandleHoldoutGenerationRound2 | Integration | US-2 AS-1..4 | Handler round 2: previous holdouts passed to agent |
| 16 | TestHandleHoldoutGenerationZeroCritical | Integration | US-1 AS-5 | HOLDOUT_GENERATION → JUDGING on zero-critical path |
| 17 | TestHandleHoldoutGenerationAgentFailure | Integration | US-1 AS-6 | Escalation on agent failure |
| 18 | TestReviewingTransitionsToHoldoutGeneration | Integration | US-5 AS-1 | REVIEWING handler → HOLDOUT_GENERATION |
| 19 | TestReviserReceivesNoHoldoutFindings | Integration | US-4 AS-2 | End-to-end filtering of holdout findings |
| 20 | TestAssembleFinalSpecWithRoundHoldouts | Integration | US-7 AS-1 | Finalization uses holdouts-round-N.md |
| 21 | TestAssembleFinalSpecFallbackToDrafterHoldouts | Integration | US-7 AS-2 | Fallback to drafter holdouts |
| 22 | TestRewindToHoldoutGeneration | Integration | US-8 AS-1 | Rewind preserves artefacts, sets state |
| 23 | TestResumeFromHoldoutGeneration | Integration | US-8 AS-2 | Resume detects missing holdout output |

### Test Dataset: Finding Target Filtering

| # | Finding ID | Target | Expected in Reviser Input | Traces to |
|---|-----------|--------|--------------------------|-----------|
| 1 | CRIT-001 | spec | Yes | US-4 AS-3 |
| 2 | MAJ-001 | holdout | No | US-4 AS-2 |
| 3 | MAJ-002 | spec | Yes | US-4 AS-3 |
| 4 | MIN-001 | holdout | No | US-4 AS-2 |
| 5 | MIN-002 | spec | Yes | US-4 AS-3 |
| 6 | OBS-001 | holdout | No | US-4 AS-2 |
| 7 | MAJ-003 | spec | Yes | US-4 AS-3 |
| 8 | CRIT-002 | holdout | No | US-4 AS-2 |
| 9 | OBS-002 | (empty) | Yes (defaults to spec) | US-4 AS-3 |

### Test Dataset: State Transitions

| # | From State | To State | Expected | Traces to |
|---|-----------|----------|----------|-----------|
| 1 | REVIEWING | HOLDOUT_GENERATION | Success | US-5 AS-1 |
| 2 | HOLDOUT_GENERATION | REVISING | Success | US-5 AS-2 |
| 3 | HOLDOUT_GENERATION | JUDGING | Success | US-5 AS-3 |
| 4 | HOLDOUT_GENERATION | ESCALATED | Success | US-5 |
| 5 | HOLDOUT_GENERATION | ERROR | Success | US-5 |
| 6 | DRAFTING | HOLDOUT_GENERATION | Fail | US-5 AS-5 |
| 7 | HOLDOUT_GENERATION | REVIEWING | Fail | US-5 AS-4 |
| 8 | JUDGING | HOLDOUT_GENERATION | Fail | US-5 |
| 9 | INIT | HOLDOUT_GENERATION | Fail | US-5 |
| 10 | HOLDOUT_GENERATION | DRAFTING | Fail | US-5 |

### Test Dataset: Holdout File Resolution for Finalization

| # | Round Holdout Exists | Drafter Holdout Exists | Expected Source | Traces to |
|---|---------------------|----------------------|-----------------|-----------|
| 1 | holdouts-round-2.md | feature-holdouts.md | holdouts-round-2.md | US-7 AS-1 |
| 2 | holdouts-round-1.md | feature-holdouts.md | holdouts-round-1.md | US-7 AS-1 |
| 3 | (none) | feature-holdouts.md | feature-holdouts.md | US-7 AS-2 |
| 4 | (none) | (none) | no holdout section | US-7 |

### Regression Test Requirements

This feature modifies existing functionality in the following areas:

1. **REVIEWING handler**: Currently transitions to REVISING or JUDGING. Will now transition to HOLDOUT_GENERATION. Existing tests for REVIEWING → REVISING and REVIEWING → JUDGING must be updated to expect REVIEWING → HOLDOUT_GENERATION.
2. **Reviewer prompts**: Gain an optional holdout file path in round 2+. Existing reviewer prompt tests must continue passing for round 1 (no holdout).
3. **Reviser prompts**: Must continue to NOT include holdout paths (existing behavior preserved by design). Existing reviser prompt tests should be augmented to verify holdout exclusion.
4. **Finalize**: Changes holdout file source. Existing finalize tests for drafter holdouts become the fallback case.
5. **State machine transition table**: New state and transitions. Existing transition tests must be updated.
6. **MergedFinding struct**: Gains a `Target` field. Existing merge tests must be verified to work with default `Target` value.

---

## Functional Requirements

- **FR-001**: System MUST add a `HOLDOUT_GENERATION` state to the workflow state machine between REVIEWING and REVISING/JUDGING.
- **FR-002**: System MUST dispatch a holdout agent after findings are merged in REVIEWING, before transitioning to REVISING or JUDGING.
- **FR-003**: System MUST provide the holdout agent with current spec path, merged findings path, and source doc paths.
- **FR-004**: System MUST provide the holdout agent with the previous round's holdout file path in round 2+.
- **FR-005**: System MUST write holdout agent JSON output to `holdout-round-{N}.json` in the spec directory.
- **FR-006**: System MUST write holdout scenarios to `holdouts-round-{N}.md` in the spec directory.
- **FR-007**: System MUST preserve previous round holdout files (not overwrite).
- **FR-008**: System MUST include the latest holdout file in reviewer prompts for round 2+.
- **FR-009**: System MUST NOT include holdout files in reviewer prompts for round 1.
- **FR-010**: System MUST NOT include holdout file paths in the reviser prompt.
- **FR-011**: System MUST filter findings with `target: "holdout"` from the reviser's input.
- **FR-012**: System MUST transition HOLDOUT_GENERATION → REVISING when CRITICAL/MAJOR findings exist.
- **FR-013**: System MUST transition HOLDOUT_GENERATION → JUDGING when zero CRITICAL/MAJOR findings exist.
- **FR-014**: System MUST use the latest `holdouts-round-{N}.md` in final spec assembly.
- **FR-015**: System MUST fall back to drafter holdouts (`{feature}-holdouts.md`) when no round holdouts exist.
- **FR-016**: System MUST NOT delete drafter-generated holdouts during finalization.
- **FR-017**: System MUST include HOLDOUT_GENERATION in rewindable states.
- **FR-018**: System MUST report `holdout-round-{N}.json` as the expected output file for resume detection.
- **FR-019**: System MUST display HOLDOUT_GENERATION in the dashboard pipeline stepper between Review and Revise.
- **FR-020**: System MUST escalate from HOLDOUT_GENERATION on agent failure after max retries.
- **FR-021**: System SHOULD gracefully degrade when previous holdout files are missing (run as round 1).
- **FR-022**: System MUST add a `Target` field to `MergedFinding` with values `"spec"` (default) or `"holdout"`.
- **FR-023**: System MUST add a `HoldoutTimeoutSeconds` config field defaulting to 300.

## Success Criteria

- **SC-001**: All 23 tests in the TDD plan pass.
- **SC-002**: A workflow started from INIT reaches HOLDOUT_GENERATION after the first REVIEWING phase.
- **SC-003**: `holdout-round-1.json` and `holdouts-round-1.md` are present in the spec directory after the first HOLDOUT_GENERATION phase completes.
- **SC-004**: In round 2, the reviewer prompts contain a reference to `holdouts-round-1.md`.
- **SC-005**: In round 2, the reviser prompt contains zero references to any holdout file and zero holdout-targeted findings.
- **SC-006**: The dashboard pipeline stepper displays "Holdout" between "Review" and "Revise".
- **SC-007**: A finalized workflow's `spec-final.md` contains holdout content from `holdouts-round-{N}.md`.

## Traceability Matrix

| Requirement | User Story | BDD Scenario(s) | Test(s) |
|-------------|-----------|------------------|---------|
| FR-001 | US-5 | Valid HOLDOUT_GENERATION transitions | 1, 2 |
| FR-002 | US-1 | Round 1 holdout generation | 14, 18 |
| FR-003 | US-1 | Round 1 holdout generation | 5, 14 |
| FR-004 | US-2 | Round 2 holdout refinement | 6, 15 |
| FR-005 | US-1 | Round 1 holdout generation | 13, 14 |
| FR-006 | US-1 | Round 1 holdout generation | 14 |
| FR-007 | US-2 | Round 2 holdout refinement | 15 |
| FR-008 | US-3 | Reviewer prompt includes holdouts | 8 |
| FR-009 | US-3 | Round 1 reviewers have no holdouts | 9 |
| FR-010 | US-4 | Reviser never sees holdouts | 10 |
| FR-011 | US-4 | Reviser receives only spec-targeted findings | 11, 19 |
| FR-012 | US-5 | Transition to REVISING when findings exist | 14, 16 |
| FR-013 | US-5 | Zero-critical path holdouts | 16 |
| FR-014 | US-7 | Finalization with round holdouts | 20 |
| FR-015 | US-7 | Fallback to drafter holdouts | 21 |
| FR-016 | US-7 | Finalization with round holdouts | 20 |
| FR-017 | US-8 | Rewind to HOLDOUT_GENERATION | 3, 22 |
| FR-018 | US-8 | Resume from HOLDOUT_GENERATION | 4, 23 |
| FR-019 | US-6 | (UI verification) | Manual |
| FR-020 | US-1 | Agent failure after retries | 17 |
| FR-021 | US-2 | Missing previous holdouts | 7 |
| FR-022 | US-3 | Holdout findings carry target field | 12 |
| FR-023 | US-1 | (Config) | 14 |

---

## Holdout Agent Output Schema

```json
{
  "schema_version": "1.0",
  "agent": "holdout",
  "round": 1,
  "holdout_file": "holdouts-round-1.md",
  "scenario_count": 12,
  "categories": {
    "happy_path": 4,
    "error_path": 3,
    "edge_case": 3,
    "alternate_path": 2
  },
  "findings_addressed": ["MAJ-002", "MIN-004"],
  "changelog": "Initial holdout generation from spec-v0.md and 78 merged findings."
}
```

**Fields:**

- `schema_version`: Always `"1.0"`.
- `agent`: Always `"holdout"`.
- `round`: The review round number.
- `holdout_file`: Path to the generated markdown holdout file (relative to spec dir).
- `scenario_count`: Total number of evaluation scenarios generated.
- `categories`: Breakdown by scenario category.
- `findings_addressed`: List of finding IDs (from merged findings) that informed new or revised scenarios. Empty in round 1 if no holdout-targeted findings exist.
- `changelog`: Human-readable summary of what changed from previous round (or "Initial generation" for round 1).

---

## Implementation Order

### Phase 1: Data Model & State Machine (Tests 1-4, 12-13)

1. Add `StateHoldoutGeneration` constant to `types.go`
2. Add transitions to `statemachine.go`
3. Add `Target` field to `MergedFinding` in `agent_output.go`
4. Add `HoldoutOutput` struct to `agent_output.go`
5. Add `HoldoutTimeoutSeconds` to config
6. Add to `RewindableStates` and `ExpectedOutputFile`

### Phase 2: Prompt Building (Tests 5-11)

7. Add `BuildHoldoutPrompt` to `prompts.go`
8. Update `BuildReviewerPrompt` to accept optional holdout path
9. Add `FilterFindingsByTarget` utility function
10. Verify reviser prompt exclusion

### Phase 3: Orchestration (Tests 14-19)

11. Create `orchestrator_holdout.go` with `handleHoldoutGeneration`
12. Update `handleReviewing` to transition to HOLDOUT_GENERATION
13. Update `handleRevising` to filter holdout findings
14. Add HOLDOUT_GENERATION case to main orchestrator loop

### Phase 4: Finalization & Infrastructure (Tests 20-23)

15. Update `finalize.go` to use round holdouts with fallback
16. Update `resume.go` for new state
17. Update pipeline stepper in `app.js` and `events.go`

---

## Holdout Evaluation Scenarios

> These are post-implementation verification scenarios. Do NOT reference
> in the TDD plan or traceability matrix.

1. **Happy Path — Full round trip**: Start a workflow, let it complete round 1 with findings. Verify `holdouts-round-1.md` exists and contains evaluation scenarios that reference specific spec sections.
2. **Happy Path — Multi-round refinement**: Complete 3 rounds. Compare `holdouts-round-1.md`, `holdouts-round-2.md`, and `holdouts-round-3.md`. Verify scenarios evolve — new ones added for new findings, existing ones refined.
3. **Happy Path — Reviewer catches holdout issue**: In round 2, verify a reviewer can produce a finding about a holdout scenario. Verify that finding has `target: "holdout"` and the reviser never sees it.
4. **Error — Agent timeout**: Set `HoldoutTimeoutSeconds` to 1. Verify the agent times out and the workflow escalates after retries.
5. **Error — Corrupt previous holdouts**: Replace `holdouts-round-1.md` with invalid content. Verify round 2 holdout agent still produces valid output (treats as no previous holdouts or adapts).
6. **Edge Case — Zero findings round**: Run a round where reviewers find zero issues. Verify holdout generation still runs and produces scenarios.
7. **Edge Case — All findings target holdouts**: Create a round where every finding has `target: "holdout"`. Verify the reviser receives zero findings and the spec is unchanged, while holdout agent addresses them in the next round.
