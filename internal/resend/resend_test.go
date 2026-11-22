package resend

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/baalimago/clai/pkg/claierr"
)

var agreed = []time.Duration{time.Second, 10 * time.Second, 30 * time.Second, 60 * time.Second}

func status(code int, retryAfter time.Duration) *claierr.APIError {
	return &claierr.APIError{StatusCode: code, RetryAfter: retryAfter}
}

type run struct {
	sends int
	waits []time.Duration
	tries []Try
}

func (r *run) do(ctx context.Context, again Policy, script func(send int) error) error {
	s := Schedule{Delays: agreed, Wait: func(_ context.Context, d time.Duration) error {
		r.waits = append(r.waits, d)
		return nil
	}}
	_, err := Do(ctx, s, again, func(t Try) { r.tries = append(r.tries, t) }, func(context.Context) (int, error) {
		r.sends++
		return r.sends, script(r.sends)
	})
	return err
}

func TestDoResendsUntilAnAnswer(t *testing.T) {
	r := &run{}
	err := r.do(context.Background(), Throttled, func(send int) error {
		if send < 3 {
			return claierr.NewRateLimited(status(429, 0), time.Time{}, 0, 0)
		}
		return nil
	})
	if err != nil || r.sends != 3 || !slices.Equal(r.waits, agreed[:2]) {
		t.Fatalf("err=%v sends=%d waits=%v, want 3 sends after %v", err, r.sends, r.waits, agreed[:2])
	}
	if got := r.tries[1].String() + " · " + Reason(r.tries[1].Cause); got != "retry 2/4 in 10s · rate limited (429)" {
		t.Fatalf("notice = %q", got)
	}
}

func TestDoNeverWaitsLessThanRetryAfter(t *testing.T) {
	r := &run{}
	err := r.do(context.Background(), Throttled, func(send int) error {
		switch send {
		case 1:
			return claierr.NewRateLimited(status(429, 7*time.Second), time.Time{}, 0, 0)
		case 2:
			return claierr.NewProviderUnavailable(status(503, 2*time.Second))
		}
		return nil
	})
	if want := []time.Duration{7 * time.Second, 10 * time.Second}; err != nil || !slices.Equal(r.waits, want) {
		t.Fatalf("err=%v waits=%v, want %v", err, r.waits, want)
	}
}

func TestDoReturnsTheLastErrorWhenTheScheduleIsSpent(t *testing.T) {
	r := &run{}
	err := r.do(context.Background(), Unanswered, func(int) error { return claierr.NewProviderUnavailable(status(503, 0)) })
	if !errors.Is(err, claierr.ErrProviderUnavailable) || r.sends != 5 || !slices.Equal(r.waits, agreed) {
		t.Fatalf("err=%v sends=%d waits=%v, want 5 sends over %v", err, r.sends, r.waits, agreed)
	}
}

func TestDoSendsOnceWhenThePolicyRejects(t *testing.T) {
	quota := errors.Join(claierr.NewRateLimited(status(429, 0), time.Time{}, 0, 0), claierr.NewInsufficientCredits(status(429, 0)))
	for name, failure := range map[string]error{
		"auth":         claierr.NewAuthFailed(status(401, 0)),
		"credits":      claierr.NewInsufficientCredits(status(402, 0)),
		"empty quota":  quota,
		"bad request":  claierr.NewUnexpectedProviderResponse(400, nil),
		"not provider": errors.New("ffmpeg"),
	} {
		r := &run{}
		if err := r.do(context.Background(), Throttled, func(int) error { return failure }); !errors.Is(err, failure) || r.sends != 1 || len(r.waits) != 0 {
			t.Errorf("%s: err=%v sends=%d waits=%v, want one send", name, err, r.sends, r.waits)
		}
	}
	r := &run{}
	limited := claierr.NewRateLimited(status(429, 0), time.Time{}, 0, 0)
	if err := r.do(context.Background(), Unanswered, func(int) error { return limited }); !errors.Is(err, claierr.ErrRateLimited) || r.sends != 1 {
		t.Errorf("Unanswered resent a rate limit: err=%v sends=%d", err, r.sends)
	}
}

func TestDoStopsWhenTheContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := &run{}
	err := r.do(ctx, Unanswered, func(int) error {
		cancel()
		return claierr.NewTransport(context.Canceled)
	})
	if err == nil || r.sends != 1 || len(r.waits) != 0 {
		t.Fatalf("err=%v sends=%d waits=%v, want one send and no wait", err, r.sends, r.waits)
	}
}

func TestDoReturnsAFailedWait(t *testing.T) {
	stop := errors.New("stopped")
	s := Schedule{Delays: agreed, Wait: func(context.Context, time.Duration) error { return stop }}
	_, err := Do(context.Background(), s, Unanswered, nil, func(context.Context) (int, error) {
		return 0, claierr.NewTransport(errors.New("reset"))
	})
	if !errors.Is(err, stop) {
		t.Fatalf("err = %v, want the wait's error", err)
	}
}

func TestDefaultIsTheAgreedSchedule(t *testing.T) {
	got := Default()
	if !slices.Equal(got.Delays, agreed) || got.Wait == nil || got.Validate() != nil {
		t.Fatalf("Default() = %v, wait set %t, validate %v", got.Delays, got.Wait != nil, got.Validate())
	}
	if err := (Schedule{Delays: agreed}).Validate(); !errors.Is(err, ErrNoWait) {
		t.Fatalf("err = %v, want ErrNoWait", err)
	}
	if err := (Schedule{}).Validate(); err != nil {
		t.Fatalf("zero value: %v", err)
	}
}

func TestWait(t *testing.T) {
	if err := wait(context.Background(), time.Millisecond); err != nil {
		t.Fatalf("err = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := wait(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestRetryAfter(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	for value, want := range map[string]time.Duration{
		"":                              0,
		"soon":                          0,
		"-3":                            0,
		"12":                            12 * time.Second,
		"0.5":                           500 * time.Millisecond,
		"Fri, 20 Nov 2026 12:00:30 GMT": 30 * time.Second,
		"Fri, 20 Nov 2026 11:00:00 GMT": 0,
	} {
		h := http.Header{}
		h.Set("Retry-After", value)
		if got := RetryAfter(h, now); got != want {
			t.Errorf("Retry-After %q = %v, want %v", value, got, want)
		}
	}
}

func TestReason(t *testing.T) {
	keyed := claierr.NewTransport(&url.Error{Op: "Post", URL: "https://host/v1?key=SECRET", Err: errors.New("connection\nrefused")})
	for want, cause := range map[string]error{
		"provider unavailable (503)":            claierr.NewProviderUnavailable(status(503, 0)),
		"rate limited (429)":                    claierr.NewRateLimited(status(429, 0), time.Time{}, 0, 0),
		"unexpected provider response (400)":    claierr.NewUnexpectedProviderResponse(400, nil),
		"connection failed: connection refused": keyed,
		"connection failed":                     errors.New("unknown"),
	} {
		if got := Reason(cause); got != want {
			t.Errorf("Reason = %q, want %q", got, want)
		}
	}
}
