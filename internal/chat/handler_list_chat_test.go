package chat

import (
	"testing"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestChatListTokensOrNA(t *testing.T) {
	chWith := pub_models.Chat{
		Created: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		ID:      "a",
		TokenUsage: &pub_models.Usage{
			TotalTokens: 42,
		},
	}
	chWithout := pub_models.Chat{
		Created:    time.Date(2026, 1, 2, 3, 4, 4, 0, time.UTC),
		ID:         "b",
		TokenUsage: nil,
	}

	if got := chatListTokenStr(chWith); got != "0.042K" {
		t.Fatalf("with usage: want %q, got %q", "0.042K", got)
	}
	if got := chatListTokenStr(chWithout); got != "N/A" {
		t.Fatalf("without usage: want %q, got %q", "N/A", got)
	}
}
