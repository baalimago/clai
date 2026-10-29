//go:build linux

package capture

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

func sources(ctx context.Context, ffmpeg string) ([]Source, error) {
	sinkList, err := listPulse(ctx, ffmpeg, "sinks")
	if err != nil {
		return nil, err
	}
	sourceList, err := listPulse(ctx, ffmpeg, "sources")
	if err != nil {
		return nil, err
	}

	var mics []Source
	for _, s := range sourceList {
		if isMonitor(s.Name) {
			continue
		}
		s.Kind = Mic
		mics = append(mics, s)
	}
	if len(mics) > 0 && !anyDefault(mics) {
		mics[0].Default = true
	}

	sinks := monitors(sourceList, sinkList)
	return append(mics, sinks...), nil
}

func listPulse(ctx context.Context, ffmpeg, kind string) ([]Source, error) {
	args := []string{"-hide_banner", "-" + kind, "pulse"}
	cmd := exec.CommandContext(ctx, ffmpeg, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if ctxErr := discoveryCancellation(ctx); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, childFailure(fmt.Sprintf("ffmpeg -%s pulse failed", kind), err, stderr.String())
	}
	return parsePulseList(string(out), kind)
}

func parsePulseList(out, kind string) ([]Source, error) {
	header := "Auto-detected " + kind + " for pulse:"
	if !strings.Contains(out, header) {
		return nil, fmt.Errorf("ffmpeg cannot list pulse %s: this ffmpeg has no libpulse; install an ffmpeg build with pulse support", kind)
	}
	if strings.Contains(out, "Cannot list "+kind+":") {
		return nil, fmt.Errorf("pulse server is not available; start pipewire-pulse or pulseaudio so ffmpeg can list %s", kind)
	}

	var list []Source
	for raw := range strings.SplitSeq(out, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "Auto-detected") {
			continue
		}
		entry := strings.TrimSpace(strings.TrimPrefix(line, "*"))
		name, rest, _ := strings.Cut(entry, " ")
		label := name
		if open := strings.Index(rest, "["); open >= 0 {
			if close := strings.Index(rest[open:], "]"); close > 0 {
				label = rest[open+1 : open+close]
			}
		}
		list = append(list, Source{Name: name, Label: label, Default: strings.HasPrefix(line, "*")})
	}
	return list, nil
}

func isMonitor(name string) bool { return strings.HasSuffix(name, ".monitor") }

func monitors(fromSources, fromSinks []Source) []Source {
	defaultSink := ""
	for _, s := range fromSinks {
		if s.Default {
			defaultSink = s.Name
			break
		}
	}
	defaultMonitor := defaultSink + ".monitor"

	var sinks []Source
	for _, s := range fromSources {
		if !isMonitor(s.Name) {
			continue
		}
		sinks = append(sinks, Source{Kind: Sink, Name: s.Name, Label: s.Label, Default: s.Name == defaultMonitor})
	}
	if len(sinks) > 0 && !anyDefault(sinks) {
		sinks[0].Default = true
	}
	return sinks
}

func anyDefault(sources []Source) bool {
	for _, s := range sources {
		if s.Default {
			return true
		}
	}
	return false
}
