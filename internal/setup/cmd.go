package setup

import (
	"context"
	"fmt"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
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
	c.OnSetup = func(_ context.Context, c *internal.Command) error {
		if args := c.Args(); len(args) > 1 {
			Input = utils.NewMacroReader(args[1:])
		}
		_, ann, err := ConfigRunPrep(true)
		announcements = ann
		return err
	}
	c.OnRun = func(_ context.Context, _ *internal.Command) error {
		// Announce upgrades before the wizard starts, so the user sees what
		// the united migration changed while the configs were upgraded. The
		// wizard's TUI redraws by clearing its own frame plus one line above
		// the header (table ClearTermTo clears upTo+1 lines), so the
		// announcement block ends with a blank separator line that absorbs
		// that overshoot; without it the first redraw would wipe the
		// announcement. Printed even when InitCmd fails, which reports exit
		// as table.ErrUserInitiatedExit.
		for _, msg := range announcements {
			ancli.PrintOK(msg + "\n")
		}
		if len(announcements) > 0 {
			fmt.Println()
		}
		if err := InitCmd(); err != nil {
			return fmt.Errorf("failed to run setup: %w", err)
		}
		return nil
	}
	return c
}
