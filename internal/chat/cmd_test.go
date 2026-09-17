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
	"github.com/baalimago/clai/internal/vendors"
	"github.com/baalimago/clai/internal/vendors/anthropic"
	"github.com/baalimago/clai/internal/vendors/jsonltest"
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

// --- foreign cache wiring (phase 5) -----------------------------------------

// countingCacheFactory is a CommandDeps.ForeignCache that records how often
// the command tree asked for an index.
func countingCacheFactory(cache vendors.SourceCache) (func() vendors.SourceCache, *int) {
	calls := 0
	return func() vendors.SourceCache {
		calls++
		return cache
	}, &calls
}

// chatTreeDeps builds a tree whose config prep never touches a real config
// directory.
func chatTreeDeps(t *testing.T, factory func() vendors.SourceCache) CommandDeps {
	t.Helper()
	confDir := t.TempDir()
	return CommandDeps{
		ConfigPrep:   func() (string, error) { return confDir, nil },
		ForeignCache: factory,
	}
}

// TestChatCommand_cacheFactoryRunsOncePerVerb: building the tree asks for no
// index, building a handler asks for none either — the factory is resolved
// where the cache is consulted (D27) — and a verb that does consult it asks
// for exactly one. Summarize builds its own handler and lists nothing, so it
// asks for none. The verbs that never consult the cache are asserted by
// TestChatCommand_onlyConsultingVerbsConstructTheIndex.
func TestChatCommand_cacheFactoryRunsOncePerVerb(t *testing.T) {
	for _, verb := range []string{"continue|c", "list|l"} {
		t.Run(verb, func(t *testing.T) {
			restoreFlags(t)
			factory, calls := countingCacheFactory(nil)
			c := Command(chatTreeDeps(t, factory))
			if *calls != 0 {
				t.Fatalf("building the chat tree asked for %d indexes, want 0", *calls)
			}
			sub := c.Subcommands()[verb].(*internal.Command)
			if err := sub.Flagset().Parse(nil); err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if err := sub.Setup(context.Background()); err != nil {
				t.Fatalf("Setup: %v", err)
			}
			if *calls != 0 {
				t.Fatalf("the %q verb asked for %d indexes before consulting the cache, want 0", verb, *calls)
			}
		})

		t.Run(verb+" run to completion", func(t *testing.T) {
			if calls := runChatVerbCounting(t, verb, []string{"-n", "-r", "q"}); calls != 1 {
				t.Fatalf("the %q verb asked for %d indexes, want exactly 1", verb, calls)
			}
		})
	}

	t.Run("summarize|s", func(t *testing.T) {
		restoreFlags(t)
		factory, calls := countingCacheFactory(nil)
		deps := chatTreeDeps(t, factory)
		deps.NewSummarizer = func(string) (models.Summarizer, error) { return nil, errors.New("no summarizer") }
		deps.ParseSince = func(string, time.Time) (time.Time, error) { return time.Time{}, nil }
		c := Command(deps)
		sub := c.Subcommands()["summarize|s"].(*internal.Command)
		if err := sub.Flagset().Parse([]string{"7d"}); err != nil {
			t.Fatalf("Parse: %v", err)
		}
		_ = sub.Setup(context.Background())
		if *calls != 0 {
			t.Fatalf("summarize asked for %d indexes; it lists nothing", *calls)
		}
	})
}

