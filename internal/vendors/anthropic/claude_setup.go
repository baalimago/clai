package anthropic

import (
	"fmt"
	"net/http"
	"os"

	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

func (c *Claude) Setup() error {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		return fmt.Errorf("environment variable 'ANTHROPIC_API_KEY' not set")
	}
	c.client = &http.Client{}
	c.apiKey = apiKey
	if misc.Truthy(os.Getenv("DEBUG")) || misc.Truthy(os.Getenv("ANTHROPIC_DEBUG")) {
		c.debug = true
	}
	return nil
}

func (c *Claude) RegisterTool(tool tools.AiTool) {
	c.tools = append(c.tools, tool.UserFunction())
}
