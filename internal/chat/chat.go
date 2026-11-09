package chat

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/baalimago/clai/internal/asset"
	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func FromPath(ctx context.Context, path string) (pub_models.Chat, error) {
	traceChatf(ctx, "loading chat file path=%q", path)
	if debugChatEnabled(ctx) {
		if stat, err := os.Stat(path); err == nil {
			traceChatf(ctx, "chat file stat path=%q size_bytes=%d", path, stat.Size())
		} else {
			traceChatf(ctx, "chat file stat failed path=%q err=%v", path, err)
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return pub_models.Chat{}, diag.Errorf("failed to read file: %w", err)
	}
	traceChatf(ctx, "chat file read path=%q bytes=%d", path, len(b))
	var chat pub_models.Chat
	err = json.Unmarshal(b, &chat)
	if err != nil {
		return pub_models.Chat{}, diag.Errorf("failed to decode JSON: %w", err)
	}
	traceChatf(ctx, "chat file decoded path=%q chat_id=%q messages=%d", path, chat.ID, len(chat.Messages))

	// Best-effort: restore out-of-band reasoning items. A sidecar problem must not
	// prevent loading the conversation itself.
	if err := loadReasoningSidecars(filepath.Dir(path), &chat); err != nil {
		runenv.Warnf(ctx, runenv.SubsystemChat, "failed to load reasoning sidecar for chat %q: %v", chat.ID, err)
	}

	return chat, nil
}

// Save stores chat's pending images under <saveAt>/assets, then writes it (Persister Save.5).
func Save(ctx context.Context, saveAt string, chat pub_models.Chat) error {
	_, err := saveChat(ctx, saveAt, convAssetStore(saveAt), chat, withIndex)
	return err
}

// SaveWithoutIndex persists the conversation and its sidecar but leaves the
// chat index to a batch writer (UpsertChatIndexBatch).
func SaveWithoutIndex(ctx context.Context, saveAt string, chat pub_models.Chat) error {
	_, err := saveChat(ctx, saveAt, convAssetStore(saveAt), chat, withoutIndex)
	return err
}

type indexMode int

const (
	withIndex indexMode = iota
	withoutIndex
)

func saveChat(ctx context.Context, saveAt string, store asset.AssetStorer, chat pub_models.Chat, index indexMode) (pub_models.Chat, error) {
	chat, err := storePendingImages(store, chat)
	if err != nil {
		return pub_models.Chat{}, err
	}
	// Stamp GroupKey once on first persist (never overwritten).
	if chat.GroupKey == "" {
		chat.GroupKey = ComputeGroupKey(chat)
	}
	b, err := json.MarshalIndent(chat, "", "  ")
	if err != nil {
		return pub_models.Chat{}, diag.Errorf("failed to encode JSON: %w", err)
	}
	b = append(b, '\n')
	fileName := conversationPathFromDir(saveAt, chat.ID)
	for _, subsystem := range []string{runenv.SubsystemReplyMode, runenv.SubsystemVerbose} {
		if runenv.DebugEnabled(ctx, subsystem) {
			runenv.Debugf(ctx, subsystem, "saving chat to: '%v'", fileName)
			break
		}
	}
	traceChatf(ctx, "saving chat file path=%q chat_id=%q messages=%d", fileName, chat.ID, len(chat.Messages))
	if err := utils.WriteFileAtomic(fileName, b, 0o644); err != nil {
		return pub_models.Chat{}, diag.Errorf("failed to write chat file: %w", err)
	}
	if index == withIndex {
		if err := upsertChatIndex(ctx, saveAt, chat); err != nil {
			return pub_models.Chat{}, diag.Errorf("failed to update chat index: %w", err)
		}
	}
	// Best-effort: persist out-of-band reasoning items. The conversation is already
	// saved; a sidecar failure only costs reasoning continuity, not the chat.
	if err := saveReasoningSidecars(saveAt, chat); err != nil {
		runenv.Warnf(ctx, runenv.SubsystemChat, "failed to save reasoning sidecar for chat %q: %v", chat.ID, err)
	}
	return chat, nil
}
