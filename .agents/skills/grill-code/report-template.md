# Adversarial Code Review Report Template

Use this template when assembling the findings report in Phase 4.

---

```markdown
# Code Review: [Feature Name]

**Spec reviewed**: [path/to/spec-file.md]
**Review date**: [YYYY-MM-DD]
**Verdict**: [BLOCK | REVISE | PASS]
**Spec compliance**: [N/M requirements implemented] ([percentage]%)

## Executive Summary

[2-3 sentences. Spec compliance score, task audit results, total findings
by severity. Be direct.]

| Metric | Value |
|--------|-------|
| Files reviewed | [N files] |
<!-- Include the following rows only when a spec was provided -->
| Functional requirements | [N implemented / M total] |
| BDD scenarios with tests | [N covered / M total] |
<!-- Include the following row only when tasks exist -->
| Tasks genuinely complete | [N verified / M claimed] |
| Wiring gaps | [N stubs, N unwired, N partial] |
| Tests passing | [N pass / M total] |

| Severity | Count |
|----------|-------|
| CRITICAL | N |
| MAJOR | N |
| MINOR | N |
| OBSERVATION | N |
| **Total** | **N** |

---

## Spec Compliance Matrix

<!-- Include this section only when a spec was provided.
     If plan-spec format: use FR-xxx identifiers as shown below.
     If other spec format: use the spec's own requirement identifiers
     or short descriptions instead of FR-xxx. -->

| Requirement | Status | Evidence |
|-------------|--------|----------|
| FR-001: [requirement text] | IMPLEMENTED | `path/to/file.go:42-58` |
| FR-002: [requirement text] | PARTIAL | `path/to/file.go:60` — missing [detail] |
| FR-003: [requirement text] | MISSING | No implementation found |
| FR-004: [requirement text] | INCORRECT | `path/to/file.go:80` — spec says X, code does Y |

**Compliance score**: [N/M] ([percentage]%)

---

## BDD Scenario Coverage

<!-- Include this section only when a plan-spec format spec was provided.
     For non-plan-spec specs, omit this section entirely and incorporate
     any scenario/acceptance-criteria coverage into the Spec Compliance Matrix. -->

| BDD Scenario | Category | Test File | Test Correct | Passes |
|-------------|----------|-----------|-------------|--------|
| Scenario: [name] | Happy Path | `path/to/file_test.go:TestName` | YES | PASS |
| Scenario: [name] | Error Path | `path/to/file_test.go:TestName` | PARTIAL | PASS |
| Scenario: [name] | Edge Case | — | NO TEST | — |

**Coverage**: [N/M] scenarios have correct, passing tests

---

## Task Audit

<!-- Include this section only when tasks exist (docs/plan/*/tasks.md or .tasks/*.task.json).
     Omit entirely in code-only and spec-only modes. -->

| Task ID | Title | Claimed Status | Verified Status | Details |
|---------|-------|---------------|----------------|---------|
| [id] | [title] | closed | GENUINELY COMPLETE | All criteria verified |
| [id] | [title] | closed | INCOMPLETE | [which criterion failed] |
| [id] | [title] | in_progress | BLOCKED | [what's blocking] |

### Incomplete Task Details

#### Task [id]: [title]

**Acceptance criteria from task:**
1. [criterion] — VERIFIED at `file:line`
2. [criterion] — NOT MET: [what's wrong]
3. [criterion] — NOT MET: [what's missing]

---

## Wiring & Integration Audit

<!-- This section surfaces implemented-but-unwired code, stubs, and partial
     wiring. These are high-priority findings because they create false
     confidence that features work when they're actually inert.
     Always include this section. If no wiring issues are found, state
     "No wiring gaps detected — all implemented components are connected
     end-to-end." -->

### Stubs Found

| File | Function/Method | Stub Pattern | Severity |
|------|----------------|--------------|----------|
| `path/to/file.go:42` | `DoSomething()` | Returns nil, no real logic | MAJOR |

### Implemented but Unwired

| Package / Component | Has Tests | Called from Binary | Status |
|---------------------|-----------|-------------------|--------|
| `internal/foo` | YES (12 tests) | NO — no call site in `cmd/` | UNWIRED |
| `internal/bar.Start()` | YES | Created but `Start()` never called | PARTIAL |

### Partial Wiring

| Component | What's Connected | What's Missing |
|-----------|-----------------|----------------|
| `Sentinel` | Created in `main.go:45` | `Start()` never called, `Subscribe()` never called |

---

## Code Findings

### CRITICAL Findings

#### [CRIT-001] [Short title]

- **Lens**: [Correctness | Error Handling | Security | Testing | Observability | Overcomplexity]
- **File**: `path/to/file.go:142`
- **Code**:
  ```go
  // The problematic code
  resp, _ := client.Do(req)
  ```
- **Issue**: [What is wrong. Be specific.]
- **Impact**: [What happens in production if this ships.]
- **Fix**: [Exact change needed. Show corrected code if possible.]
  ```go
  // The corrected code
  resp, err := client.Do(req)
  if err != nil {
      return fmt.Errorf("request to %s failed: %w", url, err)
  }
  ```

---

### MAJOR Findings

#### [MAJ-001] [Short title]

- **Lens**: [Lens name]
- **File**: `path/to/file.go:line`
- **Code**: [relevant snippet]
- **Issue**: [What is wrong]
- **Impact**: [Consequence]
- **Fix**: [Specific change needed]

---

### MINOR Findings

#### [MIN-001] [Short title]

- **Lens**: [Lens name]
- **File**: `path/to/file.go:line`
- **Issue**: [What is wrong]
- **Fix**: [Specific change]

---

### Observations

#### [OBS-001] [Short title]

- **Lens**: [Lens name]
- **File**: `path/to/file.go:line`
- **Suggestion**: [Improvement idea]

---

## Test Results

```
[Paste actual test output here — pass/fail/skip for each test]
```

| Status | Count |
|--------|-------|
| PASS | N |
| FAIL | N |
| SKIP | N |

### Failing Tests

| Test | File | Failure Reason |
|------|------|----------------|
| TestName | `path/to/file_test.go:42` | [Error message or assertion failure] |

### Skipped Tests

| Test | File | Skip Reason |
|------|------|-------------|
| TestName | `path/to/file_test.go:58` | [t.Skip message] |

---

## Verdict Rationale

[1-2 paragraphs. Reference the most impactful findings. State what must
change before the implementation can be considered complete. If PASS,
briefly state why the implementation is satisfactory.]

### Recommended Next Actions

- [ ] [Action item — reference finding ID and specific file:line]
- [ ] [Action item — reference finding ID and specific file:line]
- [ ] [Action item — reference finding ID and specific file:line]

### Suggested Follow-up Actions

- [ ] Fix [CRIT-001 title] — [file:line] — [what to change]
- [ ] Fix [MAJ-001 title] — [file:line] — [what to change]
- [ ] [Additional action items from findings]

After fixing, re-run: `/grill-code [path]`
```
