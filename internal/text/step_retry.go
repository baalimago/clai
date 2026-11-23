package text

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/baalimago/clai/internal/display"
	"github.com/baalimago/clai/internal/look"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/resend"
	"github.com/baalimago/clai/internal/runenv"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// streamWithRetry resends a request that failed before its stream opened. No
// step state exists yet, so the conversation continues where it is.
func (q *Querier[C]) streamWithRetry(ctx context.Context, session *QuerySession, chat pub_models.Chat) (chan models.CompletionEvent, error) {
	return resend.Do(ctx, q.stepRetry, resend.Unanswered, q.noticeStepRetry, func(ctx context.Context) (chan models.CompletionEvent, error) {
		session.requestStartedAt = time.Now()
		return q.Model.StreamCompletions(ctx, chat)
	})
}

// noticeStepRetry is best effort: failing to report a retry must not cost it.
func (q *Querier[C]) noticeStepRetry(t resend.Try) {
	title, model, reason := t.String(), q.routeModel(), resend.Reason(t.Cause)
	if q.json.active() {
		_ = q.json.stderr.ReportProgress(display.Progress{
			Title:  "retry",
			Row:    model,
			State:  "update",
			Detail: fmt.Sprintf("attempt %d/%d in %v: %s", t.Attempt, t.Of, t.Delay, reason),
		})
		return
	}
	if q.activityViewport != nil && q.usesActivityViewport() {
		_ = q.warnf("%s · %s · %s", title, model, reason)
		return
	}
	var w io.Writer = runenv.From(q.diagContext()).Stderr()
	if q.errOut != nil {
		w = q.errOut
	}
	_, _ = fmt.Fprintln(w, look.For(w).Head(title, model, reason))
}
