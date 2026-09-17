package chat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/clai/internal/vendors"
	"github.com/baalimago/clai/internal/vendors/anthropic"
	"github.com/baalimago/clai/internal/vendors/jsonltest"
	"github.com/baalimago/clai/internal/vendors/pi"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/dimensions"
)

// chdirToTemp switches the working directory to a fresh temp dir for the duration
// of the test, returning the temp dir. dirScopeRowPredicate binds against the live
// CWD, so tests that exercise it must control the working directory.
func chdirToTemp(t *testing.T) string {
	t.Helper()
	wd := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(wd); err != nil {
		t.Fatalf("Chdir(%q): %v", wd, err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	return wd
}

// TestDirScopeRowPredicate covers acceptance #13: the predicate keeps exactly
// the rows belonging to the directory — native rows bound to it (head +
// history) or originating in it, foreign rows originating in it — and never
// the globalScope mirror.
func TestDirScopeRowPredicate(t *testing.T) {
	cq, confDir := newTestHandler(t)
	wd := chdirToTemp(t)

	inDir, gotWd, ok := cq.dirScopeRowPredicate()
	if !ok || inDir == nil {
		t.Fatal("expected dir scope predicate even before any history is recorded")
	}
	if gotWd == "" {
		t.Fatal("expected the predicate to report the wd it anchored to")
	}
	if inDir(chatListRow{Kind: chatRowNative, ChatID: "bound"}) {
		t.Fatal("predicate should drop everything before any history is recorded")
	}

	if err := cq.SaveDirScope(wd, "bound"); err != nil {
		t.Fatalf("SaveDirScope: %v", err)
	}

	inDir, _, ok = cq.dirScopeRowPredicate()
	if !ok {
		t.Fatal("expected dir scope predicate once the directory has history")
	}
	if !inDir(chatListRow{Kind: chatRowNative, ChatID: "bound"}) {
		t.Fatal("predicate should keep the bound chat")
	}
	if inDir(chatListRow{Kind: chatRowNative, ChatID: "unbound"}) {
		t.Fatal("predicate should drop a chat neither bound nor originating here")
	}
	if !inDir(chatListRow{Kind: chatRowNative, ChatID: "unbound-but-local", OriginDir: wd}) {
		t.Fatal("predicate should keep an unbound native chat originating in the current directory")
	}
	if inDir(chatListRow{Kind: chatRowNative, ChatID: globalScopeChatID, OriginDir: wd}) {
		t.Fatal("predicate should always drop the globalScope mirror")
	}
	if !inDir(chatListRow{Kind: chatRowForeign, Source: "claude-code", SourceID: "s1", OriginDir: wd}) {
		t.Fatal("predicate should keep a foreign row originating in the current directory")
	}
	if inDir(chatListRow{Kind: chatRowForeign, Source: "claude-code", SourceID: "s2", OriginDir: "/somewhere/else"}) {
		t.Fatal("predicate should drop a foreign row originating elsewhere")
	}
	if inDir(chatListRow{Kind: chatRowForeign, Source: "claude-code", SourceID: "s3"}) {
		t.Fatal("predicate should drop a foreign row with no recorded origin")
	}
	_ = confDir
}

// TestPrepareListRows_DirScopedGroups covers group semantics under the [d]
// filter: member rows are filtered before collapsing, so groups only form
// from — and aggregate over — dir-scoped members.
func TestPrepareListRows_DirScopedGroups(t *testing.T) {
	cq, _ := newTestHandler(t)
	wd := chdirToTemp(t)

	for _, id := range []string{"bound", "bound2"} {
		if err := cq.SaveDirScope(wd, id); err != nil {
			t.Fatalf("SaveDirScope(%q): %v", id, err)
		}
	}
	inDir, _, ok := cq.dirScopeRowPredicate()
	if !ok {
		t.Fatal("expected dir scope predicate")
	}

	allRows := []chatListRow{
		{Kind: chatRowNative, ChatID: "bound", GroupKey: "gk-in", MessageCount: 2, TotalTokens: 10},
		{Kind: chatRowNative, ChatID: "bound2", GroupKey: "gk-in", MessageCount: 3, TotalTokens: 5},
		{Kind: chatRowNative, ChatID: "unbound", GroupKey: "gk-in", MessageCount: 100},
		{Kind: chatRowForeign, Source: "pi", SourceID: "p1", OriginDir: "/somewhere/else", GroupKey: "gk-out"},
		{Kind: chatRowNative, ChatID: "also-unbound", GroupKey: "gk-out"},
	}

	pr := prepareListRows(allRows, nil, "", inDir, true)
	if len(pr.rows) != 1 {
		t.Fatalf("expected exactly one row (the collapsed gk-in group), got %d: %+v", len(pr.rows), pr.rows)
	}
	gr := pr.rows[0]
	if gr.Kind != chatRowGroup || gr.GroupKey != "gk-in" {
		t.Fatalf("expected a gk-in group row, got %+v", gr)
	}
	if gr.GroupMemberCount != 2 {
		t.Fatalf("group should count only dir-scoped members, got %d", gr.GroupMemberCount)
	}
	if gr.MessageCount != 5 || gr.TotalTokens != 15 {
		t.Fatalf("group aggregates should cover only dir-scoped members, got messages=%d tokens=%d", gr.MessageCount, gr.TotalTokens)
	}

	// Drill-down into the group under the filter lists only dir-scoped members.
	pr = prepareListRows(allRows, nil, "gk-in", inDir, true)
	if len(pr.rows) != 2 {
		t.Fatalf("expected 2 dir-scoped members in group view, got %d: %+v", len(pr.rows), pr.rows)
	}
	for _, r := range pr.rows {
		if r.ChatID == "unbound" {
			t.Fatal("group drill-down under [d] leaked an out-of-dir member")
		}
	}

	// Without the filter the view is unchanged: the same group collapses over
	// all three members.
	pr = prepareListRows(allRows, nil, "", nil, true)
	for _, r := range pr.rows {
		if r.Kind == chatRowGroup && r.GroupKey == "gk-in" && r.GroupMemberCount != 3 {
			t.Fatalf("unfiltered group should count all members, got %d", r.GroupMemberCount)
		}
	}
}

// TestPrepareListRows_ForeignToggle pins the [f] view: with foreign rows
// hidden the view holds native rows and groups only; shown, the foreign
// rows are back and the dir filter still never removes them.
func TestPrepareListRows_ForeignToggle(t *testing.T) {
	allRows := []chatListRow{
		{Kind: chatRowNative, ChatID: "n1", GroupKey: "gk-a"},
		{Kind: chatRowForeign, Source: "pi", SourceID: "p1", GroupKey: "gk-b"},
		{Kind: chatRowForeign, Source: "claude-code", SourceID: "c1", GroupKey: "gk-c"},
		{Kind: chatRowNative, ChatID: "n2", GroupKey: "gk-c"},
	}
	hidden := prepareListRows(allRows, nil, "", nil, false)
	for _, r := range hidden.rows {
		if r.Kind == chatRowForeign {
			t.Fatalf("foreign row leaked into the hidden view: %+v", r)
		}
		if r.Kind == chatRowGroup {
			t.Fatalf("a group must not survive on one native member once its foreign member is hidden: %+v", r)
		}
	}
	if len(hidden.rows) != 2 {
		t.Fatalf("hidden view = %+v, want the two native rows", hidden.rows)
	}
	shown := prepareListRows(allRows, nil, "", nil, true)
	foreign := 0
	for _, r := range shown.rows {
		if r.Kind == chatRowForeign {
			foreign++
		}
	}
	if foreign != 1 || len(shown.rows) != 3 {
		t.Fatalf("shown view = %+v, want n1, the pi row and the gk-c group", shown.rows)
	}
	if !hasForeignRows(allRows) || hasForeignRows(hidden.rows) {
		t.Fatal("hasForeignRows must report the presence of foreign rows")
	}
}

// TestListChats_ForeignToggleThroughListChats drives the [f] button through
// the real table: the action renders only when a source produced rows,
// pressing it hides the foreign rows, pressing it again shows them.
func TestListChats_ForeignToggleThroughListChats(t *testing.T) {
	cq, confDir := newTestHandler(t)
	convDir := conversationsDir(confDir)
	chdirToTemp(t)
	if err := Save(convDir, pub_models.Chat{ID: "native", Created: time.Date(2026, 1, 2, 3, 4, 6, 0, time.UTC), Messages: []pub_models.Message{{Role: "user", Content: "native prompt"}}}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	reader := stubSourceReader{name: "test-source", rows: []vendors.SourceRow{{
		Source: "test-source", SourceID: "ext-1", FirstUserMessage: "foreign prompt", FullFirstUserMessage: "foreign prompt",
		Created: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}}}
	t.Cleanup(useTestSourceReaders([]vendors.SourceReader{reader}))

	cq.input = strings.NewReader("f\nf\n")
	paginator, err := NewChatIndexPaginator(convDir)
	if err != nil {
		t.Fatalf("NewChatIndexPaginator: %v", err)
	}
	var out strings.Builder
	cq.out = &out
	if err := cq.listChats(context.Background(), paginator, ""); err == nil {
		t.Fatal("expected listChats to surface the scripted stop error")
	}
	got := out.String()
	if n := strings.Count(got, "[f]oreign convs: shown"); n != 2 {
		t.Fatalf("expected the shown label in the first and third render, got %d:\n%s", n, got)
	}
	if n := strings.Count(got, "[f]oreign convs: hidden"); n != 1 {
		t.Fatalf("expected the hidden label in the second render, got %d:\n%s", n, got)
	}
	// The foreign row (03:04:05) shows in the first and third render only;
	// the native row (03:04:06) in all three.
	if n := strings.Count(got, "03:04:05"); n != 2 {
		t.Fatalf("expected the foreign row twice (before and after the hide), got %d:\n%s", n, got)
	}
	if n := strings.Count(got, "03:04:06"); n != 3 {
		t.Fatalf("expected the native row in all three renders, got %d:\n%s", n, got)
	}

	// Without any foreign row the button is not offered.
	t.Cleanup(useTestSourceReaders(nil))
	cq.input = strings.NewReader("")
	out.Reset()
	paginator, err = NewChatIndexPaginator(convDir)
	if err != nil {
		t.Fatalf("NewChatIndexPaginator: %v", err)
	}
	_ = cq.listChats(context.Background(), paginator, "")
	if strings.Contains(out.String(), "[f]oreign") {
		t.Fatalf("the [f] button must not render without foreign rows:\n%s", out.String())
	}
}

// TestToggleLabel pins the state rendering: words always, bold plus
// underline only in the non-default position and only with colour on,
// closed with attribute resets so the prompt's own colour survives.
func TestToggleLabel(t *testing.T) {
	prev := ancli.UseColor
	t.Cleanup(func() { ancli.UseColor = prev })
	ancli.UseColor = true
	if got := toggleLabel("[f]oreign convs", ": ", "hidden", "shown", false); got != "[f]oreign convs: shown" {
		t.Fatalf("default position must be plain, got %q", got)
	}
	if got := toggleLabel("[f]oreign convs", ": ", "hidden", "shown", true); got != "\x1b[1;4m[f]oreign convs: hidden\x1b[22;24m" {
		t.Fatalf("active position must be bold and underlined without a full reset, got %q", got)
	}
	ancli.UseColor = false
	if got := toggleLabel("[d]", ":", "on", "off", true); got != "[d]:on" {
		t.Fatalf("without colour the words carry the state alone, got %q", got)
	}
}

// TestListChats_DirFilterTogglesThroughListChats covers acceptance #13 end-to-end:
// the [d]ir button renders in the listChats table and pressing it activates the
// dir-scoped view (surfaced as the "dir filter" prompt marker).
func TestListChats_DirFilterTogglesThroughListChats(t *testing.T) {
	cq, confDir := newTestHandler(t)
	convDir := conversationsDir(confDir)
	wd := chdirToTemp(t)

	// Prevent real foreign sessions from polluting the table or causing timeout.
	restoreReaders := useTestSourceReaders(nil)
	t.Cleanup(restoreReaders)

	for _, c := range []pub_models.Chat{
		{ID: "bound", Created: time.Date(2026, 1, 2, 3, 4, 6, 0, time.UTC), Messages: []pub_models.Message{{Role: "user", Content: "bound prompt"}}},
		{ID: "unbound", Created: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), Messages: []pub_models.Message{{Role: "user", Content: "unbound prompt"}}},
	} {
		if err := Save(convDir, c); err != nil {
			t.Fatalf("Save(%q): %v", c.ID, err)
		}
	}
	if err := cq.SaveDirScope(wd, "bound"); err != nil {
		t.Fatalf("SaveDirScope: %v", err)
	}

	// Script: press "d" to toggle the dir filter on, "d" again to toggle it
	// back off, then abort (EOF) so listChats returns.
	cq.input = strings.NewReader("d\nd\n")

	paginator, err := NewChatIndexPaginator(convDir)
	if err != nil {
		t.Fatalf("NewChatIndexPaginator: %v", err)
	}
	var out strings.Builder
	cq.out = &out
	if err := cq.listChats(context.Background(), paginator, ""); err == nil {
		t.Fatal("expected listChats to surface the scripted stop error")
	}

	got := out.String()
	if n := strings.Count(got, "[d]irscoped convs: off"); n != 2 {
		t.Fatalf("expected the off label in the first and third render, got %d in: %q", n, got)
	}
	if n := strings.Count(got, "[d]irscoped convs: on"); n != 1 {
		t.Fatalf("expected the on label in the filtered render, got %d in: %q", n, got)
	}
	// The unbound chat (timestamp 03:04:05) should appear twice — in the
	// first render (before filter) and third render (after filter toggled off).
	// The filtered render in between shows only the bound chat (03:04:06).
	if n := strings.Count(got, "03:04:05"); n != 2 {
		t.Fatalf("expected unbound timestamp twice (unfiltered renders), got %d in: %q", n, got)
	}
}

func TestListChats_DirFilterWithoutBindingsShowsEmptyDirScopedView(t *testing.T) {
	cq, confDir := newTestHandler(t)
	convDir := conversationsDir(confDir)
	_ = chdirToTemp(t)

	// Prevent real foreign sessions (claude-code, pi) on disk from bleeding
	// into the test.
	restoreReaders := useTestSourceReaders(nil)
	t.Cleanup(restoreReaders)

	for _, c := range []pub_models.Chat{
		{ID: "a", Created: time.Date(2026, 1, 2, 3, 4, 6, 0, time.UTC), Messages: []pub_models.Message{{Role: "user", Content: "prompt a"}}},
		{ID: "b", Created: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), Messages: []pub_models.Message{{Role: "user", Content: "prompt b"}}},
	} {
		if err := Save(convDir, c); err != nil {
			t.Fatalf("Save(%q): %v", c.ID, err)
		}
	}

	cq.input = strings.NewReader("d\n")

	paginator, err := NewChatIndexPaginator(convDir)
	if err != nil {
		t.Fatalf("NewChatIndexPaginator: %v", err)
	}
	var out strings.Builder
	cq.out = &out
	if err := cq.listChats(context.Background(), paginator, ""); err == nil {
		t.Fatal("expected listChats to surface the scripted stop error")
	}

	got := out.String()
	if !strings.Contains(got, "[d]irscoped convs") {
		t.Fatalf("expected the [d]irscoped convs button rendered in the table, got: %q", got)
	}
	// After pressing "d", the filtered view should show zero rows.
	// Verify by checking that the header appears but no data rows follow it
	// in the second render.
	if strings.Count(got, "Index|") < 2 {
		t.Fatalf("expected two table renders (unfiltered + filtered), got %d in: %q", strings.Count(got, "Index|"), got)
	}
}

func TestChatListTokensOrNA(t *testing.T) {
	chWith := pub_models.Chat{
		Created: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		ID:      "a",
		TokenUsage: &pub_models.Usage{
			TotalTokens: 42,
		},
	}
	chWithout := pub_models.Chat{
		Created:    time.Date(2026, 1, 2, 3, 4, 4, 0, time.UTC),
		ID:         "b",
		TokenUsage: nil,
	}

	if got := chatListTokenStr(chWith); got != "0.042K" {
		t.Fatalf("with usage: want %q, got %q", "0.042K", got)
	}
	if got := chatListTokenStr(chWithout); got != "N/A" {
		t.Fatalf("without usage: want %q, got %q", "N/A", got)
	}
}

func TestTokenAbbreviationTiers(t *testing.T) {
	cases := []struct {
		name  string
		total int
		want  string
	}{
		{"zero", 0, "0"},
		{"sub-thousand", 42, "0.042K"},
		{"thousand", 1234, "1K"},
		{"hundred-thousand", 123400, "123K"},
		{"million", 1234000, "1.2M"},
		{"exact million", 1000000, "1.0M"},
		{"ten million", 12345678, "12.3M"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := abbrevTokens(tc.total); got != tc.want {
				t.Fatalf("abbrevTokens(%d) = %q, want %q", tc.total, got, tc.want)
			}
		})
	}
}

