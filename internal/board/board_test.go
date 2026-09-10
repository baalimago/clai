package board

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

func plainStatus(t *testing.T) {
	t.Helper()
	prev := ancli.UseColor
	ancli.UseColor = false
	t.Cleanup(func() { ancli.UseColor = prev })
}

// fixedBoard mirrors the audio diarization layout the package was
// extracted from: index, span, marked state, four counters, time.
func fixedBoard(out *bytes.Buffer, live bool) *Board {
	cfg := Config{
		Title: "calibrated diarization  meeting.wav  2 cores × 21m06s  3 workers",
		Index: "core",
		Columns: []Column{
			{Name: "span", Width: 13},
			{Name: "state", Width: 30, Mark: true},
			{Name: "req", Width: 5},
			{Name: "verified", Width: 8},
			{Name: "unresolved", Width: 10},
			{Name: "unknown", Width: 8},
		},
		Time: "time",
	}
	rows := []Row{
		{Cells: []string{"00:00–21:06", "queued", "·", "·", "·", "·"}, Mark: MarkPending},
		{Cells: []string{"21:06–42:12", "queued", "·", "·", "·", "·"}, Mark: MarkPending},
	}
	b := New(out, live, cfg, rows)
	b.SetWidth(120)
	t0 := time.Date(2026, 9, 2, 14, 0, 0, 0, time.UTC)
	tickN := 0
	b.SetClock(func() time.Time { tickN++; return t0.Add(time.Duration(tickN) * time.Minute) })
	return b
}

func TestBoardPlainModePrintsRowsOnChange(t *testing.T) {
	plainStatus(t)
	out := &bytes.Buffer{}
	b := fixedBoard(out, false)
	b.Update(0, func(r *Row) {
		r.Cells[1] = "uncalibrated pass"
		r.Active = true
		r.Cells[2] = "1/4"
		r.Started = time.Now()
	})
	b.Update(0, func(r *Row) {
		r.Cells[1], r.Active, r.Mark = "calibrated", false, MarkDone
		r.Cells[2], r.Cells[3], r.Cells[4], r.Cells[5] = "2/4", "3/3", "0", "0s"
		r.Elapsed = 12*time.Minute + 32*time.Second
	})
	b.Update(1, func(r *Row) {
		r.Cells[1], r.Mark, r.Cells[4], r.Cells[5] = "2 unresolved", MarkWarn, "2", "1m12s"
		r.Elapsed = 8 * time.Minute
	})
	b.SetPhase("bootstrap · core 1 of 2")
	b.SetPhase("bootstrap · core 1 of 2") // unchanged phase prints nothing
	b.Log("  note: a line above the table")
	b.Finish("requests 4/≤8 · speakers 5", "  unknown-1: 21:10–21:40 (30s speech, no-sample)")
	got := out.String()
	for _, want := range []string{
		"▸ calibrated diarization", "1/2    00:00–21:06", "uncalibrated pass", "✓ calibrated", "2/4", "3/3", "✗ 2 unresolved", "1m12s", "12m32s",
		"  requests 4/≤8 · speakers 5", "unknown-1:", "phase  bootstrap · core 1 of 2", "note: a line above the table",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in plain output:\n%v", want, got)
		}
	}
	if strings.Contains(got, "\x1b[") {
		t.Errorf("plain mode must not emit cursor control:\n%q", got)
	}
	if n := strings.Count(got, "\n"); n != 8 {
		t.Errorf("expected header + 3 row lines + phase + log + footer + detail = 8 lines, got %v:\n%v", n, got)
	}
	if rows := b.Rows(); rows[0].Mark != MarkDone || rows[1].Cells[5] != "1m12s" {
		t.Errorf("snapshot must reflect the updates: %+v", rows)
	}
	if b.Footer() != "requests 4/≤8 · speakers 5" {
		t.Errorf("Footer() = %q", b.Footer())
	}
	b.Finish("ignored") // closed boards stay as they are
	if strings.Contains(out.String(), "ignored") {
		t.Error("a second Finish must print nothing")
	}
}

