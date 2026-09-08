package audio

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

const (
	discoveryIterations = 2

	reasonCollapsedOntoKnown = "collapsed-onto-known"
	reasonSameVoice          = "same-voice-as-promoted"
	reasonMixed              = "mixed"
	reasonMissing            = "missing"
	reasonAmbiguousKnown     = "ambiguous-known-samples"
)

// RequestFunc transcribes one assembled request. Phase 3 injects the
// accounting, cache, and provider behind it.
type RequestFunc func(ctx context.Context, samples []SampleClip, core SourceInterval) (*RequestManifest, []Segment, error)

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
}

// Pass transcribes core with the snapshot's samples and maps the result.
func (d *Discovery) Pass(ctx context.Context, core SourceInterval, snap Snapshot) (Mapping, error) {
	return d.request(ctx, snap.Samples(), core)
}

// PassWith transcribes core with an explicit prefix order (the recovery
// retry order for an ambiguity).
func (d *Discovery) PassWith(ctx context.Context, core SourceInterval, samples []SampleClip) (Mapping, error) {
	return d.request(ctx, samples, core)
}

func (d *Discovery) request(ctx context.Context, samples []SampleClip, core SourceInterval) (Mapping, error) {
	m, segs, err := d.Transcribe(ctx, samples, core)
	if err != nil {
		return Mapping{}, err
	}
	return d.Mapper.Map(m, segs), nil
}

// Bootstrap transcribes the first core uncalibrated, then once more with a
// candidate for every material label; a second candidate iteration runs
// only when a candidate was mixed.
func (d *Discovery) Bootstrap(ctx context.Context, core SourceInterval) (Mapping, DiscoveryReport, error) {
	first, err := d.request(ctx, nil, core)
	if err != nil {
		return Mapping{}, DiscoveryReport{}, fmt.Errorf("bootstrap pass failed: %w", err)
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
	err := d.Registry.Discover(ctx, func(tx *RegistryTx) error {
		for rep.Attempts < discoveryIterations {
			pending := current.MaterialUnmapped()
			if len(pending) == 0 {
				return nil
			}
			if rep.Attempts > 0 && bootstrap && !hadMixed(current) {
				return nil
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			fresh := tx.Snapshot()
			cands := d.candidates(current, pending, tried)
			if len(cands) == 0 {
				for _, u := range pending {
					rep.Rejected = append(rep.Rejected, CandidateRejection{Label: u.Label, Reason: reasonNoClip})
				}
				return nil
			}
			samples := fresh.Samples()
			for _, c := range cands {
				tried = append(tried, c.Clip)
				samples = append(samples, SampleClip{ID: c.id, Start: c.Clip.Start, End: c.Clip.End, Candidate: true})
			}
			rep.Attempts++
			next, err := d.request(ctx, samples, core)
			if err != nil {
				return fmt.Errorf("discovery request %v failed: %w", rep.Attempts, err)
			}
			d.promote(tx, &next, cands, &rep)
			current = next
		}
		return nil
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
func (d *Discovery) candidates(m Mapping, pending []UnmappedLabel, tried []SourceInterval) []candidate {
	var out []candidate
	for i, u := range pending {
		for _, c := range d.Extractor.Candidates(m.Core, u.Label) {
			if overlapsAny(c.Clip, tried) {
				continue
			}
			out = append(out, candidate{Candidate: c, id: "cand-" + strconv.Itoa(i) + "-" + u.Label})
			break
		}
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
func (d *Discovery) promote(tx *RegistryTx, m *Mapping, cands []candidate, rep *DiscoveryReport) {
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
		d.add(tx, m, label, byID[best.ID], rep)
	}
	for _, s := range m.Samples {
		if !s.Candidate {
			continue
		}
		c := byID[s.ID]
		switch s.Status {
		case SampleMapped:
			d.add(tx, m, s.Label, c, rep)
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

func (d *Discovery) add(tx *RegistryTx, m *Mapping, label string, c candidate, rep *DiscoveryReport) {
	var ranges []SourceInterval
	for _, s := range m.Core {
		if s.Speaker == label || s.Speaker == c.id {
			ranges = append(ranges, SourceInterval{Start: s.Start, End: s.End})
		}
	}
	id, reason, ok := tx.AddSpeaker(SampleClip{Start: c.Clip.Start, End: c.Clip.End}, ranges)
	if !ok {
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
	rep.Promoted = append(rep.Promoted, id)
	m.resolve(label, c.id, id)
}

// Recover applies the replacement row for every failed sample in m, then
// retries the chunk once against the refreshed snapshot. It returns the
// retry's mapping and the speakers whose sample this call replaced.
func (d *Discovery) Recover(ctx context.Context, core SourceInterval, m Mapping, snap Snapshot) (Mapping, []string, error) {
	applied, err := d.Replace(ctx, m, snap)
	if err != nil {
		return Mapping{}, nil, err
	}
	retry, err := d.Pass(ctx, core, d.Registry.Snapshot())
	if err != nil {
		return Mapping{}, applied, fmt.Errorf("replacement retry failed: %w", err)
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
				continue
			}
			clip := d.replacementClip(sp)
			if ok, _ := tx.ReplaceSample(f.ID, sp.SampleVersion, clip); ok {
				applied = append(applied, f.ID)
			}
		}
		return nil
	})
	return applied, err
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
