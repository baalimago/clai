package voices

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// The inspection's limits: a clip is 3–10 s of mostly speech, not clipped
// and not too quiet.
const (
	sampleRate      = 16000
	minSeconds      = 3.0
	maxSeconds      = 10.0
	minSpeechShare  = 0.6
	speechDBFS      = -35.0
	silenceSamples  = sampleRate * 3 / 10
	maxClippedRun   = 2
	minLoudnessDBFS = -40.0
	silentDBFS      = -91.0
	fullScale       = 32768.0
	detailLimit     = 240
	toClipFormat    = "aresample=16000,aformat=sample_fmts=s16:channel_layouts=mono"
	lengthRule      = "a clip is 3–10 s"
	ffmpeg          = "ffmpeg"
	ffprobe         = "ffprobe"
	unknownLength   = time.Duration(-1)
	maxDecimals     = 6
)

// Runner runs ffmpeg and ffprobe. audio.ExecRunner satisfies it.
type Runner interface {
	LookPath(name string) (string, error)
	Run(ctx context.Context, name string, args ...string) (stdout, stderr string, err error)
}

// Cut is the part of a file to inspect. The zero To is the file's end, so
// the zero Cut is the whole file.
type Cut struct {
	From time.Duration
	To   time.Duration
}

// Inspection is an accepted clip and its measurements.
type Inspection struct {
	// FLAC is the clip as it will be stored: 16 kHz mono.
	FLAC    []byte
	Seconds float64
	// SpeechShare is the part of the clip that is speech, 0 to 1.
	SpeechShare float64
	// ClippedRun is the longest run of consecutive samples at full scale.
	ClippedRun int
	MeanDBFS   float64
}

type QualityCheck string

const (
	CheckLength   QualityCheck = "length"
	CheckSpeech   QualityCheck = "speech"
	CheckClipping QualityCheck = "clipping"
	CheckLoudness QualityCheck = "loudness"
)

// ClipQualityError refuses a clip: Failed names every failed check, the
// other fields are what was measured.
type ClipQualityError struct {
	Failed      []QualityCheck
	Seconds     float64
	SpeechShare float64
	ClippedRun  int
	MeanDBFS    float64
}

func (e *ClipQualityError) Error() string {
	parts := make([]string, 0, len(e.Failed))
	for _, check := range e.Failed {
		switch check {
		case CheckLength:
			which, limit := "short", minSeconds
			if e.Seconds >= minSeconds {
				which, limit = "long", maxSeconds
			}
			parts = append(parts, fmt.Sprintf("clip too %s: %s s, %s", which, offLimit(e.Seconds, 1, limit), lengthRule))
		case CheckSpeech:
			parts = append(parts, fmt.Sprintf("too little speech: %s%% of the clip is speech, a clip needs %d%%",
				offLimit(100*e.SpeechShare, 0, 100*minSpeechShare), int(100*minSpeechShare)))
		case CheckClipping:
			parts = append(parts, fmt.Sprintf("clipped: %d consecutive samples at full scale, a clip allows %d", e.ClippedRun, maxClippedRun))
		case CheckLoudness:
			parts = append(parts, fmt.Sprintf("too quiet: mean volume %s dBFS, a clip needs %d dBFS",
				offLimit(e.MeanDBFS, 1, minLoudnessDBFS), int(minLoudnessDBFS)))
		}
	}
	if len(parts) == 0 {
		return "clip refused"
	}
	return strings.Join(parts, "; ")
}

// offLimit formats v, adding decimals while it would read as the limit it
// is not on.
func offLimit(v float64, decimals int, limit float64) string {
	for ; decimals < maxDecimals; decimals++ {
		text := strconv.FormatFloat(v, 'f', decimals, 64)
		if v == limit || text != strconv.FormatFloat(limit, 'f', decimals, 64) {
			return text
		}
	}
	return strconv.FormatFloat(v, 'f', decimals, 64)
}

// CutError refuses a cut that selects nothing from a file of Length.
type CutError struct {
	Cut    Cut
	Length time.Duration
}

func (e *CutError) Error() string {
	seconds := func(d time.Duration) string { return strconv.FormatFloat(d.Seconds(), 'f', -1, 64) + " s" }
	to := "the end"
	if e.Cut.To != 0 {
		to = seconds(e.Cut.To)
	}
	return "the cut from " + seconds(e.Cut.From) + " to " + to + " selects nothing from a file of " + seconds(e.Length)
}

type MissingToolError struct{ Tool string }

func (e *MissingToolError) Error() string {
	return e.Tool + " is not installed; inspecting a clip needs ffmpeg and ffprobe"
}

// SourceError says the file could not be read as audio. Detail is the
// program's last message, never audio.
type SourceError struct {
	File   string
	Detail string
	Err    error
}

