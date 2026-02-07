package chat

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"slices"
	"unicode/utf8"

	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

const chatInfoPrintHeight = 13

// chatListTokenStr returns a human-readable representation of total tokens in "kilo" units.
// Examples:
//  - 3013  -> "3K"
//  - 191828 -> "191K"
//  - 15    -> "0.015K"
func chatListTokenStr(item pub_models.Chat) string {
	if item.TokenUsage == nil {
		return "N/A"
	}
	v := item.TokenUsage.TotalTokens
	if v >= 1000 {
		return fmt.Sprintf("%dK", v/1000)
	}
	// show three decimal places for values < 1000 (e.g. 15 -> 0.015K)
	f := float64(v) / 1000.0
	return fmt.Sprintf("%.3fK", f)
}

func (cq *ChatHandler) actOnChat(ctx context.Context, chat pub_models.Chat) error {
	// Print the chat info to the handler's configured output (not os.Stdin/Stdout)
	err := cq.printChatInfo(cq.out, chat)
	if err != nil {
		return fmt.Errorf("failed to printChatInfo: %w", err)
	}
	choice, err := utils.ReadUserInput()
	if err != nil {
		return fmt.Errorf("failed to read user input: %w", err)
	}
	switch choice {
	case "E", "e":
		return cq.handleEditMessages(ctx, chat)
	case "D", "d":
		return cq.handleDeleteMessages(ctx, chat)
	case "B", "b":
		clearErr := utils.ClearTermTo(cq.out, -1, chatInfoPrintHeight)
		if clearErr != nil {
			return fmt.Errorf("failed to clear term: %w", clearErr)
		}

		return cq.handleListCmd(ctx)
	case "P", "p":
		return SaveAsPreviousQuery(cq.confDir, chat)
	case "":
		// Treat empty input (pressing Enter) as "continue" — bind this chat to CWD and
		// print an obfuscated preview, mirroring the behavior of `clai chat continue`.
		// Profile sticking logic from cont(): prefer chat.Profile when set, otherwise
		// stamp current UseProfile into chat.Profile for persistence.
		if chat.Profile != "" {
			cq.config.UseProfile = chat.Profile
		} else if cq.config.UseProfile != "" {
			chat.Profile = cq.config.UseProfile
		}

		if err := cq.printChat(chat); err != nil {
			return fmt.Errorf("failed to print chat: %w", err)
		}

		if err := cq.UpdateDirScopeFromCWD(chat.ID); err != nil {
			return fmt.Errorf("failed to update directory-scoped binding: %w", err)
		}
		ancli.Noticef("chat %s is now replyable with flag \"clai -dre query <prompt>\"\n", chat.ID)
		return nil
	default:
		return fmt.Errorf("unknown choice: %q", choice)
	}
}

func (cq *ChatHandler) handleEditMessages(ctx context.Context, chat pub_models.Chat) error {
	clearErr := utils.ClearTermTo(cq.out, -1, chatInfoPrintHeight)
	if clearErr != nil {
		return fmt.Errorf("failed to clear term: %w", clearErr)
	}
	editErr := cq.editMessageInChat(chat)
	if editErr != nil {
		return fmt.Errorf("failed to editChat: %w", editErr)
	}
	return cq.actOnChat(ctx, chat)
}

func (cq *ChatHandler) handleDeleteMessages(ctx context.Context, chat pub_models.Chat) error {
	clearErr := utils.ClearTermTo(cq.out, -1, chatInfoPrintHeight)
	if clearErr != nil {
		return fmt.Errorf("failed to clear term: %w", clearErr)
	}
	editErr := cq.deleteMessageInChat(chat)
	if editErr != nil {
		return fmt.Errorf("failed to editChat: %w", editErr)
	}
	updatedChat, getChatErr := cq.getByID(chat.ID)
	if getChatErr != nil {
		return fmt.Errorf("failed to re-fetch chat: %w", getChatErr)
	}
	return cq.actOnChat(ctx, updatedChat)
}

