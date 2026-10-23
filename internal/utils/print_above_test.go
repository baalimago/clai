package utils

import (
	"strings"
	"testing"
)

// TestActivityViewport_PrintAboveKeepsTheFrame pins that a line written while a
// frame is drawn lands above it and the frame is redrawn below, so the frame's
// cursor bookkeeping survives.
func TestActivityViewport_PrintAboveKeepsTheFrame(t *testing.T) {
	t.Setenv("NO_COLOR", "true")
	viewport := NewActivityViewport(20, 10, 24)
	viewport.SetHeader([]HeaderLine{testHeaderLine("header")})
	viewport.AppendReasoning("inspect")

	var out strings.Builder
	if err := viewport.Render(&out); err != nil {
		t.Fatalf("Render: %v", err)
	}
	before := out.Len()
	if err := viewport.PrintAbove(&out, "warning: budget"); err != nil {
		t.Fatalf("PrintAbove: %v", err)
	}
	want := "\x1b[3A\r\x1b[Jwarning: budget\nheader\n∴ thinking\n  inspect\n"
	if got := out.String()[before:]; got != want {
		t.Fatalf("PrintAbove wrote %q, want %q", got, want)
	}

	// The redrawn frame is still the drawn region: a following append must not
	// clear it and a collapse must clear exactly the frame rows.
	viewport.CollapseToHeader()
	if err := viewport.Render(&out); err != nil {
		t.Fatalf("Render after collapse: %v", err)
	}
	if got := out.String()[before+len(want):]; got != "\x1b[2A\r\x1b[J" {
		t.Fatalf("collapse wrote %q, want the two body rows cleared in place", got)
	}
}

// TestActivityViewport_PrintAboveWithoutADrawnFrame pins that nothing is redrawn
// when no frame owns the cursor yet.
func TestActivityViewport_PrintAboveWithoutADrawnFrame(t *testing.T) {
	t.Setenv("NO_COLOR", "true")
	viewport := NewActivityViewport(20, 10, 24)
	viewport.SetHeader([]HeaderLine{testHeaderLine("header")})

	var out strings.Builder
	if err := viewport.PrintAbove(&out, "warning: budget"); err != nil {
		t.Fatalf("PrintAbove: %v", err)
	}
	if got, want := out.String(), "warning: budget\n"; got != want {
		t.Fatalf("PrintAbove wrote %q, want %q", got, want)
	}
	if err := viewport.Render(&out); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got := out.Len(); got != len("warning: budget\n") {
		t.Fatalf("a header-only frame must not draw after a notice, output = %q", out.String())
	}
}

// TestActivityViewport_PrintAboveSanitizesAndTruncates pins the single-row
// contract of the notice.
func TestActivityViewport_PrintAboveSanitizesAndTruncates(t *testing.T) {
	t.Setenv("NO_COLOR", "true")
	viewport := NewActivityViewport(10, 10, 24)

	var out strings.Builder
	if err := viewport.PrintAbove(&out, "one\ntwo\x1b[31m and a long tail"); err != nil {
		t.Fatalf("PrintAbove: %v", err)
	}
	if got, want := out.String(), "one two a…\n"; got != want {
		t.Fatalf("PrintAbove wrote %q, want the sanitized and truncated row %q", got, want)
	}
}

// TestActivityViewport_PrintAboveWriteFailureReturnsError pins that a failed
// notice marks the viewport dirty so the next Render retries the whole frame.
func TestActivityViewport_PrintAboveWriteFailureReturnsError(t *testing.T) {
	t.Setenv("NO_COLOR", "true")
	viewport := NewActivityViewport(20, 10, 24)
	viewport.SetHeader([]HeaderLine{testHeaderLine("header")})
	viewport.AppendReasoning("inspect")
	if err := viewport.Render(&strings.Builder{}); err != nil {
		t.Fatalf("Render: %v", err)
	}

	err := viewport.PrintAbove(&failAfterWriter{}, "warning: budget")
	if err == nil || !strings.Contains(err.Error(), "print above activity viewport") {
		t.Fatalf("PrintAbove error = %v, want the write failure", err)
	}
	var retry strings.Builder
	if err := viewport.Render(&retry); err != nil {
		t.Fatalf("Render retry: %v", err)
	}
	if !strings.Contains(retry.String(), "\x1b[3A\r\x1b[J") {
		t.Fatalf("retry must redraw the whole frame, got %q", retry.String())
	}
}

// TestActivityViewport_PrintAboveEmptyLineIsNoop pins that an all-whitespace
// notice writes nothing.
func TestActivityViewport_PrintAboveEmptyLineIsNoop(t *testing.T) {
	viewport := NewActivityViewport(20, 10, 24)
	var out strings.Builder
	if err := viewport.PrintAbove(&out, "  \n\t "); err != nil {
		t.Fatalf("PrintAbove: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("PrintAbove wrote %q, want nothing", out.String())
	}
}
