package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/cloud"
	"github.com/baalimago/clai/internal/cloud/cloudtest"
)

var uuidV7 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type chatOpt func(map[string]any)

func withQueries(qs ...[2]string) chatOpt {
	return func(m map[string]any) {
		var out []map[string]any
		for _, q := range qs {
			out = append(out, map[string]any{"created_at": q[0], "model": q[1]})
		}
		m["queries"] = out
	}
}

func withTitle(title string) chatOpt { return func(m map[string]any) { m["title"] = title } }

func withImages(refs ...string) chatOpt {
	return func(m map[string]any) {
		parts := []map[string]any{{"type": "text", "text": "see"}}
		for _, r := range refs {
			parts = append(parts, map[string]any{"type": "image", "asset": r})
		}
		m["messages"] = append(m["messages"].([]map[string]any), map[string]any{"role": "user", "content": parts})
	}
}

func chatJSON(id string, texts []string, opts ...chatOpt) string {
	var msgs []map[string]any
	for _, s := range texts {
		msgs = append(msgs, map[string]any{"role": "user", "content": s})
	}
	m := map[string]any{"id": id, "created": "2026-01-01T00:00:00Z", "messages": msgs}
	for _, o := range opts {
		o(m)
	}
	b, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func convPath(id string) string { return "conversations/" + id + ".json" }

func imageRef(content string) string { return cloudtest.Hash([]byte(content)) + ".png" }

func assetPath(ref string) string { return "conversations/assets/" + ref }

func pullConv(t *testing.T, m machine, id string) ConversationPull {
	t.Helper()
	res, err := m.PullConversation(context.Background(), id)
	if err != nil {
		t.Fatalf("PullConversation(%s): %v", id, err)
	}
	return res
}

func trashFile(m machine, rel string) string {
	return filepath.Join(m.ConfDir, "cloud", "trash", filepath.FromSlash(rel)+".20261026T120000Z")
}

func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			target, err := os.Readlink(p)
			out[p] = "-> " + target
			return err
		}
		b, err := os.ReadFile(p)
		out[p] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func readDoc(t *testing.T, m machine, id string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(m.read(convPath(id))), &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func messageCount(doc map[string]any) int { return len(doc["messages"].([]any)) }

func TestPullConversation_adopts(t *testing.T) {
	srv := cloudtest.New(t)
	imgA, imgB := "png-a", "png-b"
	cloudChat := chatJSON("X", []string{"a", "b"}, withImages(imageRef(imgA), imageRef(imgB)))
	srv.Put("conversations", convPath("X"), cloudChat)
	srv.Put("conversations", assetPath(imageRef(imgA)), imgA)
	srv.Put("conversations", assetPath(imageRef(imgB)), imgB)
	srv.Put("conversations", "conversations/reasoning/X/c1.json", `[{"id":"r1"}]`)
	srv.Put("conversations", "conversations/reasoning/X/c2.json", `[{"id":"r2"}]`)
	srv.Put("conversations", convPath("other"), chatJSON("other", []string{"z"}))
	m := newMachine(t, srv, "tok-b")

	res := pullConv(t, m, "X")

	if res.Outcome != ConversationAdopted || res.ID != "X" || res.TrashPath != "" || res.ForkID != "" {
		t.Fatalf("result = %+v", res)
	}
	want, _ := srv.Content("conversations", convPath("X"))
	if got := m.read(convPath("X")); got != want {
		t.Fatalf("X.json = %s, want %s", got, want)
	}
	if m.read("conversations/reasoning/X/c1.json") != `[{"id":"r1"}]` || m.read("conversations/reasoning/X/c2.json") != `[{"id":"r2"}]` {
		t.Fatal("sidecar not written")
	}
	if m.read(assetPath(imageRef(imgA))) != imgA || m.read(assetPath(imageRef(imgB))) != imgB {
		t.Fatal("images not written")
	}
	if m.exists(convPath("other")) {
		t.Fatal("another conversation was written")
	}
}

func TestPullConversation_unchanged(t *testing.T) {
	for _, c := range []struct {
		name        string
		local       string
		cloud       string
		wantTrashed bool
	}{
		{"identical bytes", chatJSON("X", []string{"a", "b"}), chatJSON("X", []string{"a", "b"}), false},
		{"other title", chatJSON("X", []string{"a", "b"}, withTitle("local")), chatJSON("X", []string{"a", "b"}, withTitle("cloud")), true},
		{"titled local over untitled cloud", chatJSON("X", []string{"a", "b"}, withTitle("mine")), chatJSON("X", []string{"a", "b"}), true},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv := cloudtest.New(t)
			srv.Put("conversations", convPath("X"), c.cloud)
			m := newMachine(t, srv, "tok-b")
			m.write(convPath("X"), c.local)

			res := pullConv(t, m, "X")

			if res.Outcome != ConversationUnchanged || res.ForkID != "" {
				t.Fatalf("result = %+v", res)
			}
			if got := m.read(convPath("X")); got != c.cloud {
				t.Fatalf("X.json = %s, want the cloud's", got)
			}
			if !c.wantTrashed {
				if res.TrashPath != "" || m.exists("cloud/trash") {
					t.Fatalf("identical bytes were trashed: %+v", res)
				}
				return
			}
			if res.TrashPath != trashFile(m, convPath("X")) {
				t.Fatalf("TrashPath = %q", res.TrashPath)
			}
			if b, _ := os.ReadFile(res.TrashPath); string(b) != c.local {
				t.Fatalf("trash holds %s", b)
			}
			if ids := listChatFiles(t, m); !slices.Equal(ids, []string{"X.json"}) {
				t.Fatalf("a fork was made: %v", ids)
			}
		})
	}
}

