package audio

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/audio/voices"
	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/internal/testenv/procenv"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/clai/pkg/claierr"
	"github.com/baalimago/clai/pkg/diagnostic"
)

const sec = time.Second

// finalFooter is the board's last line at the end of a run, failures included.
var finalFooter = regexp.MustCompile(`requests \d+/≤\d+ · resent \d+ · retries \d+ · cache hits \d+ · uploaded \S+ · speakers \d+ · unknown \S+ · \S+ elapsed`)

// surveyRun is one Coordinator.Run over the chunk diarizer at audio=debug.
type surveyRun struct {
	c      *Coordinator
	asm    *arithmeticAssembler
	d      *chunkDiarizer
	segs   []Segment
	err    error
	status string
	log    string
}

func runSurvey(t *testing.T, d *chunkDiarizer, tweaks ...func(*Coordinator, *arithmeticAssembler)) surveyRun {
	t.Helper()
	return runSurveyCtx(t, context.Background(), d, tweaks...)
}

func runSurveyCtx(t *testing.T, ctx context.Context, d *chunkDiarizer, tweaks ...func(*Coordinator, *arithmeticAssembler)) surveyRun {
	t.Helper()
	plainStatus(t)
	c, asm, status := surveyFor(d)
	for _, tweak := range tweaks {
		tweak(c, asm)
	}
	log := &lockedWriter{w: &strings.Builder{}}
	segs, err := c.Run(procenv.Diagnostics(ctx, log, diagnostic.LevelDebug), "meeting.wav")
	return surveyRun{c: c, asm: asm, d: d, segs: segs, err: err, status: status.String(), log: log.String()}
}

// onePerVoice asserts that each voice's speech has exactly one speaker-N,
// no two voices share one, and returns voice to speaker.
func onePerVoice(t *testing.T, segs []Segment, truth []voiceSpan, voices ...string) map[string]string {
	t.Helper()
	ids := idsByVoice(t, segs, truth)
	got := map[string]string{}
	owner := map[string]string{}
	for _, v := range voices {
		if len(ids[v]) != 1 {
			t.Errorf("voice %v maps to %v, want one speaker", v, ids[v])
			continue
		}
		for id := range ids[v] {
			if !strings.HasPrefix(id, "speaker-") {
				t.Errorf("voice %v is %v, want a speaker-N", v, id)
			}
			if other, dup := owner[id]; dup {
				t.Errorf("voices %v and %v share %v", other, v, id)
			}
			owner[id], got[v] = v, id
		}
	}
	return got
}

// unknownOnly asserts that a voice's speech is under one unknown-N only.
func unknownOnly(t *testing.T, segs []Segment, truth []voiceSpan, voice string) {
	t.Helper()
	ids := idsByVoice(t, segs, truth)[voice]
	if len(ids) != 1 {
		t.Fatalf("voice %v is under %v, want one unknown-N", voice, ids)
	}
	for id := range ids {
		if !strings.HasPrefix(id, unknownPrefix) {
			t.Errorf("voice %v is %v, want unknown-N", voice, id)
		}
	}
}

func unknownNames(segs []Segment) []string {
	var names []string
	for _, s := range segs {
		if strings.HasPrefix(s.Speaker, unknownPrefix) && !slices.Contains(names, s.Speaker) {
			names = append(names, s.Speaker)
		}
	}
	return names
}

func speechOf(segs []Segment) time.Duration {
	total := time.Duration(0)
	for _, s := range segs {
		total += s.End - s.Start
	}
	return total
}

func truthSpeech(truth []voiceSpan) time.Duration {
	total := time.Duration(0)
	for _, v := range truth {
		total += time.Duration((v.end - v.start) * float64(time.Second))
	}
	return total
}

// requestsOf lists the requests the diarizer saw for the core starting at.
func requestsOf(d *chunkDiarizer, at time.Duration) []seenRequest {
	var out []seenRequest
	for _, r := range d.requests() {
		if r.core.Start == at && r.class != classMergeForward && r.class != classMergeReversed {
			out = append(out, r)
		}
	}
	return out
}

// clipAt is the source instant one second into a voice's first turn of a
// chunk: inside that voice's candidate clip there.
func clipAt(truth []voiceSpan, voice string, chunk int) time.Duration {
	return firstTurn(truth, voice, chunk) + sec
}

// splitInMerge makes every merge request hear the clip at as voice as.
func splitInMerge(at time.Duration, voice, as string) voiceRule {
	return voiceRule{when: heldBy{class: "merge", core: anyCore, at: at}, voice: voice, as: as, inClip: true}
}

// coreHeardAs makes a chunk's prefixed request of turn (0 any) hear voice's
// core speech as as.
func coreHeardAs(core time.Duration, turn int, voice, as string) voiceRule {
	return voiceRule{when: heldBy{class: classPrefixed, core: core, turn: turn, at: noClip}, voice: voice, as: as}
}

func assertSourceTimes(t *testing.T, segs []Segment, truth []voiceSpan) {
	t.Helper()
	if len(segs) != len(truth) {
		t.Fatalf("%v segments, want %v", len(segs), len(truth))
	}
	for i, v := range truth {
		want := SourceInterval{Start: time.Duration(v.start * float64(time.Second)), End: time.Duration(v.end * float64(time.Second))}
		if segs[i].Start != want.Start || segs[i].End != want.End {
			t.Errorf("segment %v at %v–%v, want %v–%v", i, segs[i].Start, segs[i].End, want.Start, want.End)
		}
	}
}

// Run.2–Run.4, Run.6, Run.11, Run.18, I10
func TestSurveyPathLabelsEveryChunk(t *testing.T) {
	truth := twoInChunkOne()
	d := newChunkDiarizer(truth)
	r := runSurvey(t, d)
	if r.err != nil {
		t.Fatal(r.err)
	}
	ids := onePerVoice(t, r.segs, truth, "v1", "v2", "v3", "v4", "v5", "v6")
	want := map[string]string{"v1": "speaker-1", "v2": "speaker-2", "v3": "speaker-3", "v4": "speaker-4", "v5": "speaker-5", "v6": "speaker-6"}
	if !maps.Equal(ids, want) {
		t.Errorf("speakers %v, want %v numbered by earliest clip", ids, want)
	}
	if names := unknownNames(r.segs); len(names) != 0 {
		t.Errorf("unknown speech %v, want none", names)
	}
	tot := r.c.Totals()
	if tot.Requests != 14 || tot.Retries != 0 || tot.Unknown != 0 || tot.Cores != 4 || tot.CacheHits != 0 {
		t.Errorf("totals %+v, want 14 requests (4 surveys, 6 merge, 4 labelling) and no retry", tot)
	}
	if d.count(classSurvey) != 4 || d.count("merge") != 6 || d.count(classPrefixed) != 4 {
		t.Errorf("surveys %v, merge %v, prefixed %v; want 4, 6, 4", d.count(classSurvey), d.count("merge"), d.count(classPrefixed))
	}
	assertSourceTimes(t, r.segs, truth)
}

// Run.2, I3, I6
func TestSurveyNeverReachesTheOutput(t *testing.T) {
	truth := twoInChunkOne()
	d := newChunkDiarizer(truth)
	d.textIsClass = true
	// Chunk 1's survey calls v1 B and v2 C; its labelling request calls the
	// clips speaker-1 (v1) A and speaker-2 (v2) B: B names two people.
	d.shift = func(r seenRequest) int {
		if r.class == classSurvey {
			return 1 + int(r.core.Start/(450*sec))
		}
		return 0
	}
	r := runSurvey(t, d)
	if r.err != nil {
		t.Fatal(r.err)
	}
	for _, s := range r.segs {
		if s.Text != string(classPrefixed) {
			t.Fatalf("output segment %+v was read from a %q answer", s, s.Text)
		}
	}
	onePerVoice(t, r.segs, truth, "v1", "v2", "v3", "v4", "v5", "v6")
	reqs := r.asm.requests()
	for i, q := range reqs {
		if survey := i < 4; survey != (len(q.Samples) == 0) {
			t.Errorf("request %v %+v: the four surveys come first and only they hold no clip", i, q)
		}
	}
	cores := map[SourceInterval]bool{}
	for _, q := range reqs[:4] {
		cores[q.Core] = true
	}
	for _, core := range cores4() {
		if !cores[core] {
			t.Errorf("no survey of core %v", core)
		}
	}
}

