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

// mcpLogEntry is one buffered stderr line. An exit entry carries the flushed
// tail of a terminated server and is always classified as an error so it
// elevates out of the rolling window.
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
	mu     sync.Mutex
	mode   mcpLogMode
	notice func(server, line string)
	errOut io.Writer
	queue  []mcpLogEntry
	tails  map[string][]string
	notify chan struct{}
}

func newMcpLogSink(mode mcpLogMode) *mcpLogSink {
	return &mcpLogSink{
		mode:   mode,
		notice: func(server, line string) { ancli.Noticef("mcp_%v: %v\n", server, line) },
		errOut: os.Stderr,
		tails:  make(map[string][]string),
		notify: make(chan struct{}, 1),
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
// process. Rolling mode queues the line for the session loop; direct modes
// print or drop it immediately. Every mode retains the bounded termination
// tail so ServerExited can flush the crash reason.
func (s *mcpLogSink) AppendServerLog(server, line string) {
	isErr := utils.IsMcpLogErrorLine(line)
	s.mu.Lock()
	defer s.mu.Unlock()
	switch s.mode {
	case mcpLogPlainDirect:
		s.notice(server, line)
		return
	case mcpLogRawStructured:
		s.retainTailLocked(server, line)
		if isErr {
			fmt.Fprintf(s.errOut, "mcp_%v: %v\n", server, line)
		}
		return
	}
	s.retainTailLocked(server, line)
	s.queue = append(s.queue, mcpLogEntry{server: server, line: line, isError: isErr})
	s.dropOldestNonErrorLocked()
	if isErr {
		s.signalLocked()
	}
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

// Drain returns and clears all buffered entries. It is called from the
// serialized session loop only.
func (s *mcpLogSink) Drain() []mcpLogEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) == 0 {
		return nil
	}
	entries := s.queue
	s.queue = nil
	return entries
}
