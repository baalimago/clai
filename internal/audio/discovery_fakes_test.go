package audio

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// voiceSpan is ground truth: who speaks when, in source seconds.
type voiceSpan struct {
	start, end float64
	voice      string
}

// voiceProvider is a scripted diarizer. It labels every region of an
// assembled request from the truth timeline with letters permuted per
// request, and injects transport faults.
type voiceProvider struct {
	truth []voiceSpan
	mu    sync.Mutex
	calls []AssembleRequest
	// delay slows each request; inFlight/maxInFlight measure concurrency
	delay       time.Duration
	inFlight    int
	maxInFlight int
	// hold blocks each request until released; used for concurrency tests
	hold chan struct{}
	// held is told, without blocking, each time a request starts to wait on hold
	held chan struct{}
	err  error
	// failCore fails the next requests for the core starting here, in order
	failCore map[time.Duration][]error
	// answered is told each core a request answered
	answered func(core SourceInterval)
}

func (p *voiceProvider) nextFailure(core time.Duration) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	failures := p.failCore[core]
	if len(failures) == 0 {
		return nil
	}
	p.failCore[core] = failures[1:]
	return failures[0]
}

func (p *voiceProvider) requests() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.calls)
}

// label diarizes an assembled request from the truth timeline; n is the
// request number used for letter permutation.
func (p *voiceProvider) label(m *RequestManifest, n int) []Segment {
	letters := map[string]string{}
	next := 0
	letter := func(voice string) string {
		if l, ok := letters[voice]; ok {
			return l
		}
		l := string(rune('A' + (next+n)%26))
		next++
		letters[voice] = l
		return l
	}
	var segs []Segment
	emit := func(r Region, spanStart, spanEnd float64, voice string) {
		a := time.Duration(spanStart * float64(time.Second))
		b := time.Duration(spanEnd * float64(time.Second))
		start, end := max(a, r.SrcStart), min(b, r.SrcEnd)
		if end <= start {
			return
		}
		segs = append(segs, Segment{Start: r.ReqStart + start - r.SrcStart, End: r.ReqStart + end - r.SrcStart, Speaker: letter(voice), Text: voice})
	}
	for _, r := range m.Regions {
		switch r.Kind {
		case RegionGap:
			continue
		case RegionSample, RegionCore:
			for _, v := range p.truth {
				emit(r, v.start, v.end, v.voice)
			}
		}
	}
	return segs
}

// identityOf returns, for each truth voice inside core, the set of global
// IDs the mapping assigned to its speech (duration weighted).
func identityOf(m Mapping, truth []voiceSpan, core SourceInterval) map[string]map[string]time.Duration {
	out := map[string]map[string]time.Duration{}
	for _, v := range truth {
		a := time.Duration(v.start * float64(time.Second))
		b := time.Duration(v.end * float64(time.Second))
		for _, s := range m.Core {
			if o := overlap(a, b, s.Start, s.End); o > 0 {
				if out[v.voice] == nil {
					out[v.voice] = map[string]time.Duration{}
				}
				out[v.voice][s.Speaker] += o
			}
		}
	}
	_ = core
	return out
}

// arithmeticAssembler builds manifests without ffmpeg for coordinator tests.
type arithmeticAssembler struct {
	mu      sync.Mutex
	closed  bool
	removed int
	// maxRequestDuration is the seconds budget; zero checks none
	maxRequestDuration time.Duration
	// refuse is a budget check of the test's own; nil refuses nothing
	refuse func(AssembleRequest) error
	// assembled is every request asked for, refused ones included
	assembled []AssembleRequest
}

func (a *arithmeticAssembler) Assemble(ctx context.Context, req AssembleRequest) (*RequestManifest, error) {
	a.mu.Lock()
	a.assembled = append(a.assembled, AssembleRequest{Samples: append([]SampleClip(nil), req.Samples...), Core: req.Core})
	a.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if req.empty() {
		return nil, ErrEmptyRequest
	}
	m := manifestFor(req.Samples, req.Core)
	if a.maxRequestDuration > 0 {
		if err := checkRequestSeconds(m, a.maxRequestDuration); err != nil {
			return nil, err
		}
	}
	if a.refuse != nil {
		if err := a.refuse(req); err != nil {
			return nil, err
		}
	}
	m.RequestHash = fmt.Sprintf("%v|%v", req.Samples, req.Core)
	m.FilePath = "req.mp3"
	return m, nil
}

func (a *arithmeticAssembler) Remove(*RequestManifest) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.removed++
	return nil
}

func (a *arithmeticAssembler) requests() []AssembleRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]AssembleRequest(nil), a.assembled...)
}

func (a *arithmeticAssembler) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closed = true
	return nil
}

// manifestTranscriber adapts the voice provider to the coordinator's seam.
type manifestTranscriber struct{ p *voiceProvider }

func (t manifestTranscriber) TranscribeRequest(ctx context.Context, m *RequestManifest) ([]Segment, error) {
	p := t.p
	p.mu.Lock()
	p.calls = append(p.calls, AssembleRequest{Core: SourceInterval{Start: m.CoreStart, End: m.CoreEnd}, Samples: samplesOfManifest(m)})
	n := len(p.calls)
	p.inFlight++
	p.maxInFlight = max(p.maxInFlight, p.inFlight)
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.inFlight--
		p.mu.Unlock()
	}()
	if p.delay > 0 {
		select {
		case <-time.After(p.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if p.hold != nil {
		select {
		case p.held <- struct{}{}:
		default:
		}
		select {
		case <-p.hold:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if p.err != nil {
		return nil, p.err
	}
	if err := p.nextFailure(m.CoreStart); err != nil {
		return nil, err
	}
	if p.answered != nil {
		p.answered(SourceInterval{Start: m.CoreStart, End: m.CoreEnd})
	}
	return p.label(m, n), nil
}

func samplesOfManifest(m *RequestManifest) []SampleClip {
	var out []SampleClip
	for _, r := range m.Regions {
		if r.Kind == RegionSample {
			out = append(out, SampleClip{ID: r.Label, Start: r.SrcStart + sampleGuard, End: r.SrcEnd - sampleGuard, Candidate: r.Candidate})
		}
	}
	return out
}
