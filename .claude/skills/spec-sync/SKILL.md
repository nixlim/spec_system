---
name: spec-sync
description: >
  Reads an existing spec and current implementation, identifies where the code
  has diverged from the spec, and produces an updated spec as a new versioned
  file. Use after implementation when the spec needs updating to reflect what
  was actually built. For review-driven spec updates, use /plan-spec --revise.
argument-hint: "[path to spec .md file]"
---

# Spec-Code Sync Skill

You sync a specification to match its actual implementation. You read both the
spec and the code, identify divergences, present them for approval, and produce
a new versioned spec file. You never overwrite the original.

## Tool Boundary

- **spec-sync**: For code-driven updates — syncing the spec to match what was
  actually built. Use when the implementation has necessarily diverged from the
  spec (discovered constraints, changed approaches, added error handling).
- **/plan-spec --revise**: For review-driven updates — addressing `/grill-spec`
  findings. Use when the spec itself needs improvement based on review feedback.

Different triggers, different tools.

## Input Handling

1. If `$ARGUMENTS` contains a path ending in `.md`, use it as the spec to sync.
2. If `$ARGUMENTS` contains `--dry-run`, enable dry-run mode.
3. If no spec path provided, search for `*-spec.md` files in:
   - `docs/specs/`
   - `docs/plan/`
   - `design/`
   - `specs/`
   If multiple found, present the list and ask the user which one to sync.

## Pre-flight Checks

Run these checks in order. Stop at the first failure.

### 1. Find the Spec

If no spec file is found (from arguments or search):
- Report: **"No spec found to sync — consider running /plan-spec first"**
- Stop. No file is created.

### 2. Find the Implementation

Locate the implementation code using:
- The spec's traceability matrix and file scope sections
- `git diff main --name-only` for recent changes on the current branch
- The spec's `files_scope` or referenced directories

If no implementation code is found:
- Report: **"No implementation found to sync against"**
- Stop. No file is created.

### 3. Dirty Working Tree Check

Run `git diff --name-only` and check if the spec file path appears in the
output.

If the spec has uncommitted changes:
- Abort with: **"Spec file has uncommitted changes. Commit or stash changes before running spec-sync."**
- Stop. No file is created.

## Analysis Phase

### Step 1: Read the Spec

Read the existing spec completely. Extract:
- All functional requirements (FR-xxx or equivalent identifiers)
- All BDD scenarios with their categories and trace references
- Test datasets and expected boundaries
- Behavioral contract statements
- Integration boundaries
- Success criteria
- The holdout evaluation scenarios section (note its exact boundaries)

### Step 2: Read the Implementation

Read all implementation code:
- Files from the spec's file scope
- Files from `git diff main --name-only`
- Test files associated with the implementation

### Step 3: Identify Divergences

Compare spec vs implementation. For each divergence, classify into:

1. **Requirement changes**: An FR was implemented differently than specified
   (changed approach, discovered constraints, different error handling)
2. **New behaviors**: Code implements behaviors not in the spec (new error
   paths, additional validation, retry logic)
3. **Removed behaviors**: Spec requirements that were intentionally not
   implemented (with justification)
4. **Test changes**: Test structure, datasets, or coverage differs from
   the TDD plan

### Step 4: Apply Update Priorities

Order proposed changes by update priority:

| Priority | Sections | Rationale |
|----------|----------|-----------|
| Highest | TDD plan, test datasets, test implementation order | Most likely to diverge during implementation |
| Medium | Behavioral contract, edge cases, integration boundaries | May need updating to reflect actual behavior |
| Lowest | Requirements (FR-xxx), user stories | Only update if fundamentally changed |

## Presentation & Confirmation

### Dry-Run Mode (`--dry-run`)

If `--dry-run` is active:
1. Display the proposed changes grouped by priority level
2. For each change, show: affected section name, description of change,
   priority level
3. Do NOT write any file
4. Do NOT request confirmation
5. Exit after displaying

### Normal Mode

1. Present a diff summary to the user showing each proposed change:
   - Affected section name
   - Description of what changed and why
   - Priority level (Highest / Medium / Lowest)
2. Ask the user: "Apply these changes to create a new versioned spec? (yes/no)"
3. If user declines:
   - Report: **"Sync cancelled by user"**
   - Stop. No changes are made.
4. Approval is all-or-nothing — no selective sync of individual changes.

### No Divergences Found

If spec and implementation are perfectly aligned:
- Report: **"Spec and implementation are aligned — no sync needed"**
- Stop. No file is created.

## Output

### Version Number Determination

- If input has no version suffix (e.g., `my-feature-spec.md`):
  treat as v0, output is `my-feature-spec-v1.md`
- If input is versioned (e.g., `my-feature-spec-v3.md`):
  output is `my-feature-spec-v4.md`
- Parse version from the filename suffix `-v{N}` before the `.md` extension

### File Writing

1. Write the new versioned file in the same directory as the input spec
2. NEVER modify the original spec file
3. Include updated frontmatter:
   ```
   **Created**: [original creation date]
   **Revised**: [today's date] (v{N} — spec-sync from implementation)
   **Last synced**: [today's date]
   **Status**: [original status]
   ```

### Content Rules

When updating the spec content:

1. **New BDD scenarios** added by spec-sync MUST include `Traces to:`
   references linking them to the appropriate user story
2. **New BDD scenarios** MUST be added to the traceability matrix
3. **Requirement changes** should note the reason for divergence
   (discovered constraint, implementation necessity, etc.)
4. **Holdout evaluation scenarios** MUST be preserved byte-for-byte.
   Copy this section exactly as it appears in the original. Do not add,
   remove, or modify any holdout scenario.

## Error Handling Summary

| Condition | Action |
|-----------|--------|
| No spec found | Report "No spec found to sync — consider running /plan-spec first", stop |
| No implementation found | Report "No implementation found to sync against", stop |
| Spec has uncommitted changes | Abort: "Spec file has uncommitted changes. Commit or stash changes before running spec-sync." |
| User rejects confirmation | Report "Sync cancelled by user", no changes |
| Spec and code aligned | Report "Spec and implementation are aligned — no sync needed", no file created |
| `--dry-run` flag present | Display changes, no file written, no confirmation requested |