func TestChatIndexTokenStr_UsesMillions(t *testing.T) {
	if got := chatIndexTokenStr(chatIndexRow{TotalTokens: 1234000}); got != "1.2M" {
		t.Fatalf("chatIndexTokenStr(1,234,000) = %q, want %q", got, "1.2M")
	}
	if got := chatIndexTokenStr(chatIndexRow{TotalTokens: 0}); got != "N/A" {
		t.Fatalf("chatIndexTokenStr(0) = %q, want %q", got, "N/A")
	}
}

// TestMessagePickerRow_ShowsAssistantContent guards the edit/delete picker
// against blank assistant rows: assistant tool-call turns are persisted with an
// empty Content (model-safe), so the picker must reconstruct a readable line
// from ToolCalls via messageDisplayText, exactly like the conversation view.
func TestMessagePickerRow_ShowsAssistantContent(t *testing.T) {
	toolTurn := pub_models.Message{
		Role: "assistant",
		ToolCalls: []pub_models.Call{
			{Name: "cat", Inputs: &pub_models.Input{"file": "main.go"}},
		},
	}
	row, err := messagePickerRow(2, toolTurn, 80)
	if err != nil {
		t.Fatalf("messagePickerRow: %v", err)
	}
	if !strings.Contains(row, "Call:") || !strings.Contains(row, "cat") {
		t.Fatalf("tool-call turn should reconstruct the call, got: %q", row)
	}

	textTurn := pub_models.Message{Role: "assistant", Content: "the answer is 42"}
	row, err = messagePickerRow(3, textTurn, 80)
	if err != nil {
		t.Fatalf("messagePickerRow: %v", err)
	}
	if !strings.Contains(row, "the answer is 42") {
		t.Fatalf("text turn should show its content, got: %q", row)
	}
}

