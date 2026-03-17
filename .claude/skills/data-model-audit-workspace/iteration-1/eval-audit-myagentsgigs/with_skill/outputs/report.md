# Data Model Audit Report

## Project: MyAgentsGigs
## Overall Maturity: Level 3 -- Stability (Partial)
## Date: 2026-03-15

---

### Summary

MyAgentsGigs has a well-structured, schema-first data model expressed through SQL migrations and a comprehensive OpenAPI 3.1 specification. The SQL schema uses proper constraints, foreign keys, typed enums via CHECK, and indexing. However, several correspondence gaps between the schema layers (SQL, OpenAPI, Go models) prevent the project from fully reaching Level 4. The project is young (17 commits, all in March 2026), so stability assessment is based on limited history.

### Schema Files Found

| File | Type | First Committed |
|------|------|-----------------|
| `db/migrations/000001_create_users_and_consumers.up.sql` | SQL migration | 2026-03-13 |
| `db/migrations/000002_create_developers.up.sql` | SQL migration | 2026-03-13 |
| `db/migrations/000003_create_categories.up.sql` | SQL migration | 2026-03-13 |
| `db/migrations/000004_create_event_outbox.up.sql` | SQL migration | 2026-03-13 |
| `db/migrations/000005_create_idempotency_responses.up.sql` | SQL migration | 2026-03-13 |
| `db/migrations/000006_create_stub_records.up.sql` | SQL migration | 2026-03-13 |
| `db/migrations/000007_b1_schema.up.sql` | SQL migration | Untracked (not yet committed) |
| `openapi.yaml` | OpenAPI 3.1 spec | 2026-03-13 |

---

### Level 1: Existence -- Pass

**Evidence**: Seven SQL migration files and one OpenAPI 3.1 specification exist. The B0 migrations (000001-000006) and `openapi.yaml` were committed in the same commit as the application code (`d5b74a0`, 2026-03-13), alongside the Go handlers, services, and repositories.

**Details**: The project follows a schema-first approach. The data model was designed as part of a deliberate B0/B1 build plan (evidenced by spec documents in `docs/specs/`). SQL migrations define the database schema, OpenAPI defines the API contract, and Go model files (`models.go`, `model.go`) serve as code-level representations. The schema was not an afterthought -- it shipped alongside the first application code.

### Level 2: Completeness -- Pass (with minor gaps)

**Evidence**: The SQL schema defines 11 tables after B1 migration: `users`, `consumers`, `developers`, `categories`, `category_job_types`, `listings`, `manifest_revisions`, `notification_configs`, `refresh_tokens`, `processed_stripe_events`, `idempotency_responses`, and the temporary `stub_records`.

**Details**:

Strengths:
- Every entity referenced in Go code has a corresponding SQL table definition.
- Relationships are explicit via foreign keys: `consumers.user_id -> users.id`, `developers.user_id -> users.id`, `listings.developer_id -> developers.id`, `listings.category_slug -> categories.slug`, `manifest_revisions.listing_id -> listings.id`, `notification_configs.developer_id -> developers.id`, `refresh_tokens.user_id -> users.id`.
- Constraints are thorough: CHECK constraints for enum-like fields (`users.role`, `developers.kyc_status`, `listings.review_status`, `notification_configs.mode`, `manifest_revisions.review_result`), NOT NULL where appropriate, UNIQUE constraints on `users.email`, `developers.stripe_account_id`, and composite keys.
- Field types are specific: UUIDs for identifiers, TIMESTAMPTZ for times, TSVECTOR for full-text search, JSONB for flexible payloads.
- Indexes are well-chosen: GIN index on search_vector, partial index on refresh_tokens for active tokens, composite index on listings for category+status filtering.

Minor gaps:
- The `admin` role exists in Go code (`internal/platform/authn/role.go`: `RoleAdmin = "admin"`) but is not in the SQL CHECK constraint on `users.role`, which only allows `consumer` and `developer`.
- Duplicate notification config model definitions exist in both `internal/notifications/config.go` and `internal/notificationconfig/model.go` with slightly different type structures (one uses `string` for mode, the other uses a typed `NotificationMode`). This creates risk of drift.
- The `manifest_data` column in `manifest_revisions` is `JSONB` -- a flexible blob. The structure of manifest data is defined in Go (`listings.Manifest` struct) and OpenAPI (`ManifestUploadRequest`) but not in the SQL schema. No JSON schema constraint validates its structure at the database level.
- The `event_outbox.payload` column is also unstructured JSONB. Event payload schemas are defined only in Go code (`internal/platform/events/b1events.go`).

