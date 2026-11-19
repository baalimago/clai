// Package menu is what the menus of clai share: a Screen that shows one
// level at a time and asks one question at a time, and a Table fitted to the
// terminal. What a level holds is the caller's.
package menu

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/table"
)

// ErrLeft says the menu was left at a question: by q or quit, by the end of
// the input, or by a cancelled ctx, which is what the interrupt key does.
var ErrLeft = errors.New("menu: left")

// OutputError says a write to the screen failed.
type OutputError struct{ Err error }

func (e *OutputError) Error() string { return "menu: write to the session: " + e.Err.Error() }
func (e *OutputError) Unwrap() error { return e.Err }

// InputError says a read of an answer failed; the input ending is not a
// failure.
type InputError struct{ Err error }

func (e *InputError) Error() string { return "menu: read an answer: " + e.Err.Error() }
func (e *InputError) Unwrap() error { return e.Err }

// Asking is a run's record of whether one of its menus waits for an answer,
// so the run can tell an interrupt at a menu question, which leaves the
// menu, from any other. The zero value is ready.
type Asking struct{ waiting atomic.Int32 }

func (a *Asking) Waiting() bool { return a != nil && a.waiting.Load() > 0 }

type askingKey struct{}

// WithAsking returns a ctx whose menus report to a.
func WithAsking(ctx context.Context, a *Asking) context.Context {
	return context.WithValue(ctx, askingKey{}, a)
}

// Screen is a terminal a menu draws on. It remembers what is on the screen
// since its last draw (what it wrote and what was answered), so the next
// draw replaces exactly those rows and nothing that was there before.
type Screen struct {
	// In is where answers are read, one line each and never further.
	In  io.Reader
	Out io.Writer
	// Prompt, when set, shows each question in place of writing it to Out.
	Prompt func(question string) error

	shown strings.Builder
	drawn bool
}

// Draw replaces what the screen shows with content, at a terminal cols
// wide. The first draw starts on a row of its own.
func (s *Screen) Draw(content string, cols int) error {
	erase := "\n"
	if s.drawn {
		erase = "\r\x1b[J"
		if rows := utils.TerminalRows(s.shown.String(), cols) - 1; rows > 0 {
			erase = fmt.Sprintf("\x1b[%dA", rows) + erase
		}
	}
	s.drawn = true
	s.shown.Reset()
	if _, err := io.WriteString(s.Out, erase); err != nil {
		return &OutputError{Err: err}
	}
	_, err := io.WriteString(s, content)
	return err
}

// Write shows p below what is drawn; the next Draw erases it too.
func (s *Screen) Write(p []byte) (int, error) {
	n, err := s.Out.Write(p)
	s.shown.Write(p[:n])
	if err != nil {
		return n, &OutputError{Err: err}
	}
	return n, nil
}

// Echoed counts a line the terminal echoed as it was typed.
func (s *Screen) Echoed(line string) { s.shown.WriteString(line + "\n") }

// Ask shows question and returns the answer, one line of In. It returns
// ErrLeft when the menu is left, and then nothing more is to be drawn.
func (s *Screen) Ask(ctx context.Context, question string) (string, error) {
	if ctx.Err() != nil {
		return "", ErrLeft
	}
	if s.Prompt != nil {
		if err := s.Prompt(question); err != nil {
			return "", &OutputError{Err: err}
		}
	} else if _, err := io.WriteString(s, question); err != nil {
		return "", err
	}
	answer, err := s.read(ctx)
	switch {
	case errors.Is(err, ErrLeft):
		return "", ErrLeft
	case errors.Is(err, table.ErrUserInitiatedExit):
		s.Echoed("q")
		return "", ErrLeft
	case errors.Is(err, io.EOF):
		if shown := s.shown.String(); shown != "" && !strings.HasSuffix(shown, "\n") {
			if _, err := io.WriteString(s, "\n"); err != nil {
				return "", err
			}
		}
		return "", ErrLeft
	case err != nil:
		return "", &InputError{Err: err}
	}
	s.Echoed(answer)
	if strings.EqualFold(answer, "q") || strings.EqualFold(answer, "quit") {
		return "", ErrLeft
	}
	return answer, nil
}

// read waits for one line of In or for ctx to be cancelled. A cancel leaves
// the read behind: the line it then takes is dropped.
func (s *Screen) read(ctx context.Context) (string, error) {
	type reply struct {
		answer string
		err    error
	}
	asking, _ := ctx.Value(askingKey{}).(*Asking)
	if asking != nil {
		asking.waiting.Add(1)
		defer asking.waiting.Add(-1)
	}
	got := make(chan reply, 1)
	go func() {
		answer, err := table.ReadUserInputFrom(s.In)
		got <- reply{answer, err}
	}()
	select {
	case <-ctx.Done():
		return "", ErrLeft
	case r := <-got:
		if ctx.Err() != nil {
			return "", ErrLeft
		}
		return r.answer, r.err
	}
}

// Column is one column of a Table.
type Column struct {
	Title string
	// Drop orders the fitting: the column with the highest Drop goes first,
	// the rightmost of equals before the others. Drop 0 is never dropped.
	Drop int
}

// Table writes rows, numbered from first, under their header. While a line
// is wider than cols a column is dropped; no cell is ever cut or wrapped.
func Table(columns []Column, rows [][]string, first, cols int) string {
	keep := make([]int, len(columns))
	for i := range keep {
		keep[i] = i
	}
	for {
		lines := tableLines(columns, keep, rows, first)
		drop := -1
		for _, i := range keep {
			if columns[i].Drop > 0 && (drop < 0 || columns[i].Drop >= columns[drop].Drop) {
				drop = i
			}
		}
		fits := !slices.ContainsFunc(lines, func(line string) bool { return utils.TerminalRows(line, cols) > 1 })
		if drop < 0 || fits {
			return strings.Join(lines, "\n") + "\n"
		}
		keep = slices.DeleteFunc(keep, func(i int) bool { return i == drop })
	}
}

func tableLines(columns []Column, keep []int, rows [][]string, first int) []string {
	grid := make([][]string, 0, len(rows)+1)
	head := []string{"Index"}
	for _, i := range keep {
		head = append(head, columns[i].Title)
	}
	grid = append(grid, head)
	for n, row := range rows {
		line := []string{strconv.Itoa(first + n)}
		for _, i := range keep {
			line = append(line, row[i])
		}
		grid = append(grid, line)
	}
	widths := make([]int, len(head))
	for _, line := range grid {
		for c, cell := range line {
			widths[c] = max(widths[c], utils.TerminalStringWidth(cell))
		}
	}
	lines := make([]string, len(grid))
	for l, line := range grid {
		last := len(line) - 1
		for c, cell := range line[:last] {
			line[c] = Pad(cell, widths[c])
		}
		lines[l] = strings.Join(line, " | ")
	}
	return lines
}

// Pad fills s with spaces to cells terminal cells.
func Pad(s string, cells int) string {
	return s + strings.Repeat(" ", max(cells-utils.TerminalStringWidth(s), 0))
}
