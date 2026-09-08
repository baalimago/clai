package audio

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/table"
)

var spinnerFrames = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

const (
	markDone    = "✓"
	markWarn    = "✗"
	markPending = "·"
	boardTick   = 200 * time.Millisecond
)

// boardRow is one core (or plain chunk) on the progress board.
type boardRow struct {
	span       SourceInterval
	state      string
	mark       string // "" while pending, spinner while active, ✓/✗ when done
	active     bool
	req        string
	verified   string
	unresolved string
	unknown    string
	started    time.Time
	elapsed    time.Duration
}

// progressBoard is the chunk board: one row per core, redrawn in place on a
// terminal (rolling-view style: ▸ header in the primary colour, two-space
// body, ✓/✗ markers) and printed as static rows when stderr is not one.
type progressBoard struct {
	mu     sync.Mutex
	out    io.Writer
	live   bool
	width  int
	title  string
	phase  string
	rows   []boardRow
	footer string
	// footerFn, when set, renders the footer live from a row snapshot; it
	// runs under the board lock and must not call back into the board
	footerFn func(rows []boardRow) string
	drawn    int
	frame    int
	now      func() time.Time
	begun    time.Time
	closed   bool
}

// newProgressBoard creates the board; live selects in-place redraw and is
// true when out is a terminal.
func newProgressBoard(out io.Writer, live bool, title string, spans []SourceInterval) *progressBoard {
	if out == nil {
		out = os.Stderr
	}
	b := &progressBoard{out: out, live: live, title: title, now: time.Now}
	if b.live {
		b.width = utils.SessionDimensions(out).Width
	}
	b.begun = b.now()
	for _, s := range spans {
		b.rows = append(b.rows, boardRow{span: s, state: "queued", mark: markPending, req: "·", verified: "·", unresolved: "·", unknown: "·"})
	}
	if !b.live {
		fmt.Fprintln(out, b.header())
	}
	return b
}

// update mutates one row and refreshes the display.
func (b *progressBoard) update(i int, fn func(r *boardRow)) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if i < 0 || i >= len(b.rows) {
		return
	}
	fn(&b.rows[i])
	if b.live {
		b.render()
		return
	}
	fmt.Fprintln(b.out, b.rowLine(i))
}

// setFooterFunc makes the footer live: fn runs at every render with a
// snapshot of the rows.
func (b *progressBoard) setFooterFunc(fn func(rows []boardRow) string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.footerFn = fn
	if b.live {
		b.render()
	}
}

// setPhase names the run phase shown under the header; plain mode prints
// each change once.
func (b *progressBoard) setPhase(phase string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if phase == b.phase {
		return
	}
	b.phase = phase
	if b.live {
		b.render()
		return
	}
	fmt.Fprintln(b.out, b.phaseLine())
}

// inFlight counts rows with a request in progress.
func inFlight(rows []boardRow) int {
	n := 0
	for _, r := range rows {
		if r.active {
			n++
		}
	}
	return n
}

// unknownSoFar sums the unknown speech of finished rows.
func unknownSoFar(rows []boardRow) time.Duration {
	total := time.Duration(0)
	for _, r := range rows {
		if r.active || r.unknown == "·" {
			continue
		}
		if d, err := time.ParseDuration(r.unknown); err == nil {
			total += d
		}
	}
	return total
}

func (b *progressBoard) phaseLine() string {
	if b.phase == "" {
		return ""
	}
	return "  " + b.color(utils.TableTheme().Secondary, "phase") + "  " + b.phase
}

// animate advances the spinner and elapsed times until ctx ends.
func (b *progressBoard) animate(ctx context.Context) {
	if b == nil || !b.live {
		return
	}
	t := time.NewTicker(boardTick)
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

// finish draws the final frame and leaves it on screen; extra lines (the
// unknown-speech detail) follow it in both modes.
func (b *progressBoard) finish(footer string, extra ...string) {
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

func (b *progressBoard) header() string {
	return b.color(utils.TableTheme().Primary, "▸ "+b.title)
}

func (b *progressBoard) color(c, s string) string {
	if !ancli.UseColor || c == "" {
		return s
	}
	return table.Colorize(c, s)
}

func (b *progressBoard) columns() string {
	return b.color(utils.TableTheme().Secondary, fmt.Sprintf("  %-5s  %-13s  %-32s  %-5s  %-8s  %-10s  %-8s  %s",
		"core", "span", "state", "req", "verified", "unresolved", "unknown", "time"))
}

func (b *progressBoard) rowLine(i int) string {
	r := b.rows[i]
	mark := r.mark
	if r.active {
		mark = string(spinnerFrames[b.frame%len(spinnerFrames)])
	}
	elapsed := r.elapsed
	if r.active && !r.started.IsZero() {
		elapsed = b.now().Sub(r.started)
	}
	line := fmt.Sprintf("  %-5s  %-13s  %s %-30s  %-5s  %-8s  %-10s  %-8s  %s",
		fmt.Sprintf("%d/%d", i+1, len(b.rows)), spanText(r.span), mark, truncateRow(r.state, 30), r.req, r.verified, r.unresolved, r.unknown, elapsedText(elapsed))
	if r.mark == markWarn && !r.active {
		return b.color(utils.RoleColor("tool"), line)
	}
	return line
}

func (b *progressBoard) footerLine() string {
	footer := b.footer
	if b.footerFn != nil {
		footer = b.footerFn(append([]boardRow(nil), b.rows...))
	}
	if footer == "" {
		return ""
	}
	return "  " + footer
}

// render redraws the whole board in place (caller holds the lock).
func (b *progressBoard) render() {
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
		sb.WriteString("\r\x1b[2K" + truncateRow(line, b.width) + "\n")
	}
	// Clear leftover rows from a taller previous frame
	for i := len(lines); i < b.drawn; i++ {
		sb.WriteString("\r\x1b[2K\n")
	}
	if extra := b.drawn - len(lines); extra > 0 {
		fmt.Fprintf(&sb, "\x1b[%dA", extra)
	}
	fmt.Fprint(b.out, sb.String())
	b.drawn = len(lines)
}

func spanText(s SourceInterval) string {
	return clock(s.Start) + "–" + clock(s.End)
}

func clock(d time.Duration) string {
	d = d.Round(time.Second)
	return fmt.Sprintf("%02d:%02d", int(d.Minutes()), int(d.Seconds())%60)
}

func elapsedText(d time.Duration) string {
	if d <= 0 {
		return "·"
	}
	return d.Round(time.Second).String()
}

// truncateRow cuts a line to the terminal width, ignoring escape sequences
// only approximately (they are short and sit at the ends of a line).
func truncateRow(line string, width int) string {
	if width <= 0 {
		return line
	}
	runes := []rune(line)
	if len(runes) <= width {
		return line
	}
	return string(runes[:width-1]) + "…"
}
