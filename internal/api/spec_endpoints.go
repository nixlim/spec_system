// Package api provides HTTP handlers for the adversarial spec system.
// This file implements REST endpoints for spec viewing, diffing, issue
// tracking, convergence metrics, and workflow cancellation.
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/foundry-zero/adversarial-spec-system/internal/specworkflow"
)

// SpecAPIConfig holds the configuration and callbacks required by the spec
// API endpoints. Each handler closes over a copy of this struct.
type SpecAPIConfig struct {
	// WorkspaceDir is the directory containing spec-v*.md files.
	WorkspaceDir string
	// FeatureName is the default feature name used as a fallback.
	FeatureName string
	// GetTracker returns the issue tracker for the given feature name.
	// If featureName is empty, returns the tracker for the active workflow.
	GetTracker func(featureName ...string) *specworkflow.IssueTracker
	// GetState returns the workflow state for the given feature name.
	// If featureName is empty, returns the state for the active workflow.
	GetState func(featureName ...string) *specworkflow.WorkflowStateJSON
	// CancelFunc cancels the running workflow.
	CancelFunc func() error
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// writeError writes a JSON error response.
func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// specVersionsDir returns the specs/{featureName}/ directory under workspaceDir.
func specVersionsDir(workspaceDir, featureName string) string {
	return filepath.Join(workspaceDir, "specs", featureName)
}

// specFilePath returns the filesystem path for a spec version file.
func specFilePath(workspaceDir, featureName string, version int) string {
	return filepath.Join(specVersionsDir(workspaceDir, featureName), fmt.Sprintf("spec-v%d.md", version))
}

// resolveFeatureFromRequest extracts the feature name from the ?feature=
// query parameter when available. If absent, falls back to the active
// workflow state, then to the static config default. This allows all spec
// endpoints to be feature-aware without changing their URL structure.
func resolveFeatureFromRequest(config SpecAPIConfig, r *http.Request) string {
	if feature := r.URL.Query().Get("feature"); feature != "" {
		return feature
	}
	if config.GetState != nil {
		if state := config.GetState(); state != nil && state.FeatureName != "" {
			return state.FeatureName
		}
	}
	return config.FeatureName
}

// getStateForRequest returns the workflow state for the feature identified
// by the ?feature= query param, falling back to the default behavior.
func getStateForRequest(config SpecAPIConfig, r *http.Request) *specworkflow.WorkflowStateJSON {
	if feature := r.URL.Query().Get("feature"); feature != "" && config.GetState != nil {
		return config.GetState(feature)
	}
	if config.GetState != nil {
		return config.GetState()
	}
	return nil
}

// getTrackerForRequest returns the issue tracker for the feature identified
// by the ?feature= query param, falling back to the default behavior.
func getTrackerForRequest(config SpecAPIConfig, r *http.Request) *specworkflow.IssueTracker {
	if feature := r.URL.Query().Get("feature"); feature != "" && config.GetTracker != nil {
		return config.GetTracker(feature)
	}
	if config.GetTracker != nil {
		return config.GetTracker()
	}
	return nil
}

// listSpecFiles returns all spec-v*.md entries in the spec versions directory,
// sorted by version number ascending.
func listSpecFiles(workspaceDir, featureName string) ([]specVersionInfo, error) {
	dir := specVersionsDir(workspaceDir, featureName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var versions []specVersionInfo
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "spec-v") || !strings.HasSuffix(name, ".md") {
			continue
		}
		numStr := strings.TrimPrefix(name, "spec-v")
		numStr = strings.TrimSuffix(numStr, ".md")
		n, err := strconv.Atoi(numStr)
		if err != nil {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		versions = append(versions, specVersionInfo{
			Version:    n,
			File:       name,
			ModifiedAt: info.ModTime().UTC().Format("2006-01-02T15:04:05Z"),
		})
	}

	sort.Slice(versions, func(i, j int) bool {
		return versions[i].Version < versions[j].Version
	})
	return versions, nil
}

// specVersionInfo describes a single spec version file.
type specVersionInfo struct {
	Version    int    `json:"version"`
	File       string `json:"file"`
	ModifiedAt string `json:"modified_at"`
}

// HandleGetCurrentSpec returns an HTTP handler that serves the current spec
// version's markdown content. The current version is determined from the
// workflow state's CurrentSpecVersion field. Returns 404 if the file does
// not exist.
func HandleGetCurrentSpec(config SpecAPIConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		state := getStateForRequest(config, r)
		if state == nil {
			writeError(w, http.StatusNotFound, "workflow state not available")
			return
		}

		version := state.CurrentSpecVersion
		path := specFilePath(config.WorkspaceDir, resolveFeatureFromRequest(config, r), version)

		content, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, fmt.Sprintf("spec-v%d.md not found", version))
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to read spec file")
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"content": string(content)})
	}
}

