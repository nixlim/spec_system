---
name: data-model-audit
description: >
  Audit a project's data model maturity across five levels: Existence,
  Completeness, Stability, Correspondence, and Sufficiency. Based on the
  principle that good data structures make good code inevitable. Examines
  schema files, migrations, OpenAPI specs, protobuf definitions, ORM models,
  and any data model definitions. Uses git history to measure schema-first
  development and churn rates. Produces a structured report with per-level
  scores and actionable recommendations. Use this skill whenever the user
  asks to "audit data model", "check schema quality", "data model review",
  "schema maturity", "evaluate data structures", "is my schema good",
  "review my database design", "check data model", or any request to assess
  whether a project's data model is well-designed or follows schema-first
  principles. Also trigger when someone asks about the relationship between
  their schema and their code, or whether their data model is complete enough
  to drive development.
allowed-tools: Read, Glob, Grep, Bash
---

# Data Model Audit

You evaluate a project's data model maturity using a five-level framework.
The core insight driving this audit comes from Linus Torvalds:

> "Bad programmers worry about the code. Good programmers worry about data
> structures and their relationships."

If the data model is right, the code tends to become straightforward. If the
data model is wrong, no amount of clever code fixes the problem. Your job is
to measure how well a project lives up to this principle.

## The Five Levels

Each level builds on the previous one. A project scores the highest level
where it passes — partial passes at a level mean the project hasn't fully
reached it yet.

### Level 1: Existence

**Question**: Does a schema definition exist, and was it created before the
bulk of the application code?

**What to check**:
- Search for schema files: SQL migrations, OpenAPI/Swagger specs, protobuf
  `.proto` files, GraphQL schemas, ORM model definitions, JSON Schema files,
  `schema.prisma`, `schema.graphql`, `*.avsc`, database seed files, ERD
  documents
- Use `git log --diff-filter=A --format='%ai %s' -- <schema-files>` to find
  when schema files were first committed
- Compare against the first commit dates of application code files
- Schema-first means the data model was a deliberate design decision, not an
  afterthought bolted on after the code

**Scoring**:
- **Pass**: Schema files exist AND were committed before or alongside the
  earliest application code
- **Partial**: Schema files exist but were added after significant code was
  already written
- **Fail**: No recognizable schema files, or data model is only implicit in
  code (e.g., inline SQL strings, ad-hoc struct definitions with no central
  schema)

### Level 2: Completeness

**Question**: Does the schema fully describe every entity, relationship,
field type, and constraint in the system?

**What to check**:
- Read all schema/migration files and build a mental model of the entities
- Cross-reference against the codebase: are there structs, classes, or
  tables referenced in code that have no schema definition?
- Check that relationships are explicit (foreign keys, references, join
  tables) rather than implicit (convention-based IDs with no declared
  relationship)
- Check that field types are specific (not generic `text` or `any` where a
  more constrained type would be appropriate)
- Check that constraints exist: NOT NULL, UNIQUE, CHECK constraints, enums
  for status fields, length limits
- Look for "shadow entities" — data structures that exist in code but not in
  the schema (in-memory caches that are really persistent state, config
  objects that behave like entities)

**Scoring**:
- **Pass**: Every entity in the codebase has a schema definition.
  Relationships are explicit. Types and constraints are specific and
  meaningful.
- **Partial**: Most entities are defined but some are missing, OR
  relationships exist only by naming convention, OR constraints are sparse
- **Fail**: Significant entities have no schema definition, or the schema is
  so incomplete that you can't understand the data model from it alone

### Level 3: Stability

**Question**: Does the schema change less frequently than the code, and is it
stabilizing over time?

**What to check**:
- Count commits that touch schema files vs. commits that touch application
  code files over the project's history
- Calculate a churn ratio: `schema_commits / total_commits`
- Look at the timeline: are schema changes front-loaded (lots early, few
  recently) or scattered throughout?
- A healthy pattern is heavy schema work early, then the schema becomes a
  stable foundation that code builds on
- An unhealthy pattern is constant schema changes interleaved with code
  changes — this suggests the data model is being discovered through code
  rather than designed upfront

Use git commands like:
```bash
# Schema file change frequency
git log --oneline -- <schema-files> | wc -l

# Total commits
git log --oneline | wc -l

# Schema changes over time (by month)
git log --format='%ai' -- <schema-files> | cut -d'-' -f1,2 | sort | uniq -c
```

**Scoring**:
- **Pass**: Schema churn ratio is notably lower than code churn, AND schema
  changes are front-loaded (stabilizing over time)
- **Partial**: Schema is somewhat stable but still sees regular changes mixed
  with code changes, OR churn is low but changes are scattered rather than
  front-loaded
- **Fail**: Schema changes as frequently as code, or schema churn is
  increasing over time

### Level 4: Correspondence

**Question**: Can you predict what the code does by reading only the schema?

