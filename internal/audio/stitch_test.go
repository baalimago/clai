package audio

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestStitchNumbersUnknownPerVoice(t *testing.T) {
	m1 := Mapping{unmapped: map[string]bool{"X": true, "Y": true}, Unmapped: []UnmappedLabel{
		{Label: "X", Reason: reasonNoSample, Material: true}, {Label: "Y", Reason: reasonNonMaterial},
	}}
	m1.Core = []Segment{seg(0, 5, "speaker-1"), seg(5, 8, "X"), seg(8, 9, "X"), seg(9, 10, "Y"), seg(10, 12, "speaker-1"), seg(12, 14, "X")}
	m2 := Mapping{unmapped: map[string]bool{"X": true}, Unmapped: []UnmappedLabel{{Label: "X", Reason: reasonNonMaterial}}}
	m2.Core = []Segment{seg(20, 22, "X"), seg(22, 30, "speaker-2")}
	segs, voices := stitch([]Mapping{m1, m2})
	wantNames := []string{"speaker-1", "unknown-1", "unknown-1", "unknown-2", "speaker-1", "unknown-1", "unknown-3", "speaker-2"}
	for i, s := range segs {
		if s.Speaker != wantNames[i] {
			t.Errorf("segment %v labeled %v, expected %v", i, s.Speaker, wantNames[i])
		}
	}
	want := []UnknownVoice{
		{Name: "unknown-1", Chunk: 0, Label: "X", Start: 5 * time.Second, End: 14 * time.Second, Speech: 6 * time.Second, Runs: 2, Material: true, Reason: reasonNoSample},
		{Name: "unknown-2", Chunk: 0, Label: "Y", Start: 9 * time.Second, End: 10 * time.Second, Speech: time.Second, Runs: 1, Reason: reasonNonMaterial},
		{Name: "unknown-3", Chunk: 1, Label: "X", Start: 20 * time.Second, End: 22 * time.Second, Speech: 2 * time.Second, Runs: 1, Reason: reasonNonMaterial},
	}
	if !slices.Equal(voices, want) {
		t.Fatalf("unknown voices:\n%+v\nwant:\n%+v", voices, want)
	}
	total, detail := summarizeUnknown(voices)
	if total != 9*time.Second || len(detail) != 3 {
		t.Fatalf("unexpected summary %v %q", total, detail)
	}
	if !strings.Contains(detail[0], "9.0s · voices 3 · runs 4 · 1 no-sample, 2 non-material") {
		t.Errorf("unexpected head line %q", detail[0])
	}
	if !strings.Contains(detail[1], "00:05–00:14") || !strings.Contains(detail[1], "6.0s") || !strings.Contains(detail[1], "unknown-1") || !strings.Contains(detail[1], "no-sample · runs 2") {
		t.Errorf("expected the 6 s voice listed, got %q", detail[1])
	}
	if !strings.Contains(detail[2], "2 voices under 3s") || !strings.Contains(detail[2], "3.0s total") {
		t.Errorf("expected the short-voice count line, got %q", detail[2])
	}
	if total, detail := summarizeUnknown(nil); total != 0 || detail != nil {
		t.Errorf("no unknown speech must give no summary, got %v %q", total, detail)
	}
}