func listChatFiles(t *testing.T, m machine) []string {
	t.Helper()
	entries, err := os.ReadDir(m.abs("conversations"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			out = append(out, e.Name())
		}
	}
	slices.Sort(out)
	return out
}

func TestPullConversation_replacesShorterLocal(t *testing.T) {
	srv := cloudtest.New(t)
	cloudChat := chatJSON("X", []string{"a", "b", "c", "d"})
	srv.Put("conversations", convPath("X"), cloudChat)
	m := newMachine(t, srv, "tok-b")
	local := chatJSON("X", []string{"a", "b"})
	m.write(convPath("X"), local)

	res := pullConv(t, m, "X")

	if res.Outcome != ConversationReplaced || res.ForkID != "" {
		t.Fatalf("result = %+v", res)
	}
	if res.TrashPath != trashFile(m, convPath("X")) {
		t.Fatalf("TrashPath = %q", res.TrashPath)
	}
	if b, _ := os.ReadFile(res.TrashPath); string(b) != local {
		t.Fatalf("trash holds %s", b)
	}
	if m.read(convPath("X")) != cloudChat {
		t.Fatal("cloud copy not written")
	}
	if got := listChatFiles(t, m); !slices.Equal(got, []string{"X.json"}) {
		t.Fatalf("a fork was made: %v", got)
	}
}

