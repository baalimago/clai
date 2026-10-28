// Package capture records this machine's audio with one ffmpeg process: it
// discovers the sources, builds the argument line, and interrupts that process
// to stop. ffmpeg is the only required program.
package capture

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	// ErrUnsupported means this build has no capture backend for the platform.
	ErrUnsupported = errors.New("audio capture is not supported on this platform; it needs linux or macOS")
	// ErrNoSource means the selection resolved to no source.
	ErrNoSource = errors.New("no capture source selected")
	// ErrNoAudio means no source reached the silence threshold, so no file stays.
	ErrNoAudio = errors.New("no audio captured")
	// ErrInterrupted means the caller cancelled the recording; the message holds the saved path.
	ErrInterrupted = errors.New("recording interrupted")
)

// discoveryCancellation reports the caller's cancellation when the context
// ended while a discovery child ran: exec kills that child, and its signal
// must not stand in for the cancellation (A01).
func discoveryCancellation(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("source discovery: %w", err)
	}
	return nil
}

// childFailure names a failed child, its error and the stderr it wrote, and
// leaves the tail out when the child wrote nothing.
func childFailure(summary string, err error, stderr string) error {
	if tail := strings.TrimSpace(stderr); tail != "" {
		return fmt.Errorf("%s: %w: %s", summary, err, tail)
	}
	return fmt.Errorf("%s: %w", summary, err)
}

// Kind classifies one capture source.
type Kind string

const (
	// Mic captures an input device.
	Mic Kind = "mic"
	// Sink captures what an output plays, through its monitor or a loopback.
	Sink Kind = "sink"
)

// Source is one audio input. Name is what the capture command receives;
// Default marks the source a role selection resolves to.
type Source struct {
	Kind    Kind
	Name    string
	Label   string
	Default bool
}

// Selection chooses sources by role so a configuration stays portable. Sinks
// is "default", "all" or "none".
type Selection struct {
	Sinks string
	Mic   bool
}

// StopReason is why one recording ended.
type StopReason string

const (
	StopSilence    StopReason = "silence"
	StopMaxSeconds StopReason = "max_seconds"
	StopEnter      StopReason = "enter"
	StopInterrupt  StopReason = "interrupt"
	StopError      StopReason = "error"
)

// CaptureState is the silence state that the running capture reports.
type CaptureState string

const (
	// StateSilent means every frame of the last window stayed below the
	// silence threshold.
	StateSilent CaptureState = "silent"
	// StateSound means a frame of the last window reached the threshold.
	StateSound CaptureState = "sound"
)

// Request is one recording. Dir must be absolute. Devices overrides Selection
// when it is not empty. Stop ends the recording with StopEnter when it is
// closed; nil disables it. A zero SilenceTimeout disables the silence stop.
// OnState runs on the reader goroutine that parses the capture output, so it
// must not block; nil disables it.
type Request struct {
	Devices   []string
	Selection Selection

	Dir        string
	NameSuffix string

	MaxDuration    time.Duration // 0 disables the duration stop
	SilenceTimeout time.Duration
	SilencePeakDB  float64

	Stop    <-chan struct{}
	OnState func(CaptureState)
}

// Recording is the outcome of one recording. Sources are the sources actually
// captured, in start order.
type Recording struct {
	Path           string
	StartedAt      time.Time
	EndedAt        time.Time
	Duration       time.Duration
	StoppedBecause StopReason
	Sources        []Source
}

// Recorder captures this machine's audio, the same way on every supported
// platform.
type Recorder interface {
	// Sources lists what this machine can capture now, mics first.
	Sources(ctx context.Context) ([]Source, error)
	// Record captures the chosen sources into one WAV file and reports its facts.
	Record(ctx context.Context, req Request) (Recording, error)
	// Measure reports the ambient level the default microphone holds during the
	// request's window, without writing a file.
	Measure(ctx context.Context, req MeasureRequest) (Ambient, error)
}

// Supported reports whether this build captures audio on this platform.
func Supported() bool { return supportedForGOOS() }

// New returns the production Recorder.
func New() Recorder { return newRecorder() }
