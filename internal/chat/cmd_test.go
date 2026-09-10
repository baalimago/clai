package chat

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
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

// TestChatCommand_summarizeSub pins the summarize verb: its flags register
// on the sub only, the positional window is required and parsed through the injected
// ParseSince, the summarizer is built through the injected constructor and
// attached with the options for this verb only, and setup failures never
// reach the handler.
func TestChatCommand_summarizeSub(t *testing.T) {
	since := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	type depCalls struct {
		sinceInput  string
		summarizers int
	}
	build := func(t *testing.T, s models.Summarizer, newErr, parseErr error) (*internal.Command, *depCalls, string) {
		t.Helper()
		confDir := t.TempDir()
		calls := &depCalls{}
		deps := CommandDeps{
			ConfigPrep: func() (string, error) { return confDir, nil },
			NewSummarizer: func(dir string) (models.Summarizer, error) {
				calls.summarizers++
				if dir != confDir {
					t.Fatalf("NewSummarizer dir = %q, want %q", dir, confDir)
				}
				return s, newErr
			},
			ParseSince: func(v string, _ time.Time) (time.Time, error) {
				calls.sinceInput = v
				return since, parseErr
			},
		}
		oldLive := utils.Live
		t.Cleanup(func() { utils.Live = oldLive })
		return Command(deps), calls, confDir
	}
	sub := func(c *internal.Command, name string) *internal.Command {
		return c.Subcommands()[name].(*internal.Command)
	}

	t.Run("flags register on the sub only", func(t *testing.T) {
		c, _, _ := build(t, nil, nil, nil)
		if !strings.Contains(c.Describe(), "summarize") {
			t.Fatalf("parent description must list the verb, got %q", c.Describe())
		}
		s := sub(c, "summarize|s")
		if s.Flagset().Lookup("since") != nil {
			t.Fatal("the window is positional; summarize must not register -since")
		}
		for _, name := range []string{"force", "y", "workers", "sm", "r", "n", "p"} {
			if s.Flagset().Lookup(name) == nil {
				t.Fatalf("summarize must register -%v", name)
			}
		}
		for _, other := range []*internal.Command{c, sub(c, "continue|c"), sub(c, "list|l")} {
			for _, name := range []string{"force", "y", "workers", "sm"} {
				if other.Flagset().Lookup(name) != nil {
					t.Fatalf("%v must not register -%v", other.Name, name)
				}
			}
		}
	})

	t.Run("setup attaches summarizer and options for this verb only", func(t *testing.T) {
		s := instantSummarizer()
		var gotModel string
		s.fn = func(_ context.Context, req models.SummaryRequest) (models.Summary, error) {
			gotModel = req.Model
			return fakeLabel(req.Chat.ID), nil
		}
		c, calls, confDir := build(t, s, nil, nil)
		convDir := conversationsDir(confDir)
		if err := os.MkdirAll(convDir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := Save(convDir, pub_models.Chat{ID: "seed", Created: time.Now(), Title: "old", Summary: "old", Messages: []pub_models.Message{{Role: "user", Content: "hi"}}}); err != nil {
			t.Fatalf("Save: %v", err)
		}
		verb := sub(c, "summarize|s")
		if err := verb.Flagset().Parse([]string{"-y", "-force", "-sm", "test", "7d"}); err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if err := verb.Setup(context.Background()); err != nil {
			t.Fatalf("Setup: %v", err)
		}
		if calls.sinceInput != "7d" || calls.summarizers != 1 {
			t.Fatalf("deps calls = %+v, want ParseSince(7d) and one summarizer", calls)
		}
		testboil.CaptureStdout(t, func(t *testing.T) {
			if err := verb.Run(context.Background()); err != nil {
				t.Fatalf("Run: %v", err)
			}
		})
		if gotModel != "test" {
			t.Fatalf("summarizer model = %q, want the -sm value", gotModel)
		}
		got, err := FromPath(filepath.Join(convDir, "seed.json"))
		if err != nil {
			t.Fatalf("FromPath: %v", err)
		}
		if got.Title != "title-seed" {
			t.Fatalf("title = %q, want the forced relabel", got.Title)
		}

		cont := sub(c, "continue|c")
		if err := cont.Flagset().Parse([]string{"seed"}); err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if err := cont.Setup(context.Background()); err != nil {
			t.Fatalf("Setup(continue): %v", err)
		}
		if calls.summarizers != 1 {
			t.Fatalf("continue must not build a summarizer, got %d constructions", calls.summarizers)
		}
	})

	t.Run("missing window", func(t *testing.T) {
		c, calls, _ := build(t, instantSummarizer(), nil, nil)
		verb := sub(c, "summarize|s")
		if err := verb.Flagset().Parse([]string{"-y"}); err != nil {
			t.Fatalf("Parse: %v", err)
		}
		err := verb.Setup(context.Background())
		if err == nil || !strings.Contains(err.Error(), "<window>") {
			t.Fatalf("err = %v, want the usage naming <window>", err)
		}
		if calls.summarizers != 0 {
			t.Fatal("no summarizer may be built without a window")
		}
	})

	t.Run("extra positional arguments are refused", func(t *testing.T) {
		c, calls, _ := build(t, instantSummarizer(), nil, nil)
		verb := sub(c, "summarize|s")
		if err := verb.Flagset().Parse([]string{"-y", "7d", "extra"}); err != nil {
			t.Fatalf("Parse: %v", err)
		}
		err := verb.Setup(context.Background())
		if err == nil || !strings.Contains(err.Error(), "<window>") {
			t.Fatalf("err = %v, want the usage naming <window>", err)
		}
		if calls.summarizers != 0 {
			t.Fatal("no summarizer may be built with extra arguments")
		}
	})

	t.Run("a flag after the window names the order", func(t *testing.T) {
		c, calls, _ := build(t, instantSummarizer(), nil, nil)
		verb := sub(c, "summarize|s")
		// stdlib flag parsing stops at the first positional, so -y lands in Args.
		if err := verb.Flagset().Parse([]string{"7d", "-y"}); err != nil {
			t.Fatalf("Parse: %v", err)
		}
		err := verb.Setup(context.Background())
		if err == nil || !strings.Contains(err.Error(), "flags go before the window") || !strings.Contains(err.Error(), "-y") {
			t.Fatalf("err = %v, want the flag-order hint naming -y", err)
		}
		if calls.summarizers != 0 {
			t.Fatal("no summarizer may be built on a usage error")
		}
	})

	t.Run("unparsable window", func(t *testing.T) {
		parseErr := errors.New("since: expected a duration, an RFC 3339 timestamp or a YYYY-MM-DD date")
		c, calls, _ := build(t, instantSummarizer(), nil, parseErr)
		verb := sub(c, "summarize|s")
		if err := verb.Flagset().Parse([]string{"-y", "yesterday"}); err != nil {
			t.Fatalf("Parse: %v", err)
		}
		err := verb.Setup(context.Background())
		if !errors.Is(err, parseErr) {
			t.Fatalf("err = %v, want the ParseSince error naming the accepted forms", err)
		}
		if calls.summarizers != 0 {
			t.Fatal("no summarizer may be built after a window parse failure")
		}
	})

	t.Run("-workers below one is a usage error", func(t *testing.T) {
		for _, workers := range []string{"0", "-4"} {
			c, calls, _ := build(t, instantSummarizer(), nil, nil)
			verb := sub(c, "summarize|s")
			if err := verb.Flagset().Parse([]string{"-y", "-workers", workers, "7d"}); err != nil {
				t.Fatalf("Parse: %v", err)
			}
			err := verb.Setup(context.Background())
			if err == nil || !strings.Contains(err.Error(), "-workers") {
				t.Fatalf("-workers %s: err = %v, want a usage error naming the flag", workers, err)
			}
			if calls.summarizers != 0 {
				t.Fatalf("-workers %s: no summarizer may be built on a usage error", workers)
			}
		}
	})

	t.Run("NewSummarizer error", func(t *testing.T) {
		newErr := errors.New("no text config")
		c, _, _ := build(t, nil, newErr, nil)
		verb := sub(c, "summarize|s")
		if err := verb.Flagset().Parse([]string{"7d"}); err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if err := verb.Setup(context.Background()); !errors.Is(err, newErr) {
			t.Fatalf("err = %v, want the wrapped constructor error", err)
		}
	})
}
