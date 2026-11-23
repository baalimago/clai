package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/text/generic"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

const cacheMember = `,"cache_control":{"type":"ephemeral"}}`

var goldenNames = []string{"plain", "tools", "schema"}

func rawBody(t *testing.T, c *Claude, chat pub_models.Chat) []byte {
	t.Helper()
	req, err := c.constructRequest(context.Background(), chat)
	if err != nil {
		t.Fatalf("constructRequest: %v", err)
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return body
}

func toolChat() pub_models.Chat {
	return pub_models.Chat{Messages: []pub_models.Message{
		{Role: "system", Content: "be brief"},
		{Role: "user", Content: "list files"},
		{Role: "assistant", ToolCalls: []pub_models.Call{{Name: "ls", ID: "t1", Inputs: &pub_models.Input{"dir": "."}}}},
		{Role: "tool", ToolCallID: "t1", Content: "a.go"},
		{Role: "assistant", Content: "a.go"},
		{Role: "user", Content: "thanks"},
	}}
}

func cacheSchemaFormat() *generic.ResponseFormat {
	return &generic.ResponseFormat{
		Type: "json_schema",
		JSONSchema: &generic.JSONSchemaSpec{Name: "r", Strict: true, Schema: map[string]any{
			"type": "object", "properties": map[string]any{"a": map[string]any{"type": "string"}},
		}},
	}
}

func goldenCase(name string) (*Claude, pub_models.Chat) {
	tools := []pub_models.Specification{{Name: "ls", Description: "list"}}
	switch name {
	case "plain":
		return &Claude{Model: "claude-3-5-sonnet", URL: "http://localhost/v1/messages", MaxTokens: 100, Temperature: 0.5, TopK: 3},
			pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hi"}}}
	case "tools":
		return &Claude{Model: "claude-sonnet-5-5", URL: "http://localhost/v1/messages", MaxTokens: 100, tools: tools}, toolChat()
	default:
		c := &Claude{Model: "claude-sonnet-5-5", URL: "http://localhost/v1/messages", MaxTokens: 100, tools: tools, StopSequences: []string{"x"}}
		c.SetResponseFormat(cacheSchemaFormat())
		return c, toolChat()
	}
}

func Test_constructRequest_cacheControl(t *testing.T) {
	long := pub_models.Chat{}
	for range 50 {
		long.Messages = append(long.Messages,
			pub_models.Message{Role: "user", Content: "q"}, pub_models.Message{Role: "assistant", Content: "a"})
	}
	long.Messages = append(long.Messages, pub_models.Message{Role: "user", Content: "q"})
	hi := pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hi"}}}
	withFormat := &Claude{Model: "claude-sonnet-5-5", URL: "http://localhost/v1/messages"}
	withFormat.SetResponseFormat(cacheSchemaFormat())
	tests := []struct {
		name string
		c    *Claude
		chat pub_models.Chat
	}{
		{"plain", &Claude{Model: "claude-3-5-sonnet", URL: "http://localhost/v1/messages"}, hi},
		{"tools", &Claude{Model: "claude-sonnet-5-5", URL: "http://localhost/v1/messages", tools: []pub_models.Specification{{Name: "ls"}}}, toolChat()},
		{"response format", withFormat, hi},
		{"omitsTemperature model", &Claude{Model: "claude-opus-5", URL: "http://localhost/v1/messages"}, hi},
		{"long chat", &Claude{Model: "claude-sonnet-5-5", URL: "http://localhost/v1/messages"}, long},
	}
	want := map[string]any{"type": "ephemeral"}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got map[string]any
			if err := json.Unmarshal(rawBody(t, tt.c, tt.chat), &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if !reflect.DeepEqual(got["cache_control"], want) {
				t.Fatalf("cache_control = %#v, want %#v", got["cache_control"], want)
			}
		})
	}
}

func countKey(v any, key string) int {
	n := 0
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			if k == key {
				n++
			}
			n += countKey(child, key)
		}
	case []any:
		for _, child := range x {
			n += countKey(child, key)
		}
	}
	return n
}

