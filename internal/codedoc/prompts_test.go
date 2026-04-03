package codedoc

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Discovery prompt
// ---------------------------------------------------------------------------

func TestPromptsDiscoveryNonEmpty(t *testing.T) {
	p := BuildDiscoveryPrompt("/repo", "full")
	if p == "" {
		t.Fatal("expected non-empty discovery prompt")
	}
}

func TestPromptsDiscoveryContainsCodePath(t *testing.T) {
	p := BuildDiscoveryPrompt("/my/repo", "full")
	if !strings.Contains(p, "/my/repo") {
		t.Error("discovery prompt should contain code path")
	}
}

func TestPromptsDiscoveryFullModeNoIncremental(t *testing.T) {
	p := BuildDiscoveryPrompt("/repo", "full")
	if strings.Contains(p, "Incremental Mode") {
		t.Error("full mode should not include incremental instructions")
	}
}

func TestPromptsDiscoveryIncrementalModeIncludesSection(t *testing.T) {
	p := BuildDiscoveryPrompt("/repo", "incremental")
	if !strings.Contains(p, "Incremental Mode") {
		t.Error("incremental mode should include incremental instructions")
	}
	if !strings.Contains(p, "codedoc-manifest") {
		t.Error("incremental mode should reference manifest file")
	}
}

func TestPromptsDiscoveryRequiresJSONOutput(t *testing.T) {
	p := BuildDiscoveryPrompt("/repo", "full")
	if !strings.Contains(p, "JSON") {
		t.Error("discovery prompt should require JSON output")
	}
	if !strings.Contains(p, "schema_version") {
		t.Error("discovery prompt should reference schema_version field")
	}
}

// ---------------------------------------------------------------------------
// Discovery merge prompt
// ---------------------------------------------------------------------------

func TestPromptsDiscoveryMergeContainsBothInputs(t *testing.T) {
	p := BuildDiscoveryMergePrompt(`{"agent":"claude"}`, `{"agent":"codex"}`)
	if !strings.Contains(p, `{"agent":"claude"}`) {
		t.Error("merge prompt should contain claude JSON")
	}
	if !strings.Contains(p, `{"agent":"codex"}`) {
		t.Error("merge prompt should contain codex JSON")
	}
}

func TestPromptsDiscoveryMergeHasAllSevenRules(t *testing.T) {
	p := BuildDiscoveryMergePrompt("{}", "{}")
	rules := []string{
		"Modules",
		"Entry points",
		"Dependency graph",
		"Existing docs",
		"Languages/frameworks",
		"Suggested scope",
		"Conflicts",
	}
	for _, r := range rules {
		if !strings.Contains(p, r) {
			t.Errorf("merge prompt missing rule: %s", r)
		}
	}
}

func TestPromptsDiscoveryMergeRequiresMergeLog(t *testing.T) {
	p := BuildDiscoveryMergePrompt("{}", "{}")
	if !strings.Contains(p, "merge_log") {
		t.Error("merge prompt should require merge_log in output")
	}
}

// ---------------------------------------------------------------------------
// Drafter prompt
// ---------------------------------------------------------------------------

func TestPromptsDrafterContainsFourArtefactTypes(t *testing.T) {
	p := BuildDrafterPrompt(`{"modules":[]}`, "/repo")
	artefacts := []string{
		"As-implemented report",
		"Architecture diagrams",
		"Code audit",
		"Doc updates",
	}
	for _, a := range artefacts {
		if !strings.Contains(p, a) {
			t.Errorf("drafter prompt missing artefact type: %s", a)
		}
	}
}

func TestPromptsDrafterContainsDiscoveryInput(t *testing.T) {
	p := BuildDrafterPrompt(`{"test":"discovery"}`, "/repo")
	if !strings.Contains(p, `{"test":"discovery"}`) {
		t.Error("drafter prompt should contain discovery JSON")
	}
}

func TestPromptsDrafterRequiresJSONOutput(t *testing.T) {
	p := BuildDrafterPrompt("{}", "/repo")
	if !strings.Contains(p, "Drafter Output Schema") {
		t.Error("drafter prompt should reference Drafter Output Schema")
	}
}

// ---------------------------------------------------------------------------
// Drafter combine prompt
// ---------------------------------------------------------------------------

