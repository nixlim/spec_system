// Package specworkflow defines the core types for the adversarial spec review
// workflow. This file implements agent failure detection, retry logic with
// exponential backoff, and escalation/recovery strategies.
package specworkflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Error type constants
// ---------------------------------------------------------------------------

// ErrProcessKilled is a sentinel returned by runners when the process was
// intentionally killed via the kill API. Callers use errors.Is to detect it
// and skip retry logic.
var ErrProcessKilled = errors.New("process killed by user")

const (
	// ErrKilled indicates the agent process was intentionally killed by the user.
	ErrKilled = "killed"
	// ErrTimeout indicates the agent exceeded its time limit.
	ErrTimeout = "timeout"
	// ErrCrash indicates the agent process exited with a non-zero code.
	ErrCrash = "crash"
	// ErrMissingOutput indicates the expected output file was not produced.
	ErrMissingOutput = "missing_output"
	// ErrInvalidJSON indicates the output file contains invalid JSON.
	ErrInvalidJSON = "invalid_json"
	// ErrSchemaViolation indicates the output JSON does not conform to the schema.
	ErrSchemaViolation = "schema_violation"
	// ErrContextOverflow indicates the agent exceeded its context window limit.
	ErrContextOverflow = "context_overflow"
	// ErrRateLimited indicates the agent was rate-limited by the LLM provider.
	ErrRateLimited = "rate_limited"
)

// ---------------------------------------------------------------------------
// AgentError
// ---------------------------------------------------------------------------

// AgentError captures a structured error from an agent invocation, including
// the failure type, which agent failed, retry state, and human-readable detail.
type AgentError struct {
	// Type is one of the Err* constants (e.g. ErrCrash, ErrTimeout).
	Type string
	// Agent is the name of the agent that failed.
	Agent string
	// Detail is a human-readable description of the failure.
	Detail string
	// RetryCount is the number of retries already attempted.
	RetryCount int
	// MaxRetries is the maximum number of retries allowed.
	MaxRetries int
}

// Error implements the error interface for AgentError.
func (e *AgentError) Error() string {
	return fmt.Sprintf("agent %q failed: %s — %s (retry %d/%d)",
		e.Agent, e.Type, e.Detail, e.RetryCount, e.MaxRetries)
}

// ---------------------------------------------------------------------------
// Failure detection
// ---------------------------------------------------------------------------

// contextOverflowPatterns are stderr substrings that indicate context overflow.
var contextOverflowPatterns = []string{
	"context length",
	"token limit",
	"maximum context",
}

// rateLimitPatterns are stderr substrings that indicate rate limiting.
var rateLimitPatterns = []string{
	"rate limit",
	"rate_limit",
	"usage limit",
	"usage_limit",
	"429",
}

// containsAny returns true if s contains any of the given substrings
// (case-insensitive).
func containsAny(s string, patterns []string) bool {
	lower := strings.ToLower(s)
	for _, p := range patterns {
		if strings.Contains(lower, strings.ToLower(p)) {
			return true
		}
	}
	return false
}

// DetectFailureType inspects an agent's exit code, stderr output, and output
// file path to determine the type of failure. Returns an empty string if no
// error is detected.
func DetectFailureType(exitCode int, stderr string, outputPath string) string {
	// Non-zero exit: check for specific error patterns first.
	if exitCode != 0 {
		if containsAny(stderr, contextOverflowPatterns) {
			return ErrContextOverflow
		}
		if containsAny(stderr, rateLimitPatterns) {
			return ErrRateLimited
		}
		return ErrCrash
	}

	// Exit code 0 — check output file.
	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		return ErrMissingOutput
	}

	// File exists — check if it's valid JSON.
	data, err := os.ReadFile(outputPath)
	if err != nil {
		return ErrMissingOutput
	}
	if !json.Valid(data) {
		return ErrInvalidJSON
	}

	return ""
}

// ---------------------------------------------------------------------------
// Retry logic
// ---------------------------------------------------------------------------

// retryBaseDelay is the base delay for exponential backoff.
const retryBaseDelay = 5 * time.Second

// retryMultiplier is the multiplier for exponential backoff.
const retryMultiplier = 3

// RetryDelay returns the delay before the given retry attempt using
// exponential backoff: 5s, 15s, 45s (5 * 3^attempt seconds).
// Attempt is zero-indexed (0 = first retry).
func RetryDelay(attempt int) time.Duration {
	multiplier := 1
	for i := 0; i < attempt; i++ {
		multiplier *= retryMultiplier
	}
	return retryBaseDelay * time.Duration(multiplier)
}

// ShouldRetry returns true if the agent error has retries remaining.
// Returns false when RetryCount >= MaxRetries.
func ShouldRetry(err *AgentError) bool {
	return err.RetryCount < err.MaxRetries
}

// ---------------------------------------------------------------------------
// Recovery actions
// ---------------------------------------------------------------------------

