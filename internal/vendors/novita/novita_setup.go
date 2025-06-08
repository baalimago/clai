package novita

import (
	"fmt"
	"os"

	"github.com/baalimago/clai/internal/tools"
)

const ChatURL = "https://api.novita.ai/v3/openai/chat/completions"

func (g *Novita) Setup() error {
	if os.Getenv("NOVITA_API_KEY") == "" {
		os.Setenv("NOVITA_API_KEY", "novita")
	}
	err := g.StreamCompleter.Setup("NOVITA_API_KEY", ChatURL, "NOVITA_DEBUG")
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

func (g *Novita) RegisterTool(tool tools.LLMTool) {
	g.StreamCompleter.InternalRegisterTool(tool)
}
