package utils

import (
	"path/filepath"
	"testing"
)

func TestExpandUserPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAI_TEST_DIR", "/opt/clai")

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "bare tilde", in: "~", want: home},
		{name: "tilde slash", in: "~/.envfile", want: filepath.Join(home, ".envfile")},
		{name: "tilde nested", in: "~/conf/env", want: filepath.Join(home, "conf", "env")},
		{name: "dollar HOME", in: "$HOME/.envfile", want: filepath.Join(home, ".envfile")},
		{name: "braced HOME", in: "${HOME}/.envfile", want: filepath.Join(home, ".envfile")},
		{name: "custom env var", in: "$CLAI_TEST_DIR/env", want: "/opt/clai/env"},
		{name: "absolute untouched", in: "/etc/clai/env", want: "/etc/clai/env"},
		{name: "relative untouched", in: "conf/env", want: "conf/env"},
		{name: "tilde user untouched", in: "~otheruser/env", want: "~otheruser/env"},
		{name: "empty untouched", in: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ExpandUserPath(tt.in)
			if err != nil {
				t.Fatalf("ExpandUserPath(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("ExpandUserPath(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestExpandUserPath_EnvVarHoldingTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAI_TEST_TILDE", "~/env")

	got, err := ExpandUserPath("$CLAI_TEST_TILDE")
	if err != nil {
		t.Fatalf("ExpandUserPath: %v", err)
	}
	want := filepath.Join(home, "env")
	if got != want {
		t.Fatalf("ExpandUserPath = %q, want %q", got, want)
	}
}
