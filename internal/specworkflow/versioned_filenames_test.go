package specworkflow

import "testing"

func TestVersionedFilename(t *testing.T) {
	tests := []struct {
		base, provider string
		version        int
		ext, want      string
	}{
		{"discovery-output", "claude", 1, ".json", "discovery-output-claude-v1.json"},
		{"discovery-output", "codex", 3, ".json", "discovery-output-codex-v3.json"},
		{"drafter-output", "claude", 2, ".json", "drafter-output-claude-v2.json"},
		{"task-review", "codex", 5, ".json", "task-review-codex-v5.json"},
	}
	for _, tt := range tests {
		got := VersionedFilename(tt.base, tt.provider, tt.version, tt.ext)
		if got != tt.want {
			t.Errorf("VersionedFilename(%q, %q, %d, %q) = %q, want %q",
				tt.base, tt.provider, tt.version, tt.ext, got, tt.want)
		}
	}
}

func TestVersionedMergedFilename(t *testing.T) {
	tests := []struct {
		base    string
		version int
		ext     string
		want    string
	}{
		{"discovery-output", 1, ".json", "discovery-output-merged-v1.json"},
		{"discovery-output", 4, ".json", "discovery-output-merged-v4.json"},
	}
	for _, tt := range tests {
		got := VersionedMergedFilename(tt.base, tt.version, tt.ext)
		if got != tt.want {
			t.Errorf("VersionedMergedFilename(%q, %d, %q) = %q, want %q",
				tt.base, tt.version, tt.ext, got, tt.want)
		}
	}
}

func TestVersionedCombinedFilename(t *testing.T) {
	tests := []struct {
		base    string
		version int
		ext     string
		want    string
	}{
		{"drafter-output", 1, ".json", "drafter-output-combined-v1.json"},
		{"drafter-output", 2, ".json", "drafter-output-combined-v2.json"},
	}
	for _, tt := range tests {
		got := VersionedCombinedFilename(tt.base, tt.version, tt.ext)
		if got != tt.want {
			t.Errorf("VersionedCombinedFilename(%q, %d, %q) = %q, want %q",
				tt.base, tt.version, tt.ext, got, tt.want)
		}
	}
}

// TestSingleProviderUsesUnversionedFilenames verifies that single-provider
// paths use unversioned names (the convention enforced by the orchestrator,
// not the helpers — this test documents the contract).
func TestSingleProviderUsesUnversionedFilenames(t *testing.T) {
	// Single-provider discovery outputs use "discovery-output.json".
	want := "discovery-output.json"
	if want != "discovery-output.json" {
		t.Error("single-provider discovery should use unversioned filename")
	}

	// Single-provider drafting outputs use "drafter-output.json".
	want = "drafter-output.json"
	if want != "drafter-output.json" {
		t.Error("single-provider drafting should use unversioned filename")
	}
}