// Run.1, I5
func TestPlanReservesEightSlots(t *testing.T) {
	eight := meetingOf([]string{"v1", "v2", "v3", "v4"}, []string{"v5", "v6", "v7", "v8"}, []string{"v1", "v3", "v5", "v7"}, []string{"v2", "v4", "v6", "v8"})
	tests := []struct {
		name     string
		truth    []voiceSpan
		speakers int
	}{
		{"the reserve is 88 s", twoInChunkOne(), 6},
		{"a registry of eight fits every request", eight, 8},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newChunkDiarizer(tt.truth)
			r := runSurvey(t, d, func(c *Coordinator, asm *arithmeticAssembler) {
				asm.maxRequestDuration = c.Budgets.MaxRequestDuration
			})
			if r.err != nil {
				t.Fatal(r.err)
			}
			runner := r.c.Runner.(*scriptedRunner)
			if runner.count("ffprobe", "") != 1 || runner.count("ffmpeg", "silencedetect") != 1 {
				t.Errorf("ffprobe %v, silence passes %v; want one plan", runner.count("ffprobe", ""), runner.count("ffmpeg", "silencedetect"))
			}
			if len(r.c.states) != 4 {
				t.Fatalf("%v cores, want 4 of 450 s (a 33 s reserve plans 3)", len(r.c.states))
			}
			for _, st := range r.c.states {
				if st.core.End-st.core.Start+88*sec > r.c.Budgets.MaxRequestDuration {
					t.Errorf("core %v plus the 88 s reserve exceeds %v", st.core, r.c.Budgets.MaxRequestDuration)
				}
			}
			if n := len(r.c.registry.Speakers); n != tt.speakers {
				t.Errorf("%v speakers, want %v", n, tt.speakers)
			}
			assembled := map[string]bool{}
			for _, q := range r.asm.requests() {
				assembled[fmt.Sprint(q.Core, clipSpans(q.Samples))] = true
			}
			for _, seen := range d.requests() {
				if !assembled[fmt.Sprint(seen.core, clipSpans(seen.samples))] {
					t.Errorf("%v request %v was uploaded without the assembler", seen.class, seen.core)
				}
			}
			if d.count("merge") == 0 || len(d.requests())+r.c.Totals().CacheHits != len(r.asm.requests()) {
				t.Errorf("assembled %v, uploaded %v; every request, merge links included, is assembled", len(r.asm.requests()), len(d.requests()))
			}
		})
	}
}

func clipSpans(clips []SampleClip) []SourceInterval {
	out := make([]SourceInterval, len(clips))
	for i, c := range clips {
		out[i] = SourceInterval{Start: c.Start, End: c.End}
	}
	return out
}

// Run.7, Run.8, I2, I4
func TestJoinAfterLabelling(t *testing.T) {
	truth := twoInChunkOne()
	b, c := clipAt(truth, "v1", 1), clipAt(truth, "v1", 2)
	split := splitInMerge(b, "v1", "v1-b")
	tests := []struct {
		name     string
		script   func(d *chunkDiarizer)
		joins    []string
		speakers int
		retries  int
		// clips are the joined speaker's, ranked
		clips []SourceInterval
	}{
		{
			"one label in all four requests joins", func(d *chunkDiarizer) { d.rules = []voiceRule{split} },
			[]string{`join from=speaker-3 into=speaker-1 shared=4 apart=0$`},
			6, 0,
			[]SourceInterval{{0, 10 * sec}, {450 * sec, 460 * sec}, {936 * sec, 946 * sec}, {1368 * sec, 1378 * sec}},
		},
		{
			"one request apart joins nothing", func(d *chunkDiarizer) {
				d.rules = []voiceRule{split, {when: heldBy{class: classPrefixed, core: 1350 * sec, turn: 1, at: b}, voice: "v1", as: "v1-apart", inClip: true}}
			},
			nil, 7, 3, nil,
		},
		{
			"one shared request only joins nothing", func(d *chunkDiarizer) {
				d.rules = []voiceRule{split}
				for _, core := range []time.Duration{450 * sec, 900 * sec, 1350 * sec} {
					d.mix = append(d.mix, heldBy{class: classPrefixed, core: core, turn: 1, at: b})
				}
			},
			nil, 7, 1, nil,
		},
		{
			"a speaker kept apart from a joined one stays", func(d *chunkDiarizer) {
				d.rules = []voiceRule{split, splitInMerge(c, "v1", "v1-c"), {when: heldBy{class: classPrefixed, core: 1350 * sec, turn: 1, at: c}, voice: "v1", as: "v1-apart", inClip: true}}
				d.mix = []heldBy{{class: classPrefixed, core: 1350 * sec, turn: 1, at: b}}
			},
			[]string{`join from=speaker-3 into=speaker-1 shared=3 apart=0$`},
			7, 3,
			[]SourceInterval{{0, 10 * sec}, {450 * sec, 460 * sec}, {1368 * sec, 1378 * sec}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newChunkDiarizer(truth)
			tt.script(d)
			r := runSurvey(t, d)
			if r.err != nil {
				t.Fatal(r.err)
			}
			joins := regexp.MustCompile(`(?m)^debug \[audio\] calibration join from=.*$`).FindAllString(r.log, -1)
			if len(joins) != len(tt.joins) {
				t.Fatalf("joins %q, want %d", joins, len(tt.joins))
			}
			assertRecords(t, r.log, tt.joins...)
			assertRecords(t, r.log, fmt.Sprintf(`join state=done joined=%d speakers=%d$`, len(tt.joins), tt.speakers))
			if n := len(r.c.registry.Speakers); n != tt.speakers {
				t.Errorf("%v speakers after the join, want %v", n, tt.speakers)
			}
			for i, sp := range r.c.registry.Speakers {
				if want := fmt.Sprintf("speaker-%d", i+1); sp.ID != want || sp.Sample != sp.Clips[0] {
					t.Errorf("speaker %v is %v with sample %v, want %v numbered without a gap and its best clip", i, sp.ID, sp.Sample, want)
				}
			}
			if got := r.c.Totals().Retries; got != tt.retries {
				t.Errorf("%v retries, want %v", got, tt.retries)
			}
			if len(tt.joins) == 0 {
				if strings.Contains(r.log, "calibration registry ") {
					t.Error("a registry record without a join")
				}
				return
			}
			assertRecords(t, r.log, fmt.Sprintf(`registry id=speaker-1 was=speaker-1\+speaker-3 clips=%d sample=0\.0-10\.0$`, len(tt.clips)))
			if got := clipSpans(r.c.registry.Speakers[0].Clips); !slices.Equal(got, tt.clips) {
				t.Errorf("joined speaker's clips %v, want %v: both speakers' clips, ranked", got, tt.clips)
			}
		})
	}
	t.Run("a merger whose report breaks Merge.7 joins nothing", func(t *testing.T) {
		d := newChunkDiarizer(truth)
		d.rules = []voiceRule{split}
		r := runSurvey(t, d, func(c *Coordinator, asm *arithmeticAssembler) {
			c.Merger = reportless{ChainMerger{Transcribe: c.mergeRequest(asm, nil)}}
		})
		if r.err != nil || len(r.c.registry.Speakers) != 7 || strings.Contains(r.log, "calibration join from=") {
			t.Errorf("err %v, %v speakers; want no join without the report's ranking", r.err, len(r.c.registry.Speakers))
		}
	})
	// Rows (e) and (f): v3 and v4 speak in chunk 2, whose survey hears them
	// under labels of their own, and every labelling request hears v4's clip
	// as v3.
	v4Clip := clipAt(truth, "v4", 1)
	clipAsV3 := voiceRule{when: heldBy{class: classPrefixed, core: anyCore, turn: 1, at: v4Clip}, voice: "v4", as: "v3", inClip: true}
	t.Run("a bad clip that leaves its voice naming nobody joins nothing", func(t *testing.T) {
		d := newChunkDiarizer(truth)
		d.rules = []voiceRule{clipAsV3}
		r := runSurvey(t, d)
		if r.err != nil {
			t.Fatal(r.err)
		}
		if strings.Contains(r.log, "calibration join from=") || len(r.c.registry.Speakers) != 6 {
			t.Fatalf("%v speakers after the join; want v3 and v4 kept apart:\n%s", len(r.c.registry.Speakers), r.log)
		}
		assertRecords(t, r.log, `join refused a=speaker-3 b=speaker-4 shared=4 chunks=2$`, `join state=done joined=0 speakers=6$`)
		onePerVoice(t, r.segs, truth, "v1", "v2", "v3", "v4", "v5", "v6")
		if names := unknownNames(r.segs); len(names) != 0 || r.c.Totals().Retries != 2 {
			t.Errorf("unknown %v, totals %+v; want every voice named after the retries of chunks 2 and 4", names, r.c.Totals())
		}
	})
	t.Run("a survey that split one person still joins", func(t *testing.T) {
		d := newChunkDiarizer(truth)
		clip := clipAsV3
		clip.when.turn = 0
		d.rules = []voiceRule{clip, {when: heldBy{class: classPrefixed, core: anyCore, at: noClip}, voice: "v4", as: "v3"}}
		r := runSurvey(t, d)
		if r.err != nil {
			t.Fatal(r.err)
		}
		assertRecords(t, r.log, `join from=speaker-4 into=speaker-3 shared=4 apart=0$`, `join state=done joined=1 speakers=5$`)
		if strings.Contains(r.log, "calibration join refused") || len(unknownNames(r.segs)) != 0 || r.c.Totals().Retries != 0 {
			t.Errorf("unknown %v, totals %+v; want the join, nothing unknown and no retry", unknownNames(r.segs), r.c.Totals())
		}
	})
	t.Run("a voice left over elsewhere does not keep a split person apart", func(t *testing.T) {
		d := newChunkDiarizer(truth)
		clip := clipAsV3
		clip.when.turn = 0
		d.rules = []voiceRule{clip, {when: heldBy{class: classPrefixed, core: anyCore, at: noClip}, voice: "v4", as: "v3"}, coreHeardAs(900*sec, 1, "v5", "v5-core")}
		r := runSurvey(t, d)
		if r.err != nil {
			t.Fatal(r.err)
		}
		assertRecords(t, r.log, `join from=speaker-4 into=speaker-3 shared=4 apart=0$`)
		if strings.Contains(r.log, "calibration join refused") {
			t.Error("a refusal for a chunk whose survey did not hear the pair")
		}
	})
	t.Run("a speaker the survey heard apart from one already joined stays apart", func(t *testing.T) {
		// The merge splits v1's chunk-2 clip off as speaker-3, which joins
		// speaker-1; v4's clip is heard as v1 in every labelling request,
		// and chunk 2's survey heard v4 apart from that clip.
		d := newChunkDiarizer(truth)
		v4AsV1 := clipAsV3
		v4AsV1.as = "v1"
		d.rules = []voiceRule{split, v4AsV1}
		r := runSurvey(t, d)
		if r.err != nil {
			t.Fatal(r.err)
		}
		assertRecords(t, r.log, `join from=speaker-3 into=speaker-1 shared=4 apart=0$`, `join refused a=speaker-3 b=speaker-5 shared=4 chunks=2$`, `join state=done joined=1 speakers=6$`)
		onePerVoice(t, r.segs, truth, "v1", "v2", "v3", "v4", "v5", "v6")
		if names := unknownNames(r.segs); len(names) != 0 {
			t.Errorf("unknown %v, want every voice named after the retries", names)
		}
	})
	t.Run("the joined speaker labels every chunk", func(t *testing.T) {
		d := newChunkDiarizer(truth)
		d.rules = []voiceRule{split}
		r := runSurvey(t, d)
		if r.err != nil {
			t.Fatal(r.err)
		}
		ids := onePerVoice(t, r.segs, truth, "v1", "v2", "v3", "v4", "v5", "v6")
		if ids["v1"] != "speaker-1" || ids["v3"] != "speaker-3" || len(unknownNames(r.segs)) != 0 || r.c.Totals().Requests != 14 {
			t.Errorf("speakers %v, unknown %v, totals %+v; want one v1, renumbered, no retry", ids, unknownNames(r.segs), r.c.Totals())
		}
	})
}

