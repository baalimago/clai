package audio

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/baalimago/clai/internal/diag"
)

// maxVoices is the room the fixed prefix reserve holds: 8 slots of 11 s. (I5)
const maxVoices = 8

// The two requests of one merge link.
const (
	RequestMergeForward  RequestKind = "merge-forward"
	RequestMergeReversed RequestKind = "merge-reversed"
)

// RequestFunc transcribes one assembled request. Phase 3 injects the
// accounting, cache, and provider behind it.
type RequestFunc func(ctx context.Context, kind RequestKind, samples []SampleClip, core SourceInterval) (*RequestManifest, []Segment, error)

// SurveyCandidate is the best clip of one material label of one survey
// chunk.
type SurveyCandidate struct {
	// Chunk is the 0-based index of the planned core the label was heard in.
	Chunk int
	// Candidate is the clip as SampleExtractor ranked it: Label, Clip,
	// Speech, Clearance.
	Candidate
	// LabelSpeech is all the survey speech of Label in its chunk.
	LabelSpeech time.Duration
}

// ID names the clip in a merge request and in the report: "c2-A" for label
// A of the second chunk.
func (c SurveyCandidate) ID() string {
	return "c" + strconv.Itoa(c.Chunk+1) + "-" + c.Label
}

// CandidateError refuses the first bad candidate before any request: an
// "empty clip", a "negative chunk" or a "duplicate label", checked in that
// order.
type CandidateError struct {
	Candidate SurveyCandidate
	Reason    string
}

func (e *CandidateError) Error() string {
	return fmt.Sprintf("candidate %v of chunk %v: %v", e.Candidate.ID(), e.Candidate.Chunk+1, e.Reason)
}

// ClipVerdict is what one clip received in one merge request.
type ClipVerdict struct {
	// Label is the request-local label holding most of the clip's labelled
	// speech; "" when nothing in the clip was labelled.
	Label string
	// Share is Label's part of the clip's labelled speech, 0 to 1.
	Share float64
	// Settled is Share >= 0.80 (the dominance threshold of LabelMapper).
	Settled bool
}

// LinkOutcome says what one link did with one clip.
type LinkOutcome string

const (
	// LinkHeld: a voice's clip, settled in both orders; the voice goes on.
	LinkHeld LinkOutcome = "held"
	// LinkMerged: a voice's clip that shared both labels with an earlier
	// voice's clip; the two voices are one from here on.
	LinkMerged LinkOutcome = "merged"
	// LinkJoined: a candidate that shared both labels with a voice's clip
	// and joined that voice.
	LinkJoined LinkOutcome = "joined"
	// LinkNew: a candidate that shared both labels with no voice's clip and
	// began a voice.
	LinkNew LinkOutcome = "new"
	// LinkUnsettled: a clip not settled in both orders. A candidate is then
	// in no voice; a voice is unchanged by the link.
	LinkUnsettled LinkOutcome = "unsettled"
)

// LinkClip is one clip of one link.
type LinkClip struct {
	// ID is the voice ("voice-3") for a voice's clip, SurveyCandidate.ID for
	// a candidate.
	ID       string
	Clip     SourceInterval
	Forward  ClipVerdict
	Reversed ClipVerdict
	Outcome  LinkOutcome
	// Voice is the voice the clip belongs to after the link; "" for an
	// unsettled candidate.
	Voice string
}

// MergeLink is one link: the voices so far against one chunk's candidates.
type MergeLink struct {
	// Chunk is the 0-based chunk whose candidates the link carried.
	Chunk int
	// Clips are in the forward request's order.
	Clips []LinkClip
}

// MergedVoice is one voice the merge found.
type MergedVoice struct {
	// Voice is "voice-N", N counting voices in the order they began.
	Voice string
	// Speaker is the registry ID, or "" for a voice over the cap of 8.
	Speaker string
	// Clips are the voice's candidates, best first.
	Clips []SurveyCandidate
	// SurveySpeech is the sum of the clips' LabelSpeech.
	SurveySpeech time.Duration
}

// MergeReport says what the merge heard and decided, without transcript
// text.
type MergeReport struct {
	// Links holds one entry per link sent, in chunk order.
	Links []MergeLink
	// Voices holds every voice found: the kept ones in speaker order, then
	// the ones over the cap, most survey speech first.
	Voices []MergedVoice
}

