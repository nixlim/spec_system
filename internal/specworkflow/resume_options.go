// Package specworkflow — resume_options.go probes the workspace for existing
// per-stage artefacts and reports what resume strategies are available when
// the user wants to continue a workflow that escalated mid-stage.
//
// The design is API-level: callers (e.g. the /api/workflow/{feature}/resume-options
// HTTP handler) invoke ProbeResumeOptions to get a report and surface it in the
// UI; the user picks a mode; the caller then passes that mode to the resume
// endpoint, which applies the corresponding state transition and/or re-runs
// the specific agent steps. This avoids coupling resume logic to per-stage
// in-orchestrator gate channels.
package specworkflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ResumeMode is the strategy the user chooses when resuming a workflow.
type ResumeMode string

const (
	// ResumeModeRestartFresh discards in-progress stage artefacts and re-runs
	// the stage from scratch. Always available as a fallback.
	ResumeModeRestartFresh ResumeMode = "restart_fresh"
	// ResumeModeReplayMerge re-runs the merge/combine step of a dual-provider
	// stage using the existing per-provider outputs, without re-dispatching
	// the underlying agents. Only available when both per-provider outputs
	// exist for the current round.
	ResumeModeReplayMerge ResumeMode = "replay_merge"
	// ResumeModeSkipToGate accepts the stage's canonical output as-is and
	// advances to the stage's trailing human gate (or the next active state
	// when the stage has no trailing gate). Only available when the canonical
	// output is present and parses as valid JSON.
	ResumeModeSkipToGate ResumeMode = "skip_to_gate"
)

// StageResumeOptions reports the resume modes available for a single stage.
// Multiple stages may be reported when the persisted state is ambiguous
// (e.g. ESCALATED with artefacts from several stages on disk), but
// ProbeResumeOptions typically returns only the inferred current stage.
type StageResumeOptions struct {
	// Stage is the WorkflowState string this entry refers to (e.g. "DRAFTING").
	Stage string `json:"stage"`
	// NextGate is the state skip_to_gate would advance to (e.g. "HUMAN_GATE_2").
	// Empty when skip_to_gate is not available.
	NextGate string `json:"next_gate,omitempty"`
	// AvailableModes lists the modes the UI may offer for this stage.
	// restart_fresh is always present; the other modes depend on artefacts.
	AvailableModes []ResumeMode `json:"available_modes"`
	// HasCanonical is true when the canonical per-stage output file exists
	// and contains valid JSON (or valid markdown, for non-JSON stages).
	HasCanonical bool `json:"has_canonical"`
	// HasClaude and HasCodex report whether per-provider outputs exist for
	// the current round of a dual-provider stage.
	HasClaude bool `json:"has_claude,omitempty"`
	HasCodex  bool `json:"has_codex,omitempty"`
	// CanonicalPreview is a small JSON-serialisable summary of the canonical
	// output (e.g. user story count for drafter, actor count for discovery)
	// so the UI can show something meaningful without fetching the whole file.
	CanonicalPreview interface{} `json:"canonical_preview,omitempty"`
}

// ResumeOptions is the full report returned by ProbeResumeOptions.
type ResumeOptions struct {
	FeatureName    string               `json:"feature_name"`
	PersistedState string               `json:"persisted_state"`
	Round          int                  `json:"round"`
	// InferredStage is the stage the orchestrator would land in if the user
	// chooses restart_fresh with mode unset. It is the value determineResumeState
	// would return, made explicit for the UI.
	InferredStage string              `json:"inferred_stage"`
	Stages        []StageResumeOptions `json:"stages"`
	// DefaultMode is the recommended mode the UI should pre-select based on
	// the artefacts on disk. It is the "most forward" safe choice:
	// skip_to_gate when canonical output is valid, else replay_merge when
	// per-provider outputs exist, else restart_fresh.
	DefaultMode ResumeMode `json:"default_mode"`
}

