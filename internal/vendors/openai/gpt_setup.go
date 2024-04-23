package openai

import (
	"fmt"
	"net/http"
	"os"

	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

func (g *ChatGPT) Setup() error {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return fmt.Errorf("environment variable 'OPENAI_API_KEY' not set")
	}
	g.client = &http.Client{}
	g.apiKey = apiKey

	if misc.Truthy(os.Getenv("DEBUG")) || misc.Truthy(os.Getenv("OPENAI_DEBUG")) {
		g.debug = true
	}

	return nil
}

func convertToGptTool(tool tools.UserFunction) GptTool {
	return GptTool{
		Name:        tool.Name,
		Description: tool.Description,
		Inputs:      tool.Inputs,
	}
}

func (g *ChatGPT) RegisterTool(tool tools.AiTool) {
	g.tools = append(g.tools, GptToolSuper{
		Type:     "function",
		Function: convertToGptTool(tool.UserFunction()),
	})
}
