package chat

import (
	"testing"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestSaveAsPreviousQuery_PreservesProfile(t *testing.T) {
	tmp := t.TempDir()

	given := pub_models.Chat{
		ID:      "anything",
		Created: time.Now(),
		Profile: "gopher",
		Messages: []pub_models.Message{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: "yo"},
			{Role: "system", Content: "final"},
		},
	}

	if err := SaveAsPreviousQuery(tmp, given); err != nil {
		t.Fatalf("SaveAsPreviousQuery: %v", err)
	}

	got, err := LoadPrevQuery(tmp)
	if err != nil {
		t.Fatalf("LoadPrevQuery: %v", err)
	}

	if got.Profile != "gopher" {
		t.Fatalf("expected profile %q got %q", "gopher", got.Profile)
	}
}
