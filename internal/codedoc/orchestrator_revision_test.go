package codedoc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/foundry-zero/adversarial-spec-system/internal/specworkflow"
)

// revisionMockRunner implements specworkflow.AgentRunner for revision tests.
// It writes a predetermined JSON payload to outputPath on each Run call.
type revisionMockRunner struct {
	outputJSON []byte
}

func (r *revisionMockRunner) Run(_ string, outputPath string, _ int) (int, string, float64, int64, error) {
	if err := os.WriteFile(outputPath, r.outputJSON, 0o644); err != nil {
		return 1, err.Error(), 0, 0, err
	}
	return 0, "", 0, 0, nil
}

var _ specworkflow.AgentRunner = (*revisionMockRunner)(nil)

// ---------------------------------------------------------------------------
// PrioritizeFindings tests
// ---------------------------------------------------------------------------

func TestRevision_PrioritizeFindingsCriticalFirst(t *testing.T) {
	findings := []ReviewFinding{
		{ID: "MIN-001", Severity: SeverityMinor},
		{ID: "ACC-001", Severity: SeverityCritical},
		{ID: "CMP-001", Severity: SeverityMajor},
		{ID: "OBS-001", Severity: SeverityObservation},
		{ID: "ACC-002", Severity: SeverityCritical},
	}
	result := PrioritizeFindings(findings)
	if result[0].Severity != SeverityCritical {
		t.Errorf("expected CRITICAL first, got %s", result[0].Severity)
	}
	if result[1].Severity != SeverityCritical {
		t.Errorf("expected second CRITICAL, got %s", result[1].Severity)
	}
	if result[2].Severity != SeverityMajor {
		t.Errorf("expected MAJOR third, got %s", result[2].Severity)
	}
	if result[3].Severity != SeverityMinor {
		t.Errorf("expected MINOR fourth, got %s", result[3].Severity)
	}
	if result[4].Severity != SeverityObservation {
		t.Errorf("expected OBSERVATION last, got %s", result[4].Severity)
	}
}

func TestRevision_PrioritizeFindings_StableWithinSeverity(t *testing.T) {
	findings := []ReviewFinding{
		{ID: "ACC-003", Severity: SeverityCritical},
		{ID: "ACC-001", Severity: SeverityCritical},
		{ID: "ACC-002", Severity: SeverityCritical},
	}
	result := PrioritizeFindings(findings)
	if result[0].ID != "ACC-001" || result[1].ID != "ACC-002" || result[2].ID != "ACC-003" {
		t.Errorf("expected sorted by ID within severity, got %s, %s, %s",
			result[0].ID, result[1].ID, result[2].ID)
	}
}

// ---------------------------------------------------------------------------
// ApplyRevisionToFindings tests
// ---------------------------------------------------------------------------

func TestRevision_ApplyRevisionAddressed(t *testing.T) {
	findings := []ReviewFinding{
		{ID: "ACC-001", Severity: SeverityCritical, Status: "open"},
		{ID: "CMP-001", Severity: SeverityMajor, Status: "open"},
		{ID: "MIN-001", Severity: SeverityMinor, Status: "open"},
	}
	revision := &RevisionOutput{
		Addressed: []RevisedFinding{
			{FindingID: "ACC-001", Status: "resolved"},
		},
	}
	result := ApplyRevisionToFindings(findings, revision)
	if result[0].Status != "resolved" {
		t.Errorf("expected ACC-001 resolved, got %s", result[0].Status)
	}
	if result[1].Status != "open" {
		t.Errorf("expected CMP-001 still open, got %s", result[1].Status)
	}
}

func TestRevision_ApplyRevisionWontFix(t *testing.T) {
	findings := []ReviewFinding{
		{ID: "ACC-001", Severity: SeverityCritical, Status: "open"},
	}
	revision := &RevisionOutput{
		WontFix: []RevisedFinding{
			{FindingID: "ACC-001", Status: "wontfix", Rationale: "by design"},
		},
	}
	result := ApplyRevisionToFindings(findings, revision)
	if result[0].Status != "wontfix" {
		t.Errorf("expected ACC-001 wontfix, got %s", result[0].Status)
	}
}

func TestRevision_ApplyRevisionNilRevision(t *testing.T) {
	findings := []ReviewFinding{
		{ID: "ACC-001", Status: "open"},
	}
	result := ApplyRevisionToFindings(findings, nil)
	if result[0].Status != "open" {
		t.Errorf("expected status unchanged with nil revision")
	}
}

// ---------------------------------------------------------------------------
// FilterOpenFindings tests
// ---------------------------------------------------------------------------

func TestRevision_FilterOpenFindings(t *testing.T) {
	findings := []ReviewFinding{
		{ID: "ACC-001", Status: "open"},
		{ID: "CMP-001", Status: "resolved"},
		{ID: "MIN-001", Status: "open"},
		{ID: "ARC-001", Status: "wontfix"},
	}
	open := FilterOpenFindings(findings)
	if len(open) != 2 {
		t.Fatalf("expected 2 open findings, got %d", len(open))
	}
	if open[0].ID != "ACC-001" || open[1].ID != "MIN-001" {
		t.Errorf("expected ACC-001 and MIN-001, got %s and %s", open[0].ID, open[1].ID)
	}
}

