package audio

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/audio/generic"
	"github.com/baalimago/clai/internal/runenv"
)

// jsonTranscribe runs s on input under -json with no StatusOut and returns
// the progress records it wrote.
func jsonTranscribe(t *testing.T, s *Splitter, input string) ([]progressRecord, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	env := jsonRecordEnv(strings.NewReader(""), &stdout, &stderr)
	s.StatusOut = nil
	_, err := s.Transcribe(runenv.With(context.Background(), env), input)
	if closeErr := env.Streams().Close(); closeErr != nil {
		t.Fatalf("Close: %v", closeErr)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want nothing", stdout.String())
	}
	return transcribeProgress(t, stderr.String()), err
}

func states(records []progressRecord) []string {
	out := make([]string, 0, len(records))
	for _, r := range records {
		out = append(out, r.Row+"|"+r.State+"|"+r.Detail)
	}
	return out
}

// hearing answers like a streaming provider: it reports each end, then one segment.
func hearing(ends ...time.Duration) func(ctx context.Context, filePath string) ([]Segment, error) {
	return func(ctx context.Context, filePath string) ([]Segment, error) {
		for _, end := range ends {
			generic.ReportHeard(ctx, end)
		}
		return []Segment{{Start: 0, End: time.Second, Text: "hi"}}, nil
	}
}

// TestSplitter_OneRequestReportsItsProgress: a file sent whole gets a status,
// not a board, with how far the provider has got (architecture/audio.md, Progress).
func TestSplitter_OneRequestReportsItsProgress(t *testing.T) {
	trans := &fakeTranscriber{transcribe: hearing(1600*time.Millisecond, 1900*time.Millisecond, 75*time.Second)}
	s := NewSplitter(trans, &fakeRunner{})

	records, err := jsonTranscribe(t, s, sparseFile(t, 10<<20))
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	want := []string{
		"|start|input.wav · 1 request · 10.0 MB",
		"|update|heard 00:02",
		"|update|heard 01:15",
		"|done|transcribed · 1 segments",
	}
	if got := states(records); !slices.Equal(got, want) {
		t.Fatalf("records = %q, want %q", got, want)
	}
}

func TestSplitter_OneRequestThatFailsReportsWhy(t *testing.T) {
	trans := &fakeTranscriber{transcribe: func(context.Context, string) ([]Segment, error) {
		return nil, errors.New("transcription request failed with status: 401\nbody: nope")
	}}
	s := NewSplitter(trans, &fakeRunner{})

	records, err := jsonTranscribe(t, s, sparseFile(t, 1<<20))
	if err == nil {
		t.Fatal("Transcribe: no error, want the request's")
	}
	want := []string{"|start|input.wav · 1 request · 1.0 MB", "|failed|transcription request failed with status: 401"}
	if got := states(records); !slices.Equal(got, want) {
		t.Fatalf("records = %q, want %q", got, want)
	}
}

func TestSplitter_EncodedRequestReportsTheEncodeThenTheRequest(t *testing.T) {
	s, trans, _, _, input := newRequestLocalSplitter(t, 20<<20)
	trans.transcribe = hearing(90 * time.Second)

	records, err := jsonTranscribe(t, s, input)
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	want := []string{
		"|start|input.wav · 60.0 MB · encoding for 1 request",
		"|update|requesting · 20.0 MB",
		"|update|heard 01:30",
		"|done|transcribed · 1 segments",
	}
	if got := states(records); !slices.Equal(got, want) {
		t.Fatalf("records = %q, want %q", got, want)
	}
}

