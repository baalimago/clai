package audio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"
)

const (
	requestCodec     = "mp3 cbr 64k mono 16k"
	envelopeReserve  = 16 << 10
	sampleGuard      = 250 * time.Millisecond
	sampleGap        = 500 * time.Millisecond
	requestExtension = ".mp3"
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
	// Candidate marks a discovery clip that is not yet a registry speaker
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

// SourceTime maps a request-time instant inside a sample or core region to
// source time.
func (m *RequestManifest) SourceTime(req time.Duration) (time.Duration, error) {
	for _, r := range m.Regions {
		if req < r.ReqStart || req >= r.ReqEnd {
			continue
		}
		if r.Kind == RegionGap {
			return 0, fmt.Errorf("request time %v falls in a synthesized gap", req)
		}
		return r.SrcStart + (req - r.ReqStart), nil
	}
	if req == m.Duration && len(m.Regions) > 0 {
		last := m.Regions[len(m.Regions)-1]
		return last.SrcEnd, nil
	}
	return 0, fmt.Errorf("request time %v is outside the request (%v)", req, m.Duration)
}

// SampleClip is a speech interval in source time; guards are added by the
// assembler.
type SampleClip struct {
	ID    string
	Start time.Duration
	End   time.Duration
	// Candidate clips belong to discovery, not to a registry speaker
	Candidate bool
}

type AssembleRequest struct {
	Samples []SampleClip
	Core    SourceInterval
}

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
		return nil, fmt.Errorf("failed to hash source: %w", err)
	}
	runDir, err := os.MkdirTemp("", "clai-audio-calibrate-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create run directory: %w", err)
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

func (a *AudioAssembler) RunDir() string { return a.runDir }

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
	base := filepath.Join(a.runDir, "req-"+strconv.FormatInt(a.seq.Add(1), 10))
	workDir := base + ".work"
	if err := os.Mkdir(workDir, 0o700); err != nil {
		return nil, fmt.Errorf("failed to create request work dir: %w", err)
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
		return nil, fmt.Errorf("failed to create timeline: %w", err)
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
			return nil, fmt.Errorf("failed to write gap: %w", err)
		}
		appendRegion(Region{Kind: RegionGap}, gap)
	}
	m.PrefixDuration = durationOf(pos)
	coreStart, coreEnd := snap(req.Core.Start), snap(req.Core.End)
	n, err := a.extractPCM(ctx, coreStart, coreEnd, filepath.Join(workDir, "core.raw"), out)
	if err != nil {
		return nil, err
	}
	appendRegion(Region{Kind: RegionCore, SrcStart: coreStart, SrcEnd: coreEnd}, n)
	m.CoreStart, m.CoreEnd = coreStart, coreEnd
	m.Duration = durationOf(pos)
	if err := timeline.Close(); err != nil {
		return nil, fmt.Errorf("failed to flush timeline: %w", err)
	}
	if m.Duration > a.Budgets.MaxRequestDuration {
		return nil, fmt.Errorf("request of %v exceeds max-request-seconds %v (prefix %v, core %v)",
			m.Duration, a.Budgets.MaxRequestDuration, m.PrefixDuration, coreEnd-coreStart)
	}
	if err := a.encode(ctx, timelinePath, encoded); err != nil {
		return nil, err
	}
	info, err := os.Stat(encoded)
	if err != nil {
		return nil, fmt.Errorf("encoded request missing: %w", err)
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
			return 0, fmt.Errorf("pcm extraction aborted: %w", ctx.Err())
		}
		return 0, fmt.Errorf("ffmpeg failed to extract %v–%v: %w, stderr: %v", start, end, err, tail(stderr))
	}
	f, err := os.Open(tmp)
	if err != nil {
		return 0, fmt.Errorf("extracted pcm missing: %w", err)
	}
	defer f.Close()
	written, err := io.Copy(out, f)
	if err != nil {
		return 0, fmt.Errorf("failed to append pcm to timeline: %w", err)
	}
	os.Remove(tmp)
	return written / bytesPerSample, nil
}

func (a *AudioAssembler) encode(ctx context.Context, timeline, encoded string) error {
	_, stderr, err := a.Runner.Run(ctx, ffmpegBin,
		"-v", "error", "-nostdin",
		"-f", "s16le", "-ar", strconv.Itoa(sampleRate), "-ac", "1",
		"-i", timeline,
		"-map_metadata", "-1", "-fflags", "+bitexact",
		"-c:a", "libmp3lame", "-b:a", "64k", "-ac", "1", "-ar", strconv.Itoa(sampleRate),
		"-y", encoded)
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("request encode aborted: %w", ctx.Err())
		}
		return fmt.Errorf("ffmpeg failed to encode request: %w, stderr: %v", err, tail(stderr))
	}
	return nil
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
