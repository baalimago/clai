// Package internal holds clai's command adapter and flag machinery: the
// bridge between go_away_boilerplate/pkg/cmd dispatch and clai's
// per-command flag groups. It sits at the internal root because every
// subpackage depends on it: domain packages (text, chat, photo, ...)
// define their commands with it, and the composition root (main.go)
// injects config-prep and querier factories. It is a leaf — it must
// never import a domain package.
package internal

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/cmd"
)

type completionNotificationSuppressor interface {
	SuppressCompletionNotification() bool
}

func triggerCompletionNotification(completed any) {
	if suppressor, ok := completed.(completionNotificationSuppressor); ok && suppressor.SuppressCompletionNotification() {
		return
	}
	if !utils.NotificationBellEnabled() {
		return
	}
	fmt.Fprint(os.Stdout, "\a")
}

// Command adapts one clai command to the cmd.Command interface. Each
// command owns its flag values (shared groups from this package plus any
// domain-specific flags) and registers them in Register; subcommands share
// values with their parent by closing over the same structs. A command
// with Subs becomes a cmd.Subcommander tree; each level parses its own
// flagset (D11).
type Command struct {
	Name     string
	Desc     string
	HelpText string
	Register func(fs *flag.FlagSet)
	OnSetup  func(ctx context.Context, c *Command) error
	OnRun    func(ctx context.Context, c *Command) error
	Subs     map[string]cmd.Command

	// Raw and NonInteractive point at the command's flag groups (nil when
	// the command doesn't own them) so Setup can derive the session
	// globals in one place.
	Raw            *RawFlag
	NonInteractive *NonInteractiveFlag

	// Optional completion hooks; both must stay side-effect-free apart
	// from lazy, memoized data loading.
	CompleteFlagValueFn func(flagName, partial string) []cmd.CompletionItem
	CompleteArgsFn      func(args []string, partial string) []cmd.CompletionItem

	fs      *flag.FlagSet
	args    []string
	querier models.Querier
}

// Args returns the positional args, [0] being the command name; valid once
// Setup has run.
func (c *Command) Args() []string { return c.args }

// SetArgs replaces the positional args, for setups that reconstruct a
// legacy arg shape before delegating.
func (c *Command) SetArgs(args []string) { c.args = args }

// SetQuerier hands the adapter the querier its default Run should execute.
func (c *Command) SetQuerier(q models.Querier) { c.querier = q }

func (c *Command) Flagset() *flag.FlagSet {
	if c.fs == nil {
		c.fs = flag.NewFlagSet(c.Name, flag.ContinueOnError)
		if c.Register != nil {
			c.Register(c.fs)
		}
	}
	return c.fs
}

func (c *Command) Subcommands() map[string]cmd.Command { return c.Subs }

func (c *Command) CompleteFlagValue(flagName, partial string) []cmd.CompletionItem {
	if c.CompleteFlagValueFn == nil {
		return nil
	}
	return c.CompleteFlagValueFn(flagName, partial)
}

func (c *Command) CompleteArgs(args []string, partial string) []cmd.CompletionItem {
	if c.CompleteArgsFn == nil {
		return nil
	}
	return c.CompleteArgsFn(args, partial)
}

func (c *Command) Describe() string { return c.Desc }

// Help returns the command description plus a flag list derived from the
// command's own flagset.
func (c *Command) Help() string {
	fs := c.Flagset()
	var flagList bytes.Buffer
	fs.SetOutput(&flagList)
	fs.PrintDefaults()
	fs.SetOutput(io.Discard)
	if flagList.Len() == 0 {
		return c.HelpText
	}
	return c.HelpText + "\n\nFlags:\n" + flagList.String()
}

func (c *Command) Setup(ctx context.Context) error {
	c.args = append([]string{c.Name}, c.fs.Args()...)

	// Raw (machine-readable) runs must not mutate the user's configs: the
	// loaders fill missing fields in memory but leave the files untouched.
	utils.ReadonlyConfig = c.Raw != nil && c.Raw.Value()
	utils.NoCreateConfig = false
	utils.Live = !(c.NonInteractive != nil && c.NonInteractive.Value())
	if c.OnSetup != nil {
		return c.OnSetup(ctx, c)
	}
	return nil
}

func (c *Command) Run(ctx context.Context) error {
	if c.OnRun != nil {
		return c.OnRun(ctx, c)
	}
	if err := c.querier.Query(ctx); err != nil {
		return err
	}
	triggerCompletionNotification(c.querier)
	return nil
}
