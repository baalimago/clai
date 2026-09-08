package audio

import (
	"testing"
	"time"
)

// manifestFor builds a manifest by arithmetic alone (no runner): samples get
// guards and gaps as the assembler would.
func manifestFor(samples []SampleClip, core SourceInterval) *RequestManifest {
	m := &RequestManifest{Codec: requestCodec}
	pos := int64(0)
	add := func(r Region, n int64) {
		r.ReqStart, r.ReqEnd = durationOf(pos), durationOf(pos+n)
		m.Regions = append(m.Regions, r)
		pos += n
	}
	for _, s := range samples {
		start, end := snap(s.Start-sampleGuard), snap(s.End+sampleGuard)
		add(Region{Kind: RegionSample, Label: s.ID, SrcStart: start, SrcEnd: end, Candidate: s.Candidate}, samplesOf(end-start))
		add(Region{Kind: RegionGap}, samplesOf(sampleGap))
	}
	m.PrefixDuration = durationOf(pos)
	add(Region{Kind: RegionCore, SrcStart: core.Start, SrcEnd: core.End}, samplesOf(core.End-core.Start))
	m.CoreStart, m.CoreEnd, m.Duration = core.Start, core.End, durationOf(pos)
	return m
}

// reqSeg emits a segment in request time offset into a region.
func reqSeg(r Region, from, to float64, label string) Segment {
	return Segment{
		Start:   r.ReqStart + time.Duration(from*float64(time.Second)),
		End:     r.ReqStart + time.Duration(to*float64(time.Second)),
		Speaker: label, Text: "t",
	}
}

func twoSpeakerManifest() *RequestManifest {
	return manifestFor([]SampleClip{clip("speaker-1", 100), clip("speaker-2", 200)}, SourceInterval{Start: 600 * time.Second, End: 660 * time.Second})
}

func TestMappingRejectsMixedSample(t *testing.T) {
	m := twoSpeakerManifest()
	s1, s2, core := m.Regions[0], m.Regions[2], m.Regions[4]
	segs := []Segment{
		reqSeg(s1, 0.25, 3.0, "A"), reqSeg(s1, 3.0, 5.25, "B"), // 55/45: mixed
		reqSeg(s2, 0.25, 5.25, "C"),
		reqSeg(core, 0, 20, "A"), reqSeg(core, 20, 40, "C"), reqSeg(core, 40, 60, "B"),
	}
	mp := LabelMapper{}.Map(m, segs)
	if mp.Samples[0].Status != SampleMixed || mp.Samples[0].Share > 0.6 {
		t.Errorf("expected first sample mixed, got %+v", mp.Samples[0])
	}
	if mp.Samples[1].Status != SampleMapped || mp.Samples[1].Label != "C" {
		t.Errorf("expected second sample mapped to C, got %+v", mp.Samples[1])
	}
	if mp.LabelToID["C"] != "speaker-2" || len(mp.LabelToID) != 1 {
		t.Errorf("expected only C mapped, got %+v", mp.LabelToID)
	}
	if len(mp.Failed()) != 1 || mp.Failed()[0].ID != "speaker-1" {
		t.Errorf("expected speaker-1 reported failed, got %+v", mp.Failed())
	}
}

func TestMappingRejectsSampleCollapse(t *testing.T) {
	m := twoSpeakerManifest()
	s1, s2, core := m.Regions[0], m.Regions[2], m.Regions[4]
	segs := []Segment{
		reqSeg(s1, 0.25, 5.25, "A"), reqSeg(s2, 0.25, 5.25, "A"),
		reqSeg(core, 0, 30, "A"), reqSeg(core, 30, 60, "B"),
	}
	mp := LabelMapper{}.Map(m, segs)
	for _, s := range mp.Samples {
		if s.Status != SampleAmbiguous {
			t.Errorf("expected ambiguous, got %+v", s)
		}
	}
	if len(mp.LabelToID) != 0 {
		t.Errorf("collapsed label must not map: %+v", mp.LabelToID)
	}
	if len(mp.Ambiguous()) != 2 {
		t.Errorf("expected both samples reported ambiguous, got %+v", mp.Ambiguous())
	}
	for _, c := range mp.Core {
		if c.Speaker == "speaker-1" || c.Speaker == "speaker-2" {
			t.Errorf("no identity may be assigned from a collapse: %+v", c)
		}
	}
}

