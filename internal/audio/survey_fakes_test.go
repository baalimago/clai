package audio

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
)

// requestClass is what a RequestTranscriber can tell of a request from its
// manifest alone: a survey has a core and no sample, a merge request
// samples and no core (forward when they run in source order), and a
// prefixed request (labelling or retry) both.
type requestClass string

const (
	classSurvey        requestClass = "survey"
	classMergeForward  requestClass = "merge-forward"
	classMergeReversed requestClass = "merge-reversed"
	classPrefixed      requestClass = "prefixed"
)

// anyCore and noClip leave a heldBy's core or clip unconstrained.
const (
	anyCore = time.Duration(-1)
	noClip  = time.Duration(-1)
)

// heldBy keys a fault by what a request holds (L1.3): its class ("merge"
// matches both orders, "" any), the start of its core (anyCore for any),
// its turn among its core's distinct prefixed requests (1 labelling, 2
// retry, 0 any) and a sample holding the source instant at (noClip for
// none needed).
type heldBy struct {
	class requestClass
	core  time.Duration
	turn  int
	at    time.Duration
}

// seenRequest is one request as the diarizer saw it.
type seenRequest struct {
	class   requestClass
	core    SourceInterval
	turn    int
	samples []SampleClip
}

func (h heldBy) matches(r seenRequest) bool {
	switch {
	case h.class != "" && !strings.HasPrefix(string(r.class), string(h.class)):
		return false
	case h.core != anyCore && r.core.Start != h.core:
		return false
	case h.turn != 0 && r.turn != h.turn:
		return false
	case h.at == noClip:
		return true
	}
	return slices.ContainsFunc(r.samples, func(s SampleClip) bool { return s.Start <= h.at && h.at < s.End })
}

// voiceRule labels voice's speech as voice as's in the requests it holds:
// in the core, or in the clip holding when.at when inClip is set. An as
// that does not speak in the request gets a letter of its own.
type voiceRule struct {
	when      heldBy
	voice, as string
	inClip    bool
}

// chunkDiarizer is the scripted diarizer behind the coordinator. It sees a
// request only as its manifest, labels every region from the truth
// timeline, derives each request's letters from what the request holds,
// and keys every fault by what a request holds, so requests in flight
// together get the same answers whatever order they arrive in.
type chunkDiarizer struct {
	truth []voiceSpan
	rules []voiceRule
	// mix labels the clip a request holds at half with its own voice and
	// half with a label of its own
	mix []heldBy
	// share labels the clip a request holds at with its own voice for this
	// part of it and with a label of its own for the rest
	share map[heldBy]float64
	// fail answers matching requests with these errors, one per send
	fail map[heldBy][]error
	// block holds a matching request until its context ends, telling blocked
	block   map[heldBy]bool
	blocked chan seenRequest
	// peers holds a request until this many of its class are in flight
	peers map[requestClass]int
	// textIsClass writes the request's class into every segment's text
	textIsClass bool
	// shift overrides the letter shift derived from the request's content
	shift func(seenRequest) int

	mu        sync.Mutex
	seen      []seenRequest
	turns     map[time.Duration][]string
	inFlight  map[requestClass]int
	most      map[requestClass]int
	total     int
	mostTotal int
	gates     map[requestClass]chan struct{}
	waiting   map[requestClass]int
	gated     map[string]bool
	opened    map[requestClass]chan struct{}
	cancelled int
}

func newChunkDiarizer(truth []voiceSpan) *chunkDiarizer {
	return &chunkDiarizer{truth: truth, fail: map[heldBy][]error{}}
}

func (d *chunkDiarizer) requests() []seenRequest {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.seen)
}

func (d *chunkDiarizer) count(class requestClass) int {
	n := 0
	for _, r := range d.requests() {
		if strings.HasPrefix(string(r.class), string(class)) {
			n++
		}
	}
	return n
}

func classOf(m *RequestManifest) requestClass {
	samples := samplesOfManifest(m)
	switch {
	case len(samples) == 0:
		return classSurvey
	case m.CoreEnd > m.CoreStart:
		return classPrefixed
	case slices.IsSortedFunc(samples, func(a, b SampleClip) int { return byClipStart(a.Start, b.Start, a.ID, b.ID) }):
		return classMergeForward
	default:
		return classMergeReversed
	}
}

