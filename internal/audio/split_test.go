package audio

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func sparseFile(t *testing.T, size int64) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "input.wav")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("failed to create sparse file: %v", err)
	}
	if err := f.Truncate(size); err != nil {
		t.Fatalf("failed to truncate sparse file: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("failed to close sparse file: %v", err)
	}
	return path
}

type fakeTranscriber struct {
	mu          sync.Mutex
	calls       []string
	inFlight    atomic.Int64
	maxInFlight atomic.Int64
	delay       time.Duration
	// transcribe overrides the default per-path segment response when set
	transcribe func(ctx context.Context, filePath string) ([]Segment, error)
}

func (f *fakeTranscriber) Transcribe(ctx context.Context, filePath string) ([]Segment, error) {
	current := f.inFlight.Add(1)
	defer f.inFlight.Add(-1)
	for {
		max := f.maxInFlight.Load()
		if current <= max || f.maxInFlight.CompareAndSwap(max, current) {
			break
		}
	}
	f.mu.Lock()
	f.calls = append(f.calls, filePath)
	f.mu.Unlock()
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	if f.transcribe != nil {
		return f.transcribe(ctx, filePath)
	}
	return []Segment{
		{Start: 5 * time.Second, End: 6 * time.Second, Text: filepath.Base(filePath)},
	}, nil
}

func (f *fakeTranscriber) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

type fakeRunner struct {
	mu           sync.Mutex
	calls        [][]string
	lookPathErrs map[string]error
	// durations keys ffprobe stdout by target file base name; defaultDur is the fallback
	durations  map[string]string
	defaultDur string
	chunkCount int
	ffmpegErr  error
	ffmpegOut  string
	blockOnRun bool
	chunkDir   string
}

func (f *fakeRunner) LookPath(name string) (string, error) {
	if err := f.lookPathErrs[name]; err != nil {
		return "", err
	}
	return "/usr/bin/" + name, nil
}

func (f *fakeRunner) Run(ctx context.Context, name string, args ...string) (string, string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string{name}, args...))
	f.mu.Unlock()
	if f.blockOnRun {
		<-ctx.Done()
		return "", "", ctx.Err()
	}
	switch name {
	case "ffprobe":
		target := filepath.Base(args[len(args)-1])
		if d, ok := f.durations[target]; ok {
			return d, "", nil
		}
		return f.defaultDur, "", nil
	case "ffmpeg":
		if f.ffmpegErr != nil {
			return "", f.ffmpegOut, f.ffmpegErr
		}
		pattern := args[len(args)-1]
		f.mu.Lock()
		f.chunkDir = filepath.Dir(pattern)
		f.mu.Unlock()
		for i := range f.chunkCount {
			if err := os.WriteFile(fmt.Sprintf(pattern, i), []byte("chunk"), 0o644); err != nil {
				return "", "", err
			}
		}
	}
	return "", "", nil
}

func (f *fakeRunner) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeRunner) tempDir() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.chunkDir
}

// newOversizedSplitter wires a 60 MB input against a 3-chunk happy-path fake:
// total 1800 s, chunks of 600 s each.
func newOversizedSplitter(t *testing.T) (*Splitter, *fakeTranscriber, *fakeRunner, *bytes.Buffer, string) {
	t.Helper()
	trans := &fakeTranscriber{}
	runner := &fakeRunner{
		defaultDur: "600.000000",
		durations:  map[string]string{"input.wav": "1800.000000"},
		chunkCount: 3,
	}
	statusOut := &bytes.Buffer{}
	splitter := NewSplitter(trans, runner)
	splitter.StatusOut = statusOut
	return splitter, trans, runner, statusOut, sparseFile(t, 60<<20)
}

