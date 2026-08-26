package mcp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseEnvFileContent(t *testing.T) {
	content := `
# comment
FOO=bar
export BAZ=qux
EMPTY=
QUOTED="a b"
SINGLE='c d'
`
	env, err := parseEnvFileContent(content)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if env["FOO"] != "bar" {
		t.Fatalf("expected FOO=bar, got %q", env["FOO"])
	}
	if env["BAZ"] != "qux" {
		t.Fatalf("expected BAZ=qux, got %q", env["BAZ"])
	}
	if env["EMPTY"] != "" {
		t.Fatalf("expected EMPTY to be empty, got %q", env["EMPTY"])
	}
	if env["QUOTED"] != "a b" {
		t.Fatalf("expected QUOTED='a b', got %q", env["QUOTED"])
	}
	if env["SINGLE"] != "c d" {
		t.Fatalf("expected SINGLE='c d', got %q", env["SINGLE"])
	}
}

func TestLoadEnvFile_HomeResolution(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, ".envfile"), []byte("FOO=bar"), 0o644); err != nil {
		t.Fatalf("write envfile: %v", err)
	}

	for _, in := range []string{"~/.envfile", "$HOME/.envfile", "${HOME}/.envfile"} {
		t.Run(in, func(t *testing.T) {
			env, err := loadEnvFile(in)
			if err != nil {
				t.Fatalf("loadEnvFile(%q): %v", in, err)
			}
			if env["FOO"] != "bar" {
				t.Fatalf("expected FOO=bar, got %q", env["FOO"])
			}
		})
	}
}