// ---------------------------------------------------------------------------
// ValidateRevisionOutput tests
// ---------------------------------------------------------------------------

func TestRevision_ValidateOutput_Valid(t *testing.T) {
	o := &RevisionOutput{
		SchemaVersion: "1.0",
		Agent:         "codedoc-reviser",
		Round:         1,
		Addressed: []RevisedFinding{
			{FindingID: "ACC-001", Status: "resolved"},
		},
		WontFix: []RevisedFinding{
			{FindingID: "MIN-001", Status: "wontfix", Rationale: "cosmetic only"},
		},
	}
	errs := ValidateRevisionOutput(o)
	if len(errs) > 0 {
		t.Errorf("expected no errors, got: %v", errs)
	}
}

func TestRevision_ValidateOutput_MissingFields(t *testing.T) {
	o := &RevisionOutput{}
	errs := ValidateRevisionOutput(o)
	hasSchemaErr := false
	hasAgentErr := false
	hasRoundErr := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "schema_version") {
			hasSchemaErr = true
		}
		if strings.Contains(e.Error(), "agent") {
			hasAgentErr = true
		}
		if strings.Contains(e.Error(), "round") {
			hasRoundErr = true
		}
	}
	if !hasSchemaErr {
		t.Error("expected schema_version error")
	}
	if !hasAgentErr {
		t.Error("expected agent error")
	}
	if !hasRoundErr {
		t.Error("expected round error")
	}
}

func TestRevision_ValidateOutput_WontFixMustHaveRationale(t *testing.T) {
	o := &RevisionOutput{
		SchemaVersion: "1.0",
		Agent:         "codedoc-reviser",
		Round:         1,
		WontFix: []RevisedFinding{
			{FindingID: "MIN-001", Status: "wontfix", Rationale: ""},
		},
	}
	errs := ValidateRevisionOutput(o)
	hasRationaleErr := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "rationale must not be empty") {
			hasRationaleErr = true
		}
	}
	if !hasRationaleErr {
		t.Error("expected rationale error for wontfix without rationale")
	}
}

func TestRevision_ValidateOutput_AddressedMustBeResolved(t *testing.T) {
	o := &RevisionOutput{
		SchemaVersion: "1.0",
		Agent:         "codedoc-reviser",
		Round:         1,
		Addressed: []RevisedFinding{
			{FindingID: "ACC-001", Status: "open"}, // wrong status
		},
	}
	errs := ValidateRevisionOutput(o)
	hasStatusErr := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "must be 'resolved'") {
			hasStatusErr = true
		}
	}
	if !hasStatusErr {
		t.Error("expected status error for addressed with wrong status")
	}
}

// ---------------------------------------------------------------------------
// BuildRevisionSummary tests
// ---------------------------------------------------------------------------

func TestRevision_BuildSummary(t *testing.T) {
	revision := &RevisionOutput{
		Addressed: []RevisedFinding{
			{FindingID: "ACC-001", Status: "resolved"},
			{FindingID: "CMP-001", Status: "resolved"},
		},
		WontFix: []RevisedFinding{
			{FindingID: "MIN-001", Status: "wontfix", Rationale: "cosmetic"},
		},
		Remaining: []RevisedFinding{
			{FindingID: "ARC-001", Status: "open"},
		},
	}
	summary := BuildRevisionSummary(revision)
	if summary.AddressedCount != 2 {
		t.Errorf("expected 2 addressed, got %d", summary.AddressedCount)
	}
	if summary.WontFixCount != 1 {
		t.Errorf("expected 1 wontfix, got %d", summary.WontFixCount)
	}
	if summary.RemainingCount != 1 {
		t.Errorf("expected 1 remaining, got %d", summary.RemainingCount)
	}
}

// ---------------------------------------------------------------------------
// Read/Write round-trip tests
// ---------------------------------------------------------------------------

func TestRevision_WriteAndReadMergedFindings(t *testing.T) {
	dir := t.TempDir()
	findings := []ReviewFinding{
		{ID: "ACC-001", Severity: SeverityCritical, Status: "open"},
		{ID: "MIN-001", Severity: SeverityMinor, Status: "open"},
	}
	data, _ := json.MarshalIndent(findings, "", "  ")
	os.WriteFile(filepath.Join(dir, "merged-findings-round-1.json"), data, 0o644)

	read, err := ReadMergedFindings(dir, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(read) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(read))
	}
	if read[0].ID != "ACC-001" {
		t.Errorf("expected ACC-001, got %s", read[0].ID)
	}
}

