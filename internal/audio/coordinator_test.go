package audio

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
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

func TestLateSpeakerDoesNotInvalidateChunks(t *testing.T) {
	p := &voiceProvider{truth: sixVoiceMeeting()}
	c, asm, _ := newCoordinator(p)
	segs, err := c.Run(context.Background(), "meeting.wav")
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	ids := assertSixStableIDs(t, segs, p.truth)
	if !strings.HasPrefix(ids["v6"], "speaker-") {
		t.Errorf("late speaker should get a global ID, got %v", ids["v6"])
	}
	// bootstrap 2 + chunk2 (pass + discovery) 2 + chunk3 2 + chunk4 2 = 8, no chunk transcribed again
	if p.requests() != 8 {
		t.Errorf("expected 8 requests, got %v", p.requests())
	}
	coresSeen := map[SourceInterval]int{}
	for _, call := range p.calls {
		coresSeen[call.Core]++
	}
	for _, core := range cores4() {
		if coresSeen[core] > 2 {
			t.Errorf("core %v transcribed %v times; accepted chunks must not be redone", core, coresSeen[core])
		}
	}
	if !asm.closed {
		t.Error("assembler must be closed after the run")
	}
	if tot := c.Totals(); tot.Requests != 8 || tot.Retries != 0 || tot.Cores != 4 {
		t.Errorf("unexpected totals %+v", tot)
	}
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

func TestFrozenSnapshotAllowsBoundedParallelism(t *testing.T) {
	p := &voiceProvider{truth: sixVoiceMeeting(), delay: 20 * time.Millisecond}
	c, _, _ := newCoordinator(p)
	c.Parallelism = 2
	segs, err := c.Run(context.Background(), "meeting.wav")
	if err != nil {
		t.Fatal(err)
	}
	assertSixStableIDs(t, segs, p.truth)
	if p.maxInFlight != 2 {
		t.Errorf("expected exactly 2 concurrent requests at peak, got %v", p.maxInFlight)
	}
}

// plainStatus disables ancli colors so board rows are matched literally.
func plainStatus(t *testing.T) {
	t.Helper()
	prev := ancli.UseColor
	ancli.UseColor = false
	t.Cleanup(func() { ancli.UseColor = prev })
}

func TestBestEffortRendersUnknown(t *testing.T) {
	plainStatus(t)
	p := &voiceProvider{truth: sixVoiceMeeting(), mixCandidateVoice: "v5"}
	c, _, status := newCoordinator(p)
	segs, err := c.Run(context.Background(), "meeting.wav")
	if err != nil {
		t.Fatalf("best-effort must not fail: %v", err)
	}
	ids := idsByVoice(t, segs, p.truth)
	if len(ids["v5"]) == 0 {
		t.Fatal("v5 speech missing from output")
	}
	for id := range ids["v5"] {
		if !strings.HasPrefix(id, "unknown-") {
			t.Errorf("expected unknown-N for v5, got %v", id)
		}
	}
	total := time.Duration(0)
	for _, s := range segs {
		total += s.End - s.Start
	}
	if total != 8*3*15*time.Second+8*2*15*time.Second+8*3*15*time.Second+8*3*15*time.Second {
		t.Errorf("speech was dropped: %v", total)
	}
	out := status.String()
	for _, want := range []string{"unknown speech kept as unknown-N", "unknown-1", "no-sample", "✗ 1 unresolved", "in 8 runs"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected stderr summary to contain %q, got:\n%v", want, out)
		}
	}
	// v5 speaks in separate stretches interleaved with others: numbering is per contiguous run in source order
	names := map[string]bool{}
	for _, s := range segs {
		if strings.HasPrefix(s.Speaker, "unknown-") {
			names[s.Speaker] = true
		}
	}
	if len(names) != 8 {
		t.Errorf("expected 8 unknown runs (one per v5 turn), got %v", len(names))
	}
}

