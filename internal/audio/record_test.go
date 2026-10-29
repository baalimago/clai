package audio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/capture"
	"github.com/baalimago/clai/internal/utils"
	pkgtools "github.com/baalimago/clai/pkg/tools"
)

func TestRecordText_Facts(t *testing.T) {
	fakeFFmpeg(t)
	dir := t.TempDir()
	setAudioConfig(t, map[string]any{
		"dir":             dir,
		"max-seconds":     1,
		"silence-timeout": 0,
		"silence-peak-db": -40,
		"sinks":           "none",
		"mic":             true,
	})
	status := captureStderr(t)

	result, err := recordText(context.Background(), pkgtools.AudioRecordCall{NameSuffix: "release-week"}, capture.New(), strings.NewReader(""), false)
	stderr := status()
	if err != nil {
		t.Fatalf("recordText: %v", err)
	}

	lines := strings.Split(strings.TrimSuffix(result, "\n"), "\n")
	if len(lines) != 6 {
		t.Fatalf("result has %d lines, want 6:\n%s", len(lines), result)
	}
	wantPrefixes := []string{"recorded: ", "started_at: ", "ended_at: ", "duration_seconds: ", "stopped_because: ", "sources: "}
	for i, prefix := range wantPrefixes {
		if !strings.HasPrefix(lines[i], prefix) {
			t.Fatalf("line %d = %q, want prefix %q", i, lines[i], prefix)
		}
	}
	path := strings.TrimPrefix(lines[0], "recorded: ")
	if !filepath.IsAbs(path) {
		t.Fatalf("path %q is not absolute", path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("recording %q: %v", path, err)
	}
	if _, err := time.Parse(time.RFC3339, strings.TrimPrefix(lines[1], "started_at: ")); err != nil {
		t.Fatalf("started_at is not RFC3339: %v", err)
	}
	if _, err := time.Parse(time.RFC3339, strings.TrimPrefix(lines[2], "ended_at: ")); err != nil {
		t.Fatalf("ended_at is not RFC3339: %v", err)
	}
	if lines[4] != "stopped_because: max_seconds" {
		t.Fatalf("stop reason = %q", lines[4])
	}
	if lines[5] != "sources: mic=Built-in Audio Analog Stereo" {
		t.Fatalf("sources = %q", lines[5])
	}
	wantNotice := "recording " + dir + " · stops: max 1s\n"
	if stderr != wantNotice {
		t.Fatalf("stderr = %q, want %q", stderr, wantNotice)
	}
}

func TestRecordText_Config(t *testing.T) {
	dir := t.TempDir()
	outOfRange := []struct {
		name   string
		values map[string]any
		want   string
	}{
		{name: "sinks", values: map[string]any{"dir": dir, "sinks": "speakers"}, want: "capture.sinks"},
		{name: "max seconds", values: map[string]any{"dir": dir, "max-seconds": -1}, want: "capture.max-seconds"},
		{name: "silence timeout", values: map[string]any{"dir": dir, "silence-timeout": -1}, want: "capture.silence-timeout"},
		{name: "silence peak", values: map[string]any{"dir": dir, "silence-peak-db": 1}, want: "capture.silence-peak-db"},
	}
	for _, tc := range outOfRange {
		t.Run(tc.name, func(t *testing.T) {
			setAudioConfig(t, tc.values)
			stub := &recordStub{}
			_, err := recordText(context.Background(), pkgtools.AudioRecordCall{}, stub, nil, false)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to name %s", err, tc.want)
			}
			if stub.called {
				t.Fatalf("recorder ran on an invalid configuration")
			}
		})
	}

	t.Run("call overrides the file", func(t *testing.T) {
		fileDir := t.TempDir()
		callDir := t.TempDir()
		setAudioConfig(t, map[string]any{
			"dir":             fileDir,
			"max-seconds":     5,
			"silence-timeout": 6,
			"silence-peak-db": -30,
			"sinks":           "all",
			"mic":             false,
		})
		stub := &recordStub{recording: capture.Recording{Path: "x.wav"}}
		mic := true
		_, err := recordText(context.Background(), pkgtools.AudioRecordCall{
			Devices:        []string{"device-a"},
			MaxSeconds:     2,
			SilenceTimeout: 3,
			NameSuffix:     "standup",
			Dir:            callDir,
			Sinks:          "none",
			Mic:            &mic,
		}, stub, nil, false)
		if err != nil {
			t.Fatalf("recordText: %v", err)
		}
		got := stub.request
		if got.Dir != callDir {
			t.Fatalf("dir = %q, want %q", got.Dir, callDir)
		}
		if got.MaxDuration != 2*time.Second || got.SilenceTimeout != 3*time.Second {
			t.Fatalf("durations = %v/%v, want 2s/3s", got.MaxDuration, got.SilenceTimeout)
		}
		if got.Selection.Sinks != "none" || !got.Selection.Mic {
			t.Fatalf("selection = %+v, want none/true", got.Selection)
		}
		if len(got.Devices) != 1 || got.Devices[0] != "device-a" || got.NameSuffix != "standup" {
			t.Fatalf("call fields not propagated: %+v", got)
		}
		if got.SilencePeakDB != -30 {
			t.Fatalf("silence peak = %v, want the file's -30", got.SilencePeakDB)
		}
	})

	t.Run("file overrides the built-in default", func(t *testing.T) {
		dir := t.TempDir()
		setAudioConfig(t, map[string]any{"dir": dir, "max-seconds": 7})
		stub := &recordStub{}
		if _, err := recordText(context.Background(), pkgtools.AudioRecordCall{}, stub, nil, false); err != nil {
			t.Fatalf("recordText: %v", err)
		}
		got := stub.request
		if got.MaxDuration != 7*time.Second {
			t.Fatalf("max duration = %v, want 7s", got.MaxDuration)
		}
		if got.SilenceTimeout != time.Duration(Default.Capture.SilenceTimeout)*time.Second {
			t.Fatalf("silence timeout = %v, want the default", got.SilenceTimeout)
		}
		if got.Selection.Sinks != "default" || !got.Selection.Mic {
			t.Fatalf("selection = %+v, want the defaults", got.Selection)
		}
	})
}

