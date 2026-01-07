package chat

import (
	"testing"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestContinueUsesLastProfileFromChatFile(t *testing.T) {
	tmp := t.TempDir()

	// existing chat created with profile "gopher"
	c := pub_models.Chat{
		ID:      "mychat",
		Created: time.Now(),
		Profile: "gopher",
		Messages: []pub_models.Message{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: "yo"},
		},
	}
	if err := Save(tmp, c); err != nil {
		t.Fatalf("save: %v", err)
	}

	h := &ChatHandler{
		convDir: tmp,
		config: NotCyclicalImport{
			UseProfile: "default",
		},
	}

	h.prompt = "mychat"
	loaded, err := h.findChatByID(h.prompt)
	if err != nil {
		t.Fatalf("findChatByID: %v", err)
	}

	// mirror the logic in cont(): prefer stored profile.
	if loaded.Profile != "" {
		h.config.UseProfile = loaded.Profile
	} else if h.config.UseProfile != "" {
		loaded.Profile = h.config.UseProfile
	}

	if h.config.UseProfile != "gopher" {
		t.Fatalf("expected handler to adopt chat profile, got %q", h.config.UseProfile)
	}
}
