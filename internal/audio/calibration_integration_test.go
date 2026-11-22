package audio

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/audio/generic"
	"github.com/baalimago/clai/internal/remote"
	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/internal/stdio"
	"github.com/baalimago/clai/internal/utils"
	pkgtools "github.com/baalimago/clai/pkg/tools"
	"github.com/baalimago/go_away_boilerplate/pkg/cmd"
)

// smallMeeting: 180 s source in four 45 s parts. chunk 1: a b; chunk 2: a c;
// chunk 3: b c a; chunk 4: d a.
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
	s := NewSplitter(&fakeTranscriber{diarization: generic.DiarizationAnchored}, &scriptedRunner{duration: "180.000000"})
	s.Model = conf.Transcribe.Model
	s.Budgets = budgets
	s.MaxBytes = budgets.MaxRequestBytes
	s.Parallelism = conf.Transcribe.Parallelism
	s.RequestTranscriber = manifestTranscriber{p}
	s.StatusOut = status
	return s, status, sparseFile(t, 2<<20)
}

// Run.1, Run.4, I11: the Splitter, the real assembler and planner over the
// scripted runner; a 134 s budget holds the 88 s reserve and plans four
// cores of 45 s from the 180 s source.
func TestSurveyPathEndToEnd(t *testing.T) {
	t.Parallel()
	p := &voiceProvider{truth: smallMeeting()}
	conf := smallBudgetConf("gpt-4o-transcribe-diarize")
	conf.Transcribe.MaxRequestSeconds = 134
	s, status, input := newCalibratedSplitter(t, p, conf)
	segs, err := s.Transcribe(context.Background(), input)
	if err != nil {
		t.Fatalf("transcribe failed: %v", err)
	}
	if !strings.Contains(status.String(), "▸ speaker labelling  ") || !strings.Contains(status.String(), "  4 chunks × 45s  ≤18 requests") {
		t.Fatalf("expected four chunks on the speaker labelling board, got:\n%v", status.String())
	}
	ids := idsByVoice(t, segs, p.truth)
	seen := map[string]string{}
	for _, v := range []string{"a", "b", "c", "d"} {
		if len(ids[v]) != 1 {
			t.Errorf("voice %v maps to %v, expected one speaker", v, ids[v])
			continue
		}
		for id := range ids[v] {
			if other, dup := seen[id]; dup || !strings.HasPrefix(id, "speaker-") {
				t.Errorf("voice %v is %v (also %v), want a speaker of its own", v, id, other)
			}
			seen[id] = v
		}
	}
	if len(segs) != len(p.truth) {
		t.Errorf("expected %v segments, got %v", len(p.truth), len(segs))
	}
	runner := s.Runner.(*scriptedRunner)
	runner.mu.Lock()
	calls := slices.Clone(runner.calls)
	runner.mu.Unlock()
	for _, call := range calls {
		if m := atrimRe.FindStringSubmatch(strings.Join(call, " ")); m != nil && m[1] == m[2] {
			t.Errorf("a core of length 0 was extracted: %v", call)
		}
	}
	if encodes := runner.count("ffmpeg", "libmp3lame"); encodes != p.requests() || p.requests() < 14 {
		t.Errorf("%v encodes for %v uploads; want one encode per uploaded request, merge links included", encodes, p.requests())
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
	t.Parallel()
	// 60 KB input: with a 40 KB cap the non-diarized path splits into ceil(60/32)=2 chunks
	trans := &fakeTranscriber{}
	runner := &fakeRunner{defaultDur: "900.000000", durations: map[string]string{"input.wav": "1800.000000"}, chunkCount: 2}
	s := NewSplitter(trans, runner)
	s.StatusOut = &bytes.Buffer{}
	s.Model = "whisper-1"
	s.MaxBytes = 40 << 10
	if _, err := s.Transcribe(context.Background(), sparseFile(t, 60<<10)); err != nil {
		t.Fatal(err)
	}
	segmentTime := ""
	for _, c := range runner.calls {
		if i := indexOf(c, "-segment_time"); i >= 0 {
			segmentTime = c[i+1]
		}
	}
	if segmentTime != "900.500" {
		t.Errorf("expected 2 chunks of 900 s plus the split slack under a 40 KB cap (80%% target), got segment time %q", segmentTime)
	}
	// A 4 MiB cap (the multipart reserve is 16 KiB) passes a 3 MiB diarized file through and calibrates a 5 MiB one
	p := &voiceProvider{truth: smallMeeting()}
	conf := smallBudgetConf("gpt-4o-transcribe-diarize")
	conf.Transcribe.MaxRequestBytes = 4 << 20
	conf.Transcribe.MaxRequestSeconds = 200
	cs, _, _ := newCalibratedSplitter(t, p, conf)
	direct := &fakeTranscriber{diarization: generic.DiarizationAnchored}
	cs.Transcriber = direct
	if _, err := cs.Transcribe(context.Background(), sparseFile(t, 3<<20)); err != nil {
		t.Fatal(err)
	}
	if direct.callCount() != 1 || p.requests() != 0 {
		t.Errorf("3 MiB under a 4 MiB cap must be one direct request, got direct %v calibrated %v", direct.callCount(), p.requests())
	}
	if _, err := cs.Transcribe(context.Background(), sparseFile(t, 5<<20)); err != nil {
		t.Fatal(err)
	}
	if p.requests() == 0 {
		t.Error("5 MiB over a 4 MiB cap must calibrate")
	}
}

func TestCalibrationToolBridgeUsesSameEngine(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	conf := smallBudgetConf("gpt-4o-transcribe-diarize")
	s, err := createSplitter(conf)
	if err != nil {
		t.Fatal(err)
	}
	if s.MaxBytes != 1<<20 || s.Budgets.MaxRequestDuration != 80*time.Second || s.Parallelism != 1 {
		t.Errorf("splitter not configured from config: %+v", s)
	}
	if _, ok := s.Transcriber.(interface{ Endpoint() string }); !ok {
		t.Error("vendor transcriber must expose its endpoint for the cache key")
	}
	if pkgtools.AudioTranscribeEngine == nil {
		t.Fatal("tool engine not wired")
	}
	// The tool bridge and the CLI share createSplitter; a config error surfaces the same way in both
	conf.Transcribe.MaxRequestSeconds = -1
	if _, err := createSplitter(conf); err == nil || !strings.Contains(err.Error(), "max-request-seconds") {
		t.Errorf("expected budget error through the shared engine, got %v", err)
	}
}

func TestFlagOverridesBudget(t *testing.T) {
	conf := Default
	f := &Flags{}
	for _, sv := range []struct {
		v   interface{ Set(string) error }
		val string
	}{{&f.MaxRequestBytes, "1048576"}, {&f.MaxRequestSeconds, "600"}} {
		if err := sv.v.Set(sv.val); err != nil {
			t.Fatal(err)
		}
	}
	if err := ApplyFlagOverrides(&conf, f); err != nil {
		t.Fatal(err)
	}
	tc := conf.Transcribe
	if tc.MaxRequestBytes != 1<<20 || tc.MaxRequestSeconds != 600 {
		t.Errorf("flags did not override config: %+v", tc)
	}
	untouched := Default
	if err := ApplyFlagOverrides(&untouched, &Flags{}); err != nil || !reflect.DeepEqual(untouched, Default) {
		t.Errorf("unset flags must leave the file config: %+v, %v", untouched, err)
	}
}

func TestFlagRejectsNegativeBudget(t *testing.T) {
	for _, name := range []string{"max-request-bytes", "max-request-seconds"} {
		f := &Flags{}
		var target *interface{ Set(string) error }
		switch name {
		case "max-request-bytes":
			v := interface{ Set(string) error }(&f.MaxRequestBytes)
			target = &v
		default:
			v := interface{ Set(string) error }(&f.MaxRequestSeconds)
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

// L3.2, I7, Q2: there is no strict mode and no speaker cap. An old config
// that still sets either loads and the values are ignored; the flags are
// refused like any flag clai does not define, before a request.
func TestOldConfigSettingsLoad(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "audioConfig.json"), []byte(`{"transcribe":{"strict-speakers":true,"max-speakers":3,"parallelism":2}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	conf, _, err := utils.LoadConfigFromFileCollect(context.Background(), dir, "audioConfig.json", nil, &Default)
	if err != nil || conf.Transcribe.Parallelism != 2 {
		t.Fatalf("an old config with strict-speakers and max-speakers must load, got %+v, %v", conf.Transcribe, err)
	}
	fs := flag.NewFlagSet("audio", flag.ContinueOnError)
	(&Flags{}).Register(fs)
	for _, name := range []string{"strict-speakers", "max-speakers"} {
		if fs.Lookup(name) != nil {
			t.Errorf("-%v must not exist", name)
		}
	}
	for _, unknown := range []string{"-max-speakers", "-no-such-flag"} {
		prepared := 0
		c := Command(CommandDeps{
			ConfigPrep: func(context.Context) (string, []string, error) {
				prepared++
				return t.TempDir(), nil, nil
			},
			NewRemoteResolver: func(string, remote.Prompter) (remote.Resolver, error) {
				return remote.Resolver{}, errors.New("no resolver in this test")
			},
		})
		streams := stdio.New(stdio.Config{Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard})
		ctx := runenv.With(context.Background(), runenv.New(runenv.Config{Streams: streams}))
		status, err := cmd.RunSilentTo(ctx, []string{"clai", "audio", "transcribe", unknown, "3", "f.wav"}, map[string]cmd.Command{"audio": c}, io.Discard)
		if status != 1 || err == nil || !strings.Contains(err.Error(), "flag provided but not defined: "+unknown) || prepared != 0 {
			t.Errorf("%v: status %v, err %v, config prepared %v times; want it refused as an unknown flag before a request", unknown, status, err, prepared)
		}
	}
}

func TestToolBridgePropagatesCalibrationError(t *testing.T) {
	prev := pkgtools.AudioTranscribeEngine
	t.Cleanup(func() { pkgtools.AudioTranscribeEngine = prev })
	pkgtools.AudioTranscribeEngine = func(_ context.Context, _ pkgtools.AudioTranscribeCall) (string, error) {
		return "", errors.New("labelling request for chunk 2: connection reset by peer")
	}
	got, err := pkgtools.AudioTranscribe.Call(map[string]any{"file_path": "meeting.wav"})
	if err == nil || got != "" || !strings.Contains(err.Error(), "connection reset by peer") {
		t.Errorf("expected the calibration error returned by the tool, got %q, %v", got, err)
	}
}
