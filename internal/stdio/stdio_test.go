package stdio_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/baalimago/clai/internal/jsonl"
	"github.com/baalimago/clai/internal/stdio"
	"github.com/baalimago/clai/internal/testenv"
)

func TestMain(m *testing.M) { testenv.Main(m) }

type providerFactory struct {
	name string
	new  func(cfg stdio.Config) stdio.StreamProvider
}

func providerFactories() []providerFactory {
	return []providerFactory{{name: "New", new: stdio.New}}
}

// recordingWriter keeps every byte written to it, failing every write
// with err when err is set.
type recordingWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
	err error
}

func (w *recordingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return 0, w.err
	}
	return w.buf.Write(p)
}

func (w *recordingWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// observingReader runs before each read, then reads from source.
type observingReader struct {
	before func()
	source io.Reader
	reads  int
}

func (r *observingReader) Read(p []byte) (int, error) {
	r.reads++
	if r.before != nil {
		r.before()
	}
	return r.source.Read(p)
}

type record map[string]any

func decodeLines(t *testing.T, out string) []record {
	t.Helper()
	if out == "" {
		return nil
	}
	if !strings.HasSuffix(out, "\n") {
		t.Fatalf("output does not end in a newline: %q", out)
	}
	var records []record
	for line := range strings.SplitSeq(strings.TrimSuffix(out, "\n"), "\n") {
		var r record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("line %q is not one JSON object: %v", line, err)
		}
		if !strings.HasPrefix(line, `{"type":`) {
			t.Fatalf("line %q does not start with its type", line)
		}
		records = append(records, r)
	}
	return records
}

type fixture struct {
	stdout, stderr *recordingWriter
	stdin          *observingReader
	cfg            stdio.Config
}

func newFixture(format jsonl.Format, input string) *fixture {
	f := &fixture{
		stdout: &recordingWriter{},
		stderr: &recordingWriter{},
		stdin:  &observingReader{source: strings.NewReader(input)},
	}
	f.cfg = stdio.Config{Format: format, Stdin: f.stdin, Stdout: f.stdout, Stderr: f.stderr}
	return f
}

func terminalCombinations() []stdio.Config {
	var combos []stdio.Config
	for mask := range 8 {
		combos = append(combos, stdio.Config{
			StdinTerminal:  mask&1 != 0,
			StdoutTerminal: mask&2 != 0,
			StderrTerminal: mask&4 != 0,
		})
	}
	return combos
}

func TestStreamProviderContract(t *testing.T) {
	for _, factory := range providerFactories() {
		t.Run(factory.name, func(t *testing.T) {
			runContract(t, factory)
		})
	}
}

