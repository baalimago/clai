package audio

import (
	"testing"
	"time"
)

func seg(start, end float64, speaker string) Segment {
	return Segment{Start: time.Duration(start * float64(time.Second)), End: time.Duration(end * float64(time.Second)), Speaker: speaker, Text: "t"}
}

func TestExtractorSkipsNonMaterialLabels(t *testing.T) {
	segs := []Segment{
		seg(0, 6, "A"), seg(6, 12, "A"), seg(12, 13, "B"), seg(13, 25, "A"), seg(25, 27.5, "C"), seg(27.5, 33, "A"), seg(33, 40, "A"),
		seg(40, 44, "D"), // 4 s: above the 3 s floor and above 1 % of chunk speech -> material
		seg(44, 46, "E"), // 2 s: below the 3 s floor
	}
	stats := Materiality(segs)
	if !stats["A"].Material || !stats["D"].Material {
		t.Errorf("expected A and D material, got %+v", stats)
	}
	if stats["B"].Material || stats["C"].Material || stats["E"].Material {
		t.Errorf("expected B, C, E non-material, got %+v", stats)
	}
	if stats["E"].Reason != reasonNonMaterial || stats["C"].Reason != reasonNonMaterial {
		t.Errorf("expected non-material reason, got %+v", stats)
	}
	var ex SampleExtractor
	if cands := ex.Candidates(segs, "E"); len(cands) != 0 {
		t.Errorf("expected no candidate for a 2 s label, got %+v", cands)
	}
	cands := ex.Candidates(segs, "A")
	if len(cands) != 3 {
		t.Fatalf("expected three runs for A, got %+v", cands)
	}
	// Runs of A: 0-12 (whole-segment prefix 0-6), 13-25 (single 12 s segment, cut to 10 s), 27.5-40 (prefix 27.5-33).
	for _, c := range cands {
		if c.Clip.End-c.Clip.Start > 10*time.Second || c.Clip.End-c.Clip.Start < 3*time.Second {
			t.Errorf("candidate outside sample length: %+v", c)
		}
	}
	if cands[0].Clip.Start != 13*time.Second || cands[0].Clip.End != 23*time.Second {
		t.Errorf("expected the cut 10 s clip ranked first, got %+v", cands[0])
	}
}

func TestExtractorRanksByDurationThenClearance(t *testing.T) {
	segs := []Segment{
		seg(0, 4, "A"), seg(4, 5, "A"), // run 0-5: 5 s, clearance 0 (B follows immediately)
		seg(5, 6, "B"),
		seg(8, 13, "A"), // run 8-13: 5 s, clearance 2 s (gap before, then C at 15)
		seg(15, 16, "C"),
		seg(20, 23.5, "A"), // 3.5 s
	}
	cands := SampleExtractor{}.Candidates(segs, "A")
	if len(cands) != 3 {
		t.Fatalf("expected 3 candidates, got %+v", cands)
	}
	if cands[0].Clip.Start != 8*time.Second || cands[1].Clip.Start != 0 || cands[2].Clip.Start != 20*time.Second {
		t.Errorf("unexpected ranking: %+v", cands)
	}
	if cands[0].Clearance != 2*time.Second {
		t.Errorf("expected 2 s clearance on the best clip, got %v", cands[0].Clearance)
	}
	long := []Segment{seg(0, 4, "A"), seg(4, 9, "A"), seg(9, 14, "A"), seg(14, 15, "B")}
	best := SampleExtractor{}.Candidates(long, "A")
	if len(best) != 1 || best[0].Clip.End != 9*time.Second {
		t.Errorf("expected the longest whole-segment prefix under 10 s (0-9), got %+v", best)
	}
}