// HandleListSpecVersions returns an HTTP handler that lists all spec version
// files in the workspace directory. Returns a JSON array of version objects
// sorted by version number.
func HandleListSpecVersions(config SpecAPIConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		versions, err := listSpecFiles(config.WorkspaceDir, resolveFeatureFromRequest(config, r))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list spec versions")
			return
		}

		if versions == nil {
			versions = []specVersionInfo{}
		}

		writeJSON(w, http.StatusOK, versions)
	}
}

// HandleGetSpecVersion returns an HTTP handler that serves a specific spec
// version's markdown content. The version number is extracted from the URL
// path suffix. Returns 404 if the version does not exist, 400 for invalid
// version numbers.
func HandleGetSpecVersion(config SpecAPIConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Extract version from last path segment.
		parts := strings.Split(strings.TrimRight(r.URL.Path, "/"), "/")
		nStr := parts[len(parts)-1]

		n, err := strconv.Atoi(nStr)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "invalid version number")
			return
		}

		path := specFilePath(config.WorkspaceDir, resolveFeatureFromRequest(config, r), n)
		content, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, fmt.Sprintf("spec-v%d.md not found", n))
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to read spec file")
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"content": string(content)})
	}
}

// HandleGetSpecDiff returns an HTTP handler that computes a line-by-line
// unified diff between two spec versions. The two version numbers are
// extracted from the last two URL path segments (/diff/{a}/{b}). Returns
// 400 for invalid versions, 404 if either file is missing.
func HandleGetSpecDiff(config SpecAPIConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Extract /diff/{a}/{b} from path.
		parts := strings.Split(strings.TrimRight(r.URL.Path, "/"), "/")
		if len(parts) < 2 {
			writeError(w, http.StatusBadRequest, "expected /diff/{a}/{b}")
			return
		}

		bStr := parts[len(parts)-1]
		aStr := parts[len(parts)-2]

		a, errA := strconv.Atoi(aStr)
		b, errB := strconv.Atoi(bStr)
		if errA != nil || errB != nil || a < 0 || b < 0 {
			writeError(w, http.StatusBadRequest, "invalid version numbers")
			return
		}

		feature := resolveFeatureFromRequest(config, r)
		contentA, err := os.ReadFile(specFilePath(config.WorkspaceDir, feature, a))
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, fmt.Sprintf("spec-v%d.md not found", a))
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to read spec file")
			return
		}

		contentB, err := os.ReadFile(specFilePath(config.WorkspaceDir, feature, b))
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, fmt.Sprintf("spec-v%d.md not found", b))
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to read spec file")
			return
		}

		diff := computeDiff(string(contentA), string(contentB))
		writeJSON(w, http.StatusOK, map[string]string{"diff": diff})
	}
}