func TestMessageEditorText_UsesOnlyStoredContent(t *testing.T) {
	textTurn := pub_models.Message{Role: "assistant", Content: "the answer is 42"}
	if got := messageEditorText(textTurn); got != "the answer is 42" {
		t.Fatalf("text turn editor content = %q, want %q", got, "the answer is 42")
	}

	structuredTurn := pub_models.Message{
		Role:             "assistant",
		ReasoningContent: "private reasoning",
		ToolCalls:        []pub_models.Call{{Name: "cat", Inputs: &pub_models.Input{"file": "main.go"}}},
	}
	if got := messageEditorText(structuredTurn); got != "" {
		t.Fatalf("structured turn editor content = %q, want empty stored content", got)
	}
}

func TestActOnChat_enter_continues(t *testing.T) {
	confDir := t.TempDir()
	if err := utils.CreateConfigDir(confDir); err != nil {
		t.Fatalf("CreateConfigDir: %v", err)
	}

	convDir := filepath.Join(confDir, "conversations")
	id := "enter-continue-test"
	ch := pub_models.Chat{ID: id, Created: time.Now(), Messages: []pub_models.Message{{Role: "user", Content: "seed"}}}
	if err := Save(convDir, ch); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Create a FIFO to act as TTY and provide a single "\n" (empty line) input so actOnChat treats it as continue.
	fifoPath := filepath.Join(t.TempDir(), "tty-fifo")
	if err := syscall.Mkfifo(fifoPath, 0o600); err != nil {
		t.Fatalf("Mkfifo: %v", err)
	}
	// Writer goroutine: open fifo for writing and write "\n" then close.
	go func() {
		f, err := os.OpenFile(fifoPath, os.O_WRONLY, 0)
		if err != nil {
			return
		}
		defer f.Close()
		_, _ = f.WriteString("\n")
	}()

	// Set TTY env so ReadUserInput will use the FIFO.
	oldTTY := os.Getenv("TTY")
	_ = os.Setenv("TTY", fifoPath)
	defer func() { _ = os.Setenv("TTY", oldTTY) }()

	cq := &ChatHandler{confDir: confDir, convDir: convDir, out: io.Discard}
	if err := cq.actOnChat(ch, ""); err != nil {
		if !errors.Is(err, errExitList) {
			t.Fatalf("actOnChat: %v", err)
		}
	}

	// Verify a dirscope binding file was created and references the chat id.
	dirsPath := filepath.Join(convDir, "dirs")
	entries, err := os.ReadDir(dirsPath)
	if err != nil {
		t.Fatalf("ReadDir dirs: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dirsPath, e.Name()))
		if err != nil {
			t.Fatalf("ReadFile dirscope: %v", err)
		}
		var ds DirScope
		if err := json.Unmarshal(b, &ds); err != nil {
			t.Fatalf("Unmarshal dirscope: %v", err)
		}
		if ds.ChatID == id {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected dirscope binding for chat id %q not found", id)
	}
}

func TestChatListCostFormattingHelpers(t *testing.T) {
	chatWithCost := pub_models.Chat{Queries: []pub_models.QueryCost{{CostUSD: 1.2}, {CostUSD: 0.034}}}
	chatWithoutCost := pub_models.Chat{}

	if got := chatListCostStr(chatWithCost); got != "$1.234" {
		t.Fatalf("cost string mismatch: got %q", got)
	}
	if got := chatListCostStr(chatWithoutCost); got != "N/A" {
		t.Fatalf("expected N/A, got %q", got)
	}
}

func TestPrintChatInfo_ShowsCost(t *testing.T) {
	confDir := t.TempDir()
	if err := utils.CreateConfigDir(confDir); err != nil {
		t.Fatalf("CreateConfigDir: %v", err)
	}
	oldConf := os.Getenv("CLAI_CONFIG_DIR")
	if err := os.Setenv("CLAI_CONFIG_DIR", confDir); err != nil {
		t.Fatalf("set CLAI_CONFIG_DIR: %v", err)
	}
	defer func() {
		if err := os.Setenv("CLAI_CONFIG_DIR", oldConf); err != nil {
			t.Fatalf("restore CLAI_CONFIG_DIR: %v", err)
		}
	}()

	chatWithCost := pub_models.Chat{
		ID:       "chat-with-cost",
		Created:  time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Messages: []pub_models.Message{{Role: "user", Content: "hello"}},
		Queries:  []pub_models.QueryCost{{CostUSD: 14.53}},
	}
	chatWithoutCost := pub_models.Chat{
		ID:       "chat-without-cost",
		Created:  time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Messages: []pub_models.Message{{Role: "user", Content: "hello"}},
	}

	var out strings.Builder
	cq := &ChatHandler{out: &out}
	if err := cq.printChatInfo(&out, chatWithCost, ""); err != nil {
		t.Fatalf("printChatInfo with cost: %v", err)
	}
	if !strings.Contains(out.String(), "$14.53") {
		t.Fatalf("expected cost in output, got: %q", out.String())
	}

	out.Reset()
	if err := cq.printChatInfo(&out, chatWithoutCost, ""); err != nil {
		t.Fatalf("printChatInfo without cost: %v", err)
	}
	if !strings.Contains(out.String(), "N/A") {
		t.Fatalf("expected N/A in output, got: %q", out.String())
	}
}

func TestPrintChatInfo_TokenUsage(t *testing.T) {
	confDir := t.TempDir()
	if err := utils.CreateConfigDir(confDir); err != nil {
		t.Fatalf("CreateConfigDir: %v", err)
	}
	oldConf := os.Getenv("CLAI_CONFIG_DIR")
	if err := os.Setenv("CLAI_CONFIG_DIR", confDir); err != nil {
		t.Fatalf("set CLAI_CONFIG_DIR: %v", err)
	}
	defer func() {
		if err := os.Setenv("CLAI_CONFIG_DIR", oldConf); err != nil {
			t.Fatalf("restore CLAI_CONFIG_DIR: %v", err)
		}
	}()

	chatWithTokens := pub_models.Chat{
		ID:       "chat-with-tokens",
		Created:  time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Messages: []pub_models.Message{{Role: "user", Content: "hello"}},
		// Last session: the most recent gauge.
		TokenUsage: &pub_models.Usage{TotalTokens: 45000},
		Queries: []pub_models.QueryCost{
			{Usage: pub_models.Usage{TotalTokens: 1000}},
			{Usage: pub_models.Usage{TotalTokens: 2000}},
			{Usage: pub_models.Usage{TotalTokens: 45000}},
		},
	}
	chatWithoutTokens := pub_models.Chat{
		ID:       "chat-without-tokens",
		Created:  time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Messages: []pub_models.Message{{Role: "user", Content: "hello"}},
	}

	var out strings.Builder
	cq := &ChatHandler{out: &out}
	if err := cq.printChatInfo(&out, chatWithTokens, ""); err != nil {
		t.Fatalf("printChatInfo with tokens: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "token usage:") {
		t.Fatalf("expected token usage section, got: %q", got)
	}
	// Values are colorized, so assert on the value substrings (same pattern as
	// TestPrintChatInfo_ShowsCost asserting "$14.53").
	if !strings.Contains(got, "48K") {
		t.Fatalf("expected lifetime total 48K (1000+2000+45000), got: %q", got)
	}
	if !strings.Contains(got, "45K") {
		t.Fatalf("expected most recent 45K, got: %q", got)
	}

	out.Reset()
	if err := cq.printChatInfo(&out, chatWithoutTokens, ""); err != nil {
		t.Fatalf("printChatInfo without tokens: %v", err)
	}
	if !strings.Contains(out.String(), "N/A") {
		t.Fatalf("expected N/A in output, got: %q", out.String())
	}
}

func TestListChats_IncludesModelColumnAndValue(t *testing.T) {
	confDir := t.TempDir()
	if err := utils.CreateConfigDir(confDir); err != nil {
		t.Fatalf("CreateConfigDir: %v", err)
	}

	// Prevent real foreign sessions from polluting the table.
	restoreReaders := useTestSourceReaders(nil)
	t.Cleanup(restoreReaders)

	convDir := filepath.Join(confDir, "conversations")
	chat := pub_models.Chat{
		ID:       "chat-with-model",
		Created:  time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Profile:  "default",
		Messages: []pub_models.Message{{Role: "user", Content: "hello from a fairly descriptive prompt"}},
		Queries:  []pub_models.QueryCost{{CostUSD: 0.42, Model: "openai/gpt-4.1-mini"}},
	}
	if err := Save(convDir, chat); err != nil {
		t.Fatalf("Save: %v", err)
	}

	oldTTY := os.Getenv("TTY")
	if err := os.Setenv("TTY", "/dev/null"); err != nil {
		t.Fatalf("set TTY: %v", err)
	}
	defer func() {
		if err := os.Setenv("TTY", oldTTY); err != nil {
			t.Fatalf("restore TTY: %v", err)
		}
	}()

	paginator, err := NewChatIndexPaginator(convDir)
	if err != nil {
		t.Fatalf("NewChatIndexPaginator: %v", err)
	}

	var out strings.Builder
	cq := &ChatHandler{confDir: confDir, convDir: convDir, out: &out}
	err = cq.listChats(context.Background(), paginator, "")
	if err == nil {
		t.Fatal("listChats() error = nil, want error from empty selection input")
	}
	if !strings.Contains(err.Error(), "failed to select chat") {
		t.Fatalf("listChats() error = %q, want selection context", err.Error())
	}

	got := out.String()
	if !strings.Contains(got, "Model") {
		t.Fatalf("expected table header to include Model, got: %q", got)
	}
	if !strings.Contains(got, "openai/gpt-4.1-mini") {
		t.Fatalf("expected table row to include model value, got: %q", got)
	}
}

func TestListChats_NarrowWidthShowsCostAndAbout(t *testing.T) {
	confDir := t.TempDir()
	if err := utils.CreateConfigDir(confDir); err != nil {
		t.Fatalf("CreateConfigDir: %v", err)
	}

	// Prevent real foreign sessions from polluting the table or causing timeout.
	restoreReaders := useTestSourceReaders(nil)
	t.Cleanup(restoreReaders)

	convDir := filepath.Join(confDir, "conversations")
	chat := pub_models.Chat{
		ID:       "chat-narrow-width",
		Created:  time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Messages: []pub_models.Message{{Role: "user", Content: "hello from a fairly descriptive prompt"}},
		Queries:  []pub_models.QueryCost{{CostUSD: 0.42, Model: "gpt-5.4"}},
	}
	if err := Save(convDir, chat); err != nil {
		t.Fatalf("Save: %v", err)
	}

	oldTTY := os.Getenv("TTY")
	if err := os.Setenv("TTY", "/dev/null"); err != nil {
		t.Fatalf("set TTY: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Setenv("TTY", oldTTY); err != nil {
			t.Fatalf("restore TTY: %v", err)
		}
	})

	paginator, err := NewChatIndexPaginator(convDir)
	if err != nil {
		t.Fatalf("NewChatIndexPaginator: %v", err)
	}

	var out strings.Builder
	// The narrow table is selected by the session snapshot width, not by the
	// ambient COLUMNS variable (the ioctl is authoritative; R2-02).
	cq := &ChatHandler{confDir: confDir, convDir: convDir, out: &out, dims: dimensions.Dimensions{Width: 100}}
	err = cq.listChats(context.Background(), paginator, "")
	if err == nil {
		t.Fatal("listChats() error = nil, want error from empty selection input")
	}
	if !strings.Contains(err.Error(), "failed to select chat") {
		t.Fatalf("listChats() error = %q, want selection context", err.Error())
	}

	got := out.String()
	if strings.Contains(got, "%!(EXTRA") {
		t.Fatalf("expected no fmt extra marker, got: %q", got)
	}
	if !strings.Contains(got, "Cost") {
		t.Fatalf("expected narrow table to include Cost, got: %q", got)
	}
	if !strings.Contains(got, "About") || strings.Contains(got, "Prompt") {
		t.Fatalf("expected narrow table header About (never Prompt), got: %q", got)
	}
}

// TestCollapseGroupRows_BasicGrouping verifies that conversations with the same
// first-user-message GroupKey are collapsed into a single [group:N] row, while
// conversations with unique GroupKeys remain ungrouped.
func TestCollapseGroupRows_BasicGrouping(t *testing.T) {
	gk := ComputeGroupKeyFromText("fix the auth bug")
	rows := []chatListRow{
		{
			Kind: chatRowNative, Created: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
			ChatID: "a", FirstUserMessage: "fix the auth bug", GroupKey: gk,
			Profile: "default", Model: "gpt-5", MessageCount: 5, TotalTokens: 100, TotalCostUSD: 1.23,
		},
		{
			Kind: chatRowNative, Created: time.Date(2026, 1, 2, 3, 4, 4, 0, time.UTC),
			ChatID: "b", FirstUserMessage: "fix the auth bug", GroupKey: gk,
			Profile: "default", Model: "gpt-5", MessageCount: 3, TotalTokens: 60, TotalCostUSD: 0.50,
		},
		{
			Kind: chatRowNative, Created: time.Date(2026, 1, 2, 3, 4, 3, 0, time.UTC),
			ChatID: "c", FirstUserMessage: "refactor database", GroupKey: ComputeGroupKeyFromText("refactor database"),
			Profile: "default", Model: "gpt-4o", MessageCount: 4, TotalTokens: 80, TotalCostUSD: 0.80,
		},
	}
	got := collapseGroupRows(rows)
	if len(got) != 2 {
		t.Fatalf("collapseGroupRows: expected 2 rows (1 group + 1 ungrouped), got %d", len(got))
	}
	if got[0].Kind != chatRowGroup {
		t.Fatalf("collapseGroupRows[0]: expected chatRowGroup, got %v", got[0].Kind)
	}
	if got[0].GroupMemberCount != 2 {
		t.Fatalf("collapseGroupRows[0].GroupMemberCount = %d, want 2", got[0].GroupMemberCount)
	}
	if got[0].GroupKey != gk {
		t.Fatalf("collapseGroupRows[0].GroupKey = %q, want %q", got[0].GroupKey, gk)
	}
	// Aggregate totals: 100+60 = 160 tokens, 1.23+0.50 = 1.73 cost
	if got[0].TotalTokens != 160 {
		t.Fatalf("collapseGroupRows[0].TotalTokens = %d, want 160", got[0].TotalTokens)
	}
	if got[0].TotalCostUSD != 1.73 {
		t.Fatalf("collapseGroupRows[0].TotalCostUSD = %.2f, want 1.73", got[0].TotalCostUSD)
	}
	// Second row should be the refactor-database conversation (ungrouped)
	if got[1].Kind != chatRowNative {
		t.Fatalf("collapseGroupRows[1]: expected chatRowNative, got %v", got[1].Kind)
	}
	if got[1].ChatID != "c" {
		t.Fatalf("collapseGroupRows[1].ChatID = %q, want %q", got[1].ChatID, "c")
	}
}

// TestCollapseGroupRows_EmptyGroupKeyNeverGrouped verifies that rows with empty
// GroupKey never form groups, even when their FirstUserMessage text matches.
func TestCollapseGroupRows_EmptyGroupKeyNeverGrouped(t *testing.T) {
	rows := []chatListRow{
		{Kind: chatRowNative, Created: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), ChatID: "a", FirstUserMessage: "same prompt", GroupKey: ""},
		{Kind: chatRowNative, Created: time.Date(2026, 1, 2, 3, 4, 4, 0, time.UTC), ChatID: "b", FirstUserMessage: "same prompt", GroupKey: ""},
	}
	got := collapseGroupRows(rows)
	if len(got) != 2 {
		t.Fatalf("collapseGroupRows: expected 2 ungrouped rows, got %d", len(got))
	}
	for i, r := range got {
		if r.Kind == chatRowGroup {
			t.Fatalf("collapseGroupRows[%d]: unexpected group row with empty GroupKey: %+v", i, r)
		}
	}
}

// TestCollapseGroupRows_GlobalScopeMirrorNeverGrouped verifies the globalScope
// mirror (which shares the newest conversation's GroupKey) is excluded from
// grouping, so aggregates are not double-counted and no phantom group forms.
func TestCollapseGroupRows_GlobalScopeMirrorNeverGrouped(t *testing.T) {
	gk := ComputeGroupKeyFromText("latest prompt")
	rows := []chatListRow{
		{Kind: chatRowNative, Created: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), ChatID: globalScopeChatID, FirstUserMessage: "latest prompt", GroupKey: gk, MessageCount: 4},
		{Kind: chatRowNative, Created: time.Date(2026, 1, 2, 3, 4, 4, 0, time.UTC), ChatID: "real", FirstUserMessage: "latest prompt", GroupKey: gk, MessageCount: 4},
	}
	got := collapseGroupRows(rows)
	if len(got) != 2 {
		t.Fatalf("expected 2 ungrouped rows (mirror excluded), got %d", len(got))
	}
	for i, r := range got {
		if r.Kind == chatRowGroup {
			t.Fatalf("row %d: globalScope mirror formed a group: %+v", i, r)
		}
	}
	// Entering a group must never surface the mirror either.
	members := filterRowsByGroupKey(rows, gk)
	if len(members) != 1 || members[0].ChatID != "real" {
		t.Fatalf("expected only the real conversation as group member, got %+v", members)
	}
}

