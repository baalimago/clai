package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

func newBareStdioConn() (*StdioConn, *bytes.Buffer) {
	var out bytes.Buffer
	c := &StdioConn{
		serverName: "srv",
		stdin:      nopWriteCloser{&out},
		enc:        json.NewEncoder(&out),
		readBound:  4096,
		pending:    map[int]chan pendingResult{},
		notifyCh:   make(chan string, 8),
	}
	return c, &out
}

func TestBoundedLineReader(t *testing.T) {
	t.Run("clamps tiny bounds", func(t *testing.T) {
		r := newBoundedLineReader(strings.NewReader("abc\n"), 1)
		line, err := r.readLine()
		if err != nil || string(line) != "abc" {
			t.Fatalf("readLine = %q, %v", line, err)
		}
	})

	t.Run("returns trailing data without a newline", func(t *testing.T) {
		r := newBoundedLineReader(strings.NewReader("abc"), 16)
		line, err := r.readLine()
		if err != nil || string(line) != "abc" {
			t.Fatalf("readLine = %q, %v", line, err)
		}
	})

	t.Run("resynchronises after an oversized line", func(t *testing.T) {
		oversized := strings.Repeat("x", 64)
		r := newBoundedLineReader(strings.NewReader(oversized+"\nnext\n"), 16)
		if _, err := r.readLine(); !errors.Is(err, errFrameTooLarge) {
			t.Fatalf("err = %v, want errFrameTooLarge", err)
		}
		line, err := r.readLine()
		if err != nil || string(line) != "next" {
			t.Fatalf("readLine = %q, %v", line, err)
		}
	})
}

func TestStdioConnHandleLineBranches(t *testing.T) {
	t.Run("undecodable frame fails all pending", func(t *testing.T) {
		c, _ := newBareStdioConn()
		ch := make(chan pendingResult, 1)
		c.pending[1] = ch
		c.handleLine([]byte("{not json"))
		if (<-ch).err == nil {
			t.Fatal("expected pending callers to be failed")
		}
	})

	t.Run("missing jsonrpc fails all pending", func(t *testing.T) {
		c, _ := newBareStdioConn()
		ch := make(chan pendingResult, 1)
		c.pending[1] = ch
		c.handleLine([]byte(`{"id":1}`))
		if res := <-ch; !errors.Is(res.err, errFrameNotJSONRPC) {
			t.Fatalf("pending result = %+v, want invalid JSON-RPC frame error", res)
		}
	})

	t.Run("server notification is published", func(t *testing.T) {
		c, _ := newBareStdioConn()
		c.handleLine([]byte(`{"jsonrpc":"2.0","method":"notifications/tools/list_changed"}`))
		select {
		case method := <-c.Notifications():
			if method != "notifications/tools/list_changed" {
				t.Fatalf("method = %q", method)
			}
		default:
			t.Fatal("expected a published notification")
		}
	})

	t.Run("server request is answered with method not found", func(t *testing.T) {
		c, out := newBareStdioConn()
		c.handleLine([]byte(`{"jsonrpc":"2.0","id":4,"method":"ping"}`))
		if !strings.Contains(out.String(), "-32601") {
			t.Fatalf("expected a method-not-found response, got %q", out.String())
		}
	})

	t.Run("server request with a non-numeric id is ignored", func(t *testing.T) {
		c, out := newBareStdioConn()
		c.handleLine([]byte(`{"jsonrpc":"2.0","id":"x","method":"ping"}`))
		if out.Len() != 0 {
			t.Fatalf("expected no response, got %q", out.String())
		}
	})

	t.Run("response without id is ignored", func(t *testing.T) {
		c, _ := newBareStdioConn()
		ch := make(chan pendingResult, 1)
		c.pending[1] = ch
		c.handleLine([]byte(`{"jsonrpc":"2.0","result":{}}`))
		if len(c.pending) != 1 || len(ch) != 0 {
			t.Fatalf("response without id changed pending calls: pending=%v results=%d", c.pending, len(ch))
		}
	})

	t.Run("response with a non-numeric id is ignored", func(t *testing.T) {
		c, _ := newBareStdioConn()
		ch := make(chan pendingResult, 1)
		c.pending[1] = ch
		c.handleLine([]byte(`{"jsonrpc":"2.0","id":"x","result":{}}`))
		if len(c.pending) != 1 || len(ch) != 0 {
			t.Fatalf("response with non-numeric id changed pending calls: pending=%v results=%d", c.pending, len(ch))
		}
	})

	t.Run("undecodable response fails all pending", func(t *testing.T) {
		c, _ := newBareStdioConn()
		ch := make(chan pendingResult, 1)
		c.pending[1] = ch
		c.handleLine([]byte(`{"jsonrpc":"2.0","id":1,"error":"boom"}`))
		if (<-ch).err == nil {
			t.Fatal("expected pending callers to be failed")
		}
	})

	t.Run("response for an unknown waiter is dropped", func(t *testing.T) {
		c, _ := newBareStdioConn()
		ch := make(chan pendingResult, 1)
		c.pending[1] = ch
		c.handleLine([]byte(`{"jsonrpc":"2.0","id":99,"result":{}}`))
		if len(c.pending) != 1 || len(ch) != 0 {
			t.Fatalf("unknown response changed another waiter: pending=%v results=%d", c.pending, len(ch))
		}
	})

	t.Run("response resolves the matching waiter", func(t *testing.T) {
		c, _ := newBareStdioConn()
		ch := make(chan pendingResult, 1)
		c.pending[7] = ch
		c.handleLine([]byte(`{"jsonrpc":"2.0","id":7,"result":{"ok":true}}`))
		res := <-ch
		if res.err != nil || string(res.raw) != `{"ok":true}` {
			t.Fatalf("res = %#v", res)
		}
	})
}

