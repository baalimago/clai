package audio

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/audio/generic"
)

// formatTranscriber is a fake that, like the vendor clients, reports the
// response format it negotiated.
type formatTranscriber struct {
	fakeTranscriber
	format generic.ResponseFormat
}

func (f *formatTranscriber) RequestFormat() generic.ResponseFormat { return f.format }

func newDurationSplitter(t *testing.T, format generic.ResponseFormat, inputSeconds string, maxSeconds int) (*Splitter, *formatTranscriber, *fakeRunner, *bytes.Buffer) {
	t.Helper()
	trans := &formatTranscriber{format: format}
	runner := &fakeRunner{
		defaultDur: "1000.000000",
		durations:  map[string]string{"input.wav": inputSeconds},
		chunkCount: 3,
	}
	statusOut := &bytes.Buffer{}
	splitter := NewSplitter(trans, runner)
	splitter.StatusOut = statusOut
	splitter.Budgets = Budgets{MaxRequestBytes: MaxRequestBytes, MaxRequestDuration: time.Duration(maxSeconds) * time.Second}
	return splitter, trans, runner, statusOut
}

func TestSplitter_JSONFormat_LongSubCapFileSplitsByDuration(t *testing.T) {
	splitter, trans, runner, statusOut := newDurationSplitter(t, generic.ResponseFormatJSON, "3000.000000", 1400)
	input := sparseFile(t, 10<<20)

	got, err := splitter.Transcribe(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if trans.callCount() != 3 {
		t.Fatalf("expected 3 chunk transcriptions for 3000 s under a 1400 s cap, got %v: %v", trans.callCount(), trans.calls)
	}
	wantOffsets := []time.Duration{0, 1000 * time.Second, 2000 * time.Second}
	for i, offset := range wantOffsets {
		if got[i].Start != offset+5*time.Second {
			t.Errorf("segment %v: expected start %v, got %v", i, offset+5*time.Second, got[i].Start)
		}
	}
	status := statusOut.String()
	if !strings.Contains(status, "1400 s") || !strings.Contains(status, "3 chunks") {
		t.Errorf("expected status to name the duration cap and chunk count, got: %q", status)
	}
	if _, err := os.Stat(runner.tempDir()); err == nil {
		t.Errorf("expected temp chunk dir to be removed")
	}
}

func TestSplitter_JSONFormat_ShortSubCapFileGoesDirect(t *testing.T) {
	splitter, trans, runner, _ := newDurationSplitter(t, generic.ResponseFormatJSON, "600.000000", 1400)
	input := sparseFile(t, 10<<20)

	got, err := splitter.Transcribe(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if trans.callCount() != 1 || trans.calls[0] != input {
		t.Errorf("expected one direct transcription, got: %v", trans.calls)
	}
	if runner.callCount() != 1 || runner.calls[0][0] != "ffprobe" {
		t.Errorf("expected exactly one ffprobe call, got: %v", runner.calls)
	}
	if got[0].Start != 5*time.Second {
		t.Errorf("expected unmodified timestamps, got: %v", got[0].Start)
	}
}

func TestSplitter_JSONFormat_WithoutFfprobePostsWhole(t *testing.T) {
	splitter, trans, runner, _ := newDurationSplitter(t, generic.ResponseFormatJSON, "3000.000000", 1400)
	runner.lookPathErrs = map[string]error{"ffprobe": errors.New("not found")}
	input := sparseFile(t, 10<<20)

	if _, err := splitter.Transcribe(context.Background(), input); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if trans.callCount() != 1 || trans.calls[0] != input {
		t.Errorf("expected the whole file to be posted when ffprobe is missing, got: %v", trans.calls)
	}
	if runner.callCount() != 0 {
		t.Errorf("expected no runner invocations without ffprobe, got: %v", runner.calls)
	}
}

func TestSplitter_JSONFormat_ProbeFailureIsAnError(t *testing.T) {
	splitter, trans, _, _ := newDurationSplitter(t, generic.ResponseFormatJSON, "not-a-number", 1400)
	input := sparseFile(t, 10<<20)

	_, err := splitter.Transcribe(context.Background(), input)
	if err == nil {
		t.Fatal("expected error when ffprobe output cannot be parsed, got nil")
	}
	if !strings.Contains(err.Error(), "not-a-number") {
		t.Errorf("expected error to carry the ffprobe output, got: %v", err)
	}
	if trans.callCount() != 0 {
		t.Errorf("expected no transcription after a probe failure, got: %v", trans.calls)
	}
}

func TestSplitter_VerboseFormat_IgnoresDuration(t *testing.T) {
	splitter, trans, runner, _ := newDurationSplitter(t, generic.ResponseFormatVerboseJSON, "9000.000000", 1400)
	input := sparseFile(t, 10<<20)

	if _, err := splitter.Transcribe(context.Background(), input); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if runner.callCount() != 0 {
		t.Errorf("expected no probe for a format without a duration cap, got: %v", runner.calls)
	}
	if trans.callCount() != 1 {
		t.Errorf("expected one direct transcription, got: %v", trans.calls)
	}
}

func TestSplitter_JSONFormat_ZeroBudgetUsesDefaultCap(t *testing.T) {
	splitter, trans, _, _ := newDurationSplitter(t, generic.ResponseFormatJSON, "3000.000000", 0)
	splitter.Budgets = Budgets{}
	input := sparseFile(t, 10<<20)

	if _, err := splitter.Transcribe(context.Background(), input); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if trans.callCount() != 3 {
		t.Errorf("expected the default 1400 s cap to split 3000 s into 3 chunks, got %v", trans.callCount())
	}
}

func TestSplitter_JSONFormat_OversizedUsesTighterOfBytesAndDuration(t *testing.T) {
	splitter, trans, runner, _ := newDurationSplitter(t, generic.ResponseFormatJSON, "1800.000000", 500)
	runner.chunkCount = 4
	input := sparseFile(t, 60<<20)

	got, err := splitter.Transcribe(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// bytes alone give 3 chunks; 1800 s under a 500 s cap needs 4
	if trans.callCount() != 4 || len(got) != 4 {
		t.Errorf("expected 4 chunks, got %v calls and %v segments", trans.callCount(), len(got))
	}
	if got[3].Start != 1350*time.Second+5*time.Second {
		t.Errorf("expected 4th chunk offset 1350 s, got start %v", got[3].Start)
	}
}

func TestSplitter_ClampsChunkEndToWindow(t *testing.T) {
	splitter, trans, _, _ := newDurationSplitter(t, generic.ResponseFormatJSON, "1800.000000", 600)
	// A billed duration rounded up past the window, as gpt-transcribe reports
	trans.transcribe = func(_ context.Context, _ string) ([]Segment, error) {
		return []Segment{{Start: 0, End: 601 * time.Second, Text: "chunk"}}, nil
	}
	input := sparseFile(t, 60<<20)

	got, err := splitter.Transcribe(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for i := range got {
		wantEnd := time.Duration(i+1) * 600 * time.Second
		if got[i].End != wantEnd {
			t.Errorf("segment %v: expected end clamped to window %v, got %v", i, wantEnd, got[i].End)
		}
		if i > 0 && got[i].Start < got[i-1].End {
			t.Errorf("segment %v starts %v before segment %v ends %v", i, got[i].Start, i-1, got[i-1].End)
		}
	}
}
