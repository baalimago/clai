package vttest_test

import (
	"slices"
	"testing"

	"github.com/baalimago/clai/internal/vttest"
)

func TestTerminal(t *testing.T) {
	for _, tc := range []struct {
		name  string
		width int
		out   string
		want  []string
	}{
		{"newlines and carriage return", 0, "abc\nxyz\rX", []string{"abc", "Xyz"}},
		{"erase line and move up", 0, "one\ntwo\n\x1b[1A\r\x1b[2Knew", []string{"one", "new"}},
		{"clear to the end", 0, "one\ntwo\nthree\x1b[1A\r\x1b[J", []string{"one"}},
		{"sgr and osc take no cells", 0, "\x1b[31ma\x1b[0m\x1b]8;;file:///x\x1b\\b\x1b]8;;\x1b\\", []string{"ab"}},
		{"wraps at width", 4, "abcdef", []string{"abcd", "ef"}},
		{"wide cells wrap whole", 3, "a🐱b", []string{"a🐱", "b"}},
		{"tab stops", 0, "a\tb", []string{"a       b"}},
		{"tab stops at the last column", 10, "abcdefghi\tX", []string{"abcdefghiX"}},
		{"bell, nul and delete take no cell", 0, "a\x07b\x7fc\x00d", []string{"abcd"}},
		{"zero-width marks take no cell", 4, "e\u0301e\u0301e\u0301e\u0301", []string{"eeee"}},
		{"erase at the right margin", 4, "abcd\x1b[K", []string{"abc"}},
		{"clear to the end at the right margin", 4, "abcd\x1b[J", []string{"abc"}},
		{"cursor up ends a pending wrap", 4, "\nabcd\x1b[1AX", []string{"   X", "abcd"}},
		{"clear the screen", 0, "one\ntwo\x1b[2JX", []string{"", "   X"}},
	} {
		term := &vttest.Terminal{Width: tc.width}
		term.WriteString(tc.out)
		if got := term.Screen(); !slices.Equal(got, tc.want) {
			t.Errorf("%s: screen %q, want %q", tc.name, got, tc.want)
		}
	}
	wrapped := &vttest.Terminal{Width: 4}
	wrapped.WriteString("abcdef\nxy\n12345\x1b[1A\r\x1b[K\nabcdefg\r\x1b[J9")
	if got, want := wrapped.Lines(), []string{"abcdef", "xy", "", "abcd", "9"}; !slices.Equal(got, want) {
		t.Errorf("lines %q, want %q: autowrapped rows joined, an erased row breaks the join", got, want)
	}
	spaced := &vttest.Terminal{Width: 5}
	spaced.WriteString("abcd efgh")
	if got := spaced.Lines(); !slices.Equal(got, []string{"abcd efgh"}) {
		t.Errorf("lines %q: want the space at the wrap column kept", got)
	}
	split := &vttest.Terminal{}
	split.WriteString("a\xf0\x9f")
	split.WriteString("\x90\xb1b")
	if got := split.Screen(); !slices.Equal(got, []string{"a🐱b"}) {
		t.Errorf("a rune split across writes: screen %q, want it whole", got)
	}
	odd := &vttest.Terminal{}
	odd.WriteString("a\x1b[2Cb\x1b[31mc\x1b(Bd\be\vf\u009bg\x1b]8;;file:///x e")
	if got, want := odd.Unhandled(), []string{"\x1b[2C", "\x1b(", "\b", "\v", "\u009b", "\x1b]8;;file:///x e"}; !slices.Equal(got, want) {
		t.Errorf("unhandled = %q, want %q: unknown CSI, a non-CSI escape, cursor-moving and C1 controls, an unterminated OSC", got, want)
	}
	cut := &vttest.Terminal{}
	cut.WriteString("a\xf0\x9f")
	if got := cut.Unhandled(); !slices.Equal(got, []string{"\xf0\x9f"}) {
		t.Errorf("unhandled = %q, want the rune cut off at the end", got)
	}
	term := &vttest.Terminal{}
	term.WriteString("one\n\x1b[99")
	term.WriteString("9Xtwo")
	if snaps := term.Snapshots(); len(snaps) != 1 || !slices.Equal(snaps[0], []string{"one"}) || term.Row() != 1 {
		t.Fatalf("snapshots %q, row %d: want one snapshot of the first row, and a sequence split across writes", snaps, term.Row())
	}
}