**What to check**:
- Read the schema files without looking at the code
- Write down what you'd expect the application to do based on the entities,
  their relationships, and their constraints
- Then read the code and compare
- Look for surprises: business logic that has no reflection in the schema,
  entities that are used in ways the schema doesn't suggest, relationships
  that exist in code but not in the schema
- A good schema is a map of the system. If reading it gives you an accurate
  mental model of what the application does, it scores well here.

**Scoring**:
- **Pass**: Reading the schema gives you an accurate prediction of 80%+ of
  the application's behavior. The schema is a reliable map.
- **Partial**: The schema is a useful guide but there are significant
  behaviors or data flows that aren't reflected in it (50-80% predictable)
- **Fail**: The schema is misleading or so incomplete that reading it gives
  you a wrong impression of what the application does (<50% predictable)

### Level 5: Sufficiency

**Question**: Could a competent developer write the application using only
the schema?

**What to check**:
- This is the ultimate test. Look at the schema and ask: if I handed this to
  a skilled developer with no other context, could they build a working
  version of this application?
- The schema would need to encode not just structure but intent: what
  operations are valid, what state transitions are allowed, what invariants
  must hold
- Check for: documented workflows or state machines in the schema, clear
  naming that communicates purpose, constraints that encode business rules
  (not just data types), enum values that describe valid states

**Scoring**:
- **Pass**: The schema is so well-designed that a competent developer could
  build a functionally equivalent application from it alone. Business rules
  are encoded in constraints and naming. State machines are visible.
- **Partial**: A developer could build the CRUD layer from the schema but
  would need additional context for business logic, workflows, or edge cases
- **Fail**: The schema defines storage but not behavior — a developer would
  need extensive additional documentation or conversations to build the app

## Running the Audit

### Step 1: Discover schema files

Search broadly. Data models hide in many places:

```
**/*.sql, **/migrations/**, **/schema.*, **/*.proto, **/*.graphql,
**/*.avsc, **/openapi.*, **/swagger.*, **/*.prisma, **/models.py,
**/models/*.py, **/entities/**, **/domain/**, **/*_schema.*,
**/*_model.*, **/db/**, **/database/**
```

Also check for OpenAPI/Swagger YAML files, JSON Schema definitions, and
any file that looks like it defines data structure rather than behavior.

Record what you find and where. If you find nothing, the project fails
Level 1 immediately — but still document what data structures exist
implicitly in the code.

### Step 2: Evaluate each level in order

Work through levels 1-5 sequentially. For each level:
1. Run the checks described above
2. Collect specific evidence (file paths, git dates, code snippets)
3. Score: Pass / Partial / Fail
4. Write a brief explanation with the evidence

Stop evaluating higher levels once you hit a Fail — a project can't reach
Level 4 if it fails Level 3. But do note what the project would need to
reach the next level.

### Step 3: Produce the report

Use this structure:

```
# Data Model Audit Report

## Project: [name]
## Overall Maturity: Level [N] — [level name]
## Date: [date]

---

### Summary

[2-3 sentences on the overall state of the data model]

### Schema Files Found

| File | Type | First Committed |
|------|------|-----------------|
| ... | SQL migration | 2024-01-15 |

---

### Level 1: Existence — [Pass/Partial/Fail]

**Evidence**: [specific findings]

**Details**: [explanation]

### Level 2: Completeness — [Pass/Partial/Fail]

**Evidence**: [specific findings]

**Details**: [explanation]

[...continue for each level evaluated...]

---

### Recommendations

[Specific, actionable steps to reach the next maturity level. Focus on the
highest-impact changes — what would move the needle most. Don't list
everything that could theoretically be improved; pick the 2-3 changes that
would matter most.]

### Churn Analysis

| Period | Schema Commits | Code Commits | Ratio |
|--------|---------------|--------------|-------|
| ... | ... | ... | ... |

[Brief interpretation of the trend]
```

## Important Considerations

**Tech-stack awareness**: Different ecosystems express schemas differently.
A Rails project uses ActiveRecord migrations. A Go project might use SQL
migrations plus struct tags. A Node project might use Prisma or TypeORM
decorators. Don't penalize a project for using its ecosystem's conventions
— evaluate whether the data model is well-expressed within whatever
conventions the project uses.

**Monorepos and microservices**: If the project contains multiple services,
evaluate the data model holistically. Are cross-service relationships
documented? Is there a shared schema or are services defining overlapping
models independently?

**Schema evolution**: Migration files are a feature, not a bug. A project
with 50 well-structured migrations that tell a clear story of schema
evolution can score higher than a project with a single monolithic schema
file that gets rewritten periodically.

**Don't confuse ORM models with schema**: ORM model files (like Django
models.py or Go struct definitions) count as schema definitions only if
they are the source of truth that generates the actual database schema. If
they're just code-level representations that might drift from the real
schema, they're weaker evidence.
