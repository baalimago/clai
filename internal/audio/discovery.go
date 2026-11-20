package audio

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/baalimago/clai/internal/diag"
)

const (
	discoveryIterations = 2

	reasonCollapsedOntoKnown = "collapsed-onto-known"
	reasonSameVoice          = "same-voice-as-promoted"
	reasonMixed              = "mixed"
	reasonMissing            = "missing"
	reasonAmbiguousKnown     = "ambiguous-known-samples"
	reasonNoRun              = "no-run-of-sample-length"
	reasonClipsTried         = "every-clip-tried"
)

// RequestFunc transcribes one assembled request. Phase 3 injects the
// accounting, cache, and provider behind it.
type RequestFunc func(ctx context.Context, kind RequestKind, samples []SampleClip, core SourceInterval) (*RequestManifest, []Segment, error)

// CandidateRejection explains why a discovery clip did not become a speaker.
type CandidateRejection struct {
	Label  string
	Clip   SourceInterval
	Reason string
}

type DiscoveryReport struct {
	Attempts int
	Promoted []string
	Rejected []CandidateRejection
}

// Discovery drives bootstrap and later-chunk speaker discovery against the
// registry. All discovery requests run under the registry's discovery lock.
type Discovery struct {
	Registry   *SpeakerRegistry
	Transcribe RequestFunc
	Extractor  SampleExtractor
	Mapper     LabelMapper
	Log        EventLog
}

// Pass transcribes core with the snapshot's samples and maps the result.
func (d *Discovery) Pass(ctx context.Context, core SourceInterval, snap Snapshot) (Mapping, error) {
	return d.request(ctx, RequestForward, snap.Samples(), core)
}

// PassWith transcribes core with an explicit prefix order (the recovery
// retry order for an ambiguity).
func (d *Discovery) PassWith(ctx context.Context, core SourceInterval, samples []SampleClip) (Mapping, error) {
	return d.request(ctx, RequestReversedRetry, samples, core)
}

func (d *Discovery) request(ctx context.Context, kind RequestKind, samples []SampleClip, core SourceInterval) (Mapping, error) {
	m, segs, err := d.Transcribe(ctx, kind, samples, core)
	if err != nil {
		return Mapping{}, err
	}
	mapping := d.Mapper.Map(m, segs)
	logResponse(ctx, d.Log, len(segs), mapping)
	return mapping, nil
}

// Bootstrap transcribes the first core uncalibrated, then once more with a
// candidate for every material label; a second candidate iteration runs
// only when a candidate was mixed.
func (d *Discovery) Bootstrap(ctx context.Context, core SourceInterval) (Mapping, DiscoveryReport, error) {
	first, err := d.request(ctx, RequestBootstrap, nil, core)
	if err != nil {
		return Mapping{}, DiscoveryReport{}, diag.Errorf("bootstrap pass failed: %w", err)
	}
	return d.discover(ctx, core, first, d.Registry.Snapshot(), true)
}

// Resolve runs discovery for a later chunk whose first pass left material
// labels unmapped.
func (d *Discovery) Resolve(ctx context.Context, core SourceInterval, first Mapping, snap Snapshot) (Mapping, DiscoveryReport, error) {
	return d.discover(ctx, core, first, snap, false)
}

type candidate struct {
	Candidate
	id string
}

