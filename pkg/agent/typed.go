package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/baalimago/clai/pkg/text/models"
)

// TypedResponse is the interface for LLM queries that return a typed result
// parsed from the system message JSON.
type TypedResponse[T any] interface {
	Setup(context.Context) error
	Query(context.Context, models.Chat) (T, error)
}

// TypedQuerier wraps an Agent and adds typed JSON parsing on top of Query.
// Each call to Query extracts the last system message, finds JSON candidates
// in its content, and unmarshals the first valid one into T.
type TypedQuerier[T any] struct {
	agent *Agent
}

// NewTyped creates a TypedQuerier that wraps an agent configured with the
// given options. The agent's response format defaults to json_object;
// callers may override it via WithResponseFormat.
func NewTyped[T any](options ...Option) *TypedQuerier[T] {
	// Ensure json_object is set so LLMs know to output JSON.
	opts := append([]Option{WithResponseFormat(models.ResponseFormat{Type: "json_object"})}, options...)
	a := New(opts...)
	return &TypedQuerier[T]{agent: &a}
}

func (tq *TypedQuerier[T]) Setup(ctx context.Context) error {
	return tq.agent.Setup(ctx)
}

func (tq *TypedQuerier[T]) Query(ctx context.Context, chat models.Chat) (T, error) {
	var zero T
	resp, err := tq.agent.Query(ctx, chat)
	if err != nil {
		return zero, fmt.Errorf("typed query: %w", err)
	}
	msg, _, err := resp.LastOfRole("system")
	if err != nil {
		return zero, fmt.Errorf("typed query: %w", err)
	}
	return parseTyped[T](msg.Content)
}

// extractJSONCandidates finds all balanced {…} substrings in content and
// returns them sorted by length descending (largest candidate first).
// This avoids capturing {YYYY} tokens from LLM thinking text that appear
// before the actual JSON output.
func extractJSONCandidates(content string) []string {
	type span struct{ start, end int }
	var spans []span

	for i := 0; i < len(content); i++ {
		if content[i] != '{' {
			continue
		}
		depth := 0
		for j := i; j < len(content); j++ {
			switch content[j] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					spans = append(spans, span{i, j})
				}
			}
			if depth == 0 {
				break
			}
		}
	}

	candidates := make([]string, len(spans))
	for idx, s := range spans {
		candidates[idx] = content[s.start : s.end+1]
	}

	sort.Slice(candidates, func(i, j int) bool {
		return len(candidates[i]) > len(candidates[j])
	})

	return candidates
}

// parseTyped extracts JSON candidates from content and unmarshals the first
// valid one into T. Returns an error if no candidate parses successfully.
func parseTyped[T any](content string) (T, error) {
	var zero T
	candidates := extractJSONCandidates(content)
	if len(candidates) == 0 {
		return zero, fmt.Errorf("no JSON found in response")
	}

	var lastErr error
	for _, candidate := range candidates {
		var result T
		if err := json.Unmarshal([]byte(candidate), &result); err != nil {
			lastErr = err
			continue
		}
		return result, nil
	}

	return zero, fmt.Errorf("failed to unmarshal JSON from all %d candidates: %w", len(candidates), lastErr)
}