func TestPromptsDrafterCombineHasFourRules(t *testing.T) {
	p := BuildDrafterCombinePrompt("{}", "{}", "{}")
	rules := []string{
		"As-implemented report",
		"Architecture diagrams",
		"Code audit",
		"Doc updates",
	}
	for _, r := range rules {
		if !strings.Contains(p, r) {
			t.Errorf("combine prompt missing rule: %s", r)
		}
	}
}

func TestPromptsDrafterCombineContainsAllThreeInputs(t *testing.T) {
	p := BuildDrafterCombinePrompt(`{"c":"claude"}`, `{"c":"codex"}`, `{"d":"disc"}`)
	if !strings.Contains(p, `{"c":"claude"}`) {
		t.Error("combine prompt should contain claude JSON")
	}
	if !strings.Contains(p, `{"c":"codex"}`) {
		t.Error("combine prompt should contain codex JSON")
	}
	if !strings.Contains(p, `{"d":"disc"}`) {
		t.Error("combine prompt should contain discovery JSON")
	}
}

// ---------------------------------------------------------------------------
// Sanitisation prompt
// ---------------------------------------------------------------------------

func TestPromptsSanitisationCoversAllPatternCategories(t *testing.T) {
	p := BuildSanitisationPrompt("/drafts")
	categories := []string{
		"api_key",
		"token",
		"connection_string",
		"password",
		"private_key",
		"pii",
	}
	for _, c := range categories {
		if !strings.Contains(p, c) {
			t.Errorf("sanitisation prompt missing pattern category: %s", c)
		}
	}
}

func TestPromptsSanitisationContainsDraftDir(t *testing.T) {
	p := BuildSanitisationPrompt("/my/drafts")
	if !strings.Contains(p, "/my/drafts") {
		t.Error("sanitisation prompt should contain draft directory")
	}
}

func TestPromptsSanitisationRequiresJSONOutput(t *testing.T) {
	p := BuildSanitisationPrompt("/drafts")
	if !strings.Contains(p, "SanitisationReport") {
		t.Error("sanitisation prompt should reference SanitisationReport output")
	}
}

// ---------------------------------------------------------------------------
// Reviewer prompts — lens groups
// ---------------------------------------------------------------------------

func TestPromptsReviewerAllGroupsIncludeSeverityRubric(t *testing.T) {
	for _, group := range CodedocLensGroups {
		p := BuildReviewerPrompt(group, "/drafts", 1)
		if !strings.Contains(p, "Severity Classification Rubric") {
			t.Errorf("reviewer prompt for group %q missing severity rubric", group)
		}
		// Check all 4 severity levels mentioned.
		for _, sev := range []string{"CRITICAL", "MAJOR", "MINOR", "OBSERVATION"} {
			if !strings.Contains(p, sev) {
				t.Errorf("reviewer prompt for group %q missing severity %s in rubric", group, sev)
			}
		}
	}
}

func TestPromptsReviewerAccuracyLenses(t *testing.T) {
	p := BuildReviewerPrompt("accuracy", "/drafts", 1)
	if !strings.Contains(p, "ACC") || !strings.Contains(p, "CUR") {
		t.Error("accuracy group should include ACC and CUR lenses")
	}
}

func TestPromptsReviewerCompletenessLenses(t *testing.T) {
	p := BuildReviewerPrompt("completeness", "/drafts", 1)
	if !strings.Contains(p, "CMP") || !strings.Contains(p, "CLA") {
		t.Error("completeness group should include CMP and CLA lenses")
	}
}

func TestPromptsReviewerArchitectureLenses(t *testing.T) {
	p := BuildReviewerPrompt("architecture", "/drafts", 1)
	if !strings.Contains(p, "ARC") || !strings.Contains(p, "STR") {
		t.Error("architecture group should include ARC and STR lenses")
	}
}

func TestPromptsReviewerArchitectureRequiresMermaidSnippets(t *testing.T) {
	p := BuildReviewerPrompt("architecture", "/drafts", 1)
	if !strings.Contains(p, "corrected Mermaid snippet") {
		t.Error("ARC lens instructions must require corrected Mermaid snippets")
	}
}

func TestPromptsReviewerAuditSecurityLenses(t *testing.T) {
	p := BuildReviewerPrompt("audit_security", "/drafts", 1)
	for _, lens := range []string{"AUD", "CON", "SEC"} {
		if !strings.Contains(p, lens) {
			t.Errorf("audit_security group should include %s lens", lens)
		}
	}
}

