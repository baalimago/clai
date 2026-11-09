package runtimeboundary_test

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/diag/diagfake"

	"github.com/baalimago/clai/internal/jsonl"
	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/internal/runtimeboundary"
	"github.com/baalimago/clai/internal/stdio"
	"github.com/baalimago/clai/internal/testenv"
	"github.com/baalimago/clai/pkg/diagnostic"
	"github.com/baalimago/go_away_boilerplate/pkg/cmd"
	"github.com/baalimago/go_away_boilerplate/pkg/table"
)

func TestMain(m *testing.M) { testenv.Main(m) }

const signalType = "shutdown.signal"

var (
	errSetup    = errors.New("setup failed")
	errReport   = errors.New("report failed")
	errObserved = errors.New("signal source unavailable")
)

type typedRunError struct{ reason string }

func (e typedRunError) Error() string { return "typed run failure: " + e.reason }

type fakeCommand struct {
	fs       *flag.FlagSet
	setupErr error
	runErr   error
	ran      bool
}

func newFakeCommand(setupErr, runErr error) *fakeCommand {
	return &fakeCommand{fs: flag.NewFlagSet("fake", flag.ContinueOnError), setupErr: setupErr, runErr: runErr}
}

func (c *fakeCommand) Setup(context.Context) error { return c.setupErr }

func (c *fakeCommand) Run(context.Context) error {
	c.ran = true
	return c.runErr
}

func (c *fakeCommand) Help() string           { return "fake help" }
func (c *fakeCommand) Describe() string       { return "fake command" }
func (c *fakeCommand) Flagset() *flag.FlagSet { return c.fs }

type implementation func(SignalSourceFunc, diagnostic.Reporter, func()) runtimeboundary.DispatchMonitor

// SignalSourceFunc adapts a function as a runtimeboundary.SignalSource.
type SignalSourceFunc func() (<-chan os.Signal, func(), error)

func (f SignalSourceFunc) Subscribe() (<-chan os.Signal, func(), error) { return f() }

func implementations() map[string]implementation {
	return map[string]implementation{
		"NewDispatchMonitor": func(source SignalSourceFunc, reporter diagnostic.Reporter, forceExit func()) runtimeboundary.DispatchMonitor {
			return runtimeboundary.NewDispatchMonitor(source, reporter, signalType, forceExit)
		},
	}
}

func unusedSource() SignalSourceFunc {
	return func() (<-chan os.Signal, func(), error) { return nil, nil, errObserved }
}

type streams struct {
	stdout string
	stderr string
}

// captureStreams runs fn with the process stdout and stderr replaced by pipes.
func captureStreams(t *testing.T, fn func()) streams {
	t.Helper()
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	origOut, origErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outW, errW
	var wg sync.WaitGroup
	var outBuf, errBuf bytes.Buffer
	wg.Go(func() { _, _ = io.Copy(&outBuf, outR) })
	wg.Go(func() { _, _ = io.Copy(&errBuf, errR) })
	defer func() {
		os.Stdout, os.Stderr = origOut, origErr
	}()
	fn()
	os.Stdout, os.Stderr = origOut, origErr
	_ = outW.Close()
	_ = errW.Close()
	wg.Wait()
	_ = outR.Close()
	_ = errR.Close()
	return streams{stdout: outBuf.String(), stderr: errBuf.String()}
}

