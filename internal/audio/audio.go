// Package audio holds the vendor-agnostic transcription segment model,
// wire-payload normalization, local output rendering, and the ffmpeg-backed
// split/stitch orchestration for oversized files. No HTTP: vendor clients
// live in audio/generic and internal/vendors.
package audio

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/baalimago/clai/internal/audio/generic"
)

// Segment aliases the shared transcript unit, defined in audio/generic so
// vendor payload parsing sits below the vendor clients (no upward import).
type Segment = generic.Segment

// Parser re-exports keep this package's historical API surface.
var (
	ParseVerboseJSON  = generic.ParseVerboseJSON
	ParseDiarizedJSON = generic.ParseDiarizedJSON
)

type OutputFormat string

const (
	FormatVTT  OutputFormat = "vtt"
	FormatSRT  OutputFormat = "srt"
	FormatText OutputFormat = "text"
	FormatJSON OutputFormat = "json"
)

// ParseOutputFormat validates a flag/config value into an OutputFormat.
func ParseOutputFormat(s string) (OutputFormat, error) {
	switch OutputFormat(s) {
	case FormatVTT, FormatSRT, FormatText, FormatJSON:
		return OutputFormat(s), nil
	default:
		return "", fmt.Errorf("unknown output format: %q, valid formats are: vtt, srt, text, json", s)
	}
}

// Offset returns a copy of segs with all timestamps shifted by delta.
func Offset(segs []Segment, delta time.Duration) []Segment {
	shifted := make([]Segment, len(segs))
	for i, s := range segs {
		s.Start += delta
		s.End += delta
		shifted[i] = s
	}
	return shifted
}

// Render formats segments into the given output format.
func Render(segs []Segment, format OutputFormat) (string, error) {
	switch format {
	case FormatVTT:
		return renderVTT(segs), nil
	case FormatSRT:
		return renderSRT(segs), nil
	case FormatText:
		return renderText(segs), nil
	case FormatJSON:
		return renderJSON(segs)
	default:
		return "", fmt.Errorf("unknown output format: %q", format)
	}
}

func renderVTT(segs []Segment) string {
	var sb strings.Builder
	sb.WriteString("WEBVTT\n")
	for _, s := range segs {
		text := s.Text
		if s.Speaker != "" {
			text = fmt.Sprintf("<v %v>%v</v>", s.Speaker, s.Text)
		}
		fmt.Fprintf(&sb, "\n%v --> %v\n%v\n", formatTimestamp(s.Start, "."), formatTimestamp(s.End, "."), text)
	}
	return sb.String()
}

func renderSRT(segs []Segment) string {
	var sb strings.Builder
	for i, s := range segs {
		if i > 0 {
			sb.WriteString("\n")
		}
		fmt.Fprintf(&sb, "%v\n%v --> %v\n%v\n", i+1, formatTimestamp(s.Start, ","), formatTimestamp(s.End, ","), speakerPrefixed(s))
	}
	return sb.String()
}

func renderText(segs []Segment) string {
	var sb strings.Builder
	for _, s := range segs {
		sb.WriteString(speakerPrefixed(s))
		sb.WriteString("\n")
	}
	return sb.String()
}

func speakerPrefixed(s Segment) string {
	if s.Speaker == "" {
		return s.Text
	}
	return fmt.Sprintf("%v: %v", s.Speaker, s.Text)
}

// renderSegment is the render-time DTO for the json output format. Segment is
// never marshaled directly: time.Duration would emit nanosecond ints, while
// the wire contract is float seconds.
type renderSegment struct {
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Speaker string  `json:"speaker,omitempty"`
	Text    string  `json:"text"`
}

func renderJSON(segs []Segment) (string, error) {
	dtos := make([]renderSegment, 0, len(segs))
	for _, s := range segs {
		dtos = append(dtos, renderSegment{
			Start:   s.Start.Seconds(),
			End:     s.End.Seconds(),
			Speaker: s.Speaker,
			Text:    s.Text,
		})
	}
	b, err := json.Marshal(dtos)
	if err != nil {
		return "", fmt.Errorf("failed to marshal segments to json: %w", err)
	}
	return string(b), nil
}

func formatTimestamp(d time.Duration, millisSeparator string) string {
	sign := ""
	if d < 0 {
		sign = "-"
		d = -d
	}
	ms := d.Milliseconds()
	return fmt.Sprintf("%v%02d:%02d:%02d%v%03d",
		sign,
		ms/3600000,
		ms/60000%60,
		ms/1000%60,
		millisSeparator,
		ms%1000,
	)
}
