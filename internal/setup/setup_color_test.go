package setup

import (
	"os"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/utils"
)

func TestStage0Colorized_RespectsThemeAndNoColor(t *testing.T) {
	// Ensure deterministic theme values.
	if err := utils.LoadTheme(t.TempDir()); err != nil {
		t.Fatalf("load theme: %v", err)
	}

	// Force identifiable theme colors.
	// NOTE: We're in package setup and can't directly set globalTheme; use Colorize wrappers by
	// temporarily writing a theme.json into the temp config dir is overkill. Instead we validate for
	// default theme sequences.

	t.Setenv("NO_COLOR", "")
	out := stage0Colorized()
	if !strings.Contains(out, utils.ThemePrimaryColor()) {
		t.Fatalf("expected output to contain primary ANSI sequence")
	}
	if !strings.Contains(out, utils.ThemeSecondaryColor()) {
		t.Fatalf("expected output to contain secondary ANSI sequence")
	}
	if !strings.Contains(out, utils.ThemeBreadtextColor()) {
		t.Fatalf("expected output to contain breadtext ANSI sequence")
	}

	// With NO_COLOR, should be raw uncolored text.
	t.Setenv("NO_COLOR", "1")
	outNoColor := stage0Colorized()
	if outNoColor != stage0Raw {
		t.Fatalf("expected no-color output to equal raw template.\nraw:\n%q\n---\nout:\n%q", stage0Raw, outNoColor)
	}

	// Avoid unused import on some toolchains.
	_ = os.Stdout
}
