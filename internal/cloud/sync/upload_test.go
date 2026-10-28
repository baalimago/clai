package sync

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/cloud/cloudtest"
)

var singleBlob = regexp.MustCompile(`^/api/v1/blobs/[0-9a-f]{64}$`)

func singlePuts(srv *cloudtest.Server) int {
	unlock := srv.Lock()
	defer unlock()
	n := 0
	for path, count := range srv.Requests {
		if singleBlob.MatchString(path) {
			n += count
		}
	}
	return n
}

func heldBlobs(srv *cloudtest.Server) int {
	unlock := srv.Lock()
	defer unlock()
	return len(srv.Blobs)
}

func TestCommit_uploadsSmallFilesInBatchesAndLargeOnesAlone(t *testing.T) {
	srv := cloudtest.New(t)
	m := newMachine(t, srv, "tok-a")
	m.Engine.limits = limits{batchBlobs: 5, batchAlone: 2 << 10}
	var log progressLog
	m.Progress = log.add
	for i := range 12 {
		m.write(fmt.Sprintf("conversations/c%03d.json", i), fmt.Sprintf(`{"n":%d}`, i))
	}
	m.write("conversations/big.json", strings.Repeat("x", 2<<10+1))
	res := m.commit(StoreConversations, "bulk")
	if res.Uploaded != 13 || heldBlobs(srv) != 13 {
		t.Fatalf("uploaded %d, server holds %d, want 13", res.Uploaded, heldBlobs(srv))
	}
	if n := requests(srv, "/api/v1/blobs/batch"); n != 3 {
		t.Fatalf("%d batch requests, want 3 batches of at most 5 for 12 small files", n)
	}
	if n := singlePuts(srv); n != 1 {
		t.Fatalf("%d single uploads, want only the large file", n)
	}
	up, _ := log.last(PhaseUploading)
	if up.Done != 13 || up.Total != 13 || up.Bytes != up.TotalBytes {
		t.Fatalf("last upload event = %+v", up)
	}
}

func TestCommit_uploadsOneByOneToAServerWithoutBatches(t *testing.T) {
	srv := cloudtest.New(t)
	unlock := srv.Lock()
	srv.NoBatch = true
	unlock()
	m := newMachine(t, srv, "tok-a")
	for i := range 20 {
		m.write(fmt.Sprintf("conversations/c%02d.json", i), fmt.Sprintf(`{"n":%d}`, i))
	}
	if res := m.commit(StoreConversations, "old server"); res.Uploaded != 20 {
		t.Fatalf("uploaded %d", res.Uploaded)
	}
	if n := singlePuts(srv); n != 20 || heldBlobs(srv) != 20 {
		t.Fatalf("%d single uploads, %d held", n, heldBlobs(srv))
	}
}

func TestCommit_aBlobTheBatchRefusesStopsTheCommit(t *testing.T) {
	srv := cloudtest.New(t)
	unlock := srv.Lock()
	srv.BatchOutcome = "mismatch"
	unlock()
	m := newMachine(t, srv, "tok-a")
	m.write("conversations/a.json", `{"a":1}`)
	m.write("conversations/b.json", `{"b":1}`)
	_, err := m.Commit(context.Background(), StoreConversations, CommitOptions{Message: "x"})
	if err == nil || !strings.Contains(err.Error(), "conversations/") || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("err = %v, want the refused file and why", err)
	}
	if n := requests(srv, "/api/v1/stores/conversations/commit"); n != 0 {
		t.Fatalf("committed %d times after a refused upload", n)
	}
}
