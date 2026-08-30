package chat

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/cmd"
)

// CommandDeps are the composition-root collaborators the chat command
// needs: config prep lives in internal/setup, which chat cannot import
// (setup imports the domain packages), so main.go injects it.
type CommandDeps struct {
	ConfigPrep func() (confDir string, err error)
}

// Command builds the chat command tree.
func Command(deps CommandDeps) *internal.Command {
	sources := &internal.CompletionSources{}
	// One ChatFlags is shared by the whole tree: the parent registers its
	// flags at the top level and each sub registers its own subset onto its
	// own flagset, all against the same values — so parent-level flags stay
	// visible to the executing leaf (D11).
	cf := &internal.ChatFlags{}
	c := &internal.Command{
		Name: "chat",
		Desc: "Manage chats: continue|delete|list|dir|dirv2|help",
		HelpText: `chat <subcommand> [args]. Manages stored conversations. Run
'clai chat help' for detailed subcommand docs.

Examples:
  clai c l          # list chats
  clai c c 0        # continue the most recent chat
  clai -r c dirv2   # raw directory-scoped chat info`,
		Register:            cf.Register,
		Raw:                 &cf.Raw,
		NonInteractive:      &cf.NonInteractive,
		CompleteFlagValueFn: sources.TextFlagValues,
	}
	// An unmatched or absent positional stays with the parent, which keeps
	// today's chat.New behavior (unknown-subcommand error, chat usage).
	fullSetup := fullChatSetup(deps, cf)
	readOnlySetup := readOnlyChatSetup(cf)
	c.OnSetup = fullSetup
	rawOnly := func(fs *flag.FlagSet) { cf.Raw.Register(fs) }
	rawMacro := func(fs *flag.FlagSet) {
		cf.Raw.Register(fs)
		cf.NonInteractive.Register(fs)
	}
	c.Subs = map[string]cmd.Command{
		"continue|c": chatSub(sources, cf, "continue", "Continue an existing chat with the given chat ID or index",
			"  clai c c 0                    # continue by list index\n  clai chat continue my-chat-id",
			cf.Register, fullSetup),
		"delete|d": chatSub(sources, cf, "delete", "Delete the chat with the given chat ID or index",
			"  clai c d 0\n  clai chat delete my-chat-id",
			rawMacro, fullSetup),
		"list|l": chatSub(sources, cf, "list", "List all existing chats",
			"  clai c l\n  clai -n -r c l q   # print the list and quit (macro)",
			rawMacro, readOnlySetup),
		"dir": chatSub(sources, cf, "dir", "Show directory chat info with the stable v1 output",
			"  clai chat dir",
			rawOnly, readOnlySetup),
		"dirv2": chatSub(sources, cf, "dirv2", "Show directory chat info with total and recent token usage",
			"  clai -r c dirv2",
			rawOnly, readOnlySetup),
		"help|h": chatSub(sources, cf, "help", "Display detailed help for chat subcommands",
			"", nil, readOnlySetup),
	}
	return c
}

// chatSub builds one chat subcommand. It shares the tree's flag values,
// registers only the sub-relevant flag names on its own level, and
// reconstructs the legacy ["chat", verb, args...] shape the chat handler
// expects.
func chatSub(sources *internal.CompletionSources, cf *internal.ChatFlags, verb, describe, examples string, register func(fs *flag.FlagSet), setup func(ctx context.Context, c *internal.Command) error) *internal.Command {
	help := "chat " + verb + ": " + describe + "."
	if examples != "" {
		help += "\n\nExamples:\n" + examples
	}
	return &internal.Command{
		Name:           verb,
		Desc:           describe,
		HelpText:       help,
		Register:       register,
		Raw:            &cf.Raw,
		NonInteractive: &cf.NonInteractive,
		// The completion engine reads the hook off the deepest resolved
		// command, so each sub carries the tree's value sources.
		CompleteFlagValueFn: sources.TextFlagValues,
		OnSetup: func(ctx context.Context, c *internal.Command) error {
			c.SetArgs(append([]string{"chat"}, c.Args()...))
			return setup(ctx, c)
		},
	}
}

