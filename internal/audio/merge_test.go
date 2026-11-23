package audio

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/pkg/claierr"
)

func at(s float64) time.Duration { return time.Duration(s * float64(time.Second)) }

// said is one survey candidate and the person who spoke it.
type said struct {
	who string
	SurveyCandidate
}

// heard is who speaking [start, end) s under label in chunk (0-based); the
// clip is the candidate, one second clear of its neighbours.
func heard(who string, chunk int, label string, start, end float64) said {
	return said{
		who:   who,
		Chunk: chunk,
		Label: label, Clip: SourceInterval{Start: at(start), End: at(end)}, Speech: at(end - start), Clearance: time.Second,
		LabelSpeech: at(10 * (end - start)),
	}
}

func (s said) clearance(c float64) said { s.Clearance = at(c); return s }

func (s said) speech(total float64) said { s.LabelSpeech = at(total); return s }

type mergeScript []said

func (s mergeScript) truth() []voiceSpan {
	spans := make([]voiceSpan, 0, len(s))
	for _, l := range s {
		spans = append(spans, voiceSpan{l.Clip.Start.Seconds(), l.Clip.End.Seconds(), l.who})
	}
	return spans
}

func (s mergeScript) candidates() []SurveyCandidate {
	out := make([]SurveyCandidate, 0, len(s))
	for _, l := range s {
		out = append(out, l.SurveyCandidate)
	}
	return out
}

// inside is a source instant inside the clip of candidate id.
func (s mergeScript) inside(t *testing.T, id string) time.Duration {
	t.Helper()
	for _, l := range s {
		if l.ID() == id {
			return l.Clip.Start + time.Second
		}
	}
	t.Fatalf("no candidate %v in the script", id)
	return 0
}

type mergerImpl struct {
	name  string
	build func(RequestFunc) Merger
}

func mergersUnderTest() []mergerImpl {
	return []mergerImpl{{"ChainMerger", func(f RequestFunc) Merger { return ChainMerger{Transcribe: f} }}}
}

func eachMerger(t *testing.T, test func(t *testing.T, build func(RequestFunc) Merger)) {
	for _, impl := range mergersUnderTest() {
		t.Run(impl.name, func(t *testing.T) { test(t, impl.build) })
	}
}

func mergeWith(m Merger, cands []SurveyCandidate) (Snapshot, MergeReport, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return m.Merge(ctx, cands)
}

func mustMerge(t *testing.T, m Merger, s mergeScript) (Snapshot, MergeReport) {
	t.Helper()
	snap, report, err := mergeWith(m, s.candidates())
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}
	checkRegistry(t, snap, report, s)
	return snap, report
}

// speakers lists each kept voice's candidate IDs, best first.
func speakers(report MergeReport) map[string][]string {
	out := map[string][]string{}
	for _, v := range report.Voices {
		if v.Speaker == "" {
			continue
		}
		for _, c := range v.Clips {
			out[v.Speaker] = append(out[v.Speaker], c.ID())
		}
	}
	return out
}

// outcomes renders each clip of a link as "<outcome> <voice>".
func outcomes(link MergeLink) map[string]string {
	out := map[string]string{}
	for _, c := range link.Clips {
		out[c.ID] = strings.TrimSpace(string(c.Outcome) + " " + c.Voice)
	}
	return out
}

func voiceNames(report MergeReport) []string {
	names := make([]string, 0, len(report.Voices))
	for _, v := range report.Voices {
		names = append(names, v.Voice)
	}
	return names
}

func earliest(v MergedVoice) time.Duration {
	first := v.Clips[0].Clip.Start
	for _, c := range v.Clips {
		first = min(first, c.Clip.Start)
	}
	return first
}

func rankedBefore(a, b SurveyCandidate) bool {
	if a.Speech != b.Speech {
		return a.Speech > b.Speech
	}
	if a.Clearance != b.Clearance {
		return a.Clearance > b.Clearance
	}
	return a.Clip.Start < b.Clip.Start
}

