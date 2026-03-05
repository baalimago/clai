package generic

import (
	"testing"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/tools"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type fakeTool struct{ spec pub_models.Specification }

func (f fakeTool) Call(input pub_models.Input) (string, error) { return "", nil }
func (f fakeTool) Specification() pub_models.Specification     { return f.spec }

// Validates that streamed OpenAI-style tool_calls are assembled, parsed as JSON,
// and emitted as a pub_models.Call.
func TestStreamCompleter_EmitsToolCall(t *testing.T) {
	s := &StreamCompleter{}

	orig := tools.Registry
	tools.Registry = tools.NewRegistry()
	defer func() { tools.Registry = orig }()

	tools.Registry.Set("mcp_everything_get-annotated-message", fakeTool{spec: pub_models.Specification{Name: "mcp_everything_get-annotated-message"}})

	// Stream args in multiple chunks, like the real API does.
	first := Choice{Delta: Delta{ToolCalls: []ToolsCall{{
		ID:    "id1",
		Index: 0,
		Type:  "function",
		Function: Func{
			Name:      "mcp_everything_get-annotated-message",
			Arguments: `{"messageType":"`,
		},
	}}}}
	ev := s.handleChoice(first)
	if _, ok := ev.(models.NoopEvent); !ok {
		t.Fatalf("expected NoopEvent, got %T: %#v", ev, ev)
	}

	second := Choice{Delta: Delta{ToolCalls: []ToolsCall{{Function: Func{Arguments: `debug"`}}}}}
	ev = s.handleChoice(second)
	if _, ok := ev.(models.NoopEvent); !ok {
		t.Fatalf("expected NoopEvent, got %T: %#v", ev, ev)
	}

	third := Choice{Delta: Delta{ToolCalls: []ToolsCall{{Function: Func{Arguments: `}`}}}}}
	ev = s.handleChoice(third)
	call, ok := ev.(pub_models.Call)
	if !ok {
		t.Fatalf("expected Call, got %T: %#v", ev, ev)
	}
	if call.Name != "mcp_everything_get-annotated-message" {
		t.Fatalf("unexpected tool name: %q", call.Name)
	}
	if call.Inputs == nil || (*call.Inputs)["messageType"] != "debug" {
		t.Fatalf("unexpected inputs: %#v", call.Inputs)
	}
}
