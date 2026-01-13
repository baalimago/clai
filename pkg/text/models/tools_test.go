package models

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCallPatchAndPretty(t *testing.T) {
	// empty -> defaults
	c := Call{}
	c.Patch()
	if c.Type != "function" {
		t.Fatalf("expected default type function, got %q", c.Type)
	}
	if c.Function.Name == "" {
		t.Fatalf("expected function name filled from Name or placeholder")
	}
	if c.Function.Arguments == "" {
		t.Fatalf("expected arguments to be auto-filled with JSON")
	}
	// Test PrettyPrint and JSON on populated object
	inp := Input{"path": "a", "flags": 2}
	c = Call{Name: "ls", Inputs: &inp}
	c.Patch()
	if c.Function.Name != "ls" || c.Type != "function" {
		t.Fatalf("unexpected patch results: %#v", c)
	}

	// Test PrettyPrint output
	pp := c.PrettyPrint()
	if !strings.Contains(pp, "Call: 'ls'") {
		t.Errorf("PrettyPrint expected to contain name 'ls', got %q", pp)
	}
	// Since map iteration is random, we check if keys exist in the string
	if !strings.Contains(pp, "'path': 'a'") {
		t.Errorf("PrettyPrint expected to contain path input, got %q", pp)
	}
	if !strings.Contains(pp, "'flags': '2'") {
		t.Errorf("PrettyPrint expected to contain flags input, got %q", pp)
	}

	// Test JSON output
	js := c.JSON()
	if !json.Valid([]byte(js)) {
		t.Errorf("JSON() returned invalid json: %s", js)
	}
	if !strings.Contains(js, `"name":"ls"`) {
		t.Errorf("JSON output missing name field: %s", js)
	}
}

func TestInputSchemaPatchAndIsOk(t *testing.T) {
	is := &InputSchema{}
	is.Patch()
	if is.Type != "object" || is.Required == nil || is.Properties == nil {
		t.Fatalf("patch did not initialize fields: %#v", is)
	}

	// array without items -> not ok
	is.Properties["arr"] = ParameterObject{Type: "array"}
	if is.IsOk() {
		t.Fatalf("expected IsOk to fail when array items are missing")
	}

	// array with items -> ok
	is.Properties["arr"] = ParameterObject{Type: "array", Items: &ParameterObject{Type: "string"}}
	if !is.IsOk() {
		t.Fatalf("expected IsOk to pass when array items are provided")
	}
}

func TestParameterObjectUnmarshalTypeAsStringOrArray(t *testing.T) {
	// Test unmarshaling type as a string
	jsonStr := `{"type": "string", "description": "A string parameter"}`
	var p1 ParameterObject
	if err := json.Unmarshal([]byte(jsonStr), &p1); err != nil {
		t.Fatalf("failed to unmarshal type as string: %v", err)
	}
	if p1.Type != "string" {
		t.Errorf("expected type 'string', got %q", p1.Type)
	}

	// Test unmarshaling type as an array (union type like ["string", "null"])
	jsonArr := `{"type": ["string", "null"], "description": "A string or null parameter"}`
	var p2 ParameterObject
	if err := json.Unmarshal([]byte(jsonArr), &p2); err != nil {
		t.Fatalf("failed to unmarshal type as array: %v", err)
	}
	if p2.Type != "string" {
		t.Errorf("expected type 'string' (first element of array), got %q", p2.Type)
	}

	// Test marshaling always outputs string
	data, err := json.Marshal(p2)
	if err != nil {
		t.Fatalf("failed to marshal ParameterObject: %v", err)
	}
	if !strings.Contains(string(data), `"type":"string"`) {
		t.Errorf("expected marshaled JSON to contain type as string, got: %s", string(data))
	}
}

func TestInputSchemaWithCompositionKeywords(t *testing.T) {
	// Simulate a realistic MCP server response with composition keywords
	// This is similar to what the Notion MCP server might return
	jsonSchema := `{
		"type": "object",
		"required": ["action"],
		"properties": {
			"action": {
				"description": "The action to perform",
				"type": "string",
				"enum": ["create", "update", "delete"]
			},
			"data": {
				"description": "The data for the action",
				"oneOf": [
					{
						"type": "object",
						"description": "Create a page",
						"properties": {
							"title": {"type": "string"},
							"content": {"type": "string"}
						}
					},
					{
						"type": "object",
						"description": "Update a page",
						"properties": {
							"pageId": {"type": "string"},
							"updates": {"type": "object"}
						}
					}
				]
			},
			"options": {
				"description": "Optional configuration",
				"anyOf": [
					{"type": "object"},
					{"type": "null"}
				]
			}
		}
	}`

	var schema InputSchema
	if err := json.Unmarshal([]byte(jsonSchema), &schema); err != nil {
		t.Fatalf("failed to unmarshal schema with composition keywords: %v", err)
	}

	// Verify basic structure
	if schema.Type != "object" {
		t.Errorf("expected schema type 'object', got %q", schema.Type)
	}
	if len(schema.Required) != 1 || schema.Required[0] != "action" {
		t.Errorf("expected required to be ['action'], got %v", schema.Required)
	}
	if len(schema.Properties) != 3 {
		t.Errorf("expected 3 properties, got %d", len(schema.Properties))
	}

	// Verify action property with enum
	action, ok := schema.Properties["action"]
	if !ok {
		t.Fatal("expected 'action' property to exist")
	}
	if action.Type != "string" {
		t.Errorf("expected action type 'string', got %q", action.Type)
	}
	if action.Enum == nil || len(*action.Enum) != 3 {
		t.Errorf("expected action to have 3 enum values, got %v", action.Enum)
	}

	// Verify data property with oneOf
	data, ok := schema.Properties["data"]
	if !ok {
		t.Fatal("expected 'data' property to exist")
	}
	if len(data.OneOf) != 2 {
		t.Errorf("expected data to have 2 oneOf schemas, got %d", len(data.OneOf))
	}
	if data.OneOf[0].Type != "object" {
		t.Errorf("expected first oneOf schema to be object, got %q", data.OneOf[0].Type)
	}

	// Verify options property with anyOf
	options, ok := schema.Properties["options"]
	if !ok {
		t.Fatal("expected 'options' property to exist")
	}
	if len(options.AnyOf) != 2 {
		t.Errorf("expected options to have 2 anyOf schemas, got %d", len(options.AnyOf))
	}
	if options.AnyOf[0].Type != "object" {
		t.Errorf("expected first anyOf schema to be object, got %q", options.AnyOf[0].Type)
	}
	if options.AnyOf[1].Type != "null" {
		t.Errorf("expected second anyOf schema to be null, got %q", options.AnyOf[1].Type)
	}

	// Verify marshaling preserves composition keywords
	data2, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("failed to marshal schema: %v", err)
	}
	if !strings.Contains(string(data2), `"oneOf"`) {
		t.Errorf("expected marshaled schema to contain oneOf, got: %s", string(data2))
	}
	if !strings.Contains(string(data2), `"anyOf"`) {
		t.Errorf("expected marshaled schema to contain anyOf, got: %s", string(data2))
	}
}