// checkRegistry holds every merge to Merge.5–Merge.7 and Found.1.
func checkRegistry(t *testing.T, snap Snapshot, report MergeReport, s mergeScript) {
	t.Helper()
	var kept []MergedVoice
	for i, v := range report.Voices {
		if v.Speaker == "" {
			continue
		}
		if i != len(kept) {
			t.Errorf("kept voice %v listed after a voice over the cap: %v", v.Voice, voiceNames(report))
		}
		kept = append(kept, v)
	}
	if len(kept) > maxVoices || len(snap.Speakers) != len(kept) {
		t.Fatalf("registry %+v does not hold the %v kept voices of %v", snap, len(kept), voiceNames(report))
	}
	for i, v := range kept {
		id := fmt.Sprintf("speaker-%d", i+1)
		want := Speaker{ID: id}
		for _, c := range v.Clips {
			want.Clips = append(want.Clips, SampleClip{ID: id, Start: c.Clip.Start, End: c.Clip.End})
		}
		want.Sample = want.Clips[0]
		if !reflect.DeepEqual(snap.Speakers[i], want) {
			t.Errorf("speaker %v = %+v, want %+v", i, snap.Speakers[i], want)
		}
		if v.Speaker != id {
			t.Errorf("voice %v kept as %q, want %q", v.Voice, v.Speaker, id)
		}
		if i > 0 && earliest(kept[i-1]) >= earliest(v) {
			t.Errorf("%v starts at %v, not after %v at %v", id, earliest(v), kept[i-1].Speaker, earliest(kept[i-1]))
		}
	}
	for _, v := range report.Voices {
		total := time.Duration(0)
		for j, c := range v.Clips {
			total += c.LabelSpeech
			if j > 0 && rankedBefore(c, v.Clips[j-1]) {
				t.Errorf("voice %v: clip %v ranked after %v", v.Voice, c.ID(), v.Clips[j-1].ID())
			}
		}
		if total != v.SurveySpeech {
			t.Errorf("voice %v: SurveySpeech %v, clips hold %v", v.Voice, v.SurveySpeech, total)
		}
	}
	over := report.Voices[len(kept):]
	for j := 1; j < len(over); j++ {
		if over[j].SurveySpeech > over[j-1].SurveySpeech {
			t.Errorf("voices over the cap not most speech first: %+v", over)
		}
	}
	if report.Found() != len(report.Voices) {
		t.Errorf("Found() = %v, report lists %v voices", report.Found(), len(report.Voices))
	}
	text := fmt.Sprintf("%+v", report)
	for _, l := range s {
		if strings.Contains(text, l.who) || strings.Contains(text, "mix") {
			t.Fatalf("the report carries segment text: %v", text)
		}
	}
}

func onePersonAcrossChunks() mergeScript {
	return mergeScript{
		heard("ann", 0, "A", 10, 18), heard("bob", 0, "B", 30, 37), heard("cyd", 0, "C", 50, 56),
		heard("ann", 1, "C", 110, 118), heard("bob", 1, "A", 130, 137).clearance(2), heard("cyd", 1, "B", 150, 156),
		heard("ann", 2, "B", 210, 219), heard("cyd", 2, "A", 250, 256),
		heard("ann", 3, "A", 310, 318), heard("bob", 3, "C", 330, 337), heard("cyd", 3, "B", 350, 356),
	}
}

func twoVoicesOfOnePerson() mergeScript {
	return mergeScript{
		heard("ann", 0, "A", 10, 18), heard("ann", 0, "B", 30, 36), heard("bob", 0, "C", 50, 57),
		heard("ann", 1, "A", 110, 119), heard("bob", 1, "B", 130, 137),
		heard("ann", 2, "B", 210, 217), heard("cyd", 2, "A", 250, 256), heard("cyd", 2, "C", 270, 275),
	}
}

// tenPeople speaks p01–p04 in chunk 1, p01 and p05–p07 in chunk 2, p02 and
// p08–p10 in chunk 3, with the survey speech given per voice.
// labelsAgainstClipOrder seeds bob (label B) before ann (label A), and its
// last link begins dee (B) before cyd (A).
func labelsAgainstClipOrder() mergeScript {
	return mergeScript{
		heard("ann", 0, "A", 50, 58), heard("bob", 0, "B", 10, 17),
		heard("ann", 1, "B", 110, 118), heard("bob", 1, "A", 150, 157),
		heard("cyd", 2, "A", 260, 266), heard("dee", 2, "B", 230, 236), heard("ann", 2, "C", 210, 218),
	}
}

func tenPeople(speech map[string]float64) mergeScript {
	var s mergeScript
	add := func(who string, chunk int, label string, start float64, share float64) {
		s = append(s, heard(who, chunk, label, start, start+6).speech(share))
	}
	for i, who := range []string{"p01", "p02", "p03", "p04"} {
		share := speech[who]
		if who == "p01" || who == "p02" {
			share /= 2
		}
		add(who, 0, string(rune('A'+i)), float64(10+10*i), share)
	}
	for i, who := range []string{"p01", "p05", "p06", "p07"} {
		share := speech[who]
		if who == "p01" {
			share /= 2
		}
		add(who, 1, string(rune('A'+i)), float64(110+10*i), share)
	}
	for i, who := range []string{"p02", "p08", "p09", "p10"} {
		share := speech[who]
		if who == "p02" {
			share /= 2
		}
		add(who, 2, string(rune('A'+i)), float64(210+10*i), share)
	}
	return s
}

