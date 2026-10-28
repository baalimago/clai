//go:build linux

package capture

import (
	"strings"
	"testing"
)

func TestSources_Linux(t *testing.T) {
	sources, err := parsePulseList(`Auto-detected sources for pulse:
  alsa_output.pci-0000_00_1f.3.analog-stereo.monitor [Monitor of Built-in Audio Analog Stereo] (none)
* alsa_input.pci-0000_00_1f.3.analog-stereo [Built-in Audio Analog Stereo] (none)
`, "sources")
	if err != nil {
		t.Fatalf("parse sources: %v", err)
	}
	if len(sources) != 2 {
		t.Fatalf("sources = %+v, want two entries", sources)
	}
	if sources[1].Name != "alsa_input.pci-0000_00_1f.3.analog-stereo" || !sources[1].Default {
		t.Fatalf("default mic = %+v", sources[1])
	}
	if sources[1].Label != "Built-in Audio Analog Stereo" {
		t.Fatalf("label = %q", sources[1].Label)
	}

	sinks, err := parsePulseList(`Auto-detected sinks for pulse:
* alsa_output.pci-0000_00_1f.3.analog-stereo [Built-in Audio Analog Stereo] (none)
`, "sinks")
	if err != nil {
		t.Fatalf("parse sinks: %v", err)
	}
	monitors := monitors(sources, sinks)
	if len(monitors) != 1 || monitors[0].Kind != Sink || !monitors[0].Default {
		t.Fatalf("monitors = %+v, want the default sink's monitor", monitors)
	}
}

func TestSources_LinuxFailures(t *testing.T) {
	_, err := parsePulseList("some unrelated output", "sources")
	if err == nil || !strings.Contains(err.Error(), "libpulse") {
		t.Fatalf("error = %v, want a libpulse remedy", err)
	}

	_, err = parsePulseList("Auto-detected sources for pulse:\nCannot list sources: Connection refused\n", "sources")
	if err == nil || !strings.Contains(err.Error(), "pulse server") || !strings.Contains(err.Error(), "start") {
		t.Fatalf("error = %v, want a stopped-server remedy", err)
	}
}
