package stdio

import (
	"errors"
	"io"
	"io/fs"
	"log/slog"

	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/jsonl"
)

func init() {
	ErrTextFormat = errors.New("stdio: no encoder under the text format")
}

var errUnknownStream = errors.New("stdio: unknown stream")

// destination is one output stream as the run sees it.
type destination struct {
	writer   io.Writer
	terminal bool
	encoder  jsonl.Encoder
	wrapper  jsonl.Wrapper
}

type provider struct {
	format        jsonl.Format
	stdin         io.Reader
	stdinTerminal bool
	outputs       [2]destination
}

func newProvider(cfg Config) *provider {
	p := &provider{
		format:        cfg.Format,
		stdin:         cfg.Stdin,
		stdinTerminal: cfg.StdinTerminal,
		outputs: [2]destination{
			Stdout: {writer: cfg.Stdout, terminal: cfg.StdoutTerminal},
			Stderr: {writer: cfg.Stderr, terminal: cfg.StderrTerminal},
		},
	}
	if p.format == jsonl.FormatJSON {
		for s, name := range [2]string{Stdout: "stdout", Stderr: "stderr"} {
			out := &p.outputs[s]
			out.encoder = jsonl.NewEncoder(out.writer)
			out.wrapper = jsonl.NewWrapper(out.encoder, "output", slog.String("stream", name))
		}
	}
	return p
}

func (p *provider) output(s Stream) (*destination, error) {
	if int(s) >= len(p.outputs) {
		return nil, diag.Errorf("%w: %d", errUnknownStream, s)
	}
	return &p.outputs[s], nil
}

func (p *provider) Writer(s Stream) io.Writer {
	out, err := p.output(s)
	if err != nil {
		return failingWriter{err: err}
	}
	if p.format == jsonl.FormatJSON {
		return out.wrapper
	}
	return out.writer
}

func (p *provider) Encoder(s Stream) (jsonl.Encoder, error) {
	out, err := p.output(s)
	if err != nil {
		return nil, err
	}
	if p.format != jsonl.FormatJSON {
		return nil, diag.Errorf("stdio: encoder for stream %d: %w", s, ErrTextFormat)
	}
	return out.encoder, nil
}

func (p *provider) IsTerminal(s Stream) bool {
	out, err := p.output(s)
	if err != nil || p.format == jsonl.FormatJSON {
		return false
	}
	return out.terminal
}

func (p *provider) Input() io.Reader {
	if p.format != jsonl.FormatJSON {
		return p.stdin
	}
	reader := flushingReader{provider: p}
	if file, ok := p.stdin.(statter); ok {
		return statFlushingReader{flushingReader: reader, file: file}
	}
	return reader
}

func (p *provider) InputIsTerminal() bool {
	return p.stdinTerminal
}

func (p *provider) Close() error {
	if p.format != jsonl.FormatJSON {
		return nil
	}
	var errs []error
	for _, out := range p.outputs {
		if err := out.wrapper.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return diag.Errorf("stdio: close outputs: %w", errors.Join(errs...))
	}
	return nil
}

// flushingReader makes every text written before a read a record before
// the read starts. (F13)
type flushingReader struct {
	provider *provider
}

func (r flushingReader) Read(b []byte) (int, error) {
	for _, out := range r.provider.outputs {
		if err := out.wrapper.Flush(); err != nil {
			return 0, diag.Errorf("stdio: flush before a read: %w", err)
		}
	}
	return r.provider.stdin.Read(b)
}

type statter interface {
	Stat() (fs.FileInfo, error)
}

// statFlushingReader is the flushingReader of a stdin with a file identity,
// which it keeps, so a reader can tell a terminal, a file, or /dev/null
// from a pipe as it can without -json (F11).
type statFlushingReader struct {
	flushingReader
	file statter
}

func (r statFlushingReader) Stat() (fs.FileInfo, error) {
	return r.file.Stat()
}

type failingWriter struct {
	err error
}

func (w failingWriter) Write([]byte) (int, error) {
	return 0, w.err
}
