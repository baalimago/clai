package vendors_test

import (
	"testing"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/internal/vendors"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func completeTaskCallAt(t *testing.T, m *vendors.Mock, prompt string, prior int) (pub_models.Call, bool) {
	t.Helper()
	chat := pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: prompt}}}
	for range prior {
		chat.Messages = append(chat.Messages,
			pub_models.Message{Role: "assistant", ToolCalls: []pub_models.Call{{ID: "c", Name: "complete_task"}}},
			pub_models.Message{Role: "tool", ToolCallID: "c", Content: "accepted"},
		)
	}
	events, err := m.StreamCompletions(t.Context(), chat)
	if err != nil {
		t.Fatalf("StreamCompletions: %v", err)
	}
	for event := range events {
		switch e := event.(type) {
		case pub_models.Call:
			return e, true
		case models.StopEvent:
			return pub_models.Call{}, false
		}
	}
	return pub_models.Call{}, false
}

func TestMock_scriptsCompleteTask(t *testing.T) {
	const prompt = "work on the goal tool_complete_task tool_complete_task"

	cases := []struct {
		name       string
		envStatus  string
		envSummary string
		wantStatus text.LoopStatus
		wantSum    string
	}{
		{"default", "", "", text.LoopStatusAchieved, "Mock loop summary."},
		{"scripted", "blocked", "Need credentials.", text.LoopStatusBlocked, "Need credentials."},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CLAI_MOCK_COMPLETE_TASK_STATUS", tc.envStatus)
			t.Setenv("CLAI_MOCK_COMPLETE_TASK_SUMMARY", tc.envSummary)

			m := &vendors.Mock{}
			recorder := text.NewCompletionRecorder("")
			m.RegisterTool(recorder)

			for prior := range 2 {
				call, ok := completeTaskCallAt(t, m, prompt, prior)
				if !ok {
					t.Fatalf("prior=%d: expected a scripted complete_task call", prior)
				}
				if call.Name != "complete_task" {
					t.Fatalf("prior=%d: call = %q, want complete_task", prior, call.Name)
				}
				if _, err := recorder.CallWithContext(t.Context(), *call.Inputs); err != nil {
					t.Fatalf("prior=%d: recorder rejected the scripted input: %v", prior, err)
				}
				submission, ok := recorder.Submission()
				if !ok || submission.Status != tc.wantStatus || submission.Summary != tc.wantSum {
					t.Fatalf("prior=%d: submission = (%+v, %v), want (%q, %q)", prior, submission, ok, tc.wantStatus, tc.wantSum)
				}
			}

			if _, ok := completeTaskCallAt(t, m, prompt, 2); ok {
				t.Fatal("every token consumed: expected no further call")
			}
			if _, ok := completeTaskCallAt(t, m, "no completion token", 0); ok {
				t.Fatal("prompt without tool_complete_task emitted a call")
			}
		})
	}
}
