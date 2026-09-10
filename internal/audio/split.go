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
	"time"

	"github.com/baalimago/clai/internal/audio/generic"
	"github.com/baalimago/clai/internal/board"
	"github.com/baalimago/clai/internal/utils"
)

const (
	// MaxRequestBytes is the default per-request upload cap (OpenAI: 25 MB)
	MaxRequestBytes = 25 << 20
	// nonDiarizedChunkRatio sizes plain split chunks against the byte cap
	nonDiarizedChunkRatio = 0.8
	ffprobeBin            = "ffprobe"
	ffmpegBin             = "ffmpeg"
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
	// Calibrated diarization (oversized files with a diarize model)
	Budgets Budgets
	Strict  bool
	// RequestTranscriber overrides the file-based Transcriber for
	// calibrated requests; nil adapts Transcriber
	RequestTranscriber  RequestTranscriber
	MaxRequestsPerChunk int
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
	if strings.Contains(s.Model, "diarize") {
		return s.calibrate(ctx, filePath, info.Size(), maxBytes)
	}
	return s.splitTranscribeStitch(ctx, filePath, info.Size(), maxBytes)
}

// calibrate routes an oversized diarized file through the coordinator.
func (s *Splitter) calibrate(ctx context.Context, filePath string, size, maxBytes int64) ([]Segment, error) {
	if err := s.requireBinaries(filePath, maxBytes); err != nil {
		return nil, err
	}
	budgets := s.Budgets
	if budgets.MaxRequestBytes == 0 {
		b, err := ResolveBudgets(Default.Transcribe)
		if err != nil {
			return nil, err
		}
		budgets = b
	}
	budgets.MaxRequestBytes = maxBytes
	rt := s.RequestTranscriber
	if rt == nil {
		rt = FileTranscriber{s.Transcriber}
	}
	endpoint := ""
	if e, ok := s.Transcriber.(interface{ Endpoint() string }); ok {
		endpoint = e.Endpoint()
	}
	c := &Coordinator{
		Runner:              s.Runner,
		Budgets:             budgets,
		Transcriber:         rt,
		Parallelism:         s.Parallelism,
		Strict:              s.Strict,
		MaxRequestsPerChunk: s.MaxRequestsPerChunk,
		Model:               s.Model,
		Endpoint:            endpoint,
		Options:             "diarized_json",
		StatusOut:           s.StatusOut,
	}
	return c.Run(ctx, filePath)
}

func (s *Splitter) requireBinaries(filePath string, maxBytes int64) error {
	for _, bin := range []string{ffmpegBin, ffprobeBin} {
		if _, err := s.Runner.LookPath(bin); err != nil {
			return fmt.Errorf("'%v' is required to transcribe files over %.0f MB, but it wasn't found: %w. Install it, or split the file manually: 'ffmpeg -i %v -f segment -segment_time 600 -c copy chunk_%%03d%v'",
				bin, toMB(maxBytes), err, filePath, filepath.Ext(filePath))
		}
	}
	return nil
}