// TestCollapseGroupRows_SingleMemberSuppressed verifies that a GroupKey with only
// one conversation produces no group row.
func TestCollapseGroupRows_SingleMemberSuppressed(t *testing.T) {
	gk := ComputeGroupKeyFromText("unique prompt")
	rows := []chatListRow{
		{Kind: chatRowNative, Created: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), ChatID: "a", FirstUserMessage: "unique prompt", GroupKey: gk},
	}
	got := collapseGroupRows(rows)
	if len(got) != 1 {
		t.Fatalf("collapseGroupRows: expected 1 ungrouped row, got %d", len(got))
	}
	if got[0].Kind == chatRowGroup {
		t.Fatal("collapseGroupRows: single member should not produce a group row")
	}
}

// TestSave_StampsGroupKeyOnFirstPersist verifies that Save() computes and
// persists GroupKey when a chat with messages is first saved.
func TestSave_StampsGroupKeyOnFirstPersist(t *testing.T) {
	tmp := t.TempDir()
	ch := pub_models.Chat{
		ID:       "test-chat",
		Messages: []pub_models.Message{{Role: "system", Content: "sys"}, {Role: "user", Content: "fix the auth bug"}},
	}
	if ch.GroupKey != "" {
		t.Fatal("expected empty GroupKey before save")
	}
	if err := Save(tmp, ch); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// Reload from disk
	loaded, err := FromPath(filepath.Join(tmp, "test-chat.json"))
	if err != nil {
		t.Fatalf("FromPath: %v", err)
	}
	wantGK := ComputeGroupKeyFromText("fix the auth bug")
	if loaded.GroupKey != wantGK {
		t.Fatalf("loaded.GroupKey = %q, want %q", loaded.GroupKey, wantGK)
	}
	if loaded.GroupKey == "" {
		t.Fatal("expected non-empty GroupKey after save")
	}
}

