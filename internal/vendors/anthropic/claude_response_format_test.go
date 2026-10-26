package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"testing"

	"github.com/baalimago/clai/internal/text/generic"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

var _ generic.ResponseFormatSetter = (*Claude)(nil)

func requestBody(t *testing.T, c *Claude) map[string]any {
	t.Helper()
	req, err := c.constructRequest(context.Background(), pub_models.Chat{
		Messages: []pub_models.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("constructRequest: %v", err)
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	return got
}

func Test_constructRequest_responseFormat(t *testing.T) {
	schema := map[string]any{
		"type":                 "object",
		"properties":           map[string]any{"summary": map[string]any{"type": "string"}},
		"required":             []any{"summary"},
		"additionalProperties": false,
	}

	t.Run("json_schema maps to output_config.format", func(t *testing.T) {
		c := &Claude{Model: "claude-sonnet-5-5", URL: "http://localhost/v1/messages"}
		c.SetResponseFormat(&generic.ResponseFormat{
			Type:       "json_schema",
			JSONSchema: &generic.JSONSchemaSpec{Name: "review", Strict: true, Schema: schema},
		})
		got := requestBody(t, c)
		want := map[string]any{"format": map[string]any{"type": "json_schema", "schema": schema}}
		if !reflect.DeepEqual(got["output_config"], want) {
			t.Fatalf("output_config = %#v, want %#v", got["output_config"], want)
		}
	})

	for name, rf := range map[string]*generic.ResponseFormat{
		"no format":   nil,
		"text format": {Type: "text"},
	} {
		t.Run(name+" sends no output_config", func(t *testing.T) {
			c := &Claude{Model: "claude-sonnet-5-5", URL: "http://localhost/v1/messages"}
			c.SetResponseFormat(rf)
			if oc, present := requestBody(t, c)["output_config"]; present {
				t.Fatalf("output_config = %#v, want absent", oc)
			}
		})
	}

	for name, rf := range map[string]*generic.ResponseFormat{
		"json_object":            {Type: "json_object"},
		"json_schema, no schema": {Type: "json_schema"},
		"unknown type":           {Type: "yaml"},
	} {
		t.Run(name+" is rejected", func(t *testing.T) {
			c := &Claude{Model: "claude-sonnet-5-5", URL: "http://localhost/v1/messages"}
			c.SetResponseFormat(rf)
			_, err := c.constructRequest(context.Background(), pub_models.Chat{
				Messages: []pub_models.Message{{Role: "user", Content: "hi"}},
			})
			if !errors.Is(err, generic.ErrResponseFormatUnsupported) {
				t.Fatalf("constructRequest err = %v, want ErrResponseFormatUnsupported", err)
			}
		})
	}
}

func Test_constructRequest_responseFormatKeepsPropertyOrder(t *testing.T) {
	var rf generic.ResponseFormat
	if err := json.Unmarshal([]byte(`{"type":"json_schema","json_schema":{"name":"verdict","schema":{"type":"object","properties":{"ship":{"type":"boolean"},"reason":{"type":"string"}},"required":["ship","reason"],"additionalProperties":false}}}`), &rf); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	c := &Claude{Model: "claude-sonnet-5-5", URL: "http://localhost/v1/messages"}
	c.SetResponseFormat(&rf)
	req, err := c.constructRequest(context.Background(), pub_models.Chat{
		Messages: []pub_models.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("constructRequest: %v", err)
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if ship, reason := bytes.Index(body, []byte(`"ship"`)), bytes.Index(body, []byte(`"reason"`)); ship < 0 || ship > reason {
		t.Fatalf("request reorders schema properties, want ship before reason:\n%s", body)
	}
}
