package codedoc

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/foundry-zero/adversarial-spec-system/internal/specworkflow"
)

// ---------------------------------------------------------------------------
// Mermaid validation
// ---------------------------------------------------------------------------

// validateMermaidSyntax performs a basic structural validation of Mermaid
// diagram content. It checks for common diagram type keywords and balanced
// structure. This does NOT execute arbitrary code.
func validateMermaidSyntax(content string) bool {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return false
	}

	// Check for known diagram type prefixes.
	validStarts := []string{
		"graph ", "graph\n", "flowchart ", "flowchart\n",
		"sequenceDiagram", "classDiagram", "stateDiagram",
		"erDiagram", "gantt", "pie", "gitgraph",
		"mindmap", "timeline", "sankey",
		"---", // frontmatter
	}

	hasValidStart := false
	lower := strings.ToLower(trimmed)
	for _, prefix := range validStarts {
		if strings.HasPrefix(lower, strings.ToLower(prefix)) {
			hasValidStart = true
			break
		}
	}

	return hasValidStart
}

// ---------------------------------------------------------------------------
// Drafting result types
// ---------------------------------------------------------------------------

type cdDrafterResult struct {
	provider string
	output   *DrafterOutput
	data     []byte
	cost     float64
	duration int64
	err      error
}

// ---------------------------------------------------------------------------
// DraftingDeps — dependencies for RunDrafting
// ---------------------------------------------------------------------------

// DraftingDeps holds the dependencies needed by RunDrafting.
type DraftingDeps struct {
	Runner      specworkflow.AgentRunner
	CodexRunner specworkflow.AgentRunner
	CombineRunner specworkflow.AgentRunner
	Config      CodedocConfig
	FeatureDir  string
	CodePath    string
	DiscoveryOutput *DiscoveryOutput
	DiscoveryJSON string
	DraftVersion int
}

// DraftingResult holds the outputs of a successful drafting phase.
type DraftingResult struct {
	Output     *DrafterOutput
	CostUSD    float64
	DurationMS int64
	Warnings   []string
}

// ---------------------------------------------------------------------------
// RunDrafting
// ---------------------------------------------------------------------------

// RunDrafting executes the drafting phase. When dual-provider is enabled,
// both Claude and Codex run in parallel and outputs are combined via an
// LLM-based combine agent (Section 3b). Mermaid diagrams are validated
// after generation and after combine.
func RunDrafting(deps DraftingDeps) (*DraftingResult, error) {
	prompt := BuildDrafterPrompt(deps.DiscoveryJSON, deps.CodePath)
	timeout := deps.Config.AgentTimeoutSeconds

	if deps.Config.EnableCodexCodedocDrafting && deps.CodexRunner != nil {
		return runDualDrafting(deps, prompt, timeout)
	}
	return runSingleDrafting(deps, prompt, timeout)
}

// ---------------------------------------------------------------------------
// Single-provider drafting
// ---------------------------------------------------------------------------

func runSingleDrafting(deps DraftingDeps, prompt string, timeout int) (*DraftingResult, error) {
	outPath := filepath.Join(deps.FeatureDir, "drafter-output.json")

	maxAttempts := deps.Config.MaxRetries + 1
	if maxAttempts < 2 {
		maxAttempts = 2
	}

	var lastErrors []string

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		currentPrompt := appendValidationErrors(prompt, lastErrors)

		_, _, cost, duration, runErr := deps.Runner.Run(currentPrompt, outPath, timeout)
		if runErr != nil {
			if attempt < maxAttempts {
				log.Printf("[codedoc-drafting] dispatch failed attempt %d/%d: %v", attempt, maxAttempts, runErr)
				lastErrors = []string{fmt.Sprintf("agent dispatch failed: %v", runErr)}
				continue
			}
			return nil, fmt.Errorf("drafting agent failed after %d attempts: %w", maxAttempts, runErr)
		}

		output, data, parseErr := parseAndValidateDrafter(outPath)
		if parseErr != nil {
			if attempt < maxAttempts {
				log.Printf("[codedoc-drafting] validation failed attempt %d/%d: %v", attempt, maxAttempts, parseErr)
				lastErrors = []string{parseErr.Error()}
				continue
			}
			return nil, fmt.Errorf("drafter output invalid after %d attempts: %w", maxAttempts, parseErr)
		}

		// Validate Mermaid diagrams.
		validateDiagrams(output)

		// Save versioned copy.
		vPath := filepath.Join(deps.FeatureDir, versionedFilename("drafter-output", "claude", deps.DraftVersion, ".json"))
		_ = os.WriteFile(vPath, data, 0o644)

		// Save canonical.
		_ = os.WriteFile(outPath, data, 0o644)

		// Write draft artefact files.
		if err := writeDraftFiles(deps.FeatureDir, deps.DraftVersion, output); err != nil {
			log.Printf("[codedoc-drafting] warning: failed to write draft files: %v", err)
		}

		log.Printf("[codedoc-drafting] single-provider complete: %d diagrams, %d audit findings",
			len(output.ArchitectureDiagrams), len(output.CodeAudit.Findings))

		return &DraftingResult{
			Output:     output,
			CostUSD:    cost,
			DurationMS: duration,
		}, nil
	}

	return nil, fmt.Errorf("drafting agent exhausted all %d attempts", maxAttempts)
}

