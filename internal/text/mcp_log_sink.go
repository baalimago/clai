package text

import (
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

// mcpLogMode decides how MCP server stderr lines are handled before the
// session loop drains them. Rolling output queues everything into the shared
// activity viewport. Plain non-rolling output keeps the legacy direct print.
// Raw and structured output suppress normal lines and surface only errors.
type mcpLogMode uint8

const (
	mcpLogRolling mcpLogMode = iota
	mcpLogPlainDirect
	mcpLogRawStructured
)

// mcpLogQueueCap bounds the number of buffered stderr lines per run. The
// oldest non-error entries drop first when the cap is hit, so errors survive a
// log burst.
const mcpLogQueueCap = 256

// mcpLogExitTailLines is the number of trailing stderr lines flushed as an
// elevated error block when a server terminates unexpectedly.
const mcpLogExitTailLines = 10

// mcpLogAuthFollowLines is the number of lines elevated after an auth prompt,
// so payloads like URLs and device codes on the following lines stay visible.
const mcpLogAuthFollowLines = 3

// mcpLogEntry is one buffered stderr line. isError marks entries that elevate
// out of the rolling window: errors, auth prompts and their follow window. An
// exit entry carries the flushed tail of a terminated server and always
// elevates.
type mcpLogEntry struct {
	server  string
	line    string
	isError bool
	exit    bool
	lines   []string
}

// mcpLogSink buffers or prints MCP server stderr lines. AppendServerLog and
// ServerExited run on the MCP client goroutines; Drain runs on the serialized
// session loop, so the queue is mutex-guarded. The notify channel wakes the
// session loop when an error entry is buffered, so elevated errors render
// live instead of waiting for the next session event.
type mcpLogSink struct {
	mu         sync.Mutex
	mode       mcpLogMode
	notice     func(server, line string)
	errOut     io.Writer
	termWidth  func() int
	queue      []mcpLogEntry
	tails      map[string][]string
	authFollow map[string]int
	notify     chan struct{}
	// attached marks the serialized session loop as running. Before that (MCP
	// setup may block on the very auth prompt a server just wrote), rolling
	// mode renders every stderr line live into per-server startup windows
	// instead of queueing it for a drain that would never come.
	attached   bool
	startup    *mcpStartupWindows
	termHeight func() int
	// authOpen tracks, per server, the close function returned by a
	// currently open authorization wait (phase 6): presence is "open". A
	// duplicate AuthPending for an already-open server returns the same
	// close function rather than opening a second window or ringing the
	// bell again.
	authOpen map[string]func()
}

func newMcpLogSink(mode mcpLogMode) *mcpLogSink {
	return newMcpLogSinkTo(mode, os.Stderr)
}

// newMcpLogSinkTo builds a sink that writes to errOut instead of the process
// stderr, so a querier whose output is discarded never touches the global
// stream (phase 8 gate finding, R2-09).
func newMcpLogSinkTo(mode mcpLogMode, errOut io.Writer) *mcpLogSink {
	return &mcpLogSink{
		mode:       mode,
		notice:     func(server, line string) { ancli.Noticef("mcp_%v: %v\n", server, line) },
		errOut:     errOut,
		termWidth:  func() int { return utils.SessionDimensions(errOut).Width },
		termHeight: func() int { return utils.SessionDimensions(errOut).Height },
		tails:      make(map[string][]string),
		authFollow: make(map[string]int),
		startup:    newMcpStartupWindows(),
		notify:     make(chan struct{}, 1),
		authOpen:   make(map[string]func()),
	}
}

// mcpLogModeFor picks the stderr display policy from the session settings. The
// policy mirrors the session output mode: debug keeps the legacy direct print,
// raw/structured and redirected output keep only errors on stderr, and a
// terminal rolling-output session buffers everything into the window.
func mcpLogModeFor(debug, outputIsTerminal, raw, structured, rollingEnabled bool) mcpLogMode {
	if debug {
		return mcpLogPlainDirect
	}
	if !outputIsTerminal || raw || structured {
		return mcpLogRawStructured
	}
	if rollingEnabled {
		return mcpLogRolling
	}
	return mcpLogPlainDirect
}

// AppendServerLog receives one non-empty stderr line from an MCP server
// process. During startup, rolling mode renders the line live into the
// server's startup window; once the windows are cleared or the session loop
// attaches, lines queue for the session loop instead. Direct modes print or
// drop immediately. Queued error lines and auth prompts elevate; an auth
// prompt also opens a short follow window elevating payload-shaped lines
// (URLs, user codes), since the actionable payload often arrives on its own
// line. Every mode retains the bounded termination tail so ServerExited can
// flush the crash reason.
func (s *mcpLogSink) AppendServerLog(server, line string) {
	isErr := utils.IsMcpLogErrorLine(line)
	isAuth := utils.IsMcpLogAuthLine(line)
	s.mu.Lock()
	defer s.mu.Unlock()
	followPayload := s.authFollow[server] > 0 && utils.IsMcpLogAuthPayloadLine(line)
	elevate := isErr || isAuth || followPayload
	if s.authFollow[server] > 0 {
		s.authFollow[server]--
	}
	if isAuth {
		s.authFollow[server] = mcpLogAuthFollowLines
	}
	switch s.mode {
	case mcpLogPlainDirect:
		s.notice(server, line)
		return
	case mcpLogRawStructured:
		s.retainTailLocked(server, line)
		if elevate {
			fmt.Fprintf(s.errOut, "mcp_%v: %v\n", server, line)
		}
		return
	}
	s.retainTailLocked(server, line)
	if (!s.attached && !s.startup.cleared) || s.startup.isReopened(server) {
		s.startup.appendLine(server, line, isAuth || followPayload)
		s.startup.render(s.errOut, s.termWidth(), s.termHeight())
		return
	}
	s.queue = append(s.queue, mcpLogEntry{server: server, line: line, isError: elevate})
	s.dropOldestNonErrorLocked()
	if elevate {
		s.signalLocked()
	}
}

// attach marks the serialized session loop as running: from now on elevated
// lines queue for live elevation out of the rolling window instead of
// printing directly to stderr.
func (s *mcpLogSink) attach() {
	s.mu.Lock()
	s.attached = true
	s.mu.Unlock()
}

// AuthPrintWriter exposes the sink's own injectable writer, so the
// authorization phase's printed-URL fallback writes through it instead of
// the process's raw stdout (R2-08): a library consumer must never have
// output written to its real stdout, and errOut is already whatever this
// run was configured with (the process stderr by default, userConf.ErrOut
// otherwise).
func (s *mcpLogSink) AuthPrintWriter() io.Writer { return s.errOut }

// setupSucceeded reports that every MCP server finished setup: any pending
// auth flow completed, so the startup windows are cleared in place and later
// lines queue for the session loop. Setup failures and hangs never reach
// this, keeping each server's last lines visible.
func (s *mcpLogSink) setupSucceeded() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.startup.clear(s.errOut)
}

