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

// AgentTagger is an optional interface that AgentRunner implementations can
// satisfy to produce per-agent runner clones with OTEL resource attributes.
// When the dispatch layer detects this interface, it creates per-agent runners
// so telemetry can be attributed to specific agents.
type AgentTagger interface {
	CloneForAgent(agentName string) AgentRunner
}

// taggedRunner returns a per-agent runner if the runner supports AgentTagger,
// otherwise returns the original runner unchanged.
func taggedRunner(runner AgentRunner, agentName string) AgentRunner {
	if tagger, ok := runner.(AgentTagger); ok {
		return tagger.CloneForAgent(agentName)
	}
	return runner
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
	// AgentName is the full agent name including provider suffix
	// (e.g. "reviewer-clarity-claude" or "reviewer-clarity-codex").
	AgentName string
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

// AgentCompleteFunc is an optional callback invoked when an individual
// reviewer agent completes (either success or failure). This enables
// real-time UI updates as each agent finishes, rather than waiting for
// all agents to complete. May be nil.
type AgentCompleteFunc func(result ReviewerResult)

// ---------------------------------------------------------------------------
// Reviewer lens groups
// ---------------------------------------------------------------------------

// reviewerLensGroups defines the four canonical reviewer lens groups.
var reviewerLensGroups = []string{"clarity", "consistency", "security", "correctness"}

// ---------------------------------------------------------------------------
// Dispatch
// ---------------------------------------------------------------------------

// SpecReviewerLensGroups returns the canonical lens groups for spec review workflows.
func SpecReviewerLensGroups() []string {
	return []string{"clarity", "consistency", "security", "correctness"}
}

// DispatchReviewers launches reviewer agents in parallel, collects their
// results, and applies retry logic on failure. lensGroups defines which
// lens groups to dispatch (e.g. SpecReviewerLensGroups() for spec workflows
// or codereview.CodeReviewLensGroups for code review workflows).
//
// When codexRunner is non-nil, 2*len(lensGroups) goroutines are launched.
// When nil, len(lensGroups) claude-only goroutines.
//
// codexOutputPaths should be nil when codexRunner is nil.
//
// Failure tolerance scales with total reviewers: maxFailuresAllowed = total/2 - 1.
func DispatchReviewers(
	runner AgentRunner,
	codexRunner AgentRunner,
	lensGroups []string,
	prompts map[string]string,
	outputPaths map[string]string,
	codexOutputPaths map[string]string,
	config ReviewDispatchConfig,
	delayFn DelayFunc,
	onComplete AgentCompleteFunc,
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

	// Validate and launch claude reviewers.
	for _, lens := range lensGroups {
		prompt, ok := prompts[lens]
		if !ok {
			return nil, fmt.Errorf("missing prompt for lens group %q", lens)
		}
		outPath, ok := outputPaths[lens]
		if !ok {
			return nil, fmt.Errorf("missing output path for lens group %q", lens)
		}

		agentName := "reviewer-" + lens + "-claude"
		agentRunner := taggedRunner(runner, agentName)
		wg.Add(1)
		go func(r AgentRunner, name, lensGroup, prompt, outPath string) {
			defer wg.Done()

			result := runReviewerWithRetries(r, name, lensGroup, prompt, outPath, config, delayFn)

			if onComplete != nil {
				onComplete(result)
			}
			mu.Lock()
			defer mu.Unlock()
			if result.Error != nil {
				failures = append(failures, result)
			} else {
				results = append(results, result)
			}
		}(agentRunner, agentName, lens, prompt, outPath)
	}

	// Launch codex reviewers when available.
	if codexRunner != nil && codexOutputPaths != nil {
		for _, lens := range lensGroups {
			prompt, ok := prompts[lens]
			if !ok {
				continue // already validated above for claude
			}
			outPath, ok := codexOutputPaths[lens]
			if !ok {
				return nil, fmt.Errorf("missing codex output path for lens group %q", lens)
			}

			agentName := "reviewer-" + lens + "-codex"
			wg.Add(1)
			go func(r AgentRunner, name, lensGroup, prompt, outPath string) {
				defer wg.Done()

				result := runReviewerWithRetries(r, name, lensGroup, prompt, outPath, config, delayFn)

				if onComplete != nil {
					onComplete(result)
				}
				mu.Lock()
				defer mu.Unlock()
				if result.Error != nil {
					failures = append(failures, result)
				} else {
					results = append(results, result)
				}
			}(codexRunner, agentName, lens, prompt, outPath)
		}
	}

	wg.Wait()

	// Compute total reviewer count and failure tolerance.
	totalReviewers := len(results) + len(failures)
	maxFailuresAllowed := totalReviewers/2 - 1
	if maxFailuresAllowed < 1 {
		maxFailuresAllowed = 1
	}

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
			dispatchResult.CoverageLoss = append(dispatchResult.CoverageLoss, f.AgentName)
		}
	}

	// Log warnings for reduced coverage.
	if len(failures) > 0 && len(failures) <= maxFailuresAllowed {
		log.Printf("WARNING: %d reviewer(s) failed after %d retries; proceeding with reduced coverage (lost: %v)",
			len(failures), config.MaxRetries, dispatchResult.CoverageLoss)
	}

	// If too many reviewers failed, return error for escalation.
	if len(failures) > maxFailuresAllowed {
		agentNames := make([]string, len(failures))
		for i, f := range failures {
			agentNames[i] = f.AgentName
		}
		return dispatchResult, fmt.Errorf(
			"review dispatch failed: %d/%d reviewers failed after retries (failed: %v); escalation required",
			len(failures), totalReviewers, agentNames,
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
	agentName string,
	lensGroup string,
	prompt string,
	outputPath string,
	config ReviewDispatchConfig,
	delayFn DelayFunc,
) ReviewerResult {
	result := ReviewerResult{
		LensGroup: lensGroup,
		AgentName: agentName,
	}

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
