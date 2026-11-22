package voicestest_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/audio/voices"
	"github.com/baalimago/clai/internal/audio/voices/voicestest"
	"github.com/baalimago/clai/internal/testenv"
)

func TestMain(m *testing.M) { testenv.Main(m) }

func TestRunner_ScriptedAudio(t *testing.T) {
	var runner voicestest.Runner
	inspector := voices.Inspector{Runner: &runner}
	seen := map[voices.ClipID]bool{}
	for seconds, accepted := range map[float64]bool{2.9: false, 3: true, 10: true, 10.1: false} {
		runner.Serve("meeting.wav", voicestest.Speech(seconds))
		got, err := inspector.Inspect(context.Background(), "meeting.wav", voices.Cut{})
		refusal, refused := errors.AsType[*voices.ClipQualityError](err)
		if accepted != (err == nil) || refused && !slices.Equal(refusal.Failed, []voices.QualityCheck{voices.CheckLength}) || seen[voices.ClipIDOf(got.FLAC)] {
			t.Fatalf("Speech(%v): %v; want accepted: %v, as a clip of its own, or refused for its length only", seconds, err, accepted)
		}
		seen[voices.ClipIDOf(got.FLAC)] = accepted
	}
	if silence, loud := voicestest.Silence(0.5), voicestest.Tone(1, 6); len(silence) != 8000 || slices.Max(silence) != 0 || slices.Max(loud) != 32767 || slices.Min(loud) != -32768 {
		t.Fatal("Silence(0.5) is not 8000 zero samples, or a tone above full scale is not held at full scale")
	}
	runner.Serve("tone.wav", voicestest.Tone(5, -30))
	clip, err := inspector.Inspect(context.Background(), "tone.wav", voices.Cut{})
	file := filepath.Join(t.TempDir(), "clip.flac")
	if err := errors.Join(err, os.WriteFile(file, clip.FLAC, 0o600)); err != nil || clip.MeanDBFS > -29.9 || clip.MeanDBFS < -30.1 {
		t.Fatalf("Tone(5, -30) measures %v dBFS, %v", clip.MeanDBFS, err)
	}
	fresh := voices.Inspector{Runner: voicestest.NewRunner()}
	if whole, err := fresh.Inspect(context.Background(), file, voices.Cut{}); err != nil || !reflect.DeepEqual(whole, clip) {
		t.Fatalf("a clip the Runner encoded, inspected whole, is not the clip it was: %v", err)
	}
	trimmed, err := fresh.Inspect(context.Background(), file, voices.Cut{From: time.Second, To: 4 * time.Second})
	if err != nil || trimmed.Seconds != 3 || voices.ClipIDOf(trimmed.FLAC) == voices.ClipIDOf(clip.FLAC) {
		t.Fatalf("the trim is %v s, %v; want 3 s of another clip", trimmed.Seconds, err)
	}
}

func TestRunner_ScriptedFailures(t *testing.T) {
	runner := voicestest.NewRunner()
	runner.Missing("ffprobe")
	_, ffmpeg := runner.LookPath("ffmpeg")
	_, ffprobe := runner.LookPath("ffprobe")
	_, _, sox := runner.Run(context.Background(), "sox", "in.wav")
	if calls := runner.Calls(); ffmpeg != nil || ffprobe == nil || sox == nil || !reflect.DeepEqual(calls, []voicestest.Call{{Name: "sox", Args: []string{"in.wav"}}}) {
		t.Fatalf("ffmpeg: %v, ffprobe after Missing: %v, sox: %v, calls %v", ffmpeg, ffprobe, sox, calls)
	}
	runner.Fail("notes.txt", "Invalid data found when processing input")
	runner.Hang("hang.wav")
	runner.Serve("meeting.wav", voicestest.Speech(4))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := runner.Run(ctx, "ffmpeg", "-i", "file:hang.wav"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Hang: %v, want the context's end", err)
	}
	filter := "aresample=16000,aformat=sample_fmts=s16:channel_layouts=mono,atrim=start_sample="
	for stderr, args := range map[string][]string{
		"Invalid data found when processing input\n": {"-i", "file:notes.txt", "-af", filter + "0", "pipe:1"},
		"absent.wav: No such file or directory\n":    {"-i", "file:absent.wav", "-af", filter + "0", "pipe:1"},
		"bad atrim option start_sample=x\n":          {"-i", "file:meeting.wav", "-af", filter + "x", "pipe:1"},
		"":                                           {"-i", "file:meeting.wav", "-af", filter + "0", "file:" + filepath.Join(t.TempDir(), "absent", "clip.flac")},
	} {
		if stdout, got, err := runner.Run(context.Background(), "ffmpeg", args...); err == nil || stdout != "" || stderr != "" && got != stderr {
			t.Fatalf("ffmpeg %v answered %q, %q, %v; want the error %q", args, stdout, got, err, stderr)
		}
	}
	if stdout, _, err := runner.Run(context.Background(), "ffmpeg", "-i", "file:meeting.wav", "-af", filter+"64000:end_sample=99999999", "pipe:1"); err != nil || stdout != "" {
		t.Fatalf("a trim past the end: %d bytes, %v; want none", len(stdout), err)
	}
}
