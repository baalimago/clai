package runtimeboundary

import (
	"context"
	"log/slog"
	"os"

	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/pkg/diagnostic"
	"github.com/baalimago/go_away_boilerplate/pkg/cmd"
)

// shutdownStep is the escalation step a signal takes, counted from zero.
type shutdownStep int

const (
	stepCancel shutdownStep = iota
	stepWarn
	stepForceExit
)

// failureBacklog lets every step of the escalation ladder send its delivery
// failure without waiting on the owner.
const failureBacklog = int(stepForceExit) + 1

// missingCollaboratorError names the collaborator the owner did not supply.
type missingCollaboratorError string

func (e missingCollaboratorError) Error() string {
	return "runtime boundary is missing its " + string(e)
}

type dispatchMonitor struct {
	source     SignalSource
	reporter   diagnostic.Reporter
	signalType string
	forceExit  func()
}

// Dispatch writes completion output to the result writer of the run's Env
// on ctx, and where cmd.RunSilent writes it when ctx carries none.
func (m *dispatchMonitor) Dispatch(ctx context.Context, invocation Invocation) (ExitStatus, error) {
	env, ok := runenv.Lookup(ctx)
	if !ok {
		status, err := cmd.RunSilent(ctx, invocation.Args, invocation.Commands)
		return ExitStatus(status), err
	}
	status, err := cmd.RunSilentTo(ctx, invocation.Args, invocation.Commands, env.Stdout())
	return ExitStatus(status), err
}

func (m *dispatchMonitor) Monitor(ctx context.Context, cancel context.CancelFunc) (<-chan error, error) {
	if err := m.validate(cancel); err != nil {
		return nil, err
	}
	signals, stop, err := m.source.Subscribe()
	if err != nil {
		return nil, diag.Errorf("subscribe to the signal source: %w", err)
	}
	if signals == nil {
		if stop != nil {
			stop()
		}
		return nil, missingCollaboratorError("signal channel")
	}
	if stop == nil {
		stop = func() {}
	}
	failures := make(chan error, failureBacklog)
	go m.observe(ctx, cancel, signals, stop, failures)
	return failures, nil
}

func (m *dispatchMonitor) validate(cancel context.CancelFunc) error {
	switch {
	case m.source == nil:
		return missingCollaboratorError("signal source")
	case m.reporter == nil:
		return missingCollaboratorError("reporter")
	case m.signalType == "":
		return missingCollaboratorError("signal type")
	case m.forceExit == nil:
		return missingCollaboratorError("force exit")
	case cancel == nil:
		return missingCollaboratorError("cancel function")
	}
	return nil
}

func (m *dispatchMonitor) observe(ctx context.Context, cancel context.CancelFunc, signals <-chan os.Signal, stop func(), failures chan<- error) {
	defer close(failures)
	defer stop()
	step := stepCancel
	for {
		select {
		case <-ctx.Done():
			return
		case sig, open := <-signals:
			if !open {
				return
			}
			if err := m.report(ctx, sig, step); err != nil {
				select {
				case failures <- err:
				case <-ctx.Done():
					return
				}
			}
			m.escalate(cancel, step)
			if step < stepForceExit {
				step++
			}
		}
	}
}

func (m *dispatchMonitor) escalate(cancel context.CancelFunc, step shutdownStep) {
	switch step {
	case stepCancel:
		cancel()
	case stepWarn:
	default:
		m.forceExit()
	}
}

func (m *dispatchMonitor) report(ctx context.Context, sig os.Signal, step shutdownStep) error {
	event := diagnostic.Event{
		Type:    m.signalType,
		Message: step.message(),
		Facts:   []slog.Attr{slog.String("signal", signalName(sig))},
	}
	// The event is reported after ctx may be done, since the first signal
	// cancels; delivery must not depend on the observed context.
	if err := m.reporter.Report(context.WithoutCancel(ctx), event); err != nil {
		return diag.Errorf("report shutdown signal %v: %w", signalName(sig), err)
	}
	return nil
}

func (s shutdownStep) message() string {
	switch s {
	case stepCancel:
		return "shutting down gracefully"
	case stepWarn:
		return "graceful shutdown ongoing, signal again to force exit"
	default:
		return "forcing exit"
	}
}

func signalName(sig os.Signal) string {
	if sig == nil {
		return "unknown"
	}
	return sig.String()
}
