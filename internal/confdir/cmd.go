// Package confdir prints the clai config dir or registered subpaths of it.
package confdir

import (
	"context"
	"fmt"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/internal/utils"
)

// Command builds the confdir command.
func Command() *internal.Command {
	c := &internal.Command{
		Name: "confdir",
		Desc: "Print clai config dir or a registered config subpath",
		HelpText: `confdir [subpath ...]. Prints the config dir, or a registered subpath
within it.

Examples:
  clai confdir
  clai confdir mcpServers`,
	}
	c.OnRun = func(ctx context.Context, c *internal.Command) error {
		return Print(ctx, c.Args())
	}
	return c
}

// Print resolves and prints the config dir, or a registered subpath of it
// when args carry one, to the run's stdout.
func Print(ctx context.Context, args []string) error {
	configDir, err := utils.GetClaiConfigDir()
	if err != nil {
		return diag.Errorf("failed to get clai config dir: %w", err)
	}

	resolved, err := utils.ResolveConfigDirPath(configDir, args[1:])
	if err != nil {
		return diag.Errorf("failed to resolve config dir path: %w", err)
	}

	if _, err := fmt.Fprintln(runenv.From(ctx).Stdout(), resolved); err != nil {
		return diag.Errorf("print config dir: %w", err)
	}
	return nil
}