func TestMergerContract(t *testing.T) {
	// Merge.1–Merge.4, Merge.6, I2, I3
	t.Run("one_person_across_chunks", func(t *testing.T) {
		eachMerger(t, func(t *testing.T, build func(RequestFunc) Merger) {
			s := onePersonAcrossChunks()
			d := newMergeDiarizer(s.truth())
			_, report := mustMerge(t, build(d.transcribe), s)
			want := map[string][]string{
				"speaker-1": {"c3-B", "c1-A", "c2-C", "c4-A"},
				"speaker-2": {"c2-A", "c1-B", "c4-C"},
				"speaker-3": {"c1-C", "c2-B", "c3-A", "c4-B"},
			}
			if got := speakers(report); !reflect.DeepEqual(got, want) {
				t.Errorf("speakers = %v, want %v", got, want)
			}
			calls := d.requests()
			if len(calls) != 6 || len(report.Links) != 3 {
				t.Fatalf("expected 3 links of 2 requests, got %v requests and %+v", len(calls), report.Links)
			}
			for i, link := range report.Links {
				if link.Chunk != i+1 {
					t.Errorf("link %v carried chunk %v, want %v", i, link.Chunk, i+1)
				}
			}
			for _, c := range calls {
				for _, sample := range c.samples {
					if sample.Candidate && strings.HasPrefix(sample.ID, "c1-") {
						t.Errorf("a request carried the seeding chunk's candidate %v", sample.ID)
					}
				}
			}
		})
	})

	// Merge.3, Merge.4, I2
	t.Run("one_order_is_not_enough", func(t *testing.T) {
		s := mergeScript{heard("ann", 0, "A", 10, 18), heard("ann", 1, "B", 110, 118), heard("bob", 1, "A", 130, 137)}
		for _, kind := range []RequestKind{RequestMergeForward, RequestMergeReversed} {
			t.Run(string(kind)+" only", func(t *testing.T) {
				eachMerger(t, func(t *testing.T, build func(RequestFunc) Merger) {
					d := newMergeDiarizer(s.truth())
					d.sameLabel = map[heldClip][2]string{{kind, s.inside(t, "c2-A")}: {"ann", "bob"}}
					_, report := mustMerge(t, build(d.transcribe), s)
					if want := map[string][]string{"speaker-1": {"c1-A", "c2-B"}, "speaker-2": {"c2-A"}}; !reflect.DeepEqual(speakers(report), want) {
						t.Errorf("speakers = %v, want %v", speakers(report), want)
					}
					link := report.Links[0]
					if want := map[string]string{"voice-1": "held voice-1", "c2-B": "joined voice-1", "c2-A": "new voice-2"}; !reflect.DeepEqual(outcomes(link), want) {
						t.Errorf("outcomes = %v, want %v", outcomes(link), want)
					}
					ann, bob := link.Clips[1], link.Clips[2]
					shared, apart := ann.Forward.Label == bob.Forward.Label, ann.Reversed.Label != bob.Reversed.Label
					if kind == RequestMergeReversed {
						shared, apart = ann.Reversed.Label == bob.Reversed.Label, ann.Forward.Label != bob.Forward.Label
					}
					if !shared || !apart {
						t.Errorf("expected one label in %v only, got %+v and %+v", kind, ann, bob)
					}
				})
			})
		}
	})

	// Merge.2, I8
	t.Run("link_shape", func(t *testing.T) {
		eachMerger(t, func(t *testing.T, build func(RequestFunc) Merger) {
			s := mergeScript{
				heard("ann", 0, "A", 10, 18), heard("bob", 0, "B", 30, 37),
				heard("bob", 1, "A", 110, 117), heard("ann", 1, "B", 130, 139), heard("cyd", 1, "C", 150, 156),
				heard("cyd", 3, "A", 310, 316), heard("ann", 3, "B", 330, 338),
			}
			d := newMergeDiarizer(s.truth())
			d.peers = 2
			_, report := mustMerge(t, build(d.transcribe), s)
			voice := func(id string, start, end float64) SampleClip {
				return SampleClip{ID: id, Start: at(start), End: at(end)}
			}
			cand := func(id string, start, end float64) SampleClip {
				return SampleClip{ID: id, Start: at(start), End: at(end), Candidate: true}
			}
			links := [][]SampleClip{
				{voice("voice-1", 10, 18), voice("voice-2", 30, 37), cand("c2-A", 110, 117), cand("c2-B", 130, 139), cand("c2-C", 150, 156)},
				{voice("voice-2", 30, 37), voice("voice-1", 130, 139), voice("voice-3", 150, 156), cand("c4-A", 310, 316), cand("c4-B", 330, 338)},
			}
			calls := d.requests()
			if len(calls) != 4 {
				t.Fatalf("expected 2 links of 2 requests and nothing for the empty chunk, got %v", len(calls))
			}
			for i, want := range links {
				pair := calls[2*i : 2*i+2]
				slices.SortFunc(pair, func(a, b mergeCall) int { return strings.Compare(string(a.kind), string(b.kind)) })
				reversed := slices.Clone(want)
				slices.Reverse(reversed)
				if pair[0].kind != RequestMergeForward || !slices.Equal(pair[0].samples, want) {
					t.Errorf("link %v forward = %v %+v, want %+v", i+1, pair[0].kind, pair[0].samples, want)
				}
				if pair[1].kind != RequestMergeReversed || !slices.Equal(pair[1].samples, reversed) {
					t.Errorf("link %v reversed = %v %+v, want %+v", i+1, pair[1].kind, pair[1].samples, reversed)
				}
				for _, c := range pair {
					if c.core != (SourceInterval{}) {
						t.Errorf("link %v sent a core %+v", i+1, c.core)
					}
				}
			}
			events := d.log()
			lastAnswer, firstStart := -1, len(events)
			for i, e := range events {
				if strings.HasPrefix(e, "answer") && strings.Contains(e, "c2-") {
					lastAnswer = max(lastAnswer, i)
				}
				if strings.HasPrefix(e, "start") && strings.Contains(e, "c4-") {
					firstStart = min(firstStart, i)
				}
			}
			if lastAnswer < 0 || firstStart < lastAnswer {
				t.Errorf("the second link started before the first one answered: %v", events)
			}
			if len(report.Links) != 2 || report.Links[0].Chunk != 1 || report.Links[1].Chunk != 3 {
				t.Errorf("links = %+v, want chunks 1 and 3", report.Links)
			}
		})
	})

	// Merge.4, Merge.5, Merge.7, Merge.8 (a merged name is not reused)
	t.Run("two_voices_become_one", func(t *testing.T) {
		eachMerger(t, func(t *testing.T, build func(RequestFunc) Merger) {
			s := twoVoicesOfOnePerson()
			d := newMergeDiarizer(s.truth())
			_, report := mustMerge(t, build(d.transcribe), s)
			want := map[string][]string{
				"speaker-1": {"c2-A", "c1-A", "c3-B", "c1-B"},
				"speaker-2": {"c1-C", "c2-B"},
				"speaker-3": {"c3-A", "c3-C"},
			}
			if got := speakers(report); !reflect.DeepEqual(got, want) {
				t.Errorf("speakers = %v, want %v", got, want)
			}
			if len(report.Links) != 2 {
				t.Fatalf("expected 2 links, got %+v", report.Links)
			}
			first := map[string]string{"voice-1": "held voice-1", "voice-2": "merged voice-1", "voice-3": "held voice-3", "c2-A": "joined voice-1", "c2-B": "joined voice-3"}
			if got := outcomes(report.Links[0]); !reflect.DeepEqual(got, first) {
				t.Errorf("link 1 outcomes = %v, want %v", got, first)
			}
			second := map[string]string{"voice-3": "held voice-3", "voice-1": "held voice-1", "c3-B": "joined voice-1", "c3-A": "new voice-4", "c3-C": "new voice-4"}
			if got := outcomes(report.Links[1]); !reflect.DeepEqual(got, second) {
				t.Errorf("link 2 outcomes = %v, want %v", got, second)
			}
			for _, c := range report.Links[1].Clips {
				if c.ID == "voice-1" && c.Clip != (SourceInterval{Start: at(110), End: at(119)}) {
					t.Errorf("link 2 carried %+v for her, not her best clip", c.Clip)
				}
			}
			if got := voiceNames(report); !slices.Equal(got, []string{"voice-1", "voice-3", "voice-4"}) {
				t.Errorf("voices = %v, want the merged voice's name unused", got)
			}
		})
	})

	// Merge.3, Merge.4, Merge.7
	t.Run("unsettled_clips", func(t *testing.T) {
		s := mergeScript{
			heard("ann", 0, "A", 10, 14.5), heard("bob", 0, "B", 30, 37),
			heard("ann", 1, "A", 110, 114.5), heard("bob", 1, "B", 130, 137),
		}
		cand, own := s.inside(t, "c2-A"), s.inside(t, "c1-A")
		cases := []struct {
			name     string
			fault    heldClip
			share    float64
			speakers map[string][]string
			outcome  map[string]string
			settled  bool
		}{
			{
				name: "candidate 50/50 in the forward request", fault: heldClip{RequestMergeForward, cand}, share: 0.5,
				speakers: map[string][]string{"speaker-1": {"c1-A"}, "speaker-2": {"c1-B", "c2-B"}},
				outcome:  map[string]string{"voice-1": "held voice-1", "voice-2": "held voice-2", "c2-A": "unsettled", "c2-B": "joined voice-2"},
			},
			{
				name: "candidate with no labelled speech", fault: heldClip{"", cand}, share: 0,
				speakers: map[string][]string{"speaker-1": {"c1-A"}, "speaker-2": {"c1-B", "c2-B"}},
				outcome:  map[string]string{"voice-1": "held voice-1", "voice-2": "held voice-2", "c2-A": "unsettled", "c2-B": "joined voice-2"},
			},
			{
				name: "candidate at 79 % in the reversed request", fault: heldClip{RequestMergeReversed, cand}, share: 0.79,
				speakers: map[string][]string{"speaker-1": {"c1-A"}, "speaker-2": {"c1-B", "c2-B"}},
				outcome:  map[string]string{"voice-1": "held voice-1", "voice-2": "held voice-2", "c2-A": "unsettled", "c2-B": "joined voice-2"},
			},
			{
				name: "voice's clip mixed in the reversed request", fault: heldClip{RequestMergeReversed, own}, share: 0.5,
				speakers: map[string][]string{"speaker-1": {"c1-A"}, "speaker-2": {"c1-B", "c2-B"}, "speaker-3": {"c2-A"}},
				outcome:  map[string]string{"voice-1": "unsettled voice-1", "voice-2": "held voice-2", "c2-A": "new voice-3", "c2-B": "joined voice-2"},
			},
			{
				name: "candidate at exactly 80 % is settled", fault: heldClip{RequestMergeForward, cand}, share: 0.80, settled: true,
				speakers: map[string][]string{"speaker-1": {"c1-A", "c2-A"}, "speaker-2": {"c1-B", "c2-B"}},
				outcome:  map[string]string{"voice-1": "held voice-1", "voice-2": "held voice-2", "c2-A": "joined voice-1", "c2-B": "joined voice-2"},
			},
		}
		t.Run("two clips covered only by unlabelled segments", func(t *testing.T) {
			eachMerger(t, func(t *testing.T, build func(RequestFunc) Merger) {
				d := newMergeDiarizer(s.truth())
				d.unlabelled = map[heldClip]bool{{"", own}: true, {"", s.inside(t, "c2-B")}: true}
				_, report := mustMerge(t, build(d.transcribe), s)
				if want := map[string][]string{"speaker-1": {"c1-A"}, "speaker-2": {"c1-B"}, "speaker-3": {"c2-A"}}; !reflect.DeepEqual(speakers(report), want) {
					t.Errorf("speakers = %v, want %v", speakers(report), want)
				}
				want := map[string]string{"voice-1": "unsettled voice-1", "voice-2": "held voice-2", "c2-A": "new voice-3", "c2-B": "unsettled"}
				if got := outcomes(report.Links[0]); !reflect.DeepEqual(got, want) {
					t.Errorf("outcomes = %v, want %v", got, want)
				}
				for _, c := range report.Links[0].Clips {
					if c.ID != "voice-1" && c.ID != "c2-B" {
						continue
					}
					if none := (ClipVerdict{}); c.Forward != none || c.Reversed != none {
						t.Errorf("%v: verdicts %+v and %+v, want no labelled speech", c.ID, c.Forward, c.Reversed)
					}
				}
			})
		})
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				eachMerger(t, func(t *testing.T, build func(RequestFunc) Merger) {
					d := newMergeDiarizer(s.truth())
					d.share = map[heldClip]float64{tc.fault: tc.share}
					_, report := mustMerge(t, build(d.transcribe), s)
					if got := speakers(report); !reflect.DeepEqual(got, tc.speakers) {
						t.Errorf("speakers = %v, want %v", got, tc.speakers)
					}
					if got := outcomes(report.Links[0]); !reflect.DeepEqual(got, tc.outcome) {
						t.Errorf("outcomes = %v, want %v", got, tc.outcome)
					}
					for _, c := range report.Links[0].Clips {
						if c.Clip.Start > tc.fault.at || tc.fault.at >= c.Clip.End {
							continue
						}
						verdict := c.Forward
						if tc.fault.kind == RequestMergeReversed {
							verdict = c.Reversed
						}
						if verdict.Share != tc.share || verdict.Settled != tc.settled || (tc.share == 0) != (verdict.Label == "") {
							t.Errorf("verdict = %+v, want share %v settled %v", verdict, tc.share, tc.settled)
						}
					}
				})
			})
		}
	})

	// Merge.6, Merge.7, Found.1, I10
	t.Run("more_than_eight", func(t *testing.T) {
		cases := []struct {
			name    string
			speech  map[string]float64
			dropped []string
		}{
			{
				name:    "distinct survey speech",
				speech:  map[string]float64{"p01": 60, "p02": 90, "p03": 5, "p04": 70, "p05": 65, "p06": 55, "p07": 50, "p08": 35, "p09": 6, "p10": 40},
				dropped: []string{"c3-C", "c1-C"},
			},
			{
				name:    "a tie at the cut keeps the earlier voice",
				speech:  map[string]float64{"p01": 60, "p02": 90, "p03": 5, "p04": 70, "p05": 65, "p06": 55, "p07": 50, "p08": 20, "p09": 20, "p10": 40},
				dropped: []string{"c3-C", "c1-C"},
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				eachMerger(t, func(t *testing.T, build func(RequestFunc) Merger) {
					s := tenPeople(tc.speech)
					d := newMergeDiarizer(s.truth())
					snap, report := mustMerge(t, build(d.transcribe), s)
					if report.Found() != 10 || len(snap.Speakers) != maxVoices {
						t.Fatalf("Found() = %v with %v speakers, want 10 and 8", report.Found(), len(snap.Speakers))
					}
					want := map[string][]string{
						"speaker-1": {"c1-A", "c2-A"}, "speaker-2": {"c1-B", "c3-A"}, "speaker-3": {"c1-D"},
						"speaker-4": {"c2-B"}, "speaker-5": {"c2-C"}, "speaker-6": {"c2-D"},
						"speaker-7": {"c3-B"}, "speaker-8": {"c3-D"},
					}
					if got := speakers(report); !reflect.DeepEqual(got, want) {
						t.Errorf("speakers = %v, want %v", got, want)
					}
					var dropped []string
					for _, v := range report.Voices[maxVoices:] {
						dropped = append(dropped, v.Clips[0].ID())
						if v.Speaker != "" {
							t.Errorf("voice over the cap %v kept as %q", v.Voice, v.Speaker)
						}
					}
					if !slices.Equal(dropped, tc.dropped) {
						t.Errorf("dropped voices = %v, want %v", dropped, tc.dropped)
					}
					names := []string{"voice-1", "voice-2", "voice-4", "voice-5", "voice-6", "voice-7", "voice-8", "voice-10", "voice-9", "voice-3"}
					if got := voiceNames(report); !slices.Equal(got, names) {
						t.Errorf("voices = %v, want %v", got, names)
					}
				})
			})
		}
	})

	// Merge.5, Merge.8, ID.1, I10
	t.Run("deterministic", func(t *testing.T) {
		scripts := []struct {
			name   string
			script mergeScript
		}{
			{"one person across chunks", onePersonAcrossChunks()},
			{"two voices of one person", twoVoicesOfOnePerson()},
			{"labels against clip order", labelsAgainstClipOrder()},
			{"ten people", tenPeople(map[string]float64{"p01": 60, "p02": 90, "p03": 5, "p04": 70, "p05": 65, "p06": 55, "p07": 50, "p08": 20, "p09": 20, "p10": 40})},
		}
		for _, sc := range scripts {
			t.Run(sc.name, func(t *testing.T) {
				eachMerger(t, func(t *testing.T, build func(RequestFunc) Merger) {
					d := newMergeDiarizer(sc.script.truth())
					base, baseReport := mustMerge(t, build(d.transcribe), sc.script)
					for seed := range uint64(4) {
						cands := sc.script.candidates()
						if seed == 0 {
							slices.Reverse(cands)
						} else {
							rand.New(rand.NewPCG(seed, seed)).Shuffle(len(cands), func(i, j int) { cands[i], cands[j] = cands[j], cands[i] })
						}
						for _, reversedFirst := range []bool{false, true} {
							d := newMergeDiarizer(sc.script.truth())
							d.reversedFirst = reversedFirst
							snap, report, err := mergeWith(build(d.transcribe), cands)
							if err != nil {
								t.Fatalf("seed %v, reversed first %v: %v", seed, reversedFirst, err)
							}
							if !reflect.DeepEqual(snap, base) || !reflect.DeepEqual(report, baseReport) {
								t.Errorf("seed %v, reversed first %v: the merge differs:\n%+v\n%+v\nwant\n%+v\n%+v", seed, reversedFirst, snap, report, base, baseReport)
							}
						}
					}
				})
			})
		}
		t.Run("seed names and samples", func(t *testing.T) {
			eachMerger(t, func(t *testing.T, build func(RequestFunc) Merger) {
				s := onePersonAcrossChunks()
				snap, report := mustMerge(t, build(newMergeDiarizer(s.truth()).transcribe), s)
				if got := voiceNames(report); !slices.Equal(got, []string{"voice-1", "voice-2", "voice-3"}) {
					t.Errorf("seed voices = %v, want voice-1–voice-3 in clip order", got)
				}
				samples := []SourceInterval{{Start: at(210), End: at(219)}, {Start: at(130), End: at(137)}, {Start: at(50), End: at(56)}}
				for i, want := range samples {
					if got := snap.Speakers[i].Sample; got.Start != want.Start || got.End != want.End {
						t.Errorf("speaker %v sample = %+v, want %+v (longest, then clearance, then earliest)", i+1, got, want)
					}
				}
			})
		})
		t.Run("names follow clip order", func(t *testing.T) {
			eachMerger(t, func(t *testing.T, build func(RequestFunc) Merger) {
				s := labelsAgainstClipOrder()
				_, report := mustMerge(t, build(newMergeDiarizer(s.truth()).transcribe), s)
				want := map[string][]string{"speaker-1": {"c1-B", "c2-A"}, "speaker-2": {"c1-A", "c2-B", "c3-C"}, "speaker-3": {"c3-B"}, "speaker-4": {"c3-A"}}
				if got := speakers(report); !reflect.DeepEqual(got, want) {
					t.Errorf("speakers = %v, want %v", got, want)
				}
				if got := voiceNames(report); !slices.Equal(got, []string{"voice-1", "voice-2", "voice-3", "voice-4"}) {
					t.Errorf("voices = %v, want the seed and the new voices named by clip order, not by ID", got)
				}
				last := map[string]string{"voice-1": "held voice-1", "voice-2": "held voice-2", "c3-C": "joined voice-2", "c3-B": "new voice-3", "c3-A": "new voice-4"}
				if got := outcomes(report.Links[1]); !reflect.DeepEqual(got, last) {
					t.Errorf("last link outcomes = %v, want %v", got, last)
				}
			})
		})
		ids := map[string]SurveyCandidate{
			"c1-A":   {Chunk: 0, Candidate: Candidate{Label: "A"}},
			"c2-A":   {Chunk: 1, Candidate: Candidate{Label: "A"}},
			"c12-B7": {Chunk: 11, Candidate: Candidate{Label: "B7"}},
		}
		for want, c := range ids {
			if got := c.ID(); got != want {
				t.Errorf("ID() = %q, want %q", got, want)
			}
		}
	})

	// Merge.1, Merge.9, I7
	t.Run("nothing_to_merge", func(t *testing.T) {
		cases := []struct {
			name     string
			script   mergeScript
			speakers map[string][]string
			requests int
			links    []int
		}{
			{name: "no candidates", speakers: map[string][]string{}},
			{
				name:     "candidates in one chunk only",
				script:   mergeScript{heard("ann", 0, "A", 10, 18), heard("bob", 0, "B", 30, 37)},
				speakers: map[string][]string{"speaker-1": {"c1-A"}, "speaker-2": {"c1-B"}},
			},
			{
				name: "the first chunk without candidates",
				script: mergeScript{
					heard("ann", 1, "A", 110, 118), heard("bob", 1, "B", 130, 137),
					heard("bob", 2, "A", 210, 217), heard("ann", 2, "B", 230, 238),
				},
				speakers: map[string][]string{"speaker-1": {"c2-A", "c3-B"}, "speaker-2": {"c2-B", "c3-A"}},
				requests: 2, links: []int{2},
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				eachMerger(t, func(t *testing.T, build func(RequestFunc) Merger) {
					d := newMergeDiarizer(tc.script.truth())
					snap, report := mustMerge(t, build(d.transcribe), tc.script)
					if tc.script == nil && (!reflect.DeepEqual(snap, Snapshot{}) || !reflect.DeepEqual(report, MergeReport{})) {
						t.Errorf("expected an empty registry and report, got %+v and %+v", snap, report)
					}
					if got := speakers(report); !reflect.DeepEqual(got, tc.speakers) {
						t.Errorf("speakers = %v, want %v", got, tc.speakers)
					}
					if got := len(d.requests()); got != tc.requests {
						t.Errorf("sent %v requests, want %v", got, tc.requests)
					}
					var links []int
					for _, l := range report.Links {
						links = append(links, l.Chunk)
					}
					if !slices.Equal(links, tc.links) {
						t.Errorf("links for chunks %v, want %v", links, tc.links)
					}
					for _, c := range d.requests() {
						for _, sample := range c.samples {
							if sample.Candidate && strings.HasPrefix(sample.ID, "c2-") {
								t.Errorf("a request carried the seeding chunk's candidate %v", sample.ID)
							}
						}
					}
				})
			})
		}
	})

	// Merge.12, I1, I4
	t.Run("independent_calls", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		eachMerger(t, func(t *testing.T, build func(RequestFunc) Merger) {
			first, second := onePersonAcrossChunks(), twoVoicesOfOnePerson()
			d := newMergeDiarizer(first.truth())
			m := build(d.transcribe)
			mustMerge(t, m, first)
			d.voices.truth = second.truth()
			snap, report := mustMerge(t, m, second)
			freshSnap, freshReport := mustMerge(t, build(newMergeDiarizer(second.truth()).transcribe), second)
			if !reflect.DeepEqual(snap, freshSnap) || !reflect.DeepEqual(report, freshReport) {
				t.Errorf("the second merge differs from a fresh merger's:\n%+v\n%+v\nwant\n%+v\n%+v", snap, report, freshSnap, freshReport)
			}
		})
	})
}

