package specworkflow

import (
	"encoding/json"
	"testing"
)

func TestHoldoutOutputSchema_ValidJSON(t *testing.T) {
	raw := HoldoutOutputSchema()
	var parsed map[string]interface{}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("HoldoutOutputSchema is not valid JSON: %v", err)
	}
}

func TestHoldoutOutputSchema_RequiredFields(t *testing.T) {
	raw := HoldoutOutputSchema()
	var schema map[string]interface{}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	required, ok := schema["required"].([]interface{})
	if !ok {
		t.Fatal("schema missing 'required' array")
	}
	want := map[string]bool{
		"scenario_count": false,
		"categories":     false,
		"holdout_file":   false,
	}
	for _, r := range required {
		s, _ := r.(string)
		if _, exists := want[s]; exists {
			want[s] = true
		}
	}
	for field, found := range want {
		if !found {
			t.Errorf("expected %q in required array", field)
		}
	}
}
