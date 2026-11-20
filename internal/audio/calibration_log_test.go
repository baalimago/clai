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

func TestCalibrationLogExplainsUnmappedLabel(t *testing.T) {
	log, _, _ := calibrationLog(t, &voiceProvider{truth: sixVoiceMeeting(), mixCandidateVoice: "v5"})
	assertRecords(t, log,
		`plan source=1800\.0 max_request_seconds=700\.0 max_request_bytes=\d+ max_speakers=8 prefix_reserve=88\.0 core_target=\d+\.\d silence_window=\d+\.\d silences=0 cores=4 workers=1 request_limit=4$`,
		`plan-core core=2 start=450\.0 planned_end=900\.0 end=900\.0 align=no-silence$`,
		`request chunk=1 attempt=1 kind=bootstrap core=0\.0-450\.0 seconds=450\.0 bytes=\d+ cache=miss samples=0 prefix=-$`,
		`request chunk=1 attempt=2 kind=bootstrap-candidates core=0\.0-450\.0 seconds=483\.0 bytes=\d+ cache=miss samples=3 prefix=cand-0-\w@\S+>0\.0-10\.5,cand-1-\w@17\.8-28\.2>11\.0-21\.5,cand-2-\w@35\.8-46\.2>22\.0-32\.5$`,
		`response chunk=1 attempt=2 segments=\d+ labels=3 samples=3 mapped=3 mixed=0 missing=0 ambiguous=0 collapsed=0 speech=\d+\.\d unmapped=0\.0$`,
		`sample chunk=3 attempt=1 id=speaker-1 label=\w share=1\.00 labelled=\d+\.\d verdict=mapped$`,
		`sample chunk=3 attempt=2 id=cand-0-\w label=\w share=0\.50 labelled=\d+\.\d verdict=mixed$`,
		`label chunk=3 attempt=1 label=\w speech=120\.0 material=true global=- reason=no-sample$`,
		`label chunk=3 attempt=1 label=\w speech=120\.0 material=true global=speaker-2$`,
		`candidate chunk=1 attempt=1 label=\w id=cand-0-\w clip=\d+\.\d-\d+\.\d speech=10\.0 clearance=\d+\.\d$`,
		`registry chunk=1 attempt=2 op=add id=speaker-1 label=\w sample=\d+\.\d-\d+\.\d version=1$`,
		`discovery chunk=3 attempt=1 iteration=1 state=start pending=\w:120\.0 candidates=1 speakers=4 version=4$`,
		`discovery chunk=3 attempt=2 iteration=1 state=done promoted=- rejected=\w:mixed pending=\w:120\.0$`,
		`discovery chunk=3 attempt=3 state=stop reason=iterations-exhausted pending=\w:120\.0$`,
		`discovery chunk=2 attempt=2 state=stop reason=resolved$`,
		`accepted chunk=3 attempt=3 discovery_attempts=2 requests=3 retries=0 mapped=240\.0 unknown=120\.0 material_unmapped=1 map=\w>speaker-1,\w>speaker-2,\w>speaker-3,\w>speaker-4$`,
		`unmapped chunk=3 attempt=3 label=\w speech=120\.0 material=true reason=no-sample runs=8$`,
		`unknown chunk=3 names=unknown-1\.\.unknown-8 runs=8 short_runs=0 speech=120\.0$`,
		`stitched segments=\d+ speakers=5 unknown_runs=8 unknown_short_runs=0 unknown=120\.0 requests=\d+ retries=0 cache_hits=0 uploaded=\d+\.\d$`,
	)
}

func TestCalibrationLogNamesTheSampleThatBlocksDiscovery(t *testing.T) {
	// Requests 3 and 4 are chunk 2's pass and its replacement retry.
	p := &voiceProvider{truth: sixVoiceMeeting(), mixSample: map[int]string{3: "speaker-1", 4: "speaker-1"}}
	log, _, _ := calibrationLog(t, p)
	assertRecords(t, log,
		`sample chunk=2 attempt=1 id=speaker-1 label=\w+ share=0\.50 labelled=\d+\.\d verdict=mixed$`,
		`recovery chunk=2 attempt=1 trigger=failed-sample samples=speaker-1:mixed$`,
		`registry chunk=2 attempt=1 op=replace id=speaker-1 cause=mixed old=\d+\.\d-\d+\.\d new=\d+\.\d-\d+\.\d replacements=1 version=4$`,
		`request chunk=2 attempt=2 kind=replacement-retry .* samples=3 prefix=speaker-1@`,
		`discovery chunk=2 attempt=2 state=skipped reason=failed-registry-sample failed=speaker-1:mixed pending=\w+:\d+\.\d`,
		`unmapped chunk=2 attempt=2 label=\w+ speech=\d+\.\d material=true reason=no-sample runs=\d+$`,
	)
}

