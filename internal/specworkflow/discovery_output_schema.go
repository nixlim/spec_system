package specworkflow

// DiscoveryOutputSchema returns a JSON Schema document (as raw bytes) that
// describes the expected structure of DiscoveryOutput. All object types include
// "additionalProperties": false as required by OpenAI's structured output API.
func DiscoveryOutputSchema() []byte {
	return []byte(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": [
    "schema_version",
    "agent",
    "actors",
    "problem_statement",
    "scope",
    "constraints",
    "integration_points",
    "priorities",
    "assumptions",
    "open_questions"
  ],
  "properties": {
    "schema_version": {
      "type": "string"
    },
    "agent": {
      "type": "string"
    },
    "actors": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["name", "type", "description"],
        "properties": {
          "name": { "type": "string" },
          "type": { "type": "string", "enum": ["human", "system", "external"] },
          "description": { "type": "string" }
        },
        "additionalProperties": false
      }
    },
    "problem_statement": {
      "type": "string"
    },
    "scope": {
      "type": "object",
      "required": ["in_scope", "out_of_scope"],
      "properties": {
        "in_scope": { "type": "array", "items": { "type": "string" } },
        "out_of_scope": { "type": "array", "items": { "type": "string" } }
      },
      "additionalProperties": false
    },
    "constraints": {
      "type": "array",
      "items": { "type": "string" }
    },
    "integration_points": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["system", "description", "direction"],
        "properties": {
          "system": { "type": "string" },
          "description": { "type": "string" },
          "direction": { "type": "string", "enum": ["inbound", "outbound", "bidirectional"] }
        },
        "additionalProperties": false
      }
    },
    "priorities": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["item", "priority", "rationale"],
        "properties": {
          "item": { "type": "string" },
          "priority": { "type": "string", "enum": ["P0", "P1", "P2", "P3", "P4"] },
          "rationale": { "type": "string" }
        },
        "additionalProperties": false
      }
    },
    "assumptions": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["assumption", "confidence", "question_for_user"],
        "properties": {
          "assumption": { "type": "string" },
          "confidence": { "type": "string", "enum": ["high", "medium", "low"] },
          "question_for_user": { "type": ["string", "null"] }
        },
        "additionalProperties": false
      }
    },
    "open_questions": {
      "type": "array",
      "items": { "type": "string" }
    }
  },
  "additionalProperties": false
}`)
}
