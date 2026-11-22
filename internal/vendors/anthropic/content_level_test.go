package anthropic

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/testenv/procenv"
	"github.com/baalimago/clai/pkg/diagnostic"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestStreamContentIsTraceOnly(t *testing.T) {
	const stream = `event: message_start
data: {"type": "message_start", "message": {"id": "msg_1", "type": "message", "role": "assistant", "content": [], "usage": {"input_tokens": 25, "output_tokens": 1}}}

event: content_block_start
data: {"type": "content_block_start", "index": 0, "content_block": {"type": "text", "text": ""}}

event: content_block_delta
data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": "REPLY-MARKER"}}

event: content_block_stop
data: {"type": "content_block_stop", "index": 0}

event: message_stop
data: {"type": "message_stop"}

`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, stream)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("ANTHROPIC_API_KEY", "somekey")
	run := func(level diagnostic.Level) string {
		var out bytes.Buffer
		c := Claude{URL: srv.URL}
		if err := c.Setup(); err != nil {
			t.Fatalf("setup: %v", err)
		}
		events, err := c.StreamCompletions(procenv.Diagnostics(context.Background(), &out, level),
			pub_models.Chat{ID: "test", Messages: []pub_models.Message{{Role: "system", Content: "SYSTEM-MARKER"}, {Role: "user", Content: "PROMPT-MARKER"}}})
		if err != nil {
			t.Fatalf("stream: %v", err)
		}
		var reply strings.Builder
		for ev := range events {
			switch v := ev.(type) {
			case string:
				reply.WriteString(v)
			case error:
				if !errors.Is(v, io.EOF) {
					t.Fatalf("stream error: %v", v)
				}
			}
		}
		if reply.String() != "REPLY-MARKER" {
			t.Fatalf("reply %q", reply.String())
		}
		return out.String()
	}
	debug := run(diagnostic.LevelDebug)
	if strings.Contains(debug, "MARKER") {
		t.Errorf("debug output carries conversation content:\n%s", debug)
	}
	for _, want := range []string{"debug [vendor.anthropic] response events=5 bytes=", " tokens_in=25 tokens_out=1 took=", " types=content_block_delta:1,content_block_start:1,content_block_stop:1,message_start:1,message_stop:1\n", "claudified messages: count=1", "request url=" + srv.URL + " model= messages=1 tools=0 bytes="} {
		if !strings.Contains(debug, want) {
			t.Errorf("debug output lacks %q:\n%s", want, debug)
		}
	}
	trace := run(diagnostic.LevelTrace)
	for _, want := range []string{"full message: 'REPLY-MARKER'", "deltaStruct: ", "delta struct: ", "claudified messages: [", "PROMPT-MARKER", "SYSTEM-MARKER"} {
		if !strings.Contains(trace, want) {
			t.Errorf("trace output lacks %q:\n%s", want, trace)
		}
	}
}
