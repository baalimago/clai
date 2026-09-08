package audio

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	pkgtools "github.com/baalimago/clai/pkg/tools"
)

// smallMeeting: 180 s source, four 45 s cores under an 80 s budget with
// max-speakers 3 (reserve 33 s). chunk 1: a b; chunk 2: a c; chunk 3: b c a; chunk 4: d a.
func smallMeeting() []voiceSpan {
	var spans []voiceSpan
	pattern := [][]string{{"a", "b"}, {"a", "c"}, {"b", "c", "a"}, {"d", "a"}}
	for chunk, voices := range pattern {
		base := float64(chunk) * 45
		for t := 0.0; t < 42; t += 21 {
			for i, v := range voices {
				start := base + t + float64(i)*7
				spans = append(spans, voiceSpan{start, start + 5, v})
			}
		}
	}
	return spans
}

func smallBudgetConf(model string) Configurations {
	conf := Default
	conf.Transcribe.Model = model
	conf.Transcribe.MaxRequestBytes = 1 << 20
	conf.Transcribe.MaxRequestSeconds = 80
	conf.Transcribe.MaxSpeakers = 3
	conf.Transcribe.Parallelism = 1
	return conf
}

// newCalibratedSplitter wires the real Splitter and real assembler with the
// scripted runner and the scripted diarizer, entered through Transcribe.
func newCalibratedSplitter(t *testing.T, p *voiceProvider, conf Configurations) (*Splitter, *bytes.Buffer, string) {
	t.Helper()
	budgets, err := ResolveBudgets(conf.Transcribe)
	if err != nil {
		t.Fatal(err)
	}
	status := &bytes.Buffer{}
	s := NewSplitter(&fakeTranscriber{}, &scriptedRunner{duration: "180.000000"})
	s.Model = conf.Transcribe.Model
	s.Budgets = budgets
	s.MaxBytes = budgets.MaxRequestBytes
	s.Strict = conf.Transcribe.StrictSpeakers
	s.Parallelism = conf.Transcribe.Parallelism
	s.RequestTranscriber = manifestTranscriber{p}
	s.StatusOut = status
	return s, status, sparseFile(t, 2<<20)
}

func TestCalibrationEndToEndWithPermutedLabels(t *testing.T) {
	p := &voiceProvider{truth: smallMeeting()}
	s, status, input := newCalibratedSplitter(t, p, smallBudgetConf("gpt-4o-transcribe-diarize"))
	segs, err := s.Transcribe(context.Background(), input)
	if err != nil {
		t.Fatalf("transcribe failed: %v", err)
	}
	ids := idsByVoice(t, segs, p.truth)
	seen := map[string]string{}
	for _, v := range []string{"a", "b", "c"} {
		if len(ids[v]) != 1 {
			t.Errorf("voice %v maps to %v, expected one stable ID", v, ids[v])
			continue
		}
		for id := range ids[v] {
			if other, dup := seen[id]; dup {
				t.Errorf("voices %v and %v merged onto %v", other, v, id)
			}
			seen[id] = v
		}
	}
	// max-speakers 3: the fourth voice is refused and rendered unknown
	for id := range ids["d"] {
		if !strings.HasPrefix(id, "unknown-") {
			t.Errorf("fourth voice must be unknown under max-speakers 3, got %v", id)
		}
	}
	if !strings.Contains(status.String(), reasonReserveExhausted) && !strings.Contains(status.String(), "unknown") {
		t.Errorf("expected the refusal reported on stderr, got:\n%v", status.String())
	}
	if len(segs) != len(p.truth) {
		t.Errorf("expected %v segments, got %v", len(p.truth), len(segs))
	}
	if !strings.Contains(status.String(), "calibrated diarization") {
		t.Errorf("expected calibration status line, got:\n%v", status.String())
	}
}

