package setup

import (
	"testing"

	"github.com/baalimago/clai/internal/utils"
)

func TestStage0Colorized_RespectsThemeAndNoColor(t *testing.T) {
	if err := utils.LoadTheme(t.TempDir()); err != nil {
		t.Fatalf("load theme: %v", err)
	}

	t.Setenv("NO_COLOR", "")
	out := stage0Colorized()
	if out != colorPrimary(stage0Raw) {
		t.Fatalf("expected stage0Colorized to use primary color wrapper, got %q", out)
	}

	t.Setenv("NO_COLOR", "1")
	outNoColor := stage0Colorized()
	if outNoColor != stage0Raw {
		t.Fatalf("expected no-color output to equal raw template.\nraw:\n%q\n---\nout:\n%q", stage0Raw, outNoColor)
	}
}