### Level 3: Stability -- Partial

**Evidence**:
- Total commits: 17
- Commits touching schema files: 1 (the B0 foundation commit)
- The B1 migration (000007) is untracked -- staged but not yet committed
- Code commits: 3 (B0 foundation + middleware improvements + gofmt fix)

**Details**: The project is only 2 days old with 17 total commits. There has been exactly 1 commit touching schema files (the B0 foundation), and the B1 schema migration has been developed but not yet committed. This makes stability difficult to assess definitively. The pattern is consistent with schema-first development: the B0 schema was committed with the first code, and the B1 migration extends it for the next build phase. However, the limited history prevents a confident Pass.

The schema evolution from B0 to B1 is well-structured: migration 000007 uses proper ALTER TABLE statements, recreates tables where needed (categories, idempotency_responses), and maintains referential integrity throughout. This suggests deliberate schema design rather than reactive patching.

---

### Level 4: Correspondence -- Not fully evaluated (Level 3 is Partial)

**Preliminary assessment**: The schema provides strong predictive power for the application's behavior. Reading the SQL migrations and OpenAPI spec, one can accurately predict:

- Two-role user system (consumer/developer) with shared identity
- Developer KYC lifecycle (pending -> onboarding -> verified/restricted) via Stripe Connect
- Marketplace listings with category-based classification and review workflow (pending -> in_review -> approved/rejected/suspended)
- Versioned agent manifests with review gates
- Webhook-or-poll notification delivery for developers
- JWT authentication with token refresh and rotation
- Idempotency for mutating API calls
- Transactional outbox pattern for domain events

However, notable gaps reduce correspondence:
1. The 4-gate review pipeline (schema validation, endpoint reachability, category validation, content moderation) is entirely a code-level concern with no schema representation.
2. The `kyc_status` enum mismatch: the OpenAPI spec for `DeveloperProfile` lists `[pending, onboarding, verified]` (3 values) while the SQL CHECK allows `[pending, onboarding, verified, restricted]` (4 values). A reader of only the API spec would miss the `restricted` state.
3. The `admin` role exists in code but not in the database schema, making admin capabilities invisible from the schema alone.
4. Listing limits (mentioned in OpenAPI error codes as `listing_limit_reached`) have no schema representation -- the limit is presumably a configuration or code constant.

### Level 5: Sufficiency -- Not evaluated

---

### Recommendations

1. **Reconcile the `kyc_status` enum across all three layers.** The SQL CHECK constraint allows `restricted`, the Go `DevStatus` type includes `restricted`, but the OpenAPI `DeveloperProfile.kyc_status` enum omits it. Either add `restricted` to the OpenAPI enum or document why it is intentionally hidden from the API response. This is the highest-priority fix because it means API consumers cannot observe a valid database state.

2. **Resolve the duplicate notification config models.** Two packages (`internal/notifications` and `internal/notificationconfig`) define overlapping types for the same domain concept. Consolidate to one canonical package to prevent divergence. The `notificationconfig` package with its typed `NotificationMode` enum is the stronger definition.

3. **Add the `admin` role to the SQL CHECK constraint on `users.role`**, or remove the `RoleAdmin` constant from Go code if admin users are not yet part of the data model. Currently the database would reject an admin user that the application code considers valid.

### Churn Analysis

| Period | Schema Commits | Code Commits | Ratio |
|--------|---------------|--------------|-------|
| 2026-03 | 1 | 3 | 0.33 |

The project is too young for meaningful churn analysis. All development has occurred within a 2-day window in March 2026 across 17 total commits. The B1 schema migration is developed but uncommitted, which is consistent with a deliberate build-plan approach where schema is designed upfront before the code that uses it.

The pattern so far is healthy: schema work is front-loaded (B0 schema committed with initial code, B1 schema designed before B1 code is complete), and schema files change much less frequently than application code. If this pattern continues as the project matures, it will indicate strong schema-first discipline.
