# Spec Output Template

This is the structural template for the output document. Fill in every section.
Replace all `[bracketed placeholders]` with actual content. Remove this header
block from the final output.

---

# Feature Specification: [Feature Name]

**Created**: [YYYY-MM-DD]
**Status**: Draft
**Input**: [Brief description of what triggered this spec, or link to source document]

---

## Available Reference Patterns

> Patterns from `docs/reference/go-implementation/` relevant to this feature.
> Remove this section if no reference patterns apply.

| Reference File | Pattern | Relevance to This Feature |
|----------------|---------|---------------------------|
| [filename] | [pattern name] | [how it maps — what to reuse, what to adapt] |

---

## Existing Codebase Context

> Populated by GitNexus code graph analysis. Remove this section if the GitNexus
> index is empty or unavailable.

### Symbols Involved

| Symbol | Role | GitNexus Context |
|--------|------|-----------------|
| [name] | [calls / modifies / extends] | [summary from gitnexus_context] |

### Impact Assessment

| Symbol Modified | Risk Level | d=1 Dependents | d=2 Dependents |
|----------------|------------|----------------|----------------|
| [name] | [LOW/MEDIUM/HIGH/CRITICAL] | [list] | [list] |

### Relevant Execution Flows

| Process Name | Relevance |
|-------------|-----------|
| [name] | [how this feature interacts with it] |

### Cluster Placement

This feature belongs to the **[cluster name]** cluster.
[Note if it spans multiple clusters and architectural implications.]

---

## User Stories & Acceptance Criteria

### User Story 1 — [Title] (Priority: P[0-4])

[Narrative paragraph: A [role/actor] wants to [action] so that [benefit].
Describe the current pain point and how this story addresses it.]

**Why this priority**: [Justify why this story has this priority relative to others.]

**Independent Test**: [Describe how this story can be verified in isolation,
delivering value even if other stories are not yet implemented.]

**Acceptance Scenarios**:

1. **Given** [precondition], **When** [action], **Then** [expected outcome].
2. **Given** [precondition], **When** [action], **Then** [expected outcome].

---

### User Story 2 — [Title] (Priority: P[0-4])

[Repeat the same structure for each user story.]

---

## Behavioral Contract

Primary flows:
- When [condition], the system [behavior].

Error flows:
- When [error condition], the system [behavior].

Boundary conditions:
- When [boundary condition], the system [behavior].

---

## Edge Cases

- [What happens when [unusual condition]? Expected: [behaviour].]
- [What happens when [boundary condition]? Expected: [behaviour].]
- [What happens when [error condition]? Expected: [behaviour].]

---

## Explicit Non-Behaviors & Safeguards

### Qualitative Prohibitions

- The system must not [behavior] because [reason].
- The system must not [behavior] because [reason].
- [Include behaviors an AI agent might "helpfully" add beyond scope]
- [Include scope boundaries that need enforcement]
- [Include security/safety boundaries]

### Machine-Verifiable Constraints

> Adapt categories to the feature type. HTTP APIs get status codes and error
> messages. CLI tools get exit codes and output format constraints. File
> processors get format constraints and size limits. Include only categories
> relevant to this feature.

**Error Codes / Messages** (for APIs):
- When [boundary violation], the system MUST return HTTP [status code] with body `[exact error message or format]`.

**CLI Exit Codes** (for CLI tools):
- When [condition], the tool MUST exit with code [N] and print `[message]` to stderr.

**Performance Bounds**:
- [Metric] MUST be [operator] [threshold] [unit] at [measurement condition].

**Scope Boundaries**:
- The system MUST NOT [extend to / accept / process] [boundary] because [reason].

**Data Constraints**:
- [Field/input] MUST be [constraint with exact values, ranges, or formats].

### Conservative Type Design

> See `docs/reference/conservative-type-design.md` for the full principle and
> language-specific examples.

Do not introduce a user-defined nominal type unless it carries invariants,
methods, or domain semantics that the underlying built-in type cannot express.

---

## Prerequisites

