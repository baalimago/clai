package text

import (
	"bytes"
	"context"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/jsonl"
	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/internal/stdio"
)

// hostileLine is an error line with an SGR sequence, a bell and a
// secret-shaped value.
const hostileLine = "error: \x1b[31mboom\x1b[0m\a token=supersecretvalue123"

const hostileLineEscaped = `error: \x1b[31mboom\x1b[0m\x07 token=[REDACTED]`

// hostileLineRedacted is what the rolling display is handed; it strips the
// control characters itself.
const hostileLineRedacted = "error: \x1b[31mboom\x1b[0m\a token=[REDACTED]"

func TestMcpLogSink_RawStructuredTextEscapesAndRedacts(t *testing.T) {
	var errOut bytes.Buffer
	sink := newMcpLogSinkTo(context.Background(), mcpLogRawStructured, &errOut)

	sink.AppendServerLog("fs", hostileLine)

	if got, want := errOut.String(), "mcp_fs: "+hostileLineEscaped+"\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestMcpLogSink_RawStructuredJSONRedacts(t *testing.T) {
	var out, errb bytes.Buffer
	env := runenv.New(runenv.Config{
		Streams: stdio.New(stdio.Config{Format: jsonl.FormatJSON, Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb}),
		Format:  jsonl.FormatJSON,
	})
	sink := newMcpLogSinkTo(runenv.With(context.Background(), env), mcpLogRawStructured, io.Discard)

	sink.AppendServerLog("fs", hostileLine)

	want := `{"type":"child_output","source":"mcp_fs","stream":"stderr","text":"error: \u001b[31mboom\u001b[0m\u0007 token=[REDACTED]"}` + "\n"
	if got := errb.String(); got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestMcpLogSink_RollingRedacts(t *testing.T) {
	t.Run("startup window", func(t *testing.T) {
		var output bytes.Buffer
		sink := newMcpLogSinkTo(context.Background(), mcpLogRolling, &output)
		sink.termWidth = func() int { return 200 }
		sink.termHeight = func() int { return 40 }

		sink.AppendServerLog("fs", hostileLine)

		if got, want := output.String(), "error: boom token=[REDACTED]"; !strings.Contains(got, want) || strings.Contains(got, "\a") {
			t.Fatalf("startup window = %q, want %q without the bell", got, want)
		}
	})
	t.Run("queued for the session loop", func(t *testing.T) {
		sink := newMcpLogSinkTo(context.Background(), mcpLogRolling, io.Discard)
		sink.attach()

		sink.AppendServerLog("fs", hostileLine)
		sink.ServerExited("fs")

		entries := sink.Drain()
		if len(entries) != 1 || entries[0].line != hostileLineRedacted || !entries[0].isError {
			t.Fatalf("entries = %+v, want the one redacted error line and no exit tail repeating it", entries)
		}
	})
}

func TestMcpLogSink_PlainDirectLeavesTheLineToThePresenter(t *testing.T) {
	sink := newMcpLogSinkTo(context.Background(), mcpLogPlainDirect, io.Discard)
	var noticed []string
	sink.notice = func(_, line string) { noticed = append(noticed, line) }

	sink.AppendServerLog("fs", hostileLine)

	if !slices.Equal(noticed, []string{hostileLine}) {
		t.Fatalf("noticed = %q, want the line as written", noticed)
	}
}

// A line relayed while the server ran is not relayed again in its exit tail.
func TestMcpLogSink_ExitTailSkipsLinesAlreadyShown(t *testing.T) {
	t.Run("raw structured", func(t *testing.T) {
		var errOut bytes.Buffer
		sink := newMcpLogSinkTo(context.Background(), mcpLogRawStructured, &errOut)

		sink.AppendServerLog("fs", "started")
		sink.AppendServerLog("fs", "fatal: cannot start")
		sink.AppendServerLog("fs", "bye")
		sink.ServerExited("fs")

		if got, want := errOut.String(), "mcp_fs: fatal: cannot start\nmcp_fs: started\nmcp_fs: bye\n"; got != want {
			t.Fatalf("stderr = %q, want %q", got, want)
		}
	})
	t.Run("rolling", func(t *testing.T) {
		sink := newMcpLogSinkTo(context.Background(), mcpLogRolling, io.Discard)
		sink.attach()

		sink.AppendServerLog("fs", "started")
		sink.AppendServerLog("fs", "fatal: cannot start")
		sink.AppendServerLog("fs", "bye")
		sink.ServerExited("fs")

		entries := sink.Drain()
		last := entries[len(entries)-1]
		if !last.exit || !slices.Equal(last.lines, []string{"started", "bye"}) {
			t.Fatalf("exit entry = %+v, want the two lines not elevated before", last)
		}
	})
}
