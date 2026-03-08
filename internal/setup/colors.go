package setup

import (
	"github.com/baalimago/clai/internal/utils"
)

func colorPrimary(s string) string   { return utils.Colorize(utils.ThemePrimaryColor(), s) }
func colorSecondary(s string) string { return utils.Colorize(utils.ThemeSecondaryColor(), s) }
func colorBreadtext(s string) string { return utils.Colorize(utils.ThemeBreadtextColor(), s) }
