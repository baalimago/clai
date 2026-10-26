package board

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/baalimago/clai/internal/utils"
)

// Status is a one-line live indicator in the board's look: "▸ title  ⠋ 12s".
// Start draws it on the cursor's row and animates it; Stop erases that row and
// leaves the cursor at its start. A status that is not live writes nothing.
type Status struct {
	ctl   sync.Mutex // serializes Start and Stop
	mu    sync.Mutex // guards drawing state
	out   io.Writer
	live  bool
	width int
	// measure reads the terminal's width before each frame, so a narrowed
	// terminal never wraps the row; nil keeps width.
	measure func() int
	title   string
	detail  string
	now     func() time.Time
	started time.Time
	frame   int
	running bool
	stop    chan struct{}
	done    chan struct{}
}

// NewStatus creates a stopped status; live is true when out is a terminal.
func NewStatus(out io.Writer, live bool, title string) *Status {
	if out == nil {
		out = os.Stderr
	}
	s := &Status{out: out, live: live, title: title, now: time.Now}
	if live {
		s.measure = func() int { return utils.SessionDimensions(out).Width }
	}
	return s
}

// SetDetail changes the text drawn after the elapsed time, such as a
// count of files; it shows from the next frame.
func (s *Status) SetDetail(detail string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.detail = detail
}

// SetClock replaces the clock (tests).
func (s *Status) SetClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
}

// SetWidth fixes the truncation width (tests).
func (s *Status) SetWidth(width int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.width = width
	s.measure = nil
}

// Start shows the status and counts elapsed time from now. Starting a running
// status keeps its first start time.
func (s *Status) Start() {
	if s == nil || !s.live {
		return
	}
	s.ctl.Lock()
	defer s.ctl.Unlock()
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.started = s.now()
	s.frame = 0
	s.draw()
	stop, done := make(chan struct{}), make(chan struct{})
	s.stop, s.done = stop, done
	s.mu.Unlock()
	go s.animate(stop, done)
}

// Stop erases the status. Stopping a stopped status writes nothing.
func (s *Status) Stop() {
	if s == nil || !s.live {
		return
	}
	s.ctl.Lock()
	defer s.ctl.Unlock()
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	stop, done := s.stop, s.done
	s.mu.Unlock()
	close(stop)
	<-done
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = false
	fmt.Fprint(s.out, "\r\x1b[2K")
}

func (s *Status) animate(stop, done chan struct{}) {
	defer close(done)
	t := time.NewTicker(TickInterval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			s.tick()
		}
	}
}

// tick advances the spinner and redraws while running.
func (s *Status) tick() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return
	}
	s.frame++
	s.draw()
}

// draw writes the current frame over the cursor's row (caller holds mu).
func (s *Status) draw() {
	if s.measure != nil {
		s.width = s.measure()
	}
	spinner := string(spinnerFrames[s.frame%len(spinnerFrames)])
	line := colorize(utils.TableTheme().Primary, "▸ "+s.title) + "  " + spinner + " " + ElapsedText(s.now().Sub(s.started))
	if s.detail != "" {
		line += " · " + s.detail
	}
	fmt.Fprint(s.out, "\r\x1b[2K"+Truncate(line, s.width))
}
