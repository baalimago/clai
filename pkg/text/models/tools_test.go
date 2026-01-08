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
