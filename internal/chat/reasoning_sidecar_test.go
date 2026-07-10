package chat

import (
	"bytes"
	"os"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func reasoningChat() pub_models.Chat {
	return pub_models.Chat{
		ID: "chat123",
		Messages: []pub_models.Message{
			{Role: "user", Content: "hi"},
			{
				Role:      "assistant",
				ToolCalls: []pub_models.Call{{ID: "call_1", Name: "foo"}},
				ReasoningItems: []pub_models.ReasoningItem{{
					ID:               "rs_1",
					EncryptedContent: "SEALED-BLOB",
					Summary:          []string{"planning"},
				}},
			},
		},
	}
}

// TestReasoningSidecar_RoundTripThroughSaveAndFromPath is the core regression: an
// assistant turn's reasoning items survive Save -> FromPath via the sidecar, while
// the opaque blob never leaks into the human-readable conversation JSON.
func TestReasoningSidecar_RoundTripThroughSaveAndFromPath(t *testing.T) {
	t.Parallel()

	convDir := t.TempDir()
	chat := reasoningChat()

	if err := Save(convDir, chat); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// The main conversation JSON stays clean: all OpenAI-only continuity metadata
	// is out-of-band.
	raw, err := os.ReadFile(conversationPathFromDir(convDir, chat.ID))
	if err != nil {
		t.Fatalf("read chat json: %v", err)
	}
	if bytes.Contains(raw, []byte("SEALED-BLOB")) {
		t.Fatalf("encrypted reasoning must NOT be inlined into the conversation JSON")
	}
	if bytes.Contains(raw, []byte("response_id")) {
		t.Fatalf("OpenAI response metadata must not leak into portable message JSON")
	}

	// Sidecar file exists at the first tool-call id, which is already part of the
	// portable transcript and therefore needs no extra inline lookup key.
	if _, err := os.Stat(reasoningFileFromConvDir(convDir, chat.ID, "call_1")); err != nil {
		t.Fatalf("expected sidecar file: %v", err)
	}

	// FromPath restores the reasoning items.
	loaded, err := FromPath(conversationPathFromDir(convDir, chat.ID))
	if err != nil {
		t.Fatalf("FromPath: %v", err)
	}
	if len(loaded.Messages) != 2 {
		t.Fatalf("messages: got %d want 2", len(loaded.Messages))
	}
	got := loaded.Messages[1].ReasoningItems
	if len(got) != 1 {
		t.Fatalf("reasoning items: got %d want 1 (%#v)", len(got), got)
	}
	if got[0].ID != "rs_1" || got[0].EncryptedContent != "SEALED-BLOB" {
		t.Fatalf("restored reasoning item mismatch: %#v", got[0])
	}
	if len(got[0].Summary) != 1 || got[0].Summary[0] != "planning" {
		t.Fatalf("restored summary mismatch: %#v", got[0].Summary)
	}
}

func TestReasoningSidecar_RemoveDeletesDir(t *testing.T) {
	t.Parallel()

	convDir := t.TempDir()
	chat := reasoningChat()
	if err := Save(convDir, chat); err != nil {
		t.Fatalf("Save: %v", err)
	}
	dir := reasoningDirFromConvDir(convDir, chat.ID)
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("expected reasoning dir before delete: %v", err)
	}

	if err := removeReasoningSidecars(convDir, chat.ID); err != nil {
		t.Fatalf("removeReasoningSidecars: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("expected reasoning dir removed, stat err=%v", err)
	}
}

// TestReasoningSidecar_MissingIsNonFatal ensures loading a conversation whose
// sidecar was never written (e.g. a pre-feature chat) simply yields no reasoning
// items rather than an error.
func TestReasoningSidecar_MissingIsNonFatal(t *testing.T) {
	t.Parallel()

	convDir := t.TempDir()
	chat := pub_models.Chat{
		ID: "old",
		Messages: []pub_models.Message{
			{Role: "user", Content: "hi"},
			// References a tool call but no sidecar file exists.
			{Role: "assistant", ToolCalls: []pub_models.Call{{ID: "c", Name: "foo"}}},
		},
	}
	if err := Save(convDir, chat); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := FromPath(conversationPathFromDir(convDir, chat.ID))
	if err != nil {
		t.Fatalf("FromPath must not fail on a missing sidecar: %v", err)
	}
	if len(loaded.Messages[1].ReasoningItems) != 0 {
		t.Fatalf("expected no reasoning items, got %#v", loaded.Messages[1].ReasoningItems)
	}
}

func TestSafeSidecarKey(t *testing.T) {
	t.Parallel()

	cases := map[string]bool{
		"resp_abc123": true,
		"":            false,
		".":           false,
		"..":          false,
		"a/b":         false,
		`a\b`:         false,
		"../escape":   false,
	}
	for id, want := range cases {
		if got := safeSidecarKey(id); got != want {
			t.Fatalf("safeSidecarKey(%q): got %v want %v", id, got, want)
		}
	}
}

func TestReasoningSidecar_RejectsUnsafeChatID(t *testing.T) {
	t.Parallel()

	convDir := t.TempDir()
	outside := reasoningDirFromConvDir(convDir, "safe")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := outside + "/marker"
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := removeReasoningSidecars(convDir, "../reasoning/safe"); err == nil {
		t.Fatal("expected unsafe chat id to be rejected")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("unsafe removal touched data outside its chat directory: %v", err)
	}
}
