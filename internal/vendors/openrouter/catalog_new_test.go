package openrouter

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/cost"
	"github.com/baalimago/clai/internal/testenv/procenv"
	"github.com/baalimago/clai/pkg/diagnostic"
)

func TestNewModelCatalog(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		cat, err := NewModelCatalog("sk-or-v1-abcdefgh")
		if err != nil {
			t.Fatalf("NewModelCatalog: %v", err)
		}
		if cat.fetchModels == nil {
			t.Fatalf("fetchModels is nil, want the live fetcher")
		}
	})

	t.Run("debug trace redacts the key", func(t *testing.T) {
		cat, err := NewModelCatalog("sk-or-v1-abcdefgh")
		if err != nil {
			t.Fatalf("NewModelCatalog: %v", err)
		}
		cat.fetchModels = func(context.Context) ([]Model, error) {
			return []Model{{ID: "some/model"}}, nil
		}
		var diagnostics bytes.Buffer
		ctx := procenv.Diagnostics(context.Background(), &diagnostics, diagnostic.LevelDebug)
		if _, err := cat.FetchModel(ctx, "some/model"); err != nil {
			t.Fatalf("FetchModel: %v", err)
		}
		got := diagnostics.String()
		if !strings.Contains(got, "an api key of 17 characters") || strings.Contains(got, "sk-or") || strings.Contains(got, "abcdefgh") {
			t.Errorf("debug diagnostics = %q, want the key's length and no part of the key", got)
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
		if !errors.Is(err, cost.ErrModelNotInCatalog) {
			t.Fatalf("err = %v, want cost.ErrModelNotInCatalog", err)
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
