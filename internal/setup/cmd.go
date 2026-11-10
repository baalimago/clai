package setup

import (
	"context"
	"fmt"

	"github.com/baalimago/clai/internal/runenv"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/utils"
)

// Command builds the setup command: the interactive configuration wizard.
func Command() *internal.Command {
	c := &internal.Command{
		Name: "setup",
		Desc: "Setup the configuration files",
		HelpText: `setup. Starts the interactive configuration wizard.

Examples:
  clai s
  clai -n s 1 q   # macro-drive: open model files, then quit`,
	}
	nonInteractive := &internal.NonInteractiveFlag{}
	c.Register = nonInteractive.Register
	c.NonInteractive = nonInteractive
	var announcements []string
	c.OnSetup = func(ctx context.Context, c *internal.Command) error {
		if args := c.Args(); len(args) > 1 {
			Input = utils.NewMacroReaderFrom(args[1:], runenv.From(ctx).Stdin())
		}
		_, ann, err := ConfigRunPrep(ctx, true)
		announcements = ann
		return err
	}
	c.OnRun = func(ctx context.Context, _ *internal.Command) error {
		// Announce upgrades before the wizard starts, so the user sees what
		// the united migration changed while the configs were upgraded. The
		// wizard's TUI redraws by clearing its own frame plus one line above
		// the header (table ClearTermTo clears upTo+1 lines), so the
		// announcement block ends with a blank separator line that absorbs
		// that overshoot; without it the first redraw would wipe the
		// announcement. Printed even when InitCmd fails, which reports exit
		// as table.ErrUserInitiatedExit.
		for _, msg := range announcements {
			runenv.Noticef(ctx, runenv.SubsystemConfig, "%s", msg)
		}
		if len(announcements) > 0 {
			fmt.Fprintln(runenv.From(ctx).Stderr())
		}
		if err := InitCmd(ctx); err != nil {
			return diag.Errorf("failed to run setup: %w", err)
		}
		return nil
	}
	return c
}
