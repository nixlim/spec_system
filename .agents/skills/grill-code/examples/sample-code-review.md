# Code Review: Password Reset

**Spec reviewed**: password-reset-spec.md
**Review date**: 2026-02-17
**Verdict**: REVISE
**Spec compliance**: 8/10 requirements implemented (80%)

## Executive Summary

The password reset implementation covers the core happy path and most
error scenarios. However, 2 functional requirements are not implemented
(rate limiting and concurrent token invalidation), one task is falsely
marked complete, and there are 2 critical code-level issues: an unchecked
error on the email send path and a timing side-channel on token validation.
Tests cover 7 of 10 BDD scenarios, but 2 tests have weak assertions that
don't actually verify the stated behaviour.

| Metric | Value |
|--------|-------|
| Functional requirements | 8 implemented / 10 total |
| BDD scenarios with tests | 7 covered / 10 total |
| Tasks genuinely complete | 4 verified / 5 claimed |
| Tests passing | 14 pass / 16 total |

| Severity | Count |
|----------|-------|
| CRITICAL | 2 |
| MAJOR | 3 |
| MINOR | 2 |
| OBSERVATION | 1 |
| **Total** | **8** |

---

## Spec Compliance Matrix

| Requirement | Status | Evidence |
|-------------|--------|----------|
| FR-001: System MUST accept email and initiate reset | IMPLEMENTED | `auth/reset_handler.go:45-62` |
| FR-002: System MUST validate email format | IMPLEMENTED | `auth/reset_handler.go:48`, uses `mail.ParseAddress` |
| FR-003: System MUST send reset email with token link | IMPLEMENTED | `auth/reset_service.go:78-95` |
| FR-004: System MUST generate cryptographic reset token | IMPLEMENTED | `auth/token.go:12-28`, uses `crypto/rand`, 32 bytes |
| FR-005: Token MUST expire after 30 minutes | IMPLEMENTED | `auth/token.go:30`, `TokenTTL = 30 * time.Minute` |
| FR-006: System MUST validate token on reset submission | IMPLEMENTED | `auth/reset_handler.go:70-88` |
| FR-007: System MUST hash new password with bcrypt | IMPLEMENTED | `auth/password.go:15`, `bcrypt.GenerateFromPassword` |
| FR-008: System MUST invalidate token after use | IMPLEMENTED | `auth/reset_service.go:102`, `DeleteToken()` called |
| FR-009: System MUST rate-limit token verification | MISSING | No rate limiting code found anywhere |
| FR-010: System MUST invalidate previous tokens on new request | MISSING | `CreateToken()` at `auth/reset_service.go:55` does not delete existing tokens |

**Compliance score**: 8/10 (80%)

---

## BDD Scenario Coverage

| BDD Scenario | Category | Test File | Test Correct | Passes |
|-------------|----------|-----------|-------------|--------|
| Scenario: Valid email initiates reset | Happy Path | `auth/reset_handler_test.go:TestResetRequest` | YES | PASS |
| Scenario: Invalid email rejected | Error Path | `auth/reset_handler_test.go:TestResetInvalidEmail` | YES | PASS |
| Scenario: Non-existent email returns success | Security | `auth/reset_handler_test.go:TestResetUnknownEmail` | YES | PASS |
| Scenario: Valid token resets password | Happy Path | `auth/reset_handler_test.go:TestResetWithValidToken` | YES | PASS |
| Scenario: Expired token rejected | Error Path | `auth/reset_handler_test.go:TestResetExpiredToken` | PARTIAL | PASS |
| Scenario: Used token rejected | Error Path | `auth/reset_handler_test.go:TestResetUsedToken` | YES | PASS |
| Scenario: Weak password rejected | Error Path | `auth/reset_handler_test.go:TestResetWeakPassword` | PARTIAL | FAIL |
| Scenario: Brute-force rate limited | Error Path | — | NO TEST | — |
| Scenario: Concurrent resets invalidate old tokens | Edge Case | — | NO TEST | — |
| Scenario: Token at exact expiry boundary | Edge Case | — | NO TEST | — |

**Coverage**: 7/10 scenarios have tests (5 correct, 2 partial)

---

## Task Audit

| Task ID | Title | Claimed Status | Verified Status | Details |
|---------|-------|---------------|----------------|---------|
| TDP-42 | Implement reset request endpoint | closed | GENUINELY COMPLETE | All 3 criteria verified |
| TDP-43 | Implement token generation | closed | GENUINELY COMPLETE | All criteria verified |
| TDP-44 | Implement token validation and password reset | closed | GENUINELY COMPLETE | All criteria verified |
| TDP-45 | Implement security hardening | closed | INCOMPLETE | 2 of 4 criteria not met |
| TDP-46 | Add comprehensive test coverage | closed | GENUINELY COMPLETE | All criteria verified |

### Incomplete Task Details

#### Task TDP-45: Implement security hardening

