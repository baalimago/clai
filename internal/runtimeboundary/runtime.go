package runtimeboundary

import (
	"context"
	"os"

	"github.com/baalimago/clai/pkg/diagnostic"
	"github.com/baalimago/go_away_boilerplate/pkg/cmd"
)

type ExitStatus int

type Invocation struct {
	Args     []string
	Commands map[string]cmd.Command
}

type Dispatcher interface {
	// Dispatch runs the command named by Args and returns its exit status with
	// the original parse, setup, or run error, so errors.Is and errors.As reach
	// the cause. A help request returns status 0 and an error that errors.As
	// matches cmd.HelpRequestedError; a user-initiated exit returns status 0
	// and an error that errors.Is matches cmd.ErrUserInitiatedExit or
	// table.ErrUserInitiatedExit; no arguments returns status 1 and
	// cmd.ErrNoArgs. It writes no diagnostic and no usage. (Dispatch.1, F2, F9)
	//
	// Completion output and each command's result stream are unchanged.
	// (Dispatch.2, F1, F9)
	Dispatch(context.Context, Invocation) (ExitStatus, error)
}

type ShutdownMonitor interface {
	// Monitor observes interrupt signals until ctx is done and reports each
	// signal as a diagnostic through Reporter.Report. The first signal cancels
	// through cancel; the third calls the owner's force-exit collaborator. A
	// diagnostic delivery failure is sent on the returned channel, which closes
	// when observation stops. It returns an error, and starts nothing, when
	// the owner's signal source cannot be observed. It never calls os.Exit
	// itself. (Monitor.1, F9)
	Monitor(context.Context, context.CancelFunc) (<-chan error, error)
}

type DispatchMonitor interface {
	Dispatcher
	ShutdownMonitor
}

// SignalSource is where the owner's interrupt signals come from.
type SignalSource interface {
	// Subscribe starts delivery on signals until stop runs, or returns an
	// error when the source cannot be observed. (Subscribe.1)
	Subscribe() (signals <-chan os.Signal, stop func(), err error)
}

// NewDispatchMonitor returns the DispatchMonitor that observes source,
// reports each signal as an event of signalType through reporter, and calls
// forceExit on the third signal. It reads no process state itself.
// (NewDispatchMonitor.1, F9)
func NewDispatchMonitor(source SignalSource, reporter diagnostic.Reporter, signalType string, forceExit func()) DispatchMonitor {
	return &dispatchMonitor{source: source, reporter: reporter, signalType: signalType, forceExit: forceExit}
}