func TestStrictFailsWithDiagnostics(t *testing.T) {
	p := &voiceProvider{truth: sixVoiceMeeting(), mixCandidateVoice: "v5"}
	c, asm, status := newCoordinator(p)
	c.Strict = true
	segs, err := c.Run(context.Background(), "meeting.wav")
	if err == nil || segs != nil {
		t.Fatalf("expected strict failure with no transcript, got %v segments, err %v", len(segs), err)
	}
	var ue *UnresolvedError
	if !errors.As(err, &ue) {
		t.Fatalf("expected UnresolvedError, got %T: %v", err, err)
	}
	if ue.Chunk != 2 || ue.Interval.Start != 900*time.Second || ue.Attempts != discoveryIterations || ue.Requests != 3 {
		t.Errorf("unexpected diagnostics: %+v", ue)
	}
	for _, want := range []string{"chunk 3", "15m0s", "attempts", reasonMixed, "strict-speakers"} {
		if !strings.Contains(err.Error(), want) && !strings.Contains(status.String(), want) {
			t.Errorf("expected diagnostics to mention %q, got: %v", want, err)
		}
	}
	if !asm.closed {
		t.Error("artifacts must be removed on strict failure")
	}
}

func TestStrictSpeakersDefaultsToBestEffort(t *testing.T) {
	if (TranscribeConfig{}).StrictSpeakers || Default.Transcribe.StrictSpeakers {
		t.Fatal("strict-speakers zero value must be best-effort")
	}
	p := &voiceProvider{truth: sixVoiceMeeting(), mixCandidateVoice: "v5"}
	c, _, _ := newCoordinator(p)
	if _, err := c.Run(context.Background(), "meeting.wav"); err != nil {
		t.Errorf("zero-value Strict must render unknowns instead of failing: %v", err)
	}
}

func TestRequestAccountingCountsRetries(t *testing.T) {
	// Request 3 is chunk 2's first pass: speaker-2's sample comes back mixed
	p := &voiceProvider{truth: sixVoiceMeeting(), mixSample: map[int]string{3: "speaker-1"}}
	c, _, _ := newCoordinator(p)
	segs, err := c.Run(context.Background(), "meeting.wav")
	if err != nil {
		t.Fatal(err)
	}
	assertSixStableIDs(t, segs, p.truth)
	tot := c.Totals()
	// bootstrap 2 (both counted) + chunk 2: pass, recovery retry, discovery 3 + chunk 3: 2 + chunk 4: 2 = 9
	if tot.Requests != 9 || tot.Retries != 1 || tot.CacheHits != 0 {
		t.Errorf("unexpected totals %+v (calls %v)", tot, len(p.calls))
	}
	if p.requests() != tot.Requests {
		t.Errorf("provider saw %v requests, totals say %v", p.requests(), tot.Requests)
	}
	uploaded := time.Duration(0)
	for _, call := range p.calls {
		uploaded += manifestFor(call.Samples, call.Core).Duration
	}
	if tot.Uploaded != uploaded {
		t.Errorf("uploaded seconds %v, expected %v", tot.Uploaded, uploaded)
	}
}

func TestSecondSampleFailureDoesNotRetry(t *testing.T) {
	// Chunk 2 (requests 3 and 4): speaker-1's sample fails in the pass and again in the retry
	p := &voiceProvider{truth: sixVoiceMeeting(), mixSample: map[int]string{3: "speaker-1", 4: "speaker-1"}}
	c, _, status := newCoordinator(p)
	segs, err := c.Run(context.Background(), "meeting.wav")
	if err != nil {
		t.Fatal(err)
	}
	// pass + one recovery retry, no discovery while the sample is still failing
	chunk2 := 0
	for _, call := range p.calls {
		if call.Core.Start == 450*time.Second {
			chunk2++
		}
	}
	if chunk2 != 2 {
		t.Errorf("expected exactly two requests for chunk 2 (pass + one recovery), got %v", chunk2)
	}
	if c.Totals().Retries != 1 || !strings.Contains(status.String(), "retries 1") || !strings.Contains(status.String(), "recovery retry") {
		t.Errorf("expected exactly one retry counted and announced, got %+v", c.Totals())
	}
	ids := idsByVoice(t, segs, p.truth)
	// v1's chunk-2 speech is unknown there, mapped to one speaker everywhere else; no duplicate identity
	mapped := 0
	for id := range ids["v1"] {
		if !strings.HasPrefix(id, "unknown-") {
			mapped++
		}
	}
	if mapped != 1 {
		t.Errorf("expected v1 mapped to exactly one global ID outside chunk 2, got %v", ids["v1"])
	}
	if n := len(d(t, c).Speakers); n != 5 {
		t.Errorf("expected five speakers (v4 stays unknown in chunk 2, no duplicate v1), got %v", n)
	}
	if r := d(t, c).Speakers[0].Replacements; r != 1 {
		t.Errorf("expected one replacement consumed, got %v", r)
	}
}

