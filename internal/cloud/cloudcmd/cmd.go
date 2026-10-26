// Package cloudcmd is `clai cloud`: the clai.chat account (login, logout,
// whoami) and git-style sync of the config and conversation stores
// (status, diff, pull, commit, resolve, log). Nothing syncs on its own.
// See architecture/cloud.md.
package cloudcmd

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/clai/internal/cloud"
	"github.com/baalimago/clai/internal/cloud/sync"
	"github.com/baalimago/go_away_boilerplate/pkg/cmd"
)

// ExitDecisionNeeded is the exit status of a pull or commit that stopped
// for a decision: conflicts left, a commit behind the server, or a first
// pull with nobody to ask.
const ExitDecisionNeeded = 4

// Deps carries what the cloud command needs from the composition root, and
// the seams tests replace.
type Deps struct {
	ConfDir func() (string, error)
	Hooks   sync.Hooks
	Out     io.Writer
	In      io.Reader
	// Interactive is whether a person can answer a prompt on In.
	Interactive func() bool
	Now         func() time.Time
	// Sleep waits between device-login polls.
	Sleep func(ctx context.Context, d time.Duration) error
	// Hostname names the device on the account page.
	Hostname func() (string, error)
	// SetExit asks the entry point for an exit status other than 0 or 1.
	SetExit func(code int)
}

