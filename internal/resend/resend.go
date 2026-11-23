// Package resend sends one request again, on a schedule, when the provider
// may answer it later (architecture/query.md, Request Retry).
package resend

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/pkg/claierr"
)

// ErrNoWait means a Schedule has delays and nothing to wait them with.
var ErrNoWait = errors.New("resend schedule has delays but no wait")

// Schedule is the delays between sends of one request. The zero value sends once.
type Schedule struct {
	Delays []time.Duration
	Wait   func(context.Context, time.Duration) error
}

// Default is the schedule the CLI runs with.
func Default() Schedule {
	return Schedule{
		Delays: []time.Duration{time.Second, 10 * time.Second, 30 * time.Second, 60 * time.Second},
		Wait:   wait,
	}
}

func (s Schedule) Validate() error {
	if len(s.Delays) > 0 && s.Wait == nil {
		return ErrNoWait
	}
	return nil
}

func wait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Try is one wait before a request is sent again.
type Try struct {
	Attempt, Of int
	Delay       time.Duration
	Cause       error
}

func (t Try) String() string { return fmt.Sprintf("retry %d/%d in %v", t.Attempt, t.Of, t.Delay) }

// Policy reports whether a failed request may be answered when sent again.
type Policy func(error) bool

// Unanswered is a provider failure or a request that got no answer.
func Unanswered(err error) bool {
	return errors.Is(err, claierr.ErrProviderUnavailable) || errors.Is(err, claierr.ErrTransport)
}

// Throttled is Unanswered plus a rate limit that is not an empty account.
func Throttled(err error) bool {
	if errors.Is(err, claierr.ErrLikelyInsufficientCredits) || errors.Is(err, claierr.ErrAuthFailed) {
		return false
	}
	return Unanswered(err) || errors.Is(err, claierr.ErrRateLimited)
}

// Do sends until an answer, a failure again rejects, or the spent schedule; no wait is shorter than Retry-After.
func Do[T any](ctx context.Context, s Schedule, again Policy, notice func(Try), send func(context.Context) (T, error)) (T, error) {
	for try := 0; ; try++ {
		value, err := send(ctx)
		if err == nil || ctx.Err() != nil || try >= len(s.Delays) || !again(err) {
			return value, err
		}
		t := Try{Attempt: try + 1, Of: len(s.Delays), Delay: max(s.Delays[try], retryAfter(err)), Cause: err}
		if notice != nil {
			notice(t)
		}
		if waitErr := s.Wait(ctx, t.Delay); waitErr != nil {
			var zero T
			return zero, diag.Errorf("retry wait after %v: %w", err, waitErr)
		}
	}
}

func retryAfter(err error) time.Duration {
	var facts claierr.APIErrorer
	if errors.As(err, &facts) {
		return facts.API().RetryAfter
	}
	return 0
}

// RetryAfter reads a Retry-After header: seconds or an HTTP date; zero when absent.
func RetryAfter(h http.Header, now time.Time) time.Duration {
	value := strings.TrimSpace(h.Get("Retry-After"))
	if seconds, err := strconv.ParseFloat(value, 64); err == nil && seconds > 0 {
		return time.Duration(seconds * float64(time.Second))
	}
	if at, err := http.ParseTime(value); err == nil && at.After(now) {
		return at.Sub(now)
	}
	return 0
}

// Reason is the one-phrase cause of a Try, without the provider's prose.
func Reason(cause error) string {
	var facts claierr.APIErrorer
	if errors.As(cause, &facts) {
		meaning := claierr.UnavailableMeaning(cause)
		if meaning == nil {
			meaning = claierr.ErrUnexpectedProviderResponse
		}
		return fmt.Sprintf("%v (%d)", meaning, facts.API().StatusCode)
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