// TestSplitter_EncodedRequestTooBigHandsOverToTheBoard: the status stays open
// around the chunk board, so the run's last record ends the transcription.
func TestSplitter_EncodedRequestTooBigHandsOverToTheBoard(t *testing.T) {
	s, _, _, _, input := newRequestLocalSplitter(t, 50<<20)

	records, err := jsonTranscribe(t, s, input)
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	got := states(records)
	if got[0] != "|start|input.wav · 60.0 MB · encoding for 1 request" ||
		got[1] != "|update|encoded 50.0 MB, over the 25 MB request cap: splitting" ||
		got[len(got)-1] != "|done|transcribed · 6 segments" {
		t.Fatalf("records = %q, want the status around the board", got)
	}
	if !strings.Contains(got[2], "|start|splitting via ffmpeg  input.mp3") || !strings.Contains(got[2], "3 chunks") {
		t.Fatalf("record 2 = %q, want the board's start", got[2])
	}
}

func TestSplitter_EncodeFailureFailsTheStatus(t *testing.T) {
	s, _, runner, _, input := newRequestLocalSplitter(t, 20<<20)
	runner.ffmpegErr = errors.New("exit status 1")

	records, err := jsonTranscribe(t, s, input)
	if err == nil {
		t.Fatal("Transcribe: no error, want the encode's")
	}
	if got := states(records); len(got) != 2 || got[1] != "|failed|ffmpeg failed to encode request: exit status 1, stderr: " {
		t.Fatalf("records = %q, want start then the encode failure", got)
	}
}

func TestSplitter_OneRequestOffATerminalPrintsALinePerState(t *testing.T) {
	trans := &fakeTranscriber{transcribe: hearing(5 * time.Second)}
	s := NewSplitter(trans, &fakeRunner{})
	var status bytes.Buffer
	s.StatusOut = &status

	if _, err := s.Transcribe(context.Background(), sparseFile(t, 2<<20)); err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	want := "transcribing input.wav · 1 request · 2.0 MB\nheard 00:05\ntranscribed · 1 segments\n"
	if status.String() != want {
		t.Fatalf("status = %q, want %q", status.String(), want)
	}
}

func TestRequestStatus_LiveRowErasesItself(t *testing.T) {
	var out bytes.Buffer
	s := newLiveRequestStatus(&out, false, "input.wav · 1 request · 2.0 MB")
	s.heard(65 * time.Second)
	s.finish([]Segment{{}}, nil)
	s.update("after finish")
	drawn := out.String()
	if !strings.Contains(drawn, "transcribing") || !strings.Contains(drawn, "input.wav · 1 request · 2.0 MB") {
		t.Fatalf("drawn = %q, want the title and the first detail", drawn)
	}
	if !strings.HasSuffix(drawn, "\r\x1b[2K") {
		t.Fatalf("drawn = %q, want the row erased at the end", drawn)
	}
}

func TestMockTranscriber_DelayReportsEachSegmentAsItIsHeard(t *testing.T) {
	var heard []time.Duration
	ctx := generic.WithHeard(context.Background(), func(end time.Duration) { heard = append(heard, end) })
	start := time.Now()
	segs, err := MockTranscriber{Delay: 40 * time.Millisecond}.Transcribe(ctx, sparseFile(t, 8))
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 40*time.Millisecond {
		t.Errorf("took %v, want at least the 40ms delay", elapsed)
	}
	if want := []time.Duration{segs[0].End, segs[1].End}; !slices.Equal(heard, want) {
		t.Errorf("heard = %v, want %v", heard, want)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (MockTranscriber{Delay: time.Hour}).Transcribe(cancelled, sparseFile(t, 8)); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want the cancellation", err)
	}
}

func TestCreateSplitter_MockDelayFromTheEnvironment(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  time.Duration
		err   string
	}{
		{"", 0, ""},
		{"1500ms", 1500 * time.Millisecond, ""},
		{"soon", 0, "is not a duration"},
		{"-1s", 0, "must not be negative"},
	} {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv(mockDelayEnv, tc.value)
			s, err := createSplitter(audioConfWithModel("test"))
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) || !strings.Contains(err.Error(), mockDelayEnv) {
					t.Fatalf("err = %v, want one naming %v and %q", err, mockDelayEnv, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("createSplitter: %v", err)
			}
			if got := s.Transcriber.(MockTranscriber).Delay; got != tc.want {
				t.Fatalf("Delay = %v, want %v", got, tc.want)
			}
		})
	}
}
