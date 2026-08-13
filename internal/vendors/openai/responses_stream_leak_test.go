package openai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/models"
)

// TestResponsesStream_ClosesOnCtxCancelMidSend is the regression guard for the
// ctx-cancellation leak class found alongside the generic StreamCompleter fix
// of 2026-08-12: a consumer that abandons the channel on ctx cancellation
// while the producer is blocked on an in-flight send. The send must unblock
// when the context is cancelled and the producer must terminate, closing the
// response body. Frames carry a single trailing newline so no blank line (and
// its extra send) can interleave between the frames under test.
func TestResponsesStream_ClosesOnCtxCancelMidSend(t *testing.T) {
	pr, pw := io.Pipe()
	res := &http.Response{StatusCode: http.StatusOK, Body: pr}
	s := &responsesStreamer{apiKey: "k"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := make(chan models.CompletionEvent)
	go s.readResponsesStream(ctx, res.Body, out)

	// Consumer reads exactly one event, then abandons the channel.
	received := make(chan struct{})
	go func() {
		<-out
		close(received)
	}()

	// First frame: producer sends "x", consumer receives it.
	if _, err := fmt.Fprintf(pw, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"x\"}\n"); err != nil {
		t.Fatalf("write first frame: %v", err)
	}
	select {
	case <-received:
	case <-time.After(2 * time.Second):
		t.Fatal("consumer did not receive first event")
	}

	// Second frame: the pipe write returns only once the producer has consumed
	// the bytes; the producer is then blocked on the send of the second event
	// (the consumer is gone) or about to be.
	if _, err := fmt.Fprintf(pw, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"y\"}\n"); err != nil {
		t.Fatalf("write second frame: %v", err)
	}

	// Cancelling must unblock the in-flight send. The producer then terminates
	// and closes the response body (pr), which makes further pipe writes fail
	// with io.ErrClosedPipe. Pre-fix the producer stays blocked on the abandoned
	// channel send and the write times out.
	cancel()
	writeDone := make(chan error, 1)
	go func() {
		_, err := pw.Write([]byte("z"))
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
