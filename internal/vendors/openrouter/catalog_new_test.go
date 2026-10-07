package openrouter

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNewModelCatalog(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		t.Setenv("DEBUG_OPENROUTER_MODEL_CATALOG", "")
		cat, err := NewModelCatalog("sk-or-v1-abcdefgh")
		if err != nil {
			t.Fatalf("NewModelCatalog: %v", err)
		}
		if cat.debug {
			t.Errorf("debug = true without a flag")
		}
		if cat.fetchModels == nil {
			t.Fatalf("fetchModels is nil, want the live fetcher")
		}
	})

	t.Run("debug flag", func(t *testing.T) {
		t.Setenv("DEBUG_OPENROUTER_MODEL_CATALOG", "1")
		cat, err := NewModelCatalog("sk-or-v1-abcdefgh")
		if err != nil {
			t.Fatalf("NewModelCatalog: %v", err)
		}
		if !cat.debug {
			t.Errorf("debug = false with the flag set")
		}
	})
}

func TestFetchModelErrors(t *testing.T) {
	wantErr := errors.New("network down")

	t.Run("fetch failure is wrapped", func(t *testing.T) {
		cat := OpenRouterModelCatalog{fetchModels: func(context.Context) ([]Model, error) {
			return nil, wantErr
		}}
		if _, err := cat.FetchModel(context.Background(), "any"); !errors.Is(err, wantErr) {
			t.Fatalf("err = %v, want the fetch failure", err)
		}
	})

	t.Run("unknown model", func(t *testing.T) {
		cat := OpenRouterModelCatalog{fetchModels: func(context.Context) ([]Model, error) {
			return []Model{{ID: "other/model"}}, nil
		}}
		_, err := cat.FetchModel(context.Background(), "missing/model")
		if err == nil || !strings.Contains(err.Error(), "model not found") {
			t.Fatalf("err = %v, want model not found", err)
		}
	})

	t.Run("unparseable prompt price", func(t *testing.T) {
		cat := OpenRouterModelCatalog{fetchModels: func(context.Context) ([]Model, error) {
			return []Model{{ID: "broken/model", Pricing: ModelPricing{Prompt: "not-a-number"}}}, nil
		}}
		_, err := cat.FetchModel(context.Background(), "broken/model")
		if err == nil || !strings.Contains(err.Error(), "parse prompt price") {
			t.Fatalf("err = %v, want a prompt price parse error", err)
		}
	})

	t.Run("unparseable completion price", func(t *testing.T) {
		cat := OpenRouterModelCatalog{fetchModels: func(context.Context) ([]Model, error) {
			return []Model{{ID: "broken/model", Pricing: ModelPricing{Prompt: "0.1", Completion: "nope"}}}, nil
		}}
		_, err := cat.FetchModel(context.Background(), "broken/model")
		if err == nil || !strings.Contains(err.Error(), "parse completion price") {
			t.Fatalf("err = %v, want a completion price parse error", err)
		}
	})

	t.Run("unparseable cached prompt price", func(t *testing.T) {
		cat := OpenRouterModelCatalog{fetchModels: func(context.Context) ([]Model, error) {
			return []Model{{ID: "broken/model", Pricing: ModelPricing{Prompt: "0.1", Completion: "0.2", InputCacheRead: "nope"}}}, nil
		}}
		_, err := cat.FetchModel(context.Background(), "broken/model")
		if err == nil || !strings.Contains(err.Error(), "parse cached prompt price") {
			t.Fatalf("err = %v, want a cached price parse error", err)
		}
	})
}

func TestParseOpenRouterPriceEmpty(t *testing.T) {
	got, err := parseOpenRouterPrice("")
	if err != nil {
		t.Fatalf("parseOpenRouterPrice(\"\"): %v", err)
	}
	if got != 0 {
		t.Errorf("price = %v, want 0", got)
	}
}
