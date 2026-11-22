package generic

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"time"
)

// StreamSummary is one model response as its debug record (architecture/output.md).
type StreamSummary struct {
	started       time.Time
	events        map[string]int
	bytes         int
	input, output int
	recorded      bool
}

func NewStreamSummary() *StreamSummary {
	return &StreamSummary{started: time.Now(), events: map[string]int{}}
}

// Body counts the bytes read through it.
func (s *StreamSummary) Body(r io.Reader) io.Reader { return summaryReader{r: r, s: s} }

// Event counts one event; a nil summary counts nothing.
func (s *StreamSummary) Event(kind string) {
	if s != nil {
		s.events[kind]++
	}
}

func (s *StreamSummary) Tokens(input, output int) {
	if s != nil {
		s.input, s.output = input, output
	}
}

// Record reports the summary once; call it before the stream's last event is sent.
func (s *StreamSummary) Record(report func(summary string)) {
	if s == nil || s.recorded {
		return
	}
	s.recorded = true
	report(s.String())
}

func (s *StreamSummary) String() string {
	total, kinds := 0, make([]string, 0, len(s.events))
	for _, kind := range slices.Sorted(maps.Keys(s.events)) {
		total += s.events[kind]
		kinds = append(kinds, fmt.Sprintf("%s:%d", kind, s.events[kind]))
	}
	return fmt.Sprintf("response events=%d bytes=%d tokens_in=%d tokens_out=%d took=%.1fs types=%s",
		total, s.bytes, s.input, s.output, time.Since(s.started).Seconds(), strings.Join(kinds, ","))
}

type summaryReader struct {
	r io.Reader
	s *StreamSummary
}

func (r summaryReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	r.s.bytes += n
	return n, err
}
