package text

import (
	"context"
	"errors"
	"testing"

	"github.com/baalimago/clai/internal/models"
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
	t.Run("should return nil and not call tool if Gemini 3 detected and no extra content", func(t *testing.T) {
		q := &Querier[*MockQuerier]{
			isLikelyGemini3Preview: true,
			// Model is nil, will panic if TextQuery is called
		}

		call := pub_models.Call{
			Name:         "some_tool",
			ExtraContent: nil,
		}

		err := q.handleToolCall(context.Background(), call)
		if err != nil {
			t.Errorf("expected nil error (early return), got: %v", err)
		}
	})

	t.Run("should NOT return early if Gemini 3 detected AND extra content present", func(t *testing.T) {
		mockModel := &MockQuerier{
			errChan:        make(chan error, 1),
			completionChan: make(chan models.CompletionEvent, 1),
		}
		mockModel.errChan <- errors.New("mock error to stop query")

		q := &Querier[*MockQuerier]{
			isLikelyGemini3Preview: true,
			Model:                  mockModel,
		}

		call := pub_models.Call{
			Name: "some_tool",
			ExtraContent: map[string]any{
				"foo": "bar",
			},
		}

		err := q.handleToolCall(context.Background(), call)

		expectedErr := "failed to query after tool call: TextQuery: failed to handle completion: completion stream error: mock error to stop query"
		if err == nil {
			t.Error("expected error, got nil")
		} else if err.Error() != expectedErr {
			t.Errorf("expected error \n'%v'\n, got: \n'%v'", expectedErr, err)
		}
	})

	t.Run("should detect Gemini 3 and return early in one pass", func(t *testing.T) {
		mockModel := &MockQuerier{
			errChan:        make(chan error, 1),
			completionChan: make(chan models.CompletionEvent, 1),
		}

		go func() {
			mockModel.errChan <- errors.New("mock error to stop query")
		}()

		q := &Querier[*MockQuerier]{
			isLikelyGemini3Preview: false,
			Model:                  mockModel,
		}

		call1 := pub_models.Call{
			Name: "some_tool",
			ExtraContent: map[string]any{
				"google": map[string]any{
					"thought_signature": "confirmed",
				},
			},
		}

		err := q.handleToolCall(context.Background(), call1)
		expectedErr := "failed to query after tool call: TextQuery: failed to handle completion: completion stream error: mock error to stop query"
		if err == nil {
			t.Error("expected error, got nil")
		} else if err.Error() != expectedErr {
			t.Errorf("expected error \n'%v'\n, got: \n'%v'", expectedErr, err)
		}

		if !q.isLikelyGemini3Preview {
			t.Error("expected isLikelyGemini3Preview to be set to true")
		}

		// Second call has NO extra content. Should return early (no error).
		call2 := pub_models.Call{
			Name:         "some_tool",
			ExtraContent: nil,
		}
		err = q.handleToolCall(context.Background(), call2)
		if err != nil {
			t.Errorf("expected nil error (early return), got: %v", err)
		}
	})
}
