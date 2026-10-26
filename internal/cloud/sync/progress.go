package sync

import (
	"context"
	"fmt"
	"os"
	stdsync "sync"
)

// Phase is the step of a pull or commit a Progress reports.
type Phase string

const (
	PhaseScanning    Phase = "scanning"
	PhaseHashing     Phase = "hashing"
	PhaseExporting   Phase = "exporting"
	PhaseUploading   Phase = "uploading"
	PhaseCommitting  Phase = "committing"
	PhaseDownloading Phase = "downloading"
)

// Progress is one step forward of a pull, commit or status. Total and
// TotalBytes are 0 while the total is unknown, as it is while scanning.
// Hashing counts only the files whose bytes must be read; Reused counts
// the ones whose hash the last run left and whose size and mod time have
// not moved.
type Progress struct {
	Store      Store
	Phase      Phase
	Done       int
	Total      int
	Bytes      int64
	TotalBytes int64
	Reused     int
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

// uploadWorkers is how many blobs go up at once.
const uploadWorkers = 6

type uploadJob struct {
	sha, path string
	size      int64
}

// uploadMissing sends jobs on uploadWorkers goroutines. The first error
// cancels the rest and is returned.
func (e *Engine) uploadMissing(ctx context.Context, store Store, jobs []uploadJob) (int, error) {
	var total int64
	for _, j := range jobs {
		total += j.size
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	queue := make(chan uploadJob)
	errs := make(chan error, uploadWorkers)
	var mu stdsync.Mutex
	done, sent := 0, int64(0)
	var wg stdsync.WaitGroup
	for range min(uploadWorkers, len(jobs)) {
		wg.Go(func() {
			for j := range queue {
				if err := e.uploadOne(ctx, j); err != nil {
					errs <- err
					cancel()
					return
				}
				mu.Lock()
				done++
				sent += j.size
				// Under the lock, so the reports arrive in order.
				e.report(Progress{Store: store, Phase: PhaseUploading, Done: done, Total: len(jobs), Bytes: sent, TotalBytes: total})
				mu.Unlock()
			}
		})
	}
feed:
	for _, j := range jobs {
		select {
		case queue <- j:
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

func (e *Engine) uploadOne(ctx context.Context, j uploadJob) error {
	b, err := os.ReadFile(e.abs(j.path))
	if err != nil {
		return err
	}
	if hashBytes(b) != j.sha {
		return &ChangedDuringCommitError{Path: j.path}
	}
	return e.api().putBlob(ctx, j.sha, b)
}
