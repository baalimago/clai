package jsonl_test

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"testing"

	"github.com/baalimago/clai/internal/jsonl"
)

type wrapperFactory struct {
	name string
	new  func(enc jsonl.Encoder, recordType string, fields ...slog.Attr) jsonl.Wrapper
}

func wrapperFactories() []wrapperFactory {
	return []wrapperFactory{{name: "NewWrapper", new: jsonl.NewWrapper}}
}

// recordingEncoder keeps every record it is given, failing from the
// failAt-th call on when failAt is positive.
type recordingEncoder struct {
	mu      sync.Mutex
	records []jsonl.Record
	calls   int
	failAt  int
}

var errEncoder = errors.New("encoder failed")

func (e *recordingEncoder) Encode(r jsonl.Record) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls++
	if e.failAt > 0 && e.calls >= e.failAt {
		return errEncoder
	}
	e.records = append(e.records, r)
	return nil
}

func (e *recordingEncoder) texts(t *testing.T) []string {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	texts := make([]string, 0, len(e.records))
	for _, r := range e.records {
		last := r.Fields[len(r.Fields)-1]
		if last.Key != "text" {
			t.Fatalf("record %v: last field is %q, want \"text\"", r, last.Key)
		}
		texts = append(texts, last.Value.String())
	}
	return texts
}

func writeAll(t *testing.T, w jsonl.Wrapper, chunks ...string) {
	t.Helper()
	for _, chunk := range chunks {
		n, err := w.Write([]byte(chunk))
		if err != nil || n != len(chunk) {
			t.Fatalf("Write(%q) = %d, %v; want %d, nil", chunk, n, err, len(chunk))
		}
	}
}

func assertTexts(t *testing.T, enc *recordingEncoder, want ...string) {
	t.Helper()
	if want == nil {
		want = []string{}
	}
	if got := enc.texts(t); !slices.Equal(got, want) {
		t.Fatalf("record texts = %q, want %q", got, want)
	}
}

func TestWrapperContract(t *testing.T) {
	for _, factory := range wrapperFactories() {
		t.Run(factory.name, func(t *testing.T) {
			wrapperContract(t, factory)
		})
	}
}

