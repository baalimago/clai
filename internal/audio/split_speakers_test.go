package audio

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/audio/generic"
)

var errNotFound = errors.New("not found")

// twoSpeakers answers every request with the request-local labels A and B.
func twoSpeakers(_ context.Context, filePath string) ([]Segment, error) {
	return []Segment{
		{Start: time.Second, End: 2 * time.Second, Speaker: "A", Text: filepath.Base(filePath) + " first"},
		{Start: 3 * time.Second, End: 4 * time.Second, Speaker: "B", Text: filepath.Base(filePath) + " second"},
	}, nil
}

func newRequestLocalSplitter(t *testing.T, encodedBytes int64) (*Splitter, *fakeTranscriber, *fakeRunner, *bytes.Buffer, string) {
	t.Helper()
	trans := &fakeTranscriber{transcribe: twoSpeakers, diarization: generic.DiarizationRequestLocal}
	runner := &fakeRunner{
		defaultDur:   "600.000000",
		durations:    map[string]string{"input.mp3": "1800.000000"},
		chunkCount:   3,
		encodedBytes: encodedBytes,
	}
	status := &bytes.Buffer{}
	s := NewSplitter(trans, runner)
	s.StatusOut = status
	s.MaxBytes = 25 << 20
	return s, trans, runner, status, sparseFile(t, 60<<20)
}

func ranWith(runner *fakeRunner, arg string) bool {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return slices.ContainsFunc(runner.calls, func(c []string) bool { return slices.Contains(c, arg) })
}

func TestSplitter_RequestLocal_EncodesAnOversizedFileIntoOneRequest(t *testing.T) {
	s, trans, runner, _, input := newRequestLocalSplitter(t, 20<<20)

	got, err := s.Transcribe(context.Background(), input)
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if trans.callCount() != 1 || filepath.Base(trans.calls[0]) != "input.mp3" {
		t.Fatalf("requests = %v, want one request with the encoded file", trans.calls)
	}
	if !ranWith(runner, "libmp3lame") || !ranWith(runner, "64000") {
		t.Errorf("runner calls = %v, want a 64 kbit/s mp3 encode", runner.calls)
	}
	if ranWith(runner, "silencedetect") {
		t.Errorf("runner calls = %v, request-local labels must not calibrate", runner.calls)
	}
	if got[0].Speaker != "A" || got[1].Speaker != "B" {
		t.Errorf("speakers = %q %q, want the one request's labels unchanged", got[0].Speaker, got[1].Speaker)
	}
	if _, err := os.Stat(trans.calls[0]); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("encoded request %v left behind: %v", trans.calls[0], err)
	}
}

func TestSplitter_RequestLocal_ScopesLabelsPerChunk(t *testing.T) {
	s, trans, _, status, input := newRequestLocalSplitter(t, 50<<20)

	got, err := s.Transcribe(context.Background(), input)
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if trans.callCount() != 3 {
		t.Fatalf("requests = %v, want one per chunk of the encoded file", trans.calls)
	}
	var speakers []string
	for _, seg := range got {
		speakers = append(speakers, seg.Speaker)
	}
	if want := []string{"1-A", "1-B", "2-A", "2-B", "3-A", "3-B"}; !slices.Equal(speakers, want) {
		t.Fatalf("speakers = %v, want %v: a label never spans two requests", speakers, want)
	}
	if got[2].Start != 600333*time.Millisecond+time.Second {
		t.Errorf("chunk 2 starts at %v, want its window offset", got[2].Start)
	}
	if !strings.Contains(status.String(), "speaker labels per chunk") {
		t.Errorf("status = %q, want the per-chunk label notice", status.String())
	}
}

func TestSplitter_RequestLocal_SubCapFileIsSentAsIs(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ffprobe error
	}{
		{"probed shorter than one request holds", nil},
		{"no ffprobe to probe with", errNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, trans, runner, _, _ := newRequestLocalSplitter(t, 1)
			runner.lookPathErrs = map[string]error{"ffprobe": tc.ffprobe}
			input := sparseFile(t, 10<<20)

			if _, err := s.Transcribe(context.Background(), input); err != nil {
				t.Fatalf("Transcribe: %v", err)
			}
			if trans.callCount() != 1 || trans.calls[0] != input {
				t.Errorf("requests = %v, want the original file once", trans.calls)
			}
			if ranWith(runner, "libmp3lame") || ranWith(runner, "segment") {
				t.Errorf("runner calls = %v, want no encode and no split", runner.calls)
			}
		})
	}
}

