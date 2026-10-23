package utils

import (
	"os"
	"path/filepath"
	"strings"
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

func TestAbsHomePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "leading tilde slash", in: "~/cat.png", want: filepath.Join(home, "cat.png")},
		{name: "cleaned", in: "~/a/../cat.png", want: filepath.Join(home, "cat.png")},
		{name: "only the leading tilde", in: "~/a~b/~/c.png", want: filepath.Join(home, "a~b", "~", "c.png")},
		{name: "inner tilde kept", in: "a/~/c.png", want: filepath.Join(cwd, "a", "~", "c.png")},
		{name: "bare tilde kept", in: "~", want: filepath.Join(cwd, "~")},
		{name: "tilde user kept", in: "~user/c.png", want: filepath.Join(cwd, "~user", "c.png")},
		{name: "relative", in: "c.png", want: filepath.Join(cwd, "c.png")},
		{name: "absolute", in: "/etc/c.png", want: "/etc/c.png"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := AbsHomePath(tt.in)
			if err != nil || got != tt.want {
				t.Fatalf("AbsHomePath(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
			}
		})
	}

	t.Setenv("HOME", "")
	if got, err := AbsHomePath("c.png"); err != nil || got != filepath.Join(cwd, "c.png") {
		t.Fatalf("without a home, AbsHomePath(%q) = %q, %v; want no home lookup", "c.png", got, err)
	}
	_, err = AbsHomePath("~/cat.png")
	if err == nil || !strings.HasPrefix(err.Error(), "failed to get home dir: ") {
		t.Fatalf("without a home, AbsHomePath = %v, want the ReplaceTildeWithHome error", err)
	}
}
