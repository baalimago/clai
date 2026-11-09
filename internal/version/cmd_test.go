package version

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/runenv"
)

func Test_Command(t *testing.T) {
	c := Command()
	if c.Describe() == "" || !strings.Contains(c.Help(), "clai version") {
		t.Fatalf("describe/help incomplete: %q / %q", c.Describe(), c.Help())
	}
	var buf strings.Builder
	ctx := runenv.With(context.Background(), runenv.Text(strings.NewReader(""), &buf, io.Discard))
	if err := c.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "version: ") {
		t.Fatalf("expected version line, got: %q", out)
	}
}

func Test_Print_buildVersionWins(t *testing.T) {
	old := BuildVersion
	BuildVersion = "v9.9.9-test"
	t.Cleanup(func() { BuildVersion = old })

	var buf strings.Builder
	ctx := runenv.With(context.Background(), runenv.Text(strings.NewReader(""), &buf, io.Discard))
	if err := Print(ctx); err != nil {
		t.Fatalf("Print: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "version: v9.9.9-test") {
		t.Fatalf("expected stamped version, got: %q", out)
	}
}
