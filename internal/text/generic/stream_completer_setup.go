package generic

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

func (s *StreamCompleter) Setup(apiKeyEnv, url, debugEnv string) error {
	apiKey := os.Getenv(apiKeyEnv)
	if apiKey == "" {
		return fmt.Errorf("environment variable '%v' not set", apiKeyEnv)
	}
	s.client = &http.Client{}
	s.apiKey = apiKey
	s.URL = url

	if misc.Truthy(os.Getenv("DEBUG")) || misc.Truthy(os.Getenv(debugEnv)) {
		s.debug = true
	}

	return nil
}

// SetupOpenAICompatible ensures the API key env var has a fallback, calls Setup,
// strips the vendor prefix from model, and assigns common OpenAI-compatible fields.
func (s *StreamCompleter) SetupOpenAICompatible(model, apiKeyEnv, fallbackKey, url, debugEnv, prefix string, frequencyPenalty, temperature, topP float64, maxTokens *int) error {
	if os.Getenv(apiKeyEnv) == "" {
		os.Setenv(apiKeyEnv, fallbackKey)
	}
	if err := s.Setup(apiKeyEnv, url, debugEnv); err != nil {
		return fmt.Errorf("failed to setup stream completer: %w", err)
	}
	s.Model = strings.TrimPrefix(model, prefix)
	s.FrequencyPenalty = &frequencyPenalty
	s.MaxTokens = maxTokens
	s.Temperature = &temperature
	s.TopP = &topP
	toolChoice := "auto"
	s.ToolChoice = &toolChoice
	return nil
}

func (g *StreamCompleter) InternalRegisterTool(tool pub_models.LLMTool) {
	g.tools = append(g.tools, ToolSuper{
		Type:     "function",
		Function: convertToGenericTool(tool.Specification()),
	})
}

func convertToGenericTool(tool pub_models.Specification) Tool {
	return Tool{
		Name:        tool.Name,
		Description: tool.Description,
		Inputs:      *tool.Inputs,
	}
}