// TestDispatcherContract proves Dispatch.1 and Dispatch.2.
func TestDispatcherContract(t *testing.T) {
	typed := typedRunError{reason: "quota"}
	tests := []struct {
		name       string
		args       []string
		commands   func() map[string]cmd.Command
		wantStatus runtimeboundary.ExitStatus
		check      func(t *testing.T, err error)
		wantStdout string
	}{
		{
			name:       "Dispatch.1 success returns 0 and no error",
			args:       []string{"clai", "fake"},
			commands:   func() map[string]cmd.Command { return map[string]cmd.Command{"fake": newFakeCommand(nil, nil)} },
			wantStatus: 0,
			check: func(t *testing.T, err error) {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
			},
		},
		{
			name:       "Dispatch.1 parse failure returns 1 and the typed cause",
			args:       []string{"clai", "unknown"},
			commands:   func() map[string]cmd.Command { return map[string]cmd.Command{"fake": newFakeCommand(nil, nil)} },
			wantStatus: 1,
			check: func(t *testing.T, err error) {
				var notFound cmd.ArgNotFoundError
				if !errors.As(err, &notFound) || string(notFound) != "unknown" {
					t.Fatalf("err = %v, want cmd.ArgNotFoundError(unknown)", err)
				}
			},
		},
		{
			name:       "Dispatch.1 setup failure returns 1 and the original cause",
			args:       []string{"clai", "fake"},
			commands:   func() map[string]cmd.Command { return map[string]cmd.Command{"fake": newFakeCommand(errSetup, nil)} },
			wantStatus: 1,
			check: func(t *testing.T, err error) {
				if !errors.Is(err, errSetup) {
					t.Fatalf("err = %v, want errSetup in chain", err)
				}
				if err.Error() != errSetup.Error() {
					t.Fatalf("err text = %q, want %q", err.Error(), errSetup.Error())
				}
			},
		},
		{
			name:       "Dispatch.1 run failure returns 1 and the typed cause",
			args:       []string{"clai", "fake"},
			commands:   func() map[string]cmd.Command { return map[string]cmd.Command{"fake": newFakeCommand(nil, typed)} },
			wantStatus: 1,
			check: func(t *testing.T, err error) {
				var got typedRunError
				if !errors.As(err, &got) || got != typed {
					t.Fatalf("err = %v, want typedRunError in chain", err)
				}
			},
		},
		{
			name:       "Dispatch.1 help returns 0 and HelpRequestedError",
			args:       []string{"clai", "fake", "-h"},
			commands:   func() map[string]cmd.Command { return map[string]cmd.Command{"fake": newFakeCommand(nil, nil)} },
			wantStatus: 0,
			check: func(t *testing.T, err error) {
				var help cmd.HelpRequestedError
				if !errors.As(err, &help) {
					t.Fatalf("err = %v, want cmd.HelpRequestedError", err)
				}
				if !strings.Contains(help.Help, "fake help") {
					t.Fatalf("help = %q, want the command's help", help.Help)
				}
			},
		},
		{
			name:       "Dispatch.1 no arguments returns 1 and ErrNoArgs",
			args:       []string{"clai"},
			commands:   func() map[string]cmd.Command { return map[string]cmd.Command{"fake": newFakeCommand(nil, nil)} },
			wantStatus: 1,
			check: func(t *testing.T, err error) {
				if !errors.Is(err, cmd.ErrNoArgs) {
					t.Fatalf("err = %v, want cmd.ErrNoArgs", err)
				}
			},
		},
		{
			name: "Dispatch.1 user exit returns 0 and cmd.ErrUserInitiatedExit",
			args: []string{"clai", "fake"},
			commands: func() map[string]cmd.Command {
				return map[string]cmd.Command{"fake": newFakeCommand(nil, cmd.ErrUserInitiatedExit)}
			},
			wantStatus: 0,
			check: func(t *testing.T, err error) {
				if !errors.Is(err, cmd.ErrUserInitiatedExit) {
					t.Fatalf("err = %v, want cmd.ErrUserInitiatedExit", err)
				}
			},
		},
		{
			name: "Dispatch.1 table user exit returns 0 and table.ErrUserInitiatedExit",
			args: []string{"clai", "fake"},
			commands: func() map[string]cmd.Command {
				return map[string]cmd.Command{"fake": newFakeCommand(nil, table.ErrUserInitiatedExit)}
			},
			wantStatus: 0,
			check: func(t *testing.T, err error) {
				if !errors.Is(err, table.ErrUserInitiatedExit) {
					t.Fatalf("err = %v, want table.ErrUserInitiatedExit", err)
				}
			},
		},
		{
			name:       "Dispatch.2 completion reaches stdout",
			args:       []string{"clai", "__complete", "clai", "fa"},
			commands:   func() map[string]cmd.Command { return map[string]cmd.Command{"fake": newFakeCommand(nil, nil)} },
			wantStatus: 0,
			check: func(t *testing.T, err error) {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
			},
			wantStdout: "fake",
		},
	}
	for implName, build := range implementations() {
		for _, tc := range tests {
			t.Run(implName+"/"+tc.name, func(t *testing.T) {
				monitor := build(unusedSource(), newRecordingReporter(t, errReport), func() {})
				var status runtimeboundary.ExitStatus
				var err error
				out := captureStreams(t, func() {
					status, err = monitor.Dispatch(context.Background(), runtimeboundary.Invocation{
						Args:     tc.args,
						Commands: tc.commands(),
					})
				})
				if status != tc.wantStatus {
					t.Fatalf("status = %d, want %d (err %v)", status, tc.wantStatus, err)
				}
				tc.check(t, err)
				if out.stderr != "" {
					t.Fatalf("stderr = %q, want empty", out.stderr)
				}
				if tc.wantStdout == "" && out.stdout != "" {
					t.Fatalf("stdout = %q, want empty", out.stdout)
				}
				if !strings.Contains(out.stdout, tc.wantStdout) {
					t.Fatalf("stdout = %q, want it to contain %q", out.stdout, tc.wantStdout)
				}
			})
		}
	}
}

