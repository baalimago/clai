// Package vttest is a minimal terminal for tests that check what output leaves on
// screen: \n, \r, tab stops, CSI nA, CSI J, CSI 2J, CSI K and CSI 2K, SGR and OSC
// skipped, wide cells, NUL, BEL, DEL and zero-width marks dropped, and autowrap at a
// width. Any other escape sequence, and one left unfinished, is reported by Unhandled.
// There is no screen height: every row stays, and CSI A stops at the first row.
package vttest

import (
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/width"
)

// tabStop is the column interval of the terminal's tab stops.
const tabStop = 8

// snapshotCode is a private CSI that records the screen when written.
const snapshotCode = "\x1b[999X"

// Terminal replays written output. Width 0 never wraps.
type Terminal struct {
	Width     int
	lines     [][]rune
	wraps     []bool
	row, col  int
	partial   string
	pending   []rune
	snapshots [][]string
	unhandled []string
}

// SnapshotCode is written by a test to record the screen at that point.
func SnapshotCode() string { return snapshotCode }

func (t *Terminal) Write(p []byte) (int, error) {
	t.WriteString(string(p))
	return len(p), nil
}

func (t *Terminal) WriteString(s string) {
	s, t.partial = t.partial+s, ""
	if i := incompleteTail(s); i < len(s) {
		s, t.partial = s[:i], s[i:]
	}
	t.pending = append(t.pending, []rune(s)...)
	for len(t.pending) > 0 {
		n := t.step(t.pending)
		if n == 0 {
			return
		}
		t.pending = t.pending[n:]
	}
}

// incompleteTail is where a rune cut off at the end of s starts, or len(s).
func incompleteTail(s string) int {
	for i := len(s) - 1; i >= 0 && i >= len(s)-utf8.UTFMax; i-- {
		if utf8.RuneStart(s[i]) {
			if utf8.FullRuneInString(s[i:]) {
				return len(s)
			}
			return i
		}
	}
	return len(s)
}

// Unhandled are the escape sequences written that the terminal does not model, then
// any output still waiting for the end of a sequence.
func (t *Terminal) Unhandled() []string {
	if len(t.pending) == 0 && t.partial == "" {
		return t.unhandled
	}
	return append(slices.Clone(t.unhandled), string(t.pending)+t.partial)
}

// Row is the cursor's row, counted from the first row written.
func (t *Terminal) Row() int { return t.row }

// Snapshots are the screens recorded by SnapshotCode, in order.
func (t *Terminal) Snapshots() [][]string { return t.snapshots }

// Lines is Screen with each row that autowrap continued joined to the next.
func (t *Terminal) Lines() []string {
	var out []string
	joined := false
	for i, row := range t.Screen() {
		if t.wraps[i] {
			row = strings.ReplaceAll(string(t.lines[i]), "\x00", "")
		}
		if joined {
			out[len(out)-1] += row
		} else {
			out = append(out, row)
		}
		joined = t.wraps[i]
	}
	return out
}

// Screen is every row written, scrollback included, without trailing blanks.
func (t *Terminal) Screen() []string {
	out := make([]string, 0, len(t.lines))
	for _, l := range t.lines {
		out = append(out, strings.TrimRight(strings.ReplaceAll(string(l), "\x00", ""), " "))
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

// step consumes one character or escape sequence, and returns 0 when rs holds only
// part of a sequence.
func (t *Terminal) step(rs []rune) int {
	r := rs[0]
	switch {
	case r == 0x1b && len(rs) == 1:
		return 0
	case r == 0x1b && rs[1] == ']':
		for j := 2; j < len(rs); j++ {
			if rs[j] == 0x07 {
				return j + 1
			}
			if rs[j] == 0x1b && j+1 < len(rs) && rs[j+1] == '\\' {
				return j + 2
			}
		}
		return 0
	case r == 0x1b && rs[1] == '[':
		for j := 2; j < len(rs); j++ {
			if rs[j] >= 0x40 && rs[j] <= 0x7e {
				t.csi(rs[j], string(rs[2:j]))
				return j + 1
			}
		}
		return 0
	case r == 0x1b:
		t.unhandled = append(t.unhandled, string(rs[:2]))
		return 2
	case r == '\n':
		t.row++
		t.col = 0
	case r == '\r':
		t.col = 0
	case unmodelledControl(r):
		t.unhandled = append(t.unhandled, string(r))
	case r == '\t':
		t.col = (t.col/tabStop + 1) * tabStop
		if t.Width > 0 && t.col >= t.Width {
			t.col = t.Width - 1
		}
	default:
		t.put(r)
	}
	return 1
}

func (t *Terminal) put(r rune) {
	w := cells(r)
	if w == 0 {
		return
	}
	if t.Width > 0 && t.col+w > t.Width {
		t.line()
		t.wraps[t.row] = true
		t.row++
		t.col = 0
	}
	l := t.line()
	for len(l) < t.col+w {
		l = append(l, ' ')
	}
	l[t.col] = r
	if w == 2 {
		l[t.col+1] = 0
	}
	t.lines[t.row] = l
	t.col += w
}

func (t *Terminal) csi(final rune, params string) {
	n, err := strconv.Atoi(params)
	if err != nil || n == 0 {
		n = 1
	}
	col := t.col
	if t.Width > 0 && col >= t.Width {
		col = t.Width - 1
	}
	if (final == 'J' || final == 'K') && (col == 0 || params == "2") && t.row > 0 {
		t.line()
		t.wraps[t.row-1] = false
	}
	switch {
	case final == 'm':
	case final == 'A':
		t.row, t.col = max(0, t.row-n), min(t.col, col)
	case final == 'J' && params == "2":
		t.line()
		for i := range t.lines {
			t.lines[i], t.wraps[i] = nil, false
		}
	case final == 'J' && params == "":
		if l := t.line(); col < len(l) {
			t.lines[t.row] = l[:col]
		}
		t.lines, t.wraps = t.lines[:t.row+1], t.wraps[:t.row+1]
		t.wraps[t.row] = false
	case final == 'K' && params == "2":
		t.line()
		t.lines[t.row], t.wraps[t.row] = nil, false
	case final == 'K' && params == "":
		if l := t.line(); col < len(l) {
			t.lines[t.row] = l[:col]
		}
		t.wraps[t.row] = false
	case final == 'X' && params == "999":
		t.snapshots = append(t.snapshots, t.Screen())
	default:
		t.unhandled = append(t.unhandled, "\x1b["+params+string(final))
	}
}

// unmodelledControl is a control a terminal acts on that this one does not model: C0
// other than NUL, BEL, \t, \n, \r and ESC, and every C1.
func unmodelledControl(r rune) bool {
	switch r {
	case 0x00, 0x07, '\t', '\n', '\r', 0x1b:
		return false
	}
	return r < 0x20 || (r >= 0x80 && r <= 0x9f)
}

func (t *Terminal) line() []rune {
	for len(t.lines) <= t.row {
		t.lines, t.wraps = append(t.lines, nil), append(t.wraps, false)
	}
	return t.lines[t.row]
}

// cells is how many columns a terminal gives r, independent of the code under test.
func cells(r rune) int {
	if unicode.IsControl(r) || unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf) {
		return 0
	}
	switch width.LookupRune(r).Kind() {
	case width.EastAsianWide, width.EastAsianFullwidth:
		return 2
	}
	if r >= 0x1f300 && r <= 0x1faff {
		return 2
	}
	return 1
}