func TestAmbiguityRetriesWithReversedPrefix(t *testing.T) {
	t.Run("reversed order resolves without mutation", func(t *testing.T) {
		// Request 3 (chunk 2 first pass) merges v1 and v2 onto one letter
		p := &voiceProvider{truth: sixVoiceMeeting(), mergeVoices: map[int][2]string{3: {"v1", "v2"}}}
		c, _, _ := newCoordinator(p)
		segs, err := c.Run(context.Background(), "meeting.wav")
		if err != nil {
			t.Fatal(err)
		}
		assertSixStableIDs(t, segs, p.truth)
		first, retry := p.calls[2].Samples, p.calls[3].Samples
		if len(first) != 3 || len(retry) != 3 || first[0].ID != retry[2].ID || first[2].ID != retry[0].ID {
			t.Errorf("expected the retry to carry the reversed prefix, got %v then %v", first, retry)
		}
		for _, s := range retry {
			if s.Candidate {
				t.Errorf("recovery retry must carry no candidates: %+v", retry)
			}
		}
		if tot := c.Totals(); tot.Retries != 1 || tot.Requests != 9 {
			t.Errorf("unexpected totals %+v", tot)
		}
	})
	t.Run("repeated ambiguity leaves both unmapped without a third request", func(t *testing.T) {
		p := &voiceProvider{truth: sixVoiceMeeting(), mergeVoices: map[int][2]string{3: {"v1", "v2"}, 4: {"v1", "v2"}}}
		c, _, _ := newCoordinator(p)
		segs, err := c.Run(context.Background(), "meeting.wav")
		if err != nil {
			t.Fatal(err)
		}
		plain := 0
		for _, call := range p.calls {
			if call.Core.Start == 450*time.Second && len(sampleIDs(call.Samples)) == 0 {
				plain++
			}
		}
		if plain != 2 {
			t.Errorf("expected pass and one reversed retry only, got %v plain requests for chunk 2", plain)
		}
		// Discovery may still anchor the voices in a later request; they must never merge
		ids := idsByVoice(t, segs, p.truth)
		for id := range ids["v1"] {
			if _, shared := ids["v2"][id]; shared && !strings.HasPrefix(id, "unknown-") {
				t.Errorf("v1 and v2 merged onto %v", id)
			}
		}
		if c.Totals().Retries != 1 {
			t.Errorf("expected one retry, got %+v", c.Totals())
		}
	})
}

func TestBudgetStopsPerChunk(t *testing.T) {
	p := &voiceProvider{truth: sixVoiceMeeting()}
	c, asm, _ := newCoordinator(p)
	c.MaxRequestsPerChunk = 1
	_, err := c.Run(context.Background(), "meeting.wav")
	var le *RequestLimitError
	if !errors.As(err, &le) {
		t.Fatalf("expected RequestLimitError, got %v", err)
	}
	if le.Chunk != 0 || le.Limit != 1 || le.Consumed != 1 || le.Interval.End != 450*time.Second || len(le.Unresolved) != 3 {
		t.Errorf("unexpected diagnostics %+v", le)
	}
	if p.requests() != 1 {
		t.Errorf("expected the second request refused before upload, got %v requests", p.requests())
	}
	if !asm.closed {
		t.Error("artifacts must be removed")
	}
}

