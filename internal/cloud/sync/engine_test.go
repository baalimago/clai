package sync

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/cloud"
	"github.com/baalimago/clai/internal/cloud/cloudtest"
)

func TestCommit_thenPullOnAnotherMachine(t *testing.T) {
	srv := cloudtest.New(t)
	a, b := newMachine(t, srv, "tok-a"), newMachine(t, srv, "tok-b")
	a.write("textConfig.json", `{"model":"gpt"}`)
	a.write("profiles/coder.json", `{"prompt":"code"}`)
	a.write("openai_gpt_gpt-5.json", `{}`)
	res := a.commit(StoreConfig, "first")
	if res.Commit != 1 || res.Files != 2 || res.Uploaded != 2 {
		t.Fatalf("commit = %+v", res)
	}
	if got, _ := srv.Content("config", "profiles/coder.json"); got != `{"prompt":"code"}` {
		t.Fatalf("server has %q", got)
	}
	if again := a.commit(StoreConfig, "again"); again.Files != 0 {
		t.Fatalf("second commit sent %d files", again.Files)
	}
	pulled := b.pull(StoreConfig, PullOptions{})
	if pulled.Added != 2 || pulled.Commit != 1 {
		t.Fatalf("pull = %+v", pulled)
	}
	if b.read("textConfig.json") != `{"model":"gpt"}` {
		t.Fatal("b did not get textConfig.json")
	}
	if b.exists("openai_gpt_gpt-5.json") {
		t.Fatal("a model file synced")
	}
	// a pulling its own commit changes nothing.
	if own := a.pull(StoreConfig, PullOptions{}); own.Added+own.Updated+own.Removed != 0 || len(own.Conflicts) != 0 {
		t.Fatalf("own pull = %+v", own)
	}
}

func TestPull_updatesAndDeletesFilesChangedOnlyOnTheServer(t *testing.T) {
	srv := cloudtest.New(t)
	a := newMachine(t, srv, "tok-a")
	a.write("profiles/x.json", "1")
	a.write("profiles/y.json", "1")
	a.commit(StoreConfig, "")
	a.pull(StoreConfig, PullOptions{})
	srv.Put("config", "profiles/x.json", "2")
	srv.Delete("config", "profiles/y.json")
	res := a.pull(StoreConfig, PullOptions{})
	if res.Updated != 1 || res.Removed != 1 {
		t.Fatalf("pull = %+v", res)
	}
	if a.read("profiles/x.json") != "2" || a.exists("profiles/y.json") {
		t.Fatal("pull did not apply the server's changes")
	}
	b, err := os.ReadFile(baseCopy(a.ConfDir, "profiles/x.json"))
	if err != nil || string(b) != "2" {
		t.Fatalf("base copy = %q, %v", b, err)
	}
}

func TestCommit_behindIsRefusedWhole(t *testing.T) {
	srv := cloudtest.New(t)
	a := newMachine(t, srv, "tok-a")
	a.write("profiles/x.json", "1")
	a.commit(StoreConfig, "")
	srv.Put("config", "profiles/x.json", "theirs")
	a.write("profiles/x.json", "mine")
	a.write("profiles/new.json", "new")
	_, err := a.Commit(context.Background(), StoreConfig, CommitOptions{Message: "m"})
	var behind *BehindError
	if !errors.As(err, &behind) || len(behind.Paths) != 1 || behind.Paths[0].Path != "profiles/x.json" || behind.Paths[0].Device != "other" {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "pull") {
		t.Fatalf("message %q", err)
	}
	if _, ok := srv.Content("config", "profiles/new.json"); ok {
		t.Fatal("a refused commit applied a file")
	}
}

