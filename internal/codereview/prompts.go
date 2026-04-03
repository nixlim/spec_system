package codereview

import (
	"fmt"
	"strings"
)

// ---------------------------------------------------------------------------
// Reviewer prompt builder
// ---------------------------------------------------------------------------

// BuildReviewerPrompt constructs the prompt for a code review agent using the
// grill-code skill. The prompt includes the lens name, code path, grill-code
// mode, and optional spec content.
func BuildReviewerPrompt(lens, codePath, specContent string, mode GrillCodeMode) string {
	var b strings.Builder

	b.WriteString("You are a code reviewer. Use the grill-code skill to review the code.\n\n")
	b.WriteString(fmt.Sprintf("Run grill-code with --lens %s against the repository at %s.\n\n", lens, codePath))
	b.WriteString(fmt.Sprintf("Review mode: %s\n\n", mode.String()))

	switch mode {
	case GrillCodeModeCodeOnly:
		b.WriteString("This is a code-only review. No spec is provided. Focus on code quality, correctness, and best practices.\n\n")
	case GrillCodeModeSpecOnly:
		b.WriteString("Review the code against the following specification.\n\n")
		b.WriteString("<spec>\n")
		b.WriteString(specContent)
		b.WriteString("\n</spec>\n\n")
	case GrillCodeModeFullContext:
		b.WriteString("Review the code against the following specification and task list context.\n\n")
		b.WriteString("<spec>\n")
		b.WriteString(specContent)
		b.WriteString("\n</spec>\n\n")
	}

	b.WriteString("Output your findings as ReviewerOutput JSON conforming to the standard schema.\n")
	b.WriteString("Each finding must include: id, description, severity (CRITICAL/MAJOR/MINOR/OBSERVATION), ")
	b.WriteString("impact, recommendation, lens, and affected_section (file path with optional line range, e.g. 'internal/api/handler.go:45-62').\n")

	return b.String()
}

// ---------------------------------------------------------------------------
// Fix agent prompt builder
// ---------------------------------------------------------------------------

// fixAgentConstraints lists the 11 non-behavior constraints from the spec
// that the fix agent must obey.
var fixAgentConstraints = []string{
	"You must NOT force-push to any branch.",
	"You must NOT delete branches you did not create.",
	"You must NOT modify .gitignore.",
	"You must NOT touch files outside the code_path scope.",
	"You must NOT merge fix branches automatically.",
	"You must NOT run `git clean` or `git reset --hard`.",
	"You must NOT modify the adversarial_spec_system's own repository when operating on an external repo.",
	"You must NOT invoke the grill-spec skill.",
	"You must NOT add, remove, or upgrade dependencies (go.mod, package.json, etc.) unless a finding explicitly requires it.",
	"You must NOT follow symlinks to write to destinations outside code_path.",
	"You must NOT traverse or modify git submodule directories.",
}

// BuildFixAgentPrompt constructs the prompt for the fix agent. The prompt
// includes the findings file path, optional spec content, all non-behavior
// constraints, the FixOutput JSON schema, and submodule exclusion instructions.
func BuildFixAgentPrompt(findingsPath, specContent string, additionalConstraints []string) string {
	var b strings.Builder

	b.WriteString("You are a code fix agent. Your job is to fix the findings identified during code review.\n\n")

	b.WriteString(fmt.Sprintf("Read the findings from: %s\n\n", findingsPath))

	if specContent != "" {
		b.WriteString("The following specification provides context for the fixes:\n\n")
		b.WriteString("<spec>\n")
		b.WriteString(specContent)
		b.WriteString("\n</spec>\n\n")
	}

	b.WriteString("## Constraints\n\n")
	b.WriteString("You MUST obey the following constraints:\n\n")
	for _, c := range fixAgentConstraints {
		b.WriteString(fmt.Sprintf("- %s\n", c))
	}
	for _, c := range additionalConstraints {
		b.WriteString(fmt.Sprintf("- %s\n", c))
	}
	b.WriteString("\n")

	b.WriteString("## Submodule Exclusion\n\n")
	b.WriteString("Do NOT traverse or modify any git submodule directories. ")
	b.WriteString("Submodule directories have their own version control lifecycle and must not be touched. ")
	b.WriteString("If a finding references code inside a submodule, mark it as status: \"deferred\" with a description explaining that submodule code is out of scope.\n\n")

	b.WriteString("## Output Format\n\n")
	b.WriteString("After completing all fixes, produce a JSON response conforming to the FixOutput schema:\n\n")
	b.WriteString("```json\n")
	b.WriteString("{\n")
	b.WriteString("  \"round\": <int>,           // Fix round number (1-indexed, matches review round)\n")
	b.WriteString("  \"fixes_applied\": [         // List of fix actions, one per finding addressed\n")
	b.WriteString("    {\n")
	b.WriteString("      \"finding_id\": \"<string>\",  // Merged finding ID (e.g., \"CRIT-001\")\n")
	b.WriteString("      \"status\": \"<string>\",      // One of: \"fixed\", \"deferred\", \"failed\"\n")
	b.WriteString("      \"files_modified\": [\"<string>\"],  // File paths changed\n")
	b.WriteString("      \"description\": \"<string>\"  // What was changed and why\n")
	b.WriteString("    }\n")
	b.WriteString("  ],\n")
	b.WriteString("  \"test_results\": {          // null if no tests were run\n")
	b.WriteString("    \"total\": <int>,\n")
	b.WriteString("    \"passed\": <int>,\n")
	b.WriteString("    \"failed\": <int>,\n")
	b.WriteString("    \"failures\": [\"<string>\"]  // Names of failed tests\n")
	b.WriteString("  },\n")
	b.WriteString("  \"git_diff_stat\": \"<string>\"  // Output of `git diff --stat`\n")
	b.WriteString("}\n")
	b.WriteString("```\n\n")
	b.WriteString("Valid status values: \"fixed\" (finding addressed), \"deferred\" (intentionally skipped), \"failed\" (attempted but could not fix).\n")

	return b.String()
}
