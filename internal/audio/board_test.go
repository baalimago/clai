package audio

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/board"
)

// TestDiarizationBoardLayout pins the adapter over the shared board: one
// row per span, the six diarization columns, and unknownSoFar summing only
// finished rows.
func TestDiarizationBoardLayout(t *testing.T) {
	plainStatus(t)
	out := &bytes.Buffer{}
	spans := []SourceInterval{{Start: 0, End: 1266 * time.Second}, {Start: 1266 * time.Second, End: 2532 * time.Second}}
	b := newProgressBoard(out, false, "calibrated diarization  meeting.wav  2 cores × 21m06s  3 workers", spans)
	b.Update(0, func(r *board.Row) {
		r.Mark, r.Cells[colState], r.Cells[colReq], r.Cells[colVerified], r.Cells[colUnresolved], r.Cells[colUnknown] = board.MarkDone, "calibrated", "2/4", "3/3", "0", "12s"
		r.Elapsed = 12*time.Minute + 32*time.Second
	})
	b.Update(1, func(r *board.Row) { r.Active, r.Cells[colState], r.Cells[colUnknown] = true, "calibrated pass", "1m0s" })
	if got := unknownSoFar(b.Rows()); got != 12*time.Second {
		t.Errorf("unknownSoFar must sum finished rows only, got %v", got)
	}
	got := out.String()
	for _, want := range []string{"▸ calibrated diarization", "1/2    00:00–21:06    ✓ calibrated", "2/4    3/3       0           12s       12m32s", "2/2    21:06–42:12"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in:\n%s", want, got)
		}
	}
	if rows := b.Rows(); rows[1].Cells[colSpan] != "21:06–42:12" || rows[1].Mark != board.MarkPending {
		t.Errorf("queued row must carry its span and the pending mark: %+v", rows[1])
	}
}
