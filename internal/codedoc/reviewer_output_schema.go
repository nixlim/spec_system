package codedoc

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
    "structural_integrity"
  ],
  "additionalProperties": false,
  "properties": {
    "schema_version": { "type": "string" },
    "agent": { "type": "string" },
    "round": { "type": "integer", "minimum": 1 },
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
          "status",
          "impact",
          "recommendation",
          "lens",
          "affected_section"
        ],
        "additionalProperties": false,
        "properties": {
          "id":               { "type": "string" },
          "description":      { "type": "string" },
          "severity": {
            "type": "string",
            "enum": ["critical", "major", "minor", "observation"]
          },
          "status": {
            "type": "string",
            "enum": ["open", "resolved", "wontfix"]
          },
          "impact":           { "type": "string" },
          "recommendation":   { "type": "string" },
          "lens":             { "type": "string" },
          "affected_section": { "type": "string" },
          "affected_file":    { "type": "string" }
        }
      }
    },
    "structural_integrity": {
      "type": "object",
      "required": ["performed", "checks"],
      "additionalProperties": false,
      "properties": {
        "performed": { "type": "boolean" },
        "checks": {
          "type": "array",
          "items": {
            "type": "object",
            "required": ["name", "passed", "details"],
            "additionalProperties": false,
            "properties": {
              "name":    { "type": "string" },
              "passed":  { "type": "boolean" },
              "details": { "type": "string" }
            }
          }
        }
      }
    }
  }
}`)
}
