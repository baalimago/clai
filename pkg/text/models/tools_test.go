package models

import "testing"

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
	_ = c.PrettyPrint() // smoke

	// with inputs order-independent
	inp := Input{"path": "a", "flags": 2}
	c = Call{Name: "ls", Inputs: &inp}
	c.Patch()
	if c.Function.Name != "ls" || c.Type != "function" {
		t.Fatalf("unexpected patch results: %#v", c)
	}
	_ = c.JSON() // smoke
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
