package text

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/baalimago/clai/internal/utils"
)

// mcpStartupPinnedCap bounds the pinned auth lines per startup window: enough
// for a prompt, its URL, and a user code.
const mcpStartupPinnedCap = 4

// mcpStartupWindows renders per-server MCP startup logs into one shared
// terminal region: one window per server in first-seen order, each showing
// its pinned auth lines over the bounded tail of its other stderr. Auth lines
// pin so OAuth machinery chatter can never evict the prompt the user must act
// on. The region redraws in place on every line and is cleared once MCP setup
// succeeds; a failed or blocked setup keeps it visible. All methods run under
// the owning sink's mutex.
type mcpStartupWindows struct {
	order     []string
	pinned    map[string][]string
	tail      map[string][]string
	drawnRows int
	cleared   bool
}

func newMcpStartupWindows() *mcpStartupWindows {
	return &mcpStartupWindows{
		pinned: make(map[string][]string),
		tail:   make(map[string][]string),
	}
}

// appendLine records one line in its server's window: pinned lines survive
// above the rolling tail, and the bounded tail keeps a chatty server from
// drowning out its neighbours.
func (w *mcpStartupWindows) appendLine(server, line string, pin bool) {
	if _, seen := w.tail[server]; !seen {
		if _, seenPinned := w.pinned[server]; !seenPinned {
			w.order = append(w.order, server)
		}
	}
	if pin {
		pinned := append(w.pinned[server], line)
		if len(pinned) > mcpStartupPinnedCap {
			pinned = pinned[len(pinned)-mcpStartupPinnedCap:]
		}
		w.pinned[server] = pinned
		return
	}
	tail := append(w.tail[server], line)
	if bound := utils.ToolOutputRows(); len(tail) > bound {
		tail = tail[len(tail)-bound:]
	}
	w.tail[server] = tail
}

// render redraws the whole region in place. The frame is capped to the
// terminal height so the in-place clear can never run past the top of the
// screen.
func (w *mcpStartupWindows) render(out io.Writer, width, height int) {
	width = max(width, 1)
	var frame bytes.Buffer
	for _, server := range w.order {
		if err := utils.PrintMcpLogHeader(&frame, server, width); err != nil {
			return
		}
		for _, section := range [][]string{w.pinned[server], w.tail[server]} {
			for _, line := range section {
				if err := utils.PrintMcpLogLine(&frame, line, width); err != nil {
					return
				}
			}
		}
	}
	rows := strings.Split(strings.TrimSuffix(frame.String(), "\n"), "\n")
	if maxRows := max(height-1, 1); len(rows) > maxRows {
		rows = rows[len(rows)-maxRows:]
	}
	var buf bytes.Buffer
	if w.drawnRows > 0 {
		fmt.Fprintf(&buf, "\x1b[%dA\r\x1b[J", w.drawnRows)
	}
	for _, row := range rows {
		fmt.Fprintln(&buf, row)
	}
	if _, err := out.Write(buf.Bytes()); err != nil {
		return
	}
	w.drawnRows = len(rows)
}

// clear wipes the drawn region in place and retires the windows: appendLine
// and render are never called again after clear.
func (w *mcpStartupWindows) clear(out io.Writer) {
	w.cleared = true
	if w.drawnRows == 0 {
		return
	}
	fmt.Fprintf(out, "\x1b[%dA\r\x1b[J", w.drawnRows)
	w.drawnRows = 0
}
