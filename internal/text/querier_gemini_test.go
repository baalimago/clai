package text

import (
	"context"
	"strings"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func Test_checkIfGemini3Preview(t *testing.T) {
	t.Run("should return false if no extra content", func(t *testing.T) {
		q := &Querier[*MockQuerier]{}
		call := pub_models.Call{}
		if q.checkIfGemini3Preview(call) {
			t.Error("expected false, got true")
		}
	})

	t.Run("should return false if extra content is not google", func(t *testing.T) {
		q := &Querier[*MockQuerier]{}
		call := pub_models.Call{
			ExtraContent: map[string]any{
				"openai": "something",
			},
		}
		if q.checkIfGemini3Preview(call) {
			t.Error("expected false, got true")
		}
	})

	t.Run("should return true if extra content has google thought_signature", func(t *testing.T) {
		q := &Querier[*MockQuerier]{}
		call := pub_models.Call{
			ExtraContent: map[string]any{
				"google": map[string]any{
					"thought_signature": "some signature",
				},
			},
		}
		if !q.checkIfGemini3Preview(call) {
			t.Error("expected true, got false")
		}
	})

	t.Run("should return true if already detected", func(t *testing.T) {
		q := &Querier[*MockQuerier]{
			isLikelyGemini3Preview: true,
		}
		call := pub_models.Call{}
		if !q.checkIfGemini3Preview(call) {
			t.Error("expected true, got false")
		}
	})
}

func Test_handleToolCall_GeminiLogic(t *testing.T) {
	t.Run("should return to user and not execute tool if Gemini 3 already detected and no extra content", func(t *testing.T) {
		q := &Querier[*MockQuerier]{
			isLikelyGemini3Preview: true,
		}
		session := &QuerySession{
			LikelyGeminiPreview: true,
		}

		decision := q.decideToolCall(session, pub_models.Call{Name: "some_tool"})

		if !decision.TreatAsReturnToUser {
			t.Fatal("expected TreatAsReturnToUser to be true")
		}
		if !decision.SkipExecution {
			t.Fatal("expected SkipExecution to be true")
		}
	})

	t.Run("should execute tool if Gemini 3 detected and extra content present", func(t *testing.T) {
		q := &Querier[*MockQuerier]{
			isLikelyGemini3Preview: true,
		}
		session := &QuerySession{
			LikelyGeminiPreview: true,
		}

		decision := q.decideToolCall(session, pub_models.Call{
			Name: "some_tool",
			ExtraContent: map[string]any{
				"foo": "bar",
			},
		})

		if decision.TreatAsReturnToUser {
			t.Fatal("expected TreatAsReturnToUser to be false")
		}
		if decision.SkipExecution {
			t.Fatal("expected SkipExecution to be false")
		}
		if decision.PatchedCall.Name != "some_tool" {
			t.Fatalf("expected patched call name preserved, got %q", decision.PatchedCall.Name)
		}
	})

	t.Run("should detect Gemini 3 and then return early on later call without extra content", func(t *testing.T) {
		q := &Querier[*MockQuerier]{}
		session := &QuerySession{}

		firstDecision := q.decideToolCall(session, pub_models.Call{
			Name: "some_tool",
			ExtraContent: map[string]any{
				"google": map[string]any{
					"thought_signature": "confirmed",
				},
			},
		})
		if firstDecision.TreatAsReturnToUser {
			t.Fatal("expected first Gemini preview call to still execute")
		}
		if !session.LikelyGeminiPreview {
			t.Fatal("expected session likely Gemini preview to be set")
		}

		secondDecision := q.decideToolCall(session, pub_models.Call{Name: "some_tool"})
		if !secondDecision.TreatAsReturnToUser {
			t.Fatal("expected second call to return to user")
		}
		if !secondDecision.SkipExecution {
			t.Fatal("expected second call to skip execution")
		}
	})

	t.Run("tool executor should preserve streamed text as final assistant text when Gemini returns to user", func(t *testing.T) {
		q := &Querier[*MockQuerier]{}
		session := &QuerySession{
			LikelyGeminiPreview: true,
		}
		session.AppendPendingText("partial answer")

		err := toolExecutor[*MockQuerier]{querier: q}.Execute(context.TODO(), session, pub_models.Call{Name: "some_tool"})
		if err != nil {
			t.Fatalf("Execute returned err: %v", err)
		}
		if session.FinalAssistantText != "partial answer" {
			t.Fatalf("expected final assistant text to be preserved, got %q", session.FinalAssistantText)
		}
		if got := session.PendingTextString(); got != "" {
			t.Fatalf("expected pending text to be cleared, got %q", got)
		}
	})

	t.Run("tool executor should append assistant and tool messages when Gemini call has extra content", func(t *testing.T) {
		q := &Querier[*MockQuerier]{
			out: &strings.Builder{},
		}
		session := &QuerySession{
			LikelyGeminiPreview: true,
			Chat: pub_models.Chat{
				Messages: []pub_models.Message{{Role: "user", Content: "hello"}},
			},
		}

		err := toolExecutor[*MockQuerier]{querier: q}.Execute(context.TODO(), session, pub_models.Call{
			ID:   "call-1",
			Name: "some_tool",
			ExtraContent: map[string]any{
				"foo": "bar",
			},
		})
		if err != nil {
			t.Fatalf("Execute returned err: %v", err)
		}
		if len(session.Chat.Messages) != 3 {
			t.Fatalf("expected 3 messages, got %d", len(session.Chat.Messages))
		}
		if session.Chat.Messages[1].Role != "assistant" {
			t.Fatalf("expected assistant tool call message, got %+v", session.Chat.Messages[1])
		}
		if session.Chat.Messages[2].Role != "tool" {
			t.Fatalf("expected tool message, got %+v", session.Chat.Messages[2])
		}
	})
}