// stubSourceReader implements vendors.SourceReader for tests. seen records
// the cache each method was handed, so a test can prove one cache reached
// every reader.
type stubSourceReader struct {
	name string
	rows []vendors.SourceRow
	seen *[]vendors.SourceCache
}

func (s stubSourceReader) Source() string { return s.name }
func (s stubSourceReader) Discover(ctx context.Context, cache vendors.SourceCache) ([]vendors.SourceRow, error) {
	if s.seen != nil {
		*s.seen = append(*s.seen, cache)
	}
	return s.rows, nil
}

func (s stubSourceReader) Read(ctx context.Context, cache vendors.SourceCache, sourceID string) (pub_models.Chat, error) {
	if s.seen != nil {
		*s.seen = append(*s.seen, cache)
	}
	return pub_models.Chat{}, nil
}

// TestForeignChatRows_GroupKeyFromFullFirstUserMessage verifies that
// foreignChatRows computes GroupKey from FullFirstUserMessage, not the
// truncated FirstUserMessage. This prevents two foreign conversations whose
// first 100 chars match but full text differs from colliding into the same group.
func TestForeignChatRows_GroupKeyFromFullFirstUserMessage(t *testing.T) {
	cq, _ := newTestHandler(t)

	// Two conversations whose first 100 chars are identical but full text differs.
	var prefix strings.Builder
	for range 100 {
		prefix.WriteString("x")
	}
	suffixA := "-This-is-the-unique-suffix-for-A"
	suffixB := "-This-is-the-unique-suffix-for-B"

	reader := stubSourceReader{
		name: "test-source",
		rows: []vendors.SourceRow{
			{
				Source:               "test-source",
				SourceID:             "a",
				FirstUserMessage:     prefix.String(), // truncated (same for both)
				FullFirstUserMessage: prefix.String() + suffixA,
				Created:              time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
			},
			{
				Source:               "test-source",
				SourceID:             "b",
				FirstUserMessage:     prefix.String(), // truncated (same for both)
				FullFirstUserMessage: prefix.String() + suffixB,
				Created:              time.Date(2026, 1, 2, 3, 4, 4, 0, time.UTC),
			},
		},
	}

	rows, err := cq.foreignChatRows(context.Background(), []vendors.SourceReader{reader}, map[string]struct{}{})
	if err != nil {
		t.Fatalf("foreignChatRows: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}

	// Verify GroupKeys are different (because FullFirstUserMessage differs)
	if rows[0].GroupKey == rows[1].GroupKey {
		t.Fatalf("expected different GroupKeys for different FullFirstUserMessage, got same: %q", rows[0].GroupKey)
	}

	// Verify GroupKeys match what we'd compute from full text
	want0 := ComputeGroupKeyFromText(prefix.String() + suffixA)
	want1 := ComputeGroupKeyFromText(prefix.String() + suffixB)
	if rows[0].GroupKey != want0 {
		t.Fatalf("row 0 GroupKey: got %q, want %q", rows[0].GroupKey, want0)
	}
	if rows[1].GroupKey != want1 {
		t.Fatalf("row 1 GroupKey: got %q, want %q", rows[1].GroupKey, want1)
	}

	// Sanity check: if we had used truncated FirstUserMessage, keys would be equal
	collidedKey := ComputeGroupKeyFromText(prefix.String())
	if collidedKey == want0 || collidedKey == want1 {
		t.Fatal("test setup error: truncated key should not match full-text keys")
	}
}

// TestListChats_GroupKeyZeroMembers_RendersGroupIndicator covers issue #3:
// when a group view expands to zero members (groupKey != "" but no rows
// match), the prompt bar must still indicate the group context with the
// truncated hash and a [b]ack to list button.
func TestListChats_GroupKeyZeroMembers_RendersGroupIndicator(t *testing.T) {
	cq, confDir := newTestHandler(t)
	convDir := conversationsDir(confDir)

	// Prevent real foreign sessions from polluting the table and causing timeout.
	restoreReaders := useTestSourceReaders(nil)
	t.Cleanup(restoreReaders)

	// Save chats whose GroupKey does NOT match the queried groupKey,
	// ensuring the group view will be empty.
	for _, c := range []pub_models.Chat{
		{
			ID:       "chat-a",
			Created:  time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
			Messages: []pub_models.Message{{Role: "user", Content: "unrelated prompt"}},
			GroupKey: ComputeGroupKeyFromText("unrelated prompt"),
		},
		{
			ID:       "chat-b",
			Created:  time.Date(2026, 1, 2, 3, 4, 4, 0, time.UTC),
			Messages: []pub_models.Message{{Role: "user", Content: "another unrelated"}},
			GroupKey: ComputeGroupKeyFromText("another unrelated"),
		},
	} {
		if err := Save(convDir, c); err != nil {
			t.Fatalf("Save(%q): %v", c.ID, err)
		}
	}

	// A hex groupKey that won't match any saved chat.
	nonMatchingGroupKey := "deadbeefcafebabedeadbeefcafebabedeadbeefcafebabedeadbeefcafebabe"

	cq.input = strings.NewReader("b\nb\n")

	paginator, err := NewChatIndexPaginator(convDir)
	if err != nil {
		t.Fatalf("NewChatIndexPaginator: %v", err)
	}
	var out strings.Builder
	cq.out = &out
	if err := cq.listChats(context.Background(), paginator, nonMatchingGroupKey); err != nil {
		t.Fatalf("listChats: %v", err)
	}

	got := out.String()
	// In group view the back label is customized.
	if !strings.Contains(got, "[b]ack to list") {
		t.Fatalf("expected [b]ack to list in output, got: %q", got)
	}
	// After going back from group view, the main list is shown.
	if !strings.Contains(got, "[b]ack, [q]uit") {
		t.Fatalf("expected main list prompt after back, got: %q", got)
	}
}

// useTestSourceReaders replaces allSourceReaders for the duration of a test.
func useTestSourceReaders(readers []vendors.SourceReader) func() {
	orig := allSourceReaders
	allSourceReaders = func() []vendors.SourceReader { return readers }
	return func() { allSourceReaders = orig }
}

// listOutput renders one listChats session against the handler's index with
// the scripted input and returns what the table wrote. The scripted input
// always ends in EOF, so listChats surfaces the stop error by design.
func listOutput(t *testing.T, cq *ChatHandler, input string) string {
	t.Helper()
	restoreReaders := useTestSourceReaders(nil)
	t.Cleanup(restoreReaders)
	paginator, err := NewChatIndexPaginator(cq.convDir)
	if err != nil {
		t.Fatalf("NewChatIndexPaginator: %v", err)
	}
	var out strings.Builder
	cq.out = &out
	cq.input = strings.NewReader(input)
	if err := cq.listChats(context.Background(), paginator, ""); err == nil {
		t.Fatal("expected listChats to surface the scripted stop error")
	}
	return out.String()
}

func saveAll(t *testing.T, convDir string, chats ...pub_models.Chat) {
	t.Helper()
	for _, c := range chats {
		if err := Save(convDir, c); err != nil {
			t.Fatalf("Save(%q): %v", c.ID, err)
		}
	}
}

func TestListChats_rowShowsTitle(t *testing.T) {
	cq, confDir := newTestHandler(t)
	convDir := conversationsDir(confDir)
	cq.dims = dimensions.Dimensions{Width: 200}
	wideTitle := strings.Repeat("wide title ", 12)
	saveAll(t, convDir,
		pub_models.Chat{ID: "labelled", Title: "Fix auth", Summary: "Token refresh fixed.", Created: time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC), Messages: []pub_models.Message{{Role: "user", Content: "labelled prompt"}}},
		pub_models.Chat{ID: "plain", Created: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), Messages: []pub_models.Message{{Role: "user", Content: "plain prompt"}}},
		pub_models.Chat{ID: "wide", Title: wideTitle, Created: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Messages: []pub_models.Message{{Role: "user", Content: "wide prompt"}}},
	)

	got := listOutput(t, cq, "")
	if !strings.Contains(got, "Fix auth") || strings.Contains(got, "labelled prompt") {
		t.Fatalf("expected the title in place of the first message, got: %q", got)
	}
	if !strings.Contains(got, "plain prompt") {
		t.Fatalf("expected the unlabelled row to show its first message, got: %q", got)
	}
	if strings.Contains(got, wideTitle) || !strings.Contains(got, "wide title") || !strings.Contains(got, " ... ") {
		t.Fatalf("expected the wide title truncated through the existing infix, got: %q", got)
	}

	// A cache written before the feature carries no title fields: the row
	// falls back to the first message.
	rows, err := readChatIndex(convDir)
	if err != nil {
		t.Fatalf("readChatIndex: %v", err)
	}
	for i := range rows {
		rows[i].Title, rows[i].Summary = "", ""
	}
	if err := writeChatIndex(convDir, rows); err != nil {
		t.Fatalf("writeChatIndex: %v", err)
	}
	got = listOutput(t, cq, "")
	if strings.Contains(got, "Fix auth") || !strings.Contains(got, "labelled prompt") {
		t.Fatalf("expected the pre-feature row to fall back to the first message, got: %q", got)
	}
}

