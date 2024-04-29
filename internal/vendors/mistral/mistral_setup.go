package mistral

import (
	"fmt"
	"net/http"
	"os"

	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

func (m *Mistral) Setup() error {
	apiKey := os.Getenv("MISTRAL_API_KEY")
	if apiKey == "" {
		return fmt.Errorf("environment variable 'MISTRAL_API_KEY' not set")
	}
	m.client = &http.Client{}
	m.apiKey = apiKey

	if misc.Truthy(os.Getenv("DEBUG")) || misc.Truthy(os.Getenv("MISTRAL_DEBUG")) {
		m.debug = true
	}
	return nil
}

// TODO: Function implementation, probably very similar to chatgpt
// func (m *Mistral) RegisterTool(tool tools.AiTool) {
// 	m.tools = append(m.tools, MistralToolSuper{
// 		Type:     "function",
// 		Function: mistralifyUserFunction(tool.UserFunction()),
// 	})
// }

func mistralifyUserFunction(uf tools.UserFunction) MistralTool {
	return MistralTool{
		Description: uf.Description,
		Name:        uf.Name,
		Parameters:  uf.Inputs,
	}
}
