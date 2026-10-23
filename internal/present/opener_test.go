package present_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/present"
)

func writeShim(t *testing.T, dir, name, body string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), mode); err != nil {
		t.Fatalf("write shim %s: %v", path, err)
	}
	return path
}

// recordingShim writes a shim that records its argument count and arguments, one per line.
func recordingShim(t *testing.T, dir, name string) (shim, record string) {
	t.Helper()
	record = filepath.Join(t.TempDir(), "argv")
	return writeShim(t, dir, name, `printf '%s\n' "$#" "$@" > '`+record+`'`, 0o755), record
}

func wantArgs(t *testing.T, record string, want ...string) {
	t.Helper()
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("the shim did not run: %v", err)
	}
	got := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if got[0] != strconv.Itoa(len(want)) || !slices.Equal(got[1:], want) {
		t.Fatalf("shim args = %q, want %d args %q", got, len(want), want)
	}
}

func platformLauncher() string {
	return map[string]string{"linux": "xdg-open", "darwin": "open"}[runtime.GOOS]
}

// Proves NewCommandOpener.1, Open.1, AC5.
func TestCommandOpener_Default(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	img := filepath.Join(t.TempDir(), "x.png")
	launcher := platformLauncher()
	if launcher == "" {
		var unavailable *present.OpenerUnavailableError
		if err := present.NewCommandOpener("").Open(img); !errors.As(err, &unavailable) || unavailable.Command != "" {
			t.Fatalf("Open on %s = %v, want *OpenerUnavailableError{Command: \"\"}", runtime.GOOS, err)
		}
		return
	}
	_, record := recordingShim(t, bin, launcher)
	if err := present.NewCommandOpener("").Open(img); err != nil {
		t.Fatalf("Open: %v", err)
	}
	wantArgs(t, record, img)
}

// Proves NewCommandOpener.2, AC5.
func TestCommandOpener_Missing(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	notExecutable := writeShim(t, bin, "viewer", "exit 0", 0o644)
	cases := []string{"definitely-not-a-binary", filepath.Join(t.TempDir(), "absent-viewer"), "viewer", notExecutable}
	if launcher := platformLauncher(); launcher != "" {
		cases = append(cases, "")
	}
	for _, command := range cases {
		t.Run(command, func(t *testing.T) {
			err := present.NewCommandOpener(command).Open(filepath.Join(t.TempDir(), "x.png"))
			var unavailable *present.OpenerUnavailableError
			if !errors.As(err, &unavailable) {
				t.Fatalf("Open = %v (%T), want *OpenerUnavailableError", err, err)
			}
			want := command
			if command == "" {
				want = platformLauncher()
			}
			if unavailable.Command != want {
				t.Fatalf("OpenerUnavailableError.Command = %q, want %q", unavailable.Command, want)
			}
		})
	}
}

// Proves NewCommandOpener.3.
func TestCommandOpener_Exit(t *testing.T) {
	shim := writeShim(t, t.TempDir(), "viewer", "exit 3", 0o755)
	img := filepath.Join(t.TempDir(), "x.png")
	err := present.NewCommandOpener(shim).Open(img)
	var failed *present.OpenFailedError
	if !errors.As(err, &failed) {
		t.Fatalf("Open = %v (%T), want *OpenFailedError", err, err)
	}
	if failed.Path != img {
		t.Fatalf("OpenFailedError.Path = %q, want %q", failed.Path, img)
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 3 {
		t.Fatalf("Open = %v, want exit status 3", err)
	}
}

// Proves NewCommandOpener.4.
func TestCommandOpener_Argv(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	img := filepath.Join(t.TempDir(), `my image; echo 'hi' $HOME "x".png`)
	shim, record := recordingShim(t, bin, "viewer")
	for _, command := range []string{"viewer", shim} {
		t.Run(command, func(t *testing.T) {
			if err := os.Remove(record); err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("reset record: %v", err)
			}
			if err := present.NewCommandOpener(command).Open(img); err != nil {
				t.Fatalf("Open: %v", err)
			}
			wantArgs(t, record, img)
		})
	}
}