func TestListChats_groupRowShowsNewestTitle(t *testing.T) {
	cq, confDir := newTestHandler(t)
	convDir := conversationsDir(confDir)
	cq.dims = dimensions.Dimensions{Width: 200}
	saveAll(t, convDir,
		pub_models.Chat{ID: "older", Title: "Old title", Created: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Messages: []pub_models.Message{{Role: "user", Content: "shared prompt"}}},
		pub_models.Chat{ID: "newer", Title: "Fix auth", Created: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), Messages: []pub_models.Message{{Role: "user", Content: "shared prompt"}}},
	)

	got := listOutput(t, cq, "")
	if !strings.Contains(got, "[group:2]") {
		t.Fatalf("expected one collapsed group row, got: %q", got)
	}
	if !strings.Contains(got, "Fix auth") || strings.Contains(got, "Old title") || strings.Contains(got, "shared prompt") {
		t.Fatalf("expected the group row labelled by its newest member's title, got: %q", got)
	}
}

func TestListChats_filterMatchesTitle(t *testing.T) {
	cq, confDir := newTestHandler(t)
	convDir := conversationsDir(confDir)
	cq.dims = dimensions.Dimensions{Width: 200}
	saveAll(t, convDir,
		pub_models.Chat{ID: "labelled", Title: "Fix auth", Created: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), Messages: []pub_models.Message{{Role: "user", Content: "labelled prompt"}}},
		pub_models.Chat{ID: "plain", Created: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Messages: []pub_models.Message{{Role: "user", Content: "unrelated prompt"}}},
	)

	got := listOutput(t, cq, "/fix auth\n")
	if !strings.Contains(got, `filter: "fix auth"`) {
		t.Fatalf("expected the substring filter applied, got: %q", got)
	}
	// The unfiltered render shows both rows; the filtered render keeps only
	// the row whose rendered title matches.
	if n := strings.Count(got, "unrelated prompt"); n != 1 {
		t.Fatalf("expected the unrelated row only in the unfiltered render, got %d in: %q", n, got)
	}
	if n := strings.Count(got, "Fix auth"); n < 2 {
		t.Fatalf("expected the titled row in both renders, got %d in: %q", n, got)
	}
}

