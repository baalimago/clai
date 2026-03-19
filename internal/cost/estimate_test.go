package cost

import (
	"math"
	"strings"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestManagerEstimateUSD_PromptAndCompletionPricing(t *testing.T) {
	mgr := Manager{price: &ModelPriceScheme{
		InputUSDPerToken:       0.002,
		OutputUSDPerToken:      0.004,
		InputCachedUSDPerToken: 0.001,
	}}
	got, err := mgr.estimateUSD(&pub_models.Usage{
		PromptTokens:     1000,
		CompletionTokens: 500,
		PromptTokensDetails: pub_models.PromptTokensDetails{
			CachedTokens: 0,
		},
	})
	if err != nil {
		t.Fatalf("estimateUSD: %v", err)
	}
	want := 1000*0.002 + 500*0.004
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("estimate mismatch: got %v want %v", got, want)
	}
}

func TestManagerEstimateUSD_CachedTokenPricing(t *testing.T) {
	mgr := Manager{price: &ModelPriceScheme{
		InputUSDPerToken:       1,
		InputCachedUSDPerToken: 0.25,
		OutputUSDPerToken:      2,
	}}
	got, err := mgr.estimateUSD(&pub_models.Usage{
		PromptTokens:     100,
		CompletionTokens: 10,
		PromptTokensDetails: pub_models.PromptTokensDetails{
			CachedTokens: 20,
		},
	})
	if err != nil {
		t.Fatalf("estimateUSD: %v", err)
	}
	want := 80*1.0 + 20*0.25 + 10*2.0
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("estimate mismatch: got %v want %v", got, want)
	}
}

func TestManagerEstimateUSD_ZeroTokensYieldsZero(t *testing.T) {
	mgr := Manager{price: &ModelPriceScheme{
		InputUSDPerToken:  1,
		OutputUSDPerToken: 1,
	}}
	got, err := mgr.estimateUSD(&pub_models.Usage{})
	if err != nil {
		t.Fatalf("estimateUSD: %v", err)
	}
	if got != 0 {
		t.Fatalf("expected zero, got %v", got)
	}
}

func TestManagerEstimateUSD_MissingUsage(t *testing.T) {
	mgr := Manager{price: &ModelPriceScheme{InputUSDPerToken: 1}}
	_, err := mgr.estimateUSD(nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "missing usage") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestManagerEstimateUSD_MissingPricing(t *testing.T) {
	mgr := Manager{price: &ModelPriceScheme{}}
	_, err := mgr.estimateUSD(&pub_models.Usage{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "missing pricing") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestManagerEstimateUSD_NegativeDerivedPromptClamped(t *testing.T) {
	mgr := Manager{price: &ModelPriceScheme{
		InputUSDPerToken:       2,
		InputCachedUSDPerToken: 1,
		OutputUSDPerToken:      3,
	}}
	got, err := mgr.estimateUSD(&pub_models.Usage{
		PromptTokens:     5,
		CompletionTokens: 1,
		PromptTokensDetails: pub_models.PromptTokensDetails{
			CachedTokens: 10,
		},
	})
	if err != nil {
		t.Fatalf("estimateUSD: %v", err)
	}
	want := 10*1.0 + 1*3.0
	if got != want {
		t.Fatalf("estimate mismatch: got %v want %v", got, want)
	}
}
