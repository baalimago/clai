package remote

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// PreviewLines and PreviewChars bound how much of a multi-line text, such
// as a profile's prompt, the trust prompt shows.
const (
	PreviewLines = 6
	PreviewChars = 400
)

// Printable makes untrusted text safe to print on a terminal: every C0 and
// C1 control character (escape, newline and tab among them), DEL and every
// bidirectional formatting character is shown as a visible escape such as
// \x1b or \u202e, and invalid UTF-8 as U+FFFD. Fetched bytes can then
// neither move the cursor, retitle the window nor reorder what is shown.
func Printable(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i, r := range s {
		switch {
		case r == utf8.RuneError && !strings.HasPrefix(s[i:], "�"):
			b.WriteRune(utf8.RuneError)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, r)
		case (r >= 0x80 && r <= 0x9f) || bidiControl(r):
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func bidiControl(r rune) bool {
	return r == 0x061c || r == 0x200e || r == 0x200f ||
		(r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069)
}

// Preview is the start of a multi-line text as printable lines: at most
// PreviewLines lines and PreviewChars characters, without the blank lines
// around it. When text is cut, the last line says how many characters
// were left out. A blank text has no lines.
func Preview(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return nil
	}
	total := utf8.RuneCountInString(strings.Join(lines, "\n"))
	shown := make([]string, 0, min(len(lines), PreviewLines)+1)
	budget := PreviewChars
	for i, line := range lines {
		if i == PreviewLines || budget <= 0 {
			break
		}
		if n := utf8.RuneCountInString(line); n > budget {
			line = string([]rune(line)[:budget])
		}
		budget -= utf8.RuneCountInString(line) + 1
		shown = append(shown, line)
	}
	cut := total - utf8.RuneCountInString(strings.Join(shown, "\n"))
	for i, line := range shown {
		shown[i] = Printable(line)
	}
	if cut > 0 {
		shown = append(shown, fmt.Sprintf("… %d more characters not shown", cut))
	}
	return shown
}

// commandLine renders argv as one printable line, quoting an argument that
// is empty or holds a space or a quote so the line reads as the argv it is.
func commandLine(argv []string) string {
	parts := make([]string, 0, len(argv))
	for _, a := range argv {
		if a == "" || strings.ContainsAny(a, " \"'") {
			a = fmt.Sprintf("%q", a)
		}
		parts = append(parts, Printable(a))
	}
	return strings.Join(parts, " ")
}
