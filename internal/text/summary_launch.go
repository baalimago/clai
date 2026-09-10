package text

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"syscall"
	"time"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

const defaultSummaryJoinTimeout = 5 * time.Second

type summaryResult struct {
	summary models.Summary
	err     error
}

// summaryRun is the in-flight state of one launched summarizer.
type summaryRun struct {
	result    chan summaryResult
	cancel    context.CancelFunc
	interrupt <-chan struct{}
	release   func()
}

func (q *Querier[C]) shouldLaunchSummary() bool {
	return q.summarizer != nil && q.summarizeConversations && q.shouldSaveReply && q.chat.Summary == ""
}

// launchSummary starts the summarizer alongside the main call on a context
// detached from the run's cancellation (D17, D27); the launcher keeps its own
// cancel to abandon the run. A panic in Summarize becomes an error (D26).
func (q *Querier[C]) launchSummary(ctx context.Context) {
	q.abandonSummary()
	if !q.shouldLaunchSummary() {
		return
	}
	model := q.summaryModel
	if model == "" {
		model = q.runModel
	}
	req := models.SummaryRequest{Chat: q.chat, Model: model}
	req.Chat.Messages = slices.Clone(q.chat.Messages)
	// WithoutCancel keeps values, so the run's cancel func is masked (D17).
	summaryCtx, cancel := context.WithCancel(context.WithValue(context.WithoutCancel(ctx), utils.ContextCancelKey, nil))
	run := &summaryRun{result: make(chan summaryResult, 1), cancel: cancel}
	run.interrupt, run.release = q.summaryInterrupt, func() {}
	if run.interrupt == nil {
		run.interrupt, run.release = signalInterrupt()
	}
	traceSummaryf("launching in-flight summary chat_id=%q model=%q", req.Chat.ID, model)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				run.result <- summaryResult{err: fmt.Errorf("summarizer panicked: %v", r)}
			}
		}()
		s, err := q.summarizer.Summarize(summaryCtx, req)
		run.result <- summaryResult{summary: s, err: err}
	}()
	q.summaryRun = run
}

// signalInterrupt closes the returned channel on SIGINT or SIGTERM until
// the release func is called.
func signalInterrupt() (<-chan struct{}, func()) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	interrupt := make(chan struct{})
	released := make(chan struct{})
	go func() {
		select {
		case <-signals:
			close(interrupt)
		case <-released:
		}
	}()
	return interrupt, func() {
		signal.Stop(signals)
		close(released)
	}
}

// abandonSummary cancels and forgets the in-flight summarizer; the process
// exit reaps it.
func (q *Querier[C]) abandonSummary() {
	run := q.summaryRun
	if run == nil {
		return
	}
	q.summaryRun = nil
	run.cancel()
	run.release()
}

// joinSummary waits for the in-flight result for at most the join bound or
// until the interrupt channel closes, then stamps the label onto the chat.
func (q *Querier[C]) joinSummary(chat *pub_models.Chat) {
	run := q.summaryRun
	if run == nil {
		return
	}
	defer q.abandonSummary()
	timeout := q.summaryJoinTimeout
	if timeout <= 0 {
		timeout = defaultSummaryJoinTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case res := <-run.result:
		if res.err != nil {
			traceSummaryf("in-flight summary failed chat_id=%q: %v", chat.ID, res.err)
			return
		}
		if res.summary.Summary == "" {
			traceSummaryf("in-flight summary empty chat_id=%q", chat.ID)
			return
		}
		res.summary.ApplyTo(chat)
		traceSummaryf("in-flight summary applied chat_id=%q title=%q", chat.ID, res.summary.Title)
	case <-timer.C:
		traceSummaryf("in-flight summary abandoned after %v chat_id=%q", timeout, chat.ID)
	case <-run.interrupt:
		traceSummaryf("in-flight summary abandoned on interrupt chat_id=%q", chat.ID)
	}
}
