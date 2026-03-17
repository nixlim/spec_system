package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/foundry-zero/adversarial-spec-system/internal/specworkflow"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func setupSpecTestConfig(t *testing.T) (SpecAPIConfig, string) {
	t.Helper()
	dir := t.TempDir()

	tracker := specworkflow.NewIssueTracker()
	state := &specworkflow.WorkflowStateJSON{
		State:              specworkflow.StateReviewing,
		Round:              2,
		FeatureName:        "test-feature",
		CurrentSpecVersion: 2,
		FindingsSummary: specworkflow.FindingsSummary{
			Raised:       5,
			Closed:       2,
			OpenCritical: 1,
			OpenMajor:    1,
		},
	}

	cancelled := false
	config := SpecAPIConfig{
		WorkspaceDir: dir,
		FeatureName:  "test-feature",
		GetTracker:   func() *specworkflow.IssueTracker { return tracker },
		GetState:     func() *specworkflow.WorkflowStateJSON { return state },
		CancelFunc:   func() error { cancelled = true; _ = cancelled; return nil },
	}

	return config, dir
}

func writeSpecFile(t *testing.T, dir string, version int, content string) {
	t.Helper()
	specDir := filepath.Join(dir, "specs", "test-feature")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(specDir, "spec-v"+itoa(version)+".md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func itoa(n int) string {
	return string(rune('0'+n%10)) + ""
}

func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder, v interface{}) {
	t.Helper()
	if err := json.NewDecoder(rec.Body).Decode(v); err != nil {
		t.Fatalf("failed to decode JSON response: %v\nbody: %s", err, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// TestSpecGetCurrentSpec
// ---------------------------------------------------------------------------

func TestSpecGetCurrentSpec_OK(t *testing.T) {
	config, dir := setupSpecTestConfig(t)
	writeSpecFile(t, dir, 2, "# Spec v2\nSome content.")

	handler := HandleGetCurrentSpec(config)
	req := httptest.NewRequest(http.MethodGet, "/api/spec/current", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]string
	decodeJSON(t, rec, &resp)
	if resp["content"] != "# Spec v2\nSome content." {
		t.Errorf("unexpected content: %q", resp["content"])
	}
}

func TestSpecGetCurrentSpec_NotFound(t *testing.T) {
	config, _ := setupSpecTestConfig(t)
	// No spec file written, so current version (2) won't exist.

	handler := HandleGetCurrentSpec(config)
	req := httptest.NewRequest(http.MethodGet, "/api/spec/current", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// TestSpecListVersions
// ---------------------------------------------------------------------------

func TestSpecListVersions(t *testing.T) {
	config, dir := setupSpecTestConfig(t)
	writeSpecFile(t, dir, 1, "# v1")
	writeSpecFile(t, dir, 2, "# v2")
	// Also write a non-spec file to ensure it's excluded.
	specDir := filepath.Join(dir, "specs", "test-feature")
	os.WriteFile(filepath.Join(specDir, "notes.md"), []byte("notes"), 0o644)

	handler := HandleListSpecVersions(config)
	req := httptest.NewRequest(http.MethodGet, "/api/spec/versions", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var versions []specVersionInfo
	decodeJSON(t, rec, &versions)

	if len(versions) != 2 {
		t.Fatalf("expected 2 versions, got %d", len(versions))
	}
	if versions[0].Version != 1 {
		t.Errorf("expected first version=1, got %d", versions[0].Version)
	}
	if versions[1].Version != 2 {
		t.Errorf("expected second version=2, got %d", versions[1].Version)
	}
	if versions[0].File != "spec-v1.md" {
		t.Errorf("expected file=spec-v1.md, got %q", versions[0].File)
	}
	if versions[0].ModifiedAt == "" {
		t.Error("expected non-empty modified_at")
	}
}

func TestSpecListVersions_Empty(t *testing.T) {
	config, _ := setupSpecTestConfig(t)

	handler := HandleListSpecVersions(config)
	req := httptest.NewRequest(http.MethodGet, "/api/spec/versions", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var versions []specVersionInfo
	decodeJSON(t, rec, &versions)
	if len(versions) != 0 {
		t.Errorf("expected empty list, got %d items", len(versions))
	}
}

// ---------------------------------------------------------------------------
// TestSpecGetSpecVersion
// ---------------------------------------------------------------------------

func TestSpecGetSpecVersion_OK(t *testing.T) {
	config, dir := setupSpecTestConfig(t)
	writeSpecFile(t, dir, 1, "# Version One\nFirst draft.")

	handler := HandleGetSpecVersion(config)
	req := httptest.NewRequest(http.MethodGet, "/api/spec/versions/1", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]string
	decodeJSON(t, rec, &resp)
	if resp["content"] != "# Version One\nFirst draft." {
		t.Errorf("unexpected content: %q", resp["content"])
	}
}

func TestSpecGetSpecVersion_NotFound(t *testing.T) {
	config, _ := setupSpecTestConfig(t)

	handler := HandleGetSpecVersion(config)
	req := httptest.NewRequest(http.MethodGet, "/api/spec/versions/99", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSpecGetSpecVersion_InvalidVersion(t *testing.T) {
	config, _ := setupSpecTestConfig(t)

	handler := HandleGetSpecVersion(config)
	req := httptest.NewRequest(http.MethodGet, "/api/spec/versions/abc", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// TestSpecGetSpecDiff
// ---------------------------------------------------------------------------

func TestSpecGetSpecDiff(t *testing.T) {
	config, dir := setupSpecTestConfig(t)

	specV1 := "# Feature Spec\n\n## Requirements\n- Requirement A\n- Requirement B\n"
	specV2 := "# Feature Spec\n\n## Requirements\n- Requirement A (updated)\n- Requirement B\n- Requirement C\n"

	writeSpecFile(t, dir, 1, specV1)
	writeSpecFile(t, dir, 2, specV2)

	handler := HandleGetSpecDiff(config)
	req := httptest.NewRequest(http.MethodGet, "/api/spec/diff/1/2", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]string
	decodeJSON(t, rec, &resp)

	diff := resp["diff"]
	if diff == "" {
		t.Fatal("expected non-empty diff")
	}

	// The diff should show removed line with "- " prefix and added lines with "+ ".
	if !containsLine(diff, "- - Requirement A") {
		t.Errorf("diff should contain removed '- Requirement A' line, got:\n%s", diff)
	}
	if !containsLine(diff, "+ - Requirement A (updated)") {
		t.Errorf("diff should contain added '- Requirement A (updated)' line, got:\n%s", diff)
	}
	if !containsLine(diff, "+ - Requirement C") {
		t.Errorf("diff should contain added '- Requirement C' line, got:\n%s", diff)
	}
}

func containsLine(text, prefix string) bool {
	for _, line := range splitTestLines(text) {
		if line == prefix {
			return true
		}
	}
	return false
}

func splitTestLines(s string) []string {
	lines := make([]string, 0)
	for _, l := range splitLines(s) {
		lines = append(lines, l)
	}
	return lines
}

func TestSpecGetSpecDiff_MissingVersion(t *testing.T) {
	config, dir := setupSpecTestConfig(t)
	writeSpecFile(t, dir, 1, "# v1")

	handler := HandleGetSpecDiff(config)
	req := httptest.NewRequest(http.MethodGet, "/api/spec/diff/1/99", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSpecGetSpecDiff_InvalidVersions(t *testing.T) {
	config, _ := setupSpecTestConfig(t)

	handler := HandleGetSpecDiff(config)
	req := httptest.NewRequest(http.MethodGet, "/api/spec/diff/abc/def", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// TestSpecGetIssues
// ---------------------------------------------------------------------------

func TestSpecGetIssues(t *testing.T) {
	config, _ := setupSpecTestConfig(t)
	tracker := config.GetTracker()

	// Add test findings.
	tracker.AddFindings([]specworkflow.MergedFinding{
		{
			ID:          "F-001",
			Description: "Critical issue",
			Severity:    specworkflow.SeverityCritical,
			Lens:        "security",
			Status:      "open",
		},
		{
			ID:          "F-002",
			Description: "Major issue",
			Severity:    specworkflow.SeverityMajor,
			Lens:        "completeness",
			Status:      "open",
		},
		{
			ID:          "F-003",
			Description: "Minor issue",
			Severity:    specworkflow.SeverityMinor,
			Lens:        "security",
			Status:      "open",
		},
	})

	t.Run("all issues", func(t *testing.T) {
		handler := HandleGetIssues(config)
		req := httptest.NewRequest(http.MethodGet, "/api/spec/issues", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var issues []specworkflow.TrackedIssue
		decodeJSON(t, rec, &issues)
		if len(issues) != 3 {
			t.Fatalf("expected 3 issues, got %d", len(issues))
		}
	})

	t.Run("filter by severity", func(t *testing.T) {
		handler := HandleGetIssues(config)
		req := httptest.NewRequest(http.MethodGet, "/api/spec/issues?severity=CRITICAL", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}

		var issues []specworkflow.TrackedIssue
		decodeJSON(t, rec, &issues)
		if len(issues) != 1 {
			t.Fatalf("expected 1 critical issue, got %d", len(issues))
		}
		if issues[0].Finding.ID != "F-001" {
			t.Errorf("expected F-001, got %s", issues[0].Finding.ID)
		}
	})

	t.Run("filter by lens", func(t *testing.T) {
		handler := HandleGetIssues(config)
		req := httptest.NewRequest(http.MethodGet, "/api/spec/issues?lens=security", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}

		var issues []specworkflow.TrackedIssue
		decodeJSON(t, rec, &issues)
		if len(issues) != 2 {
			t.Fatalf("expected 2 security issues, got %d", len(issues))
		}
	})

	t.Run("filter by status", func(t *testing.T) {
		handler := HandleGetIssues(config)
		req := httptest.NewRequest(http.MethodGet, "/api/spec/issues?status=raised", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}

		var issues []specworkflow.TrackedIssue
		decodeJSON(t, rec, &issues)
		if len(issues) != 3 {
			t.Fatalf("expected 3 raised issues, got %d", len(issues))
		}
	})

	t.Run("filter no matches", func(t *testing.T) {
		handler := HandleGetIssues(config)
		req := httptest.NewRequest(http.MethodGet, "/api/spec/issues?severity=OBSERVATION", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}

		var issues []specworkflow.TrackedIssue
		decodeJSON(t, rec, &issues)
		if len(issues) != 0 {
			t.Fatalf("expected 0 issues, got %d", len(issues))
		}
	})
}

// ---------------------------------------------------------------------------
// TestSpecGetIssue
// ---------------------------------------------------------------------------

func TestSpecGetIssue_OK(t *testing.T) {
	config, _ := setupSpecTestConfig(t)
	tracker := config.GetTracker()

	tracker.AddFindings([]specworkflow.MergedFinding{
		{
			ID:          "F-001",
			Description: "Critical issue",
			Severity:    specworkflow.SeverityCritical,
			Lens:        "security",
		},
	})

	handler := HandleGetIssue(config)
	req := httptest.NewRequest(http.MethodGet, "/api/spec/issues/F-001", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var issue specworkflow.TrackedIssue
	decodeJSON(t, rec, &issue)
	if issue.Finding.ID != "F-001" {
		t.Errorf("expected F-001, got %s", issue.Finding.ID)
	}
	if issue.Status != specworkflow.StatusRaised {
		t.Errorf("expected status raised, got %s", issue.Status)
	}
}

func TestSpecGetIssue_NotFound(t *testing.T) {
	config, _ := setupSpecTestConfig(t)

	handler := HandleGetIssue(config)
	req := httptest.NewRequest(http.MethodGet, "/api/spec/issues/F-999", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// TestSpecGetConvergence
// ---------------------------------------------------------------------------

func TestSpecGetConvergence(t *testing.T) {
	config, _ := setupSpecTestConfig(t)
	tracker := config.GetTracker()

	// Add findings with mixed states.
	tracker.AddFindings([]specworkflow.MergedFinding{
		{ID: "F-001", Severity: specworkflow.SeverityCritical},
		{ID: "F-002", Severity: specworkflow.SeverityMajor},
		{ID: "F-003", Severity: specworkflow.SeverityMinor},
		{ID: "F-004", Severity: specworkflow.SeverityMinor},
	})

	// Transition some to terminal states.
	_ = tracker.TransitionIssue("F-003", specworkflow.StatusAcknowledged, 1, "minor ack")
	_ = tracker.TransitionIssue("F-004", specworkflow.StatusDismissed, 1, "not applicable")

	handler := HandleGetConvergence(config)
	req := httptest.NewRequest(http.MethodGet, "/api/spec/convergence", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp convergenceResponse
	decodeJSON(t, rec, &resp)

	if resp.Round != 2 {
		t.Errorf("expected round=2, got %d", resp.Round)
	}
	// Progress should be 2/4 = 0.5 (2 terminal out of 4 total).
	if resp.Progress < 0.49 || resp.Progress > 0.51 {
		t.Errorf("expected progress ~0.5, got %f", resp.Progress)
	}
	// State is REVIEWING, so verdict should be REVISE.
	if resp.LatestVerdict != "REVISE" {
		t.Errorf("expected verdict REVISE, got %s", resp.LatestVerdict)
	}
}

// ---------------------------------------------------------------------------
// TestSpecCancelWorkflow
// ---------------------------------------------------------------------------

func TestSpecCancelWorkflow(t *testing.T) {
	config, _ := setupSpecTestConfig(t)

	handler := HandleCancelWorkflow(config)

	// Should reject GET.
	req := httptest.NewRequest(http.MethodGet, "/api/spec/cancel", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for GET, got %d", rec.Code)
	}

	// POST should work.
	req = httptest.NewRequest(http.MethodPost, "/api/spec/cancel", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]string
	decodeJSON(t, rec, &resp)
	if resp["status"] != "cancelled" {
		t.Errorf("expected status=cancelled, got %q", resp["status"])
	}
}

// ---------------------------------------------------------------------------
// TestComputeDiff
// ---------------------------------------------------------------------------

func TestComputeDiff(t *testing.T) {
	a := "line1\nline2\nline3\n"
	b := "line1\nline2-modified\nline3\nline4\n"

	diff := computeDiff(a, b)

	if !containsLine(diff, "  line1") {
		t.Errorf("expected common line 'line1' in diff:\n%s", diff)
	}
	if !containsLine(diff, "- line2") {
		t.Errorf("expected removed 'line2' in diff:\n%s", diff)
	}
	if !containsLine(diff, "+ line2-modified") {
		t.Errorf("expected added 'line2-modified' in diff:\n%s", diff)
	}
	if !containsLine(diff, "  line3") {
		t.Errorf("expected common 'line3' in diff:\n%s", diff)
	}
	if !containsLine(diff, "+ line4") {
		t.Errorf("expected added 'line4' in diff:\n%s", diff)
	}
}

func TestComputeDiff_EmptyInputs(t *testing.T) {
	// Both empty.
	diff := computeDiff("", "")
	if diff != "" {
		t.Errorf("expected empty diff for two empty inputs, got %q", diff)
	}

	// One empty.
	diff = computeDiff("", "hello\n")
	if !containsLine(diff, "+ hello") {
		t.Errorf("expected added 'hello' line, got:\n%s", diff)
	}

	diff = computeDiff("hello\n", "")
	if !containsLine(diff, "- hello") {
		t.Errorf("expected removed 'hello' line, got:\n%s", diff)
	}
}