// retryEverything is twoInChunkOne whose chunk 3 labelling answer puts the
// clips of speaker-4 (v4) and speaker-6 (v6) on one label, mixes the clip
// of speaker-3 (v3) and hears v5's core speech under a label no clip holds.
func retryEverything() *chunkDiarizer {
	truth := twoInChunkOne()
	d := newChunkDiarizer(truth)
	labelling := heldBy{class: classPrefixed, core: 900 * sec, turn: 1}
	onV4 := labelling
	onV4.at = clipAt(truth, "v4", 1)
	onV3 := labelling
	onV3.at = clipAt(truth, "v3", 1)
	d.rules = []voiceRule{{when: onV4, voice: "v4", as: "v6", inClip: true}, coreHeardAs(900*sec, 1, "v5", "v5-core")}
	d.mix = []heldBy{onV3}
	return d
}

// Run.9, I4, I8
func TestRetryFixesEverythingAtOnce(t *testing.T) {
	d := retryEverything()
	r := runSurvey(t, d)
	if r.err != nil {
		t.Fatal(r.err)
	}
	for _, core := range cores4() {
		want := 2
		if core.Start == 900*sec {
			want = 3
		}
		if got := requestsOf(d, core.Start); len(got) != want {
			t.Errorf("core %v: %v requests, want %v", core, len(got), want)
		}
	}
	got := requestsOf(d, 900*sec)
	if len(got) != 3 {
		t.FailNow()
	}
	type clip struct {
		id        string
		start     time.Duration
		candidate bool
	}
	var sent []clip
	for _, s := range got[2].samples {
		sent = append(sent, clip{s.ID, s.Start, s.Candidate})
	}
	want := []clip{{"speaker-6", 1350 * sec, false}, {"speaker-5", 918 * sec, false}, {"speaker-4", 486 * sec, false}, {"speaker-3", 1386 * sec, false}, {"speaker-2", 18 * sec, false}, {"speaker-1", 0, false}}
	if len(sent) != 7 || !slices.Equal(sent[:6], want) || !sent[6].candidate || sent[6].start != 918*sec {
		t.Errorf("retry prefix %v, want %v reversed with speaker-3's next clip in place, then v5's candidate", sent, want)
	}
	for _, other := range []time.Duration{0, 450 * sec, 1350 * sec} {
		for _, q := range requestsOf(d, other) {
			if slices.ContainsFunc(q.samples, func(s SampleClip) bool { return s.Start == 1386*sec }) {
				t.Errorf("speaker-3's next clip was sent for core %v", other)
			}
		}
	}
	for i, sp := range r.c.registry.Speakers {
		if sp.Sample != sp.Clips[0] || (i == 2 && sp.Sample.Start != 468*sec) {
			t.Errorf("speaker %v sample %v changed after the run", sp.ID, sp.Sample)
		}
	}
	onePerVoice(t, r.segs, twoInChunkOne(), "v1", "v2", "v3", "v4", "v5", "v6")
	if r.c.Totals().Retries != 1 {
		t.Errorf("totals %+v, want one retry", r.c.Totals())
	}
}

// Run.9: a joined speaker none of whose two clips settled gets its next
// clip for the first of them; nothing is dropped.
func TestRetrySwapsAJoinedSpeakersFirstFailedClip(t *testing.T) {
	clips := []SampleClip{{ID: "speaker-1", Start: 0, End: 10 * sec}, {ID: "speaker-3", Start: 450 * sec, End: 460 * sec}, {ID: "speaker-2", Start: 18 * sec, End: 28 * sec}}
	owners := []string{"speaker-1", "speaker-1", "speaker-2"}
	lab := answer{clips: clips}
	labMap := Mapping{Samples: []SampleResult{{Status: SampleMixed}, {Status: SampleMissing}, {Label: "B", Labeled: sec, Share: 1, Status: SampleMapped}}}
	registry := Snapshot{Speakers: []Speaker{
		{ID: "speaker-1", Clips: []SampleClip{{ID: "speaker-1", Start: 0, End: 10 * sec}, {ID: "speaker-1", Start: 450 * sec, End: 460 * sec}, {ID: "speaker-1", Start: 936 * sec, End: 946 * sec}}},
		{ID: "speaker-2", Clips: []SampleClip{{ID: "speaker-2", Start: 18 * sec, End: 28 * sec}, {ID: "speaker-2", Start: 900 * sec, End: 910 * sec}}},
	}}
	p := planRetry(lab, labMap, owners, registry)
	want := []SourceInterval{{936 * sec, 946 * sec}, {450 * sec, 460 * sec}, {18 * sec, 28 * sec}}
	if got := clipSpans(p.clips); !slices.Equal(got, want) || !slices.Equal(p.owners, owners) || p.reversed || p.unchanged(clips) {
		t.Errorf("retry clips %v owners %v, want %v", got, p.owners, want)
	}
	if !slices.Equal(p.swapped, []string{"speaker-1@0.0-10.0>936.0-946.0"}) {
		t.Errorf("swapped %v", p.swapped)
	}
}

// Run.9: two speakers whose clips settled on one label both get their next
// clip, in the reversed order.
func TestRetrySwapsBothClipsOnASharedLabel(t *testing.T) {
	clips := []SampleClip{{ID: "speaker-1", Start: 0, End: 10 * sec}, {ID: "speaker-2", Start: 18 * sec, End: 28 * sec}, {ID: "speaker-3", Start: 36 * sec, End: 46 * sec}}
	owners := []string{"speaker-1", "speaker-2", "speaker-3"}
	labMap := Mapping{Samples: []SampleResult{
		{Label: "A", Labeled: sec, Share: 1, Status: SampleAmbiguous},
		{Label: "A", Labeled: sec, Share: 1, Status: SampleAmbiguous},
		{Label: "B", Labeled: sec, Share: 1, Status: SampleMapped},
	}}
	registry := Snapshot{Speakers: []Speaker{
		{ID: "speaker-1", Clips: []SampleClip{{Start: 0, End: 10 * sec}, {Start: 500 * sec, End: 510 * sec}}},
		{ID: "speaker-2", Clips: []SampleClip{{Start: 18 * sec, End: 28 * sec}, {Start: 900 * sec, End: 910 * sec}}},
		{ID: "speaker-3", Clips: []SampleClip{{Start: 36 * sec, End: 46 * sec}, {Start: 1300 * sec, End: 1310 * sec}}},
	}}
	p := planRetry(answer{clips: clips}, labMap, owners, registry)
	want := []SourceInterval{{36 * sec, 46 * sec}, {900 * sec, 910 * sec}, {500 * sec, 510 * sec}}
	if got := clipSpans(p.clips); !slices.Equal(got, want) || !p.reversed || !slices.Equal(p.owners, []string{"speaker-3", "speaker-2", "speaker-1"}) {
		t.Errorf("retry clips %v owners %v reversed %v, want %v: both shared clips swapped, the settled one kept, reversed", got, p.owners, p.reversed, want)
	}
	if !slices.Equal(p.swapped, []string{"speaker-1@0.0-10.0>500.0-510.0", "speaker-2@18.0-28.0>900.0-910.0"}) || len(p.candidates) != 0 {
		t.Errorf("swapped %v candidates %v", p.swapped, p.candidates)
	}
}

