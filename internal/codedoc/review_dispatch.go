package codedoc

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Interfaces
// ---------------------------------------------------------------------------

// CDAgentRunner abstracts execution of an agent process for codedoc workflows.
type CDAgentRunner interface {
	Run(prompt string, outputPath string, timeoutSeconds int) (exitCode int, stderr string, costUSD float64, durationMS int64, err error)
}

// ---------------------------------------------------------------------------
// Result types
// ---------------------------------------------------------------------------

// CDReviewerResult holds the outcome of a single codedoc reviewer agent.
type CDReviewerResult struct {
	LensGroup  string
	AgentName  string
	Output     *ReviewerOutput
	Error      error
	CostUSD    float64
	DurationMS int64
}

// CDReviewDispatchResult aggregates results from all codedoc reviewer agents.
type CDReviewDispatchResult struct {
	Results         []CDReviewerResult
	Failures        []CDReviewerResult
	TotalCostUSD    float64
	TotalDurationMS int64
	ReducedCoverage bool
	CoverageLoss    []string
}

// CDReviewDispatchConfig controls retry and timeout for codedoc review dispatch.
type CDReviewDispatchConfig struct {
	MaxRetries     int
	TimeoutSeconds int
}

// CDDelayFunc pauses execution for the given duration.
type CDDelayFunc func(time.Duration)

// CDAgentCompleteFunc is called when an individual reviewer completes.
type CDAgentCompleteFunc func(result CDReviewerResult)

// ---------------------------------------------------------------------------
// Dedup key
// ---------------------------------------------------------------------------

// CodedocDedupKey produces a dedup key for codedoc review findings using the
// tuple (lens, affected_file, affected_section). Two findings with the same
// key are considered duplicates.
func CodedocDedupKey(f *ReviewFinding) string {
	lens := strings.ToLower(strings.TrimSpace(f.Lens))
	file := strings.ToLower(strings.TrimSpace(f.AffectedFile))
	section := strings.ToLower(strings.TrimSpace(f.AffectedSection))
	return lens + "|" + file + "|" + section
}

// ---------------------------------------------------------------------------
// Dispatch
// ---------------------------------------------------------------------------

// DispatchCodedocReviewers launches codedoc reviewer agents in parallel across
// 4 lens groups, collects results, and applies retry logic on failure.
//
// When codexRunner is non-nil, dual-provider reviewers are launched.
// Failure tolerance: maxFailuresAllowed = total/2 - 1 (min 1).
func DispatchCodedocReviewers(
	runner CDAgentRunner,
	codexRunner CDAgentRunner,
	lensGroups []string,
	prompts map[string]string,
	outputPaths map[string]string,
	codexOutputPaths map[string]string,
	config CDReviewDispatchConfig,
	delayFn CDDelayFunc,
	onComplete CDAgentCompleteFunc,
) (*CDReviewDispatchResult, error) {
	if delayFn == nil {
		delayFn = time.Sleep
	}

	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		results  []CDReviewerResult
		failures []CDReviewerResult
	)

	// Launch claude reviewers.
	for _, lens := range lensGroups {
		prompt, ok := prompts[lens]
		if !ok {
			return nil, fmt.Errorf("missing prompt for lens group %q", lens)
		}
		outPath, ok := outputPaths[lens]
		if !ok {
			return nil, fmt.Errorf("missing output path for lens group %q", lens)
		}

		agentName := "cd-reviewer-" + lens + "-claude"
		wg.Add(1)
		go func(name, lensGroup, p, out string) {
			defer wg.Done()
			result := runCDReviewerWithRetries(runner, name, lensGroup, p, out, config, delayFn)
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
		}(agentName, lens, prompt, outPath)
	}

	// Launch codex reviewers when available.
	if codexRunner != nil && codexOutputPaths != nil {
		for _, lens := range lensGroups {
			prompt, ok := prompts[lens]
			if !ok {
				continue
			}
			outPath, ok := codexOutputPaths[lens]
			if !ok {
				return nil, fmt.Errorf("missing codex output path for lens group %q", lens)
			}

			agentName := "cd-reviewer-" + lens + "-codex"
			wg.Add(1)
			go func(name, lensGroup, p, out string) {
				defer wg.Done()
				result := runCDReviewerWithRetries(codexRunner, name, lensGroup, p, out, config, delayFn)
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
			}(agentName, lens, prompt, outPath)
		}
	}

	wg.Wait()

	totalReviewers := len(results) + len(failures)
	maxFailuresAllowed := totalReviewers/2 - 1
	if maxFailuresAllowed < 1 {
		maxFailuresAllowed = 1
	}

	dispatchResult := &CDReviewDispatchResult{
		Results:  results,
		Failures: failures,
	}

	for _, r := range results {
		dispatchResult.TotalCostUSD += r.CostUSD
	}
	for _, r := range failures {
		dispatchResult.TotalCostUSD += r.CostUSD
	}
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

	if len(failures) > 0 {
		dispatchResult.ReducedCoverage = true
		for _, f := range failures {
			dispatchResult.CoverageLoss = append(dispatchResult.CoverageLoss, f.AgentName)
		}
	}

	if len(failures) > 0 && len(failures) <= maxFailuresAllowed {
		log.Printf("WARNING: %d codedoc reviewer(s) failed; proceeding with reduced coverage (lost: %v)",
			len(failures), dispatchResult.CoverageLoss)
	}

	if len(failures) > maxFailuresAllowed {
		agentNames := make([]string, len(failures))
		for i, f := range failures {
			agentNames[i] = f.AgentName
		}
		return dispatchResult, fmt.Errorf(
			"codedoc review dispatch failed: %d/%d reviewers failed (failed: %v); escalation required",
			len(failures), totalReviewers, agentNames,
		)
	}

	return dispatchResult, nil
}

