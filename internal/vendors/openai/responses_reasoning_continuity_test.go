package openai

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/tools"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// TestResponsesStreamer_RequestsEncryptedReasoningForReasoningModels asserts the
// include:["reasoning.encrypted_content"] opt-in: without it, store:false responses
// return no reasoning items to replay.
func TestResponsesStreamer_RequestsEncryptedReasoningForReasoningModels(t *testing.T) {
	t.Parallel()

	body := captureResponsesRequestBody(t, &responsesStreamer{model: "gpt-5.2"})
	inc, ok := body["include"].([]any)
	if !ok {
		t.Fatalf("include must be present for reasoning models, got %#v", body["include"])
	}
	var found bool
	for _, v := range inc {
		if v == "reasoning.encrypted_content" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected reasoning.encrypted_content in include, got %#v", inc)
	}
}

func TestResponsesStreamer_NoIncludeForNonReasoningModels(t *testing.T) {
	t.Parallel()

	body := captureResponsesRequestBody(t, &responsesStreamer{model: "gpt-4.1-mini"})
	if _, ok := body["include"]; ok {
		t.Fatalf("include must be omitted for non-reasoning models, got %#v", body["include"])
	}
}

// TestReadResponsesStream_CapturesReasoningItemsOntoCall drives a reasoning turn:
// reasoning output_item.done (encrypted_content) -> a function call. The emitted
// Call must carry the reasoning item so the assistant turn can persist it.
func TestReadResponsesStream_CapturesReasoningItemsOntoCall(t *testing.T) {
	t.Parallel()

	tools.WithTestRegistry(t, func() {
		tools.Registry.Set("foo", fakeTool{spec: pub_models.Specification{Name: "foo"}})

		sse := strings.Join([]string{
			`data: {"type":"response.created","response":{"id":"resp_xyz"}}`,
			`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs_1","encrypted_content":"SEALED","summary":[{"type":"summary_text","text":"planning"}]}}`,
			`data: {"type":"response.output_item.added","output_index":1,"item":{"id":"fc_a","type":"function_call","call_id":"call_a","name":"foo","arguments":""}}`,
			`data: {"type":"response.function_call_arguments.delta","item_id":"fc_a","output_index":1,"delta":"{}"}`,
			`data: {"type":"response.function_call_arguments.done","item_id":"fc_a","output_index":1,"arguments":"{}"}`,
			`data: {"type":"response.completed","response":{"id":"resp_xyz"}}`,
			"",
		}, "\n")

		s := &responsesStreamer{}
		out := make(chan models.CompletionEvent)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		go s.readResponsesStream(ctx, io.NopCloser(strings.NewReader(sse)), out)

		var call *pub_models.Call
		for ev := range out {
			switch c := ev.(type) {
			case pub_models.Call:
				cc := c
				call = &cc
			case error:
				t.Fatalf("unexpected error event: %v", c)
			}
		}

		if call == nil {
			t.Fatalf("expected a Call to be emitted")
		}
		if len(call.ReasoningItems) != 1 {
			t.Fatalf("expected 1 reasoning item on call, got %d: %#v", len(call.ReasoningItems), call.ReasoningItems)
		}
		ri := call.ReasoningItems[0]
		if ri.ID != "rs_1" || ri.EncryptedContent != "SEALED" {
			t.Fatalf("reasoning item: got %#v", ri)
		}
		if len(ri.Summary) != 1 || ri.Summary[0] != "planning" {
			t.Fatalf("reasoning summary: got %#v", ri.Summary)
		}
	})
}

// TestReadResponsesStream_SkipsReasoningItemWithoutEncryptedContent guards the
// defensive skip: a reasoning item with no encrypted_content is unusable for
// stateless replay and must not be persisted/attached.
func TestReadResponsesStream_SkipsReasoningItemWithoutEncryptedContent(t *testing.T) {
	t.Parallel()

	tools.WithTestRegistry(t, func() {
		tools.Registry.Set("foo", fakeTool{spec: pub_models.Specification{Name: "foo"}})

		sse := strings.Join([]string{
			`data: {"type":"response.created","response":{"id":"resp_xyz"}}`,
			`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs_1","encrypted_content":""}}`,
			`data: {"type":"response.output_item.added","output_index":1,"item":{"id":"fc_a","type":"function_call","call_id":"call_a","name":"foo","arguments":""}}`,
			`data: {"type":"response.function_call_arguments.done","item_id":"fc_a","output_index":1,"arguments":"{}"}`,
			`data: {"type":"response.completed","response":{"id":"resp_xyz"}}`,
			"",
		}, "\n")

		s := &responsesStreamer{}
		out := make(chan models.CompletionEvent)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		go s.readResponsesStream(ctx, io.NopCloser(strings.NewReader(sse)), out)

		var call *pub_models.Call
		for ev := range out {
			if c, ok := ev.(pub_models.Call); ok {
				cc := c
				call = &cc
			}
		}
		if call == nil {
			t.Fatalf("expected a Call")
		}
		if len(call.ReasoningItems) != 0 {
			t.Fatalf("reasoning item without encrypted_content must be skipped, got %#v", call.ReasoningItems)
		}
	})
}

