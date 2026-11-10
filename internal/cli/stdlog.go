package cli

import (
	"bytes"
	"context"
	"log"
	"sync"

	"github.com/baalimago/clai/internal/runenv"
)

// routeStandardLog sends everything written through Go's standard logger
// to the run's diagnostics until the returned function restores it. The
// standard library logs through it on its own, net/http among them, and the
// default slog logger and an http.Server without an ErrorLog write through
// it too; left alone those lines reach stderr raw, outside the run's format.
// The logger is process-global state, so only the CLI's composition root
// touches it; a library caller keeps its own logger.
func routeStandardLog(ctx context.Context) (restore func()) {
	writer, flags, prefix := log.Writer(), log.Flags(), log.Prefix()
	route := &diagnosticLog{ctx: context.WithoutCancel(ctx)}
	log.SetOutput(route)
	log.SetFlags(0)
	log.SetPrefix("")
	return func() {
		log.SetOutput(writer)
		log.SetFlags(flags)
		log.SetPrefix(prefix)
		route.flush()
	}
}

// diagnosticLog reports each line written to it as a warning of the run.
type diagnosticLog struct {
	ctx     context.Context
	mu      sync.Mutex
	pending []byte
}

func (d *diagnosticLog) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pending = append(d.pending, p...)
	for {
		end := bytes.IndexByte(d.pending, '\n')
		if end < 0 {
			return len(p), nil
		}
		d.report(d.pending[:end])
		d.pending = d.pending[end+1:]
	}
}

func (d *diagnosticLog) flush() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.report(d.pending)
	d.pending = nil
}

func (d *diagnosticLog) report(line []byte) {
	if line = bytes.TrimSpace(line); len(line) > 0 {
		runenv.Warnf(d.ctx, runenv.SubsystemClai, "%s", line)
	}
}
