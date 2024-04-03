package openai

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

var defaultGpt = ChatGPT{
	Model:       "gpt-4-turbo-preview",
	Temperature: 1.0,
	TopP:        1.0,
	Url:         ChatURL,
}

func loadQuerier(loadFrom, model string) (*ChatGPT, error) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("environment variable 'OPENAI_API_KEY' not set")
	}
	defaultCpy := defaultGpt
	defaultCpy.Model = model
	// Load config based on model, allowing for different configs for each model
	gptQuerier, err := tools.LoadConfigFromFile[ChatGPT](loadFrom, fmt.Sprintf("openai_gpt_%v.json", model), nil, &defaultCpy)
	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.PrintOK(fmt.Sprintf("ChatGPT config: %+v\n", gptQuerier))
	}
	if err != nil {
		ancli.PrintWarn(fmt.Sprintf("failed to load config for model: %v, error: %v\n", model, err))
	}
	gptQuerier.client = &http.Client{}
	gptQuerier.apiKey = apiKey
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	return &gptQuerier, nil
}

// NewTextQuerier returns a new ChatGPT querier using the textconfigurations to load
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
