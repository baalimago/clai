package anthropic

import (
	"errors"
	"testing"

	"github.com/baalimago/clai/internal/models"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestHandleContentBlockStart(t *testing.T) {
	t.Run("malformed payload", func(t *testing.T) {
		c := &Claude{}
		got := c.handleContentBlockStart("data: {not json")
		if _, ok := got.(error); !ok {
			t.Fatalf("got %T, want an error event", got)
		}
	})

	t.Run("tool_use records name and id", func(t *testing.T) {
		c := &Claude{}
		ev := c.handleContentBlockStart(`data: {"content_block":{"type":"tool_use","id":"tool-1","name":"search"}}`)
		if _, ok := ev.(models.NoopEvent); !ok {
			t.Fatalf("got %T, want NoopEvent", ev)
		}
		if c.contentBlockType != "tool_use" {
			t.Fatalf("contentBlockType = %q", c.contentBlockType)
		}
		if c.functionName != "search" || c.functionID != "tool-1" {
			t.Fatalf("functionName/ID = %q/%q", c.functionName, c.functionID)
		}
	})
}

func TestHandleContentBlockDelta(t *testing.T) {
	t.Run("token is not a data frame", func(t *testing.T) {
		c := &Claude{}
		if _, ok := c.handleContentBlockDelta("nope").(error); !ok {
			t.Fatal("expected an error event for a non-data token")
		}
	})

	t.Run("empty text delta", func(t *testing.T) {
		c := &Claude{}
		ev := c.handleContentBlockDelta(`data: {"delta":{"type":"text_delta"}}`)
		err, ok := ev.(error)
		if !ok || !errors.Is(err, err) || err.Error() != "unexpected empty response" {
			t.Fatalf("got %v (%T), want the empty response error", ev, ev)
		}
	})

	t.Run("input json delta accumulates the partial json", func(t *testing.T) {
		c := &Claude{}
		ev := c.handleContentBlockDelta(`data: {"delta":{"type":"input_json_delta","partial_json":"{\"a\":"}}`)
		if got, ok := ev.(string); !ok || got != `{"a":` {
			t.Fatalf("got %v (%T)", ev, ev)
		}
		if c.functionJSON != `{"a":` {
			t.Fatalf("functionJSON = %q", c.functionJSON)
		}
	})

	t.Run("unexpected delta type", func(t *testing.T) {
		c := &Claude{}
		if _, ok := c.handleContentBlockDelta(`data: {"delta":{"type":"mystery"}}`).(error); !ok {
			t.Fatal("expected an error event for an unknown delta type")
		}
	})

	t.Run("thinking delta becomes a reasoning event", func(t *testing.T) {
		c := &Claude{}
		ev := c.handleContentBlockDelta(`data: {"delta":{"type":"thinking_delta","thinking":"hmm"}}`)
		reasoning, ok := ev.(models.ReasoningEvent)
		if !ok || reasoning.Content != "hmm" {
			t.Fatalf("got %#v (%T)", ev, ev)
		}
	})
}

func TestHandleContentBlockStop(t *testing.T) {
	t.Run("malformed payload", func(t *testing.T) {
		c := &Claude{}
		if _, ok := c.handleContentBlockStop("data: {not json").(error); !ok {
			t.Fatal("expected an error event for a malformed block stop")
		}
	})

	t.Run("tool_use assembles the call", func(t *testing.T) {
		c := &Claude{
			contentBlockType: "tool_use",
			functionName:     "search",
			functionID:       "tool-1",
			functionJSON:     `{"query":"hi"}`,
		}
		ev := c.handleContentBlockStop(`data: {"type":"tool_use"}`)
		call, ok := ev.(pub_models.Call)
		if !ok {
			t.Fatalf("got %T, want a Call", ev)
		}
		if call.Name != "search" || call.ID != "tool-1" {
			t.Fatalf("unexpected call: %#v", call)
		}
		if call.Inputs == nil || (*call.Inputs)["query"] != "hi" {
			t.Fatalf("unexpected inputs: %#v", call.Inputs)
		}
		if c.functionJSON != "" {
			t.Fatalf("functionJSON must be reset, got %q", c.functionJSON)
		}
	})

	t.Run("tool_use with malformed accumulated json", func(t *testing.T) {
		c := &Claude{
			contentBlockType: "tool_use",
			functionJSON:     `{not json`,
		}
		if _, ok := c.handleContentBlockStop(`data: {"type":"tool_use"}`).(error); !ok {
			t.Fatal("expected an error event for malformed accumulated tool JSON")
		}
	})

	t.Run("non tool block is a noop", func(t *testing.T) {
		c := &Claude{contentBlockType: "text"}
		if _, ok := c.handleContentBlockStop(`data: {"type":"text"}`).(models.NoopEvent); !ok {
			t.Fatal("expected NoopEvent for a non-tool block")
		}
	})
}

func TestHandleInputJSONDelta(t *testing.T) {
	c := &Claude{}
	ev := c.handleInputJSONDelta(Delta{PartialJSON: `{"a":`})
	if ev != `{"a":` {
		t.Fatalf("got %v, want the partial json echoed", ev)
	}
	if c.functionJSON != `{"a":` {
		t.Fatalf("functionJSON = %q", c.functionJSON)
	}
}
