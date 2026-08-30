package text

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func Test_QueryCommand(t *testing.T) {
	t.Run("wires prep and the real querier path with the mock vendor", func(t *testing.T) {
		confDir := t.TempDir()
		t.Setenv("CLAI_CONFIG_DIR", confDir)
		t.Setenv("HOME", t.TempDir())
		deps := QueryCommandDeps{
			ConfigPrep: func() (string, error) { return confDir, nil },
		}
		c := QueryCommand(deps)
		if c.Describe() == "" || !strings.Contains(c.Help(), "query <text>") {
			t.Fatalf("describe/help incomplete: %q / %q", c.Describe(), c.Help())
		}
		for _, name := range []string{"cm", "t", "g", "re", "dre", "s", "rf", "asc", "r"} {
			if c.Flagset().Lookup(name) == nil {
				t.Fatalf("expected flag %q registered", name)
			}
		}
		if err := c.Flagset().Parse([]string{"-cm", "test", "hello", "there"}); err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if err := c.Setup(context.Background()); err != nil {
			t.Fatalf("Setup: %v", err)
		}
	})

	t.Run("prep error propagates", func(t *testing.T) {
		wantErr := errors.New("prep boom")
		c := QueryCommand(QueryCommandDeps{ConfigPrep: func() (string, error) { return "", wantErr }})
		if err := c.Flagset().Parse(nil); err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if err := c.Setup(context.Background()); !errors.Is(err, wantErr) {
			t.Fatalf("expected prep error, got: %v", err)
		}
	})
}
