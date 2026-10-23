package present

import (
	"errors"
	"testing"
)

// Proves NewCommandOpener.1 across platforms the host cannot be.
func TestCommandOpener_PlatformDefault(t *testing.T) {
	for _, tc := range []struct{ goos, command, want string }{
		{"linux", "", "xdg-open"},
		{"darwin", "", "open"},
		{"plan9", "", ""},
		{"windows", "", ""},
		{"plan9", "feh", "feh"},
		{"linux", "feh", "feh"},
	} {
		t.Run(tc.goos+"/"+tc.command, func(t *testing.T) {
			o, ok := newCommandOpener(tc.command, tc.goos).(commandOpener)
			if !ok {
				t.Fatalf("newCommandOpener returned %T", newCommandOpener(tc.command, tc.goos))
			}
			if o.command != tc.want {
				t.Fatalf("command = %q, want %q", o.command, tc.want)
			}
		})
	}
	t.Run("no default fails on Open", func(t *testing.T) {
		err := newCommandOpener("", "plan9").Open("/tmp/x.png")
		var unavailable *OpenerUnavailableError
		if !errors.As(err, &unavailable) || unavailable.Command != "" {
			t.Fatalf("Open = %v, want *OpenerUnavailableError{Command: \"\"}", err)
		}
	})
}