func TestPullConversation_forksLocal(t *testing.T) {
	for _, c := range []struct {
		name         string
		local, cloud []string
	}{
		{"local ahead", []string{"a", "b", "c"}, []string{"a", "b"}},
		{"diverged", []string{"a", "b", "x"}, []string{"a", "b", "c"}},
		{"one message differing", []string{"a", "y"}, []string{"a", "b"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv := cloudtest.New(t)
			cloudChat := chatJSON("X", c.cloud)
			srv.Put("conversations", convPath("X"), cloudChat)
			m := newMachine(t, srv, "tok-b")
			local := chatJSON("X", c.local, withTitle("keep me"))
			m.write(convPath("X"), local)
			m.write("conversations/reasoning/X/c1.json", `[{"id":"mine"}]`)

			res := pullConv(t, m, "X")

			if res.Outcome != ConversationLocalForked || res.ForkID == "" || res.ForkID == "X" || res.TrashPath != "" {
				t.Fatalf("result = %+v", res)
			}
			fork := readDoc(t, m, res.ForkID)
			if messageCount(fork) != len(c.local) || fork["source"] != "clai" || fork["source_id"] != "X" || fork["id"] != res.ForkID || fork["title"] != "keep me" {
				t.Fatalf("fork = %v", fork)
			}
			if got := m.read("conversations/reasoning/" + res.ForkID + "/c1.json"); got != `[{"id":"mine"}]` {
				t.Fatalf("fork sidecar = %q", got)
			}
			if m.read(convPath("X")) != cloudChat {
				t.Fatal("X is not the cloud copy")
			}
			if m.exists("conversations/reasoning/X/c1.json") {
				t.Fatal("the local sidecar stayed under X")
			}
		})
	}
}

func TestPullConversation_forkWrittenBeforeCloud(t *testing.T) {
	srv := cloudtest.New(t)
	cloudChat := chatJSON("X", []string{"a"})
	srv.Put("conversations", convPath("X"), cloudChat)
	m := newMachine(t, srv, "tok-b")
	local := chatJSON("X", []string{"a", "b"})
	m.write(convPath("X"), local)
	cc, err := m.fetchConversation(context.Background(), "X")
	if err != nil {
		t.Fatal(err)
	}

	res, err := m.putAside("X", []byte(local), ConversationLocalForked, cc)
	if err != nil {
		t.Fatal(err)
	}

	if messageCount(readDoc(t, m, res.ForkID)) != 2 {
		t.Fatal("the fork does not hold every local message before the cloud files are written")
	}
	if m.read(convPath("X")) != local {
		t.Fatal("X.json changed before writeCloud")
	}
}

func TestPullConversation_forkCreated(t *testing.T) {
	const (
		t1 = "2026-03-01T10:00:00Z"
		t2 = "2026-03-02T10:00:00Z"
		t3 = "2026-03-03T10:00:00Z"
		t4 = "2026-03-04T10:00:00Z"
		t5 = "2026-03-05T10:00:00Z"
	)
	for _, c := range []struct {
		name         string
		local, cloud []chatOpt
		want         string
	}{
		{"third query lacking", []chatOpt{withQueries([2]string{t1, "m"}, [2]string{t2, "m"}, [2]string{t3, "m"})}, []chatOpt{withQueries([2]string{t1, "m"}, [2]string{t2, "m"})}, t3},
		{"earliest lacking", []chatOpt{withQueries([2]string{t1, "m"}, [2]string{t4, "m"}, [2]string{t5, "m"})}, []chatOpt{withQueries([2]string{t1, "m"}, [2]string{t2, "m"})}, t4},
		{"same time other model", []chatOpt{withQueries([2]string{t1, "m"}, [2]string{t2, "other"})}, []chatOpt{withQueries([2]string{t1, "m"}, [2]string{t2, "m"})}, t2},
		{"every query in the cloud copy", []chatOpt{withQueries([2]string{t1, "m"})}, []chatOpt{withQueries([2]string{t1, "m"}, [2]string{t2, "m"})}, testNow.Format(time.RFC3339)},
		{"no local queries", nil, []chatOpt{withQueries([2]string{t1, "m"})}, testNow.Format(time.RFC3339)},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv := cloudtest.New(t)
			srv.Put("conversations", convPath("X"), chatJSON("X", []string{"a", "b"}, c.cloud...))
			m := newMachine(t, srv, "tok-b")
			m.write(convPath("X"), chatJSON("X", []string{"a", "x", "y"}, c.local...))

			res := pullConv(t, m, "X")

			if res.Outcome != ConversationLocalForked {
				t.Fatalf("outcome = %v", res.Outcome)
			}
			created, err := time.Parse(time.RFC3339Nano, readDoc(t, m, res.ForkID)["created"].(string))
			if err != nil {
				t.Fatal(err)
			}
			if want, _ := time.Parse(time.RFC3339, c.want); !created.Equal(want) {
				t.Fatalf("created = %v, want %v", created, want)
			}
		})
	}
}

