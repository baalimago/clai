package chat

import (
	"crypto/sha256"
	"encoding/hex"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// ComputeGroupKey returns the hex-encoded SHA-256 of the first user message's
// canonical text content. Returns empty string when no user message exists or
// the first user message has no canonical text.
//
// Canonicalization rules:
//  1. If msg.Content (plain string) is non-empty, hash that as raw UTF-8.
//  2. Otherwise, concatenate the Describe() of msg.Parts in order, so an
//     image-only message keys on its "[image <handle>]" labels.
//  3. If the result is empty, return "".
func ComputeGroupKey(chat pub_models.Chat) string {
	msg, err := chat.FirstUserMessage()
	if err != nil {
		return ""
	}
	canonical := msg.Content
	if canonical == "" {
		for _, p := range msg.Parts {
			if p != nil {
				canonical += p.Describe()
			}
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
