package openrouter

import (
	"context"
	"strings"
	"testing"
)

func TestParseOpenRouterPrice(t *testing.T) {
	got, err := parseOpenRouterPrice("0.0000016")
	if err != nil {
		t.Fatalf("parseOpenRouterPrice: %v", err)
	}
	if got != 0.0000016 {
		t.Fatalf("unexpected parsed price: %v", got)
	}
}

func TestParseOpenRouterPriceMalformed(t *testing.T) {
	_, err := parseOpenRouterPrice("abc")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "parse price") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFetchModelReturnsCorrectPricing(t *testing.T) {
	// Verify FetchModel matches against entry.ID (not entry.Name) and
	// returns pricing for the matching model, not the first in the list.
	//
	// The catalog is ordered so that a model whose pricing differs from
	// the target appears first.  If the matcher picks the wrong entry
	// (e.g. because of the inverted-condition bug or because it matches
	// on Name instead of ID), this test will catch it.
	cat := OpenRouterModelCatalog{}

	// catalogsFromJSON simulates what fetchModels returns: a slice of
	// Model entries with an Inkling-like model first (pricing ~$1/$4/$0.17
	// per MTok) and the target Kimi K3 second (pricing ~$3/$15/$0.3 per
	// MTok).  The function under test must return the Kimi pricing.
	cat.fetchModels = func(ctx context.Context) ([]Model, error) {
		return []Model{
			{
				ID:   "thinkingmachines/inkling",
				Name: "Thinking Machines: Inkling",
				Pricing: ModelPricing{
					Prompt:         "0.000001",
					Completion:     "0.00000405",
					InputCacheRead: "0.00000017",
				},
			},
			{
				ID:   "moonshotai/kimi-k3",
				Name: "MoonshotAI: Kimi K3",
				Pricing: ModelPricing{
					Prompt:         "0.000003",
					Completion:     "0.000015",
					InputCacheRead: "0.0000003",
				},
			},
		}, nil
	}

	got, err := cat.FetchModel(context.Background(), "moonshotai/kimi-k3")
	if err != nil {
		t.Fatalf("FetchModel: %v", err)
	}

	if got.InputUSDPerToken != 0.000003 {
		t.Fatalf("InputUSDPerToken: got %v, want 0.000003", got.InputUSDPerToken)
	}
	if got.OutputUSDPerToken != 0.000015 {
		t.Fatalf("OutputUSDPerToken: got %v, want 0.000015", got.OutputUSDPerToken)
	}
	if got.InputCachedUSDPerToken != 0.0000003 {
		t.Fatalf("InputCachedUSDPerToken: got %v, want 0.0000003", got.InputCachedUSDPerToken)
	}
}
