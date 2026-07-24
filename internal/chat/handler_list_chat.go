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
	"github.com/baalimago/go_away_boilerplate/pkg/table"
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

// errToggleDirFilter is returned by the [d]ir table action to signal listChats
// to flip the dir-scoped view and re-derive the rows. The filtering happens
// before group collapsing (see prepareListRows), so it cannot be expressed as
// an in-table predicate over the already-collapsed rows.
var errToggleDirFilter = errors.New("toggle dir filter")

type chatListRow struct {
	Kind    chatRowKind
	Created time.Time

	// Native
	ChatID string

	// Foreign
	Source   string
	SourceID string
	// OriginDir is the directory the conversation was started from,
	// best-effort (empty when neither clai nor the source recorded one).
	OriginDir string

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
				OriginDir:        fr.Cwd,
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
			OriginDir:        r.OriginDir,
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

func (cq *ChatHandler) actOnChat(chat pub_models.Chat, groupKey string) error {
	if err := cq.printChatInfo(cq.out, chat, groupKey); err != nil {
		return fmt.Errorf("failed to printChatInfo: %w", err)
	}
	choice, err := table.ReadUserInputFrom(cq.input)
	if err != nil {
		return fmt.Errorf("failed to read user input: %w", err)
	}
	switch choice {
	case "E", "e":
		return cq.handleEditMessages(chat)
	case "D", "d":
		return cq.handleDeleteMessages(chat)
	case "B", "b":
		clearErr := table.ClearTermTo(cq.out, chatInfoPrintHeight)
		if clearErr != nil {
			return fmt.Errorf("failed to clear term: %w", clearErr)
		}
		return nil
	case "P", "p":
		return SaveAsPreviousQuery(cq.confDir, chat)
	case "Q", "q":
		return table.ErrUserInitiatedExit
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

// handleEditMessages is a peek + edit: the message picker stays open across
// edits, reopening on the page each selection was made from, so a conversation
// can be studied and reworked in one sitting. Backing out returns to the chat
// list, which also keeps its page.
func (cq *ChatHandler) handleEditMessages(chat pub_models.Chat) error {
	clearErr := table.ClearTermTo(cq.out, chatInfoPrintHeight)
	if clearErr != nil {
		return fmt.Errorf("failed to clear term: %w", clearErr)
	}
	if err := cq.editMessageInChat(chat); err != nil {
		return fmt.Errorf("failed to editChat: %w", err)
	}
	return nil
}

// handleDeleteMessages is a peek + delete, symmetric with handleEditMessages:
// the message picker stays open across deletions, reopening on the same page.
func (cq *ChatHandler) handleDeleteMessages(chat pub_models.Chat) error {
	clearErr := table.ClearTermTo(cq.out, chatInfoPrintHeight)
	if clearErr != nil {
		return fmt.Errorf("failed to clear term: %w", clearErr)
	}
	if err := cq.deleteMessageInChat(chat); err != nil {
		return fmt.Errorf("failed to deleteMessageInChat: %w", err)
	}
	return nil
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

// dirScopeRowPredicate returns a predicate reporting whether a row belongs to
// the current working directory, plus the wd it was anchored to. Native rows
// belong when their chat_id is bound to the directory (head + history) or
// their conversation originated in it; foreign rows when their recorded origin
// directory is the current directory. The globalScope mirror never belongs: it
// duplicates another conversation and carries no directory history of its own.
//
// The binding snapshot is taken once at construction. That is safe for a list
// session: every path that rebinds a chat (Enter-continue, foreign clone)
// exits the list via errExitList.
func (cq *ChatHandler) dirScopeRowPredicate() (func(chatListRow) bool, string, bool) {
	wd, err := currentWorkingDirectory()
	if err != nil {
		return nil, "", false
	}
	ids := DirHistoryChatIDs(cq.confDir, wd)
	canonicalWd, err := canonicalDir(wd)
	if err != nil {
		canonicalWd = wd
	}
	// Canonicalization hits the filesystem (EvalSymlinks); memoize per origin
	// since the predicate runs over the full row set on every [d] toggle.
	originCache := map[string]bool{}
	originInWd := func(origin string) bool {
		if origin == "" {
			return false
		}
		if match, ok := originCache[origin]; ok {
			return match
		}
		canonical, err := canonicalDir(origin)
		if err != nil {
			canonical = origin
		}
		match := originMatches(canonical, canonicalWd, false)
		originCache[origin] = match
		return match
	}
	return func(r chatListRow) bool {
		switch r.Kind {
		case chatRowNative:
			if r.ChatID == globalScopeChatID {
				return false
			}
			if _, in := ids[r.ChatID]; in {
				return true
			}
			return originInWd(r.OriginDir)
		case chatRowForeign:
			return originInWd(r.OriginDir)
		}
		return false
	}, wd, true
}

// preparedRows bundles the rows ready for display and the source-reader
// lookup map needed for dispatching foreign-row selections.
type preparedRows struct {
	rows   []chatListRow
	byName map[string]vendors.SourceReader
}

// prepareListRows derives the display view for the given group context from
// an already-built row set — pure in-memory work. A non-nil inDir predicate
// dir-scopes the view; member rows are filtered BEFORE group collapsing, so
// group rows, their aggregates, and group drill-downs only ever count
// dir-scoped members.
func prepareListRows(allRows []chatListRow, byName map[string]vendors.SourceReader, groupKey string, inDir func(chatListRow) bool) preparedRows {
	rows := allRows
	if inDir != nil {
		rows = make([]chatListRow, 0, len(allRows))
		for _, r := range allRows {
			if inDir(r) {
				rows = append(rows, r)
			}
		}
	}
	if groupKey != "" {
		return preparedRows{rows: filterRowsByGroupKey(rows, groupKey), byName: byName}
	}
	return preparedRows{rows: collapseGroupRows(rows), byName: byName}
}

func (cq *ChatHandler) listChats(ctx context.Context, paginator *ChatIndexPaginator, groupKey string) error {
	// Foreign-session discovery walks every source's session dir on disk
	// (all of ~/.claude/projects, ~/.pi/agent/sessions, ...) — by far the
	// most expensive part of listing. Discover once per list session;
	// re-entering the list after a peek/edit or a group toggle only
	// re-derives the in-memory view, keeping the round-trip instant.
	allRows, byName, err := cq.buildChatListRows(ctx, paginator)
	if err != nil {
		return fmt.Errorf("failed to build chat list rows: %w", err)
	}
	inDir, _, hasDirFilter := cq.dirScopeRowPredicate()
	dirFilterOn := false
	// The table page survives peek/edit round-trips so a user studying a
	// conversation lands back where they left off. The main list and the
	// group view page independently.
	listPage, groupPage := 0, 0
	for {
		var scope func(chatListRow) bool
		if dirFilterOn {
			scope = inDir
		}
		pr := prepareListRows(allRows, byName, groupKey, scope)

		tableActions := []table.TableAction{}
		if hasDirFilter {
			tableActions = append(tableActions, table.TableAction{
				Format: "[d]irscoped convs",
				Short:  "d",
				Long:   "dir",
				Action: func() error { return errToggleDirFilter },
			})
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
		if tw, err := table.TermWidth(); err == nil && tw > 120 {
			isWide = true
			tblFmt = fmt.Sprintf("%%-%ds| %%-15s | %%-20s| %%-8v | %%-15s | %%-18s | %%-8s | %%-6s | %%v", maxIdxLen)
			headArgs = []any{"Index", "Source", "Created", "Messages", "Profile", "Model", "Cost", "Tokens", "Prompt"}
		}

		backLabel := ""
		if groupKey != "" {
			backLabel = "[b]ack to list"
		}

		startPage := listPage
		if groupKey != "" {
			startPage = groupPage
		}
		tb := table.New(
			table.SlicePaginator(pr.rows),
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
					withSummary, err := table.WidthAppropriateStringTrunc(item.FirstUserMessage, prefix, 15)
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
				withSummary, err := table.WidthAppropriateStringTrunc(item.FirstUserMessage, prefix, 15)
				if err != nil {
					return "", fmt.Errorf("failed to get widthAppropriateChatSummary: %w", err)
				}
				return withSummary, nil
			},
		).
			WithHeader(fmt.Sprintf(tblFmt, headArgs...)).
			WithPageSize(utils.TableTheme().Items).
			WithSingleSelect().
			WithWriter(cq.out).
			WithInput(cq.input).
			WithStartPage(startPage)

		if len(tableActions) > 0 {
			tb = tb.WithActions(tableActions...)
		}
		if backLabel != "" {
			tb = tb.WithBackLabel(backLabel)
		}

		selectedNumbers, shownPage, err := tb.Run()
		if groupKey != "" {
			groupPage = shownPage
		} else {
			listPage = shownPage
		}
		if err != nil {
			if errors.Is(err, errToggleDirFilter) {
				dirFilterOn = !dirFilterOn
				// The scoped and unscoped views paginate differently.
				listPage, groupPage = 0, 0
				continue
			}
			if errors.Is(err, table.ErrBack) {
				if groupKey != "" {
					groupKey = ""
					continue
				}
				return nil
			}
			if errors.Is(err, table.ErrUserInitiatedExit) {
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
			groupPage = 0
			continue
		}
		if sel.Kind == chatRowNative {
			selectedChat, err := cq.getByID(sel.ChatID)
			if err != nil {
				return fmt.Errorf("failed to load selected chat %q: %w", sel.ChatID, err)
			}
			if err := cq.actOnChat(selectedChat, groupKey); err != nil {
				if errors.Is(err, errExitList) {
					return nil
				}
				if errors.Is(err, table.ErrUserInitiatedExit) {
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
		if err := cq.actOnForeignChat(foreign, reader, groupKey); err != nil {
			if errors.Is(err, errExitList) {
				return nil
			}
			if errors.Is(err, table.ErrUserInitiatedExit) {
				return nil
			}
			return err
		}
		continue
	}
}

func (cq *ChatHandler) actOnForeignChat(chat pub_models.Chat, reader vendors.SourceReader, groupKey string) error {
	if err := cq.printChatInfoForeign(cq.out, chat, groupKey); err != nil {
		return fmt.Errorf("failed to printChatInfoForeign: %w", err)
	}
	choice, err := table.ReadUserInputFrom(cq.input)
	if err != nil {
		return fmt.Errorf("failed to read user input: %w", err)
	}
	switch choice {
	case "C", "c", "":
		cloned, err := cq.cloneForeignChat(chat)
		if err != nil {
			return err
		}
		ancli.Noticef("cloned %s session %s → chat %s\n", reader.Source(), chat.SourceID, cloned.ID)
		ancli.Noticef("chat %s is now replyable with flag \"clai -dre query <prompt>\"\n", cloned.ID)
		return errExitList
	case "B", "b":
		clearErr := table.ClearTermTo(cq.out, chatInfoPrintHeight)
		if clearErr != nil {
			return fmt.Errorf("failed to clear term: %w", clearErr)
		}
		return nil
	case "Q", "q":
		return table.ErrUserInitiatedExit
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
	summary, err := table.WidthAppropriateStringTrunc(firstMessages, "summary: \"", 10)
	if err != nil {
		return fmt.Errorf("failed to create widthAppropriateChatSummary: %w", err)
	}

	header := table.Colorize(utils.TableTheme().Primary, "=== Chat info ===")
	fileKey := table.Colorize(utils.TableTheme().Primary, "file path:")
	createdKey := table.Colorize(utils.TableTheme().Primary, "created_at:")
	costKey := table.Colorize(utils.TableTheme().Primary, "total cost:")
	amRepliesKey := table.Colorize(utils.TableTheme().Primary, "am replies:")
	sourceKey := table.Colorize(utils.TableTheme().Primary, "source:")
	userRole := table.Colorize(utils.RoleColor("user"), "user:")
	toolRole := table.Colorize(utils.RoleColor("tool"), "tool:")
	systemRole := table.Colorize(utils.RoleColor("system"), "system:")
	assistantRole := table.Colorize(utils.RoleColor("assistant"), "assistant:")
	bread := utils.TableTheme().Breadtext

	if _, err := fmt.Fprintf(w, "%s\n\n", header); err != nil {
		return fmt.Errorf("write chat header: %w", err)
	}
	// GroupKey display (when non-empty)
	if chat.GroupKey != "" {
		if _, err := fmt.Fprintf(w, "%s %s\n", table.Colorize(utils.TableTheme().Primary, "group key:"), table.Colorize(bread, chat.GroupKey)); err != nil {
			return fmt.Errorf("write group key: %w", err)
		}
	}
	if _, err := fmt.Fprintf(w, "%s %s\n", fileKey, table.Colorize(bread, filePath)); err != nil {
		return fmt.Errorf("write file path: %w", err)
	}
	if opts.foreign {
		if _, err := fmt.Fprintf(w, "%s %s\n", sourceKey, table.Colorize(bread, fmt.Sprintf("%s (session: %s)", chat.Source, chat.SourceID))); err != nil {
			return fmt.Errorf("write source: %w", err)
		}
	}
	if _, err := fmt.Fprintf(w, "%s %s\n", createdKey, table.Colorize(bread, fmt.Sprintf("%v", chat.Created))); err != nil {
		return fmt.Errorf("write created at: %w", err)
	}
	if _, err := fmt.Fprintf(w, "%s %s\n", costKey, table.Colorize(bread, chatListCostStr(chat))); err != nil {
		return fmt.Errorf("write total cost: %w", err)
	}
	if _, err := fmt.Fprintf(w, "%s\n", amRepliesKey); err != nil {
		return fmt.Errorf("write am replies key: %w", err)
	}
	if _, err := fmt.Fprintf(w, "\t%s %s'%v'\n", userRole, table.Colorize(bread, "   "), messageTypeCounter["user"]); err != nil {
		return fmt.Errorf("write user replies: %w", err)
	}
	if _, err := fmt.Fprintf(w, "\t%s %s'%v'\n", toolRole, table.Colorize(bread, "   "), messageTypeCounter["tool"]); err != nil {
		return fmt.Errorf("write tool replies: %w", err)
	}
	if _, err := fmt.Fprintf(w, "\t%s %s'%v'\n", systemRole, table.Colorize(bread, "  "), messageTypeCounter["system"]); err != nil {
		return fmt.Errorf("write system replies: %w", err)
	}
	if _, err := fmt.Fprintf(w, "\t%s %s'%v'\n\n", assistantRole, table.Colorize(bread, ""), messageTypeCounter["assistant"]); err != nil {
		return fmt.Errorf("write assistant replies: %w", err)
	}
	if _, err := fmt.Fprintf(w, "%s\n\n", table.Colorize(bread, summary+"\"")); err != nil {
		return fmt.Errorf("write summary: %w", err)
	}
	backLabel := "go [b]ack to list"
	if groupKey != "" {
		backLabel = "[b]ack to group"
	}
	var choices string
	if opts.foreign {
		choices = table.Colorize(utils.TableTheme().Primary, fmt.Sprintf("(press [c]ontinue (clone to clai), %s, [q]uit): ", backLabel))
	} else {
		choices = table.Colorize(utils.TableTheme().Primary, fmt.Sprintf("(make [p]revQuery (-re/-reply flag), %s, [e]dit messages, [d]elete messages, [q]uit, [<enter>] to continue): ", backLabel))
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

func (cq *ChatHandler) cloneForeignChat(foreign pub_models.Chat) (pub_models.Chat, error) {
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

// selectMessagesAt shows the conversation's message picker opened at startPage
// and returns the selection plus the page it was made on.
func (cq *ChatHandler) selectMessagesAt(chat pub_models.Chat, onlyOneSelect bool, startPage int) ([]int, int, error) {
	head := fmt.Sprintf(editMessageTblFormat, "Index", "Role", "Length", "Summary")
	tb := table.New(
		table.SlicePaginator(chat.Messages),
		func(i int, t pub_models.Message) (string, error) {
			prefix := fmt.Sprintf(editMessageTblFormat, i, t.Role, utf8.RuneCount([]byte(t.Content)), "")
			withSummary, err := table.WidthAppropriateStringTrunc(t.Content, prefix, 25)
			if err != nil {
				return "", fmt.Errorf("failed to get widthAppropriateChatSummary: %w", err)
			}
			return withSummary, nil
		},
	).
		WithHeader(head).
		WithPageSize(utils.TableTheme().Items).
		WithWriter(cq.out).
		WithInput(cq.input).
		WithStartPage(startPage)

	if onlyOneSelect {
		tb = tb.WithSingleSelect()
	}

	return tb.Run()
}

// deleteMessageInChat loops the message picker, symmetric with edit: delete
// messages, then reopen the picker on the same page so a conversation can be
// pruned in one sitting. Indices shift after each deletion; the page is
// clamped to the shrunken range. [b]ack returns to the chat list.
func (cq *ChatHandler) deleteMessageInChat(chat pub_models.Chat) error {
	page := 0
	for {
		selectedIndices, shownPage, err := cq.selectMessagesAt(chat, false, page)
		page = shownPage
		if err != nil {
			if errors.Is(err, table.ErrBack) || errors.Is(err, table.ErrUserInitiatedExit) {
				return nil
			}
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
	}
}

// editMessageInChat loops the message picker: pick a message, edit it in
// $EDITOR, then reopen the picker on the same page so several messages can be
// reworked in one sitting. [b]ack returns to the chat list.
func (cq *ChatHandler) editMessageInChat(chat pub_models.Chat) error {
	page := 0
	for {
		selectedNumbers, shownPage, err := cq.selectMessagesAt(chat, true, page)
		page = shownPage
		if err != nil {
			if errors.Is(err, table.ErrBack) || errors.Is(err, table.ErrUserInitiatedExit) {
				return nil
			}
			return fmt.Errorf("failed to select from table: %w", err)
		}
		if len(selectedNumbers) == 0 || selectedNumbers[0] < 0 || selectedNumbers[0] >= len(chat.Messages) {
			ancli.Warnf("selected message index out of range, nothing edited\n")
			continue
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
		ancli.Okf("modified chat: '%v', message with index: '%v'", chat.ID, selectedNumber)
	}
}
