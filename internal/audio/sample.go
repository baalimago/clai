package audio

import (
	"sort"
	"time"
)

// Sample and materiality parameters (README parameters table).
const (
	sampleMin = 3 * time.Second
	sampleMax = 10 * time.Second
	// runBreak splits same-label speech into separate runs across a pause
	runBreak            = time.Second
	materialMinSpeech   = 3 * time.Second
	materialMinFraction = 0.01

	reasonNonMaterial      = "non-material"
	reasonNoSample         = "no-sample"
	reasonNoClip           = "no-clip"
	reasonReserveExhausted = "prefix-reserve-exhausted"
	reasonFrozen           = "frozen"
	reasonStale            = "stale-sample-version"
)

// LabelStat is one local label's speech in a chunk and whether it may
// create a speaker.
type LabelStat struct {
	Speech   time.Duration
	Material bool
	Reason   string
}

// Materiality classifies every label of a chunk by the material label
// threshold.
func Materiality(segs []Segment) map[string]LabelStat {
	stats := map[string]LabelStat{}
	total := time.Duration(0)
	for _, s := range segs {
		d := s.End - s.Start
		if d <= 0 {
			continue
		}
		st := stats[s.Speaker]
		st.Speech += d
		stats[s.Speaker] = st
		total += d
	}
	for label, st := range stats {
		st.Material = st.Speech >= materialMinSpeech && float64(st.Speech) >= float64(total)*materialMinFraction
		if !st.Material {
			st.Reason = reasonNonMaterial
		}
		stats[label] = st
	}
	return stats
}

// Candidate is a source clip of one local label, ranked for use as a sample.
type Candidate struct {
	Label     string
	Clip      SourceInterval
	Speech    time.Duration
	Clearance time.Duration
}

// SampleExtractor ranks contiguous same-label speech by duration, then by
// clearance from neighbouring labels.
type SampleExtractor struct{}

// Candidates returns every clip of label that fits the sample length, best
// first. segs are in source time and may be unsorted.
func (SampleExtractor) Candidates(segs []Segment, label string) []Candidate {
	sorted := make([]Segment, 0, len(segs))
	for _, s := range segs {
		if s.End > s.Start {
			sorted = append(sorted, s)
		}
	}
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Start < sorted[j].Start })
	type run struct {
		segs       []Segment
		start, end time.Duration
	}
	var runs []run
	for _, s := range sorted {
		if n := len(runs); n > 0 && runs[n-1].segs[0].Speaker == s.Speaker && s.Start-runs[n-1].end <= runBreak {
			runs[n-1].segs = append(runs[n-1].segs, s)
			runs[n-1].end = max(runs[n-1].end, s.End)
			continue
		}
		runs = append(runs, run{segs: []Segment{s}, start: s.Start, end: s.End})
	}
	var cands []Candidate
	for i, r := range runs {
		if r.segs[0].Speaker != label {
			continue
		}
		end := r.start
		for _, s := range r.segs {
			if s.End-r.start > sampleMax {
				break
			}
			end = s.End
		}
		if end == r.start {
			// A single utterance longer than the sample cap: cut it
			end = r.start + sampleMax
		}
		if end-r.start < sampleMin {
			continue
		}
		clearance := time.Duration(1<<62 - 1)
		if i > 0 {
			clearance = min(clearance, r.start-runs[i-1].end)
		}
		if i+1 < len(runs) {
			clearance = min(clearance, runs[i+1].start-r.end)
		}
		if clearance < 0 {
			clearance = 0
		}
		cands = append(cands, Candidate{Label: label, Clip: SourceInterval{Start: r.start, End: end}, Speech: end - r.start, Clearance: clearance})
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].Speech != cands[j].Speech {
			return cands[i].Speech > cands[j].Speech
		}
		return cands[i].Clearance > cands[j].Clearance
	})
	return cands
}

// better reports whether a beats b as a sample: more speech, then clearance.
func better(a, b Candidate) bool {
	if a.Speech != b.Speech {
		return a.Speech > b.Speech
	}
	return a.Clearance > b.Clearance
}