func (e *SourceError) Error() string {
	return "read " + strconv.Quote(e.File) + " as audio: " + e.Detail
}
func (e *SourceError) Unwrap() error { return e.Err }

// Inspector measures a clip before it is added.
type Inspector struct{ Runner Runner }

// Inspect cuts cut from file, measures it, and returns the clip as 16 kHz
// mono FLAC, or a *ClipQualityError naming every check it failed. A file
// that is already such a FLAC, inspected whole, is returned byte for byte,
// so a clip moved between people keeps its id. It leaves no file behind.
func (in Inspector) Inspect(ctx context.Context, file string, cut Cut) (Inspection, error) {
	for _, tool := range []string{ffmpeg, ffprobe} {
		if _, err := in.Runner.LookPath(tool); err != nil {
			return Inspection{}, &MissingToolError{Tool: tool}
		}
	}
	if err := ctx.Err(); err != nil {
		return Inspection{}, err
	}
	length, stored, err := in.probe(ctx, file)
	if err != nil {
		return Inspection{}, err
	}
	samples, err := in.samples(ctx, file, cut, length)
	if err != nil {
		return Inspection{}, err
	}
	measured := measure(samples)
	if failed := measured.failed(); len(failed) > 0 {
		return Inspection{}, &ClipQualityError{
			Failed: failed, Seconds: measured.Seconds, SpeechShare: measured.SpeechShare,
			ClippedRun: measured.ClippedRun, MeanDBFS: measured.MeanDBFS,
		}
	}
	measured.FLAC, err = in.flac(ctx, file, cut, stored)
	if err != nil {
		return Inspection{}, err
	}
	return measured, nil
}

// probe reads the file's length, unknownLength when ffprobe gives none, and
// whether it is stored as a clip already: 16 kHz mono FLAC.
func (in Inspector) probe(ctx context.Context, file string) (length time.Duration, stored bool, err error) {
	stdout, err := in.run(ctx, file, ffprobe, "-v", "error", "-select_streams", "a:0",
		"-show_entries", "stream=codec_name,sample_rate,channels:format=format_name,duration",
		"-of", "json", "file:"+file)
	if err != nil {
		return 0, false, err
	}
	var answer struct {
		Streams []struct {
			CodecName  string `json:"codec_name"`
			SampleRate string `json:"sample_rate"`
			Channels   int    `json:"channels"`
		} `json:"streams"`
		Format struct {
			FormatName string `json:"format_name"`
			Duration   string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal([]byte(stdout), &answer); err != nil {
		return 0, false, &SourceError{File: file, Detail: "ffprobe gave no readable description", Err: err}
	}
	if len(answer.Streams) == 0 {
		return 0, false, &SourceError{File: file, Detail: "no audio stream"}
	}
	stream := answer.Streams[0]
	stored = answer.Format.FormatName == "flac" && stream.CodecName == "flac" &&
		stream.SampleRate == strconv.Itoa(sampleRate) && stream.Channels == 1
	length = unknownLength
	if seconds, err := strconv.ParseFloat(answer.Format.Duration, 64); err == nil && seconds >= 0 && !math.IsInf(seconds, 1) {
		length = time.Duration(math.Round(seconds*1e6)) * time.Microsecond
	}
	return length, stored, nil
}

// samples decodes the cut. With a known length a bad cut is refused before
// anything is decoded; without one the whole file is decoded and cut here.
func (in Inspector) samples(ctx context.Context, file string, cut Cut, length time.Duration) ([]int16, error) {
	if length != unknownLength {
		if err := checkCut(cut, length); err != nil {
			return nil, err
		}
		return in.decode(ctx, file, cutFilter(cut))
	}
	all, err := in.decode(ctx, file, toClipFormat)
	if err != nil {
		return nil, err
	}
	if err := checkCut(cut, time.Duration(len(all))*time.Second/sampleRate); err != nil {
		return nil, err
	}
	to := len(all)
	if cut.To != 0 {
		to = min(sampleAt(cut.To), to)
	}
	return all[min(sampleAt(cut.From), to):to], nil
}

func checkCut(cut Cut, length time.Duration) error {
	to := cut.To
	if to == 0 {
		to = length
	}
	if cut.From < 0 || cut.To < 0 || to <= cut.From || to > length {
		return &CutError{Cut: cut, Length: length}
	}
	return nil
}

func (in Inspector) decode(ctx context.Context, file, filter string) ([]int16, error) {
	stdout, err := in.run(ctx, file, ffmpeg, ffmpegArgs(file, filter, "-f", "s16le", "-c:a", "pcm_s16le", "pipe:1")...)
	if err != nil {
		return nil, err
	}
	if len(stdout)%2 != 0 {
		return nil, &SourceError{File: file, Detail: "ffmpeg's decoded audio ends in half a sample"}
	}
	samples := make([]int16, len(stdout)/2)
	if _, err := binary.Decode([]byte(stdout), binary.LittleEndian, samples); err != nil {
		return nil, &SourceError{File: file, Detail: "ffmpeg's decoded audio could not be read as samples", Err: err}
	}
	return samples, nil
}

func (in Inspector) flac(ctx context.Context, file string, cut Cut, stored bool) (clip []byte, err error) {
	failed := func(err error) error { return &SourceError{File: file, Detail: err.Error(), Err: err} }
	if cut == (Cut{}) && stored {
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, failed(err)
		}
		return data, nil
	}
	dir, err := os.MkdirTemp("", "clai-clip-")
	if err != nil {
		return nil, failed(err)
	}
	defer func() {
		if removeErr := os.RemoveAll(dir); removeErr != nil && err == nil {
			clip, err = nil, failed(removeErr)
		}
	}()
	out := filepath.Join(dir, "clip.flac")
	if _, err := in.run(ctx, file, ffmpeg, ffmpegArgs(file, cutFilter(cut),
		"-map_metadata", "-1", "-fflags", "+bitexact", "-flags:a", "+bitexact",
		"-c:a", "flac", "-f", "flac", "file:"+out)...); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(out)
	if err != nil {
		return nil, &SourceError{File: file, Detail: "ffmpeg wrote no clip", Err: err}
	}
	return data, nil
}

func (in Inspector) run(ctx context.Context, file, program string, args ...string) (string, error) {
	stdout, stderr, err := in.Runner.Run(ctx, program, args...)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", ctxErr
	}
	if err != nil {
		return "", &SourceError{File: file, Detail: lastLine(stderr, err), Err: err}
	}
	return stdout, nil
}

