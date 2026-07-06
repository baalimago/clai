package chat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/baalimago/clai/internal/cost"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/clai/internal/vendors"
	"github.com/baalimago/clai/internal/vendors/anthropic"
	"github.com/baalimago/clai/internal/vendors/pi"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

const chatInfoPrintHeight = 16

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

type chatRowKind uint8

const (
	chatRowNative chatRowKind = iota
	chatRowForeign
	chatRowGroup
)

// errExitList is returned by actOnChat/actOnForeignChat to signal
// the listChats loop should exit cleanly (e.g., after [Enter] continue).
var errExitList = errors.New("exit list")

type chatListRow struct {
	Kind    chatRowKind
	Created time.Time

	// Native
	ChatID string

	// Foreign
	Source   string
	SourceID string

	// Display
	Profile          string
	Model            string
	MessageCount     int
	TotalTokens      int
	TotalCostUSD     float64
	FirstUserMessage string
	// GroupKey is set for all rows; group rows distinguish by Kind == chatRowGroup.
	GroupKey string
	// GroupMemberCount is populated only for group rows (Kind == chatRowGroup).
	GroupMemberCount int
}

func (r chatListRow) displaySource() string {
	if r.Kind == chatRowForeign {
		return r.Source
	}
	if r.Source != "" {
		return r.Source
	}
	return "clai"
}

var allSourceReaders = func() []vendors.SourceReader {
	return []vendors.SourceReader{
		anthropic.SourceReader{},
		pi.SourceReader{},
	}
}

func sourceReaderByName(readers []vendors.SourceReader) (map[string]vendors.SourceReader, error) {
	m := map[string]vendors.SourceReader{}
	for _, r := range readers {
		name := r.Source()
		if name == "" {
			return nil, fmt.Errorf("source reader returned empty Source()")
		}
		if _, exists := m[name]; exists {
			return nil, fmt.Errorf("duplicate source reader name: %q", name)
		}
		m[name] = r
	}
	return m, nil
}

func sourceDedupKey(source, sourceID string) string {
	return source + "\x00" + sourceID
}

func (cq *ChatHandler) foreignChatRows(ctx context.Context, readers []vendors.SourceReader, existing map[string]struct{}) ([]chatListRow, error) {
	rows := []chatListRow{}
	for _, r := range readers {
		found, err := r.Discover(ctx)
		if err != nil {
			if misc.Truthy(os.Getenv("DEBUG")) {
				ancli.Noticef("skipping source %s: %v\n", r.Source(), err)
			}
			continue
		}
		for _, fr := range found {
			if fr.SourceID == "" {
				continue
			}
			k := sourceDedupKey(fr.Source, fr.SourceID)
			if _, ok := existing[k]; ok {
				continue
			}
			rows = append(rows, chatListRow{
				Kind:             chatRowForeign,
				Created:          fr.Created,
				Source:           fr.Source,
				SourceID:         fr.SourceID,
				MessageCount:     fr.MessageCount,
				FirstUserMessage: fr.FirstUserMessage,
				Model:            fr.Model,
				GroupKey:         ComputeGroupKeyFromText(fr.FullFirstUserMessage),
			})
		}
	}
	return rows, nil
}

func (cq *ChatHandler) buildChatListRows(ctx context.Context, paginator *ChatIndexPaginator) ([]chatListRow, map[string]vendors.SourceReader, error) {
	readers := allSourceReaders()
	byName, err := sourceReaderByName(readers)
	if err != nil {
		return nil, nil, err
	}

	existing := map[string]struct{}{}
	native := make([]chatListRow, 0, len(paginator.rows))
	for _, r := range paginator.rows {
		if r.Source != "" && r.SourceID != "" {
			existing[sourceDedupKey(r.Source, r.SourceID)] = struct{}{}
		}
		native = append(native, chatListRow{
			Kind:             chatRowNative,
			Created:          r.Created,
			ChatID:           r.ID,
			Source:           r.Source,
			SourceID:         r.SourceID,
			Profile:          r.Profile,
			Model:            r.Model,
			MessageCount:     r.MessageCount,
			TotalTokens:      r.TotalTokens,
			TotalCostUSD:     r.TotalCostUSD,
			FirstUserMessage: r.FirstUserMessage,
			GroupKey:         r.GroupKey,
		})
	}
	foreign, err := cq.foreignChatRows(ctx, readers, existing)
	if err != nil {
		return nil, nil, err
	}
	rows := append(native, foreign...)
	slices.SortFunc(rows, func(a, b chatListRow) int {
		if cmp := b.Created.Compare(a.Created); cmp != 0 {
			return cmp
		}
		// Tiebreaker: GroupKey lexicographic (empty sorts before non-empty)
		if a.GroupKey < b.GroupKey {
			return -1
		}
		if a.GroupKey > b.GroupKey {
			return 1
		}
		return 0
	})
	return rows, byName, nil
}