func TestSplitter_SubCapBypassesSplit(t *testing.T) {
	trans := &fakeTranscriber{}
	runner := &fakeRunner{}
	splitter := NewSplitter(trans, runner)
	splitter.StatusOut = &bytes.Buffer{}
	input := sparseFile(t, 10<<20)

	got, err := splitter.Transcribe(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if runner.callCount() != 0 {
		t.Errorf("expected zero runner calls for sub-cap file, got: %v", runner.calls)
	}
	if trans.callCount() != 1 || trans.calls[0] != input {
		t.Errorf("expected single transcription of original file, got: %v", trans.calls)
	}
	if got[0].Start != 5*time.Second {
		t.Errorf("expected unmodified timestamps, got: %v", got[0].Start)
	}
}

func TestSplitter_OversizedSplitsAndStitches(t *testing.T) {
	splitter, _, runner, statusOut, input := newOversizedSplitter(t)

	origStdout := os.Stdout
	pipeRead, pipeWrite, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create stdout capture pipe: %v", err)
	}
	os.Stdout = pipeWrite

	got, transcribeErr := splitter.Transcribe(context.Background(), input)

	os.Stdout = origStdout
	pipeWrite.Close()
	stdoutContent, _ := io.ReadAll(pipeRead)
	if transcribeErr != nil {
		t.Fatalf("unexpected error: %v", transcribeErr)
	}
	if len(stdoutContent) != 0 {
		t.Errorf("expected nothing on stdout, got: %q", stdoutContent)
	}

	if len(got) != 3 {
		t.Fatalf("expected 3 stitched segments, got %v: %+v", len(got), got)
	}
	wantOffsets := []time.Duration{0, 600 * time.Second, 1200 * time.Second}
	for i, offset := range wantOffsets {
		wantStart := offset + 5*time.Second
		if got[i].Start != wantStart {
			t.Errorf("segment %v: expected start %v, got %v", i, wantStart, got[i].Start)
		}
		wantText := fmt.Sprintf("chunk_%03d.wav", i)
		if got[i].Text != wantText {
			t.Errorf("segment %v: expected text %q (in-order stitch), got %q", i, wantText, got[i].Text)
		}
	}

	vtt, err := Render(got, FormatVTT)
	if err != nil {
		t.Fatalf("unexpected render error: %v", err)
	}
	if !strings.Contains(vtt, "00:20:05.000") {
		t.Errorf("expected chunk 3 local 00:00:05 to render at 00:20:05, got:\n%v", vtt)
	}

	status := statusOut.String()
	if !strings.Contains(status, "splitting via ffmpeg") || !strings.Contains(status, "3 chunks") {
		t.Errorf("expected split notice on status writer, got: %q", status)
	}
	if _, err := os.Stat(runner.tempDir()); !os.IsNotExist(err) {
		t.Errorf("expected temp chunk dir to be removed, stat err: %v", err)
	}
}

func TestSplitter_ParallelismBounded(t *testing.T) {
	splitter, trans, _, _, input := newOversizedSplitter(t)
	splitter.Parallelism = 2
	trans.delay = 30 * time.Millisecond

	if _, err := splitter.Transcribe(context.Background(), input); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if trans.callCount() != 3 {
		t.Fatalf("expected 3 chunk transcriptions, got %v", trans.callCount())
	}
	if max := trans.maxInFlight.Load(); max > 2 {
		t.Errorf("expected at most 2 in-flight transcriptions, observed %v", max)
	}
}

func TestSplitter_ChunkFailureCancelsSiblings(t *testing.T) {
	splitter, trans, runner, _, input := newOversizedSplitter(t)
	splitter.Parallelism = 3
	trans.transcribe = func(ctx context.Context, filePath string) ([]Segment, error) {
		if strings.Contains(filePath, "chunk_001") {
			return nil, errors.New("boom on chunk 1")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Second):
			return nil, errors.New("sibling was never cancelled")
		}
	}

	start := time.Now()
	_, err := splitter.Transcribe(context.Background(), input)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "boom on chunk 1") {
		t.Errorf("expected first error propagated, got: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("expected prompt cancellation of siblings, took %v", elapsed)
	}
	if _, statErr := os.Stat(runner.tempDir()); !os.IsNotExist(statErr) {
		t.Errorf("expected temp chunk dir removed on error, stat err: %v", statErr)
	}
}

func TestSplitter_DiarizeWarning(t *testing.T) {
	splitter, _, _, statusOut, input := newOversizedSplitter(t)
	splitter.Model = "gpt-4o-transcribe-diarize"

	if _, err := splitter.Transcribe(context.Background(), input); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(statusOut.String(), "speaker labels") {
		t.Errorf("expected speaker-label drift warning, got: %q", statusOut.String())
	}
}