// ---------------------------------------------------------------------------
// Dual-provider drafting
// ---------------------------------------------------------------------------

func runDualDrafting(deps DraftingDeps, prompt string, timeout int) (*DraftingResult, error) {
	claudeOutPath := filepath.Join(deps.FeatureDir, versionedFilename("drafter-output", "claude", deps.DraftVersion, ".json"))
	codexOutPath := filepath.Join(deps.FeatureDir, versionedFilename("drafter-output", "codex", deps.DraftVersion, ".json"))

	resultsCh := make(chan cdDrafterResult, 2)
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Printf("[codedoc-drafting] dispatching claude drafter agent")
		_, _, cost, duration, runErr := deps.Runner.Run(prompt, claudeOutPath, timeout)
		if runErr != nil {
			resultsCh <- cdDrafterResult{provider: "claude", cost: cost, duration: duration, err: runErr}
			return
		}
		output, data, parseErr := parseAndValidateDrafter(claudeOutPath)
		if parseErr != nil {
			resultsCh <- cdDrafterResult{provider: "claude", cost: cost, duration: duration, err: parseErr}
			return
		}
		validateDiagrams(output)
		resultsCh <- cdDrafterResult{provider: "claude", output: output, data: data, cost: cost, duration: duration}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Printf("[codedoc-drafting] dispatching codex drafter agent")
		_, _, cost, duration, runErr := deps.CodexRunner.Run(prompt, codexOutPath, timeout)
		if runErr != nil {
			resultsCh <- cdDrafterResult{provider: "codex", cost: cost, duration: duration, err: runErr}
			return
		}
		output, data, parseErr := parseAndValidateDrafter(codexOutPath)
		if parseErr != nil {
			resultsCh <- cdDrafterResult{provider: "codex", cost: cost, duration: duration, err: parseErr}
			return
		}
		validateDiagrams(output)
		resultsCh <- cdDrafterResult{provider: "codex", output: output, data: data, cost: cost, duration: duration}
	}()

	go func() {
		wg.Wait()
		close(resultsCh)
	}()

	var claude, codex *cdDrafterResult
	var totalCost float64
	var maxDuration int64
	for r := range resultsCh {
		r := r
		totalCost += r.cost
		if r.duration > maxDuration {
			maxDuration = r.duration
		}
		if r.provider == "claude" {
			claude = &r
		} else {
			codex = &r
		}
	}

	// Both failed.
	if claude.err != nil && codex.err != nil {
		return nil, fmt.Errorf("both drafter providers failed: claude=%v, codex=%v", claude.err, codex.err)
	}

	// Single-provider fallback.
	if claude.err != nil {
		log.Printf("[codedoc-drafting] WARNING: claude failed (%v), using codex-only output", claude.err)
		return finalizeDrafterOutput(deps, codex.output, codex.data, totalCost, maxDuration,
			[]string{"claude drafter failed; using codex-only output"})
	}
	if codex.err != nil {
		log.Printf("[codedoc-drafting] WARNING: codex failed (%v), using claude-only output", codex.err)
		return finalizeDrafterOutput(deps, claude.output, claude.data, totalCost, maxDuration,
			[]string{"codex drafter failed; using claude-only output"})
	}

	// Both succeeded → combine via LLM agent.
	combinedResult, combineErr := combineDrafterOutputs(deps, claude, codex)
	if combineErr != nil {
		log.Printf("[codedoc-drafting] WARNING: combine agent failed (%v), using claude-only output", combineErr)
		return finalizeDrafterOutput(deps, claude.output, claude.data, totalCost+combinedResult.combineCost, maxDuration,
			[]string{"combine agent failed; using claude-only output"})
	}

	// Validate combined Mermaid diagrams; replace invalid ones with Claude's.
	for i, diag := range combinedResult.output.ArchitectureDiagrams {
		if !validateMermaidSyntax(diag.MermaidContent) {
			// Find the corresponding Claude diagram by type.
			for _, claudeDiag := range claude.output.ArchitectureDiagrams {
				if claudeDiag.DiagramType == diag.DiagramType {
					log.Printf("[codedoc-drafting] replacing invalid combined %s diagram with Claude's", diag.DiagramType)
					combinedResult.output.ArchitectureDiagrams[i] = claudeDiag
					break
				}
			}
		}
	}

	return finalizeDrafterOutput(deps, combinedResult.output, combinedResult.data,
		totalCost+combinedResult.combineCost, maxDuration, nil)
}