// TestDispatcherContract_completionOnTheRunsStdout proves Dispatch.2 with a
// run's Env on the context: completion output is a result on the run's
// stdout, so under -json it is output records (F1, F12).
func TestDispatcherContract_completionOnTheRunsStdout(t *testing.T) {
	invocations := map[string][]string{
		"candidates": {"clai", "__complete", "clai", "fa"},
		"script":     {"clai", "completion", "bash"},
	}
	for implName, build := range implementations() {
		for name, args := range invocations {
			t.Run(implName+"/"+name, func(t *testing.T) {
				var stdout, stderr bytes.Buffer
				streams := stdio.New(stdio.Config{Format: jsonl.FormatJSON, Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr})
				ctx := runenv.With(context.Background(), runenv.New(runenv.Config{Streams: streams, Format: jsonl.FormatJSON}))
				monitor := build(unusedSource(), newRecordingReporter(t, errReport), func() {})
				var status runtimeboundary.ExitStatus
				var err error
				out := captureStreams(t, func() {
					status, err = monitor.Dispatch(ctx, runtimeboundary.Invocation{
						Args:     args,
						Commands: map[string]cmd.Command{"fake": newFakeCommand(nil, nil)},
					})
				})
				if status != 0 || err != nil {
					t.Fatalf("Dispatch = %d, %v; want 0, nil", status, err)
				}
				if out.stdout != "" || out.stderr != "" {
					t.Fatalf("process streams got stdout %q stderr %q, want nothing", out.stdout, out.stderr)
				}
				if err := streams.Close(); err != nil {
					t.Fatalf("close streams: %v", err)
				}
				lines := strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\n")
				if stdout.Len() == 0 || stderr.Len() != 0 {
					t.Fatalf("run stdout %q stderr %q, want output records on stdout only", stdout.String(), stderr.String())
				}
				for _, line := range lines {
					if !strings.HasPrefix(line, `{"type":"output","stream":"stdout","text":`) {
						t.Fatalf("line %q is not an output record", line)
					}
				}
			})
		}
	}
}

// TestDispatcherContract_runsTheCommand proves Dispatch.2:
// the command's own run, and so its result stream, is the caller's.
func TestDispatcherContract_runsTheCommand(t *testing.T) {
	for implName, build := range implementations() {
		t.Run(implName, func(t *testing.T) {
			command := newFakeCommand(nil, nil)
			monitor := build(unusedSource(), newRecordingReporter(t, errReport), func() {})
			status, err := monitor.Dispatch(context.Background(), runtimeboundary.Invocation{
				Args:     []string{"clai", "fake"},
				Commands: map[string]cmd.Command{"fake": command},
			})
			if status != 0 || err != nil {
				t.Fatalf("Dispatch = %d, %v; want 0, nil", status, err)
			}
			if !command.ran {
				t.Fatal("command did not run")
			}
		})
	}
}

// recordingReporter is the real routing Reporter over the signal type,
// delivering to a recording fake that runs the Deliverer contract suite.
type recordingReporter struct {
	diagnostic.Reporter
	destination *diagfake.Deliverer
	seen        <-chan struct{}
}

// newRecordingReporter builds a recordingReporter; deliveryErr makes every
// delivery, and so every Report, fail with it.
func newRecordingReporter(t *testing.T, deliveryErr error) *recordingReporter {
	t.Helper()
	destination := diagfake.New()
	if deliveryErr != nil {
		destination = diagfake.Failing(deliveryErr)
	}
	reporter, err := diag.NewReporter(
		[]diagnostic.Type{{Name: signalType, Subsystem: "shutdown", Level: diagnostic.LevelWarn, Mode: diagnostic.Immediate, ActionRequired: true}},
		diag.ResolvedLevels{"": diagnostic.LevelTrace, "shutdown": diagnostic.LevelTrace},
		map[diagnostic.OutputMode]diagnostic.Deliverer{diagnostic.Immediate: destination},
	)
	if err != nil {
		t.Fatalf("build the recording reporter: %v", err)
	}
	return &recordingReporter{Reporter: reporter, destination: destination, seen: destination.Seen()}
}

