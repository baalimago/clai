package openrouter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/baalimago/clai/internal/cost"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

const modelsEndpoint = "https://openrouter.ai/api/v1/models"

type OpenRouterModelCatalog struct {
	openrouterAPIKey string
	debug            bool
}

func NewModelCatalog(apiKey string) (OpenRouterModelCatalog, error) {
	if apiKey == "" {
		return OpenRouterModelCatalog{}, errors.New("apiKey cannot be unset")
	}
	debug := misc.Truthy(os.Getenv("DEBUG")) || misc.Truthy(os.Getenv("DEBUG_OPENROUTER_MODEL_CATALOG"))
	if debug {
		ancli.Noticef("setting up openrouter model catalog with api key: %v...(redacted)", apiKey[:5])
	}
	return OpenRouterModelCatalog{
		openrouterAPIKey: apiKey,
		debug:            debug,
	}, nil
}

type OpenRouterModel struct {
	ID                  string                     `json:"id"`
	Name                string                     `json:"name"`
	Pricing             OpenRouterModelPricing     `json:"pricing"`
	TopProvider         OpenRouterTopProvider      `json:"top_provider"`
	Architecture        OpenRouterArchitecture     `json:"architecture"`
	SupportedParameters []string                   `json:"supported_parameters"`
	ContextLength       int                        `json:"context_length,omitempty"`
	PerRequestLimits    map[string]any             `json:"per_request_limits,omitempty"`
	Raw                 map[string]json.RawMessage `json:"-"`
}

type OpenRouterModelPricing struct {
	Prompt       string `json:"prompt"`
	Completion   string `json:"completion"`
	CachedPrompt string `json:"cached_prompt"`
}

type OpenRouterTopProvider struct {
	ContextLength       int `json:"context_length"`
	MaxCompletionTokens int `json:"max_completion_tokens"`
}

type OpenRouterArchitecture struct {
	Modality     string `json:"modality"`
	Tokenizer    string `json:"tokenizer"`
	InstructType string `json:"instruct_type"`
}

func (c OpenRouterModelCatalog) fetchModels(ctx context.Context) ([]OpenRouterModel, error) {
	if c.openrouterAPIKey == "" {
		return nil, errors.New("no openrouter api key found")
	}
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		modelsEndpoint,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("create openrouter models request: %w", err)
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.openrouterAPIKey))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch openrouter models: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read openrouter models body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(
			"fetch openrouter models: status %d: %s",
			resp.StatusCode,
			string(body),
		)
	}
	var payload struct {
		Data []OpenRouterModel `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("unmarshal openrouter models body: %w", err)
	}

	return payload.Data, nil
}

func (c OpenRouterModelCatalog) FetchModel(ctx context.Context, model string) (cost.ModelPriceScheme, error) {
	d, err := c.fetchModels(ctx)
	if err != nil {
		return cost.ModelPriceScheme{}, fmt.Errorf("failed to fetch models: %w", err)
	}
	for _, entry := range d {
		// This is turbo inefficient, but good enough for now. Ideally we store price snapshot for all models here
		if strings.Contains(entry.Name, model) {
			continue
		}
		prompt, err := parseOpenRouterPrice(entry.Pricing.Prompt)
		if err != nil {
			return cost.ModelPriceScheme{}, fmt.Errorf("parse prompt price for model %q: %w", model, err)
		}
		completion, err := parseOpenRouterPrice(entry.Pricing.Completion)
		if err != nil {
			return cost.ModelPriceScheme{}, fmt.Errorf("parse completion price for model %q: %w", model, err)
		}
		cachedPrompt, err := parseOpenRouterPrice(entry.Pricing.CachedPrompt)
		if err != nil {
			return cost.ModelPriceScheme{}, fmt.Errorf("parse cached prompt price for model %q: %w", model, err)
		}
		return cost.ModelPriceScheme{
			InputUSDPerToken:       prompt,
			OutputUSDPerToken:      completion,
			InputCachedUSDPerToken: cachedPrompt,
		}, nil
	}
	return cost.ModelPriceScheme{}, fmt.Errorf("find price for model %q: model not found", model)
}

func parseOpenRouterPrice(raw string) (float64, error) {
	if raw == "" {
		return 0, nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("parse price %q: %w", raw, err)
	}
	return v, nil
}
