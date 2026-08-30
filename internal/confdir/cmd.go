// Package confdir prints the clai config dir or registered subpaths of it.
package confdir

import (
	"context"
	"fmt"

	"github.com/baalimago/clai/internal"
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
	c.OnRun = func(_ context.Context, c *internal.Command) error {
		return Print(c.Args())
	}
	return c
}

// Print resolves and prints the config dir, or a registered subpath of it
// when args carry one.
func Print(args []string) error {
	configDir, err := utils.GetClaiConfigDir()
	if err != nil {
		return fmt.Errorf("failed to get clai config dir: %w", err)
	}

	resolved, err := utils.ResolveConfigDirPath(configDir, args[1:])
	if err != nil {
		return fmt.Errorf("failed to resolve config dir path: %w", err)
	}

	fmt.Println(resolved)
	return nil
}