// snapshot is every event the reporter delivered, whether or not the
// delivery failed.
func (r *recordingReporter) snapshot() []diagnostic.Event {
	return r.destination.Attempts()
}

type fakeSource struct {
	signals chan os.Signal
	stopped chan struct{}
	once    sync.Once
}

func newFakeSource() *fakeSource {
	return &fakeSource{signals: make(chan os.Signal), stopped: make(chan struct{})}
}

func (s *fakeSource) subscribe() SignalSourceFunc {
	return func() (<-chan os.Signal, func(), error) {
		return s.signals, func() { s.once.Do(func() { close(s.stopped) }) }, nil
	}
}

const wait = 5 * time.Second

func receive[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(wait):
		t.Fatalf("timed out waiting for %s", what)
	}
	var zero T
	return zero
}

func send(t *testing.T, ch chan<- os.Signal, sig os.Signal) {
	t.Helper()
	select {
	case ch <- sig:
	case <-time.After(wait):
		t.Fatal("timed out sending a signal")
	}
}

// TestShutdownMonitorSignals proves Monitor.1, Subscribe.1, and NewDispatchMonitor.1.
func TestShutdownMonitorSignals(t *testing.T) {
	for implName, build := range implementations() {
		t.Run(implName+"/Monitor.1 cancel, warn, force-exit, failures on the channel", func(t *testing.T) {
			source := newFakeSource()
			reporter := newRecordingReporter(t, errReport)
			forced := make(chan struct{}, 4)
			cancelled := make(chan struct{}, 4)
			monitor := build(source.subscribe(), reporter, func() { forced <- struct{}{} })
			ctx, stopObserving := context.WithCancel(context.Background())
			defer stopObserving()

			errs, err := monitor.Monitor(ctx, func() { cancelled <- struct{}{} })
			if err != nil {
				t.Fatalf("Monitor: %v", err)
			}

			send(t, source.signals, syscall.SIGINT)
			receive(t, reporter.seen, "first event")
			if got := receive(t, errs, "first failure"); !errors.Is(got, errReport) {
				t.Fatalf("first failure = %v, want errReport", got)
			}
			receive(t, cancelled, "cancel on the first signal")
			if len(forced) != 0 {
				t.Fatal("force exit before the third signal")
			}

			send(t, source.signals, syscall.SIGTERM)
			receive(t, reporter.seen, "second event")
			if got := receive(t, errs, "second failure"); !errors.Is(got, errReport) {
				t.Fatalf("second failure = %v, want errReport", got)
			}
			if len(cancelled) != 0 || len(forced) != 0 {
				t.Fatal("second signal must only warn")
			}

			send(t, source.signals, syscall.SIGINT)
			receive(t, reporter.seen, "third event")
			if got := receive(t, errs, "third failure"); !errors.Is(got, errReport) {
				t.Fatalf("third failure = %v, want errReport", got)
			}
			receive(t, forced, "force exit on the third signal")

			events := reporter.snapshot()
			if len(events) != 3 {
				t.Fatalf("events = %d, want 3", len(events))
			}
			for i, event := range events {
				if event.Type != signalType {
					t.Fatalf("event %d type = %q, want %q", i, event.Type, signalType)
				}
				if event.Message == "" {
					t.Fatalf("event %d has no message", i)
				}
			}
			if events[0].Message == events[1].Message || events[1].Message == events[2].Message {
				t.Fatalf("messages must distinguish cancel, warn, and force exit: %q", []string{events[0].Message, events[1].Message, events[2].Message})
			}

			stopObserving()
			assertClosed(t, errs)
			receive(t, source.stopped, "source stop")
		})

		t.Run(implName+"/Monitor.1 successful delivery sends nothing and the channel closes with ctx", func(t *testing.T) {
			source := newFakeSource()
			reporter := newRecordingReporter(t, nil)
			monitor := build(source.subscribe(), reporter, func() { t.Error("force exit on one signal") })
			ctx, stopObserving := context.WithCancel(context.Background())
			cancelled := make(chan struct{}, 1)
			errs, err := monitor.Monitor(ctx, func() { cancelled <- struct{}{} })
			if err != nil {
				t.Fatalf("Monitor: %v", err)
			}
			send(t, source.signals, syscall.SIGINT)
			receive(t, reporter.seen, "event")
			receive(t, cancelled, "cancel")
			stopObserving()
			assertClosed(t, errs)
			receive(t, source.stopped, "source stop")
		})

		t.Run(implName+"/Monitor.1 an already done ctx closes the channel", func(t *testing.T) {
			source := newFakeSource()
			monitor := build(source.subscribe(), newRecordingReporter(t, nil), func() {})
			ctx, stopObserving := context.WithCancel(context.Background())
			stopObserving()
			errs, err := monitor.Monitor(ctx, func() {})
			if err != nil {
				t.Fatalf("Monitor: %v", err)
			}
			assertClosed(t, errs)
			receive(t, source.stopped, "source stop")
		})

		t.Run(implName+"/Monitor.1 a closed signal channel ends observation", func(t *testing.T) {
			source := newFakeSource()
			monitor := build(source.subscribe(), newRecordingReporter(t, nil), func() {})
			errs, err := monitor.Monitor(context.Background(), func() {})
			if err != nil {
				t.Fatalf("Monitor: %v", err)
			}
			close(source.signals)
			assertClosed(t, errs)
			receive(t, source.stopped, "source stop")
		})

		t.Run(implName+"/Subscribe.1 an unobservable source fails Monitor and starts nothing", func(t *testing.T) {
			reporter := newRecordingReporter(t, nil)
			monitor := build(unusedSource(), reporter, func() { t.Error("force exit") })
			errs, err := monitor.Monitor(context.Background(), func() { t.Error("cancel") })
			if !errors.Is(err, errObserved) {
				t.Fatalf("err = %v, want errObserved", err)
			}
			if errs != nil {
				t.Fatal("channel returned with an error")
			}
			if len(reporter.snapshot()) != 0 {
				t.Fatal("events reported without a source")
			}
		})

		t.Run(implName+"/Subscribe.1 a source without a channel fails Monitor and is stopped", func(t *testing.T) {
			stopped := false
			source := SignalSourceFunc(func() (<-chan os.Signal, func(), error) {
				return nil, func() { stopped = true }, nil
			})
			monitor := build(source, newRecordingReporter(t, nil), func() {})
			errs, err := monitor.Monitor(context.Background(), func() {})
			if err == nil || errs != nil {
				t.Fatalf("Monitor = %v, %v; want nil, error", errs, err)
			}
			if !stopped {
				t.Fatal("source not stopped")
			}
		})
	}
}