func wrapperContract(t *testing.T, factory wrapperFactory) {
	newOutput := func() (*recordingEncoder, jsonl.Wrapper) {
		enc := &recordingEncoder{}
		return enc, factory.new(enc, "output", slog.String("stream", "stdout"))
	}

	t.Run("Write.1 NewWrapper.1 records carry type, fields, then text", func(t *testing.T) {
		enc := &recordingEncoder{}
		w := factory.new(enc, "child_output", slog.String("source", "go"), slog.String("stream", "stderr"))
		writeAll(t, w, "first\nsecond\n")
		if len(enc.records) != 2 {
			t.Fatalf("got %d records, want 2: %v", len(enc.records), enc.records)
		}
		for i, text := range []string{"first", "second"} {
			r := enc.records[i]
			if r.Type != "child_output" {
				t.Fatalf("record %d type = %q", i, r.Type)
			}
			keys := []string{}
			for _, field := range r.Fields {
				keys = append(keys, field.Key)
			}
			if !slices.Equal(keys, []string{"source", "stream", "text"}) {
				t.Fatalf("record %d keys = %q", i, keys)
			}
			if r.Fields[0].Value.String() != "go" || r.Fields[1].Value.String() != "stderr" || r.Fields[2].Value.String() != text {
				t.Fatalf("record %d = %v", i, r)
			}
		}
	})

	t.Run("Write.1 NewWrapper.1 through the real Encoder", func(t *testing.T) {
		var out bytes.Buffer
		w := factory.new(jsonl.NewEncoder(&out), "output", slog.String("stream", "stderr"))
		writeAll(t, w, "hello \"you\"\n")
		want := `{"type":"output","stream":"stderr","text":"hello \"you\""}` + "\n"
		if out.String() != want {
			t.Fatalf("wire = %q, want %q", out.String(), want)
		}
	})

	t.Run("Write.1 lines split across writes", func(t *testing.T) {
		enc, w := newOutput()
		writeAll(t, w, "par", "tial", " line\nnext", "\n", "\n")
		assertTexts(t, enc, "partial line", "next", "")
	})

	t.Run("Write.1 multibyte rune split across writes", func(t *testing.T) {
		enc, w := newOutput()
		rune3 := []byte("ö→")
		writeAll(t, w, string(rune3[:1]), string(rune3[1:3]), string(rune3[3:])+"\n")
		assertTexts(t, enc, "ö→")
	})

	t.Run("Write.1 no record before a line completes", func(t *testing.T) {
		enc, w := newOutput()
		writeAll(t, w, "no newline yet")
		assertTexts(t, enc)
	})

	t.Run("Write.2 ANSI CSI and OSC sequences are dropped", func(t *testing.T) {
		enc, w := newOutput()
		writeAll(t, w,
			"\x1b[1;31mred\x1b[0m plain\n",
			"\x1b]8;;https://example.com\x07link\x1b]8;;\x07\n",
			"\x1b]0;title\x1b\\after title\n",
			"\x1b[2K\x1b[?25lcleared\n",
		)
		assertTexts(t, enc, "red plain", "link", "after title", "cleared")
	})

	t.Run("Write.2 escape sequences with intermediate bytes are dropped", func(t *testing.T) {
		enc, w := newOutput()
		writeAll(t, w, "x\x1b(Bplain\x1b[m\n", "a\x1b7b\x1b8c\n", "d\x1b#8e\x1b", "(", "0f\x1b=g\n")
		assertTexts(t, enc, "xplain", "abc", "defg")
	})

	t.Run("Write.2 an escape cut by a line end is dropped", func(t *testing.T) {
		enc, w := newOutput()
		writeAll(t, w, "a\x1b\nb\x1b(\nc\n")
		assertTexts(t, enc, "a", "b", "c")
	})

	t.Run("Write.2 a sequence split across writes is dropped", func(t *testing.T) {
		enc, w := newOutput()
		writeAll(t, w, "a\x1b", "[3", "8;5;12", "mb\x1b]0;t", "itle\x1b", "\\c\n")
		assertTexts(t, enc, "abc")
	})

	t.Run("Write.2 a line ending inside a sequence is not lost", func(t *testing.T) {
		enc, w := newOutput()
		writeAll(t, w, "cut\x1b[12\nnext\x1b]0;unterminated\nlast\n")
		assertTexts(t, enc, "cut", "next", "last")
	})

	t.Run("Write.2 a spinner redrawn with \\r keeps the last frame", func(t *testing.T) {
		enc, w := newOutput()
		writeAll(t, w, "⠋ loading\r", "⠙ loading\r", "⠹ loading\r\x1b[2K", "done\n")
		assertTexts(t, enc, "done")
	})

	t.Run("Write.2 a redraw that leaves an empty line is a line", func(t *testing.T) {
		enc, w := newOutput()
		writeAll(t, w, "frame\r\x1b[K\n")
		assertTexts(t, enc, "")
	})

	t.Run("Write.2 \\r\\n is a line terminator", func(t *testing.T) {
		enc, w := newOutput()
		writeAll(t, w, "crlf\r\nsecond\r\n")
		assertTexts(t, enc, "crlf", "second")
	})

	t.Run("Write.2 \\r ending one write and \\n starting the next are \\r\\n", func(t *testing.T) {
		enc, w := newOutput()
		writeAll(t, w, "split\r", "\nafter\r", "redrawn\n")
		assertTexts(t, enc, "split", "redrawn")
	})

	t.Run("Write.3 returns len(p) and nil", func(t *testing.T) {
		_, w := newOutput()
		p := []byte("one\ntwo\npartial")
		n, err := w.Write(p)
		if n != len(p) || err != nil {
			t.Fatalf("Write = %d, %v; want %d, nil", n, err, len(p))
		}
	})

	t.Run("Write.3 returns the Encoder's error", func(t *testing.T) {
		enc := &recordingEncoder{failAt: 2}
		w := factory.new(enc, "output", slog.String("stream", "stdout"))
		p := []byte("ok\nfails\nnever\n")
		n, err := w.Write(p)
		if !errors.Is(err, errEncoder) {
			t.Fatalf("Write error = %v, want %v", err, errEncoder)
		}
		if n >= len(p) {
			t.Fatalf("Write = %d with an error, want fewer than %d", n, len(p))
		}
		assertTexts(t, enc, "ok")
	})

	t.Run("Flush.1 emits a prompt without a newline", func(t *testing.T) {
		enc, w := newOutput()
		writeAll(t, w, "\x1b[1mName:\x1b[0m ")
		if err := w.Flush(); err != nil {
			t.Fatalf("Flush: %v", err)
		}
		assertTexts(t, enc, "Name: ")
		writeAll(t, w, "\n", "Hello\n")
		assertTexts(t, enc, "Name: ", "Hello")
	})

	t.Run("Flush.1 the rest of a flushed line is its own record", func(t *testing.T) {
		enc, w := newOutput()
		writeAll(t, w, "Continue? ")
		if err := w.Flush(); err != nil {
			t.Fatalf("Flush: %v", err)
		}
		writeAll(t, w, "[y/N]\n")
		assertTexts(t, enc, "Continue? ", "[y/N]")
	})

	t.Run("Flush.1 with nothing pending writes nothing", func(t *testing.T) {
		enc, w := newOutput()
		if err := w.Flush(); err != nil {
			t.Fatalf("Flush: %v", err)
		}
		writeAll(t, w, "line\n", "\x1b[0m")
		if err := w.Flush(); err != nil {
			t.Fatalf("Flush: %v", err)
		}
		assertTexts(t, enc, "line")
		if enc.calls != 1 {
			t.Fatalf("Encode called %d times, want 1", enc.calls)
		}
	})

	t.Run("Flush.1 returns the Encoder's error", func(t *testing.T) {
		enc := &recordingEncoder{failAt: 1}
		w := factory.new(enc, "output")
		writeAll(t, w, "pending")
		if err := w.Flush(); !errors.Is(err, errEncoder) {
			t.Fatalf("Flush error = %v, want %v", err, errEncoder)
		}
	})

	t.Run("Close.1 flushes a trailing partial line, then refuses writes", func(t *testing.T) {
		enc, w := newOutput()
		writeAll(t, w, "whole\ntrailing")
		if err := w.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		assertTexts(t, enc, "whole", "trailing")
		n, err := w.Write([]byte("late\n"))
		if n != 0 || !errors.Is(err, jsonl.ErrWrapperClosed) {
			t.Fatalf("Write after Close = %d, %v; want 0, ErrWrapperClosed", n, err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("second Close: %v", err)
		}
		if err := w.Flush(); err != nil {
			t.Fatalf("Flush after Close: %v", err)
		}
		assertTexts(t, enc, "whole", "trailing")
	})

	t.Run("Close.1 returns the Encoder's error and still closes", func(t *testing.T) {
		enc := &recordingEncoder{failAt: 1}
		w := factory.new(enc, "output")
		writeAll(t, w, "pending")
		if err := w.Close(); !errors.Is(err, errEncoder) {
			t.Fatalf("Close error = %v, want %v", err, errEncoder)
		}
		if _, err := w.Write([]byte("x")); !errors.Is(err, jsonl.ErrWrapperClosed) {
			t.Fatalf("Write after Close = %v, want ErrWrapperClosed", err)
		}
	})

	t.Run("Write.4 concurrent Write and Flush keep lines whole and calls contiguous", func(t *testing.T) {
		enc, w := newOutput()
		const writers, calls = 4, 50
		var wg sync.WaitGroup
		stop := make(chan struct{})
		flushErr := make(chan error, 1)
		go func() {
			defer close(flushErr)
			for {
				select {
				case <-stop:
					return
				default:
				}
				if err := w.Flush(); err != nil {
					flushErr <- err
					return
				}
			}
		}()
		writeErrs := make(chan error, writers)
		for g := range writers {
			wg.Go(func() {
				for c := range calls {
					call := fmt.Sprintf("g%d-c%d-a\n\x1b[32mg%d-c%d-b\x1b[0m\n", g, c, g, c)
					if _, err := w.Write([]byte(call)); err != nil {
						writeErrs <- err
						return
					}
				}
			})
		}
		wg.Wait()
		close(stop)
		close(writeErrs)
		for err := range writeErrs {
			t.Fatalf("Write: %v", err)
		}
		if err := <-flushErr; err != nil {
			t.Fatalf("Flush: %v", err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		texts := enc.texts(t)
		if len(texts) != writers*calls*2 {
			t.Fatalf("got %d records, want %d", len(texts), writers*calls*2)
		}
		seen := map[string]bool{}
		for i := 0; i < len(texts); i += 2 {
			var g, c int
			if _, err := fmt.Sscanf(texts[i], "g%d-c%d-a", &g, &c); err != nil {
				t.Fatalf("record %d = %q is not a call's first line: %v", i, texts[i], err)
			}
			if want := fmt.Sprintf("g%d-c%d-b", g, c); texts[i+1] != want {
				t.Fatalf("record %d = %q, want %q right after %q", i+1, texts[i+1], want, texts[i])
			}
			if seen[texts[i]] {
				t.Fatalf("line %q recorded twice", texts[i])
			}
			seen[texts[i]] = true
		}
	})

	t.Run("Write.4 a concurrent Flush never splits a line another Write completes", func(t *testing.T) {
		enc, w := newOutput()
		const lines = 200
		var wg sync.WaitGroup
		wg.Add(2)
		writeErr := make(chan error, 1)
		go func() {
			defer wg.Done()
			defer close(writeErr)
			for i := range lines {
				if _, err := w.Write(fmt.Appendf(nil, "line-%03d\n", i)); err != nil {
					writeErr <- err
					return
				}
			}
		}()
		flushErr := make(chan error, 1)
		go func() {
			defer wg.Done()
			defer close(flushErr)
			for range lines {
				if err := w.Flush(); err != nil {
					flushErr <- err
					return
				}
			}
		}()
		wg.Wait()
		if err := <-writeErr; err != nil {
			t.Fatalf("Write: %v", err)
		}
		if err := <-flushErr; err != nil {
			t.Fatalf("Flush: %v", err)
		}
		want := make([]string, lines)
		for i := range lines {
			want[i] = fmt.Sprintf("line-%03d", i)
		}
		assertTexts(t, enc, want...)
	})

	t.Run("Write.4 Close concurrent with Write loses no accepted line", func(t *testing.T) {
		enc, w := newOutput()
		const writers, calls = 4, 50
		var wg sync.WaitGroup
		accepted := make(chan string, writers*calls)
		unexpected := make(chan error, writers)
		for g := range writers {
			wg.Go(func() {
				for c := range calls {
					line := fmt.Sprintf("g%d-c%d", g, c)
					_, err := w.Write([]byte(line + "\n"))
					switch {
					case err == nil:
						accepted <- line
					case errors.Is(err, jsonl.ErrWrapperClosed):
						return
					default:
						unexpected <- err
						return
					}
				}
			})
		}
		if err := w.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		wg.Wait()
		close(accepted)
		close(unexpected)
		for err := range unexpected {
			t.Fatalf("Write: %v", err)
		}
		want := map[string]bool{}
		for line := range accepted {
			want[line] = true
		}
		got := map[string]bool{}
		for _, text := range enc.texts(t) {
			got[text] = true
		}
		if len(got) != len(want) {
			t.Fatalf("recorded %d distinct lines, accepted %d", len(got), len(want))
		}
		for line := range want {
			if !got[line] {
				t.Fatalf("accepted line %q has no record", line)
			}
		}
	})
}
