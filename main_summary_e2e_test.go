package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/summary"
	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/dimensions"
)

// summaryIndexRow mirrors the chat_index.cache row fields this file asserts on.
type summaryIndexRow struct {
	ID      string    `json:"id"`
	Created time.Time `json:"created"`
	Title   string    `json:"title"`
	Summary string    `json:"summary"`
	Updated time.Time `json:"updated"`
}

var seedSummaryAt = time.Date(2026, 9, 9, 9, 0, 0, 0, time.UTC)

func labelChat(c pub_models.Chat) pub_models.Chat {
	c.Title, c.Summary, c.SummaryAt = "T", "S", seedSummaryAt
	return c
}

func assertLabelledChat(t *testing.T, got pub_models.Chat) {
	t.Helper()
	if got.Title != "T" || got.Summary != "S" || !got.SummaryAt.Equal(seedSummaryAt) {
		t.Fatalf("expected title=T summary=S summary_at=%v, got title=%q summary=%q summary_at=%v", seedSummaryAt, got.Title, got.Summary, got.SummaryAt)
	}
}

func loadConv(t *testing.T, path string) pub_models.Chat {
	t.Helper()
	c, err := chat.FromPath(path)
	if err != nil {
		t.Fatalf("FromPath(%q): %v", path, err)
	}
	return c
}

func readIndexRows(t *testing.T, confDir string) map[string]summaryIndexRow {
	t.Helper()
	var cache struct {
		Rows []summaryIndexRow `json:"rows"`
	}
	raw := readStringFile(t, filepath.Join(confDir, "conversations", "chat_index.cache"))
	if err := json.Unmarshal([]byte(raw), &cache); err != nil {
		t.Fatalf("Unmarshal(chat_index.cache): %v", err)
	}
	rows := make(map[string]summaryIndexRow, len(cache.Rows))
	for _, row := range cache.Rows {
		rows[row.ID] = row
	}
	return rows
}

func runQuery(t *testing.T, args string) {
	t.Helper()
	if status := run(strings.Split(args, " ")); status != 0 {
		t.Fatalf("%q: status %d", args, status)
	}
}

// Integration row one: a plain -re forks a new file that inherits the seed's
// title, summary and summary_at; the seed is untouched and no binding is written.
func Test_e2e_reply_preserves_title_summary(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	confDir := setupMainTestConfigDir(t)
	t.Setenv("CLAI_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	chdirTemp(t)
	convDir := filepath.Join(confDir, "conversations")

	seed := labelChat(makeConv("seed-chat", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), msgUser("seed query"), msgAsst("seed answer")))
	seedConv(t, convDir, seed)
	mirror := seed
	mirror.ID = "globalScope"
	seedConv(t, convDir, mirror)
	seedPath := filepath.Join(convDir, "seed-chat.json")
	seedBefore := readStringFile(t, seedPath)

	runQuery(t, "-r -re -cm test q more")

	if got := readStringFile(t, seedPath); got != seedBefore {
		t.Fatalf("seed file modified by -re:\n%s", got)
	}
	convs := conversationFiles(t, confDir)
	if len(convs) != 2 {
		t.Fatalf("expected the seed plus one forked conversation, got %v", convs)
	}
	var fork pub_models.Chat
	for _, p := range convs {
		if c := loadConv(t, p); c.ID != seed.ID {
			fork = c
		}
	}
	if fork.ID == "" {
		t.Fatalf("no forked conversation found in %v", convs)
	}
	assertLabelledChat(t, fork)
	if len(fork.Messages) < len(seed.Messages)+2 {
		t.Fatalf("expected the seed's messages plus the new turn, got %d messages", len(fork.Messages))
	}
	for i, m := range seed.Messages {
		if fork.Messages[i].Content != m.Content {
			t.Fatalf("message %d = %q, want %q", i, fork.Messages[i].Content, m.Content)
		}
	}
	if fork.Messages[len(seed.Messages)].Content != "more" {
		t.Fatalf("expected the new turn after the seed messages, got %q", fork.Messages[len(seed.Messages)].Content)
	}
	assertLabelledChat(t, loadConv(t, filepath.Join(convDir, "globalScope.json")))
	if bindings, _ := filepath.Glob(filepath.Join(convDir, "dirs", "*.json")); len(bindings) != 0 {
		t.Fatalf("plain -re must not write a dirscope binding, got %v", bindings)
	}
}