// TestChatCommand_failedFactoryLeavesCacheNil: a factory that could not build
// an index yields a nil interface, the handler's field stays unset, and the
// listing falls back to scanning. An interface holding a nil pointer is not
// nil, and discovery would not read it as "always scan". Under D27 the
// factory is not invoked until a verb consults the cache, so the field is
// unset before that too.
func TestChatCommand_failedFactoryLeavesCacheNil(t *testing.T) {
	restoreFlags(t)
	factory, calls := countingCacheFactory(nil)
	h, err := newChatQuerier(t.TempDir(), []string{"chat", "list"}, &internal.ChatFlags{}, factory)
	if err != nil {
		t.Fatalf("newChatQuerier: %v", err)
	}
	if h.foreignCache != nil {
		t.Fatalf("building the handler left %#v on it, want a nil interface", h.foreignCache)
	}
	if got := h.foreignCacheOrNil(); got != nil {
		t.Fatalf("a failed factory resolved to %#v, want a nil interface", got)
	}
	if *calls != 1 {
		t.Fatalf("the consultation ran the factory %d times, want exactly 1", *calls)
	}
	if h.foreignCache != nil {
		t.Fatalf("a failed factory left %#v on the handler, want a nil interface", h.foreignCache)
	}

	// A composition root that produced no factory at all is the same case.
	noFactory, err := newChatQuerier(t.TempDir(), []string{"chat", "list"}, &internal.ChatFlags{}, nil)
	if err != nil {
		t.Fatalf("newChatQuerier without a factory: %v", err)
	}
	if got := noFactory.foreignCacheOrNil(); got != nil {
		t.Fatalf("a missing factory resolved to %#v, want a nil interface", got)
	}
}

// restoreFlags puts the process-wide config flags back after a test that
// drives a real verb through Command.Setup, which sets them.
func restoreFlags(t *testing.T) {
	t.Helper()
	noCreate, readonly, live := utils.NoCreateConfig, utils.ReadonlyConfig, utils.Live
	t.Cleanup(func() {
		utils.NoCreateConfig, utils.ReadonlyConfig, utils.Live = noCreate, readonly, live
	})
}

// runListVerb drives the real `chat list` verb to completion against an
// isolated config dir and a generated corpus, with the given flags. It
// returns what the verb wrote to stderr, which is where the handler announces
// a foreign cache it could not write.
func runListVerb(t *testing.T, args []string) string {
	t.Helper()
	restoreFlags(t)
	confDir := t.TempDir()
	if err := utils.CreateConfigDir(confDir); err != nil {
		t.Fatalf("CreateConfigDir: %v", err)
	}
	t.Setenv("CLAI_CONFIG_DIR", confDir)

	root := filepath.Join(t.TempDir(), "projects")
	jsonltest.WriteCorpus(t, root, smallCorpus(jsonltest.ShapeClaude))
	t.Cleanup(useTestSourceReaders([]vendors.SourceReader{anthropic.SourceReader{Root: root}}))

	// A cache directory whose parent is a file can never be made, so the
	// persist at the end of the listing fails however the machine is set up.
	blocked := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocked, []byte("occupied"), 0o644); err != nil {
		t.Fatalf("write %q: %v", blocked, err)
	}
	factory := func() vendors.SourceCache {
		idx, err := NewForeignIndex(filepath.Join(blocked, "clai"))
		if err != nil {
			t.Errorf("NewForeignIndex: %v", err)
			return nil
		}
		return idx
	}

	c := Command(CommandDeps{
		ConfigPrep:   func() (string, error) { return confDir, nil },
		ForeignCache: factory,
	})
	sub := c.Subcommands()["list|l"].(*internal.Command)
	if err := sub.Flagset().Parse(args); err != nil {
		t.Fatalf("Parse(%v): %v", args, err)
	}
	ctx := context.Background()
	stderr := ""
	testboil.CaptureStdout(t, func(t *testing.T) {
		stderr = testboil.CaptureStderr(t, func(t *testing.T) {
			if err := sub.Setup(ctx); err != nil {
				t.Errorf("Setup: %v", err)
				return
			}
			if err := sub.Run(ctx); err != nil {
				t.Errorf("Run: %v", err)
			}
		})
	})
	return stderr
}

