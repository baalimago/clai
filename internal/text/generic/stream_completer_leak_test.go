package generic

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/models"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// TestHandleStreamResponse_ClosesAfterStopEvent is the regression guard for the
// production goroutine leak of 2026-08-12 (sakfraga: one leaked goroutine per
// LLM round). The session runner returns on StopEvent and never reads the
// channel again; the producer, however, kept looping — the trailing blank line
// after the [DONE] frame became a NoopEvent whose send to the abandoned
// unbuffered channel blocked forever. The producer must terminate after
// delivering the terminal StopEvent and close the channel.
func TestHandleStreamResponse_ClosesAfterStopEvent(t *testing.T) {
	pr, pw := io.Pipe()
	res := &http.Response{StatusCode: http.StatusOK, Body: pr}
	s := &StreamCompleter{}
	out, err := s.handleStreamResponse(context.Background(), res)
	if err != nil {
		t.Fatalf("handleStreamResponse err: %v", err)
	}

	// Wire shape from the production repro: tool-call chunk, [DONE] frame, then
	// a trailing blank line (a separate read that yields a NoopEvent).
	go func() {
		bw := bufio.NewWriter(pw)
		fmt.Fprintf(bw, "data: %s\n\n", `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"my_tool","arguments":"{\"a\":1}"}}]}}]}`)
		bw.Flush()
		fmt.Fprintf(bw, "data: [DONE]\n\n")
		bw.Flush()
		fmt.Fprintf(bw, "\n")
		bw.Flush()
		pw.Close()
	}()

	// Consumer mirrors the session runner: it counts tool calls and returns on
	// StopEvent without reading the channel again.
	toolCalls := 0
	stopped := false
	for !stopped {
		select {
		case ev, ok := <-out:
			if !ok {
				stopped = true
				continue
			}
			switch ev.(type) {
			case pub_models.Call:
				toolCalls++
			case models.StopEvent:
				stopped = true
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timeout waiting for StopEvent")
		}
	}
	if toolCalls != 1 {
		t.Fatalf("expected 1 tool call, got %d", toolCalls)
	}

	// The producer must terminate after the terminal StopEvent and close the
	// channel. Pre-fix it blocks forever on the trailing blank line's NoopEvent
	// send and the channel stays open.
	select {
	case ev, ok := <-out:
		if ok {
			t.Fatalf("expected channel close after StopEvent, got event: %T", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("channel not closed after StopEvent: producer goroutine leaked")
	}
}

// TestHandleStreamResponse_ClosesOnCtxCancelMidSend covers the sibling leak
// class: a consumer that abandons the channel on ctx cancellation while the
// producer is blocked on an in-flight send. The send must unblock when the
// context is cancelled and the producer must terminate.
func TestHandleStreamResponse_ClosesOnCtxCancelMidSend(t *testing.T) {
	pr, pw := io.Pipe()
	res := &http.Response{StatusCode: http.StatusOK, Body: pr}
	s := &StreamCompleter{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out, err := s.handleStreamResponse(ctx, res)
	if err != nil {
		t.Fatalf("handleStreamResponse err: %v", err)
	}

	// Consumer reads exactly one event, then abandons the channel. Frames use a
	// single trailing newline so no blank line (and its NoopEvent send) can
	// interleave between the events under test.
	received := make(chan struct{})
	go func() {
		<-out
		close(received)
	}()

	// First chunk: producer sends it, consumer receives it.
	if _, err := fmt.Fprintf(pw, "data: %s\n", `{"choices":[{"delta":{"content":"one"}}]}`); err != nil {
		t.Fatalf("write first chunk: %v", err)
	}
	select {
	case <-received:
	case <-time.After(2 * time.Second):
		t.Fatal("consumer did not receive first event")
	}

	// Second chunk: the pipe write returns only once the producer has consumed
	// the bytes; the producer is then blocked on the send of the second event
	// (the consumer is gone) or about to be.
	if _, err := fmt.Fprintf(pw, "data: %s\n", `{"choices":[{"delta":{"content":"two"}}]}`); err != nil {
		t.Fatalf("write second chunk: %v", err)
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
