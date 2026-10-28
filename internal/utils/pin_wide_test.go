package utils

import (
	"testing"

	"github.com/baalimago/clai/internal/vttest"
)

// streamRows streams text token by token through PinWideRunes and returns
// the rows a terminal drawing wide runes wideCells wide ends up using.
func streamRows(tokens []string, width int, narrowEmoji bool) int {
	term := &vttest.Terminal{Width: width, NarrowEmoji: narrowEmoji}
	prior := ""
	for _, tok := range tokens {
		term.WriteString(PinWideRunes(prior, tok, width))
		prior += tok
	}
	return term.Row() + 1
}

func TestPinWideRunes_terminalsAgreeWithTerminalRows(t *testing.T) {
	// The prompt line is read at the width it wraps at and one narrow width;
	// the short texts put a wide rune on every side of a wrap boundary.
	cases := []struct {
		text   string
		widths []int
	}{
		{`{"prompt": "A flat illustration of an orange triangle on a dark navy background, no text.", "rebus": "🔶 🌌 📐", "size": "1024x1024"}`, []int{10, 72}},
		{"ab🔶cd", []int{4, 9, 10, 11}},
		{"abcdefgh🔶x", []int{4, 9, 10, 11}},
		{"abcdefgh🔶\nx", []int{4, 9, 10, 11}},
		{"abcdefghi🔶", []int{4, 9, 10, 11}},
		{"🔶🔶🔶🔶🔶🔶", []int{4, 9, 10, 11}},
		{"界界界 ab 🙂 c\n\n🙂", []int{4, 9, 10, 11}},
	}
	for _, c := range cases {
		for _, width := range c.widths {
			want := TerminalRows(c.text, width)
			for _, split := range []int{1, 3, len(c.text)} {
				tokens := splitRunes(c.text, split)
				for _, narrow := range []bool{false, true} {
					if got := streamRows(tokens, width, narrow); got != want {
						t.Errorf("text %q width %d tokens of %d runes, narrow emoji %v: %d rows, TerminalRows says %d", c.text, width, split, narrow, got, want)
					}
				}
			}
		}
	}
}

func splitRunes(s string, n int) []string {
	rs := []rune(s)
	var out []string
	for len(rs) > 0 {
		k := min(n, len(rs))
		out = append(out, string(rs[:k]))
		rs = rs[k:]
	}
	return out
}

func TestPinWideRunes_leavesNarrowTextAlone(t *testing.T) {
	if got := PinWideRunes("", "plain words", 10); got != "plain words" {
		t.Errorf("narrow text must pass through, got %q", got)
	}
	if got := PinWideRunes("abc", "\x1b[31mred 🙂\x1b[0m", 10); got != "\x1b[31mred 🙂\x1b[0m" {
		t.Errorf("a token holding an escape sequence must pass through, got %q", got)
	}
	if got := PinWideRunes("", "ab🔶cd", 10); got != "ab🔶\x1b[5Gcd" {
		t.Errorf("PinWideRunes pins the column after a wide rune, got %q", got)
	}
}
