package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/baalimago/clai/internal"
	procstdin "github.com/baalimago/clai/internal/cli/process"
	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/interrupt"
	"github.com/baalimago/clai/internal/jsonl"
	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/internal/runtimeboundary"
	"github.com/baalimago/clai/internal/stdio"
	"github.com/baalimago/clai/internal/term"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/clai/pkg/agent"
	"github.com/baalimago/clai/pkg/diagnostic"
	"github.com/baalimago/go_away_boilerplate/pkg/cmd"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
	"github.com/baalimago/go_away_boilerplate/pkg/table"
)

// sharedBufferSize is the Shared buffer size parameter (README Parameters).
const sharedBufferSize diag.ByteSize = 1 << 20

// spillDirName is the Spill-file directory under the clai cache directory.
const spillDirName = "diagnostics"

// childDrainBound is how long a run waits, after it cancels its children,
// for their output relays before it closes its streams. A cancelled MCP
// server's process group is killed, so its stderr ends well within it; only
// a descendant that left the group can hold the pipe open longer.
const childDrainBound = 2 * time.Second

// process is everything Run reads from the process once: its streams, their
// terminal states, the environment, and its signals.
type process struct {
	stdin          io.Reader
	stdout, stderr io.Writer
	stdinTerminal  bool
	stdoutTerminal bool
	stderrTerminal bool
	getenv         func(string) string
	signals        runtimeboundary.SignalSource
	forceExit      func()
	// startupWarnings were found before the run's diagnostics existed.
	startupWarnings []string
}

func osProcess() process {
	stdin, stdinTerminal := procstdin.Stdin()
	return process{
		stdin:          stdin,
		stdout:         os.Stdout,
		stderr:         os.Stderr,
		stdinTerminal:  stdinTerminal,
		stdoutTerminal: term.IsTerminal(os.Stdout),
		stderrTerminal: term.IsTerminal(os.Stderr),
		getenv:         os.Getenv,
		signals:        osSignals{},
		forceExit:      func() { os.Exit(1) },
	}
}

// osSignals is the composition's SignalSource: the process interrupt and
// termination signals.
type osSignals struct{}

func (osSignals) Subscribe() (<-chan os.Signal, func(), error) {
	ch := make(chan os.Signal, 3)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	return ch, func() { signal.Stop(ch) }, nil
}

// spellsJSONFlag reports whether arg is the -json flag, with or without a
// value.
func spellsJSONFlag(arg string) bool {
	name, _, _ := strings.Cut(arg, "=")
	return name == "-json" || name == "--json"
}

// jsonFlagFormat is the format the -json flag arg selects. Like Go's bool
// flags it takes no value or one strconv.ParseBool reads, so -json=false
// selects text; any other value is an error.
func jsonFlagFormat(arg string) (jsonl.Format, error) {
	_, value, hasValue := strings.Cut(arg, "=")
	if !hasValue {
		return jsonl.FormatJSON, nil
	}
	on, err := strconv.ParseBool(value)
	if err != nil {
		return jsonl.FormatText, diag.Errorf("invalid boolean value %q for -json: %w", value, err)
	}
	if on {
		return jsonl.FormatJSON, nil
	}
	return jsonl.FormatText, nil
}

// scanFormat finds -json where command flags are parsed, before the command
// or among its flags before its first positional argument, and removes it;
// elsewhere it is text (synopsis). A -json value that is not a bool stops
// the scan with the format found so far and the error.
func scanFormat(args []string, cmds map[string]cmd.Command) (jsonl.Format, []string, error) {
	valueFlags := valueFlagNames(cmds)
	format := jsonl.FormatText
	out := make([]string, 0, len(args))
	commandSeen := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" || arg == "-" || !strings.HasPrefix(arg, "-") {
			if commandSeen || arg == "--" || arg == "-" {
				out = append(out, args[i:]...)
				return format, out, nil
			}
			commandSeen = true
			out = append(out, arg)
			continue
		}
		if spellsJSONFlag(arg) {
			selected, err := jsonFlagFormat(arg)
			if err != nil {
				return format, out, err
			}
			format = selected
			continue
		}
		out = append(out, arg)
		name := strings.TrimLeft(arg, "-")
		if !strings.Contains(name, "=") && valueFlags[name] && i+1 < len(args) {
			i++
			out = append(out, args[i])
		}
	}
	return format, out, nil
}

func valueFlagNames(cmds map[string]cmd.Command) map[string]bool {
	names := map[string]bool{}
	var walk func(map[string]cmd.Command)
	walk = func(cmds map[string]cmd.Command) {
		for _, c := range cmds {
			fs := c.Flagset()
			if fs == nil {
				continue
			}
			fs.VisitAll(func(f *flag.Flag) {
				if b, ok := f.Value.(interface{ IsBoolFlag() bool }); !ok || !b.IsBoolFlag() {
					names[f.Name] = true
				}
			})
			if sc, ok := c.(cmd.Subcommander); ok {
				walk(sc.Subcommands())
			}
		}
	}
	walk(cmds)
	return names
}

