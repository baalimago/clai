package vendors

import (
	"context"
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