func (cq *ChatHandler) list() ([]pub_models.Chat, error) {
	files, err := os.ReadDir(cq.convDir)
	if err != nil {
		return nil, fmt.Errorf("failed to list conversations: %w", err)
	}
	var chats []pub_models.Chat
	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.PrintOK(fmt.Sprintf("found '%v' conversations:\n", len(files)))
	}
	for _, dirEntry := range files {
		// Skip directories (e.g. conversations/dirs for dirscoped bindings)
		if dirEntry.IsDir() {
			continue
		}
		p := path.Join(cq.convDir, dirEntry.Name())
		chat, pathErr := FromPath(p)
		if pathErr != nil {
			return nil, fmt.Errorf("failed to get chat: %q: %w", p, pathErr)
		}
		chats = append(chats, chat)
	}
	slices.SortFunc(chats, func(a, b pub_models.Chat) int {
		return b.Created.Compare(a.Created)
	})
	return chats, err
}

func (cq *ChatHandler) handleListCmd(ctx context.Context) error {
	chats, err := cq.list()
	if err != nil {
		return fmt.Errorf("failed to list: %w", err)
	}
	return cq.listChats(ctx, chats)
}

func (cq *ChatHandler) listChats(
	ctx context.Context,
	chats []pub_models.Chat,
) error {
	ancli.PrintOK(
		fmt.Sprintf(
			"found '%v' conversations:\n",
			len(chats),
		),
	)

	selectedNumbers, err := utils.SelectFromTable(fmt.Sprintf(selectChatTblFormat,
		"Index",
		"Created",
		"Messages",
		"Tokens",
		"Prompt"), chats,
		selectChatTblChoicesFormat,
		func(i int, item pub_models.Chat) (string, error) {
			tokenStr := chatListTokenStr(item)

			prefix := fmt.Sprintf(
				selectChatTblFormat,
				fmt.Sprintf("%v", i),
				item.Created.Format(
					"2006-01-02 15:04:05",
				),
				len(item.Messages),
				tokenStr,
				"",
			)
			firstMessages := ""
			uMsg, uMsgErr := item.FirstUserMessage()
			if uMsgErr == nil {
				firstMessages = uMsg.Content
			}

			withSummary, err := utils.WidthAppropriateStringTrunc(firstMessages, prefix, 15)
			if err != nil {
				return "", fmt.Errorf("failed to get widthAppropriateChatSummary: %w", err)
			}

			return withSummary, nil
		},
		10,
		true,
	)
	if err != nil {
		return fmt.Errorf("failed to select chat: %w", err)
	}
	return cq.actOnChat(ctx, chats[selectedNumbers[0]])
}

// fillRemainderOfTermWidth by:
// 1. Counting remaining width until termWidth
// 2. Fit remainder into remaining width, keeping padding
// 3. Format: "<start> ... <end>" when truncating

func (cq *ChatHandler) printChatInfo(w io.Writer, chat pub_models.Chat) error {
	claiConfDir, err := utils.GetClaiConfigDir()
	if err != nil {
		return fmt.Errorf("failed to get clai config dir: %w", err)
	}
	filePath := path.Join(claiConfDir, "conversations", chat.ID)
	messageTypeCounter := make(map[string]int)
	for _, m := range chat.Messages {
		messageTypeCounter[m.Role] += 1
	}
	firstMessages := ""
	uMsg, uMsgErr := chat.FirstUserMessage()
	if uMsgErr == nil {
		firstMessages = uMsg.Content
	}
	summary, err := utils.WidthAppropriateStringTrunc(firstMessages, "summary: \"", 10)
	if err != nil {
		return fmt.Errorf("failed to create widthAppropriateChatSummary: %w", err)
	}
	fmt.Fprintf(w, actOnChatFormat,
		filePath,
		chat.Created,
		messageTypeCounter["user"],
		messageTypeCounter["tools"],
		messageTypeCounter["system"],
		messageTypeCounter["assistant"],
		summary+"\"",
	)
	return nil
}

