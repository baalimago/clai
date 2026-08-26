package audio

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

const (
	// MaxRequestBytes is the vendor single-request size cap (OpenAI: 25 MB)
	MaxRequestBytes  = 25 << 20
	targetChunkBytes = 20 << 20
	ffprobeBin       = "ffprobe"
	ffmpegBin        = "ffmpeg"
)

type Transcriber interface {
	Transcribe(ctx context.Context, filePath string) ([]Segment, error)
}

// CommandRunner abstracts external binary invocation so the split path is
// testable without ffmpeg/ffprobe installed.
type CommandRunner interface {
	LookPath(name string) (string, error)
	Run(ctx context.Context, name string, args ...string) (stdout, stderr string, err error)
}

// ExecRunner is the production CommandRunner backed by os/exec.
type ExecRunner struct{}

func (ExecRunner) LookPath(name string) (string, error) {
	return exec.LookPath(name)
}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// Splitter transparently chunks oversized audio files via ffmpeg, transcribes
// the chunks with bounded parallelism and stitches the offset segments.
// Sub-cap files pass straight through to the inner Transcriber.
type Splitter struct {
	Transcriber Transcriber
	Runner      CommandRunner
	Parallelism int
	MaxBytes    int64
	Model       string
	StatusOut   io.Writer
}

func NewSplitter(transcriber Transcriber, runner CommandRunner) *Splitter {
	return &Splitter{
		Transcriber: transcriber,
		Runner:      runner,
		Parallelism: 3,
		MaxBytes:    MaxRequestBytes,
		StatusOut:   os.Stderr,
	}
}

func (s *Splitter) Transcribe(ctx context.Context, filePath string) ([]Segment, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat audio file: %w", err)
	}
	maxBytes := s.MaxBytes
	if maxBytes <= 0 {
		maxBytes = MaxRequestBytes
	}
	if info.Size() <= maxBytes {
		return s.Transcriber.Transcribe(ctx, filePath)
	}
	return s.splitTranscribeStitch(ctx, filePath, info.Size(), maxBytes)
}

// status writes a line to StatusOut (stderr in production), keeping stdout
// reserved for the rendered transcript
func (s *Splitter) status(mu *sync.Mutex, msg string) {
	out := s.StatusOut
	if out == nil {
		out = os.Stderr
	}
	mu.Lock()
	defer mu.Unlock()
	fmt.Fprintln(out, msg)
}

func (s *Splitter) notice(mu *sync.Mutex, msg string) {
	if ancli.UseColor {
		msg = ancli.ColoredMessage(ancli.CYAN, msg)
	}
	s.status(mu, msg)
}

func (s *Splitter) warn(mu *sync.Mutex, msg string) {
	if ancli.UseColor {
		msg = ancli.ColoredMessage(ancli.YELLOW, msg)
	}
	s.status(mu, msg)
}

func (s *Splitter) chunkDone(mu *sync.Mutex, msg string) {
	if ancli.UseColor {
		msg = ancli.ColoredMessage(ancli.GREEN, msg)
	}
	s.status(mu, msg)
}

func (s *Splitter) splitTranscribeStitch(ctx context.Context, filePath string, size, maxBytes int64) ([]Segment, error) {
	var statusMu sync.Mutex
	for _, bin := range []string{ffmpegBin, ffprobeBin} {
		if _, err := s.Runner.LookPath(bin); err != nil {
			return nil, fmt.Errorf("'%v' is required to transcribe files over %.0f MB, but it wasn't found: %w. Install it, or split the file manually: 'ffmpeg -i %v -f segment -segment_time 600 -c copy chunk_%%03d%v'",
				bin, toMB(maxBytes), err, filePath, filepath.Ext(filePath))
		}
	}
	duration, err := s.probeDuration(ctx, filePath)
	if err != nil {
		return nil, err
	}
	numChunks := int(math.Ceil(float64(size) / float64(targetChunkBytes)))
	segmentTime := duration / float64(numChunks)
	s.notice(&statusMu, fmt.Sprintf("%v is %.1f MB (> %.0f MB limit) → splitting via ffmpeg: %v chunks × ~%.1f min",
		filePath, toMB(size), toMB(maxBytes), numChunks, segmentTime/60))
	if strings.Contains(s.Model, "diarize") {
		s.warn(&statusMu, "diarization speaker labels are per-request: chunk 1's 'A' may not be chunk 2's 'A', labels may drift across chunks")
	}

	tempDir, err := os.MkdirTemp("", "clai-audio-split-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create chunk temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)
	chunks, err := s.split(ctx, filePath, tempDir, segmentTime)
	if err != nil {
		return nil, err
	}
	s.verifyChunkOffsets(ctx, chunks, segmentTime, &statusMu)
	return s.transcribePool(ctx, chunks, segmentTime, &statusMu)
}

func (s *Splitter) probeDuration(ctx context.Context, filePath string) (float64, error) {
	stdout, stderr, err := s.Runner.Run(ctx, ffprobeBin,
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		filePath)
	if err != nil {
		if ctx.Err() != nil {
			return 0, fmt.Errorf("ffprobe aborted: %w", ctx.Err())
		}
		return 0, fmt.Errorf("ffprobe failed on %v: %w, stderr: %v", filePath, err, tail(stderr))
	}
	duration, err := strconv.ParseFloat(strings.TrimSpace(stdout), 64)
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("failed to parse ffprobe duration %q for %v: %v", strings.TrimSpace(stdout), filePath, err)
	}
	return duration, nil
}

