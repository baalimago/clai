package audio

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/resend"
	"github.com/baalimago/clai/internal/testenv/procenv"
	"github.com/baalimago/clai/pkg/claierr"
	"github.com/baalimago/clai/pkg/diagnostic"
)

func rateLimited(retryAfter time.Duration) error {
	return claierr.NewRateLimited(&claierr.APIError{StatusCode: 429, RetryAfter: retryAfter}, time.Time{}, 0, 0)
}

// recordedSchedule is the CLI's delays with a wait that only records.
func recordedSchedule() (resend.Schedule, *[]time.Duration) {
	var mu sync.Mutex
	waits := &[]time.Duration{}
	return resend.Schedule{Delays: resend.Default().Delays, Wait: func(_ context.Context, d time.Duration) error {
		mu.Lock()
		defer mu.Unlock()
		*waits = append(*waits, d)
		return nil
	}}, waits
}

func TestCalibratedRequestIsSentAgainAfterARateLimit(t *testing.T) {
	plainStatus(t)
	_, _, clean := calibrationLog(t, &voiceProvider{truth: sixVoiceMeeting()})
	p := &voiceProvider{truth: sixVoiceMeeting(), failCore: map[time.Duration][]error{450 * time.Second: {rateLimited(0), rateLimited(42 * time.Second)}}}
	c, _, status := newCoordinator(p)
	schedule, waits := recordedSchedule()
	c.Retry = schedule
	var log bytes.Buffer
	segs, err := c.Run(procenv.Diagnostics(context.Background(), &log, diagnostic.LevelDebug), "meeting.wav")
	if err != nil {
		t.Fatal(err)
	}
	assertSixStableIDs(t, segs, p.truth)
	if want := []time.Duration{time.Second, 42 * time.Second}; !slices.Equal(*waits, want) {
		t.Errorf("waits = %v, want %v: the second honours Retry-After", *waits, want)
	}
	tot := c.Totals()
	if tot.Requests != clean.Requests || tot.Resent != 2 || tot.Retries != clean.Retries || p.requests() != clean.Requests+2 {
		t.Errorf("a resent request must count once: totals %+v after %d sends, clean run %+v", tot, p.requests(), clean)
	}
	assertRecords(t, log.String(),
		`request-retry chunk=2 attempt=1 kind=survey try=1/4 delay=1\.0 cause="rate limited \(429\)"$`,
		`request-retry chunk=2 attempt=1 kind=survey try=2/4 delay=42\.0 cause="rate limited \(429\)"$`,
		`accepted chunk=2 attempt=2 requests=2 resent=2 retries=0 `,
		`stitched .* requests=\d+ resent=2 retries=0 `)
	for _, want := range []string{"retry 1/4 in 1s · 429", "retry 2/4 in 42s · 429", "finding voices · try 3", " · resent 2 · retries 0 · "} {
		if !strings.Contains(status.String(), want) {
			t.Errorf("expected %q in status output, got:\n%v", want, status.String())
		}
	}
}

func TestCalibratedRequestFailsTheRun(t *testing.T) {
	denied := claierr.NewAuthFailed(&claierr.APIError{StatusCode: 401})
	tests := []struct {
		name     string
		failures []error
		want     error
		sends    int
		waits    []time.Duration
	}{
		{"at once when the provider rejects it", []error{denied}, claierr.ErrAuthFailed, 1, nil},
		{"when the schedule is spent", slices.Repeat([]error{rateLimited(0)}, 5), claierr.ErrRateLimited, 5, resend.Default().Delays},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &voiceProvider{truth: sixVoiceMeeting(), failCore: map[time.Duration][]error{0: tt.failures}}
			c, _, _ := newCoordinator(p)
			schedule, waits := recordedSchedule()
			c.Retry = schedule
			_, err := c.Run(context.Background(), "meeting.wav")
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if p.requests() != tt.sends || !slices.Equal(*waits, tt.waits) {
				t.Errorf("sends = %d, waits = %v, want %d and %v", p.requests(), *waits, tt.sends, tt.waits)
			}
		})
	}
}