func TestBudgetErrorsUnderBestEffort(t *testing.T) {
	p := &voiceProvider{truth: sixVoiceMeeting()}
	c, _, _ := newCoordinator(p)
	c.Strict, c.MaxRequestsPerChunk = false, 1
	segs, err := c.Run(context.Background(), "meeting.wav")
	if !IsRequestLimit(err) || segs != nil {
		t.Fatalf("limit exhaustion must error even in best-effort, got %v segments, err %v", len(segs), err)
	}
}

func TestCoordinatorReportsTotalsOnStderr(t *testing.T) {
	plainStatus(t)
	p := &voiceProvider{truth: sixVoiceMeeting()}
	c, _, status := newCoordinator(p)
	if _, err := c.Run(context.Background(), "meeting.wav"); err != nil {
		t.Fatal(err)
	}
	out := status.String()
	for _, want := range []string{
		"▸ calibrated diarization  meeting.wav  4 cores", "≤16 requests",
		"uncalibrated pass · 7m30s", "discovery ·3", "mapping", "calibrated pass",
		"✓ calibrated · 6 speakers", "4/4    22:30–30:00", "3/3", "2/4",
		"requests 8/≤16", "retries 0", "cache hits 0", "uploaded", "speakers 6", "unknown 0s", "elapsed",
		"phase  bootstrap · core 1 of 4", "phase  calibrating · cores 2–4 · 1 workers", "phase  stitching", "phase  done · ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in status output, got:\n%v", want, out)
		}
	}
	if strings.Contains(out, "\x1b[") {
		t.Errorf("non-terminal status must be plain rows, got:\n%q", out)
	}
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
	p := &voiceProvider{truth: sixVoiceMeeting(), hold: make(chan struct{})}
	c, asm, _ := newCoordinator(p)
	c.Parallelism = 3
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := c.Run(ctx, "meeting.wav")
		done <- err
	}()
	// wait for the bootstrap request to be in flight, then cancel
	deadline := time.Now().Add(5 * time.Second)
	for p.requests() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
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

// d returns the coordinator's final registry snapshot for assertions.
func d(t *testing.T, c *Coordinator) Snapshot {
	t.Helper()
	if c.registry == nil {
		t.Fatal("coordinator has no registry")
	}
	return c.registry.Snapshot()
}

// TestCoordinatorLiveBoardRendersEstimate drives the board in terminal mode
// against a buffer: the footer function runs under the board lock at every
// redraw, so any callback into the board would deadlock here.
func TestCoordinatorLiveBoardRendersEstimate(t *testing.T) {
	plainStatus(t)
	p := &voiceProvider{truth: sixVoiceMeeting(), delay: 5 * time.Millisecond}
	c, _, status := newCoordinator(p)
	c.forceLive = true
	c.Parallelism = 2
	done := make(chan error, 1)
	go func() {
		_, err := c.Run(context.Background(), "meeting.wav")
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("live board deadlocked")
	}
	out := status.String()
	for _, want := range []string{"\x1b[", "phase  bootstrap", "in flight", "left (≤", "phase  done"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in live output", want)
		}
	}
}

// slowAssembler holds the first request long enough for the live board to
// tick while the bootstrap request is being assembled.
type slowAssembler struct {
	RequestAssembler
	delay time.Duration
	once  sync.Once
}

func (s *slowAssembler) Assemble(ctx context.Context, req AssembleRequest) (*RequestManifest, error) {
	s.once.Do(func() { time.Sleep(s.delay) })
	return s.RequestAssembler.Assemble(ctx, req)
}

func TestCoordinatorLiveBoardReadsStatesSafely(t *testing.T) {
	plainStatus(t)
	p := &voiceProvider{truth: sixVoiceMeeting()}
	c, asm, _ := newCoordinator(p)
	c.forceLive = true
	c.NewAssembler = func(CommandRunner, Budgets, string, time.Duration) (RequestAssembler, error) {
		return &slowAssembler{RequestAssembler: asm, delay: 3 * boardTick}, nil
	}
	if _, err := c.Run(context.Background(), "meeting.wav"); err != nil {
		t.Fatal(err)
	}
}
