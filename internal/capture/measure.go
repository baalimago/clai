package capture

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"
)

// MeasureRequest is one ambient measurement: the loudest level the default
// microphone reaches during Window.
type MeasureRequest struct {
	Window time.Duration
}

// Ambient is the level one measurement heard.
type Ambient struct {
	Source Source
	// LevelDB is the median frame RMS_level over the window, in dBFS, and -Inf
	// when every frame was digital silence.
	LevelDB float64
}

func (recorder) Measure(ctx context.Context, req MeasureRequest) (Ambient, error) {
	if req.Window <= 0 {
		return Ambient{}, fmt.Errorf("measurement window must be positive, got %v", req.Window)
	}
	ffmpeg, err := lookFFmpeg()
	if err != nil {
		return Ambient{}, err
	}
	known, err := sources(ctx, ffmpeg)
	if err != nil {
		return Ambient{}, err
	}
	selected, err := resolveSelection(known, Request{Selection: Selection{Sinks: "none", Mic: true}})
	if err != nil {
		return Ambient{}, err
	}
	source := selected[0]

	// The window ends the process by itself: nothing needs the stop handshake
	// of a recording, and no file needs finalizing.
	cmd := exec.CommandContext(ctx, ffmpeg, measureArgs(source, req.Window)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return Ambient{}, fmt.Errorf("measure ambient level: %w", ctxErr)
	}
	level, found := medianRMS(stdout.String())
	if !found || runErr != nil {
		return Ambient{}, childFailure("ffmpeg measured no ambient level", runErr, stderr.String())
	}
	return Ambient{Source: source, LevelDB: level}, nil
}

func measureArgs(source Source, window time.Duration) []string {
	args := []string{"-hide_banner", "-nostdin", "-loglevel", "error"}
	args = append(args, inputArgsForSources([]Source{source})...)
	return append(args,
		"-t", strconv.FormatFloat(window.Seconds(), 'f', -1, 64),
		"-af", "astats=metadata=1:reset=1,ametadata=mode=print:file=-",
		"-f", "null", "-")
}

const rmsKey = "lavfi.astats.Overall.RMS_level="

// medianRMS is the middle RMS_level of ametadata's dump, and the higher of the
// two middle frames when the count is even. silencedetect compares a frame's
// average power to its noise threshold, so a steady room is described by the
// middle frame and not by a click that reaches the loudest peak. ffmpeg prints
// -inf for digital silence.
func medianRMS(dump string) (float64, bool) {
	var levels []float64
	for raw := range strings.SplitSeq(dump, "\n") {
		value, ok := strings.CutPrefix(strings.TrimSpace(raw), rmsKey)
		if !ok {
			continue
		}
		level, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil {
			continue
		}
		levels = append(levels, level)
	}
	if len(levels) == 0 {
		return 0, false
	}
	slices.Sort(levels)
	return levels[len(levels)/2], true
}
