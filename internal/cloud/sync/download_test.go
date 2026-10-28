package sync

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/cloud/cloudtest"
)

func singleGets(srv *cloudtest.Server) int { return singlePuts(srv) }

func bulkConversations(t *testing.T, srv *cloudtest.Server, n int) machine {
	t.Helper()
	a := newMachine(t, srv, "tok-a")
	for i := range n {
		a.write(fmt.Sprintf("conversations/c%03d.json", i), fmt.Sprintf(`{"n":%d}`, i))
	}
	a.commit(StoreConversations, "bulk")
	unlock := srv.Lock()
	clear(srv.Requests)
	unlock()
	return a
}

func TestPull_downloadsInBundles(t *testing.T) {
	srv := cloudtest.New(t)
	bulkConversations(t, srv, 12)
	b := newMachine(t, srv, "tok-b")
	b.Engine.limits = limits{fetchBlobs: 5}
	var log progressLog
	b.Progress = log.add
	if _, err := b.Pull(context.Background(), StoreConversations, PullOptions{}); err != nil {
		t.Fatal(err)
	}
	if b.read("conversations/c011.json") != `{"n":11}` {
		t.Fatal("a pulled file differs")
	}
	if n := requests(srv, "/api/v1/blobs/fetch"); n != 3 {
		t.Fatalf("%d fetch requests, want 3 bundles of at most 5 for 12 files", n)
	}
	if n := singleGets(srv); n != 0 {
		t.Fatalf("%d single downloads, want none", n)
	}
	last, _ := log.last(PhaseDownloading)
	if last.Done != 12 || last.Total != 12 || last.Bytes == 0 || last.Bytes != last.TotalBytes {
		t.Fatalf("last download event = %+v", last)
	}
}

func TestPull_fromAServerWithoutBundlesDownloadsOneByOne(t *testing.T) {
	srv := cloudtest.New(t)
	bulkConversations(t, srv, 20)
	unlock := srv.Lock()
	srv.NoBatch = true
	unlock()
	b := newMachine(t, srv, "tok-b")
	if _, err := b.Pull(context.Background(), StoreConversations, PullOptions{}); err != nil {
		t.Fatal(err)
	}
	if n := singleGets(srv); n != 20 || !b.exists("conversations/c019.json") {
		t.Fatalf("%d single downloads, want 20", n)
	}
}

func TestPull_aBundleWithWrongBytesIsRefused(t *testing.T) {
	srv := cloudtest.New(t)
	bulkConversations(t, srv, 3)
	unlock := srv.Lock()
	srv.CorruptFetch = true
	unlock()
	b := newMachine(t, srv, "tok-b")
	_, err := b.Pull(context.Background(), StoreConversations, PullOptions{})
	var sum *ChecksumError
	if !errors.As(err, &sum) || !strings.HasPrefix(sum.Path, "conversations/") {
		t.Fatalf("err = %v, want a ChecksumError naming the file", err)
	}
	if b.exists("conversations/c000.json") {
		t.Fatal("a corrupt bundle wrote a file")
	}
}

func TestCommit_gzipsItsBatches(t *testing.T) {
	srv := cloudtest.New(t)
	bulkConversations(t, srv, 10)
	unlock := srv.Lock()
	defer unlock()
	if srv.GzippedBatches != 1 {
		t.Fatalf("%d gzipped batches, want 1", srv.GzippedBatches)
	}
}

func TestPull_countsWhatIsAlreadyHereAndCopies(t *testing.T) {
	srv := cloudtest.New(t)
	a := newMachine(t, srv, "tok-a")
	for i := range 4 {
		a.write(fmt.Sprintf("conversations/c%d.json", i), fmt.Sprintf(`{"n":%d}`, i))
	}
	a.write("conversations/copy.json", `{"n":3}`)
	a.commit(StoreConversations, "bulk")
	b := newMachine(t, srv, "tok-b")
	b.write("conversations/c0.json", `{"n":0}`)
	b.write("conversations/c1.json", `{"n":1}`)
	unlock := srv.Lock()
	clear(srv.Requests)
	unlock()
	var log progressLog
	b.Progress = log.add
	if _, err := b.Pull(context.Background(), StoreConversations, PullOptions{}); err != nil {
		t.Fatal(err)
	}
	last, _ := log.last(PhaseDownloading)
	if last.Done != 2 || last.Total != 2 || last.Have != 2 || last.Copies != 1 {
		t.Fatalf("last download event = %+v, want 2 downloaded, 2 already here, 1 copy", last)
	}
	if b.read("conversations/copy.json") != `{"n":3}` || b.read("conversations/c3.json") != `{"n":3}` {
		t.Fatal("a copy was not written")
	}
	if n := singleGets(srv); n != 0 {
		t.Fatalf("%d single downloads, want none: a copy reuses its bundled bytes", n)
	}
}

func TestPull_reportsIndexingThePulledChats(t *testing.T) {
	srv := cloudtest.New(t)
	bulkConversations(t, srv, 3)
	b := newMachine(t, srv, "tok-b")
	var log progressLog
	b.Progress = log.add
	b.Hooks.Pulled = func(_ string, written, _ []string, progress func(done, total int)) error {
		progress(len(written), len(written))
		return nil
	}
	if _, err := b.Pull(context.Background(), StoreConversations, PullOptions{}); err != nil {
		t.Fatal(err)
	}
	last, n := log.last(PhaseIndexing)
	if n == 0 || last.Done != 3 || last.Total != 3 {
		t.Fatalf("last indexing event = %+v (%d events), want 3/3", last, n)
	}
}

func TestPull_appliesOneWindowWhileTheNextDownloads(t *testing.T) {
	srv := cloudtest.New(t)
	bulkConversations(t, srv, 40)
	b := newMachine(t, srv, "tok-b")
	b.window = 50
	if _, err := b.Pull(context.Background(), StoreConversations, PullOptions{}); err != nil {
		t.Fatal(err)
	}
	for i := range 40 {
		if b.read(fmt.Sprintf("conversations/c%03d.json", i)) != fmt.Sprintf(`{"n":%d}`, i) {
			t.Fatalf("c%03d differs", i)
		}
	}
	if n := requests(srv, "/api/v1/blobs/fetch"); n < 5 {
		t.Fatalf("%d fetch requests, want one per small window", n)
	}
	if n := singleGets(srv); n != 0 {
		t.Fatalf("%d single downloads, want none", n)
	}
}

func TestPull_aFailedWindowStopsThePull(t *testing.T) {
	srv := cloudtest.New(t)
	bulkConversations(t, srv, 40)
	unlock := srv.Lock()
	srv.CorruptFetch = true
	unlock()
	b := newMachine(t, srv, "tok-b")
	b.window = 50
	_, err := b.Pull(context.Background(), StoreConversations, PullOptions{})
	var sum *ChecksumError
	if !errors.As(err, &sum) {
		t.Fatalf("err = %v, want a ChecksumError", err)
	}
}
