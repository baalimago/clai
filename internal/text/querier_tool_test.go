package text

import (
	"bytes"
	"context"
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
		err := q.doToolCallLogic(context.Background(), pub_models.Call{})
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
		err = q.doToolCallLogic(context.Background(), pub_models.Call{})
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
	err = q.doToolCallLogic(context.Background(), pub_models.Call{})
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

func Test_toolExecutor_ExecuteLoadSkill_PrintsSummary(t *testing.T) {
	var out bytes.Buffer
	q := Querier[*MockQuerier]{out: &out, skillLoader: fakeSkillLoader{
		loaded: LoadedSkillRuntime{
			Name:         "review",
			SourceClass:  "project",
			RenderedBody: "skill body",
		},
	}}
	session := &QuerySession{}
	inputs := pub_models.Input{"skill": "review"}
	call := pub_models.Call{ID: "call-1", Name: "load_skill", Inputs: &inputs}
	err := toolExecutor[*MockQuerier]{querier: &q}.Execute(context.Background(), session, call)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
}

func Test_toolExecutor_ExecuteLoadSkill_TruncatesUserVisibleOutputUnlessRaw(t *testing.T) {
	t.Run("non_raw", func(t *testing.T) {
		var out bytes.Buffer
		q := Querier[*MockQuerier]{out: &out, skillLoader: fakeSkillLoader{
			loaded: LoadedSkillRuntime{
				Name:         "review",
				SourceClass:  "default",
				RenderedBody: "full skill body\nwith details",
				Description:  "concise",
			},
		}}
		session := &QuerySession{}
		inputs := pub_models.Input{"skill": "review"}
		call := pub_models.Call{ID: "call-1", Name: "load_skill", Inputs: &inputs}
		err := toolExecutor[*MockQuerier]{querier: &q}.Execute(context.Background(), session, call)
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		got := out.String()
		for _, want := range []string{"Name: review", "Description: concise", "Length: 28 chars", "Estimated tokens: ~7"} {
			if !strings.Contains(got, want) {
				t.Fatalf("expected output to contain %q, got %q", want, got)
			}
		}
		for _, notWant := range []string{"full skill body", "with details"} {
			if strings.Contains(got, notWant) {
				t.Fatalf("expected non-raw output to omit %q, got %q", notWant, got)
			}
		}
		if len(session.Chat.Messages) < 2 || !strings.Contains(session.Chat.Messages[1].Content, "with details") {
			t.Fatalf("expected full rendered body retained in transcript, got %#v", session.Chat.Messages)
		}
	})

	t.Run("raw", func(t *testing.T) {
		var out bytes.Buffer
		q := Querier[*MockQuerier]{out: &out, Raw: true, skillLoader: fakeSkillLoader{
			loaded: LoadedSkillRuntime{
				Name:         "review",
				SourceClass:  "default",
				RenderedBody: "## Title\nDescription: concise\nBody line",
			},
		}}
		session := &QuerySession{}
		inputs := pub_models.Input{"skill": "review"}
		call := pub_models.Call{ID: "call-1", Name: "load_skill", Inputs: &inputs}
		err := toolExecutor[*MockQuerier]{querier: &q}.Execute(context.Background(), session, call)
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !strings.Contains(out.String(), "Body line") {
			t.Fatalf("expected raw output to include full body, got %q", out.String())
		}
	})
}

func Test_toolExecutor_ExecuteLoadSkill_ActivationCapAppendsError(t *testing.T) {
	q := Querier[*MockQuerier]{skillLoader: fakeSkillLoader{
		loaded: LoadedSkillRuntime{ActivationErr: "skill activation cap exceeded: maxActivatedSkills=1"},
	}}
	session := &QuerySession{}
	inputs := pub_models.Input{"skill": "review"}
	call := pub_models.Call{ID: "call-1", Name: "load_skill", Inputs: &inputs}
	err := toolExecutor[*MockQuerier]{querier: &q}.Execute(context.Background(), session, call)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(session.Chat.Messages) != 2 || !strings.Contains(session.Chat.Messages[1].Content, "ERROR: skill activation cap exceeded") {
		t.Fatalf("unexpected messages: %#v", session.Chat.Messages)
	}
}

type fakeSkillLoader struct{ loaded LoadedSkillRuntime }

func (f fakeSkillLoader) LoadSkill(context.Context, string, string, map[string]pub_models.LLMTool) (LoadedSkillRuntime, error) {
	return f.loaded, nil
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
	before, _, ok := strings.Cut(rest, "\n")
	trimmed := rest
	if !ok {
		trimmed = strings.TrimSpace(rest)
	} else {
		trimmed = strings.TrimSpace(before)
	}
	return strings.TrimSuffix(trimmed, "]")
}
