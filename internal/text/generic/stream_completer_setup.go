package generic

import (
	"fmt"
	"net/http"
	"os"

	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

func (s *StreamCompleter) Setup(apiKeyEnv, url, debugEnv string) error {
	apiKey := os.Getenv(apiKeyEnv)
	if apiKey == "" {
		return fmt.Errorf("environment variable '%v' not set", apiKeyEnv)
	}
	s.client = &http.Client{}
	s.limiter = RateLimiter{}
	s.apiKey = apiKey
	s.url = url

	if misc.Truthy(os.Getenv("DEBUG")) || misc.Truthy(os.Getenv(debugEnv)) {
		s.debug = true
	}

	return nil
}

func (g *StreamCompleter) InternalRegisterTool(tool tools.LLMTool) {
	g.tools = append(g.tools, ToolSuper{
		Type:     "function",
		Function: convertToGenericTool(tool.Specification()),
	})
}

func (g *StreamCompleter) SetRateLimiter(rl RateLimiter) {
	g.limiter = rl
}

func convertToGenericTool(tool tools.Specification) Tool {
	return Tool{
		Name:        tool.Name,
		Description: tool.Description,
		Inputs:      *tool.Inputs,
	}
}
