package xai

import (
	"fmt"
	"os"

	"github.com/baalimago/clai/internal/text/generic"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

var Default = XAI{
	Model:       "grok-code-fast-1",
	Temperature: 0,
	TopP:        1.0,
	URL:         ChatURL,
}

type XAI struct {
	generic.StreamCompleter
	Model           string  `json:"model"`
	MaxTokens       *int    `json:"max_tokens"` // Use a pointer to allow null value
	PresencePenalty float64 `json:"presence_penalty"`
	Temperature     float64 `json:"temperature"`
	TopP            float64 `json:"top_p"`
	URL             string  `json:"url"`
}

const ChatURL = "https://api.x.ai/v1/chat/completions"

func (g *XAI) Setup() error {
	if os.Getenv("XAI_API_KEY") == "" {
		os.Setenv("XAI_API_KEY", "xai")
	}
	err := g.StreamCompleter.Setup("XAI_API_KEY", ChatURL, "XAI_DEBUG")
	if err != nil {
		return fmt.Errorf("failed to setup stream completer: %w", err)
	}
	// Vendor decoder for both decode points (non-OK body and error frame at
	// HTTP 200), per worklog 2026-09-05-error-propagation D11/D12.
	g.StreamCompleter.DecodeError = decodeError
	g.StreamCompleter.Model = g.Model
	g.StreamCompleter.MaxTokens = g.MaxTokens
	g.StreamCompleter.Temperature = &g.Temperature
	g.StreamCompleter.TopP = &g.TopP
	toolChoice := "auto"
	g.ToolChoice = &toolChoice
	return nil
}

func (g *XAI) RegisterTool(tool pub_models.LLMTool) {
	g.InternalRegisterTool(tool)
}