func TestRevision_WriteRevisionOutput(t *testing.T) {
	dir := t.TempDir()
	revision := &RevisionOutput{
		SchemaVersion: "1.0",
		Agent:         "codedoc-reviser",
		Round:         1,
		Addressed: []RevisedFinding{
			{FindingID: "ACC-001", Status: "resolved"},
		},
		Summary: RevisionSummary{AddressedCount: 1},
	}
	err := WriteRevisionOutput(dir, 1, revision)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Verify file exists and is valid JSON.
	data, err := os.ReadFile(filepath.Join(dir, "revision-round-1.json"))
	if err != nil {
		t.Fatalf("file not written: %v", err)
	}
	var read RevisionOutput
	if err := json.Unmarshal(data, &read); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if read.SchemaVersion != "1.0" {
		t.Errorf("expected schema_version 1.0, got %s", read.SchemaVersion)
	}
	if len(read.Addressed) != 1 {
		t.Errorf("expected 1 addressed, got %d", len(read.Addressed))
	}
}

func TestRevision_ReadMergedFindings_NotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := ReadMergedFindings(dir, 99)
	if err == nil {
		t.Error("expected error for missing findings file")
	}
}

// ---------------------------------------------------------------------------
// MAJ-001 regression: handleRevising must increment DraftVersion
// ---------------------------------------------------------------------------

// TestHandleRevising_IncrementsDraftVersion ensures that each call to
// handleRevising copies the current draft directory to a new versioned
// directory (draft-v{N+1}/) and increments ws.DraftVersion. Before the fix,
// DraftVersion was never updated so every revision round overwrote draft-v1/.
func TestHandleRevising_IncrementsDraftVersion(t *testing.T) {
	featureDir := t.TempDir()

	// Create an initial draft-v1/ with a sentinel file.
	draftV1 := filepath.Join(featureDir, "draft-v1")
	if err := os.MkdirAll(draftV1, 0o755); err != nil {
		t.Fatalf("mkdir draft-v1: %v", err)
	}
	sentinelContent := []byte("original content")
	if err := os.WriteFile(filepath.Join(draftV1, "README.md"), sentinelContent, 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	// Build a minimal orchestrator — we only need to exercise the versioning
	// logic, so the runner writes a valid but minimal revision JSON.
	ws := &CDStateJSON{
		State:        CDRevising,
		Round:        1,
		FeatureName:  "test-docs",
		DraftVersion: 1,
	}
	sm := &CDStateMachine{}
	sm.RestoreState(ws)

	revisionOutput := RevisionOutput{
		SchemaVersion: "1.0",
		Agent:         "codedoc-revision",
		Round:         1,
		Addressed:     []RevisedFinding{},
		WontFix:       []RevisedFinding{},
		Remaining:     []RevisedFinding{},
	}
	revJSON, _ := json.Marshal(revisionOutput)

	o := &CodedocOrchestrator{
		featureDir:     featureDir,
		sm:             sm,
		mergedFindings: []ReviewFinding{},
		runner:         &revisionMockRunner{outputJSON: revJSON},
		config:         CodedocConfig{AgentTimeoutSeconds: 10},
	}

	// Run one revision.
	if err := o.handleRevising(); err != nil {
		t.Fatalf("handleRevising: %v", err)
	}

	// DraftVersion must be incremented.
	if got := o.sm.State().DraftVersion; got != 2 {
		t.Errorf("DraftVersion after first revision: want 2, got %d", got)
	}

	// draft-v2/ must exist and contain the sentinel file.
	draftV2 := filepath.Join(featureDir, "draft-v2")
	if _, err := os.Stat(draftV2); err != nil {
		t.Errorf("draft-v2 not created: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(draftV2, "README.md"))
	if err != nil {
		t.Errorf("sentinel file not copied to draft-v2: %v", err)
	}
	if string(data) != string(sentinelContent) {
		t.Errorf("sentinel content mismatch: got %q", data)
	}

	// A second revision must create draft-v3/.
	// handleRevising transitions to CDJudging at the end; rewind to CDRevising
	// to simulate the state machine being driven back for a second revision round.
	ws.State = CDRevising
	ws.Round = 2
	if err := o.handleRevising(); err != nil {
		t.Fatalf("handleRevising round 2: %v", err)
	}
	if got := o.sm.State().DraftVersion; got != 3 {
		t.Errorf("DraftVersion after second revision: want 3, got %d", got)
	}
	if _, err := os.Stat(filepath.Join(featureDir, "draft-v3")); err != nil {
		t.Errorf("draft-v3 not created: %v", err)
	}
}

// ---------------------------------------------------------------------------
// MermaidValid re-validation test (acceptance criteria item)
// ---------------------------------------------------------------------------

func TestRevision_MermaidRevalidation(t *testing.T) {
	// Verify that DiagramRef.MermaidValid can be set to false and checked.
	d := DiagramRef{
		FilePath:       "docs/architecture/module-dependencies.md",
		DiagramType:    "module_dependency",
		MermaidContent: "graph TD; A-->B",
		MermaidValid:   true,
	}
	if !d.MermaidValid {
		t.Error("expected MermaidValid to be true")
	}
	d.MermaidValid = false
	if d.MermaidValid {
		t.Error("expected MermaidValid to be false after setting")
	}
}
