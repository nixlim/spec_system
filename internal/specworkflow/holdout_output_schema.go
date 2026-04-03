package specworkflow

import (
	"fmt"
	"path/filepath"
	"strings"
)

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

// ValidateHoldoutOutput checks a HoldoutOutput for required fields and
// expected contract values.
func ValidateHoldoutOutput(o *HoldoutOutput, expectedRound int, expectedHoldoutFile string) []error {
	var errs []error

	if strings.TrimSpace(o.SchemaVersion) == "" {
		errs = append(errs, fmt.Errorf("schema_version must not be empty"))
	}
	if strings.TrimSpace(o.Agent) == "" {
		errs = append(errs, fmt.Errorf("agent must not be empty"))
	}
	if o.Round <= 0 {
		errs = append(errs, fmt.Errorf("round must be > 0, got %d", o.Round))
	}
	if expectedRound > 0 && o.Round != expectedRound {
		errs = append(errs, fmt.Errorf("round must be %d, got %d", expectedRound, o.Round))
	}
	if o.ScenarioCount <= 0 {
		errs = append(errs, fmt.Errorf("scenario_count must be > 0, got %d", o.ScenarioCount))
	}
	if len(o.Categories) == 0 {
		errs = append(errs, fmt.Errorf("categories must not be empty"))
	}
	if strings.TrimSpace(o.HoldoutFile) == "" {
		errs = append(errs, fmt.Errorf("holdout_file must not be empty"))
	}
	if expectedHoldoutFile != "" && filepath.Clean(o.HoldoutFile) != filepath.Clean(expectedHoldoutFile) {
		errs = append(errs, fmt.Errorf("holdout_file must be %s, got %s", expectedHoldoutFile, o.HoldoutFile))
	}

	return errs
}
