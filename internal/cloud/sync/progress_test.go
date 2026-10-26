package sync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	stdsync "sync"
	"testing"

	"github.com/baalimago/clai/internal/cloud/cloudtest"
)

type progressLog struct {
	mu     stdsync.Mutex
	events []Progress
}

func (l *progressLog) add(p Progress) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, p)
}

func (l *progressLog) last(phase Phase) (Progress, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out Progress
	n := 0
	for _, p := range l.events {
		if p.Phase == phase {
			out = p
			n++
		}
	}
	return out, n
}

func TestCommit_reportsScanUploadAndCommit(t *testing.T) {
	srv := cloudtest.New(t)
	m := newMachine(t, srv, "tok-a")
	var log progressLog
	m.Progress = log.add
	for i := range 12 {
		m.write(fmt.Sprintf("profiles/p%02d.json", i), fmt.Sprintf(`{"prompt":"%d"}`, i))
	}
	res := m.commit(StoreConfig, "many")
	if res.Uploaded != 12 {
		t.Fatalf("uploaded %d, want 12", res.Uploaded)
	}
	scan, scans := log.last(PhaseScanning)
	if scans == 0 || scan.Done != 12 || scan.Store != StoreConfig {
		t.Fatalf("last scan event = %+v (%d events)", scan, scans)
	}
	up, ups := log.last(PhaseUploading)
	if ups < 12 || up.Done != 12 || up.Total != 12 || up.Bytes != up.TotalBytes || up.TotalBytes != res.Bytes {
		t.Fatalf("last upload event = %+v (%d events), commit bytes %d", up, ups, res.Bytes)
	}
	if _, n := log.last(PhaseCommitting); n == 0 {
		t.Fatal("no committing event")
	}
}

func TestCommit_uploadFailureStopsEveryUpload(t *testing.T) {
	srv := cloudtest.New(t)
	m := newMachine(t, srv, "tok-a")
	for i := range 20 {
		m.write(fmt.Sprintf("profiles/p%02d.json", i), fmt.Sprintf("%d", i))
	}
	unlock := srv.Lock()
	srv.RefuseBlobs = &cloudtest.Problem{Status: 500, Code: "boom", Message: "storage down"}
	unlock()
	_, err := m.Commit(context.Background(), StoreConfig, CommitOptions{Message: "x"})
	if err == nil || !strings.Contains(err.Error(), "storage down") {
		t.Fatalf("err = %v, want the server's upload refusal", err)
	}
	if n := requests(srv, "/api/v1/stores/config/commit"); n != 0 {
		t.Fatalf("committed %d times after a failed upload", n)
	}
}

func TestCommit_conversationsWithoutACardUploadNothing(t *testing.T) {
	srv := cloudtest.New(t)
	setBilling(srv, false, false)
	m := newMachine(t, srv, "tok-a")
	m.write("conversations/abc.json", `{"messages":[]}`)
	_, err := m.Commit(context.Background(), StoreConversations, CommitOptions{Message: "x"})
	var blocked *BillingBlockedError
	if !errors.As(err, &blocked) || blocked.Reason != CardRequired || blocked.URL == "" {
		t.Fatalf("err = %v, want a BillingBlockedError asking for a card", err)
	}
	if n := requests(srv, "/api/v1/blobs/missing"); n != 0 {
		t.Fatalf("checked blobs %d times before learning there is no card", n)
	}

	setBilling(srv, true, true)
	_, err = m.Commit(context.Background(), StoreConversations, CommitOptions{Message: "x"})
	if !errors.As(err, &blocked) || blocked.Reason != PaymentOverdue {
		t.Fatalf("err = %v, want a BillingBlockedError for an overdue payment", err)
	}

	setBilling(srv, false, false)
	unlock := srv.Lock()
	srv.BillingClosed = true
	unlock()
	_, err = m.Commit(context.Background(), StoreConversations, CommitOptions{Message: "x"})
	if !errors.As(err, &blocked) || blocked.Reason != BillingNotOpen || !strings.Contains(err.Error(), "not open") {
		t.Fatalf("err = %v, want a BillingBlockedError saying billing is not open", err)
	}
	unlock = srv.Lock()
	srv.BillingClosed = false
	unlock()

	// Deleting conversations never needs a card.
	setBilling(srv, true, false)
	m.commit(StoreConversations, "keep")
	setBilling(srv, false, false)
	if err := os.Remove(m.abs("conversations/abc.json")); err != nil {
		t.Fatal(err)
	}
	if res := m.commit(StoreConversations, "drop"); res.Files != 1 {
		t.Fatalf("delete commit = %+v", res)
	}
}