func Test_constructRequest_cacheControlOnlyAtTopLevel(t *testing.T) {
	c, chat := goldenCase("tools")
	var got map[string]any
	if err := json.Unmarshal(rawBody(t, c, chat), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if n := countKey(got, "cache_control"); n != 1 {
		t.Fatalf("cache_control occurs %d times, want 1", n)
	}
	if _, ok := got["cache_control"]; !ok {
		t.Fatal("cache_control is not at the root")
	}
}

func Test_constructRequest_restOfBodyUnchanged(t *testing.T) {
	for _, name := range goldenNames {
		t.Run(name, func(t *testing.T) {
			c, chat := goldenCase(name)
			body := rawBody(t, c, chat)
			if !bytes.HasSuffix(body, []byte(cacheMember)) {
				t.Fatalf("body does not end with the cache member:\n%s", body)
			}
			golden, err := os.ReadFile(filepath.Join("testdata", "claude_request_"+name+".json"))
			if err != nil {
				t.Fatalf("read golden: %v", err)
			}
			var got, want map[string]any
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("unmarshal body: %v", err)
			}
			if err := json.Unmarshal(golden, &want); err != nil {
				t.Fatalf("unmarshal golden: %v", err)
			}
			delete(got, "cache_control")
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("body minus cache_control differs from golden")
			}
			trimmed := append(bytes.TrimSuffix(body, []byte(cacheMember)), '}')
			if !bytes.Equal(trimmed, bytes.TrimSpace(golden)) {
				t.Fatalf("raw bytes differ from golden:\n%s\n%s", trimmed, golden)
			}
		})
	}
}

func Test_constructRequest_prefixStable(t *testing.T) {
	c := &Claude{Model: "claude-sonnet-5-5", URL: "http://localhost/v1/messages", MaxTokens: 100}
	first := pub_models.Chat{Messages: []pub_models.Message{
		{Role: "system", Content: "s"},
		{Role: "user", Content: "one"},
	}}
	second := pub_models.Chat{Messages: append(append([]pub_models.Message{}, first.Messages...),
		pub_models.Message{Role: "assistant", Content: "two"},
		pub_models.Message{Role: "user", Content: "three"})}
	a, b := rawBody(t, c, first), rawBody(t, c, second)
	const lastBlockEnd = `"one"}]}`
	cut := bytes.Index(a, []byte(lastBlockEnd))
	if cut < 0 {
		t.Fatalf("no end of the last message in %s", a)
	}
	prefix := a[:cut+len(lastBlockEnd)-1]
	if !bytes.HasPrefix(b, prefix) {
		t.Fatalf("second body does not repeat the first up to the appended messages:\n%s\n%s", a, b)
	}
	if !bytes.HasSuffix(a, []byte(cacheMember)) || !bytes.HasSuffix(b, []byte(cacheMember)) {
		t.Fatal("cache member differs or is missing")
	}
}

func Test_constructRequest_errorsUnchanged(t *testing.T) {
	c := &Claude{URL: "://bad"}
	if _, err := c.constructRequest(context.Background(), pub_models.Chat{}); err == nil || !strings.Contains(err.Error(), "failed to create request") {
		t.Fatalf("err = %v, want failed to create request", err)
	}
	c = &Claude{URL: "http://localhost/v1/messages"}
	c.SetResponseFormat(&generic.ResponseFormat{Type: "yaml"})
	if _, err := c.constructRequest(context.Background(), pub_models.Chat{}); !errors.Is(err, generic.ErrResponseFormatUnsupported) {
		t.Fatalf("err = %v, want ErrResponseFormatUnsupported", err)
	}
}

func Test_CountInputTokens_bodyHasNoCacheControl(t *testing.T) {
	var recorded []byte
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		recorded, _ = io.ReadAll(r.Body)
		return &http.Response{StatusCode: 200, Status: "200", Body: io.NopCloser(strings.NewReader(`{"input_tokens": 1}`)), Header: http.Header{}}, nil
	})}
	c := &Claude{Model: "m", URL: "https://anthropic.com/v1/messages", client: client}
	if _, err := c.CountInputTokens(context.Background(), pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hi"}}}); err != nil {
		t.Fatalf("CountInputTokens: %v", err)
	}
	if len(recorded) == 0 {
		t.Fatal("no body recorded")
	}
	if bytes.Contains(recorded, []byte("cache_control")) {
		t.Fatalf("count_tokens body has cache_control: %s", recorded)
	}
}

func Test_StreamCompletions_sendsCacheControl(t *testing.T) {
	bodies := make(chan []byte, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies <- b
		w.Header().Set("Content-Type", "text/event-stream")
		for _, e := range []string{
			sseEvent("message_start", usageMessageStart),
			sseEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`),
			sseEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`),
			sseEvent("content_block_stop", `{"type":"content_block_stop","index":0}`),
			sseEvent("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":4}}`),
			sseEvent("message_stop", `{"type":"message_stop"}`),
		} {
			_, _ = io.WriteString(w, e)
		}
	}))
	t.Cleanup(srv.Close)
	events, usage := streamUntilEndAt(t, srv.URL)
	var text string
	for _, e := range events {
		if s, ok := e.(string); ok {
			text += s
		}
	}
	if text != "ok" || usage == nil || usage.PromptTokensDetails.CachedTokens != 300 {
		t.Fatalf("text = %q, usage = %+v", text, usage)
	}
	var got map[string]any
	if err := json.Unmarshal(<-bodies, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(got["cache_control"], map[string]any{"type": "ephemeral"}) {
		t.Fatalf("cache_control = %#v", got["cache_control"])
	}
}
