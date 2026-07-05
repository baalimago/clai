package chat

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestComputeGroupKey_plainContent(t *testing.T) {
	chat := pub_models.Chat{
		Messages: []pub_models.Message{
			{Role: "system", Content: "You are helpful."},
			{Role: "user", Content: "fix the auth bug"},
		},
	}
	got := ComputeGroupKey(chat)
	want := hex.EncodeToString(sha256Sum([]byte("fix the auth bug")))
	if got != want {
		t.Fatalf("ComputeGroupKey = %q, want %q", got, want)
	}
	if len(got) != 64 {
		t.Fatalf("expected 64-char hex digest, got %d", len(got))
	}
}

func TestComputeGroupKey_contentParts(t *testing.T) {
	chat := pub_models.Chat{
		Messages: []pub_models.Message{
			{Role: "user", ContentParts: []pub_models.ImageOrTextInput{
				{Text: "hello"},
				{Text: "world"},
			}},
		},
	}
	got := ComputeGroupKey(chat)
	want := hex.EncodeToString(sha256Sum([]byte("helloworld")))
	if got != want {
		t.Fatalf("ComputeGroupKey = %q, want %q", got, want)
	}
}

func TestComputeGroupKey_empty(t *testing.T) {
	// No user message at all
	chat := pub_models.Chat{
		Messages: []pub_models.Message{
			{Role: "system", Content: "You are helpful."},
		},
	}
	if got := ComputeGroupKey(chat); got != "" {
		t.Fatalf("expected empty GroupKey, got %q", got)
	}

	// User message with empty content and no content parts
	chat2 := pub_models.Chat{
		Messages: []pub_models.Message{
			{Role: "user", Content: ""},
		},
	}
	if got := ComputeGroupKey(chat2); got != "" {
		t.Fatalf("expected empty GroupKey, got %q", got)
	}

	// Image-only: ContentParts with no Text
	chat3 := pub_models.Chat{
		Messages: []pub_models.Message{
			{Role: "user", ContentParts: []pub_models.ImageOrTextInput{
				{Type: "image_url"},
			}},
		},
	}
	if got := ComputeGroupKey(chat3); got != "" {
		t.Fatalf("expected empty GroupKey, got %q", got)
	}
}

func TestComputeGroupKey_deterministic(t *testing.T) {
	chat := pub_models.Chat{
		Messages: []pub_models.Message{
			{Role: "user", Content: "hello world"},
		},
	}
	first := ComputeGroupKey(chat)
	for i := 0; i < 100; i++ {
		if got := ComputeGroupKey(chat); got != first {
			t.Fatalf("ComputeGroupKey not deterministic: %q != %q", got, first)
		}
	}
}

func TestComputeGroupKey_differentContent(t *testing.T) {
	chat1 := pub_models.Chat{
		Messages: []pub_models.Message{
			{Role: "user", Content: "hello world"},
		},
	}
	chat2 := pub_models.Chat{
		Messages: []pub_models.Message{
			{Role: "user", Content: "hello world!"},
		},
	}
	if ComputeGroupKey(chat1) == ComputeGroupKey(chat2) {
		t.Fatal("different messages should produce different GroupKeys")
	}
}

func TestComputeGroupKey_whitespaceSensitive(t *testing.T) {
	chat1 := pub_models.Chat{
		Messages: []pub_models.Message{
			{Role: "user", Content: "hello world"},
		},
	}
	chat2 := pub_models.Chat{
		Messages: []pub_models.Message{
			{Role: "user", Content: "hello world "},
		},
	}
	if ComputeGroupKey(chat1) == ComputeGroupKey(chat2) {
		t.Fatal("whitespace differences should produce different GroupKeys")
	}
}

func sha256Sum(data []byte) []byte {
	h := sha256.Sum256(data)
	return h[:]
}
