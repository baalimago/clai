package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, dir, rel string) (string, bool) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if errors.Is(err, os.ErrNotExist) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(b), true
}

func engine(f *fakeServer, token string) *Engine {
	return &Engine{Client: f.client(token), ConfDir: "", Now: func() time.Time { return testNow }}
}

func machine(t *testing.T, f *fakeServer, token string) *Engine {
	t.Helper()
	e := engine(f, token)
	e.ConfDir = t.TempDir()
	return e
}

func mustSync(t *testing.T, e *Engine, store Store) Report {
	t.Helper()
	rep, err := e.Sync(context.Background(), store)
	if err != nil {
		t.Fatalf("sync %s: %v", store, err)
	}
	return rep
}

func TestSyncMovesConfigBetweenMachines(t *testing.T) {
	f := newFakeServer(t)
	a, b := machine(t, f, "tok-a"), machine(t, f, "tok-b")
	writeFile(t, a.ConfDir, "profiles/coder.json", `{"model":"gpt"}`)
	writeFile(t, a.ConfDir, "skills/review/SKILL.md", "# review")
	writeFile(t, a.ConfDir, "conversations/x.json", `{}`)
	writeFile(t, a.ConfDir, "notes.txt", "local only")

	rep := mustSync(t, a, StoreConfig)
	if rep.Uploaded != 2 {
		t.Fatalf("uploaded %d, want 2", rep.Uploaded)
	}
	if _, ok := f.content(StoreConfig, "notes.txt"); ok {
		t.Fatal("a file outside the allowlist was synced")
	}

	mustSync(t, b, StoreConfig)
	if got, _ := readFile(t, b.ConfDir, "profiles/coder.json"); got != `{"model":"gpt"}` {
		t.Fatalf("b has %q", got)
	}
	if _, ok := readFile(t, b.ConfDir, "conversations/x.json"); ok {
		t.Fatal("the config store carried a conversation")
	}

	// An edit on b reaches a; a's own commit coming back is no conflict.
	writeFile(t, b.ConfDir, "profiles/coder.json", `{"model":"claude"}`)
	mustSync(t, b, StoreConfig)
	writeFile(t, a.ConfDir, "skills/review/SKILL.md", "# review v2")
	rep = mustSync(t, a, StoreConfig)
	if len(rep.Conflicts) != 0 {
		t.Fatalf("conflicts: %v", rep.Conflicts)
	}
	if got, _ := readFile(t, a.ConfDir, "profiles/coder.json"); got != `{"model":"claude"}` {
		t.Fatalf("a has %q", got)
	}
	if got, _ := f.content(StoreConfig, "skills/review/SKILL.md"); got != "# review v2" {
		t.Fatalf("server has %q", got)
	}

	// A deletion travels too.
	if err := os.Remove(filepath.Join(a.ConfDir, "profiles", "coder.json")); err != nil {
		t.Fatal(err)
	}
	mustSync(t, a, StoreConfig)
	rep = mustSync(t, b, StoreConfig)
	if _, ok := readFile(t, b.ConfDir, "profiles/coder.json"); ok || rep.Removed != 1 {
		t.Fatalf("the deletion did not reach b (removed %d)", rep.Removed)
	}
}

func TestConfigConflictTakesServerCopyAndKeepsLocal(t *testing.T) {
	f := newFakeServer(t)
	a := machine(t, f, "tok-a")
	f.put(StoreConfig, "theme.json", `{"dark":true}`)
	writeFile(t, a.ConfDir, "theme.json", `{"dark":false}`)
	rep := mustSync(t, a, StoreConfig)
	if got, _ := readFile(t, a.ConfDir, "theme.json"); got != `{"dark":true}` {
		t.Fatalf("theme is %q", got)
	}
	if len(rep.Conflicts) != 1 {
		t.Fatalf("conflicts %v", rep.Conflicts)
	}
	aside := filepath.Join(a.ConfDir, "cloud", "conflicts", "theme.json.20261026T120000Z")
	if b, err := os.ReadFile(aside); err != nil || string(b) != `{"dark":false}` {
		t.Fatalf("local copy not kept: %q %v", b, err)
	}
}

func TestEditBeatsRemoteDelete(t *testing.T) {
	f := newFakeServer(t)
	a := machine(t, f, "tok-a")
	writeFile(t, a.ConfDir, "theme.json", `1`)
	mustSync(t, a, StoreConfig)
	f.del(StoreConfig, "theme.json")
	writeFile(t, a.ConfDir, "theme.json", `2`)
	mustSync(t, a, StoreConfig)
	if got, ok := f.content(StoreConfig, "theme.json"); !ok || got != "2" {
		t.Fatalf("server has %q %v", got, ok)
	}
}