func ffmpegArgs(file, filter string, output ...string) []string {
	return append([]string{
		"-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-i", "file:" + file, "-map", "0:a:0", "-af", filter,
	}, output...)
}

func cutFilter(cut Cut) string {
	if cut == (Cut{}) {
		return toClipFormat
	}
	filter := toClipFormat + ",atrim=start_sample=" + strconv.Itoa(sampleAt(cut.From))
	if cut.To != 0 {
		filter += ":end_sample=" + strconv.Itoa(sampleAt(cut.To))
	}
	return filter
}

func sampleAt(d time.Duration) int {
	return int(d/time.Second)*sampleRate + int(d%time.Second)*sampleRate/int(time.Second)
}

// measure takes the clip's measurements in one pass. A run of quiet samples
// 0.3 s or longer is silence; everything else counts as speech.
func measure(samples []int16) Inspection {
	level := fullScale * math.Pow(10, speechDBFS/20)
	var silent, quietRun, clippedRun, longest int
	var energy float64
	for _, s := range samples {
		v := float64(s)
		energy += v * v
		if math.Abs(v) < level {
			quietRun++
		} else {
			if quietRun >= silenceSamples {
				silent += quietRun
			}
			quietRun = 0
		}
		if s == math.MaxInt16 || s == math.MinInt16 {
			clippedRun++
			longest = max(longest, clippedRun)
		} else {
			clippedRun = 0
		}
	}
	if quietRun >= silenceSamples {
		silent += quietRun
	}
	n := len(samples)
	m := Inspection{Seconds: float64(n) / sampleRate, ClippedRun: longest, MeanDBFS: silentDBFS}
	if n > 0 {
		m.SpeechShare = float64(n-silent) / float64(n)
		m.MeanDBFS = max(20*math.Log10(math.Sqrt(energy/float64(n))/fullScale), silentDBFS)
	}
	return m
}

func (m Inspection) failed() []QualityCheck {
	var failed []QualityCheck
	if m.Seconds < minSeconds || m.Seconds > maxSeconds {
		failed = append(failed, CheckLength)
	}
	if m.SpeechShare < minSpeechShare {
		failed = append(failed, CheckSpeech)
	}
	if m.ClippedRun > maxClippedRun {
		failed = append(failed, CheckClipping)
	}
	if m.MeanDBFS < minLoudnessDBFS {
		failed = append(failed, CheckLoudness)
	}
	return failed
}

// lastLine is a program's last stderr line, printable text only and cut
// short, so a Detail carries words and never audio.
func lastLine(stderr string, err error) string {
	all := strings.TrimSpace(stderr)
	var kept []rune
	for _, r := range strings.ToValidUTF8(all[strings.LastIndexByte(all, '\n')+1:], "") {
		if unicode.IsPrint(r) && len(kept) < detailLimit {
			kept = append(kept, r)
		}
	}
	if line := strings.TrimSpace(string(kept)); line != "" {
		return line
	}
	return err.Error()
}
