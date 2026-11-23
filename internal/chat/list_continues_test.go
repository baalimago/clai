package chat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/cloud"
	cloudsync "github.com/baalimago/clai/internal/cloud/sync"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/dimensions"
)

var listT0 = time.Date(2026, 1, 2, 3, 4, 0, 0, time.UTC)

func listChat(id, msg string, age int) pub_models.Chat {
	return pub_models.Chat{
		ID:       id,
		Created:  listT0.Add(-time.Duration(age) * time.Minute),
		Messages: []pub_models.Message{{Role: "user", Content: msg}},
	}
}

func listFixture(t *testing.T, chats ...pub_models.Chat) (*ChatHandler, string) {
	t.Helper()
	cq, confDir := newTestHandler(t)
	cq.dims = dimensions.Dimensions{Width: 200}
	chdirToTemp(t)
	saveAll(t, conversationsDir(confDir), chats...)
	return cq, confDir
}

func setRecord(t *testing.T, confDir, id string) {
	t.Helper()
	if err := SavePreviousQuery(context.Background(), confDir, id, listT0); err != nil {
		t.Fatalf("SavePreviousQuery: %v", err)
	}
}

func writeRecord(t *testing.T, confDir, body string) {
	t.Helper()
	if err := os.MkdirAll(previousQueryStateDir(confDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(PreviousQueryPath(confDir), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

func TestChatList_continuesLine_ownRow(t *testing.T) {
	cq, confDir := listFixture(t, listChat("a", "first", 0), listChat("b", "second", 1), listChat("c", "third", 2))
	setRecord(t, confDir, "b")
	got := listOutput(t, cq, "")
	lines := strings.Split(got, "\n")
	if lines[0] != "-re continues: 1 · second" {
		t.Fatalf("first line = %q", lines[0])
	}
	if !strings.Contains(lines[1], "Index") {
		t.Fatalf("header must follow the line, got %q", lines[1])
	}
	if strings.Count(got, "-re continues") != 1 {
		t.Fatalf("line must print once, got %q", got)
	}
}

func TestChatList_continuesLine_titleWins(t *testing.T) {
	c := listChat("a", "first", 0)
	c.Title = "A title"
	cq, confDir := listFixture(t, c)
	setRecord(t, confDir, "a")
	if got := firstLine(listOutput(t, cq, "")); got != "-re continues: 0 · A title" {
		t.Fatalf("line = %q", got)
	}
}

func TestChatList_continuesLine_inGroup(t *testing.T) {
	cq, confDir := listFixture(t, listChat("new", "same question", 0), listChat("old", "same question", 5), listChat("x", "other", 9))
	setRecord(t, confDir, "old")
	got := listOutput(t, cq, "")
	if firstLine(got) != "-re continues: 0 [group] · same question" {
		t.Fatalf("line = %q", firstLine(got))
	}
}

func TestChatList_continuesLine_notInView(t *testing.T) {
	bound := listChat("bound", "bound prompt", 0)
	other := listChat("other", "other prompt", 1)
	cq, confDir := listFixture(t, bound, other)
	wd, _ := os.Getwd()
	if err := cq.SaveDirScope(context.Background(), wd, "bound"); err != nil {
		t.Fatal(err)
	}
	setRecord(t, confDir, "other")
	got := listOutput(t, cq, "d\n")
	want := "-re continues: other · other prompt (not in this view)"
	if n := strings.Count(got, want); n != 1 {
		t.Fatalf("want %q once (the filtered render), got %d in %q", want, n, got)
	}
	if n := strings.Count(got, "-re continues: 1 · other prompt"); n != 1 {
		t.Fatalf("unfiltered render must name index 1, got %d in %q", n, got)
	}
}

func TestChatList_noLine(t *testing.T) {
	for _, tc := range []struct {
		name string
		prep func(t *testing.T, confDir string)
	}{
		{"no record", func(*testing.T, string) {}},
		{"deleted chat", func(t *testing.T, confDir string) {
			setRecord(t, confDir, "gone")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cq, confDir := listFixture(t, listChat("a", "first", 0))
			baseline := listOutput(t, cq, "")
			tc.prep(t, confDir)
			got := listOutput(t, cq, "")
			if got != baseline || strings.Contains(got, "-re continues") {
				t.Fatalf("output changed:\n%q\nvs\n%q", got, baseline)
			}
		})
	}
}

func TestReadPreviousQueryState_goneAndInvalid(t *testing.T) {
	for _, tc := range []struct {
		name       string
		record     string
		wantKind   previousQueryKind
		wantReason string
	}{
		{"wrapped gone", `{"version":1,"chat_id":"gone","updated":"2026-01-02T03:04:00Z"}`, prevNone, ""},
		{"invalid chat id", `{"version":1,"chat_id":"a/b","updated":"2026-01-02T03:04:00Z"}`, prevUnreadable, "invalid chat id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, confDir := listFixture(t, listChat("a", "first", 0))
			writeRecord(t, confDir, tc.record)
			_, err := LoadPreviousQuery(context.Background(), confDir)
			if tc.wantKind == prevNone && (!errors.Is(err, ErrPreviousQueryGone) || err == ErrPreviousQueryGone) {
				t.Fatalf("err = %v, want a wrapped ErrPreviousQueryGone", err)
			}
			got := readPreviousQueryState(context.Background(), confDir)
			if got.Kind != tc.wantKind || !strings.Contains(got.Reason, tc.wantReason) {
				t.Fatalf("state = %+v", got)
			}
		})
	}
}

func TestChatList_unreadableRecord(t *testing.T) {
	for _, tc := range []struct {
		name string
		prep func(t *testing.T, confDir string)
	}{
		{"invalid json", func(t *testing.T, confDir string) { writeRecord(t, confDir, "{not json") }},
		{"other version", func(t *testing.T, confDir string) {
			writeRecord(t, confDir, `{"version":99,"chat_id":"a","updated":"2026-01-02T03:04:00Z"}`)
		}},
		{"invalid chat id", func(t *testing.T, confDir string) {
			writeRecord(t, confDir, `{"version":1,"chat_id":"../escape","updated":"2026-01-02T03:04:00Z"}`)
		}},
		{"conversation not json", func(t *testing.T, confDir string) {
			writeRecord(t, confDir, `{"version":1,"chat_id":"bad","updated":"2026-01-02T03:04:00Z"}`)
			if err := os.WriteFile(filepath.Join(conversationsDir(confDir), "bad.json"), []byte("nope"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cq, confDir := listFixture(t, listChat("a", "first", 0))
			tc.prep(t, confDir)
			before, _ := os.ReadFile(PreviousQueryPath(confDir))
			got := listOutput(t, cq, "")
			line := firstLine(got)
			if !strings.HasPrefix(line, "-re continues: unreadable record (") || !strings.HasSuffix(line, ")") {
				t.Fatalf("line = %q", line)
			}
			if !strings.Contains(got, "first") {
				t.Fatalf("table must still draw: %q", got)
			}
			after, _ := os.ReadFile(PreviousQueryPath(confDir))
			if string(before) != string(after) {
				t.Fatal("record was rewritten")
			}
		})
	}
}

func TestChatList_lineOnEveryRender(t *testing.T) {
	cq, confDir := listFixture(t, listChat("a", "msg a", 0), listChat("b", "msg b", 1), listChat("c", "msg c", 2))
	wd, _ := os.Getwd()
	if err := cq.SaveDirScope(context.Background(), wd, "a"); err != nil {
		t.Fatal(err)
	}
	setRecord(t, confDir, "c")
	got := listOutput(t, cq, "d\nd\n")
	parts := strings.Split(got, "Index")
	if len(parts) != 4 {
		t.Fatalf("want 3 renders, got %d in %q", len(parts)-1, got)
	}
	for _, part := range parts[:3] {
		if !strings.Contains(part, "-re continues: ") {
			t.Fatalf("a render lacks the line: %q", part)
		}
	}
	if n := strings.Count(got, "-re continues"); n != 3 {
		t.Fatalf("lines = %d", n)
	}
}

func TestChatList_writesNothing(t *testing.T) {
	cq, confDir := listFixture(t, listChat("a", "first", 0), listChat("b", "second", 1))
	setRecord(t, confDir, "a")
	// the first read builds the index, so it exists before the tree is frozen
	listOutput(t, cq, "")
	snap := func() map[string]string {
		out := map[string]string{}
		_ = filepath.WalkDir(confDir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			info, _ := d.Info()
			b := []byte{}
			if !d.IsDir() {
				b, _ = os.ReadFile(p)
			}
			out[p] = info.ModTime().String() + string(b)
			return nil
		})
		return out
	}
	old := utils.NoCreateConfig
	utils.NoCreateConfig = true
	t.Cleanup(func() { utils.NoCreateConfig = old })
	if err := os.RemoveAll(filepath.Join(confDir, "conversations", "state", "tmp")); err != nil {
		t.Fatal(err)
	}
	before := snap()
	freezeTree(t, confDir)
	_ = listOutput(t, cq, "")
	after := snap()
	if len(before) != len(after) {
		t.Fatalf("tree changed: %d -> %d entries", len(before), len(after))
	}
	for p, v := range before {
		if after[p] != v {
			t.Fatalf("%s changed", p)
		}
	}
}

func freezeTree(t *testing.T, root string) {
	t.Helper()
	var dirs []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			dirs = append(dirs, p)
			return nil
		}
		return os.Chmod(p, 0o444)
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range slices.Backward(dirs) {
		if err := os.Chmod(d, 0o555); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, d := range dirs {
			_ = os.Chmod(d, 0o755)
		}
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				_ = os.Chmod(p, 0o644)
			}
			return nil
		})
	})
}