func (s *Splitter) splitTranscribeStitch(ctx context.Context, filePath string, size, maxBytes int64) ([]Segment, error) {
	if err := s.requireBinaries(filePath, maxBytes); err != nil {
		return nil, err
	}
	duration, err := s.probeDuration(ctx, filePath)
	if err != nil {
		return nil, err
	}
	targetChunkBytes := int64(float64(maxBytes) * nonDiarizedChunkRatio)
	numChunks := int(math.Ceil(float64(size) / float64(targetChunkBytes)))
	segmentTime := duration / float64(numChunks)
	tempDir, err := os.MkdirTemp("", "clai-audio-split-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create chunk temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)
	chunks, err := s.split(ctx, filePath, tempDir, segmentTime)
	if err != nil {
		return nil, err
	}
	spans := make([]SourceInterval, len(chunks))
	for i := range chunks {
		spans[i] = SourceInterval{Start: time.Duration(float64(i) * segmentTime * float64(time.Second)), End: time.Duration(float64(i+1) * segmentTime * float64(time.Second))}
	}
	b := newProgressBoard(s.StatusOut, utils.IsTerminalWriter(s.StatusOut), fmt.Sprintf("splitting via ffmpeg  %v  %.1f MB > %.0f MB limit  %v chunks × %.1f min  %v workers",
		filepath.Base(filePath), toMB(size), toMB(maxBytes), numChunks, segmentTime/60, s.workers()), spans)
	s.verifyChunkOffsets(ctx, chunks, segmentTime, b)
	return s.transcribePool(ctx, chunks, segmentTime, b)
}

func (s *Splitter) probeDuration(ctx context.Context, filePath string) (float64, error) {
	return probeDuration(ctx, s.Runner, filePath)
}

// probeDuration returns the container duration in seconds via ffprobe.
func probeDuration(ctx context.Context, runner CommandRunner, filePath string) (float64, error) {
	stdout, stderr, err := runner.Run(ctx, ffprobeBin,
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
	if err != nil {
		return 0, fmt.Errorf("failed to parse ffprobe duration %q for %v: %w", strings.TrimSpace(stdout), filePath, err)
	}
	if duration <= 0 {
		// A well-formed but non-positive duration is not a parse failure: name
		// it directly instead of rendering "error: <nil>" (worklog
		// 2026-09-05-error-propagation, phase 8, D6).
		return 0, fmt.Errorf("ffprobe reported non-positive duration %v for %v", duration, filePath)
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
func (s *Splitter) verifyChunkOffsets(ctx context.Context, chunks []string, segmentTime float64, b *board.Board) {
	measuredStart := 0.0
	for i, chunk := range chunks {
		planned := float64(i) * segmentTime
		if diff := math.Abs(measuredStart - planned); diff > 1 {
			b.Update(i, func(r *board.Row) {
				r.Mark, r.Cells[colState] = board.MarkWarn, fmt.Sprintf("timestamp drift %.1f s, stitched timestamps may be off", diff)
			})
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

func (s *Splitter) workers() int {
	if s.Parallelism <= 0 {
		return 3
	}
	return s.Parallelism
}

func (s *Splitter) transcribePool(ctx context.Context, chunks []string, segmentTime float64, b *board.Board) ([]Segment, error) {
	poolCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go b.Animate(poolCtx)
	started := time.Now()
	b.SetPhase(fmt.Sprintf("transcribing · %v chunks · %v workers", len(chunks), s.workers()))
	b.SetFooterFunc(func(rows []board.Row) string {
		return fmt.Sprintf("in flight %v · %v elapsed", board.InFlight(rows), time.Since(started).Round(time.Second))
	})
	parallelism := s.workers()
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
			b.Update(i, func(r *board.Row) { r.Active, r.Cells[colState], r.Started = true, "transcribing", time.Now() })
			segs, err := s.Transcriber.Transcribe(poolCtx, chunk)
			if err != nil {
				b.Update(i, func(r *board.Row) { r.Active, r.Mark, r.Cells[colState] = false, board.MarkWarn, "request failed" })
				errMu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("failed to transcribe chunk %v/%v: %w", i+1, len(chunks), err)
				}
				errMu.Unlock()
				cancel()
				return
			}
			results[i] = Offset(segs, generic.SecondsToDuration(float64(i)*segmentTime))
			b.Update(i, func(r *board.Row) {
				r.Active, r.Elapsed, r.Cells[colState] = false, time.Since(r.Started), fmt.Sprintf("transcribed · %v segments", len(segs))
				if r.Mark != board.MarkWarn {
					r.Mark = board.MarkDone
				}
			})
		}(i, chunk)
	}
	wg.Wait()
	b.SetPhase("stitching")
	b.Finish(fmt.Sprintf("%v chunks · %v elapsed", len(chunks), time.Since(started).Round(time.Second)))
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