// ProbeResumeOptions walks the spec directory for the given feature and
// reports what resume modes are available for the stage the workflow would
// naturally land in after a crash or escalation.
//
// The returned report reflects disk state only; it does not mutate anything.
// The caller (HTTP handler) is responsible for acting on the user's choice.
func ProbeResumeOptions(workspaceDir, featureName string, state *WorkflowStateJSON) *ResumeOptions {
	specDir := filepath.Join(workspaceDir, "specs", featureName)
	round := 1
	if state != nil && state.Round > 0 {
		round = state.Round
	}

	opts := &ResumeOptions{
		FeatureName: featureName,
		Round:       round,
		Stages:      []StageResumeOptions{},
		DefaultMode: ResumeModeRestartFresh,
	}
	if state != nil {
		opts.PersistedState = state.State.String()
	}

	// The probe order matters: we report options for the MOST ADVANCED stage
	// whose prerequisites are satisfied, walking forward from the earliest
	// stage until we hit the first one that is not yet complete. The final
	// stage we report is the inferred "resume from here" stage.
	//
	// Walk order reflects the orchestrator's state machine:
	//   DISCOVERY → DRAFTING → REVIEWING → REVISING → JUDGING → TASKIFY
	// A stage is probed only when the previous stage has a valid canonical
	// output. We stop extending the chain as soon as the current stage has
	// NO artefacts at all — that's the natural resume point.

	// DISCOVERY stage probe.
	discoveryOpts := probeDiscoveryResume(specDir, round)
	if discoveryOpts != nil {
		opts.Stages = append(opts.Stages, *discoveryOpts)
	}

	// DRAFTING stage probe. Only reported when discovery has a canonical.
	var draftingOpts *StageResumeOptions
	if discoveryOpts != nil && discoveryOpts.HasCanonical {
		draftingOpts = probeDraftingResume(specDir, round)
		if draftingOpts != nil {
			opts.Stages = append(opts.Stages, *draftingOpts)
		}
	}

	// REVIEWING stage probe. Only reported when drafting has a canonical.
	var reviewingOpts *StageResumeOptions
	if draftingOpts != nil && draftingOpts.HasCanonical {
		reviewingOpts = probeReviewingResume(specDir, round)
		if reviewingOpts != nil {
			opts.Stages = append(opts.Stages, *reviewingOpts)
		}
	}

	// REVISING stage probe. Only reported when reviewing has a canonical
	// (merged-findings-round-N.json exists).
	var revisingOpts *StageResumeOptions
	if reviewingOpts != nil && reviewingOpts.HasCanonical {
		revisingOpts = probeRevisingResume(specDir, round)
		if revisingOpts != nil {
			opts.Stages = append(opts.Stages, *revisingOpts)
		}
	}

	// JUDGING stage probe. Only reported when revising has a canonical.
	var judgingOpts *StageResumeOptions
	if revisingOpts != nil && revisingOpts.HasCanonical {
		judgingOpts = probeJudgingResume(specDir, round)
		if judgingOpts != nil {
			opts.Stages = append(opts.Stages, *judgingOpts)
		}
	}

	// TASKIFY stage probe. Task graph lives outside the spec dir, under
	// the workspace's .tasks directory. Only reported once the main spec
	// workflow has reached HUMAN_GATE_FINAL / finalisation, which we
	// approximate by "drafter canonical exists" — taskify can only run
	// once the spec has been drafted.
	if draftingOpts != nil && draftingOpts.HasCanonical {
		taskifyOpts := probeTaskifyResume(workspaceDir, featureName)
		if taskifyOpts != nil {
			opts.Stages = append(opts.Stages, *taskifyOpts)
		}
	}

	// The inferred resume stage is the last stage we reported — it is the
	// most advanced stage with any artefacts on disk. If nothing at all is
	// on disk, we fall back to DISCOVERY.
	if len(opts.Stages) > 0 {
		last := opts.Stages[len(opts.Stages)-1]
		opts.InferredStage = last.Stage
		opts.DefaultMode = pickDefaultMode(last)
	} else {
		opts.InferredStage = StateDiscovery.String()
	}

	return opts
}

// pickDefaultMode picks the most forward safe mode for a given stage report:
// skip_to_gate if a valid canonical exists, else replay_merge if per-provider
// outputs can be replayed, else restart_fresh.
func pickDefaultMode(s StageResumeOptions) ResumeMode {
	for _, m := range s.AvailableModes {
		if m == ResumeModeSkipToGate {
			return m
		}
	}
	for _, m := range s.AvailableModes {
		if m == ResumeModeReplayMerge {
			return m
		}
	}
	return ResumeModeRestartFresh
}

