package sync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"testing"

	"github.com/baalimago/clai/internal/cloud/cloudtest"
)

func TestSummarize_fromHashesWithoutFetching(t *testing.T) {
	srv := cloudtest.New(t)
	a, b := newMachine(t, srv, "tok-a"), newMachine(t, srv, "tok-b")
	for i := range 50 {
		a.write(fmt.Sprintf("conversations/c%02d.json", i), `{"messages":[]}`)
	}
	a.commit(StoreConversations, "base")
	a.write("conversations/c00.json", `{"messages":[{"role":"user","content":"hi"}]}`)
	a.write("conversations/new.json", `{"messages":[]}`)
	if err := os.Remove(a.abs("conversations/c01.json")); err != nil {
		t.Fatal(err)
	}
	b.pull(StoreConversations, PullOptions{})
	b.write("conversations/c00.json", `{"messages":[{"role":"user","content":"there"}]}`)
	b.write("conversations/c02.json", `{"messages":[{"role":"user","content":"there"}]}`)
	b.write("conversations/other.json", `{"messages":[]}`)
	b.commit(StoreConversations, "b")

	unlock := srv.Lock()
	clear(srv.Requests)
	unlock()
	sum, err := a.Summarize(context.Background(), StoreConversations, true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(sum.Added, []string{"conversations/new.json"}) || !slices.Equal(sum.Modified, []string{"conversations/c00.json"}) || !slices.Equal(sum.Deleted, []string{"conversations/c01.json"}) {
		t.Fatalf("local side: %+v", sum)
	}
	rs := sum.Remote
	if rs == nil || !slices.Equal(rs.Added, []string{"conversations/other.json"}) || !slices.Equal(rs.Modified, []string{"conversations/c00.json", "conversations/c02.json"}) || !slices.Equal(rs.Both, []string{"conversations/c00.json"}) {
		t.Fatalf("remote side: %+v", rs)
	}
	unlock = srv.Lock()
	defer unlock()
	for path, n := range srv.Requests {
		if path != "/api/v1/stores/conversations/changes" {
			t.Errorf("summary asked %s %d times", path, n)
		}
	}
}

func TestDiffs_listsTheServerOncePerStore(t *testing.T) {
	srv := cloudtest.New(t)
	a := newMachine(t, srv, "tok-a")
	a.write("profiles/x.json", "x\n")
	a.write("profiles/y.json", "y\n")
	a.commit(StoreConfig, "base")
	a.write("profiles/x.json", "x2\n")
	a.write("profiles/y.json", "y2\n")
	unlock := srv.Lock()
	clear(srv.Requests)
	unlock()
	var got []string
	err := a.Diffs(context.Background(), []string{"profiles/x.json", "profiles/y.json"}, true, func(d FileDiff) error {
		got = append(got, d.Path)
		return nil
	})
	if err != nil || len(got) != 2 {
		t.Fatalf("diffs %v: %v", got, err)
	}
	unlock = srv.Lock()
	defer unlock()
	if n := srv.Requests["/api/v1/stores/config/changes"]; n != 1 {
		t.Fatalf("listed the server %d times", n)
	}
}

func TestPlan_stopsWhenCancelled(t *testing.T) {
	srv := cloudtest.New(t)
	a := newMachine(t, srv, "tok-a")
	for i := range 20 {
		a.write(fmt.Sprintf("conversations/c%02d.json", i), `{"messages":[]}`)
	}
	ctx, cancel := context.WithCancel(context.Background())
	hashed := 0
	a.Progress = func(p Progress) {
		if p.Phase == PhaseHashing && p.Done > 0 {
			hashed = p.Done
			cancel()
		}
	}
	_, err := a.Plan(ctx, StoreConversations, PlanOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("plan after cancel: %v", err)
	}
	if hashed != 1 {
		t.Fatalf("hashed %d files after the cancel", hashed)
	}
	if _, err := a.Summarize(ctx, StoreConversations, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("summary after cancel: %v", err)
	}
}