// Found counts the voices the merge found, the ones over the cap of 8
// included.
func (r MergeReport) Found() int {
	return len(r.Voices)
}

// Merger builds the speaker registry of one recording from its survey
// candidates.
type Merger interface {
	// Merge returns the registry the chained merge of the candidates builds
	// and the report of how it was reached (architecture/audio.md, "Survey
	// and merge"; the clauses Merge.1–12 are in the survey-calibration
	// worklog's phase 1). No candidates is an empty registry and no error. A
	// request that fails ends the merge, cancels its link's other request and
	// names the link: "merge link for chunk N".
	Merge(ctx context.Context, candidates []SurveyCandidate) (Snapshot, MergeReport, error)
}

// ChainMerger is the Merger. Transcribe sends one request and is called
// with a zero core only.
type ChainMerger struct {
	Transcribe RequestFunc
}

func (m ChainMerger) Merge(ctx context.Context, candidates []SurveyCandidate) (Snapshot, MergeReport, error) {
	if err := checkCandidates(candidates); err != nil {
		return Snapshot{}, MergeReport{}, err
	}
	if len(candidates) == 0 {
		return Snapshot{}, MergeReport{}, nil
	}
	sorted := slices.Clone(candidates)
	slices.SortFunc(sorted, func(a, b SurveyCandidate) int {
		return cmp.Or(cmp.Compare(a.Chunk, b.Chunk), byClipStart(a.Clip.Start, b.Clip.Start, a.ID(), b.ID()))
	})
	chain := &voiceChain{}
	var links []MergeLink
	for _, chunk := range byChunk(sorted) {
		if len(chain.voices) == 0 {
			for _, c := range chunk {
				chain.begin(c)
			}
			continue
		}
		link, err := m.link(ctx, chain, chunk)
		if err != nil {
			return Snapshot{}, MergeReport{}, err
		}
		links = append(links, link)
	}
	snap, voices := chain.registry()
	return snap, MergeReport{Links: links, Voices: voices}, nil
}

const (
	reasonEmptyClip      = "empty clip"
	reasonNegativeChunk  = "negative chunk"
	reasonDuplicateLabel = "duplicate label"
)

func checkCandidates(candidates []SurveyCandidate) error {
	type chunkLabel struct {
		chunk int
		label string
	}
	seen := map[chunkLabel]bool{}
	for _, c := range candidates {
		key := chunkLabel{c.Chunk, c.Label}
		reason := ""
		switch {
		case c.Clip.End <= c.Clip.Start:
			reason = reasonEmptyClip
		case c.Chunk < 0:
			reason = reasonNegativeChunk
		case seen[key]:
			reason = reasonDuplicateLabel
		}
		if reason != "" {
			return &CandidateError{Candidate: c, Reason: reason}
		}
		seen[key] = true
	}
	return nil
}

// byChunk splits candidates sorted by chunk into one run per chunk.
func byChunk(sorted []SurveyCandidate) [][]SurveyCandidate {
	var runs [][]SurveyCandidate
	for i, c := range sorted {
		if i == 0 || c.Chunk != sorted[i-1].Chunk {
			runs = append(runs, nil)
		}
		runs[len(runs)-1] = append(runs[len(runs)-1], c)
	}
	return runs
}

func byClipStart(a, b time.Duration, idA, idB string) int {
	return cmp.Or(cmp.Compare(a, b), cmp.Compare(idA, idB))
}

// chainVoice is one voice while the chain runs; clips stay ranked.
type chainVoice struct {
	n     int
	clips []SurveyCandidate
}

func (v *chainVoice) name() string { return "voice-" + strconv.Itoa(v.n) }

func (v *chainVoice) add(clips ...SurveyCandidate) {
	v.clips = append(v.clips, clips...)
	slices.SortStableFunc(v.clips, func(a, b SurveyCandidate) int {
		return cmp.Or(cmp.Compare(b.Speech, a.Speech), cmp.Compare(b.Clearance, a.Clearance), byClipStart(a.Clip.Start, b.Clip.Start, a.ID(), b.ID()))
	})
}

