package capture

import (
	"errors"
	"fmt"
	"strings"
)

func resolveSelection(known []Source, req Request) ([]Source, error) {
	byName := map[string]Source{}
	var knownNames []string
	var sinks []Source
	var mics []Source
	for _, s := range known {
		byName[s.Name] = s
		knownNames = append(knownNames, s.Name)
		switch s.Kind {
		case Sink:
			sinks = append(sinks, s)
		case Mic:
			mics = append(mics, s)
		}
	}

	if len(req.Devices) > 0 {
		var out []Source
		for _, name := range req.Devices {
			s, ok := byName[name]
			if !ok {
				return nil, fmt.Errorf("unknown capture device %q; known: %s", name, strings.Join(knownNames, ", "))
			}
			out = append(out, s)
		}
		if len(out) == 0 {
			return nil, ErrNoSource
		}
		return out, nil
	}

	wantSinks := req.Selection.Sinks
	if wantSinks == "" {
		wantSinks = "default"
	}

	var out []Source
	switch wantSinks {
	case "none":
		// none
	case "all":
		if len(sinks) == 0 {
			return nil, errNoSink
		}
		out = append(out, sinks...)
	case "default":
		for _, s := range sinks {
			if s.Default {
				out = append(out, s)
				break
			}
		}
		if len(out) == 0 && len(sinks) > 0 {
			out = append(out, sinks[0])
		}
		if len(out) == 0 && len(sinks) == 0 {
			return nil, errNoSink
		}
	default:
		return nil, fmt.Errorf("capture.sinks must be one of default, all, none, got %q", wantSinks)
	}

	if req.Selection.Mic {
		picked := false
		for _, m := range mics {
			if m.Default {
				out = append(out, m)
				picked = true
				break
			}
		}
		if !picked && len(mics) > 0 {
			out = append(out, mics[0])
			picked = true
		}
		if !picked {
			return nil, errors.New("no microphone capture source found; connect or enable an input device")
		}
	}

	if len(out) == 0 {
		return nil, ErrNoSource
	}
	return out, nil
}

var errNoSink = errors.New("no system-audio capture source found; install a loopback device such as BlackHole 2ch on macOS, or start pipewire-pulse or pulseaudio on linux")
