package codedoc

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// ---------------------------------------------------------------------------
// Discovery result
// ---------------------------------------------------------------------------

// cdDiscoveryResult holds the outcome of a single discovery agent dispatch.
type cdDiscoveryResult struct {
	provider string
	output   *DiscoveryOutput
	data     []byte
	cost     float64
	duration int64
	err      error
}

// ---------------------------------------------------------------------------
// File naming helpers
// ---------------------------------------------------------------------------

// versionedFilename builds a filename like "discovery-output-claude-v1.json".
func versionedFilename(base, provider string, round int, ext string) string {
	return fmt.Sprintf("%s-%s-v%d%s", base, provider, round, ext)
}

// RunDiscovery executes the discovery phase. When dual-provider is enabled,
// both Claude and Codex run in parallel and outputs are merged via an
// LLM-based merge agent (Section 3a). On single-provider failure, the
// survivor's output is used. On both failures, an error is returned.
func RunDiscovery(deps DiscoveryDeps) (*DiscoveryResult, error) {
	prompt := BuildDiscoveryPrompt(deps.CodePath, deps.Mode)
	timeout := deps.Config.DiscoveryTimeoutSeconds

	if deps.Config.EnableCodexCodedocDiscovery && deps.CodexRunner != nil {
		return runDualDiscovery(deps, prompt, timeout)
	}
	return runSingleDiscovery(deps, prompt, timeout)
}

// ---------------------------------------------------------------------------
// Single-provider discovery
// ---------------------------------------------------------------------------

func runSingleDiscovery(deps DiscoveryDeps, prompt string, timeout int) (*DiscoveryResult, error) {
	outPath := filepath.Join(deps.FeatureDir, "discovery-output.json")

	maxAttempts := deps.Config.MaxRetries + 1
	if maxAttempts < 2 {
		maxAttempts = 2
	}

	var lastErrors []string

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		currentPrompt := appendValidationErrors(prompt, lastErrors)

		_, stderr, cost, duration, runErr := deps.Runner.Run(currentPrompt, outPath, timeout)
		if runErr != nil {
			if attempt < maxAttempts {
				log.Printf("[codedoc-discovery] dispatch failed attempt %d/%d: %v", attempt, maxAttempts, runErr)
				lastErrors = []string{fmt.Sprintf("agent dispatch failed: %v", runErr)}
				continue
			}
			return nil, fmt.Errorf("discovery agent failed after %d attempts: %w", maxAttempts, runErr)
		}
		_ = stderr

		output, data, parseErr := parseAndValidateDiscovery(outPath)
		if parseErr != nil {
			if attempt < maxAttempts {
				log.Printf("[codedoc-discovery] validation failed attempt %d/%d: %v", attempt, maxAttempts, parseErr)
				lastErrors = []string{parseErr.Error()}
				continue
			}
			return nil, fmt.Errorf("discovery output invalid after %d attempts: %w", maxAttempts, parseErr)
		}

		// Save versioned copy.
		vPath := filepath.Join(deps.FeatureDir, versionedFilename("discovery-output", "claude", deps.Round, ".json"))
		_ = os.WriteFile(vPath, data, 0o644)

		log.Printf("[codedoc-discovery] single-provider complete: %d modules, %d entry points, status=%s",
			len(output.Modules), len(output.EntryPoints), output.CompletionStatus.Status)

		return &DiscoveryResult{
			Output:     output,
			CostUSD:    cost,
			DurationMS: duration,
		}, nil
	}

	return nil, fmt.Errorf("discovery agent exhausted all %d attempts", maxAttempts)
}

// ---------------------------------------------------------------------------
// Dual-provider discovery
// ---------------------------------------------------------------------------

