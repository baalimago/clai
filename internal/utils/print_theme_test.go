package utils

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/table"
)

func TestAttemptPrettyPrint_UsesThemeColorsWhenNoGlow(t *testing.T) {
	// Ensure NO_COLOR isn't set so we exercise color output.
	t.Setenv("NO_COLOR", "")

	// Force the "no glow installed" path.
	origPath := os.Getenv("PATH")
	t.Cleanup(func() { _ = os.Setenv("PATH", origPath) })
	if err := os.Setenv("PATH", ""); err != nil {
		t.Fatalf("set PATH empty: %v", err)
	}

	// Set a clearly identifiable theme color.
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

func TestAttemptPrettyPrint_PassesTerminalWidthMinusFiveToGlow(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("COLUMNS", "100")

	origTheme := globalTheme
	t.Cleanup(func() { globalTheme = origTheme })
	globalTheme = Theme{}

	tmpDir := t.TempDir()
	argsPath := filepath.Join(tmpDir, "glow-args.txt")
	glowPath := filepath.Join(tmpDir, "glow")

	script := fmt.Sprintf(`#!/bin/sh
if [ "$1" = "--version" ]; then
	echo "glow test"
	exit 0
fi
printf '%%s\n' "$*" > %q
/bin/cat
`, argsPath)

	if err := os.WriteFile(glowPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake glow: %v", err)
	}

	t.Setenv("PATH", tmpDir)

	var buf bytes.Buffer
	msg := pub_models.Message{Role: "assistant", Content: "hello markdown"}
	if err := AttemptPrettyPrint(&buf, msg, "alice", false); err != nil {
		t.Fatalf("AttemptPrettyPrint: %v", err)
	}

	gotArgsBytes, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read fake glow args: %v", err)
	}

	if got, want := strings.TrimSpace(string(gotArgsBytes)), "-w 95"; got != want {
		t.Fatalf("unexpected glow args\nwant: %q\ngot:  %q", want, got)
	}
	_ = table.NoColor // ensure import used
}