**Acceptance criteria from task:**
1. Rate limiting on token verification endpoint — NOT MET: no rate limiting code exists
2. Previous tokens invalidated on new request — NOT MET: `CreateToken()` does not call `DeleteTokensByUser()`
3. Token verification uses constant-time comparison — VERIFIED at `auth/token.go:35` uses `subtle.ConstantTimeCompare`
4. Error responses do not leak token existence — VERIFIED at `auth/reset_handler.go:75` returns generic "invalid or expired" message

---

## Code Findings

### CRITICAL Findings

#### [CRIT-001] Email send error silently swallowed

- **Lens**: Error Handling
- **File**: `auth/reset_service.go:88`
- **Code**:
  ```go
  go func() {
      emailService.Send(ctx, resetEmail)
  }()
  ```
- **Issue**: The email send is fired in a goroutine with no error handling.
  If `Send()` fails (SMTP timeout, invalid relay, rate limit), the user
  sees "reset email sent" but receives nothing. No log, no metric, no
  retry. The user has no way to know the email was never sent.
- **Impact**: Silent email delivery failures. Users request resets, see
  success messages, wait indefinitely. Support tickets ensue. No observability
  into the failure.
- **Fix**: Handle the error, log it, and consider a synchronous send or
  at minimum an async error channel:
  ```go
  go func() {
      if err := emailService.Send(ctx, resetEmail); err != nil {
          tdpLog.Error().
              Any("error", errorformatting.FormatError(err)).
              Str("email", maskedEmail).
              Str("traceId", traceId).
              Msg("Failed to send password reset email")
          metrics.IncrCounter("reset_email_send_failure", 1)
      }
  }()
  ```

---

#### [CRIT-002] Timing side-channel on token lookup

- **Lens**: Security
- **File**: `auth/reset_handler.go:72-78`
- **Code**:
  ```go
  token, err := tokenStore.GetByValue(tokenValue)
  if err != nil {
      return c.JSON(400, ErrorResponse{Message: "Invalid or expired token"})
  }
  if token.ExpiresAt.Before(time.Now()) {
      return c.JSON(400, ErrorResponse{Message: "Invalid or expired token"})
  }
  ```
- **Issue**: While the error message is identical for both cases (good),
  the response time differs: a non-existent token returns immediately from
  the database lookup, while a valid-but-expired token takes the additional
  time of the `Before()` comparison. An attacker can distinguish between
  "token doesn't exist" and "token exists but is expired" by measuring
  response latency. Combined with the missing rate limiting (FR-009), this
  enables targeted brute-force.
- **Impact**: Reduces the search space for token brute-force. Attacker can
  first find valid token hashes via timing, then wait for rate limiting to
  be absent (which it is) to brute-force the full token value.
- **Fix**: Always perform both checks regardless of the first result:
  ```go
  token, lookupErr := tokenStore.GetByValue(tokenValue)
  expired := token != nil && token.ExpiresAt.Before(time.Now())
  if lookupErr != nil || expired {
      return c.JSON(400, ErrorResponse{Message: "Invalid or expired token"})
  }
  ```

---

### MAJOR Findings

#### [MAJ-001] FR-009 not implemented: no rate limiting

- **Lens**: Spec Compliance
- **File**: N/A — no implementation found
- **Issue**: The spec requires rate limiting on the token verification
  endpoint (FR-009: "maximum of 5 attempts per token per 15-minute window").
  No rate limiting middleware, counter, or check exists anywhere in the
  codebase for this endpoint.
- **Impact**: Token brute-force is possible. Combined with CRIT-002
  (timing side-channel), this is a compounding security risk.
- **Fix**: Implement rate limiting. The simplest approach: a counter keyed
  by token hash in the token store, incremented on each verification
  attempt, with a check before token lookup.

---

#### [MAJ-002] FR-010 not implemented: old tokens not invalidated

- **Lens**: Spec Compliance
- **File**: `auth/reset_service.go:50-60`
- **Issue**: `CreateToken()` generates a new token but does not delete
  existing tokens for the same user. The spec requires: "When a user
  requests a new reset while a previous token is still active, the previous
  token MUST be invalidated immediately."
- **Impact**: Multiple active tokens per user increases attack surface.
- **Fix**: Add `tokenStore.DeleteByUserID(userID)` before creating the
  new token in `CreateToken()`.

---

#### [MAJ-003] Weak password test asserts wrong condition

- **Lens**: Testing Quality
- **File**: `auth/reset_handler_test.go:142`
- **Code**:
  ```go
  func TestResetWeakPassword(t *testing.T) {
      // ... setup ...
      assert.NotNil(t, resp)
      assert.Equal(t, 400, resp.StatusCode)
  }
  ```
- **Issue**: The test checks that weak passwords return 400, but does NOT
  verify the error message or which validation rule triggered. The test
  passes with status 400, but the BDD scenario specifies: "Then the system
  displays 'Password must be at least 12 characters with uppercase,
  lowercase, and a number.'" The test doesn't check this. Additionally,
  the test is currently FAILING because the handler returns 422, not 400.
- **Impact**: False confidence. The test name suggests password validation
  works, but the assertion doesn't verify the right thing.
