package codedoc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// ---------------------------------------------------------------------------
// Mock runner
// ---------------------------------------------------------------------------

// mockRunner is a test AgentRunner that writes predetermined output to the
// output path and optionally returns an error.
type mockRunner struct {
	output    interface{} // will be JSON-marshaled and written to outputPath
	err       error
	cost      float64
	duration  int64
	callCount int32 // atomic
}

func (m *mockRunner) Run(prompt string, outputPath string, timeoutSeconds int) (int, string, float64, int64, error) {
	atomic.AddInt32(&m.callCount, 1)
	if m.err != nil {
		return 1, "mock error", m.cost, m.duration, m.err
	}
	data, _ := json.MarshalIndent(m.output, "", "  ")
	_ = os.WriteFile(outputPath, data, 0644)
	return 0, "", m.cost, m.duration, nil
}

func (m *mockRunner) calls() int {
	return int(atomic.LoadInt32(&m.callCount))
}

// validDiscoveryOutput returns a minimal valid DiscoveryOutput for testing.
func validDiscoveryOutput() *DiscoveryOutput {
	return &DiscoveryOutput{
		SchemaVersion:    "1.0",
		Agent:            "codedoc-discovery",
		Mode:             "full",
		CompletionStatus: CompletionStatus{Status: "complete", Reason: "Inventory complete"},
		Languages:        []LanguageInfo{{Language: "Go", FileCount: 10, LineCount: 3000}},
		Frameworks:       []string{"net/http"},
		Modules: []ModuleInfo{
			{Path: "internal/api", Name: "api", Description: "HTTP handlers", Files: 5, Lines: 1200, HasTests: true, ContentHash: "sha256:abc"},
			{Path: "internal/core", Name: "core", Description: "Core logic", Files: 3, Lines: 800, HasTests: true, ContentHash: "sha256:def"},
		},
		EntryPoints: []EntryPoint{
			{Path: "cmd/main.go", Type: "cli", Description: "Main entry point"},
		},
		DependencyGraph: DependencyGraph{
			Edges: []DependencyEdge{{From: "cmd/main", To: "internal/api"}},
		},
		ExistingDocs: []ExistingDoc{
			{Path: "README.md", EstimatedStaleness: "high"},
		},
		TestCoverage: TestCoverageOverview{TestFileCount: 5, PackagesWithTests: 2},
		SuggestedScope: SuggestedScope{
			Include: []string{"internal/", "cmd/"},
			Exclude: []string{".git/"},
		},
	}
}

func defaultTestConfig() CodedocConfig {
	cfg := DefaultCodedocConfig()
	cfg.DiscoveryTimeoutSeconds = 60
	cfg.MaxRetries = 2
	return cfg
}

// ---------------------------------------------------------------------------
// Single-provider tests
// ---------------------------------------------------------------------------

