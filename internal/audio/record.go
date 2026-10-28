package audio

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/baalimago/clai/internal/capture"
	"github.com/baalimago/clai/internal/utils"
	pkgtools "github.com/baalimago/clai/pkg/tools"
)

// CaptureConfig is the capture section of audioConfig.json. It holds no
// device names, so a configuration stays portable between machines.
type CaptureConfig struct {
	Dir            string  `json:"dir"`
	MaxSeconds     int     `json:"max-seconds"`
	SilenceTimeout int     `json:"silence-timeout"`
	SilencePeakDB  float64 `json:"silence-peak-db"`
	Sinks          string  `json:"sinks"`
	Mic            *bool   `json:"mic"`
	// Calibrated records that the silence levels of this machine were measured
	// or accepted, so the calibration notice does not repeat.
	Calibrated bool `json:"calibrated"`
}

func recordText(ctx context.Context, call pkgtools.AudioRecordCall, r capture.Recorder, stdin io.Reader, stdinIsTTY bool) (string, error) {
	fileConfig, err := loadCaptureConfig()
	if err != nil {
		return "", err
	}
	config, err := resolveCaptureConfig(fileConfig, call)
	if err != nil {
		return "", err
	}
	dir, err := prepareRecordingDir(config.Dir)
	if err != nil {
		return "", err
	}

	notice := newRecordNotice(os.Stderr, utils.IsTerminalWriter(os.Stderr), config, recordingPlace(dir, config, stdinIsTTY))
	if !config.Calibrated && stdinIsTTY {
		if err := notice.note(calibrationNotice); err != nil {
			return "", err
		}
	}
	if err := notice.start(); err != nil {
		return "", err
	}
	defer notice.stopNotice()

	recording, err := r.Record(ctx, capture.Request{
		Devices:        call.Devices,
		Selection:      capture.Selection{Sinks: config.Sinks, Mic: *config.Mic},
		Dir:            dir,
		NameSuffix:     call.NameSuffix,
		MaxDuration:    time.Duration(config.MaxSeconds) * time.Second,
		SilenceTimeout: time.Duration(config.SilenceTimeout) * time.Second,
		SilencePeakDB:  config.SilencePeakDB,
		Stop:           stopOnKeys(stdin, stdinIsTTY, notice.acknowledge),
		OnState:        notice.setState,
	})
	if err != nil {
		return "", err
	}
	return recordingFacts(recording), nil
}

func loadCaptureConfig() (CaptureConfig, error) {
	configDir, err := utils.GetClaiConfigDir()
	if err != nil {
		return CaptureConfig{}, fmt.Errorf("find config dir: %w", err)
	}
	// The collect variant keeps the upgrade announcement off stdout: a tool
	// result is the only thing a model may receive.
	config, _, err := utils.LoadConfigFromFileCollect(configDir, "audioConfig.json", nil, &Default)
	if err != nil {
		return CaptureConfig{}, fmt.Errorf("load audioConfig.json: %w", err)
	}
	return config.Capture, nil
}

func resolveCaptureConfig(file CaptureConfig, call pkgtools.AudioRecordCall) (CaptureConfig, error) {
	config := file
	if call.Dir != "" {
		config.Dir = call.Dir
	}
	if call.MaxSeconds != 0 {
		config.MaxSeconds = call.MaxSeconds
	}
	if call.SilenceTimeout != 0 {
		config.SilenceTimeout = call.SilenceTimeout
	}
	if call.Sinks != "" {
		config.Sinks = call.Sinks
	}
	if call.Mic != nil {
		config.Mic = call.Mic
	}

	switch {
	case config.Dir == "":
		return CaptureConfig{}, errors.New("capture.dir is empty; set it in audioConfig.json")
	case config.MaxSeconds < 0:
		return CaptureConfig{}, fmt.Errorf("capture.max-seconds must not be negative, got %d", config.MaxSeconds)
	case config.SilenceTimeout < 0:
		return CaptureConfig{}, fmt.Errorf("capture.silence-timeout must not be negative, got %d", config.SilenceTimeout)
	case config.SilencePeakDB > 0:
		return CaptureConfig{}, fmt.Errorf("capture.silence-peak-db must not be positive, got %v", config.SilencePeakDB)
	}
	switch config.Sinks {
	case "default", "all", "none":
	default:
		return CaptureConfig{}, fmt.Errorf("capture.sinks must be one of default, all, none, got %q", config.Sinks)
	}
	if config.Mic == nil {
		config.Mic = new(true)
	}
	return config, nil
}

func prepareRecordingDir(dir string) (string, error) {
	absolute, err := utils.AbsHomePath(dir)
	if err != nil {
		return "", fmt.Errorf("resolve recording dir %q: %w", dir, err)
	}
	if err := os.MkdirAll(absolute, 0o755); err != nil {
		return "", fmt.Errorf("create recording dir %q: %w", absolute, err)
	}
	return absolute, nil
}

// calibrationNotice tells the user that the silence levels of this machine are
// still the defaults. It is line-based like every other clai prompt: A and
// Enter acknowledges, Enter alone stops the recording.
const calibrationNotice = "silence levels may not match this machine · run 'clai audio calibrate', or [A] and Enter to acknowledge"

// stopOnKeys stops the recording on a completed line, and acknowledges the
// calibration notice on "a". Only a completed line is an Enter press: /dev/null
// is a character device too, so it looks like a terminal, and closing here
// would interrupt ffmpeg before it wrote the WAV (worklog
// 2026-10-28-audio-record-tool, R3-01).
func stopOnKeys(stdin io.Reader, stdinIsTTY bool, ack func()) <-chan struct{} {
	if !stdinIsTTY || stdin == nil {
		return nil
	}
	stop := make(chan struct{})
	go func() {
		reader := bufio.NewReader(stdin)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			if strings.ToLower(strings.TrimSpace(line)) == "a" {
				if ack != nil {
					ack()
				}
				continue
			}
			close(stop)
			return
		}
	}()
	return stop
}

func recordingFacts(recording capture.Recording) string {
	var facts strings.Builder
	fmt.Fprintf(&facts, "recorded: %s\n", recording.Path)
	fmt.Fprintf(&facts, "started_at: %s\n", recording.StartedAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(&facts, "ended_at: %s\n", recording.EndedAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(&facts, "duration_seconds: %d\n", int64(recording.Duration.Seconds()))
	fmt.Fprintf(&facts, "stopped_because: %s\n", recording.StoppedBecause)
	labels := make([]string, 0, len(recording.Sources))
	for _, source := range recording.Sources {
		labels = append(labels, fmt.Sprintf("%s=%s", source.Kind, source.Label))
	}
	fmt.Fprintf(&facts, "sources: %s\n", strings.Join(labels, "; "))
	return facts.String()
}

func init() {
	pkgtools.AudioRecordEngine = func(ctx context.Context, call pkgtools.AudioRecordCall) (string, error) {
		return recordText(ctx, call, capture.New(), os.Stdin, utils.IsTerminalWriter(os.Stdin))
	}
}
