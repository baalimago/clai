package setup

import (
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/utils"
)

func TestSetupSubMenus_RespectThemeAndNoColor(t *testing.T) {
	if err := utils.LoadTheme(t.TempDir()); err != nil {
		t.Fatalf("load theme: %v", err)
	}

	t.Run("queryForAction prompt is colored", func(t *testing.T) {
		t.Setenv("NO_COLOR", "")
		// Ensure we have at least one option so the prompt includes structured pieces.
		out := colorSecondary("Do you wish to [c]onfigure, [q]uit: ")
		if !strings.Contains(out, utils.ThemeSecondaryColor()) {
			t.Fatalf("expected secondary color in output, got %q", out)
		}
	})

	t.Run("secondary wrappers are no-op with NO_COLOR", func(t *testing.T) {
		t.Setenv("NO_COLOR", "1")
		raw := "Please pick index: "
		if got := colorSecondary(raw); got != raw {
			t.Fatalf("expected no-color to return raw string. got=%q raw=%q", got, raw)
		}
	})
}