// TestChatList_unwritableCacheWarnsThroughTheVerb: `clai chat list` sets
// utils.NoCreateConfig unconditionally, so gating the warning on that flag
// made it unreachable for the one verb the cache exists for — the user
// rescanned the whole foreign corpus on every listing with no way to learn
// why (R1-03, D25). Driven through the real verb, because it is the verb
// that sets the flag.
func TestChatList_unwritableCacheWarnsThroughTheVerb(t *testing.T) {
	got := runListVerb(t, []string{"q"})
	if n := strings.Count(got, "warning:"); n != 1 {
		t.Fatalf("an interactive listing printed %d warnings, want exactly 1: %q", n, got)
	}
}

// TestChatList_rawVerbStaysSilent: the same fault under -r, which is what a
// shell prompt hook or a script uses. utils.ReadonlyConfig marks it, and it
// must see nothing on stderr.
func TestChatList_rawVerbStaysSilent(t *testing.T) {
	got := runListVerb(t, []string{"-r", "-n", "q"})
	if got != "" {
		t.Fatalf("a raw listing surfaced %q; a shell-prompt hook must stay quiet", got)
	}
}

// --- review two: the index is built only by a verb that reads it (D27) ------

// runChatVerbCounting drives one real chat verb to completion against an
// isolated config dir and a generated corpus, and reports how many times that
// verb asked the composition root for a foreign index. A verb may legitimately
// fail — `delete` with no such chat does — and must still construct nothing.
func runChatVerbCounting(t *testing.T, verb string, args []string) int {
	t.Helper()
	restoreFlags(t)
	confDir := t.TempDir()
	if err := utils.CreateConfigDir(confDir); err != nil {
		t.Fatalf("CreateConfigDir: %v", err)
	}
	t.Setenv("CLAI_CONFIG_DIR", confDir)

	root := filepath.Join(t.TempDir(), "projects")
	jsonltest.WriteCorpus(t, root, smallCorpus(jsonltest.ShapeClaude))
	t.Cleanup(useTestSourceReaders([]vendors.SourceReader{anthropic.SourceReader{Root: root}}))

	// A populated cache directory, so a construction is a real read and
	// decode of a cache file rather than a no-op.
	cacheDir := t.TempDir()
	warm := newForeignIndexT(t, cacheDir)
	if _, err := (anthropic.SourceReader{Root: root}).Discover(context.Background(), warm); err != nil {
		t.Fatalf("warming Discover: %v", err)
	}
	persistT(t, warm)

	calls := 0
	factory := func() vendors.SourceCache {
		calls++
		idx, err := NewForeignIndex(cacheDir)
		if err != nil {
			t.Errorf("NewForeignIndex: %v", err)
			return nil
		}
		return idx
	}
	c := Command(CommandDeps{
		ConfigPrep:   func() (string, error) { return confDir, nil },
		ForeignCache: factory,
	})
	sub := c.Subcommands()[verb].(*internal.Command)
	if err := sub.Flagset().Parse(args); err != nil {
		t.Fatalf("Parse(%v): %v", args, err)
	}
	ctx := context.Background()
	testboil.CaptureStdout(t, func(t *testing.T) {
		if err := sub.Setup(ctx); err != nil {
			t.Errorf("Setup: %v", err)
			return
		}
		_ = sub.Run(ctx)
	})
	return calls
}

// TestChatCommand_onlyConsultingVerbsConstructTheIndex: readOnlyChatSetup
// serves shell-prompt hot paths, so `clai -r c dirv2` in a precmd hook used to
// decode a corpus-sized index on every prompt render. Only a verb that
// actually reads the cache may build it (R2-03, D27).
func TestChatCommand_onlyConsultingVerbsConstructTheIndex(t *testing.T) {
	for _, tc := range []struct {
		verb string
		args []string
	}{
		{"help|h", nil},
		{"dir", []string{"-r"}},
		{"dirv2", []string{"-r"}},
		{"delete|d", []string{"-r", "-n", "no-such-chat"}},
	} {
		t.Run(tc.verb, func(t *testing.T) {
			if calls := runChatVerbCounting(t, tc.verb, tc.args); calls != 0 {
				t.Fatalf("the %q verb constructed the foreign index %d times; it never consults it", tc.verb, calls)
			}
		})
	}

	t.Run("list|l", func(t *testing.T) {
		if calls := runChatVerbCounting(t, "list|l", []string{"-r", "-n", "q"}); calls != 1 {
			t.Fatalf("the listing constructed the foreign index %d times, want exactly 1", calls)
		}
	})
}

