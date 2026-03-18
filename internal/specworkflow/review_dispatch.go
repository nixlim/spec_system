// Package specworkflow defines the core types for the adversarial spec review
// workflow. This file implements parallel dispatch of reviewer agents across
// four lens groups (clarity, consistency, security, correctness), with retry
// logic, output validation, and graceful degradation when a minority of
// reviewers fail.
package specworkflow

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Interfaces
// ---------------------------------------------------------------------------

// AgentRunner abstracts the execution of an agent process. Implementations
// launch the agent with the given prompt, write output to outputPath, and
// return execution metadata. This interface enables testing with mock runners
// that avoid real subprocess execution.
type AgentRunner interface {
	// Run executes an agent with the given prompt and timeout.
	// It returns the process exit code, any stderr output, the estimated
	// cost in USD, the wall-clock duration in milliseconds, and any error
	// from the runner infrastructure itself (not the agent).
	Run(prompt string, outputPath string, timeoutSeconds int) (exitCode int, stderr string, costUSD float64, durationMS int64, err error)
}

// CostProvider abstracts read access to cumulative cost data from an external
// telemetry source (e.g. the OTEL receiver). The orchestrator uses this to
// sync authoritative cost data into workflow state, since the Claude CLI may
// report zero cost while OTEL telemetry captures the real cost.
type CostProvider interface {
	// GetCostUSD returns the cumulative cost in USD tracked by the provider.
	GetCostUSD() float64
}

// ---------------------------------------------------------------------------
// Result types
// ---------------------------------------------------------------------------

// ReviewerResult holds the outcome of a single reviewer agent invocation,
// including the parsed output on success or the structured error on failure.
type ReviewerResult struct {
	// LensGroup identifies which review lens this result belongs to.
	// One of: "clarity", "consistency", "security", "correctness".
	LensGroup string
	// Output is the parsed reviewer output, nil on failure.
	Output *ReviewerOutput
	// Error is the structured agent error, nil on success.
	Error *AgentError
	// CostUSD is the cumulative cost across all attempts for this reviewer.
	CostUSD float64
	// DurationMS is the cumulative wall-clock time across all attempts.
	DurationMS int64
}

// ReviewDispatchResult aggregates the results from all reviewer agents.
type ReviewDispatchResult struct {
	// Results contains the successful reviewer outcomes.
	Results []ReviewerResult
	// Failures contains reviewer outcomes that failed after all retries.
	Failures []ReviewerResult
	// TotalCostUSD is the sum of costs across all reviewers (success + failure).
	TotalCostUSD float64
	// TotalDurationMS is the maximum wall-clock duration across all reviewers
	// (since they run in parallel, total elapsed time is the slowest one).
	TotalDurationMS int64
	// ReducedCoverage is true if fewer than 4 reviewers succeeded.
	ReducedCoverage bool
	// CoverageLoss lists the lens groups that failed to produce results.
	CoverageLoss []string
}

// ---------------------------------------------------------------------------
// Configuration
// ---------------------------------------------------------------------------

// ReviewDispatchConfig controls retry and timeout behaviour for reviewer
// dispatch.
type ReviewDispatchConfig struct {
	// MaxRetries is the maximum number of retry attempts per reviewer.
	MaxRetries int
	// TimeoutSeconds is the timeout for each individual agent invocation.
	TimeoutSeconds int
}

// ---------------------------------------------------------------------------
// DelayFunc
// ---------------------------------------------------------------------------

// DelayFunc is a function that pauses execution for the given duration.
// Production code uses time.Sleep; tests inject a no-op to avoid delays.
type DelayFunc func(time.Duration)

// ---------------------------------------------------------------------------
// Reviewer lens groups
// ---------------------------------------------------------------------------

// reviewerLensGroups defines the four canonical reviewer lens groups.
var reviewerLensGroups = []string{"clarity", "consistency", "security", "correctness"}

// maxFailuresAllowed is the maximum number of reviewer failures before the
// entire dispatch is considered failed. If more than this many reviewers fail
// after all retries, DispatchReviewers returns an error.
const maxFailuresAllowed = 1

// ---------------------------------------------------------------------------
// Dispatch
// ---------------------------------------------------------------------------

