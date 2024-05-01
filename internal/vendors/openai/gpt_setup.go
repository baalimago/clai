package openai

import (
	"fmt"

	"github.com/baalimago/clai/internal/tools"
)

func (g *ChatGPT) Setup() error {
	err := g.StreamCompleter.Setup("OPENAI_API_KEY", ChatURL, "DEBUG_OPENAI")
	if err != nil {
		return fmt.Errorf("failed to setup stream completer: %w", err)
	}
	g.StreamCompleter.Model = g.Model
	g.StreamCompleter.FrequencyPenalty = &g.FrequencyPenalty
	g.StreamCompleter.MaxTokens = g.MaxTokens
	g.StreamCompleter.Temperature = &g.Temperature
	g.StreamCompleter.TopP = &g.TopP
	toolChoice := "auto"
	g.StreamCompleter.ToolChoice = &toolChoice
	return nil
}

func (g *ChatGPT) RegisterTool(tool tools.AiTool) {
	g.StreamCompleter.InternalRegisterTool(tool)
}