// sourceOrderLetters labels prefixed requests from 'A' in order of first
// appearance, so their labels sort as their voices first speak.
func sourceOrderLetters(r seenRequest) int {
	if r.class == classPrefixed {
		return 0
	}
	return contentShift(RequestKind(r.class), r.samples) + int(r.core.Start/time.Second)
}

// Run.9, L2.2 (R2-08): the 8-clip cap, most speech first, the swap only for a
// speaker none of whose clips settled on a label of its own, no candidate for
// a shared label.
func TestRetryBuildsItsPrefix(t *testing.T) {
	eight := meetingOf([]string{"v1", "v2", "v3", "v4"}, []string{"v5", "v6", "v7", "v8"}, []string{"v1", "v3", "v5", "v7"}, []string{"v2", "v4", "v6", "v8"})
	var seven []voiceSpan
	turns := 0
	for _, v := range meetingOf([]string{"v1", "v2"}, []string{"v1", "v3", "v4"}, []string{"v5", "v2", "v1"}, []string{"v6", "v7", "v3"}) {
		if v.voice == "v5" {
			if turns++; turns > 4 {
				continue
			}
		}
		seven = append(seven, v)
	}
	shared := twoInChunkOne()
	tests := []struct {
		name     string
		truth    []voiceSpan
		rules    []voiceRule
		core     time.Duration
		requests int
		record   string
		// candidate is the start of the one candidate the retry carries; -1 none
		candidate time.Duration
		// retry is the retry's clips when not the labelling clips reversed
		retry []SourceInterval
	}{
		{
			"eight speakers leave no room for a candidate", eight,
			[]voiceRule{coreHeardAs(900*sec, 1, "v1", "v1-core")},
			900 * sec, 2,
			`retry chunk=3 attempt=2 state=skipped reason=nothing-to-change pending=\w:90\.0$`, -1, nil,
		},
		{
			"the most speech first, as many as fit", seven,
			[]voiceRule{coreHeardAs(900*sec, 1, "v5", "v5-core"), coreHeardAs(900*sec, 1, "v2", "v2-core")},
			900 * sec, 3,
			`retry chunk=3 attempt=2 state=start reversed=false swapped=- candidates=I:918\.0-928\.0 pending=H:60\.0,I:120\.0$`, 918 * sec, nil,
		},
		{
			"a shared label swaps both its clips and gives no candidate", shared,
			[]voiceRule{{when: heldBy{class: classPrefixed, core: 0, turn: 1, at: clipAt(shared, "v3", 1)}, voice: "v3", as: "v1", inClip: true}},
			0, 3,
			`retry chunk=1 attempt=2 state=start reversed=true swapped=speaker-1@0\.0-10\.0>450\.0-460\.0,speaker-3@468\.0-478\.0>1386\.0-1396\.0 candidates=- pending=\w:180\.0$`, -1,
			[]SourceInterval{{1350 * sec, 1360 * sec}, {918 * sec, 928 * sec}, {486 * sec, 496 * sec}, {1386 * sec, 1396 * sec}, {18 * sec, 28 * sec}, {450 * sec, 460 * sec}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newChunkDiarizer(tt.truth)
			d.rules, d.shift = tt.rules, sourceOrderLetters
			r := runSurvey(t, d)
			if r.err != nil {
				t.Fatal(r.err)
			}
			got := requestsOf(d, tt.core)
			if len(got) != tt.requests {
				t.Fatalf("%v requests for core %v, want %v", len(got), tt.core, tt.requests)
			}
			assertRecords(t, r.log, tt.record)
			if tt.requests < 3 {
				return
			}
			labelling, retry := got[1].samples, got[2].samples
			var cands []time.Duration
			for _, s := range retry {
				if s.Candidate {
					cands = append(cands, s.Start)
				}
			}
			if want := []time.Duration{tt.candidate}; (tt.candidate < 0 && len(cands) != 0) || (tt.candidate >= 0 && !slices.Equal(cands, want)) || len(retry) > maxVoices {
				t.Errorf("retry candidates %v of %v clips, want %v", cands, len(retry), tt.candidate)
			}
			backward := clipSpans(labelling)
			slices.Reverse(backward)
			if tt.retry != nil {
				backward = tt.retry
			}
			if tt.candidate < 0 && !slices.Equal(clipSpans(retry), backward) {
				t.Errorf("retry %v, want %v: the labelling clips %v reversed, with only the swaps the row names", clipSpans(retry), backward, clipSpans(labelling))
			}
		})
	}
}

// Run.6, L2.5, Run.7, Run.8 (R2-09)
func TestLabellingOrderJoinAndNaming(t *testing.T) {
	t.Run("labelling clips run by source start, not by speaker number", func(t *testing.T) {
		// v1 and v2 speak close together in chunk 1, so their best clips are
		// later ones: speaker-1's at 450 s, speaker-2's at 900 s
		var truth []voiceSpan
		for at := 0.0; at+33.5 <= 448; at += 33.5 {
			truth = append(truth, voiceSpan{at, at + 15, "v1"}, voiceSpan{at + 15.5, at + 30.5, "v2"})
		}
		for _, v := range twoInChunkOne() {
			if v.start >= 450 {
				truth = append(truth, v)
			}
		}
		d := newChunkDiarizer(truth)
		r := runSurvey(t, d)
		if r.err != nil {
			t.Fatal(r.err)
		}
		var ids []string
		for _, s := range requestsOf(d, 0)[1].samples {
			ids = append(ids, s.ID)
		}
		if want := []string{"speaker-1", "speaker-3", "speaker-4", "speaker-2", "speaker-5", "speaker-6"}; !slices.Equal(ids, want) {
			t.Errorf("labelling prefix %v, want %v", ids, want)
		}
		onePerVoice(t, r.segs, truth, "v1", "v2", "v3", "v4", "v5", "v6")
	})
	truth := twoInChunkOne()
	b, c := clipAt(truth, "v1", 1), clipAt(truth, "v1", 2)
	e := clipAt(truth, "v5", 2)
	labelling := func(core time.Duration, at time.Duration) heldBy {
		return heldBy{class: classPrefixed, core: core, turn: 1, at: at}
	}
	tests := []struct {
		name  string
		rules []voiceRule
		mix   []heldBy
		joins []string
		speak int
	}{
		{
			"a speaker apart from the earliest joins a later one it agrees with",
			[]voiceRule{
				splitInMerge(b, "v1", "v1-b"), splitInMerge(c, "v1", "v1-c"),
				{when: labelling(0, b), voice: "v1", as: "v1-q", inClip: true},
				{when: labelling(0, c), voice: "v1", as: "v1-q", inClip: true},
			},
			nil,
			[]string{`join from=speaker-7 into=speaker-3 shared=4 apart=0$`},
			7,
		},
		{
			"a speaker blocked at its earliest agreeing speaker tries no later one",
			[]voiceRule{
				splitInMerge(b, "v1", "v1-b"), splitInMerge(c, "v1", "v1-c"),
				{when: labelling(1350*sec, c), voice: "v1", as: "v1-apart", inClip: true},
				{when: labelling(1350*sec, e), voice: "v5", as: "v1-apart", inClip: true},
				{when: labelling(900*sec, e), voice: "v5", as: "v1", inClip: true},
			},
			[]heldBy{labelling(1350*sec, b), labelling(0, e), labelling(450*sec, e)},
			[]string{`join from=speaker-3 into=speaker-1 shared=3 apart=0$`},
			7,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newChunkDiarizer(truth)
			d.rules, d.mix = tt.rules, tt.mix
			r := runSurvey(t, d)
			if r.err != nil {
				t.Fatal(r.err)
			}
			joins := regexp.MustCompile(`(?m)^debug \[audio\] calibration join from=.*$`).FindAllString(r.log, -1)
			if len(joins) != len(tt.joins) {
				t.Fatalf("joins %q, want %d", joins, len(tt.joins))
			}
			assertRecords(t, r.log, tt.joins...)
			assertRecords(t, r.log, fmt.Sprintf(`join state=done joined=%d speakers=%d$`, len(tt.joins), tt.speak))
		})
	}
	t.Run("an unsettled clip names no label", func(t *testing.T) {
		d := newChunkDiarizer(truth)
		d.share = map[heldBy]float64{labelling(900*sec, e): 0.6}
		r := runSurvey(t, d)
		if r.err != nil {
			t.Fatal(r.err)
		}
		assertRecords(t, r.log,
			`sample chunk=3 attempt=2 id=speaker-5 label=\w share=0\.60 labelled=10\.\d verdict=mixed$`,
			`label chunk=3 attempt=2 label=\w speech=120\.0 material=true global=- reason=no-sample$`)
		if n := len(requestsOf(d, 900*sec)); n != 3 {
			t.Errorf("chunk 3 sent %v requests, want its retry: the label its 60 %% clip leans to names nobody", n)
		}
	})
}

