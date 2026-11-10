package chat

import (
	"context"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestCloneForeignChat_RejectsMissingSourceFields(t *testing.T) {
	cq, _ := newTestHandler(t)

	_, err := cq.cloneForeignChat(context.Background(), pub_models.Chat{Source: "", SourceID: ""})
	if err == nil {
		t.Fatalf("expected error")
	}
}
