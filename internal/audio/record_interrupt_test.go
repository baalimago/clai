package audio

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/capture"
	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/internal/stdio"
	pkgtools "github.com/baalimago/clai/pkg/tools"
)

func textRecordEnv(stderr io.Writer) runenv.Env {
	return runenv.New(runenv.Config{
		Streams: stdio.New(stdio.Config{Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: stderr}),
	})
}

// TestRecordText_InterruptedAndSaved pins what the user is told when a
// cancelled recording was kept: one line on stderr with the path, in text and
// as a record, and an error that carries the same path to the tool result.
func TestRecordText_InterruptedAndSaved(t *testing.T) {
	saved := filepath.Join(t.TempDir(), "2026-11-10T10-00-00Z.wav")
	interrupted := &capture.InterruptedError{Path: saved, Finish: capture.FinishFinalized}
	line := "recording interrupted: saved " + saved

	t.Run("text", func(t *testing.T) {
		dir := t.TempDir()
		setAudioConfig(t, map[string]any{"dir": dir, "max-seconds": 1, "silence-timeout": 0, "sinks": "none", "calibrated": true})
		var stderr bytes.Buffer
		ctx := runenv.With(context.Background(), textRecordEnv(&stderr))

		result, err := recordText(ctx, pkgtools.AudioRecordCall{}, &recordStub{err: interrupted}, nil, false)
		if result != "" || !errors.Is(err, capture.ErrInterrupted) || err.Error() != line {
			t.Fatalf("recordText = %q, %v, want no facts and %q", result, err, line)
		}
		want := "recording " + dir + " · stops: max 1s\n" + line + "\n"
		if got := stderr.String(); got != want {
			t.Fatalf("stderr = %q, want %q", got, want)
		}
	})

	t.Run("json", func(t *testing.T) {
		setAudioConfig(t, map[string]any{"dir": t.TempDir(), "calibrated": true, "sinks": "none"})
		var stdout, stderr bytes.Buffer
		env := jsonRecordEnv(strings.NewReader(""), &stdout, &stderr)
		ctx := runenv.With(context.Background(), env)

		result, err := recordText(ctx, pkgtools.AudioRecordCall{}, &stateRecorder{err: interrupted}, env.Stdin(), false)
		if result != "" || !errors.Is(err, capture.ErrInterrupted) {
			t.Fatalf("recordText = %q, %v, want no facts and the interrupted error", result, err)
		}
		if err := env.Streams().Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		got := progressRecords(t, stderr.String())
		if len(got) != 2 || got[0].State != "start" || got[1].State != "done" || got[1].Detail != line {
			t.Fatalf("records = %+v, want start, then done with %q", got, line)
		}
		if stdout.Len() != 0 {
			t.Fatalf("stdout = %q, want empty", stdout.String())
		}
	})

	t.Run("a cancelled recording that kept nothing ends failed", func(t *testing.T) {
		setAudioConfig(t, map[string]any{"dir": t.TempDir(), "calibrated": true, "sinks": "none"})
		var stderr bytes.Buffer
		env := jsonRecordEnv(strings.NewReader(""), io.Discard, &stderr)
		ctx := runenv.With(context.Background(), env)
		lost := &capture.InterruptedError{Lost: capture.ErrNoAudio}

		if _, err := recordText(ctx, pkgtools.AudioRecordCall{}, &stateRecorder{err: lost}, env.Stdin(), false); !errors.Is(err, capture.ErrInterrupted) {
			t.Fatalf("error = %v, want the interrupted error", err)
		}
		if err := env.Streams().Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		got := progressRecords(t, stderr.String())
		want := "recording interrupted: nothing saved: no audio captured"
		if len(got) != 2 || got[1].State != "failed" || got[1].Detail != want {
			t.Fatalf("records = %+v, want start, then failed with %q", got, want)
		}
	})
}

// TestRecordText_InterruptKeepsWithoutTranscribing pins the decision for a
// cancelled recording, with the recorder and a fake ffmpeg that needs the
// interrupt to finalize its file: the file is kept and named, the call
// returns no facts for a model to act on, and the recording itself starts no
// transcription.
func TestRecordText_InterruptKeepsWithoutTranscribing(t *testing.T) {
	fakeFFmpeg(t)
	t.Setenv("FAKE_FFMPEG_RECORD_MODE", "growing")
	dir := t.TempDir()
	setAudioConfig(t, map[string]any{"dir": dir, "max-seconds": 10, "silence-timeout": 0, "sinks": "none", "mic": true, "calibrated": true})

	transcriptions := 0
	engine := pkgtools.AudioTranscribeEngine
	pkgtools.AudioTranscribeEngine = func(ctx context.Context, path, format string) (string, error) {
		transcriptions++
		return engine(ctx, path, format)
	}
	t.Cleanup(func() { pkgtools.AudioTranscribeEngine = engine })

	var stderr bytes.Buffer
	ctx, cancel := context.WithCancel(runenv.With(context.Background(), textRecordEnv(&stderr)))
	defer cancel()
	type outcome struct {
		result string
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := recordText(ctx, pkgtools.AudioRecordCall{}, capture.New(), nil, false)
		done <- outcome{result, err}
	}()
	cancelOnPartFile(t, dir, cancel)
	got := <-done

	var interrupted *capture.InterruptedError
	if got.result != "" || !errors.As(got.err, &interrupted) || interrupted.Path == "" {
		t.Fatalf("recordText = %q, %v, want no facts and the saved path", got.result, got.err)
	}
	if filepath.Dir(interrupted.Path) != dir {
		t.Fatalf("saved %q, want it in the recording directory %q", interrupted.Path, dir)
	}
	if info, err := os.Stat(interrupted.Path); err != nil || info.Size() < 44 {
		t.Fatalf("saved file: %v, %v, want a WAV", info, err)
	}
	if !strings.HasSuffix(stderr.String(), "recording interrupted: saved "+interrupted.Path+"\n") {
		t.Fatalf("stderr = %q, want the saved path as its last line", stderr.String())
	}
	if transcriptions != 0 {
		t.Fatalf("the recording started %d transcriptions, want none", transcriptions)
	}
}
