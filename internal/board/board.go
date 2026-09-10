// Package board renders a parallel-work progress table: a ▸ title, an
// optional phase line, a column header, one row per unit of work with a
// ✓/✗/spinner mark and a live elapsed time, and a footer. On a terminal the
// whole table is redrawn in place; elsewhere every change prints one static
// line. Extracted from the audio transcribe flows and shared with
// chat summarize.
package board

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/table"
	"golang.org/x/text/width"
)

var spinnerFrames = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

// Row marks. MarkPending doubles as the placeholder of an empty elapsed or
// index cell.
const (
	MarkDone    = "✓"
	MarkWarn    = "✗"
	MarkPending = "·"
	// TickInterval is the spinner and elapsed-time refresh interval of Animate.
	TickInterval = 200 * time.Millisecond
	indexWidth   = 5
)

// Column is one domain column between the position column and the elapsed
// column. A Mark column renders the row's mark (or the spinner while the
// row is active) in front of its cell.
type Column struct {
	Name  string
	Width int
	Mark  bool
}

// Config names the table: the title after ▸, the position column header,
// the domain columns and the elapsed column header.
type Config struct {
	Title   string
	Index   string
	Columns []Column
	Time    string
}

// Row is one unit of work. Cells holds one entry per Config.Column; Mark is
// "" or MarkPending while queued, MarkDone/MarkWarn when finished; Active
// replaces the mark with the spinner and counts elapsed live from Started.
type Row struct {
	Cells []string
	// Index, when set, replaces the row's position (i/n) in the first
	// column: a worker slot shows the job it is on.
	Index   string
	Mark    string
	Active  bool
	Started time.Time
	Elapsed time.Duration
}

// Board is the table. Every method is safe for concurrent use.
type Board struct {
	mu     sync.Mutex
	out    io.Writer
	live   bool
	width  int
	cfg    Config
	phase  string
	rows   []Row
	footer string
	// footerFn renders the footer live from a row snapshot; it runs under
	// the board lock and must not call back into the board.
	footerFn func(rows []Row) string
	drawn    int
	frame    int
	now      func() time.Time
	closed   bool
}

// New creates the board over rows; live selects in-place redraw and is true
// when out is a terminal. Plain mode prints the header at once.
func New(out io.Writer, live bool, cfg Config, rows []Row) *Board {
	if out == nil {
		out = os.Stderr
	}
	b := &Board{out: out, live: live, cfg: cfg, now: time.Now}
	if b.live {
		b.width = utils.SessionDimensions(out).Width
	}
	for _, r := range rows {
		cells := make([]string, len(cfg.Columns))
		copy(cells, r.Cells)
		r.Cells = cells
		b.rows = append(b.rows, r)
	}
	if !b.live {
		fmt.Fprintln(out, b.header())
	}
	return b
}

// SetClock replaces the clock (tests).
func (b *Board) SetClock(now func() time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.now = now
}

// SetWidth fixes the live truncation width (tests, or callers that know
// better than the terminal probe).
func (b *Board) SetWidth(width int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.width = width
}

// Rows returns a snapshot of the rows.
func (b *Board) Rows() []Row {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.snapshot()
}

// Footer returns the footer as it renders now: the footer function's result
// while one is set, otherwise the last fixed footer text.
func (b *Board) Footer() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.footerFn != nil {
		return b.footerFn(b.snapshot())
	}
	return b.footer
}

// Update mutates one row and refreshes the display: a redraw when live, the
// row's line when plain. No-op after Finish.
func (b *Board) Update(i int, fn func(r *Row)) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || i < 0 || i >= len(b.rows) {
		return
	}
	fn(&b.rows[i])
	if b.live {
		b.render()
		return
	}
	fmt.Fprintln(b.out, b.rowLine(i))
}

// SetFooterFunc makes the footer live: fn runs at every render with a
// snapshot of the rows. No-op after Finish.
func (b *Board) SetFooterFunc(fn func(rows []Row) string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.footerFn = fn
	if b.live {
		b.render()
	}
}