// ---------------------------------------------------------------------------
// Per-reviewer retry loop
// ---------------------------------------------------------------------------

func runCDReviewerWithRetries(
	runner CDAgentRunner,
	agentName string,
	lensGroup string,
	prompt string,
	outputPath string,
	config CDReviewDispatchConfig,
	delayFn CDDelayFunc,
) CDReviewerResult {
	result := CDReviewerResult{
		LensGroup: lensGroup,
		AgentName: agentName,
	}

	for attempt := 0; attempt <= config.MaxRetries; attempt++ {
		if attempt > 0 {
			delay := time.Duration(attempt) * 2 * time.Second
			delayFn(delay)
		}

		exitCode, stderr, costUSD, durationMS, runErr := runner.Run(prompt, outputPath, config.TimeoutSeconds)
		result.CostUSD += costUSD
		result.DurationMS += durationMS

		if runErr != nil {
			result.Error = fmt.Errorf("agent %s: runner error (attempt %d/%d): %w", agentName, attempt+1, config.MaxRetries+1, runErr)
			continue
		}

		if exitCode != 0 {
			result.Error = fmt.Errorf("agent %s: non-zero exit code %d (attempt %d/%d): %s", agentName, exitCode, attempt+1, config.MaxRetries+1, stderr)
			continue
		}

		data, readErr := os.ReadFile(outputPath)
		if readErr != nil {
			result.Error = fmt.Errorf("agent %s: missing output (attempt %d/%d): %w", agentName, attempt+1, config.MaxRetries+1, readErr)
			continue
		}

		var output ReviewerOutput
		if jsonErr := json.Unmarshal(data, &output); jsonErr != nil {
			result.Error = fmt.Errorf("agent %s: invalid JSON (attempt %d/%d): %w", agentName, attempt+1, config.MaxRetries+1, jsonErr)
			continue
		}

		validFindings, rejectedCount, validationErrs := ValidateReviewerOutput(&output)
		if len(validFindings) == 0 && len(validationErrs) > 0 {
			result.Error = fmt.Errorf("agent %s: schema violation (attempt %d/%d): %d errors: %v", agentName, attempt+1, config.MaxRetries+1, len(validationErrs), validationErrs[0])
			continue
		}

		if rejectedCount > 0 {
			log.Printf("WARNING: %s: accepted %d valid findings, rejected %d", agentName, len(validFindings), rejectedCount)
		}

		output.Findings = validFindings
		result.Error = nil
		result.Output = &output
		return result
	}

	return result
}

// ---------------------------------------------------------------------------
// Merge findings
// ---------------------------------------------------------------------------

