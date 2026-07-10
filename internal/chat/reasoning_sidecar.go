package chat

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// The reasoning sidecar stores opaque OpenAI Responses reasoning items
// (id + encrypted_content) out of the main conversation JSON, in
// <conversations>/reasoning/<chatid>/<tool_call_id>.json. The first call id is
// already part of the portable transcript, so no OpenAI-only lookup key needs to
// leak into message JSON. Keeping the items out-of-band
// preserves the conversation file's readability and portability, while still
// allowing stateless (store:false) reasoning-model tool loops to replay the
// reasoning on the next turn. All operations are best-effort: a sidecar failure
// must never prevent the conversation itself from saving or loading.

// safeSidecarKey guards an identifier used as a filename against path traversal.
func safeSidecarKey(id string) bool {
	if id == "" || id == "." || id == ".." {
		return false
	}
	return !strings.ContainsAny(id, `/\`)
}

func safeChatID(id string) bool {
	return safeSidecarKey(id)
}

// saveReasoningSidecars writes the reasoning items of each tool-bearing assistant
// turn to its sidecar file. convDir is the conversations directory.
func saveReasoningSidecars(convDir string, chat pub_models.Chat) error {
	if !safeChatID(chat.ID) {
		return fmt.Errorf("unsafe chat id %q", chat.ID)
	}
	for _, msg := range chat.Messages {
		if msg.Role != "assistant" || len(msg.ToolCalls) == 0 || len(msg.ReasoningItems) == 0 {
			continue
		}
		key := msg.ToolCalls[0].ID
		if !safeSidecarKey(key) {
			continue
		}
		dir := reasoningDirFromConvDir(convDir, chat.ID)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create reasoning dir %q: %w", dir, err)
		}
		b, err := json.MarshalIndent(msg.ReasoningItems, "", "  ")
		if err != nil {
			return fmt.Errorf("encode reasoning items: %w", err)
		}
		b = append(b, '\n')
		f := reasoningFileFromConvDir(convDir, chat.ID, key)
		if err := os.WriteFile(f, b, 0o644); err != nil {
			return fmt.Errorf("write reasoning sidecar %q: %w", f, err)
		}
	}
	return nil
}

// loadReasoningSidecars repopulates ReasoningItems on each assistant turn that
// references a sidecar. A missing sidecar (e.g. a pre-feature chat) is skipped.
func loadReasoningSidecars(convDir string, chat *pub_models.Chat) error {
	if !safeChatID(chat.ID) {
		return fmt.Errorf("unsafe chat id %q", chat.ID)
	}
	for i := range chat.Messages {
		msg := &chat.Messages[i]
		if msg.Role != "assistant" || len(msg.ToolCalls) == 0 {
			continue
		}
		key := msg.ToolCalls[0].ID
		if !safeSidecarKey(key) {
			continue
		}
		f := reasoningFileFromConvDir(convDir, chat.ID, key)
		b, err := os.ReadFile(f)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("read reasoning sidecar %q: %w", f, err)
		}
		var items []pub_models.ReasoningItem
		if err := json.Unmarshal(b, &items); err != nil {
			return fmt.Errorf("decode reasoning sidecar %q: %w", f, err)
		}
		msg.ReasoningItems = items
	}
	return nil
}

// removeReasoningSidecars deletes a chat's entire reasoning sidecar directory. Used
// when the conversation is deleted.
func removeReasoningSidecars(convDir, chatID string) error {
	if !safeChatID(chatID) {
		return fmt.Errorf("unsafe chat id %q", chatID)
	}
	dir := reasoningDirFromConvDir(convDir, chatID)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove reasoning dir %q: %w", dir, err)
	}
	return nil
}
