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
	bulkConversations(t, srv, 250)
	b := newMachine(t, srv, "tok-b")
	var log progressLog
	b.Progress = log.add
	if _, err := b.Pull(context.Background(), StoreConversations, PullOptions{}); err != nil {
		t.Fatal(err)
	}
	if b.read("conversations/c249.json") != `{"n":249}` {
		t.Fatal("a pulled file differs")
	}
	if n := requests(srv, "/api/v1/blobs/fetch"); n != 3 {
		t.Fatalf("%d fetch requests, want 3 bundles for 250 files", n)
	}
	if n := singleGets(srv); n != 0 {
		t.Fatalf("%d single downloads, want none", n)
	}
	last, _ := log.last(PhaseDownloading)
	if last.Done != 250 || last.Total != 250 || last.Bytes == 0 || last.Bytes != last.TotalBytes {
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