// TestChatCommand_lazyFactoryRunsAtMostOnce: the lazy resolution is guarded,
// so however many times the consuming code asks — a listing that repaginates,
// a continue that falls back to the list — exactly one index is built and
// every asker gets the same one.
func TestChatCommand_lazyFactoryRunsAtMostOnce(t *testing.T) {
	restoreFlags(t)
	cacheDir := t.TempDir()
	built := 0
	factory := func() vendors.SourceCache {
		built++
		idx := newForeignIndexT(t, cacheDir)
		return idx
	}
	h, err := newChatQuerier(t.TempDir(), []string{"chat", "list"}, &internal.ChatFlags{}, factory)
	if err != nil {
		t.Fatalf("newChatQuerier: %v", err)
	}
	if built != 0 {
		t.Fatalf("building the handler constructed %d indexes; only a consulting verb may (D27)", built)
	}

	root := filepath.Join(t.TempDir(), "projects")
	jsonltest.WriteCorpus(t, root, smallCorpus(jsonltest.ShapeClaude))
	readers := []vendors.SourceReader{anthropic.SourceReader{Root: root}}
	first, err := h.foreignChatRows(context.Background(), readers, map[string]struct{}{})
	if err != nil {
		t.Fatalf("first foreignChatRows: %v", err)
	}
	after := h.foreignCache
	second, err := h.foreignChatRows(context.Background(), readers, map[string]struct{}{})
	if err != nil {
		t.Fatalf("second foreignChatRows: %v", err)
	}
	if built != 1 {
		t.Fatalf("two consultations constructed %d indexes, want exactly 1", built)
	}
	if h.foreignCache != after {
		t.Fatal("the second consultation replaced the index the first one built")
	}
	if len(first) == 0 || len(second) != len(first) {
		t.Fatalf("the warm consultation returned %d rows against %d cold", len(second), len(first))
	}
}

// TestChatCommand_lazyFactoryFailureLeavesCacheUnset: a lazy construction
// that fails is remembered, not retried. Without the once-guard a listing
// whose index cannot be built would pay the failing construction again on
// every consultation, and the field must stay a nil interface throughout so
// discovery keeps scanning.
func TestChatCommand_lazyFactoryFailureLeavesCacheUnset(t *testing.T) {
	restoreFlags(t)
	factory, calls := countingCacheFactory(nil)
	h, err := newChatQuerier(t.TempDir(), []string{"chat", "list"}, &internal.ChatFlags{}, factory)
	if err != nil {
		t.Fatalf("newChatQuerier: %v", err)
	}
	if *calls != 0 {
		t.Fatalf("building the handler asked for %d indexes, want 0 (D27)", *calls)
	}

	root := filepath.Join(t.TempDir(), "projects")
	jsonltest.WriteCorpus(t, root, smallCorpus(jsonltest.ShapeClaude))
	readers := []vendors.SourceReader{anthropic.SourceReader{Root: root}}
	for i := range 3 {
		rows, err := h.foreignChatRows(context.Background(), readers, map[string]struct{}{})
		if err != nil {
			t.Fatalf("consultation %d: foreignChatRows: %v", i, err)
		}
		if len(rows) == 0 {
			t.Fatalf("consultation %d: the listing did not fall back to scanning", i)
		}
		if h.foreignCache != nil {
			t.Fatalf("consultation %d: a failed construction left %#v on the handler", i, h.foreignCache)
		}
	}
	if *calls != 1 {
		t.Fatalf("three consultations retried the failing construction %d times, want exactly 1", *calls)
	}
}
