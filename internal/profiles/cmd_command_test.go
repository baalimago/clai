package profiles

import (
	"context"
	"strings"
	"testing"
)

func Test_Command_tree(t *testing.T) {
	t.Setenv("CLAI_CONFIG_DIR", t.TempDir())
	c := Command(CommandDeps{})
	if c.Describe() == "" || !strings.Contains(c.Help(), "profiles [list|install|update|trust|publish]") {
		t.Fatalf("describe/help incomplete: %q / %q", c.Describe(), c.Help())
	}
	for _, want := range []string{Help, RemoteHelp, "<config-dir>/profiles/<name>.json", "clai profiles publish", "cchub:<handle>/<agent>"} {
		if !strings.Contains(c.Help(), want) {
			t.Fatalf("profiles help lacks %q", want)
		}
	}
	if strings.Contains(c.Help(), `\'`) {
		t.Fatal("profiles help holds an escaped quote")
	}
	if _, ok := c.Subcommands()["list"]; !ok {
		t.Fatal("missing list subcommand")
	}

	t.Run("unknown positional errors on run", func(t *testing.T) {
		if err := c.Flagset().Parse([]string{"bogus"}); err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if err := c.Setup(context.Background()); err != nil {
			t.Fatalf("Setup: %v", err)
		}
		if err := c.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "unknown profiles subcommand") {
			t.Fatalf("expected unknown-subcommand error, got: %v", err)
		}
	})
}
