package audio

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

// sixVoiceMeeting: four 450 s cores (1800 s source at a 700 s budget).
// chunk 1: v1 v2 v3; chunk 2: v1 v4; chunk 3: v2 v5 v1; chunk 4: v6 v1 v3.
func sixVoiceMeeting() []voiceSpan {
	var spans []voiceSpan
	pattern := [][]string{{"v1", "v2", "v3"}, {"v1", "v4"}, {"v2", "v5", "v1"}, {"v6", "v1", "v3"}}
	for chunk, voices := range pattern {
		base := float64(chunk) * 450
		// eight turns per voice per chunk, all inside the chunk
		for t := 0.0; t < 448; t += 56 {
			for i, v := range voices {
				start := base + t + float64(i)*18
				spans = append(spans, voiceSpan{start, start + 15, v})
			}
		}
	}
	return spans
}

func testBudgets() Budgets {
	b := defaultBudgets()
	b.MaxRequestDuration = 700 * time.Second
	return b
}

func newCoordinator(p *voiceProvider) (*Coordinator, *arithmeticAssembler, *bytes.Buffer) {
	asm := &arithmeticAssembler{}
	status := &bytes.Buffer{}
	c := &Coordinator{
		Runner:      &scriptedRunner{duration: "1800.000000"},
		Budgets:     testBudgets(),
		Transcriber: manifestTranscriber{p},
		NewAssembler: func(CommandRunner, Budgets, string, time.Duration) (RequestAssembler, error) {
			return asm, nil
		},
		Parallelism: 1,
		Model:       "gpt-4o-transcribe-diarize",
		Endpoint:    "https://example.test/v1/audio/transcriptions",
		Options:     "diarized_json",
		StatusOut:   status,
	}
	return c, asm, status
}

func cores4() []SourceInterval {
	var out []SourceInterval
	for i := range 4 {
		out = append(out, SourceInterval{Start: time.Duration(i) * 450 * time.Second, End: time.Duration(i+1) * 450 * time.Second})
	}
	return out
}

func idsByVoice(t *testing.T, segs []Segment, truth []voiceSpan) map[string]map[string]time.Duration {
	t.Helper()
	m := Mapping{Core: segs}
	return identityOf(m, truth, SourceInterval{})
}

func assertSixStableIDs(t *testing.T, segs []Segment, truth []voiceSpan) map[string]string {
	t.Helper()
	ids := idsByVoice(t, segs, truth)
	got := map[string]string{}
	seen := map[string]string{}
	for _, v := range []string{"v1", "v2", "v3", "v4", "v5", "v6"} {
		if len(ids[v]) != 1 {
			t.Errorf("voice %v maps to %v, expected one global ID", v, ids[v])
			continue
		}
		for id := range ids[v] {
			got[v] = id
			if other, dup := seen[id]; dup {
				t.Errorf("voices %v and %v merged onto %v", other, v, id)
			}
			seen[id] = v
		}
	}
	return got
}

func TestStitchRemovesPrefix(t *testing.T) {
	p := &voiceProvider{truth: sixVoiceMeeting()}
	c, _, _ := newCoordinator(p)
	segs, err := c.Run(context.Background(), "meeting.wav")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range segs {
		if s.Start < 0 || s.End > 1800*time.Second {
			t.Errorf("segment outside the source: %+v", s)
		}
		if strings.HasPrefix(s.Speaker, "cand-") || s.Text == "mix" {
			t.Errorf("prefix material leaked into output: %+v", s)
		}
	}
	// Every truth span appears once; prefix copies would double the count
	total := time.Duration(0)
	for _, s := range segs {
		total += s.End - s.Start
	}
	want := time.Duration(0)
	for _, v := range p.truth {
		want += time.Duration((v.end - v.start) * float64(time.Second))
	}
	if total != want {
		t.Errorf("output speech %v differs from source speech %v", total, want)
	}
}