// HandleGetIssues returns an HTTP handler that lists tracked issues. Supports
// optional query parameters for filtering: severity, status, and lens. All
// filters are case-insensitive. Returns a JSON array of matching issues.
func HandleGetIssues(config SpecAPIConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		tracker := getTrackerForRequest(config, r)
		if tracker == nil {
			writeJSON(w, http.StatusOK, []interface{}{})
			return
		}

		// Collect all issues sorted by ID.
		var issues []specworkflow.TrackedIssue
		for _, issue := range tracker.Issues {
			issues = append(issues, *issue)
		}
		sort.Slice(issues, func(i, j int) bool {
			return issues[i].Finding.ID < issues[j].Finding.ID
		})

		// Apply filters from query params.
		severityFilter := strings.ToUpper(r.URL.Query().Get("severity"))
		statusFilter := strings.ToLower(r.URL.Query().Get("status"))
		lensFilter := strings.ToLower(r.URL.Query().Get("lens"))

		var filtered []specworkflow.TrackedIssue
		for _, issue := range issues {
			if severityFilter != "" && issue.Finding.Severity.String() != severityFilter {
				continue
			}
			if statusFilter != "" && string(issue.Status) != statusFilter {
				continue
			}
			if lensFilter != "" && strings.ToLower(issue.Finding.Lens) != lensFilter {
				continue
			}
			filtered = append(filtered, issue)
		}

		if filtered == nil {
			filtered = []specworkflow.TrackedIssue{}
		}

		writeJSON(w, http.StatusOK, filtered)
	}
}

// HandleGetIssue returns an HTTP handler that serves a single tracked issue
// by its finding ID (extracted from the last URL path segment). Returns the
// issue with its full lifecycle history, or 404 if not found.
func HandleGetIssue(config SpecAPIConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Extract ID from last path segment.
		parts := strings.Split(strings.TrimRight(r.URL.Path, "/"), "/")
		id := parts[len(parts)-1]

		tracker := getTrackerForRequest(config, r)
		if tracker == nil {
			writeError(w, http.StatusNotFound, fmt.Sprintf("issue %q not found", id))
			return
		}

		issue, ok := tracker.Issues[id]
		if !ok {
			writeError(w, http.StatusNotFound, fmt.Sprintf("issue %q not found", id))
			return
		}

		writeJSON(w, http.StatusOK, issue)
	}
}

// convergenceResponse is the JSON structure returned by the convergence endpoint.
type convergenceResponse struct {
	Round         int     `json:"round"`
	OpenCritical  int     `json:"open_critical"`
	OpenMajor     int     `json:"open_major"`
	OpenMinor     int     `json:"open_minor"`
	LatestVerdict string  `json:"latest_verdict"`
	Progress      float64 `json:"progress"`
}

// HandleGetConvergence returns an HTTP handler that reports convergence
// metrics for the current workflow. Progress is computed as the ratio of
// terminal (closed/dismissed/acknowledged) issues to total issues.
func HandleGetConvergence(config SpecAPIConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		state := getStateForRequest(config, r)
		if state == nil {
			writeError(w, http.StatusNotFound, "workflow state not available")
			return
		}

		resp := convergenceResponse{
			Round:         state.Round,
			OpenCritical:  state.FindingsSummary.OpenCritical,
			OpenMajor:     state.FindingsSummary.OpenMajor,
			LatestVerdict: "",
			Progress:      0,
		}

		// Count open minor issues and compute progress from tracker.
		tracker := getTrackerForRequest(config, r)
		if tracker != nil {
			total := 0
			terminal := 0
			openMinor := 0
			for _, issue := range tracker.Issues {
				total++
				if specworkflow.IsTerminal(issue.Status) {
					terminal++
				} else if issue.Finding.Severity == specworkflow.SeverityMinor {
					openMinor++
				}
			}
			resp.OpenMinor = openMinor
			if total > 0 {
				resp.Progress = float64(terminal) / float64(total)
			}
		}

		// Derive latest verdict from state: if finalized => PASS, if reviewing/revising => REVISE.
		switch state.State {
		case specworkflow.StateFinalized:
			resp.LatestVerdict = "PASS"
		case specworkflow.StateReviewing, specworkflow.StateRevising:
			resp.LatestVerdict = "REVISE"
		case specworkflow.StateEscalated:
			resp.LatestVerdict = "BLOCK"
		default:
			resp.LatestVerdict = state.State.String()
		}

		writeJSON(w, http.StatusOK, resp)
	}
}

