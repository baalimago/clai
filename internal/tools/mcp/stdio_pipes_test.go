package mcp

import (
	"strconv"
	"testing"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// slowSink reads slower than the server writes, so a reap that closes the
// pipe under it loses the unread tail.
type slowSink struct {
	recordingSink
	delay time.Duration
}

func (s *slowSink) AppendServerLog(server, line string) {
	time.Sleep(s.delay)
	s.recordingSink.AppendServerLog(server, line)
}

// TestStdioConnDrainsStderrTailAfterServerSelfExits: every line the server
// wrote must reach the sink before its exit is announced.
func TestStdioConnDrainsStderrTailAfterServerSelfExits(t *testing.T) {
	const lines = 40
	sink := &slowSink{delay: time.Millisecond}
	conn, err := NewStdioConn(t.Context(), pub_models.McpServer{
		Name:    "bulk-tail",
		Command: testServerBinary(t),
		Env:     map[string]string{"TEST_SERVER_STDERR_BULK": strconv.Itoa(lines)},
	}, sink)
	if err != nil {
		t.Fatalf("NewStdioConn: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	deadline := time.Now().Add(10 * time.Second)
	for {
		got, exited := sink.snapshot()
		if len(exited) > 0 {
			if len(got) != lines {
				t.Fatalf("sink received %d stderr lines when the exit was announced, want %d", len(got), lines)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the server's exit was never reported")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