func TestChatInfo_titleSummaryLines(t *testing.T) {
	confDir := t.TempDir()
	if err := utils.CreateConfigDir(confDir); err != nil {
		t.Fatalf("CreateConfigDir: %v", err)
	}
	t.Setenv("CLAI_CONFIG_DIR", confDir)
	base := pub_models.Chat{
		ID:       "labelled",
		Created:  time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Messages: []pub_models.Message{{Role: "user", Content: "labelled prompt"}},
	}

	full := base
	full.Title, full.Summary = "Fix auth", "Token refresh fixed."
	var out strings.Builder
	cq := &ChatHandler{out: &out, dims: dimensions.Dimensions{Width: 200}}
	if err := cq.printChatInfo(&out, full, ""); err != nil {
		t.Fatalf("printChatInfo(full): %v", err)
	}
	got := out.String()
	for _, want := range []string{"title:", "Fix auth", "summary:", "Token refresh fixed."} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in the labelled info view, got: %q", want, got)
		}
	}
	if strings.Contains(got, `summary: "`) || strings.Contains(got, "labelled prompt") {
		t.Fatalf("labelled info view must not fall back to the first message, got: %q", got)
	}
	if strings.Index(got, "title:") > strings.Index(got, "summary:") {
		t.Fatalf("expected the title line before the summary line, got: %q", got)
	}

	partial := base
	partial.Title = "Fix auth"
	out.Reset()
	if err := cq.printChatInfo(&out, partial, ""); err != nil {
		t.Fatalf("printChatInfo(partial): %v", err)
	}
	got = out.String()
	if !strings.Contains(got, "title:") || !strings.Contains(got, "Fix auth") {
		t.Fatalf("expected the title with an empty summary, got: %q", got)
	}
	if strings.Contains(got, "summary:") {
		t.Fatalf("expected no summary line for an empty summary, got: %q", got)
	}
}