// probeDiscoveryResume inspects the spec directory for discovery artefacts.
// Returns nil when no discovery artefacts of any kind are present — the
// caller should then treat discovery as "not yet started".
func probeDiscoveryResume(specDir string, round int) *StageResumeOptions {
	canonicalPath := filepath.Join(specDir, "discovery-output.json")
	claudePath := filepath.Join(specDir, VersionedFilename("discovery-output", "claude", round, ".json"))
	codexPath := filepath.Join(specDir, VersionedFilename("discovery-output", "codex", round, ".json"))

	opts := &StageResumeOptions{
		Stage:          StateDiscovery.String(),
		NextGate:       StateHumanGate1.String(),
		AvailableModes: []ResumeMode{ResumeModeRestartFresh},
	}

	if raw, err := os.ReadFile(canonicalPath); err == nil {
		var out DiscoveryOutput
		if json.Unmarshal(raw, &out) == nil && out.SchemaVersion != "" {
			opts.HasCanonical = true
			opts.CanonicalPreview = &DiscoveryOutputSummary{
				ActorCount:        len(out.Actors),
				PriorityCount:     len(out.Priorities),
				OpenQuestionCount: len(out.OpenQuestions),
				ConstraintCount:   len(out.Constraints),
			}
			opts.AvailableModes = append([]ResumeMode{ResumeModeSkipToGate}, opts.AvailableModes...)
		}
	}

	// Per-provider outputs: try current round first, then fall back to round 1
	// (matches the existing checkDiscoveryArtefacts behaviour).
	for _, r := range []int{round, 1} {
		c := filepath.Join(specDir, VersionedFilename("discovery-output", "claude", r, ".json"))
		x := filepath.Join(specDir, VersionedFilename("discovery-output", "codex", r, ".json"))
		if _, err := os.Stat(c); err == nil {
			opts.HasClaude = true
			claudePath = c
		}
		if _, err := os.Stat(x); err == nil {
			opts.HasCodex = true
			codexPath = x
		}
		if opts.HasClaude || opts.HasCodex {
			break
		}
	}
	_ = claudePath
	_ = codexPath

	if opts.HasClaude && opts.HasCodex {
		// Insert replay_merge just after skip_to_gate (if present) or at head.
		opts.AvailableModes = insertMode(opts.AvailableModes, ResumeModeReplayMerge)
	}

	if !opts.HasCanonical && !opts.HasClaude && !opts.HasCodex {
		return nil
	}
	return opts
}

// drafterOutputPreview is a lightweight summary of a DrafterOutput for UI
// display. Kept local so we don't introduce a new exported type.
type drafterOutputPreview struct {
	UserStoryCount    int `json:"user_story_count"`
	BDDScenarioCount  int `json:"bdd_scenario_count"`
	FRCount           int `json:"fr_count"`
	TestCount         int `json:"test_count"`
	AmbiguityWarnings int `json:"ambiguity_warnings"`
}

// probeDraftingResume inspects the spec directory for drafter artefacts.
// Returns nil when no drafter artefacts of any kind are present.
func probeDraftingResume(specDir string, round int) *StageResumeOptions {
	canonicalPath := filepath.Join(specDir, "drafter-output.json")

	opts := &StageResumeOptions{
		Stage:          StateDrafting.String(),
		NextGate:       StateHumanGate2.String(),
		AvailableModes: []ResumeMode{ResumeModeRestartFresh},
	}

	if raw, err := os.ReadFile(canonicalPath); err == nil {
		var out DrafterOutput
		if json.Unmarshal(raw, &out) == nil && out.SchemaVersion != "" {
			opts.HasCanonical = true
			opts.CanonicalPreview = &drafterOutputPreview{
				UserStoryCount:    out.StructuralSummary.UserStoryCount,
				BDDScenarioCount:  out.StructuralSummary.BDDScenarioCount,
				FRCount:           out.StructuralSummary.FRCount,
				TestCount:         out.StructuralSummary.TestCount,
				AmbiguityWarnings: len(out.AmbiguityWarnings),
			}
			opts.AvailableModes = append([]ResumeMode{ResumeModeSkipToGate}, opts.AvailableModes...)
		}
	}

	// Per-provider drafter outputs: try current-round and round-1 versioned
	// filenames. The drafter versions are indexed by Gate2RedraftCount+1, but
	// we probe round==Round first then fall back to 1.
	for _, r := range []int{round, 1} {
		c := filepath.Join(specDir, VersionedFilename("drafter-output", "claude", r, ".json"))
		x := filepath.Join(specDir, VersionedFilename("drafter-output", "codex", r, ".json"))
		if _, err := os.Stat(c); err == nil {
			opts.HasClaude = true
		}
		if _, err := os.Stat(x); err == nil {
			opts.HasCodex = true
		}
		if opts.HasClaude || opts.HasCodex {
			break
		}
	}

	if opts.HasClaude && opts.HasCodex {
		opts.AvailableModes = insertMode(opts.AvailableModes, ResumeModeReplayMerge)
	}

	if !opts.HasCanonical && !opts.HasClaude && !opts.HasCodex {
		return nil
	}
	return opts
}