func TestCalibrationEndToEndDiscoversLateSpeakers(t *testing.T) {
	p := &voiceProvider{truth: smallMeeting()}
	conf := smallBudgetConf("gpt-4o-transcribe-diarize")
	conf.Transcribe.MaxSpeakers = 4
	s, _, input := newCalibratedSplitter(t, p, conf)
	segs, err := s.Transcribe(context.Background(), input)
	if err != nil {
		t.Fatalf("transcribe failed: %v", err)
	}
	ids := idsByVoice(t, segs, p.truth)
	for _, v := range []string{"a", "b", "c", "d"} {
		if len(ids[v]) != 1 {
			t.Errorf("voice %v maps to %v, expected one stable ID", v, ids[v])
		}
		for id := range ids[v] {
			if !strings.HasPrefix(id, "speaker-") {
				t.Errorf("voice %v should be a global speaker, got %v", v, id)
			}
		}
	}
	for i := 1; i < len(segs); i++ {
		if segs[i].Start < segs[i-1].Start {
			t.Fatalf("output not in source order at %v", i)
		}
	}
}

func TestCalibrationPreservesNonDiarizedAndSubLimitPaths(t *testing.T) {
	t.Run("sub-limit diarized file is one plain request", func(t *testing.T) {
		trans := &fakeTranscriber{}
		s := NewSplitter(trans, &scriptedRunner{})
		s.Model = "gpt-4o-transcribe-diarize"
		s.MaxBytes = 4 << 20
		if _, err := s.Transcribe(context.Background(), sparseFile(t, 1<<20)); err != nil {
			t.Fatal(err)
		}
		if trans.callCount() != 1 {
			t.Errorf("expected one direct request, got %v", trans.calls)
		}
	})
	t.Run("oversized non-diarized keeps the segment path", func(t *testing.T) {
		splitter, trans, runner, _, input := newOversizedSplitter(t)
		splitter.Model = "whisper-1"
		splitter.Budgets = defaultBudgets()
		got, err := splitter.Transcribe(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 3 || trans.callCount() != 3 {
			t.Errorf("expected 3 chunk transcriptions, got %v/%v", len(got), trans.callCount())
		}
		for _, c := range runner.calls {
			if strings.Contains(strings.Join(c, " "), "silencedetect") {
				t.Errorf("non-diarized path must not plan calibration: %v", c)
			}
		}
	})
}

func TestMaxRequestBytesAppliesToBothPaths(t *testing.T) {
	// 60 MB input: with a 40 MB cap the non-diarized path splits into ceil(60/32)=2 chunks
	trans := &fakeTranscriber{}
	runner := &fakeRunner{defaultDur: "900.000000", durations: map[string]string{"input.wav": "1800.000000"}, chunkCount: 2}
	s := NewSplitter(trans, runner)
	s.StatusOut = &bytes.Buffer{}
	s.Model = "whisper-1"
	s.MaxBytes = 40 << 20
	if _, err := s.Transcribe(context.Background(), sparseFile(t, 60<<20)); err != nil {
		t.Fatal(err)
	}
	segmentTime := ""
	for _, c := range runner.calls {
		if i := indexOf(c, "-segment_time"); i >= 0 {
			segmentTime = c[i+1]
		}
	}
	if segmentTime != "900.000" {
		t.Errorf("expected 2 chunks of 900 s under a 40 MB cap (80%% target), got segment time %q", segmentTime)
	}
	// Same cap makes a 30 MB diarized file calibrate instead of passing through
	p := &voiceProvider{truth: smallMeeting()}
	conf := smallBudgetConf("gpt-4o-transcribe-diarize")
	conf.Transcribe.MaxRequestBytes = 40 << 20
	cs, _, _ := newCalibratedSplitter(t, p, conf)
	direct := &fakeTranscriber{}
	cs.Transcriber = direct
	if _, err := cs.Transcribe(context.Background(), sparseFile(t, 30<<20)); err != nil {
		t.Fatal(err)
	}
	if direct.callCount() != 1 || p.requests() != 0 {
		t.Errorf("30 MB under a 40 MB cap must be one direct request, got direct %v calibrated %v", direct.callCount(), p.requests())
	}
	if _, err := cs.Transcribe(context.Background(), sparseFile(t, 50<<20)); err != nil {
		t.Fatal(err)
	}
	if p.requests() == 0 {
		t.Error("50 MB over a 40 MB cap must calibrate")
	}
}

func TestCalibrationToolBridgeUsesSameEngine(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	conf := smallBudgetConf("gpt-4o-transcribe-diarize")
	conf.Transcribe.StrictSpeakers = true
	s, err := createSplitter(conf)
	if err != nil {
		t.Fatal(err)
	}
	if s.MaxBytes != 1<<20 || s.Budgets.MaxRequestDuration != 80*time.Second || s.Budgets.MaxSpeakers != 3 || !s.Strict || s.Parallelism != 1 {
		t.Errorf("splitter not configured from config: %+v", s)
	}
	if _, ok := s.Transcriber.(interface{ Endpoint() string }); !ok {
		t.Error("vendor transcriber must expose its endpoint for the cache key")
	}
	if pkgtools.AudioTranscribeEngine == nil {
		t.Fatal("tool engine not wired")
	}
	// The tool bridge and the CLI share createSplitter; a config error surfaces the same way in both
	conf.Transcribe.MaxSpeakers = -1
	if _, err := createSplitter(conf); err == nil || !strings.Contains(err.Error(), "max-speakers") {
		t.Errorf("expected budget error through the shared engine, got %v", err)
	}
}

func TestFlagOverridesBudgetAndStrict(t *testing.T) {
	conf := Default
	f := &Flags{}
	for _, sv := range []struct {
		v   interface{ Set(string) error }
		val string
	}{{&f.MaxRequestBytes, "1048576"}, {&f.MaxRequestSeconds, "600"}, {&f.MaxSpeakers, "4"}, {&f.StrictSpeakers, "true"}} {
		if err := sv.v.Set(sv.val); err != nil {
			t.Fatal(err)
		}
	}
	if err := ApplyFlagOverrides(&conf, f); err != nil {
		t.Fatal(err)
	}
	tc := conf.Transcribe
	if tc.MaxRequestBytes != 1<<20 || tc.MaxRequestSeconds != 600 || tc.MaxSpeakers != 4 || !tc.StrictSpeakers {
		t.Errorf("flags did not override config: %+v", tc)
	}
	untouched := Default
	if err := ApplyFlagOverrides(&untouched, &Flags{}); err != nil || untouched != Default {
		t.Errorf("unset flags must leave the file config: %+v, %v", untouched, err)
	}
}

func TestFlagRejectsNegativeBudget(t *testing.T) {
	for _, name := range []string{"max-request-bytes", "max-request-seconds", "max-speakers"} {
		f := &Flags{}
		var target *interface{ Set(string) error }
		switch name {
		case "max-request-bytes":
			v := interface{ Set(string) error }(&f.MaxRequestBytes)
			target = &v
		case "max-request-seconds":
			v := interface{ Set(string) error }(&f.MaxRequestSeconds)
			target = &v
		default:
			v := interface{ Set(string) error }(&f.MaxSpeakers)
			target = &v
		}
		if err := (*target).Set("-5"); err != nil {
			t.Fatal(err)
		}
		conf := Default
		err := ApplyFlagOverrides(&conf, f)
		if err == nil || !strings.Contains(err.Error(), "-"+name) || !strings.Contains(err.Error(), "transcribe."+name) {
			t.Errorf("expected error naming flag and field for %v, got %v", name, err)
		}
	}
}

func TestCalibrationStrictFailureHasNoStdout(t *testing.T) {
	p := &voiceProvider{truth: smallMeeting()} // max-speakers 3 leaves voice d unresolved
	conf := smallBudgetConf("gpt-4o-transcribe-diarize")
	conf.Transcribe.StrictSpeakers = true
	s, _, input := newCalibratedSplitter(t, p, conf)
	out := &strings.Builder{}
	q := &TranscribeQuerier{Splitter: s, FilePath: input, Format: FormatText, Out: out}
	err := q.Query(context.Background())
	var ue *UnresolvedError
	if !errors.As(err, &ue) {
		t.Fatalf("expected UnresolvedError, got %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("strict failure must write nothing to stdout, got %q", out.String())
	}
}

func TestToolBridgePropagatesCalibrationError(t *testing.T) {
	prev := pkgtools.AudioTranscribeEngine
	t.Cleanup(func() { pkgtools.AudioTranscribeEngine = prev })
	pkgtools.AudioTranscribeEngine = func(filePath, outputFormat string) (string, error) {
		return "", &UnresolvedError{Chunk: 1, Labels: []UnmappedLabel{{Label: "Q", Speech: 5 * time.Second, Reason: reasonNoSample}}}
	}
	got, err := pkgtools.AudioTranscribe.Call(map[string]any{"file_path": "meeting.wav"})
	if err == nil || got != "" || !strings.Contains(err.Error(), "strict-speakers") {
		t.Errorf("expected the calibration error returned by the tool, got %q, %v", got, err)
	}
}