func TestPromptsReviewerOutputFormatRequiresJSON(t *testing.T) {
	p := BuildReviewerPrompt("accuracy", "/drafts", 1)
	if !strings.Contains(p, "ReviewerOutput") {
		t.Error("reviewer prompt should reference ReviewerOutput JSON format")
	}
	if !strings.Contains(p, "finding") {
		t.Error("reviewer prompt should describe findings in output")
	}
}

// ---------------------------------------------------------------------------
// Revision prompt
// ---------------------------------------------------------------------------

func TestPromptsRevisionAddressesCriticalFirst(t *testing.T) {
	p := BuildRevisionPrompt(`[{"id":"ACC-001"}]`, "/drafts", 2)
	idx := strings.Index(p, "CRITICAL")
	idxMaj := strings.Index(p, "MAJOR")
	if idx < 0 || idxMaj < 0 {
		t.Fatal("revision prompt should mention CRITICAL and MAJOR")
	}
	if idx > idxMaj {
		t.Error("revision prompt should address CRITICAL before MAJOR")
	}
}

func TestPromptsRevisionContainsFindingsJSON(t *testing.T) {
	findings := `[{"id":"ACC-001","severity":"critical"}]`
	p := BuildRevisionPrompt(findings, "/drafts", 1)
	if !strings.Contains(p, findings) {
		t.Error("revision prompt should contain the findings JSON")
	}
}

func TestPromptsRevisionContainsRoundNumber(t *testing.T) {
	p := BuildRevisionPrompt("[]", "/drafts", 3)
	if !strings.Contains(p, "3") {
		t.Error("revision prompt should contain the round number")
	}
}

// ---------------------------------------------------------------------------
// Judge prompt
// ---------------------------------------------------------------------------

func TestPromptsJudgeVerdictOptions(t *testing.T) {
	p := BuildJudgePrompt("[]", 1)
	for _, v := range []string{"PASS", "REVISE", "BLOCK"} {
		if !strings.Contains(p, v) {
			t.Errorf("judge prompt missing verdict option: %s", v)
		}
	}
}

func TestPromptsJudgeAuthorityLimits(t *testing.T) {
	p := BuildJudgePrompt("[]", 1)
	if !strings.Contains(p, "downgrade") {
		t.Error("judge prompt should mention downgrade authority limits")
	}
	if !strings.Contains(p, "dismiss") {
		t.Error("judge prompt should mention dismiss authority limits")
	}
}

func TestPromptsJudgeRequiresJSONOutput(t *testing.T) {
	p := BuildJudgePrompt("[]", 1)
	if !strings.Contains(p, "JudgeOutput") {
		t.Error("judge prompt should reference JudgeOutput JSON format")
	}
}

func TestPromptsJudgeContainsFindingsJSON(t *testing.T) {
	findings := `[{"id":"CMP-001","severity":"major","status":"open"}]`
	p := BuildJudgePrompt(findings, 2)
	if !strings.Contains(p, findings) {
		t.Error("judge prompt should contain the findings JSON")
	}
}

// ---------------------------------------------------------------------------
// Lens group mappings
// ---------------------------------------------------------------------------

func TestPromptsLensGroupMappingsComplete(t *testing.T) {
	allLenses := map[string]bool{
		"ACC": false, "CUR": false,
		"CMP": false, "CLA": false,
		"ARC": false, "STR": false,
		"AUD": false, "CON": false, "SEC": false,
	}
	for _, group := range CodedocLensGroups {
		codes := LensCodesForGroup(group)
		if len(codes) == 0 {
			t.Errorf("lens group %q has no lens codes", group)
		}
		for _, code := range codes {
			allLenses[code] = true
		}
	}
	for lens, found := range allLenses {
		if !found {
			t.Errorf("lens %q is not assigned to any group", lens)
		}
	}
}

func TestPromptsReviewerGroupLetters(t *testing.T) {
	expected := map[string]string{
		"accuracy":       "a",
		"completeness":   "b",
		"architecture":   "c",
		"audit_security": "d",
	}
	for group, want := range expected {
		got := ReviewerGroupLetter(group)
		if got != want {
			t.Errorf("ReviewerGroupLetter(%q) = %q, want %q", group, got, want)
		}
	}
}

func TestPromptsFourLensGroups(t *testing.T) {
	if len(CodedocLensGroups) != 4 {
		t.Errorf("expected 4 lens groups, got %d", len(CodedocLensGroups))
	}
}