func (v *chainVoice) earliest() time.Duration {
	first := v.clips[0].Clip.Start
	for _, c := range v.clips[1:] {
		first = min(first, c.Clip.Start)
	}
	return first
}

func (v *chainVoice) speech() time.Duration {
	total := time.Duration(0)
	for _, c := range v.clips {
		total += c.LabelSpeech
	}
	return total
}

// voiceChain holds the voices found so far, in the order they began.
type voiceChain struct {
	voices []*chainVoice
	began  int
}

func (ch *voiceChain) begin(c SurveyCandidate) *chainVoice {
	ch.began++
	v := &chainVoice{n: ch.began}
	v.add(c)
	ch.voices = append(ch.voices, v)
	return v
}

// linkKey is a clip's label in each order of one link.
type linkKey struct{ forward, reversed string }

// linkSlot is one clip of a link: a voice's best clip or a candidate.
type linkSlot struct {
	id    string
	clip  SourceInterval
	voice *chainVoice
	cand  SurveyCandidate
}

func (m ChainMerger) link(ctx context.Context, ch *voiceChain, chunk []SurveyCandidate) (MergeLink, error) {
	slots := make([]linkSlot, 0, len(ch.voices)+len(chunk))
	for _, v := range ch.voices {
		slots = append(slots, linkSlot{id: v.name(), clip: v.clips[0].Clip, voice: v})
	}
	for _, c := range chunk {
		slots = append(slots, linkSlot{id: c.ID(), clip: c.Clip, cand: c})
	}
	slices.SortFunc(slots, func(a, b linkSlot) int { return byClipStart(a.clip.Start, b.clip.Start, a.id, b.id) })
	forward := make([]SampleClip, 0, len(slots))
	for _, s := range slots {
		forward = append(forward, SampleClip{ID: s.id, Start: s.clip.Start, End: s.clip.End, Candidate: s.voice == nil})
	}
	reversed := slices.Clone(forward)
	slices.Reverse(reversed)
	n := chunk[0].Chunk
	verdicts, err := m.both(ctx, n, forward, reversed)
	if err != nil {
		return MergeLink{}, err
	}
	link := MergeLink{Chunk: n, Clips: make([]LinkClip, len(slots))}
	keys := map[string]linkKey{}
	for i, s := range slots {
		f, r := verdicts[0][s.id], verdicts[1][s.id]
		link.Clips[i] = LinkClip{ID: s.id, Clip: s.clip, Forward: f, Reversed: r, Outcome: LinkUnsettled}
		if s.voice != nil {
			link.Clips[i].Voice = s.voice.name()
		}
		if f.Settled && r.Settled {
			keys[s.id] = linkKey{f.Label, r.Label}
		}
	}
	decided := ch.decide(slots, keys)
	for i := range link.Clips {
		if d, ok := decided[link.Clips[i].ID]; ok {
			link.Clips[i].Outcome, link.Clips[i].Voice = d.outcome, d.voice
		}
	}
	return link, nil
}

type slotDecision struct {
	outcome LinkOutcome
	voice   string
}

// decide applies Merge.4 to one link's keys: voices first, in the order
// they began, then candidates in forward order.
func (ch *voiceChain) decide(slots []linkSlot, keys map[string]linkKey) map[string]slotDecision {
	decided := map[string]slotDecision{}
	owners := map[linkKey]*chainVoice{}
	alive := ch.voices[:0:0]
	for _, v := range ch.voices {
		key, ok := keys[v.name()]
		owner, taken := owners[key]
		switch {
		case !ok:
			alive = append(alive, v)
		case taken:
			owner.add(v.clips...)
			decided[v.name()] = slotDecision{LinkMerged, owner.name()}
		default:
			owners[key] = v
			alive = append(alive, v)
			decided[v.name()] = slotDecision{LinkHeld, v.name()}
		}
	}
	ch.voices = alive
	fresh := map[linkKey]*chainVoice{}
	for _, s := range slots {
		key, ok := keys[s.id]
		if s.voice != nil || !ok {
			continue
		}
		if owner, taken := owners[key]; taken {
			owner.add(s.cand)
			decided[s.id] = slotDecision{LinkJoined, owner.name()}
			continue
		}
		v, begun := fresh[key]
		if begun {
			v.add(s.cand)
		} else {
			v = ch.begin(s.cand)
			fresh[key] = v
		}
		decided[s.id] = slotDecision{LinkNew, v.name()}
	}
	return decided
}

