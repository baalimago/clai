package sync

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/cloud"
	"github.com/baalimago/clai/internal/cloud/cloudtest"
)

// A symlinked profiles/ dir (dotfiles setup) is invisible to scan: the first
// pull overwrites the local file through the link with no trash.
func TestPull_leavesASymlinkedDirAlone(t *testing.T) {
	srv := cloudtest.New(t)
	srv.Put("config", "profiles/p.json", `{"theirs":1}`)
	b := newMachine(t, srv, "tok-b")
	dot := t.TempDir()
	if err := os.WriteFile(filepath.Join(dot, "p.json"), []byte(`{"mine":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(b.ConfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dot, b.abs("profiles")); err != nil {
		t.Fatal(err)
	}
	res := b.pull(StoreConfig, PullOptions{})
	got, _ := os.ReadFile(filepath.Join(dot, "p.json"))
	if string(got) != `{"mine":1}` || len(res.Linked) != 1 {
		t.Fatalf("local edit lost: now %s, trashed=%v, first=%+v", got, res.Trashed, res.First)
	}
}

// Same dir symlinked after a sync: commit deletes every profile on the server.
func TestCommit_symlinkedDirIsNoDelete(t *testing.T) {
	srv := cloudtest.New(t)
	a := newMachine(t, srv, "tok-a")
	a.write("profiles/p.json", `{"x":1}`)
	a.commit(StoreConfig, "one")
	dot := t.TempDir()
	if err := os.Rename(a.abs("profiles"), filepath.Join(dot, "profiles")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dot, "profiles"), a.abs("profiles")); err != nil {
		t.Fatal(err)
	}
	a.commit(StoreConfig, "two")
	if _, ok := srv.Content("config", "profiles/p.json"); !ok {
		t.Fatal("moving profiles/ behind a symlink deleted it on the server")
	}
}

// A conversation the server holds as invalid JSON, changed here too, fails
// every pull forever: the cursor never moves past it.
func TestPull_brokenRemoteChatDoesNotStickThePull(t *testing.T) {
	srv := cloudtest.New(t)
	a := newMachine(t, srv, "tok-a")
	a.write("conversations/c.json", `{"messages":[{"role":"user","content":"hi"}]}`)
	a.commit(StoreConversations, "one")
	b := newMachine(t, srv, "tok-b")
	b.pull(StoreConversations, PullOptions{})
	b.write("conversations/c.json", `{"messages":[{"role":"user","content":"b"}]}`)
	srv.Put("conversations", "conversations/c.json", `{"messages":[{"role":"user","content":"tru`)
	srv.Put("conversations", "conversations/other.json", `{"messages":[]}`)
	for range 2 {
		if _, err := b.Pull(context.Background(), StoreConversations, PullOptions{Mode: ModeConflicts}); err != nil {
			t.Fatalf("pull: %v", err)
		}
	}
}

// Same messages, both sides retitled: theirs overwrites mine, no trash.
func TestPull_sameMessagesOtherTitleTrashesMine(t *testing.T) {
	srv := cloudtest.New(t)
	a := newMachine(t, srv, "tok-a")
	a.write("conversations/c.json", `{"title":"orig","messages":[{"role":"user","content":"hi"}]}`)
	a.commit(StoreConversations, "one")
	b := newMachine(t, srv, "tok-b")
	b.pull(StoreConversations, PullOptions{})
	b.write("conversations/c.json", `{"title":"my rename","messages":[{"role":"user","content":"hi"}]}`)
	srv.Put("conversations", "conversations/c.json", `{"title":"their rename","messages":[{"role":"user","content":"hi"}]}`)
	res := b.pull(StoreConversations, PullOptions{})
	if !strings.Contains(b.read("conversations/c.json"), "my rename") && len(res.Trashed) == 0 {
		t.Fatalf("local title lost and not trashed: %s", b.read("conversations/c.json"))
	}
}

// A blob too large for a bundle never counts toward Done.
func TestPull_progressCountsBlobsFetchedAlone(t *testing.T) {
	srv := cloudtest.New(t)
	a := newMachine(t, srv, "tok-a")
	a.write("conversations/big.json", `{"messages":[],"pad":"`+strings.Repeat("x", 13<<20)+`"}`)
	a.write("conversations/small.json", `{"messages":[]}`)
	a.commit(StoreConversations, "one")
	b := newMachine(t, srv, "tok-b")
	var log progressLog
	b.Progress = log.add
	b.pull(StoreConversations, PullOptions{})
	last, _ := log.last(PhaseDownloading)
	if last.Done != last.Total || last.Bytes != last.TotalBytes {
		t.Fatalf("last download event = %+v", last)
	}
}

// A bundle frame announcing a huge size crashes the process instead of
// returning a BundleError.
func TestFetchBlobs_boundsAFrame(t *testing.T) {
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		zw := gzip.NewWriter(w)
		fmt.Fprintf(zw, "%s %d\n", strings.Repeat("a", 64), int64(1)<<50)
		zw.Close()
	}))
	defer hs.Close()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panic: %v", r)
		}
	}()
	_, err := api{c: cloud.NewClient(hs.URL, "x")}.fetchBlobs(context.Background(), []string{strings.Repeat("a", 64)})
	var be *BundleError
	if !errors.As(err, &be) {
		t.Fatalf("err = %v, want a BundleError", err)
	}
}

func TestFindSecret_commonShapes(t *testing.T) {
	for _, c := range []string{
		`{"command":"npx","args":["-y","srv","--api-key","q7Hk2LmP9xRt4VwZ8nB3cD6fG1jK5sQ0"]}`,
		`{"url":"https://mcp.example.com/sse?token=q7Hk2LmP9xRt4VwZ8nB3cD6fG1jK5sQ0"}`,
		`{"headers":{"Cookie":"session=q7Hk2LmP9xRt4VwZ8nB3cD6fG1jK5sQ0"}}`,
		`{"GITHUBTOKEN":"q7Hk2LmP9xRt4"}`,
		`{"note":"hf_q7Hk2LmP9xRt4VwZ8nB3cD6fG1jK5sQ0ab"}`,
		`{"apiKey":"https://u:pass@x"}`,
	} {
		if _, found := FindSecret("mcpServers/x.json", []byte(c)); !found {
			t.Errorf("not caught: %s", c)
		}
	}
}
