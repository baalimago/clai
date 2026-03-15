package cost

import (
	"fmt"

	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

type ModelPriceScheme struct {
	InputUSDPerToken       float64 `json:"input_usd_per_token"`
	InputCachedUSDPerToken float64 `json:"input_cached_usd_per_token"`
	OutputUSDPerToken      float64 `json:"output_usd_per_token"`
}

func (m ModelPriceScheme) HasAnyPricing() bool {
	return m.InputUSDPerToken > 0 || m.InputCachedUSDPerToken > 0 || m.OutputUSDPerToken > 0
}

func (m *Manager) estimateUSD(usage *pub_models.Usage) (float64, error) {
	if usage == nil {
		return 0, fmt.Errorf("estimate query cost: missing usage")
	}

	if !m.price.HasAnyPricing() {
		return 0, fmt.Errorf("estimate query cost: missing pricing")
	}

	cachedPromptTokens := usage.PromptTokensDetails.CachedTokens
	nonCachedPromptTokens := max(usage.PromptTokens-cachedPromptTokens, 0)

	cachedPrice := m.price.InputCachedUSDPerToken
	if cachedPrice == 0 {
		cachedPrice = m.price.InputUSDPerToken
	}

	total := float64(nonCachedPromptTokens)*m.price.InputUSDPerToken +
		float64(cachedPromptTokens)*cachedPrice +
		float64(usage.CompletionTokens)*m.price.OutputUSDPerToken

	if m.debug {
		ancli.Noticef("found total: %v", total)
	}
	return total, nil
}