func runDualDiscovery(deps DiscoveryDeps, prompt string, timeout int) (*DiscoveryResult, error) {
	claudeOutPath := filepath.Join(deps.FeatureDir, versionedFilename("discovery-output", "claude", deps.Round, ".json"))
	codexOutPath := filepath.Join(deps.FeatureDir, versionedFilename("discovery-output", "codex", deps.Round, ".json"))

	resultsCh := make(chan cdDiscoveryResult, 2)
	var wg sync.WaitGroup

	// Claude discovery agent.
	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Printf("[codedoc-discovery] dispatching claude discovery agent")
		_, _, cost, duration, runErr := deps.Runner.Run(prompt, claudeOutPath, timeout)
		if runErr != nil {
			resultsCh <- cdDiscoveryResult{provider: "claude", cost: cost, duration: duration, err: runErr}
			return
		}
		output, data, parseErr := parseAndValidateDiscovery(claudeOutPath)
		if parseErr != nil {
			resultsCh <- cdDiscoveryResult{provider: "claude", cost: cost, duration: duration, err: parseErr}
			return
		}
		resultsCh <- cdDiscoveryResult{provider: "claude", output: output, data: data, cost: cost, duration: duration}
	}()

	// Codex discovery agent.
	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Printf("[codedoc-discovery] dispatching codex discovery agent")
		_, _, cost, duration, runErr := deps.CodexRunner.Run(prompt, codexOutPath, timeout)
		if runErr != nil {
			resultsCh <- cdDiscoveryResult{provider: "codex", cost: cost, duration: duration, err: runErr}
			return
		}
		output, data, parseErr := parseAndValidateDiscovery(codexOutPath)
		if parseErr != nil {
			resultsCh <- cdDiscoveryResult{provider: "codex", cost: cost, duration: duration, err: parseErr}
			return
		}
		resultsCh <- cdDiscoveryResult{provider: "codex", output: output, data: data, cost: cost, duration: duration}
	}()

	// Wait for both to finish, then close the channel.
	go func() {
		wg.Wait()
		close(resultsCh)
	}()

	var claude, codex *cdDiscoveryResult
	var totalCost float64
	var maxDuration int64
	for r := range resultsCh {
		r := r
		totalCost += r.cost
		if r.duration > maxDuration {
			maxDuration = r.duration
		}
		if r.provider == "claude" {
			claude = &r
		} else {
			codex = &r
		}
	}

	// Both failed → error (causes escalation).
	if claude.err != nil && codex.err != nil {
		return nil, fmt.Errorf("both discovery providers failed: claude=%v, codex=%v", claude.err, codex.err)
	}

	// Single-provider fallback.
	if claude.err != nil {
		log.Printf("[codedoc-discovery] WARNING: claude failed (%v), using codex-only output", claude.err)
		canonicalPath := filepath.Join(deps.FeatureDir, "discovery-output.json")
		_ = os.WriteFile(canonicalPath, codex.data, 0o644)
		return &DiscoveryResult{
			Output:     codex.output,
			CostUSD:    totalCost,
			DurationMS: maxDuration,
			Warnings:   []string{"claude discovery failed; using codex-only output"},
		}, nil
	}
	if codex.err != nil {
		log.Printf("[codedoc-discovery] WARNING: codex failed (%v), using claude-only output", codex.err)
		canonicalPath := filepath.Join(deps.FeatureDir, "discovery-output.json")
		_ = os.WriteFile(canonicalPath, claude.data, 0o644)
		return &DiscoveryResult{
			Output:     claude.output,
			CostUSD:    totalCost,
			DurationMS: maxDuration,
			Warnings:   []string{"codex discovery failed; using claude-only output"},
		}, nil
	}

	// Both succeeded → merge via LLM agent.
	mergedResult, mergeErr := mergeDiscoveryOutputs(deps, claude, codex)
	if mergeErr != nil {
		log.Printf("[codedoc-discovery] WARNING: merge agent failed (%v), using claude-only output", mergeErr)
		canonicalPath := filepath.Join(deps.FeatureDir, "discovery-output.json")
		_ = os.WriteFile(canonicalPath, claude.data, 0o644)
		return &DiscoveryResult{
			Output:     claude.output,
			CostUSD:    totalCost + mergedResult.mergeCost,
			DurationMS: maxDuration,
			Warnings:   []string{"merge agent failed; using claude-only output"},
		}, nil
	}

	// Save merged output as canonical and versioned.
	canonicalPath := filepath.Join(deps.FeatureDir, "discovery-output.json")
	_ = os.WriteFile(canonicalPath, mergedResult.data, 0o644)
	mergedVersionPath := filepath.Join(deps.FeatureDir, versionedFilename("discovery-output", "merged", deps.Round, ".json"))
	_ = os.WriteFile(mergedVersionPath, mergedResult.data, 0o644)

	log.Printf("[codedoc-discovery] dual-provider merge complete: %d modules",
		len(mergedResult.output.Modules))

	return &DiscoveryResult{
		Output:     mergedResult.output,
		CostUSD:    totalCost + mergedResult.mergeCost,
		DurationMS: maxDuration,
	}, nil
}

// ---------------------------------------------------------------------------
// Merge agent
// ---------------------------------------------------------------------------

type mergeResult struct {
	output    *DiscoveryOutput
	data      []byte
	mergeCost float64
}

func mergeDiscoveryOutputs(deps DiscoveryDeps, claude, codex *cdDiscoveryResult) (*mergeResult, error) {
	mergePrompt := BuildDiscoveryMergePrompt(string(claude.data), string(codex.data))

	runner := deps.MergeRunner
	if runner == nil {
		runner = deps.Runner
	}

	mergeOutPath := filepath.Join(deps.FeatureDir, versionedFilename("discovery-output", "merge-raw", deps.Round, ".json"))
	_, _, cost, _, runErr := runner.Run(mergePrompt, mergeOutPath, deps.Config.DiscoveryTimeoutSeconds)
	if runErr != nil {
		return &mergeResult{mergeCost: cost}, fmt.Errorf("merge agent dispatch failed: %w", runErr)
	}

	output, data, parseErr := parseAndValidateDiscovery(mergeOutPath)
	if parseErr != nil {
		return &mergeResult{mergeCost: cost}, fmt.Errorf("merge agent output invalid: %w", parseErr)
	}

	return &mergeResult{output: output, data: data, mergeCost: cost}, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// parseAndValidateDiscovery reads a discovery output file, unmarshals it,
// and validates the required fields.
func parseAndValidateDiscovery(path string) (*DiscoveryOutput, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("reading discovery output %s: %w", path, err)
	}

	var output DiscoveryOutput
	if err := json.Unmarshal(data, &output); err != nil {
		return nil, nil, fmt.Errorf("parsing discovery output JSON: %w", err)
	}

	if errs := ValidateDiscoveryOutput(&output); len(errs) > 0 {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
		}
		return nil, nil, fmt.Errorf("discovery output validation failed: %s", msgs[0])
	}

	return &output, data, nil
}

// appendValidationErrors appends validation error context to a prompt for
// the retry pattern (same as specworkflow.AppendValidationErrorsToPrompt).
func appendValidationErrors(prompt string, errors []string) string {
	if len(errors) == 0 {
		return prompt
	}
	result := prompt + "\n\n## IMPORTANT: Previous attempt failed\n\nFix these errors:\n"
	for _, e := range errors {
		result += "- " + e + "\n"
	}
	return result
}
