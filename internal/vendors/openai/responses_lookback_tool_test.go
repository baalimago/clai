package openai

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/tools"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// TestReadResponsesStream_LookbackToolNotInGlobalRegistry reproduces the reported
// failure: a model injected the lookback tool via -lb calls `search_conversations`,
// but that tool is registered on the querier's toolBox, not the global
// tools.Registry, so ToolFromName cannot resolve it. The stream must still surface a
// pub_models.Call (dispatched downstream by name) rather than aborting with
// "resolve tool from name \"search_conversations\": tool not found".
func TestReadResponsesStream_LookbackToolNotInGlobalRegistry(t *testing.T) {
	t.Parallel()

	tools.WithTestRegistry(t, func() {
		// Intentionally register NOTHING: mirror a run where only lookback tools are
		// active (e.g. -lb without -t), so the global registry is empty.
		sse := strings.Join([]string{
			`data: {"type":"response.output_item.added","output_index":0,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"search_conversations","arguments":""}}`,
			`data: {"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":0,"delta":"{\"query\":"}`,
			`data: {"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":0,"delta":"\"needle\"}"}`,
			`data: {"type":"response.function_call_arguments.done","item_id":"fc_1","output_index":0,"arguments":"{\"query\":\"needle\"}"}`,
			`data: {"type":"response.completed"}`,
			"",
		}, "\n")

		s := &responsesStreamer{}
		out := make(chan models.CompletionEvent)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		go s.readResponsesStream(ctx, io.NopCloser(strings.NewReader(sse)), out)

		var call *pub_models.Call
		var sawStop bool
		for ev := range out {
			switch c := ev.(type) {
			case pub_models.Call:
				cc := c
				call = &cc
			case models.StopEvent:
				sawStop = true
			case error:
				t.Fatalf("stream must not error on an unregistered advertised tool, got: %v", c)
			}
		}

		if !sawStop {
			t.Fatalf("expected a StopEvent")
		}
		if call == nil {
			t.Fatalf("expected a search_conversations Call to be emitted")
		}
		if call.Name != string(pub_models.SearchConversationsTool) {
			t.Fatalf("call name: got %q want %q", call.Name, pub_models.SearchConversationsTool)
		}
		if got := call.Function.Arguments; got != `{"query":"needle"}` {
			t.Fatalf("call args: got %q want %q", got, `{"query":"needle"}`)
		}
	})
}
