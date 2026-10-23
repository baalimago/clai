package utils

import (
	"slices"
	"strings"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestActivityViewport_HeaderIsPinnedFirstRow(t *testing.T) {
	viewport := NewActivityViewport(60, 4, 24)
	viewport.SetHeader([]HeaderLine{testHeaderLine("▸ loop 2/10 · tests pass")})
	viewport.AppendReasoning("inspect the repository")

	rows := viewport.Rows()
	if len(rows) == 0 || rows[0] != "▸ loop 2/10 · tests pass" {
		t.Fatalf("first row = %q, want the pinned title", rows)
	}

	viewport.AppendTool(pub_models.Call{Name: "ls"}, "one\ntwo\nthree", 10)
	rows = viewport.Rows()
	if len(rows) > 4 {
		t.Fatalf("rows = %d, want at most the effective height of 4", len(rows))
	}
	if rows[0] != "▸ loop 2/10 · tests pass" {
		t.Fatalf("first row = %q, want the title to survive height trimming", rows)
	}
}

func TestActivityViewport_HeaderSurvivesBlockEviction(t *testing.T) {
	viewport := NewActivityViewport(60, 30, 30)
	viewport.SetHeader([]HeaderLine{testHeaderLine("▸ loop 7/10 · still working")})
	for i := range maxActivityBlocks + 4 {
		viewport.AppendTool(pub_models.Call{Name: "ls", ID: string(rune('a' + i))}, "out", 2)
	}
	rows := viewport.Rows()
	if len(rows) == 0 || rows[0] != "▸ loop 7/10 · still working" {
		t.Fatalf("first row = %q, want the title to survive block eviction", rows)
	}
}

func TestActivityViewport_HeaderSanitizesToOneRow(t *testing.T) {
	viewport := NewActivityViewport(40, 10, 24)
	viewport.SetHeader([]HeaderLine{{Spans: []HeaderSpan{{Text: "first\nsecond"}, {Text: "\x1b[31mthird", Color: "red"}}}})
	rows := viewport.Rows()
	if len(rows) != 1 {
		t.Fatalf("rows = %q, want one title row", rows)
	}
	if rows[0] != "first secondthird" {
		t.Fatalf("title = %q, want a sanitized single row", rows[0])
	}
}

func TestActivityViewport_RenderDefersHeaderUntilBodyExists(t *testing.T) {
	viewport := NewActivityViewport(60, 10, 24)
	viewport.SetHeader([]HeaderLine{testHeaderLine("▸ loop 1/4 · goal: ship")})

	var out strings.Builder
	if err := viewport.Render(&out); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("title-only Render printed %q, want nothing", out.String())
	}

	out.WriteString("startup log\n")
	viewport.AppendReasoning("inspect")
	if err := viewport.Render(&out); err != nil {
		t.Fatalf("Render with body: %v", err)
	}
	if index := strings.Index(out.String(), "▸ loop 1/4"); index < strings.Index(out.String(), "startup log") {
		t.Fatalf("title must follow unrelated output, got %q", out.String())
	}
}

func TestActivityViewport_RenderPinnedDrawsBeforeBody(t *testing.T) {
	viewport := NewActivityViewport(60, 10, 24)
	viewport.SetHeader([]HeaderLine{testHeaderLine("▸ loop 1/4 · goal: ship")})

	var out strings.Builder
	if err := viewport.RenderPinned(&out); err != nil {
		t.Fatalf("RenderPinned: %v", err)
	}
	if !strings.Contains(out.String(), "▸ loop 1/4") {
		t.Fatalf("RenderPinned printed %q, want the pinned title", out.String())
	}
}

func TestActivityViewport_EmptyHeaderRemovesRow(t *testing.T) {
	viewport := NewActivityViewport(40, 10, 24)
	viewport.SetHeader([]HeaderLine{testHeaderLine("▸ loop 2/10")})
	viewport.SetHeader(nil)
	viewport.AppendReasoning("inspect")

	rows := viewport.Rows()
	if len(rows) == 0 || rows[0] != "∴ thinking" {
		t.Fatalf("first row = %q, want the reasoning header after the title was removed", rows)
	}
}

func TestActivityViewport_CollapseToHeader(t *testing.T) {
	viewport := NewActivityViewport(60, 10, 24)
	viewport.SetHeader([]HeaderLine{testHeaderLine("▸ loop 2/10 · tests pass")})
	viewport.AppendReasoning("inspect the repository")
	viewport.AppendTool(pub_models.Call{Name: "ls"}, "one", 4)
	viewport.AppendText("working")

	viewport.CollapseToHeader()
	rows := viewport.Rows()
	if len(rows) != 1 || rows[0] != "▸ loop 2/10 · tests pass" {
		t.Fatalf("rows after collapse = %q, want only the title", rows)
	}
	if viewport.TextBlockActive() {
		t.Fatal("collapse must close the assistant-prose block")
	}
}

