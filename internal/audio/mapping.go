package audio

import (
	"sort"
	"time"
)

const dominantShare = 0.80

type SampleStatus string

const (
	SampleMapped    SampleStatus = "mapped"
	SampleMixed     SampleStatus = "mixed"
	SampleMissing   SampleStatus = "missing"
	SampleAmbiguous SampleStatus = "ambiguous"
	// SampleCollapsed is a candidate sharing a label with another sample:
	// the same voice. Onto names the known speaker when there is one.
	SampleCollapsed SampleStatus = "collapsed"
)

// SampleResult is what one prefix sample received in a request.
type SampleResult struct {
	ID        string
	Label     string
	Share     float64
	Labeled   time.Duration
	Status    SampleStatus
	Candidate bool
	Onto      string
}

// UnmappedLabel is core speech no sample explains.
type UnmappedLabel struct {
	Label     string
	Speech    time.Duration
	Material  bool
	Reason    string
	Intervals []SourceInterval
}

// Mapping is the mapper's report for one request: local labels resolved to
// global IDs, the core relabeled in source time, and everything unresolved.
type Mapping struct {
	Manifest  *RequestManifest
	Samples   []SampleResult
	LabelToID map[string]string
	// Labels is every local label of the core, mapped or not
	Labels      map[string]LabelStat
	Core        []Segment
	Unmapped    []UnmappedLabel
	ChunkSpeech time.Duration
	unmapped    map[string]bool
}

func (m Mapping) IsUnmapped(s Segment) bool { return m.unmapped[s.Speaker] }

// MaterialUnmapped returns unmapped labels that could create a speaker.
func (m Mapping) MaterialUnmapped() []UnmappedLabel {
	var out []UnmappedLabel
	for _, u := range m.Unmapped {
		if u.Material {
			out = append(out, u)
		}
	}
	return out
}

// UnknownSpeech is the core speech no sample explains.
func (m Mapping) UnknownSpeech() time.Duration {
	total := time.Duration(0)
	for _, u := range m.Unmapped {
		total += u.Speech
	}
	return total
}

func (m Mapping) MappedSpeech() time.Duration { return m.ChunkSpeech - m.UnknownSpeech() }

// LabelMapper turns a manifest plus returned segments into a Mapping. It
// only reports; it changes no registry.
type LabelMapper struct{}

func (LabelMapper) Map(m *RequestManifest, segs []Segment) Mapping {
	out := Mapping{Manifest: m, LabelToID: map[string]string{}, unmapped: map[string]bool{}}
	holders := map[string][]int{}
	for _, r := range m.Regions {
		if r.Kind != RegionSample {
			continue
		}
		shares := map[string]time.Duration{}
		labeled := time.Duration(0)
		for _, s := range segs {
			o := overlap(s.Start, s.End, r.ReqStart, r.ReqEnd)
			if o > 0 {
				shares[s.Speaker] += o
				labeled += o
			}
		}
		res := SampleResult{ID: r.Label, Labeled: labeled, Status: SampleMissing, Candidate: r.Candidate}
		if labeled > 0 {
			for label, d := range shares {
				if share := float64(d) / float64(labeled); share > res.Share || (share == res.Share && label < res.Label) {
					res.Label, res.Share = label, share
				}
			}
			res.Status = SampleMixed
			if res.Share >= dominantShare {
				res.Status = SampleMapped
				holders[res.Label] = append(holders[res.Label], len(out.Samples))
			}
		}
		out.Samples = append(out.Samples, res)
	}
	for label, idx := range holders {
		var known, cands []int
		for _, i := range idx {
			if out.Samples[i].Candidate {
				cands = append(cands, i)
			} else {
				known = append(known, i)
			}
		}
		switch {
		case len(known) > 1:
			// Two verified samples on one label: ambiguity, never a merge
			for _, i := range idx {
				out.Samples[i].Status = SampleAmbiguous
			}
		case len(known) == 1:
			out.LabelToID[label] = out.Samples[known[0]].ID
			for _, i := range cands {
				out.Samples[i].Status, out.Samples[i].Onto = SampleCollapsed, out.Samples[known[0]].ID
			}
		case len(cands) == 1:
			out.LabelToID[label] = out.Samples[cands[0]].ID
		default:
			// Several candidates, one voice: none of them names the label
			for _, i := range cands {
				out.Samples[i].Status = SampleCollapsed
			}
		}
	}
	core := m.Regions[len(m.Regions)-1]
	var coreSegs []Segment
	for _, s := range segs {
		start, end := max(s.Start, core.ReqStart), min(s.End, core.ReqEnd)
		if end <= start {
			continue
		}
		s.Start, s.End = start-core.ReqStart+core.SrcStart, end-core.ReqStart+core.SrcStart
		coreSegs = append(coreSegs, s)
	}
	sort.SliceStable(coreSegs, func(i, j int) bool { return coreSegs[i].Start < coreSegs[j].Start })
	stats := Materiality(coreSegs)
	out.Labels = stats
	byLabel := map[string]*UnmappedLabel{}
	for _, s := range coreSegs {
		out.ChunkSpeech += s.End - s.Start
		if id, ok := out.LabelToID[s.Speaker]; ok {
			s.Speaker = id
			out.Core = append(out.Core, s)
			continue
		}
		u, ok := byLabel[s.Speaker]
		if !ok {
			st := stats[s.Speaker]
			u = &UnmappedLabel{Label: s.Speaker, Speech: st.Speech, Material: st.Material, Reason: reasonNoSample}
			if !st.Material {
				u.Reason = reasonNonMaterial
			}
			byLabel[s.Speaker] = u
			out.unmapped[s.Speaker] = true
		}
		if n := len(u.Intervals); n > 0 && u.Intervals[n-1].End == s.Start {
			u.Intervals[n-1].End = s.End
		} else {
			u.Intervals = append(u.Intervals, SourceInterval{Start: s.Start, End: s.End})
		}
		out.Core = append(out.Core, s)
	}
	labels := make([]string, 0, len(byLabel))
	for l := range byLabel {
		labels = append(labels, l)
	}
	sort.Strings(labels)
	for _, l := range labels {
		out.Unmapped = append(out.Unmapped, *byLabel[l])
	}
	return out
}

// resolve relabels a local label (and the provisional candidate ID the
// mapper may already have applied) to a global ID.
func (m *Mapping) resolve(label, provisional, id string) {
	m.LabelToID[label] = id
	delete(m.unmapped, label)
	for i := range m.Core {
		if m.Core[i].Speaker == label || (provisional != "" && m.Core[i].Speaker == provisional) {
			m.Core[i].Speaker = id
		}
	}
	kept := m.Unmapped[:0]
	for _, u := range m.Unmapped {
		if u.Label != label {
			kept = append(kept, u)
		}
	}
	m.Unmapped = kept
}

func overlap(a0, a1, b0, b1 time.Duration) time.Duration {
	return max(0, min(a1, b1)-max(a0, b0))
}
