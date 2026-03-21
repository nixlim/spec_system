package specworkflow

import "fmt"

// HoldoutDispatchResult holds the outcome of holdout agent dispatch.
type HoldoutDispatchResult struct {
	// ClaudeOutput is the parsed output from the claude holdout agent (nil if failed).
	ClaudeOutput *HoldoutOutput
	// CodexOutput is the parsed output from the codex holdout agent (nil if failed or unavailable).
	CodexOutput *HoldoutOutput
	// ClaudeMDPath is the path to the claude holdout markdown.
	ClaudeMDPath string
	// CodexMDPath is the path to the codex holdout markdown.
	CodexMDPath string
	// MergedMDPath is the path to the merged holdout markdown.
	MergedMDPath string
	// TotalCostUSD is the combined cost of all holdout agents.
	TotalCostUSD float64
	// Error is non-nil only if both providers failed.
	Error error
}

// MergeHoldoutMarkdown combines holdout markdown content from multiple providers
// into a single attributed document. Each provider's contribution is wrapped in
// a section header identifying its source.
func MergeHoldoutMarkdown(round int, claudeMD, codexMD string, claudeOK, codexOK bool) string {
	if claudeOK && codexOK {
		return fmt.Sprintf("# Holdout Tests — Round %d (Merged)\n\n"+
			"## Provider: Claude\n\n%s\n\n"+
			"## Provider: Codex\n\n%s\n",
			round, claudeMD, codexMD)
	}
	if claudeOK {
		return fmt.Sprintf("# Holdout Tests — Round %d\n\n"+
			"> Note: Only Claude provider contributed (Codex unavailable or failed).\n\n"+
			"## Provider: Claude\n\n%s\n",
			round, claudeMD)
	}
	if codexOK {
		return fmt.Sprintf("# Holdout Tests — Round %d\n\n"+
			"> Note: Only Codex provider contributed (Claude failed).\n\n"+
			"## Provider: Codex\n\n%s\n",
			round, codexMD)
	}
	return fmt.Sprintf("# Holdout Tests — Round %d\n\n> Error: No providers produced output.\n", round)
}
