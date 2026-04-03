package specworkflow

// DrafterOutputSchema returns a JSON Schema document (as raw bytes) that
// describes the expected structure of DrafterOutput. All object types include
// "additionalProperties": false as required by OpenAI's structured output API.
func DrafterOutputSchema() []byte {
	return []byte(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": [
    "schema_version",
    "agent",
    "spec_file",
    "holdout_file",
    "ambiguity_warnings",
    "structural_summary"
  ],
  "properties": {
    "schema_version": {
      "type": "string"
    },
    "agent": {
      "type": "string"
    },
    "spec_file": {
      "type": "string"
    },
    "holdout_file": {
      "type": "string"
    },
    "ambiguity_warnings": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["id", "section", "ambiguity", "agent_assumption", "question_for_user"],
        "properties": {
          "id": { "type": "string" },
          "section": { "type": "string" },
          "ambiguity": { "type": "string" },
          "agent_assumption": { "type": "string" },
          "question_for_user": { "type": "string" }
        },
        "additionalProperties": false
      }
    },
    "structural_summary": {
      "type": "object",
      "required": ["user_story_count", "bdd_scenario_count", "fr_count", "test_count"],
      "properties": {
        "user_story_count": { "type": "integer" },
        "bdd_scenario_count": { "type": "integer" },
        "fr_count": { "type": "integer" },
        "test_count": { "type": "integer" }
      },
      "additionalProperties": false
    }
  },
  "additionalProperties": false
}`)
}