func TestPullConversation_forkID(t *testing.T) {
	setup := func(t *testing.T) machine {
		srv := cloudtest.New(t)
		srv.Put("conversations", convPath("X"), chatJSON("X", []string{"a"}))
		m := newMachine(t, srv, "tok-b")
		m.write(convPath("X"), chatJSON("X", []string{"a", "b"}))
		return m
	}
	t.Run("default is a chatid", func(t *testing.T) {
		m := setup(t)
		res := pullConv(t, m, "X")
		if !uuidV7.MatchString(res.ForkID) {
			t.Fatalf("fork id %q is not a chatid", res.ForkID)
		}
	})
	t.Run("injected id", func(t *testing.T) {
		m := setup(t)
		m.newID = func() (string, error) { return "fixed-fork", nil }
		if res := pullConv(t, m, "X"); res.ForkID != "fixed-fork" || !m.exists(convPath("fixed-fork")) {
			t.Fatalf("result = %+v", res)
		}
	})
	t.Run("failing id changes nothing", func(t *testing.T) {
		m := setup(t)
		boom := errors.New("no entropy")
		m.newID = func() (string, error) { return "", boom }
		before := snapshot(t, m.ConfDir)
		_, err := m.PullConversation(context.Background(), "X")
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v", err)
		}
		if after := snapshot(t, m.ConfDir); fmt.Sprint(before) != fmt.Sprint(after) {
			t.Fatal("local files changed")
		}
	})
}

