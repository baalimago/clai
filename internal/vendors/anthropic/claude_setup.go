package anthropic

import (
	"fmt"
	"net/http"
	"os"

	"github.com/baalimago/clai/internal/debugflags"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func (c *Claude) Setup() error {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		return fmt.Errorf("environment variable 'ANTHROPIC_API_KEY' not set")
	}
	c.client = &http.Client{}
	c.apiKey = apiKey
	if debugflags.EnabledEnv("ANTHROPIC_DEBUG") {
		c.debug = true
	}
	return nil
}

func (c *Claude) RegisterTool(tool pub_models.LLMTool) {
	c.tools = append(c.tools, tool.Specification())
}
