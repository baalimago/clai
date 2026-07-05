package chat

import (
	"crypto/sha256"
	"encoding/hex"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// ComputeGroupKey returns the hex-encoded SHA-256 of the first user message's
// canonical text content. Returns empty string when no user message exists or
// the first user message has no text content (image-only).
//
// Canonicalization rules:
//  1. If msg.Content (plain string) is non-empty, hash that as raw UTF-8.
//  2. Otherwise, concatenate the .Text fields of msg.ContentParts in order.
//  3. If the result is empty, return "".
func ComputeGroupKey(chat pub_models.Chat) string {
	msg, err := chat.FirstUserMessage()
	if err != nil {
		return ""
	}
	canonical := msg.Content
	if canonical == "" {
		for _, cp := range msg.ContentParts {
			canonical += cp.Text
		}
	}
	if canonical == "" {
		return ""
	}
	return ComputeGroupKeyFromText(canonical)
}

// ComputeGroupKeyFromText returns the hex-encoded SHA-256 of the given text.
// Returns empty string when text is empty.
func ComputeGroupKeyFromText(text string) string {
	if text == "" {
		return ""
	}
	h := sha256.Sum256([]byte(text))
	return hex.EncodeToString(h[:])
}
