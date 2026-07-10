package chat

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

func FromPath(path string) (pub_models.Chat, error) {
	traceChatf("loading chat file path=%q", path)
	if debugChatEnabled() {
		if stat, err := os.Stat(path); err == nil {
			traceChatf("chat file stat path=%q size_bytes=%d", path, stat.Size())
		} else {
			traceChatf("chat file stat failed path=%q err=%v", path, err)
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return pub_models.Chat{}, fmt.Errorf("failed to read file: %w", err)
	}
	traceChatf("chat file read path=%q bytes=%d", path, len(b))
	var chat pub_models.Chat
	err = json.Unmarshal(b, &chat)
	if err != nil {
		return pub_models.Chat{}, fmt.Errorf("failed to decode JSON: %w", err)
	}
	traceChatf("chat file decoded path=%q chat_id=%q messages=%d", path, chat.ID, len(chat.Messages))

	// Best-effort: restore out-of-band reasoning items. A sidecar problem must not
	// prevent loading the conversation itself.
	if err := loadReasoningSidecars(filepath.Dir(path), &chat); err != nil {
		ancli.Warnf("failed to load reasoning sidecar for chat %q: %v", chat.ID, err)
	}

	return chat, nil
}

func Save(saveAt string, chat pub_models.Chat) error {
	// Stamp GroupKey once on first persist (never overwritten).
	if chat.GroupKey == "" {
		chat.GroupKey = ComputeGroupKey(chat)
	}
	b, err := json.MarshalIndent(chat, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode JSON: %w", err)
	}
	b = append(b, '\n')
	fileName := conversationPathFromDir(saveAt, chat.ID)
	if misc.Truthy(os.Getenv("DEBUG")) && misc.Truthy(os.Getenv("DEBUG_VERBOSE")) || misc.Truthy(os.Getenv("DEBUG_REPLY_MODE")) {
		ancli.PrintOK(fmt.Sprintf("saving chat to: '%v'", fileName))
	}
	traceChatf("saving chat file path=%q chat_id=%q messages=%d", fileName, chat.ID, len(chat.Messages))
	if err := os.WriteFile(fileName, b, 0o644); err != nil {
		return fmt.Errorf("failed to write chat file: %w", err)
	}
	if err := upsertChatIndex(saveAt, chat); err != nil {
		return fmt.Errorf("failed to update chat index: %w", err)
	}
	// Best-effort: persist out-of-band reasoning items. The conversation is already
	// saved; a sidecar failure only costs reasoning continuity, not the chat.
	if err := saveReasoningSidecars(saveAt, chat); err != nil {
		ancli.Warnf("failed to save reasoning sidecar for chat %q: %v", chat.ID, err)
	}
	return nil
}
