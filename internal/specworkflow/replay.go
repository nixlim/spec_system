package specworkflow

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// ReplayDiscoveryMerge re-runs the intelligent merge of Claude and Codex
// discovery outputs without re-dispatching agents. It reads the per-provider
// output files from disk, dispatches a merge agent with validation+retry,
// and writes the merged result to discovery-output.json. Falls back to
// mechanical merge on agent failure.
func ReplayDiscoveryMerge(runner AgentRunner, specDir string, round int, timeoutSeconds int) (string, error) {
	// Check both "claude" and "opencode" names for the primary provider output.
	claudePath := filepath.Join(specDir, VersionedFilename("discovery-output", "claude", round, ".json"))
	if _, err := os.Stat(claudePath); err != nil {
		altPath := filepath.Join(specDir, VersionedFilename("discovery-output", "opencode", round, ".json"))
		if _, altErr := os.Stat(altPath); altErr == nil {
			claudePath = altPath
		}
	}
	codexPath := filepath.Join(specDir, VersionedFilename("discovery-output", "codex", round, ".json"))

	// Read and validate both provider outputs.
	claudeOutput, claudeData, err := parseAndValidateDiscoveryOutput(claudePath)
	if err != nil {
		return "", fmt.Errorf("read primary provider discovery output: %w", err)
	}
	codexOutput, codexData, err := parseAndValidateDiscoveryOutput(codexPath)
	if err != nil {
		return "", fmt.Errorf("read codex discovery output: %w", err)
	}

	basePrompt := buildDiscoveryMergePrompt(claudeData, codexData)
	mergedPath := filepath.Join(specDir, VersionedMergedFilename("discovery-output", round, ".json"))

	var mergeRunner AgentRunner = runner
	if jo, ok := runner.(JSONOnlyRunner); ok {
		mergeRunner = jo.ForJSONOnlyMode(DiscoveryOutputSchema())
	}

	// Dispatch with validation+retry (2 attempts).
	result, runErr := RunWithValidation(ValidateAndRetryConfig{
		AgentName:      "discovery-merge-replay",
		MaxAttempts:    2,
		OutputPath:     mergedPath,
		TimeoutSeconds: timeoutSeconds,
		Validator:      DiscoveryOutputValidator(),
		Runner:         mergeRunner,
		BuildPrompt: func(validationErrors []string) string {
			return AppendValidationErrorsToPrompt(basePrompt, validationErrors)
		},
	})

	var finalData []byte

	if runErr == nil && result.Data != nil {
		finalData = result.Data
		var out DiscoveryOutput
		if json.Unmarshal(finalData, &out) == nil {
			log.Printf("[replay] discovery merge agent produced: %d actors, %d priorities, %d open questions",
				len(out.Actors), len(out.Priorities), len(out.OpenQuestions))
		}
	} else {
		// Fall back to mechanical merge.
		log.Printf("[replay] agent-based merge failed, falling back to mechanical merge")
		merged := MergeDiscoveryOutputs(claudeOutput, codexOutput)
		fb, marshalErr := json.MarshalIndent(merged, "", "  ")
		if marshalErr != nil {
			return "", fmt.Errorf("marshal mechanical merge: %w", marshalErr)
		}
		finalData = fb
	}

	// Write canonical discovery-output.json.
	canonicalPath := filepath.Join(specDir, "discovery-output.json")
	if err := os.WriteFile(canonicalPath, finalData, 0o644); err != nil {
		return "", fmt.Errorf("write discovery-output.json: %w", err)
	}

	// Also update the versioned copy.
	versionedPath := filepath.Join(specDir, fmt.Sprintf("discovery-output-%d.json", round))
	if err := os.WriteFile(versionedPath, finalData, 0o644); err != nil {
		log.Printf("[replay] warning: failed to write versioned discovery output: %v", err)
	}

	return "Merged 2 discovery outputs", nil
}

