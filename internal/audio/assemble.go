package audio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/baalimago/clai/internal/diag"
)

const (
	requestCodec = "mp3 cbr 64k mono 16k"
	// requestBitsPerSecond is requestCodec's bitrate
	requestBitsPerSecond = 64_000
	envelopeReserve      = 16 << 10
	sampleGuard          = 250 * time.Millisecond
	sampleGap            = 500 * time.Millisecond
	requestExtension     = ".mp3"
)

type RegionKind string

const (
	RegionSample RegionKind = "sample"
	RegionGap    RegionKind = "gap"
	RegionCore   RegionKind = "core"
)

// Region maps one request-time interval to its source-time interval. Gaps
// are synthesized silence and have no source interval.
type Region struct {
	Kind     RegionKind
	Label    string
	ReqStart time.Duration
	ReqEnd   time.Duration
	SrcStart time.Duration
	SrcEnd   time.Duration
	// Clipped marks a sample whose guard hit a source bound
	Clipped bool
	// Candidate marks a clip that is not a registry speaker's: a merge or retry candidate
	Candidate bool
}

// RequestManifest describes one encoded request exactly.
type RequestManifest struct {
	FilePath       string
	Codec          string
	Regions        []Region
	PrefixDuration time.Duration
	CoreStart      time.Duration
	CoreEnd        time.Duration
	Duration       time.Duration
	Bytes          int64
	SourceHash     string
	RequestHash    string
}

// SampleClip is a speech interval in source time; guards are added by the
// assembler.
type SampleClip struct {
	ID    string
	Start time.Duration
	End   time.Duration
	// Candidate marks a clip that is not a registry speaker's: a merge or retry candidate
	Candidate bool
}

// ErrEmptyRequest refuses a request with no sample and no core
// (Assemble.2); callers match it with errors.Is.
var ErrEmptyRequest = errors.New("request has no audio: no sample and no core")

// AssembleRequest.Core may be the zero SourceInterval:
//
//   - Assemble.1 — With samples and a zero Core the request holds the samples
//     and their gaps only. No core audio is extracted. The manifest ends with
//     a core region of zero length at the end of the request, CoreStart and
//     CoreEnd are 0, and Duration equals PrefixDuration. Both budgets are
//     checked as for any request. (I5)
//   - Assemble.2 — With no sample and a zero Core, Assemble returns
//     ErrEmptyRequest and leaves no file behind.
//   - Assemble.3 — A request with a core is assembled exactly as today: the
//     same regions, bytes and RequestHash.
type AssembleRequest struct {
	Samples []SampleClip
	Core    SourceInterval
}

func (r AssembleRequest) empty() bool { return len(r.Samples) == 0 && r.Core == SourceInterval{} }

func (r AssembleRequest) hasCore() bool { return r.Core != SourceInterval{} }

// AudioAssembler builds request files in two stages: an exact PCM timeline,
// then one deterministic encode. Artifacts live in a run-scoped directory
// removed by Close.
type AudioAssembler struct {
	Runner         CommandRunner
	Budgets        Budgets
	SourcePath     string
	SourceDuration time.Duration
	runDir         string
	sourceHash     string
	seq            atomic.Int64
}

func NewAssembler(runner CommandRunner, budgets Budgets, sourcePath string, sourceDuration time.Duration) (*AudioAssembler, error) {
	sourceHash, err := hashFile(sourcePath)
	if err != nil {
		return nil, diag.Errorf("failed to hash source: %w", err)
	}
	runDir, err := os.MkdirTemp("", "clai-audio-calibrate-*")
	if err != nil {
		return nil, diag.Errorf("failed to create run directory: %w", err)
	}
	return &AudioAssembler{
		Runner:         runner,
		Budgets:        budgets,
		SourcePath:     sourcePath,
		SourceDuration: sourceDuration,
		runDir:         runDir,
		sourceHash:     sourceHash,
	}, nil
}

// Close removes every artifact of the run.
func (a *AudioAssembler) Close() error {
	return os.RemoveAll(a.runDir)
}

// Remove deletes one encoded request once it has been uploaded.
func (a *AudioAssembler) Remove(m *RequestManifest) error {
	if m == nil || m.FilePath == "" {
		return nil
	}
	return os.Remove(m.FilePath)
}

