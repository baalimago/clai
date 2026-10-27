package cli

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/baalimago/clai/internal"
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

// withHelp adds the help command, which prints usage with the table of
// cmds, itself and the completion command included.
func withHelp(cmds map[string]cmd.Command, usage string) map[string]cmd.Command {
	c := &internal.Command{
		Name: "help",
		Desc: "Print this usage; 'clai help <command>' is that command's -h",
		HelpText: `help. Prints clai's usage and command table to stdout.

Examples:
  clai help
  clai help query`,
	}
	cmds["help"] = c
	c.OnRun = func(context.Context, *internal.Command) error {
		all := maps.Clone(cmds)
		if _, ok := all["completion"]; !ok {
			all["completion"] = cmd.NewCompletionCommand("clai")
		}
		_, err := fmt.Printf(usage, commandTable(all))
		if err != nil {
			return fmt.Errorf("print usage: %w", err)
		}
		return nil
	}
	return cmds
}

func commandTable(cmds map[string]cmd.Command) string {
	var b strings.Builder
	w := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	for _, name := range slices.Sorted(maps.Keys(cmds)) {
		fmt.Fprintf(w, "\t%v\t%v\n", name, cmds[name].Describe())
	}
	w.Flush()
	return b.String()
}
