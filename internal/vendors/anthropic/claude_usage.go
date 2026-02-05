package anthropic

import pub_models "github.com/baalimago/clai/pkg/text/models"

// TokenUsage implements models.UsageTokenCounter.
//
// Claude exposes the (counted) input token usage via CountInputTokens.
// Anthropic's streaming API does not provide completion token usage in this implementation,
// so we only populate prompt tokens.
func (c *Claude) TokenUsage() *pub_models.Usage {
	if c == nil {
		return nil
	}
	return &pub_models.Usage{
		PromptTokens: c.amInputTokens,
		TotalTokens:  c.amInputTokens,
	}
}