func TestStitchRestoresSourceTimestamps(t *testing.T) {
	p := &voiceProvider{truth: sixVoiceMeeting()}
	c, _, _ := newCoordinator(p)
	segs, err := c.Run(context.Background(), "meeting.wav")
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) != len(p.truth) {
		t.Fatalf("expected %v segments, got %v", len(p.truth), len(segs))
	}
	for i := 1; i < len(segs); i++ {
		if segs[i].Start < segs[i-1].Start {
			t.Fatalf("output not source ordered at %v: %+v", i, segs[i-1:i+1])
		}
	}
	for i, v := range p.truth {
		want := SourceInterval{Start: time.Duration(v.start * float64(time.Second)), End: time.Duration(v.end * float64(time.Second))}
		if segs[i].Start != want.Start || segs[i].End != want.End {
			t.Errorf("segment %v at %v–%v, expected %v–%v", i, segs[i].Start, segs[i].End, want.Start, want.End)
		}
	}
}

// plainStatus disables ancli colors so board rows are matched literally.
func plainStatus(t *testing.T) {
	t.Helper()
	prev := ancli.UseColor
	ancli.UseColor = false
	t.Cleanup(func() { ancli.UseColor = prev })
}

func TestCoordinatorPropagatesTranscriberError(t *testing.T) {
	p := &voiceProvider{truth: sixVoiceMeeting(), err: errors.New("status 500")}
	c, asm, _ := newCoordinator(p)
	_, err := c.Run(context.Background(), "meeting.wav")
	if err == nil || !strings.Contains(err.Error(), "status 500") || !strings.Contains(err.Error(), "chunk 1") {
		t.Fatalf("expected wrapped transcriber error with chunk context, got %v", err)
	}
	if !asm.closed {
		t.Error("artifacts must be removed on error")
	}
}

func TestCoordinatorCancelsAndCleansUp(t *testing.T) {
	p := &voiceProvider{truth: sixVoiceMeeting(), hold: make(chan struct{}), held: make(chan struct{}, 1)}
	c, asm, _ := newCoordinator(p)
	c.Parallelism = 1
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := c.Run(ctx, "meeting.wav")
		done <- err
	}()
	// cancel once the first survey is held in flight
	<-p.held
	cancel()
	err := <-done
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	p.mu.Lock()
	inFlight := p.inFlight
	p.mu.Unlock()
	if inFlight != 0 {
		t.Errorf("expected no in-flight requests after cancel, got %v", inFlight)
	}
	if !asm.closed {
		t.Error("artifacts must be removed on cancel")
	}
	if p.requests() != 1 {
		t.Errorf("queued chunks must never start after cancel, got %v requests", p.requests())
	}
}

// TestCoordinatorLiveBoardRendersEstimate drives the board in terminal mode
// against a buffer: the footer function runs under the board lock at every
// redraw, so any callback into the board would deadlock here.
func TestCoordinatorLiveBoardRendersEstimate(t *testing.T) {
	plainStatus(t)
	c, _, status := surveyFor(newChunkDiarizer(twoInChunkOne()))
	c.forceLive = true
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := c.Run(ctx, "meeting.wav"); err != nil {
		t.Fatalf("live board: %v", err)
	}
	out := status.String()
	for _, want := range []string{"\x1b[", "phase  finding voices", "in flight", "left (≤", "phase  done"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in live output", want)
		}
	}
}

// The board's final footer, failures included (R2-01).
func TestCoordinatorLiveErrorExitWritesTheFinalFooter(t *testing.T) {
	plainStatus(t)
	p := &voiceProvider{truth: sixVoiceMeeting(), err: errors.New("status 500")}
	c, _, status := newCoordinator(p)
	c.forceLive = true
	if _, err := c.Run(context.Background(), "meeting.wav"); err == nil {
		t.Fatal("expected the transcriber error")
	}
	out := status.String()
	last := strings.LastIndex(out, "▸ speaker labelling")
	if last < 0 {
		t.Fatalf("no live frame in:\n%q", out)
	}
	// The live frame cuts the footer at the board's width
	final := out[last:]
	if !strings.Contains(final, "  requests 1/≤18 · resent 0 · retries 0 · cache hits 0 · uploaded 7m30s") || strings.Contains(final, "in flight") {
		t.Errorf("the error-exit frame must end with the final footer:\n%q", final)
	}
}