// TestSplitter_RequestLocal_LongSubCapFileIsEncodedAndSplit pins the request-local duration bound (architecture/audio.md).
func TestSplitter_RequestLocal_LongSubCapFileIsEncodedAndSplit(t *testing.T) {
	s, trans, runner, _, _ := newRequestLocalSplitter(t, 50<<20)
	runner.durations["input.wav"] = "7200.000000"
	input := sparseFile(t, 10<<20)

	got, err := s.Transcribe(context.Background(), input)
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if !ranWith(runner, "libmp3lame") || trans.callCount() != 3 {
		t.Fatalf("requests = %v, runner = %v, want the file encoded and split", trans.calls, runner.calls)
	}
	if got[0].Speaker != "1-A" {
		t.Errorf("speaker = %q, want labels scoped per chunk", got[0].Speaker)
	}
}

func TestRequestCodecDuration(t *testing.T) {
	if got := requestCodecDuration(MaxRequestBytes); got != 3276800*time.Millisecond {
		t.Fatalf("requestCodecDuration(25 MiB) = %v, want 54m36.8s at 64 kbit/s", got)
	}
}

func TestSplitter_ClampNeverEndsACueBeforeItStarts(t *testing.T) {
	splitter, trans, _, _, input := newOversizedSplitter(t)
	trans.transcribe = func(context.Context, string) ([]Segment, error) {
		return []Segment{{Start: 650 * time.Second, End: 660 * time.Second, Text: "past the window"}}, nil
	}

	got, err := splitter.Transcribe(context.Background(), input)
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	for i, seg := range got {
		if seg.End < seg.Start {
			t.Errorf("segment %v ends at %v before it starts at %v", i, seg.End, seg.Start)
		}
	}
}

func TestSplitter_RequestLocal_Failures(t *testing.T) {
	t.Run("missing ffmpeg names the binary", func(t *testing.T) {
		s, trans, runner, _, input := newRequestLocalSplitter(t, 20<<20)
		runner.lookPathErrs = map[string]error{"ffmpeg": errNotFound}
		_, err := s.Transcribe(context.Background(), input)
		if err == nil || !strings.Contains(err.Error(), "ffmpeg") {
			t.Fatalf("err = %v, want it to name ffmpeg", err)
		}
		if trans.callCount() != 0 {
			t.Errorf("requests = %v, want none", trans.calls)
		}
	})
	t.Run("a failed encode names ffmpeg's reason", func(t *testing.T) {
		s, trans, runner, _, input := newRequestLocalSplitter(t, 20<<20)
		runner.ffmpegErr, runner.ffmpegOut = errNotFound, "Unknown encoder 'libmp3lame'"
		_, err := s.Transcribe(context.Background(), input)
		if err == nil || !strings.Contains(err.Error(), "Unknown encoder") {
			t.Fatalf("err = %v, want ffmpeg's stderr", err)
		}
		if trans.callCount() != 0 {
			t.Errorf("requests = %v, want none", trans.calls)
		}
	})
	t.Run("a cancelled encode reports the cancellation", func(t *testing.T) {
		s, _, runner, _, input := newRequestLocalSplitter(t, 20<<20)
		runner.blockOnRun = true
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := s.Transcribe(ctx, input)
		if err == nil || !strings.Contains(err.Error(), "aborted") {
			t.Fatalf("err = %v, want an aborted encode", err)
		}
	})
}

func TestSplitter_AnchoredDiarizationCalibrates(t *testing.T) {
	p := &voiceProvider{truth: smallMeeting()}
	s, status, input := newCalibratedSplitter(t, p, smallBudgetConf("anything"))
	if _, err := s.Transcribe(context.Background(), input); err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if !strings.Contains(status.String(), "▸ calibrated diarization") || p.requests() == 0 {
		t.Errorf("status = %q, want the calibration path whatever the model is named", status.String())
	}
}

func TestSplitter_ModelNameAloneDoesNotCalibrate(t *testing.T) {
	splitter, trans, runner, _, input := newOversizedSplitter(t)
	splitter.Model = "or:vendor/some-diarize-model"

	if _, err := splitter.Transcribe(context.Background(), input); err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if trans.callCount() != 3 || ranWith(runner, "silencedetect") {
		t.Errorf("requests = %v, runner = %v, want the plain split for a transcriber without anchored labels", trans.calls, runner.calls)
	}
}

// TestSplitter_SegmentTimeEndsPastTheProbedDuration pins the split slack (architecture/audio.md).
func TestSplitter_SegmentTimeEndsPastTheProbedDuration(t *testing.T) {
	splitter, _, runner, _, input := newOversizedSplitter(t)

	if _, err := splitter.Transcribe(context.Background(), input); err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	var split []string
	for _, c := range runner.calls {
		if slices.Contains(c, "segment") {
			split = c
		}
	}
	i := slices.Index(split, "-segment_time")
	if i < 0 || split[i+1] != "600.333" {
		t.Fatalf("split call = %v, want -segment_time 600.333 for 1800 s in 3 chunks", split)
	}
}
