package text

import (
	"context"
	"os"
	"path"
	"testing"

	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/models"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestReplyMode_AttachesProfileToPrevQueryConversation(t *testing.T) {
	tmpConfigDir := path.Join(t.TempDir(), ".clai")
	if err := os.MkdirAll(path.Join(tmpConfigDir, "conversations"), os.ModePerm); err != nil {
		t.Fatalf("mkdir conversations: %v", err)
	}

	q := Querier[*MockQuerier]{
		Raw:             true,
		out:             os.Stdout,
		shouldSaveReply: true,
		configDir:       tmpConfigDir,
		chat: pub_models.Chat{
			ID:      "prevQuery",
			Profile: "gopher",
			Messages: []pub_models.Message{
				{Role: "system", Content: "prior"},
			},
		},
		Model: &MockQuerier{
			shouldBlock:    false,
			completionChan: make(chan models.CompletionEvent),
			errChan:        make(chan error),
		},
	}

	go func() {
		q.Model.completionChan <- "something"
		q.Model.completionChan <- "CLOSE"
	}()

	if err := q.Query(context.Background()); err != nil {
		t.Fatalf("query: %v", err)
	}

	prev, err := chat.LoadPrevQuery(tmpConfigDir)
	if err != nil {
		t.Fatalf("load prevquery: %v", err)
	}
	if prev.Profile != "gopher" {
		t.Fatalf("expected prevQuery to have profile %q, got %q", "gopher", prev.Profile)
	}
}