func TestSplitter_NoDiarizeWarningForPlainModel(t *testing.T) {
	splitter, _, _, statusOut, input := newOversizedSplitter(t)
	splitter.Model = "whisper-1"

	if _, err := splitter.Transcribe(context.Background(), input); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(statusOut.String(), "speaker labels") {
		t.Errorf("unexpected diarize warning for non-diarize model: %q", statusOut.String())
	}
}

func TestSplitter_ChunkDriftWarning(t *testing.T) {
	splitter, _, runner, statusOut, input := newOversizedSplitter(t)
	// Actual chunk durations 660 s vs planned 600 s: chunk 1 starts 60 s late
	runner.defaultDur = "660.000000"

	if _, err := splitter.Transcribe(context.Background(), input); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(statusOut.String(), "drift") {
		t.Errorf("expected drift warning, got: %q", statusOut.String())
	}
}

func TestSplitter_MissingBinary(t *testing.T) {
	for _, missing := range []string{"ffmpeg", "ffprobe"} {
		t.Run(missing, func(t *testing.T) {
			splitter, trans, runner, _, input := newOversizedSplitter(t)
			runner.lookPathErrs = map[string]error{missing: errors.New("not found")}

			_, err := splitter.Transcribe(context.Background(), input)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), missing) {
				t.Errorf("expected error naming %v, got: %v", missing, err)
			}
			if !strings.Contains(err.Error(), "ffmpeg -i") {
				t.Errorf("expected manual split command hint, got: %v", err)
			}
			if trans.callCount() != 0 {
				t.Errorf("expected no transcription requests, got: %v", trans.calls)
			}
		})
	}
}

func TestSplitter_FfmpegFails(t *testing.T) {
	splitter, _, runner, _, input := newOversizedSplitter(t)
	runner.ffmpegErr = errors.New("exit status 1")
	runner.ffmpegOut = "Invalid data found when processing input"

	_, err := splitter.Transcribe(context.Background(), input)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "Invalid data found") {
		t.Errorf("expected ffmpeg stderr in error, got: %v", err)
	}
	if dir := runner.tempDir(); dir != "" {
		if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
			t.Errorf("expected temp dir removed, stat err: %v", statErr)
		}
	}
}

func TestSplitter_ZeroChunks(t *testing.T) {
	splitter, _, runner, _, input := newOversizedSplitter(t)
	runner.chunkCount = 0

	_, err := splitter.Transcribe(context.Background(), input)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "no chunks") {
		t.Errorf("expected explicit zero-chunk error, got: %v", err)
	}
}

func TestSplitter_CtxCancelledDuringSplit(t *testing.T) {
	splitter, _, runner, _, input := newOversizedSplitter(t)
	runner.blockOnRun = true
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := splitter.Transcribe(ctx, input)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled in chain, got: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("expected prompt abort, took %v", elapsed)
	}
}

func TestSplitter_InputFileMissing(t *testing.T) {
	splitter, _, _, _, _ := newOversizedSplitter(t)
	_, err := splitter.Transcribe(context.Background(), filepath.Join(t.TempDir(), "gone.wav"))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestExecRunner(t *testing.T) {
	runner := ExecRunner{}
	t.Run("lookpath finds existing binary", func(t *testing.T) {
		if _, err := runner.LookPath("sh"); err != nil {
			t.Errorf("expected sh on PATH, got: %v", err)
		}
	})
	t.Run("lookpath errors on missing binary", func(t *testing.T) {
		if _, err := runner.LookPath("clai-definitely-not-a-binary"); err == nil {
			t.Error("expected error, got nil")
		}
	})
	t.Run("run captures stdout and stderr", func(t *testing.T) {
		stdout, stderr, err := runner.Run(context.Background(), "sh", "-c", "echo out; echo err >&2")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if strings.TrimSpace(stdout) != "out" {
			t.Errorf("expected stdout 'out', got: %q", stdout)
		}
		if strings.TrimSpace(stderr) != "err" {
			t.Errorf("expected stderr 'err', got: %q", stderr)
		}
	})
	t.Run("run returns error on non-zero exit", func(t *testing.T) {
		if _, _, err := runner.Run(context.Background(), "sh", "-c", "exit 3"); err == nil {
			t.Error("expected error, got nil")
		}
	})
}
