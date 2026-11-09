package audio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/board"
	"github.com/baalimago/clai/internal/jsonl"
	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/internal/stdio"
)

func textEnv(stdout, stderr io.Writer) runenv.Env {
	return runenv.New(runenv.Config{
		Streams: stdio.New(stdio.Config{Format: jsonl.FormatText, Stdin: strings.NewReader(""), Stdout: stdout, Stderr: stderr}),
		Format:  jsonl.FormatText,
	})
}

func transcribeProgress(t *testing.T, stderr string) []progressRecord {
	t.Helper()
	if strings.ContainsAny(stderr, "\r\x1b") {
		t.Fatalf("stderr draws: %q", stderr)
	}
	var records []progressRecord
	for line := range strings.SplitSeq(strings.TrimSuffix(stderr, "\n"), "\n") {
		var r progressRecord
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("stderr line %q is not JSON: %v", line, err)
		}
		if r.Type != "progress" || r.Title != transcribeProgressTitle {
			t.Fatalf("stderr line %q is not an %s progress record", line, transcribeProgressTitle)
		}
		records = append(records, r)
	}
	return records
}

// TestChunkBoardJSONProgress is R2-02: under -json the chunk board draws
// nothing and reports one progress record per state change.
func TestChunkBoardJSONProgress(t *testing.T) {
	var stdout, stderr bytes.Buffer
	ctx := runenv.With(context.Background(), jsonRecordEnv(strings.NewReader(""), &stdout, &stderr))
	spans := []SourceInterval{{Start: 0, End: time.Minute}, {Start: time.Minute, End: 2 * time.Minute}}
	b := newChunkBoard(ctx, nil, false, "splitting via ffmpeg  meeting.wav", spans)
	b.SetPhase("transcribing · 2 chunks")
	b.SetPhase("transcribing · 2 chunks")
	b.Update(0, func(r *board.Row) { r.Active, r.Cells[colState] = true, "transcribing" })
	b.Update(0, func(r *board.Row) { r.Active, r.Cells[colState] = true, "transcribing" })
	b.Update(1, func(r *board.Row) { r.Active, r.Cells[colState] = true, "transcribing" })
	b.Update(0, func(r *board.Row) {
		r.Active, r.Mark, r.Cells[colState] = false, board.MarkDone, "transcribed · 2 segments"
	})
	b.Update(1, func(r *board.Row) { r.Active, r.Mark, r.Cells[colState] = false, board.MarkWarn, "request failed" })
	b.Finish("2 chunks · 1s elapsed")
	b.Finish("2 chunks · 1s elapsed")
	b.Update(0, func(r *board.Row) { r.Cells[colState] = "after finish" })

	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want nothing", stdout.String())
	}
	got := transcribeProgress(t, stderr.String())
	want := []progressRecord{
		{Type: "progress", Title: transcribeProgressTitle, State: "start", Detail: "splitting via ffmpeg  meeting.wav"},
		{Type: "progress", Title: transcribeProgressTitle, State: "update", Detail: "transcribing · 2 chunks"},
		{Type: "progress", Title: transcribeProgressTitle, Row: "00:00–01:00", State: "update", Detail: "transcribing"},
		{Type: "progress", Title: transcribeProgressTitle, Row: "01:00–02:00", State: "update", Detail: "transcribing"},
		{Type: "progress", Title: transcribeProgressTitle, Row: "00:00–01:00", State: "done", Detail: "transcribed · 2 segments"},
		{Type: "progress", Title: transcribeProgressTitle, Row: "01:00–02:00", State: "failed", Detail: "request failed"},
		{Type: "progress", Title: transcribeProgressTitle, State: "failed", Detail: "2 chunks · 1s elapsed"},
	}
	if len(got) != len(want) {
		t.Fatalf("records = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("record %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestChunkBoardTextDrawsOnStderr: in text mode with no StatusOut the board
// draws on the run's stderr.
func TestChunkBoardTextDrawsOnStderr(t *testing.T) {
	var stdout, stderr bytes.Buffer
	ctx := runenv.With(context.Background(), textEnv(&stdout, &stderr))
	b := newChunkBoard(ctx, nil, false, "splitting via ffmpeg  meeting.wav", []SourceInterval{{Start: 0, End: time.Minute}})
	b.Update(0, func(r *board.Row) { r.Mark, r.Cells[colState] = board.MarkDone, "transcribed · 1 segments" })
	b.Finish("1 chunks · 1s elapsed")
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "splitting via ffmpeg") || !strings.Contains(stderr.String(), "transcribed · 1 segments") {
		t.Fatalf("stdout %q stderr %q, want the board on stderr only", stdout.String(), stderr.String())
	}
}

// TestChunkBoardJSONWarnedRowAndFail: a row that finished with a warning is
// done, and Fail makes the board's last record failed.
func TestChunkBoardJSONWarnedRowAndFail(t *testing.T) {
	var stdout, stderr bytes.Buffer
	ctx := runenv.With(context.Background(), jsonRecordEnv(strings.NewReader(""), &stdout, &stderr))
	b := newChunkBoard(ctx, nil, false, "calibrated diarization", []SourceInterval{{Start: 0, End: time.Minute}})
	b.Update(0, func(r *board.Row) { r.Mark, r.Cells[colState] = board.MarkWarn, "timestamp drift 2.0 s" })
	b.Update(0, func(r *board.Row) { r.Active, r.Started, r.Cells[colState] = true, time.Now(), "transcribing" })
	b.Update(0, func(r *board.Row) { r.Active, r.Cells[colState] = false, "2 unresolved · 3 speakers" })
	b.Fail()
	b.Finish("1 chunks")
	got := transcribeProgress(t, stderr.String())
	states := make([]string, 0, len(got))
	for _, r := range got {
		states = append(states, r.Row+":"+r.State)
	}
	want := "[:start 00:00–01:00:update 00:00–01:00:update 00:00–01:00:done :failed]"
	if fmt.Sprint(states) != want {
		t.Fatalf("states = %v, want %v", states, want)
	}
}
