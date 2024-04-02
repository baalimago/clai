package anthropic

import (
	"fmt"
	"net/http"
	"os"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

var defaultClaude = Claude{
	Model:            "claude-3-opus-20240229",
	Url:              ClaudeURL,
	AnthropicVersion: "2023-06-01",
	AnthropicBeta:    "messages-2023-12-15",
	MaxTokens:        1024,
}

func loadQuerier(loadFrom, model string) (*Claude, error) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("environment variable 'ANTHROPIC_API_KEY' not set")
	}
	defaultCpy := defaultClaude
	defaultCpy.Model = model
	// Load config based on model, allowing for different configs for each model
	claudeQuerier, err := tools.LoadConfigFromFile[Claude](loadFrom, fmt.Sprintf("anthropic_claude_%v.json", model), nil, &defaultCpy)
	if misc.Truthy(os.Getenv("DEBUG_CLAUDE")) {
		ancli.PrintOK(fmt.Sprintf("Claude config: %+v\n", claudeQuerier))
	}
	if err != nil {
		ancli.PrintWarn(fmt.Sprintf("failed to load config for model: %v, error: %v\n", model, err))
	}
	claudeQuerier.client = &http.Client{}
	claudeQuerier.apiKey = apiKey
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	return &claudeQuerier, nil
}

// NewTextQuerier returns a new Claude querier using the textconfigurations to load
// the correct model. API key is fetched via environment variable
func NewTextQuerier(conf text.Configurations) (models.ChatQuerier, error) {
	home, _ := os.UserConfigDir()
	querier, err := loadQuerier(home, conf.Model)
	if err != nil {
		return nil, fmt.Errorf("failed to load querier of model: %v, error: %w", conf.Model, err)
	}
	if misc.Truthy(os.Getenv("DEBUG")) {
		querier.debug = true
	}
	querier.chat = conf.InitialPrompt
	querier.Raw = conf.Raw
	return querier, nil
}
