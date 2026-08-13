package anthropic

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"
)

// TestClaudeStream_ClosesAfterEOFWithoutDoubleSend is the regression guard for
// the claude stream leak class found alongside the generic StreamCompleter fix
// of 2026-08-12: when the stream ends at a line boundary without a
// message_stop event, the reader hits io.EOF with an empty token, emits the
// io.EOF terminal event, and then sent a second "failed to read line" error
// into the channel. The consumer returns on io.EOF and never reads again, so
// that second send blocked forever. The producer must terminate after the
// terminal io.EOF event and close the channel.
func TestClaudeStream_ClosesAfterEOFWithoutDoubleSend(t *testing.T) {
	pr, pw := io.Pipe()
	res := &http.Response{StatusCode: http.StatusOK, Body: pr}
	c := &Claude{}
	out, err := c.handleStreamResponse(context.Background(), res)
	if err != nil {
		t.Fatalf("handleStreamResponse err: %v", err)
	}

	// A text delta, then the stream ends cleanly at a line boundary without a
	// message_stop event (e.g. connection drop after the final frame).
	go func() {
		bw := bufio.NewWriter(pw)
		fmt.Fprintf(bw, "event: content_block_delta\n")
		fmt.Fprintf(bw, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n")
		fmt.Fprintf(bw, "\n")
		bw.Flush()
		pw.Close()
	}()

	// Consumer mirrors the session runner: return on io.EOF, never read again.
	for {
		ev, ok := <-out
		if !ok {
			t.Fatal("channel closed before the io.EOF terminal event")
		}
		if asErr, isErr := ev.(error); isErr && errors.Is(asErr, io.EOF) {
			break
		}
	}

	// The producer must terminate after the terminal io.EOF and close the
	// channel. Pre-fix it sends a second, unconsumed "failed to read line: EOF"
	// error and blocks forever.
	select {
	case ev, ok := <-out:
		if ok {
			t.Fatalf("expected channel close after io.EOF, got event: %T", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("channel not closed after io.EOF: producer goroutine leaked")
	}
}

// TestClaudeStream_ClosesOnCtxCancelMidSend covers the sibling leak class: a
// consumer that abandons the channel on ctx cancellation while the producer is
// blocked on an in-flight send. The send must unblock when the context is
// cancelled and the channel must close.
func TestClaudeStream_ClosesOnCtxCancelMidSend(t *testing.T) {
	pr, pw := io.Pipe()
	res := &http.Response{StatusCode: http.StatusOK, Body: pr}
	c := &Claude{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out, err := c.handleStreamResponse(ctx, res)
	if err != nil {
		t.Fatalf("handleStreamResponse err: %v", err)
	}

	// Consumer reads exactly one event, then abandons the channel.
	received := make(chan struct{})
	go func() {
		<-out
		close(received)
	}()

	// First event: producer sends "hi", consumer receives it. Frames carry no
	// trailing blank line so the pipe write completes once the producer has
	// consumed the event's bytes.
	if _, err := fmt.Fprintf(pw, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n"); err != nil {
		t.Fatalf("write first event: %v", err)
	}
	select {
	case <-received:
	case <-time.After(2 * time.Second):
		t.Fatal("consumer did not receive first event")
	}

	// Second event: the pipe write returns only once the producer has consumed
	// the bytes; the producer is then blocked on the send of the second event
	// (the consumer is gone) or about to be.
	if _, err := fmt.Fprintf(pw, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"there\"}}\n"); err != nil {
		t.Fatalf("write second event: %v", err)
	}

	// Cancelling must unblock the in-flight send. The producer then terminates and
	// closes the response body (pr), which makes further pipe writes fail with
	// io.ErrClosedPipe. Pre-fix the producer stays blocked on the abandoned
	// channel send and the write times out.
	cancel()
	writeDone := make(chan error, 1)
	go func() {
		_, err := pw.Write([]byte("x"))
		writeDone <- err
	}()
	select {
	case err := <-writeDone:
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("expected ErrClosedPipe after producer termination, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("producer goroutine leaked: channel send never unblocked after ctx cancel")
	}
	_ = pw.Close()
}