func TestParameterObjectCompositionKeywords(t *testing.T) {
	// Test unmarshaling allOf
	jsonAllOf := `{
		"description": "A parameter with allOf",
		"allOf": [
			{"type": "string"},
			{"minLength": 1}
		]
	}`
	var p1 ParameterObject
	if err := json.Unmarshal([]byte(jsonAllOf), &p1); err != nil {
		t.Fatalf("failed to unmarshal allOf: %v", err)
	}
	if len(p1.AllOf) != 2 {
		t.Errorf("expected 2 allOf schemas, got %d", len(p1.AllOf))
	}
	if p1.AllOf[0].Type != "string" {
		t.Errorf("expected first allOf schema to have type 'string', got %q", p1.AllOf[0].Type)
	}

	// Test unmarshaling anyOf
	jsonAnyOf := `{
		"description": "A parameter with anyOf",
		"anyOf": [
			{"type": "string"},
			{"type": "number"}
		]
	}`
	var p2 ParameterObject
	if err := json.Unmarshal([]byte(jsonAnyOf), &p2); err != nil {
		t.Fatalf("failed to unmarshal anyOf: %v", err)
	}
	if len(p2.AnyOf) != 2 {
		t.Errorf("expected 2 anyOf schemas, got %d", len(p2.AnyOf))
	}
	if p2.AnyOf[0].Type != "string" {
		t.Errorf("expected first anyOf schema to have type 'string', got %q", p2.AnyOf[0].Type)
	}
	if p2.AnyOf[1].Type != "number" {
		t.Errorf("expected second anyOf schema to have type 'number', got %q", p2.AnyOf[1].Type)
	}

	// Test unmarshaling oneOf
	jsonOneOf := `{
		"description": "A parameter with oneOf",
		"oneOf": [
			{"type": "string", "description": "A string value"},
			{"type": "object", "description": "An object value"}
		]
	}`
	var p3 ParameterObject
	if err := json.Unmarshal([]byte(jsonOneOf), &p3); err != nil {
		t.Fatalf("failed to unmarshal oneOf: %v", err)
	}
	if len(p3.OneOf) != 2 {
		t.Errorf("expected 2 oneOf schemas, got %d", len(p3.OneOf))
	}
	if p3.OneOf[0].Type != "string" {
		t.Errorf("expected first oneOf schema to have type 'string', got %q", p3.OneOf[0].Type)
	}
	if p3.OneOf[1].Type != "object" {
		t.Errorf("expected second oneOf schema to have type 'object', got %q", p3.OneOf[1].Type)
	}

	// Test marshaling with composition keywords
	data, err := json.Marshal(p3)
	if err != nil {
		t.Fatalf("failed to marshal ParameterObject with oneOf: %v", err)
	}
	if !strings.Contains(string(data), `"oneOf"`) {
		t.Errorf("expected marshaled JSON to contain oneOf, got: %s", string(data))
	}

	// Test complex nested schema with composition keywords
	jsonComplex := `{
		"description": "Complex parameter",
		"oneOf": [
			{
				"type": "object",
				"properties": {
					"name": {"type": "string"}
				}
			},
			{
				"type": "array",
				"items": {"type": "string"}
			}
		]
	}`
	var p4 ParameterObject
	if err := json.Unmarshal([]byte(jsonComplex), &p4); err != nil {
		t.Fatalf("failed to unmarshal complex schema: %v", err)
	}
	if len(p4.OneOf) != 2 {
		t.Errorf("expected 2 oneOf schemas in complex schema, got %d", len(p4.OneOf))
	}
	if p4.OneOf[1].Type != "array" {
		t.Errorf("expected second oneOf schema to be array, got %q", p4.OneOf[1].Type)
	}
	if p4.OneOf[1].Items == nil {
		t.Error("expected array schema to have items defined")
	} else if p4.OneOf[1].Items.Type != "string" {
		t.Errorf("expected array items to be string, got %q", p4.OneOf[1].Items.Type)
	}
}
