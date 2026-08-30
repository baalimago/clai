package chat

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/clai/internal/utils"
)

func Test_Command_tree(t *testing.T) {
	prepCalls := 0
	deps := CommandDeps{
		ConfigPrep: func() (string, error) {
			prepCalls++
			return t.TempDir(), nil
		},
	}
	c := Command(deps)
	if c.Describe() == "" || !strings.Contains(c.Help(), "clai chat help") {
		t.Fatalf("describe/help incomplete: %q / %q", c.Describe(), c.Help())
	}
	for _, sub := range []string{"continue|c", "delete|d", "list|l", "dir", "dirv2", "help|h"} {
		if _, ok := c.Subcommands()[sub]; !ok {
			t.Fatalf("missing subcommand %q", sub)
		}
	}

	// Every chat subcommand reads stored transcripts and runs no model, so
	// the agent group's flags would be inert noise on 'clai c -h'.
	t.Run("no model flags on the tree", func(t *testing.T) {
		for name, command := range map[string]*internal.Command{
			"chat":     c,
			"continue": c.Subcommands()["continue|c"].(*internal.Command),
		} {
			for _, flagName := range []string{"cm", "t", "mt", "mtc", "cmd-ban", "lb", "g", "am", "af", "prp"} {
				if command.Flagset().Lookup(flagName) != nil {
					t.Fatalf("%v must not register the inert flag -%v", name, flagName)
				}
			}
			for _, flagName := range []string{"r", "n", "p"} {
				if command.Flagset().Lookup(flagName) == nil {
					t.Fatalf("%v must register -%v", name, flagName)
				}
			}
		}
	})

	t.Run("continue sets up without a model querier", func(t *testing.T) {
		before := prepCalls
		sub := c.Subcommands()["continue|c"].(*internal.Command)
		if err := sub.Flagset().Parse([]string{"0"}); err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if err := sub.Setup(context.Background()); err != nil {
			t.Fatalf("Setup: %v", err)
		}
		if prepCalls != before+1 {
			t.Fatalf("config prep calls: got %v want %v", prepCalls, before+1)
		}
	})

	t.Run("list sub is structurally read-only", func(t *testing.T) {
		old := utils.NoCreateConfig
		t.Cleanup(func() { utils.NoCreateConfig = old })
		utils.NoCreateConfig = false
		sub := c.Subcommands()["list|l"].(*internal.Command)
		if err := sub.Flagset().Parse(nil); err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if err := sub.Setup(context.Background()); err != nil {
			t.Fatalf("Setup: %v", err)
		}
		if !utils.NoCreateConfig {
			t.Fatal("read-only sub must set NoCreateConfig")
		}
	})
}

// Test_New pins the handler's arg shape: the dispatcher hands over
// "<verb> <rest...>", the verb selects the action and the rest is the
// prompt (a chat id or index for continue/delete).
func Test_New(t *testing.T) {
	t.Run("splits verb from prompt", func(t *testing.T) {
		h, err := New(t.TempDir(), "continue my-chat-id", "gopher", true, io.Discard)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if h.subCmd != "continue" || h.prompt != "my-chat-id" {
			t.Fatalf("got subCmd=%q prompt=%q", h.subCmd, h.prompt)
		}
		if h.profile != "gopher" || !h.raw {
			t.Fatalf("got profile=%q raw=%v", h.profile, h.raw)
		}
	})

	t.Run("list macro inputs", func(t *testing.T) {
		h, err := New(t.TempDir(), "list q", "", false, io.Discard)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if h.input == nil {
			t.Fatal("trailing args after 'list' must become macro inputs")
		}
	})

	t.Run("requires a config dir", func(t *testing.T) {
		if _, err := New("", "list", "", false, io.Discard); err == nil {
			t.Fatal("expected an error for an empty config dir")
		}
	})
}

func Test_ReplayCommand_construction(t *testing.T) {
	c := ReplayCommand()
	if c.Describe() == "" || !strings.Contains(c.Help(), "clai re") {
		t.Fatalf("describe/help incomplete: %q / %q", c.Describe(), c.Help())
	}
	if c.Flagset().Lookup("r") == nil {
		t.Fatal("replay must own -r")
	}
}

func Test_DirscopeReplayCommand_setup(t *testing.T) {
	c := DirscopeReplayCommand()
	if c.Describe() == "" || !strings.Contains(c.Help(), "clai dre") {
		t.Fatalf("describe/help incomplete: %q / %q", c.Describe(), c.Help())
	}
	if err := c.Flagset().Parse([]string{"-r"}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := c.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
}