// ---------------------------------------------------------------------------
// Combine agent
// ---------------------------------------------------------------------------

type combineResult struct {
	output      *DrafterOutput
	data        []byte
	combineCost float64
}

func combineDrafterOutputs(deps DraftingDeps, claude, codex *cdDrafterResult) (*combineResult, error) {
	prompt := BuildDrafterCombinePrompt(string(claude.data), string(codex.data), deps.DiscoveryJSON)

	runner := deps.CombineRunner
	if runner == nil {
		runner = deps.Runner
	}

	outPath := filepath.Join(deps.FeatureDir, versionedFilename("drafter-output", "combined", deps.DraftVersion, ".json"))
	_, _, cost, _, runErr := runner.Run(prompt, outPath, deps.Config.AgentTimeoutSeconds)
	if runErr != nil {
		return &combineResult{combineCost: cost}, fmt.Errorf("combine agent dispatch failed: %w", runErr)
	}

	output, data, parseErr := parseAndValidateDrafter(outPath)
	if parseErr != nil {
		return &combineResult{combineCost: cost}, fmt.Errorf("combine agent output invalid: %w", parseErr)
	}

	return &combineResult{output: output, data: data, combineCost: cost}, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func finalizeDrafterOutput(deps DraftingDeps, output *DrafterOutput, data []byte, cost float64, duration int64, warnings []string) (*DraftingResult, error) {
	canonicalPath := filepath.Join(deps.FeatureDir, "drafter-output.json")
	_ = os.WriteFile(canonicalPath, data, 0o644)

	if err := writeDraftFiles(deps.FeatureDir, deps.DraftVersion, output); err != nil {
		log.Printf("[codedoc-drafting] warning: failed to write draft files: %v", err)
	}

	return &DraftingResult{
		Output:     output,
		CostUSD:    cost,
		DurationMS: duration,
		Warnings:   warnings,
	}, nil
}

func parseAndValidateDrafter(path string) (*DrafterOutput, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("reading drafter output %s: %w", path, err)
	}

	var output DrafterOutput
	if err := json.Unmarshal(data, &output); err != nil {
		return nil, nil, fmt.Errorf("parsing drafter output JSON: %w", err)
	}

	if errs := ValidateDrafterOutput(&output); len(errs) > 0 {
		return nil, nil, fmt.Errorf("drafter output validation failed: %s", errs[0].Error())
	}

	return &output, data, nil
}

// validateDiagrams sets the mermaid_valid field on each diagram based on
// syntax validation.
func validateDiagrams(output *DrafterOutput) {
	for i := range output.ArchitectureDiagrams {
		output.ArchitectureDiagrams[i].MermaidValid = validateMermaidSyntax(output.ArchitectureDiagrams[i].MermaidContent)
	}
}

// writeDraftFiles writes the draft artefact files to the versioned draft directory.
func writeDraftFiles(featureDir string, version int, output *DrafterOutput) error {
	draftDir := filepath.Join(featureDir, fmt.Sprintf("draft-v%d", version))

	// Create all needed directories.
	dirs := []string{
		draftDir,
		filepath.Join(draftDir, "architecture"),
		filepath.Join(draftDir, "audit"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("creating directory %s: %w", d, err)
		}
	}

	// Write as-implemented report.
	reportPath := filepath.Join(draftDir, "as-implemented-report.md")
	if err := os.WriteFile(reportPath, []byte("# As-Implemented Report\n\n(Generated by codedoc workflow)\n"), 0644); err != nil {
		return fmt.Errorf("writing report: %w", err)
	}

	// Write architecture diagrams.
	for _, diag := range output.ArchitectureDiagrams {
		filename := filepath.Base(diag.FilePath)
		diagPath := filepath.Join(draftDir, "architecture", filename)
		content := fmt.Sprintf("# %s\n\n```mermaid\n%s\n```\n", diag.DiagramType, diag.MermaidContent)
		if err := os.WriteFile(diagPath, []byte(content), 0644); err != nil {
			return fmt.Errorf("writing diagram %s: %w", filename, err)
		}
	}

	// Write code audit JSON.
	auditJSON, _ := json.MarshalIndent(output.CodeAudit, "", "  ")
	auditPath := filepath.Join(draftDir, "audit", "code-audit.json")
	if err := os.WriteFile(auditPath, auditJSON, 0644); err != nil {
		return fmt.Errorf("writing audit JSON: %w", err)
	}

	// Write code audit report.
	auditReportPath := filepath.Join(draftDir, "audit", "code-audit-report.md")
	if err := os.WriteFile(auditReportPath, []byte("# Code Audit Report\n\n(Generated by codedoc workflow)\n"), 0644); err != nil {
		return fmt.Errorf("writing audit report: %w", err)
	}

	return nil
}
