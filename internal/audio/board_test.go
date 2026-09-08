package audio

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

func fixedBoard(out *bytes.Buffer, live bool) *progressBoard {
	spans := []SourceInterval{{Start: 0, End: 1266 * time.Second}, {Start: 1266 * time.Second, End: 2532 * time.Second}}
	b := newProgressBoard(out, live, "calibrated diarization  meeting.wav  2 cores × 21m06s  3 workers", spans)
	b.width = 120
	t0 := time.Date(2026, 9, 2, 14, 0, 0, 0, time.UTC)
	tick := 0
	b.now = func() time.Time { tick++; return t0.Add(time.Duration(tick) * time.Minute) }
	return b
}

func TestBoardPlainModePrintsRowsOnChange(t *testing.T) {
	plainStatus(t)
	out := &bytes.Buffer{}
	b := fixedBoard(out, false)
	b.update(0, func(r *boardRow) { r.state = "uncalibrated pass"; r.active = true; r.req = "1/4"; r.started = b.now() })
	b.update(0, func(r *boardRow) {
		r.state = "calibrated"
		r.active = false
		r.mark = markDone
		r.req = "2/4"
		r.verified = "3/3"
		r.unresolved = "0"
		r.unknown = "0s"
		r.elapsed = 12*time.Minute + 32*time.Second
	})
	b.update(1, func(r *boardRow) {
		r.state = "2 unresolved"
		r.mark = markWarn
		r.unresolved = "2"
		r.unknown = "1m12s"
		r.elapsed = 8 * time.Minute
	})
	b.setPhase("bootstrap · core 1 of 2")
	b.setPhase("bootstrap · core 1 of 2") // unchanged phase prints nothing
	if got := unknownSoFar(b.rows); got != 72*time.Second {
		t.Errorf("unknown so far should sum finished rows, got %v", got)
	}
	b.finish("requests 4/≤8 · speakers 5", "  unknown-1: 21:10–21:40 (30s speech, no-sample)")
	got := out.String()
	for _, want := range []string{"▸ calibrated diarization", "1/2    00:00–21:06", "uncalibrated pass", "✓ calibrated", "2/4", "3/3", "✗ 2 unresolved", "1m12s", "12m32s", "  requests 4/≤8 · speakers 5", "unknown-1:", "phase  bootstrap · core 1 of 2"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in plain output:\n%v", want, got)
		}
	}
	if strings.Contains(got, "\x1b[") {
		t.Errorf("plain mode must not emit cursor control:\n%q", got)
	}
	if n := strings.Count(got, "\n"); n != 7 {
		t.Errorf("expected header + 3 row lines + phase + footer + detail = 7 lines, got %v:\n%v", n, got)
	}
}

func TestBoardLiveModeRedrawsInPlace(t *testing.T) {
	plainStatus(t)
	out := &bytes.Buffer{}
	b := fixedBoard(out, true)
	b.update(0, func(r *boardRow) { r.state = "uncalibrated pass"; r.active = true; r.req = "1/4"; r.started = b.now() })
	first := out.String()
	if strings.Contains(first, "\x1b[4A") || !strings.Contains(first, "\r\x1b[2K") {
		t.Errorf("first frame must not move the cursor up but must clear lines:\n%q", first)
	}
	if strings.Count(first, "\n") != 4 {
		t.Errorf("expected header, columns, two rows in the first frame, got:\n%q", first)
	}
	out.Reset()
	b.frame++
	ticks := 0
	b.setFooterFunc(func(rows []boardRow) string {
		ticks++
		return fmt.Sprintf("tick %v · %v in flight", ticks, inFlight(rows))
	})
	b.setPhase("calibrating")
	out.Reset()
	b.update(1, func(r *boardRow) { r.state = "calibrated pass"; r.active = true; r.req = "1/4"; r.started = b.now() })
	second := out.String()
	if !strings.HasPrefix(second, "\x1b[6A") {
		t.Errorf("second frame must move up over the six drawn lines (header, phase, columns, two rows, footer):\n%q", second)
	}
	if !strings.Contains(second, string(spinnerFrames[1])) || !strings.Contains(second, "phase  calibrating") || !strings.Contains(second, "tick 3") {
		t.Errorf("expected spinner, phase line and a live footer in:\n%q", second)
	}
	if !strings.Contains(second, "2 in flight") {
		t.Errorf("expected the row snapshot to reach the footer, got:\n%q", second)
	}
	out.Reset()
	b.finish("requests 2/≤8")
	if !strings.HasPrefix(out.String(), "\x1b[6A") || !strings.Contains(out.String(), "  requests 2/≤8") || strings.Contains(out.String(), "tick") {
		t.Errorf("finish must redraw with the footer:\n%q", out.String())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b.animate(ctx) // returns immediately on a done context
}

func TestBoardColorsWarningRowsOnly(t *testing.T) {
	prev := ancli.UseColor
	ancli.UseColor = true
	t.Cleanup(func() { ancli.UseColor = prev })
	out := &bytes.Buffer{}
	b := fixedBoard(out, false)
	b.update(0, func(r *boardRow) { r.mark = markDone; r.state = "calibrated" })
	b.update(1, func(r *boardRow) { r.mark = markWarn; r.state = "1 unresolved" })
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if !strings.Contains(lines[0], "\x1b[") {
		t.Errorf("header should be coloured: %q", lines[0])
	}
	if strings.Contains(lines[1], "\x1b[") {
		t.Errorf("a clean row must not be coloured: %q", lines[1])
	}
	if !strings.Contains(lines[2], "\x1b[") {
		t.Errorf("a warning row must be coloured: %q", lines[2])
	}
}