func TestClearPreviousQueryLine(t *testing.T) {
	const clearLine, lineUp = "\r\x1b[K", "\x1b[1A"
	shown := []chatListRow{{Kind: chatRowNative, ChatID: "a", Title: "first"}}
	named := previousQueryState{Kind: prevNamed, Chat: pub_models.Chat{ID: "a", Title: "first"}}
	line := previousQueryLine(named, shown, false)
	for _, tc := range []struct {
		name      string
		width     int
		state     previousQueryState
		input     io.Reader
		wantRows  int
		wantNoOut bool
	}{
		{"one row line", 200, named, nil, 1, false},
		{"wrapped line", 10, named, nil, utils.TerminalRows(line, 10), false},
		{"no line", 200, previousQueryState{Kind: prevNone}, nil, 0, true},
		{"scripted input", 200, named, strings.NewReader(""), 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cq, _ := listFixture(t)
			var out strings.Builder
			cq.out, cq.input, cq.dims = &out, tc.input, dimensions.Dimensions{Width: tc.width}
			if err := cq.clearPreviousQueryLine(tc.state, shown, false); err != nil {
				t.Fatalf("clear: %v", err)
			}
			if tc.wantNoOut {
				if out.Len() != 0 {
					t.Fatalf("wrote %q", out.String())
				}
				return
			}
			if tc.name == "wrapped line" && tc.wantRows < 2 {
				t.Fatalf("fixture must wrap, rows = %d", tc.wantRows)
			}
			if got := strings.Count(out.String(), lineUp); got != tc.wantRows {
				t.Fatalf("cursor-ups = %d, want the line's %d rows", got, tc.wantRows)
			}
			if got := strings.Count(out.String(), clearLine); got != tc.wantRows+1 {
				t.Fatalf("line clears = %d, want the line's rows plus the header: %d", got, tc.wantRows+1)
			}
		})
	}
}

