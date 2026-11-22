package audio

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/testenv/procenv"
	pkgtools "github.com/baalimago/clai/pkg/tools"
)

// testStopFilePoll is the stop file's check interval in these tests: the
// production stopFilePoll would make every row wait whole fifths of a second.
const testStopFilePoll = 10 * time.Millisecond

func TestStopOnFile(t *testing.T) {
	t.Parallel()
	t.Run("the file appearing closes the channel", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "stop")
		stop, err := stopOnFile(context.Background(), path, 10*time.Millisecond)
		if err != nil {
			t.Fatalf("stopOnFile: %v", err)
		}
		select {
		case <-stop:
			t.Fatal("closed before the file existed")
		case <-time.After(50 * time.Millisecond):
		}
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		select {
		case <-stop:
		case <-time.After(time.Second):
			t.Fatal("the file did not close the stop channel")
		}
	})

	t.Run("a file there before the recording is a stale request", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "stop")
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		stop, err := stopOnFile(context.Background(), path, 10*time.Millisecond)
		var stale *StaleStopFileError
		if !errors.As(err, &stale) || stale.Path != path {
			t.Fatalf("error = %v, want a StaleStopFileError for %s", err, path)
		}
		if stop != nil {
			t.Fatal("a stale stop file must not install a stop channel")
		}
	})

	t.Run("a path that cannot be checked is an error", func(t *testing.T) {
		t.Parallel()
		file := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(file, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := stopOnFile(context.Background(), filepath.Join(file, "stop"), 10*time.Millisecond); err == nil {
			t.Fatal("want an error for a path under a regular file")
		}
	})

	t.Run("no path installs no channel", func(t *testing.T) {
		t.Parallel()
		stop, err := stopOnFile(context.Background(), "", 10*time.Millisecond)
		if err != nil || stop != nil {
			t.Fatalf("stopOnFile(\"\") = %v, %v; want nil, nil", stop, err)
		}
	})

	t.Run("the watch ends with its context", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "stop")
		ctx, cancel := context.WithCancel(context.Background())
		stop, err := stopOnFile(ctx, path, 10*time.Millisecond)
		if err != nil {
			t.Fatalf("stopOnFile: %v", err)
		}
		cancel()
		time.Sleep(30 * time.Millisecond)
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		select {
		case <-stop:
			t.Fatal("a watch whose context ended still closed the channel")
		case <-time.After(80 * time.Millisecond):
		}
	})
}

func TestRecordText_StopFile(t *testing.T) {
	t.Run("the stop file ends the recording", func(t *testing.T) {
		fakeFFmpeg(t)
		dir := t.TempDir()
		setAudioConfig(t, map[string]any{"dir": dir, "max-seconds": 5, "silence-timeout": 0, "sinks": "none", "mic": true, "calibrated": true})
		path := filepath.Join(t.TempDir(), "stop")
		recorder := newHeardRecorder()
		recorder.afterSound(t, func() { _ = os.WriteFile(path, nil, 0o644) })
		status := captureStderr(t)
		result, err := recordText(procenv.Context(context.Background()), pkgtools.AudioRecordCall{}, recorder, stopSources{stopFile: path, poll: testStopFilePoll})
		stderr := status()
		if err != nil {
			t.Fatalf("recordText: %v", err)
		}
		if !strings.Contains(result, "stopped_because: stop_file") {
			t.Fatalf("result = %q, want stop_file", result)
		}
		if want := "recording " + dir + " · stops: stop file, max 5s\n"; stderr != want {
			t.Fatalf("stderr = %q, want %q", stderr, want)
		}
	})

	t.Run("a stale stop file fails before anything records", func(t *testing.T) {
		stub := &recordStub{}
		setAudioConfig(t, map[string]any{"dir": t.TempDir(), "max-seconds": 1, "sinks": "none"})
		path := filepath.Join(t.TempDir(), "stop")
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := recordText(procenv.Context(context.Background()), pkgtools.AudioRecordCall{}, stub, stopSources{stopFile: path})
		if _, ok := errors.AsType[*StaleStopFileError](err); !ok {
			t.Fatalf("error = %v, want a StaleStopFileError", err)
		}
		if stub.called {
			t.Fatal("the recorder ran despite a stale stop file")
		}
	})

	t.Run("without a stop file the request has no file stop", func(t *testing.T) {
		stub := &recordStub{}
		setAudioConfig(t, map[string]any{"dir": t.TempDir(), "max-seconds": 1, "sinks": "none"})
		if _, err := recordText(procenv.Context(context.Background()), pkgtools.AudioRecordCall{}, stub, stopSources{}); err != nil {
			t.Fatalf("recordText: %v", err)
		}
		if stub.request.StopFile != nil {
			t.Fatal("no stop file must install no file stop")
		}
	})

	t.Run("the watch ends with the recording", func(t *testing.T) {
		stub := &recordStub{}
		setAudioConfig(t, map[string]any{"dir": t.TempDir(), "max-seconds": 1, "sinks": "none"})
		path := filepath.Join(t.TempDir(), "stop")
		if _, err := recordText(procenv.Context(context.Background()), pkgtools.AudioRecordCall{}, stub, stopSources{stopFile: path, poll: testStopFilePoll}); err != nil {
			t.Fatalf("recordText: %v", err)
		}
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		select {
		case <-stub.request.StopFile:
			t.Fatal("the stop file was still watched after the recording ended")
		case <-time.After(3 * testStopFilePoll):
		}
	})
}
