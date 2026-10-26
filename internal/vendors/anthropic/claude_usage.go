package anthropic

import pub_models "github.com/baalimago/clai/pkg/text/models"

// TokenUsage implements models.UsageTokenCounter. Prompt tokens start as the
// counted input and are replaced by the stream's message_start usage, which
// includes cache reads and writes; completion tokens come from message_delta.
func (c *Claude) TokenUsage() *pub_models.Usage {
	if c == nil {
		return nil
	}
	u := &pub_models.Usage{
		PromptTokens:     c.amInputTokens,
		CompletionTokens: c.amOutputTokens,
		TotalTokens:      c.amInputTokens + c.amOutputTokens,
	}
	u.PromptTokensDetails.CachedTokens = c.amCachedTokens
	return u
}

func (c *Claude) recordStreamUsage(u TokenInfo) {
	c.amInputTokens = u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
	c.amCachedTokens = u.CacheReadInputTokens
	c.amOutputTokens = u.OutputTokens
}