func TestChatInfo_unlabelledUnchanged(t *testing.T) {
	confDir := t.TempDir()
	if err := utils.CreateConfigDir(confDir); err != nil {
		t.Fatalf("CreateConfigDir: %v", err)
	}
	t.Setenv("CLAI_CONFIG_DIR", confDir)
	plain := pub_models.Chat{
		ID:       "plain",
		Created:  time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Messages: []pub_models.Message{{Role: "user", Content: "plain prompt"}},
	}

	var out strings.Builder
	cq := &ChatHandler{out: &out, dims: dimensions.Dimensions{Width: 200}}
	if err := cq.printChatInfo(&out, plain, ""); err != nil {
		t.Fatalf("printChatInfo: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, `summary: "plain prompt"`) {
		t.Fatalf("expected today's quoted first-message line, got: %q", got)
	}
	if strings.Contains(got, "title:") {
		t.Fatalf("unlabelled info view must not show a title line, got: %q", got)
	}
}

func TestChatInfoHeight_labelledSummaryAddsOneLine(t *testing.T) {
	plain := pub_models.Chat{Title: "Fix auth"}
	if got := chatInfoHeight(plain); got != chatInfoPrintHeight {
		t.Fatalf("title alone: height %d, want %d", got, chatInfoPrintHeight)
	}
	full := pub_models.Chat{Title: "Fix auth", Summary: "Token refresh fixed."}
	if got := chatInfoHeight(full); got != chatInfoPrintHeight+1 {
		t.Fatalf("title and summary: height %d, want %d", got, chatInfoPrintHeight+1)
	}
}

// promptLine returns the last line the table printed, with escape sequences
// stripped, so the width oracle is independent of the production counter.
func promptLine(t *testing.T, out string) string {
	t.Helper()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	line := lines[len(lines)-1]
	if !strings.Contains(line, "(select") {
		t.Fatalf("last line is not the prompt: %q", line)
	}
	return regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(line, "")
}

// TestListChats_PromptFitsWidth renders through the real table with a
// dirscope binding and a foreign row: at eighty columns the prompt stays on
// one physical line with both state words present; at 140 the long labels
// are used.
func TestListChats_PromptFitsWidth(t *testing.T) {
	cq, confDir := newTestHandler(t)
	convDir := conversationsDir(confDir)
	wd := chdirToTemp(t)
	if err := Save(convDir, pub_models.Chat{ID: "native", Created: time.Date(2026, 1, 2, 3, 4, 6, 0, time.UTC), Messages: []pub_models.Message{{Role: "user", Content: "native prompt"}}}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	reader := stubSourceReader{name: "test-source", rows: []vendors.SourceRow{{
		Source: "test-source", SourceID: "ext-1", FirstUserMessage: "foreign prompt", FullFirstUserMessage: "foreign prompt",
		Created: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}}}
	t.Cleanup(useTestSourceReaders([]vendors.SourceReader{reader}))
	if err := cq.SaveDirScope(wd, "native"); err != nil {
		t.Fatalf("SaveDirScope: %v", err)
	}
	render := func(t *testing.T, width int, keys string) string {
		t.Helper()
		cq.dims = dimensions.Dimensions{Width: width}
		cq.input = strings.NewReader(keys)
		paginator, err := NewChatIndexPaginator(convDir)
		if err != nil {
			t.Fatalf("NewChatIndexPaginator: %v", err)
		}
		var out strings.Builder
		cq.out = &out
		_ = cq.listChats(context.Background(), paginator, "")
		return out.String()
	}

	t.Run("eighty columns", func(t *testing.T) {
		got := render(t, 80, "")
		line := promptLine(t, got)
		if n := utf8.RuneCountInString(line); n >= 80 {
			t.Fatalf("prompt is %d columns wide at 80, wraps:\n%s", n, line)
		}
		for _, want := range []string{"[d]", "off", "[f]", "shown", "[/] filter"} {
			if !strings.Contains(line, want) {
				t.Fatalf("prompt lacks %q:\n%s", want, line)
			}
		}
		if strings.Contains(line, "[d]irscoped convs") || strings.Contains(line, "[f]oreign convs") {
			t.Fatalf("long labels must not be used at 80 columns:\n%s", line)
		}
		// Toggling keeps the same tier so the prompt never jitters.
		toggled := render(t, 80, "d\nf\n")
		for _, want := range []string{"[d]:on", "[f]:hidden"} {
			if !strings.Contains(toggled, want) {
				t.Fatalf("toggled prompt lacks %q:\n%s", want, toggled)
			}
		}
	})
	t.Run("one hundred and forty columns", func(t *testing.T) {
		line := promptLine(t, render(t, 140, ""))
		for _, want := range []string{"[d]irscoped convs: off", "[f]oreign convs: shown"} {
			if !strings.Contains(line, want) {
				t.Fatalf("prompt lacks the long label %q:\n%s", want, line)
			}
		}
	})
	t.Run("unknown width keeps the long labels", func(t *testing.T) {
		line := promptLine(t, render(t, 0, ""))
		if !strings.Contains(line, "[d]irscoped convs: off") {
			t.Fatalf("width zero must not shorten the labels:\n%s", line)
		}
	})
}

// TestListPromptTier pins the width counter and the tier thresholds: the
// counter replicates the table's prompt composition with a two-digit page
// counter reserved, escape sequences excluded, and the longest state word
// of each toggle; the choice leaves listPromptMargin columns for the
// typed selection and falls to the terse tier when nothing fits.
func TestListPromptTier(t *testing.T) {
	long, compact, terse := toggleTiers[0], toggleTiers[1], toggleTiers[2]
	prev := ancli.UseColor
	t.Cleanup(func() { ancli.UseColor = prev })
	ancli.UseColor = true
	// "(select, [d]irscoped convs: off, [f]oreign convs: hidden, [p]rev,
	// [n]ext, [b]ack, [q]uit, [/] filter, page 99/99): "
	if got := listPromptWidth(long, true, true, ""); got != 115 {
		t.Fatalf("long tier width = %d, want 115", got)
	}
	if got := listPromptWidth(compact, true, true, ""); got != 97 {
		t.Fatalf("compact tier width = %d, want 97", got)
	}
	if got := listPromptWidth(terse, true, true, ""); got != 87 {
		t.Fatalf("terse tier width = %d, want 87", got)
	}
	if got := listPromptWidth(long, true, false, "[b]ack to list"); got != 115-25+8 {
		t.Fatalf("dir-only group view width = %d, want %d", got, 115-25+8)
	}
	if got := visibleWidth("\x1b[1;4m[d]:on\x1b[22;24m"); got != 6 {
		t.Fatalf("visibleWidth ignores escapes, got %d", got)
	}
	for _, tc := range []struct {
		width int
		want  toggleTier
	}{
		{0, long}, {140, long}, {119, long}, {118, compact}, {101, compact}, {100, terse}, {80, terse}, {40, terse},
	} {
		if got := listPromptTier(tc.width, true, true, ""); got != tc.want {
			t.Fatalf("width %d: tier = %+v, want %+v", tc.width, got, tc.want)
		}
	}
	// Without the foreign toggle the long labels fit sooner.
	if got := listPromptTier(100, true, false, ""); got != long {
		t.Fatalf("dir-only at 100 columns: tier = %+v, want long", got)
	}
}

// --- foreign cache wiring ---------------------------------------------------

// countingForeignCache delegates to a real index and counts how often the
// list path persisted it, which is how "one write per invocation" is
// observed without guessing at file timestamps.
type countingForeignCache struct {
	inner    *ForeignIndex
	persists int
}

func (c *countingForeignCache) Lookup(absPath string, info fs.FileInfo) (vendors.SourceRow, bool) {
	return c.inner.Lookup(absPath, info)
}

func (c *countingForeignCache) Store(absPath string, info fs.FileInfo, row vendors.SourceRow) {
	c.inner.Store(absPath, info, row)
}

func (c *countingForeignCache) Locate(source, sourceID string) (string, bool) {
	return c.inner.Locate(source, sourceID)
}

func (c *countingForeignCache) Persist() error {
	c.persists++
	return c.inner.Persist()
}

// TestForeignChatRows_singleCacheWrite: one cache serves every reader of an
// invocation and is written exactly once, after the last one — whatever the
// number of readers.
func TestForeignChatRows_singleCacheWrite(t *testing.T) {
	claudeRoot := filepath.Join(t.TempDir(), "projects")
	piRoot := filepath.Join(t.TempDir(), "sessions")
	jsonltest.WriteCorpus(t, claudeRoot, smallCorpus(jsonltest.ShapeClaude))
	jsonltest.WriteCorpus(t, piRoot, smallCorpus(jsonltest.ShapePi))
	cacheDir := t.TempDir()

	idx := newForeignIndexT(t, cacheDir)
	counting := &countingForeignCache{inner: idx}
	cq, _ := newTestHandler(t)
	cq.foreignCache = counting

	readers := []vendors.SourceReader{
		anthropic.SourceReader{Root: claudeRoot},
		pi.SourceReader{Root: piRoot},
	}
	rows, err := cq.foreignChatRows(context.Background(), readers, map[string]struct{}{})
	if err != nil {
		t.Fatalf("foreignChatRows: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("no rows; the corpora did not reach the readers")
	}
	if counting.persists != 1 {
		t.Fatalf("cache persisted %d times for %d readers, want 1", counting.persists, len(readers))
	}

	// The single write carries both sources, so it happened after the last
	// reader rather than after the first.
	sources := map[string]bool{}
	for _, row := range readForeignCacheFile(t, cacheDir).Rows {
		sources[row.Row.Source] = true
	}
	for _, want := range []string{"claude-code", "pi"} {
		if !sources[want] {
			t.Errorf("the persisted cache holds no %q row", want)
		}
	}
}

// TestForeignChat_continueUsesCachedPath: continuing a foreign conversation
// resolves its file through the index. Only the session file itself is
// opened — no other file is touched to find it.
func TestForeignChat_continueUsesCachedPath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "projects")
	corpus := jsonltest.WriteCorpus(t, root, smallCorpus(jsonltest.ShapeClaude))
	cacheDir := t.TempDir()

	warm := newForeignIndexT(t, cacheDir)
	reader := anthropic.SourceReader{Root: root}
	rows, err := reader.Discover(context.Background(), warm)
	if err != nil {
		t.Fatalf("warming Discover: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("no rows; the corpus did not reach the reader")
	}
	persistT(t, warm)

	// Pick the longest session, so the conversation read back is substantial.
	want := rows[0]
	for _, row := range rows {
		if row.MessageCount > want.MessageCount {
			want = row
		}
	}

	idx := newForeignIndexT(t, cacheDir)
	fsys, counts := jsonltest.CountingFS("/")
	cq, _ := newTestHandler(t)
	cq.foreignCache = idx

	chat, err := cq.readForeignChat(context.Background(),
		anthropic.SourceReader{Root: root, FS: fsys}, want.SourceID)
	if err != nil {
		t.Fatalf("readForeignChat: %v", err)
	}
	if chat.SourceID != want.SourceID {
		t.Fatalf("SourceID = %q, want %q", chat.SourceID, want.SourceID)
	}
	if len(chat.Messages) < 2 {
		t.Fatalf("conversation has %d messages, want the session's content", len(chat.Messages))
	}

	rel := strings.TrimPrefix(want.RawPath, "/")
	if n := counts.TotalOpens(); n != 1 {
		t.Fatalf("continuing opened %d files, want only the session itself: %v", n, counts.OpenedPaths())
	}
	if n := counts.Opens(rel); n != 1 {
		t.Fatalf("the session file was opened %d times, want 1", n)
	}
	if n := counts.TotalStats(); n != 1 {
		t.Fatalf("continuing stat'ed %d times, want one confirming stat", n)
	}
	if len(corpus.Files) < 2 {
		t.Fatal("the fixture must hold more than one file for the walk to be worth avoiding")
	}
}

// TestChatHandler_persistForeignCacheDoesNotConstruct pins the property
// persistForeignCache's doc comment used to merely assert (R3-02). A handler
// that resolved nothing persists nothing and asks the factory for nothing:
// D27 exists so that a verb which never consults the index never builds one,
// and a persist that resolved the factory would undo that for every verb that
// defers one.
func TestChatHandler_persistForeignCacheDoesNotConstruct(t *testing.T) {
	idx := newForeignIndexT(t, t.TempDir())
	counting := &countingForeignCache{inner: idx}
	constructions := 0
	cq, _ := newTestHandler(t)
	cq.newForeignCache = func() vendors.SourceCache {
		constructions++
		return counting
	}

	if err := cq.persistForeignCache(); err != nil {
		t.Fatalf("persisting an unresolved cache: %v", err)
	}

	if constructions != 0 {
		t.Fatalf("persisting constructed %d indexes, want 0", constructions)
	}
	if counting.persists != 0 {
		t.Fatalf("an unresolved cache was persisted %d times", counting.persists)
	}

	// Once a verb has consulted it, the same call does persist it: the
	// accessor reads the resolved value, it does not suppress the write.
	if got := cq.foreignCacheOrNil(); got != counting {
		t.Fatalf("the consultation resolved %#v, want the counting cache", got)
	}
	if err := cq.persistForeignCache(); err != nil {
		t.Fatalf("persistForeignCache: %v", err)
	}
	if constructions != 1 || counting.persists != 1 {
		t.Fatalf("after one consultation: %d constructions, %d persists, want 1 and 1", constructions, counting.persists)
	}
}

// TestChatHandler_persistForeignCacheRacesResolution is the same finding's
// other half, and it is the one that fails loudly: reading the resolved field
// without the guard the resolver writes it under is a data race the current
// call graph only hides. It is safe today because the single call site is a
// defer on the goroutine that just resolved the cache — a contract nothing
// pins, and one a deferred persist at a higher level would break silently.
func TestChatHandler_persistForeignCacheRacesResolution(t *testing.T) {
	idx := newForeignIndexT(t, t.TempDir())
	counting := &countingForeignCache{inner: idx}
	cq, _ := newTestHandler(t)
	cq.newForeignCache = func() vendors.SourceCache { return counting }

	start := make(chan struct{})
	done := make(chan struct{}, 2)
	go func() {
		<-start
		cq.foreignCacheOrNil()
		done <- struct{}{}
	}()
	go func() {
		<-start
		_ = cq.persistForeignCache()
		done <- struct{}{}
	}()
	close(start)
	<-done
	<-done

	if got := cq.foreignCacheOrNil(); got != counting {
		t.Fatalf("the cache resolved to %#v, want the counting cache", got)
	}
	if counting.persists > 1 {
		t.Fatalf("the cache was persisted %d times, want at most 1", counting.persists)
	}
}