// Integration row two: -dre continues the bound conversation in place and
// keeps its title, summary and summary_at; the binding history bumps as today.
func Test_e2e_dirreply_preserves_title_summary(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	confDir := setupMainTestConfigDir(t)
	t.Setenv("CLAI_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	chdirTemp(t)
	convDir := filepath.Join(confDir, "conversations")

	runQuery(t, "-r -cm test q seed query")
	convs := conversationFiles(t, confDir)
	if len(convs) != 1 {
		t.Fatalf("expected one seeded conversation, got %v", convs)
	}
	seedConv(t, convDir, labelChat(loadConv(t, convs[0])))
	before := readOnlyBinding(t, confDir)

	runQuery(t, "-r -dre -cm test q follow up")

	if got := conversationFiles(t, confDir); len(got) != 1 {
		t.Fatalf("expected -dre to continue in place, got %v", got)
	}
	got := loadConv(t, convs[0])
	assertLabelledChat(t, got)
	if got.ID != before.ChatID {
		t.Fatalf("id = %q, want bound %q", got.ID, before.ChatID)
	}
	after := readOnlyBinding(t, confDir)
	if after.ChatID != before.ChatID || len(after.History) != 1 || !after.History[0].LastScoped.After(before.History[0].LastScoped) {
		t.Fatalf("expected binding history bumped in place, before=%+v after=%+v", before, after)
	}
}

// Integration row three: a rebuilt cache mirrors title/summary and stamps
// updated from each file's modification time. chat list is structurally
// read-only (readOnlyChatSetup sets NoCreateConfig), so its rebuild is an
// in-memory scan that never persists; the first query after the cache is
// deleted performs the persisted rebuild through chat.Save.
func Test_e2e_chat_list_rebuild_mirrors_title(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	confDir := setupMainTestConfigDir(t)
	t.Setenv("CLAI_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	t.Setenv("HOME", t.TempDir())
	convDir := filepath.Join(confDir, "conversations")
	cachePath := filepath.Join(convDir, "chat_index.cache")

	mtimes := map[string]time.Time{
		"labelled": time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC),
		"plain":    time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC),
	}
	seedConv(t, convDir, labelChat(makeConv("labelled", time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), msgUser("labelled"), msgAsst("ok"))))
	seedConv(t, convDir, makeConv("plain", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), msgUser("plain"), msgAsst("ok")))
	for id, mtime := range mtimes {
		if err := os.Chtimes(filepath.Join(convDir, id+".json"), mtime, mtime); err != nil {
			t.Fatalf("Chtimes(%q): %v", id, err)
		}
	}
	if err := os.Remove(cachePath); err != nil {
		t.Fatalf("Remove(chat_index.cache): %v", err)
	}

	stdout, status := runOne(t, confDir, "-n -r c l q")
	if status != 0 {
		t.Fatalf("expected zero status, got %d. stdout=%q", status, stdout)
	}
	if _, err := os.Stat(cachePath); !os.IsNotExist(err) {
		t.Fatalf("read-only chat list must not persist the cache, stat err=%v", err)
	}

	runQuery(t, "-r -cm test q rebuild trigger")

	rows := readIndexRows(t, confDir)
	if rows["labelled"].Title != "T" || rows["labelled"].Summary != "S" {
		t.Fatalf("expected title/summary mirrored, got %+v", rows["labelled"])
	}
	if rows["plain"].Title != "" || rows["plain"].Summary != "" {
		t.Fatalf("unlabelled row must carry no label, got %+v", rows["plain"])
	}
	for id, want := range mtimes {
		if !rows[id].Updated.Equal(want) {
			t.Fatalf("row %q updated = %v, want mtime %v", id, rows[id].Updated, want)
		}
	}
	for id, row := range rows {
		if id == "labelled" || id == "plain" {
			continue
		}
		if row.Updated.Before(mtimes["labelled"]) {
			t.Fatalf("upserted row %q must carry an upsert-time updated, got %v", id, row.Updated)
		}
	}
}

// runQueryCaptured runs the CLI once through run(), capturing stdout and
// stderr, and asserts a zero exit status.
func runQueryCaptured(t *testing.T, args string) (string, string) {
	t.Helper()
	var status int
	stdout, stderr := captureStdoutStderr(t, func() {
		status = run(strings.Split(args, " "))
	})
	if status != 0 {
		t.Fatalf("%q: status %d stdout=%q stderr=%q", args, status, stdout, stderr)
	}
	return stdout, stderr
}

func summaryRows(c pub_models.Chat) []pub_models.QueryCost {
	var out []pub_models.QueryCost
	for _, row := range c.Queries {
		if row.Purpose == "summary" {
			out = append(out, row)
		}
	}
	return out
}

func onlyConversation(t *testing.T, confDir string) pub_models.Chat {
	t.Helper()
	convs := conversationFiles(t, confDir)
	if len(convs) != 1 {
		t.Fatalf("expected exactly one conversation file, got %v", convs)
	}
	return loadConv(t, convs[0])
}

