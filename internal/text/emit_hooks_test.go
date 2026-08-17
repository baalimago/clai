package text

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/dimensions"
)

// TestQuerier_CloseReasoningIfOpen_logsOnce proves the reasoning hook emits one
// record per completed reasoning block — not per streamed token — and that a
// spurious close of an empty buffer emits no record (worklog 2026-08-15-agent-slog-output, Phase 3).
func TestQuerier_CloseReasoningIfOpen_logsOnce(t *testing.T) {
	t.Run("one record per block", func(t *testing.T) {
		h := &captureHandler{}
		q := &Querier[*MockQuerier]{
			out:           &strings.Builder{},
			agentSettings: &AgentSettings{Logger: slog.New(h)},
		}
		session := &QuerySession{}
		q.appendReasoning("token one ")
		q.appendReasoning("token two")
		q.reasoningActive = true

		q.closeReasoningIfOpen(context.Background(), session)

		if len(h.records) != 1 {
			t.Fatalf("expected exactly one reasoning record, got %d", len(h.records))
		}
		attrs := recordAttrs(h.records[0])
		if attrs["kind"] != "reasoning" {
			t.Fatalf("kind = %v, want reasoning", attrs["kind"])
		}
		if attrs["text"] != "token one token two" {
			t.Fatalf("text = %v, want the full block", attrs["text"])
		}
	})

	t.Run("empty buffer emits nothing", func(t *testing.T) {
		h := &captureHandler{}
		q := &Querier[*MockQuerier]{
			out:           &strings.Builder{},
			agentSettings: &AgentSettings{Logger: slog.New(h)},
		}
		q.reasoningActive = true

		q.closeReasoningIfOpen(context.Background(), &QuerySession{})

		if len(h.records) != 0 {
			t.Fatalf("expected no record for an empty reasoning buffer, got %d", len(h.records))
		}
	})
}

// TestFinalizeAssistantText_Logs proves the assistant hook fires only for
// finalized, non-empty prose: echoed tool-call text and empty pending text
// emit no record (worklog 2026-08-15-agent-slog-output, Phase 3).
func TestFinalizeAssistantText_Logs(t *testing.T) {
	call := pub_models.Call{ID: "call-1", Name: "pwd"}

	newQuerier := func(h *captureHandler) *Querier[*MockQuerier] {
		return &Querier[*MockQuerier]{
			out:           &strings.Builder{},
			dims:          dimensions.Dimensions{Width: 80, Height: 24},
			agentSettings: &AgentSettings{Logger: slog.New(h)},
		}
	}

	t.Run("non-empty prose logs assistant", func(t *testing.T) {
		h := &captureHandler{}
		q := newQuerier(h)
		session := &QuerySession{}
		session.AppendPendingText("I will check that for you.")

		if err := (toolExecutor[*MockQuerier]{querier: q}).finalizeAssistantTextBeforeToolCall(context.Background(), session, call); err != nil {
			t.Fatalf("finalizeAssistantTextBeforeToolCall: %v", err)
		}
		if len(h.records) != 1 {
			t.Fatalf("expected one assistant record, got %d", len(h.records))
		}
		attrs := recordAttrs(h.records[0])
		if attrs["kind"] != "assistant" || attrs["text"] != "I will check that for you." {
			t.Fatalf("record = %v, want kind=assistant with the pending prose", attrs)
		}
	})

	t.Run("non-rolling prose logs assistant", func(t *testing.T) {
		// The plain (non-rolling) finalize path is a hook site of its own; a
		// raw session forces it while the log must still fire.
		h := &captureHandler{}
		q := newQuerier(h)
		q.Raw = true
		session := &QuerySession{}
		session.AppendPendingText("plain path prose")

		if err := (toolExecutor[*MockQuerier]{querier: q}).finalizeAssistantTextBeforeToolCall(context.Background(), session, call); err != nil {
			t.Fatalf("finalizeAssistantTextBeforeToolCall: %v", err)
		}
		if len(h.records) != 1 {
			t.Fatalf("expected one assistant record, got %d", len(h.records))
		}
		if attrs := recordAttrs(h.records[0]); attrs["kind"] != "assistant" || attrs["text"] != "plain path prose" {
			t.Fatalf("record = %v, want kind=assistant with the pending prose", attrs)
		}
	})

	t.Run("echoed tool-call text does not log", func(t *testing.T) {
		h := &captureHandler{}
		q := newQuerier(h)
		session := &QuerySession{}
		session.AppendPendingText("\n" + call.PrettyPrint() + "\n")

		if err := (toolExecutor[*MockQuerier]{querier: q}).finalizeAssistantTextBeforeToolCall(context.Background(), session, call); err != nil {
			t.Fatalf("finalizeAssistantTextBeforeToolCall: %v", err)
		}
		if len(h.records) != 0 {
			t.Fatalf("echoed tool-call text must not log, got %d records", len(h.records))
		}
	})

	t.Run("empty pending does not log", func(t *testing.T) {
		h := &captureHandler{}
		q := newQuerier(h)

		if err := (toolExecutor[*MockQuerier]{querier: q}).finalizeAssistantTextBeforeToolCall(context.Background(), &QuerySession{}, call); err != nil {
			t.Fatalf("finalizeAssistantTextBeforeToolCall: %v", err)
		}
		if len(h.records) != 0 {
			t.Fatalf("empty pending must not log, got %d records", len(h.records))
		}
	})
}

