# Review Remediation Plan

## Goal

Address the repo-wide correctness, wiring, dead-code, and operability issues
found in the audit on 2026-04-03. The plan prioritises user-visible failures,
security exposure, and workflow stages that are currently only partially wired.

## Scope

In scope:

- Codedoc API, orchestrator lifecycle, gate handling, and persistence
- Spec workflow holdout generation and downstream integration
- Shared workflow wiring in the server and dashboard
- Regression coverage for the audited failure paths

Out of scope:

- New workflow features unrelated to the audited failures
- Cosmetic UI cleanup not tied to incorrect state or broken actions
- Refactors that do not reduce the specific risks identified in review

## Priority Order

### Phase 0: Security and correctness blockers

1. Validate `codedoc` start inputs before any filesystem use.
   - Reject path traversal in `feature_name`.
   - Verify `code_path` exists and is a directory before returning `202`.
   - Persist an initial durable state before the async worker starts.
2. Make codedoc workflows resumable and conflict-safe after restart.
   - Detect persisted runs before creating a new in-memory orchestrator.
   - Allow gate actions to recover or resume state instead of failing with `404`.

### Phase 1: Complete the holdout pipeline

1. Repair the holdout generation contract.
   - Use a structured holdout prompt and validate holdout JSON output.
   - Treat missing or invalid holdout artifacts as failure, not soft success.
2. Wire holdout artifacts into the rest of the workflow.
   - Add `target` routing for `spec` vs `holdout` findings.
   - Feed latest round holdouts into reviewer prompts where intended.
   - Filter holdout-targeted findings out of reviser input.
   - Finalize from `holdouts-round-{N}.md` with legacy fallback.

### Phase 2: Fix cross-workflow wiring mismatches

1. Align dashboard and backend workflow contracts.
   - Add the holdout stage to the spec stepper.
   - Point reset actions to workflow-type-specific endpoints.
   - Either support `workspace_dir` for codedoc or remove the UI control.
2. Repair startup/config contract mismatches.
   - Enforce required skill-template configuration at startup or provide safe defaults.
   - Apply workspace overrides before any runner or source-doc setup for all workflow types.
3. Remove dead or misleading gate wiring.
   - Either transmit and persist codedoc gate comments or remove the unused field.
   - Replace placeholder codedoc final-gate payload values with real counts and drift data.

### Phase 3: Regression coverage and release gate

1. Add integration tests for every audited failure mode.
   - Codedoc traversal rejection
   - Durable codedoc start and cleanup
   - Resume/gate handling after restart
   - Holdout generation artifact validation
   - Holdout consumption in reviewer/reviser/finalization paths
   - Per-workflow reset and workspace override behavior
2. Require `go test ./...` green before closing remediation.

## Release Criteria

- `go test ./...` passes without codedoc API failures.
- Starting codedoc with invalid `feature_name` or `code_path` is rejected synchronously.
- Gate actions still work after server restart for code review and codedoc workflows.
- Holdout generation produces validated artifacts and influences downstream behavior.
- The dashboard accurately reflects the holdout stage and sends actions to the correct endpoints.
- Workspace override behavior is either consistently supported or consistently rejected across workflows.