func (a *AudioAssembler) Assemble(ctx context.Context, req AssembleRequest) (manifest *RequestManifest, err error) {
	if req.empty() {
		return nil, ErrEmptyRequest
	}
	base := filepath.Join(a.runDir, "req-"+strconv.FormatInt(a.seq.Add(1), 10))
	workDir := base + ".work"
	if err := os.Mkdir(workDir, 0o700); err != nil {
		return nil, diag.Errorf("failed to create request work dir: %w", err)
	}
	encoded := base + requestExtension
	defer func() {
		os.RemoveAll(workDir)
		if err != nil {
			os.Remove(encoded)
		}
	}()
	timelinePath := filepath.Join(workDir, "timeline.raw")
	timeline, err := os.Create(timelinePath)
	if err != nil {
		return nil, diag.Errorf("failed to create timeline: %w", err)
	}
	defer timeline.Close()
	hasher := sha256.New()
	out := io.MultiWriter(timeline, hasher)
	m := &RequestManifest{Codec: requestCodec, SourceHash: a.sourceHash}
	pos := int64(0)
	appendRegion := func(r Region, samples int64) {
		r.ReqStart, r.ReqEnd = durationOf(pos), durationOf(pos+samples)
		m.Regions = append(m.Regions, r)
		pos += samples
	}
	for i, s := range req.Samples {
		start, end, clipped := a.guarded(s)
		n, err := a.extractPCM(ctx, start, end, filepath.Join(workDir, "sample-"+strconv.Itoa(i)+".raw"), out)
		if err != nil {
			return nil, err
		}
		appendRegion(Region{Kind: RegionSample, Label: s.ID, SrcStart: start, SrcEnd: end, Clipped: clipped, Candidate: s.Candidate}, n)
		gap := samplesOf(sampleGap)
		if _, err := out.Write(make([]byte, gap*bytesPerSample)); err != nil {
			return nil, diag.Errorf("failed to write gap: %w", err)
		}
		appendRegion(Region{Kind: RegionGap}, gap)
	}
	m.PrefixDuration = durationOf(pos)
	coreStart, coreEnd := snap(req.Core.Start), snap(req.Core.End)
	var n int64
	if req.hasCore() {
		if n, err = a.extractPCM(ctx, coreStart, coreEnd, filepath.Join(workDir, "core.raw"), out); err != nil {
			return nil, err
		}
	}
	appendRegion(Region{Kind: RegionCore, SrcStart: coreStart, SrcEnd: coreEnd}, n)
	m.CoreStart, m.CoreEnd = coreStart, coreEnd
	m.Duration = durationOf(pos)
	if err := timeline.Close(); err != nil {
		return nil, diag.Errorf("failed to flush timeline: %w", err)
	}
	if err := checkRequestSeconds(m, a.Budgets.MaxRequestDuration); err != nil {
		return nil, err
	}
	if err := a.encode(ctx, timelinePath, encoded); err != nil {
		return nil, err
	}
	info, err := os.Stat(encoded)
	if err != nil {
		return nil, diag.Errorf("encoded request missing: %w", err)
	}
	m.Bytes = info.Size()
	if m.Bytes+envelopeReserve > a.Budgets.MaxRequestBytes {
		return nil, fmt.Errorf("encoded request of %v bytes plus the 16 KiB multipart envelope reserve exceeds max-request-bytes %v",
			m.Bytes, a.Budgets.MaxRequestBytes)
	}
	m.FilePath = encoded
	hasher.Write([]byte(requestCodec))
	m.RequestHash = hex.EncodeToString(hasher.Sum(nil))
	return m, nil
}

func checkRequestSeconds(m *RequestManifest, limit time.Duration) error {
	if m.Duration > limit {
		return fmt.Errorf("request of %v exceeds max-request-seconds %v (prefix %v, core %v)",
			m.Duration, limit, m.PrefixDuration, m.CoreEnd-m.CoreStart)
	}
	return nil
}

// guarded widens a speech interval by the sample guard, clipping at the
// source bounds.
func (a *AudioAssembler) guarded(s SampleClip) (start, end time.Duration, clipped bool) {
	start, end = snap(s.Start-sampleGuard), snap(s.End+sampleGuard)
	if start < 0 {
		start, clipped = 0, true
	}
	if a.SourceDuration > 0 && end > a.SourceDuration {
		end, clipped = snap(a.SourceDuration), true
	}
	return start, end, clipped
}

// extractPCM decodes [start,end) of the source to canonical PCM via a
// sample-accurate atrim, appends it to the timeline and returns its samples.
func (a *AudioAssembler) extractPCM(ctx context.Context, start, end time.Duration, tmp string, out io.Writer) (int64, error) {
	_, stderr, err := a.Runner.Run(ctx, ffmpegBin,
		"-v", "error", "-nostdin",
		"-i", a.SourcePath,
		"-af", fmt.Sprintf("atrim=start=%s:end=%s", seconds(start), seconds(end)),
		"-f", "s16le", "-ac", "1", "-ar", strconv.Itoa(sampleRate),
		"-y", tmp)
	if err != nil {
		if ctx.Err() != nil {
			return 0, diag.Errorf("pcm extraction aborted: %w", ctx.Err())
		}
		return 0, diag.Errorf("ffmpeg failed to extract %v–%v: %w, stderr: %v", start, end, err, tail(stderr))
	}
	f, err := os.Open(tmp)
	if err != nil {
		return 0, diag.Errorf("extracted pcm missing: %w", err)
	}
	defer f.Close()
	written, err := io.Copy(out, f)
	if err != nil {
		return 0, diag.Errorf("failed to append pcm to timeline: %w", err)
	}
	os.Remove(tmp)
	return written / bytesPerSample, nil
}

func (a *AudioAssembler) encode(ctx context.Context, timeline, encoded string) error {
	return encodeRequest(ctx, a.Runner, []string{"-f", "s16le", "-ar", strconv.Itoa(sampleRate), "-ac", "1", "-i", timeline}, encoded)
}

// encodeRequest writes the input at the request codec (requestCodec).
func encodeRequest(ctx context.Context, runner CommandRunner, input []string, encoded string) error {
	args := append([]string{"-v", "error", "-nostdin"}, input...)
	args = append(args,
		"-map_metadata", "-1", "-fflags", "+bitexact",
		"-c:a", "libmp3lame", "-b:a", strconv.Itoa(requestBitsPerSecond), "-ac", "1", "-ar", strconv.Itoa(sampleRate),
		"-y", encoded)
	_, stderr, err := runner.Run(ctx, ffmpegBin, args...)
	if err != nil {
		if ctx.Err() != nil {
			return diag.Errorf("request encode aborted: %w", ctx.Err())
		}
		return diag.Errorf("ffmpeg failed to encode request: %w, stderr: %v", err, tail(stderr))
	}
	return nil
}

// requestCodecDuration is the audio one request of maxBytes holds at requestCodec.
func requestCodecDuration(maxBytes int64) time.Duration {
	return time.Duration(maxBytes*8) * time.Second / requestBitsPerSecond
}

func seconds(d time.Duration) string {
	return strconv.FormatFloat(d.Seconds(), 'f', 6, 64)
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
