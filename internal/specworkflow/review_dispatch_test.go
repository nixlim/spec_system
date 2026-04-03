package specworkflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Mock AgentRunner
// ---------------------------------------------------------------------------

// mockRunCall captures parameters passed to a single Run invocation.
type mockRunCall struct {
	Prompt         string
	OutputPath     string
	TimeoutSeconds int
}

// mockAgentRunner is a configurable mock implementation of AgentRunner.
// It can be set up to return different results per lens group, per attempt,
// and optionally write output files to disk.
type mockAgentRunner struct {
	// handler is called for each Run invocation. It receives the call
	// details and returns the desired result. The handler is responsible
	// for writing output files when simulating success.
	handler func(call mockRunCall) (exitCode int, stderr string, costUSD float64, durationMS int64, err error)

	// callCount tracks the total number of Run calls (thread-safe).
	callCount atomic.Int64
}

// Run implements AgentRunner.
func (m *mockAgentRunner) Run(prompt string, outputPath string, timeoutSeconds int) (int, string, float64, int64, error) {
	m.callCount.Add(1)
	return m.handler(mockRunCall{
		Prompt:         prompt,
		OutputPath:     outputPath,
		TimeoutSeconds: timeoutSeconds,
	})
}

// sync is imported as a side-effect of atomic; import the real sync for Map.
// (sync.Map is in the sync package which is already imported via atomic's usage.)

// ---------------------------------------------------------------------------
// Helper: valid reviewer output JSON
// ---------------------------------------------------------------------------

func validReviewerOutputJSON(lensGroup string) []byte {
	output := ReviewerOutput{
		SchemaVersion: "1.0",
		Agent:         "reviewer-" + lensGroup,
		Round:         1,
		LensesApplied: []string{lensGroup},
		Findings: []Finding{
			{
				ID:              "F-001",
				Description:     "Test finding for " + lensGroup,
				Severity:        SeverityMinor,
				Impact:          "Low impact",
				Recommendation:  "Fix the thing",
				Lens:            lensGroup,
				AffectedSection: "1.0",
			},
		},
		StructuralIntegrity: StructuralIntegrity{
			Performed: true,
			Checks: []IntegrityCheck{
				{Check: "basic", Result: "PASS"},
			},
		},
		MarkdownReportFile: "report-" + lensGroup + ".md",
	}
	data, err := json.Marshal(output)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal test output: %v", err))
	}
	return data
}

// noopDelay is a DelayFunc that does not sleep.
func noopDelay(_ time.Duration) {}

// makePrompts returns a standard prompts map for all 4 lens groups.
func makePrompts() map[string]string {
	return map[string]string{
		"clarity":     "Review for clarity",
		"consistency": "Review for consistency",
		"security":    "Review for security",
		"correctness": "Review for correctness",
	}
}

// makeOutputPaths returns output paths in the given temp directory.
func makeOutputPaths(dir string) map[string]string {
	return map[string]string{
		"clarity":     filepath.Join(dir, "clarity.json"),
		"consistency": filepath.Join(dir, "consistency.json"),
		"security":    filepath.Join(dir, "security.json"),
		"correctness": filepath.Join(dir, "correctness.json"),
	}
}