func TestBoardLiveModeRedrawsInPlace(t *testing.T) {
	plainStatus(t)
	out := &bytes.Buffer{}
	b := fixedBoard(out, true)
	if out.Len() != 0 {
		t.Fatalf("live mode must not print the header before the first change, got %q", out.String())
	}
	b.Update(0, func(r *Row) {
		r.Cells[1] = "uncalibrated pass"
		r.Active = true
		r.Cells[2] = "1/4"
		r.Started = time.Now()
	})
	first := out.String()
	if strings.Contains(first, "\x1b[4A") || !strings.Contains(first, "\r\x1b[2K") {
		t.Errorf("first frame must not move the cursor up but must clear lines:\n%q", first)
	}
	if strings.Count(first, "\n") != 4 {
		t.Errorf("expected header, columns, two rows in the first frame, got:\n%q", first)
	}
	if !strings.Contains(first, "  core   span           state                             req    verified  unresolved  unknown   time") {
		t.Errorf("live frame must carry the column header with the mark column two wider:\n%q", first)
	}
	out.Reset()
	b.Tick()
	ticks := 0
	b.SetFooterFunc(func(rows []Row) string {
		ticks++
		return fmt.Sprintf("tick %v · %v in flight", ticks, InFlight(rows))
	})
	b.SetPhase("calibrating")
	out.Reset()
	b.Update(1, func(r *Row) {
		r.Cells[1] = "calibrated pass"
		r.Active = true
		r.Cells[2] = "1/4"
		r.Started = time.Now()
	})
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
	b.Log("✓ logged above")
	logged := out.String()
	if !strings.HasPrefix(logged, "\x1b[6A\r\x1b[2K✓ logged above\n") {
		t.Errorf("Log must move over the frame, print the line, then redraw below it:\n%q", logged)
	}
	if strings.Count(logged, "\n") != 7 || strings.Contains(logged[len("\x1b[6A\r\x1b[2K✓ logged above\n"):], "\x1b[6A") {
		t.Errorf("the redraw after Log must not move the cursor up again:\n%q", logged)
	}
	out.Reset()
	b.Finish("requests 2/≤8")
	if !strings.HasPrefix(out.String(), "\x1b[6A") || !strings.Contains(out.String(), "  requests 2/≤8") || strings.Contains(out.String(), "tick") {
		t.Errorf("finish must redraw with the footer:\n%q", out.String())
	}
	out.Reset()
	b.Tick()
	if out.Len() != 0 {
		t.Errorf("Tick after Finish must draw nothing, got %q", out.String())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b.Animate(ctx) // returns immediately on a done context
}

func TestBoardAnimateStopsOnFinish(t *testing.T) {
	plainStatus(t)
	out := &bytes.Buffer{}
	b := fixedBoard(out, true)
	done := make(chan struct{})
	go func() { b.Animate(context.Background()); close(done) }()
	time.Sleep(2 * TickInterval)
	b.Finish("done")
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Animate must return once the board is finished")
	}
	if !strings.Contains(out.String(), "  done") {
		t.Errorf("final frame missing the footer:\n%q", out.String())
	}
}

