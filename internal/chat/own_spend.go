package chat

import (
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// OwnSpend is what a chat spent itself.
type OwnSpend struct {
	Tokens  int
	CostUSD float64
}

// chatOwnSpend totals the queries a chat created itself (I10, ownSpend.1-4).
func chatOwnSpend(chat pub_models.Chat) OwnSpend {
	isFork := chat.Source == "clai" && chat.SourceID != ""
	if len(chat.Queries) == 0 {
		if isFork || chat.TokenUsage == nil {
			return OwnSpend{}
		}
		return OwnSpend{Tokens: chat.TokenUsage.TotalTokens}
	}
	var counted []pub_models.QueryCost
	var cost float64
	for _, q := range chat.Queries {
		switch {
		case q.CreatedAt.IsZero() && isFork:
			continue
		case !q.CreatedAt.IsZero() && q.CreatedAt.Before(chat.Created):
			continue
		}
		counted = append(counted, q)
		cost += q.CostUSD
	}
	return OwnSpend{Tokens: aggregateQueryTotalTokens(counted), CostUSD: cost}
}
