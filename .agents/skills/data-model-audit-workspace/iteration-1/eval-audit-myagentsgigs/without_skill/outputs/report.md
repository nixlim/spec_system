# Data Model Audit: MyAgentsGigs

**Date:** 2026-03-15
**Scope:** Database migrations (000001-000007), Go domain models, OpenAPI 3.1 spec, repository implementations

---

## 1. Schema Overview

The database consists of 11 tables across 7 migrations, evolving from a B0 proving phase to B1 production schema:

| Table | Purpose | PK |
|-------|---------|-----|
| `users` | Shared identity (consumer + developer) | UUID |
| `consumers` | Consumer profile (minimal after B1) | UUID |
| `developers` | Developer profile, KYC, Stripe link | UUID |
| `categories` | Listing categories | TEXT slug |
| `category_job_types` | Job types per category | composite (slug, job_type) |
| `listings` | Agent marketplace listings | UUID |
| `manifest_revisions` | Versioned agent manifests | UUID |
| `notification_configs` | Developer notification preferences | UUID |
| `refresh_tokens` | JWT refresh token tracking | UUID |
| `processed_stripe_events` | Stripe webhook deduplication | TEXT event_id |
| `idempotency_responses` | Request idempotency replay | composite (key, method, path) |
| `event_outbox` | Transactional outbox for domain events | UUID |
| `stub_records` | B0 test fixture (to be removed) | UUID |

---

## 2. Strengths

### 2.1 Well-Designed Relational Structure

- **Clean user/role separation**: The `users` table serves as a shared identity layer with `consumers` and `developers` as role-specific extension tables, linked via `UNIQUE` foreign keys. This is a textbook single-table-inheritance pattern with extension tables, and it works well here.
- **Categories use natural keys**: Changing categories from UUID PK (B0) to TEXT slug PK (B1) was the right call. Slugs are human-readable, URL-safe, and avoid unnecessary UUID joins for what is essentially reference data.
- **Composite PKs where appropriate**: `category_job_types(category_slug, job_type)` and `idempotency_responses(key, method, path)` use meaningful composite keys rather than synthetic UUIDs.

### 2.2 Infrastructure Tables Are Solid

- **Event outbox** follows the transactional outbox pattern correctly: events are written in the same transaction as domain mutations, with a partial index on `published_at IS NULL` for efficient polling.
- **Idempotency responses** have a well-structured B1 schema with `(key, method, path)` scoping, `user_id` for audit, and `request_hash` for body mismatch detection.
- **Refresh tokens** use partial indexes (`WHERE revoked_at IS NULL`) for active-token lookups, and the cleanup query handles both expired and stale-revoked tokens.
- **Stripe event deduplication** is simple and effective.

### 2.3 Good Use of PostgreSQL Features

- Full-text search via `TSVECTOR` column with automatic trigger-based updates on `listings`.
- GIN index on `search_vector` for efficient search.
- Composite indexes on `(category_slug, review_status)` for filtered listing queries.
- `CHECK` constraints enforce valid enum values at the database level (roles, statuses, notification modes).
- `ON DELETE CASCADE` on user -> consumer/developer FK for clean teardown.

### 2.4 Go Model Layer

- Domain models are cleanly separated from DTOs (e.g., `Developer` vs `DeveloperProfileResponse`).
- Typed string enums for `ListingStatus`, `SuspensionMode`, `NotificationMode` with validation methods.
- Sensitive data (`StripeAccountID`, `HMACSecret`) properly tagged with `json:"-"` to prevent serialization.
- Repository interfaces are well-defined with clear contract documentation.

### 2.5 OpenAPI Spec Quality

- All schemas use `additionalProperties: false` preventing unexpected fields.
- Structured error responses with machine-readable codes and field-level details.
- Consistent use of `format: uuid`, `format: email`, `format: uri` for string validation.
- `maxLength` constraints on listing title (200) and description (5000) match the DB `CHECK` constraints.

---

## 3. Issues Found

### 3.1 Duplicate/Conflicting Model Definitions (MEDIUM)

**Location:** `internal/notificationconfig/model.go` vs `internal/notifications/config.go`