func (cq *ChatHandler) actOnChat(ctx context.Context, chat pub_models.Chat, groupKey string) error {
	if err := cq.printChatInfo(cq.out, chat, groupKey); err != nil {
		return fmt.Errorf("failed to printChatInfo: %w", err)
	}
	choice, err := utils.ReadUserInput()
	if err != nil {
		return fmt.Errorf("failed to read user input: %w", err)
	}
	switch choice {
	case "E", "e":
		return cq.handleEditMessages(ctx, chat, groupKey)
	case "D", "d":
		return cq.handleDeleteMessages(ctx, chat, groupKey)
	case "B", "b":
		clearErr := utils.ClearTermTo(cq.out, chatInfoPrintHeight)
		if clearErr != nil {
			return fmt.Errorf("failed to clear term: %w", clearErr)
		}
		return nil
	case "P", "p":
		return SaveAsPreviousQuery(cq.confDir, chat)
	case "Q", "q":
		return utils.ErrUserInitiatedExit
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
		return errExitList
	default:
		return fmt.Errorf("unknown choice: %q", choice)
	}
}

func (cq *ChatHandler) handleEditMessages(ctx context.Context, chat pub_models.Chat, groupKey string) error {
	clearErr := utils.ClearTermTo(cq.out, chatInfoPrintHeight)
	if clearErr != nil {
		return fmt.Errorf("failed to clear term: %w", clearErr)
	}
	if err := cq.editMessageInChat(chat); err != nil {
		return fmt.Errorf("failed to editChat: %w", err)
	}
	return cq.actOnChat(ctx, chat, groupKey)
}

func (cq *ChatHandler) handleDeleteMessages(ctx context.Context, chat pub_models.Chat, groupKey string) error {
	clearErr := utils.ClearTermTo(cq.out, chatInfoPrintHeight)
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
	return cq.actOnChat(ctx, updatedChat, groupKey)
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
		p := filepath.Join(cq.convDir, dirEntry.Name())
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
	return cq.listChats(ctx, paginator, "")
}

// collapseGroupRows collapses rows with the same non-empty GroupKey and N≥2
// into a single group row. Ungrouped rows pass through unchanged.
func collapseGroupRows(rows []chatListRow) []chatListRow {
	// Collect members per group key
	groups := map[string][]int{} // groupKey → indices into rows
	for i, r := range rows {
		if r.GroupKey == "" {
			continue
		}
		// The globalScope mirror shares the newest conversation's GroupKey;
		// grouping it would double-count that conversation's aggregates.
		if r.ChatID == globalScopeChatID {
			continue
		}
		groups[r.GroupKey] = append(groups[r.GroupKey], i)
	}

	// Determine which indices are collapsed (first member of each multi-member group)
	collapsedAt := map[int]chatListRow{} // index → group row
	skipIndices := map[int]bool{}        // indices to exclude
	for gk, members := range groups {
		if len(members) < 2 {
			continue
		}
		// Build a group row from the first (most recent) member
		gr := buildGroupRow(rows, members, gk)
		collapsedAt[members[0]] = gr
		for _, idx := range members[1:] {
			skipIndices[idx] = true
		}
	}

	// Rebuild the list: keep all rows except skipped ones, replacing group leaders
	out := make([]chatListRow, 0, len(rows))
	for i, r := range rows {
		if skipIndices[i] {
			continue
		}
		if gr, ok := collapsedAt[i]; ok {
			out = append(out, gr)
		} else {
			out = append(out, r)
		}
	}
	return out
}

// buildGroupRow creates a group row from the given member indices.
func buildGroupRow(rows []chatListRow, members []int, groupKey string) chatListRow {
	// Find most recent member (members are in order, but verify)
	newest := rows[members[0]]
	for _, idx := range members[1:] {
		if rows[idx].Created.After(newest.Created) {
			newest = rows[idx]
		}
	}

	// Aggregate totals (native members only; foreign contribute zero)
	var totalTokens int
	var totalCost float64
	var totalMessages int
	for _, idx := range members {
		r := rows[idx]
		totalMessages += r.MessageCount
		if r.Kind == chatRowNative {
			totalTokens += r.TotalTokens
			totalCost += r.TotalCostUSD
		}
	}

	// Model/profile: prefer most recent native member; fall back to most recent
	model := newest.Model
	profile := newest.Profile
	if newest.Kind == chatRowForeign {
		// members indices are in Created-desc order (inherited from the sorted rows
		// slice passed to collapseGroupRows), so the first native encountered is
		// the most recent native member.
		for _, idx := range members {
			if rows[idx].Kind == chatRowNative {
				model = rows[idx].Model
				profile = rows[idx].Profile
				break
			}
		}
		if profile == "" {
			profile = "N/A"
		}
	}

	return chatListRow{
		Kind:             chatRowGroup,
		Created:          newest.Created,
		Source:           newest.Source,
		Profile:          profile,
		Model:            model,
		MessageCount:     totalMessages,
		TotalTokens:      totalTokens,
		TotalCostUSD:     totalCost,
		FirstUserMessage: newest.FirstUserMessage,
		GroupKey:         groupKey,
		GroupMemberCount: len(members),
	}
}

