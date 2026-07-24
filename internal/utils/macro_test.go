package utils

import (
	"io"
	"strings"
	"testing"
)

func TestNewMacroReader_NilForEmpty(t *testing.T) {
	if got := NewMacroReader(nil); got != nil {
		t.Fatalf("NewMacroReader(nil) = %v, want nil", got)
	}
	if got := NewMacroReader([]string{}); got != nil {
		t.Fatalf("NewMacroReader([]) = %v, want nil", got)
	}
}

func TestNewMacroReader_ContentOrdering(t *testing.T) {
	r := NewMacroReader([]string{"3", "d", "0:5"})
	if r == nil {
		t.Fatal("expected non-nil reader for non-empty inputs")
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	content := string(data)

	// Verify real inputs come first, in order, each on its own line.
	if !strings.HasPrefix(content, "3\nd\n0:5\n") {
		t.Fatalf("expected prefix '3\\nd\\n0:5\\n', got %q", content)
	}

	// Verify trailing quits are present.
	if !strings.HasSuffix(content, "q\n") {
		t.Fatalf("expected trailing 'q\\n', got suffix of %q", content)
	}

	// Verify exactly 10 trailing "q\n" terminators.
	qCount := strings.Count(content, "q\n")
	if qCount != 10 {
		t.Fatalf("expected 10 trailing 'q\\n' terminators, got %d; full content: %q", qCount, content)
	}

	// Verify no quits appear before the real inputs are consumed.
	firstQ := strings.Index(content, "q\n")
	expectedFirstQ := strings.Index(content[strings.Index(content, "0:5\n"):], "q\n") + strings.Index(content, "0:5\n")
	if firstQ != expectedFirstQ {
		t.Fatalf("first 'q\\n' at position %d, want %d", firstQ, expectedFirstQ)
	}
}

func TestNewMacroReader_SingleInput(t *testing.T) {
	r := NewMacroReader([]string{"hello"})
	if r == nil {
		t.Fatal("expected non-nil reader")
	}
	data, _ := io.ReadAll(r)
	content := string(data)
	if !strings.HasPrefix(content, "hello\n") {
		t.Fatalf("expected 'hello\\n' prefix, got %q", content)
	}
	if strings.Count(content, "q\n") != 10 {
		t.Fatalf("expected 10 trailing quits, got %q", content)
	}
}

func TestNewMacroReader_LiveMode_NavigatesThenEOF(t *testing.T) {
	oldLive := Live
	Live = true
	t.Cleanup(func() { Live = oldLive })

	r := NewMacroReader([]string{"0", "3", "1"})
	if r == nil {
		t.Fatal("expected non-nil reader for non-empty inputs")
	}

	lr, ok := r.(*liveReader)
	if !ok {
		t.Fatalf("expected *liveReader when Live=true, got %T", r)
	}
	if lr.drained {
		t.Fatal("reader should not be drained before any read")
	}

	// Read the full buffer — should contain inputs + newlines, then EOF.
	// (We can't read from os.Stdin in tests, but we verify the buffer content.)
	buf := make([]byte, 32)
	n, err := lr.buf.Read(buf)
	if err != nil {
		t.Fatalf("unexpected error reading buffer: %v", err)
	}
	content := string(buf[:n])
	if !strings.HasPrefix(content, "0\n3\n1\n") {
		t.Fatalf("expected '0\\n3\\n1\\n', got %q", content)
	}

	// Exhaust the remaining buffer.
	_, err = io.ReadAll(lr.buf)
	if err != nil {
		t.Fatalf("unexpected error draining buffer: %v", err)
	}
}

func TestNewMacroReader_LiveMode_TransitionsToDrained(t *testing.T) {
	oldLive := Live
	Live = true
	t.Cleanup(func() { Live = oldLive })

	r := NewMacroReader([]string{"x"})
	lr := r.(*liveReader)

	// Read all buffered content.
	data, err := io.ReadAll(lr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != "x\n" {
		t.Fatalf("expected 'x\\n', got %q", string(data))
	}

	// After draining, the reader should be in drained state.
	if !lr.drained {
		t.Fatal("reader should be drained after io.ReadAll")
	}
}