// TestMapMessage_ReplaysReasoningItemsBeforeFunctionCall verifies replay ordering
// and the reasoning-model gate.
func TestMapMessage_ReplaysReasoningItemsBeforeFunctionCall(t *testing.T) {
	t.Parallel()

	msg := pub_models.Message{
		Role: "assistant",
		ToolCalls: []pub_models.Call{{
			ID:       "call_a",
			Name:     "foo",
			Function: pub_models.Specification{Name: "foo", Arguments: "{}"},
		}},
		ReasoningItems: []pub_models.ReasoningItem{{
			ID:               "rs_1",
			EncryptedContent: "SEALED",
			Summary:          []string{"planning"},
		}},
	}

	// Reasoning model: the reasoning item is replayed immediately before the call.
	items, err := mapMessageToResponsesInputItems(msg, true)
	if err != nil {
		t.Fatalf("map: %v", err)
	}
	if len(items) < 2 {
		t.Fatalf("expected reasoning + function_call items, got %#v", items)
	}
	if items[0].Type != "reasoning" {
		t.Fatalf("item[0] type: got %q want reasoning", items[0].Type)
	}
	if items[0].ID != "rs_1" || items[0].EncryptedContent != "SEALED" {
		t.Fatalf("reasoning item: got %#v", items[0])
	}
	if items[0].Summary == nil || len(*items[0].Summary) != 1 {
		t.Fatalf("reasoning summary must be present, got %#v", items[0].Summary)
	}
	if (*items[0].Summary)[0].Type != "summary_text" || (*items[0].Summary)[0].Text != "planning" {
		t.Fatalf("reasoning summary shape: got %#v", *items[0].Summary)
	}
	if items[1].Type != "function_call" {
		t.Fatalf("item[1] type: got %q want function_call", items[1].Type)
	}

	// Non-reasoning target (or Chat Completions path): reasoning items are dropped
	// so an endpoint that cannot decrypt them never receives them.
	dropped, err := mapMessageToResponsesInputItems(msg, false)
	if err != nil {
		t.Fatalf("map (drop): %v", err)
	}
	for _, it := range dropped {
		if it.Type == "reasoning" {
			t.Fatalf("reasoning item must be dropped when includeReasoning=false, got %#v", dropped)
		}
	}
}

// TestMapMessage_ReasoningItemSummaryAlwaysPresent guards the 400 regression:
// OpenAI requires `summary` to be present as an array on every reasoning input
// item, even when clai captured no summary text. omitempty must not drop it.
func TestMapMessage_ReasoningItemSummaryAlwaysPresent(t *testing.T) {
	t.Parallel()

	msg := pub_models.Message{
		Role: "assistant",
		ToolCalls: []pub_models.Call{{
			ID:       "call_a",
			Name:     "foo",
			Function: pub_models.Specification{Name: "foo", Arguments: "{}"},
		}},
		// A reasoning item with NO summary text (a legitimate case for some steps).
		ReasoningItems: []pub_models.ReasoningItem{{ID: "rs_1", EncryptedContent: "SEALED"}},
	}

	items, err := mapMessageToResponsesInputItems(msg, true)
	if err != nil {
		t.Fatalf("map: %v", err)
	}
	if items[0].Type != "reasoning" {
		t.Fatalf("item[0] type: got %q want reasoning", items[0].Type)
	}

	// The reasoning item must serialize with a present (empty) summary array.
	b, err := json.Marshal(items[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	summ, ok := raw["summary"]
	if !ok {
		t.Fatalf("summary key must be present on reasoning input item, got %s", b)
	}
	if string(summ) != "[]" {
		t.Fatalf("empty summary must serialize as [], got %s", summ)
	}

	// A non-reasoning item (the function_call) must NOT carry a summary key.
	fb, err := json.Marshal(items[1])
	if err != nil {
		t.Fatalf("marshal function_call: %v", err)
	}
	var fraw map[string]json.RawMessage
	if err := json.Unmarshal(fb, &fraw); err != nil {
		t.Fatalf("unmarshal function_call: %v", err)
	}
	if _, ok := fraw["summary"]; ok {
		t.Fatalf("function_call item must not carry a summary key, got %s", fb)
	}
}