// levelRegistry holds every registered Type.Subsystem: the CLI's and the
// agent's, which include every subsystem the Legacy controls table names.
func levelRegistry(types []diagnostic.Type) []string {
	var registry []string
	for _, t := range types {
		if !slices.Contains(registry, t.Subsystem) {
			registry = append(registry, t.Subsystem)
		}
	}
	return registry
}

// cliTypes is the CLI's type registry: its own types and the agent's, each
// once; the agent's include the internal registry its runs report through.
func cliTypes() []diagnostic.Type {
	types := runenv.Types()
	for _, agentType := range agent.DiagnosticTypes() {
		if !slices.ContainsFunc(types, func(registered diagnostic.Type) bool { return registered.Name == agentType.Name }) {
			types = append(types, agentType)
		}
	}
	return types
}

// undeliverablePanic aborts the run when a diagnostic can be reported
// nowhere (F6); every other failed Report leaves the operation running.
type undeliverablePanic struct {
	reporter diagnostic.Reporter
}

func (r undeliverablePanic) Report(ctx context.Context, event diagnostic.Event) error {
	err := r.reporter.Report(ctx, event)
	if errors.Is(err, diag.ErrUndeliverable) {
		panic(fmt.Sprintf("clai: diagnostic output failed: %v", err))
	}
	return err
}

// composition is one run's diagnostic and stream wiring.
type composition struct {
	streams   stdio.StreamProvider
	presenter diag.Presenter
	buffered  diag.BufferedDeliverer
	reporter  diagnostic.Reporter
	env       runenv.Env
	children  *runenv.Children
	// row is whether streamed reply text left stdout mid-row.
	row *runenv.OpenRow
}

func newPresenter(streams stdio.StreamProvider, format jsonl.Format, p process, level diagnostic.Level) diag.Presenter {
	if format == jsonl.FormatJSON {
		enc, err := streams.Encoder(stdio.Stderr)
		if err == nil {
			return diag.NewJSONPresenter(enc, level)
		}
	}
	color := diag.Plain
	if p.stderrTerminal && colorAllowed(p) {
		color = diag.Colored
	}
	return diag.NewTextPresenter(streams.Writer(stdio.Stderr), color, level)
}

// colorAllowed is the one NO_COLOR rule of the run: any value misc.Truthy
// accepts turns colour off (phase 6 Deliver.3), as it does for the table
// library's own colours.
func colorAllowed(p process) bool {
	return !misc.Truthy(p.getenv("NO_COLOR"))
}

// compose resolves the levels and builds the run's Presenter, buffer,
// Reporter and Env. A level failure returns the Presenter that reports it.
func compose(p process, format jsonl.Format) (composition, error) {
	streams := stdio.New(stdio.Config{
		Format:         format,
		Stdin:          p.stdin,
		Stdout:         p.stdout,
		Stderr:         p.stderr,
		StdinTerminal:  p.stdinTerminal,
		StdoutTerminal: p.stdoutTerminal,
		StderrTerminal: p.stderrTerminal,
	})
	types := cliTypes()
	levels, err := diag.NewLevelResolver().Resolve(diag.LevelRequest{
		Registry: levelRegistry(types),
		Legacy:   diag.LegacyControls(p.getenv),
		LogLevel: p.getenv("LOG_LEVEL"),
	})
	if err != nil {
		return composition{streams: streams, presenter: newPresenter(streams, format, p, diagnostic.LevelInfo)}, diag.Errorf("resolve diagnostic levels: %w", err)
	}
	presenter := newPresenter(streams, format, p, levels[""])
	spillDir := ""
	if cacheDir, cacheErr := utils.GetClaiCacheDir(); cacheErr == nil {
		spillDir = filepath.Join(cacheDir, spillDirName)
	}
	buffered := diag.NewBufferedDeliverer(spillDir, sharedBufferSize, presenter)
	routing, err := diag.NewReporter(types, levels, map[diagnostic.OutputMode]diagnostic.Deliverer{
		diagnostic.Immediate: presenter,
		diagnostic.Buffered:  buffered,
	})
	if err != nil {
		return composition{streams: streams, presenter: presenter}, diag.Errorf("build the diagnostic reporter: %w", err)
	}
	reporter := undeliverablePanic{reporter: routing}
	children := runenv.NewChildren()
	row := runenv.NewOpenRow()
	env := runenv.New(runenv.Config{
		Streams:   streams,
		Reporter:  reporter,
		Presenter: presenter,
		Levels:    levels,
		Format:    format,
		Color:     format == jsonl.FormatText && colorAllowed(p),
		Children:  children,
		Row:       row,
	})
	return composition{streams: streams, presenter: presenter, buffered: buffered, reporter: reporter, env: env, children: children, row: row}, nil
}

