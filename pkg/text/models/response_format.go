package models

// ResponseFormat configures structured output for compatible models.
// Supported types: "text", "json_object", "json_schema".
type ResponseFormat struct {
	// Type of response format: "text", "json_object", or "json_schema".
	Type string
	// Schema is used when Type is "json_schema".
	Schema *JSONSchema
}

// JSONSchema defines the JSON Schema for structured output.
type JSONSchema struct {
	// Name of the schema. Required by the API.
	Name string
	// Description of what the schema represents. Optional.
	Description string
	// Strict enables strict mode. When true, the model will be forced to comply.
	Strict bool
	// Schema is the JSON Schema definition as a map.
	Schema map[string]any
}