// filterRowsByGroupKey returns only rows matching the given groupKey.
func filterRowsByGroupKey(rows []chatListRow, groupKey string) []chatListRow {
	out := make([]chatListRow, 0, len(rows))
	for _, r := range rows {
		if r.GroupKey == groupKey && r.Kind != chatRowGroup && r.ChatID != globalScopeChatID {
			out = append(out, r)
		}
	}
	return out
}

// dirFilterAction returns the toggleable [d]ir filter button, present only when
// the current directory has recorded conversation history. The predicate keeps
// rows whose chat_id is bound to the directory (head + history).
func (cq *ChatHandler) dirFilterAction() (utils.TableAction, bool) {
	wd, err := currentWorkingDirectory()
	if err != nil {
		return utils.TableAction{}, false
	}
	ids := DirHistoryChatIDs(cq.confDir, wd)
	return utils.TableAction{
		Format:       "[d]irscoped convs",
		Short:        "d",
		Long:         "dir",
		EmptyMessage: fmt.Sprintf("no dirscoped conversations in %s", wd),
		Filter: func(row any) bool {
			r, ok := row.(chatListRow)
			if !ok {
				return false
			}
			if r.Kind == chatRowForeign || r.Kind == chatRowGroup {
				return true
			}
			_, in := ids[r.ChatID]
			return in
		},
	}, true
}

// preparedRows bundles the rows ready for display and the source-reader
// lookup map needed for dispatching foreign-row selections.
type preparedRows struct {
	rows   []chatListRow
	byName map[string]vendors.SourceReader
}

func (cq *ChatHandler) prepareListRows(ctx context.Context, paginator *ChatIndexPaginator, groupKey string) (preparedRows, error) {
	allRows, byName, err := cq.buildChatListRows(ctx, paginator)
	if err != nil {
		return preparedRows{}, err
	}
	if groupKey != "" {
		return preparedRows{rows: filterRowsByGroupKey(allRows, groupKey), byName: byName}, nil
	}
	return preparedRows{rows: collapseGroupRows(allRows), byName: byName}, nil
}