func TestActivityViewport_HeaderColorsTheMarkerApartFromThePhase(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	origTheme := globalTheme
	t.Cleanup(func() { globalTheme = origTheme })
	globalTheme = *defaultTheme()
	globalTheme.LoopTitle = "<LOOP>"

	viewport := NewActivityViewport(60, 10, 24)
	viewport.SetHeader([]HeaderLine{{Spans: []HeaderSpan{
		{Text: "▸ loop 2/10", Color: LoopTitleColor()},
		{Text: " - tests pass; docs remain", Color: LoopPhaseColor()},
	}}})
	var out strings.Builder
	if err := viewport.RenderPinned(&out); err != nil {
		t.Fatalf("RenderPinned: %v", err)
	}
	want := "<LOOP>▸ loop 2/10\u001b[0m" + LoopPhaseColor() + " - tests pass; docs remain\u001b[0m\n"
	if out.String() != want {
		t.Fatalf("header render = %q, want %q", out.String(), want)
	}
}

// testHeaderLine builds a single-colour header line for the viewport tests.
func testHeaderLine(text string) HeaderLine {
	return HeaderLine{Spans: []HeaderSpan{{Text: text}}}
}

func TestActivityViewport_HeaderWrapKeepsEachSpanColor(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	origTheme := globalTheme
	t.Cleanup(func() { globalTheme = origTheme })
	globalTheme = *defaultTheme()
	globalTheme.LoopTitle = "<LOOP>"
	globalTheme.Primary = "<PHASE>"

	viewport := NewActivityViewport(16, 10, 24)
	viewport.SetHeader([]HeaderLine{{Spans: []HeaderSpan{
		{Text: "▸ loop 2/10", Color: LoopTitleColor()},
		{Text: " - tests pass now", Color: LoopPhaseColor()},
	}}})
	var out strings.Builder
	if err := viewport.RenderPinned(&out); err != nil {
		t.Fatalf("RenderPinned: %v", err)
	}
	reset := "\u001b[0m"
	want := "<LOOP>▸ loop 2/10" + reset + "<PHASE> - te" + reset + "\n" +
		"<PHASE>sts pass now" + reset + "\n"
	if out.String() != want {
		t.Fatalf("wrapped header render = %q, want %q", out.String(), want)
	}
	if rows := viewport.Rows(); len(rows) != 2 {
		t.Fatalf("Rows() = %q, want the two wrapped rows", rows)
	}
}

func TestActivityViewport_HeaderWrapsAndCapsRows(t *testing.T) {
	viewport := NewActivityViewport(20, 10, 24)
	viewport.SetHeader([]HeaderLine{testHeaderLine("▸ loop 2/10 - " + strings.Repeat("word ", 20))})
	rows := viewport.Rows()
	if len(rows) != maxActivityHeaderRows {
		t.Fatalf("header rows = %d, want the %d-row cap: %q", len(rows), maxActivityHeaderRows, rows)
	}
	if !strings.HasPrefix(rows[0], "▸ loop 2/10 - ") {
		t.Fatalf("first row = %q, want the iteration marker and the phase on one row", rows[0])
	}
	for _, row := range rows {
		if got := terminalStringWidth(row); got > 20 {
			t.Fatalf("header row width = %d, want at most 20: %q", got, row)
		}
	}
	if !strings.HasSuffix(rows[len(rows)-1], "…") {
		t.Fatalf("clipped header must end with an ellipsis: %q", rows)
	}
}

func TestActivityViewport_HeaderSurvivesCollapseAndProseRemoval(t *testing.T) {
	viewport := NewActivityViewport(60, 10, 24)
	viewport.SetHeader([]HeaderLine{testHeaderLine("▸ loop 2/10 - tests pass")})
	viewport.AppendText("the answer")
	if got := viewport.RemoveTextBlock(); got != 2 {
		t.Fatalf("RemoveTextBlock() = %d, want the two prose rows only", got)
	}
	want := []string{"▸ loop 2/10 - tests pass"}
	if rows := viewport.Rows(); !slices.Equal(rows, want) {
		t.Fatalf("rows after prose removal = %q, want %q", rows, want)
	}

	viewport.AppendReasoning("inspect")
	viewport.CollapseToHeader()
	if rows := viewport.Rows(); !slices.Equal(rows, want) {
		t.Fatalf("rows after collapse = %q, want %q", rows, want)
	}
}

func TestActivityViewport_RemoveTextBlockIgnoresHeaderRow(t *testing.T) {
	viewport := NewActivityViewport(60, 10, 24)
	viewport.SetHeader([]HeaderLine{testHeaderLine("▸ loop 2/10 · tests pass")})
	viewport.AppendText("the answer")

	if got := viewport.RemoveTextBlock(); got != 2 {
		t.Fatalf("RemoveTextBlock() = %d, want the two prose rows only", got)
	}
	if rows := viewport.Rows(); len(rows) != 1 || rows[0] != "▸ loop 2/10 · tests pass" {
		t.Fatalf("rows after prose removal = %q, want only the title", rows)
	}
}
