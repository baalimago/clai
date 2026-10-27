package sync

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"os"
	stdsync "sync"

	"github.com/baalimago/clai/internal/cloud"
)

// Phase is the step of a pull or commit a Progress reports.
type Phase string

const (
	PhaseScanning    Phase = "scanning"
	PhaseHashing     Phase = "hashing"
	PhaseExporting   Phase = "exporting"
	PhaseDiscovering Phase = "discovering"
	PhaseUploading   Phase = "uploading"
	PhaseCommitting  Phase = "committing"
	PhaseDownloading Phase = "downloading"
	PhaseIndexing    Phase = "indexing"
)

// Progress is one step forward of a pull, commit or status. Total and
// TotalBytes are 0 while the total is unknown, as it is while scanning.
// Hashing counts only the files whose bytes must be read; Reused counts
// the ones whose hash the last run left and whose size and mod time have
// not moved. Downloading's Have counts the files this machine already has
// as the server does, and Copies the files whose bytes another download
// brings.
type Progress struct {
	Store      Store
	Phase      Phase
	Done       int
	Total      int
	Bytes      int64
	TotalBytes int64
	Reused     int
	Have       int
	Copies     int
}

func (e *Engine) report(p Progress) {
	if e.Progress != nil {
		e.Progress(p)
	}
}

func (e *Engine) scanned(store Store) func(Progress) {
	if e.Progress == nil {
		return nil
	}
	return func(p Progress) {
		p.Store = store
		e.report(p)
	}
}

// BillingReason is why the account may not commit conversations now.
type BillingReason string

const (
	CardRequired   BillingReason = "card_required"
	PaymentOverdue BillingReason = "payment_overdue"
	BillingNotOpen BillingReason = "billing_not_open"
)

// BillingBlockedError is a conversation commit the account's billing
// refuses, found before anything was uploaded.
type BillingBlockedError struct {
	Reason BillingReason
	URL    string
}

func (e *BillingBlockedError) Error() string {
	switch e.Reason {
	case PaymentOverdue:
		return fmt.Sprintf("a payment has been due for 7 days, so commits are refused; pay it at %s", e.URL)
	case BillingNotOpen:
		return "conversation sync is not open yet; config sync works now"
	}
	return fmt.Sprintf("syncing conversations needs a card on file ($1 per started GB a month); add one at %s", e.URL)
}

// checkBilling asks the server whether this account may grow its
// conversations, so a refused commit costs no upload.
func (e *Engine) checkBilling(ctx context.Context, store Store, changes []LocalChange) error {
	grows := false
	for _, c := range changes {
		if c.Kind != Deleted {
			grows = true
			break
		}
	}
	if store != StoreConversations || !grows {
		return nil
	}
	me, err := e.Client.Me(ctx)
	if err != nil {
		return fmt.Errorf("check billing: %w", err)
	}
	switch {
	case me.Usage.Overdue:
		return &BillingBlockedError{Reason: PaymentOverdue, URL: me.Usage.BillingURL}
	case !me.Usage.CardOnFile && !me.Usage.BillingOpen:
		return &BillingBlockedError{Reason: BillingNotOpen, URL: me.Usage.BillingURL}
	case !me.Usage.CardOnFile:
		return &BillingBlockedError{Reason: CardRequired, URL: me.Usage.BillingURL}
	}
	return nil
}

const (
	// uploadWorkers is how many requests go up at once.
	uploadWorkers = 8
	// batchBlobMax is the largest file that goes up in a batch; a larger
	// one goes alone.
	batchBlobMax = 512 << 10
	// batchMaxBlobs and batchMaxBytes stay under clai-cloud's
	// BATCH_MAX_BLOBS and BATCH_MAX_BYTES (8 MiB), with room for the
	// multipart framing.
	batchMaxBlobs = 250
	batchMaxBytes = 6 << 20
)

type uploadJob struct {
	sha, path string
	size      int64
}

// BlobRefusedError is a file the server would not store, such as bytes
// that do not hash to their name.
type BlobRefusedError struct {
	Path    string
	Outcome string
}

func (e *BlobRefusedError) Error() string {
	return fmt.Sprintf("upload %s: the server answered %s", e.Path, e.Outcome)
}

func packUploads(jobs []uploadJob) [][]uploadJob {
	return pack(jobs, func(j uploadJob) int64 { return j.size }, packLimits{count: batchMaxBlobs, bytes: batchMaxBytes, alone: batchBlobMax})
}

func packBytes(pack []uploadJob) int64 {
	var n int64
	for _, j := range pack {
		n += j.size
	}
	return n
}

// uploadMissing sends jobs, packed into batches, on uploadWorkers
// goroutines. The first error cancels the rest and is returned.
func (e *Engine) uploadMissing(ctx context.Context, store Store, jobs []uploadJob) (int, error) {
	total := packBytes(jobs)
	packs := packUploads(jobs)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	queue := make(chan []uploadJob)
	errs := make(chan error, uploadWorkers)
	var mu stdsync.Mutex
	done, sent := 0, int64(0)
	var wg stdsync.WaitGroup
	for range min(uploadWorkers, len(packs)) {
		wg.Go(func() {
			for pack := range queue {
				if err := e.uploadPack(ctx, pack); err != nil {
					errs <- err
					cancel()
					return
				}
				mu.Lock()
				done += len(pack)
				sent += packBytes(pack)
				// Under the lock, so the reports arrive in order.
				e.report(Progress{Store: store, Phase: PhaseUploading, Done: done, Total: len(jobs), Bytes: sent, TotalBytes: total})
				mu.Unlock()
			}
		})
	}
feed:
	for _, p := range packs {
		select {
		case queue <- p:
		case <-ctx.Done():
			break feed
		}
	}
	close(queue)
	wg.Wait()
	close(errs)
	if err, ok := <-errs; ok {
		return done, err
	}
	if err := ctx.Err(); err != nil && done < len(jobs) {
		return done, err
	}
	return done, nil
}

func (e *Engine) readJob(j uploadJob) (batchBlob, error) {
	b, err := os.ReadFile(e.abs(j.path))
	if err != nil {
		return batchBlob{}, err
	}
	if hashBytes(b) != j.sha {
		return batchBlob{}, &ChangedDuringCommitError{Path: j.path}
	}
	return batchBlob{sha: j.sha, data: b}, nil
}

// uploadPack sends a batch in one request, or one by one to a server that
// has no batch endpoint.
func (e *Engine) uploadPack(ctx context.Context, pack []uploadJob) error {
	blobs := make([]batchBlob, 0, len(pack))
	for _, j := range pack {
		b, err := e.readJob(j)
		if err != nil {
			return err
		}
		blobs = append(blobs, b)
	}
	if len(blobs) > 1 {
		results, err := e.api().putBlobs(ctx, blobs)
		if apiErr, ok := cloud.AsAPIError(err); !ok || apiErr.Status != http.StatusNotFound {
			if err != nil {
				return err
			}
			for _, j := range pack {
				if r := results[j.sha]; r != "stored" && r != "exists" {
					return &BlobRefusedError{Path: j.path, Outcome: cmp.Or(r, "nothing")}
				}
			}
			return nil
		}
	}
	for _, b := range blobs {
		if err := e.api().putBlob(ctx, b.sha, b.data); err != nil {
			return err
		}
	}
	return nil
}
