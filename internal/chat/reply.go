package chat

import (
	"fmt"
	"os"
	"time"

	"github.com/baalimago/clai/internal/asset"
	"github.com/baalimago/clai/internal/chatid"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// SaveAsPreviousQuery saves the global-scoped chat at
// <claiConfDir>/conversations/globalScope.json with ID globalScope, storing its
// pending images first (Persister Save.5).
//
// Name kept for backwards compatibility.
func SaveAsPreviousQuery(claiConfDir string, chat pub_models.Chat) error {
	return promote(claiConfDir, convAssetStore(conversationsDir(claiConfDir)), chat)
}

func promote(claiConfDir string, store asset.AssetStorer, chat pub_models.Chat) error {
	chat, err := storePendingImages(store, chat)
	if err != nil {
		return err
	}
	traceChatf("save previous query start conf_dir=%q chat_id=%q messages=%d profile=%q", claiConfDir, chat.ID, len(chat.Messages), chat.Profile)
	sourceChat := chat
	convPath := conversationsDir(claiConfDir)
	if _, convDirExistsErr := os.Stat(convPath); convDirExistsErr != nil {
		if err := os.MkdirAll(convPath, 0o755); err != nil {
			return fmt.Errorf("create conversations dir %q: %w", convPath, err)
		}
	}
	globalScopeChat := pub_models.Chat{
		Created:          time.Now(),
		ID:               globalScopeChatID,
		Profile:          chat.Profile,
		OriginDir:        chat.OriginDir,
		Title:            chat.Title,
		Summary:          chat.Summary,
		SummaryAt:        chat.SummaryAt,
		Messages:         chat.Messages,
		TokenUsage:       chat.TokenUsage,
		RecentTokenUsage: chat.RecentTokenUsage,
		Queries:          chat.Queries,
	}
	// This check avoids storing queries without any replies, which would most likely
	// flood the conversations needlessly. Only promote to a fresh conversation when
	// the chat does not already identify an existing persisted conversation.
	if len(chat.Messages) > 2 && chat.Messages[len(chat.Messages)-1].Role != "user" && (sourceChat.ID == "" || sourceChat.ID == globalScopeChatID) {
		firstUserMsg, err := globalScopeChat.FirstUserMessage()
		if err != nil {
			return fmt.Errorf("failed to get first user message: %w", err)
		}
		traceChatf("save previous query promoting conversation first_user_len=%d", len(firstUserMsg.Content))
		convID, err := chatid.New()
		if err != nil {
			return fmt.Errorf("generate promoted conversation id: %w", err)
		}
		convChat := pub_models.Chat{
			Created:          time.Now(),
			ID:               convID,
			Profile:          chat.Profile,
			OriginDir:        chat.OriginDir,
			Title:            chat.Title,
			Summary:          chat.Summary,
			SummaryAt:        chat.SummaryAt,
			Messages:         chat.Messages,
			TokenUsage:       chat.TokenUsage,
			RecentTokenUsage: chat.RecentTokenUsage,
			Queries:          chat.Queries,
		}
		traceChatf("save previous query conversation path=%q conv_id=%q", convPath, convChat.ID)
		if _, err := saveChat(convPath, store, convChat, withIndex); err != nil {
			return fmt.Errorf("failed to save previous query as new conversation: %w", err)
		}
	}

	traceChatf("save previous query global scope path=%q", GlobalScopePath(claiConfDir))
	if _, err := saveChat(convPath, store, globalScopeChat, withIndex); err != nil {
		return fmt.Errorf("save global scope chat: %w", err)
	}
	if sourceChat.ID != "" && sourceChat.ID != globalScopeChatID {
		traceChatf("save previous query update source conversation path=%q chat_id=%q", convPath, sourceChat.ID)
		if _, err := saveChat(convPath, store, sourceChat, withIndex); err != nil {
			return fmt.Errorf("save source conversation chat: %w", err)
		}
	}
	return nil
}

// LoadPrevQuery loads the global-scoped chat.
// Name kept for backwards compatibility.
func LoadPrevQuery(claiConfDir string) (pub_models.Chat, error) {
	traceChatf("load previous query conf_dir=%q", claiConfDir)
	c, err := LoadGlobalScope(claiConfDir)
	if err != nil {
		return pub_models.Chat{}, fmt.Errorf("load global scope chat: %w", err)
	}
	traceChatf("load previous query done chat_id=%q messages=%d", c.ID, len(c.Messages))
	return c, nil
}
