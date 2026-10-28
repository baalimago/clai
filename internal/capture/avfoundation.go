package capture

import (
	"errors"
	"regexp"
	"strings"
)

var avDeviceLine = regexp.MustCompile(`^\s*\[(\d+)\]\s+(.+?)\s*$`)

func parseAVFoundationDevices(out string) ([]Source, error) {
	_, audioSection, ok := strings.Cut(out, "AVFoundation audio devices:")
	if !ok {
		return nil, errors.New("ffmpeg cannot list avfoundation devices: this ffmpeg has no avfoundation; install an ffmpeg build with avfoundation support")
	}

	var mics, sinks []Source
	for line := range strings.SplitSeq(audioSection, "\n") {
		match := avDeviceLine.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		source := Source{Kind: Mic, Name: match[1], Label: match[2]}
		if isLoopbackLabel(source.Label) {
			source.Kind = Sink
			sinks = append(sinks, source)
			continue
		}
		mics = append(mics, source)
	}
	if len(mics) > 0 {
		mics[0].Default = true
	}
	if len(sinks) > 0 {
		sinks[0].Default = true
	}
	return append(mics, sinks...), nil
}

func isLoopbackLabel(label string) bool {
	lower := strings.ToLower(label)
	for _, needle := range []string{"blackhole", "soundflower", "loopback audio", "aggregate"} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}