> Hardware, OS, runtimes, services, and network assumptions a user must have
> before they can build or run this feature. Pull these directly from the
> source documents — do not invent. If none of this applies to the feature
> (e.g. pure algorithm work), write `[None applicable for this feature]` in
> each subsection rather than omitting them.

- **Hardware / OS**: [e.g. "macOS arm64 or Linux x86_64; 16 GB RAM minimum"]
- **Required runtimes**: [e.g. "Go 1.22+", "Node 20+", "Python 3.11+"]
- **Required services**: [e.g. "Docker 24+", "PostgreSQL 15+", "Redis 7+"]
- **Network assumptions**: [e.g. "outbound HTTPS to api.example.com", "offline operation supported"]
- **Accounts / credentials**: [e.g. "no external accounts required", "AWS credentials with S3 read"]

---

## Development Setup

> Exact commands to go from a clean checkout to a working local system.
> Transcribe verbatim from source documents where possible. Numbered steps,
> each with the literal command. If no setup applies, write
> `[None applicable for this feature]`.

1. `[clone / cd]`
2. `[dependency install, e.g. go mod download]`
3. `[service start, e.g. docker compose up -d]`
4. `[bootstrap / migration, e.g. make migrate]`
5. `[smoke check, e.g. ./bin/feature status]`

**Expected first-run behaviour**: [what the user should see when setup succeeds]