Two separate packages define overlapping notification config models:
- `notificationconfig.Config` uses typed `NotificationMode` with `Valid()` method
- `notifications.NotificationConfig` uses plain `string` for mode

This creates confusion about which is canonical. The `notifications` package is the one with an actual repository implementation, so `notificationconfig` appears orphaned or represents a planned refactor that was never completed.

**Recommendation:** Consolidate into a single package. If `notificationconfig` was intended as the clean domain model, the `notifications` repository should use it.

### 3.2 OpenAPI DeveloperProfile Enum Mismatch with DB (MEDIUM)

**Location:** OpenAPI `DeveloperProfile.kyc_status` vs DB `developers_kyc_status_check`

The OpenAPI spec defines `kyc_status` enum as: `[pending, onboarding, verified]`
The database CHECK constraint allows: `[pending, onboarding, verified, restricted]`

The `restricted` status is missing from the OpenAPI spec. While this may be intentional (restricted developers should not see their own profile), it means the API cannot represent developers who have been restricted. If an admin or internal endpoint ever needs to return this status, the spec will be wrong.

**Recommendation:** Either add `restricted` to the OpenAPI enum or document why it is intentionally omitted.

### 3.3 No `updated_at` Trigger (LOW-MEDIUM)

**Location:** All tables with `updated_at` columns

The schema relies on application-level `updated_at = now()` in UPDATE queries. There is no database trigger to automatically update `updated_at` on row modification. This means:
- Any direct SQL update (migration, manual fix, new code path) that forgets `updated_at = now()` will leave stale timestamps.
- The `consumers` table had `updated_at` in B0 but the column status after B1 migration is unclear (B1 drops `display_name` but does not drop `updated_at`).

**Recommendation:** Add a `moddatetime` trigger or a custom `set_updated_at()` trigger function for all tables with `updated_at`.

### 3.4 `listings.description` Allows NULL but Go Model Uses `string` (LOW-MEDIUM)

**Location:** Migration 000007, `internal/listings/model.go`

The DB column is: `description TEXT CHECK (length(description) <= 5000)` -- nullable (no `NOT NULL`).
The Go model uses: `Description string` with `json:"description,omitempty"`.

When the DB returns NULL for description, the pgx scan into a `string` will fail unless the driver handles it (pgx will error on NULL -> string scan). The repository code scans directly into `&l.Description` (a `string`, not `*string`), which will produce a runtime error for listings with NULL descriptions.

**Recommendation:** Either make the column `NOT NULL DEFAULT ''` or change the Go model field to `*string`.

### 3.5 `listings.pricing_guidance` Same NULL/string Mismatch (LOW-MEDIUM)

**Location:** Same pattern as 3.4

`pricing_guidance TEXT` is nullable in DB but `PricingGuidance string` in Go. Same scan risk.

### 3.6 Manifest Version Race Condition (LOW-MEDIUM)

**Location:** `internal/listings/pg_repository.go`, `SaveManifest` method

The next version number is computed with `SELECT COALESCE(MAX(version), 0) + 1` followed by a separate `INSERT`. Under concurrent requests for the same listing, two transactions could read the same MAX and attempt to insert the same version, violating the `UNIQUE (listing_id, version)` constraint. The unique constraint will prevent data corruption but will produce an opaque error rather than a clear "concurrent manifest upload" message.

**Recommendation:** Use a `SELECT ... FOR UPDATE` lock, or handle the unique violation error explicitly with a retry or clear error message.

### 3.7 `RestoreListingsByDeveloper` Sets Empty String Instead of NULL (LOW)

**Location:** `internal/developers/pg_repository.go:127`

```go
`UPDATE listings SET review_status = 'approved', suspension_reason = '', ...`
```

Sets `suspension_reason` to empty string `''` rather than `NULL`. The OpenAPI spec and Go model treat `suspension_reason` as an optional/nullable field. This inconsistency means a restored listing will have `suspension_reason: ""` in JSON responses rather than the field being omitted.

**Recommendation:** Use `NULL` instead of `''`.

### 3.8 No Index on `manifest_revisions.listing_id` (LOW)

**Location:** Migration 000007

