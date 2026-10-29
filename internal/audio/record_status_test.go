package audio

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/capture"
	"github.com/baalimago/clai/internal/utils"
)

func TestRecordingPlace(t *testing.T) {
	const dir = "/home/imago/Recordings"
	tests := []struct {
		name       string
		cfg        CaptureConfig
		enterStops bool
		want       string
	}{
		{
			name:       "every stop",
			cfg:        CaptureConfig{SilenceTimeout: 600, MaxSeconds: 5400},
			enterStops: true,
			want:       dir + " · stops: [Enter], 10m silence, max 1h30m",
		},
		{
			name: "no terminal line",
			cfg:  CaptureConfig{SilenceTimeout: 600, MaxSeconds: 5400},
			want: dir + " · stops: 10m silence, max 1h30m",
		},
		{
			name:       "only Enter",
			enterStops: true,
			want:       dir + " · stops: [Enter]",
		},
		{
			name: "sub-minute thresholds keep their seconds",
			cfg:  CaptureConfig{SilenceTimeout: 90, MaxSeconds: 45},
			want: dir + " · stops: 1m30s silence, max 45s",
		},
		{
			name: "no stop at all",
			want: dir,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := recordingPlace(dir, tc.cfg, tc.enterStops); got != tc.want {
				t.Fatalf("recordingPlace = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStopText(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want string
	}{
		{45 * time.Second, "45s"},
		{90 * time.Second, "1m30s"},
		{10 * time.Minute, "10m"},
		{90 * time.Minute, "1h30m"},
		{2 * time.Hour, "2h"},
		{time.Minute + 500*time.Millisecond, "1m1s"},
	}
	for _, tc := range tests {
		if got := stopText(tc.in); got != tc.want {
			t.Errorf("stopText(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRecordNoticeDetail(t *testing.T) {
	t0 := time.Date(2026, 10, 29, 8, 0, 0, 0, time.UTC)
	cfg := CaptureConfig{SilencePeakDB: -40, SilenceTimeout: 600, MaxSeconds: 5400}
	notice := newRecordNotice(io.Discard, false, cfg, "/r · stops: [Enter]")
	notice.now = func() time.Time { return t0 }

	if got, want := notice.detail(t0), "threshold -40 dB · /r · stops: [Enter]"; got != want {
		t.Fatalf("no event yet: detail = %q, want %q", got, want)
	}

	notice.setState(capture.StateSound)
	if got, want := notice.detail(t0.Add(time.Second)), "▮ sound · threshold -40 dB · /r · stops: [Enter]"; got != want {
		t.Fatalf("sound: detail = %q, want %q", got, want)
	}

	notice.setState(capture.StateSilent)
	if got, want := notice.detail(t0.Add(6*time.Second)), "▁ silent 00:06 / 10m · threshold -40 dB · /r · stops: [Enter]"; got != want {
		t.Fatalf("silent: detail = %q, want %q", got, want)
	}

	notice.cfg.SilenceTimeout = 0
	if got, want := notice.detail(t0.Add(6*time.Second)), "▁ silent 00:06 · threshold -40 dB · /r · stops: [Enter]"; got != want {
		t.Fatalf("no silence stop: detail = %q, want %q", got, want)
	}
}

func TestRecordNoticeDetail_SilenceClockRestartsOnSound(t *testing.T) {
	now := time.Date(2026, 10, 29, 8, 0, 0, 0, time.UTC)
	notice := newRecordNotice(io.Discard, false, CaptureConfig{SilencePeakDB: -30.5}, "/r")
	notice.now = func() time.Time { return now }

	notice.setState(capture.StateSilent)
	now = now.Add(3 * time.Second)
	notice.setState(capture.StateSound)
	now = now.Add(2 * time.Second)
	notice.setState(capture.StateSilent)
	now = now.Add(4 * time.Second)

	if got, want := notice.detail(now), "▁ silent 00:04 · threshold -30.5 dB · /r"; got != want {
		t.Fatalf("detail = %q, want %q", got, want)
	}
}

func TestDbText(t *testing.T) {
	for _, tc := range []struct {
		in   float64
		want string
	}{{-40, "-40"}, {-40.5, "-40.5"}, {-12, "-12"}} {
		if got := dbText(tc.in); got != tc.want {
			t.Errorf("dbText(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRecordNotice(t *testing.T) {
	cfg := CaptureConfig{SilencePeakDB: -40}

	t.Run("off a terminal it is one plain line", func(t *testing.T) {
		var out bytes.Buffer
		notice := newRecordNotice(&out, false, cfg, "/r · stops: [Enter]")
		if err := notice.start(); err != nil {
			t.Fatalf("start: %v", err)
		}
		notice.stopNotice()
		if got := out.String(); got != "recording /r · stops: [Enter]\n" {
			t.Fatalf("plain notice = %q", got)
		}
	})

	t.Run("a terminal gets the live row and it erases itself", func(t *testing.T) {
		plainStatus(t)
		var out bytes.Buffer
		notice := newRecordNotice(&out, true, cfg, "/r · stops: [Enter]")
		if err := notice.start(); err != nil {
			t.Fatalf("start: %v", err)
		}
		notice.stopNotice()
		got := out.String()
		for _, want := range []string{"▸ recording", "threshold -40 dB", "/r · stops: [Enter]"} {
			if !strings.Contains(got, want) {
				t.Fatalf("live row = %q, want %q", got, want)
			}
		}
		if !strings.HasSuffix(got, "\r\x1b[2K") {
			t.Fatalf("live row = %q, want it erased when the recording stops", got)
		}
	})

	t.Run("stopping a notice that never started writes nothing", func(t *testing.T) {
		var out bytes.Buffer
		notice := newRecordNotice(&out, true, cfg, "/r")
		notice.stopNotice()
		if out.Len() != 0 {
			t.Fatalf("stop wrote %q", out.String())
		}
	})

	t.Run("a failed plain line is an error", func(t *testing.T) {
		notice := newRecordNotice(failingWriter{}, false, cfg, "/r")
		if err := notice.start(); err == nil || !strings.Contains(err.Error(), "print recording notice") {
			t.Fatalf("error = %v, want a notice write error", err)
		}
	})

	t.Run("notes print above the row", func(t *testing.T) {
		var live bytes.Buffer
		notice := newRecordNotice(&live, true, cfg, "/r")
		if err := notice.note("acknowledged"); err != nil {
			t.Fatalf("note: %v", err)
		}
		if got, want := live.String(), "\r\x1b[2Kacknowledged\n"; got != want {
			t.Fatalf("live note = %q, want %q", got, want)
		}

		var plain bytes.Buffer
		notice = newRecordNotice(&plain, false, cfg, "/r")
		if err := notice.note("acknowledged"); err != nil {
			t.Fatalf("note: %v", err)
		}
		if got, want := plain.String(), "acknowledged\n"; got != want {
			t.Fatalf("plain note = %q, want %q", got, want)
		}
	})

	t.Run("a failed note is an error", func(t *testing.T) {
		notice := newRecordNotice(failingWriter{}, true, cfg, "/r")
		if err := notice.note("x"); err == nil || !strings.Contains(err.Error(), "print recording note") {
			t.Fatalf("error = %v, want a note write error", err)
		}
	})
}

func TestRecordNotice_Acknowledge(t *testing.T) {
	t.Run("a stored acknowledgement is reported", func(t *testing.T) {
		setAudioConfig(t, map[string]any{"dir": t.TempDir()})
		var out bytes.Buffer
		notice := newRecordNotice(&out, false, CaptureConfig{}, "/r")
		notice.acknowledge()
		if got := out.String(); got != "silence calibration acknowledged\n" {
			t.Fatalf("acknowledge wrote %q", got)
		}
		if !readAudioConfig(t).Capture.Calibrated {
			t.Fatal("acknowledge did not store calibrated")
		}
	})

	t.Run("a failed store is reported and leaves the notice alone", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("CLAI_CONFIG_DIR", dir)
		var out bytes.Buffer
		notice := newRecordNotice(&out, false, CaptureConfig{}, "/r")
		notice.acknowledge()
		if got := out.String(); !strings.Contains(got, "silence calibration not stored") {
			t.Fatalf("acknowledge wrote %q, want the failure", got)
		}
	})
}

func TestStoreCalibration(t *testing.T) {
	t.Run("preserves the other fields", func(t *testing.T) {
		dir := t.TempDir()
		setAudioConfig(t, map[string]any{"dir": dir, "silence-peak-db": -33, "silence-timeout": 30})
		if err := storeCalibration(); err != nil {
			t.Fatalf("storeCalibration: %v", err)
		}
		got := readAudioConfig(t)
		if !got.Capture.Calibrated || got.Capture.SilencePeakDB != -33 || got.Capture.SilenceTimeout != 30 {
			t.Fatalf("stored config = %+v, want calibrated with the other fields kept", got.Capture)
		}
	})

	t.Run("an unreadable config is an error", func(t *testing.T) {
		t.Setenv("CLAI_CONFIG_DIR", t.TempDir())
		if err := storeCalibration(); err == nil || !strings.Contains(err.Error(), "read audioConfig.json") {
			t.Fatalf("error = %v, want a read error", err)
		}
	})

	t.Run("a broken config is an error", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("CLAI_CONFIG_DIR", dir)
		if err := os.WriteFile(filepath.Join(dir, "audioConfig.json"), []byte("{"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := storeCalibration(); err == nil || !strings.Contains(err.Error(), "parse audioConfig.json") {
			t.Fatalf("error = %v, want a parse error", err)
		}
	})

	t.Run("a read-only run is an error", func(t *testing.T) {
		prev := utils.ReadonlyConfig
		utils.ReadonlyConfig = true
		t.Cleanup(func() { utils.ReadonlyConfig = prev })
		if err := storeCalibration(); err == nil || !strings.Contains(err.Error(), "read-only") {
			t.Fatalf("error = %v, want a read-only error", err)
		}
	})
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }

// readAudioConfig decodes the file the audio commands write.
func readAudioConfig(t *testing.T) Configurations {
	t.Helper()
	dir, err := utils.GetClaiConfigDir()
	if err != nil {
		t.Fatalf("config dir: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "audioConfig.json"))
	if err != nil {
		t.Fatalf("read audioConfig.json: %v", err)
	}
	var conf Configurations
	if err := json.Unmarshal(raw, &conf); err != nil {
		t.Fatalf("parse audioConfig.json: %v", err)
	}
	return conf
}
