// Package runenv carries one run's streams and diagnostic reporting on the
// context, so every producer writes through the run's StreamProvider and
// reports through its Reporter instead of reaching the process streams.
// The composition root (internal/cli) builds the Env; a context without one
// yields a silent Env, so library callers stay quiet (F3).
package runenv

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"

	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/display"
	"github.com/baalimago/clai/internal/jsonl"
	"github.com/baalimago/clai/internal/stdio"
	"github.com/baalimago/clai/pkg/diagnostic"
)

// Config is what the composition root supplies for one run.
type Config struct {
	Streams   stdio.StreamProvider
	Reporter  diagnostic.Reporter
	Presenter diag.Presenter
	Levels    diag.ResolvedLevels
	Format    jsonl.Format
	// Color is the text producers' color choice; it is false under FormatJSON.
	Color bool
	// Children tracks the relays of child output the run waits for before
	// it closes its streams; nil tracks none.
	Children *Children
	// Row is where the producer of the reply says whether it left stdout
	// mid-row; nil keeps no such state.
	Row *OpenRow
}

// OpenRow is whether the run's stdout was left mid-row by streamed text, so
// a notice written to the same terminal can start on a row of its own.
type OpenRow struct{ open atomic.Bool }

// NewOpenRow returns the state of a stdout at the start of a row.
func NewOpenRow() *OpenRow { return &OpenRow{} }

// End reports whether the row was open, and marks it ended.
func (r *OpenRow) End() bool { return r.open.Swap(false) }

// MarkRow records whether text just written to stdout left its row open.
// Without a Row in the Env it does nothing.
func (e Env) MarkRow(open bool) {
	if e.cfg.Row != nil {
		e.cfg.Row.open.Store(open)
	}
}

// Env is the run's view of its streams and diagnostics.
type Env struct {
	cfg Config
}

type envKey struct{}

// New returns the Env for cfg. A nil Streams becomes a silent text provider.
func New(cfg Config) Env {
	if cfg.Streams == nil {
		cfg.Streams = stdio.New(stdio.Config{Format: jsonl.FormatText, Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard})
	}
	if cfg.Format == jsonl.FormatJSON {
		cfg.Color = false
	}
	return Env{cfg: cfg}
}

// Text returns an Env that writes text to stdout and stderr, reads stdin, and
// reports nothing. Tests and library callers use it to give producers their
// own writers.
func Text(stdin io.Reader, stdout, stderr io.Writer) Env {
	return New(Config{Streams: stdio.New(stdio.Config{Format: jsonl.FormatText, Stdin: stdin, Stdout: stdout, Stderr: stderr})})
}

// With returns ctx carrying env.
func With(ctx context.Context, env Env) context.Context {
	return context.WithValue(ctx, envKey{}, env)
}

// Lookup returns the Env on ctx, and false when ctx carries none.
func Lookup(ctx context.Context) (Env, bool) {
	if ctx == nil {
		return Env{}, false
	}
	env, ok := ctx.Value(envKey{}).(Env)
	return env, ok
}

// From returns the Env on ctx, or a silent Env when there is none.
func From(ctx context.Context) Env {
	if ctx != nil {
		if env, ok := ctx.Value(envKey{}).(Env); ok {
			return env
		}
	}
	return New(Config{})
}

// Streams returns the run's StreamProvider.
func (e Env) Streams() stdio.StreamProvider { return e.cfg.Streams }

// Stdout is the run's result writer.
func (e Env) Stdout() io.Writer { return e.cfg.Streams.Writer(stdio.Stdout) }

// Stderr is the run's diagnostic and progress writer.
func (e Env) Stderr() io.Writer { return e.cfg.Streams.Writer(stdio.Stderr) }

// Stdin is the run's input.
func (e Env) Stdin() io.Reader { return e.cfg.Streams.Input() }

// StdinIsTerminal reports the configured stdin terminal state.
func (e Env) StdinIsTerminal() bool { return e.cfg.Streams.InputIsTerminal() }

// IsTerminal reports whether s is a terminal; always false under FormatJSON.
func (e Env) IsTerminal(s stdio.Stream) bool { return e.cfg.Streams.IsTerminal(s) }

// JSON reports whether the run uses the JSON format.
func (e Env) JSON() bool { return e.cfg.Format == jsonl.FormatJSON }

// Color reports whether text producers may color their output.
func (e Env) Color() bool { return e.cfg.Color }

// Displayer returns the JSON Displayer of s. Under FormatText it returns an
// error matching stdio.ErrTextFormat: text producers draw instead.
func (e Env) Displayer(s stdio.Stream) (display.Displayer, error) {
	enc, err := e.cfg.Streams.Encoder(s)
	if err != nil {
		return nil, err
	}
	return display.NewJSONDisplayer(enc), nil
}