// ReplayReviewMerge re-runs the dedup merge of reviewer outputs for the given
// round without re-dispatching agents. It reads all review output files for
// the round, merges them, and writes the merged findings file.
func ReplayReviewMerge(specDir string, round int) (string, error) {
	// Find all review output files for this round.
	var reviewerOutputs []*ReviewerOutput
	var filesRead int

	entries, err := os.ReadDir(specDir)
	if err != nil {
		return "", fmt.Errorf("read spec directory: %w", err)
	}

	roundSuffix := fmt.Sprintf("round-%d.json", round)
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "review-") || !strings.HasSuffix(name, roundSuffix) {
			continue
		}

		data, readErr := os.ReadFile(filepath.Join(specDir, name))
		if readErr != nil {
			log.Printf("[replay] warning: could not read %s: %v", name, readErr)
			continue
		}

		var output ReviewerOutput
		if parseErr := json.Unmarshal(data, &output); parseErr != nil {
			log.Printf("[replay] warning: could not parse %s: %v", name, parseErr)
			continue
		}

		reviewerOutputs = append(reviewerOutputs, &output)
		filesRead++
	}

	if len(reviewerOutputs) == 0 {
		return "", fmt.Errorf("no valid review output files found for round %d", round)
	}

	// Merge findings using the same dedup algorithm as the orchestrator.
	merged, err := MergeReviewerOutputs(reviewerOutputs, round, SpecDedupKey, true)
	if err != nil {
		return "", fmt.Errorf("merge reviewer outputs: %w", err)
	}

	// Write merged findings.
	mergedPath := filepath.Join(specDir, fmt.Sprintf("merged-findings-round-%d.json", round))
	mergedData, marshalErr := json.MarshalIndent(merged, "", "  ")
	if marshalErr != nil {
		return "", fmt.Errorf("marshal merged findings: %w", marshalErr)
	}
	if err := os.WriteFile(mergedPath, mergedData, 0o644); err != nil {
		return "", fmt.Errorf("write merged findings: %w", err)
	}

	msg := fmt.Sprintf("Merged %d reviewer outputs (%d findings, %d after dedup)", filesRead, merged.TotalFindings, merged.TotalAfterDedup)
	log.Printf("[replay] %s", msg)
	return msg, nil
}

// ReplayDraftingCombine re-runs the Claude combine agent that merges two
// drafter outputs without re-dispatching the drafters. Reads the per-provider
// drafter output files, dispatches the combine agent, and writes the combined
// result to drafter-output.json.
func ReplayDraftingCombine(runner AgentRunner, specDir string, version int, timeoutSeconds int) (string, error) {
	// Check both "claude" and "opencode" names for the primary provider output.
	claudeOutPath := filepath.Join(specDir, VersionedFilename("drafter-output", "claude", version, ".json"))
	if _, err := os.Stat(claudeOutPath); err != nil {
		altPath := filepath.Join(specDir, VersionedFilename("drafter-output", "opencode", version, ".json"))
		if _, altErr := os.Stat(altPath); altErr == nil {
			claudeOutPath = altPath
		}
	}
	codexOutPath := filepath.Join(specDir, VersionedFilename("drafter-output", "codex", version, ".json"))

	// Validate both files exist and are valid JSON.
	claudeData, err := os.ReadFile(claudeOutPath)
	if err != nil {
		return "", fmt.Errorf("read primary provider drafter output: %w", err)
	}
	var claudeDraft DrafterOutput
	if err := json.Unmarshal(claudeData, &claudeDraft); err != nil {
		return "", fmt.Errorf("parse claude drafter output: %w", err)
	}

	codexData, err := os.ReadFile(codexOutPath)
	if err != nil {
		return "", fmt.Errorf("read codex drafter output: %w", err)
	}
	var codexDraft DrafterOutput
	if err := json.Unmarshal(codexData, &codexDraft); err != nil {
		return "", fmt.Errorf("parse codex drafter output: %w", err)
	}

	// Dispatch Claude combine agent.
	combinedOutPath := filepath.Join(specDir, VersionedCombinedFilename("drafter-output", version, ".json"))
	combinePrompt := buildCombinePrompt(claudeOutPath, codexOutPath, combinedOutPath)

	combineRunner := taggedRunner(runner, "drafter-combine")
	combineExitCode, combineStderr, _, _, combineErr := combineRunner.Run(combinePrompt, combinedOutPath, timeoutSeconds)

	if combineErr == nil && combineExitCode != 0 {
		combineErr = fmt.Errorf("combine agent exited with code %d: %s", combineExitCode, combineStderr)
	}
	if combineErr == nil {
		if ft := DetectFailureType(combineExitCode, combineStderr, combinedOutPath); ft != "" {
			combineErr = fmt.Errorf("combine agent failure: %s", ft)
		}
	}

	if combineErr != nil {
		// Fallback: concatenate with attribution.
		log.Printf("[replay] combine reviser failed, falling back to concatenation: %v", combineErr)
		concatenated := concatenateDrafts(claudeData, codexData)
		if err := os.WriteFile(combinedOutPath, concatenated, 0o644); err != nil {
			return "", fmt.Errorf("write concatenated draft: %w", err)
		}
	}

	// Copy combined output to canonical drafter-output.json.
	combinedData, err := os.ReadFile(combinedOutPath)
	if err != nil {
		return "", fmt.Errorf("read combined drafter output: %w", err)
	}
	finalOutPath := filepath.Join(specDir, "drafter-output.json")
	if err := os.WriteFile(finalOutPath, combinedData, 0o644); err != nil {
		return "", fmt.Errorf("write drafter-output.json: %w", err)
	}

	log.Printf("[replay] drafting combine complete")
	return "Combined 2 drafter outputs", nil
}