func (d *chunkDiarizer) arrive(m *RequestManifest) seenRequest {
	r := seenRequest{class: classOf(m), core: SourceInterval{Start: m.CoreStart, End: m.CoreEnd}, samples: samplesOfManifest(m)}
	d.mu.Lock()
	defer d.mu.Unlock()
	if r.class == classPrefixed {
		if d.turns == nil {
			d.turns = map[time.Duration][]string{}
		}
		name := fmt.Sprint(r.samples)
		at := slices.Index(d.turns[r.core.Start], name)
		if at < 0 {
			d.turns[r.core.Start] = append(d.turns[r.core.Start], name)
			at = len(d.turns[r.core.Start]) - 1
		}
		r.turn = at + 1
	}
	d.seen = append(d.seen, r)
	if d.inFlight == nil {
		d.inFlight, d.most = map[requestClass]int{}, map[requestClass]int{}
	}
	d.inFlight[r.class]++
	d.most[r.class] = max(d.most[r.class], d.inFlight[r.class])
	d.total++
	d.mostTotal = max(d.mostTotal, d.total)
	return r
}

func (d *chunkDiarizer) leave(r seenRequest) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.inFlight[r.class]--
	d.total--
}

func (d *chunkDiarizer) TranscribeRequest(ctx context.Context, m *RequestManifest) ([]Segment, error) {
	r := d.arrive(m)
	defer d.leave(r)
	if err := d.awaitPeers(ctx, r); err != nil {
		return nil, err
	}
	for h := range d.block {
		if h.matches(r) {
			if d.blocked != nil {
				d.blocked <- r
			}
			<-ctx.Done()
			d.mu.Lock()
			d.cancelled++
			d.mu.Unlock()
			return nil, ctx.Err()
		}
	}
	if err := d.nextFailure(r); err != nil {
		return nil, err
	}
	return d.label(m, r), nil
}

func (d *chunkDiarizer) nextFailure(r seenRequest) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for h, errs := range d.fail {
		if len(errs) > 0 && h.matches(r) {
			d.fail[h] = errs[1:]
			return errs[0]
		}
	}
	return nil
}

