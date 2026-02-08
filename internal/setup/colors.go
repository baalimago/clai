package setup

import (
	"strings"

	"github.com/baalimago/clai/internal/utils"
)

func colorPrimary(s string) string   { return utils.Colorize(utils.ThemePrimaryColor(), s) }
func colorSecondary(s string) string { return utils.Colorize(utils.ThemeSecondaryColor(), s) }
func colorBreadtext(s string) string { return utils.Colorize(utils.ThemeBreadtextColor(), s) }

func colorizeLines(s string, colorLine func(line string) (string, bool)) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if nl, ok := colorLine(l); ok {
			lines[i] = nl
		}
	}
	return strings.Join(lines, "\n")
}
