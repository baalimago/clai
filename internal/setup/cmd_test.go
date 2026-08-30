package setup

import (
	"context"
	"strings"
	"testing"
)

func Test_Command_setupPhase(t *testing.T) {
	t.Run("prep runs against the config dir and macro args feed Input", func(t *testing.T) {
		t.Setenv("CLAI_CONFIG_DIR", t.TempDir())
		c := Command()
		if c.Describe() == "" || !strings.Contains(c.Help(), "configuration wizard") {
			t.Fatalf("describe/help incomplete: %q / %q", c.Describe(), c.Help())
		}
		old := Input
		t.Cleanup(func() { Input = old })
		if err := c.Flagset().Parse([]string{"1", "q"}); err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if err := c.Setup(context.Background()); err != nil {
			t.Fatalf("Setup: %v", err)
		}
		if Input == old {
			t.Fatal("expected macro args to replace the wizard input reader")
		}
	})
}

func Test_ConfigRunPrep(t *testing.T) {
	confDir := t.TempDir()
	t.Setenv("CLAI_CONFIG_DIR", confDir)
	gotDir, announcements, err := ConfigRunPrep(true)
	if err != nil {
		t.Fatalf("ConfigRunPrep: %v", err)
	}
	if gotDir != confDir {
		t.Fatalf("conf dir: got %q want %q", gotDir, confDir)
	}
	// A fresh dir has no configs to upgrade, so nothing is announced.
	if len(announcements) != 0 {
		t.Fatalf("expected no announcements on empty dir, got %v", announcements)
	}
}
