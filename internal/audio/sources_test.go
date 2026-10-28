package audio

import (
	"context"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/capture"
)

func TestSourcesText(t *testing.T) {
	fakeFFmpeg(t)
	rec := capture.New()

	out, err := sourcesText(context.Background(), rec)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %d, want 3\nout:\n%s", len(lines), out)
	}
	if lines[0] != "system_audio: available" {
		t.Fatalf("line0 = %q", lines[0])
	}
	if !strings.Contains(lines[1], "mic \"alsa_input.pci-0000_00_1f.3.analog-stereo\" [Built-in Audio Analog Stereo] default") {
		t.Fatalf("line1 = %q", lines[1])
	}
	if !strings.Contains(lines[2], "sink \"alsa_output.pci-0000_00_1f.3.analog-stereo.monitor\" [Monitor of Built-in Audio Analog Stereo] default") {
		t.Fatalf("line2 = %q", lines[2])
	}

	t.Setenv("FAKE_FFMPEG_SINKS_MODE", "empty")
	t.Setenv("FAKE_FFMPEG_SOURCES_MODE", "miconly")
	out, err = sourcesText(context.Background(), rec)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.HasPrefix(out, "system_audio: unavailable\nmic \"alsa_input.pci-0000_00_1f.3.analog-stereo\"") {
		t.Fatalf("want system_audio unavailable, got:\n%s", out)
	}

	t.Setenv("FAKE_FFMPEG_SOURCES_MODE", "refused")
	_, wantErr := rec.Sources(context.Background())
	if wantErr == nil {
		t.Fatal("the fixture's refused listing must be an error")
	}
	_, err = sourcesText(context.Background(), rec)
	if err == nil || err.Error() != wantErr.Error() {
		t.Fatalf("err = %v, want the recorder's error %v", err, wantErr)
	}
}
