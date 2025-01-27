package deepseek

import (
	"fmt"
	"os"

	"github.com/baalimago/clai/internal/tools"
)

const ChatURL = "https://api.deepseek.com/chat/completions"

func (g *Deepseek) Setup() error {
	if os.Getenv("DEEPSEEK_API_KEY") == "" {
		os.Setenv("DEEPSEEK_API_KEY", "deepseek")
	}
	err := g.StreamCompleter.Setup("DEEPSEEK_API_KEY", ChatURL, "DEEPSEEK_DEBUG")
	if err != nil {
		return fmt.Errorf("failed to setup stream completer: %w", err)
	}
	g.StreamCompleter.Model = g.Model
	g.StreamCompleter.FrequencyPenalty = &g.FrequencyPenalty
	g.StreamCompleter.MaxTokens = g.MaxTokens
	g.StreamCompleter.Temperature = &g.Temperature
	g.StreamCompleter.TopP = &g.TopP
	toolChoice := "auto"
	g.ToolChoice = &toolChoice
	return nil
}

func (g *Deepseek) RegisterTool(tool tools.AiTool) {
	g.InternalRegisterTool(tool)
}
