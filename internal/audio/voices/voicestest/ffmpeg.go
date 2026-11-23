// Package voicestest is test support for package voices: an ffmpeg and
// ffprobe that answer the Inspector's calls from scripted audio, so a test
// needs neither program.
package voicestest

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
)

const sampleRate = 16000

// clipMagic starts every clip the fake encodes; the samples follow it.
var clipMagic = []byte("fLaC")

// Call is one program run the Runner was asked for.
type Call struct {
	Name string
	Args []string
}

// Runner satisfies voices.Runner without running anything. It resolves
// ffmpeg and ffprobe and nothing else. A file is audio when Serve scripted
// it, or when it holds a clip this Runner encoded, which then counts as
// 16 kHz mono FLAC. The zero value is ready and safe for concurrent use.
type Runner struct {
	mu      sync.Mutex
	served  map[string][]int16
	failing map[string]string
	hanging map[string]bool
	missing []string
	calls   []Call
}

func NewRunner() *Runner { return &Runner{} }

// Serve scripts file as audio that decodes to samples.
func (r *Runner) Serve(file string, samples []int16) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.served == nil {
		r.served = map[string][]int16{}
	}
	r.served[file] = samples
}

// Fail scripts file as one the programs fail on, with message as the last
// stderr line.
func (r *Runner) Fail(file, message string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failing == nil {
		r.failing = map[string]string{}
	}
	r.failing[file] = message
}

// Hang scripts file as one the programs finish with only when ctx ends.
func (r *Runner) Hang(file string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.hanging == nil {
		r.hanging = map[string]bool{}
	}
	r.hanging[file] = true
}

// Missing scripts programs as not installed.
func (r *Runner) Missing(programs ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.missing = append(r.missing, programs...)
}

// Calls returns the calls run so far.
func (r *Runner) Calls() []Call {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.calls)
}

func (r *Runner) LookPath(name string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if (name != "ffmpeg" && name != "ffprobe") || slices.Contains(r.missing, name) {
		return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
	}
	return "/voicestest/bin/" + name, nil
}

func (r *Runner) Run(ctx context.Context, name string, args ...string) (stdout, stderr string, err error) {
	r.mu.Lock()
	r.calls = append(r.calls, Call{Name: name, Args: slices.Clone(args)})
	r.mu.Unlock()
	if _, err := r.LookPath(name); err != nil {
		return "", "", err
	}
	input := args[len(args)-1]
	if i := slices.Index(args, "-i"); name == "ffmpeg" && i >= 0 && i+1 < len(args) {
		input = args[i+1]
	}
	samples, stored, stderr, err := r.open(ctx, strings.TrimPrefix(input, "file:"))
	if err != nil {
		return "", stderr, err
	}
	if name == "ffprobe" {
		return probe(samples, stored), "", nil
	}
	return convert(samples, args)
}

type exitError int

func (e exitError) Error() string { return "exit status " + strconv.Itoa(int(e)) }

func (r *Runner) open(ctx context.Context, path string) (samples []int16, stored bool, stderr string, err error) {
	r.mu.Lock()
	samples, served := r.served[path]
	message, failing := r.failing[path]
	hanging := r.hanging[path]
	r.mu.Unlock()
	switch {
	case hanging:
		<-ctx.Done()
		return nil, false, "", ctx.Err()
	case failing:
		return nil, false, message + "\n", exitError(1)
	case served:
		return samples, false, "", nil
	}
	data, err := os.ReadFile(path)
	pcm, isClip := bytes.CutPrefix(data, clipMagic)
	if err != nil || !isClip || len(pcm)%2 != 0 {
		return nil, false, path + ": No such file or directory\n", exitError(1)
	}
	samples = make([]int16, len(pcm)/2)
	if _, err := binary.Decode(pcm, binary.LittleEndian, samples); err != nil {
		return nil, false, err.Error() + "\n", exitError(1)
	}
	return samples, true, "", nil
}

func probe(samples []int16, stored bool) string {
	codec, format, rate, channels := "pcm_s16le", "wav", 44100, 2
	if stored {
		codec, format, rate, channels = "flac", "flac", sampleRate, 1
	}
	return fmt.Sprintf(`{"streams":[{"codec_name":%q,"sample_rate":"%d","channels":%d}],"format":{"format_name":%q,"duration":"%.6f"}}`,
		codec, rate, channels, format, float64(len(samples))/sampleRate)
}

// convert answers the two ffmpeg calls the Inspector makes: a decode of the
// (trimmed) audio to stdout, and an encode of it to the file named last.
func convert(samples []int16, args []string) (stdout, stderr string, err error) {
	filter := args[slices.Index(args, "-af")+1]
	if _, trim, trimmed := strings.Cut(filter, ",atrim="); trimmed {
		start, end := 0, len(samples)
		for option := range strings.SplitSeq(trim, ":") {
			key, value, _ := strings.Cut(option, "=")
			n, err := strconv.Atoi(value)
			if err != nil {
				return "", "bad atrim option " + option + "\n", exitError(1)
			}
			if key == "start_sample" {
				start = n
			} else {
				end = n
			}
		}
		end = min(max(end, 0), len(samples))
		samples = samples[min(max(start, 0), end):end]
	}
	pcm, err := binary.Append(nil, binary.LittleEndian, samples)
	if err != nil {
		return "", err.Error() + "\n", exitError(1)
	}
	output := args[len(args)-1]
	if output == "pipe:1" {
		return string(pcm), "", nil
	}
	if err := os.WriteFile(strings.TrimPrefix(output, "file:"), append(slices.Clone(clipMagic), pcm...), 0o600); err != nil {
		return "", err.Error() + "\n", exitError(1)
	}
	return "", "", nil
}

// Tone returns seconds of a steady tone whose peak and mean volume are near
// dbfs.
func Tone(seconds, dbfs float64) []int16 {
	const halfPeriod = 20
	amplitude := math.Round(32768 * math.Pow(10, dbfs/20))
	high, low := int16(min(amplitude, math.MaxInt16)), int16(max(-amplitude, math.MinInt16))
	samples := Silence(seconds)
	period := min(len(samples), 2*halfPeriod)
	for i := range period {
		samples[i] = high
		if i >= halfPeriod {
			samples[i] = low
		}
	}
	for filled := period; filled > 0 && filled < len(samples); filled *= 2 {
		copy(samples[filled:], samples[:filled])
	}
	return samples
}

// Silence returns seconds of zero samples.
func Silence(seconds float64) []int16 {
	return make([]int16, max(0, int(math.Round(seconds*sampleRate))))
}

// Speech returns seconds of audio the Inspector accepts as a clip when
// seconds is 3 to 10; different lengths give different clips.
func Speech(seconds float64) []int16 { return Tone(seconds, -20) }