// DispatchReviewers launches four reviewer agents in parallel (one per lens
// group), collects their results, and applies retry logic on failure.
//
// Each reviewer is run with its corresponding prompt and output path from the
// prompts and outputPaths maps (keyed by lens group name). On failure, the
// agent is retried up to config.MaxRetries times with exponential backoff
// delays provided by delayFn.
//
// If exactly one reviewer fails after all retries, the dispatch proceeds with
// reduced coverage and logs a warning. If two or more reviewers fail, the
// function returns an error indicating escalation is required.
//
// The delayFn parameter controls how long to wait between retries. Pass
// time.Sleep for production use or a no-op function for tests.
func DispatchReviewers(
	runner AgentRunner,
	prompts map[string]string,
	outputPaths map[string]string,
	config ReviewDispatchConfig,
	delayFn DelayFunc,
) (*ReviewDispatchResult, error) {
	if delayFn == nil {
		delayFn = time.Sleep
	}

	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		results  []ReviewerResult
		failures []ReviewerResult
	)

	for _, lens := range reviewerLensGroups {
		prompt, ok := prompts[lens]
		if !ok {
			return nil, fmt.Errorf("missing prompt for lens group %q", lens)
		}
		outPath, ok := outputPaths[lens]
		if !ok {
			return nil, fmt.Errorf("missing output path for lens group %q", lens)
		}

		wg.Add(1)
		go func(lensGroup, prompt, outPath string) {
			defer wg.Done()

			result := runReviewerWithRetries(runner, lensGroup, prompt, outPath, config, delayFn)

			mu.Lock()
			defer mu.Unlock()
			if result.Error != nil {
				failures = append(failures, result)
			} else {
				results = append(results, result)
			}
		}(lens, prompt, outPath)
	}

	wg.Wait()

	// Build the dispatch result.
	dispatchResult := &ReviewDispatchResult{
		Results:  results,
		Failures: failures,
	}

	// Compute totals.
	for _, r := range results {
		dispatchResult.TotalCostUSD += r.CostUSD
	}
	for _, r := range failures {
		dispatchResult.TotalCostUSD += r.CostUSD
	}

	// TotalDurationMS is the max across all reviewers (parallel execution).
	for _, r := range results {
		if r.DurationMS > dispatchResult.TotalDurationMS {
			dispatchResult.TotalDurationMS = r.DurationMS
		}
	}
	for _, r := range failures {
		if r.DurationMS > dispatchResult.TotalDurationMS {
			dispatchResult.TotalDurationMS = r.DurationMS
		}
	}

	// Coverage analysis.
	if len(failures) > 0 {
		dispatchResult.ReducedCoverage = true
		for _, f := range failures {
			dispatchResult.CoverageLoss = append(dispatchResult.CoverageLoss, f.LensGroup)
		}
	}

	// Log warnings for reduced coverage.
	if len(failures) == 1 {
		log.Printf("WARNING: reviewer %q failed after %d retries; proceeding with reduced coverage (lenses lost: %v)",
			failures[0].LensGroup, config.MaxRetries, dispatchResult.CoverageLoss)
	}

	// If 2+ reviewers failed, return error for escalation.
	if len(failures) > maxFailuresAllowed {
		lensNames := make([]string, len(failures))
		for i, f := range failures {
			lensNames[i] = f.LensGroup
		}
		return dispatchResult, fmt.Errorf(
			"review dispatch failed: %d/%d reviewers failed after retries (failed lenses: %v); escalation required",
			len(failures), len(reviewerLensGroups), lensNames,
		)
	}

	return dispatchResult, nil
}

// ---------------------------------------------------------------------------
// Per-reviewer retry loop
// ---------------------------------------------------------------------------

// runReviewerWithRetries runs a single reviewer agent with retry logic.
// It accumulates cost and duration across all attempts.
func runReviewerWithRetries(
	runner AgentRunner,
	lensGroup string,
	prompt string,
	outputPath string,
	config ReviewDispatchConfig,
	delayFn DelayFunc,
) ReviewerResult {
	result := ReviewerResult{
		LensGroup: lensGroup,
	}

	agentName := "reviewer-" + lensGroup

	for attempt := 0; attempt <= config.MaxRetries; attempt++ {
		// Wait before retry (not before first attempt).
		if attempt > 0 {
			delay := RetryDelay(attempt - 1)
			delayFn(delay)
		}

		exitCode, stderr, costUSD, durationMS, runErr := runner.Run(prompt, outputPath, config.TimeoutSeconds)
		result.CostUSD += costUSD
		result.DurationMS += durationMS

		// Infrastructure error from the runner itself.
		if runErr != nil {
			result.Error = &AgentError{
				Type:       ErrCrash,
				Agent:      agentName,
				Detail:     runErr.Error(),
				RetryCount: attempt,
				MaxRetries: config.MaxRetries,
			}
			continue
		}

		// Detect failure type from exit code, stderr, output file.
		failureType := DetectFailureType(exitCode, stderr, outputPath)
		if failureType != "" {
			result.Error = &AgentError{
				Type:       failureType,
				Agent:      agentName,
				Detail:     stderr,
				RetryCount: attempt,
				MaxRetries: config.MaxRetries,
			}
			continue
		}

		// Read and parse the output file.
		data, readErr := os.ReadFile(outputPath)
		if readErr != nil {
			result.Error = &AgentError{
				Type:       ErrMissingOutput,
				Agent:      agentName,
				Detail:     readErr.Error(),
				RetryCount: attempt,
				MaxRetries: config.MaxRetries,
			}
			continue
		}

		var output ReviewerOutput
		if jsonErr := json.Unmarshal(data, &output); jsonErr != nil {
			result.Error = &AgentError{
				Type:       ErrInvalidJSON,
				Agent:      agentName,
				Detail:     jsonErr.Error(),
				RetryCount: attempt,
				MaxRetries: config.MaxRetries,
			}
			continue
		}

		// Validate the parsed output. ValidateReviewerOutput separates
		// valid findings from rejected ones (e.g. missing recommendation).
		// Only retry if there are zero valid findings AND validation errors
		// exist — that means the output is truly broken. If some findings
		// are valid, accept the output with only the valid subset.
		validFindings, rejectedCount, validationErrs := ValidateReviewerOutput(&output)
		if len(validFindings) == 0 && len(validationErrs) > 0 {
			detail := fmt.Sprintf("%d validation errors: %v", len(validationErrs), validationErrs[0])
			result.Error = &AgentError{
				Type:       ErrSchemaViolation,
				Agent:      agentName,
				Detail:     detail,
				RetryCount: attempt,
				MaxRetries: config.MaxRetries,
			}
			continue
		}

		// Log a warning if some findings were rejected but output is still usable.
		if rejectedCount > 0 {
			log.Printf("WARNING: %s: accepted %d valid findings, rejected %d (validation: %v)",
				agentName, len(validFindings), rejectedCount, validationErrs)
		}

		// Replace findings with only the validated subset.
		output.Findings = validFindings

		// Success — clear any previous error and set output.
		result.Error = nil
		result.Output = &output
		return result
	}

	// All attempts exhausted; result.Error is set from the last attempt.
	return result
}
