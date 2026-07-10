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

// TestReadResponsesStream_InterleavedParallelToolCalls proves that argument deltas
// from two parallel function calls, interleaved on the wire and discriminated by
// item_id, are attributed to the correct call. A single shared toolCallState would
// let the second call's output_item.added reset the first call's buffered args and
// mis-attribute later deltas.
func TestReadResponsesStream_InterleavedParallelToolCalls(t *testing.T) {
	t.Parallel()

	tools.WithTestRegistry(t, func() {
		tools.Registry.Set("foo", fakeTool{spec: pub_models.Specification{Name: "foo"}})
		tools.Registry.Set("bar", fakeTool{spec: pub_models.Specification{Name: "bar"}})

		// fc_a (foo) and fc_b (bar) are opened, then their argument deltas are
		// interleaved before each is finished.
		sse := strings.Join([]string{
			`data: {"type":"response.output_item.added","output_index":0,"item":{"id":"fc_a","type":"function_call","call_id":"call_a","name":"foo","arguments":""}}`,
			`data: {"type":"response.function_call_arguments.delta","item_id":"fc_a","output_index":0,"delta":"{\"a\":"}`,
			`data: {"type":"response.output_item.added","output_index":1,"item":{"id":"fc_b","type":"function_call","call_id":"call_b","name":"bar","arguments":""}}`,
			`data: {"type":"response.function_call_arguments.delta","item_id":"fc_b","output_index":1,"delta":"{\"b\":"}`,
			`data: {"type":"response.function_call_arguments.delta","item_id":"fc_a","output_index":0,"delta":"1}"}`,
			`data: {"type":"response.function_call_arguments.done","item_id":"fc_a","output_index":0,"arguments":"{\"a\":1}"}`,
			`data: {"type":"response.function_call_arguments.delta","item_id":"fc_b","output_index":1,"delta":"2}"}`,
			`data: {"type":"response.function_call_arguments.done","item_id":"fc_b","output_index":1,"arguments":"{\"b\":2}"}`,
			`data: {"type":"response.completed"}`,
			"",
		}, "\n")

		s := &responsesStreamer{}
		out := make(chan models.CompletionEvent)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		go s.readResponsesStream(ctx, io.NopCloser(strings.NewReader(sse)), out)

		calls := map[string]pub_models.Call{}
		var sawStop bool
		for ev := range out {
			switch c := ev.(type) {
			case pub_models.Call:
				calls[c.Name] = c
			case models.StopEvent:
				sawStop = true
			case error:
				t.Fatalf("unexpected error event: %v", c)
			}
		}

		if !sawStop {
			t.Fatalf("expected a StopEvent")
		}
		if len(calls) != 2 {
			t.Fatalf("expected 2 distinct calls, got %d: %#v", len(calls), calls)
		}

		foo, ok := calls["foo"]
		if !ok {
			t.Fatalf("missing foo call, got %#v", calls)
		}
		if foo.ID != "call_a" {
			t.Fatalf("foo call id: got %q want %q", foo.ID, "call_a")
		}
		if got := foo.Function.Arguments; got != `{"a":1}` {
			t.Fatalf("foo args: got %q want %q", got, `{"a":1}`)
		}

		bar, ok := calls["bar"]
		if !ok {
			t.Fatalf("missing bar call, got %#v", calls)
		}
		if bar.ID != "call_b" {
			t.Fatalf("bar call id: got %q want %q", bar.ID, "call_b")
		}
		if got := bar.Function.Arguments; got != `{"b":2}` {
			t.Fatalf("bar args: got %q want %q", got, `{"b":2}`)
		}
	})
}