// ReplayTaskReviewMerge re-runs the dedup merge of task reviewer outputs for
// the given round without re-dispatching agents.
func ReplayTaskReviewMerge(specDir string, round int) (string, error) {
	// Find all task review output files for this round.
	var reviewerOutputs []*ReviewerOutput
	var filesRead int

	entries, err := os.ReadDir(specDir)
	if err != nil {
		return "", fmt.Errorf("read spec directory: %w", err)
	}

	for _, e := range entries {
		name := e.Name()
		// Task review files follow the pattern: task-review-{provider}-v{round}.json
		if !strings.HasPrefix(name, "task-review-") {
			continue
		}
		expectedSuffix := fmt.Sprintf("-v%d.json", round)
		if !strings.HasSuffix(name, expectedSuffix) {
			continue
		}

		data, readErr := os.ReadFile(filepath.Join(specDir, name))
		if readErr != nil {
			log.Printf("[replay] warning: could not read %s: %v", name, readErr)
			continue
		}

		var output ReviewerOutput
		if parseErr := json.Unmarshal(data, &output); parseErr != nil {
			log.Printf("[replay] warning: could not parse %s: %v", name, parseErr)
			continue
		}

		reviewerOutputs = append(reviewerOutputs, &output)
		filesRead++
	}

	if len(reviewerOutputs) == 0 {
		return "", fmt.Errorf("no valid task review output files found for round %d", round)
	}

	// Merge using same algorithm as orchestrator.
	merged, err := MergeReviewerOutputs(reviewerOutputs, round, SpecDedupKey, true)
	if err != nil {
		return "", fmt.Errorf("merge task review outputs: %w", err)
	}

	// Write merged findings to task-findings-round-{N}.json.
	findingsPath := filepath.Join(specDir, fmt.Sprintf("task-findings-round-%d.json", round))
	findingsData, marshalErr := json.MarshalIndent(merged, "", "  ")
	if marshalErr != nil {
		return "", fmt.Errorf("marshal task review findings: %w", marshalErr)
	}
	if err := os.WriteFile(findingsPath, findingsData, 0o644); err != nil {
		return "", fmt.Errorf("write task-findings-round-%d.json: %w", round, err)
	}

	msg := fmt.Sprintf("Merged %d task reviewer outputs (%d findings, %d after dedup)", filesRead, merged.TotalFindings, merged.TotalAfterDedup)
	log.Printf("[replay] %s", msg)
	return msg, nil
}