// Recovery action constants.
const (
	// ActionRetry indicates the agent should be retried with the same prompt.
	ActionRetry = "retry"
	// ActionEscalate indicates the failure should be escalated to a human.
	ActionEscalate = "escalate"
	// ActionDefaultRevise indicates a conservative default REVISE verdict
	// should be used when the judge fails.
	ActionDefaultRevise = "default_revise"
	// ActionBestEffortParse indicates partial output should be extracted.
	ActionBestEffortParse = "best_effort_parse"
)

// RecoveryAction describes the action to take when an agent fails, along with
// the reason for choosing that action.
type RecoveryAction struct {
	// Action is one of "retry", "escalate", "default_revise", or "best_effort_parse".
	Action string
	// Reason is a human-readable explanation for the chosen action.
	Reason string
}

// DetermineRecovery decides the recovery strategy for a failed agent based on
// the error, current workflow state, current round, and maximum rounds allowed.
func DetermineRecovery(err *AgentError, currentState WorkflowState, round, maxRounds int) RecoveryAction {
	// Intentional kills are never retried — the user made an explicit decision.
	if err.Type == ErrKilled {
		return RecoveryAction{
			Action: ActionEscalate,
			Reason: "agent was killed by user request; not retrying",
		}
	}

	// If retries remain, always retry first.
	if ShouldRetry(err) {
		return RecoveryAction{
			Action: ActionRetry,
			Reason: fmt.Sprintf("retry %d/%d for %s error", err.RetryCount+1, err.MaxRetries, err.Type),
		}
	}

	// Schema violation: try to extract valid findings from partial output.
	if err.Type == ErrSchemaViolation {
		return RecoveryAction{
			Action: ActionBestEffortParse,
			Reason: "schema violation after max retries; extracting valid findings from partial output",
		}
	}

	// Judge failures.
	if currentState == StateJudging {
		// Hard failures (crash, timeout, rate-limit, context overflow) have no
		// recoverable output — always escalate regardless of round count.
		if err.Type == ErrCrash || err.Type == ErrTimeout || err.Type == ErrRateLimited || err.Type == ErrContextOverflow {
			return RecoveryAction{
				Action: ActionEscalate,
				Reason: fmt.Sprintf("judge hard failure (%s) after %d retries; escalating to human", err.Type, err.MaxRetries),
			}
		}
		// Soft failures (bad/missing output) — fall back to REVISE while rounds remain.
		if round >= maxRounds {
			return RecoveryAction{
				Action: ActionEscalate,
				Reason: fmt.Sprintf("judge failed at max round %d/%d; escalating to human", round, maxRounds),
			}
		}
		return RecoveryAction{
			Action: ActionDefaultRevise,
			Reason: "judge produced invalid output; defaulting to conservative REVISE verdict",
		}
	}

	// Reviewing state: partial failure is handled by dispatch (3/4 reviewers OK).
	// Fall through to escalate.

	// All other states after max retries: escalate.
	return RecoveryAction{
		Action: ActionEscalate,
		Reason: fmt.Sprintf("agent %q failed with %s after %d retries; escalating to human",
			err.Agent, err.Type, err.MaxRetries),
	}
}

// ---------------------------------------------------------------------------
// Best-effort parsing
// ---------------------------------------------------------------------------

// BestEffortParse attempts to parse data as a ReviewerOutput JSON and extract
// individually valid findings. It returns the valid findings and a list of
// parse/validation errors encountered. If the top-level JSON is completely
// unparseable, it returns nil findings and the parse error.
func BestEffortParse(data []byte) ([]Finding, []error) {
	var errs []error

	var output ReviewerOutput
	if err := json.Unmarshal(data, &output); err != nil {
		return nil, []error{fmt.Errorf("failed to parse reviewer output JSON: %w", err)}
	}

	var valid []Finding
	for i, f := range output.Findings {
		var findingErrs []error

		if strings.TrimSpace(f.ID) == "" {
			findingErrs = append(findingErrs, fmt.Errorf("findings[%d].id must not be empty", i))
		}
		if strings.TrimSpace(f.Description) == "" {
			findingErrs = append(findingErrs, fmt.Errorf("findings[%d].description must not be empty", i))
		}
		if strings.TrimSpace(f.Impact) == "" {
			findingErrs = append(findingErrs, fmt.Errorf("findings[%d].impact must not be empty", i))
		}
		if strings.TrimSpace(f.Recommendation) == "" {
			findingErrs = append(findingErrs, fmt.Errorf("findings[%d].recommendation must not be empty", i))
		}
		if strings.TrimSpace(f.Lens) == "" {
			findingErrs = append(findingErrs, fmt.Errorf("findings[%d].lens must not be empty", i))
		}
		if strings.TrimSpace(f.AffectedSection) == "" {
			findingErrs = append(findingErrs, fmt.Errorf("findings[%d].affected_section must not be empty", i))
		}

		if len(findingErrs) > 0 {
			errs = append(errs, findingErrs...)
		} else {
			valid = append(valid, f)
		}
	}

	return valid, errs
}

// ---------------------------------------------------------------------------
// Timeout detection
// ---------------------------------------------------------------------------

// IsTimeoutError returns true if the elapsed duration exceeds or equals the
// configured timeout duration.
func IsTimeoutError(elapsed time.Duration, timeout time.Duration) bool {
	return elapsed >= timeout
}
