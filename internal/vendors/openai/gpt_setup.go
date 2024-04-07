package openai

import (
	"fmt"
	"net/http"
	"os"

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