func TestChatList_noStateDirCreated(t *testing.T) {
	cq, confDir := listFixture(t, listChat("a", "first", 0))
	_ = listOutput(t, cq, "")
	if _, err := os.Stat(previousQueryStateDir(confDir)); !os.IsNotExist(err) {
		t.Fatalf("state dir exists: %v", err)
	}
}

func TestChatList_indexUnreadable(t *testing.T) {
	cq, confDir := listFixture(t, listChat("a", "first", 0))
	setRecord(t, confDir, "a")
	idx := chatIndexPath(conversationsDir(confDir))
	if err := os.Remove(idx); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(idx, 0o755); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	cq.out, cq.input = &out, strings.NewReader("")
	err := cq.handleListCmd(context.Background())
	if err == nil || !strings.Contains(err.Error(), "failed to read chat index") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(out.String(), "-re continues") {
		t.Fatalf("line drawn before error: %q", out.String())
	}
}

func TestChatList_legacyMirrorNotMigrated(t *testing.T) {
	cq, confDir := listFixture(t, listChat("a", "first", 0))
	mirror := filepath.Join(conversationsDir(confDir), legacyGlobalScopeFile)
	b := `{"id":"globalScope","created":"2026-01-02T03:04:00Z","messages":[{"role":"user","content":"first"}]}`
	if err := os.WriteFile(mirror, []byte(b), 0o644); err != nil {
		t.Fatal(err)
	}
	got := listOutput(t, cq, "")
	if strings.Contains(got, "-re continues") {
		t.Fatalf("line from a legacy mirror: %q", got)
	}
	if _, err := os.Stat(mirror); err != nil {
		t.Fatalf("mirror removed: %v", err)
	}
	if _, err := os.Stat(PreviousQueryPath(confDir)); !os.IsNotExist(err) {
		t.Fatalf("record created: %v", err)
	}
}

