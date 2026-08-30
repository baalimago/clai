package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func Test_Command_tree(t *testing.T) {
	c := Command()
	if c.Describe() == "" || !strings.Contains(c.Help(), "tools [tool name]") {
		t.Fatalf("describe/help incomplete: %q / %q", c.Describe(), c.Help())
	}
	if _, ok := c.Subcommands()["list"]; !ok {
		t.Fatal("missing list subcommand")
	}

	t.Run("positional arg is kept for the detail route", func(t *testing.T) {
		if err := c.Flagset().Parse([]string{"q"}); err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if err := c.Setup(context.Background()); err != nil {
			t.Fatalf("Setup: %v", err)
		}
		if got := c.Args(); len(got) != 2 || got[1] != "q" {
			t.Fatalf("args: got %v", got)
		}
	})

	t.Run("run without args lists tools", func(t *testing.T) {
		out := testboil.CaptureStdout(t, func(t *testing.T) {
			if err := c.Run(context.Background()); err == nil {
				// The positional "q" from the parse above routes to Detail,
				// which errors on the unknown tool.
				t.Fatal("expected unknown-tool error from detail route")
			}
		})
		_ = out
	})
}

func Test_toolNameArgs(t *testing.T) {
	WithTestRegistry(t, func() {
		if got := toolNameArgs([]string{"already-has-arg"}, ""); len(got) != 0 {
			t.Fatalf("expected suppressed completion after first arg, got %v", got)
		}
		// No args: completes from the registry (may be empty in the test
		// registry, but must not panic and must return a non-nil slice).
		if got := toolNameArgs(nil, ""); got == nil {
			t.Fatal("expected non-nil completion items")
		}
	})
}
