package openrouter

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/baalimago/clai/internal/audio/generic"
	"github.com/baalimago/clai/internal/diag"
)

type diarizedPayload struct {
	Segments *[]diarizedUnit `json:"segments"`
	Words    *[]diarizedUnit `json:"words"`
}

// diarizedUnit is a segment (text) or a word (word); both may carry a speaker.
type diarizedUnit struct {
	Text         string  `json:"text"`
	Word         string  `json:"word"`
	Start        float64 `json:"start"`
	End          float64 `json:"end"`
	Speaker      *int    `json:"speaker"`
	SpeakerLabel string  `json:"speaker_label"`
}

func (u diarizedUnit) speakerKey() string {
	if u.SpeakerLabel != "" {
		return u.SpeakerLabel
	}
	if u.Speaker != nil {
		return fmt.Sprintf("speaker_%d", *u.Speaker)
	}
	return ""
}

func (u diarizedUnit) content() string {
	if u.Word != "" {
		return strings.TrimSpace(u.Word)
	}
	return strings.TrimSpace(u.Text)
}

var (
	// ErrNoSpeakerLabels reports speech that a diarize request returned without any label.
	ErrNoSpeakerLabels = errors.New("diarized payload carries no speaker labels")
	// ErrMalformedDiarized reports a diarized payload that cannot be read.
	ErrMalformedDiarized = errors.New("malformed diarized verbose_json payload")
)

// ParseDiarized maps a diarize=true verbose_json payload to speaker turns A, B, … (architecture/audio.md).
func ParseDiarized(data []byte) ([]generic.Segment, error) {
	var payload diarizedPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, diag.Errorf("%w: %w", ErrMalformedDiarized, err)
	}
	if payload.Segments == nil && payload.Words == nil {
		return nil, diag.Errorf("%w: neither segments nor words", ErrMalformedDiarized)
	}
	var segments, words []diarizedUnit
	if payload.Segments != nil {
		segments = *payload.Segments
	}
	if payload.Words != nil {
		words = *payload.Words
	}
	switch {
	case labeled(words):
		return relabel(turns(words)), nil
	case labeled(segments):
		return relabel(asSegments(segments)), nil
	case spoken(segments) || spoken(words):
		return nil, ErrNoSpeakerLabels
	}
	return []generic.Segment{}, nil
}

func labeled(units []diarizedUnit) bool {
	for _, u := range units {
		if u.speakerKey() != "" {
			return true
		}
	}
	return false
}

func spoken(units []diarizedUnit) bool {
	for _, u := range units {
		if u.content() != "" {
			return true
		}
	}
	return false
}

func asSegments(units []diarizedUnit) []generic.Segment {
	segs := make([]generic.Segment, 0, len(units))
	for _, u := range units {
		segs = append(segs, generic.Segment{
			Start:   generic.SecondsToDuration(u.Start),
			End:     generic.SecondsToDuration(u.End),
			Speaker: u.speakerKey(),
			Text:    u.content(),
		})
	}
	return segs
}

// turns joins consecutive words of one speaker; an unlabeled word stays in its turn.
func turns(words []diarizedUnit) []generic.Segment {
	var segs []generic.Segment
	var text []string
	flush := func() {
		if len(segs) > 0 {
			segs[len(segs)-1].Text = strings.Join(text, " ")
		}
		text = text[:0]
	}
	for _, w := range words {
		content := w.content()
		if content == "" {
			continue
		}
		key := w.speakerKey()
		start, end := generic.SecondsToDuration(w.Start), generic.SecondsToDuration(w.End)
		if len(segs) == 0 || (key != "" && key != segs[len(segs)-1].Speaker) {
			flush()
			segs = append(segs, generic.Segment{Start: start, End: end, Speaker: key})
		}
		current := &segs[len(segs)-1]
		current.End = max(current.End, end)
		text = append(text, content)
	}
	flush()
	return segs
}

// relabel renames provider labels to A, B, … in order of first appearance.
func relabel(segs []generic.Segment) []generic.Segment {
	names := map[string]string{}
	for i, s := range segs {
		if s.Speaker == "" {
			continue
		}
		name, ok := names[s.Speaker]
		if !ok {
			name = letters(len(names))
			names[s.Speaker] = name
		}
		segs[i].Speaker = name
	}
	return segs
}

// letters is the bijective base-26 name of n: A … Z, AA, AB, …
func letters(n int) string {
	name := ""
	for n++; n > 0; n = (n - 1) / 26 {
		name = string(rune('A'+(n-1)%26)) + name
	}
	return name
}
