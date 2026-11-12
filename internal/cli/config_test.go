package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

// usageCommands is the usage's Commands block: the lines between "Commands:"
// and the blank line that closes it. The subsystem table further down names a
// `profiles` subsystem, and a subsystem is not a command.
func usageCommands(usage string) string {
	const head = "\nCommands:\n"
	_, after, ok := strings.Cut(usage, head)
	if !ok {
		return ""
	}
	rest := after
	if end := strings.Index(rest, "\n\n"); end >= 0 {
		rest = rest[:end]
	}
	return rest
}

// TestHiddenAliasesAbsent proves clauses 11 and 12 at the root: the help table
// and the completion word list show `config` but none of the pre-existing
// spellings this part hides, while a command with no Hidden method stays
// listed.
func TestHiddenAliasesAbsent(t *testing.T) {
	isolateEnv(t)

	var status int
	help := testboil.CaptureStdout(t, func(t *testing.T) {
		status = Run([]string{"help"}, DefaultDeps())
	})
	if status != 0 {
		t.Fatalf("Run(help) = %d, want 0", status)
	}
	table := usageCommands(help)
	for _, visible := range []string{"\n  config ", "\n  query|q "} {
		if !strings.Contains("\n"+table, visible) {
			t.Fatalf("help lacks %q:\n%s", visible, help)
		}
	}
	for _, hidden := range []string{"\n  profiles ", "\n  confdir ", "\n  setup|s ", "\n  s "} {
		if strings.Contains("\n"+table, hidden) {
			t.Fatalf("help lists hidden spelling %q:\n%s", hidden, help)
		}
	}

	var completeStatus int
	completion := testboil.CaptureStdout(t, func(t *testing.T) {
		completeStatus = Run([]string{"__complete", "clai", ""}, DefaultDeps())
	})
	if completeStatus != 0 {
		t.Fatalf("Run(__complete) = %d, want 0", completeStatus)
	}
	if !strings.Contains(completion, "config\tplain") {
		t.Fatalf("completion lacks config:\n%s", completion)
	}
	lines := strings.Split(strings.TrimSuffix(completion, "\n"), "\n")
	for _, hidden := range []string{"profiles\tplain", "confdir\tplain", "setup\tplain", "s\tplain"} {
		if slices.Contains(lines, hidden) {
			t.Fatalf("completion offers hidden spelling %q:\n%s", hidden, completion)
		}
	}
}