func TestRecordText_DirError(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	setAudioConfig(t, map[string]any{"dir": filepath.Join(file, "recordings")})
	stub := &recordStub{}
	_, err := recordText(context.Background(), pkgtools.AudioRecordCall{}, stub, nil, false)
	if err == nil || !strings.Contains(err.Error(), "create recording dir") {
		t.Fatalf("error = %v, want a directory error", err)
	}
	if stub.called {
		t.Fatalf("recorder ran when the directory could not be created")
	}
}

func TestRecordText_Enter(t *testing.T) {
	t.Run("terminal line stops the recording", func(t *testing.T) {
		fakeFFmpeg(t)
		dir := t.TempDir()
		setAudioConfig(t, map[string]any{"dir": dir, "max-seconds": 5, "silence-timeout": 0, "sinks": "none", "mic": true})
		result, err := recordText(context.Background(), pkgtools.AudioRecordCall{}, capture.New(), enterAfter(150*time.Millisecond), true)
		if err != nil {
			t.Fatalf("recordText: %v", err)
		}
		if !strings.Contains(result, "stopped_because: enter") {
			t.Fatalf("result = %q, want enter", result)
		}
	})

	t.Run("non-terminal stdin passes no stop channel", func(t *testing.T) {
		stub := &recordStub{}
		dir := t.TempDir()
		setAudioConfig(t, map[string]any{"dir": dir, "max-seconds": 1, "sinks": "none"})
		if _, err := recordText(context.Background(), pkgtools.AudioRecordCall{}, stub, strings.NewReader("\n"), false); err != nil {
			t.Fatalf("recordText: %v", err)
		}
		if stub.request.Stop != nil {
			t.Fatalf("non-terminal stdin must not install a stop channel")
		}
	})

	t.Run("end of terminal stdin is not an Enter press", func(t *testing.T) {
		stub := &recordStub{}
		setAudioConfig(t, map[string]any{"dir": t.TempDir(), "max-seconds": 1, "sinks": "none"})
		if _, err := recordText(context.Background(), pkgtools.AudioRecordCall{}, stub, strings.NewReader(""), true); err != nil {
			t.Fatalf("recordText: %v", err)
		}
		if stub.request.Stop == nil {
			t.Fatal("terminal stdin must install a stop channel")
		}
		select {
		case <-stub.request.Stop:
			t.Fatal("end of input closed the stop channel")
		case <-time.After(50 * time.Millisecond):
		}
	})
}

