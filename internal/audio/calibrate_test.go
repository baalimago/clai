package audio

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/capture"
)

func TestSilencePeakTarget(t *testing.T) {
	tests := []struct {
		name    string
		level   float64
		want    float64
		wantErr string
	}{
		{name: "a quiet room keeps the margin", level: -48.1, want: -42},
		{name: "the level is rounded before the margin", level: -45.6, want: -40},
		{name: "a loud room is clamped below zero", level: -0.4, want: -1},
		{name: "digital silence cannot be a threshold", level: math.Inf(-1), wantErr: "digital silence"},
		{name: "no number is not a level", level: math.NaN(), wantErr: "not a number"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := silencePeakTarget(tc.level)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("silencePeakTarget: %v", err)
			}
			if got != tc.want {
				t.Fatalf("target = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCalibrationText(t *testing.T) {
	ambient := capture.Ambient{
		Source:  capture.Source{Kind: capture.Mic, Name: "alsa_input.analog-stereo"},
		LevelDB: -44.2,
	}
	if got, want := calibrationText(ambient, -40, -38),
		`mic "alsa_input.analog-stereo" · ambient level -44 dB RMS · silence-peak-db -40 → -38`+"\n"; got != want {
		t.Fatalf("calibrationText = %q, want %q", got, want)
	}

	ambient.LevelDB = -0.4
	if got := calibrationText(ambient, -12, -1); !strings.Contains(got, "ambient level 0 dB") {
		t.Fatalf("calibrationText = %q, want a zero without a sign", got)
	}
}

func TestCalibrateText(t *testing.T) {
	t.Run("stores the ambient peak plus the margin", func(t *testing.T) {
		fakeFFmpeg(t)
		setAudioConfig(t, map[string]any{"dir": t.TempDir(), "silence-peak-db": -40, "silence-timeout": 30})

		text, err := calibrateText(context.Background(), capture.New(), time.Second)
		if err != nil {
			t.Fatalf("calibrateText: %v", err)
		}
		want := `mic "alsa_input.pci-0000_00_1f.3.analog-stereo" · ambient level -44 dB RMS · silence-peak-db -40 → -38` + "\n"
		if text != want {
			t.Fatalf("text = %q, want %q", text, want)
		}
		got := readAudioConfig(t).Capture
		if got.SilencePeakDB != -38 || !got.Calibrated {
			t.Fatalf("stored capture config = %+v, want peak -38 and calibrated", got)
		}
		if got.SilenceTimeout != 30 {
			t.Fatalf("silence-timeout = %v, want the file's 30", got.SilenceTimeout)
		}
	})

	t.Run("a digital-silence measurement leaves the file alone", func(t *testing.T) {
		fakeFFmpeg(t)
		t.Setenv("FAKE_FFMPEG_MEASURE_MODE", "silence")
		setAudioConfig(t, map[string]any{"dir": t.TempDir(), "silence-peak-db": -40})

		_, err := calibrateText(context.Background(), capture.New(), time.Second)
		if err == nil || !strings.Contains(err.Error(), "digital silence") {
			t.Fatalf("error = %v, want the digital-silence refusal", err)
		}
		got := readAudioConfig(t).Capture
		if got.SilencePeakDB != -40 || got.Calibrated {
			t.Fatalf("stored capture config = %+v, want the file untouched", got)
		}
	})

	t.Run("a measurement failure leaves the file alone", func(t *testing.T) {
		fakeFFmpeg(t)
		t.Setenv("FAKE_FFMPEG_MEASURE_MODE", "fail")
		setAudioConfig(t, map[string]any{"dir": t.TempDir(), "silence-peak-db": -40})

		_, err := calibrateText(context.Background(), capture.New(), time.Second)
		if err == nil || !strings.Contains(err.Error(), "device open failed") {
			t.Fatalf("error = %v, want the measurement failure", err)
		}
		if got := readAudioConfig(t).Capture; got.SilencePeakDB != -40 || got.Calibrated {
			t.Fatalf("stored capture config = %+v, want the file untouched", got)
		}
	})

	t.Run("a loud room is clamped and still stored", func(t *testing.T) {
		fakeFFmpeg(t)
		t.Setenv("FAKE_FFMPEG_MEASURE_MODE", "loud")
		setAudioConfig(t, map[string]any{"dir": t.TempDir(), "silence-peak-db": -40})

		text, err := calibrateText(context.Background(), capture.New(), time.Second)
		if err != nil {
			t.Fatalf("calibrateText: %v", err)
		}
		if !strings.Contains(text, "silence-peak-db -40 → -1") {
			t.Fatalf("text = %q, want the clamped threshold", text)
		}
		if got := readAudioConfig(t).Capture; got.SilencePeakDB != -1 || !got.Calibrated {
			t.Fatalf("stored capture config = %+v, want peak -1 and calibrated", got)
		}
	})
}
