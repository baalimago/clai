package cli

import (
	"context"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"text/tabwriter"
	"text/template"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/go_away_boilerplate/pkg/cmd"
)

var helpFlags = []string{"-h", "--help", "-help"}

// helpAsCommand rewrites a first-argument help flag into the help command,
// and `help <command> ...` into `<command> ... -h`, so every way of asking
// for help shares one dispatch path.
func helpAsCommand(args []string) []string {
	if len(args) == 0 {
		return args
	}
	if slices.Contains(helpFlags, args[0]) {
		return append([]string{"help"}, args[1:]...)
	}
	if args[0] == "help" && len(args) > 1 {
		return append(slices.Clone(args[1:]), "-h")
	}
	return args
}

// usageText is what usageTemplate shows; write fills Commands.
type usageText struct {
	Title, Summary, Synopsis, Commands, Subsystems, ConfigDir, CacheDir string
}

// write prints the usage with the table of cmds and the completion command.
func (u usageText) write(w io.Writer, cmds map[string]cmd.Command) error {
	t, err := template.New("usage").Parse(usageTemplate)
	if err != nil {
		return diag.Errorf("parse the usage template: %w", err)
	}
	u.Commands = commandTable(withCompletionCommand(cmds))
	if err := t.Execute(w, u); err != nil {
		return diag.Errorf("print usage: %w", err)
	}
	return nil
}

// withHelp adds the help command, which prints usage with the table of
// cmds, itself and the completion command included.
func withHelp(cmds map[string]cmd.Command, usage usageText) map[string]cmd.Command {
	c := &internal.Command{
		Name: "help",
		Desc: "Print this usage; 'clai help <command>' is that command's -h",
		HelpText: `help. Prints clai's usage and command table to stdout.

Examples:
  clai help
  clai help query`,
	}
	cmds["help"] = c
	c.OnRun = func(ctx context.Context, _ *internal.Command) error {
		return usage.write(runenv.From(ctx).Stdout(), cmds)
	}
	return cmds
}

func commandTable(cmds map[string]cmd.Command) string {
	var b strings.Builder
	w := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	for _, name := range slices.Sorted(maps.Keys(cmds)) {
		if cmd.IsHidden(cmds[name]) {
			continue
		}
		fmt.Fprintf(w, "\t%v\t%v\n", name, cmds[name].Describe())
	}
	w.Flush()
	return b.String()
}

// subsystemTable is the usage's table of subsystems: the name LOG_LEVEL
// takes and what the subsystem reports.
func subsystemTable(subsystems []runenv.Subsystem) string {
	var b strings.Builder
	w := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	for _, subsystem := range subsystems {
		fmt.Fprintf(w, "\t%v\t%v\n", subsystem.Name, subsystem.Purpose)
	}
	w.Flush()
	return b.String()
}