// TestEnterStop_EOFIsNotALine pins EOF apart from Enter: /dev/null is a
// character device, so a caller can mistake it for a terminal, and closing
// the stop channel there would interrupt a recording before ffmpeg wrote the
// WAV (worklog 2026-10-28-audio-record-tool, R3-01).
func TestEnterStop_EOFIsNotALine(t *testing.T) {
	t.Run("a line stops the recording", func(t *testing.T) {
		stop := stopOnKeys(strings.NewReader("\n"), true, nil)
		if stop == nil {
			t.Fatal("stopOnKeys returned no channel for a terminal")
		}
		select {
		case <-stop:
		case <-time.After(time.Second):
			t.Fatal("a line did not close the stop channel")
		}
	})

	t.Run("A acknowledges and a later line stops", func(t *testing.T) {
		acked := make(chan struct{}, 1)
		stop := stopOnKeys(strings.NewReader("a\n\n"), true, func() { acked <- struct{}{} })
		select {
		case <-acked:
		case <-time.After(time.Second):
			t.Fatal("A did not acknowledge")
		}
		select {
		case <-stop:
		case <-time.After(time.Second):
			t.Fatal("the line after A did not stop the recording")
		}
	})

	t.Run("A alone leaves the recording running", func(t *testing.T) {
		acked := make(chan struct{}, 1)
		stop := stopOnKeys(strings.NewReader("a\n"), true, func() { acked <- struct{}{} })
		select {
		case <-acked:
		case <-time.After(time.Second):
			t.Fatal("A did not acknowledge")
		}
		select {
		case <-stop:
			t.Fatal("A closed the stop channel")
		case <-time.After(50 * time.Millisecond):
		}
	})

	t.Run("a non-terminal passes no stop channel", func(t *testing.T) {
		if stop := stopOnKeys(strings.NewReader("\n"), false, nil); stop != nil {
			t.Fatal("non-terminal stdin must not install a stop channel")
		}
	})

	t.Run("end of input leaves the channel open", func(t *testing.T) {
		stop := stopOnKeys(strings.NewReader(""), true, nil)
		select {
		case <-stop:
			t.Fatal("end of input closed the stop channel")
		case <-time.After(50 * time.Millisecond):
		}
	})
}

func TestRecordText_CalibrationNotice(t *testing.T) {
	t.Run("an uncalibrated machine is told before the row", func(t *testing.T) {
		dir := t.TempDir()
		setAudioConfig(t, map[string]any{"dir": dir, "max-seconds": 1, "sinks": "none"})
		status := captureStderr(t)
		if _, err := recordText(context.Background(), pkgtools.AudioRecordCall{}, &recordStub{}, strings.NewReader(""), true); err != nil {
			t.Fatalf("recordText: %v", err)
		}
		want := calibrationNotice + "\nrecording " + dir + " · stops: [Enter], 10m silence, max 1s\n"
		if got := status(); got != want {
			t.Fatalf("stderr = %q, want %q", got, want)
		}
	})

	t.Run("a calibrated machine is not told", func(t *testing.T) {
		dir := t.TempDir()
		setAudioConfig(t, map[string]any{"dir": dir, "max-seconds": 1, "sinks": "none", "calibrated": true})
		status := captureStderr(t)
		if _, err := recordText(context.Background(), pkgtools.AudioRecordCall{}, &recordStub{}, strings.NewReader(""), true); err != nil {
			t.Fatalf("recordText: %v", err)
		}
		want := "recording " + dir + " · stops: [Enter], 10m silence, max 1s\n"
		if got := status(); got != want {
			t.Fatalf("stderr = %q, want %q", got, want)
		}
	})

	t.Run("a non-interactive run is not told", func(t *testing.T) {
		dir := t.TempDir()
		setAudioConfig(t, map[string]any{"dir": dir, "max-seconds": 1, "sinks": "none"})
		status := captureStderr(t)
		if _, err := recordText(context.Background(), pkgtools.AudioRecordCall{}, &recordStub{}, strings.NewReader(""), false); err != nil {
			t.Fatalf("recordText: %v", err)
		}
		if got := status(); strings.Contains(got, calibrationNotice) {
			t.Fatalf("stderr = %q, want no calibration notice without a terminal", got)
		}
	})
}

