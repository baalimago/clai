package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

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
	for _, visible := range []string{"\n  config ", "\n  query|q "} {
		if !strings.Contains(help, visible) {
			t.Fatalf("help lacks %q:\n%s", visible, help)
		}
	}
	for _, hidden := range []string{"\n  profiles ", "\n  confdir ", "\n  setup|s ", "\n  s "} {
		if strings.Contains(help, hidden) {
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