// Merge.10, CandidateError.1, CandidateError.2
func TestMergerRefusesBadCandidates(t *testing.T) {
	ok := heard("ann", 0, "A", 10, 18).SurveyCandidate
	empty := heard("bob", 1, "B", 30, 30).SurveyCandidate
	negative := heard("bob", 1, "B", 30, 37).SurveyCandidate
	negative.Chunk = -1
	both := empty
	both.Chunk = -1
	duplicate := heard("cyd", 1, "A", 150, 156).SurveyCandidate
	again := heard("cyd", 1, "A", 160, 166).SurveyCandidate
	backwards := heard("bob", 1, "B", 37, 30).SurveyCandidate
	cases := []struct {
		name   string
		cands  []SurveyCandidate
		want   SurveyCandidate
		reason string
		text   string
	}{
		{"an empty clip", []SurveyCandidate{ok, empty}, empty, "empty clip", "candidate c2-B of chunk 2: empty clip"},
		{"a clip ending before it starts", []SurveyCandidate{ok, backwards}, backwards, "empty clip", "candidate c2-B of chunk 2: empty clip"},
		{"a negative chunk", []SurveyCandidate{ok, negative}, negative, "negative chunk", "candidate c0-B of chunk 0: negative chunk"},
		{"one chunk and label twice", []SurveyCandidate{duplicate, ok, again}, again, "duplicate label", "candidate c2-A of chunk 2: duplicate label"},
		{"two faults report the first", []SurveyCandidate{ok, both}, both, "empty clip", "candidate c0-B of chunk 0: empty clip"},
		{"the first failing candidate of the slice", []SurveyCandidate{ok, negative, empty}, negative, "negative chunk", "candidate c0-B of chunk 0: negative chunk"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			m := ChainMerger{Transcribe: func(context.Context, RequestKind, []SampleClip, SourceInterval) (*RequestManifest, []Segment, error) {
				calls++
				return nil, nil, errors.New("no request may be sent")
			}}
			snap, report, err := m.Merge(context.Background(), tc.cands)
			var refused *CandidateError
			if !errors.As(err, &refused) {
				t.Fatalf("expected a *CandidateError, got %v", err)
			}
			if refused.Reason != tc.reason || !reflect.DeepEqual(refused.Candidate, tc.want) || err.Error() != tc.text {
				t.Errorf("got %+v (%q), want reason %q for %+v (%q)", refused, err, tc.reason, tc.want, tc.text)
			}
			if calls != 0 || !reflect.DeepEqual(snap, Snapshot{}) || !reflect.DeepEqual(report, MergeReport{}) {
				t.Errorf("expected no request and a zero result, got %v requests, %+v, %+v", calls, snap, report)
			}
		})
	}
}

