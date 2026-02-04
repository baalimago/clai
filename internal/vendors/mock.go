package vendors

import (
	"context"

	"github.com/baalimago/clai/internal/models"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// Mock is a StreamCompleter that streams a fixed, mocked response.
type Mock struct{}

func (m *Mock) Setup() error {
	return nil
}

func (m *Mock) StreamCompletions(ctx context.Context, chat pub_models.Chat) (chan models.CompletionEvent, error) {
	ch := make(chan models.CompletionEvent, 2)
	go func() {
		uMsg, _, _ := chat.LastOfRole("user")
		defer close(ch)
		ch <- uMsg.Content
		ch <- models.StopEvent{}
	}()
	return ch, nil
}