func TestPreviousQueryLine(t *testing.T) {
	named := previousQueryState{Kind: prevNamed, Chat: listChat("z", "zeta\nline", 0)}
	rows := []chatListRow{
		{Kind: chatRowNative, ChatID: "a", FirstUserMessage: "alpha"},
		{Kind: chatRowGroup, GroupMemberIDs: []string{"g1", "z"}},
		{Kind: chatRowForeign, FirstUserMessage: "foreign"},
	}
	for _, tc := range []struct {
		name   string
		state  previousQueryState
		shown  []chatListRow
		inView bool
		want   string
	}{
		{"none", previousQueryState{Kind: prevNone}, rows, false, ""},
		{"own row", previousQueryState{Kind: prevNamed, Chat: pub_models.Chat{ID: "a"}}, rows, false, "-re continues: 0 · alpha"},
		{"group member", named, rows, false, "-re continues: 1 [group] · zeta line"},
		{"hidden", named, rows[:1], false, "-re continues: z · zeta line (not in this view)"},
		{"group view skips groups", named, rows, true, "-re continues: z · zeta line (not in this view)"},
		{"unreadable multi-line", previousQueryState{Kind: prevUnreadable, Reason: utils.SanitizeTerminalRow("bad\x1b[31m\nreason")}, nil, false, "-re continues: unreadable record (bad reason)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := previousQueryLine(tc.state, tc.shown, tc.inView)
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
			if strings.ContainsAny(got, "\n\x1b") {
				t.Fatalf("control characters in %q", got)
			}
		})
	}
}

func TestReadPreviousQueryState(t *testing.T) {
	ctx := context.Background()
	cq, confDir := listFixture(t, listChat("a", "first", 0))
	_ = cq
	if got := readPreviousQueryState(ctx, confDir); got.Kind != prevNone {
		t.Fatalf("no record: %v", got.Kind)
	}
	setRecord(t, confDir, "a")
	if got := readPreviousQueryState(ctx, confDir); got.Kind != prevNamed || got.Chat.ID != "a" {
		t.Fatalf("named: %+v", got)
	}
	setRecord(t, confDir, "gone")
	if got := readPreviousQueryState(ctx, confDir); got.Kind != prevNone {
		t.Fatalf("gone: %v", got.Kind)
	}
	writeRecord(t, confDir, "{")
	if got := readPreviousQueryState(ctx, confDir); got.Kind != prevUnreadable || got.Reason == "" {
		t.Fatalf("decode error: %+v", got)
	}
}

func TestChatIndexRowFromChat_ownSpend(t *testing.T) {
	t1 := listT0
	parent := pub_models.Chat{ID: "p", Created: t1, Queries: []pub_models.QueryCost{ownQuery(t1, 10, 0.1), ownQuery(t1.Add(time.Hour), 20, 0.2)}}
	fork := parent
	fork.ID, fork.Created, fork.Source, fork.SourceID = "f", t1.Add(30*time.Minute), "clai", "p"
	pr, fr := chatIndexRowFromChat(parent), chatIndexRowFromChat(fork)
	if pr.TotalTokens != 30 || pr.OwnTotalTokens != 30 {
		t.Fatalf("parent: %+v", pr)
	}
	if fr.TotalTokens != 30 || fr.OwnTotalTokens != 20 || fr.OwnTotalCostUSD != 0.2 {
		t.Fatalf("fork: %+v", fr)
	}
	zero := chatIndexRowFromChat(pub_models.Chat{ID: "z", Queries: []pub_models.QueryCost{ownQuery(t1, 5, 0.5)}})
	if zero.OwnTotalTokens != 5 {
		t.Fatalf("zero created: %+v", zero)
	}
	b, _ := jsonMarshal(chatIndexRowFromChat(pub_models.Chat{ID: "e"}))
	if strings.Contains(string(b), "own_total") {
		t.Fatalf("zero own fields must be omitted: %s", b)
	}
	b, _ = jsonMarshal(fr)
	if !strings.Contains(string(b), `"own_total_tokens":20`) || !strings.Contains(string(b), `"own_total_cost_usd":0.2`) {
		t.Fatalf("json: %s", b)
	}
}

