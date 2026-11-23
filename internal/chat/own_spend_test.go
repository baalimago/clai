package chat

import (
	"testing"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func ownQuery(at time.Time, tokens int, cost float64) pub_models.QueryCost {
	return pub_models.QueryCost{CreatedAt: at, CostUSD: cost, Usage: pub_models.Usage{TotalTokens: tokens}}
}

func TestChatOwnSpend(t *testing.T) {
	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Hour)
	t3 := t2.Add(time.Hour)
	t4 := t3.Add(time.Hour)
	inherited := []pub_models.QueryCost{ownQuery(t1, 10, 0.1), ownQuery(t2, 20, 0.2)}
	fork := func(queries ...pub_models.QueryCost) pub_models.Chat {
		return pub_models.Chat{ID: "f", Created: t3, Source: "clai", SourceID: "p", Queries: queries}
	}
	for _, tc := range []struct {
		name string
		chat pub_models.Chat
		want OwnSpend
	}{
		{"parent counts every query", pub_models.Chat{ID: "p", Created: t1, Queries: inherited}, OwnSpend{Tokens: 30, CostUSD: 0.1 + 0.2}},
		{"fork counts only its own query", fork(append(append([]pub_models.QueryCost{}, inherited...), ownQuery(t4, 5, 0.05))...), OwnSpend{Tokens: 5, CostUSD: 0.05}},
		{"fork with only inherited queries", fork(inherited...), OwnSpend{}},
		{"zero-time query counts on a non-fork", pub_models.Chat{ID: "p", Created: t1, Queries: []pub_models.QueryCost{ownQuery(time.Time{}, 7, 0.7)}}, OwnSpend{Tokens: 7, CostUSD: 0.7}},
		{"zero-time query is not counted on a fork", fork(ownQuery(time.Time{}, 7, 0.7)), OwnSpend{}},
		{"no queries falls back to usage", pub_models.Chat{ID: "p", Created: t1, TokenUsage: &pub_models.Usage{TotalTokens: 99}}, OwnSpend{Tokens: 99}},
		{"fork with inherited usage and no queries", pub_models.Chat{ID: "f", Created: t3, Source: "clai", SourceID: "p", TokenUsage: &pub_models.Usage{TotalTokens: 99}}, OwnSpend{}},
		{"no queries and no usage", pub_models.Chat{ID: "p", Created: t1}, OwnSpend{}},
		{"zero Created counts every dated query", pub_models.Chat{ID: "p", Queries: inherited}, OwnSpend{Tokens: 30, CostUSD: 0.1 + 0.2}},
		{"source clai without SourceID is not a fork", pub_models.Chat{ID: "p", Created: t1, Source: "clai", Queries: []pub_models.QueryCost{ownQuery(time.Time{}, 3, 0)}}, OwnSpend{Tokens: 3}},
		{"queries all before Created are not counted", pub_models.Chat{ID: "p", Created: t4, TokenUsage: &pub_models.Usage{TotalTokens: 50}, Queries: inherited}, OwnSpend{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := chatOwnSpend(tc.chat)
			if got.Tokens != tc.want.Tokens || got.CostUSD < tc.want.CostUSD-1e-9 || got.CostUSD > tc.want.CostUSD+1e-9 {
				t.Fatalf("chatOwnSpend = %+v, want %+v", got, tc.want)
			}
		})
	}
}
