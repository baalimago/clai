package setup

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/internal/testenv/procenv"
	"github.com/baalimago/clai/pkg/diagnostic"
	"github.com/baalimago/go_away_boilerplate/pkg/table"
)

// signalBuffer is a concurrency-safe stdout that signals once its content
// contains want.
type signalBuffer struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	want   string
	seen   chan struct{}
	closed bool
}

func newSignalBuffer(want string) *signalBuffer {
	return &signalBuffer{want: want, seen: make(chan struct{})}
}

func (b *signalBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n, err := b.buf.Write(p)
	if !b.closed && strings.Contains(b.buf.String(), b.want) {
		b.closed = true
		close(b.seen)
	}
	return n, err
}

func (b *signalBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestInitCmd_RunsWriteToTheirOwnStdout(t *testing.T) {
	old := Input
	Input = nil
	t.Cleanup(func() { Input = old })

	firstIn, firstInWriter := io.Pipe()
	t.Cleanup(func() { _ = firstInWriter.Close() })
	firstOut := newSignalBuffer(stage0Raw)
	firstCtx := withStreams(context.Background(), firstIn, firstOut)

	firstErr := make(chan error, 1)
	go func() { firstErr <- InitCmd(firstCtx) }()

	select {
	case <-firstOut.seen:
	case <-time.After(10 * time.Second):
		t.Fatal("first run never printed the category menu")
	}

	secondOut := newSignalBuffer(stage0Raw)
	secondCtx := withStreams(context.Background(), strings.NewReader("q\n"), secondOut)
	if err := InitCmd(secondCtx); !errors.Is(err, table.ErrUserInitiatedExit) {
		t.Fatalf("second run: got %v, want user exit", err)
	}

	go func() { _, _ = io.WriteString(firstInWriter, "0\nq\n") }()
	select {
	case err := <-firstErr:
		if !errors.Is(err, table.ErrUserInitiatedExit) {
			t.Fatalf("first run: got %v, want user exit", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("first run never finished")
	}

	const firstRunOnly = "Configs in general config"
	if !strings.Contains(firstOut.String(), firstRunOnly) {
		t.Fatalf("first run stdout lacks %q:\n%s", firstRunOnly, firstOut.String())
	}
	if strings.Contains(secondOut.String(), firstRunOnly) {
		t.Fatalf("second run stdout received the first run's output %q:\n%s", firstRunOnly, secondOut.String())
	}
	if !strings.Contains(secondOut.String(), stage0Raw) {
		t.Fatalf("second run stdout lacks its own menu %q:\n%s", stage0Raw, secondOut.String())
	}
}

func withStreams(ctx context.Context, stdin io.Reader, stdout io.Writer) context.Context {
	return runenv.With(ctx, procenv.WithStreams(stdin, stdout, io.Discard, diagnostic.LevelError))
}
