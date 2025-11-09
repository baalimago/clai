package chat

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

const chatInfoPrintHeight = 13

func (cq *ChatHandler) actOnChat(ctx context.Context, chat pub_models.Chat) error {
	err := cq.printChatInfo(os.Stdin, chat)
	if err != nil {
		return fmt.Errorf("failed to printChatInfo: %w", err)
	}
	choice, err := utils.ReadUserInput()
	if err != nil {
		return fmt.Errorf("falied to read user input: %w", err)
	}
	switch choice {
	case "E", "e":
		return cq.handleEditMessages(ctx, chat)
	case "D", "d":
		return cq.handleDeleteMessages(ctx, chat)
	case "B", "b":
		clearErr := utils.ClearTermTo(-1, chatInfoPrintHeight)
		if clearErr != nil {
			return fmt.Errorf("failed to clear term: %w", clearErr)
		}

		return cq.handleListCmd(ctx)
	case "C", "c":
		err = cq.printChat(chat)
		if err != nil {
			return fmt.Errorf(
				"selection ok, print chat not ok: %w",
				err,
			)
		}
		cq.chat = chat
		return cq.loop(ctx)

	case "P", "p":
		return SaveAsPreviousQuery(cq.confDir, chat.Messages)
	default:
		return fmt.Errorf("unknown choice: '%v'", choice)
	}
}

func (cq *ChatHandler) handleEditMessages(ctx context.Context, chat pub_models.Chat) error {
	clearErr := utils.ClearTermTo(-1, chatInfoPrintHeight)
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
	clearErr := utils.ClearTermTo(-1, chatInfoPrintHeight)
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
	for _, file := range files {
		chat, pathErr := FromPath(path.Join(cq.convDir, file.Name()))
		if pathErr != nil {
			return nil, fmt.Errorf("failed to get chat: %w", pathErr)
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
		return fmt.Errorf("failed to list: %v", err)
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
		"Prompt"), chats,
		selectChatTblChoicesFormat,
		func(i int, item pub_models.Chat) (string, error) {
			prefix := fmt.Sprintf(
				selectChatTblFormat,
				fmt.Sprintf("%v", i),
				item.Created.Format(
					"2006-01-02 15:04:05",
				),
				len(item.Messages),
				"",
			)
			firstMessages := ""
			uMsg, uMsgErr := item.FirstUserMessage()
			if uMsgErr == nil {
				firstMessages = uMsg.Content
			}

			withSummary, err := widthAppropriateChatSummary(firstMessages, prefix, 15)
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
func fillRemainderOfTermWidth(prefix, remainder string, termWidth, padding int) string {
	infix := " ... "
	infixLen := utf8.RuneCountInString(infix)
	remainingWidth := termWidth - utf8.RuneCountInString(prefix) - padding
	if remainingWidth < 0 {
		remainingWidth = 0
	}
	widthAdjustedRemainder := ""
	r := []rune(remainder)
	if remainingWidth == 0 {
		widthAdjustedRemainder = ""
	} else if len(r) <= remainingWidth {
		widthAdjustedRemainder = remainder
	} else if remainingWidth <= infixLen {
		widthAdjustedRemainder = string(r[:remainingWidth])
	} else {
		avail := remainingWidth - infixLen
		startLen := avail / 2
		endLen := avail - startLen
		if endLen < 0 {
			endLen = 0
		}
		if startLen < 0 {
			startLen = 0
		}
		if startLen > len(r) {
			startLen = len(r)
		}
		if endLen > len(r)-startLen {
			endLen = len(r) - startLen
		}
		endStart := len(r) - endLen
		if endStart < 0 {
			endStart = 0
		}
		widthAdjustedRemainder = string(r[:startLen]) +
			infix +
			string(r[endStart:])
	}

	return prefix + widthAdjustedRemainder
}

func widthAppropriateChatSummary(toShorten, prefix string, padding int) (string, error) {
	toShorten = strings.ReplaceAll(toShorten, "\n", "\\n")
	toShorten = strings.ReplaceAll(toShorten, "\t", "\\t")
	termWidth, err := utils.TermWidth()
	if err != nil {
		return "", fmt.Errorf("failed to get termWidth: %w", err)
	}

	return fillRemainderOfTermWidth(prefix, toShorten, termWidth, padding), nil
}

func isInt(s string) bool {
	_, err := strconv.Atoi(s)
	return err == nil
}

func (cq *ChatHandler) printChatInfo(w io.Writer, chat pub_models.Chat) error {
	claiConfDir, err := utils.GetClaiConfigDir()
	if err != nil {
		return fmt.Errorf("failed to get clai config dir: %w", err)
	}
	filePath := path.Join(claiConfDir, "conversations", chat.ID)
	messgeTypeCounter := make(map[string]int)
	for _, m := range chat.Messages {
		messgeTypeCounter[m.Role] += 1
	}
	firstMessages := ""
	uMsg, uMsgErr := chat.FirstUserMessage()
	if uMsgErr == nil {
		firstMessages = uMsg.Content
	}
	summary, err := widthAppropriateChatSummary(firstMessages, "summary: \"", 10)
	if err != nil {
		return fmt.Errorf("failed to create widthAppropriateChatSummary: %w", err)
	}
	fmt.Fprintf(w, actOnChatFormat,
		filePath,
		chat.Created,
		messgeTypeCounter["user"],
		messgeTypeCounter["tools"],
		messgeTypeCounter["system"],
		messgeTypeCounter["assistant"],
		summary+"\"",
	)
	return nil
}

// escapeEditString by:
// 1. Unescaping toEdit
// 2. Writing the string to a temporary file
// 3. Opening the file with EDITOR
// 4. On close, reading the edited file
// 5. Re-escaping the edited string
// 6. Returning the newly re-escaped edited string
func escapeEditString(toEdit string) (string, error) {
	ret := strings.ReplaceAll(toEdit, "\\n", "\n")
	ret = strings.ReplaceAll(ret, "\\t", "\t")

	f, err := os.CreateTemp("", "clai-edit-*.txt")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(f.Name())

	_, err = f.WriteString(ret)
	if err != nil {
		f.Close()
		return "", fmt.Errorf("failed to write temp file: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("failed to close temp file: %w", err)
	}

	editor := os.Getenv("EDITOR")
	if editor == "" {
		return "", fmt.Errorf("environment variable EDITOR is not set")
	}

	cmd := exec.Command(editor, f.Name())
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to edit file %s: %v", f.Name(), err)
	}

	b, err := os.ReadFile(f.Name())
	if err != nil {
		return "", fmt.Errorf("failed to read edited file: %w", err)
	}
	edited := string(b)

	edited = strings.ReplaceAll(edited, "\r\n", "\n")
	edited = strings.ReplaceAll(edited, "\n", "\\n")
	edited = strings.ReplaceAll(edited, "\t", "\\t")

	return edited, nil
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

			withSummary, err := widthAppropriateChatSummary(
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

			withSummary, err := widthAppropriateChatSummary(t.Content, prefix, 25)
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
	editedString, err := escapeEditString(selectedMessage.Content)
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