func editorEditString(toEdit string) (string, error) {
	ret := toEdit

	f, err := os.CreateTemp("", "clai-edit-*.txt")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(f.Name())

	_, err = f.WriteString(ret)
	if err != nil {
		_ = f.Close()
		return "", fmt.Errorf("failed to write temp file: %w", err)
	}
	if closeErr := f.Close(); closeErr != nil {
		return "", fmt.Errorf("failed to close temp file: %w", closeErr)
	}

	editor := os.Getenv("EDITOR")
	if editor == "" {
		return "", fmt.Errorf("environment variable EDITOR is not set")
	}

	cmd := exec.Command(editor, f.Name())
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if runErr := cmd.Run(); runErr != nil {
		return "", fmt.Errorf("failed to edit file %s: %w", f.Name(), runErr)
	}

	b, err := os.ReadFile(f.Name())
	if err != nil {
		return "", fmt.Errorf("failed to read edited file: %w", err)
	}

	return string(b), nil
}

func (cq *ChatHandler) deleteMessageInChat(
	chat pub_models.Chat,
) error {
	header := fmt.Sprintf(
		editMessageTblFormat,
		"Index",
		"Role",
		"Length",
		"Summary",
	)
	selectedIndices, err := utils.SelectFromTable(
		header,
		chat.Messages,
		deleteMessagesChoicesFormat,
		func(i int, t pub_models.Message) (string, error) {
			prefix := fmt.Sprintf(
				editMessageTblFormat,
				i,
				t.Role,
				utf8.RuneCount([]byte(t.Content)),
				"",
			)

			withSummary, err := utils.WidthAppropriateStringTrunc(
				t.Content,
				prefix,
				25,
			)
			if err != nil {
				return "", fmt.Errorf(
					"failed to get widthAppropriateChatSummary: %w",
					err,
				)
			}

			return withSummary, nil
		},
		10,
		false,
	)
	if err != nil {
		return fmt.Errorf(
			"failed to select from table: %w",
			err,
		)
	}

	idxSet := make(map[int]struct{}, len(selectedIndices))
	for _, idx := range selectedIndices {
		idxSet[idx] = struct{}{}
	}

	filtered := make(
		[]pub_models.Message,
		0,
		len(chat.Messages),
	)
	for i, m := range chat.Messages {
		if _, ok := idxSet[i]; ok {
			continue
		}
		filtered = append(filtered, m)
	}
	chat.Messages = filtered

	if err := Save(cq.convDir, chat); err != nil {
		return fmt.Errorf("failed to save chat: %w", err)
	}

	ancli.Okf(
		"modified chat: '%v', deleted messages: '%v'",
		chat.ID,
		selectedIndices,
	)
	return nil
}

func (cq *ChatHandler) editMessageInChat(chat pub_models.Chat) error {
	header := fmt.Sprintf(editMessageTblFormat, "Index", "Role", "Length", "Summary")
	selectedNumbers, err := utils.SelectFromTable(header, chat.Messages, editMessageChoicesFormat,
		func(i int, t pub_models.Message) (string, error) {
			prefix := fmt.Sprintf(editMessageTblFormat,
				i,
				t.Role,
				utf8.RuneCount([]byte(t.Content)),
				"",
			)

			withSummary, err := utils.WidthAppropriateStringTrunc(t.Content, prefix, 25)
			if err != nil {
				return "", fmt.Errorf("failed to get widthAppropriateChatSummary: %w", err)
			}

			return withSummary, nil
		}, 10, true,
	)
	if err != nil {
		return fmt.Errorf("failed to select from table: %w", err)
	}
	selectedNumber := selectedNumbers[0]
	selectedMessage := chat.Messages[selectedNumber]
	editedString, err := editorEditString(selectedMessage.Content)
	if err != nil {
		return fmt.Errorf("failed to escapeEdit string: %w", err)
	}

	selectedMessage.Content = editedString
	chat.Messages[selectedNumber] = selectedMessage

	err = Save(cq.convDir, chat)
	if err != nil {
		return fmt.Errorf("failed to save chat: %w", err)
	}

	ancli.Okf("modified chat: '%v', message with index: '%v'", chat.ID, selectedNumbers)
	return nil
}
