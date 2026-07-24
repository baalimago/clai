package setup

import (
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/table"
)

func colorPrimary(s string) string   { return table.Colorize(utils.TableTheme().Primary, s) }
func colorSecondary(s string) string { return table.Colorize(utils.TableTheme().Secondary, s) }
func colorBreadtext(s string) string { return table.Colorize(utils.TableTheme().Breadtext, s) }
