package models

import (
	"testing"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestSummaryApplyTo(t *testing.T) {
	t.Run("stamps fields and appends queries", func(t *testing.T) {
		at := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
		chat := pub_models.Chat{Queries: []pub_models.QueryCost{{Model: "main"}}}
		s := Summary{
			Title:       "A title",
			Summary:     "A summary.",
			GeneratedAt: at,
			Queries:     []pub_models.QueryCost{{Model: "sum", Purpose: "summary"}},
		}
		s.ApplyTo(&chat)
		if chat.Title != "A title" || chat.Summary != "A summary." {
			t.Fatalf("label not stamped: %+v", chat)
		}
		if !chat.SummaryAt.Equal(at) {
			t.Fatalf("expected SummaryAt %v, got %v", at, chat.SummaryAt)
		}
		if len(chat.Queries) != 2 || chat.Queries[0].Model != "main" || chat.Queries[1].Purpose != "summary" {
			t.Fatalf("queries not appended in order: %+v", chat.Queries)
		}
	})

	t.Run("zero GeneratedAt falls back to now in UTC", func(t *testing.T) {
		before := time.Now()
		var chat pub_models.Chat
		Summary{Title: "t", Summary: "s"}.ApplyTo(&chat)
		if chat.SummaryAt.Before(before) || chat.SummaryAt.After(time.Now()) {
			t.Fatalf("expected SummaryAt within the call window, got %v", chat.SummaryAt)
		}
		if chat.SummaryAt.Location() != time.UTC {
			t.Fatalf("expected a UTC stamp, got location %v", chat.SummaryAt.Location())
		}
		if chat.Queries != nil {
			t.Fatalf("expected no queries appended, got %+v", chat.Queries)
		}
	})
}
