package codedoc

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
// Mock CDAgentRunner
// ---------------------------------------------------------------------------

type mockCDRunCall struct {
	Prompt         string
	OutputPath     string
	TimeoutSeconds int
}

type mockCDAgentRunner struct {
	handler   func(call mockCDRunCall) (int, string, float64, int64, error)
	callCount atomic.Int64
}

func (m *mockCDAgentRunner) Run(prompt string, outputPath string, timeoutSeconds int) (int, string, float64, int64, error) {
	m.callCount.Add(1)
	return m.handler(mockCDRunCall{Prompt: prompt, OutputPath: outputPath, TimeoutSeconds: timeoutSeconds})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func noopCDDelay(_ time.Duration) {}

func validCDReviewerOutputJSON(lensGroup string) []byte {
	lenses := LensCodesForGroup(lensGroup)
	if lenses == nil {
		lenses = []string{lensGroup}
	}
	output := ReviewerOutput{
		SchemaVersion: "1.0",
		Agent:         "cd-reviewer-" + lensGroup,
		Round:         1,
		LensesApplied: lenses,
		Findings: []ReviewFinding{
			{
				ID:              lenses[0] + "-001",
				Description:     "Test finding for " + lensGroup,
				Severity:        SeverityMinor,
				Status:          "open",
				Impact:          "Low impact",
				Recommendation:  "Fix the thing",
				Lens:            lenses[0],
				AffectedSection: "module-overview",
				AffectedFile:    "docs/as-implemented-report.md",
			},
		},
		StructuralIntegrity: StructuralIntegrity{Performed: true},
	}
	data, err := json.Marshal(output)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal test output: %v", err))
	}
	return data
}

func makeCDPrompts() map[string]string {
	m := make(map[string]string)
	for _, g := range CodedocLensGroups {
		m[g] = BuildReviewerPrompt(g, "/drafts", 1)
	}
	return m
}

func makeCDOutputPaths(dir string) map[string]string {
	m := make(map[string]string)
	for _, g := range CodedocLensGroups {
		letter := ReviewerGroupLetter(g)
		m[g] = filepath.Join(dir, fmt.Sprintf("review-%s-round-1.json", letter))
	}
	return m
}

// ---------------------------------------------------------------------------
// DispatchCodedocReviewers — all succeed
// ---------------------------------------------------------------------------

func TestReviewDispatchAllSucceed(t *testing.T) {
	dir := t.TempDir()
	paths := makeCDOutputPaths(dir)

	runner := &mockCDAgentRunner{
		handler: func(call mockCDRunCall) (int, string, float64, int64, error) {
			// Determine lens group from output path.
			for g, p := range paths {
				if call.OutputPath == p {
					os.WriteFile(call.OutputPath, validCDReviewerOutputJSON(g), 0o644)
					return 0, "", 0.01, 100, nil
				}
			}
			return 1, "unknown path", 0, 0, nil
		},
	}

	result, err := DispatchCodedocReviewers(
		runner, nil, CodedocLensGroups,
		makeCDPrompts(), paths, nil,
		CDReviewDispatchConfig{MaxRetries: 0, TimeoutSeconds: 30},
		noopCDDelay, nil,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Results) != 4 {
		t.Errorf("expected 4 results, got %d", len(result.Results))
	}
	if len(result.Failures) != 0 {
		t.Errorf("expected 0 failures, got %d", len(result.Failures))
	}
	if result.ReducedCoverage {
		t.Error("expected no reduced coverage")
	}
}

// ---------------------------------------------------------------------------
// DispatchCodedocReviewers — one failure, 3 succeed (reduced coverage)
// ---------------------------------------------------------------------------

func TestReviewDispatchOneFailureReducedCoverage(t *testing.T) {
	dir := t.TempDir()
	paths := makeCDOutputPaths(dir)

	failGroup := CodedocLensGroups[0] // accuracy fails
	runner := &mockCDAgentRunner{
		handler: func(call mockCDRunCall) (int, string, float64, int64, error) {
			for g, p := range paths {
				if call.OutputPath == p {
					if g == failGroup {
						return 1, "agent crashed", 0.01, 50, nil
					}
					os.WriteFile(call.OutputPath, validCDReviewerOutputJSON(g), 0o644)
					return 0, "", 0.01, 100, nil
				}
			}
			return 1, "unknown", 0, 0, nil
		},
	}

	result, err := DispatchCodedocReviewers(
		runner, nil, CodedocLensGroups,
		makeCDPrompts(), paths, nil,
		CDReviewDispatchConfig{MaxRetries: 0, TimeoutSeconds: 30},
		noopCDDelay, nil,
	)
	if err != nil {
		t.Fatalf("unexpected error (1 failure should be tolerated): %v", err)
	}
	if len(result.Results) != 3 {
		t.Errorf("expected 3 results, got %d", len(result.Results))
	}
	if len(result.Failures) != 1 {
		t.Errorf("expected 1 failure, got %d", len(result.Failures))
	}
	if !result.ReducedCoverage {
		t.Error("expected reduced coverage")
	}
}