// TestRecordText_AcknowledgeKey proves the A key reaches the config file: a
// recording stub holds Record open until the flag lands, so the assertion never
// races the reader goroutine.
func TestRecordText_AcknowledgeKey(t *testing.T) {
	setAudioConfig(t, map[string]any{"dir": t.TempDir(), "max-seconds": 1, "sinks": "none"})
	path := audioConfigPath(t)
	captureStderr(t)
	block := make(chan struct{})
	go func() {
		defer close(block)
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			raw, err := os.ReadFile(path)
			if err == nil {
				var conf Configurations
				if json.Unmarshal(raw, &conf) == nil && conf.Capture.Calibrated {
					return
				}
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	if _, err := recordText(context.Background(), pkgtools.AudioRecordCall{}, &recordStub{block: block}, strings.NewReader("a\n"), true); err != nil {
		t.Fatalf("recordText: %v", err)
	}
	if !readAudioConfig(t).Capture.Calibrated {
		t.Fatal("A did not store the calibration acknowledgement")
	}
}

func audioConfigPath(t *testing.T) string {
	t.Helper()
	dir, err := utils.GetClaiConfigDir()
	if err != nil {
		t.Fatalf("config dir: %v", err)
	}
	return filepath.Join(dir, "audioConfig.json")
}

func TestRecordText_RecorderError(t *testing.T) {
	t.Run("interrupted error keeps its saved path", func(t *testing.T) {
		setAudioConfig(t, map[string]any{"dir": t.TempDir()})
		saved := filepath.Join(t.TempDir(), "saved.wav")
		stub := &recordStub{err: fmt.Errorf("%w: saved %s", capture.ErrInterrupted, saved)}
		_, err := recordText(context.Background(), pkgtools.AudioRecordCall{}, stub, nil, false)
		if !errors.Is(err, capture.ErrInterrupted) || !strings.Contains(err.Error(), saved) {
			t.Fatalf("error = %v, want the interrupted error with %q", err, saved)
		}
	})

	t.Run("ffmpeg failure keeps its message", func(t *testing.T) {
		fakeFFmpeg(t)
		setAudioConfig(t, map[string]any{"dir": t.TempDir(), "max-seconds": 2, "sinks": "none", "mic": true})
		t.Setenv("FAKE_FFMPEG_RECORD_MODE", "fail")
		_, err := recordText(context.Background(), pkgtools.AudioRecordCall{}, capture.New(), nil, false)
		if err == nil || !strings.Contains(err.Error(), "device open failed") {
			t.Fatalf("error = %v, want ffmpeg's message", err)
		}
	})

	t.Run("cancelled call returns the interrupted error", func(t *testing.T) {
		fakeFFmpeg(t)
		dir := t.TempDir()
		setAudioConfig(t, map[string]any{"dir": dir, "max-seconds": 10, "silence-timeout": 0, "sinks": "none", "mic": true})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		result := make(chan error, 1)
		go func() {
			_, err := recordText(ctx, pkgtools.AudioRecordCall{}, capture.New(), nil, false)
			result <- err
		}()
		cancelOnPartFile(t, dir, cancel)
		if err := <-result; !errors.Is(err, capture.ErrInterrupted) {
			t.Fatalf("error = %v, want ErrInterrupted", err)
		}
	})
}

func TestRecordText_WritesNothingToStdout(t *testing.T) {
	setAudioConfig(t, map[string]any{"dir": t.TempDir()})
	stdout := os.Stdout
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = write
	t.Cleanup(func() { os.Stdout = stdout })

	_, callErr := recordText(context.Background(), pkgtools.AudioRecordCall{}, &recordStub{}, nil, false)
	os.Stdout = stdout
	_ = write.Close()
	out, readErr := io.ReadAll(read)
	_ = read.Close()
	if callErr != nil {
		t.Fatalf("recordText: %v", callErr)
	}
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(out) != 0 {
		t.Fatalf("stdout = %q, want nothing", out)
	}
}

type recordStub struct {
	called    bool
	request   capture.Request
	recording capture.Recording
	err       error
	// block holds Record until it is closed, for tests that must wait for
	// something the recording itself triggers.
	block <-chan struct{}
}

func (s *recordStub) Sources(context.Context) ([]capture.Source, error) {
	return nil, errors.New("Sources is not used by recordText")
}

func (s *recordStub) Measure(context.Context, capture.MeasureRequest) (capture.Ambient, error) {
	return capture.Ambient{}, errors.New("Measure is not used by recordText")
}

func (s *recordStub) Record(_ context.Context, req capture.Request) (capture.Recording, error) {
	s.called = true
	s.request = req
	if s.block != nil {
		<-s.block
	}
	return s.recording, s.err
}

func setAudioConfig(t *testing.T, captureSection map[string]any) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CLAI_CONFIG_DIR", dir)
	raw, err := json.Marshal(map[string]any{"capture": captureSection})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "audioConfig.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func captureStderr(t *testing.T) func() string {
	t.Helper()
	original := os.Stderr
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = write
	finished := false
	finish := func() string {
		if finished {
			return ""
		}
		finished = true
		os.Stderr = original
		_ = write.Close()
		out, _ := io.ReadAll(read)
		_ = read.Close()
		return string(out)
	}
	t.Cleanup(func() { _ = finish() })
	return finish
}

func fakeFFmpeg(t *testing.T) {
	t.Helper()
	binDir := t.TempDir()
	data, err := os.ReadFile(filepath.Join("..", "capture", "testdata", "fake_ffmpeg.sh"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "ffmpeg"), data, 0o755); err != nil {
		t.Fatalf("install fixture: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// cancelOnPartFile cancels once ffmpeg has written the WAV, so the test pins
// a cancelled recording and not a cancelled discovery (A01).
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

func enterAfter(delay time.Duration) io.Reader {
	read, write := io.Pipe()
	go func() {
		time.Sleep(delay)
		_, _ = write.Write([]byte("\n"))
		_ = write.Close()
	}()
	return read
}
