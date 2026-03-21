package specworkflow

// ReviewerOutputSchema returns a JSON Schema document (as raw bytes) that
// describes the expected structure of ReviewerOutput. All object types include
// "additionalProperties": false as required by OpenAI's structured output API.
func ReviewerOutputSchema() []byte {
	return []byte(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": [
    "schema_version",
    "agent",
    "round",
    "lenses_applied",
    "findings",
    "structural_integrity",
    "markdown_report_file"
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
    "lenses_applied": {
      "type": "array",
      "items": { "type": "string" },
      "minItems": 1
    },
    "findings": {
      "type": "array",
      "items": {
        "type": "object",
        "required": [
          "id",
          "description",
          "severity",
          "impact",
          "recommendation",
          "lens",
          "affected_section",
          "constitution_principle"
        ],
        "properties": {
          "id": { "type": "string" },
          "description": { "type": "string" },
          "severity": {
            "type": "string",
            "enum": ["CRITICAL", "MAJOR", "MINOR", "OBSERVATION"]
          },
          "impact": { "type": "string" },
          "recommendation": { "type": "string" },
          "lens": { "type": "string" },
          "affected_section": { "type": "string" },
          "constitution_principle": { "type": ["string", "null"] }
        },
        "additionalProperties": false
      }
    },
    "structural_integrity": {
      "type": "object",
      "required": ["performed", "checks"],
      "properties": {
        "performed": { "type": "boolean" },
        "checks": {
          "type": "array",
          "items": {
            "type": "object",
            "required": ["check", "result", "detail"],
            "properties": {
              "check": { "type": "string" },
              "result": { "type": "string", "enum": ["PASS", "FAIL"] },
              "detail": { "type": ["string", "null"] }
            },
            "additionalProperties": false
          }
        }
      },
      "additionalProperties": false
    },
    "markdown_report_file": {
      "type": "string"
    }
  },
  "additionalProperties": false
}`)
}
