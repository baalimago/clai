package capture

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/testenv"
)

func TestMain(m *testing.M) { testenv.Main(m) }

func TestSupportedByOS(t *testing.T) {
	if !supportedFor("linux") || !supportedFor("darwin") {
		t.Fatalf("linux and darwin must be supported")
	}
	if supportedFor("windows") {
		t.Fatalf("windows must be unsupported")
	}
	if Supported() != supportedFor(runtime.GOOS) {
		t.Fatalf("Supported() = %v, want supportedFor(%q) = %v", Supported(), runtime.GOOS, supportedFor(runtime.GOOS))
	}
	if !strings.Contains(ErrUnsupported.Error(), "linux or macOS") {
		t.Fatalf("ErrUnsupported = %q, want the platforms it needs", ErrUnsupported)
	}
}

func TestFFmpegMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	rec := New()

	if _, err := rec.Sources(context.Background()); err == nil || !strings.Contains(err.Error(), "ffmpeg") {
		t.Fatalf("Sources error = %v, want it to name ffmpeg", err)
	}
	_, err := rec.Record(context.Background(), Request{Dir: t.TempDir(), Selection: Selection{Sinks: "none", Mic: true}})
	if err == nil || !strings.Contains(err.Error(), "ffmpeg") {
		t.Fatalf("Record error = %v, want it to name ffmpeg", err)
	}
	if err == nil || !strings.Contains(err.Error(), "install ffmpeg") {
		t.Fatalf("Record error = %v, want the install action", err)
	}
	if _, err := rec.Measure(context.Background(), MeasureRequest{Window: time.Second}); err == nil || !strings.Contains(err.Error(), "install ffmpeg") {
		t.Fatalf("Measure error = %v, want the install action", err)
	}
}

func TestRecord_Refusals(t *testing.T) {
	requireCapturePlatform(t)
	fakeFFmpeg(t)
	rec := New()
	dir := t.TempDir()

	if _, err := rec.Record(context.Background(), Request{Dir: dir, Selection: Selection{Sinks: "none"}, MaxDuration: 100 * time.Millisecond}); !errors.Is(err, ErrNoSource) {
		t.Fatalf("empty selection error = %v, want ErrNoSource", err)
	}

	_, err := rec.Record(context.Background(), Request{Dir: dir, Devices: []string{"nope"}, Selection: Selection{Sinks: "none"}, MaxDuration: 100 * time.Millisecond})
	if err == nil || !strings.Contains(err.Error(), "nope") || !strings.Contains(err.Error(), "alsa_input") {
		t.Fatalf("unknown device error = %v, want the name and the known names", err)
	}

	t.Setenv("FAKE_FFMPEG_SINKS_MODE", "empty")
	t.Setenv("FAKE_FFMPEG_SOURCES_MODE", "miconly")
	_, err = rec.Record(context.Background(), Request{Dir: dir, Selection: Selection{Sinks: "default"}, MaxDuration: 100 * time.Millisecond})
	if err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("sink request without a sink error = %v, want the loopback remedy", err)
	}

	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("refusals left files behind: %v", entries)
	}
}

func TestRecord_StopReasons(t *testing.T) {
	requireCapturePlatform(t)

	t.Run("silence", func(t *testing.T) {
		fakeFFmpeg(t)
		req := recordRequest(t)
		req.SilenceTimeout = 100 * time.Millisecond
		req.MaxDuration = 10 * time.Second
		rec, err := New().Record(context.Background(), req)
		requireRecording(t, rec, err, StopSilence)
	})

	t.Run("max_seconds", func(t *testing.T) {
		fakeFFmpeg(t)
		req := recordRequest(t)
		req.MaxDuration = 150 * time.Millisecond
		rec, err := New().Record(context.Background(), req)
		requireRecording(t, rec, err, StopMaxSeconds)
	})

	t.Run("enter", func(t *testing.T) {
		fakeFFmpeg(t)
		req := recordRequest(t)
		req.MaxDuration = 10 * time.Second
		stop := make(chan struct{})
		req.Stop = stop
		go func() {
			time.Sleep(100 * time.Millisecond)
			close(stop)
		}()
		rec, err := New().Record(context.Background(), req)
		requireRecording(t, rec, err, StopEnter)
	})

	t.Run("interrupt", func(t *testing.T) {
		fakeFFmpeg(t)
		req := recordRequest(t)
		req.MaxDuration = 10 * time.Second
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go cancelOnPartFile(t, req.Dir, cancel)
		rec, err := New().Record(ctx, req)
		if !errors.Is(err, ErrInterrupted) {
			t.Fatalf("error = %v, want ErrInterrupted", err)
		}
		if rec.StoppedBecause != StopInterrupt {
			t.Fatalf("reason = %q, want %q", rec.StoppedBecause, StopInterrupt)
		}
		if !strings.Contains(err.Error(), rec.Path) {
			t.Fatalf("error %q must hold the saved path %q", err, rec.Path)
		}
		if ok, readErr := hasRIFFHeader(rec.Path); readErr != nil || !ok {
			t.Fatalf("recording at %q is not a WAV: ok=%v err=%v", rec.Path, ok, readErr)
		}
	})
}

