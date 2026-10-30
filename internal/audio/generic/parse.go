// Wire-level transcript normalization: the shared Segment model and the
// vendor payload parsers live below the vendor clients so that both the
// vendors and internal/audio can import them (text keeps the same shape
// with pkg/text/models).
package generic

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
)

// Segment is the shared transcript unit all vendor payloads normalize into
// and all output formats render from.
type Segment struct {
	Start   time.Duration
	End     time.Duration
	Speaker string // "" when model does not diarize
	Text    string
}

type wireSegment struct {
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Speaker string  `json:"speaker"`
	Text    string  `json:"text"`
}

type wirePayload struct {
	// Pointer distinguishes a missing segments field from an empty array
	Segments *[]wireSegment `json:"segments"`
}

func parsePayload(data []byte, formatName string) ([]Segment, error) {
	var payload wirePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("failed to parse %v payload: %w", formatName, err)
	}
	if payload.Segments == nil {
		return nil, fmt.Errorf("%v payload has no segments field", formatName)
	}
	segs := make([]Segment, 0, len(*payload.Segments))
	for _, ws := range *payload.Segments {
		segs = append(segs, Segment{
			Start:   SecondsToDuration(ws.Start),
			End:     SecondsToDuration(ws.End),
			Speaker: ws.Speaker,
			Text:    strings.TrimSpace(ws.Text),
		})
	}
	return segs, nil
}

// ParseVerboseJSON normalizes an OpenAI/OpenRouter verbose_json payload.
func ParseVerboseJSON(data []byte) ([]Segment, error) {
	return parsePayload(data, "verbose_json")
}

type wireUsage struct {
	Type    string  `json:"type"`
	Seconds float64 `json:"seconds"`
}

type wireTextPayload struct {
	// Pointer distinguishes a missing text field from an empty transcript
	Text  *string   `json:"text"`
	Usage wireUsage `json:"usage"`
}

// ParseJSON normalizes a text-only json payload into one segment ending at
// the billed duration when usage reports one, else at zero; empty text is silence.
func ParseJSON(data []byte) ([]Segment, error) {
	var payload wireTextPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("failed to parse json payload: %w", err)
	}
	if payload.Text == nil {
		return nil, fmt.Errorf("json payload has no text (field missing or null)")
	}
	text := strings.TrimSpace(*payload.Text)
	if text == "" {
		return []Segment{}, nil
	}
	var end time.Duration
	if payload.Usage.Type == "duration" {
		if payload.Usage.Seconds < 0 {
			return nil, fmt.Errorf("json payload reports negative duration: %v", payload.Usage.Seconds)
		}
		end = SecondsToDuration(payload.Usage.Seconds)
	}
	return []Segment{{Start: 0, End: end, Text: text}}, nil
}

// ParseDiarizedJSON normalizes a gpt-4o-transcribe-diarize diarized_json payload.
func ParseDiarizedJSON(data []byte) ([]Segment, error) {
	return parsePayload(data, "diarized_json")
}

// SecondsToDuration rounds to millisecond precision, matching subtitle
// timestamp resolution and keeping float-seconds render output clean.
func SecondsToDuration(s float64) time.Duration {
	return time.Duration(math.Round(s*1000)) * time.Millisecond
}
