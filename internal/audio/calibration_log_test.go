package audio

import (
	"bytes"
	"context"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/audio/voices"
	"github.com/baalimago/clai/internal/testenv/procenv"
	"github.com/baalimago/clai/pkg/diagnostic"
)

// calibrationLog runs the fake calibrated pipeline at the audio debug level.
func calibrationLog(t *testing.T, p *voiceProvider, tweaks ...func(*Coordinator)) (string, []Segment, Totals) {
	t.Helper()
	var out bytes.Buffer
	c, _, _ := newCoordinator(p)
	for _, tweak := range tweaks {
		tweak(c)
	}
	segs, err := c.Run(procenv.Diagnostics(context.Background(), &out, diagnostic.LevelDebug), "meeting.wav")
	if err != nil {
		t.Fatal(err)
	}
	return out.String(), segs, c.Totals()
}

func assertRecords(t *testing.T, log string, patterns ...string) {
	t.Helper()
	for _, pattern := range patterns {
		if !regexp.MustCompile(`(?m)^debug \[audio\] calibration ` + pattern).MatchString(log) {
			t.Errorf("no record matches %q in:\n%s", pattern, log)
		}
	}
}

// The event log: one record per row of the event table, no transcript text,
// and the same run with and without it.
func TestCalibrationLogOfTheSurveyPath(t *testing.T) {
	truth := twoInChunkOne()
	joined := func() *chunkDiarizer {
		d := newChunkDiarizer(truth)
		d.rules = []voiceRule{splitInMerge(clipAt(truth, "v1", 1), "v1", "v1-b")}
		return d
	}
	tests := []struct {
		name    string
		script  func() *chunkDiarizer
		records []string
	}{
		{"a join", joined, []string{
			`plan source=1800\.0 max_request_seconds=700\.0 max_request_bytes=\d+ voices=8 prefix_reserve=88\.0 core_target=599\.8 silence_window=\d+\.\d silences=0 cores=4 parallelism=all requests_max=18$`,
			`plan-core core=2 start=450\.0 planned_end=900\.0 end=900\.0 align=no-silence$`,
			`request chunk=1 attempt=1 kind=survey core=0\.0-450\.0 seconds=450\.0 bytes=\d+ cache=miss samples=0 prefix=-$`,
			`response chunk=1 attempt=1 segments=24 labels=2 samples=0 mapped=0 mixed=0 missing=0 ambiguous=0 collapsed=0 speech=360\.0 unmapped=360\.0 took=\d+\.\d$`,
			`candidate chunk=1 attempt=1 label=\w id=c1-\w clip=0\.0-10\.0 speech=10\.0 clearance=3\.0 label_speech=180\.0$`,
			`request link=2 kind=merge-forward core=0\.0-0\.0 seconds=\d+\.\d bytes=\d+ cache=miss samples=5 prefix=voice-1@`,
			`request link=4 kind=merge-reversed core=0\.0-0\.0 `,
			`merge link=2 clip=c2-\w source=450\.0-460\.0 forward=\w:1\.00 reversed=\w:1\.00 outcome=new voice=voice-3$`,
			`merge link=3 clip=voice-1 source=0\.0-10\.0 forward=\w:1\.00 reversed=\w:1\.00 outcome=held voice=voice-1$`,
			`merge link=3 clip=c3-\w source=936\.0-946\.0 forward=\w:1\.00 reversed=\w:1\.00 outcome=joined voice=voice-1$`,
			`merge voice=voice-1 speaker=speaker-1 clips=c1-\w\+c3-\w\+c4-\w sample=0\.0-10\.0 survey_speech=420\.0$`,
			`merge state=done links=3 found=7 kept=7$`,
			`request chunk=3 attempt=2 kind=labelling core=900\.0-1350\.0 seconds=527\.0 bytes=\d+ cache=miss samples=7 prefix=speaker-1@-0\.2-10\.2>0\.0-10\.5,speaker-2@`,
			`sample chunk=3 attempt=2 id=speaker-3 label=\w share=1\.00 labelled=10\.\d verdict=ambiguous$`,
			`join from=speaker-3 into=speaker-1 shared=4 apart=0$`,
			`join state=done joined=1 speakers=6$`,
			`registry id=speaker-1 was=speaker-1\+speaker-3 clips=4 sample=0\.0-10\.0$`,
			`registry id=speaker-6 was=speaker-7 clips=1 sample=1350\.0-1360\.0$`,
			`accepted chunk=3 attempt=2 requests=2 resent=0 retries=0 mapped=360\.0 unknown=0\.0 material_unmapped=0 map=(\w>speaker-\d,){5}\w>speaker-\d$`,
			`stitched segments=\d+ speakers=6 unknown_voices=0 unknown_runs=0 unknown_short_voices=0 unknown=0\.0 requests=14 resent=0 retries=0 cache_hits=0 uploaded=\d+\.\d$`,
		}},
		{"a retry", retryEverything, []string{
			`label chunk=3 attempt=2 label=\w speech=120\.0 material=true global=- reason=no-sample$`,
			`retry chunk=3 attempt=2 state=start reversed=true swapped=speaker-3@468\.0-478\.0>1386\.0-1396\.0 candidates=\w:918\.0-928\.0 pending=\w:120\.0$`,
			`request chunk=3 attempt=3 kind=retry core=900\.0-1350\.0 .* samples=7 prefix=speaker-6@1349\.8-1360\.2>0\.0-10\.5,`,
			`sample chunk=3 attempt=3 id=cand-\w label=\w share=1\.00 labelled=10\.\d verdict=collapsed onto=speaker-5$`,
			`retry chunk=3 attempt=3 outcome=accepted mapped=240\.0>360\.0 unknown=120\.0>0\.0 material_unmapped=1>0$`,
			`accepted chunk=3 attempt=3 requests=3 resent=0 retries=1 mapped=360\.0 unknown=0\.0 material_unmapped=0 `,
			`join state=done joined=0 speakers=6$`,
			`stitched segments=\d+ speakers=6 unknown_voices=0 .* requests=15 resent=0 retries=1 `,
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := runSurvey(t, tt.script())
			if r.err != nil {
				t.Fatal(r.err)
			}
			assertRecords(t, r.log, tt.records...)
			for _, old := range []string{" bootstrap", " discovery", " revisit", " recovery", "op="} {
				if strings.Contains(r.log, old) {
					t.Errorf("log writes %q", old)
				}
			}
			if regexp.MustCompile(`\bv[1-6]\b|\bmix\b`).MatchString(r.log) {
				t.Errorf("log carries transcript text:\n%s", r.log)
			}
			for line := range strings.Lines(r.log) {
				if len(line) > 700 {
					t.Errorf("record of %v bytes: %s", len(line), line)
				}
			}
			c, _, _ := surveyFor(tt.script())
			quiet, err := c.Run(context.Background(), "meeting.wav")
			if err != nil || !slices.Equal(quiet, r.segs) || c.Totals() != r.c.Totals() {
				t.Errorf("the log changed the run: %v, totals %+v, want %+v", err, c.Totals(), r.c.Totals())
			}
		})
	}
}

