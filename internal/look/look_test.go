package look

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestHead_plainWithoutColor(t *testing.T) {
	if got := (Style{}).Head("tools", "a", "b"); got != "▸ tools · a · b" {
		t.Fatalf("Head = %q", got)
	}
	if got := (Style{}).Head("model"); got != "▸ model" {
		t.Fatalf("Head = %q", got)
	}
}

func TestHead_colorsOnlyTheMarkerAndDetails(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	got := (Style{Color: true}).Head("tools", "a")
	if !strings.Contains(got, "\x1b[") || !strings.Contains(got, " tools") {
		t.Fatalf("Head = %q, want colour codes around the marker", got)
	}
	if strings.Contains(got, "\x1b[0mtools") {
		t.Fatalf("title painted: %q", got)
	}
}

func TestGrid_alignsColumnsAndMarksNextSteps(t *testing.T) {
	got := Grid("  ", [][]string{{"a", "x", "end"}, {"long", "y"}, {"→ run", "why"}, {"solo"}}, Style{})
	want := []string{"  a       x   end", "  long    y", "  → run   why", "  solo"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("Grid =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestLinkAndPad(t *testing.T) {
	if got := (Style{}).Link("  ", "https://x/"); got != "  → https://x/" {
		t.Fatalf("Link = %q", got)
	}
	if got := Pad("ab", 4); got != "ab  " {
		t.Fatalf("Pad = %q", got)
	}
	if got := (Style{Color: true}).Paint("", "t"); got != "t" {
		t.Fatalf("Paint without a colour = %q", got)
	}
}

func TestFor_noColorOffATerminal(t *testing.T) {
	if For(&bytes.Buffer{}).Color {
		t.Fatal("a buffer is not a terminal")
	}
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if For(f).Color {
		t.Fatal("a regular file is not a terminal")
	}
}
