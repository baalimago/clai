package audio

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/baalimago/clai/internal/capture"
)

// calibrateMarginDB is the headroom between the measured ambient peak and the
// silence threshold: a quiet room peaks below it, speech peaks above it.
const calibrateMarginDB = 6

// maxSilencePeakDB is the highest threshold a calibration writes. A threshold
// at or above 0 dB never marks a sample as silent.
const maxSilencePeakDB = -1

// calibrateText measures this machine's ambient level and stores the silence
// threshold above it, so silencedetect separates a quiet room from speech
// (architecture/audio.md, capture configuration).
func calibrateText(ctx context.Context, r capture.Recorder, window time.Duration) (string, error) {
	previous, err := loadCaptureConfig()
	if err != nil {
		return "", err
	}
	ambient, err := r.Measure(ctx, capture.MeasureRequest{Window: window})
	if err != nil {
		return "", err
	}
	target, err := silencePeakTarget(ambient.LevelDB)
	if err != nil {
		return "", err
	}
	if err := updateCaptureConfig(func(c *CaptureConfig) {
		c.SilencePeakDB = target
		c.Calibrated = true
	}); err != nil {
		return "", err
	}
	return calibrationText(ambient, previous.SilencePeakDB, target), nil
}

// silencePeakTarget is the threshold one measurement maps to. A muted or
// disconnected microphone reports digital silence, which no threshold can sit
// above.
func silencePeakTarget(levelDB float64) (float64, error) {
	if math.IsInf(levelDB, -1) {
		return 0, errors.New("the microphone reported digital silence; check that it is not muted or disconnected, then calibrate again")
	}
	if math.IsNaN(levelDB) {
		return 0, errors.New("ffmpeg reported a level that is not a number")
	}
	return min(math.Round(levelDB)+calibrateMarginDB, maxSilencePeakDB), nil
}

func calibrationText(ambient capture.Ambient, previous, target float64) string {
	level := math.Round(ambient.LevelDB)
	if level == 0 {
		level = math.Abs(level)
	}
	return fmt.Sprintf("%v %q · ambient level %v dB RMS · silence-peak-db %v → %v\n",
		ambient.Source.Kind, ambient.Source.Name, dbText(level), dbText(previous), dbText(target))
}
