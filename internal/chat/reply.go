package chat

import (
	"context"
	"os"
	"time"

	"github.com/baalimago/clai/internal/asset"
	"github.com/baalimago/clai/internal/chatid"
	"github.com/baalimago/clai/internal/diag"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// RunOutcome says how the run that produced a chat ended.
type RunOutcome int

const (
	// RunAnswered is a run that ended with its reply, or a chat that was not
	// produced by a run at all.
	RunAnswered RunOutcome = iota
	// RunFailedBeforeAnswer is a run that failed before it answered. Its fresh
	// chat is still saved, so the next -re retries with the prompt.
	RunFailedBeforeAnswer
)

// SaveAsPreviousQuery makes chat the previous query: it persists chat and then
// records it as the conversation replies continue, storing its pending images
// first (Persister Save.5).
//
// Name kept for backwards compatibility (it once wrote a mirror copy of the
// chat; see MigratePreviousQuery).
func SaveAsPreviousQuery(ctx context.Context, claiConfDir string, chat pub_models.Chat) error {
	_, err := promote(ctx, claiConfDir, convAssetStore(conversationsDir(claiConfDir)), chat, RunAnswered)
	return err
}

// SavedID is the conversation a save wrote; empty when it wrote none.
type SavedID string

func promote(ctx context.Context, claiConfDir string, store asset.AssetStorer, chat pub_models.Chat, outcome RunOutcome) (SavedID, error) {
	chat, err := storePendingImages(store, chat)
	if err != nil {
		return "", err
	}
	traceChatf(ctx, "save previous query start conf_dir=%q chat_id=%q messages=%d profile=%q", claiConfDir, chat.ID, len(chat.Messages), chat.Profile)
	sourceChat := chat
	convPath := conversationsDir(claiConfDir)
	if _, convDirExistsErr := os.Stat(convPath); convDirExistsErr != nil {
		if err := os.MkdirAll(convPath, 0o755); err != nil {
			return "", diag.Errorf("create conversations dir %q: %w", convPath, err)
		}
	}
	// The sentinel id old mirrors carried is not an identity: a caller that
	// still passes it gets a fresh conversation instead of a file by that name.
	targetID := sourceChat.ID
	if targetID == legacyGlobalScopeChatID {
		targetID = ""
	}
	// This check avoids storing queries without any replies, which would most likely
	// flood the conversations needlessly, except for a run that failed before
	// answering: its prompt is what -re retries. Only promote to a fresh
	// conversation when the chat does not already identify an existing persisted
	// conversation.
	if targetID == "" && worthPromoting(chat, outcome) {
		firstUserMsg, err := sourceChat.FirstUserMessage()
		if err != nil {
			return "", diag.Errorf("failed to get first user message: %w", err)
		}
		traceChatf(ctx, "save previous query promoting conversation first_user_len=%d", len(firstUserMsg.Content))
		convID, err := chatid.New()
		if err != nil {
			return "", diag.Errorf("generate promoted conversation id: %w", err)
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
		traceChatf(ctx, "save previous query conversation path=%q conv_id=%q", convPath, convChat.ID)
		if _, err := saveChat(ctx, convPath, store, convChat, withIndex); err != nil {
			return "", diag.Errorf("failed to save previous query as new conversation: %w", err)
		}
		targetID = convID
	}

	if sourceChat.ID != "" && sourceChat.ID != legacyGlobalScopeChatID {
		traceChatf(ctx, "save previous query update source conversation path=%q chat_id=%q", convPath, sourceChat.ID)
		if _, err := saveChat(ctx, convPath, store, sourceChat, withIndex); err != nil {
			return "", diag.Errorf("save source conversation chat: %w", err)
		}
	}
	if targetID == "" {
		// Nothing was persisted, so there is no conversation to point at. The
		// existing record stays: it names the last conversation that exists.
		return "", nil
	}
	if err := SavePreviousQuery(ctx, claiConfDir, targetID, time.Now()); err != nil {
		return "", diag.Errorf("save previous query record: %w", err)
	}
	return SavedID(targetID), nil
}

// LoadPrevQuery resolves the previous query to the conversation it names.
// Name kept for backwards compatibility.
func LoadPrevQuery(ctx context.Context, claiConfDir string) (pub_models.Chat, error) {
	prev, err := LoadPreviousQuery(ctx, claiConfDir)
	if err != nil {
		return pub_models.Chat{}, err
	}
	return prev.Chat, nil
}

func worthPromoting(chat pub_models.Chat, outcome RunOutcome) bool {
	if len(chat.Messages) == 0 {
		return false
	}
	if chat.Messages[len(chat.Messages)-1].Role != "user" {
		return len(chat.Messages) > 2
	}
	return outcome == RunFailedBeforeAnswer
}