// SetPhase names the run phase shown under the header; plain mode prints
// each change once. No-op after Finish.
func (b *Board) SetPhase(phase string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || phase == b.phase {
		return
	}
	b.phase = phase
	if b.live {
		b.render()
		return
	}
	fmt.Fprintln(b.out, b.phaseLine())
}

// Log prints a line that stays above the table: live mode inserts it over
// the current frame and redraws the table below it, plain mode prints it.
// No-op after Finish.
func (b *Board) Log(line string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	if !b.live {
		fmt.Fprintln(b.out, line)
		return
	}
	// Relies on the frame on screen being exactly drawn lines tall, which
	// holds because every live state change renders.
	var sb strings.Builder
	if b.drawn > 0 {
		fmt.Fprintf(&sb, "\x1b[%dA", b.drawn)
	}
	sb.WriteString("\r\x1b[2K" + Truncate(line, b.width) + "\n")
	fmt.Fprint(b.out, sb.String())
	b.drawn = 0
	b.render()
}

// Tick advances the spinner and redraws (live only).
func (b *Board) Tick() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || !b.live {
		return
	}
	b.frame++
	b.render()
}

// Animate ticks the spinner and elapsed times until ctx ends or Finish runs.
func (b *Board) Animate(ctx context.Context) {
	if b == nil || !b.live {
		return
	}
	t := time.NewTicker(TickInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			b.mu.Lock()
			if b.closed {
				b.mu.Unlock()
				return
			}
			b.frame++
			b.render()
			b.mu.Unlock()
		}
	}
}

// Finish draws the final frame and leaves it on screen; extra lines follow
// it in both modes. Later calls are no-ops.
func (b *Board) Finish(footer string, extra ...string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.footer = footer
	b.footerFn = nil
	b.closed = true
	if b.live {
		b.render()
	} else if footer != "" {
		fmt.Fprintln(b.out, "  "+footer)
	}
	for _, line := range extra {
		fmt.Fprintln(b.out, line)
	}
}

// InFlight counts active rows.
func InFlight(rows []Row) int {
	n := 0
	for _, r := range rows {
		if r.Active {
			n++
		}
	}
	return n
}

// ElapsedText renders a duration for the elapsed column; zero is "·".
func ElapsedText(d time.Duration) string {
	if d <= 0 {
		return MarkPending
	}
	return d.Round(time.Second).String()
}

// Truncate cuts a line to width display columns and ends it with "…". SGR
// escapes are kept without counting, and a cut coloured line is closed with
// a reset. A width of zero leaves the line alone.
func Truncate(line string, width int) string {
	if width <= 0 || displayWidth(line) <= width {
		return line
	}
	var sb strings.Builder
	rs := []rune(line)
	used, coloured := 0, false
	for i := 0; i < len(rs); i++ {
		if k := sgrLen(rs[i:]); k > 0 {
			sb.WriteString(string(rs[i : i+k]))
			i += k - 1
			coloured = true
			continue
		}
		w := runeWidth(rs[i])
		if used+w > width-1 {
			break
		}
		sb.WriteRune(rs[i])
		used += w
	}
	sb.WriteString("…")
	if coloured {
		sb.WriteString("\x1b[0m")
	}
	return sb.String()
}

func (b *Board) snapshot() []Row {
	out := make([]Row, len(b.rows))
	for i, r := range b.rows {
		r.Cells = append([]string(nil), r.Cells...)
		out[i] = r
	}
	return out
}

func (b *Board) header() string {
	return b.color(utils.TableTheme().Primary, "▸ "+b.cfg.Title)
}

func (b *Board) phaseLine() string {
	if b.phase == "" {
		return ""
	}
	return "  " + b.color(utils.TableTheme().Secondary, "phase") + "  " + b.phase
}

func (b *Board) color(c, s string) string {
	if !ancli.UseColor || c == "" {
		return s
	}
	return table.Colorize(c, s)
}

