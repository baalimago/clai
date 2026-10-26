package text

import (
	"testing"

	"github.com/baalimago/clai/internal/vttest"
)

// finalScreen is the lines out leaves on a terminal width columns wide (0 never wraps),
// scrollback included, with rows that autowrap split joined back.
func finalScreen(t *testing.T, out string, width int) []string {
	t.Helper()
	return replayScreen(t, &vttest.Terminal{Width: width}, out)
}

func replayScreen(t *testing.T, term *vttest.Terminal, out string) []string {
	t.Helper()
	term.WriteString(out)
	if odd := term.Unhandled(); len(odd) > 0 {
		t.Fatalf("output uses sequences the test terminal does not model: %q", odd)
	}
	return term.Lines()
}
