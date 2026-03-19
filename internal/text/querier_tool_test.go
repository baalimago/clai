package text

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func Test_maxToolCalls(t *testing.T) {
	amToolCalls := 3
	q := Querier[mockCompleter]{
		maxToolCalls: &amToolCalls,
	}

	getLastMsgStr := func(m []pub_models.Message) string {
		return m[len(q.chat.Messages)-1].Content
	}

	for i := range amToolCalls {
		err := q.doToolCallLogic(pub_models.Call{})
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
		err = q.doToolCallLogic(pub_models.Call{})
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
	err = q.doToolCallLogic(pub_models.Call{})
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF error, got: %T", err)
	}
}

func Test_limitToolOutput_TruncatesByRuneCount(t *testing.T) {
	out := "abcdef"
	got := limitToolOutput(out, 3)
	if !strings.Contains(got, "abc") {
		t.Fatalf("expected truncated prefix in output, got %q", got)
	}
	if !strings.Contains(got, "3 more characters") {
		t.Fatalf("expected remaining rune count in output, got %q", got)
	}
}

func Test_toolExecutor_NormalizesEmptyToolOutput(t *testing.T) {
	q := Querier[*MockQuerier]{}
	session := &QuerySession{}
	call := pub_models.Call{ID: "call-1"}

	out := limitToolOutput("", q.toolOutputRuneLimit)
	if out != "" {
		t.Fatalf("expected empty output before normalization, got %q", out)
	}
	if out == "" {
		out = "<EMPTY-RESPONSE>"
	}

	msg := pub_models.Message{
		Role:       "tool",
		Content:    out,
		ToolCallID: call.ID,
	}
	session.Chat.Messages = append(session.Chat.Messages, msg)

	if session.Chat.Messages[0].Content != "<EMPTY-RESPONSE>" {
		t.Fatalf("expected normalized placeholder, got %q", session.Chat.Messages[0].Content)
	}
}
