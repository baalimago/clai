package chat

import (
	"testing"

	"github.com/baalimago/clai/internal/testenv"
)

func TestNew_watchesTheWidthOfARealTerminal(t *testing.T) {
	confDir := newConfDir(t)
	master, slave := testenv.OpenPTY(t)
	defer master.Close()
	defer slave.Close()
	for _, tc := range []struct {
		args string
		want bool
	}{
		{"list", true},
		// Macro input answers the table; nothing is read from the terminal.
		{"list 0 q", false},
	} {
		t.Run(tc.args, func(t *testing.T) {
			ch, err := New(streamsContext(slave, slave, true, true), confDir, tc.args, "", false, nil)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if got := ch.openListInput != nil; got != tc.want {
				t.Fatalf("width watch wired = %v, want %v", got, tc.want)
			}
		})
	}
}