func (d Deps) withDefaults() Deps {
	if d.Out == nil {
		d.Out = os.Stdout
	}
	if d.In == nil {
		d.In = os.Stdin
	}
	if d.Interactive == nil {
		d.Interactive = stdinIsTerminal
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Sleep == nil {
		d.Sleep = func(ctx context.Context, dur time.Duration) error {
			t := time.NewTimer(dur)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		}
	}
	if d.Hostname == nil {
		d.Hostname = os.Hostname
	}
	if d.SetExit == nil {
		d.SetExit = func(int) {}
	}
	return d
}

func stdinIsTerminal() bool {
	info, err := os.Stdin.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// UsageError is a cloud subcommand given the wrong arguments.
type UsageError struct{ Msg string }

func (e *UsageError) Error() string { return e.Msg }

const helpText = `cloud <subcommand>. Your clai.chat account, and git-style sync of your
config and conversations. Nothing syncs on its own: only these commands
talk to clai.chat, and clai works the same without an account.

Account
  login [-url URL] [-name NAME]   connect this machine (approve in the browser)
  logout                          disconnect it and forget its key
  whoami                          account, device, storage

Sync
  status [store]                  local changes, and what the server has
  diff [path...] [-remote]        your changes; -remote for the server's
  pull [store] [-theirs|-mine]    bring down server changes
  commit [-m msg] [-imports] [store|path...]   upload your changes
  resolve <path> [-mine|-theirs]  settle a conflict
  log [store] [-limit N]          recent commits

Stores: config (profiles, MCP servers, shell contexts, skills, mode configs,
theme) and conversations. 1 GB free, then $1/GB a month.
A config file that holds a key is never committed: status names it.
Conflicts keep your file; the server's copy goes to <config>/cloud/theirs/.
-r prints JSON. Exit 4 means a decision is needed: conflicts, a commit
behind the server, or a first pull with no -theirs/-mine and no terminal.
CLAI_CLOUD_TOKEN and CLAI_CLOUD_URL stand in for a login, for CI.

Examples:
  clai cloud login
  clai cloud status
  clai cloud commit -m "new review profile"
  clai cloud pull conversations -theirs -r`

// Command builds the cloud command tree.
func Command(deps Deps) *internal.Command {
	deps = deps.withDefaults()
	r := &runner{deps: deps}
	c := &internal.Command{Name: "cloud", Desc: "Your clai.chat account and sync (optional)", HelpText: helpText}
	c.OnRun = func(_ context.Context, c *internal.Command) error {
		_, err := fmt.Fprintln(deps.Out, c.Help())
		return err
	}
	sub := func(name, desc, help string, register func(fs *flag.FlagSet), raw *internal.RawFlag, run func(ctx context.Context, args []string) error) *internal.Command {
		s := &internal.Command{Name: name, Desc: desc, HelpText: help, Raw: raw}
		s.Register = func(fs *flag.FlagSet) {
			if raw != nil {
				raw.Register(fs)
			}
			if register != nil {
				register(fs)
			}
		}
		s.OnRun = func(ctx context.Context, s *internal.Command) error {
			args, err := positionals(s.Flagset(), s.Args()[1:])
			if err != nil {
				return &UsageError{Msg: err.Error()}
			}
			return run(ctx, args)
		}
		return s
	}

	var loginURL, loginName string
	var whoamiRaw, statusRaw, pullRaw, commitRaw, logRaw internal.RawFlag
	var statusImports, diffRemote, pullTheirs, pullMine, commitImports, resolveMine, resolveTheirs bool
	var commitMsg string
	logN := 20
	c.Subs = map[string]cmd.Command{
		"login": sub("login", "Connect this machine to your clai.chat account",
			"cloud login. Prints a code to approve on clai.chat and waits for it. Login syncs nothing.",
			func(fs *flag.FlagSet) {
				fs.StringVar(&loginURL, "url", "", "the service to log in to (default "+cloud.DefaultURL+" or CLAI_CLOUD_URL)")
				fs.StringVar(&loginName, "name", "", "this device's name on the account page (default: the hostname)")
			}, nil, func(ctx context.Context, _ []string) error { return r.login(ctx, loginURL, loginName) }),
		"logout": sub("logout", "Disconnect this machine and forget its key",
			"cloud logout. Revokes this machine's key and forgets its sync state. Local files stay.",
			nil, nil, func(ctx context.Context, _ []string) error { return r.logout(ctx) }),
		"whoami": sub("whoami", "Show the account, this device and storage", "cloud whoami.",
			nil, &whoamiRaw, func(ctx context.Context, _ []string) error { return r.whoami(ctx, whoamiRaw.Value()) }),
		"status": sub("status", "Show local changes and what the server has",
			"cloud status [store]. The local part works offline.",
			func(fs *flag.FlagSet) {
				fs.BoolVar(&statusImports, "imports", false, "count conversations imported from other tools as changes")
			}, &statusRaw, func(ctx context.Context, args []string) error {
				return r.status(ctx, args, statusImports, statusRaw.Value())
			}),
		"diff": sub("diff", "Show your changes, or the server's",
			"cloud diff [path...]. Your changes against the base; both sides of a conflict.",
			func(fs *flag.FlagSet) {
				fs.BoolVar(&diffRemote, "remote", false, "compare against the server's current copy")
			}, nil, func(ctx context.Context, args []string) error { return r.diff(ctx, args, diffRemote) }),
		"pull": sub("pull", "Bring down server changes",
			"cloud pull [store]. Files you did not change update in place. A file both sides changed\nstays as it is, and the server's copy goes to cloud/theirs/<path>.",
			func(fs *flag.FlagSet) {
				fs.BoolVar(&pullTheirs, "theirs", false, "take the server's copy of files both sides changed (yours go to cloud/trash/)")
				fs.BoolVar(&pullMine, "mine", false, "keep this machine's copy of files both sides changed, to commit")
			}, &pullRaw, func(ctx context.Context, args []string) error {
				return r.pull(ctx, args, pullTheirs, pullMine, pullRaw.Value())
			}),
		"commit": sub("commit", "Upload your changes",
			"cloud commit [-m msg] [store|path...]. One commit per store. Refused while conflicts are open,\nand when the server changed a file since your last pull.",
			func(fs *flag.FlagSet) {
				fs.StringVar(&commitMsg, "m", "", "the commit message")
				fs.BoolVar(&commitImports, "imports", false, "include the conversations clai imports from other tools")
			}, &commitRaw, func(ctx context.Context, args []string) error {
				return r.commit(ctx, args, commitMsg, commitImports, commitRaw.Value())
			}),
		"resolve": sub("resolve", "Settle a conflict",
			"cloud resolve <path> [-mine|-theirs]. Without a flag, keeps your file as you edited it.",
			func(fs *flag.FlagSet) {
				fs.BoolVar(&resolveMine, "mine", false, "keep this machine's file")
				fs.BoolVar(&resolveTheirs, "theirs", false, "take the server's file (yours goes to cloud/trash/)")
			}, nil, func(_ context.Context, args []string) error { return r.resolve(args, resolveMine, resolveTheirs) }),
		"log": sub("log", "Show recent commits", "cloud log [store].",
			func(fs *flag.FlagSet) {
				fs.IntVar(&logN, "limit", 20, "how many commits per store")
			}, &logRaw, func(ctx context.Context, args []string) error { return r.log(ctx, args, logN, logRaw.Value()) }),
	}
	return c
}

// positionals re-parses args so flags may follow positional arguments, as
// in `clai cloud pull conversations -theirs`.
func positionals(fs *flag.FlagSet, args []string) ([]string, error) {
	var out []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			break
		}
		out = append(out, args[0])
		args = args[1:]
	}
	return out, nil
}
