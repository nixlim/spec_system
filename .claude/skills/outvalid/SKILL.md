---
name: outvalid
description: >
  Validate your JSON output against a workflow schema template and write it to
  disk. Use this whenever a prompt instructs you to produce structured JSON —
  run outvalid with the specified schema, read the errors, fix your output,
  and retry until validation passes.
argument-hint: --schema <template> --input <your-output.json> --writeTo <destination>
---

# outvalid: Validate and Write Structured Agent Output

Every workflow step that requires structured JSON output uses this skill.
The prompt you received will tell you which schema to use. Run `outvalid`,
read the errors, fix your draft, and retry.

## Tool

```
bin/outvalid
```

Add `bin/` to your PATH or invoke with the full relative path: `./bin/outvalid`

## Interface

```
outvalid --schema <schema-path> --input <your-draft.json> --writeTo <destination-path>
```

| Flag | Required | Description |
|---|---|---|
| `--schema` | yes | Path to the JSON Schema file for this workflow step |
| `--input` | yes | Your draft JSON file (or pipe via stdin) |
| `--writeTo` | no | Where to write the file if validation passes |

## Template library

All workflow schemas live in `workflow-templates/`:

```
workflow-templates/
  specworkflow/
    discovery-output.schema.json   — discovery agent output
    drafter-output.schema.json     — spec drafter output
    reviewer-output.schema.json    — reviewer agent output
    holdout-output.schema.json     — holdout generator output
    revision-output.schema.json    — revision agent output
    judge-output.schema.json       — judge agent output
  codedoc/
    discovery-output.schema.json   — codedoc discovery output
    drafter-output.schema.json     — codedoc drafter output
    reviewer-output.schema.json    — codedoc reviewer output
    judge-output.schema.json       — codedoc judge output
  codereview/
    fix-output.schema.json         — fix agent output
```

The prompt you are responding to will tell you which schema applies.

## Workflow

### Step 1 — Write your draft

Write your JSON output to a temp file. Use the Write tool:

```
/tmp/my-output-draft.json
```

Do not write directly to the destination until validation passes.

### Step 2 — Validate

```bash
outvalid \
  --schema workflow-templates/specworkflow/reviewer-output.schema.json \
  --input /tmp/my-output-draft.json \
  --writeTo workspace/round-1/reviewer-claude.json
```

**On success** (exit 0):

```
OUTVALID: OK — validation passed. Written to: workspace/round-1/reviewer-claude.json
```

Done. The file is written.

**On failure** (exit 1), you get numbered errors:

```
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
OUTVALID: FAILED — 3 schema error(s) found
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Fix ALL errors below, then re-run outvalid:

  [1] path: (root)
       'schema_version' is a required property

  [2] path: $.findings[0]
       'recommendation' is a required property

  [3] path: $.findings[0].severity
       'critical' is not one of ['CRITICAL', 'MAJOR', 'MINOR', 'OBSERVATION']
```

### Step 3 — Fix and retry

For each error, read the `path` to find the exact location, fix it in your
draft with the Edit tool, then re-run `outvalid`. Allow up to **3 fix
attempts**. If validation still fails after 3 tries, report the remaining
errors.

### Step 4 — Clean up

```bash
rm /tmp/my-output-draft.json
```

## Common errors

| Error | Fix |
|---|---|
| `'X' is a required property` | Add the missing field |
| `'foo' is not one of [...]` | Use an exact enum value — check the schema |
| `[] is too short` (minItems) | Array must have at least one item |
| Input is not valid JSON | Remove markdown fences; output must be pure JSON |
| `Additional properties are not allowed` | Remove the unexpected field |
