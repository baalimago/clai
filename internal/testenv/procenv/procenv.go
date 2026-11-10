// Package procenv gives tests a run Env over the process streams, read at
// each write, so a test that swaps os.Stdout or os.Stdin still captures what
// a producer writes. Diagnostics go to the current os.Stderr as plain text,
// at the levels the environment selects, as in the CLI.
package procenv

import (
	"context"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/jsonl"
	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/internal/stdio"
	"github.com/baalimago/clai/pkg/diagnostic"
)

type stdoutWriter struct{}

func (stdoutWriter) Write(p []byte) (int, error) { return os.Stdout.Write(p) }

type stderrWriter struct{}

func (stderrWriter) Write(p []byte) (int, error) { return os.Stderr.Write(p) }

type stdinReader struct{}

func (stdinReader) Read(p []byte) (int, error) { return os.Stdin.Read(p) }

// Env returns the process Env. Its levels are resolved from the environment
// when Env is called, as the CLI resolves them once per run, so a test sets
// a legacy control or LOG_LEVEL with t.Setenv before building its context.
// An environment Resolve rejects leaves every subsystem at info.
func Env() runenv.Env {
	streams := stdio.New(stdio.Config{Format: jsonl.FormatText, Stdin: stdinReader{}, Stdout: stdoutWriter{}, Stderr: stderrWriter{}})
	presenter := diag.NewTextPresenter(stderrWriter{}, diag.Plain, diagnostic.LevelTrace)
	types := runenv.Types()
	registry := subsystems(types)
	levels, err := diag.NewLevelResolver().Resolve(diag.LevelRequest{Registry: registry, Legacy: diag.LegacyControls(os.Getenv), LogLevel: os.Getenv("LOG_LEVEL")})
	if err != nil {
		levels = uniformLevels(registry, diagnostic.LevelInfo)
	}
	return runenv.New(runenv.Config{Streams: streams, Reporter: newReporter(types, levels, presenter), Presenter: presenter, Levels: levels, Format: jsonl.FormatText})
}

func subsystems(types []diagnostic.Type) []string {
	var registry []string
	for _, t := range types {
		if !slices.Contains(registry, t.Subsystem) {
			registry = append(registry, t.Subsystem)
		}
	}
	return registry
}

func uniformLevels(registry []string, level diagnostic.Level) diag.ResolvedLevels {
	levels := diag.ResolvedLevels{"": level}
	for _, name := range registry {
		levels[name] = level
	}
	return levels
}

// newReporter is the real Reporter over presenter for both output modes.
func newReporter(types []diagnostic.Type, levels diag.ResolvedLevels, presenter diag.Presenter) diagnostic.Reporter {
	reporter, err := diag.NewReporter(types, levels, map[diagnostic.OutputMode]diagnostic.Deliverer{
		diagnostic.Immediate: presenter,
		diagnostic.Buffered:  presenter,
	})
	if err != nil {
		panic(err)
	}
	return reporter
}

// WithStreams returns an Env over the given streams whose diagnostics are
// plain text on stderr at level.
func WithStreams(stdin io.Reader, stdout, stderr io.Writer, level diagnostic.Level) runenv.Env {
	streams := stdio.New(stdio.Config{Format: jsonl.FormatText, Stdin: stdin, Stdout: stdout, Stderr: stderr})
	presenter := diag.NewTextPresenter(stderr, diag.Plain, level)
	types := runenv.Types()
	levels := uniformLevels(subsystems(types), level)
	return runenv.New(runenv.Config{Streams: streams, Reporter: newReporter(types, levels, presenter), Presenter: presenter, Levels: levels, Format: jsonl.FormatText, Color: false})
}

// Diagnostics returns ctx carrying an Env with empty input, discarded
// output, and diagnostics written to w at level.
func Diagnostics(ctx context.Context, w io.Writer, level diagnostic.Level) context.Context {
	return runenv.With(ctx, WithStreams(strings.NewReader(""), io.Discard, w, level))
}

// Context returns ctx carrying the process Env.
func Context(ctx context.Context) context.Context {
	return runenv.With(ctx, Env())
}

func (stdinReader) Stat() (os.FileInfo, error) { return os.Stdin.Stat() }