func TestBuildGroupRow_ownSpend(t *testing.T) {
	rows := []chatListRow{
		{Kind: chatRowNative, ChatID: "fb", Created: listT0.Add(3 * time.Hour), GroupKey: "g", TotalTokens: 40, TotalCostUSD: 0.4, OwnTotalTokens: 10, OwnTotalCostUSD: 0.1},
		{Kind: chatRowNative, ChatID: "fa", Created: listT0.Add(2 * time.Hour), GroupKey: "g", TotalTokens: 50, TotalCostUSD: 0.5, OwnTotalTokens: 20, OwnTotalCostUSD: 0.2},
		{Kind: chatRowForeign, Created: listT0.Add(time.Hour), GroupKey: "g", TotalTokens: 999, TotalCostUSD: 9},
		{Kind: chatRowNative, ChatID: "p", Created: listT0, GroupKey: "g", TotalTokens: 30, TotalCostUSD: 0.3, OwnTotalTokens: 30, OwnTotalCostUSD: 0.3},
	}
	out := collapseGroupRows(rows)
	if len(out) != 1 {
		t.Fatalf("collapsed to %d rows", len(out))
	}
	g := out[0]
	if g.TotalTokens != 60 || g.TotalCostUSD < 0.6-1e-9 || g.TotalCostUSD > 0.6+1e-9 {
		t.Fatalf("group total = %d / %v", g.TotalTokens, g.TotalCostUSD)
	}
	if strings.Join(g.GroupMemberIDs, ",") != "fb,fa,p" {
		t.Fatalf("member ids = %v", g.GroupMemberIDs)
	}
}

func TestGroupTotal_cloudLocalForkCountsEachSpendOnce(t *testing.T) {
	r := newCloudRun(t, "continue", "cc:X", tokenClient("tok-a"))
	cloudAt := time.Date(2026, 3, 1, 10, 1, 0, 0, time.UTC)
	localAt := time.Date(2026, 3, 1, 11, 0, 0, 0, time.UTC)
	cloudChat := cloudChatX("hello", "hi there")
	cloudChat.Queries = []pub_models.QueryCost{ownQuery(cloudAt, 100, 1)}
	cloudChat.Queries[0].Model = "cloud-model"
	r.putCloud(t, cloudChat)
	local := cloudChatX("hello", "hi there", "more", "and more")
	local.Queries = []pub_models.QueryCost{cloudChat.Queries[0], ownQuery(localAt, 200, 2)}
	local.Queries[1].Model = "local-model"
	saveAll(t, r.convDir, local)
	engine := &cloudsync.Engine{Client: cloud.NewClient(r.srv.URL(), "tok-a"), ConfDir: r.confDir, Now: func() time.Time { return localAt.Add(time.Hour) }}
	pull, err := engine.PullConversation(context.Background(), "X")
	if err != nil || pull.Outcome != cloudsync.ConversationLocalForked || pull.ForkID == "" {
		t.Fatalf("pull = %+v, %v", pull, err)
	}
	fork, err := r.cq.getByID(context.Background(), pull.ForkID)
	if err != nil || fork.Source != "clai" || fork.SourceID != "X" || !fork.Created.Equal(localAt) {
		t.Fatalf("fork = %+v, %v", fork, err)
	}
	restore := useTestSourceReaders(nil)
	t.Cleanup(restore)
	if _, err := rebuildChatIndex(context.Background(), r.convDir, 0, "test"); err != nil {
		t.Fatal(err)
	}
	paginator, err := NewChatIndexPaginator(context.Background(), r.convDir)
	if err != nil {
		t.Fatal(err)
	}
	rows, _, err := r.cq.buildChatListRows(context.Background(), paginator)
	if err != nil {
		t.Fatal(err)
	}
	own := map[string]chatListRow{}
	for _, row := range rows {
		own[row.ChatID] = row
	}
	if x, l := own["X"], own[pull.ForkID]; x.OwnTotalTokens != 100 || l.OwnTotalTokens != 200 || l.TotalTokens != 300 {
		t.Fatalf("rows X=%+v L=%+v", x, l)
	}
	var groups []chatListRow
	for _, row := range collapseGroupRows(rows) {
		if row.Kind == chatRowGroup {
			groups = append(groups, row)
		}
	}
	if len(groups) != 1 || !slices.Contains(groups[0].GroupMemberIDs, "X") || !slices.Contains(groups[0].GroupMemberIDs, pull.ForkID) {
		t.Fatalf("groups = %+v", groups)
	}
	if g := groups[0]; g.TotalTokens != 300 || g.TotalCostUSD < 3-1e-9 || g.TotalCostUSD > 3+1e-9 {
		t.Fatalf("group total = %d / %v, want each spend once: 300 / 3", g.TotalTokens, g.TotalCostUSD)
	}
}