func setupSummaryE2E(t *testing.T) string {
	t.Helper()
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	confDir := setupMainTestConfigDir(t)
	// The ladder's floor is the mock, so a row that omits -sm never
	// resolves to a paid vendor (R3-05).
	textConf := text.Default
	textConf.Model = "test"
	if err := utils.CreateFile(filepath.Join(confDir, "textConfig.json"), &textConf); err != nil {
		t.Fatalf("CreateFile(textConfig.json): %v", err)
	}
	t.Setenv("CLAI_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	blankDebugAndVendorKeys(t)
	t.Setenv(summary.EnvSummarizer, "")
	// TestMain swaps the real constructor for a refusing one; the summary
	// rows are the only opt-in.
	newSummarizer = summary.NewAgentSummarizer
	t.Cleanup(func() { newSummarizer = refusingSummarizer })
	// A current, empty index keeps the pre-existing "Building cache index"
	// chatter off stderr so the rows' empty-stderr oracle is exact.
	cachePath := filepath.Join(confDir, "conversations", "chat_index.cache")
	if err := os.WriteFile(cachePath, []byte(`{"version":2,"rows":[]}`), 0o644); err != nil {
		t.Fatalf("WriteFile(chat_index.cache): %v", err)
	}
	chdirTemp(t)
	return confDir
}

// Phase 4 integration row one: the first query labels the conversation in
// flight through the real summarizer and the mock vendor; the file, the
// index row and the mirror carry the label, stderr stays empty.
func Test_e2e_query_labels_in_flight(t *testing.T) {
	confDir := setupSummaryE2E(t)

	stdout, stderr := runQueryCaptured(t, "-r -cm test -t ls q hello tool_submit_summary")

	if !strings.Contains(stdout, "hello tool_submit_summary") {
		t.Fatalf("stdout = %q, want the echoed answer", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	got := onlyConversation(t, confDir)
	if got.Title != "Mock title" || got.Summary != "Mock summary." || got.SummaryAt.IsZero() {
		t.Fatalf("persisted label = title=%q summary=%q at=%v", got.Title, got.Summary, got.SummaryAt)
	}
	if len(got.Queries) != 2 || got.Queries[0].Purpose != "" || got.Queries[1].Purpose != "summary" {
		t.Fatalf("Queries = %+v, want the main row then the summary row", got.Queries)
	}
	if row := readIndexRows(t, confDir)[got.ID]; row.Title != "Mock title" || row.Summary != "Mock summary." {
		t.Fatalf("index row = %+v, want the label", row)
	}
	if mirror := loadConv(t, filepath.Join(confDir, "conversations", "globalScope.json")); mirror.Title != "Mock title" {
		t.Fatalf("globalScope.json title = %q, want the label", mirror.Title)
	}
	readOnlyBinding(t, confDir)
}

// Phase 4 integration row two: -summarize=false skips the launch.
func Test_e2e_query_summarize_opt_out(t *testing.T) {
	confDir := setupSummaryE2E(t)

	_, stderr := runQueryCaptured(t, "-r -cm test -t ls -summarize=false q hello tool_submit_summary")

	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	got := onlyConversation(t, confDir)
	if got.Title != "" || got.Summary != "" {
		t.Fatalf("opt-out must leave the chat unlabelled, got title=%q", got.Title)
	}
	if len(got.Queries) != 1 {
		t.Fatalf("Queries = %+v, want one row", got.Queries)
	}
}

// Phase 4 integration row three: a summarizer that never submits fails
// silently; the answer prints, the run exits zero, the chat stays unlabelled.
func Test_e2e_query_summary_failure_silent(t *testing.T) {
	confDir := setupSummaryE2E(t)

	stdout, stderr := runQueryCaptured(t, "-r -cm test -t ls q hello")

	if !strings.Contains(stdout, "hello") {
		t.Fatalf("stdout = %q, want the answer", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	got := onlyConversation(t, confDir)
	if got.Title != "" || len(summaryRows(got)) != 0 {
		t.Fatalf("failed summary must leave no label or row, got title=%q queries=%+v", got.Title, got.Queries)
	}
}

// Phase 4 integration row four: -sm selects the summarizer's model.
func Test_e2e_query_summary_model_flag(t *testing.T) {
	confDir := setupSummaryE2E(t)

	runQueryCaptured(t, "-r -cm test -t ls -sm test q hello tool_submit_summary")

	got := onlyConversation(t, confDir)
	rows := summaryRows(got)
	if len(rows) != 1 || rows[0].Model != "test" {
		t.Fatalf("summary rows = %+v, want one row with model test", rows)
	}
}

// Phase 4 integration row five: a -dre continuation of a labelled chat keeps
// the label and runs no second summarizer.
func Test_e2e_dirreply_does_not_relabel(t *testing.T) {
	confDir := setupSummaryE2E(t)
	runQueryCaptured(t, "-r -cm test -t ls q hello tool_submit_summary")
	before := onlyConversation(t, confDir)
	t.Setenv("CLAI_MOCK_SUMMARY_TITLES", "Second title")

	runQueryCaptured(t, "-r -cm test -dre q again tool_submit_summary")

	after := onlyConversation(t, confDir)
	if after.ID != before.ID || after.Title != "Mock title" || !after.SummaryAt.Equal(before.SummaryAt) {
		t.Fatalf("-dre changed the label: before=%q/%v after=%q/%v", before.Title, before.SummaryAt, after.Title, after.SummaryAt)
	}
	if rows := summaryRows(after); len(rows) != 1 {
		t.Fatalf("summary rows = %+v, want exactly one", rows)
	}
	if len(after.Queries) != 3 {
		t.Fatalf("Queries = %+v, want the two main rows and one summary row", after.Queries)
	}
}

// Phase 4 integration row six: the in-flight launch leaves the main run's
// cmd-ban policy effective (D29) while the label still lands.
func Test_e2e_query_summary_keeps_cmd_ban(t *testing.T) {
	confDir := setupSummaryE2E(t)
	marker := filepath.Join(t.TempDir(), "summary-banned-marker")
	t.Setenv("CLAI_MOCK_CMD_COMMAND", "touch "+marker)

	_, stderr := runQueryCaptured(t, "-r -cm test -t=cmd -cmd-ban=touch q tool_cmd tool_submit_summary")

	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("banned command must never spawn, marker stat err=%v", err)
	}
	got := onlyConversation(t, confDir)
	var toolResult string
	for _, m := range got.Messages {
		if m.Role == "tool" {
			toolResult = m.Content
		}
	}
	if !strings.HasPrefix(toolResult, "ERROR:") || !strings.Contains(toolResult, "touch") {
		t.Fatalf("tool result = %q, want a refusal naming touch", toolResult)
	}
	if got.Title != "Mock title" {
		t.Fatalf("title = %q, want the label", got.Title)
	}
}

// runStatusCaptured runs the CLI once through run(), returning stdout,
// stderr and the exit status.
func runStatusCaptured(t *testing.T, args string) (string, string, int) {
	t.Helper()
	var status int
	stdout, stderr := captureStdoutStderr(t, func() {
		status = run(strings.Split(args, " "))
	})
	return stdout, stderr, status
}

// seedSummarizable seeds an unlabelled conversation whose user message
// scripts the mock's submit_summary call.
func seedSummarizable(t *testing.T, convDir, id string, created time.Time) {
	t.Helper()
	seedConv(t, convDir, makeConv(id, created, msgUser("question "+id+" tool_submit_summary"), msgAsst("answer "+id)))
}

func assertMockLabel(t *testing.T, got pub_models.Chat) {
	t.Helper()
	if got.Title != "Mock title" || got.Summary != "Mock summary." || got.SummaryAt.IsZero() {
		t.Fatalf("chat %q label = title=%q summary=%q at=%v, want the mock label", got.ID, got.Title, got.Summary, got.SummaryAt)
	}
	if rows := summaryRows(got); len(rows) != 1 {
		t.Fatalf("chat %q summary rows = %+v, want exactly one", got.ID, got.Queries)
	}
}

// Phase 5 integration row one: the batch labels the rows inside the window
// through the real summarizer and the mock vendor, one job's completion
// does not cancel the other, the older file and the mirror stay untouched.
func Test_e2e_chat_summarize_window(t *testing.T) {
	confDir := setupSummaryE2E(t)
	convDir := filepath.Join(confDir, "conversations")
	old := time.Now().Add(-30 * 24 * time.Hour)
	seedSummarizable(t, convDir, "inside-a", time.Now().Add(-2*time.Hour))
	seedSummarizable(t, convDir, "inside-b", time.Now().Add(-time.Hour))
	seedSummarizable(t, convDir, "older", old)
	olderPath := filepath.Join(convDir, "older.json")
	// Save stamps every index row at now; the rebuild stamps from the file
	// mtime, which is what places the older row outside the window.
	if err := os.Chtimes(olderPath, old, old); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	if err := os.Remove(filepath.Join(convDir, "chat_index.cache")); err != nil {
		t.Fatalf("Remove(chat_index.cache): %v", err)
	}
	olderBefore := readStringFile(t, olderPath)

	stdout, stderr, status := runStatusCaptured(t, "c summarize -y -sm test 7d")

	if status != 0 {
		t.Fatalf("status %d, stdout=%q stderr=%q", status, stdout, stderr)
	}
	for _, id := range []string{"inside-a", "inside-b"} {
		if !strings.Contains(stdout, id+": Mock title") {
			t.Fatalf("stdout %q must list %q with the mock title", stdout, id)
		}
		assertMockLabel(t, loadConv(t, filepath.Join(convDir, id+".json")))
	}
	if strings.Contains(stdout, "older") {
		t.Fatalf("stdout %q must not list the row outside the window", stdout)
	}
	if got := readStringFile(t, olderPath); got != olderBefore {
		t.Fatalf("older file modified:\n%s", got)
	}
	rows := readIndexRows(t, confDir)
	if rows["inside-a"].Title != "Mock title" || rows["inside-b"].Title != "Mock title" || rows["older"].Title != "" {
		t.Fatalf("index rows = %+v, want the two inside rows labelled", rows)
	}
	if _, err := os.Stat(filepath.Join(convDir, "globalScope.json")); !os.IsNotExist(err) {
		t.Fatalf("globalScope.json must stay untouched, stat err=%v", err)
	}
	if bindings, _ := filepath.Glob(filepath.Join(convDir, "dirs", "*.json")); len(bindings) != 0 {
		t.Fatalf("the batch must not write a dirscope binding, got %v", bindings)
	}
}

// Phase 5 integration rows two and three: a rerun selects nothing and
// rewrites no file; -force regenerates with a newer summary_at.
func Test_e2e_chat_summarize_idempotent_and_force(t *testing.T) {
	confDir := setupSummaryE2E(t)
	convDir := filepath.Join(confDir, "conversations")
	seedSummarizable(t, convDir, "inside-a", time.Now().Add(-2*time.Hour))
	seedSummarizable(t, convDir, "inside-b", time.Now().Add(-time.Hour))
	pathA := filepath.Join(convDir, "inside-a.json")
	cachePath := filepath.Join(convDir, "chat_index.cache")
	if _, _, status := runStatusCaptured(t, "c summarize -y -sm test 7d"); status != 0 {
		t.Fatalf("first run status %d", status)
	}
	first := loadConv(t, pathA)
	assertMockLabel(t, first)
	fileBefore, cacheBefore := readStringFile(t, pathA), readStringFile(t, cachePath)

	stdout, _, status := runStatusCaptured(t, "c summarize -y -sm test 7d")

	if status != 0 || !strings.Contains(stdout, "0 conversations") {
		t.Fatalf("rerun status %d stdout %q, want the zero notice", status, stdout)
	}
	if readStringFile(t, pathA) != fileBefore || readStringFile(t, cachePath) != cacheBefore {
		t.Fatal("rerun must rewrite neither the file nor the index")
	}

	stdout, _, status = runStatusCaptured(t, "c summarize -y -sm test -force 7d")

	if status != 0 || !strings.Contains(stdout, "inside-a: Mock title") || !strings.Contains(stdout, "inside-b: Mock title") {
		t.Fatalf("-force status %d stdout %q, want both ids listed again", status, stdout)
	}
	forced := loadConv(t, pathA)
	if !forced.SummaryAt.After(first.SummaryAt) {
		t.Fatalf("summary_at %v must be newer than %v after -force", forced.SummaryAt, first.SummaryAt)
	}
	if readStringFile(t, cachePath) == cacheBefore {
		t.Fatal("-force must rewrite the index")
	}
}

// Phase 10 (R3-05): without -sm and with no recorded Queries the ladder
// bottoms out on the fixture's config model, which is the mock; the batch
// never resolves to a paid vendor by omission.
func Test_e2e_chat_summarize_floor_is_config_model(t *testing.T) {
	confDir := setupSummaryE2E(t)
	convDir := filepath.Join(confDir, "conversations")
	seedSummarizable(t, convDir, "inside", time.Now().Add(-time.Hour))

	stdout, stderr, status := runStatusCaptured(t, "c summarize -y 7d")

	if status != 0 || !strings.Contains(stdout, "inside: Mock title") {
		t.Fatalf("status %d stdout=%q stderr=%q, want the mock label without -sm", status, stdout, stderr)
	}
	got := loadConv(t, filepath.Join(convDir, "inside.json"))
	assertMockLabel(t, got)
	if rows := summaryRows(got); rows[0].Model != "test" {
		t.Fatalf("summary row model = %q, want the config floor test", rows[0].Model)
	}
}

// Phase 5 integration row four: -n without -y refuses before touching anything.
func Test_e2e_chat_summarize_noninteractive_needs_yes(t *testing.T) {
	confDir := setupSummaryE2E(t)
	convDir := filepath.Join(confDir, "conversations")
	seedSummarizable(t, convDir, "inside", time.Now().Add(-time.Hour))
	path := filepath.Join(convDir, "inside.json")
	before := readStringFile(t, path)

	_, stderr, status := runStatusCaptured(t, "-n c summarize 7d")

	if status == 0 || !strings.Contains(stderr, "-y") {
		t.Fatalf("status %d stderr %q, want a non-zero status naming -y", status, stderr)
	}
	if readStringFile(t, path) != before {
		t.Fatal("no file may be touched without confirmation")
	}
}

// Phase 5 integration row five: a model without a vendor key fails every
// job with an error line, touches no conversation, index or mirror, and
// leaves chat list working in the same dir.
func Test_e2e_chat_summarize_vendor_error_leaves_list_working(t *testing.T) {
	confDir := setupSummaryE2E(t)
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("HOME", t.TempDir())
	convDir := filepath.Join(confDir, "conversations")
	seedSummarizable(t, convDir, "inside", time.Now().Add(-time.Hour))
	path := filepath.Join(convDir, "inside.json")
	cachePath := filepath.Join(convDir, "chat_index.cache")
	fileBefore, cacheBefore := readStringFile(t, path), readStringFile(t, cachePath)

	stdout, _, status := runStatusCaptured(t, "c summarize -y -sm gpt-does-not-exist 7d")

	if status == 0 {
		t.Fatalf("expected a non-zero status, stdout=%q", stdout)
	}
	if !strings.Contains(stdout, "inside: ERROR") || !strings.Contains(stdout, "1 failed") {
		t.Fatalf("stdout %q, want the error line and the totals", stdout)
	}
	if readStringFile(t, path) != fileBefore || readStringFile(t, cachePath) != cacheBefore {
		t.Fatal("a failed job must touch neither the file nor the index")
	}
	if _, err := os.Stat(filepath.Join(convDir, "globalScope.json")); !os.IsNotExist(err) {
		t.Fatalf("globalScope.json must stay untouched, stat err=%v", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if listOut, listStatus := runOne(t, cwd, "-n -r c l q"); listStatus != 0 || !strings.Contains(listOut, "| clai ") {
		t.Fatalf("chat list status %d stdout %q, want the seeded native row", listStatus, listOut)
	}
}

// Phase 5 integration row six (phase 8: positional): the window is required.
func Test_e2e_chat_summarize_window_required(t *testing.T) {
	setupSummaryE2E(t)
	_, stderr, status := runStatusCaptured(t, "c summarize -y")
	if status == 0 || !strings.Contains(stderr, "<window>") {
		t.Fatalf("status %d stderr %q, want a non-zero status naming <window>", status, stderr)
	}
}

// wideFallback widens the no-TTY dimension fallback so the list's prompt
// column has room to render a label under captured stdout.
func wideFallback(t *testing.T) {
	t.Helper()
	old := dimensions.Fallback
	dimensions.Fallback = dimensions.Dimensions{Width: 200, Height: 40}
	t.Cleanup(func() { dimensions.Fallback = old })
}

func fixAuthChat(c pub_models.Chat) pub_models.Chat {
	c.Title, c.Summary, c.SummaryAt = "Fix auth", "Token refresh fixed.", seedSummaryAt
	return c
}

// seedBoundLabelled binds the test's working directory to one conversation
// through a real query, then labels that conversation on disk.
func seedBoundLabelled(t *testing.T) (confDir, cwd string) {
	t.Helper()
	confDir = setupSummaryE2E(t)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	runQuery(t, "-r -cm test q seed query")
	seedConv(t, filepath.Join(confDir, "conversations"), fixAuthChat(onlyConversation(t, confDir)))
	return confDir, cwd
}

func dirInfoJSON(t *testing.T, cwd, args string) map[string]any {
	t.Helper()
	stdout, status := runOne(t, cwd, args)
	if status != 0 {
		t.Fatalf("%q: status %d stdout=%q", args, status, stdout)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("%q: unmarshal %v, stdout=%q", args, err, stdout)
	}
	if got["title"] != "Fix auth" || got["summary"] != "Token refresh fixed." {
		t.Fatalf("%q: expected title and summary, got %+v", args, got)
	}
	return got
}

// Phase 6 integration row one: the list row renders the title for a labelled
// chat and the first message for an unlabelled one.
func Test_e2e_chat_list_shows_title(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	confDir := setupMainTestConfigDir(t)
	t.Setenv("CLAI_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	t.Setenv("HOME", t.TempDir())
	wideFallback(t)
	convDir := filepath.Join(confDir, "conversations")
	seedConv(t, convDir, fixAuthChat(makeConv("labelled", time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), msgUser("labelled prompt"), msgAsst("ok"))))
	seedConv(t, convDir, makeConv("plain", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), msgUser("plain prompt"), msgAsst("ok")))

	stdout, status := runOne(t, confDir, "-n -r c l q")
	if status != 0 {
		t.Fatalf("expected zero status, got %d. stdout=%q", status, stdout)
	}
	if !strings.Contains(stdout, "Fix auth") || strings.Contains(stdout, "labelled prompt") {
		t.Fatalf("expected the labelled row to show its title, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "plain prompt") {
		t.Fatalf("expected the unlabelled row to show its first message, got:\n%s", stdout)
	}
}

// Phase 6 integration row two: dirv2 JSON carries the label; version stays.
func Test_e2e_chat_dirv2_title_summary(t *testing.T) {
	_, cwd := seedBoundLabelled(t)
	got := dirInfoJSON(t, cwd, "-r c dirv2")
	if got["version"] != float64(2) {
		t.Fatalf("dirv2 version must stay 2, got %+v", got)
	}
}

// Phase 6 integration row three: v1 JSON carries the two keys.
func Test_e2e_chat_dir_title_summary(t *testing.T) {
	_, cwd := seedBoundLabelled(t)
	got := dirInfoJSON(t, cwd, "-r c dir")
	if _, ok := got["version"]; ok {
		t.Fatalf("v1 JSON must carry no version, got %+v", got)
	}
}

// Phase 6 integration rows four and five: the lookback element renders
// `title: summary` for a labelled history entry and the first-message head
// for an unlabelled one, in the same persisted system message.
func Test_e2e_lookback_block_title_summary(t *testing.T) {
	confDir := setupSummaryE2E(t)
	convDir := filepath.Join(confDir, "conversations")
	runQuery(t, "-r -cm test q seed query")
	seed := onlyConversation(t, confDir)

	runQuery(t, "-r -cm test -lb -t ls q hello")
	if got := newestSystemMessage(t, confDir, seed.ID); !strings.Contains(got, ">seed query</conversation>") {
		t.Fatalf("expected the unlabelled history entry rendered as its first-message head, got:\n%s", got)
	}

	seedConv(t, convDir, fixAuthChat(seed))
	runQuery(t, "-r -cm test -lb -t ls q hello")

	got := newestSystemMessage(t, confDir, seed.ID)
	if !strings.Contains(got, `id="`+seed.ID+`"`) || !strings.Contains(got, ">Fix auth: Token refresh fixed.</conversation>") {
		t.Fatalf("expected the labelled history entry rendered as title: summary, got:\n%s", got)
	}
	if !strings.Contains(got, ">hello</conversation>") {
		t.Fatalf("expected the unlabelled history entry rendered as its first-message head, got:\n%s", got)
	}
}

// newestSystemMessage returns the persisted system message of the newest
// conversation other than the seed, by conversation creation time.
func newestSystemMessage(t *testing.T, confDir, seedID string) string {
	t.Helper()
	var newest pub_models.Chat
	for _, p := range conversationFiles(t, confDir) {
		c := loadConv(t, p)
		if c.ID == seedID || c.Created.Before(newest.Created) {
			continue
		}
		newest = c
	}
	if newest.ID == "" || len(newest.Messages) == 0 || newest.Messages[0].Role != "system" {
		t.Fatalf("expected a newest conversation with a system message, got %+v", newest)
	}
	return newest.Messages[0].Content
}

// Phase 6 integration row six: the info view shows the title and summary lines.
func Test_e2e_chat_info_title_summary(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	confDir := setupMainTestConfigDir(t)
	t.Setenv("CLAI_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	t.Setenv("HOME", t.TempDir())
	convDir := filepath.Join(confDir, "conversations")
	seedConv(t, convDir, fixAuthChat(makeConv("labelled", time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), msgUser("labelled prompt"), msgAsst("ok"))))

	stdout, status := runOne(t, confDir, "-n -r c l 0 q")
	if status != 0 {
		t.Fatalf("expected zero status, got %d. stdout=%q", status, stdout)
	}
	if !strings.Contains(stdout, "=== Chat info ===") {
		t.Fatalf("expected the info view, got:\n%s", stdout)
	}
	for _, want := range []string{"title:", "Fix auth", "summary:", "Token refresh fixed."} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected %q in the info view, got:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, `summary: "`) {
		t.Fatalf("labelled info view must not fall back to the first message, got:\n%s", stdout)
	}
}

// Phase 8 integration row one: repeated cold-config batch runs with as many
// workers as rows never report a torn per-model config file (R2-01).
func Test_e2e_chat_summarize_cold_model_config(t *testing.T) {
	confDir := setupSummaryE2E(t)
	convDir := filepath.Join(confDir, "conversations")
	ids := []string{"c1", "c2", "c3", "c4", "c5", "c6"}
	for i, id := range ids {
		seedSummarizable(t, convDir, id, time.Now().Add(-time.Duration(i+1)*time.Hour))
	}
	modelConfig := filepath.Join(confDir, "mock_test_test.json")
	for round := range 10 {
		if err := os.Remove(modelConfig); err != nil && !os.IsNotExist(err) {
			t.Fatalf("round %d Remove: %v", round, err)
		}
		stdout, _, status := runStatusCaptured(t, "c summarize -y -sm test -workers 6 -force 7d")
		if status != 0 || strings.Contains(stdout, "ERROR") {
			t.Fatalf("round %d: status %d stdout=%q", round, status, stdout)
		}
		for _, id := range ids {
			if !strings.Contains(stdout, id+": Mock title") {
				t.Fatalf("round %d: stdout %q must list %q", round, stdout, id)
			}
		}
		b, err := os.ReadFile(modelConfig)
		if err != nil {
			t.Fatalf("round %d: model config missing: %v", round, err)
		}
		var parsed map[string]any
		if err := json.Unmarshal(b, &parsed); err != nil {
			t.Fatalf("round %d: model config unparseable: %v\n%s", round, err, b)
		}
	}
}

// Phase 8 integration row three: with no price in the model config and no
// catalog key, the in-flight summarizer adds no warning of its own. ancli
// prints warnings on stdout, so the main run's single enrich warning is the
// only one there and stderr stays empty (R2-03).
func Test_e2e_query_labels_in_flight_cold_price(t *testing.T) {
	confDir := setupSummaryE2E(t)
	if err := os.WriteFile(filepath.Join(confDir, "mock_test_test.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	stdout, stderr := runQueryCaptured(t, "-r -cm test -t ls q hello tool_submit_summary")

	if !strings.Contains(stdout, "hello tool_submit_summary") {
		t.Fatalf("stdout = %q, want the echoed answer", stdout)
	}
	if n := strings.Count(stdout, "failed to enrich chat with cost estimate"); n != 1 {
		t.Fatalf("stdout = %q, want exactly the main run's one enrich warning, got %d", stdout, n)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	got := onlyConversation(t, confDir)
	if got.Title != "Mock title" {
		t.Fatalf("persisted title = %q, want the label", got.Title)
	}
}

// refusingSummarizer is what every root test gets from main.go's wiring
// unless setupSummaryE2E restores the real constructor: running the test
// suite must never trigger a summarization that costs money (D32).
func refusingSummarizer(string) (models.Summarizer, error) {
	return nil, errors.New("real summarizer is off in the test binary; opt in through setupSummaryE2E")
}

func TestMain(m *testing.M) {
	newSummarizer = refusingSummarizer
	os.Exit(m.Run())
}

// Phase 8 guard rows: without the fixture's opt-in a test process never
// builds the real summarizer (the query stays unlabelled, chat summarize
// refuses and touches no file), and CLAI_SUMMARIZER=off refuses in the same
// way for any process.
func Test_e2e_summarizer_guard(t *testing.T) {
	modes := []struct {
		name  string
		apply func(t *testing.T)
		want  string
	}{
		{name: "test binary default", apply: func(t *testing.T) { newSummarizer = refusingSummarizer }, want: "off in the test binary"},
		{name: "CLAI_SUMMARIZER=off", apply: func(t *testing.T) { t.Setenv(summary.EnvSummarizer, "off") }, want: summary.EnvSummarizer},
	}
	for _, mode := range modes {
		t.Run("query "+mode.name, func(t *testing.T) {
			confDir := setupSummaryE2E(t)
			mode.apply(t)
			stdout, stderr := runQueryCaptured(t, "-r -cm test -t ls q hello tool_submit_summary")
			if !strings.Contains(stdout, "hello tool_submit_summary") || stderr != "" {
				t.Fatalf("stdout = %q stderr = %q, want the answer and a clean stderr", stdout, stderr)
			}
			if got := onlyConversation(t, confDir); got.Title != "" || len(summaryRows(got)) != 0 {
				t.Fatalf("conversation labelled without opt-in: %+v", got)
			}
		})
		t.Run("chat summarize "+mode.name, func(t *testing.T) {
			confDir := setupSummaryE2E(t)
			mode.apply(t)
			convDir := filepath.Join(confDir, "conversations")
			seedSummarizable(t, convDir, "guarded", time.Now().Add(-time.Hour))
			before := readStringFile(t, filepath.Join(convDir, "guarded.json"))
			stdout, stderr, status := runStatusCaptured(t, "c summarize -y -sm test 7d")
			if status == 0 || !strings.Contains(stdout+stderr, mode.want) {
				t.Fatalf("status %d stdout=%q stderr=%q, want a refusal naming %q", status, stdout, stderr, mode.want)
			}
			if got := readStringFile(t, filepath.Join(convDir, "guarded.json")); got != before {
				t.Fatalf("conversation modified by a refused run:\n%s", got)
			}
		})
	}
}