func TestPullConversation_writesExactly(t *testing.T) {
	srv := cloudtest.New(t)
	held, missing := "png-held", "png-missing"
	srv.Put("conversations", convPath("X"), chatJSON("X", []string{"a"}, withImages(imageRef(held), imageRef(missing))))
	srv.Put("conversations", assetPath(imageRef(held)), held)
	srv.Put("conversations", assetPath(imageRef(missing)), missing)
	srv.Put("conversations", "conversations/reasoning/X/b.json", `{"cloud":"b"}`)
	srv.Put("conversations", "conversations/reasoning/X/c.json", `{"cloud":"c"}`)
	m := newMachine(t, srv, "tok-b")
	m.write("conversations/reasoning/X/a.json", `{"local":"a"}`)
	m.write("conversations/reasoning/X/b.json", `{"local":"b"}`)
	m.write(assetPath(imageRef(held)), held)
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(m.abs(assetPath(imageRef(held))), old, old); err != nil {
		t.Fatal(err)
	}

	pullConv(t, m, "X")

	entries, err := os.ReadDir(m.abs("conversations/reasoning/X"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if !slices.Equal(names, []string{"b.json", "c.json"}) {
		t.Fatalf("sidecar dir = %v", names)
	}
	if m.read("conversations/reasoning/X/b.json") != `{"cloud":"b"}` || m.read("conversations/reasoning/X/c.json") != `{"cloud":"c"}` {
		t.Fatal("sidecar bytes are not the cloud's")
	}
	info, err := os.Stat(m.abs(assetPath(imageRef(held))))
	if err != nil || !info.ModTime().Equal(old) {
		t.Fatalf("held image touched: %v %v", info, err)
	}
	if m.read(assetPath(imageRef(missing))) != missing {
		t.Fatal("missing image not written")
	}
	if got := srv.FetchedHashes; slices.Contains(got, cloudtest.Hash([]byte(held))) {
		t.Fatal("held image was fetched")
	}
}

func TestPullConversation_recordsBase(t *testing.T) {
	srv := cloudtest.New(t)
	srv.Put("config", "profiles/p.json", `{"p":1}`)
	srv.Put("conversations", convPath("X"), chatJSON("X", []string{"a"}))
	srv.Put("conversations", convPath("other"), chatJSON("other", []string{"o"}))
	m := newMachine(t, srv, "tok-b")
	m.pull(StoreConfig, PullOptions{})
	m.pull(StoreConversations, PullOptions{Mode: ModeTheirs})
	srv.Put("conversations", convPath("X"), chatJSON("X", []string{"a", "b"}))
	img := "png-new"
	srv.Put("conversations", convPath("X"), chatJSON("X", []string{"a", "b"}, withImages(imageRef(img))))
	srv.Put("conversations", assetPath(imageRef(img)), img)
	srv.Put("conversations", "conversations/reasoning/X/c.json", `[1]`)
	srv.Put("conversations", convPath("later"), chatJSON("later", []string{"l"}))

	st, err := loadState(m.ConfDir, StoreConversations)
	if err != nil {
		t.Fatal(err)
	}
	st.Conflicts[convPath("X")] = conflict{Rev: 1}
	m.write("cloud/theirs/"+convPath("X"), "stale")
	m.write("conversations/reasoning/X/old.json", `[0]`)
	st.Entries["conversations/reasoning/X/old.json"] = baseEntry{Rev: 1, SHA256: "h"}
	st.Hashes["conversations/reasoning/X/old.json"] = hashEntry{SHA256: "h"}
	if err := st.save(m.ConfDir, StoreConversations); err != nil {
		t.Fatal(err)
	}
	cursor, commit := st.Cursor, st.Commit

	pullConv(t, m, "X")

	st, err = loadState(m.ConfDir, StoreConversations)
	if err != nil {
		t.Fatal(err)
	}
	if st.Cursor != cursor || st.Commit != commit {
		t.Fatalf("cursor %d->%d, commit %d->%d", cursor, st.Cursor, commit, st.Commit)
	}
	for _, rel := range []string{convPath("X"), "conversations/reasoning/X/c.json", assetPath(imageRef(img))} {
		e, ok := st.Entries[rel]
		srvEntry := srv.Entries["conversations"][rel]
		if !ok || e.Rev != srvEntry.Rev || e.SHA256 != srvEntry.SHA256 {
			t.Errorf("base of %s = %+v (%v), server has %+v", rel, e, ok, srvEntry)
		}
	}
	if _, open := st.Conflicts[convPath("X")]; open || m.exists("cloud/theirs/"+convPath("X")) {
		t.Fatal("conflict on X.json not cleared")
	}
	if _, ok := st.Entries["conversations/reasoning/X/old.json"]; ok {
		t.Fatal("removed sidecar file kept its base")
	}
	if _, ok := st.Hashes["conversations/reasoning/X/old.json"]; ok {
		t.Fatal("removed sidecar file kept its hash")
	}

	res := m.pull(StoreConversations, PullOptions{})
	if res.Updated != 0 || len(res.Merged) != 0 || len(res.Conflicts) != 0 || len(res.Forks) != 0 || res.Removed != 0 || res.Added != 1 {
		t.Fatalf("following pull = %+v", res)
	}
	if !m.exists(convPath("later")) {
		t.Fatal("the cursor was advanced: another conversation was missed")
	}
}

func TestPullConversation_fetchesOnlyItsBlobs(t *testing.T) {
	srv := cloudtest.New(t)
	for i := range 50 {
		id := fmt.Sprintf("c%02d", i)
		srv.Put("conversations", convPath(id), chatJSON(id, []string{id}))
	}
	srv.Put("conversations", convPath("X2"), chatJSON("X2", []string{"x2"}))
	srv.Put("conversations", "conversations/imported/claude/i.json", `{"imported":1}`)
	img := "png-x"
	xChat := chatJSON("X", []string{"a"}, withImages(imageRef(img)))
	srv.Put("conversations", convPath("X"), xChat)
	srv.Put("conversations", assetPath(imageRef(img)), img)
	srv.Put("conversations", "conversations/reasoning/X/c.json", `[1]`)
	srv.Put("conversations", "conversations/reasoning/X2/c.json", `[2]`)
	m := newMachine(t, srv, "tok-b")

	pullConv(t, m, "X")

	want := []string{cloudtest.Hash([]byte(xChat)), cloudtest.Hash([]byte(`[1]`)), cloudtest.Hash([]byte(img))}
	got := slices.Clone(srv.FetchedHashes)
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("fetched %v, want %v", got, want)
	}
}

func TestPullConversation_pagedStore(t *testing.T) {
	srv := cloudtest.New(t)
	srv.PageSize = 2
	for i := range 6 {
		id := fmt.Sprintf("c%d", i)
		srv.Put("conversations", convPath(id), chatJSON(id, []string{id}))
	}
	srv.Put("conversations", convPath("X"), chatJSON("X", []string{"a"}))
	m := newMachine(t, srv, "tok-b")

	if res := pullConv(t, m, "X"); res.Outcome != ConversationAdopted {
		t.Fatalf("result = %+v", res)
	}
}

func TestPullConversation_hookPulled(t *testing.T) {
	var calls [][]string
	setup := func(t *testing.T) machine {
		srv := cloudtest.New(t)
		srv.Put("conversations", convPath("X"), chatJSON("X", []string{"a"}, withImages(imageRef("i"))))
		srv.Put("conversations", assetPath(imageRef("i")), "i")
		srv.Put("conversations", "conversations/reasoning/X/c.json", `[1]`)
		m := newMachine(t, srv, "tok-b")
		m.Hooks.Pulled = func(_ context.Context, _ string, written, removed []string, _ func(done, total int)) error {
			calls = append(calls, slices.Clone(written))
			if len(removed) != 0 {
				t.Errorf("removed = %v", removed)
			}
			return nil
		}
		return m
	}
	t.Run("adopted", func(t *testing.T) {
		calls = nil
		pullConv(t, setup(t), "X")
		if len(calls) != 1 || !slices.Equal(calls[0], []string{convPath("X")}) {
			t.Fatalf("calls = %v", calls)
		}
	})
	t.Run("local forked", func(t *testing.T) {
		calls = nil
		m := setup(t)
		m.write(convPath("X"), chatJSON("X", []string{"a", "b"}))
		m.newID = func() (string, error) { return "L1", nil }
		pullConv(t, m, "X")
		if len(calls) != 1 || !slices.Equal(calls[0], []string{convPath("X"), convPath("L1")}) {
			t.Fatalf("calls = %v", calls)
		}
	})
	t.Run("nil hook", func(t *testing.T) {
		m := setup(t)
		m.Hooks.Pulled = nil
		pullConv(t, m, "X")
	})
}

func TestPullConversation_writesNothingToServer(t *testing.T) {
	srv := cloudtest.New(t)
	srv.Put("conversations", convPath("X"), chatJSON("X", []string{"a"}))
	m := newMachine(t, srv, "tok-b")
	m.write(convPath("X"), chatJSON("X", []string{"a", "b"}))
	commits, entries, blobs := len(srv.Commits["conversations"]), len(srv.Entries["conversations"]), len(srv.Blobs)

	if res := pullConv(t, m, "X"); res.Outcome != ConversationLocalForked {
		t.Fatalf("result = %+v", res)
	}

	if len(srv.Commits["conversations"]) != commits || len(srv.Entries["conversations"]) != entries || len(srv.Blobs) != blobs {
		t.Fatal("the pull wrote to the server")
	}
	if n := srv.Requests["/api/v1/stores/conversations/commit"] + srv.Requests["/api/v1/blobs/batch"]; n != 0 {
		t.Fatalf("%d upload requests", n)
	}
}

func TestSettle(t *testing.T) {
	a, b := chatJSON("X", []string{"a"}), chatJSON("X", []string{"a", "b"})
	for _, c := range []struct {
		name         string
		local, cloud string
		want         ConversationOutcome
	}{
		{"identical", a, a, ConversationUnchanged},
		{"cloud longer", a, b, ConversationReplaced},
		{"local longer", b, a, ConversationLocalForked},
		{"diverged", chatJSON("X", []string{"z"}), a, ConversationLocalForked},
		{"title only", chatJSON("X", []string{"a"}, withTitle("t")), a, ConversationUnchanged},
		{"other title", chatJSON("X", []string{"a"}, withTitle("t")), chatJSON("X", []string{"a"}, withTitle("u")), ConversationUnchanged},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := settle([]byte(c.local), []byte(c.cloud)); got != c.want {
				t.Fatalf("settle = %v, want %v", got, c.want)
			}
		})
	}
}