func TestPull_conflictKeepsMineAndSavesTheirs(t *testing.T) {
	srv := cloudtest.New(t)
	a := newMachine(t, srv, "tok-a")
	a.write("profiles/x.json", "base")
	a.commit(StoreConfig, "")
	srv.Put("config", "profiles/x.json", "theirs")
	a.write("profiles/x.json", "mine")
	res := a.pull(StoreConfig, PullOptions{})
	if !slices.Equal(res.Conflicts, []string{"profiles/x.json"}) {
		t.Fatalf("conflicts = %v", res.Conflicts)
	}
	if a.read("profiles/x.json") != "mine" {
		t.Fatal("pull touched the local file")
	}
	if b, _ := os.ReadFile(TheirsPath(a.ConfDir, "profiles/x.json")); string(b) != "theirs" {
		t.Fatalf("theirs copy = %q", b)
	}
	_, err := a.Commit(context.Background(), StoreConfig, CommitOptions{})
	var open *ConflictsOpenError
	if !errors.As(err, &open) || !strings.Contains(err.Error(), "resolve") {
		t.Fatalf("commit with open conflict: %v", err)
	}
	d, err := a.Diff(context.Background(), "profiles/x.json", false)
	if err != nil || !d.Conflict || !strings.Contains(d.Text(), "+theirs") || !strings.Contains(d.Text(), "+mine") {
		t.Fatalf("diff = %q, %v", d.Text(), err)
	}
	st, err := a.Status(context.Background(), StoreConfig, PlanOptions{})
	if err != nil || !slices.Equal(st.Conflicts, []string{"profiles/x.json"}) {
		t.Fatalf("status = %+v, %v", st, err)
	}
	r, err := a.Resolve("profiles/x.json", ResolveMine)
	if err != nil || r.How != ResolveMine {
		t.Fatalf("resolve: %+v %v", r, err)
	}
	if c := a.commit(StoreConfig, "mine wins"); c.Files != 1 {
		t.Fatalf("commit after resolve = %+v", c)
	}
	if got, _ := srv.Content("config", "profiles/x.json"); got != "mine" {
		t.Fatalf("server has %q", got)
	}
	if _, err := os.Stat(TheirsPath(a.ConfDir, "profiles/x.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("theirs copy kept after resolve")
	}
}

func TestResolve_theirsTrashesMine(t *testing.T) {
	srv := cloudtest.New(t)
	a := newMachine(t, srv, "tok-a")
	a.write("profiles/x.json", "base")
	a.commit(StoreConfig, "")
	srv.Put("config", "profiles/x.json", "theirs")
	a.write("profiles/x.json", "mine")
	a.pull(StoreConfig, PullOptions{})
	r, err := a.Resolve("profiles/x.json", ResolveTheirs)
	if err != nil || r.Trashed == "" {
		t.Fatalf("resolve: %+v %v", r, err)
	}
	if a.read("profiles/x.json") != "theirs" {
		t.Fatal("theirs not taken")
	}
	if b, _ := os.ReadFile(r.Trashed); string(b) != "mine" {
		t.Fatalf("trash = %q", b)
	}
	if c := a.commit(StoreConfig, ""); c.Files != 0 {
		t.Fatalf("nothing should be left to commit: %+v", c)
	}
	var not *NotConflictedError
	if _, err := a.Resolve("profiles/x.json", ResolveMine); !errors.As(err, &not) {
		t.Fatalf("second resolve: %v", err)
	}
	var unknown *UnknownPathError
	if _, err := a.Resolve("notes.txt", ResolveMine); !errors.As(err, &unknown) {
		t.Fatalf("unknown path: %v", err)
	}
}

func TestResolve_serverDeletion(t *testing.T) {
	srv := cloudtest.New(t)
	a := newMachine(t, srv, "tok-a")
	a.write("profiles/x.json", "base")
	a.commit(StoreConfig, "")
	srv.Delete("config", "profiles/x.json")
	a.write("profiles/x.json", "mine")
	a.pull(StoreConfig, PullOptions{})
	if _, err := a.Resolve("profiles/x.json", ResolveTheirs); err != nil {
		t.Fatal(err)
	}
	if a.exists("profiles/x.json") {
		t.Fatal("the server deleted the file; theirs should remove it")
	}
}

func TestResolve_theirsCopyGone(t *testing.T) {
	srv := cloudtest.New(t)
	a := newMachine(t, srv, "tok-a")
	a.write("profiles/x.json", "base")
	a.commit(StoreConfig, "")
	srv.Put("config", "profiles/x.json", "theirs")
	a.write("profiles/x.json", "mine")
	a.pull(StoreConfig, PullOptions{})
	_ = os.Remove(TheirsPath(a.ConfDir, "profiles/x.json"))
	if _, err := a.Resolve("profiles/x.json", ResolveTheirs); err == nil || !strings.Contains(err.Error(), "pull again") {
		t.Fatalf("err = %v", err)
	}
}

func TestPull_modesSettleBothSidesChanged(t *testing.T) {
	for _, mode := range []Mode{ModeTheirs, ModeMine} {
		t.Run(string(mode), func(t *testing.T) {
			srv := cloudtest.New(t)
			a := newMachine(t, srv, "tok-a")
			a.write("profiles/x.json", "base")
			a.commit(StoreConfig, "")
			srv.Put("config", "profiles/x.json", "theirs")
			a.write("profiles/x.json", "mine")
			res := a.pull(StoreConfig, PullOptions{Mode: mode})
			if len(res.Conflicts) != 0 {
				t.Fatalf("conflicts = %v", res.Conflicts)
			}
			want := map[Mode]string{ModeTheirs: "theirs", ModeMine: "mine"}[mode]
			if a.read("profiles/x.json") != want {
				t.Fatalf("file = %q", a.read("profiles/x.json"))
			}
			c := a.commit(StoreConfig, "")
			if wantFiles := map[Mode]int{ModeTheirs: 0, ModeMine: 1}[mode]; c.Files != wantFiles {
				t.Fatalf("commit = %+v", c)
			}
		})
	}
}

func TestPull_firstPullAsksWhenFilesDiffer(t *testing.T) {
	srv := cloudtest.New(t)
	srv.Put("config", "profiles/x.json", "theirs")
	srv.Put("config", "profiles/same.json", "same")
	srv.Put("config", "profiles/only.json", "only")
	a := newMachine(t, srv, "tok-a")
	a.write("profiles/x.json", "mine")
	a.write("profiles/same.json", "same")

	_, err := a.Pull(context.Background(), StoreConfig, PullOptions{})
	var first *FirstPullError
	if !errors.As(err, &first) {
		t.Fatalf("err = %v", err)
	}
	want := FirstPull{Remote: 3, Local: 2, OnlyRemote: 1, Same: 1, Differ: []string{"profiles/x.json"}}
	if first.First.Remote != want.Remote || first.First.OnlyRemote != 1 || first.First.Same != 1 || !slices.Equal(first.First.Differ, want.Differ) {
		t.Fatalf("first = %+v", first.First)
	}
	if !strings.Contains(err.Error(), "--theirs") || a.exists("profiles/only.json") {
		t.Fatalf("a refused first pull wrote files or said %q", err)
	}

	stop := errors.New("stop")
	if _, err := a.Pull(context.Background(), StoreConfig, PullOptions{Choose: func(FirstPull) (Mode, error) { return "", stop }}); !errors.Is(err, stop) {
		t.Fatalf("choose error: %v", err)
	}

	asked := 0
	res := a.pull(StoreConfig, PullOptions{Choose: func(f FirstPull) (Mode, error) { asked++; return ModeTheirs, nil }})
	if asked != 1 || a.read("profiles/x.json") != "theirs" || len(res.Trashed) != 1 || res.First == nil {
		t.Fatalf("asked %d, res %+v", asked, res)
	}
	srv.Put("config", "profiles/x.json", "again")
	a.pull(StoreConfig, PullOptions{Choose: func(FirstPull) (Mode, error) { asked++; return ModeTheirs, nil }})
	if asked != 1 {
		t.Fatal("a later pull asked again")
	}
}

func TestPlan_excludesSecretsLargeFilesAndImports(t *testing.T) {
	srv := cloudtest.New(t)
	a := newMachine(t, srv, "tok-a")
	a.write("profiles/leak.json", `{"api_key":"abcdefgh1234"}`)
	a.write("profiles/ok.json", `{"api_key":"$KEY"}`)
	a.write("conversations/imported/claude-code/s1.json", `{}`)
	a.write("conversations/c1.json", `{"messages":[]}`)
	a.write("conversations/globalScope.json", `{}`)
	a.write("conversations/dirs/x.json", `{}`)
	big := strings.Repeat("x", int(FileLimits[StoreConfig])+1)
	a.write("profiles/big.json", big)

	cfg, err := a.Plan(StoreConfig, PlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]ExclusionKind{}
	for _, x := range cfg.Excluded {
		kinds[x.Path] = x.Kind
	}
	if kinds["profiles/leak.json"] != ExcludedSecret || kinds["profiles/big.json"] != ExcludedTooLarge {
		t.Fatalf("excluded = %+v", cfg.Excluded)
	}
	if len(cfg.Changes) != 1 || cfg.Changes[0].Path != "profiles/ok.json" || cfg.Changes[0].Kind != Added {
		t.Fatalf("changes = %+v", cfg.Changes)
	}
	conv, err := a.Plan(StoreConversations, PlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(conv.Changes) != 1 || len(conv.Excluded) != 1 || conv.Excluded[0].Kind != ExcludedImport {
		t.Fatalf("conversations plan = %+v", conv)
	}
	with, _ := a.Plan(StoreConversations, PlanOptions{Imports: true})
	if len(with.Changes) != 2 {
		t.Fatalf("with imports = %+v", with.Changes)
	}
	narrowed, _ := a.Plan(StoreConfig, PlanOptions{Paths: []string{"profiles/ok.json"}})
	if len(narrowed.Changes) != 1 {
		t.Fatalf("narrowed = %+v", narrowed.Changes)
	}
	none, _ := a.Plan(StoreConfig, PlanOptions{Paths: []string{"skills/"}})
	if len(none.Changes) != 0 {
		t.Fatalf("skills/ = %+v", none.Changes)
	}
}

func TestCommit_importsRunExportAndCount(t *testing.T) {
	srv := cloudtest.New(t)
	a := newMachine(t, srv, "tok-a")
	exported := 0
	a.Hooks.Export = func(_ context.Context, dir string) error {
		exported++
		p := filepath.Join(dir, "conversations", "imported", "claude-code", "s1.json")
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		return os.WriteFile(p, []byte(`{"messages":[]}`), 0o644)
	}
	if c := a.commit(StoreConversations, ""); c.Files != 0 || exported != 0 {
		t.Fatalf("plain commit exported: %+v, %d", c, exported)
	}
	res, err := a.Commit(context.Background(), StoreConversations, CommitOptions{PlanOptions: PlanOptions{Imports: true}})
	if err != nil || res.Imported != 1 || exported != 1 {
		t.Fatalf("commit -imports = %+v, %v", res, err)
	}
	a.Hooks.Export = func(context.Context, string) error { return errors.New("boom") }
	if _, err := a.Commit(context.Background(), StoreConversations, CommitOptions{PlanOptions: PlanOptions{Imports: true}}); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("export error: %v", err)
	}
}

func TestCommit_deletionAndBatches(t *testing.T) {
	srv := cloudtest.New(t)
	a := newMachine(t, srv, "tok-a")
	a.write("profiles/x.json", "1")
	a.commit(StoreConfig, "")
	_ = os.Remove(a.abs("profiles/x.json"))
	res := a.commit(StoreConfig, "rm")
	if res.Files != 1 || res.Changes[0].Kind != Deleted {
		t.Fatalf("delete commit = %+v", res)
	}
	if _, ok := srv.Content("config", "profiles/x.json"); ok {
		t.Fatal("server kept the file")
	}
}

func TestCommit_serverErrorsPassThrough(t *testing.T) {
	srv := cloudtest.New(t)
	srv.Refuse = &cloudtest.Problem{Status: 402, Code: "card_required", Message: "add a card", URL: "https://clai.chat/account/"}
	a := newMachine(t, srv, "tok-a")
	a.write("profiles/x.json", "1")
	_, err := a.Commit(context.Background(), StoreConfig, CommitOptions{})
	apiErr, ok := cloud.AsAPIError(err)
	if !ok || apiErr.Code != "card_required" || apiErr.URL == "" {
		t.Fatalf("err = %v", err)
	}
}

func TestCommit_fileChangedDuringUpload(t *testing.T) {
	srv := cloudtest.New(t)
	a := newMachine(t, srv, "tok-a")
	a.write("profiles/x.json", "1")
	st, _ := loadState(a.ConfDir, StoreConfig)
	plan, _ := a.plan(StoreConfig, st, PlanOptions{})
	a.write("profiles/x.json", "2")
	var changed *ChangedDuringCommitError
	if _, err := a.upload(context.Background(), StoreConfig, plan.Changes); !errors.As(err, &changed) || !strings.Contains(err.Error(), "commit again") {
		t.Fatalf("err = %v", err)
	}
}

func TestPull_conversationMerges(t *testing.T) {
	m1 := `{"role":"user","content":"hello there"}`
	m2 := `{"role":"assistant","content":"hi"}`
	m3 := `{"role":"user","content":"more"}`
	chat := func(msgs ...string) string { return `{"id":"c1","messages":[` + strings.Join(msgs, ",") + `]}` }
	setup := func(t *testing.T) (*cloudtest.Server, machine, *[]string) {
		srv := cloudtest.New(t)
		a := newMachine(t, srv, "tok-a")
		written := &[]string{}
		a.Hooks.Pulled = func(_ string, w, _ []string) error { *written = append(*written, w...); return nil }
		a.write("conversations/c1.json", chat(m1))
		a.commit(StoreConversations, "")
		return srv, a, written
	}
	t.Run("theirs only added messages", func(t *testing.T) {
		srv, a, written := setup(t)
		srv.Put("conversations", "conversations/c1.json", chat(m1, m2))
		a.write("conversations/c1.json", chat(m1)+" ")
		res := a.pull(StoreConversations, PullOptions{})
		if len(res.Merged) != 1 || a.read("conversations/c1.json") != chat(m1, m2) || len(*written) != 1 {
			t.Fatalf("res = %+v", res)
		}
	})
	t.Run("mine only added messages", func(t *testing.T) {
		srv, a, _ := setup(t)
		srv.Put("conversations", "conversations/c1.json", chat(m1)+" ")
		a.write("conversations/c1.json", chat(m1, m2))
		res := a.pull(StoreConversations, PullOptions{})
		if len(res.Merged) != 1 || a.read("conversations/c1.json") != chat(m1, m2) {
			t.Fatalf("res = %+v", res)
		}
		if c := a.commit(StoreConversations, ""); c.Files != 1 {
			t.Fatalf("the longer copy should go up: %+v", c)
		}
	})
	t.Run("diverged forks theirs", func(t *testing.T) {
		srv, a, written := setup(t)
		srv.Put("conversations", "conversations/c1.json", chat(m1, m2))
		a.write("conversations/c1.json", chat(m1, m3))
		res := a.pull(StoreConversations, PullOptions{})
		if len(res.Forks) != 1 || len(res.Conflicts) != 0 {
			t.Fatalf("res = %+v", res)
		}
		f := res.Forks[0]
		if a.read("conversations/c1.json") != chat(m1, m3) || f.Title != "hello there (other)" {
			t.Fatalf("fork = %+v", f)
		}
		forked := a.read(f.Forked)
		if !strings.Contains(forked, `"source_id":"c1"`) || !strings.Contains(forked, `"source":"clai"`) {
			t.Fatalf("forked = %s", forked)
		}
		if !slices.Contains(*written, f.Forked) {
			t.Fatalf("hook not told of the fork: %v", *written)
		}
	})
	t.Run("not json forks fail", func(t *testing.T) {
		srv, a, _ := setup(t)
		srv.Put("conversations", "conversations/c1.json", "not json")
		a.write("conversations/c1.json", chat(m1, m3))
		if _, err := a.Pull(context.Background(), StoreConversations, PullOptions{}); err == nil {
			t.Fatal("want an error forking a broken conversation")
		}
	})
}

func TestMergeChats_titles(t *testing.T) {
	if mergeChats([]byte(`{"title":"t","messages":[]}`), []byte(`{"messages":[]}`)) != keepLocal {
		t.Fatal("the titled copy should win")
	}
	if mergeChats([]byte(`{"messages":[{"a":1}]}`), []byte(`{"messages":[{ "a": 1 }]}`)) != keepRemote {
		t.Fatal("equal messages keep the server's copy")
	}
	if firstUserText([]byte(`[{"role":"user","content":"`+strings.Repeat("w ", 40)+`"}]`)) == "" {
		t.Fatal("first user text")
	}
	if firstUserText([]byte(`nope`)) != "" {
		t.Fatal("broken messages")
	}
}

func TestStatus_countsCommitsAheadAndBothChanged(t *testing.T) {
	srv := cloudtest.New(t)
	a := newMachine(t, srv, "tok-a")
	a.write("profiles/x.json", "1")
	a.commit(StoreConfig, "")
	srv.Put("config", "profiles/x.json", "2")
	srv.Put("config", "profiles/y.json", "y")
	a.write("profiles/x.json", "3")
	st, err := a.Status(context.Background(), StoreConfig, PlanOptions{})
	if err != nil || st.Remote == nil {
		t.Fatalf("status %v", err)
	}
	if st.Remote.Ahead != 2 || !slices.Equal(st.Remote.Both, []string{"profiles/x.json"}) || len(st.Remote.Changed) != 2 {
		t.Fatalf("remote = %+v", st.Remote)
	}
	srv.Close()
	off, err := a.Status(context.Background(), StoreConfig, PlanOptions{})
	if err != nil || off.RemoteError == nil || len(off.Changes) != 1 {
		t.Fatalf("offline status = %+v, %v", off, err)
	}
}

func TestLog_newestFirst(t *testing.T) {
	srv := cloudtest.New(t)
	a := newMachine(t, srv, "tok-a")
	a.write("profiles/x.json", "1")
	a.commit(StoreConfig, "one")
	a.write("profiles/x.json", "2")
	a.commit(StoreConfig, "two")
	log, err := a.Log(context.Background(), StoreConfig, 1)
	if err != nil || len(log) != 1 || log[0].Message != "two" || log[0].Device != "a" || log[0].Files != 1 {
		t.Fatalf("log = %+v, %v", log, err)
	}
}

func TestPull_pagesThroughTheFeed(t *testing.T) {
	srv := cloudtest.New(t)
	srv.PageSize = 1
	for _, p := range []string{"a", "b", "c"} {
		srv.Put("config", "profiles/"+p+".json", p)
	}
	srv.Put("config", "profiles/a.json", "a2")
	a := newMachine(t, srv, "tok-a")
	res := a.pull(StoreConfig, PullOptions{})
	if res.Added != 3 || a.read("profiles/a.json") != "a2" || res.Commit != 4 {
		t.Fatalf("res = %+v", res)
	}
}

func TestPull_rejectsATamperedBlob(t *testing.T) {
	srv := cloudtest.New(t)
	srv.Put("config", "profiles/x.json", "good")
	unlock := srv.Lock()
	srv.Blobs[cloudtest.Hash([]byte("good"))] = []byte("evil")
	unlock()
	a := newMachine(t, srv, "tok-a")
	_, err := a.Pull(context.Background(), StoreConfig, PullOptions{})
	var sum *ChecksumError
	if !errors.As(err, &sum) || !strings.Contains(err.Error(), "profiles/x.json") {
		t.Fatalf("err = %v", err)
	}
	if a.exists("profiles/x.json") {
		t.Fatal("tampered bytes written")
	}
	// The failed pull keeps the cursor, so the next one tries again.
	st, _ := loadState(a.ConfDir, StoreConfig)
	if st.Pulled || st.Cursor != 0 {
		t.Fatalf("state advanced: %+v", st)
	}
}

func TestState_unreadableIsAnError(t *testing.T) {
	srv := cloudtest.New(t)
	a := newMachine(t, srv, "tok-a")
	p := statePath(a.ConfDir, StoreConfig)
	_ = os.MkdirAll(filepath.Dir(p), 0o700)
	_ = os.WriteFile(p, []byte("{"), 0o600)
	var se *StateError
	if _, err := a.Pull(context.Background(), StoreConfig, PullOptions{}); !errors.As(err, &se) || !strings.Contains(err.Error(), "move it aside") || se.Unwrap() == nil {
		t.Fatalf("err = %v", err)
	}
	for _, f := range []func() error{
		func() error { _, err := a.Plan(StoreConfig, PlanOptions{}); return err },
		func() error { _, err := a.Commit(context.Background(), StoreConfig, CommitOptions{}); return err },
		func() error { _, err := a.Status(context.Background(), StoreConfig, PlanOptions{}); return err },
		func() error { _, err := a.Diff(context.Background(), "profiles/x.json", false); return err },
		func() error { _, err := a.Resolve("profiles/x.json", ResolveMine); return err },
	} {
		if err := f(); !errors.As(err, &se) {
			t.Fatalf("want StateError, got %v", err)
		}
	}
	if err := ResetState(a.ConfDir); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Plan(StoreConfig, PlanOptions{}); err != nil {
		t.Fatalf("after reset: %v", err)
	}
}

func TestPruneTrash_dropsOldCopies(t *testing.T) {
	srv := cloudtest.New(t)
	a := newMachine(t, srv, "tok-a")
	if err := a.pruneTrash(); err != nil {
		t.Fatalf("no trash yet: %v", err)
	}
	a.write("profiles/x.json", "old")
	where, err := a.trash("profiles/x.json")
	if err != nil || where == "" {
		t.Fatal(err)
	}
	if err := a.pruneTrash(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(where); err != nil {
		t.Fatal("fresh trash removed")
	}
	old := testNow.Add(-31 * 24 * time.Hour)
	_ = os.Chtimes(where, old, old)
	if err := a.pruneTrash(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(where); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("old trash kept")
	}
	if where, err := a.trash("profiles/none.json"); err != nil || where != "" {
		t.Fatalf("trash of a missing file = %q, %v", where, err)
	}
}

func TestDiff_localRemoteAndChats(t *testing.T) {
	srv := cloudtest.New(t)
	a := newMachine(t, srv, "tok-a")
	a.write("profiles/x.json", "one\ntwo\nthree\n")
	a.write("conversations/c1.json", `{"messages":[{"role":"user","content":"x"}]}`)
	a.commit(StoreConfig, "")
	a.commit(StoreConversations, "")
	a.write("profiles/x.json", "one\n2\nthree\n")
	d, err := a.Diff(context.Background(), "profiles/x.json", false)
	if err != nil || d.Theirs != nil || !strings.Contains(d.Text(), "-two\n") || !strings.Contains(d.Text(), "+2\n") {
		t.Fatalf("diff = %q, %v", d.Text(), err)
	}
	srv.Put("config", "profiles/x.json", "one\ntwo\nTHREE\n")
	d, err = a.Diff(context.Background(), "profiles/x.json", true)
	if err != nil || d.Theirs == nil || !strings.Contains(d.Text(), "+THREE") || !strings.Contains(d.Theirs.Label, "other") {
		t.Fatalf("remote diff = %q, %v", d.Text(), err)
	}
	// Conversations have no base copy: the base comes from the server.
	srv.Put("conversations", "conversations/c1.json", `{"messages":[{"role":"user","content":"x"},{"role":"assistant","content":"y"}]}`)
	d, err = a.Diff(context.Background(), "conversations/c1.json", true)
	if err != nil || !strings.Contains(d.Text(), "base   (rev 1): 1 messages; theirs") || !strings.Contains(d.Text(), "2 messages") {
		t.Fatalf("chat diff = %q, %v", d.Text(), err)
	}
	d, err = a.Diff(context.Background(), "profiles/new.json", false)
	if err != nil || !d.Base.Missing || !d.Mine.Missing {
		t.Fatalf("absent = %+v, %v", d, err)
	}
	var unknown *UnknownPathError
	if _, err := a.Diff(context.Background(), "x.txt", false); !errors.As(err, &unknown) {
		t.Fatalf("unknown: %v", err)
	}
	srv.Close()
	if _, err := a.Diff(context.Background(), "profiles/x.json", true); err == nil {
		t.Fatal("remote diff offline should fail")
	}
}

func TestUnifiedHunks_largeInputsDegrade(t *testing.T) {
	a := strings.Repeat("a\n", 3000)
	b := strings.Repeat("b\n", 3000)
	out := unifiedHunks(splitLines(Side{Bytes: []byte(a)}), splitLines(Side{Bytes: []byte(b)}), 3)
	if out == "" {
		t.Fatal("no output for a large diff")
	}
	if describeChat(Side{Bytes: []byte("x")}) != "not a conversation" || describeChat(Side{Missing: true}) != "absent" {
		t.Fatal("describeChat")
	}
}

func TestPull_unauthorized(t *testing.T) {
	srv := cloudtest.New(t)
	a := newMachine(t, srv, "nope")
	_, err := a.Pull(context.Background(), StoreConfig, PullOptions{})
	if apiErr, ok := cloud.AsAPIError(err); !ok || !apiErr.Unauthorized() {
		t.Fatalf("err = %v", err)
	}
}