func TestSecretsAndLargeFilesStayLocal(t *testing.T) {
	f := newFakeServer(t)
	a := machine(t, f, "tok-a")
	writeFile(t, a.ConfDir, "mcpServers/gh.json", `{"env":{"GITHUB_TOKEN":"ghp_abcdefghijklmnopqrstuvwxyz0123456789"}}`)
	writeFile(t, a.ConfDir, "mcpServers/ok.json", `{"env":{"GITHUB_TOKEN":"$GITHUB_TOKEN"}}`)
	writeFile(t, a.ConfDir, "skills/big/blob.bin", strings.Repeat("x", 1_000_001))
	rep := mustSync(t, a, StoreConfig)
	if len(rep.Skipped) != 2 {
		t.Fatalf("skipped %v", rep.Skipped)
	}
	if _, ok := f.content(StoreConfig, "mcpServers/gh.json"); ok {
		t.Fatal("a token was uploaded")
	}
	if _, ok := f.content(StoreConfig, "mcpServers/ok.json"); !ok {
		t.Fatal("an env reference was taken for a secret")
	}
}

func chatJSON(t *testing.T, id string, msgs ...string) string {
	t.Helper()
	type msg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	c := struct {
		ID       string `json:"id"`
		Messages []msg  `json:"messages"`
	}{ID: id}
	for _, m := range msgs {
		c.Messages = append(c.Messages, msg{Role: "user", Content: m})
	}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestConversationMerges(t *testing.T) {
	f := newFakeServer(t)
	a := machine(t, f, "tok-a")
	var written, removed []string
	a.Hooks.Pulled = func(_ string, w, r []string) error {
		written, removed = append(written, w...), append(removed, r...)
		return nil
	}
	writeFile(t, a.ConfDir, "conversations/c1.json", chatJSON(t, "c1", "hi"))
	writeFile(t, a.ConfDir, "conversations/c2.json", chatJSON(t, "c2", "hi"))
	writeFile(t, a.ConfDir, "conversations/c3.json", chatJSON(t, "c3", "hi"))
	writeFile(t, a.ConfDir, "conversations/globalScope.json", chatJSON(t, "g", "hi"))
	writeFile(t, a.ConfDir, "conversations/dirs/abc.json", `{}`)
	mustSync(t, a, StoreConversations)
	if _, ok := f.content(StoreConversations, "conversations/globalScope.json"); ok {
		t.Fatal("globalScope synced")
	}
	if _, ok := f.content(StoreConversations, "conversations/dirs/abc.json"); ok {
		t.Fatal("dirs synced")
	}

	// c1: the other device only added; theirs is longer and wins.
	f.put(StoreConversations, "conversations/c1.json", chatJSON(t, "c1", "hi", "more"))
	// c2: this machine added more than the other device; ours wins.
	f.put(StoreConversations, "conversations/c2.json", chatJSON(t, "c2", "hi", "x"))
	writeFile(t, a.ConfDir, "conversations/c2.json", chatJSON(t, "c2", "hi", "x", "y"))
	// c3: both diverged; both are kept.
	f.put(StoreConversations, "conversations/c3.json", chatJSON(t, "c3", "hi", "theirs"))
	writeFile(t, a.ConfDir, "conversations/c3.json", chatJSON(t, "c3", "hi", "mine"))
	writeFile(t, a.ConfDir, "conversations/c1.json", chatJSON(t, "c1", "hi"))
	f.del(StoreConversations, "conversations/c1.json")
	f.put(StoreConversations, "conversations/c1.json", chatJSON(t, "c1", "hi", "more"))

	rep := mustSync(t, a, StoreConversations)
	if got, _ := readFile(t, a.ConfDir, "conversations/c1.json"); got != chatJSON(t, "c1", "hi", "more") {
		t.Fatalf("c1 = %s", got)
	}
	if got, _ := f.content(StoreConversations, "conversations/c2.json"); got != chatJSON(t, "c2", "hi", "x", "y") {
		t.Fatalf("server c2 = %s", got)
	}
	if got, _ := readFile(t, a.ConfDir, "conversations/c3.json"); got != chatJSON(t, "c3", "hi", "theirs") {
		t.Fatalf("c3 = %s", got)
	}
	if len(rep.Conflicts) != 1 {
		t.Fatalf("conflicts %v", rep.Conflicts)
	}
	var forked string
	for _, w := range written {
		switch w {
		case "conversations/c1.json", "conversations/c2.json", "conversations/c3.json":
		default:
			forked = w
		}
	}
	b, _ := readFile(t, a.ConfDir, forked)
	var fork struct {
		ID       string `json:"id"`
		Source   string `json:"source"`
		SourceID string `json:"source_id"`
	}
	if err := json.Unmarshal([]byte(b), &fork); err != nil || fork.Source != "clai" || fork.SourceID != "c3" || fork.ID == "c3" {
		t.Fatalf("fork %q: %+v %v", forked, fork, err)
	}
	if _, ok := f.content(StoreConversations, forked); !ok {
		t.Fatal("the fork was not pushed")
	}
	if len(removed) != 0 {
		t.Fatalf("removed %v", removed)
	}
}

func TestMergeChats(t *testing.T) {
	cases := []struct {
		name         string
		mine, theirs string
		want         mergeOutcome
	}{
		{"equal", `{"messages":[1]}`, `{"messages":[1]}`, keepRemote},
		{"title only here", `{"messages":[1],"title":"t"}`, `{"messages":[1]}`, keepLocal},
		{"not json", `x`, `{}`, keepBoth},
		{"spacing", `{"messages":[{"a": 1}, 2]}`, `{"messages":[{"a":1}]}`, keepLocal},
	}
	for _, c := range cases {
		if got := mergeChats([]byte(c.mine), []byte(c.theirs)); got != c.want {
			t.Errorf("%s: got %d want %d", c.name, got, c.want)
		}
	}
}

func TestConversationStoreNeedsSubscription(t *testing.T) {
	f := newFakeServer(t)
	f.subscribed = false
	a := machine(t, f, "tok-a")
	_, err := a.Sync(context.Background(), StoreConversations)
	apiErr, ok := IsAPIError(err)
	if !ok || !apiErr.SubscriptionRequired() {
		t.Fatalf("err %v", err)
	}
	var syncErr *SyncError
	if !errors.As(err, &syncErr) || syncErr.Store != StoreConversations {
		t.Fatalf("err %T", err)
	}
}

func TestPullPagesAndRetriesAfterLostRace(t *testing.T) {
	f := newFakeServer(t)
	f.pageSize = 2
	for i := range 5 {
		f.put(StoreConfig, "profiles/p"+string(rune('a'+i))+".json", "{}")
	}
	a := machine(t, f, "tok-a")
	if rep := mustSync(t, a, StoreConfig); rep.Downloaded != 5 {
		t.Fatalf("downloaded %d", rep.Downloaded)
	}
	// Another device commits theme.json between our pull and our push:
	// the commit conflicts, and the next round pulls and settles it.
	writeFile(t, a.ConfDir, "theme.json", "mine")
	f.onCommit = func() { f.putLocked(StoreConfig, "theme.json", "theirs") }
	rep := mustSync(t, a, StoreConfig)
	if got, _ := readFile(t, a.ConfDir, "theme.json"); got != "theirs" || len(rep.Conflicts) != 1 {
		t.Fatalf("theme %q, conflicts %v", got, rep.Conflicts)
	}
}

func TestDownloadChecksIntegrity(t *testing.T) {
	f := newFakeServer(t)
	f.put(StoreConfig, "theme.json", "real")
	f.mu.Lock()
	for k := range f.blobs {
		f.blobs[k] = []byte("tampered")
	}
	f.mu.Unlock()
	a := machine(t, f, "tok-a")
	_, err := a.Sync(context.Background(), StoreConfig)
	var sumErr *ChecksumError
	if !errors.As(err, &sumErr) {
		t.Fatalf("err %v", err)
	}
}

func TestExportHookRunsBeforeConversations(t *testing.T) {
	f := newFakeServer(t)
	a := machine(t, f, "tok-a")
	a.Hooks.Export = func(_ context.Context, confDir string) error {
		writeFile(t, confDir, "conversations/imported/claude-code/s1.json", chatJSON(t, "s1", "hi"))
		return nil
	}
	mustSync(t, a, StoreConversations)
	if _, ok := f.content(StoreConversations, "conversations/imported/claude-code/s1.json"); !ok {
		t.Fatal("the export was not synced")
	}
	a.Hooks.Export = func(context.Context, string) error { return errors.New("boom") }
	rep := mustSync(t, a, StoreConversations)
	if len(rep.Skipped) != 1 {
		t.Fatalf("skipped %v", rep.Skipped)
	}
}
