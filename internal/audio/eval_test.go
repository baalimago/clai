package audio

import (
	"strings"
	"testing"
	"time"
)

const sampleAnnotation = `start	end	speaker
# a comment line
0	10	alice
10	20	bob
20	22	crosstalk
22	30	alice
30	40	carol
`

func TestAttributionEvaluatorReportsConfusionAndUnknown(t *testing.T) {
	ann, err := ParseAnnotation(strings.NewReader(sampleAnnotation))
	if err != nil {
		t.Fatal(err)
	}
	if len(ann) != 5 {
		t.Fatalf("expected 5 intervals, got %v", len(ann))
	}
	segs := []Segment{
		seg(0, 10, "speaker-1"), seg(10, 19, "speaker-2"), seg(19, 22, "speaker-1"),
		seg(22, 30, "speaker-1"), seg(30, 36, "unknown-1"), seg(36, 40, "speaker-3"),
	}
	r := Evaluate(segs, ann)
	if r.Annotated != 38*time.Second || r.Crosstalk != 2*time.Second {
		t.Errorf("unexpected totals annotated %v crosstalk %v", r.Annotated, r.Crosstalk)
	}
	if r.Mapping["speaker-1"] != "alice" || r.Mapping["speaker-2"] != "bob" || r.Mapping["speaker-3"] != "carol" {
		t.Errorf("unexpected mapping %+v", r.Mapping)
	}
	// correct: alice 18 + bob 9 + carol 4 = 31 of 38; 1 s of bob went to speaker-1
	if r.Correct != 31*time.Second || r.Unknown != 6*time.Second {
		t.Errorf("unexpected correct %v unknown %v", r.Correct, r.Unknown)
	}
	if r.Confusion["speaker-1"]["bob"] != time.Second {
		t.Errorf("expected 1 s bob→speaker-1 in the confusion matrix, got %+v", r.Confusion["speaker-1"])
	}
	if r.Merges != 0 || r.Extra != 0 {
		t.Errorf("unexpected merges %v extra %v", r.Merges, r.Extra)
	}
	out := r.String()
	for _, want := range []string{"attribution 81.6%", "unknown 15.8%", "alice", "speaker-3"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in report:\n%v", want, out)
		}
	}
	// A merge: one ID covering two speakers, plus an extra material ID
	merged := []Segment{seg(0, 10, "speaker-1"), seg(10, 20, "speaker-1"), seg(22, 30, "speaker-1"), seg(30, 40, "speaker-9"), seg(0, 5, "speaker-7")}
	r2 := Evaluate(merged, ann)
	if r2.Merges != 1 || r2.Extra != 1 {
		t.Errorf("expected one merge (bob) and one extra ID (speaker-7), got merges %v extra %v mapping %+v", r2.Merges, r2.Extra, r2.Mapping)
	}
}

func TestEvaluatorRejectsMalformedAnnotation(t *testing.T) {
	cases := map[string]string{
		"wrong field count": "start\tend\tspeaker\n0\t10\n",
		"end before start":  "start\tend\tspeaker\n10\t5\talice\n",
		"non-numeric":       "start\tend\tspeaker\nx\t5\talice\n",
		"empty":             "start\tend\tspeaker\n",
	}
	for name, body := range cases {
		_, err := ParseAnnotation(strings.NewReader(body))
		if err == nil {
			t.Errorf("%v: expected error", name)
			continue
		}
		if name != "empty" && !strings.Contains(err.Error(), "line 2") {
			t.Errorf("%v: expected the line named, got %v", name, err)
		}
	}
}
