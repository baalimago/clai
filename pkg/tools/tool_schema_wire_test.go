package tools

import (
	"bytes"
	"encoding/json"
	"testing"
)

// TestToolSchemasMarshalAsJSONSchema pins that every tool reaches a provider as
// valid JSON Schema: an absent Required or Properties must not encode as null
// (2026-10-30, deepseek 400 on audio_record).
func TestToolSchemasMarshalAsJSONSchema(t *testing.T) {
	for _, tool := range exportedTools() {
		spec := tool.Specification()
		if spec.Inputs == nil {
			t.Fatalf("%q has no input schema", spec.Name)
		}
		raw, err := json.Marshal(spec.Inputs)
		if err != nil {
			t.Fatalf("%q: marshal input schema: %v", spec.Name, err)
		}
		var schema struct {
			Type       string          `json:"type"`
			Required   json.RawMessage `json:"required"`
			Properties json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatalf("%q: decode input schema: %v", spec.Name, err)
		}
		if schema.Type != "object" {
			t.Fatalf("%q: type = %q, want object", spec.Name, schema.Type)
		}
		if !bytes.HasPrefix(schema.Required, []byte("[")) {
			t.Fatalf("%q: required = %s, want an array", spec.Name, schema.Required)
		}
		if !bytes.HasPrefix(schema.Properties, []byte("{")) {
			t.Fatalf("%q: properties = %s, want an object", spec.Name, schema.Properties)
		}
	}
}