- **Fix**: Assert the specific error message and fix the expected status code:
  ```go
  assert.Equal(t, 422, resp.StatusCode)
  assert.Contains(t, body, "Password must be at least 12 characters")
  ```

---

### MINOR Findings

#### [MIN-001] Missing structured logging context on successful reset

- **Lens**: Observability
- **File**: `auth/reset_handler.go:92`
- **Issue**: Successful password resets are not logged. Failed attempts
  are logged (good), but there's no audit trail for successful resets.
  The spec's OBS-001 observation suggested audit logging.
- **Fix**: Add a structured log entry after successful password change:
  ```go
  tdpLog.Info().Str("userId", userID).Str("traceId", traceId).
      Msg("Password reset completed successfully")
  ```

---

#### [MIN-002] Token generation uses 32 bytes but spec says "at least 128 bits"

- **Lens**: Correctness
- **File**: `auth/token.go:18`
- **Issue**: The code generates 32 bytes (256 bits) of entropy, which
  exceeds the spec's minimum of 128 bits. This is fine — but the constant
  is named `tokenLength = 32` without a comment explaining the relationship
  to the spec's requirement. Future maintainers might reduce it.
- **Fix**: Add a comment: `// 32 bytes = 256 bits of entropy. Spec requires minimum 128 bits (FR-004).`

---

### Observations

#### [OBS-001] Consider table-driven tests for password validation

- **Lens**: Testing Quality
- **File**: `auth/reset_handler_test.go:130-165`
- **Suggestion**: The test file has 4 separate test functions for different
  invalid passwords (too short, no uppercase, no number, common password).
  These could be a single table-driven test matching the spec's password
  test dataset, which would make it easier to add new validation rules
  and would directly trace to the spec's test dataset rows.

---

## Test Results

```
=== RUN   TestResetRequest
--- PASS: TestResetRequest (0.02s)
=== RUN   TestResetInvalidEmail
--- PASS: TestResetInvalidEmail (0.01s)
=== RUN   TestResetUnknownEmail
--- PASS: TestResetUnknownEmail (0.01s)
=== RUN   TestResetWithValidToken
--- PASS: TestResetWithValidToken (0.03s)
=== RUN   TestResetExpiredToken
--- PASS: TestResetExpiredToken (0.02s)
=== RUN   TestResetUsedToken
--- PASS: TestResetUsedToken (0.02s)
=== RUN   TestResetWeakPassword
--- FAIL: TestResetWeakPassword (0.01s)
    reset_handler_test.go:145: expected 400, got 422
=== RUN   TestTokenGeneration
--- PASS: TestTokenGeneration (0.01s)
=== RUN   TestTokenExpiry
--- PASS: TestTokenExpiry (0.01s)
=== RUN   TestPasswordHashing
--- PASS: TestPasswordHashing (0.02s)
```

| Status | Count |
|--------|-------|
| PASS | 9 |
| FAIL | 1 |
| SKIP | 0 |

### Failing Tests

| Test | File | Failure Reason |
|------|------|----------------|
| TestResetWeakPassword | `auth/reset_handler_test.go:145` | Expected status 400, got 422 |

---

## Verdict Rationale

The implementation covers 80% of the spec's functional requirements and
the core user-facing flows work correctly. However, two security-critical
requirements (rate limiting and token invalidation) are entirely missing,
and the security hardening task (TDP-45) was falsely marked complete.
The two CRITICAL code-level findings (silent email failures and timing
side-channel) would cause operational and security issues in production.

The test suite covers most scenarios but has a failing test and two tests
with assertions that don't verify what they claim to verify.

### Recommended Next Actions

- [ ] Fix CRIT-001: Add error handling to email send goroutine (`auth/reset_service.go:88`)
- [ ] Fix CRIT-002: Eliminate timing side-channel in token validation (`auth/reset_handler.go:72-78`)
- [ ] Implement MAJ-001: Add rate limiting for token verification (FR-009)
- [ ] Implement MAJ-002: Invalidate old tokens on new request (FR-010)
- [ ] Fix MAJ-003: Correct TestResetWeakPassword assertions and expected status code
- [ ] Reopen task TDP-45 and complete remaining acceptance criteria
- [ ] Add missing tests for BDD scenarios: brute-force, concurrent resets, expiry boundary

### Suggested Follow-up Actions

- [ ] Fix: Silent email send failure (CRIT-001) — Add error handling to async email send in `auth/reset_service.go:88`. Log failures with structured logging, increment failure metric.
- [ ] Fix: Timing side-channel on token lookup (CRIT-002) — Eliminate timing difference between non-existent and expired tokens in `auth/reset_handler.go:72-78`. Always perform both checks.
- [ ] Implement: Token verification rate limiting (FR-009) — Add rate limiting: max 5 attempts per token per 15-min window. Invalidate token after limit exceeded. Add BDD Error Path test.
- [ ] Implement: Invalidate old tokens on new request (FR-010) — Call `tokenStore.DeleteByUserID` before creating new token in `auth/reset_service.go:55`. Add BDD Edge Case test.