// reviewingPreview is a lightweight summary of the reviewing stage for the UI.
type reviewingPreview struct {
	ReviewerFileCount int `json:"reviewer_file_count"`
	MergedFindings    int `json:"merged_findings,omitempty"`
}

// probeReviewingResume inspects the spec directory for reviewer artefacts at
// the given round. The "canonical" output for reviewing is the merged
// findings file (merged-findings-round-N.json); the "per-provider" outputs
// are the individual review-{a,b,c,d}-round-N.json files.
//
// Modes offered:
//   - restart_fresh: always.
//   - replay_merge:  when at least 2 reviewer outputs exist on disk (so the
//     dedup merge has something to work with). Runs ReplayReviewMerge.
//   - skip_to_gate:  when the merged-findings file already exists. Advances
//     past reviewing into REVISING (the next active state — not a human
//     gate, but we reuse the NextGate field for "what to transition to").
func probeReviewingResume(specDir string, round int) *StageResumeOptions {
	opts := &StageResumeOptions{
		Stage:          StateReviewing.String(),
		NextGate:       StateRevising.String(),
		AvailableModes: []ResumeMode{ResumeModeRestartFresh},
	}

	// Count reviewer output files for this round. Keep this letter list in
	// sync with reviewerGroupLetter in prompts.go.
	reviewerCount := 0
	for _, letter := range []string{"a", "b", "c", "d", "e"} {
		p := filepath.Join(specDir, fmt.Sprintf("review-%s-round-%d.json", letter, round))
		if _, err := os.Stat(p); err == nil {
			reviewerCount++
		}
	}

	// Canonical: merged findings file.
	mergedPath := filepath.Join(specDir, fmt.Sprintf("merged-findings-round-%d.json", round))
	var mergedFindingsCount int
	if raw, err := os.ReadFile(mergedPath); err == nil {
		// Don't require a specific type; just that it parses as JSON.
		var probe map[string]interface{}
		if json.Unmarshal(raw, &probe) == nil {
			opts.HasCanonical = true
			if total, ok := probe["total_after_dedup"].(float64); ok {
				mergedFindingsCount = int(total)
			}
			opts.AvailableModes = insertMode(opts.AvailableModes, ResumeModeSkipToGate)
		}
	}

	if reviewerCount >= 2 {
		opts.AvailableModes = insertMode(opts.AvailableModes, ResumeModeReplayMerge)
	}

	if reviewerCount == 0 && !opts.HasCanonical {
		return nil
	}

	opts.CanonicalPreview = &reviewingPreview{
		ReviewerFileCount: reviewerCount,
		MergedFindings:    mergedFindingsCount,
	}
	return opts
}

// revisingPreview is a lightweight summary of the revising stage for the UI.
type revisingPreview struct {
	HasRevisionOutput bool `json:"has_revision_output"`
}

// probeRevisingResume inspects the spec directory for revision artefacts at
// the given round. REVISING is a single-agent stage (no dual-provider, no
// merge), so the only modes offered are skip_to_gate (advances to JUDGING)
// and restart_fresh. Returns nil when no revision output exists.
func probeRevisingResume(specDir string, round int) *StageResumeOptions {
	revisionPath := filepath.Join(specDir, fmt.Sprintf("revision-round-%d.json", round))
	if raw, err := os.ReadFile(revisionPath); err == nil {
		var probe map[string]interface{}
		if json.Unmarshal(raw, &probe) == nil {
			return &StageResumeOptions{
				Stage:          StateRevising.String(),
				NextGate:       StateJudging.String(),
				AvailableModes: []ResumeMode{ResumeModeSkipToGate, ResumeModeRestartFresh},
				HasCanonical:   true,
				CanonicalPreview: &revisingPreview{
					HasRevisionOutput: true,
				},
			}
		}
	}
	return nil
}

// judgingPreview is a lightweight summary of the judging stage for the UI.
type judgingPreview struct {
	Verdict string `json:"verdict,omitempty"`
}