func (s *Splitter) split(ctx context.Context, filePath, tempDir string, segmentTime float64) ([]string, error) {
	pattern := filepath.Join(tempDir, "chunk_%03d"+filepath.Ext(filePath))
	_, stderr, err := s.Runner.Run(ctx, ffmpegBin,
		"-v", "error",
		"-i", filePath,
		"-f", "segment",
		"-segment_time", strconv.FormatFloat(segmentTime, 'f', 3, 64),
		"-c", "copy",
		pattern)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("ffmpeg split aborted: %w", ctx.Err())
		}
		return nil, fmt.Errorf("ffmpeg failed to split %v: %w, stderr: %v", filePath, err, tail(stderr))
	}
	chunks, err := filepath.Glob(filepath.Join(tempDir, "chunk_*"))
	if err != nil {
		return nil, fmt.Errorf("failed to glob chunk files: %w", err)
	}
	if len(chunks) == 0 {
		return nil, fmt.Errorf("ffmpeg produced no chunks for %v", filePath)
	}
	return chunks, nil
}

// verifyChunkOffsets best-effort compares the planned i×segmentTime starts
// against ffprobe-measured chunk durations, warning once on drift > 1 s
func (s *Splitter) verifyChunkOffsets(ctx context.Context, chunks []string, segmentTime float64, statusMu *sync.Mutex) {
	measuredStart := 0.0
	for i, chunk := range chunks {
		planned := float64(i) * segmentTime
		if diff := math.Abs(measuredStart - planned); diff > 1 {
			s.warn(statusMu, fmt.Sprintf("chunk %v timestamp drift: planned start %.1f s, measured %.1f s (%.1f s drift), stitched timestamps may be off",
				i, planned, measuredStart, diff))
			return
		}
		stdout, _, err := s.Runner.Run(ctx, ffprobeBin,
			"-v", "error",
			"-show_entries", "format=duration",
			"-of", "default=noprint_wrappers=1:nokey=1",
			chunk)
		if err != nil {
			return
		}
		chunkDuration, err := strconv.ParseFloat(strings.TrimSpace(stdout), 64)
		if err != nil {
			return
		}
		measuredStart += chunkDuration
	}
}

func (s *Splitter) transcribePool(ctx context.Context, chunks []string, segmentTime float64, statusMu *sync.Mutex) ([]Segment, error) {
	poolCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	parallelism := s.Parallelism
	if parallelism <= 0 {
		parallelism = 3
	}
	sem := make(chan struct{}, parallelism)
	results := make([][]Segment, len(chunks))
	var wg sync.WaitGroup
	var errMu sync.Mutex
	var firstErr error
	for i, chunk := range chunks {
		wg.Add(1)
		go func(i int, chunk string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-poolCtx.Done():
				return
			}
			defer func() { <-sem }()
			segs, err := s.Transcriber.Transcribe(poolCtx, chunk)
			if err != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("failed to transcribe chunk %v/%v: %w", i+1, len(chunks), err)
				}
				errMu.Unlock()
				cancel()
				return
			}
			results[i] = Offset(segs, secondsToDuration(float64(i)*segmentTime))
			s.chunkDone(statusMu, fmt.Sprintf("chunk %v/%v transcribed", i+1, len(chunks)))
		}(i, chunk)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var stitched []Segment
	for _, segs := range results {
		stitched = append(stitched, segs...)
	}
	return stitched, nil
}

func toMB(bytes int64) float64 {
	return float64(bytes) / (1 << 20)
}

func tail(s string) string {
	const limit = 512
	if len(s) <= limit {
		return s
	}
	return "…" + s[len(s)-limit:]
}