func TestPullConversation_failures(t *testing.T) {
	type env struct {
		srv *cloudtest.Server
		m   machine
		ctx context.Context
	}
	img := "png-f"
	good := chatJSON("X", []string{"a"})
	basic := func(t *testing.T) env {
		srv := cloudtest.New(t)
		srv.Put("conversations", convPath("X"), good)
		return env{srv: srv, m: newMachine(t, srv, "tok-b"), ctx: context.Background()}
	}
	withImg := func(t *testing.T, ref string) env {
		e := basic(t)
		e.srv.Put("conversations", convPath("X"), chatJSON("X", []string{"a"}, withImages(ref)))
		return e
	}
	hashOf := func(s string) string { return cloudtest.Hash([]byte(s)) }
	isType := func(target any) func(error) bool {
		return func(err error) bool { return errors.As(err, target) }
	}
	var (
		notFound   *ConversationNotFoundError
		invalid    *InvalidConversationIDError
		malformed  *MalformedConversationError
		localErr   *LocalConversationError
		missingBlb *MissingBlobError
		checksum   *ChecksumError
		linked     *LinkedConversationError
		stateErr   *StateError
		apiErr     *cloud.APIError
		transport  *cloud.TransportError
	)
	for _, c := range []struct {
		name      string
		id        string
		setup     func(t *testing.T) env
		check     func(error) bool
		noRequest bool
		changes   bool
	}{
		{"unknown", "nope", basic, isType(&notFound), false, false},
		{"deleted", "X", func(t *testing.T) env { e := basic(t); e.srv.Delete("conversations", convPath("X")); return e }, isType(&notFound), false, false},
		{"only in the config store", "X", func(t *testing.T) env {
			e := basic(t)
			e.srv.Delete("conversations", convPath("X"))
			e.srv.Put("config", convPath("X"), good)
			return e
		}, isType(&notFound), false, false},
		{"empty id", "", basic, isType(&invalid), true, false},
		{"slash id", "a/b", basic, isType(&invalid), true, false},
		{"dot id", ".h", basic, isType(&invalid), true, false},
		{"cloud file not json", "X", func(t *testing.T) env { e := basic(t); e.srv.Put("conversations", convPath("X"), "not json"); return e }, isType(&malformed), false, false},
		{"image ref outside the store", "X", func(t *testing.T) env { return withImg(t, "../x.png") }, isType(&malformed), false, false},
		{"image with no entry", "X", func(t *testing.T) env { return withImg(t, imageRef(img)) }, isType(&missingBlb), false, false},
		{"corrupt blob", "X", func(t *testing.T) env {
			e := basic(t)
			e.srv.Blobs[hashOf(good)] = []byte("tampered")
			return e
		}, isType(&checksum), false, false},
		{"blob missing from the server", "X", func(t *testing.T) env {
			e := basic(t)
			delete(e.srv.Blobs, hashOf(good))
			return e
		}, isType(&missingBlb), false, false},
		{"linked local chat", "X", func(t *testing.T) env {
			e := basic(t)
			dot := t.TempDir()
			if err := os.WriteFile(filepath.Join(dot, "X.json"), []byte(good), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(e.m.abs("conversations"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(dot, "X.json"), e.m.abs(convPath("X"))); err != nil {
				t.Fatal(err)
			}
			return e
		}, isType(&linked), false, false},
		{"linked sidecar dir", "X", func(t *testing.T) env {
			e := basic(t)
			e.srv.Put("conversations", "conversations/reasoning/X/c.json", `[1]`)
			if err := os.MkdirAll(e.m.abs("conversations/reasoning"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(t.TempDir(), e.m.abs("conversations/reasoning/X")); err != nil {
				t.Fatal(err)
			}
			return e
		}, isType(&linked), false, false},
		{"local not json", "X", func(t *testing.T) env { e := basic(t); e.m.write(convPath("X"), "{{{"); return e }, isType(&localErr), false, false},
		{"local unreadable", "X", func(t *testing.T) env {
			e := basic(t)
			if err := os.MkdirAll(e.m.abs(convPath("X")), 0o755); err != nil {
				t.Fatal(err)
			}
			return e
		}, isType(&localErr), false, false},
		{"state unreadable", "X", func(t *testing.T) env {
			e := basic(t)
			e.m.write("cloud/state/conversations.json", "garbage")
			return e
		}, isType(&stateErr), true, false},
		{"revoked token", "X", func(t *testing.T) env { e := basic(t); delete(e.srv.Tokens, "tok-b"); return e }, func(err error) bool {
			a, ok := cloud.AsAPIError(err)
			return ok && a.Unauthorized() && errors.As(err, &apiErr)
		}, false, false},
		{"closed server", "X", func(t *testing.T) env { e := basic(t); e.srv.Close(); return e }, isType(&transport), false, false},
		{"cancelled context", "X", func(t *testing.T) env {
			e := basic(t)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			e.ctx = ctx
			return e
		}, func(err error) bool { return errors.Is(err, context.Canceled) }, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := c.setup(t)
			before := snapshot(t, e.m.ConfDir)
			requests := 0
			for _, n := range e.srv.Requests {
				requests += n
			}

			res, err := e.m.PullConversation(e.ctx, c.id)

			if err == nil || !c.check(err) {
				t.Fatalf("err = %v (%T)", err, err)
			}
			if res.Outcome != 0 || res.ForkID != "" {
				t.Fatalf("result = %+v on failure", res)
			}
			if c.noRequest {
				after := 0
				for _, n := range e.srv.Requests {
					after += n
				}
				if after != requests {
					t.Fatalf("%d requests were sent", after-requests)
				}
			}
			after := snapshot(t, e.m.ConfDir)
			if !c.changes && fmt.Sprint(before) != fmt.Sprint(after) {
				t.Fatalf("local files changed:\nbefore %v\nafter  %v", before, after)
			}
		})
	}
	t.Run("error texts", func(t *testing.T) {
		for _, err := range []error{
			&ConversationNotFoundError{ID: "a"}, &InvalidConversationIDError{ID: "a"},
			&MalformedConversationError{ID: "a", Cause: errors.New("c")}, &LocalConversationError{ID: "a", Cause: errors.New("c")},
			&MissingBlobError{Path: "p", SHA256: "s"}, &LinkedConversationError{Path: "p"},
		} {
			if err.Error() == "" || !strings.ContainsAny(err.Error(), "ap") {
				t.Errorf("%T has no text", err)
			}
		}
		cause := errors.New("c")
		if !errors.Is(&MalformedConversationError{Cause: cause}, cause) || !errors.Is(&LocalConversationError{Cause: cause}, cause) {
			t.Fatal("causes are not unwrapped")
		}
	})
}