func TestNamingLogSaysWhatBecameOfEachSpeaker(t *testing.T) {
	seg := func(start, end float64, speaker string) Segment {
		return Segment{Start: time.Duration(start * float64(time.Second)), End: time.Duration(end * float64(time.Second)), Speaker: speaker, Text: "confidential"}
	}
	segs := []Segment{seg(0, 8, "speaker-1"), seg(9, 14, "speaker-2"), seg(15, 16, "speaker-3"), seg(17, 18, "unknown-1")}
	report := NamingReport{
		Status: NamingNamed, Origin: voices.OriginFlag,
		Speakers: []SpeakerNaming{
			{Label: "speaker-1", Name: "Ada", Voice: &Provenance{Status: voices.Confirmed}},
			{Label: "speaker-2", Reason: ReasonDisagreed},
			{Label: "speaker-3", Reason: ReasonNoCandidate},
			{Label: "unknown-1", Reason: ReasonNoCandidate},
		},
		Voices: []VoiceNaming{{Name: "Ada", Result: VoiceNamed}, {Name: "Bo", Result: VoiceAbsent}},
	}
	var out bytes.Buffer
	logNaming(procenv.Diagnostics(context.Background(), &out, diagnostic.LevelDebug), segs, report)
	want := `debug [audio] naming status=named origin=flag speakers=4 voices=2
debug [audio] naming-speaker label=speaker-1 clip=0.0-8.0 speech=8.0 outcome=named name="Ada" voice_status=confirmed
debug [audio] naming-speaker label=speaker-2 clip=9.0-14.0 speech=5.0 outcome=kept reason=disagreed
debug [audio] naming-speaker clip=- outcome=kept reason=no-candidate speakers=2 unknown_voices=1 labels=speaker-3
debug [audio] naming-voice name="Ada" result=named
debug [audio] naming-voice name="Bo" result=absent
`
	if out.String() != want {
		t.Errorf("naming log:\n%s\nwant:\n%s", out.String(), want)
	}
	out.Reset()
	report = NamingReport{Status: NamingRequestFailed, Err: &NamingRequestError{Order: OrderForward, Stage: StageSend, Err: context.DeadlineExceeded}}
	logNaming(procenv.Diagnostics(context.Background(), &out, diagnostic.LevelDebug), nil, report)
	if !strings.Contains(out.String(), `naming status=request-failed origin= speakers=0 voices=0 error="naming request (forward order) failed to send: context deadline exceeded"`) {
		t.Errorf("failed naming log:\n%s", out.String())
	}
}
