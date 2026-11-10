package confdir

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/runenv"
)

func Test_Command(t *testing.T) {
	confDir := t.TempDir()
	t.Setenv("CLAI_CONFIG_DIR", confDir)

	c := Command()
	if c.Describe() == "" || !strings.Contains(c.Help(), "confdir") {
		t.Fatalf("describe/help incomplete: %q / %q", c.Describe(), c.Help())
	}
	if err := c.Flagset().Parse(nil); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := c.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	var buf strings.Builder
	ctx := runenv.With(context.Background(), runenv.Text(strings.NewReader(""), &buf, io.Discard))
	if err := c.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, confDir) {
		t.Fatalf("expected config dir %q, got: %q", confDir, out)
	}
}

func Test_Print_unknownSubpathErrors(t *testing.T) {
	t.Setenv("CLAI_CONFIG_DIR", t.TempDir())
	if err := Print(context.Background(), []string{"confdir", "not-a-registered-subpath"}); err == nil {
		t.Fatal("expected error for unregistered subpath")
	}
}
