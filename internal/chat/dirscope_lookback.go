package chat

import (
	"fmt"
	"strings"
)

// LookbackDescriptor is the result of building the passive, CWD-scoped
// recent-conversations memory block.
type LookbackDescriptor struct {
	Block      string // the rendered system-prompt block ("" when no history)
	TotalChats int    // total recorded conversations for the directory
	Shown      int    // how many are rendered in the block
	TotalMsgs  int    // aggregate message count across recorded conversations
	HasHistory bool   // whether the directory has any recorded history
}

// BuildLookbackDescriptor renders the dir-scoped recent-conversations block for
// the binding of dir, drawing one-line summaries and message counts from the
// chat index (never inlining a transcript). It shows at most injectCount of the
// newest-first history entries with a statistics header. When the directory has
// no history, Block is empty and HasHistory is false.
func BuildLookbackDescriptor(confDir, dir string, injectCount int) (LookbackDescriptor, error) {
	cq := &ChatHandler{confDir: confDir}
	scope, err := cq.LoadDirScope(dir)
	if err != nil {
		// Missing binding => no history; not an error for the caller.
		return LookbackDescriptor{}, nil
	}
	if len(scope.History) == 0 {
		return LookbackDescriptor{}, nil
	}

	rows, err := readChatIndex(conversationsDir(confDir))
	if err != nil {
		return LookbackDescriptor{}, fmt.Errorf("read chat index for descriptor: %w", err)
	}
	// Only the (capped) history ids are needed, so bound the lookup map to them
	// rather than the whole corpus index.
	need := make(map[string]struct{}, len(scope.History))
	for _, sc := range scope.History {
		need[sc.ChatID] = struct{}{}
	}
	byID := make(map[string]chatIndexRow, len(need))
	for _, r := range rows {
		if _, ok := need[r.ID]; ok {
			byID[r.ID] = r
		}
	}

	total := len(scope.History)
	totalMsgs := 0
	for _, sc := range scope.History {
		if r, ok := byID[sc.ChatID]; ok {
			totalMsgs += r.MessageCount
		}
	}
	if injectCount <= 0 {
		injectCount = 5
	}
	shown := total
	if shown > injectCount {
		shown = injectCount
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "This directory has %d recorded conversation(s) (showing the %d most recent, %d message(s) total).\n", total, shown, totalMsgs)
	sb.WriteString("Call `search_conversations` to find more (by keyword, here or in another path), `inspect_conversation`\n")
	sb.WriteString("to list a conversation's messages, and `read_message` to read one.\n\n")
	sb.WriteString("<recent_conversations>\n")
	for _, sc := range scope.History[:shown] {
		row := byID[sc.ChatID]
		summary := previewOf(row.FirstUserMessage, 80)
		fmt.Fprintf(&sb, "  <conversation id=%q last_scoped=%q messages=%q>%s</conversation>\n",
			sc.ChatID, humanizeAge(sc.LastScoped), fmt.Sprintf("%d", row.MessageCount), summary)
	}
	sb.WriteString("</recent_conversations>")

	return LookbackDescriptor{
		Block:      sb.String(),
		TotalChats: total,
		Shown:      shown,
		TotalMsgs:  totalMsgs,
		HasHistory: true,
	}, nil
}

// DirHistoryChatIDs returns the set of chat ids bound to dir (head + history),
// used by the [d]ir table filter. Returns an empty set (not an error) when no
// binding exists.
func DirHistoryChatIDs(confDir, dir string) map[string]struct{} {
	cq := &ChatHandler{confDir: confDir}
	scope, err := cq.LoadDirScope(dir)
	if err != nil {
		return map[string]struct{}{}
	}
	ids := make(map[string]struct{}, len(scope.History)+1)
	if scope.ChatID != "" {
		ids[scope.ChatID] = struct{}{}
	}
	for _, sc := range scope.History {
		ids[sc.ChatID] = struct{}{}
	}
	return ids
}