// Run.13 (R2-10): with Parallelism 1 a second request waits for the slot,
// whatever its kind; unset, nothing waits.
func TestParallelismSlots(t *testing.T) {
	plan := &Plan{Cores: []CorePlan{{End: 450 * sec}, {Start: 450 * sec, End: 900 * sec}}}
	ended, cancel := context.WithCancel(context.Background())
	cancel()
	capped := &Coordinator{Parallelism: 1, StatusOut: &lockedWriter{w: &strings.Builder{}}}
	capped.start(context.Background(), "meeting.wav", plan)
	release, err := capped.slot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := capped.slot(ended); !errors.Is(err, context.Canceled) {
		t.Fatalf("a second request took the one slot: %v", err)
	}
	release()
	again, err := capped.slot(ended)
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err == nil {
		again()
	}
	if again, err = capped.slot(context.Background()); err != nil {
		t.Fatalf("the slot was not given back: %v", err)
	}
	again()
	unset := &Coordinator{StatusOut: &lockedWriter{w: &strings.Builder{}}}
	unset.start(context.Background(), "meeting.wav", plan)
	for range 8 {
		if _, err := unset.slot(ended); err != nil {
			t.Fatalf("an unset parallelism waited: %v", err)
		}
	}
}

// Run.9, Run.10
func TestRetryResult(t *testing.T) {
	short := twoInChunkOne()
	for k := range 8 {
		at := 900 + 54*float64(k) + 51.5
		short = append(short, voiceSpan{at, at + 2, "v7"})
	}
	slices.SortStableFunc(short, func(a, b voiceSpan) int { return int((a.start - b.start) * 1000) })
	tests := []struct {
		name     string
		truth    []voiceSpan
		rules    []voiceRule
		requests int
		record   string
		unknown  string
	}{
		{
			"more speech mapped is kept", twoInChunkOne(),
			[]voiceRule{coreHeardAs(900*sec, 1, "v5", "v5-core")},
			3,
			`retry chunk=3 attempt=3 outcome=accepted mapped=240\.0>360\.0 unknown=120\.0>0\.0 material_unmapped=1>0$`, "",
		},
		{
			"the same speech mapped keeps the labelling answer", twoInChunkOne(),
			[]voiceRule{coreHeardAs(900*sec, 0, "v5", "v5-core")},
			3,
			`retry chunk=3 attempt=3 outcome=kept reason=no-gain mapped=240\.0>240\.0 unknown=120\.0>120\.0 material_unmapped=1>1$`, "v5",
		},
		{
			"less speech mapped keeps the labelling answer", twoInChunkOne(),
			[]voiceRule{coreHeardAs(900*sec, 0, "v5", "v5-core"), coreHeardAs(900*sec, 2, "v2", "v2-core")},
			3,
			`retry chunk=3 attempt=3 outcome=kept reason=no-gain mapped=240\.0>120\.0 unknown=120\.0>240\.0 material_unmapped=1>2$`, "v5",
		},
		{
			"nothing to change sends nothing", short, nil, 2,
			`retry chunk=3 attempt=2 state=skipped reason=nothing-to-change pending=\w:16\.0$`, "v7",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newChunkDiarizer(tt.truth)
			d.rules = tt.rules
			r := runSurvey(t, d)
			if r.err != nil {
				t.Fatal(r.err)
			}
			if got := requestsOf(d, 900*sec); len(got) != tt.requests {
				t.Errorf("chunk 3 sent %v requests, want %v", len(got), tt.requests)
			}
			assertRecords(t, r.log, tt.record)
			if tt.unknown != "" {
				unknownOnly(t, r.segs, tt.truth, tt.unknown)
			}
			if speechOf(r.segs) != truthSpeech(tt.truth) {
				t.Errorf("speech %v, want %v", speechOf(r.segs), truthSpeech(tt.truth))
			}
		})
	}
}

// Run.10–Run.12, I4, I7
func TestUnmatchedVoiceStaysUnknown(t *testing.T) {
	var truth []voiceSpan
	turns := 0
	for _, v := range meetingOf([]string{"v1", "v2"}, []string{"v1", "v3", "v4"}, []string{"v2", "v5", "v1", "v7"}, []string{"v6", "v1", "v3"}) {
		if v.voice == "v7" {
			if turns++; turns > 4 {
				continue
			}
		}
		truth = append(truth, v)
	}
	d := newChunkDiarizer(truth)
	d.rules = []voiceRule{{when: heldBy{class: classSurvey, core: 900 * sec, at: noClip}, voice: "v7", as: "v5"}}
	r := runSurvey(t, d)
	if r.err != nil {
		t.Fatalf("a voice matching nobody failed the run: %v", r.err)
	}
	got := requestsOf(d, 900*sec)
	if len(got) != 3 {
		t.Fatalf("chunk 3 sent %v requests, want survey, labelling, retry", len(got))
	}
	first := firstTurn(truth, "v7", 2)
	cands := slices.DeleteFunc(slices.Clone(got[2].samples), func(s SampleClip) bool { return !s.Candidate })
	if len(cands) != 1 || cands[0].Start != first {
		t.Errorf("retry candidates %v, want her clip at %v", cands, first)
	}
	assertRecords(t, r.log,
		`retry chunk=3 attempt=2 state=start reversed=false swapped=- candidates=\w:954\.0-964\.0 pending=\w:60\.0$`,
		`sample chunk=3 attempt=3 id=cand-\w label=\w share=1\.00 labelled=10\.\d verdict=mapped$`,
		`label chunk=3 attempt=3 label=\w speech=60\.0 material=true global=- reason=no-sample$`,
		`retry chunk=3 attempt=3 outcome=kept reason=no-gain mapped=270\.0>270\.0 unknown=60\.0>60\.0 material_unmapped=1>1$`)
	unknownOnly(t, r.segs, truth, "v7")
	if names := unknownNames(r.segs); len(names) != 1 {
		t.Errorf("unknown names %v, want her one", names)
	}
	onePerVoice(t, r.segs, truth, "v1", "v2", "v3", "v4", "v5", "v6")
	if len(r.c.registry.Speakers) != 6 || speechOf(r.segs) != truthSpeech(truth) {
		t.Errorf("%v speakers, speech %v of %v; want six and nothing dropped", len(r.c.registry.Speakers), speechOf(r.segs), truthSpeech(truth))
	}
}

// Run.5, Run.12, Q2
func TestMoreThanEightVoices(t *testing.T) {
	people := func(last ...string) []voiceSpan {
		return meetingOf([]string{"p1", "p2", "p3"}, []string{"p4", "p5", "p6"}, []string{"p7", "p8", "p2"}, append([]string{"p1", "p2", "p3"}, last...))
	}
	tests := []struct {
		name    string
		truth   []voiceSpan
		line    string
		dropped []string
	}{
		{"ten", people("p9", "p10"), "found 10 voices; more than 8 usually means one person was not recognised across chunks", []string{"p9", "p10"}},
		{"nine", people("p9"), "found 9 voices; more than 8 usually means one person was not recognised across chunks", []string{"p9"}},
		{"eight", people(), "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := runSurvey(t, newChunkDiarizer(tt.truth))
			if r.err != nil {
				t.Fatalf("more voices than room failed the run: %v", r.err)
			}
			hasLine := regexp.MustCompile(`(?m)^found \d+ voices; `).FindString(r.status)
			switch {
			case tt.line == "" && hasLine != "":
				t.Errorf("status holds %q for eight voices", hasLine)
			case tt.line != "" && !strings.Contains(r.status, "\n"+tt.line+"\n"):
				t.Errorf("status lacks the line %q:\n%s", tt.line, r.status)
			}
			if n := len(r.c.registry.Speakers); n != 8 {
				t.Errorf("%v speakers, want 8", n)
			}
			kept := []string{"p1", "p2", "p3", "p4", "p5", "p6", "p7", "p8"}
			onePerVoice(t, r.segs, tt.truth, kept...)
			for _, v := range tt.dropped {
				unknownOnly(t, r.segs, tt.truth, v)
			}
		})
	}
}

