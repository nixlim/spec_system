package codereview

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/foundry-zero/adversarial-spec-system/internal/specworkflow"
)

// defaultGitBranchManager implements GitBranchManager using exec.Command.
type defaultGitBranchManager struct{}

func (g *defaultGitBranchManager) CreateFixBranch(codePath string, round int) error {
	branchName := fmt.Sprintf("cr-fix-round-%d", round)

	// Delete existing branch if present (from a previous aborted run).
	delCmd := exec.Command("git", "-C", codePath, "branch", "-D", branchName)
	delCmd.Run() // ignore errors — branch may not exist

	// Create and checkout the new branch.
	cmd := exec.Command("git", "-C", codePath, "checkout", "-b", branchName)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("create fix branch %s: %s: %w", branchName, string(out), err)
	}
	return nil
}

func (g *defaultGitBranchManager) SubmodulePaths(codePath string) ([]string, error) {
	cmd := exec.Command("git", "-C", codePath, "submodule", "status")
	out, err := cmd.Output()
	if err != nil {
		// No submodules or git error — return empty list.
		return nil, nil
	}
	var paths []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Format: " <sha> <path> (<desc>)" or "-<sha> <path>"
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			paths = append(paths, parts[1])
		}
	}
	return paths, nil
}

func (g *defaultGitBranchManager) DiffNameOnly(codePath, baseSHA string) ([]string, error) {
	cmd := exec.Command("git", "-C", codePath, "diff", "--name-only", baseSHA)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff --name-only: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var files []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

// ---------------------------------------------------------------------------
// RunFixPhase
// ---------------------------------------------------------------------------

// RunFixPhase executes the fix phase of the code review workflow:
//  1. Create fix branch (if branch_per_round mode).
//  2. Build fix agent prompt.
//  3. Dispatch fix agent.
//  4. Parse FixOutput.
//  5. Validate modified files are within code_path scope.
//  6. Determine routing.
func RunFixPhase(cfg FixPhaseConfig) (*FixPhaseResult, error) {
	bm := cfg.BranchManager
	if bm == nil {
		bm = &defaultGitBranchManager{}
	}

	// Step 1: Create fix branch if needed.
	if cfg.CommitMode == "branch_per_round" {
		if err := bm.CreateFixBranch(cfg.CodePath, cfg.Round); err != nil {
			return nil, fmt.Errorf("create fix branch: %w", err)
		}
	}

	// Step 2: Build fix agent prompt.
	prompt := BuildFixAgentPrompt(cfg.FindingsPath, cfg.SpecContent, nil)

	// Step 3: Dispatch fix agent.
	fixOutputPath := filepath.Join(cfg.WorkspaceDir, fmt.Sprintf("fix-output-round-%d.json", cfg.Round))
	exitCode, stderr, costUSD, durationMS, runErr := cfg.Runner.Run(prompt, fixOutputPath, cfg.FixerTimeoutSeconds)

	result := &FixPhaseResult{
		CostUSD:    costUSD,
		DurationMS: durationMS,
	}

	// Handle agent failure (non-zero exit, timeout, etc.).
	if runErr != nil || exitCode != 0 {
		errMsg := "fix agent failed"
		if runErr != nil {
			errMsg = fmt.Sprintf("fix agent error: %v", runErr)
		} else if exitCode != 0 {
			errMsg = fmt.Sprintf("fix agent exited with code %d", exitCode)
		}
		if stderr != "" {
			errMsg += fmt.Sprintf("; stderr: %s", truncate(stderr, 500))
		}

		// Do NOT retry — fix agent side effects are not idempotent.
		result.RouteDecision = FixRouteDecision{
			NextState: CRHumanGateFixes,
			Reason:    errMsg,
			Warnings:  []string{errMsg},
		}
		return result, nil
	}

	// Step 4: Parse FixOutput.
	outputData, err := os.ReadFile(fixOutputPath)
	if err != nil {
		result.RouteDecision = RouteAfterFixParseError(fmt.Errorf("read fix output: %w", err))
		return result, nil
	}
	result.FixOutputRaw = string(outputData)

	parseResult := ParseFixOutput(outputData)
	if parseResult.Err != nil {
		result.RouteDecision = RouteAfterFixParseError(parseResult.Err)
		return result, nil
	}

	result.FixOutput = parseResult.Output

	// Step 5: Post-fix validation — verify modified files are within code_path.
	if cfg.HeadSHA != "" {
		modifiedFiles, diffErr := bm.DiffNameOnly(cfg.CodePath, cfg.HeadSHA)
		if diffErr == nil {
			absCodePath, _ := filepath.Abs(cfg.CodePath)
			for _, f := range modifiedFiles {
				absFile := filepath.Join(absCodePath, f)
				if !strings.HasPrefix(absFile, absCodePath+string(filepath.Separator)) && absFile != absCodePath {
					result.RouteDecision = FixRouteDecision{
						NextState: CREscalated,
						Reason:    fmt.Sprintf("fix agent wrote outside code_path scope: %s", f),
						Warnings:  []string{fmt.Sprintf("SECURITY: fix agent modified file outside code_path: %s", f)},
					}
					return result, nil
				}
			}

			// Check for modifications inside submodule directories.
			subPaths, subErr := bm.SubmodulePaths(cfg.CodePath)
			if subErr == nil && len(subPaths) > 0 {
				for _, f := range modifiedFiles {
					for _, sub := range subPaths {
						if strings.HasPrefix(f, sub+"/") || f == sub {
							result.RouteDecision = FixRouteDecision{
								NextState: CREscalated,
								Reason:    fmt.Sprintf("fix agent modified file in submodule %s: %s", sub, f),
								Warnings:  []string{fmt.Sprintf("SECURITY: fix agent modified file in submodule directory: %s", f)},
							}
							return result, nil
						}
					}
				}
			}
		}
	}

	// Step 6: Route based on fix output.
	result.RouteDecision = RouteAfterFix(parseResult.Output, cfg.CriticalMajorIDs)

	// Append any parse warnings.
	result.RouteDecision.Warnings = append(result.RouteDecision.Warnings, parseResult.Warnings...)

	// Persist fix output.
	persistPath := filepath.Join(cfg.WorkspaceDir, fmt.Sprintf("fix-output-round-%d.json", cfg.Round))
	if data, err := json.MarshalIndent(parseResult.Output, "", "  "); err == nil {
		os.WriteFile(persistPath, append(data, '\n'), 0644)
	}

	return result, nil
}

// truncate returns the first n bytes of s, appending "..." if truncated.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// NewFixAgentRunner creates a ClaudeRunner configured for the fix agent with
// restricted tool access (--allowedTools "Read,Write,Bash") and WorkspaceDir
// set to the target code path. This explicitly does NOT use
// --dangerously-skip-permissions as required by the spec (FR-018, FR-026).
func NewFixAgentRunner(codePath string, timeoutSeconds int, otelPort int, featureName string) *specworkflow.ClaudeRunner {
	env := map[string]string{
		"CLAUDE_CODE_MAX_TURNS":        "50",
		"CLAUDE_CODE_ENABLE_TELEMETRY": "1",
	}
	if otelPort > 0 {
		env["OTEL_METRICS_EXPORTER"] = "otlp"
		env["OTEL_LOGS_EXPORTER"] = "otlp"
		env["OTEL_EXPORTER_OTLP_PROTOCOL"] = "grpc"
		env["OTEL_EXPORTER_OTLP_ENDPOINT"] = fmt.Sprintf("http://localhost:%d", otelPort)
		env["OTEL_METRIC_EXPORT_INTERVAL"] = "5000"
		env["OTEL_SERVICE_NAME"] = "adversarial-spec-system"
		if featureName != "" {
			env["OTEL_RESOURCE_ATTRIBUTES"] = "workflow.feature=" + featureName
		}
	}
	return &specworkflow.ClaudeRunner{
		Command:      "claude",
		Args:         []string{"--allowedTools", "Read,Write,Bash", "--output-format", "json", "--verbose"},
		Timeout:      time.Duration(timeoutSeconds) * time.Second,
		WorkspaceDir: codePath,
		Env:          env,
	}
}
