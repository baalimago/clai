package anthropic

import (
	"fmt"
	"net/http"
	"os"

	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

var defaultClaude = Claude{
	Model:            "claude-3-opus-20240229",
	Url:              ClaudeURL,
	AnthropicVersion: "2023-06-01",
	AnthropicBeta:    "messages-2023-12-15",
	MaxTokens:        1024,
}

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