// Merge.11
func TestMergerRequestFailure(t *testing.T) {
	s := mergeScript{
		heard("ann", 0, "A", 10, 18), heard("bob", 0, "B", 30, 37),
		heard("ann", 1, "B", 110, 118), heard("bob", 1, "A", 130, 137),
		heard("ann", 2, "A", 210, 218),
	}
	link2 := s.inside(t, "c2-A")

	t.Run("the forward request fails while the reversed one waits", func(t *testing.T) {
		cause := claierr.NewTransport(errors.New("connection reset by peer"))
		d := newMergeDiarizer(s.truth())
		d.peers = 2
		d.fail = map[heldClip]error{{RequestMergeForward, link2}: cause}
		d.block = map[heldClip]bool{{RequestMergeReversed, link2}: true}
		snap, report, err := mergeWith(ChainMerger{Transcribe: d.transcribe}, s.candidates())
		var transport *claierr.TransportError
		if !errors.Is(err, claierr.ErrTransport) || !errors.As(err, &transport) || transport != cause {
			t.Fatalf("expected the transport error under errors.Is and errors.As, got %v", err)
		}
		if want := "merge link for chunk 2: " + cause.Error(); err.Error() != want {
			t.Errorf("error = %q, want %q", err, want)
		}
		d.mu.Lock()
		cancelled := d.cancelled
		d.mu.Unlock()
		if cancelled != 1 {
			t.Errorf("expected the waiting request to see its context end, %v did", cancelled)
		}
		if len(d.requests()) != 2 || !reflect.DeepEqual(snap, Snapshot{}) || !reflect.DeepEqual(report, MergeReport{}) {
			t.Errorf("expected link 2 only and a zero result, got %v requests, %+v, %+v", len(d.requests()), snap, report)
		}
	})

	t.Run("the context is cancelled during link 2", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		d := newMergeDiarizer(s.truth())
		d.peers = 2
		d.block = map[heldClip]bool{{"", link2}: true}
		arrivals := 0
		d.arrived = func(kind RequestKind, samples []SampleClip) {
			d.mu.Lock()
			defer d.mu.Unlock()
			if arrivals++; arrivals == 2 {
				cancel()
			}
		}
		snap, report, err := ChainMerger{Transcribe: d.transcribe}.Merge(ctx, s.candidates())
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected the context's error, got %v", err)
		}
		if len(d.requests()) != 2 || !reflect.DeepEqual(snap, Snapshot{}) || !reflect.DeepEqual(report, MergeReport{}) {
			t.Errorf("expected link 2 only and a zero result, got %v requests, %+v, %+v", len(d.requests()), snap, report)
		}
	})

	t.Run("a request answers without a manifest", func(t *testing.T) {
		d := newMergeDiarizer(s.truth())
		m := ChainMerger{Transcribe: func(ctx context.Context, kind RequestKind, samples []SampleClip, core SourceInterval) (*RequestManifest, []Segment, error) {
			if kind == RequestMergeReversed {
				return nil, nil, nil
			}
			return d.transcribe(ctx, kind, samples, core)
		}}
		snap, _, err := mergeWith(m, s.candidates())
		if err == nil || !strings.HasPrefix(err.Error(), "merge link for chunk 2: ") || !reflect.DeepEqual(snap, Snapshot{}) {
			t.Errorf("expected link 2 to fail without a manifest, got %v and %+v", err, snap)
		}
	})
}