// ServerExited reports an unexpected server termination. Rolling mode appends
// the buffered tail as one exit entry so the crash reason elevates out of the
// window. Raw and structured modes keep normal lines suppressed while the
// server runs, but the termination tail is emitted to stderr as an error
// diagnostic. Plain direct mode already printed every line live and has
// nothing to flush.
func (s *mcpLogSink) ServerExited(server string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tail := s.tails[server]
	if len(tail) == 0 {
		return
	}
	switch s.mode {
	case mcpLogRolling:
		if !s.attached && !s.startup.cleared {
			// The startup window already shows this server's trailing lines;
			// a failed setup leaves them visible as the crash reason.
			return
		}
		s.queue = append(s.queue, mcpLogEntry{server: server, exit: true, lines: tail, isError: true})
		s.dropOldestNonErrorLocked()
		s.signalLocked()
	case mcpLogRawStructured:
		for _, line := range tail {
			fmt.Fprintf(s.errOut, "mcp_%v: %v\n", server, line)
		}
	}
}

// retainTailLocked keeps the bounded trailing stderr lines per server so the
// termination tail can be flushed by ServerExited.
func (s *mcpLogSink) retainTailLocked(server, line string) {
	tail := s.tails[server]
	tail = append(tail, line)
	if len(tail) > mcpLogExitTailLines {
		tail = tail[len(tail)-mcpLogExitTailLines:]
	}
	s.tails[server] = tail
}

