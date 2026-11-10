package capture

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/baalimago/clai/internal/diag"
)

// recorder is the ffmpeg Recorder. finalizeGrace bounds how long a stop waits
// for ffmpeg to finalize the file before the process is killed; zero means
// defaultFinalizeGrace.
type recorder struct {
	finalizeGrace time.Duration
}

// defaultFinalizeGrace is the wait between the interrupt and the kill. ffmpeg
// needs about 300 ms to finalize the WAV (measured 2026-10-28), and a killed
// ffmpeg still leaves a file settleWAV repairs, so a short bound costs at most
// the samples of the last packet.
const defaultFinalizeGrace = 2 * time.Second

func (r recorder) grace() time.Duration {
	if r.finalizeGrace > 0 {
		return r.finalizeGrace
	}
	return defaultFinalizeGrace
}

func (recorder) Sources(ctx context.Context) ([]Source, error) {
	ffmpeg, err := lookFFmpeg()
	if err != nil {
		return nil, err
	}
	return sources(ctx, ffmpeg)
}

func (r recorder) Record(ctx context.Context, req Request) (Recording, error) {
	ffmpeg, err := lookFFmpeg()
	if err != nil {
		return Recording{}, err
	}
	dir, err := prepareDir(req.Dir)
	if err != nil {
		return Recording{}, err
	}

	known, err := sources(ctx, ffmpeg)
	if err != nil {
		return Recording{}, err
	}
	selected, err := resolveSelection(known, req)
	if err != nil {
		return Recording{}, err
	}

	startedAt := time.Now().UTC()
	finalPath := filepath.Join(dir, fileName(startedAt, req.NameSuffix))
	partPath := finalPath + ".part"

	var (
		stopRequested atomic.Bool
		sawSound      atomic.Bool
		lastLine      string
	)
	cmd, stderr, err := startFFmpeg(ffmpeg, selected, partPath, req.SilencePeakDB)
	if err != nil {
		return Recording{}, err
	}

	silence := make(chan struct{}, 1)
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		scanCaptureStderr(stderr, &stopRequested, &sawSound, silence, req, func(line string) {
			lastLine = line
		})
	}()

	procDone := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(procDone)
	}()

	reason := awaitStop(ctx, procDone, silence, req)
	stopRequested.Store(true)
	if reason != StopError {
		requestStop(cmd, procDone, r.grace())
	}
	<-procDone

	select {
	case <-stderrDone:
	case <-time.After(2 * time.Second):
		_ = stderr.Close()
		<-stderrDone
	}
	_ = stderr.Close()

	endedAt := time.Now().UTC()
	rec := Recording{
		Path:           finalPath,
		StartedAt:      startedAt,
		EndedAt:        endedAt,
		Duration:       endedAt.Sub(startedAt),
		StoppedBecause: reason,
		Sources:        selected,
	}

	shape, hasSound, err := inspectPart(partPath, req.SilencePeakDB)
	if err != nil {
		_ = os.Remove(partPath)
		return rec, lostRecording(reason, ffmpegFailure(lastLine, err))
	}
	// A cancelled recording is kept even when it is quiet: nothing reads it
	// before the user does, and the threshold may be wrong for this room.
	if !hasSound && (reason != StopInterrupt || shape.dataBytes == 0) {
		_ = os.Remove(partPath)
		if reason == StopError {
			return rec, ffmpegFailure(lastLine, nil)
		}
		return rec, lostRecording(reason, ErrNoAudio)
	}
	if err := os.Rename(partPath, finalPath); err != nil {
		return rec, lostRecording(reason, diag.Errorf("finalize recording %q: %w", finalPath, err))
	}
	rec.Finish = shape.finish

	if reason == StopError {
		return rec, ffmpegFailure(lastLine, nil)
	}
	if reason == StopInterrupt {
		return rec, &InterruptedError{Path: finalPath, Finish: shape.finish, Quiet: !hasSound}
	}
	return rec, nil
}

// inspectPart settles the header of the part file and reports whether its
// samples hold sound.
func inspectPart(partPath string, silencePeakDB float64) (wavShape, bool, error) {
	shape, err := settleWAV(partPath)
	if err != nil {
		return wavShape{}, false, err
	}
	hasSound, err := wavHasSound(partPath, silencePeakDB)
	if err != nil {
		return wavShape{}, false, err
	}
	return shape, hasSound, nil
}

// lostRecording is the error of a recording that left no file: a cancelled
// call still reports the cancellation, with the loss as its cause.
func lostRecording(reason StopReason, cause error) error {
	if reason == StopInterrupt {
		return &InterruptedError{Lost: cause}
	}
	return cause
}

// awaitStop returns the reason the recording ends, without touching the
// process: the caller stops it afterwards.
func awaitStop(ctx context.Context, procDone <-chan struct{}, silence <-chan struct{}, req Request) StopReason {
	var maxCh <-chan time.Time
	if req.MaxDuration > 0 {
		timer := time.NewTimer(req.MaxDuration)
		defer timer.Stop()
		maxCh = timer.C
	}
	select {
	case <-ctx.Done():
		return StopInterrupt
	case <-maxCh:
		return StopMaxSeconds
	case <-req.Stop:
		return StopEnter
	case <-silence:
		return StopSilence
	case <-procDone:
		if ctx.Err() != nil {
			return StopInterrupt
		}
		// A cancellation that races the process exit still means the call was
		// interrupted, not that ffmpeg failed.
		select {
		case <-ctx.Done():
			return StopInterrupt
		case <-time.After(cancelArrivalGrace):
		}
		return StopError
	}
}

