package mcp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/jsonl"
	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/internal/stdio"
)

func TestStdioConnRelaysStderrAsChildOutputUnderJSON(t *testing.T) {
	var out, errb bytes.Buffer
	env := runenv.New(runenv.Config{
		Streams: stdio.New(stdio.Config{Format: jsonl.FormatJSON, Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb}),
		Format:  jsonl.FormatJSON,
	})
	c, _ := newBareStdioConn()
	done := make(chan struct{})
	c.readStderr(runenv.With(context.Background(), env), strings.NewReader("starting up\n\n\x1b[31mred\x1b[0m line\n"), nil, done)
	<-done

	want := `{"type":"child_output","source":"mcp_srv","stream":"stderr","text":"starting up"}` + "\n" +
		`{"type":"child_output","source":"mcp_srv","stream":"stderr","text":"\u001b[31mred\u001b[0m line"}` + "\n"
	if got := errb.String(); got != want {
		t.Fatalf("stderr =\n%s\nwant\n%s", got, want)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want nothing", out.String())
	}
}

func TestStdioConnRelaysStderrAsTextWithoutJSON(t *testing.T) {
	var out, errb bytes.Buffer
	ctx := runenv.With(context.Background(), runenv.Text(strings.NewReader(""), &out, &errb))
	c, _ := newBareStdioConn()
	done := make(chan struct{})
	c.readStderr(ctx, strings.NewReader("starting up\nready\n"), nil, done)
	<-done

	if got, want := errb.String(), "mcp_srv: starting up\nmcp_srv: ready\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want nothing", out.String())
	}
}

// A04: the run waits for the stderr relay before it closes its streams, so a
// line the server wrote is a record before the run ends, and nothing writes
// to the run's stderr after Wait returns.
func TestStdioConnHoldsTheRunUntilItsStderrIsRelayed(t *testing.T) {
	var out, errb bytes.Buffer
	children := runenv.NewChildren()
	env := runenv.New(runenv.Config{
		Streams:  stdio.New(stdio.Config{Format: jsonl.FormatJSON, Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb}),
		Format:   jsonl.FormatJSON,
		Children: children,
	})
	ctx, cancel := context.WithCancel(runenv.With(context.Background(), env))
	defer cancel()
	stdinR, stdinW := io.Pipe()
	defer stdinR.Close()
	stdoutR, stdoutW := io.Pipe()
	defer stdoutW.Close()
	stderrR, stderrW := io.Pipe()
	newStdioConn(ctx, "late", stdioTransport{stdin: stdinW, stdout: stdoutR, stderr: stderrR, wait: func() {}}, nil)

	bound, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stop()
	if err := children.Wait(bound); !errors.Is(err, runenv.ErrChildrenStillWriting) {
		t.Fatalf("Wait with stderr open = %v, want ErrChildrenStillWriting", err)
	}

	go func() {
		_, _ = io.WriteString(stderrW, "last words\n")
		_ = stderrW.Close()
	}()
	cancel()
	if err := children.Wait(context.Background()); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	want := `{"type":"child_output","source":"mcp_late","stream":"stderr","text":"last words"}` + "\n"
	if got := errb.String(); got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}