// signalLocked wakes the session loop without blocking the MCP client
// goroutine. A pending wake-up coalesces into the single buffered slot.
func (s *mcpLogSink) signalLocked() {
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

// Notify returns the channel the sink signals when an error entry is
// buffered. The serialized session loop selects on it and drains the sink, so
// elevated MCP errors render live. The channel only wakes the loop; it never
// mutates the viewport.
func (s *mcpLogSink) Notify() <-chan struct{} {
	return s.notify
}

// dropOldestNonErrorLocked evicts the oldest non-error entries once the queue
// exceeds its cap. When every retained entry is an error, the oldest entry
// drops regardless.
func (s *mcpLogSink) dropOldestNonErrorLocked() {
	if len(s.queue) <= mcpLogQueueCap {
		return
	}
	excess := len(s.queue) - mcpLogQueueCap
	kept := make([]mcpLogEntry, 0, mcpLogQueueCap)
	dropped := 0
	for _, entry := range s.queue {
		if dropped < excess && !entry.isError {
			dropped++
			continue
		}
		kept = append(kept, entry)
	}
	if len(kept) > mcpLogQueueCap {
		kept = kept[len(kept)-mcpLogQueueCap:]
	}
	s.queue = kept
}

// Drain returns and clears buffered entries. It is called from the
// serialized session loop only. A server whose authorization wait window is
// open (phase 6) never has its own lines queued in the first place (they
// render straight into the reopened window instead); Drain still scopes its
// suspension per server rather than globally (R1-07), so an unrelated
// server's lines are never held hostage by one server's open window. An
// entry whose server somehow still has an open window is deferred rather
// than dropped — the next Drain after that window closes returns it.
func (s *mcpLogSink) Drain() []mcpLogEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) == 0 {
		return nil
	}
	if len(s.authOpen) == 0 {
		entries := s.queue
		s.queue = nil
		return entries
	}
	var drained, deferred []mcpLogEntry
	for _, e := range s.queue {
		if _, open := s.authOpen[e.server]; open {
			deferred = append(deferred, e)
			continue
		}
		drained = append(drained, e)
	}
	s.queue = deferred
	return drained
}

// AuthPending implements mcp.AuthPendingSink. A duplicate call for a server
// whose window is already open returns the same close function: no second
// bell, no second window. The bell is subject to the theme gate and always
// considered, even in a mode with no window to open, so a headless-looking
// but still-terminal session is not silently left unnotified.
func (s *mcpLogSink) AuthPending(server string) (done func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, open := s.authOpen[server]; open {
		return existing
	}
	s.ringBellLocked()
	if s.mode == mcpLogRolling {
		s.startup.reopen(server)
		s.startup.render(s.errOut, s.termWidth(), s.termHeight())
	}
	var closeOnce sync.Once
	doneFn := func() {
		closeOnce.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			delete(s.authOpen, server)
			if s.mode == mcpLogRolling {
				s.startup.closeReopened(server, s.errOut, s.termWidth(), s.termHeight())
			}
		})
	}
	s.authOpen[server] = doneFn
	return doneFn
}

// ringBellLocked emits the terminal bell once, subject to the theme gate,
// to the sink's own injectable error writer — never the process standard
// output (the existing newMcpLogSinkTo contract this phase reuses).
func (s *mcpLogSink) ringBellLocked() {
	if !utils.NotificationBellEnabled() {
		return
	}
	fmt.Fprint(s.errOut, "\a")
}
