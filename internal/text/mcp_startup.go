package text

import (
	"bytes"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/baalimago/clai/internal/board"
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
	status    map[string]mcpServerStatus
	drawnRows int
	cleared   bool
	frame     int
	now       func() time.Time
	// reopened overrides cleared for one server at a time: a mid-run
	// authorization wait (phase 6) reopens the window for its server even
	// after the pre-session region has already cleared, so the prompt
	// still renders pinned above a bounded tail exactly as during setup.
	reopened map[string]bool
}

// mcpServerStatus is one server's in-flight connect: what the wait is for and
// when it started, drawn as that server's live row.
type mcpServerStatus struct {
	label   string
	started time.Time
}

func newMcpStartupWindows() *mcpStartupWindows {
	return &mcpStartupWindows{
		pinned: make(map[string][]string),
		tail:   make(map[string][]string),
		status: make(map[string]mcpServerStatus),
		now:    time.Now,
	}
}

// startStatus enters server's connect row, registering the server in first-seen
// order even when it has printed nothing: a silent hang must still be visible
// as a running wait rather than as no window at all.
func (w *mcpStartupWindows) startStatus(server, label string) {
	w.touch(server)
	w.status[server] = mcpServerStatus{label: label, started: w.now()}
}

// finishStatus retires server's connect row. A server that has nothing else to
// show leaves the region entirely, so a connect that printed no stderr does not
// leave a bare header behind.
func (w *mcpStartupWindows) finishStatus(server string) {
	delete(w.status, server)
	if len(w.pinned[server]) == 0 && len(w.tail[server]) == 0 {
		w.forget(server)
	}
}

// hasStatus reports whether any server has a connect in flight, which is what
// keeps the redraw ticker alive.
func (w *mcpStartupWindows) hasStatus() bool { return len(w.status) > 0 }

// advance moves the shared spinner to its next frame.
func (w *mcpStartupWindows) advance() { w.frame++ }

// touch appends server to the render order once.
func (w *mcpStartupWindows) touch(server string) {
	if slices.Contains(w.order, server) {
		return
	}
	w.order = append(w.order, server)
}

// forget removes server from the render order.
func (w *mcpStartupWindows) forget(server string) {
	for i, s := range w.order {
		if s == server {
			w.order = append(w.order[:i], w.order[i+1:]...)
			return
		}
	}
}

// reopen marks server as reopened: appendLine and render accept lines for it
// even though cleared is true. A server already reopened is a no-op.
func (w *mcpStartupWindows) reopen(server string) {
	if w.reopened == nil {
		w.reopened = make(map[string]bool)
	}
	w.reopened[server] = true
}

// isReopened reports whether server's window is open under the phase 6
// mid-run mechanism, independent of cleared.
func (w *mcpStartupWindows) isReopened(server string) bool {
	return w.reopened != nil && w.reopened[server]
}

// closeReopened ends server's mid-run window: it stops accepting lines for
// that server, drops its pinned and tail state and its order entry, and
// redraws the shared region without it so its rows disappear in place
// rather than lingering until the next unrelated line redraws everything.
func (w *mcpStartupWindows) closeReopened(server string, out io.Writer, width, height int) {
	if w.reopened != nil {
		delete(w.reopened, server)
	}
	delete(w.pinned, server)
	delete(w.tail, server)
	delete(w.status, server)
	w.forget(server)
	w.render(out, width, height)
}

// appendLine records one line in its server's window: pinned lines survive
// above the rolling tail, and the bounded tail keeps a chatty server from
// drowning out its neighbours.
func (w *mcpStartupWindows) appendLine(server, line string, pin bool) {
	w.touch(server)
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
func (w *mcpStartupWindows) render(out io.Writer, width, height int) error {
	width = max(width, 1)
	var frame bytes.Buffer
	for _, server := range w.order {
		if err := utils.PrintMcpLogHeader(&frame, server, width); err != nil {
			return err
		}
		for _, section := range [][]string{w.pinned[server], w.tail[server]} {
			for _, line := range section {
				if err := utils.PrintMcpLogLine(&frame, line, width); err != nil {
					return err
				}
			}
		}
		// The status row is drawn last in its block: the frame keeps the
		// tail of the whole region when it outgrows the terminal, and the
		// row that says what is happening must be the one that survives.
		if status, ok := w.status[server]; ok {
			if err := utils.PrintMcpStatusLine(&frame, w.statusRow(status), width); err != nil {
				return err
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
		return err
	}
	w.drawnRows = len(rows)
	return nil
}

// statusRow renders one connect row: the shared spinner, what the wait is for,
// and how long it has run.
func (w *mcpStartupWindows) statusRow(status mcpServerStatus) string {
	elapsed := w.now().Sub(status.started).Round(time.Second)
	return board.SpinnerFrame(w.frame) + " " + status.label + " · " + elapsed.String()
}

// clear wipes the drawn region in place and retires the windows: appendLine
// and render are never called again after clear.
func (w *mcpStartupWindows) clear(out io.Writer) {
	w.cleared = true
	clear(w.status)
	if w.drawnRows == 0 {
		return
	}
	fmt.Fprintf(out, "\x1b[%dA\r\x1b[J", w.drawnRows)
	w.drawnRows = 0
}
