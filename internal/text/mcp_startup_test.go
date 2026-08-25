package text

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func Test_mcpStartupWindows_AppendLineKeepsFirstSeenOrder(t *testing.T) {
	w := newMcpStartupWindows()
	w.appendLine("notion", "n1", false)
	w.appendLine("linear", "l1", true)
	w.appendLine("notion", "n2", true)
	w.appendLine("intercom", "i1", false)

	want := []string{"notion", "linear", "intercom"}
	if len(w.order) != len(want) {
		t.Fatalf("order = %v, want %v", w.order, want)
	}
	for i, server := range want {
		if w.order[i] != server {
			t.Errorf("order[%d] = %q, want %q", i, w.order[i], server)
		}
	}
}

func Test_mcpStartupWindows_AppendLineSeparatesPinnedFromTail(t *testing.T) {
	w := newMcpStartupWindows()
	w.appendLine("notion", "prompt", true)
	w.appendLine("notion", "chatter", false)

	if len(w.pinned["notion"]) != 1 || w.pinned["notion"][0] != "prompt" {
		t.Errorf("pinned = %v, want [prompt]", w.pinned["notion"])
	}
	if len(w.tail["notion"]) != 1 || w.tail["notion"][0] != "chatter" {
		t.Errorf("tail = %v, want [chatter]", w.tail["notion"])
	}
}

func Test_mcpStartupWindows_AppendLineBoundsPinned(t *testing.T) {
	w := newMcpStartupWindows()
	for i := range mcpStartupPinnedCap + 3 {
		w.appendLine("notion", fmt.Sprintf("pin %d", i), true)
	}
	pinned := w.pinned["notion"]
	if len(pinned) != mcpStartupPinnedCap {
		t.Fatalf("pinned holds %d lines, want cap %d", len(pinned), mcpStartupPinnedCap)
	}
	if pinned[0] != "pin 3" {
		t.Errorf("oldest retained pin = %q, want %q", pinned[0], "pin 3")
	}
}

func Test_mcpStartupWindows_AppendLineBoundsTail(t *testing.T) {
	w := newMcpStartupWindows()
	for i := range 10 {
		w.appendLine("notion", fmt.Sprintf("line %d", i), false)
	}
	tail := w.tail["notion"]
	if len(tail) == 10 {
		t.Fatal("tail unbounded: all 10 lines retained")
	}
	if tail[len(tail)-1] != "line 9" {
		t.Errorf("newest tail line = %q, want %q", tail[len(tail)-1], "line 9")
	}
}

func Test_mcpStartupWindows_RenderDrawsSectionsInOrder(t *testing.T) {
	w := newMcpStartupWindows()
	w.appendLine("notion", "please sign in", true)
	w.appendLine("notion", "chatter", false)
	w.appendLine("linear", "linear line", false)

	var out bytes.Buffer
	w.render(&out, 80, 40)
	got := out.String()
	if strings.Contains(got, "\x1b[J") {
		t.Errorf("first render must not clear a region: %q", got)
	}
	order := []string{"▸ mcp.notion log", "» please sign in", "chatter", "▸ mcp.linear log", "linear line"}
	last := -1
	for _, want := range order {
		idx := strings.Index(got, want)
		if idx < 0 {
			t.Fatalf("render missing %q; got: %q", want, got)
		}
		if idx < last {
			t.Errorf("%q rendered out of order; got: %q", want, got)
		}
		last = idx
	}
	if w.drawnRows == 0 {
		t.Error("drawnRows not tracked after render")
	}
}

func Test_mcpStartupWindows_RerenderClearsPreviousRegion(t *testing.T) {
	w := newMcpStartupWindows()
	w.appendLine("notion", "line one", false)
	var out bytes.Buffer
	w.render(&out, 80, 40)
	rows := w.drawnRows
	out.Reset()

	w.appendLine("notion", "line two", false)
	w.render(&out, 80, 40)
	if want := fmt.Sprintf("\x1b[%dA\r\x1b[J", rows); !strings.Contains(out.String(), want) {
		t.Errorf("redraw missing clear of %d previous rows; got: %q", rows, out.String())
	}
}

func Test_mcpStartupWindows_RenderCapsFrameToTerminalHeight(t *testing.T) {
	w := newMcpStartupWindows()
	for _, server := range []string{"one", "two", "three"} {
		for i := range 6 {
			w.appendLine(server, fmt.Sprintf("%s line %d", server, i), false)
		}
	}
	var out bytes.Buffer
	w.render(&out, 80, 6)
	if w.drawnRows > 5 {
		t.Errorf("drawnRows = %d exceeds height budget 5", w.drawnRows)
	}
	if got := strings.Count(out.String(), "\n"); got > 5 {
		t.Errorf("rendered %d rows, want at most 5", got)
	}
}

func Test_mcpStartupWindows_RenderEmptyIsANoop(t *testing.T) {
	w := newMcpStartupWindows()
	var out bytes.Buffer
	w.render(&out, 80, 40)
	// An empty frame still renders one blank row; the point is it must not
	// emit a clear sequence or panic.
	if strings.Contains(out.String(), "\x1b[J") {
		t.Errorf("empty render emitted a clear: %q", out.String())
	}
}

func Test_mcpStartupWindows_ClearWipesRegionOnceAndRetires(t *testing.T) {
	w := newMcpStartupWindows()
	w.appendLine("notion", "line", false)
	var out bytes.Buffer
	w.render(&out, 80, 40)
	rows := w.drawnRows
	out.Reset()

	w.clear(&out)
	if want := fmt.Sprintf("\x1b[%dA\r\x1b[J", rows); out.String() != want {
		t.Errorf("clear = %q, want %q", out.String(), want)
	}
	if !w.cleared {
		t.Error("cleared flag not set")
	}
	if w.drawnRows != 0 {
		t.Errorf("drawnRows = %d after clear, want 0", w.drawnRows)
	}
	out.Reset()
	w.clear(&out)
	if out.Len() != 0 {
		t.Errorf("second clear wrote output: %q", out.String())
	}
}

func Test_mcpStartupWindows_ClearWithoutRenderEmitsNothing(t *testing.T) {
	w := newMcpStartupWindows()
	var out bytes.Buffer
	w.clear(&out)
	if out.Len() != 0 {
		t.Errorf("clear with nothing drawn wrote output: %q", out.String())
	}
	if !w.cleared {
		t.Error("cleared flag not set")
	}
}
