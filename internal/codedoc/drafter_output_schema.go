package codedoc

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
    "as_implemented_report",
    "architecture_diagrams",
    "code_audit",
    "doc_updates",
    "structural_summary"
  ],
  "additionalProperties": false,
  "properties": {
    "schema_version": { "type": "string" },
    "agent": { "type": "string" },
    "as_implemented_report": {
      "type": "object",
      "required": ["file_path", "sections"],
      "additionalProperties": false,
      "properties": {
        "file_path": { "type": "string" },
        "sections":  { "type": "array", "items": { "type": "string" } }
      }
    },
    "architecture_diagrams": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["file_path", "diagram_type", "mermaid_content", "mermaid_valid"],
        "additionalProperties": false,
        "properties": {
          "file_path":       { "type": "string" },
          "diagram_type":    { "type": "string" },
          "mermaid_content": { "type": "string" },
          "mermaid_valid":   { "type": "boolean" }
        }
      }
    },
    "code_audit": {
      "type": "object",
      "required": ["json_file_path", "report_file_path", "findings", "summary"],
      "additionalProperties": false,
      "properties": {
        "json_file_path":   { "type": "string" },
        "report_file_path": { "type": "string" },
        "findings": {
          "type": "array",
          "items": {
            "type": "object",
            "required": ["id", "type", "severity", "file_path", "line_number", "symbol", "description", "evidence"],
            "additionalProperties": false,
            "properties": {
              "id":          { "type": "string" },
              "type":        { "type": "string" },
              "severity": {
                "type": "string",
                "enum": ["critical", "major", "minor", "observation"]
              },
              "file_path":   { "type": "string" },
              "line_number": { "type": "integer", "minimum": 0 },
              "symbol":      { "type": "string" },
              "description": { "type": "string" },
              "evidence":    { "type": "string" }
            }
          }
        },
        "summary": {
          "type": "object",
          "required": ["dead_code", "stubs", "todos", "fixmes", "empty_catches", "non_wired"],
          "additionalProperties": false,
          "properties": {
            "dead_code":     { "type": "integer", "minimum": 0 },
            "stubs":         { "type": "integer", "minimum": 0 },
            "todos":         { "type": "integer", "minimum": 0 },
            "fixmes":        { "type": "integer", "minimum": 0 },
            "empty_catches": { "type": "integer", "minimum": 0 },
            "non_wired":     { "type": "integer", "minimum": 0 }
          }
        }
      }
    },
    "doc_updates": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["file_path", "action", "sections_changed", "reason"],
        "additionalProperties": false,
        "properties": {
          "file_path":        { "type": "string" },
          "action":           { "type": "string" },
          "sections_changed": { "type": "array", "items": { "type": "string" } },
          "reason":           { "type": "string" }
        }
      }
    },
    "structural_summary": {
      "type": "object",
      "required": [
        "report_sections",
        "diagram_count",
        "audit_finding_count",
        "doc_updates",
        "modules_documented",
        "entry_points_documented"
      ],
      "additionalProperties": false,
      "properties": {
        "report_sections":         { "type": "integer", "minimum": 0 },
        "diagram_count":           { "type": "integer", "minimum": 0 },
        "audit_finding_count":     { "type": "integer", "minimum": 0 },
        "doc_updates":             { "type": "integer", "minimum": 0 },
        "modules_documented":      { "type": "integer", "minimum": 0 },
        "entry_points_documented": { "type": "integer", "minimum": 0 }
      }
    }
  }
}`)
}