func TestStdioConnReadFramesSkipsBlankAndFailsOversized(t *testing.T) {
	t.Run("blank lines are skipped", func(t *testing.T) {
		c, _ := newBareStdioConn()
		ch := make(chan pendingResult, 1)
		c.pending[1] = ch
		c.readFrames(strings.NewReader("\n\n{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"ok\":true}}\n"))
		res := <-ch
		if res.err != nil || string(res.raw) != `{"ok":true}` {
			t.Fatalf("res = %#v", res)
		}
	})

	t.Run("oversized frame fails pending callers and keeps reading", func(t *testing.T) {
		c, _ := newBareStdioConn()
		c.readBound = 16
		ch := make(chan pendingResult, 1)
		c.pending[1] = ch
		c.readFrames(strings.NewReader(strings.Repeat("x", 64) + "\n"))
		if (<-ch).err == nil {
			t.Fatal("expected the oversized frame to fail pending callers")
		}
	})
}

func TestStdioConnPublishAndFailAfterClose(t *testing.T) {
	c, _ := newBareStdioConn()
	ch := make(chan pendingResult, 2)
	c.pending[1] = ch
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if res := <-ch; res.err == nil {
		t.Fatal("Close did not fail the pending waiter")
	}
	c.publishNotification("x")
	c.failAllPending(errors.New("boom"))
	if method, ok := <-c.Notifications(); ok {
		t.Fatalf("notification after close was delivered: %q", method)
	}
	select {
	case res := <-ch:
		t.Fatalf("closed connection emitted a second result: %+v", res)
	default:
	}
}

func TestStdioConnPublishNotificationDropsWhenFull(t *testing.T) {
	c, _ := newBareStdioConn()
	for range cap(c.notifyCh) {
		c.publishNotification("fill")
	}
	c.publishNotification("overflow")
	for i := range cap(c.notifyCh) {
		if method := <-c.Notifications(); method != "fill" {
			t.Fatalf("notification %d = %q, want the buffered notification", i, method)
		}
	}
}

func TestStdioConnAuthPendingLifecycle(t *testing.T) {
	c, _ := newBareStdioConn()
	// No sink: the signal is dropped rather than recorded.
	c.raiseAuthPending(true)
	if c.AuthChallenged() {
		t.Fatal("expected no challenge without an auth sink")
	}

	doneCalled := false
	c.authDone = func() { doneCalled = true }
	c.resolveAuthPending()
	if !doneCalled {
		t.Fatal("expected the pending auth wait to be resolved")
	}
}

func TestStdioConnReadStderr(t *testing.T) {
	c, _ := newBareStdioConn()
	c.readBound = 4096
	sink := &recordingSink{}
	c.authSink = sink
	done := make(chan struct{})
	c.readStderr(strings.NewReader("please authorize\n\nplain log line\n"), sink, done)
	<-done
	lines, _ := sink.snapshot()
	if len(lines) != 2 || lines[0] != "please authorize" || lines[1] != "plain log line" {
		t.Fatalf("stderr lines = %q", lines)
	}
	if pending, _ := sink.authSnapshot(); len(pending) != 1 || pending[0] != "srv" {
		t.Fatalf("auth pending signals = %q, want one signal for srv", pending)
	}
	if !c.AuthChallenged() {
		t.Fatal("explicit authorization prompt did not mark the connection as challenged")
	}
}

func TestStdioConnNotifyOnClosedConnection(t *testing.T) {
	c, _ := newBareStdioConn()
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := c.Notify(context.Background(), "x", nil); err == nil {
		t.Fatal("expected Notify to fail on a closed connection")
	}
	if _, err := c.Call(context.Background(), "x", nil); err == nil {
		t.Fatal("expected Call to fail on a closed connection")
	}
}

func TestStdioConnCloseFailsPendingWaiters(t *testing.T) {
	c, _ := newBareStdioConn()
	ch := make(chan pendingResult, 1)
	c.pending[1] = ch
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if (<-ch).err == nil {
		t.Fatal("expected Close to fail pending waiters")
	}
}