type mergeAnswer struct {
	order    int
	verdicts map[string]ClipVerdict
	err      error
}

// both sends a link's two requests at once and returns each one's verdict
// per clip ID, forward first. The first failure cancels the other request.
func (m ChainMerger) both(ctx context.Context, chunk int, forward, reversed []SampleClip) ([2]map[string]ClipVerdict, error) {
	var out [2]map[string]ClipVerdict
	linkCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	answers := make(chan mergeAnswer, 2)
	for i, req := range []struct {
		kind    RequestKind
		samples []SampleClip
	}{{RequestMergeForward, forward}, {RequestMergeReversed, reversed}} {
		go func() {
			verdicts, err := m.request(linkCtx, req.kind, req.samples)
			answers <- mergeAnswer{order: i, verdicts: verdicts, err: err}
		}()
	}
	var failed error
	for range 2 {
		a := <-answers
		if a.err != nil && failed == nil {
			failed = a.err
			cancel()
		}
		out[a.order] = a.verdicts
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if failed != nil {
		return out, diag.Errorf("merge link for chunk %v: %w", chunk+1, failed)
	}
	return out, nil
}

func (m ChainMerger) request(ctx context.Context, kind RequestKind, samples []SampleClip) (map[string]ClipVerdict, error) {
	manifest, segs, err := m.Transcribe(ctx, kind, samples, SourceInterval{})
	if err != nil {
		return nil, err
	}
	if manifest == nil || len(manifest.Regions) == 0 {
		return nil, fmt.Errorf("%v request answered without a manifest", kind)
	}
	out := map[string]ClipVerdict{}
	for _, s := range labelledSamples(manifest, segs) {
		out[s.ID] = ClipVerdict{Label: s.Label, Share: s.Share, Settled: settled(s)}
	}
	return out, nil
}

// labelledSamples is each sample region's verdict, in request order, over
// the labelled segments only: a segment with no speaker is no label (L1.7,
// L2.11).
func labelledSamples(manifest *RequestManifest, segs []Segment) []SampleResult {
	labelled := slices.DeleteFunc(slices.Clone(segs), func(s Segment) bool { return s.Speaker == "" })
	return (LabelMapper{}).Map(manifest, labelled).Samples
}

// settled is one label holding at least 80 % of a clip's labelled speech.
func settled(s SampleResult) bool { return s.Labeled > 0 && s.Share >= dominantShare }

// registry keeps the 8 voices with the most survey speech and numbers them
// by their earliest clip (Merge.6).
func (ch *voiceChain) registry() (Snapshot, []MergedVoice) {
	ranked := slices.Clone(ch.voices)
	slices.SortFunc(ranked, func(a, b *chainVoice) int {
		return cmp.Or(cmp.Compare(b.speech(), a.speech()), cmp.Compare(a.earliest(), b.earliest()), cmp.Compare(a.n, b.n))
	})
	kept, over := ranked[:min(len(ranked), maxVoices)], ranked[min(len(ranked), maxVoices):]
	slices.SortFunc(kept, func(a, b *chainVoice) int {
		return cmp.Or(cmp.Compare(a.earliest(), b.earliest()), cmp.Compare(a.n, b.n))
	})
	snap := Snapshot{Speakers: make([]Speaker, 0, len(kept))}
	voices := make([]MergedVoice, 0, len(ranked))
	for i, v := range kept {
		id := "speaker-" + strconv.Itoa(i+1)
		sp := Speaker{ID: id, Clips: make([]SampleClip, 0, len(v.clips))}
		for _, c := range v.clips {
			sp.Clips = append(sp.Clips, SampleClip{ID: id, Start: c.Clip.Start, End: c.Clip.End})
		}
		sp.Sample = sp.Clips[0]
		snap.Speakers = append(snap.Speakers, sp)
		voices = append(voices, v.merged(id))
	}
	for _, v := range over {
		voices = append(voices, v.merged(""))
	}
	return snap, voices
}

func (v *chainVoice) merged(speaker string) MergedVoice {
	return MergedVoice{Voice: v.name(), Speaker: speaker, Clips: slices.Clone(v.clips), SurveySpeech: v.speech()}
}
