// Package look draws clai's terminal look: a "▸ title · detail" head per
// subject, aligned rows under it, and "→ command   why" rows for what
// comes next. The marker is in the theme's primary colour and details in
// its secondary one, or plain text when color is off.
package look

import (
	"io"
	"strings"
	"unicode/utf8"

	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/table"
)

const gap = "   "

// Style paints in the theme's colours when Color is set.
type Style struct{ Color bool }

func (s Style) Paint(c, t string) string {
	if !s.Color || c == "" || t == "" {
		return t
	}
	return table.Colorize(c, t)
}

func (s Style) Primary(t string) string   { return s.Paint(utils.TableTheme().Primary, t) }
func (s Style) Secondary(t string) string { return s.Paint(utils.TableTheme().Secondary, t) }

// Head is "▸ <title> · <detail> · <detail>".
func (s Style) Head(title string, details ...string) string {
	line := s.Primary("▸") + " " + title
	if len(details) > 0 {
		line += s.Secondary(" · " + strings.Join(details, " · "))
	}
	return line
}

// Link is "→ <target>" under indent.
func (s Style) Link(indent, target string) string {
	return indent + s.Primary("→") + " " + target
}

// Grid lays rows out under indent: every cell but a row's last is padded
// to its column's width, the first column in plain text and the rest in
// the secondary colour. A first cell starting with "→ " gets the marker in
// the primary colour.
func Grid(indent string, rows [][]string, s Style) []string {
	var widths []int
	for _, r := range rows {
		for i, c := range r[:len(r)-1] {
			if i == len(widths) {
				widths = append(widths, 0)
			}
			widths[i] = max(widths[i], utf8.RuneCountInString(c))
		}
	}
	lines := make([]string, 0, len(rows))
	for _, r := range rows {
		var line strings.Builder
		line.WriteString(indent)
		for i, c := range r {
			text := c
			if i < len(r)-1 {
				text = Pad(c, widths[i]) + gap
			}
			switch {
			case i == 0 && strings.HasPrefix(c, "→ "):
				line.WriteString(s.Primary("→") + strings.TrimPrefix(text, "→"))
			case i == 0:
				line.WriteString(text)
			default:
				line.WriteString(s.Secondary(text))
			}
		}
		lines = append(lines, strings.TrimRight(line.String(), " "))
	}
	return lines
}

// Pad fills t with spaces to width runes.
func Pad(t string, width int) string {
	return t + strings.Repeat(" ", max(0, width-utf8.RuneCountInString(t)))
}

// For is the look for w: in colour when w is a terminal and NO_COLOR is
// not set.
func For(w io.Writer) Style {
	return Style{Color: utils.IsTerminalWriter(w) && !table.NoColor()}
}
