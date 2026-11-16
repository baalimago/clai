package generic

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/baalimago/clai/internal/diag"
)

// Heard receives how far into the audio a transcription has got: the end of
// the latest segment the provider sent.
type Heard func(end time.Duration)

type heardKey struct{}

// WithHeard asks the requests made with ctx to stream their transcript where
// the vendor offers it, and to report each segment's end to heard as it
// arrives (architecture/audio.md, Progress).
func WithHeard(ctx context.Context, heard Heard) context.Context {
	return context.WithValue(ctx, heardKey{}, heard)
}

// ReportHeard passes end to the listener WithHeard attached to ctx, if any.
func ReportHeard(ctx context.Context, end time.Duration) {
	if heard, ok := heardFrom(ctx); ok {
		heard(end)
	}
}

func heardFrom(ctx context.Context) (Heard, bool) {
	heard, ok := ctx.Value(heardKey{}).(Heard)
	return heard, ok && heard != nil
}

// ErrStreamEnded is a transcript stream that stopped before
// transcript.text.done: the transcript is incomplete.
var ErrStreamEnded = errors.New("transcription stream ended before transcript.text.done")

// maxStreamEvent bounds one event; transcript.text.done carries the whole text.
const maxStreamEvent = 16 << 20

type wireStreamEvent struct {
	Type string `json:"type"`
	wireSegment
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

// ParseStream reads a diarized transcript sent as server-sent events: one
// transcript.text.segment per segment, then transcript.text.done (measured
// 2026-11-16, architecture/audio.md). heard, when set, gets each segment's
// end as it is read.
func ParseStream(r io.Reader, heard Heard) ([]Segment, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64<<10), maxStreamEvent)
	segs := []Segment{}
	text := ""
	done := false
	for !done && scanner.Scan() {
		data, ok := strings.CutPrefix(scanner.Text(), "data:")
		data = strings.TrimSpace(data)
		if !ok || data == "[DONE]" {
			continue
		}
		var event wireStreamEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return nil, diag.Errorf("failed to parse transcription stream event: %w", err)
		}
		switch event.Type {
		case "transcript.text.segment":
			seg := Segment{
				Start:   SecondsToDuration(event.Start),
				End:     SecondsToDuration(event.End),
				Speaker: event.Speaker,
				Text:    strings.TrimSpace(event.Text),
			}
			segs = append(segs, seg)
			if heard != nil {
				heard(seg.End)
			}
		case "transcript.text.done":
			text, done = strings.TrimSpace(event.Text), true
		case "error":
			return nil, fmt.Errorf("transcription stream failed: %v", event.Error.Message)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, diag.Errorf("failed to read transcription stream: %w", err)
	}
	if !done {
		return nil, ErrStreamEnded
	}
	if len(segs) == 0 && text != "" {
		return nil, fmt.Errorf("transcription stream has text but no segments: %q", truncate([]byte(text), 120))
	}
	return segs, nil
}