func TestBoardColorsWarningRowsOnly(t *testing.T) {
	prev := ancli.UseColor
	ancli.UseColor = true
	t.Cleanup(func() { ancli.UseColor = prev })
	out := &bytes.Buffer{}
	b := fixedBoard(out, false)
	b.Update(0, func(r *Row) { r.Mark = MarkDone; r.Cells[1] = "calibrated" })
	b.Update(1, func(r *Row) { r.Mark = MarkWarn; r.Cells[1] = "1 unresolved" })
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

func TestBoardRowIndexOverride(t *testing.T) {
	plainStatus(t)
	out := &bytes.Buffer{}
	b := fixedBoard(out, false)
	b.Update(1, func(r *Row) { r.Index = "7/12"; r.Cells[1] = "working" })
	if got := out.String(); !strings.Contains(got, "  7/12   21:06–42:12") || strings.Contains(got, "2/2") {
		t.Errorf("Index must replace the position in the first column:\n%q", got)
	}
	b.Update(1, func(r *Row) { r.Index = "" })
	if got := out.String(); !strings.Contains(got, "  2/2    21:06–42:12") {
		t.Errorf("an empty Index must fall back to the position:\n%q", got)
	}
}

func TestBoardNilAndBoundsAreSafe(t *testing.T) {
	var b *Board
	b.Update(0, func(*Row) {})
	b.SetPhase("x")
	b.SetFooterFunc(nil)
	b.Log("x")
	b.Tick()
	b.Finish("x")
	b.Animate(context.Background())
	out := &bytes.Buffer{}
	real := fixedBoard(out, false)
	out.Reset()
	real.Update(7, func(*Row) { t.Fatal("out-of-range update must not run") })
	if out.Len() != 0 {
		t.Errorf("out-of-range update printed %q", out.String())
	}
}

func TestHelpers(t *testing.T) {
	if got := Truncate("abcdef", 4); got != "abc…" {
		t.Errorf("Truncate = %q", got)
	}
	if got := Truncate("abc", 0); got != "abc" {
		t.Errorf("Truncate with zero width = %q", got)
	}
	if got := ElapsedText(0); got != MarkPending {
		t.Errorf("ElapsedText(0) = %q", got)
	}
	if got := ElapsedText(90 * time.Second); got != "1m30s" {
		t.Errorf("ElapsedText = %q", got)
	}
	if got := InFlight([]Row{{Active: true}, {}, {Active: true}}); got != 2 {
		t.Errorf("InFlight = %d", got)
	}
}

func colourStatus(t *testing.T) {
	t.Helper()
	prev := ancli.UseColor
	ancli.UseColor = true
	t.Setenv("NO_COLOR", "")
	t.Cleanup(func() { ancli.UseColor = prev })
}

// eightyBoard lays out a summarize-like table whose columns line, its
// header and a warning row are exactly eighty visible columns wide.
func eightyBoard(out *bytes.Buffer, title string) *Board {
	cfg := Config{
		Title: title,
		Index: "job",
		Columns: []Column{
			{Name: "chat", Width: 20},
			{Name: "label", Width: 30, Mark: true},
			{Name: "tokens", Width: 9},
		},
		Time: "time",
	}
	rows := []Row{{Cells: []string{"chat-a", "queued", "·"}}, {Cells: []string{"chat-b", "queued", "·"}}}
	b := New(out, true, cfg, rows)
	b.SetWidth(80)
	return b
}

func TestBoardLiveColourKeepsLastColumnAndReset(t *testing.T) {
	colourStatus(t)
	out := &bytes.Buffer{}
	b := eightyBoard(out, strings.Repeat("t", 78))
	b.Update(1, func(r *Row) {
		r.Mark, r.Cells[1], r.Cells[2] = MarkWarn, "label failed", "1234"
		r.Elapsed = time.Minute
	})
	got := out.String()
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected header, columns and two rows, got %d lines:\n%q", len(lines), got)
	}
	for i, line := range lines {
		line = strings.TrimPrefix(line, "\r\x1b[2K")
		if displayWidth(line) > 80 {
			t.Errorf("line %d is %d columns wide:\n%q", i, displayWidth(line), line)
		}
		if strings.Contains(line, "\x1b[3") && !strings.HasSuffix(line, "\x1b[0m") {
			t.Errorf("coloured line %d must end with a reset:\n%q", i, line)
		}
	}
	if !strings.HasSuffix(lines[0], strings.Repeat("t", 78)+"\x1b[0m") {
		t.Errorf("header at exactly the width must keep its title:\n%q", lines[0])
	}
	if !strings.HasSuffix(lines[1], "  time\x1b[0m") {
		t.Errorf("columns line at exactly the width must keep its last column:\n%q", lines[1])
	}
	if !strings.Contains(lines[3], "✗ label failed") || !strings.HasSuffix(lines[3], "  1m0s\x1b[0m") {
		t.Errorf("warning row at exactly the width must keep its time cell:\n%q", lines[3])
	}
}

