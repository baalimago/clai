package generic

import (
	"fmt"
	"net/http"
	"os"

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
