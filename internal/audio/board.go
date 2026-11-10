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

// Diarization board columns, in Row.Cells order.
const (
	colSpan = iota
	colState
	colReq
	colVerified
	colUnresolved
	colUnknown
)

// newProgressBoard builds the chunk board (one row per core or plain chunk)
// on the shared progress table; live selects in-place redraw and is true
// when out is a terminal; color is the run's color choice.
func newProgressBoard(out io.Writer, live, color bool, title string, spans []SourceInterval) *board.Board {
	rows := make([]board.Row, 0, len(spans))
	for _, s := range spans {
		rows = append(rows, board.Row{
			Cells: []string{spanText(s), "queued", board.MarkPending, board.MarkPending, board.MarkPending, board.MarkPending},
			Mark:  board.MarkPending,
		})
	}
	return board.New(out, live, board.Config{
		Title: title,
		Index: "core",
		Columns: []board.Column{
			{Name: "span", Width: 13},
			{Name: "state", Width: 30, Mark: true},
			{Name: "req", Width: 5},
			{Name: "verified", Width: 8},
			{Name: "unresolved", Width: 10},
			{Name: "unknown", Width: 8},
		},
		Time:  "time",
		Color: color,
	}, rows)
}

// unknownSoFar sums the unknown speech of finished rows.
func unknownSoFar(rows []board.Row) time.Duration {
	total := time.Duration(0)
	for _, r := range rows {
		if r.Active || r.Cells[colUnknown] == board.MarkPending {
			continue
		}
		if d, err := time.ParseDuration(r.Cells[colUnknown]); err == nil {
			total += d
		}
	}
	return total
}

func spanText(s SourceInterval) string {
	return clock(s.Start) + "–" + clock(s.End)
}

func clock(d time.Duration) string {
	d = d.Round(time.Second)
	return fmt.Sprintf("%02d:%02d", int(d.Minutes()), int(d.Seconds())%60)
}

// stateRequestFailed is the state cell of a row whose request failed.
const stateRequestFailed = "request failed"

// transcribeProgressTitle names the progress records of a transcription
// under -json.
const transcribeProgressTitle = "audio_transcribe"

// chunkBoard is the chunk board of one transcription. With no StatusOut
// under -json it draws nothing and reports one progress record per state
// change on the run's stderr instead (F14).
type chunkBoard struct {
	*board.Board
	disp     display.Displayer
	mu       sync.Mutex
	last     map[int]display.Progress
	failed   bool
	finished bool
}

const boardKey = -1

func newChunkBoard(ctx context.Context, statusOut io.Writer, forceLive bool, title string, spans []SourceInterval) *chunkBoard {
	env := runenv.From(ctx)
	if statusOut == nil && env.JSON() {
		if disp, err := env.Displayer(stdio.Stderr); err == nil {
			b := &chunkBoard{Board: newProgressBoard(io.Discard, false, false, title, spans), disp: disp, last: map[int]display.Progress{}}
			b.report(boardKey, display.Progress{State: "start", Detail: title})
			return b
		}
	}
	out, live := statusOf(ctx, statusOut)
	return &chunkBoard{Board: newProgressBoard(out, forceLive || live, env.Color(), title, spans)}
}

func (b *chunkBoard) Update(i int, fn func(r *board.Row)) {
	if b == nil {
		return
	}
	if b.disp == nil {
		b.Board.Update(i, fn)
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.Board.Update(i, fn)
	rows := b.Rows()
	if b.finished || i < 0 || i >= len(rows) {
		return
	}
	r := rows[i]
	b.report(i, display.Progress{Row: r.Cells[colSpan], State: rowProgressState(r), Detail: r.Cells[colState]})
}

func (b *chunkBoard) SetPhase(phase string) {
	if b == nil {
		return
	}
	b.Board.SetPhase(phase)
	if b.disp == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.finished {
		return
	}
	if strings.HasPrefix(phase, "failed") {
		b.failed = true
	}
	b.report(boardKey, display.Progress{State: "update", Detail: phase})
}

// Fail marks the transcription failed, so Finish reports state failed.
func (b *chunkBoard) Fail() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failed = true
}

func (b *chunkBoard) Finish(footer string, extra ...string) {
	if b == nil {
		return
	}
	b.Board.Finish(footer, extra...)
	if b.disp == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.finished {
		return
	}
	for _, line := range extra {
		b.report(boardKey, display.Progress{State: "update", Detail: line})
	}
	state := "done"
	if b.failed {
		state = "failed"
	}
	b.report(boardKey, display.Progress{State: state, Detail: footer})
	b.finished = true
}

// report writes p unless it repeats the last record of its row. A failed
// write is ignored, as the text board ignores a failed draw (F6, F11).
func (b *chunkBoard) report(key int, p display.Progress) {
	p.Title = transcribeProgressTitle
	if prev, ok := b.last[key]; ok && prev == p {
		return
	}
	b.last[key] = p
	if p.State == "failed" {
		b.failed = true
	}
	_ = b.disp.ReportProgress(p)
}

func rowProgressState(r board.Row) string {
	switch {
	case r.Active:
		return "update"
	case r.Cells[colState] == stateRequestFailed:
		return "failed"
	case r.Mark == board.MarkDone, r.Mark == board.MarkWarn && !r.Started.IsZero():
		return "done"
	default:
		return "update"
	}
}
