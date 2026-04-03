package codereview

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ---------------------------------------------------------------------------
// Parse result
// ---------------------------------------------------------------------------

// ParseFixOutputResult holds the outcome of parsing a fix agent's JSON output.
type ParseFixOutputResult struct {
	// Output is the parsed FixOutput, nil on parse error.
	Output *FixOutput
	// Warnings holds non-fatal issues found during validation.
	Warnings []string
	// Err is set when the JSON is invalid or missing required fields.
	Err error
}

// ---------------------------------------------------------------------------
// ParseFixOutput
// ---------------------------------------------------------------------------

// ParseFixOutput parses and validates a fix agent's JSON output. It returns
// a ParseFixOutputResult with the parsed output on success, or an error and
// warnings on failure. Required fields: round (> 0), fixes_applied (non-nil).
// Each FixAction must have a non-empty finding_id and a valid status.
func ParseFixOutput(data []byte) ParseFixOutputResult {
	var result ParseFixOutputResult

	var output FixOutput
	if err := json.Unmarshal(data, &output); err != nil {
		result.Err = fmt.Errorf("fix output parse error: invalid JSON: %w", err)
		return result
	}

	// Validate required fields.
	if output.Round <= 0 || output.FixesApplied == nil {
		var missing []string
		if output.Round <= 0 {
			missing = append(missing, "round")
		}
		if output.FixesApplied == nil {
			missing = append(missing, "fixes_applied")
		}
		result.Err = fmt.Errorf("fix output parse error: missing required fields: %s", strings.Join(missing, ", "))
		return result
	}

	// Validate each fix action.
	for i, action := range output.FixesApplied {
		if action.FindingID == "" {
			result.Err = fmt.Errorf("fix output parse error: fixes_applied[%d].finding_id is required", i)
			return result
		}
		if !ValidFixStatuses[action.Status] {
			result.Err = fmt.Errorf("fix output parse error: fixes_applied[%d].status %q is invalid (must be fixed, deferred, or failed)", i, action.Status)
			return result
		}
	}

	// Non-fatal warnings.
	totalFiles := 0
	for _, action := range output.FixesApplied {
		totalFiles += len(action.FilesModified)
	}
	if totalFiles > 1000 {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("fix agent modified %d files (unusually large)", totalFiles))
	}

	result.Output = &output
	return result
}

// ---------------------------------------------------------------------------
// Route result
// ---------------------------------------------------------------------------

// FixRouteDecision describes where the workflow should transition after a fix.
type FixRouteDecision struct {
	// NextState is the recommended next state.
	NextState CodeReviewState
	// Reason explains why this route was chosen.
	Reason string
	// Warnings holds advisory messages to display at the human gate.
	Warnings []string
}

// ---------------------------------------------------------------------------
// RouteAfterFix
// ---------------------------------------------------------------------------

// RouteAfterFix determines the next workflow state based on the fix output.
// It examines the status of each fix action against findings that have
// CRITICAL or MAJOR severity. The routing rules are:
//
//   - All CRITICAL+MAJOR findings have status "fixed" and tests pass →
//     CR_REVIEWING (re-review).
//   - Any finding has status "deferred" or "failed" → CR_HUMAN_GATE_FIXES.
//   - Tests failed (test_results.failed > 0) → CR_HUMAN_GATE_FIXES.
//
// The criticalMajorIDs set contains finding IDs (e.g. "CRIT-001") that are
// CRITICAL or MAJOR severity. Only these are considered for routing — MINOR
// and OBSERVATION fix statuses don't affect the routing decision.
func RouteAfterFix(output *FixOutput, criticalMajorIDs map[string]bool) FixRouteDecision {
	var decision FixRouteDecision

	// Check for test failures first.
	if output.TestResults != nil && output.TestResults.Failed > 0 {
		decision.NextState = CRHumanGateFixes
		decision.Reason = fmt.Sprintf("test failures detected (%d failed)", output.TestResults.Failed)
		return decision
	}

	// Build a lookup of fix statuses by finding ID.
	fixStatusByID := make(map[string]FixStatus, len(output.FixesApplied))
	for _, action := range output.FixesApplied {
		fixStatusByID[action.FindingID] = action.Status
	}

	// Check each CRITICAL+MAJOR finding.
	allFixed := true
	var deferred, failed []string
	for id := range criticalMajorIDs {
		status, found := fixStatusByID[id]
		if !found {
			// Finding not addressed by fix agent — treat as failed.
			allFixed = false
			failed = append(failed, id)
			continue
		}
		switch status {
		case FixStatusFixed:
			// OK
		case FixStatusDeferred:
			allFixed = false
			deferred = append(deferred, id)
		case FixStatusFailed:
			allFixed = false
			failed = append(failed, id)
		}
	}

	if allFixed {
		decision.NextState = CRReviewing
		decision.Reason = "all CRITICAL+MAJOR findings fixed"
		return decision
	}

	decision.NextState = CRHumanGateFixes
	parts := ""
	if len(deferred) > 0 {
		parts += fmt.Sprintf("%d deferred", len(deferred))
	}
	if len(failed) > 0 {
		if parts != "" {
			parts += ", "
		}
		parts += fmt.Sprintf("%d failed", len(failed))
	}
	decision.Reason = fmt.Sprintf("not all CRITICAL+MAJOR findings fixed: %s", parts)
	return decision
}

// RouteAfterFixParseError returns a FixRouteDecision for when the fix output
// could not be parsed. The workflow routes to the human gate with a parse
// error warning.
func RouteAfterFixParseError(parseErr error) FixRouteDecision {
	return FixRouteDecision{
		NextState: CRHumanGateFixes,
		Reason:    "fix output parse error",
		Warnings:  []string{fmt.Sprintf("fix output parse error: %v", parseErr)},
	}
}