func (b *Board) columns() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "  %-*s", indexWidth, b.cfg.Index)
	for _, c := range b.cfg.Columns {
		width := c.Width
		if c.Mark {
			width += 2
		}
		fmt.Fprintf(&sb, "  %-*s", width, c.Name)
	}
	sb.WriteString("  " + b.cfg.Time)
	return b.color(utils.TableTheme().Secondary, sb.String())
}

func (b *Board) rowLine(i int) string {
	r := b.rows[i]
	mark := r.Mark
	if r.Active {
		mark = string(spinnerFrames[b.frame%len(spinnerFrames)])
	}
	if mark == "" {
		mark = MarkPending
	}
	elapsed := r.Elapsed
	if r.Active && !r.Started.IsZero() {
		elapsed = b.now().Sub(r.Started)
	}
	index := r.Index
	if index == "" {
		index = fmt.Sprintf("%d/%d", i+1, len(b.rows))
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "  %-*s", indexWidth, index)
	for k, c := range b.cfg.Columns {
		cell := ""
		if k < len(r.Cells) {
			cell = r.Cells[k]
		}
		if c.Mark {
			fmt.Fprintf(&sb, "  %s %-*s", mark, c.Width, Truncate(cell, c.Width))
			continue
		}
		fmt.Fprintf(&sb, "  %-*s", c.Width, Truncate(cell, c.Width))
	}
	sb.WriteString("  " + ElapsedText(elapsed))
	line := sb.String()
	if r.Mark == MarkWarn && !r.Active {
		return b.color(utils.RoleColor("tool"), line)
	}
	return line
}

func (b *Board) footerLine() string {
	footer := b.footer
	if b.footerFn != nil {
		footer = b.footerFn(b.snapshot())
	}
	if footer == "" {
		return ""
	}
	return "  " + footer
}

// render redraws the whole board in place (caller holds the lock).
func (b *Board) render() {
	lines := []string{b.header()}
	if p := b.phaseLine(); p != "" {
		lines = append(lines, p)
	}
	lines = append(lines, b.columns())
	for i := range b.rows {
		lines = append(lines, b.rowLine(i))
	}
	if f := b.footerLine(); f != "" {
		lines = append(lines, f)
	}
	var sb strings.Builder
	if b.drawn > 0 {
		fmt.Fprintf(&sb, "\x1b[%dA", b.drawn)
	}
	for _, line := range lines {
		sb.WriteString("\r\x1b[2K" + Truncate(line, b.width) + "\n")
	}
	for i := len(lines); i < b.drawn; i++ {
		sb.WriteString("\r\x1b[2K\n")
	}
	if extra := b.drawn - len(lines); extra > 0 {
		fmt.Fprintf(&sb, "\x1b[%dA", extra)
	}
	fmt.Fprint(b.out, sb.String())
	b.drawn = len(lines)
}

// sgrLen returns the length of the SGR escape at the start of rs, or zero.
func sgrLen(rs []rune) int {
	if len(rs) < 3 || rs[0] != '\x1b' || rs[1] != '[' {
		return 0
	}
	for i := 2; i < len(rs); i++ {
		switch {
		case rs[i] == 'm':
			return i + 1
		case rs[i] >= '0' && rs[i] <= '9', rs[i] == ';':
		default:
			return 0
		}
	}
	return 0
}

// displayWidth counts the terminal columns of s, skipping SGR escapes.
func displayWidth(s string) int {
	rs := []rune(s)
	n := 0
	for i := 0; i < len(rs); i++ {
		if k := sgrLen(rs[i:]); k > 0 {
			i += k - 1
			continue
		}
		n += runeWidth(rs[i])
	}
	return n
}

func runeWidth(r rune) int {
	if unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf) {
		return 0
	}
	switch width.LookupRune(r).Kind() {
	case width.EastAsianWide, width.EastAsianFullwidth:
		return 2
	}
	if r >= 0x1f000 && r <= 0x1faff {
		return 2
	}
	return 1
}
