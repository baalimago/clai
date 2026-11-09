package anthropic

import (
	"net/http"
	"os"

	"github.com/baalimago/clai/internal/apikey"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func (c *Claude) Setup() error {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		return &apikey.MissingError{Env: "ANTHROPIC_API_KEY", Message: "environment variable 'ANTHROPIC_API_KEY' not set"}
	}
	c.client = &http.Client{}
	c.apiKey = apiKey
	return nil
}

func (c *Claude) RegisterTool(tool pub_models.LLMTool) {
	c.tools = append(c.tools, tool.Specification())
}
