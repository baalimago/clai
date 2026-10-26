package board

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

// fixedStatus returns a live status whose clock reads t0 on the first call and
// advance later on every further call.
func fixedStatus(out *bytes.Buffer, live bool, advance time.Duration) *Status {
	s := NewStatus(out, live, "generating image")
	s.SetWidth(80)
	t0 := time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)
	calls := 0
	s.SetClock(func() time.Time {
		calls++
		if calls == 1 {
			return t0
		}
		return t0.Add(advance)
	})
	return s
}

func TestStatus_NotLiveWritesNothing(t *testing.T) {
	plainStatus(t)
	out := &bytes.Buffer{}
	s := fixedStatus(out, false, time.Second)
	s.Start()
	s.tick()
	s.Stop()
	if out.Len() != 0 {
		t.Fatalf("a status off a terminal wrote %q", out.String())
	}
}

func TestStatus_StartDrawsOneRowStopErasesIt(t *testing.T) {
	plainStatus(t)
	out := &bytes.Buffer{}
	s := fixedStatus(out, true, 0)
	s.Start()
	const first = "\r\x1b[2K▸ generating image  ⠋ ·"
	if !strings.HasPrefix(out.String(), first) {
		t.Fatalf("Start drew %q, want it to begin with %q", out.String(), first)
	}
	s.Stop()
	got := out.String()
	if !strings.HasSuffix(got, "·\r\x1b[2K") {
		t.Fatalf("Stop left %q, want the row erased last", got)
	}
	if strings.Contains(got, "\n") {
		t.Fatalf("the status must stay on its row, got %q", got)
	}
}

func TestStatus_TickAdvancesSpinnerAndElapsed(t *testing.T) {
	plainStatus(t)
	out := &bytes.Buffer{}
	s := fixedStatus(out, true, 12*time.Second)
	s.Start()
	defer s.Stop()
	s.tick()
	if !strings.Contains(out.String(), "\r\x1b[2K▸ generating image  ⠙ 12s") {
		t.Fatalf("tick drew %q, want the next spinner frame and 12s", out.String())
	}
}

func TestStatus_StartAndStopAreIdempotent(t *testing.T) {
	plainStatus(t)
	out := &bytes.Buffer{}
	s := fixedStatus(out, true, 30*time.Second)
	s.Start()
	s.Start()
	s.tick()
	if !strings.Contains(out.String(), " 30s") {
		t.Fatalf("a second Start reset the elapsed time: %q", out.String())
	}
	s.Stop()
	stopped := out.String()
	s.Stop()
	s.tick()
	if out.String() != stopped {
		t.Fatalf("a stopped status wrote %q", strings.TrimPrefix(out.String(), stopped))
	}
}

func TestStatus_TruncatesToWidthAndColoursTheTitle(t *testing.T) {
	prev := ancli.UseColor
	ancli.UseColor = true
	t.Cleanup(func() { ancli.UseColor = prev })
	out := &bytes.Buffer{}
	s := fixedStatus(out, true, 0)
	s.Start()
	s.Stop()
	if !strings.Contains(out.String(), "\x1b[") || !strings.Contains(out.String(), "generating image") {
		t.Fatalf("the title must be coloured: %q", out.String())
	}

	ancli.UseColor = false
	out.Reset()
	s = fixedStatus(out, true, 0)
	s.SetWidth(10)
	s.Start()
	s.Stop()
	if !strings.HasPrefix(out.String(), "\r\x1b[2K▸ generat…") {
		t.Fatalf("a 10-column row drew %q, want it cut with …", out.String())
	}
}

func TestStatus_NilIsSafe(t *testing.T) {
	var s *Status
	s.Start()
	s.tick()
	s.Stop()
}

func TestStatus_DetailFollowsTheElapsedTime(t *testing.T) {
	plainStatus(t)
	out := &bytes.Buffer{}
	s := fixedStatus(out, true, 3*time.Second)
	s.Start()
	defer s.Stop()
	s.SetDetail("12/40 files")
	s.tick()
	if !strings.Contains(out.String(), "3s · 12/40 files") {
		t.Fatalf("tick drew %q, want the detail after the elapsed time", out.String())
	}
	s.SetDetail("")
	s.tick()
	last := out.String()[strings.LastIndex(out.String(), "\r"):]
	if strings.Contains(last, "·") {
		t.Fatalf("an empty detail still drew %q", last)
	}
}
