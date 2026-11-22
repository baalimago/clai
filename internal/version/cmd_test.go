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

func Test_Resolve(t *testing.T) {
	old := BuildVersion
	t.Cleanup(func() { BuildVersion = old })

	BuildVersion = "v1.14.0"
	if got, err := Resolve(); err != nil || got != "v1.14.0" {
		t.Fatalf("Resolve() = %q, %v; want the stamped version", got, err)
	}

	BuildVersion = ""
	got, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve() without a stamp: %v", err)
	}
	if got == "v1.14.0" {
		t.Fatalf("Resolve() kept the old stamp")
	}
}

func Test_IsRelease(t *testing.T) {
	for v, want := range map[string]bool{
		"v1.14.0":                               true,
		"v0.0.1":                                true,
		"v10.2.33":                              true,
		"":                                      false,
		"(devel)":                               false,
		"1.14.0":                                false,
		"v1.14":                                 false,
		"v1.14.0-rc1":                           false,
		"v1.14.0+dirty":                         false,
		"v1.13.3-0.20261118133244-a7724a531a41": false,
		"unstable-a7724a5":                      false,
		"v01.2.3":                               false,
	} {
		if got := IsRelease(v); got != want {
			t.Errorf("IsRelease(%q) = %v, want %v", v, got, want)
		}
	}
}
