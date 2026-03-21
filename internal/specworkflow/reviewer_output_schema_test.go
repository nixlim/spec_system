package specworkflow

import (
	"encoding/json"
	"testing"
)

func TestReviewerOutputSchema_ValidJSON(t *testing.T) {
	raw := ReviewerOutputSchema()
	var parsed map[string]interface{}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("ReviewerOutputSchema is not valid JSON: %v", err)
	}
}

func TestReviewerOutputSchema_RequiredFields(t *testing.T) {
	raw := ReviewerOutputSchema()
	var schema map[string]interface{}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	required, ok := schema["required"].([]interface{})
	if !ok {
		t.Fatal("schema missing 'required' array")
	}
	want := map[string]bool{
		"findings":       false,
		"lenses_applied": false,
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

func TestReviewerOutputSchema_FindingFields(t *testing.T) {
	raw := ReviewerOutputSchema()
	var schema map[string]interface{}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	props := schema["properties"].(map[string]interface{})
	findings := props["findings"].(map[string]interface{})
	items := findings["items"].(map[string]interface{})
	required, ok := items["required"].([]interface{})
	if !ok {
		t.Fatal("findings items missing 'required' array")
	}
	want := map[string]bool{
		"id":               false,
		"description":      false,
		"severity":         false,
		"impact":           false,
		"recommendation":   false,
		"lens":             false,
		"affected_section": false,
	}
	for _, r := range required {
		s, _ := r.(string)
		if _, exists := want[s]; exists {
			want[s] = true
		}
	}
	for field, found := range want {
		if !found {
			t.Errorf("expected %q in finding required fields", field)
		}
	}
}
