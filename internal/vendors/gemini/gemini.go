package gemini

import (
	"fmt"
	"os"

	"github.com/baalimago/clai/internal/text/generic"
	"github.com/baalimago/clai/internal/tools"
)

var Default = Gemini{
	Model:       "gemini-2.5-flash",
	Temperature: 1.0,
	TopP:        1.0,
	URL:         ChatURL,
}

type Gemini struct {
	generic.StreamCompleter
	Model           string  `json:"model"`
	MaxTokens       *int    `json:"max_tokens"` // Use a pointer to allow null value
	PresencePenalty float64 `json:"presence_penalty"`
	Temperature     float64 `json:"temperature"`
	TopP            float64 `json:"top_p"`
	URL             string  `json:"url"`
}

const ChatURL = "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions"

func (g *Gemini) Setup() error {
	if os.Getenv("GEMINI_API_KEY") == "" {
		os.Setenv("GEMINI_API_KEY", "gemini")
	}
	err := g.StreamCompleter.Setup("GEMINI_API_KEY", ChatURL, "GEMINI_DEBUG")
	if err != nil {
		return fmt.Errorf("failed to setup stream completer: %w", err)
	}
	g.StreamCompleter.Model = g.Model
	g.StreamCompleter.MaxTokens = g.MaxTokens
	g.StreamCompleter.Temperature = &g.Temperature
	g.StreamCompleter.TopP = &g.TopP
	toolChoice := "auto"
	g.ToolChoice = &toolChoice
	return nil
}

func (g *Gemini) RegisterTool(tool tools.LLMTool) {
	g.InternalRegisterTool(tool)
}