// TestRecord_StateEvents pins the silence states the recorder reports for the
// caller's display: a pause starts as silent, sound ends it, and the next pause
// is silent again.
func TestRecord_StateEvents(t *testing.T) {
	requireCapturePlatform(t)
	fakeFFmpeg(t)
	req := recordRequest(t)
	req.MaxDuration = 500 * time.Millisecond
	req.SilenceTimeout = 10 * time.Second
	var (
		mu     sync.Mutex
		states []CaptureState
	)
	req.OnState = func(state CaptureState) {
		mu.Lock()
		defer mu.Unlock()
		states = append(states, state)
	}

	if _, err := New().Record(context.Background(), req); err != nil {
		t.Fatalf("Record: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []CaptureState{StateSilent, StateSound, StateSilent}
	if !slices.Equal(states, want) {
		t.Fatalf("states = %v, want %v", states, want)
	}
}

func TestMeasure_AmbientLevel(t *testing.T) {
	requireCapturePlatform(t)
	fakeFFmpeg(t)
	log := filepath.Join(t.TempDir(), "args.log")
	t.Setenv("FAKE_FFMPEG_ARGS_LOG", log)

	ambient, err := New().Measure(context.Background(), MeasureRequest{Window: 3 * time.Second})
	if err != nil {
		t.Fatalf("Measure: %v", err)
	}
	if ambient.LevelDB != -44.2 {
		t.Fatalf("level = %v, want the middle frame -44.2", ambient.LevelDB)
	}
	if ambient.Source.Kind != Mic || ambient.Source.Name != "alsa_input.pci-0000_00_1f.3.analog-stereo" {
		t.Fatalf("source = %+v, want the default microphone", ambient.Source)
	}
	args, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("read args log: %v", err)
	}
	if !strings.Contains(string(args), "-t 3 ") {
		t.Fatalf("ffmpeg args %q must carry the measurement window", args)
	}
}

func TestMeasure_Refusals(t *testing.T) {
	requireCapturePlatform(t)

	t.Run("a window that is not positive", func(t *testing.T) {
		if _, err := New().Measure(context.Background(), MeasureRequest{}); err == nil || !strings.Contains(err.Error(), "window") {
			t.Fatalf("err = %v, want a window error", err)
		}
	})

	t.Run("ffmpeg finds no level", func(t *testing.T) {
		fakeFFmpeg(t)
		t.Setenv("FAKE_FFMPEG_MEASURE_MODE", "none")
		if _, err := New().Measure(context.Background(), MeasureRequest{Window: time.Second}); err == nil || !strings.Contains(err.Error(), "no ambient level") {
			t.Fatalf("err = %v, want it to name the missing level", err)
		}
	})

	t.Run("ffmpeg fails", func(t *testing.T) {
		fakeFFmpeg(t)
		t.Setenv("FAKE_FFMPEG_MEASURE_MODE", "fail")
		_, err := New().Measure(context.Background(), MeasureRequest{Window: time.Second})
		if err == nil || !strings.Contains(err.Error(), "device open failed") {
			t.Fatalf("err = %v, want ffmpeg's own message", err)
		}
	})

	t.Run("the call is cancelled", func(t *testing.T) {
		fakeFFmpeg(t)
		t.Setenv("FAKE_FFMPEG_SOURCES_MODE", "slow")
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		if _, err := New().Measure(ctx, MeasureRequest{Window: time.Second}); err == nil || !strings.Contains(err.Error(), "source discovery") {
			t.Fatalf("err = %v, want the cancellation", err)
		}
	})
}

// TestMeasure_DigitalSilence pins the one level that is not a number: a muted
// or disconnected microphone reads -inf, and the caller decides what to do
// with a threshold it cannot derive.
func TestMeasure_DigitalSilence(t *testing.T) {
	requireCapturePlatform(t)
	fakeFFmpeg(t)
	t.Setenv("FAKE_FFMPEG_MEASURE_MODE", "silence")

	ambient, err := New().Measure(context.Background(), MeasureRequest{Window: time.Second})
	if err != nil {
		t.Fatalf("Measure: %v", err)
	}
	if !math.IsInf(ambient.LevelDB, -1) {
		t.Fatalf("level = %v, want -inf", ambient.LevelDB)
	}
}

func TestMedianRMS(t *testing.T) {
	tests := []struct {
		name  string
		dump  string
		want  float64
		found bool
	}{
		{
			name:  "the middle frame of an odd count",
			dump:  "lavfi.astats.Overall.RMS_level=-44.2\nlavfi.astats.Overall.RMS_level=-43.7\nlavfi.astats.Overall.RMS_level=-45.1\n",
			want:  -44.2,
			found: true,
		},
		{
			name:  "the higher middle frame of an even count",
			dump:  "lavfi.astats.Overall.RMS_level=-46\nlavfi.astats.Overall.RMS_level=-44\n",
			want:  -44,
			found: true,
		},
		{
			name:  "a loud click does not move the middle",
			dump:  "lavfi.astats.Overall.RMS_level=-2\nlavfi.astats.Overall.RMS_level=-45\nlavfi.astats.Overall.RMS_level=-44\n",
			want:  -44,
			found: true,
		},
		{
			name:  "digital silence",
			dump:  "lavfi.astats.Overall.RMS_level=-inf\n",
			want:  math.Inf(-1),
			found: true,
		},
		{
			name: "per-channel levels are not the overall level",
			dump: "lavfi.astats.1.RMS_level=-30\nlavfi.astats.Overall.Peak_level=-12\n",
		},
		{
			name: "an unreadable level is skipped",
			dump: "lavfi.astats.Overall.RMS_level=loud\n",
		},
		{
			name: "an empty dump",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			level, found := medianRMS(tc.dump)
			if found != tc.found || (found && level != tc.want) {
				t.Fatalf("medianRMS(%q) = %v, %v; want %v, %v", tc.dump, level, found, tc.want, tc.found)
			}
		})
	}
}