// probeJudgingResume inspects the spec directory for judge artefacts at the
// given round. The judge produces a verdict that the orchestrator uses to
// decide the next state (next-round reviewing, HUMAN_GATE_FINAL, etc.), so
// skip_to_gate is intentionally NOT offered here — the orchestrator must
// re-evaluate the verdict to pick the correct next state. Only restart_fresh
// is meaningful. Returns nil when no judge output exists.
func probeJudgingResume(specDir string, round int) *StageResumeOptions {
	judgePath := filepath.Join(specDir, fmt.Sprintf("judge-round-%d.json", round))
	if raw, err := os.ReadFile(judgePath); err == nil {
		var probe map[string]interface{}
		if json.Unmarshal(raw, &probe) == nil {
			verdict, _ := probe["verdict"].(string)
			return &StageResumeOptions{
				Stage:          StateJudging.String(),
				AvailableModes: []ResumeMode{ResumeModeRestartFresh},
				HasCanonical:   true,
				CanonicalPreview: &judgingPreview{
					Verdict: verdict,
				},
			}
		}
	}
	return nil
}

// taskifyPreview is a lightweight summary of the taskify stage for the UI.
type taskifyPreview struct {
	TaskCount int `json:"task_count,omitempty"`
}

// probeTaskifyResume inspects the workspace .tasks directory for a task graph
// file for the given feature. The taskify output path is
// {workspaceDir}/.tasks/{featureName}.task.json — it lives OUTSIDE the spec
// directory, unlike every other stage artefact.
//
// Modes offered:
//   - restart_fresh: always (when at least the file exists).
//   - skip_to_gate:  when the task graph parses and has at least one task.
//     Advances past taskify into TASK_HUMAN_GATE.
//
// Returns nil when no task graph file exists.
func probeTaskifyResume(workspaceDir, featureName string) *StageResumeOptions {
	taskGraphPath := filepath.Join(workspaceDir, ".tasks", featureName+".task.json")
	raw, err := os.ReadFile(taskGraphPath)
	if err != nil {
		return nil
	}

	var probe map[string]interface{}
	if json.Unmarshal(raw, &probe) != nil {
		// File exists but is unparseable — still offer restart_fresh, mark
		// canonical as absent.
		return &StageResumeOptions{
			Stage:          StateTaskify.String(),
			NextGate:       StateTaskHumanGate.String(),
			AvailableModes: []ResumeMode{ResumeModeRestartFresh},
		}
	}

	taskCount := 0
	if tasks, ok := probe["tasks"].([]interface{}); ok {
		taskCount = len(tasks)
	}

	opts := &StageResumeOptions{
		Stage:          StateTaskify.String(),
		NextGate:       StateTaskHumanGate.String(),
		AvailableModes: []ResumeMode{ResumeModeRestartFresh},
		HasCanonical:   true,
		CanonicalPreview: &taskifyPreview{
			TaskCount: taskCount,
		},
	}

	// Only offer skip_to_gate when the graph is non-empty.
	if taskCount > 0 {
		opts.AvailableModes = insertMode(opts.AvailableModes, ResumeModeSkipToGate)
	}

	return opts
}

// insertMode inserts mode into modes at the natural display position:
// skip_to_gate first, then replay_merge, then restart_fresh.
func insertMode(modes []ResumeMode, mode ResumeMode) []ResumeMode {
	for _, existing := range modes {
		if existing == mode {
			return modes
		}
	}
	// Find insertion index by desired ordering.
	order := map[ResumeMode]int{
		ResumeModeSkipToGate:   0,
		ResumeModeReplayMerge:  1,
		ResumeModeRestartFresh: 2,
	}
	inserted := false
	out := make([]ResumeMode, 0, len(modes)+1)
	for _, m := range modes {
		if !inserted && order[mode] < order[m] {
			out = append(out, mode)
			inserted = true
		}
		out = append(out, m)
	}
	if !inserted {
		out = append(out, mode)
	}
	return out
}

// ValidateResumeMode returns an error if the given mode is not one of the
// known values or is not available for the given stage options.
func ValidateResumeMode(mode ResumeMode, stage StageResumeOptions) error {
	if mode == "" {
		return fmt.Errorf("resume mode must be specified")
	}
	for _, m := range stage.AvailableModes {
		if m == mode {
			return nil
		}
	}
	return fmt.Errorf("resume mode %q is not available for stage %s (available: %v)", mode, stage.Stage, stage.AvailableModes)
}
