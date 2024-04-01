package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
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

func (c *Claude) constructRequest(ctx context.Context, chat models.Chat) (*http.Request, error) {
	sysMsg, err := chat.SystemMessage()
	if err != nil {
		ancli.PrintWarn(fmt.Sprintf("failed to find system message: %v\n", err))

	}
	claudifiedMsgs := claudifyMessages(chat.Messages)
	if c.debug {
		ancli.PrintOK(fmt.Sprintf("claudified messages: %+v\n", claudifiedMsgs))
	}
	reqData := ClaudeReq{
		Model:     c.Model,
		Messages:  claudifiedMsgs,
		MaxTokens: c.MaxTokens,
		Stream:    true,
		System:    sysMsg.Content,
	}
	jsonData, err := json.Marshal(reqData)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal ClaudeReq: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Url, bytes.NewBuffer(jsonData))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", c.AnthropicVersion)
	if c.debug {
		ancli.PrintOK(fmt.Sprintf("Request: %+v\n", req))
	}
	return req, nil
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
	return querier, nil
}