func (d *Discovery) discover(ctx context.Context, core SourceInterval, current Mapping, snap Snapshot, bootstrap bool) (Mapping, DiscoveryReport, error) {
	var rep DiscoveryReport
	var tried []SourceInterval
	kind := RequestDiscovery
	if bootstrap {
		kind = RequestBootstrapCandidates
	}
	stop := func(reason string, pending []UnmappedLabel) {
		state := "stop"
		if rep.Attempts == 0 {
			state = "skipped"
		}
		if len(pending) == 0 {
			d.Log.emit(ctx, "discovery", "state=%s reason=%s", state, reason)
			return
		}
		d.Log.emit(ctx, "discovery", "state=%s reason=%s pending=%s", state, reason, pendingText(pending))
	}
	err := d.Registry.Discover(ctx, func(tx *RegistryTx) error {
		for {
			pending := current.MaterialUnmapped()
			if len(pending) == 0 {
				if rep.Attempts == 0 {
					stop("no-material-unmapped", nil)
				} else {
					stop("resolved", nil)
				}
				return nil
			}
			if rep.Attempts >= discoveryIterations {
				stop("iterations-exhausted", pending)
				return nil
			}
			if rep.Attempts > 0 && bootstrap && !hadMixed(current) {
				stop("no-mixed-candidate", pending)
				return nil
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			fresh := tx.Snapshot()
			cands := d.candidates(ctx, current, pending, tried)
			if len(cands) == 0 {
				for _, u := range pending {
					rep.Rejected = append(rep.Rejected, CandidateRejection{Label: u.Label, Reason: reasonNoClip})
				}
				stop(reasonNoClip, pending)
				return nil
			}
			samples := fresh.Samples()
			for _, c := range cands {
				tried = append(tried, c.Clip)
				samples = append(samples, SampleClip{ID: c.id, Start: c.Clip.Start, End: c.Clip.End, Candidate: true})
			}
			rep.Attempts++
			d.Log.emit(ctx, "discovery", "iteration=%d state=start pending=%s candidates=%d speakers=%d version=%d",
				rep.Attempts, pendingText(pending), len(cands), len(fresh.Speakers), fresh.Version)
			next, err := d.request(ctx, kind, samples, core)
			if err != nil {
				return diag.Errorf("discovery request %v failed: %w", rep.Attempts, err)
			}
			promoted, rejected := len(rep.Promoted), len(rep.Rejected)
			d.promote(ctx, tx, &next, cands, &rep)
			current = next
			d.Log.emit(ctx, "discovery", "iteration=%d state=done promoted=%s rejected=%s pending=%s", rep.Attempts,
				orDash(strings.Join(rep.Promoted[promoted:], ",")), rejectedText(rep.Rejected[rejected:]), pendingText(current.MaterialUnmapped()))
		}
	})
	return current, rep, err
}

func hadMixed(m Mapping) bool {
	for _, s := range m.Samples {
		if s.Candidate && s.Status == SampleMixed {
			return true
		}
	}
	return false
}

// candidates extracts the best untried clip for every pending label.
func (d *Discovery) candidates(ctx context.Context, m Mapping, pending []UnmappedLabel, tried []SourceInterval) []candidate {
	var out []candidate
	for i, u := range pending {
		clips := d.Extractor.Candidates(m.Core, u.Label)
		at := slices.IndexFunc(clips, func(c Candidate) bool { return !overlapsAny(c.Clip, tried) })
		if at < 0 {
			reason := reasonClipsTried
			if len(clips) == 0 {
				reason = reasonNoRun
			}
			d.Log.emit(ctx, "candidate", "label=%s id=- reason=%s clips=%d", u.Label, reason, len(clips))
			continue
		}
		c := candidate{Candidate: clips[at], id: "cand-" + strconv.Itoa(i) + "-" + u.Label}
		d.Log.emit(ctx, "candidate", "label=%s id=%s clip=%s speech=%s clearance=%s", u.Label, c.id, span(c.Clip.Start, c.Clip.End), secs(c.Speech), secs(c.Clearance))
		out = append(out, c)
	}
	return out
}

func overlapsAny(c SourceInterval, tried []SourceInterval) bool {
	for _, t := range tried {
		if overlap(c.Start, c.End, t.Start, t.End) > 0 {
			return true
		}
	}
	return false
}

// promote applies the candidate outcomes of one discovery request through
// the mutation table and relabels the mapping accordingly.
func (d *Discovery) promote(ctx context.Context, tx *RegistryTx, m *Mapping, cands []candidate, rep *DiscoveryReport) {
	byID := map[string]candidate{}
	for _, c := range cands {
		byID[c.id] = c
	}
	// Candidates sharing one label are one voice: keep the better clip
	groups := map[string][]SampleResult{}
	for _, s := range m.Samples {
		if s.Candidate && s.Status == SampleCollapsed && s.Onto == "" {
			groups[s.Label] = append(groups[s.Label], s)
		}
	}
	for label, group := range groups {
		best := group[0]
		for _, s := range group[1:] {
			if better(byID[s.ID].Candidate, byID[best.ID].Candidate) {
				best = s
			}
		}
		for _, s := range group {
			if s.ID != best.ID {
				rep.Rejected = append(rep.Rejected, CandidateRejection{Label: label, Clip: byID[s.ID].Clip, Reason: reasonSameVoice})
			}
		}
		d.add(ctx, tx, m, label, byID[best.ID], rep)
	}
	for _, s := range m.Samples {
		if !s.Candidate {
			continue
		}
		c := byID[s.ID]
		switch s.Status {
		case SampleMapped:
			d.add(ctx, tx, m, s.Label, c, rep)
		case SampleCollapsed:
			if s.Onto != "" {
				rep.Rejected = append(rep.Rejected, CandidateRejection{Label: s.Label, Clip: c.Clip, Reason: reasonCollapsedOntoKnown})
			}
		case SampleMixed:
			rep.Rejected = append(rep.Rejected, CandidateRejection{Label: c.Label, Clip: c.Clip, Reason: reasonMixed})
		case SampleMissing:
			rep.Rejected = append(rep.Rejected, CandidateRejection{Label: c.Label, Clip: c.Clip, Reason: reasonMissing})
		case SampleAmbiguous:
			rep.Rejected = append(rep.Rejected, CandidateRejection{Label: s.Label, Clip: c.Clip, Reason: reasonAmbiguousKnown})
		}
	}
}

func (d *Discovery) add(ctx context.Context, tx *RegistryTx, m *Mapping, label string, c candidate, rep *DiscoveryReport) {
	var ranges []SourceInterval
	for _, s := range m.Core {
		if s.Speaker == label || s.Speaker == c.id {
			ranges = append(ranges, SourceInterval{Start: s.Start, End: s.End})
		}
	}
	id, reason, ok := tx.AddSpeaker(SampleClip{Start: c.Clip.Start, End: c.Clip.End}, ranges)
	if !ok {
		d.logRegistry(ctx, tx, "op=refuse-add label=%s reason=%s", label, reason)
		rep.Rejected = append(rep.Rejected, CandidateRejection{Label: label, Clip: c.Clip, Reason: reason})
		// The label maps to nothing: undo the provisional candidate mapping
		// and report it unmapped with the refusal reason
		delete(m.LabelToID, label)
		m.unmapped[label] = true
		speech := time.Duration(0)
		for i := range m.Core {
			if m.Core[i].Speaker == c.id {
				m.Core[i].Speaker = label
			}
			if m.Core[i].Speaker == label {
				speech += m.Core[i].End - m.Core[i].Start
			}
		}
		m.Unmapped = append(m.Unmapped, UnmappedLabel{Label: label, Speech: speech, Material: true, Reason: reason, Intervals: ranges})
		return
	}
	d.logRegistry(ctx, tx, "op=add id=%s label=%s sample=%s", id, label, span(c.Clip.Start, c.Clip.End))
	rep.Promoted = append(rep.Promoted, id)
	m.resolve(label, c.id, id)
}

// logRegistry records one mutation or refusal with the version it left.
func (d *Discovery) logRegistry(ctx context.Context, tx *RegistryTx, format string, args ...any) {
	if d.Log == nil {
		return
	}
	d.Log(ctx, "registry", "%s version=%d", fmt.Sprintf(format, args...), tx.Snapshot().Version)
}

// Recover applies the replacement row for every failed sample in m, then
// retries the chunk once against the refreshed snapshot. It returns the
// retry's mapping and the speakers whose sample this call replaced.
func (d *Discovery) Recover(ctx context.Context, core SourceInterval, m Mapping, snap Snapshot) (Mapping, []string, error) {
	applied, err := d.Replace(ctx, m, snap)
	if err != nil {
		return Mapping{}, nil, err
	}
	retry, err := d.request(ctx, RequestReplacementRetry, d.Registry.Snapshot().Samples(), core)
	if err != nil {
		return Mapping{}, applied, diag.Errorf("replacement retry failed: %w", err)
	}
	return retry, applied, nil
}

// Replace applies the replacement (or freeze) row for every failed registry
// sample in m under the discovery lock and returns the replaced IDs.
func (d *Discovery) Replace(ctx context.Context, m Mapping, snap Snapshot) ([]string, error) {
	var applied []string
	err := d.Registry.Discover(ctx, func(tx *RegistryTx) error {
		for _, f := range m.Failed() {
			if f.Candidate {
				continue
			}
			sp, ok := snap.find(f.ID)
			if !ok {
				d.logRegistry(ctx, tx, "op=refuse-replace id=%s reason=not-in-snapshot", f.ID)
				continue
			}
			clip := d.replacementClip(sp)
			before := 0
			if d.Log != nil {
				before = tx.Snapshot().Version
			}
			ok, reason := tx.ReplaceSample(f.ID, sp.SampleVersion, clip)
			if ok {
				applied = append(applied, f.ID)
			}
			d.logReplace(ctx, tx, f, sp, clip, before, ok, reason)
		}
		return nil
	})
	return applied, err
}

func (d *Discovery) logReplace(ctx context.Context, tx *RegistryTx, failed SampleResult, sp Speaker, clip SampleClip, before int, applied bool, reason string) {
	if d.Log == nil {
		return
	}
	after, _ := tx.Snapshot().find(sp.ID)
	old := span(sp.Sample.Start, sp.Sample.End)
	switch {
	case applied:
		d.logRegistry(ctx, tx, "op=replace id=%s cause=%s old=%s new=%s replacements=%d", sp.ID, failed.Status, old, span(clip.Start, clip.End), after.Replacements)
	case tx.Snapshot().Version != before:
		d.logRegistry(ctx, tx, "op=freeze id=%s cause=%s sample=%s replacements=%d verified_ranges=%d clip_found=%t",
			sp.ID, failed.Status, old, after.Replacements, len(after.Ranges), clip.End > clip.Start)
	default:
		d.logRegistry(ctx, tx, "op=refuse-replace id=%s cause=%s reason=%s", sp.ID, failed.Status, reason)
	}
}

// replacementClip cuts the best clip from the speaker's verified ranges,
// avoiding the failed sample's own interval.
func (d *Discovery) replacementClip(sp Speaker) SampleClip {
	segs := make([]Segment, 0, len(sp.Ranges))
	for _, r := range sp.Ranges {
		segs = append(segs, Segment{Start: r.Start, End: r.End, Speaker: sp.ID})
	}
	for _, c := range d.Extractor.Candidates(segs, sp.ID) {
		if overlap(c.Clip.Start, c.Clip.End, sp.Sample.Start, sp.Sample.End) > 0 {
			continue
		}
		return SampleClip{Start: c.Clip.Start, End: c.Clip.End}
	}
	return SampleClip{}
}

// RecordAccepted is the bookkeeping for an accepted mapping: each mapped
// sample counts one verification with its core ranges.
func (d *Discovery) RecordAccepted(m Mapping) {
	for _, s := range m.Samples {
		if s.Status == SampleMapped && !s.Candidate {
			d.Registry.RecordVerification(s.ID, m.RangesFor(s.ID))
		}
	}
}