// Run.13, Q1
func TestParallelism(t *testing.T) {
	tests := []struct {
		name        string
		parallelism int
		peers       map[requestClass]int
		most        int
	}{
		{"unset sends every request of a stage at once", 0, map[requestClass]int{classSurvey: 4, "merge": 2, classPrefixed: 4}, 4},
		{"two caps every stage", 2, map[requestClass]int{classSurvey: 2, "merge": 2, classPrefixed: 2}, 2},
		{"one sends a link's pair one after the other", 1, nil, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*sec)
			defer cancel()
			d := newChunkDiarizer(twoInChunkOne())
			d.peers = tt.peers
			r := runSurveyCtx(t, ctx, d, func(c *Coordinator, _ *arithmeticAssembler) { c.Parallelism = tt.parallelism })
			if r.err != nil {
				t.Fatalf("requests were not in flight together: %v", r.err)
			}
			if d.mostTotal != tt.most {
				t.Errorf("at most %v requests in flight, want %v", d.mostTotal, tt.most)
			}
			if tt.parallelism == 0 && (d.most[classSurvey] != 4 || d.most[classPrefixed] != 4 || d.most[classMergeForward] != 1 || d.most[classMergeReversed] != 1) {
				t.Errorf("in flight together %v, want every chunk's survey and labelling and a link's pair", d.most)
			}
		})
	}
}