func TestOtherChunksRunWhileOneWaitsToBeSentAgain(t *testing.T) {
	var mu sync.Mutex
	pending := map[time.Duration]bool{900 * time.Second: true, 1350 * time.Second: true}
	others := make(chan struct{})
	p := &voiceProvider{truth: sixVoiceMeeting(), failCore: map[time.Duration][]error{450 * time.Second: {rateLimited(0)}}}
	p.answered = func(core SourceInterval) {
		mu.Lock()
		defer mu.Unlock()
		if delete(pending, core.Start); len(pending) == 0 {
			select {
			case <-others:
			default:
				close(others)
			}
		}
	}
	c, _, _ := newCoordinator(p)
	c.Parallelism = 3
	c.Retry = resend.Schedule{Delays: resend.Default().Delays, Wait: func(ctx context.Context, _ time.Duration) error {
		select {
		case <-others:
			return nil
		case <-time.After(5 * time.Second):
			return errors.New("chunks 3 and 4 got no answer while chunk 2 waited")
		}
	}}
	segs, err := c.Run(context.Background(), "meeting.wav")
	if err != nil {
		t.Fatal(err)
	}
	assertSixStableIDs(t, segs, p.truth)
}

func TestPlainChunkIsSentAgainAfterARateLimit(t *testing.T) {
	plainStatus(t)
	splitter, trans, _, status, input := newOversizedSplitter(t)
	schedule, waits := recordedSchedule()
	splitter.Retry = schedule
	var mu sync.Mutex
	failed := false
	trans.transcribe = func(_ context.Context, filePath string) ([]Segment, error) {
		mu.Lock()
		defer mu.Unlock()
		if strings.Contains(filePath, "chunk_001") && !failed {
			failed = true
			return nil, rateLimited(0)
		}
		return []Segment{{Start: time.Second, End: 2 * time.Second, Text: "x"}}, nil
	}
	var log bytes.Buffer
	segs, err := splitter.Transcribe(procenv.Diagnostics(context.Background(), &log, diagnostic.LevelDebug), input)
	if err != nil || len(segs) != 3 || trans.callCount() != 4 || !slices.Equal(*waits, []time.Duration{time.Second}) {
		t.Fatalf("err=%v segments=%d sends=%d waits=%v, want 3 segments from 4 sends after 1s", err, len(segs), trans.callCount(), *waits)
	}
	if want := `transcription retry request=chunk-2/3 try=1/4 delay=1.0 cause="rate limited (429)"`; !strings.Contains(log.String(), want) {
		t.Errorf("expected %q in the debug records, got:\n%s", want, log.String())
	}
	for _, want := range []string{"retry 1/4 in 1s · 429", "transcribing · try 2"} {
		if !strings.Contains(status.String(), want) {
			t.Errorf("expected %q in status output, got:\n%v", want, status.String())
		}
	}
}

func TestSingleRequestIsSentAgainAndThenFails(t *testing.T) {
	trans := &fakeTranscriber{transcribe: func(context.Context, string) ([]Segment, error) {
		return nil, claierr.NewProviderUnavailable(&claierr.APIError{StatusCode: 503})
	}}
	status := &bytes.Buffer{}
	splitter := NewSplitter(trans, &fakeRunner{})
	splitter.StatusOut = status
	schedule, waits := recordedSchedule()
	splitter.Retry = schedule
	_, err := splitter.Transcribe(context.Background(), sparseFile(t, 1<<20))
	if !errors.Is(err, claierr.ErrProviderUnavailable) || trans.callCount() != 5 || !slices.Equal(*waits, schedule.Delays) {
		t.Fatalf("err=%v sends=%d waits=%v, want the 503 after 5 sends", err, trans.callCount(), *waits)
	}
	if want := "retry 4/4 in 1m0s · 503"; !strings.Contains(status.String(), want) {
		t.Errorf("expected %q in status output, got:\n%v", want, status.String())
	}
}

func TestRetryStateFitsTheBoard(t *testing.T) {
	try := resend.Try{Attempt: 4, Of: 4, Delay: time.Minute, Cause: claierr.NewTransport(errors.New("connection reset by peer"))}
	if got := retryState(try); got != "retry 4/4 in 1m0s · no answer" || len([]rune(got)) > 30 {
		t.Errorf("retryState = %q, want the wait within the state column", got)
	}
	if got, want := retryText(try), `try=4/4 delay=60.0 cause="connection failed: connection reset by peer"`; got != want {
		t.Errorf("retryText = %q, want %q", got, want)
	}
}