// Attach hands v to the Presenter so diagnostics are placed above it; the
// returned function detaches it. Without a Presenter it does nothing.
func (e Env) Attach(v diag.Viewport) (detach func()) {
	if e.cfg.Presenter == nil || v == nil {
		return func() {}
	}
	return e.cfg.Presenter.Attach(v)
}

// Enabled reports whether an event of subsystem at level would be shown,
// using the longest dotted match of the resolved levels. Without levels only
// errors and warnings are enabled.
func (e Env) Enabled(subsystem string, level diagnostic.Level) bool {
	if e.cfg.Reporter == nil {
		return false
	}
	name := strings.ToLower(subsystem)
	for {
		if resolved, ok := e.cfg.Levels[name]; ok {
			return level <= resolved
		}
		cut := strings.LastIndex(name, ".")
		if cut < 0 {
			break
		}
		name = name[:cut]
	}
	if resolved, ok := e.cfg.Levels[""]; ok {
		return level <= resolved
	}
	return level <= diagnostic.LevelWarn
}

// Report delivers one event of a registered type. A failed Report changes
// nothing for the producer (F6); the composition's Reporter decides what an
// undeliverable event means.
func (e Env) Report(ctx context.Context, typeName, message string, facts ...slog.Attr) {
	if e.cfg.Reporter == nil {
		return
	}
	_ = e.cfg.Reporter.Report(ctx, diagnostic.Event{Type: typeName, Message: strings.TrimRight(message, "\n"), Facts: facts})
}

// Warnf reports a warning of subsystem.
func Warnf(ctx context.Context, subsystem, format string, args ...any) {
	From(ctx).Report(ctx, TypeName(subsystem, KindWarning), fmt.Sprintf(format, args...))
}

// Noticef reports an informational notice of subsystem.
func Noticef(ctx context.Context, subsystem, format string, args ...any) {
	From(ctx).Report(ctx, TypeName(subsystem, KindNotice), fmt.Sprintf(format, args...))
}

// Failuref reports a non-terminal error of subsystem; a terminal error is
// returned to the composition instead.
func Failuref(ctx context.Context, subsystem, format string, args ...any) {
	From(ctx).Report(ctx, TypeName(subsystem, KindError), fmt.Sprintf(format, args...))
}

// Debugf reports debug detail of subsystem. The message is formatted only
// when the subsystem's level shows it.
func Debugf(ctx context.Context, subsystem, format string, args ...any) {
	env := From(ctx)
	if !env.Enabled(subsystem, diagnostic.LevelDebug) {
		return
	}
	env.Report(ctx, TypeName(subsystem, KindDebug), fmt.Sprintf(format, args...))
}

// DebugEnabled reports whether debug detail of subsystem is shown, for
// producers whose detail is costly to build.
func DebugEnabled(ctx context.Context, subsystem string) bool {
	return From(ctx).Enabled(subsystem, diagnostic.LevelDebug)
}

// EditorRefusedError is the JSON format's refusal of $EDITOR, the one child
// that takes over the terminal (F13). Path is the file the user can edit
// directly, when there is one.
type EditorRefusedError struct {
	Path string
}

func (e EditorRefusedError) Error() string {
	if e.Path == "" {
		return "$EDITOR is not started under -json"
	}
	return fmt.Sprintf("$EDITOR is not started under -json; edit %s directly", e.Path)
}

// RefuseEditor returns the EditorRefusedError for path under FormatJSON,
// before the editor starts, and nil under FormatText.
func (e Env) RefuseEditor(path string) error {
	if !e.JSON() {
		return nil
	}
	return EditorRefusedError{Path: path}
}

// TablePrompter is what a go_away_boilerplate table on s shows its
// selection prompt with: under FormatJSON a select prompt record of the
// prompt line (F13, F14); under FormatText nil, so the table writes the
// line itself.
func (e Env) TablePrompter(s stdio.Stream) func(line string) error {
	if !e.JSON() {
		return nil
	}
	return func(line string) error {
		return e.ShowPrompt(s, display.Prompt{Kind: "select", Text: strings.TrimSpace(line)})
	}
}

// ShowPrompt shows a question on s before its answer is read: a "prompt"
// record under FormatJSON, the text and a newline under FormatText (F13).
func (e Env) ShowPrompt(s stdio.Stream, p display.Prompt) error {
	if e.JSON() {
		d, err := e.Displayer(s)
		if err != nil {
			return err
		}
		return d.ShowPrompt(p)
	}
	if _, err := fmt.Fprintln(e.cfg.Streams.Writer(s), p.Text); err != nil {
		return diag.Errorf("show prompt: %w", err)
	}
	return nil
}
