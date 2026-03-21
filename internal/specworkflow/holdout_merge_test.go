package specworkflow

import (
	"strings"
	"testing"
)

func TestHoldoutMerge_BothProviders(t *testing.T) {
	result := MergeHoldoutMarkdown(1, "claude holdout content", "codex holdout content", true, true)

	if !strings.Contains(result, "## Provider: Claude") {
		t.Error("expected Claude provider header")
	}
	if !strings.Contains(result, "## Provider: Codex") {
		t.Error("expected Codex provider header")
	}
	if !strings.Contains(result, "claude holdout content") {
		t.Error("expected claude content in merged output")
	}
	if !strings.Contains(result, "codex holdout content") {
		t.Error("expected codex content in merged output")
	}
	if !strings.Contains(result, "Merged") {
		t.Error("expected 'Merged' in title when both providers succeed")
	}
}

func TestHoldoutMerge_SingleProvider(t *testing.T) {
	result := MergeHoldoutMarkdown(2, "claude only content", "", true, false)

	if !strings.Contains(result, "## Provider: Claude") {
		t.Error("expected Claude provider header")
	}
	if strings.Contains(result, "## Provider: Codex") {
		t.Error("should not have Codex provider header when codex failed")
	}
	if !strings.Contains(result, "Codex unavailable or failed") {
		t.Error("expected note about codex being unavailable")
	}
	if !strings.Contains(result, "claude only content") {
		t.Error("expected claude content in output")
	}
}

func TestHoldoutMerge_NoDeduplicate(t *testing.T) {
	claudeMD := "- Test case A\n- Test case B"
	codexMD := "- Test case A\n- Test case C"

	result := MergeHoldoutMarkdown(1, claudeMD, codexMD, true, true)

	// Both occurrences of "Test case A" should be present (no dedup).
	count := strings.Count(result, "Test case A")
	if count != 2 {
		t.Errorf("expected 2 occurrences of 'Test case A' (no dedup), got %d", count)
	}
	if !strings.Contains(result, "Test case B") {
		t.Error("expected 'Test case B' in output")
	}
	if !strings.Contains(result, "Test case C") {
		t.Error("expected 'Test case C' in output")
	}
}

func TestHoldoutMerge_CodexOnly(t *testing.T) {
	result := MergeHoldoutMarkdown(3, "", "codex only content", false, true)

	if !strings.Contains(result, "## Provider: Codex") {
		t.Error("expected Codex provider header")
	}
	if strings.Contains(result, "## Provider: Claude") {
		t.Error("should not have Claude provider header when claude failed")
	}
	if !strings.Contains(result, "Claude failed") {
		t.Error("expected note about claude failing")
	}
}

func TestHoldoutMerge_NeitherProvider(t *testing.T) {
	result := MergeHoldoutMarkdown(1, "", "", false, false)

	if !strings.Contains(result, "No providers produced output") {
		t.Error("expected error message when neither provider succeeded")
	}
}