// TestPostProcessOutput_LogsFinalAnswer_unconditionally proves the final_answer
// record fires before any display branch: a raw session that returns early
// still logs its completed answer (worklog 2026-08-15-agent-slog-output, Phase 3).
func TestPostProcessOutput_LogsFinalAnswer_unconditionally(t *testing.T) {
	h := &captureHandler{}
	q := &Querier[*MockQuerier]{
		Raw:           true,
		out:           &strings.Builder{},
		agentSettings: &AgentSettings{Logger: slog.New(h)},
	}
	q.postProcessOutput(context.Background(), pub_models.Message{Role: "assistant", Content: "final answer"})

	if len(h.records) != 1 {
		t.Fatalf("expected one final_answer record even in raw display, got %d", len(h.records))
	}
	attrs := recordAttrs(h.records[0])
	if attrs["kind"] != "final_answer" || attrs["text"] != "final answer" {
		t.Fatalf("record = %v, want kind=final_answer with the answer text", attrs)
	}
}

// TestExecuteLoadSkill_LogsToolResult proves the load_skill success path logs
// one tool_result record whose text is the final user-visible body (warnings
// folded in) and whose tool attribute is load_skill (worklog 2026-08-15-agent-slog-output, Phase 3).
func TestExecuteLoadSkill_LogsToolResult(t *testing.T) {
	h := &captureHandler{}
	loaded := LoadedSkillRuntime{
		Name:         "review",
		SourceClass:  "project",
		RenderedBody: "skill body",
	}
	q := Querier[*MockQuerier]{
		out:           &strings.Builder{},
		tooling:       tooling{skillLoader: fakeSkillLoader{loaded: loaded}},
		agentSettings: &AgentSettings{Logger: slog.New(h)},
	}
	session := &QuerySession{}
	inputs := pub_models.Input{"skill": "review"}
	call := pub_models.Call{ID: "call-1", Name: string(pub_models.LoadSkillTool), Inputs: &inputs}

	if err := (toolExecutor[*MockQuerier]{querier: &q}).Execute(context.Background(), session, call); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	toolCalls, toolResults := 0, 0
	for _, rec := range h.records {
		attrs := recordAttrs(rec)
		switch attrs["kind"] {
		case "tool_call":
			toolCalls++
		case "tool_result":
			toolResults++
			if attrs["tool"] != string(pub_models.LoadSkillTool) {
				t.Fatalf("tool = %v, want %q", attrs["tool"], string(pub_models.LoadSkillTool))
			}
			if want := formatSkillOutputForDisplay(loaded); attrs["text"] != want {
				t.Fatalf("text = %v, want %q (the final user-visible body)", attrs["text"], want)
			}
		}
	}
	if toolCalls != 1 {
		t.Fatalf("expected one tool_call record, got %d", toolCalls)
	}
	if toolResults != 1 {
		t.Fatalf("expected one tool_result record, got %d", toolResults)
	}
}
