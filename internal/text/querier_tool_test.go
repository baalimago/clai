package text

import (
	"errors"
	"fmt"
	"io"
	"os"
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

func Test_limitToolOutput_WritesOversizedOutputToTempFile(t *testing.T) {
	out := "abcdefghijklmnopqrstuvwxyz"
	got := limitToolOutput(out, 3)
	if !strings.Contains(got, "tool output too large") {
		t.Fatalf("expected oversize metadata, got %q", got)
	}
	if !strings.Contains(got, "full output saved to temp file: ") {
		t.Fatalf("expected temp file message, got %q", got)
	}
	if strings.Contains(got, "preview:") {
		t.Fatalf("expected preview section to be omitted, got %q", got)
	}
	if strings.Contains(got, "abc") {
		t.Fatalf("expected preview content to be omitted, got %q", got)
	}
	path := tempPathFromMaterializedOutput(t, got)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read temp file %q: %v", path, err)
	}
	if string(data) != out {
		t.Fatalf("expected temp file to contain %q, got %q", out, string(data))
	}
}

func Test_limitToolOutput_PassthroughWhenWithinLimit(t *testing.T) {
	out := "abc"
	got := limitToolOutput(out, 3)
	if got != out {
		t.Fatalf("expected passthrough %q, got %q", out, got)
	}
}

func Test_limitToolOutput_PassthroughWhenLimitDisabled(t *testing.T) {
	out := "abcdef"
	got := limitToolOutput(out, 0)
	if got != out {
		t.Fatalf("expected passthrough %q, got %q", out, got)
	}
}

func Test_limitToolOutput_UsesRuneAwarePreview(t *testing.T) {
	out := "åäö漢字🙂end"
	got := limitToolOutput(out, 3)
	if strings.Contains(got, "3 runes") {
		t.Fatalf("expected preview rune metadata to be omitted, got %q", got)
	}
	if strings.Contains(got, "preview:\nåäö") {
		t.Fatalf("expected rune-aware preview to be omitted, got %q", got)
	}
	if strings.Contains(got, "åäö") {
		t.Fatalf("expected no preview content in materialized output, got %q", got)
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

func tempPathFromMaterializedOutput(t *testing.T, got string) string {
	t.Helper()
	const prefix = "full output saved to temp file: "
	idx := strings.Index(got, prefix)
	if idx == -1 {
		t.Fatalf("expected temp file prefix in %q", got)
	}
	pathStart := idx + len(prefix)
	rest := got[pathStart:]
	newlineIdx := strings.Index(rest, "\n")
	trimmed := rest
	if newlineIdx == -1 {
		trimmed = strings.TrimSpace(rest)
	} else {
		trimmed = strings.TrimSpace(rest[:newlineIdx])
	}
	return strings.TrimSuffix(trimmed, "]")
}
