package anthropic

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func Test_StreamCompletions(t *testing.T) {
	want := "Hello!"
	messages := [][]byte{
		[]byte(`event: message_start
data: {"type": "message_start", "message": {"id": "msg_1nZdL29xx5MUA1yADyHTEsnR8uuvGzszyY", "type": "message", "role": "assistant", "content": [], "model": "claude-3-opus-20240229", "stop_reason": null, "stop_sequence": null, "usage": {"input_tokens": 25, "output_tokens": 1}}}

`),
		[]byte(`event: content_block_start
data: {"type": "content_block_start", "index": 0, "content_block": {"type": "text", "text": ""}}

`),

		[]byte(`event: ping
data: {"type": "ping"}

`),
		// This should be picked up
		[]byte(`event: content_block_delta
data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": "Hello"}}
    
`),
		// This should also be picked up
		[]byte(`event: content_block_delta
data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": "!"}}

`),
		[]byte(`event: content_block_stop
data: {"type": "content_block_stop", "index": 0}

`),
		[]byte(`event: message_delta
data: {"type": "message_delta", "delta": {"stop_reason": "end_turn", "stop_sequence":null, "usage":{"output_tokens": 15}}}

`),
		[]byte(`event: message_stop
data: {"type": "message_stop"}

`),
	}
	testDone := make(chan string)
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Expose-Headers", "Content-Type")
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		for _, msg := range messages {
			w.Write(msg)
			w.(http.Flusher).Flush()
		}
		<-testDone
	}))
	context, contextCancel := context.WithTimeout(context.Background(), time.Second/100)
	t.Cleanup(func() {
		contextCancel()
		// Can't seem to figure out how to close the testserver. so well... it'll have to remain open
		// testServer.Close()
		close(testDone)
	})

	// Use the test server's URL as the backend URL in your code
	c := Claude{
		URL: testServer.URL,
	}
	t.Setenv("ANTHROPIC_API_KEY", "somekey")
	err := c.Setup()
	if err != nil {
		t.Fatalf("failed to setup claude: %v", err)
	}
	out, err := c.StreamCompletions(context, pub_models.Chat{
		ID: "test",
		Messages: []pub_models.Message{
			{Role: "system", Content: "test"},
			{Role: "user", Content: "test"},
		},
	})
	if err != nil {
		t.Fatalf("failed to stream completions: %v", err)
	}

	got := ""
OUTER:
	for {
		select {
		case <-context.Done():
			t.Fatal("test timeout")
		case tok, ok := <-out:
			if !ok {
				break OUTER
			}
			switch sel := tok.(type) {
			case string:
				got += sel
			case error:
				if errors.Is(sel, io.EOF) {
					break OUTER
				}
				t.Fatalf("unexpected error: %v", sel)
			}
		}
	}

	if got != want {
		t.Fatalf("expected: %v, got: %v", want, got)
	}
}

func Test_context(t *testing.T) {
	testDone := make(chan struct{})
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-testDone
	}))
	t.Cleanup(func() {
		testServer.Close()
		close(testDone)
	})

	// Use the test server's URL as the backend URL in your code
	c := Claude{
		URL: testServer.URL,
	}
	t.Setenv("ANTHROPIC_API_KEY", "somekey")
	err := c.Setup()
	if err != nil {
		t.Fatal(err)
	}
	testboil.ReturnsOnContextCancel(t, func(ctx context.Context) {
		c.StreamCompletions(ctx, pub_models.Chat{
			ID: "test",
			Messages: []pub_models.Message{
				{Role: "system", Content: "test"},
				{Role: "user", Content: "test"},
			},
		})
	}, time.Second)
}
