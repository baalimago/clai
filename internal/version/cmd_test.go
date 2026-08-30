package version

import (
	"context"
	"strings"
	"testing"

	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func Test_Command(t *testing.T) {
	c := Command()
	if c.Describe() == "" || !strings.Contains(c.Help(), "clai version") {
		t.Fatalf("describe/help incomplete: %q / %q", c.Describe(), c.Help())
	}
	out := testboil.CaptureStdout(t, func(t *testing.T) {
		if err := c.Run(context.Background()); err != nil {
			t.Fatalf("Run: %v", err)
		}
	})
	if !strings.Contains(out, "version: ") {
		t.Fatalf("expected version line, got: %q", out)
	}
}

func Test_Print_buildVersionWins(t *testing.T) {
	old := BuildVersion
	BuildVersion = "v9.9.9-test"
	t.Cleanup(func() { BuildVersion = old })

	out := testboil.CaptureStdout(t, func(t *testing.T) {
		if err := Print(); err != nil {
			t.Fatalf("Print: %v", err)
		}
	})
	if !strings.Contains(out, "version: v9.9.9-test") {
		t.Fatalf("expected stamped version, got: %q", out)
	}
}