func TestDiscoverySingleProviderSuccess(t *testing.T) {
	dir := t.TempDir()
	runner := &mockRunner{output: validDiscoveryOutput(), cost: 1.5, duration: 5000}

	result, err := RunDiscovery(DiscoveryDeps{
		Runner:     runner,
		Config:     defaultTestConfig(),
		FeatureDir: dir,
		CodePath:   "/tmp/repo",
		Mode:       "full",
		Round:      1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Output == nil {
		t.Fatal("expected non-nil output")
	}
	if len(result.Output.Modules) != 2 {
		t.Errorf("expected 2 modules, got %d", len(result.Output.Modules))
	}
	if result.CostUSD != 1.5 {
		t.Errorf("expected cost 1.5, got %f", result.CostUSD)
	}
	if result.Output.CompletionStatus.Status != "complete" {
		t.Errorf("expected status complete, got %s", result.Output.CompletionStatus.Status)
	}

	// Verify canonical output was written.
	canonicalPath := filepath.Join(dir, "discovery-output.json")
	if _, err := os.Stat(canonicalPath); os.IsNotExist(err) {
		t.Error("expected canonical discovery-output.json to be written")
	}

	// Verify versioned copy was written.
	versionedPath := filepath.Join(dir, "discovery-output-claude-v1.json")
	if _, err := os.Stat(versionedPath); os.IsNotExist(err) {
		t.Error("expected versioned discovery-output-claude-v1.json to be written")
	}
}

func TestDiscoverySingleProviderRetryOnFailure(t *testing.T) {
	dir := t.TempDir()

	callCount := int32(0)
	// Runner that fails on first call, succeeds on second.
	runner := &mockRunner{}
	runner.output = validDiscoveryOutput()

	// We'll use a custom runner that fails first then succeeds.
	calls := int32(0)
	customRunner := &switchRunner{
		failUntil: 1,
		output:    validDiscoveryOutput(),
		calls:     &calls,
	}

	result, err := RunDiscovery(DiscoveryDeps{
		Runner:     customRunner,
		Config:     defaultTestConfig(),
		FeatureDir: dir,
		CodePath:   "/tmp/repo",
		Mode:       "full",
		Round:      1,
	})
	_ = callCount
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output == nil {
		t.Fatal("expected non-nil output after retry")
	}
}

// switchRunner fails for the first N calls, then succeeds.
type switchRunner struct {
	failUntil int32
	output    interface{}
	calls     *int32
}

func (r *switchRunner) Run(prompt string, outputPath string, timeoutSeconds int) (int, string, float64, int64, error) {
	call := atomic.AddInt32(r.calls, 1)
	if call <= r.failUntil {
		return 1, "intentional failure", 0.1, 100, nil // non-nil exit but no Go error — file won't exist
	}
	data, _ := json.MarshalIndent(r.output, "", "  ")
	_ = os.WriteFile(outputPath, data, 0644)
	return 0, "", 0.5, 1000, nil
}

func TestDiscoverySingleProviderAllFailsReturnsError(t *testing.T) {
	dir := t.TempDir()
	runner := &mockRunner{err: os.ErrNotExist, cost: 0.1}

	_, err := RunDiscovery(DiscoveryDeps{
		Runner:     runner,
		Config:     defaultTestConfig(),
		FeatureDir: dir,
		CodePath:   "/tmp/repo",
		Mode:       "full",
		Round:      1,
	})
	if err == nil {
		t.Fatal("expected error when all attempts fail")
	}
}

// ---------------------------------------------------------------------------
// Dual-provider tests
// ---------------------------------------------------------------------------

func TestDiscoveryDualProviderBothSucceed(t *testing.T) {
	dir := t.TempDir()

	claudeOutput := validDiscoveryOutput()
	codexOutput := validDiscoveryOutput()
	codexOutput.Modules = append(codexOutput.Modules, ModuleInfo{
		Path: "internal/extra", Name: "extra", Description: "Extra module from codex",
		Files: 2, Lines: 400, ContentHash: "sha256:xyz",
	})

	// Merge agent output: combined modules.
	mergedOutput := validDiscoveryOutput()
	mergedOutput.Modules = append(mergedOutput.Modules, ModuleInfo{
		Path: "internal/extra", Name: "extra", Description: "Extra module from codex",
		Files: 2, Lines: 400, ContentHash: "sha256:xyz",
	})
	mergedOutput.MergeLog = &MergeLog{ClaudeModules: 2, CodexModules: 3, MergedModules: 3}

	cfg := defaultTestConfig()
	cfg.EnableCodexCodedocDiscovery = true

	result, err := RunDiscovery(DiscoveryDeps{
		Runner:      &mockRunner{output: claudeOutput, cost: 1.0, duration: 5000},
		CodexRunner: &mockRunner{output: codexOutput, cost: 0.5, duration: 3000},
		MergeRunner: &mockRunner{output: mergedOutput, cost: 0.3, duration: 1000},
		Config:      cfg,
		FeatureDir:  dir,
		CodePath:    "/tmp/repo",
		Mode:        "full",
		Round:       1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Output == nil {
		t.Fatal("expected non-nil output")
	}
	if len(result.Output.Modules) != 3 {
		t.Errorf("expected 3 merged modules, got %d", len(result.Output.Modules))
	}
	if result.Output.MergeLog == nil {
		t.Error("expected merge_log to be present")
	}

	// Verify per-provider files were saved.
	if _, err := os.Stat(filepath.Join(dir, "discovery-output-claude-v1.json")); os.IsNotExist(err) {
		t.Error("expected claude versioned output to be saved")
	}
	if _, err := os.Stat(filepath.Join(dir, "discovery-output-codex-v1.json")); os.IsNotExist(err) {
		t.Error("expected codex versioned output to be saved")
	}
	// Merged output.
	if _, err := os.Stat(filepath.Join(dir, "discovery-output-merged-v1.json")); os.IsNotExist(err) {
		t.Error("expected merged versioned output to be saved")
	}
	// Canonical.
	if _, err := os.Stat(filepath.Join(dir, "discovery-output.json")); os.IsNotExist(err) {
		t.Error("expected canonical discovery-output.json to be saved")
	}
}

func TestDiscoveryDualProviderCodexFailsFallback(t *testing.T) {
	dir := t.TempDir()

	cfg := defaultTestConfig()
	cfg.EnableCodexCodedocDiscovery = true

	result, err := RunDiscovery(DiscoveryDeps{
		Runner:      &mockRunner{output: validDiscoveryOutput(), cost: 1.0, duration: 5000},
		CodexRunner: &mockRunner{err: os.ErrNotExist, cost: 0.1, duration: 100},
		Config:      cfg,
		FeatureDir:  dir,
		CodePath:    "/tmp/repo",
		Mode:        "full",
		Round:       1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output == nil {
		t.Fatal("expected non-nil output with claude fallback")
	}
	if len(result.Warnings) == 0 {
		t.Error("expected warning about codex failure")
	}
	if !strings.Contains(result.Warnings[0], "codex") {
		t.Errorf("expected warning to mention codex, got: %s", result.Warnings[0])
	}
}

func TestDiscoveryDualProviderClaudeFailsFallback(t *testing.T) {
	dir := t.TempDir()

	cfg := defaultTestConfig()
	cfg.EnableCodexCodedocDiscovery = true

	result, err := RunDiscovery(DiscoveryDeps{
		Runner:      &mockRunner{err: os.ErrNotExist, cost: 0.1, duration: 100},
		CodexRunner: &mockRunner{output: validDiscoveryOutput(), cost: 0.5, duration: 3000},
		Config:      cfg,
		FeatureDir:  dir,
		CodePath:    "/tmp/repo",
		Mode:        "full",
		Round:       1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output == nil {
		t.Fatal("expected non-nil output with codex fallback")
	}
	if len(result.Warnings) == 0 {
		t.Error("expected warning about claude failure")
	}
}

func TestDiscoveryDualProviderBothFailReturnsError(t *testing.T) {
	dir := t.TempDir()

	cfg := defaultTestConfig()
	cfg.EnableCodexCodedocDiscovery = true

	_, err := RunDiscovery(DiscoveryDeps{
		Runner:      &mockRunner{err: os.ErrNotExist, cost: 0.1},
		CodexRunner: &mockRunner{err: os.ErrNotExist, cost: 0.1},
		Config:      cfg,
		FeatureDir:  dir,
		CodePath:    "/tmp/repo",
		Mode:        "full",
		Round:       1,
	})
	if err == nil {
		t.Fatal("expected error when both providers fail")
	}
	if !strings.Contains(err.Error(), "both discovery providers failed") {
		t.Errorf("expected 'both discovery providers failed' in error, got: %v", err)
	}
}

func TestDiscoveryDualProviderMergeFailsFallback(t *testing.T) {
	dir := t.TempDir()

	cfg := defaultTestConfig()
	cfg.EnableCodexCodedocDiscovery = true

	result, err := RunDiscovery(DiscoveryDeps{
		Runner:      &mockRunner{output: validDiscoveryOutput(), cost: 1.0, duration: 5000},
		CodexRunner: &mockRunner{output: validDiscoveryOutput(), cost: 0.5, duration: 3000},
		MergeRunner: &mockRunner{err: os.ErrNotExist, cost: 0.2},
		Config:      cfg,
		FeatureDir:  dir,
		CodePath:    "/tmp/repo",
		Mode:        "full",
		Round:       1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output == nil {
		t.Fatal("expected non-nil output with claude fallback after merge failure")
	}
	if len(result.Warnings) == 0 {
		t.Error("expected warning about merge failure")
	}
	if !strings.Contains(result.Warnings[0], "merge") {
		t.Errorf("expected warning to mention merge, got: %s", result.Warnings[0])
	}
}

// ---------------------------------------------------------------------------
// Completion status test
// ---------------------------------------------------------------------------

func TestDiscoveryReportsCompletionStatus(t *testing.T) {
	dir := t.TempDir()

	partial := validDiscoveryOutput()
	partial.CompletionStatus = CompletionStatus{Status: "partial", Reason: "Discovery timed out after 60s"}

	result, err := RunDiscovery(DiscoveryDeps{
		Runner:     &mockRunner{output: partial, cost: 1.0},
		Config:     defaultTestConfig(),
		FeatureDir: dir,
		CodePath:   "/tmp/repo",
		Mode:       "full",
		Round:      1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output.CompletionStatus.Status != "partial" {
		t.Errorf("expected partial status, got %s", result.Output.CompletionStatus.Status)
	}
	if result.Output.CompletionStatus.Reason == "" {
		t.Error("expected reason for partial status")
	}
}
