package text

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/board"
)

// Test_mcpStartupWindows_StatusRegistersASilentServer pins the case a hang
// hides in: a runner that prints nothing before it stalls still gets a window,
// so the wait is visible instead of silent.
func Test_mcpStartupWindows_StatusRegistersASilentServer(t *testing.T) {
	w := newMcpStartupWindows()
	w.startStatus("ddg", "installing duckduckgo-mcp-server")

	if len(w.order) != 1 || w.order[0] != "ddg" {
		t.Fatalf("order = %v, want [ddg] after a status with no stderr", w.order)
	}
	var out bytes.Buffer
	if err := w.render(&out, 120, 40); err != nil {
		t.Fatalf("render: %v", err)
	}
	got := out.String()
	for _, want := range []string{"mcp.ddg log", "installing duckduckgo-mcp-server", "0s"} {
		if !strings.Contains(got, want) {
			t.Errorf("render output %q does not contain %q", got, want)
		}
	}
}

// Test_mcpStartupWindows_StatusRowFollowsItsLogLines pins the row's place: the
// frame keeps the tail of the region when it outgrows the terminal, so the row
// that says what is happening is drawn after the lines it explains.
func Test_mcpStartupWindows_StatusRowFollowsItsLogLines(t *testing.T) {
	w := newMcpStartupWindows()
	w.appendLine("ddg", "Downloading cryptography (4.5MiB)", false)
	w.startStatus("ddg", "installing duckduckgo-mcp-server")

	var out bytes.Buffer
	if err := w.render(&out, 120, 40); err != nil {
		t.Fatalf("render: %v", err)
	}
	got := out.String()
	if line, status := strings.Index(got, "Downloading cryptography"), strings.Index(got, "installing"); line < 0 || status < 0 || status < line {
		t.Errorf("status row is not drawn under its log lines:\n%q", got)
	}
}

// Test_mcpStartupWindows_AdvanceMovesSpinnerAndElapsed pins the live half: each
// advance changes the glyph and the clock decides the elapsed text, so a long
// install is readable as time passing.
func Test_mcpStartupWindows_AdvanceMovesSpinnerAndElapsed(t *testing.T) {
	w := newMcpStartupWindows()
	start := time.Date(2026, 11, 5, 21, 0, 0, 0, time.UTC)
	w.now = func() time.Time { return start }
	w.startStatus("ddg", "installing pkg")

	first := w.statusRow(w.status["ddg"])
	w.advance()
	w.now = func() time.Time { return start.Add(63 * time.Second) }
	second := w.statusRow(w.status["ddg"])

	if first == second {
		t.Fatalf("row did not change: %q", first)
	}
	if !strings.Contains(second, "1m3s") {
		t.Errorf("row = %q, want the elapsed wait", second)
	}
	if !strings.HasPrefix(second, board.SpinnerFrame(1)) {
		t.Errorf("row = %q, want the next spinner frame", second)
	}
}

// Test_mcpStartupWindows_FinishingASilentStatusRemovesItsBlock pins that a
// connect which printed nothing leaves no bare header behind.
func Test_mcpStartupWindows_FinishingASilentStatusRemovesItsBlock(t *testing.T) {
	w := newMcpStartupWindows()
	w.startStatus("ddg", "starting")
	w.finishStatus("ddg")

	var out bytes.Buffer
	if err := w.render(&out, 120, 40); err != nil {
		t.Fatalf("render: %v", err)
	}
	if got := out.String(); strings.Contains(got, "mcp.ddg") {
		t.Errorf("render output = %q, want no block after the status retired", got)
	}
}

// Test_mcpStartupWindows_StatusSurvivesFrameClipping pins that a region taller
// than the terminal keeps the live row: clipping takes the tail of the frame,
// and the last server's status row is the tail.
func Test_mcpStartupWindows_StatusSurvivesFrameClipping(t *testing.T) {
	w := newMcpStartupWindows()
	for _, server := range []string{"a", "b", "c", "d"} {
		for i := range 8 {
			w.appendLine(server, fmt.Sprintf("%s line %d", server, i), false)
		}
	}
	w.startStatus("d", "installing pkg")

	var out bytes.Buffer
	if err := w.render(&out, 120, 6); err != nil {
		t.Fatalf("render: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "installing pkg") {
		t.Errorf("clipped frame dropped the live status row:\n%q", got)
	}
}

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