// awaitPeers holds a request's first send until peers of its group are
// waiting; a request sent again passes.
func (d *chunkDiarizer) awaitPeers(ctx context.Context, r seenRequest) error {
	group := groupOf(r.class)
	want := d.peers[group]
	name := fmt.Sprint(r.class, r.core, r.samples)
	d.mu.Lock()
	if want < 2 || d.gated[name] {
		d.mu.Unlock()
		return nil
	}
	if d.gates == nil {
		d.gates, d.waiting, d.gated = map[requestClass]chan struct{}{}, map[requestClass]int{}, map[string]bool{}
	}
	d.gated[name] = true
	gate, ok := d.gates[group]
	if !ok {
		gate = make(chan struct{})
		d.gates[group] = gate
	}
	d.waiting[group]++
	if d.waiting[group] == want {
		close(gate)
		delete(d.gates, group)
		d.waiting[group] = 0
		d.openedLocked(group)
	}
	d.mu.Unlock()
	select {
	case <-gate:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func groupOf(class requestClass) requestClass {
	if strings.HasPrefix(string(class), "merge") {
		return "merge"
	}
	return class
}

// gateOpened is closed once the gate of a group has let its peers through.
func (d *chunkDiarizer) gateOpened(group requestClass) <-chan struct{} {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.openedChan(group)
}

func (d *chunkDiarizer) openedChan(group requestClass) chan struct{} {
	if d.opened == nil {
		d.opened = map[requestClass]chan struct{}{}
	}
	ch, ok := d.opened[group]
	if !ok {
		ch = make(chan struct{})
		d.opened[group] = ch
	}
	return ch
}

func (d *chunkDiarizer) openedLocked(group requestClass) {
	ch := d.openedChan(group)
	select {
	case <-ch:
	default:
		close(ch)
	}
}

// label diarizes a request from the truth timeline: every voice keeps one
// letter within the request, in order of first appearance, shifted by the
// request's content.
func (d *chunkDiarizer) label(m *RequestManifest, r seenRequest) []Segment {
	var segs []Segment
	for i, reg := range m.Regions {
		if reg.Kind == RegionGap {
			continue
		}
		for _, v := range d.truth {
			a := time.Duration(v.start * float64(time.Second))
			b := time.Duration(v.end * float64(time.Second))
			start, end := max(a, reg.SrcStart), min(b, reg.SrcEnd)
			if end <= start {
				continue
			}
			voice := d.voiceIn(r, reg, v.voice)
			segs = append(segs, Segment{Start: reg.ReqStart + start - reg.SrcStart, End: reg.ReqStart + end - reg.SrcStart, Speaker: voice, Text: voice})
		}
		if reg.Kind == RegionSample && slices.ContainsFunc(d.mix, func(h heldBy) bool { return h.matches(r) && reg.SrcStart <= h.at && h.at < reg.SrcEnd }) {
			segs = mixRegion(segs, reg, i)
		}
	}
	for h, share := range d.share {
		if h.matches(r) {
			segs = reshare(segs, m, h.at, share)
		}
	}
	shift := contentShift(RequestKind(r.class), r.samples) + int(r.core.Start/time.Second)
	if d.shift != nil {
		shift = d.shift(r)
	}
	letters := map[string]string{"": ""}
	for i := range segs {
		l, ok := letters[segs[i].Speaker]
		if !ok {
			l = string(rune('A' + (len(letters)-1+shift)%26))
			letters[segs[i].Speaker] = l
		}
		segs[i].Speaker = l
		if d.textIsClass {
			segs[i].Text = string(r.class)
			if r.class == classMergeForward || r.class == classMergeReversed {
				segs[i].Text = "merge"
			}
		}
	}
	return segs
}

// voiceIn is who the diarizer hears for voice in one region of a request.
func (d *chunkDiarizer) voiceIn(r seenRequest, reg Region, voice string) string {
	for _, rule := range d.rules {
		if rule.voice != voice || !rule.when.matches(r) {
			continue
		}
		inClip := reg.Kind == RegionSample && reg.SrcStart <= rule.when.at && rule.when.at < reg.SrcEnd
		if (rule.inClip && inClip) || (!rule.inClip && reg.Kind == RegionCore) {
			return rule.as
		}
	}
	return voice
}

// mixRegion relabels a sample region half with its own voice and half with
// a voice of its own.
func mixRegion(segs []Segment, reg Region, index int) []Segment {
	kept := slices.DeleteFunc(segs, func(s Segment) bool { return overlap(s.Start, s.End, reg.ReqStart, reg.ReqEnd) > 0 })
	own := reg.Label
	mid := reg.ReqStart + (reg.ReqEnd-reg.ReqStart)/2
	return append(kept,
		Segment{Start: reg.ReqStart, End: mid, Speaker: "mixed-own-" + own, Text: "mix"},
		Segment{Start: mid, End: reg.ReqEnd, Speaker: fmt.Sprintf("mixed-%d", index), Text: "mix"})
}

// surveyFor builds a coordinator over the chunk diarizer and the
// arithmetic assembler: four 450 s cores of an 1800 s source, every chunk
// at once.
func surveyFor(d *chunkDiarizer) (*Coordinator, *arithmeticAssembler, *lockedWriter) {
	asm := &arithmeticAssembler{}
	status := &lockedWriter{w: &strings.Builder{}}
	c := &Coordinator{
		Runner:      &scriptedRunner{duration: "1800.000000"},
		Budgets:     testBudgets(),
		Transcriber: d,
		NewAssembler: func(CommandRunner, Budgets, string, time.Duration) (RequestAssembler, error) {
			return asm, nil
		},
		Model:     "gpt-4o-transcribe-diarize",
		Endpoint:  "https://example.test/v1/audio/transcriptions",
		Options:   "diarized_json",
		StatusOut: status,
	}
	return c, asm, status
}

// lockedWriter serializes writes of the board and the test's reads.
type lockedWriter struct {
	mu sync.Mutex
	w  *strings.Builder
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func (l *lockedWriter) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.String()
}

// meetingOf is a four-chunk meeting of 450 s cores: each voice of a chunk
// speaks eight turns of 15 s inside it.
func meetingOf(pattern ...[]string) []voiceSpan {
	var spans []voiceSpan
	for chunk, voices := range pattern {
		base := float64(chunk) * 450
		step := 15.0 + 3
		round := float64(len(voices)) * step
		for t := 0.0; t+round <= 448; t += round {
			for i, v := range voices {
				start := base + t + float64(i)*step
				spans = append(spans, voiceSpan{start, start + 15, v})
			}
		}
	}
	return spans
}

// twoInChunkOne is six people over four chunks, two of them in chunk 1.
func twoInChunkOne() []voiceSpan {
	return meetingOf([]string{"v1", "v2"}, []string{"v1", "v3", "v4"}, []string{"v2", "v5", "v1"}, []string{"v6", "v1", "v3"})
}

// firstTurn is the source start of a voice's first turn in a chunk.
func firstTurn(truth []voiceSpan, voice string, chunk int) time.Duration {
	for _, v := range truth {
		if v.voice == voice && v.start >= float64(chunk)*450 && v.start < float64(chunk+1)*450 {
			return time.Duration(v.start * float64(time.Second))
		}
	}
	return -1
}