func TestCalibrationLogRecordsRegistryRefusals(t *testing.T) {
	everyRequest := func(value string) map[int]string {
		out := map[int]string{}
		for n := 3; n <= 16; n++ {
			out[n] = value
		}
		return out
	}
	tests := []struct {
		name    string
		p       *voiceProvider
		tweak   func(*Coordinator)
		records []string
	}{
		{
			"a full registry refuses the next voice",
			&voiceProvider{truth: sixVoiceMeeting()},
			func(c *Coordinator) { c.Budgets.MaxSpeakers = 3 },
			[]string{
				`registry chunk=2 attempt=2 op=refuse-add label=\w reason=prefix-reserve-exhausted version=3$`,
				`unmapped chunk=2 attempt=3 label=\w speech=\d+\.\d material=true reason=prefix-reserve-exhausted runs=\d+$`,
			},
		},
		{
			"a sample that keeps failing is frozen",
			&voiceProvider{truth: sixVoiceMeeting(), mixSample: everyRequest("speaker-1")},
			func(*Coordinator) {},
			[]string{
				`registry chunk=3 attempt=1 op=replace id=speaker-1 cause=mixed old=\S+ new=\S+ replacements=2 version=5$`,
				`registry chunk=4 attempt=1 op=freeze id=speaker-1 cause=mixed sample=\S+ replacements=2 verified_ranges=\d+ clip_found=\w+ version=6$`,
			},
		},
		{
			"a repeated ambiguity replaces nothing",
			&voiceProvider{truth: sixVoiceMeeting(), mergeVoices: map[int][2]string{3: {"v1", "v2"}, 4: {"v1", "v2"}}},
			func(*Coordinator) {},
			[]string{
				`sample chunk=2 attempt=1 id=speaker-1 label=\w share=1\.00 labelled=\d+\.\d verdict=ambiguous$`,
				`recovery chunk=2 attempt=1 trigger=ambiguous samples=speaker-1:ambiguous,speaker-2:ambiguous$`,
				`request chunk=2 attempt=2 kind=reversed-retry .* prefix=speaker-3@`,
				`recovery chunk=2 attempt=2 outcome=still-ambiguous samples=speaker-2:ambiguous,speaker-1:ambiguous$`,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log, _, _ := calibrationLog(t, tt.p, tt.tweak)
			assertRecords(t, log, tt.records...)
		})
	}
}

func TestCalibrationLogSaysWhenNothingNeedsDiscovery(t *testing.T) {
	var truth []voiceSpan
	for at := 0.0; at < 1800; at += 30 {
		truth = append(truth, voiceSpan{at, at + 12, "v1"}, voiceSpan{at + 15, at + 27, "v2"})
	}
	log, _, _ := calibrationLog(t, &voiceProvider{truth: truth})
	assertRecords(t, log,
		`discovery chunk=3 attempt=1 state=skipped reason=no-material-unmapped$`,
		`accepted chunk=3 attempt=1 discovery_attempts=0 requests=1 retries=0 mapped=360\.0 unknown=0\.0 material_unmapped=0 map=\w>speaker-1,\w>speaker-2$`,
		`stitched segments=120 speakers=2 unknown_runs=0 unknown_short_runs=0 unknown=0\.0 requests=5 retries=0 cache_hits=0 uploaded=\d+\.\d$`,
	)
}

func TestCalibrationLogChangesNothingAndCarriesNoText(t *testing.T) {
	quiet := &voiceProvider{truth: sixVoiceMeeting(), mixCandidateVoice: "v5"}
	c, _, _ := newCoordinator(quiet)
	want, err := c.Run(context.Background(), "meeting.wav")
	if err != nil {
		t.Fatal(err)
	}
	logged := &voiceProvider{truth: sixVoiceMeeting(), mixCandidateVoice: "v5"}
	log, got, totals := calibrationLog(t, logged)
	if !slices.Equal(got, want) || totals != c.Totals() || !slices.EqualFunc(logged.calls, quiet.calls, func(a, b AssembleRequest) bool {
		return a.Core == b.Core && slices.Equal(a.Samples, b.Samples)
	}) {
		t.Errorf("debug logging changed the run: totals %+v, want %+v", totals, c.Totals())
	}
	// The fake provider's segment text is its voice name.
	if regexp.MustCompile(`\bv[1-6]\b`).MatchString(log) {
		t.Errorf("log carries transcript text:\n%s", log)
	}
	for line := range strings.Lines(log) {
		if len(line) > 700 {
			t.Errorf("record of %v bytes: %s", len(line), line)
		}
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
debug [audio] naming-speaker clip=- outcome=kept reason=no-candidate speakers=2 unknown_runs=1 labels=speaker-3
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
