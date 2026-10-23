package vendors

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/models"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type mockSubmitTool struct{}

func (mockSubmitTool) Call(pub_models.Input) (string, error) { return "accepted", nil }
func (mockSubmitTool) Specification() pub_models.Specification {
	return pub_models.Specification{Name: "submit_summary"}
}

// mockCallAt streams one completion for a chat carrying n prior
// submit_summary calls and returns the scripted call, or false on a plain
// text reply.
func mockCallAt(t *testing.T, m *Mock, prior int) (pub_models.Call, bool) {
	t.Helper()
	chat := pub_models.Chat{Messages: []pub_models.Message{
		{Role: "user", Content: "label this tool_submit_summary tool_submit_summary tool_submit_summary"},
	}}
	for range prior {
		chat.Messages = append(chat.Messages,
			pub_models.Message{Role: "assistant", ToolCalls: []pub_models.Call{{ID: "c", Name: "submit_summary"}}},
			pub_models.Message{Role: "tool", ToolCallID: "c", Content: "ERROR: title: at most 60 runes"},
		)
	}
	ch, err := m.StreamCompletions(context.Background(), chat)
	if err != nil {
		t.Fatalf("StreamCompletions: %v", err)
	}
	for ev := range ch {
		if call, ok := ev.(pub_models.Call); ok {
			return call, true
		}
		if _, ok := ev.(models.StopEvent); ok {
			break
		}
	}
	return pub_models.Call{}, false
}

func TestMock_submitSummarySequence(t *testing.T) {
	t.Setenv("CLAI_MOCK_SUMMARY_TITLES", "First|Second")
	t.Setenv("CLAI_MOCK_SUMMARY_SUMMARIES", "One.|Two.|Three.")
	m := &Mock{}
	m.RegisterTool(mockSubmitTool{})

	want := []struct{ title, summary string }{
		{"First", "One."},
		{"Second", "Two."},
		{"Second", "Three."},
	}
	for prior, w := range want {
		call, ok := mockCallAt(t, m, prior)
		if !ok {
			t.Fatalf("prior=%d: expected a scripted call", prior)
		}
		if call.Name != "submit_summary" {
			t.Fatalf("prior=%d: call = %q, want submit_summary", prior, call.Name)
		}
		in := *call.Inputs
		if in["title"] != w.title || in["summary"] != w.summary {
			t.Fatalf("prior=%d: inputs = %v, want %+v", prior, in, w)
		}
	}
	if _, ok := mockCallAt(t, m, 3); ok {
		t.Fatalf("every token consumed: expected a plain reply, got a call")
	}

	t.Run("defaults", func(t *testing.T) {
		t.Setenv("CLAI_MOCK_SUMMARY_TITLES", "")
		t.Setenv("CLAI_MOCK_SUMMARY_SUMMARIES", "")
		call, ok := mockCallAt(t, m, 0)
		if !ok {
			t.Fatalf("expected a scripted call")
		}
		in := *call.Inputs
		if in["title"] != "Mock title" || in["summary"] != "Mock summary." {
			t.Fatalf("defaults = %v", in)
		}
	})

	t.Run("other tools keep ordinal-free inputs", func(t *testing.T) {
		if got := inputsForTool("ls", 3); got["directory"] != "." {
			t.Fatalf("ls inputs = %v", got)
		}
	})
}

func mockStream(t *testing.T, m *Mock, chat pub_models.Chat) []models.CompletionEvent {
	t.Helper()
	ch, err := m.StreamCompletions(context.Background(), chat)
	if err != nil {
		t.Fatalf("StreamCompletions: %v", err)
	}
	var events []models.CompletionEvent
	for ev := range ch {
		events = append(events, ev)
	}
	return events
}

// Proves M1.
func TestMockEcho_Describes(t *testing.T) {
	img := pub_models.ImagePart{MIME: "image/png", Origin: pub_models.OriginUser, Data: []byte("png bytes")}
	h, err := img.Handle()
	if err != nil {
		t.Fatal(err)
	}
	msg := pub_models.Message{Role: "user", Parts: []pub_models.Part{img, pub_models.TextPart{Text: "what is this?"}}}
	events := mockStream(t, &Mock{}, pub_models.Chat{Messages: []pub_models.Message{msg}})
	echo, ok := events[0].(string)
	if !ok || echo != msg.Describe() || !strings.Contains(echo, "[image "+h+"]") {
		t.Fatalf("echo = %#v, want %q", events[0], msg.Describe())
	}
}

// Proves M2.
func TestMockToolInputs_Images(t *testing.T) {
	img := pub_models.ImagePart{MIME: "image/png", Origin: pub_models.OriginUser, Data: []byte("png bytes")}
	user := pub_models.Message{Role: "user", Parts: []pub_models.Part{
		pub_models.TextPart{Text: "tool_generate_image then tool_view_image"}, img,
	}}
	callOf := func(events []models.CompletionEvent) pub_models.Call {
		t.Helper()
		call, ok := events[0].(pub_models.Call)
		if !ok {
			t.Fatalf("first event = %#v, want a tool call", events[0])
		}
		return call
	}

	gen := callOf(mockStream(t, &Mock{}, pub_models.Chat{Messages: []pub_models.Message{user}}))
	if gen.Name != "generate_image" || !reflect.DeepEqual(*gen.Inputs, pub_models.Input{"prompt": "mock image"}) {
		t.Fatalf("generate_image call = %s %v, want {\"prompt\":\"mock image\"}", gen.Name, *gen.Inputs)
	}

	t.Setenv("CLAI_MOCK_IMAGE_PROMPT", "a fox")
	if got := inputsForTool("generate_image", 0); got["prompt"] != "a fox" {
		t.Fatalf("prompt = %v, want the env override", got["prompt"])
	}

	t.Setenv("CLAI_MOCK_VIEW_IMAGE_PATH", "/tmp/cat.png")
	chat := pub_models.Chat{Messages: []pub_models.Message{
		user,
		{Role: "assistant", ToolCalls: []pub_models.Call{gen}},
		{Role: "tool", ToolCallID: gen.ID, Content: "generated"},
	}}
	view := callOf(mockStream(t, &Mock{}, chat))
	if view.Name != "view_image" || !reflect.DeepEqual(*view.Inputs, pub_models.Input{"path": "/tmp/cat.png"}) {
		t.Fatalf("view_image call = %s %v, want {\"path\":\"/tmp/cat.png\"}", view.Name, *view.Inputs)
	}
}