// finish ends the run's children and waits, up to childDrainBound, for
// their relays, then flushes the buffer and the JSON Wrappers. An
// undeliverable buffer aborts the run (F6).
func (c composition) finish(ctx context.Context, cancel context.CancelFunc) {
	cancel()
	if c.children != nil {
		bound, stop := context.WithTimeout(context.WithoutCancel(ctx), childDrainBound)
		if err := c.children.Wait(bound); err != nil {
			runenv.Warnf(context.WithoutCancel(ctx), runenv.SubsystemClai, "child output after the run ended is dropped: %v", err)
		}
		stop()
	}
	if c.buffered != nil {
		if err := c.buffered.Finish(context.WithoutCancel(ctx)); errors.Is(err, diag.ErrUndeliverable) {
			panic(fmt.Sprintf("clai: diagnostic output failed: %v", err))
		}
	}
	_ = c.streams.Close()
}

// Streams are the process streams and terminal states RunWithStreams uses
// instead of the process's own.
type Streams struct {
	Stdin                                         io.Reader
	Stdout, Stderr                                io.Writer
	StdinTerminal, StdoutTerminal, StderrTerminal bool
}

// RunWithStreams is Run over the given streams, with the process's
// environment and signals.
func RunWithStreams(args []string, deps Deps, s Streams) int {
	p := osProcess()
	p.stdin, p.stdout, p.stderr = s.Stdin, s.Stdout, s.Stderr
	p.stdinTerminal, p.stdoutTerminal, p.stderrTerminal = s.StdinTerminal, s.StdoutTerminal, s.StderrTerminal
	return run(args, deps, p)
}

// run is Run over an explicit process.
func run(args []string, deps Deps, p process) int {
	ctx, cancelCause := context.WithCancelCause(context.Background())
	cancel := func() { cancelCause(nil) }
	defer cancel()
	// Nested tool calls stop clai through this cancel.
	ctx = context.WithValue(ctx, utils.ContextCancelKey, cancel)
	cfgDir, _ := utils.GetClaiConfigDir()
	cacheDir, _ := utils.GetClaiCacheDir()
	usage := strings.NewReplacer("SUBSYSTEMS\n", subsystemTable(runenv.Subsystems()), "CONFIG_DIR", cfgDir, "CACHE_DIR", cacheDir).Replace(usageTemplate)
	ctx, exit := internal.WithExitStatus(ctx)
	// The remote resolver's notices go to the run's stderr once it exists.
	var notices io.Writer = io.Discard
	// rerun re-enters dispatch inside this run, so a hidden alias keeps the
	// run's streams, its -json format and its diagnostics.
	var rerun func(context.Context, []string) int
	cmds := withHelp(commands(deps, exit.Set, func() io.Writer { return notices },
		func(ctx context.Context, args []string) int { return rerun(ctx, args) }), usage)
	format, args, formatErr := scanFormat(args, cmds)
	args = helpAsCommand(versionFlagAsCommand(args))

	c, err := compose(p, format)
	if err != nil {
		_ = c.presenter.ReportError(ctx, err)
		_ = c.streams.Close()
		return 1
	}
	ctx = runenv.With(ctx, c.env)
	notices = c.env.Stderr()
	defer c.finish(ctx, cancel)
	defer routeStandardLog(ctx)()
	for _, warning := range p.startupWarnings {
		runenv.Warnf(ctx, runenv.SubsystemClai, "%s", warning)
	}

	signals := &firstSignal{source: p.signals}
	if format == jsonl.FormatText && p.stdoutTerminal && p.stderrTerminal {
		// Both streams share the terminal: the notice of the first signal
		// starts on its own row, not glued to a partial reply.
		signals.onFirst = func() {
			if c.row.End() {
				_, _ = io.WriteString(c.streams.Writer(stdio.Stderr), "\n")
			}
		}
	}
	monitor := runtimeboundary.NewDispatchMonitor(signals, c.reporter, runenv.TypeSignal, p.forceExit)
	rerun = func(ctx context.Context, args []string) int {
		status, err := monitor.Dispatch(ctx, runtimeboundary.Invocation{Args: append([]string{"clai"}, args...), Commands: cmds})
		return exit.Resolve(presentOutcome(ctx, c, int(status), err, !cmd.IsCommandFailure(err), usage, cmds))
	}
	// The monitor outlives the first signal's cancel, so its warn and
	// force-exit steps still run (phase 7 Monitor.1). The first signal is
	// the run's cancel cause, so every producer can tell an interrupt from
	// a finished run.
	observeCtx, stopObserving := context.WithCancel(context.WithoutCancel(ctx))
	failures, err := monitor.Monitor(observeCtx, func() {
		cancelCause(&interrupt.SignalError{Signal: signals.received()})
	})
	if err != nil {
		runenv.Warnf(ctx, runenv.SubsystemClai, "signals are not observed: %v", err)
	}
	defer func() {
		stopObserving()
		for range failures {
		}
	}()

	if formatErr != nil {
		return exit.Resolve(presentOutcome(ctx, c, 1, formatErr, true, usage, cmds))
	}
	if err := checkArgs(args, cmds); err != nil {
		_ = c.presenter.ReportError(ctx, withHintAsRemedy(err))
		return 1
	}
	status, err := monitor.Dispatch(ctx, runtimeboundary.Invocation{Args: append([]string{"clai"}, args...), Commands: cmds})
	if interrupted, ok := interrupt.Signal(context.Cause(ctx)); ok {
		// The run's terminal error is the interruption itself, once, never
		// the failures the cancelled context caused; a failure that is not
		// the interruption, such as a failed persist, is shown beside it.
		presentOutcome(ctx, c, int(status), errors.Join(interrupted, withoutInterruption(err)), false, usage, cmds)
		return int(interrupted.ExitStatus())
	}
	return exit.Resolve(presentOutcome(ctx, c, int(status), err, !cmd.IsCommandFailure(err), usage, cmds))
}

