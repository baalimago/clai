package audio

import (
	"fmt"
	"io"
	"time"

	"github.com/baalimago/clai/internal/board"
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
// when out is a terminal.
func newProgressBoard(out io.Writer, live bool, title string, spans []SourceInterval) *board.Board {
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
		Time: "time",
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
