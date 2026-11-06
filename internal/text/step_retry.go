package text

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/baalimago/clai/internal/look"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// ErrStepRetryWithoutWait means a StepRetry has delays and nothing to wait them with.
var ErrStepRetryWithoutWait = errors.New("step retry has delays but no wait")

// StepRetry is the delays between requests of one model step. The zero value
// makes one request.
type StepRetry struct {
	Delays []time.Duration
	Wait   func(context.Context, time.Duration) error
}

// DefaultStepRetry is the schedule the CLI runs with.
func DefaultStepRetry() StepRetry {
	return StepRetry{
		Delays: []time.Duration{time.Second, 10 * time.Second, 30 * time.Second, 60 * time.Second},
		Wait:   waitForStepRetry,
	}
}

func (r StepRetry) validate() error {
	if len(r.Delays) > 0 && r.Wait == nil {
		return ErrStepRetryWithoutWait
	}
	return nil
}

func waitForStepRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// streamWithRetry resends a request that failed before its stream opened. No
// step state exists yet, so the conversation continues where it is.
func (q *Querier[C]) streamWithRetry(ctx context.Context, session *QuerySession, chat pub_models.Chat) (chan models.CompletionEvent, error) {
	for try := 0; ; try++ {
		session.requestStartedAt = time.Now()
		completions, err := q.Model.StreamCompletions(ctx, chat)
		if err == nil || ctx.Err() != nil || try >= len(q.stepRetry.Delays) || !stepRetryable(err) {
			return completions, err
		}
		delay := q.stepRetry.Delays[try]
		q.noticeStepRetry(err, try+1, delay)
		if waitErr := q.stepRetry.Wait(ctx, delay); waitErr != nil {
			return nil, fmt.Errorf("retry wait after %v: %w", err, waitErr)
		}
	}
}

func stepRetryable(err error) bool {
	return errors.Is(err, claierr.ErrProviderUnavailable) || errors.Is(err, claierr.ErrTransport)
}

// noticeStepRetry is best effort: failing to report a retry must not cost it.
func (q *Querier[C]) noticeStepRetry(cause error, attempt int, delay time.Duration) {
	title := fmt.Sprintf("retry %d/%d in %v", attempt, len(q.stepRetry.Delays), delay)
	model, reason := q.routeModel(), stepRetryReason(cause)
	if q.activityViewport != nil && q.usesActivityViewport() {
		_ = q.warnf("%s · %s · %s", title, model, reason)
		return
	}
	var w io.Writer = os.Stderr
	if q.errOut != nil {
		w = q.errOut
	}
	_, _ = fmt.Fprintln(w, look.For(w).Head(title, model, reason))
}

func stepRetryReason(cause error) string {
	if unavailable, ok := errors.AsType[*claierr.ProviderUnavailableError](cause); ok {
		return fmt.Sprintf("provider unavailable (%d)", unavailable.StatusCode)
	}
	var transport *claierr.TransportError
	if !errors.As(cause, &transport) || transport.Cause == nil {
		return "connection failed"
	}
	detail := transport.Cause
	// A request URL can carry a vendor's API key.
	if request, ok := errors.AsType[*url.Error](detail); ok {
		detail = request.Err
	}
	return "connection failed: " + strings.Join(strings.Fields(detail.Error()), " ")
}
