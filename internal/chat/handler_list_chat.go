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

	"github.com/baalimago/clai/internal/cost"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

const chatInfoPrintHeight = 13

func chatListTokenStr(item pub_models.Chat) string {
	if item.TokenUsage == nil {
		return "N/A"
	}
	v := item.TokenUsage.TotalTokens
	if v >= 1000 {
		return fmt.Sprintf("%dK", v/1000)
	}
	return fmt.Sprintf("%.3fK", float64(v)/1000.0)
}

func chatListCostStr(item pub_models.Chat) string {
	if !item.HasCostEstimates() {
		return "N/A"
	}
	return cost.FormatUSD(item.TotalCostUSD())
}

func chatIndexTokenStr(item chatIndexRow) string {
	if item.TotalTokens <= 0 {
		return "N/A"
	}
	if item.TotalTokens >= 1000 {
		return fmt.Sprintf("%dK", item.TotalTokens/1000)
	}
	return fmt.Sprintf("%.3fK", float64(item.TotalTokens)/1000.0)
}

func chatIndexCostStr(item chatIndexRow) string {
	if item.TotalCostUSD <= 0 {
		return "N/A"
	}
	return cost.FormatUSD(item.TotalCostUSD)
}

func (cq *ChatHandler) actOnChat(ctx context.Context, chat pub_models.Chat) error {
	if err := cq.printChatInfo(cq.out, chat); err != nil {
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
	if err := cq.editMessageInChat(chat); err != nil {
		return fmt.Errorf("failed to editChat: %w", err)
	}
	return cq.actOnChat(ctx, chat)
}

func (cq *ChatHandler) handleDeleteMessages(ctx context.Context, chat pub_models.Chat) error {
	clearErr := utils.ClearTermTo(cq.out, -1, chatInfoPrintHeight)
	if clearErr != nil {
		return fmt.Errorf("failed to clear term: %w", clearErr)
	}
	if err := cq.deleteMessageInChat(chat); err != nil {
		return fmt.Errorf("failed to editChat: %w", err)
	}
	updatedChat, err := cq.getByID(chat.ID)
	if err != nil {
		return fmt.Errorf("failed to re-fetch chat: %w", err)
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
		if dirEntry.IsDir() || dirEntry.Name() == chatIndexFileName {
			continue
		}
		p := path.Join(cq.convDir, dirEntry.Name())
		chat, err := FromPath(p)
		if err != nil {
			return nil, fmt.Errorf("failed to get chat %q: %w", p, err)
		}
		chats = append(chats, chat)
	}
	slices.SortFunc(chats, func(a, b pub_models.Chat) int {
		return b.Created.Compare(a.Created)
	})
	return chats, nil
}

func (cq *ChatHandler) handleListCmd(ctx context.Context) error {
	paginator, err := NewChatIndexPaginator(cq.convDir)
	if err != nil {
		return fmt.Errorf("failed to create chat index paginator: %w", err)
	}
	return cq.listChats(ctx, paginator)
}

func (cq *ChatHandler) listChats(ctx context.Context, paginator *ChatIndexPaginator) error {
	ancli.PrintOK(fmt.Sprintf("found '%v' conversations:\n", paginator.Len()))

	tblFmt := selectChatTblFormat
	headArgs := []any{"Index", "Created", "Messages", "Model", "Cost", "Prompt"}
	includeProfile := false
	if tw, err := utils.TermWidth(); err == nil && tw > 120 {
		includeProfile = true
		tblFmt = "%-6s| %-20s| %-8v | %-10s | %-18s | %-8s | %-6s | %v"
		headArgs = []any{"Index", "Created", "Messages", "Profile", "Model", "Cost", "Tokens", "Prompt"}
	}

	selectedNumbers, err := utils.SelectFromTable(
		fmt.Sprintf(tblFmt, headArgs...),
		utils.SlicePaginator(paginator.rows),
		selectChatTblChoicesFormat,
		func(i int, item chatIndexRow) (string, error) {
			tokenStr := chatIndexTokenStr(item)
			costStr := chatIndexCostStr(item)
			if includeProfile {
				prefix := fmt.Sprintf(
					tblFmt,
					fmt.Sprintf("%v", i),
					item.Created.Format("2006-01-02 15:04:05"),
					item.MessageCount,
					item.Profile,
					item.Model,
					costStr,
					tokenStr,
					"",
				)
				withSummary, err := utils.WidthAppropriateStringTrunc(item.FirstUserMessage, prefix, 15)
				if err != nil {
					return "", fmt.Errorf("failed to get widthAppropriateChatSummary: %w", err)
				}
				return withSummary, nil
			}

			prefix := fmt.Sprintf(
				tblFmt,
				fmt.Sprintf("%v", i),
				item.Created.Format("2006-01-02 15:04:05"),
				item.MessageCount,
				item.Model,
				costStr,
				"",
			)
			withSummary, err := utils.WidthAppropriateStringTrunc(item.FirstUserMessage, prefix, 15)
			if err != nil {
				return "", fmt.Errorf("failed to get widthAppropriateChatSummary: %w", err)
			}
			return withSummary, nil
		},
		10,
		true,
		[]utils.TableAction{},
		cq.out,
	)
	if err != nil {
		return fmt.Errorf("failed to select chat: %w", err)
	}
	rows, err := paginator.Page(selectedNumbers[0], 1)
	if err != nil {
		return fmt.Errorf("failed to fetch selected chat metadata row: %w", err)
	}
	if len(rows) != 1 {
		return fmt.Errorf("failed to find selected chat metadata row at index %d", selectedNumbers[0])
	}
	selectedChat, err := cq.getByID(rows[0].ID)
	if err != nil {
		return fmt.Errorf("failed to load selected chat %q: %w", rows[0].ID, err)
	}
	return cq.actOnChat(ctx, selectedChat)
}

func (cq *ChatHandler) printChatInfo(w io.Writer, chat pub_models.Chat) error {
	claiConfDir, err := utils.GetClaiConfigDir()
	if err != nil {
		return fmt.Errorf("failed to get clai config dir: %w", err)
	}
	filePath := path.Join(claiConfDir, "conversations", chat.ID)
	messageTypeCounter := make(map[string]int)
	for _, m := range chat.Messages {
		messageTypeCounter[m.Role]++
	}
	firstMessages := ""
	if uMsg, err := chat.FirstUserMessage(); err == nil {
		firstMessages = uMsg.Content
	}
	summary, err := utils.WidthAppropriateStringTrunc(firstMessages, "summary: \"", 10)
	if err != nil {
		return fmt.Errorf("failed to create widthAppropriateChatSummary: %w", err)
	}

	header := utils.Colorize(utils.ThemePrimaryColor(), "=== Chat info ===")
	fileKey := utils.Colorize(utils.ThemePrimaryColor(), "file path:")
	createdKey := utils.Colorize(utils.ThemePrimaryColor(), "created_at:")
	costKey := utils.Colorize(utils.ThemePrimaryColor(), "total cost:")
	amRepliesKey := utils.Colorize(utils.ThemePrimaryColor(), "am replies:")
	userRole := utils.Colorize(utils.RoleColor("user"), "user:")
	toolRole := utils.Colorize(utils.RoleColor("tool"), "tool:")
	systemRole := utils.Colorize(utils.RoleColor("system"), "system:")
	assistantRole := utils.Colorize(utils.RoleColor("assistant"), "assistant:")
	bread := utils.ThemeBreadtextColor()

	if _, err := fmt.Fprintf(w, "%s\n\n", header); err != nil {
		return fmt.Errorf("write chat header: %w", err)
	}
	if _, err := fmt.Fprintf(w, "%s %s\n", fileKey, utils.Colorize(bread, filePath)); err != nil {
		return fmt.Errorf("write file path: %w", err)
	}
	if _, err := fmt.Fprintf(w, "%s %s\n", createdKey, utils.Colorize(bread, fmt.Sprintf("%v", chat.Created))); err != nil {
		return fmt.Errorf("write created at: %w", err)
	}
	if _, err := fmt.Fprintf(w, "%s %s\n", costKey, utils.Colorize(bread, chatListCostStr(chat))); err != nil {
		return fmt.Errorf("write total cost: %w", err)
	}
	if _, err := fmt.Fprintf(w, "%s\n", amRepliesKey); err != nil {
		return fmt.Errorf("write am replies key: %w", err)
	}
	if _, err := fmt.Fprintf(w, "\t%s %s'%v'\n", userRole, utils.Colorize(bread, "   "), messageTypeCounter["user"]); err != nil {
		return fmt.Errorf("write user replies: %w", err)
	}
	if _, err := fmt.Fprintf(w, "\t%s %s'%v'\n", toolRole, utils.Colorize(bread, "   "), messageTypeCounter["tool"]); err != nil {
		return fmt.Errorf("write tool replies: %w", err)
	}
	if _, err := fmt.Fprintf(w, "\t%s %s'%v'\n", systemRole, utils.Colorize(bread, "  "), messageTypeCounter["system"]); err != nil {
		return fmt.Errorf("write system replies: %w", err)
	}
	if _, err := fmt.Fprintf(w, "\t%s %s'%v'\n\n", assistantRole, utils.Colorize(bread, ""), messageTypeCounter["assistant"]); err != nil {
		return fmt.Errorf("write assistant replies: %w", err)
	}
	if _, err := fmt.Fprintf(w, "%s\n\n", utils.Colorize(bread, summary+"\"")); err != nil {
		return fmt.Errorf("write summary: %w", err)
	}
	choices := utils.Colorize(utils.ThemePrimaryColor(), "(make [p]revQuery (-re/-reply flag), go [b]ack to list, [e]dit messages, [d]elete messages, [q]uit, [<enter>] to continue): ")
	if _, err := fmt.Fprint(w, choices); err != nil {
		return fmt.Errorf("write choices: %w", err)
	}
	return nil
}

func editorEditString(toEdit string) (string, error) {
	f, err := os.CreateTemp("", "clai-edit-*.txt")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(f.Name())

	if _, err := f.WriteString(toEdit); err != nil {
		_ = f.Close()
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
		return "", fmt.Errorf("failed to edit file %s: %w", f.Name(), err)
	}

	b, err := os.ReadFile(f.Name())
	if err != nil {
		return "", fmt.Errorf("failed to read edited file: %w", err)
	}
	return string(b), nil
}

func (cq *ChatHandler) deleteMessageInChat(chat pub_models.Chat) error {
	head := fmt.Sprintf(editMessageTblFormat, "Index", "Role", "Length", "Summary")
	selectedIndices, err := utils.SelectFromTable(
		head,
		utils.SlicePaginator(chat.Messages),
		deleteMessagesChoicesFormat,
		func(i int, t pub_models.Message) (string, error) {
			prefix := fmt.Sprintf(editMessageTblFormat, i, t.Role, utf8.RuneCount([]byte(t.Content)), "")
			withSummary, err := utils.WidthAppropriateStringTrunc(t.Content, prefix, 25)
			if err != nil {
				return "", fmt.Errorf("failed to get widthAppropriateChatSummary: %w", err)
			}
			return withSummary, nil
		},
		10,
		false,
		[]utils.TableAction{},
		cq.out,
	)
	if err != nil {
		return fmt.Errorf("failed to select from table: %w", err)
	}

	idxSet := make(map[int]struct{}, len(selectedIndices))
	for _, idx := range selectedIndices {
		idxSet[idx] = struct{}{}
	}
	filtered := make([]pub_models.Message, 0, len(chat.Messages))
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
	ancli.Okf("modified chat: '%v', deleted messages: '%v'", chat.ID, selectedIndices)
	return nil
}

func (cq *ChatHandler) editMessageInChat(chat pub_models.Chat) error {
	head := fmt.Sprintf(editMessageTblFormat, "Index", "Role", "Length", "Summary")
	selectedNumbers, err := utils.SelectFromTable(
		head,
		utils.SlicePaginator(chat.Messages),
		editMessageChoicesFormat,
		func(i int, t pub_models.Message) (string, error) {
			prefix := fmt.Sprintf(editMessageTblFormat, i, t.Role, utf8.RuneCount([]byte(t.Content)), "")
			withSummary, err := utils.WidthAppropriateStringTrunc(t.Content, prefix, 25)
			if err != nil {
				return "", fmt.Errorf("failed to get widthAppropriateChatSummary: %w", err)
			}
			return withSummary, nil
		},
		10,
		true,
		[]utils.TableAction{},
		cq.out,
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
	if err := Save(cq.convDir, chat); err != nil {
		return fmt.Errorf("failed to save chat: %w", err)
	}
	ancli.Okf("modified chat: '%v', message with index: '%v'", chat.ID, selectedNumbers)
	return nil
}