// MergedCodedocFindings is the result of merging findings from all reviewer groups.
type MergedCodedocFindings struct {
	Round            int              `json:"round"`
	TotalFindings    int              `json:"total_findings"`
	TotalAfterDedup  int              `json:"total_after_dedup"`
	DuplicatesMerged int              `json:"duplicates_merged"`
	FindingsRejected int              `json:"findings_rejected"`
	Findings         []ReviewFinding  `json:"findings"`
	DedupLog         []CDDedupEntry   `json:"dedup_log"`
}

// CDDedupEntry records one dedup merge event.
type CDDedupEntry struct {
	KeptID   string `json:"kept_id"`
	MergedID string `json:"merged_id"`
	Reason   string `json:"reason"`
}

// MergeCodedocReviewerOutputs merges findings from multiple codedoc reviewer
// outputs into a single deduplicated list. Dedup key is (lens, affected_file,
// affected_section). When duplicates are found, the higher severity is kept.
func MergeCodedocReviewerOutputs(outputs []*ReviewerOutput, round int) (*MergedCodedocFindings, error) {
	if len(outputs) == 0 {
		return nil, fmt.Errorf("MergeCodedocReviewerOutputs: at least one ReviewerOutput is required")
	}

	type candidate struct {
		finding   ReviewFinding
		sourceIDs []string
		raisedBy  []string
	}

	var totalRejected int
	var totalRaw int
	var candidates []candidate
	var dedupLog []CDDedupEntry

	severityRank := map[string]int{
		SeverityCritical:    0,
		SeverityMajor:       1,
		SeverityMinor:       2,
		SeverityObservation: 3,
	}

	for _, output := range outputs {
		validFindings, rejected, _ := ValidateReviewerOutput(output)
		totalRejected += rejected
		totalRaw += len(output.Findings)
		reviewer := output.Agent

		for _, f := range validFindings {
			key := CodedocDedupKey(&f)

			dupIdx := -1
			for i := range candidates {
				if CodedocDedupKey(&candidates[i].finding) == key {
					dupIdx = i
					break
				}
			}

			if dupIdx >= 0 {
				existing := &candidates[dupIdx]

				// Promote to higher severity.
				if severityRank[f.Severity] < severityRank[existing.finding.Severity] {
					existing.finding.Severity = f.Severity
				}

				// Concatenate recommendations with attribution.
				existingRec := existing.finding.Recommendation
				if !strings.HasPrefix(existingRec, "From ") {
					existingRec = fmt.Sprintf("From %s: %s", existing.raisedBy[0], existingRec)
				}
				existing.finding.Recommendation = existingRec + "\n" + fmt.Sprintf("From %s: %s", reviewer, f.Recommendation)

				existing.sourceIDs = append(existing.sourceIDs, f.ID)
				existing.raisedBy = append(existing.raisedBy, reviewer)

				dedupLog = append(dedupLog, CDDedupEntry{
					KeptID:   existing.sourceIDs[0],
					MergedID: f.ID,
					Reason: fmt.Sprintf("duplicate: lens=%q, affected_file=%q, affected_section=%q",
						strings.ToLower(strings.TrimSpace(f.Lens)),
						strings.ToLower(strings.TrimSpace(f.AffectedFile)),
						strings.ToLower(strings.TrimSpace(f.AffectedSection))),
				})
			} else {
				candidates = append(candidates, candidate{
					finding:   f,
					sourceIDs: []string{f.ID},
					raisedBy:  []string{reviewer},
				})
			}
		}
	}

	// Build merged findings list.
	merged := make([]ReviewFinding, len(candidates))
	for i, c := range candidates {
		merged[i] = c.finding
		if merged[i].Status == "" {
			merged[i].Status = "open"
		}
	}

	duplicatesMerged := totalRaw - totalRejected - len(candidates)
	if duplicatesMerged < 0 {
		duplicatesMerged = 0
	}

	return &MergedCodedocFindings{
		Round:            round,
		TotalFindings:    totalRaw,
		TotalAfterDedup:  len(merged),
		DuplicatesMerged: duplicatesMerged,
		FindingsRejected: totalRejected,
		Findings:         merged,
		DedupLog:         dedupLog,
	}, nil
}