// taskGraphResponse is the JSON structure returned by the tasks endpoint.
type taskGraphResponse struct {
	Tasks   []json.RawMessage `json:"tasks"`
	Feature string            `json:"feature"`
}

// HandleGetTasks returns an HTTP handler that reads the task graph JSON for
// the given feature from {projectRoot}/.tasks/{featureName}.task.json.
// projectRoot is derived as the parent of WorkspaceDir. Returns
// {"tasks":[],"feature":""} (not a 404) when no file is found.
func HandleGetTasks(config SpecAPIConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		feature := resolveFeatureFromRequest(config, r)

		// projectRoot is one level above the workspace directory.
		projectRoot := filepath.Dir(filepath.Clean(config.WorkspaceDir))
		taskFile := filepath.Join(projectRoot, ".tasks", feature+".task.json")

		data, err := os.ReadFile(taskFile)
		if err != nil {
			if os.IsNotExist(err) {
				// Fall back to {feature}.json in case the agent omitted the .task infix.
				fallback := filepath.Join(projectRoot, ".tasks", feature+".json")
				data, err = os.ReadFile(fallback)
				if err != nil {
					writeJSON(w, http.StatusOK, taskGraphResponse{
						Tasks:   []json.RawMessage{},
						Feature: "",
					})
					return
				}
				taskFile = fallback
			} else {
				writeError(w, http.StatusInternalServerError, "failed to read task file")
				return
			}
		}
		// Parse just enough to extract the tasks array.
		var raw struct {
			Tasks []json.RawMessage `json:"tasks"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to parse task file")
			return
		}
		if raw.Tasks == nil {
			raw.Tasks = []json.RawMessage{}
		}

		writeJSON(w, http.StatusOK, taskGraphResponse{
			Tasks:   raw.Tasks,
			Feature: feature,
		})
	}
}

// HandleCancelWorkflow returns an HTTP handler that cancels the running
// workflow by calling the configured CancelFunc. Returns 200 with a
// cancellation confirmation on success.
func HandleCancelWorkflow(config SpecAPIConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		if config.CancelFunc == nil {
			writeError(w, http.StatusInternalServerError, "cancel function not configured")
			return
		}

		if err := config.CancelFunc(); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("cancel failed: %v", err))
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
	}
}

// computeDiff produces a simple line-by-line unified diff between two strings.
// Lines present only in a are prefixed with "- ", lines present only in b are
// prefixed with "+ ", and common lines are prefixed with "  ". This is a
// straightforward O(n*m) LCS-based diff suitable for small-to-medium specs.
func computeDiff(a, b string) string {
	linesA := splitLines(a)
	linesB := splitLines(b)

	// Build LCS table.
	n, m := len(linesA), len(linesB)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if linesA[i] == linesB[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	// Trace back to produce diff output.
	var buf strings.Builder
	i, j := 0, 0
	for i < n || j < m {
		if i < n && j < m && linesA[i] == linesB[j] {
			buf.WriteString("  ")
			buf.WriteString(linesA[i])
			buf.WriteByte('\n')
			i++
			j++
		} else if j < m && (i >= n || dp[i][j+1] >= dp[i+1][j]) {
			buf.WriteString("+ ")
			buf.WriteString(linesB[j])
			buf.WriteByte('\n')
			j++
		} else {
			buf.WriteString("- ")
			buf.WriteString(linesA[i])
			buf.WriteByte('\n')
			i++
		}
	}

	return buf.String()
}

// splitLines splits a string into lines, handling the trailing-newline edge
// case so that a file ending with "\n" does not produce an extra empty line.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	// Remove trailing empty element from a terminal newline.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
