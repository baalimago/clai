package audio

import (
	"strings"
	"testing"
	"time"
)

func TestStitchNumbersUnknownRunsInSourceOrder(t *testing.T) {
	m1 := Mapping{unmapped: map[string]bool{"X": true}, Unmapped: []UnmappedLabel{{Label: "X", Reason: reasonNoSample, Material: true}}}
	m1.Core = []Segment{seg(0, 5, "speaker-1"), seg(5, 8, "X"), seg(8, 9, "X"), seg(9, 12, "speaker-1"), seg(12, 14, "X")}
	m2 := Mapping{unmapped: map[string]bool{"X": true}, Unmapped: []UnmappedLabel{{Label: "X", Reason: reasonNonMaterial}}}
	m2.Core = []Segment{seg(20, 22, "X"), seg(22, 30, "speaker-2")}
	segs, runs := stitch([]Mapping{m1, m2})
	if len(runs) != 3 {
		t.Fatalf("expected three unknown runs, got %+v", runs)
	}
	wantNames := []string{"speaker-1", "unknown-1", "unknown-1", "speaker-1", "unknown-2", "unknown-3", "speaker-2"}
	for i, s := range segs {
		if s.Speaker != wantNames[i] {
			t.Errorf("segment %v labeled %v, expected %v", i, s.Speaker, wantNames[i])
		}
	}
	if runs[0].Speech != 4*time.Second || runs[0].Start != 5*time.Second || runs[0].End != 9*time.Second {
		t.Errorf("unexpected first run %+v", runs[0])
	}
	if runs[2].Chunk != 1 || runs[2].Material || runs[2].Reason != reasonNonMaterial {
		t.Errorf("unexpected third run %+v", runs[2])
	}
	total, detail := summarizeUnknown(runs)
	if total != 8*time.Second || len(detail) != 3 {
		t.Fatalf("unexpected summary %v %q", total, detail)
	}
	if !strings.Contains(detail[0], "8.0s in 3 runs") || !strings.Contains(detail[0], "2 no-sample, 1 non-material") {
		t.Errorf("unexpected head line %q", detail[0])
	}
	if !strings.Contains(detail[1], "00:05–00:09") || !strings.Contains(detail[1], "4.0s") || !strings.Contains(detail[1], "unknown-1") {
		t.Errorf("expected the 4 s run listed, got %q", detail[1])
	}
	if !strings.Contains(detail[2], "2 runs under 3s") || !strings.Contains(detail[2], "4.0s total") {
		t.Errorf("expected the short-run count line, got %q", detail[2])
	}
	if total, detail := summarizeUnknown(nil); total != 0 || detail != nil {
		t.Errorf("no runs must give no summary, got %v %q", total, detail)
	}
}
