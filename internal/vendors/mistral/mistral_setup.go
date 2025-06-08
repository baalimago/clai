package mistral

import (
	"context"
	"fmt"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/tools"
)

func (m *Mistral) Setup() error {
	err := m.StreamCompleter.Setup("MISTRAL_API_KEY", MistralURL, "DEBUG_MISTRAL")
	if err != nil {
		return fmt.Errorf("failed to setup stream completer: %w", err)
	}
	m.StreamCompleter.Model = m.Model
	m.StreamCompleter.FrequencyPenalty = m.FrequencyPenalty
	m.StreamCompleter.MaxTokens = &m.MaxTokens
	m.StreamCompleter.Temperature = &m.Temperature
	m.StreamCompleter.TopP = &m.TopP
	toolChoice := "auto"
	m.StreamCompleter.ToolChoice = &toolChoice
	m.StreamCompleter.Clean = clean

	return nil
}

func (m *Mistral) StreamCompletions(ctx context.Context, chat models.Chat) (chan models.CompletionEvent, error) {
	return m.StreamCompleter.StreamCompletions(ctx, chat)
}

func (m *Mistral) RegisterTool(tool tools.LLMTool) {
	m.StreamCompleter.InternalRegisterTool(tool)
}