func (cq *ChatHandler) listChats(ctx context.Context, paginator *ChatIndexPaginator, groupKey string) error {
	for {
		pr, err := cq.prepareListRows(ctx, paginator, groupKey)
		if err != nil {
			return fmt.Errorf("failed to prepare list rows: %w", err)
		}

		tableActions := []utils.TableAction{}
		if action, ok := cq.dirFilterAction(); ok {
			tableActions = append(tableActions, action)
		}

		// Compute the widest index string across all visible rows.
		maxIdxLen := 5 // "Index"
		for i, r := range pr.rows {
			s := fmt.Sprintf("%v", i)
			if r.Kind == chatRowGroup {
				s = fmt.Sprintf("%v [group:%v]", i, r.GroupMemberCount)
			}
			if len(s) > maxIdxLen {
				maxIdxLen = len(s)
			}
		}

		tblFmt := fmt.Sprintf("%%-%ds| %%-15s | %%-20s| %%-18s | %%-8s | %%v", maxIdxLen)
		headArgs := []any{"Index", "Source", "Created", "Model", "Cost", "Prompt"}
		isWide := false
		if tw, err := utils.TermWidth(); err == nil && tw > 120 {
			isWide = true
			tblFmt = fmt.Sprintf("%%-%ds| %%-15s | %%-20s| %%-8v | %%-15s | %%-18s | %%-8s | %%-6s | %%v", maxIdxLen)
			headArgs = []any{"Index", "Source", "Created", "Messages", "Profile", "Model", "Cost", "Tokens", "Prompt"}
		}

		choicesFormat := selectChatTblChoicesFormat
		backLabel := ""
		if groupKey != "" {
			displayKey := groupKey
			if len(groupKey) >= 8 {
				displayKey = groupKey[:8] + "..."
			}
			if len(pr.rows) > 0 {
				choicesFormat = fmt.Sprintf("group: %q", pr.rows[0].FirstUserMessage)
			} else {
				choicesFormat = fmt.Sprintf("group: %s", displayKey)
			}
			backLabel = "[b]ack to list"
		}

		selectedNumbers, err := utils.SelectFromTable(
			fmt.Sprintf(tblFmt, headArgs...),
			utils.SlicePaginator(pr.rows),
			choicesFormat,
			func(i int, item chatListRow) (string, error) {
				tokenStr := "N/A"
				costStr := "N/A"
				model := item.Model
				profile := item.Profile
				msgs := item.MessageCount
				idxStr := fmt.Sprintf("%v", i)
				if item.Kind == chatRowGroup {
					idxStr = fmt.Sprintf("%v [group:%v]", i, item.GroupMemberCount)
				}
				if item.Kind == chatRowNative || item.Kind == chatRowGroup {
					tokenStr = chatIndexTokenStr(chatIndexRow{TotalTokens: item.TotalTokens})
					costStr = chatIndexCostStr(chatIndexRow{TotalCostUSD: item.TotalCostUSD})
				} else {
					profile = "N/A"
				}
				if isWide {
					prefix := fmt.Sprintf(
						tblFmt,
						idxStr,
						item.displaySource(),
						item.Created.Format("2006-01-02 15:04:05"),
						msgs,
						profile,
						model,
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
					idxStr,
					item.displaySource(),
					item.Created.Format("2006-01-02 15:04:05"),
					model,
					costStr,
					"",
				)
				withSummary, err := utils.WidthAppropriateStringTrunc(item.FirstUserMessage, prefix, 15)
				if err != nil {
					return "", fmt.Errorf("failed to get widthAppropriateChatSummary: %w", err)
				}
				return withSummary, nil
			},
			utils.ThemeTableItems(),
			true,
			tableActions,
			cq.out,
			backLabel,
		)
		if err != nil {
			if errors.Is(err, utils.ErrBack) {
				if groupKey != "" {
					groupKey = ""
					continue
				}
				return nil
			}
			return fmt.Errorf("failed to select chat: %w", err)
		}
		if len(selectedNumbers) == 0 || selectedNumbers[0] < 0 || selectedNumbers[0] >= len(pr.rows) {
			fmt.Fprintf(cq.out, "selection out of range, please pick one of the listed indices\n")
			continue
		}
		sel := pr.rows[selectedNumbers[0]]
		if sel.Kind == chatRowGroup {
			groupKey = sel.GroupKey
			continue
		}
		if sel.Kind == chatRowNative {
			selectedChat, err := cq.getByID(sel.ChatID)
			if err != nil {
				return fmt.Errorf("failed to load selected chat %q: %w", sel.ChatID, err)
			}
			if err := cq.actOnChat(ctx, selectedChat, groupKey); err != nil {
				if errors.Is(err, errExitList) {
					return nil
				}
				return err
			}
			continue
		}

		reader, ok := pr.byName[sel.Source]
		if !ok {
			return fmt.Errorf("unknown source reader %q", sel.Source)
		}
		foreign, err := reader.Read(ctx, sel.SourceID)
		if err != nil {
			return fmt.Errorf("failed to read %s session %q: %w", sel.Source, sel.SourceID, err)
		}
		if err := cq.actOnForeignChat(ctx, foreign, reader, groupKey); err != nil {
			if errors.Is(err, errExitList) {
				return nil
			}
			return err
		}
		continue
	}
}

func (cq *ChatHandler) actOnForeignChat(ctx context.Context, chat pub_models.Chat, reader vendors.SourceReader, groupKey string) error {
	if err := cq.printChatInfoForeign(cq.out, chat, groupKey); err != nil {
		return fmt.Errorf("failed to printChatInfoForeign: %w", err)
	}
	choice, err := utils.ReadUserInput()
	if err != nil {
		return fmt.Errorf("failed to read user input: %w", err)
	}
	switch choice {
	case "C", "c", "":
		cloned, err := cq.cloneForeignChat(ctx, chat)
		if err != nil {
			return err
		}
		ancli.Noticef("cloned %s session %s → chat %s\n", reader.Source(), chat.SourceID, cloned.ID)
		ancli.Noticef("chat %s is now replyable with flag \"clai -dre query <prompt>\"\n", cloned.ID)
		return errExitList
	case "B", "b":
		clearErr := utils.ClearTermTo(cq.out, chatInfoPrintHeight)
		if clearErr != nil {
			return fmt.Errorf("failed to clear term: %w", clearErr)
		}
		return nil
	case "Q", "q":
		return utils.ErrUserInitiatedExit
	default:
		return fmt.Errorf("unknown choice: %q", choice)
	}
}

// chatInfoOpts controls differences between native and foreign chat info display.
type chatInfoOpts struct {
	foreign bool
}

func (cq *ChatHandler) printChatInfoCommon(w io.Writer, chat pub_models.Chat, groupKey string, opts chatInfoOpts) error {
	claiConfDir, err := utils.GetClaiConfigDir()
	if err != nil {
		return fmt.Errorf("failed to get clai config dir: %w", err)
	}
	filePath := conversationPath(claiConfDir, chat.ID)
	if opts.foreign && chat.ID == "" {
		filePath = "(not cloned)"
	}
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
	sourceKey := utils.Colorize(utils.ThemePrimaryColor(), "source:")
	userRole := utils.Colorize(utils.RoleColor("user"), "user:")
	toolRole := utils.Colorize(utils.RoleColor("tool"), "tool:")
	systemRole := utils.Colorize(utils.RoleColor("system"), "system:")
	assistantRole := utils.Colorize(utils.RoleColor("assistant"), "assistant:")
	bread := utils.ThemeBreadtextColor()

	if _, err := fmt.Fprintf(w, "%s\n\n", header); err != nil {
		return fmt.Errorf("write chat header: %w", err)
	}
	// GroupKey display (when non-empty)
	if chat.GroupKey != "" {
		if _, err := fmt.Fprintf(w, "%s %s\n", utils.Colorize(utils.ThemePrimaryColor(), "group key:"), utils.Colorize(bread, chat.GroupKey)); err != nil {
			return fmt.Errorf("write group key: %w", err)
		}
	}
	if _, err := fmt.Fprintf(w, "%s %s\n", fileKey, utils.Colorize(bread, filePath)); err != nil {
		return fmt.Errorf("write file path: %w", err)
	}
	if opts.foreign {
		if _, err := fmt.Fprintf(w, "%s %s\n", sourceKey, utils.Colorize(bread, fmt.Sprintf("%s (session: %s)", chat.Source, chat.SourceID))); err != nil {
			return fmt.Errorf("write source: %w", err)
		}
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
	backLabel := "go [b]ack to list"
	if groupKey != "" {
		backLabel = "[b]ack to group"
	}
	var choices string
	if opts.foreign {
		choices = utils.Colorize(utils.ThemePrimaryColor(), fmt.Sprintf("(press [c]ontinue (clone to clai), %s, [q]uit): ", backLabel))
	} else {
		choices = utils.Colorize(utils.ThemePrimaryColor(), fmt.Sprintf("(make [p]revQuery (-re/-reply flag), %s, [e]dit messages, [d]elete messages, [q]uit, [<enter>] to continue): ", backLabel))
	}
	if _, err := fmt.Fprint(w, choices); err != nil {
		return fmt.Errorf("write choices: %w", err)
	}
	return nil
}

func (cq *ChatHandler) printChatInfo(w io.Writer, chat pub_models.Chat, groupKey string) error {
	return cq.printChatInfoCommon(w, chat, groupKey, chatInfoOpts{})
}

func (cq *ChatHandler) printChatInfoForeign(w io.Writer, chat pub_models.Chat, groupKey string) error {
	return cq.printChatInfoCommon(w, chat, groupKey, chatInfoOpts{foreign: true})
}

func (cq *ChatHandler) cloneForeignChat(ctx context.Context, foreign pub_models.Chat) (pub_models.Chat, error) {
	_ = ctx
	if foreign.Source == "" || foreign.SourceID == "" {
		return pub_models.Chat{}, fmt.Errorf("invalid foreign chat: missing source or source_id")
	}
	cloned := foreign
	cloned.ID = NewChatID()
	// Preserve Created as discovered/read. Do not stamp wall clock here.
	// Save stamps GroupKey and upserts the index itself; a second upsert with
	// the pre-stamp copy would wipe the group_key from the index row.
	if err := Save(cq.convDir, cloned); err != nil {
		return pub_models.Chat{}, fmt.Errorf("failed to save cloned chat: %w", err)
	}
	if err := cq.UpdateDirScopeFromCWD(cloned.ID); err != nil {
		return pub_models.Chat{}, fmt.Errorf("failed to update directory-scoped binding: %w", err)
	}
	return cloned, nil
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
		utils.ThemeTableItems(),
		false,
		[]utils.TableAction{},
		cq.out,
		"",
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
		utils.ThemeTableItems(),
		true,
		[]utils.TableAction{},
		cq.out,
		"",
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
