package openai

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/baalimago/clai/internal/text/generic"
)

func Test_mapResponseFormat_keepsPropertyOrder(t *testing.T) {
	var rf generic.ResponseFormat
	if err := json.Unmarshal([]byte(`{"type":"json_schema","json_schema":{"name":"verdict","schema":{"type":"object","properties":{"ship":{"type":"boolean"},"reason":{"type":"string"}}}}}`), &rf); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	text, err := mapResponseFormat(&rf)
	if err != nil {
		t.Fatalf("mapResponseFormat: %v", err)
	}
	body, err := json.Marshal(text)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if ship, reason := bytes.Index(body, []byte(`"ship"`)), bytes.Index(body, []byte(`"reason"`)); ship < 0 || ship > reason {
		t.Fatalf("text.format reorders schema properties, want ship before reason:\n%s", body)
	}
}

func Test_mapResponseFormat_jsonSchemaWithoutSchema(t *testing.T) {
	if _, err := mapResponseFormat(&generic.ResponseFormat{Type: "json_schema", JSONSchema: &generic.JSONSchemaSpec{Name: "x"}}); err == nil {
		t.Fatal("mapResponseFormat accepted a json_schema without a schema")
	}
}
