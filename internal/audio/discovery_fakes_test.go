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

// voiceProvider is a scripted diarizer. It builds the manifest by
// arithmetic, labels every region from the truth timeline with letters
// permuted per request, and injects the provider faults the spike measured.
type voiceProvider struct {
	truth []voiceSpan
	mu    sync.Mutex
	calls []AssembleRequest
	// faults keyed by 1-based request number
	splitVoice  map[int]string    // second half of this voice's core speech gets its own letter
	mixSample   map[int]string    // sample ID (or "cand" for every candidate) labeled 50/50
	dropSample  map[int]string    // sample ID returns no segments
	mergeVoices map[int][2]string // two voices share one letter
	// mixAllCandidates makes every candidate sample mixed in every request
	mixAllCandidates bool
	// mixCandidateVoice mixes every candidate clip cut from this voice
	mixCandidateVoice string
	// delay slows each request; inFlight/maxInFlight measure concurrency
	delay       time.Duration
	inFlight    int
	maxInFlight int
	// hold blocks each request until released; used for concurrency tests
	hold chan struct{}
	err  error
}

func (p *voiceProvider) requests() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.calls)
}

func (p *voiceProvider) transcribe(ctx context.Context, samples []SampleClip, core SourceInterval) (*RequestManifest, []Segment, error) {
	p.mu.Lock()
	p.calls = append(p.calls, AssembleRequest{Samples: append([]SampleClip(nil), samples...), Core: core})
	n := len(p.calls)
	p.mu.Unlock()
	if p.hold != nil {
		select {
		case <-p.hold:
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
	}
	if p.err != nil {
		return nil, nil, p.err
	}
	m := manifestFor(samples, core)
	return m, p.label(m, n), nil
}

// label diarizes an assembled request from the truth timeline; n is the
// request number used for letter permutation and fault keys.
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
	if mv, ok := p.mergeVoices[n]; ok {
		l := letter(mv[0])
		letters[mv[1]] = l
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
		case RegionSample:
			if id, ok := p.dropSample[n]; ok && id == r.Label {
				continue
			}
			mixed := p.mixAllCandidates && r.Candidate
			if p.mixCandidateVoice != "" && r.Candidate && p.voiceAt(r.SrcStart, r.SrcEnd) == p.mixCandidateVoice {
				mixed = true
			}
			if id, ok := p.mixSample[n]; ok && (id == r.Label || (id == "cand" && r.Candidate)) {
				mixed = true
			}
			if mixed {
				mid := r.ReqStart + (r.ReqEnd-r.ReqStart)/2
				segs = append(segs,
					Segment{Start: r.ReqStart, End: mid, Speaker: letter("mix-a-" + r.Label), Text: "mix"},
					Segment{Start: mid, End: r.ReqEnd, Speaker: letter("mix-b-" + r.Label), Text: "mix"})
				continue
			}
			for _, v := range p.truth {
				emit(r, v.start, v.end, v.voice)
			}
		case RegionCore:
			split, doSplit := p.splitVoice[n]
			inCore := func(v voiceSpan) float64 {
				return max(0, min(v.end, r.SrcEnd.Seconds())-max(v.start, r.SrcStart.Seconds()))
			}
			var total, seen float64
			for _, v := range p.truth {
				if doSplit && v.voice == split {
					total += inCore(v)
				}
			}
			for _, v := range p.truth {
				voice := v.voice
				if doSplit && v.voice == split {
					seen += inCore(v)
					if seen > total/2 {
						voice = split + "-split"
					}
				}
				emit(r, v.start, v.end, voice)
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

// threeVoiceMeeting: v1 dominates, v2 and v3 speak in turns; all inside 0–600 s.
func threeVoiceMeeting() []voiceSpan {
	var spans []voiceSpan
	for t := 0.0; t < 600; t += 60 {
		spans = append(spans,
			voiceSpan{t, t + 30, "v1"},
			voiceSpan{t + 31, t + 45, "v2"},
			voiceSpan{t + 46, t + 58, "v3"},
		)
	}
	return spans
}

func withLaterVoices(spans []voiceSpan) []voiceSpan {
	for t := 600.0; t < 1200; t += 60 {
		spans = append(spans,
			voiceSpan{t, t + 20, "v1"},
			voiceSpan{t + 21, t + 40, "v4"},
			voiceSpan{t + 41, t + 58, "v5"},
		)
	}
	return spans
}

// voiceAt returns the truth voice with the most speech inside [a,b).
func (p *voiceProvider) voiceAt(a, b time.Duration) string {
	best, bestDur := "", time.Duration(0)
	for _, v := range p.truth {
		o := overlap(a, b, time.Duration(v.start*float64(time.Second)), time.Duration(v.end*float64(time.Second)))
		if o > bestDur {
			best, bestDur = v.voice, o
		}
	}
	return best
}

// arithmeticAssembler builds manifests without ffmpeg for coordinator tests.
type arithmeticAssembler struct {
	mu      sync.Mutex
	closed  bool
	removed int
}

func (a *arithmeticAssembler) Assemble(ctx context.Context, req AssembleRequest) (*RequestManifest, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m := manifestFor(req.Samples, req.Core)
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
		case <-p.hold:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if p.err != nil {
		return nil, p.err
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
