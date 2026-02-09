package text

import (
	"context"

	"github.com/baalimago/clai/internal/models"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type mockCompleter struct{}

func (m mockCompleter) Setup() error { return nil }

func (m mockCompleter) StreamCompletions(ctx context.Context, chat pub_models.Chat) (chan models.CompletionEvent, error) {
	ch := make(chan models.CompletionEvent)
	close(ch)
	return ch, nil
}
