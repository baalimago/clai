package audio

import (
	"context"
	"fmt"
	"hash/fnv"
	"slices"
	"strings"
	"sync"
	"time"
)

// heldClip keys a fault by what a request holds (L1.3): a request of kind
// ("" for either merge kind) with a clip containing the source instant at.
type heldClip struct {
	kind RequestKind
	at   time.Duration
}

func (h heldClip) in(kind RequestKind, samples []SampleClip) bool {
	if h.kind != "" && h.kind != kind {
		return false
	}
	return slices.ContainsFunc(samples, func(s SampleClip) bool { return s.Start <= h.at && h.at < s.End })
}

type mergeCall struct {
	kind    RequestKind
	samples []SampleClip
	core    SourceInterval
}

// mergeDiarizer is the scripted diarizer for requests that are in flight
// together. It labels from the voice provider's truth timeline, derives each
// request's letters from what the request holds rather than from its
// number, and keys every fault by what a request holds.
type mergeDiarizer struct {
	voices *voiceProvider
	// peers holds each request until this many are in flight at once
	peers int
	// reversedFirst holds each merge-forward answer until a merge-reversed
	// request has answered
	reversedFirst bool
	// share labels the held clip with its own voice for this part of it and
	// with another label for the rest; 0 labels nothing in it
	share map[heldClip]float64
	// sameLabel gives the second voice the first one's label in the request
	sameLabel map[heldClip][2]string
	// unlabelled answers the held clip with segments that carry no speaker
	unlabelled map[heldClip]bool
	fail       map[heldClip]error
	// block holds the request until its context ends
	block map[heldClip]bool
	// arrived is told every request as it starts
	arrived func(kind RequestKind, samples []SampleClip)

	mu         sync.Mutex
	calls      []mergeCall
	events     []string
	waiting    int
	gate       chan struct{}
	reversedOK chan struct{}
	cancelled  int
}

func newMergeDiarizer(truth []voiceSpan) *mergeDiarizer {
	return &mergeDiarizer{voices: &voiceProvider{truth: truth}, reversedOK: make(chan struct{}, 64)}
}

func (d *mergeDiarizer) requests() []mergeCall {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.calls)
}

func (d *mergeDiarizer) log() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.events)
}

func (d *mergeDiarizer) note(event string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, event)
}

func (d *mergeDiarizer) transcribe(ctx context.Context, kind RequestKind, samples []SampleClip, core SourceInterval) (*RequestManifest, []Segment, error) {
	name := callName(kind, samples)
	d.mu.Lock()
	d.calls = append(d.calls, mergeCall{kind: kind, samples: slices.Clone(samples), core: core})
	d.events = append(d.events, "start "+name)
	d.mu.Unlock()
	if d.arrived != nil {
		d.arrived(kind, samples)
	}
	if err := d.awaitPeers(ctx); err != nil {
		return nil, nil, err
	}
	for h := range d.block {
		if h.in(kind, samples) {
			<-ctx.Done()
			d.mu.Lock()
			d.cancelled++
			d.mu.Unlock()
			return nil, nil, ctx.Err()
		}
	}
	for h, err := range d.fail {
		if h.in(kind, samples) {
			return nil, nil, err
		}
	}
	if d.reversedFirst && kind == RequestMergeForward {
		select {
		case <-d.reversedOK:
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
	}
	m := manifestFor(samples, core)
	segs := d.label(kind, samples, m)
	d.note("answer " + name)
	if kind == RequestMergeReversed {
		select {
		case d.reversedOK <- struct{}{}:
		default:
		}
	}
	return m, segs, nil
}

func (d *mergeDiarizer) awaitPeers(ctx context.Context) error {
	if d.peers < 2 {
		return nil
	}
	d.mu.Lock()
	if d.gate == nil {
		d.gate = make(chan struct{})
	}
	gate := d.gate
	d.waiting++
	if d.waiting == d.peers {
		close(gate)
		d.gate, d.waiting = nil, 0
	}
	d.mu.Unlock()
	select {
	case <-gate:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (d *mergeDiarizer) label(kind RequestKind, samples []SampleClip, m *RequestManifest) []Segment {
	segs := d.voices.label(m, 0)
	for h, pair := range d.sameLabel {
		if !h.in(kind, samples) {
			continue
		}
		into := ""
		for _, s := range segs {
			if s.Text == pair[0] {
				into = s.Speaker
			}
		}
		for i := range segs {
			if segs[i].Text == pair[1] && into != "" {
				segs[i].Speaker = into
			}
		}
	}
	for h, share := range d.share {
		if h.in(kind, samples) {
			segs = reshare(segs, m, h.at, share)
		}
	}
	shift := contentShift(kind, samples)
	for i := range segs {
		if l := segs[i].Speaker; len(l) == 1 && l[0] >= 'A' && l[0] <= 'Z' {
			segs[i].Speaker = string(rune('A' + (int(l[0]-'A')+shift)%26))
		}
	}
	for h := range d.unlabelled {
		r, ok := regionAt(m, h.at)
		if !ok || !h.in(kind, samples) {
			continue
		}
		for i := range segs {
			if overlap(segs[i].Start, segs[i].End, r.ReqStart, r.ReqEnd) > 0 {
				segs[i].Speaker = ""
			}
		}
	}
	return segs
}

// regionAt is the sample region holding the source instant at.
func regionAt(m *RequestManifest, at time.Duration) (Region, bool) {
	for _, r := range m.Regions {
		if r.Kind == RegionSample && r.SrcStart <= at && at < r.SrcEnd {
			return r, true
		}
	}
	return Region{}, false
}

// reshare relabels the sample region holding the source instant at: its
// own voice holds share of it, the label "x-<clip>" the rest.
func reshare(segs []Segment, m *RequestManifest, at time.Duration, share float64) []Segment {
	r, ok := regionAt(m, at)
	if !ok {
		return segs
	}
	own, ownText, longest := "", "", time.Duration(0)
	kept := segs[:0:0]
	for _, s := range segs {
		if overlap(s.Start, s.End, r.ReqStart, r.ReqEnd) == 0 {
			kept = append(kept, s)
			continue
		}
		if s.End-s.Start > longest {
			own, ownText, longest = s.Speaker, s.Text, s.End-s.Start
		}
	}
	if share == 0 {
		return kept
	}
	cut := r.ReqStart + time.Duration(float64(r.ReqEnd-r.ReqStart)*share)
	kept = append(kept, Segment{Start: r.ReqStart, End: cut, Speaker: own, Text: ownText})
	if cut < r.ReqEnd {
		kept = append(kept, Segment{Start: cut, End: r.ReqEnd, Speaker: "x-" + r.Label, Text: "mix"})
	}
	return kept
}

// contentShift permutes a request's letters by what it holds, so that equal
// requests get equal letters whatever order they arrive in, and the two
// orders of one link get different ones.
func contentShift(kind RequestKind, samples []SampleClip) int {
	h := fnv.New32a()
	h.Write([]byte(callName(kind, samples)))
	return int(h.Sum32() % 26)
}

func callName(kind RequestKind, samples []SampleClip) string {
	ids := make([]string, 0, len(samples))
	for _, s := range samples {
		ids = append(ids, s.ID)
	}
	return fmt.Sprintf("%v[%v]", kind, strings.Join(ids, " "))
}
