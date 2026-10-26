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

// loopSummaryJoinTimeout bounds how long a loop iteration waits for its
// phase summary, which its header shows. A plain query never waits: its label
// is best effort and kept only when it lands before the answer is done.
const loopSummaryJoinTimeout = 5 * time.Second

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
	kind := models.SummaryKindConversation
	if q.loopProgress != nil {
		// Iteration 1 has no handover: its summary is the approach the loop
		// intends to take toward the goal. Later iterations summarise the
		// progression the previous handover reports.
		kind = models.SummaryKindLoopPhase
		if q.loopProgress.Iteration == 1 {
			kind = models.SummaryKindLoopPlan
		}
	}
	req := models.SummaryRequest{Chat: q.chat, Model: model, Kind: kind}
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

// startSummary launches the summarizer now when its result is waited for,
// and otherwise defers it to the run's first tool step.
func (q *Querier[C]) startSummary(ctx context.Context) {
	if q.loopProgress == nil && q.summaryJoinBound() == 0 {
		q.abandonSummary()
		q.summaryDeferred = true
		return
	}
	q.launchSummary(ctx)
}

func (q *Querier[C]) launchDeferredSummary(ctx context.Context) {
	if !q.summaryDeferred {
		return
	}
	q.summaryDeferred = false
	q.launchSummary(ctx)
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
	q.summaryReady = nil
	run.cancel()
	run.release()
}

// summaryResultChannel returns the in-flight summarizer's result channel, or
// nil to disable the session loop's select case: no run is in flight, or its
// result was already taken.
func (q *Querier[C]) summaryResultChannel() <-chan summaryResult {
	if q.summaryRun == nil || q.summaryReady != nil {
		return nil
	}
	return q.summaryRun.result
}

// awaitSummaryResult takes the summarizer's result on the session loop. A loop
// iteration rewrites its pinned header with the summary as soon as it lands, so
// the header moves from the placeholder to the iteration's progress while it
// still streams. A summary that lands before the first frame is kept on the
// progress state, so that frame is born with it. The join takes the same result
// instead of waiting for it. A failed or empty summary reveals the fallback
// phase, so the header never keeps the placeholder.
func (q *Querier[C]) awaitSummaryResult(res summaryResult) error {
	q.summaryReady = &res
	if q.loopProgress == nil {
		return nil
	}
	phase := q.loopProgress.Phase
	if res.err == nil {
		if landed := loopProgressLine(res.summary.Summary); landed != "" {
			phase = landed
		}
	}
	return q.showLoopPhase(phase)
}

// applySummary stamps a summarizer result onto chat unless it failed, was
// empty, or carries no title.
func applySummary(res summaryResult, chat *pub_models.Chat) {
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
}

// summaryWait classifies how a bounded wait for the in-flight summarizer ended.
type summaryWait uint8

const (
	// summaryWaitResult means the summarizer answered within the join bound.
	summaryWaitResult summaryWait = iota
	summaryWaitTimeout
	summaryWaitInterrupt
)

// summaryJoinBound resolves the join bound: the configured one, else the
// loop default inside a loop, else zero.
func (q *Querier[C]) summaryJoinBound() time.Duration {
	if q.summaryJoinTimeout > 0 {
		return q.summaryJoinTimeout
	}
	if q.loopProgress != nil {
		return loopSummaryJoinTimeout
	}
	return 0
}

// waitForSummary waits for the in-flight summarizer for at most the join bound
// or until the interrupt channel closes. The caller owns the result handling and
// the run's release, so a summary is taken exactly once.
func (q *Querier[C]) waitForSummary(run *summaryRun) (summaryResult, summaryWait) {
	// A landed result wins over an elapsed bound; with a zero bound the
	// select below would otherwise pick between them at random.
	select {
	case res := <-run.result:
		return res, summaryWaitResult
	default:
	}
	timer := time.NewTimer(q.summaryJoinBound())
	defer timer.Stop()
	select {
	case res := <-run.result:
		return res, summaryWaitResult
	case <-timer.C:
		return summaryResult{}, summaryWaitTimeout
	case <-run.interrupt:
		return summaryResult{}, summaryWaitInterrupt
	}
}

// joinSummary waits for the in-flight result for at most the join bound or
// until the interrupt channel closes, then stamps the label onto the chat. A
// result the session loop already took is applied without waiting.
func (q *Querier[C]) joinSummary(chat *pub_models.Chat) {
	run := q.summaryRun
	if run == nil {
		return
	}
	defer q.abandonSummary()
	if q.summaryReady != nil {
		applySummary(*q.summaryReady, chat)
		return
	}
	res, wait := q.waitForSummary(run)
	switch wait {
	case summaryWaitResult:
		applySummary(res, chat)
	case summaryWaitTimeout:
		traceSummaryf("in-flight summary abandoned after %v chat_id=%q", q.summaryJoinBound(), chat.ID)
	case summaryWaitInterrupt:
		traceSummaryf("in-flight summary abandoned on interrupt chat_id=%q", chat.ID)
	}
}