// Par.1–Par.4
func TestParallelismDefault(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	if got := NewSplitter(&fakeTranscriber{}, &scriptedRunner{}).Parallelism; got != 0 {
		t.Errorf("NewSplitter sets parallelism %v, want none", got)
	}
	splitterOf := func(t *testing.T, conf Configurations) *Splitter {
		t.Helper()
		conf.Transcribe.Model = "gpt-4o-transcribe-diarize"
		s, err := createSplitter(conf)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	t.Run("unset", func(t *testing.T) {
		s := splitterOf(t, Default)
		if Default.Transcribe.Parallelism != 0 || s.Parallelism != 0 || s.workers() != 3 || (&Coordinator{Parallelism: s.Parallelism}).stageWorkers(5) != 5 {
			t.Errorf("default %v, splitter %v, plain workers %v; want unset, 3 plain, every chunk calibrated", Default.Transcribe.Parallelism, s.Parallelism, s.workers())
		}
	})
	t.Run("a value in the file is kept", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "audioConfig.json"), []byte(`{"transcribe":{"parallelism":5}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		conf, _, err := utils.LoadConfigFromFileCollect(context.Background(), dir, "audioConfig.json", nil, &Default)
		if err != nil {
			t.Fatal(err)
		}
		s := splitterOf(t, conf)
		if s.Parallelism != 5 || s.workers() != 5 || (&Coordinator{Parallelism: s.Parallelism}).stageWorkers(8) != 5 {
			t.Errorf("splitter %v, plain workers %v; want both capped at 5", s.Parallelism, s.workers())
		}
	})
	t.Run("the flag caps both paths", func(t *testing.T) {
		conf := Default
		f := &Flags{}
		fs := flag.NewFlagSet("audio", flag.ContinueOnError)
		f.Register(fs)
		if err := fs.Parse([]string{"-parallelism", "2"}); err != nil {
			t.Fatal(err)
		}
		if err := ApplyFlagOverrides(&conf, f); err != nil {
			t.Fatal(err)
		}
		s := splitterOf(t, conf)
		if s.Parallelism != 2 || s.workers() != 2 || (&Coordinator{Parallelism: s.Parallelism}).stageWorkers(8) != 2 {
			t.Errorf("splitter %v, plain workers %v; want both capped at 2", s.Parallelism, s.workers())
		}
	})
}

// Run.14, I8
func TestPathLengthIsFixed(t *testing.T) {
	tests := []struct {
		name     string
		rules    []voiceRule
		requests int
	}{
		{"every chunk still unmatched after its retry", []voiceRule{coreHeardAs(anyCore, 0, "v1", "v1-core")}, 18},
		{"a clean run", nil, 14},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newChunkDiarizer(twoInChunkOne())
			d.rules = tt.rules
			r := runSurvey(t, d)
			if r.err != nil {
				t.Fatal(r.err)
			}
			if got := r.c.Totals().Requests; got != tt.requests || len(d.requests()) != tt.requests {
				t.Errorf("%v requests (%v seen), want %v", got, len(d.requests()), tt.requests)
			}
			for _, core := range cores4() {
				if n := len(requestsOf(d, core.Start)); n > requestsPerChunk {
					t.Errorf("core %v sent %v requests", core, n)
				}
			}
			if regexp.MustCompile(`\s[4-9]/3\s`).MatchString(r.status) {
				t.Errorf("a req cell above 3/3:\n%s", r.status)
			}
		})
	}
}

// Run.15, Q1
func TestEveryRequestKindIsSentAgain(t *testing.T) {
	retry := coreHeardAs(900*sec, 1, "v5", "v5-core")
	clean := runSurvey(t, func() *chunkDiarizer {
		d := newChunkDiarizer(twoInChunkOne())
		d.rules = []voiceRule{retry}
		return d
	}())
	if clean.err != nil {
		t.Fatal(clean.err)
	}
	tests := []struct {
		kind   RequestKind
		at     heldBy
		record string
	}{
		{RequestSurvey, heldBy{class: classSurvey, core: 450 * sec, at: noClip}, `request-retry chunk=2 attempt=1 kind=survey try=1/4 delay=1\.0 cause="rate limited \(429\)"$`},
		{RequestMergeForward, heldBy{class: classMergeForward, core: anyCore, at: noClip}, `request-retry link=2 kind=merge-forward try=1/4 delay=1\.0 cause="rate limited \(429\)"$`},
		{RequestLabelling, heldBy{class: classPrefixed, core: 450 * sec, turn: 1, at: noClip}, `request-retry chunk=2 attempt=2 kind=labelling try=1/4 delay=1\.0 cause="rate limited \(429\)"$`},
		{RequestRetry, heldBy{class: classPrefixed, core: 900 * sec, turn: 2, at: noClip}, `request-retry chunk=3 attempt=3 kind=retry try=1/4 delay=1\.0 cause="rate limited \(429\)"$`},
	}
	for _, tt := range tests {
		t.Run(string(tt.kind), func(t *testing.T) {
			d := newChunkDiarizer(twoInChunkOne())
			d.rules = []voiceRule{retry}
			d.fail[tt.at] = []error{rateLimited(0)}
			schedule, waits := recordedSchedule()
			r := runSurvey(t, d, func(c *Coordinator, _ *arithmeticAssembler) { c.Retry = schedule })
			if r.err != nil {
				t.Fatal(r.err)
			}
			if !slices.Equal(r.segs, clean.segs) {
				t.Error("a request sent again changed the labels")
			}
			tot, want := r.c.Totals(), clean.c.Totals()
			if tot.Resent != 1 || tot.Requests != want.Requests || tot.Retries != want.Retries || !slices.Equal(*waits, []time.Duration{sec}) {
				t.Errorf("totals %+v after waits %v, want the clean %+v with one resend", tot, *waits, want)
			}
			for i, st := range r.c.states {
				if got, want := st.consumed.Load(), clean.c.states[i].consumed.Load(); got != want {
					t.Errorf("chunk %v counts %v requests, want %v: a resend is not a request of the chunk", i+1, got, want)
				}
			}
			assertRecords(t, r.log, tt.record)
		})
	}
}

var errOverBytes = errors.New("encoded request of 27000000 bytes plus the 16 KiB multipart envelope reserve exceeds max-request-bytes 26214400")

type stubMerger struct{ err error }

// regionless assembles manifests that map no region.
type regionless struct{ *arithmeticAssembler }

func (r regionless) Assemble(ctx context.Context, req AssembleRequest) (*RequestManifest, error) {
	if _, err := r.arithmeticAssembler.Assemble(ctx, req); err != nil {
		return nil, err
	}
	return &RequestManifest{RequestHash: fmt.Sprint(req)}, nil
}

// reportless is a Merger whose report names no voice.
type reportless struct{ Merger }

func (m reportless) Merge(ctx context.Context, cands []SurveyCandidate) (Snapshot, MergeReport, error) {
	snap, report, err := m.Merger.Merge(ctx, cands)
	report.Voices = nil
	return snap, report, err
}

func (m stubMerger) Merge(context.Context, []SurveyCandidate) (Snapshot, MergeReport, error) {
	return Snapshot{}, MergeReport{}, m.err
}

// blockOthers holds the requests of a stage on the other cores until the
// run cancels them.
func blockOthers(d *chunkDiarizer, class requestClass, turn int, failing time.Duration) {
	d.block = map[heldBy]bool{}
	for _, core := range cores4() {
		if core.Start != failing {
			d.block[heldBy{class: class, core: core.Start, turn: turn, at: noClip}] = true
		}
	}
}

// Run.16
func TestRealFailureFailsTheRun(t *testing.T) {
	transport := func() []error {
		return slices.Repeat([]error{claierr.NewTransport(errors.New("connection reset by peer"))}, 5)
	}
	probe := errors.New("ffprobe: no such file")
	candidate := &CandidateError{Reason: reasonDuplicateLabel}
	labelling := heldBy{class: classPrefixed, core: 450 * sec, turn: 1, at: noClip}
	tests := []struct {
		name   string
		setup  func(d *chunkDiarizer, c *Coordinator, asm *arithmeticAssembler)
		cancel bool
		want   error
		// held is how many requests must see their context end
		held  int
		row   bool
		phase string
		// text is a part of the error's text
		text string
	}{
		{"the plan fails", func(_ *chunkDiarizer, c *Coordinator, _ *arithmeticAssembler) {
			c.Runner.(*scriptedRunner).probeErr = probe
		}, false, probe, 0, false, "", "ffprobe failed on meeting.wav"},
		{"a survey is not answered", func(d *chunkDiarizer, _ *Coordinator, _ *arithmeticAssembler) {
			d.fail[heldBy{class: classSurvey, core: 450 * sec, at: noClip}] = transport()
			blockOthers(d, classSurvey, 0, 450*sec)
			d.peers = map[requestClass]int{classSurvey: 4}
		}, false, claierr.ErrTransport, 3, true, "finding voices · 4 chunks", "chunk 2 request 1 (7m30s with 0 samples): transport failure"},
		{"a merge request is not answered", func(d *chunkDiarizer, _ *Coordinator, _ *arithmeticAssembler) {
			d.fail[heldBy{class: classMergeForward, core: anyCore, at: noClip}] = transport()
			d.block = map[heldBy]bool{{class: classMergeReversed, core: anyCore, at: noClip}: true}
			d.peers = map[requestClass]int{"merge": 2}
		}, false, claierr.ErrTransport, 1, false, "matching voices · 11 clips", "merge link for chunk 2: transport failure"},
		{"a labelling request is not answered", func(d *chunkDiarizer, _ *Coordinator, _ *arithmeticAssembler) {
			d.fail[labelling] = transport()
			blockOthers(d, classPrefixed, 1, 450*sec)
			d.peers = map[requestClass]int{classPrefixed: 4}
		}, false, claierr.ErrTransport, 3, true, "labelling · 4 chunks · 6 speakers", "chunk 2 request 2 ("},
		{"a labelling request is refused for the key", func(d *chunkDiarizer, _ *Coordinator, _ *arithmeticAssembler) {
			d.fail[labelling] = []error{claierr.NewAuthFailed(&claierr.APIError{StatusCode: 401})}
			blockOthers(d, classPrefixed, 1, 450*sec)
			d.peers = map[requestClass]int{classPrefixed: 4}
		}, false, claierr.ErrAuthFailed, 3, true, "labelling · 4 chunks · 6 speakers", "chunk 2 request 2 ("},
		{"the assembler refuses a labelling request over the byte budget", func(d *chunkDiarizer, _ *Coordinator, asm *arithmeticAssembler) {
			// The refusal waits until the other three are in flight
			asm.refuse = func(req AssembleRequest) error {
				if req.Core.Start == 450*sec && len(req.Samples) > 0 {
					<-d.gateOpened(classPrefixed)
					return errOverBytes
				}
				return nil
			}
			blockOthers(d, classPrefixed, 1, 450*sec)
			d.peers = map[requestClass]int{classPrefixed: 3}
		}, false, errOverBytes, 3, true, "labelling · 4 chunks · 6 speakers", "chunk 2: encoded request of"},
		{"the assembler gives a manifest without regions", func(_ *chunkDiarizer, c *Coordinator, asm *arithmeticAssembler) {
			c.NewAssembler = func(CommandRunner, Budgets, string, time.Duration) (RequestAssembler, error) {
				return regionless{asm}, nil
			}
		}, false, errNoManifest, 0, true, "finding voices · 4 chunks", "survey request answered without a manifest"},
		{"the merger refuses a candidate", func(_ *chunkDiarizer, c *Coordinator, _ *arithmeticAssembler) {
			c.Merger = stubMerger{err: candidate}
		}, false, candidate, 0, false, "matching voices · 11 clips", "candidate c1- of chunk 1: duplicate label"},
		{"cancelled while a link waits for its slot", func(d *chunkDiarizer, c *Coordinator, _ *arithmeticAssembler) {
			c.Parallelism = 1
			d.block = map[heldBy]bool{{class: "merge", core: anyCore, at: noClip}: true}
		}, true, context.Canceled, 1, false, "matching voices · 11 clips", ""},
		{"cancelled in the labelling pass", func(d *chunkDiarizer, _ *Coordinator, _ *arithmeticAssembler) {
			d.block = map[heldBy]bool{{class: classPrefixed, core: 900 * sec, turn: 1, at: noClip}: true}
		}, true, context.Canceled, 1, true, "labelling · 4 chunks · 6 speakers", ""},
		{"cancelled while a retry is in flight", func(d *chunkDiarizer, _ *Coordinator, _ *arithmeticAssembler) {
			d.rules = []voiceRule{coreHeardAs(900*sec, 1, "v5", "v5-core")}
			d.block = map[heldBy]bool{{class: classPrefixed, core: 900 * sec, turn: 2, at: noClip}: true}
		}, true, context.Canceled, 1, true, "second try · 1 chunks with unmatched voices", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			guard, stop := context.WithTimeout(context.Background(), 10*sec)
			defer stop()
			ctx, cancel := context.WithCancel(guard)
			defer cancel()
			d := newChunkDiarizer(twoInChunkOne())
			d.blocked = make(chan seenRequest, 8)
			schedule, _ := recordedSchedule()
			var asm *arithmeticAssembler
			var status *lockedWriter
			c, a, s := surveyFor(d)
			asm, status, c.Retry = a, s, schedule
			tt.setup(d, c, asm)
			if tt.cancel {
				go func() {
					<-d.blocked
					cancel()
				}()
			}
			plainStatus(t)
			segs, err := c.Run(ctx, "meeting.wav")
			if segs != nil || !errors.Is(err, tt.want) {
				t.Fatalf("got %v segments and %v, want none and %v", len(segs), err, tt.want)
			}
			if guard.Err() != nil {
				t.Fatal("the run ended only at the test's deadline")
			}
			if !strings.Contains(err.Error(), tt.text) {
				t.Errorf("error %q, want it to hold %q", err, tt.text)
			}
			if d.cancelled != tt.held {
				t.Errorf("%v requests saw their context end, want %v", d.cancelled, tt.held)
			}
			out := status.String()
			if tt.phase == "" {
				if out != "" || len(d.requests()) != 0 {
					t.Errorf("a failed plan sent %v requests and drew %q", len(d.requests()), out)
				}
				return
			}
			if !asm.closed {
				t.Error("the assembler was left open")
			}
			lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
			if last := lines[len(lines)-1]; !regexp.MustCompile(`^  ` + finalFooter.String() + `$`).MatchString(last) {
				t.Errorf("status ends with %q, want the final footer", last)
			}
			phases := regexp.MustCompile(`(?m)^\s*phase\s+(.*)$`).FindAllStringSubmatch(out, -1)
			if got := phases[len(phases)-1][1]; got != tt.phase {
				t.Errorf("last phase %q, want %q", got, tt.phase)
			}
			if strings.Contains(out, stateRequestFailed) != tt.row {
				t.Errorf("request failed on a row: %v, want %v:\n%s", !tt.row, tt.row, out)
			}
		})
	}
}

// Run.10a, Run.12, Run.18, I7
func TestFailedRetryKeepsTheLabellingResult(t *testing.T) {
	retryOf := heldBy{class: classPrefixed, core: 900 * sec, turn: 2, at: noClip}
	tests := []struct {
		name    string
		setup   func(d *chunkDiarizer, asm *arithmeticAssembler)
		cause   string
		retries int
	}{
		{"503 on every send", func(d *chunkDiarizer, _ *arithmeticAssembler) {
			d.fail[retryOf] = slices.Repeat([]error{claierr.NewProviderUnavailable(&claierr.APIError{StatusCode: 503})}, 5)
		}, "provider unavailable (503)", 2},
		{"no answer at all", func(d *chunkDiarizer, _ *arithmeticAssembler) {
			d.fail[retryOf] = slices.Repeat([]error{claierr.NewTransport(errors.New("connection reset by peer"))}, 5)
		}, "connection failed: connection reset by peer", 2},
		{"a rejected key", func(d *chunkDiarizer, _ *arithmeticAssembler) {
			d.fail[retryOf] = []error{claierr.NewAuthFailed(&claierr.APIError{StatusCode: 401})}
		}, "authentication failed (401)", 2},
		{"the budget check", func(_ *chunkDiarizer, asm *arithmeticAssembler) {
			asm.refuse = func(req AssembleRequest) error {
				if req.Core.Start == 900*sec && slices.ContainsFunc(req.Samples, func(s SampleClip) bool { return s.Candidate }) {
					return errOverBytes
				}
				return nil
			}
		}, errOverBytes.Error(), 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			truth := twoInChunkOne()
			d := newChunkDiarizer(truth)
			d.rules = []voiceRule{coreHeardAs(450*sec, 1, "v3", "v3-core"), coreHeardAs(900*sec, 1, "v5", "v5-core")}
			schedule, _ := recordedSchedule()
			r := runSurvey(t, d, func(c *Coordinator, asm *arithmeticAssembler) {
				c.Retry = schedule
				tt.setup(d, asm)
			})
			if r.err != nil {
				t.Fatalf("a failed retry failed the run: %v", r.err)
			}
			unknownOnly(t, r.segs, truth, "v5")
			onePerVoice(t, r.segs, truth, "v1", "v2", "v3", "v4", "v6")
			line := "chunk 3 (15:00–22:30) second try failed: " + tt.cause + "; its labelling result is kept"
			if n := strings.Count(r.status, "second try failed"); n != 1 || !strings.Contains(r.status, "\n"+line+"\n") {
				t.Errorf("%v failure lines, want exactly %q:\n%s", n, line, r.status)
			}
			if state := r.c.board.Rows()[2].Cells[colState]; state != "1 unmatched · 6 speakers" {
				t.Errorf("chunk 3 reads %q, want its labelling result", state)
			}
			if strings.Contains(r.status, stateRequestFailed) {
				t.Errorf("a failed retry marked its row:\n%s", r.status)
			}
			assertRecords(t, r.log, fmt.Sprintf(`retry chunk=3 attempt=3 outcome=kept reason=request-failed cause=%s$`, regexp.QuoteMeta(fmt.Sprintf("%q", tt.cause))),
				`retry chunk=2 attempt=3 outcome=accepted `)
			if got := r.c.Totals().Retries; got != tt.retries {
				t.Errorf("%v retries counted, want %v", got, tt.retries)
			}
		})
	}
}

// the board, Q4
func TestBoardOfTheSurveyPath(t *testing.T) {
	r := runSurvey(t, retryEverything())
	if r.err != nil {
		t.Fatal(r.err)
	}
	out := r.status
	phases := []string{"finding voices · 4 chunks", "matching voices · 11 clips", "labelling · 4 chunks · 6 speakers", "second try · 1 chunks with unmatched voices", "stitching", "done · "}
	at := strings.Index(out, "▸ speaker labelling  meeting.wav  4 chunks × 10m0s  ≤18 requests")
	if at < 0 {
		t.Fatalf("no header in:\n%s", out)
	}
	for _, phase := range phases {
		next := strings.Index(out[at:], "phase  "+phase)
		if next < 0 {
			t.Fatalf("no phase %q after the ones before it:\n%s", phase, out)
		}
		at += next
	}
	for _, state := range []string{"finding voices · 7m30s", "2 voices found", "3 voices found", "labelling · ", "second try · ", "✓ labelled · 6 speakers"} {
		if !strings.Contains(out, state) {
			t.Errorf("no row state %q in:\n%s", state, out)
		}
	}
	for i, row := range r.c.board.Rows() {
		want := "2/3"
		if i == 2 {
			want = "3/3"
		}
		if row.Cells[colReq] != want {
			t.Errorf("row %v req %q, want %q", i+1, row.Cells[colReq], want)
		}
	}
	if !regexp.MustCompile(`(?m)^  requests 15/≤18 · resent 0 · retries 1 · cache hits 0 · uploaded \S+ · speakers 6 · unknown 0s · \S+ elapsed$`).MatchString(out) {
		t.Errorf("no final footer in:\n%s", out)
	}
	for _, old := range []string{"bootstrap", "calibrated", "discovery", "revisit", "workers", "reserve"} {
		if strings.Contains(out, old) {
			t.Errorf("status shows %q:\n%s", old, out)
		}
	}
	t.Run("no second try when no chunk needs one", func(t *testing.T) {
		clean := runSurvey(t, newChunkDiarizer(twoInChunkOne()))
		if clean.err != nil || strings.Contains(clean.status, "second try") {
			t.Errorf("err %v, status:\n%s", clean.err, clean.status)
		}
	})
	t.Run("json progress records", func(t *testing.T) {
		var stdout, stderr lockedWriter
		stdout.w, stderr.w = &strings.Builder{}, &strings.Builder{}
		c, _, _ := surveyFor(retryEverything())
		c.StatusOut = nil
		ctx := runenv.With(context.Background(), jsonRecordEnv(strings.NewReader(""), &stdout, &stderr))
		if _, err := c.Run(ctx, "meeting.wav"); err != nil {
			t.Fatal(err)
		}
		var details []string
		for _, rec := range transcribeProgress(t, stderr.String()) {
			if rec.Row == "" {
				details = append(details, rec.Detail)
			}
		}
		at := 0
		for _, phase := range phases {
			next := slices.IndexFunc(details[at:], func(d string) bool { return strings.HasPrefix(d, phase) })
			if next < 0 {
				t.Fatalf("no progress record %q in order in %q", phase, details)
			}
			at += next
		}
	})
}

// Run.6, Run.11, Run.12, Merge.9, I6
func TestNothingToLabel(t *testing.T) {
	var truth []voiceSpan
	for chunk := range 4 {
		for i := range 5 {
			start := float64(chunk)*450 + 10 + float64(i)*80
			truth = append(truth, voiceSpan{start, start + 2, fmt.Sprintf("n%d", i+1)})
		}
	}
	d := newChunkDiarizer(truth)
	r := runSurvey(t, d)
	if r.err != nil {
		t.Fatalf("nothing to label failed the run: %v", r.err)
	}
	tot := r.c.Totals()
	if tot.Requests != 4 || tot.CacheHits != 4 || len(d.requests()) != 4 || d.count("merge") != 0 {
		t.Errorf("totals %+v, %v requests seen; want each chunk uploaded once and its labelling answered by the cache", tot, len(d.requests()))
	}
	for _, s := range r.segs {
		if !strings.HasPrefix(s.Speaker, unknownPrefix) {
			t.Errorf("segment %+v carries a survey label", s)
		}
	}
	if speechOf(r.segs) != truthSpeech(truth) {
		t.Errorf("speech %v, want %v", speechOf(r.segs), truthSpeech(truth))
	}
}

// L2.11: speech with no speaker is no label for the candidates, the retry
// trigger and the row's unmatched count; it is kept as unknown-N.
func TestSpeechWithoutASpeakerIsNoLabel(t *testing.T) {
	truth := twoInChunkOne()
	d := newChunkDiarizer(truth)
	d.rules = []voiceRule{
		{when: heldBy{class: classSurvey, core: 450 * sec, at: noClip}, voice: "v1", as: ""},
		coreHeardAs(900*sec, 1, "v5", ""),
	}
	r := runSurvey(t, d)
	if r.err != nil {
		t.Fatal(r.err)
	}
	if strings.Contains(r.status, "second try") || strings.Contains(r.log, "calibration retry ") || len(requestsOf(d, 900*sec)) != 2 {
		t.Errorf("speech with no speaker made chunk 3 due:\n%s", r.status)
	}
	if regexp.MustCompile(`(?m)calibration candidate chunk=\d attempt=1 label= `).MatchString(r.log) || !strings.Contains(r.status, "matching voices · 10 clips") {
		t.Errorf("speech with no speaker gave a candidate:\n%s", r.status)
	}
	row := r.c.board.Rows()[2]
	if row.Cells[colState] != "labelled · 6 speakers" || row.Cells[colUnresolved] != "0" || row.Cells[colUnknown] != "2m0s" {
		t.Errorf("chunk 3 row %q, want labelled with v5's speech unknown and nothing unmatched", row.Cells)
	}
	unknownOnly(t, r.segs, slices.DeleteFunc(slices.Clone(truth), func(v voiceSpan) bool { return v.start < 900 || v.start >= 1350 }), "v5")
	if speechOf(r.segs) != truthSpeech(truth) {
		t.Errorf("speech %v, want %v", speechOf(r.segs), truthSpeech(truth))
	}
}

// Run.17, I1, I9
func TestSurveyPathIsTheOnlyPath(t *testing.T) {
	run := func(t *testing.T, tweak func(*Coordinator, *arithmeticAssembler)) (surveyRun, []string) {
		t.Helper()
		d := newChunkDiarizer(twoInChunkOne())
		r := runSurvey(t, d, tweak)
		if r.err != nil {
			t.Fatal(r.err)
		}
		var seen []string
		for _, q := range d.requests() {
			seen = append(seen, fmt.Sprint(q.class, q.core, q.samples))
		}
		slices.Sort(seen)
		return r, seen
	}
	plain, plainSeen := run(t, func(*Coordinator, *arithmeticAssembler) {})
	t.Setenv("CLAI_CONFIG_DIR", filepath.Join(t.TempDir(), ".clai"))
	dir, err := utils.GetClaiConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	library := voices.NewLibrary(dir, time.Now)
	inspection := voices.Inspection{FLAC: []byte("fLaC Ada"), Seconds: 8, SpeechShare: 1, MeanDBFS: -20}
	if _, err := library.Enrol(voices.Enrolment{Name: "Ada", Inspection: inspection, Status: voices.Confirmed, Source: voices.SourceAdd}); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(dir, voices.Dir)
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o700) })
	other, otherSeen := run(t, func(c *Coordinator, _ *arithmeticAssembler) {
	})
	if !slices.Equal(plainSeen, otherSeen) || !slices.Equal(plain.segs, other.segs) || plain.c.Totals() != other.c.Totals() {
		t.Errorf("a setting or a voice library changed the run: %+v, want %+v", other.c.Totals(), plain.c.Totals())
	}
}
