package huggingface

import (
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/text/generic"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// HuggingFaceChat talks to the Hugging Face OpenAI-compatible router.
//
// It reuses the generic.StreamCompleter which expects an OpenAI-style
// /v1/chat/completions endpoint with SSE streaming.
type HuggingFaceChat struct {
	generic.StreamCompleter

	Model       string  `json:"model"`
	MaxTokens   *int    `json:"max_tokens"`
	Temperature float64 `json:"temperature"`
	TopP        float64 `json:"top_p"`
	URL         string  `json:"url"`
}

var DefaultChat = HuggingFaceChat{
	Model:       DefaultModelName,
	Temperature: 1.0,
	TopP:        1.0,
	URL:         DefaultChatURL,
}

func (h *HuggingFaceChat) Setup() error {
	if h.URL == "" {
		h.URL = DefaultChatURL
	}

	if err := h.StreamCompleter.Setup(EnvAPITokenKey, h.URL, EnvDebugKey); err != nil {
		return err
	}

	h.StreamCompleter.Model = h.Model
	h.StreamCompleter.MaxTokens = h.MaxTokens
	h.StreamCompleter.Temperature = &h.Temperature
	h.StreamCompleter.TopP = &h.TopP

	toolChoice := "auto"
	h.StreamCompleter.ToolChoice = &toolChoice
	h.StreamCompleter.Vendor = "huggingface"
	h.StreamCompleter.Images = h.ImageCapabilities()
	return nil
}

func (h *HuggingFaceChat) RegisterTool(tool pub_models.LLMTool) {
	h.InternalRegisterTool(tool)
}

// ImageCapabilities is the huggingface row of Local fact P5-1 (worklog 2026-10-21-image-support).
func (h *HuggingFaceChat) ImageCapabilities() models.ImageCapabilities {
	return models.ImageCapabilities{
		Input:      models.ImageInputInline,
		ToolResult: models.ToolResultFallback,
		MIMEs:      []string{"image/png", "image/jpeg", "image/webp"},
	}
}