// firstSignal is the run's SignalSource: it passes every signal of source
// on and remembers the first one, which is why the run stops.
type firstSignal struct {
	source runtimeboundary.SignalSource
	// onFirst runs once, before the first signal is passed on.
	onFirst func()
	mu      sync.Mutex
	first   os.Signal
}

func (f *firstSignal) Subscribe() (<-chan os.Signal, func(), error) {
	signals, stop, err := f.source.Subscribe()
	if err != nil || signals == nil {
		return signals, stop, err
	}
	out := make(chan os.Signal, cap(signals))
	done := make(chan struct{})
	go func() {
		defer close(out)
		for {
			select {
			case sig, open := <-signals:
				if !open {
					return
				}
				f.record(sig)
				select {
				case out <- sig:
				case <-done:
					return
				}
			case <-done:
				return
			}
		}
	}()
	var once sync.Once
	return out, func() {
		once.Do(func() { close(done) })
		if stop != nil {
			stop()
		}
	}, nil
}

func (f *firstSignal) record(sig os.Signal) {
	f.mu.Lock()
	isFirst := f.first == nil
	if isFirst {
		f.first = sig
	}
	f.mu.Unlock()
	if isFirst && f.onFirst != nil {
		f.onFirst()
	}
}

func (f *firstSignal) received() os.Signal {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.first
}

// withoutInterruption is err without the parts that only say the run's
// context ended: after a signal the interruption is reported as itself.
// Parts joined beside it, such as a failed persist, stay.
func withoutInterruption(err error) error {
	if err == nil {
		return nil
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok && reflect.TypeOf(err) == joinType {
		var kept []error
		for _, part := range joined.Unwrap() {
			if rest := withoutInterruption(part); rest != nil {
				kept = append(kept, rest)
			}
		}
		return errors.Join(kept...)
	}
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// joinType is the type errors.Join returns.
var joinType = reflect.TypeOf(errors.Join(io.EOF))

// presentOutcome shows what Dispatch returned: nothing on success or a user
// exit, the help text on a help request, the error and the usage on a
// dispatch failure, and the terminal error once on a setup or run failure.
func presentOutcome(ctx context.Context, c composition, status int, err error, dispatchFailure bool, usage string, cmds map[string]cmd.Command) int {
	if err == nil || errors.Is(err, cmd.ErrUserInitiatedExit) || errors.Is(err, table.ErrUserInitiatedExit) {
		return status
	}
	stdout := c.env.Stdout()
	if help, ok := errors.AsType[cmd.HelpRequestedError](err); ok {
		fmt.Fprintln(stdout, help.Help)
		return status
	}
	printUsage := func() {
		fmt.Fprintf(stdout, usage, commandTable(withCompletionCommand(cmds)))
	}
	if errors.Is(err, cmd.ErrNoArgs) {
		printUsage()
		return status
	}
	_ = c.presenter.ReportError(ctx, withHintAsRemedy(err))
	if dispatchFailure {
		printUsage()
	}
	return status
}

func withCompletionCommand(cmds map[string]cmd.Command) map[string]cmd.Command {
	all := make(map[string]cmd.Command, len(cmds)+1)
	maps.Copy(all, cmds)
	if _, ok := all["completion"]; !ok {
		all["completion"] = cmd.NewCompletionCommand("clai")
	}
	return all
}
