package codedoc

// JudgeOutputSchema returns a JSON Schema document (as raw bytes) that
// describes the expected structure of JudgeOutput. All object types include
// "additionalProperties": false as required by OpenAI's structured output API.
func JudgeOutputSchema() []byte {
	return []byte(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": [
    "schema_version",
    "agent",
    "round",
    "verdict",
    "rationale",
    "issue_updates",
    "downgrades"
  ],
  "additionalProperties": false,
  "properties": {
    "schema_version": { "type": "string" },
    "agent": { "type": "string" },
    "round": { "type": "integer", "minimum": 1 },
    "verdict": {
      "type": "string",
      "enum": ["PASS", "REVISE", "BLOCK"]
    },
    "rationale": { "type": "string" },
    "issue_updates": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["finding_id", "new_status", "reason"],
        "additionalProperties": false,
        "properties": {
          "finding_id": { "type": "string" },
          "new_status": {
            "type": "string",
            "enum": ["open", "resolved", "wontfix"]
          },
          "reason": { "type": "string" }
        }
      }
    },
    "downgrades": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["finding_id", "old_severity", "new_severity", "reason_code"],
        "additionalProperties": false,
        "properties": {
          "finding_id":   { "type": "string" },
          "old_severity": {
            "type": "string",
            "enum": ["critical", "major", "minor", "observation"]
          },
          "new_severity": {
            "type": "string",
            "enum": ["critical", "major", "minor", "observation"]
          },
          "reason_code": { "type": "string" }
        }
      }
    }
  }
}`)
}