func TestRecord_Naming(t *testing.T) {
	requireCapturePlatform(t)

	stamp := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}Z(_[a-z0-9-]+)?\.wav$`)
	cases := []struct {
		name     string
		suffix   string
		wantSlug string
	}{
		{name: "empty suffix", suffix: "", wantSlug: ""},
		{name: "suffix is slugged", suffix: "Show off to girlfriend!", wantSlug: "show-off-to-girlfriend"},
		{name: "suffix without letters is omitted", suffix: "--", wantSlug: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fakeFFmpeg(t)
			req := recordRequest(t)
			req.NameSuffix = tc.suffix
			req.MaxDuration = 100 * time.Millisecond
			rec, err := New().Record(context.Background(), req)
			if err != nil {
				t.Fatalf("Record: %v", err)
			}
			if !filepath.IsAbs(rec.Path) {
				t.Fatalf("path %q is not absolute", rec.Path)
			}
			base := filepath.Base(rec.Path)
			if !stamp.MatchString(base) {
				t.Fatalf("base = %q, want a UTC stamp", base)
			}
			wantSuffix := ".wav"
			if tc.wantSlug != "" {
				wantSuffix = "_" + tc.wantSlug + ".wav"
			}
			if !strings.HasSuffix(base, wantSuffix) {
				t.Fatalf("base = %q, want suffix %q", base, wantSuffix)
			}
			stampText := strings.TrimSuffix(base, wantSuffix)
			if _, err := time.Parse("2006-01-02T15-04-05Z", stampText); err != nil {
				t.Fatalf("stamp %q is not 2006-01-02T15-04-05Z UTC: %v", stampText, err)
			}
		})
	}
}

func TestRecord_NoAudio(t *testing.T) {
	requireCapturePlatform(t)
	fakeFFmpeg(t)
	t.Setenv("FAKE_FFMPEG_RECORD_MODE", "silent")

	dir := t.TempDir()
	req := recordRequest(t)
	req.Dir = dir
	req.MaxDuration = 150 * time.Millisecond
	if _, err := New().Record(context.Background(), req); !errors.Is(err, ErrNoAudio) {
		t.Fatalf("error = %v, want ErrNoAudio", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("silent recording left files behind: %v", entries)
	}
}

// TestRecord_ContinuousSound pins F5 for a source that never pauses: such a
// capture emits no silencedetect event at all, so only its samples can prove
// that sound was heard.
func TestRecord_ContinuousSound(t *testing.T) {
	requireCapturePlatform(t)
	fakeFFmpeg(t)
	t.Setenv("FAKE_FFMPEG_RECORD_MODE", "continuous")

	req := recordRequest(t)
	req.MaxDuration = 150 * time.Millisecond
	rec, err := New().Record(context.Background(), req)
	requireRecording(t, rec, err, StopMaxSeconds)
}

func TestRecord_FFmpegEnds(t *testing.T) {
	requireCapturePlatform(t)
	fakeFFmpeg(t)
	t.Setenv("FAKE_FFMPEG_RECORD_MODE", "ends")

	req := recordRequest(t)
	req.MaxDuration = 2 * time.Second
	rec, err := New().Record(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "ended early") {
		t.Fatalf("error = %v, want ffmpeg's own message", err)
	}
	if rec.StoppedBecause != StopError {
		t.Fatalf("reason = %q, want %q", rec.StoppedBecause, StopError)
	}
}

// TestStartFFmpeg_LeavesInterruptingToTheRecorder pins the one owner of the
// interrupt: os/exec must not signal the child when the call context ends,
// because a terminal SIGINT already reaches it (worklog
// 2026-10-28-audio-record-tool, R2-01).
func TestStartFFmpeg_LeavesInterruptingToTheRecorder(t *testing.T) {
	requireCapturePlatform(t)
	fakeFFmpeg(t)
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatal(err)
	}
	cmd, stderr, err := startFFmpeg(ffmpeg, []Source{{Kind: Mic, Name: "mic", Default: true}}, filepath.Join(t.TempDir(), "out.wav"), -40)
	if err != nil {
		t.Fatalf("startFFmpeg: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(os.Interrupt)
		_ = cmd.Wait()
		_ = stderr.Close()
	})
	if cmd.Cancel != nil {
		t.Fatalf("os/exec can interrupt ffmpeg on context cancellation")
	}
}

// TestRequestStop pins the single interrupt: a process that already stopped
// receives none, a running process receives one.
func TestRequestStop(t *testing.T) {
	requireCapturePlatform(t)

	t.Run("a stopped process receives no second interrupt", func(t *testing.T) {
		log := filepath.Join(t.TempDir(), "signals")
		cmd, procDone := startSignalChild(t, log)
		time.Sleep(100 * time.Millisecond)
		if err := cmd.Process.Signal(os.Interrupt); err != nil {
			t.Fatalf("interrupt the child: %v", err)
		}
		<-procDone

		started := time.Now()
		requestStop(cmd, procDone, time.Second)
		if waited := time.Since(started); waited > 100*time.Millisecond {
			t.Fatalf("requestStop waited %v for a stopped process", waited)
		}
		requireSignals(t, log, 1)
	})

	t.Run("a running process receives one interrupt", func(t *testing.T) {
		log := filepath.Join(t.TempDir(), "signals")
		cmd, procDone := startSignalChild(t, log)
		time.Sleep(100 * time.Millisecond)

		requestStop(cmd, procDone, 0)
		requireSignals(t, log, 1)
	})
}

// startSignalChild starts a child that logs every interrupt and exits, the way
// ffmpeg finalizes and exits on SIGINT.
func startSignalChild(t *testing.T, log string) (*exec.Cmd, <-chan struct{}) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "child.sh")
	body := "#!/bin/sh\ntrap 'echo got >> \"$SIGNAL_LOG\"; exit 255' INT\nwhile :; do sleep 0.01; done\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(script)
	cmd.Env = append(os.Environ(), "SIGNAL_LOG="+log)
	cmd.Stdout = nil
	if err := cmd.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	procDone := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(procDone)
	}()
	return cmd, procDone
}

func requireSignals(t *testing.T, log string, want int) {
	t.Helper()
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("read signals: %v", err)
	}
	if got := strings.Count(string(raw), "got"); got != want {
		t.Fatalf("child received %d interrupts, want %d: %q", got, want, raw)
	}
}

func TestRecord_Sources(t *testing.T) {
	requireCapturePlatform(t)
	fakeFFmpeg(t)

	list, err := New().Sources(context.Background())
	if err != nil {
		t.Fatalf("Sources: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("sources = %+v, want one mic and one sink", list)
	}
	if list[0].Kind != Mic || !list[0].Default {
		t.Fatalf("first source = %+v, want a default mic", list[0])
	}
	if list[1].Kind != Sink || !list[1].Default {
		t.Fatalf("second source = %+v, want a default sink", list[1])
	}
}

// TestFilterForSources pins the mix: every source is an input of one amix
// scaled by 1/N, so equal weights cannot clip (worklog
// 2026-10-28-audio-record-tool, F5).
func TestFilterForSources(t *testing.T) {
	one := filterForSources(1, -40)
	if !strings.Contains(one, "[0:a]silencedetect=noise=-40dB:d=0.5") {
		t.Fatalf("one-source filter = %q, want the stream silencedetected", one)
	}
	if strings.Contains(one, "amix") {
		t.Fatalf("one-source filter = %q, want no mix of one input", one)
	}

	three := filterForSources(3, -30)
	want := "[0:a][1:a][2:a]amix=inputs=3:normalize=1,silencedetect=noise=-30dB:d=0.5"
	if three != want {
		t.Fatalf("three-source filter = %q, want %q", three, want)
	}
}

// TestRecord_SelectedSources pins F2's selection and the one-process mix:
// every chosen source reaches ffmpeg as its own input, in the selection's
// order, and two or more sources are mixed with amix normalize=1.
func TestRecord_SelectedSources(t *testing.T) {
	requireCapturePlatform(t)
	const (
		micName  = "alsa_input.pci-0000_00_1f.3.analog-stereo"
		sinkName = "alsa_output.pci-0000_00_1f.3.analog-stereo.monitor"
	)
	cases := []struct {
		name      string
		selection Selection
		devices   []string
		env       map[string]string
		want      []Source
	}{
		{
			name:      "default sink and mic",
			selection: Selection{Sinks: "default", Mic: true},
			want:      []Source{{Kind: Sink, Name: sinkName}, {Kind: Mic, Name: micName}},
		},
		{
			name:      "sink only",
			selection: Selection{Sinks: "default"},
			want:      []Source{{Kind: Sink, Name: sinkName}},
		},
		{
			name:    "devices keep the given order",
			devices: []string{micName, sinkName},
			want:    []Source{{Kind: Mic, Name: micName}, {Kind: Sink, Name: sinkName}},
		},
		{
			name:      "all sinks",
			selection: Selection{Sinks: "all"},
			env: map[string]string{
				"FAKE_FFMPEG_SOURCES_MODE": "twosinks",
				"FAKE_FFMPEG_SINKS_MODE":   "two",
			},
			want: []Source{
				{Kind: Sink, Name: sinkName},
				{Kind: Sink, Name: "alsa_output.usb-headset.analog-stereo.monitor"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fakeFFmpeg(t)
			for name, value := range tc.env {
				t.Setenv(name, value)
			}
			argsLog := filepath.Join(t.TempDir(), "args")
			t.Setenv("FAKE_FFMPEG_ARGS_LOG", argsLog)

			req := recordRequest(t)
			req.Selection = tc.selection
			req.Devices = tc.devices
			req.MaxDuration = 150 * time.Millisecond
			rec, err := New().Record(context.Background(), req)
			requireRecording(t, rec, err, StopMaxSeconds)

			if len(rec.Sources) != len(tc.want) {
				t.Fatalf("sources = %+v, want %+v", rec.Sources, tc.want)
			}
			for i, want := range tc.want {
				if rec.Sources[i].Kind != want.Kind || rec.Sources[i].Name != want.Name {
					t.Fatalf("source %d = %+v, want %+v", i, rec.Sources[i], want)
				}
			}

			raw, err := os.ReadFile(argsLog)
			if err != nil {
				t.Fatalf("read the fixture's argument log: %v", err)
			}
			args := strings.TrimSpace(string(raw))
			at := 0
			for _, source := range tc.want {
				needle := "-f pulse -i " + source.Name
				found := strings.Index(args[at:], needle)
				if found < 0 {
					t.Fatalf("ffmpeg arguments %q do not capture %q in order", args, source.Name)
				}
				at += found + len(needle)
			}
			if len(tc.want) > 1 {
				mix := fmt.Sprintf("amix=inputs=%d:normalize=1", len(tc.want))
				if !strings.Contains(args, mix) {
					t.Fatalf("ffmpeg arguments %q do not contain %q", args, mix)
				}
			}
		})
	}
}

// TestRecord_CancelDuringDiscovery pins A01: a cancellation that lands while
// the source listing runs carries the cancellation, not the child's signal.
func TestRecord_CancelDuringDiscovery(t *testing.T) {
	requireCapturePlatform(t)
	fakeFFmpeg(t)
	t.Setenv("FAKE_FFMPEG_SOURCES_MODE", "slow")
	t.Setenv("FAKE_FFMPEG_SINKS_MODE", "slow")
	t.Setenv("FAKE_FFMPEG_AVFOUNDATION_MODE", "slow")

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	req := recordRequest(t)
	_, err := New().Record(ctx, req)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want the cancellation", err)
	}
	if strings.Contains(err.Error(), "killed") {
		t.Fatalf("error = %q, want no child signal", err)
	}
	if strings.HasSuffix(err.Error(), ": ") {
		t.Fatalf("error = %q, want no empty stderr tail", err)
	}
	entries, readErr := os.ReadDir(req.Dir)
	if readErr != nil {
		t.Fatalf("ReadDir(%q): %v", req.Dir, readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("a cancelled discovery left files behind: %v", entries)
	}
}

// cancelOnPartFile cancels once the recorder's ffmpeg has written the WAV, so
// a test pins a cancelled recording and not a cancelled discovery (A01).
func cancelOnPartFile(t *testing.T, dir string, cancel context.CancelFunc) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		matches, err := filepath.Glob(filepath.Join(dir, "*.wav.part"))
		if err != nil {
			t.Errorf("glob part file: %v", err)
			cancel()
			return
		}
		if len(matches) > 0 {
			cancel()
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Errorf("no .part file appeared in %q", dir)
	cancel()
}

func requireCapturePlatform(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("capture is supported on linux and darwin only")
	}
}

func requireRecording(t *testing.T, rec Recording, err error, want StopReason) {
	t.Helper()
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if rec.StoppedBecause != want {
		t.Fatalf("reason = %q, want %q", rec.StoppedBecause, want)
	}
	if ok, readErr := hasRIFFHeader(rec.Path); readErr != nil || !ok {
		t.Fatalf("recording at %q is not a WAV: ok=%v err=%v", rec.Path, ok, readErr)
	}
}

func recordRequest(t *testing.T) Request {
	t.Helper()
	return Request{
		Dir:           t.TempDir(),
		Selection:     Selection{Sinks: "none", Mic: true},
		SilencePeakDB: -40,
	}
}

func fakeFFmpeg(t *testing.T) {
	t.Helper()
	binDir := t.TempDir()
	data, err := os.ReadFile(filepath.Join("testdata", "fake_ffmpeg.sh"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "ffmpeg"), data, 0o755); err != nil {
		t.Fatalf("install fixture: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