// fullChatSetup builds the config-touching chat path (config creation and
// migration allowed): continue, delete, and the parent's own fallthrough.
func fullChatSetup(deps CommandDeps, cf *internal.ChatFlags) func(ctx context.Context, c *internal.Command) error {
	return func(_ context.Context, c *internal.Command) error {
		confDir, err := deps.ConfigPrep()
		if err != nil {
			return err
		}
		return setChatQuerier(c, confDir, cf)
	}
}

// readOnlyChatSetup is structurally read-only: list, dir, dirv2 and help
// must not create or mutate config files, so they can run against a
// read-only filesystem or a missing config dir. They read no mode config,
// so they prep the theme only — the migration pass ConfigPrep would run is
// a guaranteed no-op under NoCreateConfig, and these run on shell-prompt
// hot paths.
func readOnlyChatSetup(cf *internal.ChatFlags) func(ctx context.Context, c *internal.Command) error {
	return func(_ context.Context, c *internal.Command) error {
		utils.NoCreateConfig = true
		confDir, err := internal.PrepTheme()
		if err != nil {
			return err
		}
		return setChatQuerier(c, confDir, cf)
	}
}

// setChatQuerier builds the handler both chat paths run. The dispatcher
// hands over ["chat", verb, args...]; the handler wants "<verb> <args...>".
func setChatQuerier(c *internal.Command, confDir string, cf *internal.ChatFlags) error {
	h, err := New(confDir, strings.Join(c.Args()[1:], " "), cf.Profile.Value(), cf.Raw.Value(), os.Stdout)
	if err != nil {
		return fmt.Errorf("create chat handler: %w", err)
	}
	c.SetQuerier(h)
	return nil
}

// ReplayCommand builds the replay command: print the previous reply again.
func ReplayCommand() *internal.Command {
	raw := &internal.RawFlag{}
	c := &internal.Command{
		Name: "replay",
		Desc: "Replay the most recent message",
		HelpText: `replay. Prints the previous reply again.

Examples:
  clai re
  clai -r re   # raw markdown, suitable for piping`,
		Register: raw.Register,
		Raw:      raw,
	}
	// Replay renders stored conversation content, so it needs the theme even
	// though it runs no config migration.
	c.OnSetup = func(_ context.Context, _ *internal.Command) error {
		_, err := internal.PrepTheme()
		return err
	}
	c.OnRun = func(_ context.Context, _ *internal.Command) error {
		if err := Replay(raw.Value(), false); err != nil {
			return fmt.Errorf("failed to replay previous reply: %w", err)
		}
		return nil
	}
	return c
}

// DirscopeReplayCommand builds the dir-replay command: print the last
// message of the directory-scoped conversation.
func DirscopeReplayCommand() *internal.Command {
	raw := &internal.RawFlag{}
	c := &internal.Command{
		Name: "dir-replay",
		Desc: "Replay the last message of the directory-scoped conversation",
		HelpText: `dir-replay. Prints the last message from the conversation bound to the
current directory.

Examples:
  clai dre
  clai -r dre`,
		Register: raw.Register,
		Raw:      raw,
	}
	c.OnSetup = func(_ context.Context, c *internal.Command) error {
		if _, err := internal.PrepTheme(); err != nil {
			return err
		}
		c.SetQuerier(&dirscopeReplayQuerier{raw: raw.Value()})
		return nil
	}
	return c
}

// dirscopeReplayQuerier implements models.Querier for the dir-replay
// command. It prints the last message from the directory-scoped
// conversation bound to the current working directory.
type dirscopeReplayQuerier struct {
	raw bool
}

func (q dirscopeReplayQuerier) Query(ctx context.Context) error {
	_ = ctx
	if err := Replay(q.raw, true); err != nil {
		return fmt.Errorf("dir-replay: %w", err)
	}
	return nil
}

func (q dirscopeReplayQuerier) SuppressCompletionNotification() bool {
	return q.raw
}

var _ models.Querier = (*dirscopeReplayQuerier)(nil)