**Common first-run failures**: [known issues from source docs, e.g. "if docker
pull fails, check that the Neo4j image is available"]

---

## Tech Stack

> Languages, frameworks, datastores, and external services the feature
> depends on, with version pins where the source material specifies them.
> If a topic is not applicable, write `[None]` for that row rather than
> deleting it.

| Category | Choice | Version / Pin | Source |
|----------|--------|---------------|--------|
| Language | [e.g. Go] | [e.g. 1.22] | [source doc file/line] |
| Runtime / container | [e.g. Docker Compose] | [e.g. 2.x] | [source] |
| Datastore(s) | [e.g. Neo4j, Weaviate] | [versions] | [source] |
| External APIs | [e.g. Ollama] | [endpoint] | [source] |
| Build tool | [e.g. Makefile / go build] | — | [source] |
| Test framework | [e.g. go test, testify] | — | [source] |

---

## Deployment / Runtime

> How this feature runs in its target environment. Offline vs online
> requirements, resource limits, startup/shutdown semantics, and operational
> commands. If not applicable, write `[None applicable for this feature]`.

- **Target environment**: [e.g. "local workstation only", "single-node VPS", "Kubernetes cluster"]
- **Online / offline**: [e.g. "fully offline-capable once models are pre-cached", "requires outbound HTTPS"]
- **Resource limits**: [e.g. "< 4 GB RAM steady-state", "< 1 CPU core idle"]
- **Start / stop commands**: [e.g. `cortex up`, `cortex down`, `cortex status`]
- **Health check**: [e.g. `cortex doctor` verifies all dependencies are reachable]
- **Logs / telemetry**: [where the feature writes logs and metrics]

---

## Integration Boundaries

### [External System Name]

- **Data in**: [what this system receives]
- **Data out**: [what this system returns]
- **Contract**: [request/response format, protocol, auth]
- **On failure**: [behavior when unavailable or returning errors]
- **Development**: [real service | mock/simulated twin] — [reason]

---

## BDD Scenarios

### Feature: [Feature Name]

#### Scenario: [Descriptive Scenario Title]

**Traces to**: User Story [N], Acceptance Scenario [M]
**Category**: [Happy Path | Alternate Path | Error Path | Edge Case]

- **Given** [precondition]
- **And** [additional precondition, if needed]
- **When** [action]
- **And** [additional action, if needed]
- **Then** [expected outcome]
- **And** [additional assertion, if needed]

---

#### Scenario Outline: [Descriptive Title for Parameterised Scenario]

**Traces to**: User Story [N], Acceptance Scenario [M]
**Category**: [Happy Path | Alternate Path | Error Path | Edge Case]

- **Given** [precondition with `<placeholder>`]
- **When** [action with `<placeholder>`]
- **Then** [expected outcome with `<placeholder>`]

**Examples**:

| placeholder_1 | placeholder_2 | expected |
|---------------|---------------|----------|
| value_a       | value_b       | result_a |
| value_c       | value_d       | result_b |

---

[Repeat for all scenarios. Group by User Story for readability.]

---

## Test-Driven Development Plan

### Test Hierarchy

| Level       | Scope                        | Purpose                                    |
|-------------|------------------------------|--------------------------------------------|
| Unit        | [Individual functions/methods]| [Validates logic in isolation]              |
| Integration | [Module interactions]        | [Validates components work together]        |
| E2E         | [Full user workflows]        | [Validates complete feature from user view] |

### Test Implementation Order

Write these tests BEFORE implementing the feature code. Order: unit first,
then integration, then E2E. Within each level, order by dependency.

| Order | Test Name | Level | Traces to BDD Scenario | Description |
|-------|-----------|-------|------------------------|-------------|
| 1     | [test_name] | Unit | Scenario: [title] | [What this test verifies] |
| 2     | [test_name] | Unit | Scenario: [title] | [What this test verifies] |
| ...   | ...       | Integration | Scenario: [title] | ... |
| ...   | ...       | E2E  | Scenario: [title] | ... |

### Test Datasets

#### Dataset: [Context — e.g., "Email Input Validation"]

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|-------|---------------|-----------------|-----------|-------|
| 1 | [value] | [type] | [expected] | BDD Scenario: [title] | [note] |
| 2 | [value] | [type] | [expected] | BDD Scenario: [title] | [note] |

[Repeat dataset tables for each distinct input domain or validation context.]

### Regression Test Requirements

**If modifying existing functionality:**

| Existing Behaviour | Existing Test | New Regression Test Needed | Notes |
|--------------------|---------------|---------------------------|-------|
| [behaviour]        | [test name]   | [Yes/No — if yes, name]   | [why] |

**If new functionality:**

> No regression impact — new capability. Integration seams protected by: [list of existing tests covering the boundary].

---

## Functional Requirements

- **FR-001**: System MUST [requirement].
- **FR-002**: System SHOULD [requirement].
- **FR-003**: System MAY [requirement].

---

## Success Criteria

- **SC-001**: [Measurable outcome with specific threshold — e.g., "Response time under 200ms at p95 for 1000 concurrent users."]
- **SC-002**: [Observable pass/fail condition — e.g., "All exported files are valid JSON parseable by `jq`."]

---

## Traceability Matrix

| Requirement | User Story | BDD Scenario(s)          | Test Name(s)            |
|-------------|-----------|--------------------------|-------------------------|
| FR-001      | US-1      | Scenario: [title]        | [test_name]             |
| FR-002      | US-1, US-2| Scenario: [t1], [t2]     | [test_a], [test_b]      |

**Completeness check**: Every FR-xxx row must have at least one BDD scenario
and one test. Every BDD scenario must appear in at least one row.

---

## Ambiguity Warnings

| # | What's Ambiguous | Likely Agent Assumption | Question to Resolve |
|---|------------------|------------------------|---------------------|
| 1 | [gap in spec]    | [what agent would do]  | [question for user] |

---

## Evaluation Scenarios (Holdout)

> **Note**: These scenarios are for post-implementation evaluation only.
> They must NOT be visible to the implementing agent during development.
> Do not reference these in the TDD plan or traceability matrix.

### Scenario: [Title]
- **Setup**: [initial conditions]
- **Action**: [what is done]
- **Expected outcome**: [observable result]
- **Category**: [Happy Path | Error | Edge Case]

### Scenario: [Title]
- **Setup**: [initial conditions]
- **Action**: [what is done]
- **Expected outcome**: [observable result]
- **Category**: [Happy Path | Error | Edge Case]

---

## Assumptions

- [Assumption about environment, dependencies, user behaviour, or infrastructure.]
- [Assumption about what is NOT in scope.]

## Clarifications

### [YYYY-MM-DD]

- Q: [Question raised during discovery] -> A: [Answer or decision made.]
- Q: [Another question] -> A: [Answer.]