// ---------------------------------------------------------------------------
// DispatchCodedocReviewers — majority failure triggers error
// ---------------------------------------------------------------------------

func TestReviewDispatchMajorityFailureReturnsError(t *testing.T) {
	dir := t.TempDir()
	paths := makeCDOutputPaths(dir)

	runner := &mockCDAgentRunner{
		handler: func(call mockCDRunCall) (int, string, float64, int64, error) {
			// Only completeness succeeds
			for g, p := range paths {
				if call.OutputPath == p && g == "completeness" {
					os.WriteFile(call.OutputPath, validCDReviewerOutputJSON(g), 0o644)
					return 0, "", 0.01, 100, nil
				}
			}
			return 1, "crash", 0.01, 50, nil
		},
	}

	_, err := DispatchCodedocReviewers(
		runner, nil, CodedocLensGroups,
		makeCDPrompts(), paths, nil,
		CDReviewDispatchConfig{MaxRetries: 0, TimeoutSeconds: 30},
		noopCDDelay, nil,
	)
	if err == nil {
		t.Fatal("expected error when majority of reviewers fail")
	}
}

// ---------------------------------------------------------------------------
// DispatchCodedocReviewers — dual provider (codex)
// ---------------------------------------------------------------------------

func TestReviewDispatchDualProvider(t *testing.T) {
	dir := t.TempDir()
	paths := makeCDOutputPaths(dir)
	codexPaths := make(map[string]string)
	for _, g := range CodedocLensGroups {
		letter := ReviewerGroupLetter(g)
		codexPaths[g] = filepath.Join(dir, fmt.Sprintf("review-%s-codex-round-1.json", letter))
	}

	allPaths := make(map[string]string)
	for k, v := range paths {
		allPaths[k] = v
	}
	for k, v := range codexPaths {
		allPaths[k+"-codex"] = v
	}

	makeHandler := func(pathMap map[string]string) func(mockCDRunCall) (int, string, float64, int64, error) {
		return func(call mockCDRunCall) (int, string, float64, int64, error) {
			for g, p := range pathMap {
				if call.OutputPath == p {
					os.WriteFile(call.OutputPath, validCDReviewerOutputJSON(g), 0o644)
					return 0, "", 0.01, 100, nil
				}
			}
			return 1, "unknown path", 0, 0, nil
		}
	}

	claudeRunner := &mockCDAgentRunner{handler: makeHandler(paths)}
	codexRunner := &mockCDAgentRunner{handler: makeHandler(codexPaths)}

	result, err := DispatchCodedocReviewers(
		claudeRunner, codexRunner, CodedocLensGroups,
		makeCDPrompts(), paths, codexPaths,
		CDReviewDispatchConfig{MaxRetries: 0, TimeoutSeconds: 30},
		noopCDDelay, nil,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 4 claude + 4 codex = 8 total
	if len(result.Results) != 8 {
		t.Errorf("expected 8 results (4 claude + 4 codex), got %d", len(result.Results))
	}
}

// ---------------------------------------------------------------------------
// DispatchCodedocReviewers — retry on failure
// ---------------------------------------------------------------------------

func TestReviewDispatchRetrySucceeds(t *testing.T) {
	dir := t.TempDir()
	paths := makeCDOutputPaths(dir)

	var attemptCounts sync.Map
	runner := &mockCDAgentRunner{
		handler: func(call mockCDRunCall) (int, string, float64, int64, error) {
			for g, p := range paths {
				if call.OutputPath == p {
					key := g
					val, _ := attemptCounts.LoadOrStore(key, new(atomic.Int64))
					count := val.(*atomic.Int64).Add(1)
					// First attempt for accuracy fails, second succeeds.
					if g == "accuracy" && count == 1 {
						return 1, "transient error", 0.01, 50, nil
					}
					os.WriteFile(call.OutputPath, validCDReviewerOutputJSON(g), 0o644)
					return 0, "", 0.01, 100, nil
				}
			}
			return 1, "unknown", 0, 0, nil
		},
	}

	result, err := DispatchCodedocReviewers(
		runner, nil, CodedocLensGroups,
		makeCDPrompts(), paths, nil,
		CDReviewDispatchConfig{MaxRetries: 1, TimeoutSeconds: 30},
		noopCDDelay, nil,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Results) != 4 {
		t.Errorf("expected 4 results after retry, got %d", len(result.Results))
	}
}

// ---------------------------------------------------------------------------
// DispatchCodedocReviewers — onComplete callback
// ---------------------------------------------------------------------------

func TestReviewDispatchOnCompleteCallback(t *testing.T) {
	dir := t.TempDir()
	paths := makeCDOutputPaths(dir)

	runner := &mockCDAgentRunner{
		handler: func(call mockCDRunCall) (int, string, float64, int64, error) {
			for g, p := range paths {
				if call.OutputPath == p {
					os.WriteFile(call.OutputPath, validCDReviewerOutputJSON(g), 0o644)
					return 0, "", 0.01, 100, nil
				}
			}
			return 1, "unknown", 0, 0, nil
		},
	}

	var callbackCount atomic.Int64
	onComplete := func(result CDReviewerResult) {
		callbackCount.Add(1)
	}

	_, err := DispatchCodedocReviewers(
		runner, nil, CodedocLensGroups,
		makeCDPrompts(), paths, nil,
		CDReviewDispatchConfig{MaxRetries: 0, TimeoutSeconds: 30},
		noopCDDelay, onComplete,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callbackCount.Load() != 4 {
		t.Errorf("expected 4 onComplete callbacks, got %d", callbackCount.Load())
	}
}

// ---------------------------------------------------------------------------
// DispatchCodedocReviewers — missing prompt returns error
// ---------------------------------------------------------------------------

func TestReviewDispatchMissingPromptReturnsError(t *testing.T) {
	dir := t.TempDir()
	paths := makeCDOutputPaths(dir)

	prompts := makeCDPrompts()
	delete(prompts, "accuracy") // remove one prompt

	runner := &mockCDAgentRunner{
		handler: func(call mockCDRunCall) (int, string, float64, int64, error) {
			return 0, "", 0, 0, nil
		},
	}

	_, err := DispatchCodedocReviewers(
		runner, nil, CodedocLensGroups,
		prompts, paths, nil,
		CDReviewDispatchConfig{MaxRetries: 0, TimeoutSeconds: 30},
		noopCDDelay, nil,
	)
	if err == nil {
		t.Fatal("expected error for missing prompt")
	}
}

// ---------------------------------------------------------------------------
// CodedocDedupKey
// ---------------------------------------------------------------------------

func TestReviewDispatchDedupKeySameInputsSameKey(t *testing.T) {
	f1 := &ReviewFinding{Lens: "ACC", AffectedFile: "docs/report.md", AffectedSection: "Module Overview"}
	f2 := &ReviewFinding{Lens: "ACC", AffectedFile: "docs/report.md", AffectedSection: "Module Overview"}
	if CodedocDedupKey(f1) != CodedocDedupKey(f2) {
		t.Error("same inputs should produce same key")
	}
}

func TestReviewDispatchDedupKeyCaseInsensitive(t *testing.T) {
	f1 := &ReviewFinding{Lens: "ACC", AffectedFile: "Docs/Report.md", AffectedSection: "Module Overview"}
	f2 := &ReviewFinding{Lens: "acc", AffectedFile: "docs/report.md", AffectedSection: "module overview"}
	if CodedocDedupKey(f1) != CodedocDedupKey(f2) {
		t.Error("keys should match case-insensitively")
	}
}

func TestReviewDispatchDedupKeyDifferentLensDistinct(t *testing.T) {
	f1 := &ReviewFinding{Lens: "ACC", AffectedFile: "docs/report.md", AffectedSection: "overview"}
	f2 := &ReviewFinding{Lens: "CMP", AffectedFile: "docs/report.md", AffectedSection: "overview"}
	if CodedocDedupKey(f1) == CodedocDedupKey(f2) {
		t.Error("different lenses should produce different keys")
	}
}

func TestReviewDispatchDedupKeyDifferentFileDistinct(t *testing.T) {
	f1 := &ReviewFinding{Lens: "ACC", AffectedFile: "docs/report.md", AffectedSection: "overview"}
	f2 := &ReviewFinding{Lens: "ACC", AffectedFile: "docs/audit.md", AffectedSection: "overview"}
	if CodedocDedupKey(f1) == CodedocDedupKey(f2) {
		t.Error("different files should produce different keys")
	}
}

// ---------------------------------------------------------------------------
// MergeCodedocReviewerOutputs
// ---------------------------------------------------------------------------

func TestReviewDispatchMergeNoDuplicates(t *testing.T) {
	o1 := &ReviewerOutput{
		SchemaVersion: "1.0", Agent: "reviewer-a", Round: 1,
		LensesApplied: []string{"ACC"},
		Findings: []ReviewFinding{
			{ID: "ACC-001", Description: "Accuracy issue", Severity: SeverityMajor, Status: "open",
				Impact: "high", Recommendation: "Fix it", Lens: "ACC",
				AffectedSection: "overview", AffectedFile: "docs/report.md"},
		},
	}
	o2 := &ReviewerOutput{
		SchemaVersion: "1.0", Agent: "reviewer-b", Round: 1,
		LensesApplied: []string{"CMP"},
		Findings: []ReviewFinding{
			{ID: "CMP-001", Description: "Completeness issue", Severity: SeverityMinor, Status: "open",
				Impact: "low", Recommendation: "Add docs", Lens: "CMP",
				AffectedSection: "api", AffectedFile: "docs/report.md"},
		},
	}

	merged, err := MergeCodedocReviewerOutputs([]*ReviewerOutput{o1, o2}, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if merged.TotalAfterDedup != 2 {
		t.Errorf("expected 2 findings after dedup, got %d", merged.TotalAfterDedup)
	}
	if merged.DuplicatesMerged != 0 {
		t.Errorf("expected 0 duplicates merged, got %d", merged.DuplicatesMerged)
	}
}

func TestReviewDispatchMergeDeduplicates(t *testing.T) {
	o1 := &ReviewerOutput{
		SchemaVersion: "1.0", Agent: "reviewer-claude", Round: 1,
		LensesApplied: []string{"ACC"},
		Findings: []ReviewFinding{
			{ID: "ACC-001", Description: "Wrong flow", Severity: SeverityMajor, Status: "open",
				Impact: "high", Recommendation: "Fix flow direction", Lens: "ACC",
				AffectedSection: "data-flow", AffectedFile: "docs/architecture/data-flows.md"},
		},
	}
	o2 := &ReviewerOutput{
		SchemaVersion: "1.0", Agent: "reviewer-codex", Round: 1,
		LensesApplied: []string{"ACC"},
		Findings: []ReviewFinding{
			{ID: "ACC-001", Description: "Incorrect data flow", Severity: SeverityCritical, Status: "open",
				Impact: "critical", Recommendation: "Reverse the arrow", Lens: "ACC",
				AffectedSection: "data-flow", AffectedFile: "docs/architecture/data-flows.md"},
		},
	}

	merged, err := MergeCodedocReviewerOutputs([]*ReviewerOutput{o1, o2}, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if merged.TotalAfterDedup != 1 {
		t.Errorf("expected 1 finding after dedup, got %d", merged.TotalAfterDedup)
	}
	if merged.DuplicatesMerged != 1 {
		t.Errorf("expected 1 duplicate merged, got %d", merged.DuplicatesMerged)
	}
	// Should promote to higher severity (critical).
	if merged.Findings[0].Severity != SeverityCritical {
		t.Errorf("expected severity promoted to critical, got %q", merged.Findings[0].Severity)
	}
	if len(merged.DedupLog) != 1 {
		t.Errorf("expected 1 dedup log entry, got %d", len(merged.DedupLog))
	}
}

func TestReviewDispatchMergeAllOpenStatus(t *testing.T) {
	o := &ReviewerOutput{
		SchemaVersion: "1.0", Agent: "reviewer", Round: 1,
		LensesApplied: []string{"ARC"},
		Findings: []ReviewFinding{
			{ID: "ARC-001", Description: "Diagram wrong", Severity: SeverityMajor, Status: "",
				Impact: "high", Recommendation: "Fix diagram", Lens: "ARC",
				AffectedSection: "arch", AffectedFile: "docs/architecture/module-deps.md"},
		},
	}

	merged, err := MergeCodedocReviewerOutputs([]*ReviewerOutput{o}, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, f := range merged.Findings {
		if f.Status != "open" {
			t.Errorf("expected status 'open', got %q", f.Status)
		}
	}
}

func TestReviewDispatchMergeEmptyReturnsError(t *testing.T) {
	_, err := MergeCodedocReviewerOutputs(nil, 1)
	if err == nil {
		t.Fatal("expected error for empty outputs")
	}
}

func TestReviewDispatchMergeRoundIsSet(t *testing.T) {
	o := &ReviewerOutput{
		SchemaVersion: "1.0", Agent: "reviewer", Round: 3,
		LensesApplied: []string{"SEC"},
		Findings: []ReviewFinding{
			{ID: "SEC-001", Description: "Secret found", Severity: SeverityCritical, Status: "open",
				Impact: "critical", Recommendation: "Remove it", Lens: "SEC",
				AffectedSection: "config", AffectedFile: "docs/report.md"},
		},
	}
	merged, err := MergeCodedocReviewerOutputs([]*ReviewerOutput{o}, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if merged.Round != 3 {
		t.Errorf("expected round 3, got %d", merged.Round)
	}
}

