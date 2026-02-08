package utils

import (
	"bytes"
	"os"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
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

	// We should see the themed role color applied (wrapped with ansiReset) and the username used for user role.
	if want := "<USER_COLOR>alice" + ansiReset + ": hello\n"; out != want {
		t.Fatalf("unexpected output\nwant: %q\ngot:  %q", want, out)
	}
}
