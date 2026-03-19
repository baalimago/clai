package openrouter

import (
	"context"
	"encoding/json"
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
	debug bool
}

func NewModelCatalog(apiKey string) (OpenRouterModelCatalog, error) {
	debug := misc.Truthy(os.Getenv("DEBUG")) || misc.Truthy(os.Getenv("DEBUG_OPENROUTER_MODEL_CATALOG"))
	if debug {
		ancli.Noticef("setting up openrouter model catalog with api key: %v...(redacted)", apiKey[:5])
	}
	return OpenRouterModelCatalog{
		debug: debug,
	}, nil
}

func (c OpenRouterModelCatalog) fetchModels(
	ctx context.Context,
) ([]Model, error) {
	resp, err := http.Get(modelsEndpoint)
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
		Data []Model `json:"data"`
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
		cachedPrompt, err := parseOpenRouterPrice(entry.Pricing.InputCacheRead)
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