// lensFromPath extracts the lens group name from an output path.
func lensFromPath(dir, path string) string {
	for _, lens := range []string{"clarity", "consistency", "security", "correctness"} {
		if path == filepath.Join(dir, lens+".json") {
			return lens
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestReviewDispatch_AllSucceed(t *testing.T) {
	dir := t.TempDir()
	outputPaths := makeOutputPaths(dir)

	runner := &mockAgentRunner{
		handler: func(call mockRunCall) (int, string, float64, int64, error) {
			lens := lensFromPath(dir, call.OutputPath)
			if err := os.WriteFile(call.OutputPath, validReviewerOutputJSON(lens), 0644); err != nil {
				t.Fatalf("failed to write mock output: %v", err)
			}
			return 0, "", 0.05, 1000, nil
		},
	}

	config := ReviewDispatchConfig{MaxRetries: 2, TimeoutSeconds: 30}
	result, err := DispatchReviewers(runner, nil, SpecReviewerLensGroups(), makePrompts(), outputPaths, nil, config, noopDelay, nil)
	if err != nil {
		t.Fatalf("DispatchReviewers returned unexpected error: %v", err)
	}

	if len(result.Results) != 4 {
		t.Errorf("expected 4 successful results, got %d", len(result.Results))
	}
	if len(result.Failures) != 0 {
		t.Errorf("expected 0 failures, got %d", len(result.Failures))
	}
	if result.ReducedCoverage {
		t.Error("expected ReducedCoverage=false when all 4 succeed")
	}
	if len(result.CoverageLoss) != 0 {
		t.Errorf("expected empty CoverageLoss, got %v", result.CoverageLoss)
	}

	// Verify each result has a parsed output.
	for _, r := range result.Results {
		if r.Output == nil {
			t.Errorf("result for %q has nil Output", r.LensGroup)
		}
		if r.Error != nil {
			t.Errorf("result for %q has non-nil Error: %v", r.LensGroup, r.Error)
		}
	}
}

func TestReviewDispatch_OneFailsThenSucceedsOnRetry(t *testing.T) {
	dir := t.TempDir()
	outputPaths := makeOutputPaths(dir)

	// Track per-lens attempt counts.
	var attemptCounts sync.Map

	runner := &mockAgentRunner{
		handler: func(call mockRunCall) (int, string, float64, int64, error) {
			lens := lensFromPath(dir, call.OutputPath)

			// Increment attempt count for this lens.
			val, _ := attemptCounts.LoadOrStore(lens, new(atomic.Int64))
			counter := val.(*atomic.Int64)
			attempt := counter.Add(1)

			// Security fails on first attempt, succeeds on second.
			if lens == "security" && attempt == 1 {
				return 1, "crash: segfault", 0.02, 500, nil
			}

			if err := os.WriteFile(call.OutputPath, validReviewerOutputJSON(lens), 0644); err != nil {
				t.Fatalf("failed to write mock output: %v", err)
			}
			return 0, "", 0.05, 1000, nil
		},
	}

	config := ReviewDispatchConfig{MaxRetries: 2, TimeoutSeconds: 30}
	result, err := DispatchReviewers(runner, nil, SpecReviewerLensGroups(), makePrompts(), outputPaths, nil, config, noopDelay, nil)
	if err != nil {
		t.Fatalf("DispatchReviewers returned unexpected error: %v", err)
	}

	if len(result.Results) != 4 {
		t.Errorf("expected 4 successful results after retry, got %d", len(result.Results))
	}
	if len(result.Failures) != 0 {
		t.Errorf("expected 0 failures after successful retry, got %d", len(result.Failures))
	}
	if result.ReducedCoverage {
		t.Error("expected ReducedCoverage=false when all eventually succeed")
	}

	// Verify security reviewer accumulated cost from both attempts.
	for _, r := range result.Results {
		if r.LensGroup == "security" {
			// First attempt: 0.02 USD, second: 0.05 USD = 0.07 total.
			expectedCost := 0.07
			if r.CostUSD < expectedCost-0.001 || r.CostUSD > expectedCost+0.001 {
				t.Errorf("security reviewer cost = %f, want ~%f", r.CostUSD, expectedCost)
			}
			// Duration: 500 + 1000 = 1500.
			if r.DurationMS != 1500 {
				t.Errorf("security reviewer duration = %d, want 1500", r.DurationMS)
			}
		}
	}
}

func TestReviewDispatch_OneFailsAfterAllRetries(t *testing.T) {
	dir := t.TempDir()
	outputPaths := makeOutputPaths(dir)

	runner := &mockAgentRunner{
		handler: func(call mockRunCall) (int, string, float64, int64, error) {
			lens := lensFromPath(dir, call.OutputPath)

			// Consistency always fails.
			if lens == "consistency" {
				return 1, "crash: out of memory", 0.01, 200, nil
			}

			if err := os.WriteFile(call.OutputPath, validReviewerOutputJSON(lens), 0644); err != nil {
				t.Fatalf("failed to write mock output: %v", err)
			}
			return 0, "", 0.05, 1000, nil
		},
	}

	config := ReviewDispatchConfig{MaxRetries: 2, TimeoutSeconds: 30}
	result, err := DispatchReviewers(runner, nil, SpecReviewerLensGroups(), makePrompts(), outputPaths, nil, config, noopDelay, nil)
	if err != nil {
		t.Fatalf("DispatchReviewers should not return error for 1 failure, got: %v", err)
	}

	if len(result.Results) != 3 {
		t.Errorf("expected 3 successful results, got %d", len(result.Results))
	}
	if len(result.Failures) != 1 {
		t.Errorf("expected 1 failure, got %d", len(result.Failures))
	}
	if !result.ReducedCoverage {
		t.Error("expected ReducedCoverage=true when 1 reviewer failed")
	}
	if len(result.CoverageLoss) != 1 || result.CoverageLoss[0] != "reviewer-consistency-claude" {
		t.Errorf("expected CoverageLoss=[reviewer-consistency-claude], got %v", result.CoverageLoss)
	}

	// Verify failure has the right lens group and error.
	if result.Failures[0].LensGroup != "consistency" {
		t.Errorf("failure lens = %q, want consistency", result.Failures[0].LensGroup)
	}
	if result.Failures[0].Error == nil {
		t.Error("failure should have non-nil Error")
	}

	// Verify cost accumulates attempts: initial + 2 retries = 3 * 0.01.
	failCost := result.Failures[0].CostUSD
	expectedFailCost := 0.03
	if failCost < expectedFailCost-0.001 || failCost > expectedFailCost+0.001 {
		t.Errorf("failure cost = %f, want ~%f", failCost, expectedFailCost)
	}
}

func TestReviewDispatch_TwoFailAfterAllRetries_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	outputPaths := makeOutputPaths(dir)

	runner := &mockAgentRunner{
		handler: func(call mockRunCall) (int, string, float64, int64, error) {
			lens := lensFromPath(dir, call.OutputPath)

			// Security and correctness always fail.
			if lens == "security" || lens == "correctness" {
				return 1, "crash: fatal error", 0.01, 200, nil
			}

			if err := os.WriteFile(call.OutputPath, validReviewerOutputJSON(lens), 0644); err != nil {
				t.Fatalf("failed to write mock output: %v", err)
			}
			return 0, "", 0.05, 1000, nil
		},
	}

	config := ReviewDispatchConfig{MaxRetries: 1, TimeoutSeconds: 30}
	result, err := DispatchReviewers(runner, nil, SpecReviewerLensGroups(), makePrompts(), outputPaths, nil, config, noopDelay, nil)
	if err == nil {
		t.Fatal("expected error when 2+ reviewers fail, got nil")
	}

	// Result should still be returned with partial data.
	if result == nil {
		t.Fatal("expected non-nil result even on error")
	}
	if len(result.Results) != 2 {
		t.Errorf("expected 2 successful results, got %d", len(result.Results))
	}
	if len(result.Failures) != 2 {
		t.Errorf("expected 2 failures, got %d", len(result.Failures))
	}
	if !result.ReducedCoverage {
		t.Error("expected ReducedCoverage=true")
	}
}

func TestReviewDispatch_InvalidJSONTriggersRetry(t *testing.T) {
	dir := t.TempDir()
	outputPaths := makeOutputPaths(dir)

	var attemptCounts sync.Map

	runner := &mockAgentRunner{
		handler: func(call mockRunCall) (int, string, float64, int64, error) {
			lens := lensFromPath(dir, call.OutputPath)

			val, _ := attemptCounts.LoadOrStore(lens, new(atomic.Int64))
			counter := val.(*atomic.Int64)
			attempt := counter.Add(1)

			// Clarity writes invalid JSON on first attempt.
			if lens == "clarity" && attempt == 1 {
				if err := os.WriteFile(call.OutputPath, []byte("{not valid json!!!"), 0644); err != nil {
					t.Fatalf("failed to write mock output: %v", err)
				}
				return 0, "", 0.02, 300, nil
			}

			if err := os.WriteFile(call.OutputPath, validReviewerOutputJSON(lens), 0644); err != nil {
				t.Fatalf("failed to write mock output: %v", err)
			}
			return 0, "", 0.05, 1000, nil
		},
	}

	config := ReviewDispatchConfig{MaxRetries: 2, TimeoutSeconds: 30}
	result, err := DispatchReviewers(runner, nil, SpecReviewerLensGroups(), makePrompts(), outputPaths, nil, config, noopDelay, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Results) != 4 {
		t.Errorf("expected 4 results after invalid JSON retry, got %d", len(result.Results))
	}

	// Verify clarity had extra cost from the failed attempt.
	for _, r := range result.Results {
		if r.LensGroup == "clarity" {
			expectedCost := 0.07 // 0.02 + 0.05
			if r.CostUSD < expectedCost-0.001 || r.CostUSD > expectedCost+0.001 {
				t.Errorf("clarity cost = %f, want ~%f", r.CostUSD, expectedCost)
			}
		}
	}
}

func TestReviewDispatch_CostAndDurationAccumulation(t *testing.T) {
	dir := t.TempDir()
	outputPaths := makeOutputPaths(dir)

	runner := &mockAgentRunner{
		handler: func(call mockRunCall) (int, string, float64, int64, error) {
			lens := lensFromPath(dir, call.OutputPath)
			if err := os.WriteFile(call.OutputPath, validReviewerOutputJSON(lens), 0644); err != nil {
				t.Fatalf("failed to write mock output: %v", err)
			}
			// Each reviewer costs 0.10 and takes 2000ms.
			return 0, "", 0.10, 2000, nil
		},
	}

	config := ReviewDispatchConfig{MaxRetries: 0, TimeoutSeconds: 30}
	result, err := DispatchReviewers(runner, nil, SpecReviewerLensGroups(), makePrompts(), outputPaths, nil, config, noopDelay, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Total cost = 4 * 0.10 = 0.40.
	expectedTotalCost := 0.40
	if result.TotalCostUSD < expectedTotalCost-0.001 || result.TotalCostUSD > expectedTotalCost+0.001 {
		t.Errorf("TotalCostUSD = %f, want ~%f", result.TotalCostUSD, expectedTotalCost)
	}

	// Total duration should be the max (all same = 2000).
	if result.TotalDurationMS != 2000 {
		t.Errorf("TotalDurationMS = %d, want 2000", result.TotalDurationMS)
	}
}

func TestReviewDispatch_ConcurrentExecution(t *testing.T) {
	dir := t.TempDir()
	outputPaths := makeOutputPaths(dir)

	// Track how many runners are executing concurrently.
	var activeCount atomic.Int64
	var maxConcurrent atomic.Int64

	runner := &mockAgentRunner{
		handler: func(call mockRunCall) (int, string, float64, int64, error) {
			current := activeCount.Add(1)
			defer activeCount.Add(-1)

			// Update max concurrent count.
			for {
				old := maxConcurrent.Load()
				if current <= old {
					break
				}
				if maxConcurrent.CompareAndSwap(old, current) {
					break
				}
			}

			// Small sleep to ensure overlap of goroutines.
			time.Sleep(50 * time.Millisecond)

			lens := lensFromPath(dir, call.OutputPath)
			if err := os.WriteFile(call.OutputPath, validReviewerOutputJSON(lens), 0644); err != nil {
				t.Errorf("failed to write mock output: %v", err)
			}
			return 0, "", 0.05, 1000, nil
		},
	}

	config := ReviewDispatchConfig{MaxRetries: 0, TimeoutSeconds: 30}
	result, err := DispatchReviewers(runner, nil, SpecReviewerLensGroups(), makePrompts(), outputPaths, nil, config, noopDelay, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Results) != 4 {
		t.Errorf("expected 4 results, got %d", len(result.Results))
	}

	// All 4 should run concurrently.
	mc := maxConcurrent.Load()
	if mc < 2 {
		t.Errorf("expected concurrent execution (max concurrent >= 2), got %d", mc)
	}
}

func TestReviewDispatch_MissingPrompt(t *testing.T) {
	dir := t.TempDir()
	outputPaths := makeOutputPaths(dir)

	// Remove one prompt.
	prompts := makePrompts()
	delete(prompts, "security")

	runner := &mockAgentRunner{
		handler: func(call mockRunCall) (int, string, float64, int64, error) {
			return 0, "", 0, 0, nil
		},
	}

	config := ReviewDispatchConfig{MaxRetries: 0, TimeoutSeconds: 30}
	_, err := DispatchReviewers(runner, nil, SpecReviewerLensGroups(), prompts, outputPaths, nil, config, noopDelay, nil)
	if err == nil {
		t.Fatal("expected error when prompt is missing for a lens group")
	}
}

func TestReviewDispatch_MissingOutputPath(t *testing.T) {
	dir := t.TempDir()
	outputPaths := makeOutputPaths(dir)

	// Remove one output path.
	delete(outputPaths, "correctness")

	runner := &mockAgentRunner{
		handler: func(call mockRunCall) (int, string, float64, int64, error) {
			return 0, "", 0, 0, nil
		},
	}

	config := ReviewDispatchConfig{MaxRetries: 0, TimeoutSeconds: 30}
	_, err := DispatchReviewers(runner, nil, SpecReviewerLensGroups(), makePrompts(), outputPaths, nil, config, noopDelay, nil)
	if err == nil {
		t.Fatal("expected error when output path is missing for a lens group")
	}
}

func TestReviewDispatch_RetryDelayIsCalled(t *testing.T) {
	dir := t.TempDir()
	outputPaths := makeOutputPaths(dir)

	var attemptCounts sync.Map
	var delaysCalled atomic.Int64

	runner := &mockAgentRunner{
		handler: func(call mockRunCall) (int, string, float64, int64, error) {
			lens := lensFromPath(dir, call.OutputPath)

			val, _ := attemptCounts.LoadOrStore(lens, new(atomic.Int64))
			counter := val.(*atomic.Int64)
			attempt := counter.Add(1)

			// All lenses fail once, then succeed.
			if attempt == 1 {
				return 1, "crash", 0.01, 100, nil
			}

			if err := os.WriteFile(call.OutputPath, validReviewerOutputJSON(lens), 0644); err != nil {
				t.Fatalf("failed to write mock output: %v", err)
			}
			return 0, "", 0.05, 1000, nil
		},
	}

	trackingDelay := func(d time.Duration) {
		delaysCalled.Add(1)
	}

	config := ReviewDispatchConfig{MaxRetries: 2, TimeoutSeconds: 30}
	result, err := DispatchReviewers(runner, nil, SpecReviewerLensGroups(), makePrompts(), outputPaths, nil, config, trackingDelay, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Results) != 4 {
		t.Errorf("expected 4 results, got %d", len(result.Results))
	}

	// Each of the 4 reviewers should have called delay once (before retry).
	delays := delaysCalled.Load()
	if delays != 4 {
		t.Errorf("expected 4 delay calls (one per reviewer retry), got %d", delays)
	}
}

func TestReviewDispatch_SchemaViolationTriggersRetry(t *testing.T) {
	dir := t.TempDir()
	outputPaths := makeOutputPaths(dir)

	var attemptCounts sync.Map

	runner := &mockAgentRunner{
		handler: func(call mockRunCall) (int, string, float64, int64, error) {
			lens := lensFromPath(dir, call.OutputPath)

			val, _ := attemptCounts.LoadOrStore(lens, new(atomic.Int64))
			counter := val.(*atomic.Int64)
			attempt := counter.Add(1)

			// Security returns schema-violating output on first attempt
			// (valid JSON but missing required fields).
			if lens == "security" && attempt == 1 {
				badOutput := ReviewerOutput{
					// Missing SchemaVersion, Agent is empty, Round is 0,
					// no lenses applied, no markdown report file.
					Findings: []Finding{
						{
							ID:          "F-001",
							Description: "A finding",
							Severity:    SeverityMajor,
							// Missing Recommendation — will be rejected.
						},
					},
				}
				data, _ := json.Marshal(badOutput)
				if err := os.WriteFile(call.OutputPath, data, 0644); err != nil {
					t.Fatalf("failed to write mock output: %v", err)
				}
				return 0, "", 0.03, 400, nil
			}

			if err := os.WriteFile(call.OutputPath, validReviewerOutputJSON(lens), 0644); err != nil {
				t.Fatalf("failed to write mock output: %v", err)
			}
			return 0, "", 0.05, 1000, nil
		},
	}

	config := ReviewDispatchConfig{MaxRetries: 2, TimeoutSeconds: 30}
	result, err := DispatchReviewers(runner, nil, SpecReviewerLensGroups(), makePrompts(), outputPaths, nil, config, noopDelay, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Results) != 4 {
		t.Errorf("expected 4 results after schema violation retry, got %d", len(result.Results))
	}
	if len(result.Failures) != 0 {
		t.Errorf("expected 0 failures, got %d", len(result.Failures))
	}
}

func TestReviewDispatch_PartialFindingsAccepted(t *testing.T) {
	dir := t.TempDir()
	outputPaths := makeOutputPaths(dir)

	runner := &mockAgentRunner{
		handler: func(call mockRunCall) (int, string, float64, int64, error) {
			lens := lensFromPath(dir, call.OutputPath)

			// Security returns output with mix of valid and rejected findings.
			if lens == "security" {
				output := ReviewerOutput{
					SchemaVersion: "1.0",
					Agent:         "reviewer-security",
					Round:         1,
					LensesApplied: []string{"security"},
					Findings: []Finding{
						{
							ID:              "F-001",
							Description:     "Valid finding",
							Severity:        SeverityMajor,
							Impact:          "High impact",
							Recommendation:  "Fix this",
							Lens:            "security",
							AffectedSection: "2.0",
						},
						{
							ID:              "F-002",
							Description:     "Missing recommendation",
							Severity:        SeverityMinor,
							Impact:          "Low impact",
							Recommendation:  "", // will be rejected
							Lens:            "security",
							AffectedSection: "3.0",
						},
						{
							ID:              "F-003",
							Description:     "Another valid finding",
							Severity:        SeverityCritical,
							Impact:          "Critical impact",
							Recommendation:  "Fix urgently",
							Lens:            "security",
							AffectedSection: "4.0",
						},
					},
					StructuralIntegrity: StructuralIntegrity{
						Performed: true,
						Checks:    []IntegrityCheck{{Check: "basic", Result: "PASS"}},
					},
					MarkdownReportFile: "report-security.md",
				}
				data, _ := json.Marshal(output)
				if err := os.WriteFile(call.OutputPath, data, 0644); err != nil {
					t.Fatalf("failed to write mock output: %v", err)
				}
				return 0, "", 0.05, 1000, nil
			}

			if err := os.WriteFile(call.OutputPath, validReviewerOutputJSON(lens), 0644); err != nil {
				t.Fatalf("failed to write mock output: %v", err)
			}
			return 0, "", 0.05, 1000, nil
		},
	}

	config := ReviewDispatchConfig{MaxRetries: 2, TimeoutSeconds: 30}
	result, err := DispatchReviewers(runner, nil, SpecReviewerLensGroups(), makePrompts(), outputPaths, nil, config, noopDelay, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// All 4 should succeed — the partial findings output should NOT trigger a retry.
	if len(result.Results) != 4 {
		t.Errorf("expected 4 successful results, got %d", len(result.Results))
	}
	if len(result.Failures) != 0 {
		t.Errorf("expected 0 failures, got %d", len(result.Failures))
	}

	// Security reviewer should only have the 2 valid findings (F-002 rejected).
	for _, r := range result.Results {
		if r.LensGroup == "security" {
			if r.Output == nil {
				t.Fatal("security result has nil Output")
			}
			if len(r.Output.Findings) != 2 {
				t.Errorf("expected 2 valid findings for security, got %d", len(r.Output.Findings))
			}
			// Verify the rejected finding is not present.
			for _, f := range r.Output.Findings {
				if f.ID == "F-002" {
					t.Error("F-002 (missing recommendation) should have been rejected")
				}
			}
		}
	}

	// Should only have been called once per reviewer (no retries needed).
	totalCalls := runner.callCount.Load()
	if totalCalls != 4 {
		t.Errorf("expected 4 total runner calls (no retries), got %d", totalCalls)
	}
}

func TestReviewDispatch_RunnerInfrastructureError(t *testing.T) {
	dir := t.TempDir()
	outputPaths := makeOutputPaths(dir)

	runner := &mockAgentRunner{
		handler: func(call mockRunCall) (int, string, float64, int64, error) {
			lens := lensFromPath(dir, call.OutputPath)

			// Clarity returns a Go error (infrastructure failure).
			if lens == "clarity" {
				return 0, "", 0.01, 100, fmt.Errorf("docker daemon unavailable")
			}

			if err := os.WriteFile(call.OutputPath, validReviewerOutputJSON(lens), 0644); err != nil {
				t.Fatalf("failed to write mock output: %v", err)
			}
			return 0, "", 0.05, 1000, nil
		},
	}

	config := ReviewDispatchConfig{MaxRetries: 0, TimeoutSeconds: 30}
	result, err := DispatchReviewers(runner, nil, SpecReviewerLensGroups(), makePrompts(), outputPaths, nil, config, noopDelay, nil)
	if err != nil {
		t.Fatalf("expected no dispatch error for 1 failure, got: %v", err)
	}

	if len(result.Results) != 3 {
		t.Errorf("expected 3 results, got %d", len(result.Results))
	}
	if len(result.Failures) != 1 {
		t.Errorf("expected 1 failure, got %d", len(result.Failures))
	}
	if result.Failures[0].LensGroup != "clarity" {
		t.Errorf("expected clarity failure, got %q", result.Failures[0].LensGroup)
	}
}

// ---------------------------------------------------------------------------
// Backward compatibility tests (vse.10)
// ---------------------------------------------------------------------------

func TestDispatch_BackwardCompat_ClaudeOnly(t *testing.T) {
	dir := t.TempDir()
	outputPaths := makeOutputPaths(dir)

	runner := &mockAgentRunner{
		handler: func(call mockRunCall) (int, string, float64, int64, error) {
			lens := lensFromPath(dir, call.OutputPath)
			if err := os.WriteFile(call.OutputPath, validReviewerOutputJSON(lens), 0644); err != nil {
				t.Fatalf("failed to write mock output: %v", err)
			}
			return 0, "", 0.05, 1000, nil
		},
	}

	// nil codexRunner, nil codexOutputPaths — backward compat path.
	config := ReviewDispatchConfig{MaxRetries: 1, TimeoutSeconds: 30}
	result, err := DispatchReviewers(runner, nil, SpecReviewerLensGroups(), makePrompts(), outputPaths, nil, config, noopDelay, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Results) != 4 {
		t.Errorf("expected 4 claude-only results, got %d", len(result.Results))
	}
	if len(result.Failures) != 0 {
		t.Errorf("expected 0 failures, got %d", len(result.Failures))
	}

	// Verify all agent names have -claude suffix.
	for _, r := range result.Results {
		if r.AgentName == "" {
			t.Errorf("result for %q has empty AgentName", r.LensGroup)
		}
		expected := "reviewer-" + r.LensGroup + "-claude"
		if r.AgentName != expected {
			t.Errorf("AgentName = %q, want %q", r.AgentName, expected)
		}
	}
}

func TestMerge_BackwardCompat_FourInputs(t *testing.T) {
	// 4 claude-only inputs should merge the same as before.
	outputs := make([]*ReviewerOutput, 4)
	for i, lens := range []string{"clarity", "consistency", "security", "correctness"} {
		var out ReviewerOutput
		if err := json.Unmarshal(validReviewerOutputJSON(lens), &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		outputs[i] = &out
	}

	merged, err := MergeReviewerOutputs(outputs, 1, SpecDedupKey, true)
	if err != nil {
		t.Fatalf("merge error: %v", err)
	}
	if len(merged.Findings) != 4 {
		t.Errorf("expected 4 merged findings, got %d", len(merged.Findings))
	}
}

func TestTeamConfig_BackwardCompat_NoCodex(t *testing.T) {
	// When codex is disabled, 8 agents should be present.
	config := DefaultTeamConfig(false)
	if got := len(config.Agents); got != 8 {
		t.Errorf("expected 8 agents without codex, got %d", got)
	}

	// All agents should use claude provider.
	for _, agent := range config.Agents {
		if agent.Provider != "claude" {
			t.Errorf("agent %q: expected provider claude, got %q", agent.Name, agent.Provider)
		}
	}

	// Validation should pass.
	if err := ValidateTeamConfig(config); err != nil {
		t.Fatalf("validation failed: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Dual-provider dispatch tests (vse.8)
// ---------------------------------------------------------------------------

func TestDispatch_DualProvider_8Reviewers(t *testing.T) {
	claudeDir := t.TempDir()
	codexDir := t.TempDir()
	claudeOutputPaths := makeOutputPaths(claudeDir)
	codexOutputPaths := makeOutputPaths(codexDir)

	claudeRunner := &mockAgentRunner{
		handler: func(call mockRunCall) (int, string, float64, int64, error) {
			lens := lensFromPath(claudeDir, call.OutputPath)
			if err := os.WriteFile(call.OutputPath, validReviewerOutputJSON(lens), 0644); err != nil {
				t.Fatalf("failed to write mock output: %v", err)
			}
			return 0, "", 0.05, 1000, nil
		},
	}

	codexRunner := &mockAgentRunner{
		handler: func(call mockRunCall) (int, string, float64, int64, error) {
			lens := lensFromPath(codexDir, call.OutputPath)
			if err := os.WriteFile(call.OutputPath, validReviewerOutputJSON(lens), 0644); err != nil {
				t.Fatalf("failed to write mock output: %v", err)
			}
			return 0, "", 0.00, 2000, nil // codex reports zero cost
		},
	}

	config := ReviewDispatchConfig{MaxRetries: 1, TimeoutSeconds: 30}
	result, err := DispatchReviewers(claudeRunner, codexRunner, SpecReviewerLensGroups(), makePrompts(), claudeOutputPaths, codexOutputPaths, config, noopDelay, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 8 reviewers: 4 claude + 4 codex.
	if len(result.Results) != 8 {
		t.Errorf("expected 8 results, got %d", len(result.Results))
	}
	if len(result.Failures) != 0 {
		t.Errorf("expected 0 failures, got %d", len(result.Failures))
	}

	// Verify agent names.
	agentNames := make(map[string]bool)
	for _, r := range result.Results {
		agentNames[r.AgentName] = true
	}
	for _, lens := range []string{"clarity", "consistency", "security", "correctness"} {
		claudeName := "reviewer-" + lens + "-claude"
		codexName := "reviewer-" + lens + "-codex"
		if !agentNames[claudeName] {
			t.Errorf("missing agent %q", claudeName)
		}
		if !agentNames[codexName] {
			t.Errorf("missing agent %q", codexName)
		}
	}

	// Verify both runners were called.
	if claudeRunner.callCount.Load() != 4 {
		t.Errorf("expected 4 claude calls, got %d", claudeRunner.callCount.Load())
	}
	if codexRunner.callCount.Load() != 4 {
		t.Errorf("expected 4 codex calls, got %d", codexRunner.callCount.Load())
	}
}

func TestDispatch_DualProvider_FailureTolerance(t *testing.T) {
	claudeDir := t.TempDir()
	codexDir := t.TempDir()
	claudeOutputPaths := makeOutputPaths(claudeDir)
	codexOutputPaths := makeOutputPaths(codexDir)

	// Claude: all succeed. Codex: all fail.
	claudeRunner := &mockAgentRunner{
		handler: func(call mockRunCall) (int, string, float64, int64, error) {
			lens := lensFromPath(claudeDir, call.OutputPath)
			if err := os.WriteFile(call.OutputPath, validReviewerOutputJSON(lens), 0644); err != nil {
				t.Fatalf("failed to write mock output: %v", err)
			}
			return 0, "", 0.05, 1000, nil
		},
	}
	codexRunner := &mockAgentRunner{
		handler: func(call mockRunCall) (int, string, float64, int64, error) {
			return 1, "codex crash", 0.00, 100, nil
		},
	}

	// With 8 reviewers, maxFailuresAllowed = 8/2-1 = 3.
	// 4 codex failures > 3 → should return error.
	config := ReviewDispatchConfig{MaxRetries: 0, TimeoutSeconds: 30}
	result, err := DispatchReviewers(claudeRunner, codexRunner, SpecReviewerLensGroups(), makePrompts(), claudeOutputPaths, codexOutputPaths, config, noopDelay, nil)
	if err == nil {
		t.Fatal("expected error when 4 of 8 reviewers fail, got nil")
	}

	// Result should still have partial data.
	if result == nil {
		t.Fatal("expected non-nil result even on error")
	}
	if len(result.Results) != 4 {
		t.Errorf("expected 4 claude successes, got %d", len(result.Results))
	}
	if len(result.Failures) != 4 {
		t.Errorf("expected 4 codex failures, got %d", len(result.Failures))
	}
}
