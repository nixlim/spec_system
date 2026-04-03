package codereview

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// BuildReviewerPrompt tests
// ---------------------------------------------------------------------------

func TestPromptBuilder_ReviewerContainsLens(t *testing.T) {
	prompt := BuildReviewerPrompt("security", "/repo", "spec content", GrillCodeModeFullContext)
	if !strings.Contains(prompt, "--lens security") {
		t.Error("expected prompt to contain '--lens security'")
	}
}

func TestPromptBuilder_ReviewerCodeOnlyMode(t *testing.T) {
	prompt := BuildReviewerPrompt("correctness", "/repo", "", GrillCodeModeCodeOnly)
	if !strings.Contains(prompt, "code-only") {
		t.Error("expected prompt to contain 'code-only'")
	}
	if strings.Contains(prompt, "<spec>") {
		t.Error("code-only mode should not contain spec content placeholder")
	}
}

func TestPromptBuilder_ReviewerSpecOnlyMode(t *testing.T) {
	prompt := BuildReviewerPrompt("testing", "/repo", "my spec text", GrillCodeModeSpecOnly)
	if !strings.Contains(prompt, "spec-only") {
		t.Error("expected prompt to contain 'spec-only'")
	}
	if !strings.Contains(prompt, "my spec text") {
		t.Error("expected prompt to contain spec content")
	}
}

func TestPromptBuilder_ReviewerFullContextMode(t *testing.T) {
	prompt := BuildReviewerPrompt("error-handling", "/my/repo", "full spec", GrillCodeModeFullContext)
	if !strings.Contains(prompt, "full-context") {
		t.Error("expected prompt to contain 'full-context'")
	}
	if !strings.Contains(prompt, "full spec") {
		t.Error("expected prompt to contain spec content")
	}
	if !strings.Contains(prompt, "/my/repo") {
		t.Error("expected prompt to contain code path")
	}
}

func TestPromptBuilder_ReviewerContainsCodePath(t *testing.T) {
	prompt := BuildReviewerPrompt("observability", "/tmp/test-repo", "", GrillCodeModeCodeOnly)
	if !strings.Contains(prompt, "/tmp/test-repo") {
		t.Error("expected prompt to contain code path")
	}
}

func TestPromptBuilder_ReviewerAllLenses(t *testing.T) {
	for _, lens := range CodeReviewLensGroups {
		prompt := BuildReviewerPrompt(lens, "/repo", "", GrillCodeModeCodeOnly)
		expected := "--lens " + lens
		if !strings.Contains(prompt, expected) {
			t.Errorf("expected prompt to contain %q for lens %s", expected, lens)
		}
	}
}

// ---------------------------------------------------------------------------
// BuildFixAgentPrompt tests
// ---------------------------------------------------------------------------

func TestPromptBuilder_FixContainsFindingsPath(t *testing.T) {
	prompt := BuildFixAgentPrompt("/workspace/findings.json", "spec text", nil)
	if !strings.Contains(prompt, "/workspace/findings.json") {
		t.Error("expected prompt to contain findings file path")
	}
}

func TestPromptBuilder_FixContainsSpecContent(t *testing.T) {
	prompt := BuildFixAgentPrompt("/findings.json", "my spec content", nil)
	if !strings.Contains(prompt, "my spec content") {
		t.Error("expected prompt to contain spec content")
	}
}

func TestPromptBuilder_FixNoSpecContent(t *testing.T) {
	prompt := BuildFixAgentPrompt("/findings.json", "", nil)
	if strings.Contains(prompt, "<spec>") {
		t.Error("expected prompt without spec content to not contain <spec> tags")
	}
}

func TestPromptBuilder_FixContainsAllConstraints(t *testing.T) {
	prompt := BuildFixAgentPrompt("/findings.json", "", nil)

	constraints := []struct {
		keyword string
		desc    string
	}{
		{"force-push", "force-push constraint"},
		{"delete branches", "branch deletion constraint"},
		{".gitignore", ".gitignore constraint"},
		{"outside the code_path", "outside scope constraint"},
		{"merge fix branches", "merge constraint"},
		{"git clean", "git clean constraint"},
		{"adversarial_spec_system", "own repo constraint"},
		{"grill-spec", "grill-spec constraint"},
		{"dependencies", "dependencies constraint"},
		{"symlinks", "symlinks constraint"},
		{"submodule", "submodule constraint"},
	}

	for _, c := range constraints {
		if !strings.Contains(prompt, c.keyword) {
			t.Errorf("expected prompt to contain %s (%s)", c.keyword, c.desc)
		}
	}
}

func TestPromptBuilder_FixContainsFixOutputSchema(t *testing.T) {
	prompt := BuildFixAgentPrompt("/findings.json", "", nil)

	schemaKeywords := []string{
		"round",
		"fixes_applied",
		"finding_id",
		"status",
		"files_modified",
		"test_results",
		"git_diff_stat",
		"fixed",
		"deferred",
		"failed",
	}

	for _, kw := range schemaKeywords {
		if !strings.Contains(prompt, kw) {
			t.Errorf("expected FixOutput schema to contain %q", kw)
		}
	}
}

func TestPromptBuilder_FixContainsSubmoduleExclusion(t *testing.T) {
	prompt := BuildFixAgentPrompt("/findings.json", "", nil)
	if !strings.Contains(prompt, "submodule") {
		t.Error("expected prompt to contain submodule exclusion instructions")
	}
	if !strings.Contains(prompt, "Do NOT traverse or modify") {
		t.Error("expected explicit submodule exclusion instruction")
	}
}

func TestPromptBuilder_FixWithAdditionalConstraints(t *testing.T) {
	extra := []string{"Do not modify vendored code."}
	prompt := BuildFixAgentPrompt("/findings.json", "", extra)
	if !strings.Contains(prompt, "Do not modify vendored code.") {
		t.Error("expected prompt to contain additional constraint")
	}
}