func TestPull_reportsDownloads(t *testing.T) {
	srv := cloudtest.New(t)
	for i := range 5 {
		srv.Put("config", fmt.Sprintf("profiles/p%d.json", i), fmt.Sprintf("%d", i))
	}
	m := newMachine(t, srv, "tok-a")
	var log progressLog
	m.Progress = log.add
	m.pull(StoreConfig, PullOptions{})
	down, n := log.last(PhaseDownloading)
	if n == 0 || down.Done != 5 || down.Total != 5 {
		t.Fatalf("last download event = %+v (%d events)", down, n)
	}
}

func setBilling(srv *cloudtest.Server, card, overdue bool) {
	unlock := srv.Lock()
	defer unlock()
	srv.CardOnFile, srv.Overdue = card, overdue
}

func requests(srv *cloudtest.Server, path string) int {
	unlock := srv.Lock()
	defer unlock()
	return srv.Requests[path]
}

func TestScan_reportsHashingAgainstAKnownTotal(t *testing.T) {
	srv := cloudtest.New(t)
	m := newMachine(t, srv, "tok-a")
	for i := range 5 {
		m.write(fmt.Sprintf("profiles/p%d.json", i), fmt.Sprintf(`{"prompt":"%d"}`, i))
	}
	var log progressLog
	m.Progress = log.add
	if _, err := m.Status(context.Background(), StoreConfig, PlanOptions{}); err != nil {
		t.Fatal(err)
	}
	hashed, n := log.last(PhaseHashing)
	if n == 0 || hashed.Done != 5 || hashed.Total != 5 || hashed.Bytes != hashed.TotalBytes || hashed.TotalBytes == 0 || hashed.Reused != 0 {
		t.Fatalf("last hashing event = %+v (%d events)", hashed, n)
	}
	found, _ := log.last(PhaseScanning)
	if found.Done != 5 {
		t.Fatalf("last scanning event = %+v", found)
	}

	// The second look reuses every hash: nothing to read again.
	var again progressLog
	m.Progress = again.add
	if _, err := m.Status(context.Background(), StoreConfig, PlanOptions{}); err != nil {
		t.Fatal(err)
	}
	if hashed, _ := again.last(PhaseHashing); hashed.Total != 0 || hashed.Reused != 5 {
		t.Fatalf("second run hashing = %+v, want nothing to hash and 5 reused", hashed)
	}
}

func TestCommit_keepsItsHashesWhenRefused(t *testing.T) {
	srv := cloudtest.New(t)
	setBilling(srv, false, false)
	m := newMachine(t, srv, "tok-a")
	for i := range 4 {
		m.write(fmt.Sprintf("conversations/c%d.json", i), `{"messages":[]}`)
	}
	if _, err := m.Commit(context.Background(), StoreConversations, CommitOptions{Message: "x"}); err == nil {
		t.Fatal("commit without a card succeeded")
	}
	var log progressLog
	m.Progress = log.add
	_, _ = m.Commit(context.Background(), StoreConversations, CommitOptions{Message: "x"})
	if hashed, _ := log.last(PhaseHashing); hashed.Total != 0 || hashed.Reused != 4 {
		t.Fatalf("second commit hashing = %+v, want every hash reused", hashed)
	}
}

func TestStatus_namesWhatIsUploadedButNotCommitted(t *testing.T) {
	srv := cloudtest.New(t)
	m := newMachine(t, srv, "tok-a")
	m.write("profiles/a.json", `{"prompt":"a"}`)
	m.write("profiles/b.json", `{"prompt":"b"}`)
	unlock := srv.Lock()
	srv.Refuse = &cloudtest.Problem{Status: 503, Code: "busy", Message: "try again"}
	unlock()
	if _, err := m.Commit(context.Background(), StoreConfig, CommitOptions{Message: "x"}); err == nil {
		t.Fatal("refused commit succeeded")
	}
	unlock = srv.Lock()
	srv.Refuse = nil
	unlock()
	m.write("profiles/c.json", `{"prompt":"c"}`)
	st, err := m.Status(context.Background(), StoreConfig, PlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if st.Remote == nil || strings.Join(st.Remote.Uploaded, ",") != "profiles/a.json,profiles/b.json" {
		t.Fatalf("uploaded = %+v, want a and b (c was never sent)", st.Remote)
	}
}

func TestCommit_saysWhileItExportsOtherTools(t *testing.T) {
	srv := cloudtest.New(t)
	m := newMachine(t, srv, "tok-a")
	var log progressLog
	m.Progress = log.add
	exported := false
	m.Hooks.Export = func(context.Context, string) error {
		_, n := log.last(PhaseExporting)
		exported = n == 1
		return nil
	}
	_, _ = m.Commit(context.Background(), StoreConversations, CommitOptions{Message: "x", PlanOptions: PlanOptions{Imports: true}})
	if !exported {
		t.Fatal("no exporting event before the export ran")
	}
}