// TestShutdownMonitorSignals_missingCollaborators proves NewDispatchMonitor.1:
// a boundary built without its owner's collaborators fails Monitor.
func TestShutdownMonitorSignals_missingCollaborators(t *testing.T) {
	source := newFakeSource()
	tests := map[string]runtimeboundary.DispatchMonitor{
		"signal source": runtimeboundary.NewDispatchMonitor(nil, newRecordingReporter(t, nil), signalType, func() {}),
		"reporter":      runtimeboundary.NewDispatchMonitor(source.subscribe(), nil, signalType, func() {}),
		"signal type":   runtimeboundary.NewDispatchMonitor(source.subscribe(), newRecordingReporter(t, nil), "", func() {}),
		"force exit":    runtimeboundary.NewDispatchMonitor(source.subscribe(), newRecordingReporter(t, nil), signalType, nil),
	}
	for name, monitor := range tests {
		t.Run(name, func(t *testing.T) {
			errs, err := monitor.Monitor(context.Background(), func() {})
			if err == nil || errs != nil {
				t.Fatalf("Monitor = %v, %v; want nil, error", errs, err)
			}
			if !strings.Contains(err.Error(), name) {
				t.Fatalf("err = %q, want it to name %q", err, name)
			}
		})
	}
	t.Run("cancel", func(t *testing.T) {
		monitor := runtimeboundary.NewDispatchMonitor(source.subscribe(), newRecordingReporter(t, nil), signalType, func() {})
		errs, err := monitor.Monitor(context.Background(), nil)
		if err == nil || errs != nil {
			t.Fatalf("Monitor = %v, %v; want nil, error", errs, err)
		}
	})
}

func assertClosed(t *testing.T, errs <-chan error) {
	t.Helper()
	select {
	case err, open := <-errs:
		if open {
			t.Fatalf("unexpected value on the channel: %v", err)
		}
	case <-time.After(wait):
		t.Fatal("channel did not close")
	}
}
