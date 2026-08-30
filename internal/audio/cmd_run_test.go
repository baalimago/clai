package audio

import (
	"context"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func Test_Command_tree(t *testing.T) {
	deps := CommandDeps{
		ConfigPrep: func() (string, error) { return t.TempDir(), nil },
	}
	c := Command(deps)
	if c.Describe() == "" || !strings.Contains(c.Help(), "t|transcribe") {
		t.Fatalf("describe/help incomplete: %q / %q", c.Describe(), c.Help())
	}

	t.Run("no verb errors with namespace help", func(t *testing.T) {
		if err := c.Flagset().Parse(nil); err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if err := c.Setup(context.Background()); err != nil {
			t.Fatalf("Setup: %v", err)
		}
		stderr := testboil.CaptureStderr(t, func(t *testing.T) {
			if err := c.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "missing audio verb") {
				t.Fatalf("expected missing-verb error, got: %v", err)
			}
		})
		if !strings.Contains(stderr, "usage: clai audio") {
			t.Fatalf("expected namespace help on stderr, got: %q", stderr)
		}
	})

	t.Run("transcribe sub owns the audio flags and errors on a missing file", func(t *testing.T) {
		sub, ok := c.Subcommands()["transcribe|t"].(*internal.Command)
		if !ok {
			t.Fatal("transcribe sub missing")
		}
		if sub.Flagset().Lookup("af") == nil || sub.Flagset().Lookup("am") == nil {
			t.Fatal("transcribe must own the audio flag group")
		}
		if err := sub.Flagset().Parse([]string{"does-not-exist.wav"}); err != nil {
			t.Fatalf("Parse: %v", err)
		}
		err := sub.Setup(context.Background())
		if err == nil || !strings.Contains(err.Error(), "failed to find audio file") {
			t.Fatalf("expected missing-file error through the real setup path, got: %v", err)
		}
	})

	t.Run("help sub prints the namespace help", func(t *testing.T) {
		sub, ok := c.Subcommands()["help|h"].(*internal.Command)
		if !ok {
			t.Fatal("help sub missing")
		}
		out := testboil.CaptureStdout(t, func(t *testing.T) {
			if err := sub.Run(context.Background()); err != nil {
				t.Fatalf("Run: %v", err)
			}
		})
		if !strings.Contains(out, "usage: clai audio") {
			t.Fatalf("expected namespace help, got: %q", out)
		}
	})
}