// cancelArrivalGrace is how long a process that ended by itself waits for the
// call's cancellation: whoever interrupted ffmpeg from outside usually
// interrupted clai too.
const cancelArrivalGrace = 250 * time.Millisecond

// requestStop sends ffmpeg the one interrupt that finalizes the WAV and kills
// it when it is still running after grace. One interrupt only: ffmpeg exits
// before it finalizes when it receives a second one (measured 2026-10-28: a
// second SIGINT lost 20 of 20 recordings). ffmpeg runs in its own process
// group, so no terminal signal arrives beside this one, and both signals go to
// that group so a wrapper script and its ffmpeg end together. A process that
// already ended receives nothing.
func requestStop(cmd *exec.Cmd, procDone <-chan struct{}, grace time.Duration) {
	select {
	case <-procDone:
		return
	default:
	}
	_ = interruptGroup(cmd.Process)
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-procDone:
		return
	case <-timer.C:
	}
	_ = killGroup(cmd.Process)
	<-procDone
}

func scanCaptureStderr(r io.Reader, stopping, sawSound *atomic.Bool, silence chan<- struct{}, req Request, keepLast func(string)) {
	var timer *time.Timer
	fire := func() {
		select {
		case silence <- struct{}{}:
		default:
		}
	}
	notify := func(state CaptureState) {
		if req.OnState != nil {
			req.OnState(state)
		}
	}
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		keepLast(line)
		if stopping.Load() {
			continue
		}
		switch {
		case strings.Contains(line, "silence_end"):
			sawSound.Store(true)
			if timer != nil {
				timer.Stop()
				timer = nil
			}
			notify(StateSound)
		case strings.Contains(line, "silence_start"):
			if at, ok := silenceStartSeconds(line); ok && at > 0 {
				sawSound.Store(true)
			}
			if req.SilenceTimeout > 0 && sawSound.Load() && timer == nil {
				timer = time.AfterFunc(req.SilenceTimeout, fire)
			}
			notify(StateSilent)
		}
	}
}

func silenceStartSeconds(line string) (float64, bool) {
	_, rest, ok := strings.Cut(line, "silence_start:")
	if !ok {
		return 0, false
	}
	field, _, _ := strings.Cut(strings.TrimSpace(rest), " ")
	seconds, err := strconv.ParseFloat(field, 64)
	if err != nil {
		return 0, false
	}
	return seconds, true
}

func prepareDir(dir string) (string, error) {
	if dir == "" {
		return "", errors.New("capture dir is empty")
	}
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("capture dir must be absolute, got %q", dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", diag.Errorf("create capture dir %q: %w", dir, err)
	}
	return dir, nil
}

// startFFmpeg starts the one capture process in its own process group, so a
// terminal's Ctrl-C reaches clai and not ffmpeg: a second Ctrl-C would
// otherwise end ffmpeg before it finalizes the file. It carries no context,
// because os/exec would kill the child when the call context ends; the
// recorder asks it to finish instead.
//
// -flush_packets 1 writes every packet to the file as it is encoded. Without
// it ffmpeg holds up to 256 KiB, eight seconds of this format, in memory, and
// a killed ffmpeg loses them (measured 2026-11-10 with ffmpeg 8.0.1: killed
// after 2.5 s the file was empty, with the flag it held all but the last
// packet).
func startFFmpeg(ffmpeg string, selected []Source, outPath string, silencePeakDB float64) (*exec.Cmd, *os.File, error) {
	args := []string{"-hide_banner", "-nostdin", "-loglevel", "info", "-y"}
	args = append(args, inputArgsForSources(selected)...)
	args = append(args, "-filter_complex", filterForSources(len(selected), silencePeakDB))
	args = append(args, "-ac", "1", "-ar", "16000", "-flush_packets", "1", "-f", "wav", outPath)

	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		return nil, nil, diag.Errorf("create stderr pipe: %w", err)
	}
	cmd := exec.Command(ffmpeg, args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = writeEnd
	ownProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		_ = readEnd.Close()
		_ = writeEnd.Close()
		return nil, nil, diag.Errorf("start ffmpeg: %w", err)
	}
	_ = writeEnd.Close()
	return cmd, readEnd, nil
}

func filterForSources(count int, silencePeakDB float64) string {
	noise := fmt.Sprintf("noise=%gdB:d=0.5", silencePeakDB)
	if count <= 1 {
		return fmt.Sprintf("[0:a]silencedetect=%s", noise)
	}
	var inputs bytes.Buffer
	for i := range count {
		fmt.Fprintf(&inputs, "[%d:a]", i)
	}
	return fmt.Sprintf("%samix=inputs=%d:normalize=1,silencedetect=%s", inputs.String(), count, noise)
}

func ffmpegFailure(lastLine string, cause error) error {
	if lastLine != "" {
		return errors.New(lastLine)
	}
	if cause != nil {
		return diag.Errorf("ffmpeg produced no readable wav: %w", cause)
	}
	return errors.New("ffmpeg produced no readable wav")
}

func lookFFmpeg() (string, error) {
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		return "", diag.Errorf("ffmpeg not found on PATH; install ffmpeg, for example with your package manager or from https://ffmpeg.org/download.html: %w", err)
	}
	return path, nil
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func fileName(startedAt time.Time, suffix string) string {
	base := startedAt.Format("2006-01-02T15-04-05Z")
	if slug := slugify(suffix); slug != "" {
		base += "_" + slug
	}
	return base + ".wav"
}

func slugify(s string) string {
	s = slugRe.ReplaceAllString(strings.ToLower(s), "-")
	return strings.Trim(s, "-")
}