func runContract(t *testing.T, factory providerFactory) {
	t.Run("Writer.1 text format returns the configured writers", func(t *testing.T) {
		f := newFixture(jsonl.FormatText, "")
		p := factory.new(f.cfg)
		if p.Writer(stdio.Stdout) != io.Writer(f.stdout) {
			t.Fatal("Writer(Stdout) is not the configured stdout")
		}
		if p.Writer(stdio.Stderr) != io.Writer(f.stderr) {
			t.Fatal("Writer(Stderr) is not the configured stderr")
		}
		fmt.Fprint(p.Writer(stdio.Stdout), "\x1b[1mbold\x1b[0m\r\nno newline")
		fmt.Fprint(p.Writer(stdio.Stderr), "err text")
		if got := f.stdout.String(); got != "\x1b[1mbold\x1b[0m\r\nno newline" {
			t.Fatalf("stdout = %q, want the text unchanged", got)
		}
		if got := f.stderr.String(); got != "err text" {
			t.Fatalf("stderr = %q, want the text unchanged", got)
		}
	})

	t.Run("Writer.1 JSON format writes output records per destination", func(t *testing.T) {
		f := newFixture(jsonl.FormatJSON, "")
		p := factory.new(f.cfg)
		fmt.Fprint(p.Writer(stdio.Stdout), "one\n\x1b[31mtwo\x1b[0m\n")
		fmt.Fprint(p.Writer(stdio.Stderr), "warn\n")
		assertRecords(t, f.stdout.String(), []record{
			{"type": "output", "stream": "stdout", "text": "one"},
			{"type": "output", "stream": "stdout", "text": "two"},
		})
		assertRecords(t, f.stderr.String(), []record{
			{"type": "output", "stream": "stderr", "text": "warn"},
		})
	})

	t.Run("Encoder.1 JSON format returns one Encoder per destination", func(t *testing.T) {
		f := newFixture(jsonl.FormatJSON, "")
		p := factory.new(f.cfg)
		outFirst, err := p.Encoder(stdio.Stdout)
		if err != nil {
			t.Fatalf("Encoder(Stdout): %v", err)
		}
		outSecond, err := p.Encoder(stdio.Stdout)
		if err != nil {
			t.Fatalf("Encoder(Stdout) again: %v", err)
		}
		if outFirst != outSecond {
			t.Fatal("Encoder(Stdout) returned a different Encoder on the second call")
		}
		errFirst, err := p.Encoder(stdio.Stderr)
		if err != nil {
			t.Fatalf("Encoder(Stderr): %v", err)
		}
		errSecond, _ := p.Encoder(stdio.Stderr)
		if errFirst != errSecond {
			t.Fatal("Encoder(Stderr) returned a different Encoder on the second call")
		}
		if outFirst == errFirst {
			t.Fatal("stdout and stderr share one Encoder")
		}
		if err := outFirst.Encode(jsonl.Record{Type: "reply", Fields: []slog.Attr{slog.String("text", "hi")}}); err != nil {
			t.Fatalf("Encode on stdout: %v", err)
		}
		if err := errFirst.Encode(jsonl.Record{Type: "diagnostic"}); err != nil {
			t.Fatalf("Encode on stderr: %v", err)
		}
		assertRecords(t, f.stdout.String(), []record{{"type": "reply", "text": "hi"}})
		assertRecords(t, f.stderr.String(), []record{{"type": "diagnostic"}})
	})

	t.Run("Encoder.1 text format returns ErrTextFormat", func(t *testing.T) {
		f := newFixture(jsonl.FormatText, "")
		p := factory.new(f.cfg)
		for _, s := range []stdio.Stream{stdio.Stdout, stdio.Stderr} {
			enc, err := p.Encoder(s)
			if enc != nil {
				t.Fatalf("Encoder(%d) = %v, want nil", s, enc)
			}
			if !errors.Is(err, stdio.ErrTextFormat) {
				t.Fatalf("Encoder(%d) error = %v, want ErrTextFormat", s, err)
			}
		}
	})

	t.Run("IsTerminal.1 InputIsTerminal.1 every terminal combination", func(t *testing.T) {
		for _, format := range []jsonl.Format{jsonl.FormatText, jsonl.FormatJSON} {
			for _, combo := range terminalCombinations() {
				f := newFixture(format, "")
				cfg := f.cfg
				cfg.StdinTerminal, cfg.StdoutTerminal, cfg.StderrTerminal = combo.StdinTerminal, combo.StdoutTerminal, combo.StderrTerminal
				p := factory.new(cfg)
				wantOut, wantErr := combo.StdoutTerminal, combo.StderrTerminal
				if format == jsonl.FormatJSON {
					wantOut, wantErr = false, false
				}
				if got := p.IsTerminal(stdio.Stdout); got != wantOut {
					t.Errorf("format %d %+v: IsTerminal(Stdout) = %v, want %v", format, combo, got, wantOut)
				}
				if got := p.IsTerminal(stdio.Stderr); got != wantErr {
					t.Errorf("format %d %+v: IsTerminal(Stderr) = %v, want %v", format, combo, got, wantErr)
				}
				if got := p.InputIsTerminal(); got != combo.StdinTerminal {
					t.Errorf("format %d %+v: InputIsTerminal() = %v, want %v", format, combo, got, combo.StdinTerminal)
				}
			}
		}
	})

	t.Run("Input.1 text format reads the configured stdin unchanged", func(t *testing.T) {
		f := newFixture(jsonl.FormatText, "answer\n")
		p := factory.new(f.cfg)
		fmt.Fprint(p.Writer(stdio.Stdout), "Name? ")
		got, err := io.ReadAll(p.Input())
		if err != nil {
			t.Fatalf("ReadAll: %v", err)
		}
		if string(got) != "answer\n" {
			t.Fatalf("read %q, want %q", got, "answer\n")
		}
		if out := f.stdout.String(); out != "Name? " {
			t.Fatalf("stdout = %q, want the prompt unchanged", out)
		}
	})

	t.Run("Input.1 JSON format a prompt without newline is a record before the read", func(t *testing.T) {
		f := newFixture(jsonl.FormatJSON, "Ada\n")
		p := factory.new(f.cfg)
		var seenOut, seenErr string
		f.stdin.before = func() {
			if f.stdin.reads == 1 {
				seenOut, seenErr = f.stdout.String(), f.stderr.String()
			}
		}
		fmt.Fprint(p.Writer(stdio.Stdout), "Name? ")
		fmt.Fprint(p.Writer(stdio.Stderr), "hint: ")
		line, err := bufio.NewReader(p.Input()).ReadString('\n')
		if err != nil {
			t.Fatalf("ReadString: %v", err)
		}
		if line != "Ada\n" {
			t.Fatalf("read %q, want %q", line, "Ada\n")
		}
		assertRecords(t, seenOut, []record{{"type": "output", "stream": "stdout", "text": "Name? "}})
		assertRecords(t, seenErr, []record{{"type": "output", "stream": "stderr", "text": "hint: "}})
		fmt.Fprint(p.Writer(stdio.Stdout), "\n")
		if err := p.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		assertRecords(t, f.stdout.String(), []record{{"type": "output", "stream": "stdout", "text": "Name? "}})
	})

	t.Run("Input.1 every format keeps the stdin's file identity", func(t *testing.T) {
		devNull, err := os.Open(os.DevNull)
		if err != nil {
			t.Fatalf("open %s: %v", os.DevNull, err)
		}
		defer devNull.Close()
		want, err := devNull.Stat()
		if err != nil {
			t.Fatalf("stat %s: %v", os.DevNull, err)
		}
		for _, format := range []jsonl.Format{jsonl.FormatText, jsonl.FormatJSON} {
			f := newFixture(format, "")
			f.cfg.Stdin = devNull
			in := factory.new(f.cfg).Input()
			statter, ok := in.(interface{ Stat() (os.FileInfo, error) })
			if !ok {
				t.Fatalf("format %v: Input() of a file has no Stat", format)
			}
			got, err := statter.Stat()
			if err != nil {
				t.Fatalf("format %v: Stat: %v", format, err)
			}
			if !os.SameFile(got, want) || got.Mode() != want.Mode() {
				t.Fatalf("format %v: Stat = %v %v, want %s", format, got.Name(), got.Mode(), os.DevNull)
			}
		}
		f := newFixture(jsonl.FormatJSON, "")
		if _, ok := factory.new(f.cfg).Input().(interface{ Stat() (os.FileInfo, error) }); ok {
			t.Fatal("Input() of a reader without Stat has a Stat")
		}
	})

	t.Run("Input.1 JSON format a failed flush fails the read and reads nothing", func(t *testing.T) {
		flushErr := errors.New("stdout gone")
		f := newFixture(jsonl.FormatJSON, "never read\n")
		f.stdout.err = flushErr
		p := factory.new(f.cfg)
		if _, err := fmt.Fprint(p.Writer(stdio.Stdout), "Name? "); err != nil {
			t.Fatalf("Write without newline: %v", err)
		}
		buf := make([]byte, 16)
		n, err := p.Input().Read(buf)
		if !errors.Is(err, flushErr) {
			t.Fatalf("Read error = %v, want the flush error", err)
		}
		if n != 0 {
			t.Fatalf("Read returned %d bytes, want 0", n)
		}
		if f.stdin.reads != 0 {
			t.Fatalf("stdin was read %d times after a failed flush", f.stdin.reads)
		}
	})

	t.Run("Input.1 Writer.1 Encoder.1 concurrent writers and a reader", func(t *testing.T) {
		const writers, lines = 4, 50
		f := newFixture(jsonl.FormatJSON, strings.Repeat("y\n", lines))
		p := factory.new(f.cfg)
		var wg sync.WaitGroup
		for w := range writers {
			for _, s := range []stdio.Stream{stdio.Stdout, stdio.Stderr} {
				wg.Go(func() {
					for i := range lines {
						fmt.Fprintf(p.Writer(s), "text %d %d\n", w, i)
					}
				})
				wg.Go(func() {
					enc, err := p.Encoder(s)
					if err != nil {
						t.Errorf("Encoder(%d): %v", s, err)
						return
					}
					for i := range lines {
						if err := enc.Encode(jsonl.Record{Type: "progress", Fields: []slog.Attr{slog.Int("writer", w), slog.Int("i", i)}}); err != nil {
							t.Errorf("Encode: %v", err)
						}
					}
				})
			}
		}
		wg.Go(func() {
			if _, err := io.ReadAll(p.Input()); err != nil {
				t.Errorf("ReadAll: %v", err)
			}
		})
		wg.Wait()
		if err := p.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		for name, out := range map[string]string{"stdout": f.stdout.String(), "stderr": f.stderr.String()} {
			records := decodeLines(t, out)
			if len(records) != 2*writers*lines {
				t.Fatalf("%s has %d records, want %d", name, len(records), 2*writers*lines)
			}
			for _, r := range records {
				if r["type"] == "output" {
					if r["stream"] != name {
						t.Fatalf("%s record %v names the wrong stream", name, r)
					}
					if !strings.HasPrefix(r["text"].(string), "text ") {
						t.Fatalf("%s record %v has a broken line", name, r)
					}
				}
			}
		}
	})

	t.Run("Close.1 JSON format emits pending text on both destinations", func(t *testing.T) {
		f := newFixture(jsonl.FormatJSON, "")
		p := factory.new(f.cfg)
		fmt.Fprint(p.Writer(stdio.Stdout), "tail")
		fmt.Fprint(p.Writer(stdio.Stderr), "err tail")
		if err := p.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		assertRecords(t, f.stdout.String(), []record{{"type": "output", "stream": "stdout", "text": "tail"}})
		assertRecords(t, f.stderr.String(), []record{{"type": "output", "stream": "stderr", "text": "err tail"}})
		if _, err := fmt.Fprint(p.Writer(stdio.Stdout), "late\n"); !errors.Is(err, jsonl.ErrWrapperClosed) {
			t.Fatalf("Write after Close error = %v, want ErrWrapperClosed", err)
		}
	})

	t.Run("Close.1 JSON format joins the errors of failing destinations", func(t *testing.T) {
		outErr, errErr := errors.New("stdout broken"), errors.New("stderr broken")
		f := newFixture(jsonl.FormatJSON, "")
		f.stdout.err, f.stderr.err = outErr, errErr
		p := factory.new(f.cfg)
		fmt.Fprint(p.Writer(stdio.Stdout), "a")
		fmt.Fprint(p.Writer(stdio.Stderr), "b")
		err := p.Close()
		if !errors.Is(err, outErr) || !errors.Is(err, errErr) {
			t.Fatalf("Close error = %v, want both destination errors", err)
		}
	})

	t.Run("Close.1 text format does nothing", func(t *testing.T) {
		f := newFixture(jsonl.FormatText, "")
		f.stdout.err = errors.New("unused")
		p := factory.new(f.cfg)
		if err := p.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if f.stderr.String() != "" {
			t.Fatalf("Close wrote %q", f.stderr.String())
		}
	})

	t.Run("New.1 an unknown stream is neither a writer nor an Encoder", func(t *testing.T) {
		for _, format := range []jsonl.Format{jsonl.FormatText, jsonl.FormatJSON} {
			f := newFixture(format, "")
			f.cfg.StdoutTerminal, f.cfg.StderrTerminal = true, true
			p := factory.new(f.cfg)
			unknown := stdio.Stream(7)
			if _, err := p.Writer(unknown).Write([]byte("x\n")); err == nil {
				t.Fatalf("format %d: Write to an unknown stream succeeded", format)
			}
			if enc, err := p.Encoder(unknown); enc != nil || err == nil {
				t.Fatalf("format %d: Encoder(unknown) = %v, %v, want nil and an error", format, enc, err)
			}
			if p.IsTerminal(unknown) {
				t.Fatalf("format %d: an unknown stream is a terminal", format)
			}
			if f.stdout.String() != "" || f.stderr.String() != "" {
				t.Fatalf("format %d: an unknown stream wrote to a configured one", format)
			}
		}
	})

	t.Run("New.1 the package never reads the process streams or the environment", func(t *testing.T) {
		files, err := filepath.Glob("*.go")
		if err != nil {
			t.Fatalf("Glob: %v", err)
		}
		fset := token.NewFileSet()
		for _, name := range files {
			if strings.HasSuffix(name, "_test.go") {
				continue
			}
			file, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatalf("parse %s: %v", name, err)
			}
			for _, spec := range file.Imports {
				path, _ := strconv.Unquote(spec.Path.Value)
				if path == "os" || path == "syscall" || strings.HasPrefix(path, "golang.org/x/term") {
					t.Fatalf("%s imports %s", name, path)
				}
			}
		}
		if _, err := os.Stat("stdio.go"); err != nil {
			t.Fatalf("stdio.go: %v", err)
		}
	})
}

func assertRecords(t *testing.T, out string, want []record) {
	t.Helper()
	got := decodeLines(t, out)
	if len(got) != len(want) {
		t.Fatalf("got %d records %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if len(got[i]) != len(want[i]) {
			t.Fatalf("record %d = %v, want %v", i, got[i], want[i])
		}
		for k, v := range want[i] {
			if got[i][k] != v {
				t.Fatalf("record %d = %v, want %v", i, got[i], want[i])
			}
		}
	}
}
