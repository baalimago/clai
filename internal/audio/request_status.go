package audio

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/baalimago/clai/internal/board"
	"github.com/baalimago/clai/internal/display"
	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/internal/stdio"
)

// requestStatus is the progress of a transcription sent as one request,
// which has no chunk board (architecture/audio.md, Progress). Under -json
// with no StatusOut it reports one progress record per state change on the
// run's stderr; on a terminal it is a live row that erases itself; any other
// writer gets one line per state change.
type requestStatus struct {
	mu     sync.Mutex
	disp   display.Displayer
	live   *board.Status
	out    io.Writer
	said   string
	ending bool
}

func newRequestStatus(ctx context.Context, statusOut io.Writer, detail string) *requestStatus {
	env := runenv.From(ctx)
	if statusOut == nil && env.JSON() {
		if disp, err := env.Displayer(stdio.Stderr); err == nil {
			s := &requestStatus{disp: disp}
			s.report("start", detail)
			return s
		}
	}
	out, live := statusOf(ctx, statusOut)
	if live {
		return newLiveRequestStatus(out, env.Color(), detail)
	}
	s := &requestStatus{out: out}
	s.report("start", "transcribing "+detail)
	return s
}

func newLiveRequestStatus(out io.Writer, color bool, detail string) *requestStatus {
	status := board.NewStatus(out, true, "transcribing")
	status.SetColor(color)
	status.SetDetail(detail)
	status.Start()
	return &requestStatus{live: status}
}

// heard is the request's generic.Heard: one update per whole second reached.
func (s *requestStatus) heard(end time.Duration) {
	s.update("heard " + clock(end))
}

func (s *requestStatus) update(detail string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ending || detail == s.said {
		return
	}
	s.said = detail
	if s.live != nil {
		s.live.SetDetail(detail)
		return
	}
	s.report("update", detail)
}

// finish ends the status with the transcription's outcome; later calls do nothing.
func (s *requestStatus) finish(segs []Segment, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ending {
		return
	}
	s.ending = true
	if s.live != nil {
		s.live.Stop()
		return
	}
	if err != nil {
		first, _, _ := strings.Cut(err.Error(), "\n")
		s.report("failed", first)
		return
	}
	s.report("done", fmt.Sprintf("transcribed · %v segments", len(segs)))
}

// report writes one state; a failed write is ignored, as the chunk board
// ignores one (F6, F11).
func (s *requestStatus) report(state, detail string) {
	if s.disp != nil {
		_ = s.disp.ReportProgress(display.Progress{Title: transcribeProgressTitle, State: state, Detail: detail})
		return
	}
	_, _ = fmt.Fprintln(s.out, detail)
}
