package text

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func Test_maxToolCalls(t *testing.T) {
	amToolCalls := 3
	q := Querier[mockCompleter]{
		maxToolCalls: &amToolCalls,
	}

	getLastMsgStr := func(m []models.Message) string {
		return m[len(q.chat.Messages)-1].Content
	}

	for i := range amToolCalls {
		err := q.doToolCallLogic(models.Call{})
		if err != nil {
			t.Fatalf("failed to handleToolCall: %v", err)
		}

		got := getLastMsgStr(q.chat.Messages)
		want := fmt.Sprintf("Tool calls remaining: %v", amToolCalls-i)
		testboil.AssertStringContains(t, got, want)
	}

	var err error
	var got string
	iter := func() {
		err = q.doToolCallLogic(models.Call{})
		if err != nil {
			t.Fatalf("failed to handleToolCall: %v", err)
		}
		got = getLastMsgStr(q.chat.Messages)
	}

	iter()
	testboil.AssertStringContains(t, got, "ERROR: No more tool calls allowed")

	iter()
	testboil.AssertStringContains(t, got, "ERROR: No more tool calls allowed")
	testboil.AssertStringContains(t, got, "You will be HARD SHUT DOWN if you persist.")

	iter()
	testboil.AssertStringContains(t, got, "ERROR: No more tool calls allowed")
	testboil.AssertStringContains(t, got, "You will be HARD SHUT DOWN if you persist.")
	testboil.AssertStringContains(t, got, "LAST WARNING")
	err = q.doToolCallLogic(models.Call{})
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF error, got: %T", err)
	}
}

func TestHandleToolCall_DebugToolsLogging(t *testing.T) {
	orig := tools.Registry
	tools.Registry = tools.NewRegistry()
	defer func() { tools.Registry = orig }()

	tools.Registry.Set("tool_a", staticTool{name: "tool_a"})
	t.Setenv("DEBUG_TOOLS", "1")

	out := &strings.Builder{}
	model := &recorderCompleter{}
	q := Querier[*recorderCompleter]{
		Raw:   true,
		Model: model,
		out:   out,
		chat: models.Chat{
			Messages: []models.Message{{Role: "user", Content: "hi"}},
		},
	}

	err := q.handleToolCall(context.Background(), models.Call{
		ID:     "id1",
		Name:   "tool_a",
		Inputs: &models.Input{"path": "."},
	})
	if err != nil {
		t.Fatalf("handleToolCall err: %v", err)
	}

	got := out.String()
	if strings.Contains(got, "tool call received") {
		t.Fatalf("expected DEBUG_TOOLS logs to not be written to querier out builder, got: %q", got)
	}
	if strings.Contains(got, "follow-up query after single tool call") {
		t.Fatalf("expected DEBUG_TOOLS logs to not be written to querier out builder, got: %q", got)
	}
	if !strings.Contains(got, "Call: 'tool_a'") {
		t.Fatalf("expected normal assistant pretty print to remain, got: %q", got)
	}
	if len(model.chats) != 1 {
		t.Fatalf("expected one follow-up query, got %d", len(model.chats))
	}
}