func TestChatList_groupViewShowsWholeHistory(t *testing.T) {
	q := func(at time.Time, tokens int, cost float64) pub_models.QueryCost { return ownQuery(at, tokens, cost) }
	parent := listChat("p", "same question", 10)
	parent.Queries = []pub_models.QueryCost{q(parent.Created, 1000, 1)}
	fork := listChat("f", "same question", 0)
	fork.Source, fork.SourceID = "clai", "p"
	fork.Queries = []pub_models.QueryCost{q(parent.Created, 1000, 1), q(fork.Created, 2000, 2)}
	cq, _ := listFixture(t, parent, fork)
	out := listOutput(t, cq, "0\n")
	groupRow := out[:strings.Index(out, "(select")]
	if !strings.Contains(groupRow, "| $3 ") {
		t.Fatalf("group row must total own spend 1+2 = $3: %q", groupRow)
	}
	groupView := out[strings.LastIndex(out, "Index"):]
	if !strings.Contains(groupView, "| $3 ") || !strings.Contains(groupView, "| $1 ") {
		t.Fatalf("the fork's own row must show its whole history (3.00) and the parent 1.00: %q", groupView)
	}
}

func TestReadChatIndex_olderVersionRebuilt(t *testing.T) {
	c := listChat("p", "q", 0)
	c.Queries = []pub_models.QueryCost{ownQuery(c.Created, 10, 0.1)}
	for _, readonly := range []bool{false, true} {
		cq, confDir := listFixture(t, c)
		_ = cq
		convDir := conversationsDir(confDir)
		stale := `{"version":` + itoa(chatIndexVersion-1) + `,"rows":[{"id":"p","created":"2026-01-02T03:04:00Z","message_count":1,"total_tokens":10}]}`
		if err := os.WriteFile(chatIndexPath(convDir), []byte(stale), 0o644); err != nil {
			t.Fatal(err)
		}
		old := utils.NoCreateConfig
		utils.NoCreateConfig = readonly
		rows, err := readChatIndex(context.Background(), convDir)
		utils.NoCreateConfig = old
		if err != nil || len(rows) != 1 || rows[0].OwnTotalTokens != 10 {
			t.Fatalf("readonly=%v rows=%+v err=%v", readonly, rows, err)
		}
		b, _ := os.ReadFile(chatIndexPath(convDir))
		current := strings.Contains(string(b), `"version":`+itoa(chatIndexVersion)+`,`)
		if readonly == current {
			t.Fatalf("readonly=%v but cache current=%v: %s", readonly, current, b)
		}
	}
}

var _ io.Writer = io.Discard

func itoa(i int) string { return strconv.Itoa(i) }

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

func TestPreviousQueryLine_writeErrorsAreReturned(t *testing.T) {
	refused := errors.New("refused")
	shown := []chatListRow{{Kind: chatRowNative, ChatID: "a", Title: "first"}}
	named := previousQueryState{Kind: prevNamed, Chat: pub_models.Chat{ID: "a", Title: "first"}}
	cq, _ := listFixture(t)
	cq.out, cq.input, cq.dims = failingWriter{err: refused}, nil, dimensions.Dimensions{Width: 200}
	if err := cq.writePreviousQueryLine(named, shown, false); !errors.Is(err, refused) {
		t.Fatalf("write error = %v, want %v", err, refused)
	}
}
