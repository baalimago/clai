package generic

import (
	"encoding/json"
	"strings"
	"testing"
)

// orderedSchemaFile declares "ship" before "reason": the reverse of
// alphabetical order, which a map round-trip would produce.
const orderedSchemaFile = `{
  "type": "json_schema",
  "json_schema": {
    "name": "verdict",
    "strict": true,
    "schema": {
      "type": "object",
      "properties": {
        "ship": {"type": "boolean"},
        "reason": {"type": "string"}
      },
      "required": ["ship", "reason"],
      "additionalProperties": false
    }
  }
}`

// assertShipBeforeReason fails t unless "ship" precedes "reason" in body.
func assertShipBeforeReason(t *testing.T, body string) {
	t.Helper()
	ship, reason := strings.Index(body, `"ship"`), strings.Index(body, `"reason"`)
	if ship < 0 || reason < 0 || ship > reason {
		t.Fatalf("schema property order not preserved, want ship before reason:\n%s", body)
	}
}

func TestJSONSchemaSpec_preservesPropertyOrder(t *testing.T) {
	var rf ResponseFormat
	if err := json.Unmarshal([]byte(orderedSchemaFile), &rf); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if rf.JSONSchema == nil || rf.JSONSchema.Schema["type"] != "object" {
		t.Fatalf("Schema map not decoded: %#v", rf.JSONSchema)
	}

	t.Run("marshal", func(t *testing.T) {
		b, err := json.Marshal(rf)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		assertShipBeforeReason(t, string(b))
		if !strings.Contains(string(b), `"name":"verdict"`) || !strings.Contains(string(b), `"strict":true`) {
			t.Fatalf("marshalled spec lost its fields: %s", b)
		}
	})

	t.Run("SchemaJSON", func(t *testing.T) {
		got, err := rf.JSONSchema.SchemaJSON()
		if err != nil {
			t.Fatalf("SchemaJSON: %v", err)
		}
		assertShipBeforeReason(t, string(got))
	})
}

func TestJSONSchemaSpec_SchemaJSON(t *testing.T) {
	t.Run("map only", func(t *testing.T) {
		spec := JSONSchemaSpec{Schema: map[string]any{"type": "object"}}
		got, err := spec.SchemaJSON()
		if err != nil {
			t.Fatalf("SchemaJSON: %v", err)
		}
		if string(got) != `{"type":"object"}` {
			t.Fatalf("SchemaJSON = %s", got)
		}
	})
	t.Run("no schema is an error", func(t *testing.T) {
		if _, err := (&JSONSchemaSpec{Name: "x"}).SchemaJSON(); err == nil {
			t.Fatal("SchemaJSON on an empty spec returned no error")
		}
	})
	t.Run("raw that is not an object is rejected on decode", func(t *testing.T) {
		var spec JSONSchemaSpec
		if err := json.Unmarshal([]byte(`{"name":"x","schema":"nope"}`), &spec); err == nil {
			t.Fatal("Unmarshal accepted a non-object schema")
		}
	})
}
