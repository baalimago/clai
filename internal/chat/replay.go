package chat

import (
	"errors"
	"fmt"
	"strings"

	"github.com/baalimago/clai/internal/asset"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// Replay prints the most recent message.
//
// If dirScoped is true, it prints the most recent message from the conversation
// bound to the current working directory (and errors if no binding exists).
// Otherwise it prints the most recent message from the global globalScope.json.
// An image prints as "[image <handle>] <asset path>" (query.8).
func Replay(raw bool, dirScoped bool) error {
	claiConfDir, err := utils.GetClaiConfigDir()
	if err != nil {
		return fmt.Errorf("get config dir: %w", err)
	}
	if dirScoped {
		return replayDirScoped(claiConfDir, raw)
	}

	prevReply, err := LoadPrevQuery(claiConfDir)
	if err != nil {
		return fmt.Errorf("failed to load previous reply: %w", err)
	}
	amMessages := len(prevReply.Messages)
	if amMessages == 0 {
		return errors.New("failed to find any recent reply")
	}
	return printReplayed(claiConfDir, prevReply.Messages[amMessages-1], raw)
}

func replayDirScoped(claiConfDir string, raw bool) error {
	ds, err := loadDirScopeForCurrentDir(claiConfDir)
	if err != nil {
		return fmt.Errorf("load dirscope: %w", err)
	}
	if ds.ChatID == "" {
		return errors.New("no directory-scoped conversation bound to current directory")
	}

	convPath := conversationPath(claiConfDir, ds.ChatID)
	c, err := loadConversation(claiConfDir, convPath)
	if err != nil {
		return fmt.Errorf("load conversation for chat_id %q: %w", ds.ChatID, err)
	}
	if len(c.Messages) == 0 {
		return errors.New("directory-scoped conversation has no messages")
	}
	mostRecentMsg := c.Messages[len(c.Messages)-1]
	if raw {
		mostRecentMsg.ReasoningContent = ""
	}
	return printReplayed(claiConfDir, mostRecentMsg, raw)
}

func printReplayed(claiConfDir string, msg pub_models.Message, raw bool) error {
	msg, err := replayText(asset.NewStore(AssetsDir(claiConfDir)), msg)
	if err != nil {
		return fmt.Errorf("render replayed message: %w", err)
	}
	return utils.AttemptPrettyPrint(nil, msg, "system", raw)
}

// replayText returns msg with its parts rendered into Content, one per line: a
// stored image as "[image <handle>] <Store.Path(ref)>", a pending one as
// "[image <handle>] (not stored yet)" (query.8).
func replayText(store asset.AssetStorer, msg pub_models.Message) (pub_models.Message, error) {
	if len(msg.Parts) == 0 {
		return msg, nil
	}
	lines := make([]string, 0, len(msg.Parts))
	for _, p := range msg.Parts {
		img, ok := p.(pub_models.ImagePart)
		if !ok {
			lines = append(lines, p.Describe())
			continue
		}
		line, err := imagePlaceholder(store, img)
		if err != nil {
			return pub_models.Message{}, err
		}
		lines = append(lines, line)
	}
	msg.Content = strings.Join(lines, "\n")
	msg.Parts = nil
	return msg, nil
}

func imagePlaceholder(store asset.AssetStorer, img pub_models.ImagePart) (string, error) {
	label := img.Describe()
	if img.Asset == "" {
		return label + " (not stored yet)", nil
	}
	path, err := store.Path(img.Asset)
	if err != nil {
		return "", fmt.Errorf("%s: %w", label, err)
	}
	return label + " " + path, nil
}
