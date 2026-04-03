package specworkflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckDiscoveryArtefacts_NoArtefacts(t *testing.T) {
	dir := t.TempDir()
	result := checkDiscoveryArtefacts(dir, 1)
	if result != nil {
		t.Error("expected nil when no artefacts exist")
	}
}

func TestCheckDiscoveryArtefacts_MergedOnly(t *testing.T) {
	dir := t.TempDir()

	// Write a valid merged output.
	output := DiscoveryOutput{
		SchemaVersion: "1.0",
		Agent:         "merged",
		Actors:        []Actor{{Name: "User", Type: "human", Description: "A user"}},
	}
	data, _ := json.Marshal(output)
	os.WriteFile(filepath.Join(dir, "discovery-output.json"), data, 0o644)

	result := checkDiscoveryArtefacts(dir, 1)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.HasMergedOutput {
		t.Error("expected HasMergedOutput=true")
	}
	if result.HasClaudeOutput {
		t.Error("expected HasClaudeOutput=false")
	}
	if result.HasCodexOutput {
		t.Error("expected HasCodexOutput=false")
	}
	if result.CanReplayMerge {
		t.Error("expected CanReplayMerge=false")
	}
	if result.MergedPreview == nil {
		t.Fatal("expected MergedPreview to be set")
	}
	if result.MergedPreview.ActorCount != 1 {
		t.Errorf("expected 1 actor, got %d", result.MergedPreview.ActorCount)
	}
}

func TestCheckDiscoveryArtefacts_DualProviderOutputs(t *testing.T) {
	dir := t.TempDir()

	// Write per-provider outputs.
	output := DiscoveryOutput{
		SchemaVersion: "1.0",
		Agent:         "discovery",
	}
	data, _ := json.Marshal(output)

	claudePath := filepath.Join(dir, VersionedFilename("discovery-output", "claude", 1, ".json"))
	codexPath := filepath.Join(dir, VersionedFilename("discovery-output", "codex", 1, ".json"))
	os.WriteFile(claudePath, data, 0o644)
	os.WriteFile(codexPath, data, 0o644)

	result := checkDiscoveryArtefacts(dir, 1)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.HasMergedOutput {
		t.Error("expected HasMergedOutput=false (no canonical file)")
	}
	if !result.HasClaudeOutput {
		t.Error("expected HasClaudeOutput=true")
	}
	if !result.HasCodexOutput {
		t.Error("expected HasCodexOutput=true")
	}
	if !result.CanReplayMerge {
		t.Error("expected CanReplayMerge=true")
	}
}

func TestCheckDiscoveryArtefacts_AllArtefacts(t *testing.T) {
	dir := t.TempDir()

	output := DiscoveryOutput{
		SchemaVersion: "1.0",
		Agent:         "merged",
		Actors:        []Actor{{Name: "Admin", Type: "human", Description: "Administrator"}},
		Priorities:    []Priority{{Item: "Auth", Priority: "P0", Rationale: "Critical"}},
		OpenQuestions: []string{"How does X work?"},
		Constraints:   []string{"Must support 10k users"},
	}
	data, _ := json.Marshal(output)

	os.WriteFile(filepath.Join(dir, "discovery-output.json"), data, 0o644)
	os.WriteFile(filepath.Join(dir, VersionedFilename("discovery-output", "claude", 1, ".json")), data, 0o644)
	os.WriteFile(filepath.Join(dir, VersionedFilename("discovery-output", "codex", 1, ".json")), data, 0o644)

	result := checkDiscoveryArtefacts(dir, 1)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.HasMergedOutput {
		t.Error("expected HasMergedOutput=true")
	}
	if !result.CanReplayMerge {
		t.Error("expected CanReplayMerge=true")
	}

	// Check summary.
	if result.MergedPreview.ActorCount != 1 {
		t.Errorf("expected 1 actor, got %d", result.MergedPreview.ActorCount)
	}
	if result.MergedPreview.PriorityCount != 1 {
		t.Errorf("expected 1 priority, got %d", result.MergedPreview.PriorityCount)
	}
	if result.MergedPreview.OpenQuestionCount != 1 {
		t.Errorf("expected 1 open question, got %d", result.MergedPreview.OpenQuestionCount)
	}
	if result.MergedPreview.ConstraintCount != 1 {
		t.Errorf("expected 1 constraint, got %d", result.MergedPreview.ConstraintCount)
	}
}

func TestCheckDiscoveryArtefacts_InvalidJSON(t *testing.T) {
	dir := t.TempDir()

	// Write invalid JSON as merged output.
	os.WriteFile(filepath.Join(dir, "discovery-output.json"), []byte("not json"), 0o644)

	result := checkDiscoveryArtefacts(dir, 1)
	if result != nil {
		t.Error("expected nil when merged output is invalid JSON")
	}
}
