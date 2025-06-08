package ollama

import (
	"fmt"
	"os"

	"github.com/baalimago/clai/internal/tools"
)

const ChatURL = "http://localhost:11434/v1/chat/completions"

func (g *Ollama) Setup() error {
	if os.Getenv("OLLAMA_API_KEY") == "" {
		os.Setenv("OLLAMA_API_KEY", "ollama")
	}
	err := g.StreamCompleter.Setup("OLLAMA_API_KEY", ChatURL, "OLLAMA_DEBUG")
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

func (g *Ollama) RegisterTool(tool tools.LLMTool) {
	g.StreamCompleter.InternalRegisterTool(tool)
}
