package utils

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestAttemptPrettyPrint_UsesThemeColorsWhenNoGlow(t *testing.T) {
	// Ensure NO_COLOR isn't set so we exercise color output. The buffer writer
	// is a captured destination, which forces the plain ANSI fallback.
	t.Setenv("NO_COLOR", "")

	// Set a clearly identifiable theme color, and restore the default after
	// the test so later tests see the package default.
	origTheme := globalTheme
	t.Cleanup(func() { globalTheme = origTheme })
	globalTheme = Theme{
		Primary:   "<PRIMARY>",
		Secondary: "<SECONDARY>",
		Breadtext: "<BREADTEXT>",

		RoleUser:   "<USER_COLOR>",
		RoleSystem: "<SYSTEM_COLOR>",
		RoleTool:   "<TOOL_COLOR>",
		RoleOther:  "<OTHER_COLOR>",
	}

	var buf bytes.Buffer
	msg := pub_models.Message{Role: "user", Content: "hello"}
	if err := AttemptPrettyPrint(&buf, msg, "alice", false); err != nil {
		t.Fatalf("AttemptPrettyPrint: %v", err)
	}
	out := buf.String()

	// We should see the themed role color applied (wrapped with ANSI reset) and the username used for user role.
	if want := "<USER_COLOR>alice" + "\u001b[0m" + ": hello\n"; out != want {
		t.Fatalf("unexpected output\nwant: %q\ngot:  %q", want, out)
	}
}

func Test_glowRenderArgs(t *testing.T) {
	t.Run("keeps five columns clear for the role prefix", func(t *testing.T) {
		if got, want := glowRenderArgs(100), []string{"-w", "95"}; !slices.Equal(got, want) {
			t.Fatalf("unexpected glow args\nwant: %q\ngot:  %q", want, got)
		}
	})

	t.Run("never renders narrower than one column", func(t *testing.T) {
		for _, width := range []int{0, 3, 5} {
			if got, want := glowRenderArgs(width), []string{"-w", "1"}; !slices.Equal(got, want) {
				t.Fatalf("glowRenderArgs(%d): want %q, got %q", width, want, got)
			}
		}
	})
}

func Test_isTerminalWriter(t *testing.T) {
	t.Run("captured writers are not terminals", func(t *testing.T) {
		var buf bytes.Buffer
		if isTerminalWriter(&buf) {
			t.Fatal("a memory writer must not count as a terminal")
		}
	})

	t.Run("regular files are not terminals", func(t *testing.T) {
		f, err := os.CreateTemp(t.TempDir(), "captured")
		if err != nil {
			t.Fatalf("CreateTemp: %v", err)
		}
		defer f.Close()
		if isTerminalWriter(f) {
			t.Fatal("a regular file must not count as a terminal")
		}
	})

	t.Run("character devices count as terminals", func(t *testing.T) {
		devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
		if err != nil {
			t.Fatalf("open %s: %v", os.DevNull, err)
		}
		defer devNull.Close()
		if !isTerminalWriter(devNull) {
			t.Fatal("a character device must count as a terminal")
		}
	})

	t.Run("nil resolves to stdout", func(t *testing.T) {
		if isTerminalWriter(nil) != isTerminalWriter(os.Stdout) {
			t.Fatal("nil writer must resolve to the same decision as os.Stdout")
		}
	})
}

// TestAttemptPrettyPrint_SkipsGlowForCapturedWriters pins the R10-01 fix: a
// captured destination (pipe, file, test buffer) must never spawn the glow
// subprocess — the plain ANSI fallback renders instead, so the per-message
// print path stays deterministic under the race detector.
func TestAttemptPrettyPrint_SkipsGlowForCapturedWriters(t *testing.T) {
	t.Setenv("NO_COLOR", "")

	argsPath := filepath.Join(t.TempDir(), "glow-args.txt")
	glowPath := filepath.Join(t.TempDir(), "glow")
	if err := os.WriteFile(glowPath, []byte(glowRecordScript(argsPath)), 0o755); err != nil {
		t.Fatalf("write fake glow: %v", err)
	}
	t.Setenv("PATH", filepath.Dir(glowPath))

	var buf bytes.Buffer
	msg := pub_models.Message{Role: "assistant", Content: "hello markdown"}
	if err := AttemptPrettyPrint(&buf, msg, "alice", false); err != nil {
		t.Fatalf("AttemptPrettyPrint: %v", err)
	}

	if _, err := os.Stat(argsPath); !os.IsNotExist(err) {
		t.Fatalf("glow must not be spawned for a captured writer; args file %s exists", argsPath)
	}
	if out := buf.String(); !strings.Contains(out, "assistant") || !strings.Contains(out, "hello markdown") {
		t.Fatalf("expected the plain ANSI fallback, got %q", out)
	}
}

// TestAttemptPrettyPrint_UsesGlowForTerminalWriters proves the glow renderer
// still receives the width-aware args when the destination is a terminal
// (character device), and that the version probe answers the fake glow.
func TestAttemptPrettyPrint_UsesGlowForTerminalWriters(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("COLUMNS", "100")

	argsPath := filepath.Join(t.TempDir(), "glow-args.txt")
	glowPath := filepath.Join(t.TempDir(), "glow")
	if err := os.WriteFile(glowPath, []byte(glowRecordScript(argsPath)), 0o755); err != nil {
		t.Fatalf("write fake glow: %v", err)
	}
	t.Setenv("PATH", filepath.Dir(glowPath))

	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open %s: %v", os.DevNull, err)
	}
	defer devNull.Close()

	msg := pub_models.Message{Role: "assistant", Content: "hello markdown"}
	if err := AttemptPrettyPrint(devNull, msg, "alice", false); err != nil {
		t.Fatalf("AttemptPrettyPrint: %v", err)
	}

	gotArgsBytes, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read fake glow args: %v", err)
	}
	if got, want := strings.TrimSpace(string(gotArgsBytes)), "-w 95"; got != want {
		t.Fatalf("unexpected glow args\nwant: %q\ngot:  %q", want, got)
	}
}

// glowRecordScript returns a fake glow shell script that answers the version
// probe and records the render invocation's arguments into argsPath.
func glowRecordScript(argsPath string) string {
	return fmt.Sprintf(`#!/bin/sh
if [ "$1" = "--version" ]; then
	echo "glow test"
	exit 0
fi
printf '%%s\n' "$*" > %q
/bin/cat
`, argsPath)
}
