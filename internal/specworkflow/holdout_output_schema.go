package specworkflow

// HoldoutOutput is the structured output schema for holdout scenario
// generation agents.
type HoldoutOutput struct {
	// SchemaVersion identifies the output schema version.
	SchemaVersion string `json:"schema_version"`
	// Agent is the name of the agent that produced this output.
	Agent string `json:"agent"`
	// Round is the generation iteration number.
	Round int `json:"round"`
	// ScenarioCount is the number of holdout scenarios generated.
	ScenarioCount int `json:"scenario_count"`
	// Categories lists the categories of generated scenarios.
	Categories []string `json:"categories"`
	// HoldoutFile is the path to the generated holdout file.
	HoldoutFile string `json:"holdout_file"`
}

// HoldoutOutputSchema returns a JSON Schema document (as raw bytes) that
// describes the expected structure of HoldoutOutput.
func HoldoutOutputSchema() []byte {
	return []byte(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": [
    "schema_version",
    "agent",
    "round",
    "scenario_count",
    "categories",
    "holdout_file"
  ],
  "properties": {
    "schema_version": {
      "type": "string"
    },
    "agent": {
      "type": "string"
    },
    "round": {
      "type": "integer",
      "minimum": 1
    },
    "scenario_count": {
      "type": "integer"
    },
    "categories": {
      "type": "array",
      "items": { "type": "string" }
    },
    "holdout_file": {
      "type": "string"
    }
  },
  "additionalProperties": false
}`)
}