func TestBoardLiveWideTitleFitsWidth(t *testing.T) {
	colourStatus(t)
	out := &bytes.Buffer{}
	b := eightyBoard(out, strings.Repeat("日", 60))
	b.Update(0, func(r *Row) { r.Cells[1] = "working" })
	header := strings.TrimPrefix(strings.SplitN(out.String(), "\n", 2)[0], "\r\x1b[2K")
	if w := displayWidth(header); w > 80 {
		t.Errorf("header is %d columns wide, must fit 80:\n%q", w, header)
	}
	if !strings.Contains(header, "…") || !strings.HasSuffix(header, "\x1b[0m") || strings.Count(header, "日") >= 60 {
		t.Errorf("wide title must be cut with an ellipsis and closed with a reset:\n%q", header)
	}
}

func TestTruncate(t *testing.T) {
	for _, tc := range []struct {
		name, in string
		width    int
		want     string
	}{
		{"plain", "abcdef", 4, "abc…"},
		{"plain fits", "abcd", 4, "abcd"},
		{"zero width", "abc", 0, "abc"},
		{"escape fits", "\x1b[31mabcd\x1b[0m", 4, "\x1b[31mabcd\x1b[0m"},
		{"escape cut", "\x1b[31mabcdef\x1b[0m", 4, "\x1b[31mabc…\x1b[0m"},
		{"escape after cut", "abcdef\x1b[31mxyz\x1b[0m", 4, "abc…"},
		{"escape mid", "ab\x1b[1;32mcdef\x1b[0m", 4, "ab\x1b[1;32mc…\x1b[0m"},
		{"wide fits", "日本語", 6, "日本語"},
		{"wide cut", "日本語テキスト", 7, "日本語…"},
		{"wide odd", "日本語", 5, "日本…"},
		{"combining", "éabc", 3, "éa…"},
		{"emoji", "😀😀😀", 5, "😀😀…"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Truncate(tc.in, tc.width); got != tc.want {
				t.Errorf("Truncate(%q, %d) = %q, want %q", tc.in, tc.width, got, tc.want)
			}
		})
	}
}

func TestBoardMutatorsAfterFinishAreNoOps(t *testing.T) {
	plainStatus(t)
	for _, live := range []bool{true, false} {
		out := &bytes.Buffer{}
		b := fixedBoard(out, live)
		b.Update(0, func(r *Row) { r.Cells[1] = "working" })
		b.Finish("", "extra 1", "extra 2")
		before := out.String()
		if !strings.HasSuffix(before, "extra 1\nextra 2\n") {
			t.Fatalf("live=%v: extras must follow the final frame:\n%q", live, before)
		}
		b.Update(0, func(r *Row) { r.Cells[1] = "late" })
		b.SetPhase("late phase")
		b.SetFooterFunc(func([]Row) string { return "late footer" })
		b.Log("late log")
		if got := out.String(); got != before {
			t.Errorf("live=%v: mutators after Finish must write nothing, got:\n%q", live, got[len(before):])
		}
		if rows := b.Rows(); rows[0].Cells[1] != "working" {
			t.Errorf("live=%v: Update after Finish must not change the rows: %+v", live, rows[0])
		}
		if b.Footer() != "" {
			t.Errorf("live=%v: Footer() after Finish(\"\") = %q", live, b.Footer())
		}
	}
}

func TestBoardFinishShrinksFrame(t *testing.T) {
	plainStatus(t)
	out := &bytes.Buffer{}
	b := fixedBoard(out, true)
	b.SetFooterFunc(func([]Row) string { return "live footer" })
	b.Update(0, func(r *Row) { r.Cells[1] = "working" })
	if !strings.Contains(out.String(), "  live footer") {
		t.Fatalf("expected the live footer in the frame:\n%q", out.String())
	}
	if b.Footer() != "live footer" {
		t.Errorf("Footer() must reflect the footer function, got %q", b.Footer())
	}
	out.Reset()
	b.Finish("")
	got := out.String()
	if !strings.HasPrefix(got, "\x1b[5A") || !strings.HasSuffix(got, "\r\x1b[2K\n\x1b[1A") {
		t.Errorf("final frame without a footer must clear the stale line and move back up:\n%q", got)
	}
	if strings.Contains(got, "live footer") || b.Footer() != "" {
		t.Errorf("Finish(\"\") must drop the live footer, got:\n%q", got)
	}
}