The `manifest_revisions` table has a `UNIQUE (listing_id, version)` index, which covers lookups by `(listing_id, version)`. However, queries like the prune query (`DELETE ... WHERE listing_id = $1 AND id NOT IN (SELECT ... WHERE listing_id = $1 ORDER BY version DESC LIMIT 5)`) and the version-max query (`SELECT MAX(version) WHERE listing_id = $1`) should be adequately served by the unique index since `listing_id` is the leading column. This is acceptable.

### 3.9 Missing `NOT NULL` on `manifest_revisions.review_result` (LOW)

**Location:** Migration 000007

`review_result` has a CHECK constraint for valid values (`pending`, `approved`, `rejected`) but allows NULL. The `SaveManifest` code always inserts `'pending'`, so NULL should not occur from application code, but the schema allows it. Consider adding `NOT NULL DEFAULT 'pending'`.

### 3.10 `processed_stripe_events` Has No Expiry/Cleanup Mechanism (LOW)

**Location:** Migration 000007

This table grows monotonically with no TTL, cleanup index, or referenced cleanup job (unlike `refresh_tokens` which has `DeleteExpiredRefreshTokens` and `idempotency_responses` which has a created_at index for expiry). Over time this table will accumulate rows indefinitely.

**Recommendation:** Add a `processed_at` index and a periodic cleanup job to remove events older than e.g. 90 days.

### 3.11 `consumers` Table Is Nearly Empty After B1 (INFORMATIONAL)

After B1 migration drops `display_name`, the `consumers` table only has `id`, `user_id`, `created_at`, and `updated_at`. The `id` and `user_id` are both UUIDs with a 1:1 relationship. The table is effectively a marker that a user has the consumer role, which is already captured by `users.role = 'consumer'`. The table exists presumably for future consumer-specific fields.

### 3.12 `stub_records` Table Is B0 Leftover (INFORMATIONAL)

The migration comment explicitly states this table should be deleted after B0. It remains in the schema.

---

## 4. Schema-to-Model-to-API Alignment Matrix

| Entity | DB Columns | Go Model Fields | OpenAPI Properties | Aligned? |
|--------|-----------|----------------|-------------------|----------|
| User | 5 (id, email, role, password_hash, created_at, updated_at) | 6 (UserRecord) | N/A (no direct API exposure) | Yes |
| Developer | 7 (id, user_id, country_code, kyc_status, stripe_account_id, created_at, updated_at) | 8 (Developer, joins email from users) | 7 (DeveloperProfile) | Partial -- `restricted` enum mismatch |
| Listing | 12 columns | 12 fields (Listing struct) | 12 properties | Yes |
| ManifestRevision | 7 columns | 7 fields | 8 properties (review_details typed as string in API vs JSONB in DB) | Partial |
| NotificationConfig | 6 columns | 6 fields (two competing definitions) | 5 properties (GET) / 4 properties (POST response) | Partial |
| Category | 3 columns | No Go model (queried inline) | No API schema (embedded in listing) | Acceptable |

---

## 5. Summary Assessment

| Aspect | Rating | Notes |
|--------|--------|-------|
| **Normalization** | Good | Proper 3NF, no unnecessary denormalization |
| **Referential integrity** | Good | Foreign keys with appropriate cascade/restrict behavior |
| **Index coverage** | Good | Partial indexes, GIN for FTS, composite indexes for common queries |
| **Constraint enforcement** | Good | CHECK constraints for enums, UNIQUE where needed, length checks |
| **Migration quality** | Good | Reversible, transactional, well-commented |
| **Model-DB alignment** | Fair | NULL/string mismatches (3.4, 3.5), enum gaps (3.2) |
| **Package organization** | Fair | Duplicate notification config models need consolidation |
| **Data lifecycle** | Fair | Missing cleanup for `processed_stripe_events`, no `updated_at` triggers |

**Overall:** The data model is well-structured for a B1-stage marketplace application. The core entity relationships (users, developers, listings, manifests, categories) are sound. The primary concerns are the NULL-to-string scan risks that could cause runtime panics (3.4, 3.5), the duplicate notification model packages (3.1), and the kyc_status enum gap between API and database (3.2). None of these are architectural problems -- they are implementation-level issues that can be addressed incrementally.