func TestMappingRejectsMissingSample(t *testing.T) {
	m := twoSpeakerManifest()
	s2, core := m.Regions[2], m.Regions[4]
	segs := []Segment{reqSeg(s2, 0.25, 5.25, "B"), reqSeg(core, 0, 60, "B")}
	mp := LabelMapper{}.Map(m, segs)
	if mp.Samples[0].Status != SampleMissing {
		t.Errorf("expected first sample missing, got %+v", mp.Samples[0])
	}
	if len(mp.Failed()) != 1 || mp.Failed()[0].ID != "speaker-1" {
		t.Errorf("expected speaker-1 failed, got %+v", mp.Failed())
	}
	if mp.LabelToID["B"] != "speaker-2" {
		t.Errorf("expected B mapped to speaker-2, got %+v", mp.LabelToID)
	}
}

func TestMappingReportsUnmappedLabels(t *testing.T) {
	m := twoSpeakerManifest()
	s1, s2, core := m.Regions[0], m.Regions[2], m.Regions[4]
	segs := []Segment{
		reqSeg(s1, 0.25, 5.25, "A"), reqSeg(s2, 0.25, 5.25, "B"),
		reqSeg(core, 0, 20, "A"), reqSeg(core, 20, 40, "B"),
		reqSeg(core, 40, 55, "C"), // 15 s material, unmapped
		reqSeg(core, 55, 56, "D"), // 1 s non-material, unmapped
		{Start: s1.ReqStart, End: s1.ReqStart + time.Second, Speaker: "Z", Text: "prefix noise"}, // inside prefix: never rendered
	}
	mp := LabelMapper{}.Map(m, segs)
	if len(mp.Unmapped) != 2 {
		t.Fatalf("expected two unmapped labels, got %+v", mp.Unmapped)
	}
	byLabel := map[string]UnmappedLabel{}
	for _, u := range mp.Unmapped {
		byLabel[u.Label] = u
	}
	c := byLabel["C"]
	if !c.Material || c.Speech != 15*time.Second || c.Reason != reasonNoSample {
		t.Errorf("unexpected C report: %+v", c)
	}
	if len(c.Intervals) != 1 || c.Intervals[0].Start != 640*time.Second || c.Intervals[0].End != 655*time.Second {
		t.Errorf("expected C interval in source time 640–655, got %+v", c.Intervals)
	}
	if d := byLabel["D"]; d.Material || d.Reason != reasonNonMaterial {
		t.Errorf("unexpected D report: %+v", d)
	}
	if mp.ChunkSpeech != 56*time.Second {
		t.Errorf("expected 56 s chunk speech, got %v", mp.ChunkSpeech)
	}
	// Core is relabeled to global IDs in source time; prefix segments dropped
	if len(mp.Core) != 4 {
		t.Fatalf("expected 4 core segments, got %+v", mp.Core)
	}
	if mp.Core[0].Speaker != "speaker-1" || mp.Core[0].Start != 600*time.Second || mp.Core[1].Speaker != "speaker-2" {
		t.Errorf("unexpected relabeled core: %+v", mp.Core[:2])
	}
	if mp.Core[2].Speaker != "C" || !mp.IsUnmapped(mp.Core[2]) {
		t.Errorf("unmapped core segment should keep its local label and be flagged: %+v", mp.Core[2])
	}
	// A segment straddling the prefix/core boundary is clipped to the core
	straddle := append(segs, Segment{Start: core.ReqStart - time.Second, End: core.ReqStart + time.Second, Speaker: "A"})
	mp2 := LabelMapper{}.Map(m, straddle)
	found := false
	for _, c := range mp2.Core {
		if c.Start == 600*time.Second && c.End == 601*time.Second && c.Speaker == "speaker-1" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected straddling segment clipped to the core (600–601 s, speaker-1), got %+v", mp2.Core)
	}
}
