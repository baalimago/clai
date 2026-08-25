package utils

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// errorWriter fails every write with a fixed error, pinning the phase-3
// collaborator-error contract: width-aware render helpers surface writer
// failures instead of silently dropping output.
type errorWriter struct{ err error }

func (w errorWriter) Write([]byte) (int, error) { return 0, w.err }

func TestIsMcpLogErrorLine(t *testing.T) {
	errorLines := []string{
		"fatal: boom",
		"PANIC",
		"an exception occurred",
		"connection refused",
		"unable to connect",
		"could not open file",
		"timeout exceeded",
		"unreachable host",
		"permission denied",
		"WARN: slow request",
		"error: x",
	}
	for _, line := range errorLines {
		if !IsMcpLogErrorLine(line) {
			t.Errorf("IsMcpLogErrorLine(%q) = false, want true", line)
		}
	}
	normalLines := []string{
		"server started",
		"listening on :8080",
		"request handled in 12ms",
		"INFO: ready",
		"read 42 rows",
	}
	for _, line := range normalLines {
		if IsMcpLogErrorLine(line) {
			t.Errorf("IsMcpLogErrorLine(%q) = true, want false", line)
		}
	}
}

func TestIsMcpLogAuthLine(t *testing.T) {
	authLines := []string{
		"Please visit https://github.com/login/device and enter code ABCD-1234",
		"[115] Please authorize this client by visiting:",
		"https://mcp.notion.com/authorize?response_type=code&client_id=abc",
		"Not authenticated. Run 'wrangler login' first",
		"Your OAuth token has expired",
		"401 Unauthorized",
		"HTTP 403 Forbidden",
		"missing API key",
		"no credentials found",
		"Sign in to continue",
		"enter the verification code shown in your browser",
	}
	for _, line := range authLines {
		if !IsMcpLogAuthLine(line) {
			t.Errorf("IsMcpLogAuthLine(%q) = false, want true", line)
		}
	}
	// OAuth machinery status chatter must stay quiet: only prompts that need
	// the user's action elevate.
	nonAuthLines := []string{
		"server started",
		"listening on :8080",
		"request handled in 12ms",
		"INFO: ready",
		"error: boom",
		"timeout exceeded",
		"[108] Discovering OAuth server configuration...",
		"[115] Discovered authorization server: https://mcp.notion.com",
		"[115] Connecting to remote server: https://mcp.notion.com/mcp",
		"[115] Using transport strategy: http-first",
		"[115] Authentication required. Initializing auth...",
		"[115] Initializing auth coordination on-demand",
		"[115] OAuth callback server running at http://127.0.0.1:9553",
		"[115] Creating lockfile for server cb42d1a06ae8 with process 115 on port 9553",
		"[115] Authentication required. Waiting for authorization...",
		"[115] Browser opened automatically.",
	}
	for _, line := range nonAuthLines {
		if IsMcpLogAuthLine(line) {
			t.Errorf("IsMcpLogAuthLine(%q) = true, want false", line)
		}
	}
}

func TestIsMcpLogAuthPayloadLine(t *testing.T) {
	payloadLines := []string{
		"https://mcp.notion.com/authorize?response_type=code",
		"http://example.com/anything",
		"ABCD-1234",
		"WDJB-MJHT",
	}
	for _, line := range payloadLines {
		if !IsMcpLogAuthPayloadLine(line) {
			t.Errorf("IsMcpLogAuthPayloadLine(%q) = false, want true", line)
		}
	}
	nonPayloadLines := []string{
		"Browser opened automatically.",
		"Creating lockfile for server cb42d1a06ae8 with process 115 on port 9553",
		"[115]",
		"code line two",
		"",
	}
	for _, line := range nonPayloadLines {
		if IsMcpLogAuthPayloadLine(line) {
			t.Errorf("IsMcpLogAuthPayloadLine(%q) = true, want false", line)
		}
	}
}

func TestPrintMcpErrorBlock_AuthLines(t *testing.T) {
	var out bytes.Buffer
	err := PrintMcpErrorBlock(&out, "github", []string{"To use this server, please sign in:", "https://example.com/device"}, 80)
	if err != nil {
		t.Fatalf("PrintMcpErrorBlock: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "» To use this server, please sign in:") {
		t.Errorf("auth line missing » marker; got:\n%s", got)
	}
	if !strings.Contains(got, "https://example.com/device") {
		t.Errorf("auth payload line missing; got:\n%s", got)
	}
	if strings.Contains(got, "✗") {
		t.Errorf("auth line styled as error; got:\n%s", got)
	}
}

func TestPrintMcpErrorBlock_WrapsLongAuthURL(t *testing.T) {
	var out bytes.Buffer
	url := "https://mcp.notion.com/authorize?response_type=code&code_challenge=" + strings.Repeat("x", 200) + "&state=end-of-url"
	err := PrintMcpErrorBlock(&out, "notion", []string{"Please authorize this client by visiting:", url}, 80)
	if err != nil {
		t.Fatalf("PrintMcpErrorBlock: %v", err)
	}
	// Rows are indented, colorized and wrapped; strip decoration and rejoin to
	// verify the URL survived without truncation.
	stripped := regexp.MustCompile("\x1b\\[[0-9;]*m").ReplaceAllString(out.String(), "")
	joined := strings.ReplaceAll(strings.ReplaceAll(stripped, "\n", ""), " ", "")
	if !strings.Contains(joined, "&state=end-of-url") {
		t.Errorf("URL tail truncated; got:\n%s", out.String())
	}
	if strings.Contains(stripped, "…") {
		t.Errorf("elevated block truncated a line; got:\n%s", out.String())
	}
}

func TestActivityViewport_AppendMcpLogBlock_AuthMarker(t *testing.T) {
	viewport := NewActivityViewport(60, 30, 30)
	viewport.AppendMcpLogBlock("github", "server started\nNot logged in. Run 'gh auth login'", 6)
	joined := strings.Join(viewport.Rows(), "\n")
	if !strings.Contains(joined, "» Not logged in. Run 'gh auth login'") {
		t.Errorf("rows missing auth marker; got:\n%s", joined)
	}
}

func TestActivityViewport_AppendMcpLogBlock(t *testing.T) {
	viewport := NewActivityViewport(40, 30, 30)
	viewport.AppendMcpLogBlock("filesystem", "server started\nWARN: slow request\nerror: boom", 6)
	rows := viewport.Rows()
	if len(rows) == 0 {
		t.Fatal("AppendMcpLogBlock produced no rows")
	}
	if got := rows[0]; got != "▸ mcp.filesystem log" {
		t.Fatalf("header row = %q, want %q", got, "▸ mcp.filesystem log")
	}
	joined := strings.Join(rows, "\n")
	for _, want := range []string{"server started", "✗ WARN: slow request", "✗ error: boom"} {
		if !strings.Contains(joined, want) {
			t.Errorf("rows missing %q; got:\n%s", want, joined)
		}
	}
}

func TestActivityViewport_McpLogBlockWrapsLongAuthURL(t *testing.T) {
	v := NewActivityViewport(40, 30, 30)
	url := "https://example.com/authorize?code=" + strings.Repeat("x", 80) + "ENDOFURL"
	v.AppendMcpLogBlock("notion", url, 8)
	joined := strings.Join(v.Rows(), "\n")
	stripped := strings.NewReplacer("\n", "", " ", "", "»", "").Replace(joined)
	if !strings.Contains(stripped, "ENDOFURL") {
		t.Errorf("URL tail lost in window; rows:\n%s", joined)
	}
	if strings.Contains(joined, "…") {
		t.Errorf("auth URL truncated in window; rows:\n%s", joined)
	}
}

func TestActivityViewport_AppendMcpLogBlock_CompactsAndEvicts(t *testing.T) {
	viewport := NewActivityViewport(20, 6, 6)
	long := strings.Repeat("l", 60)
	viewport.AppendMcpLogBlock("fs", long+"\n"+long+"\n"+long, 3)
	rows := viewport.Rows()
	if len(rows) > 6 {
		t.Fatalf("block exceeds window height: %d rows", len(rows))
	}
	for range 20 {
		viewport.AppendMcpLogBlock("fs", "noise line", 3)
	}
	if got := viewport.Rows(); got == nil {
		t.Fatal("viewport rows nil after eviction")
	}
}

func TestPrintMcpErrorBlock(t *testing.T) {
	var out bytes.Buffer
	err := PrintMcpErrorBlock(&out, "filesystem", []string{"server started", "error: boom"}, 80)
	if err != nil {
		t.Fatalf("PrintMcpErrorBlock: %v", err)
	}
	got := out.String()
	for _, want := range []string{"▸ mcp.filesystem log", "server started", "✗ error: boom"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q; got:\n%s", want, got)
		}
	}
}

func TestPrintMcpErrorBlock_WriterErrorSurfaces(t *testing.T) {
	want := errors.New("disk full")
	err := PrintMcpErrorBlock(errorWriter{err: want}, "filesystem", []string{"error: boom"}, 80)
	if err == nil {
		t.Fatal("expected the writer error to surface")
	}
	if !errors.Is(err, want) {
		t.Fatalf("expected the writer error to be wrapped, got: %v", err)
	}
}

func TestPrintToolActivity_WriterErrorSurfaces(t *testing.T) {
	want := errors.New("disk full")
	err := PrintToolActivity(errorWriter{err: want}, pub_models.Call{Name: "ls"}, "output", 80, 3)
	if err == nil {
		t.Fatal("expected the writer error to surface")
	}
	if !errors.Is(err, want) {
		t.Fatalf("expected the writer error to be wrapped, got: %v", err)
	}
}

func TestUpdateMessageTerminalMetadata(t *testing.T) {
	testCases := []struct {
		name              string
		msg               string
		line              string
		lineCount         int
		termWidth         int
		expectedLine      string
		expectedLineCount int
	}{
		{
			name:              "Single line message",
			msg:               "Hello",
			line:              "",
			lineCount:         0,
			termWidth:         10,
			expectedLine:      "Hello",
			expectedLineCount: 1,
		},
		{
			name:              "Message with newline",
			msg:               "Hello\nWorld",
			line:              "",
			lineCount:         0,
			termWidth:         10,
			expectedLine:      "World",
			expectedLineCount: 2,
		},
		{
			name:              "Message exceeding terminal width",
			msg:               "Hello World",
			line:              "",
			lineCount:         0,
			termWidth:         5,
			expectedLine:      "World",
			expectedLineCount: 3,
		},
		{
			name:              "Append to existing line",
			msg:               "World",
			line:              "Hello ",
			lineCount:         0,
			termWidth:         20,
			expectedLine:      "Hello World",
			expectedLineCount: 1,
		},
		{
			name:              "It should handle multiple termwidth overflows",
			msg:               "1111 2222 3333 4444",
			line:              "",
			lineCount:         0,
			termWidth:         5,
			expectedLine:      "4444",
			expectedLineCount: 4,
		},
		{
			name:              "It should handle multiple termwidth overflows + newlines",
			msg:               "1111 22\n3333 4444",
			line:              "",
			lineCount:         0,
			termWidth:         5,
			expectedLine:      "4444",
			expectedLineCount: 4,
		},
		{
			name:              "It should handle multiple termwidth overflows + newlines",
			msg:               "11 22 33 44 55 66",
			line:              "",
			lineCount:         0,
			termWidth:         3,
			expectedLine:      "66",
			expectedLineCount: 6,
		},
		{
			name: "it should not fail on this edge case that I found",
			msg:  "Debugging involves systematically finding and resolving issues within your code or software. Start by identifying the problem, replicate the error, and use tools like breakpoints or logging to trace the source. Testing changes iteratively helps ensure the fix is successful and doesn't cause new issues.",
			// This is not correct, but that's fine, the last line functionality isn't used anywhere anyways
			expectedLine:      "issues.",
			lineCount:         0,
			termWidth:         223,
			expectedLineCount: 2,
		},
		{
			name:              "it should not fail on this edge case that I found",
			msg:               "*Hurrmph* I'm as well as a 90-year old can be, which is better than the alternative, I suppose. My joints are creaking like an old rocking chair, but my mind is still sharp as a tack.\n\nWhat can I help you with today, young whippersnapper? *adjusts spectacles*\n",
			expectedLine:      "",
			lineCount:         0,
			termWidth:         127,
			expectedLineCount: 5,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			line := tc.line
			lineCount := tc.lineCount

			UpdateMessageTerminalMetadata(tc.msg, &line, &lineCount, tc.termWidth)

			if line != tc.expectedLine {
				t.Errorf("Expected line: %q, got: %q", tc.expectedLine, line)
			}

			if lineCount != tc.expectedLineCount {
				t.Errorf("Expected lineCount: %d, got: %d", tc.expectedLineCount, lineCount)
			}
		})
	}
}

func Test_ShortenedOutput(t *testing.T) {
	t.Run("it should shorten line with a lot of newlines", func(t *testing.T) {
		var given strings.Builder
		amNewlines := 90
		maxShortenedNewlines := 5
		for range amNewlines {
			given.WriteString("\n")
		}
		gotStr := ShortenedOutput(given.String(), maxShortenedNewlines)
		got := strings.Count(gotStr, "\n")
		want := maxShortenedNewlines + 1
		if got >= want {
			t.Fatalf("expected: %v, got: %v", want, got)
		}
	})

	t.Run("it should prioritize newline shortening over rune shortening", func(t *testing.T) {
		var given strings.Builder
		for range 30 {
			given.WriteString("0123456789\n")
		}
		got := ShortenedOutput(given.String(), 5)
		if !strings.Contains(got, "[and 26 more lines]") {
			t.Fatalf("expected line-based shortening, got %q", got)
		}
	})
}

func TestPrepareDisplayMessage(t *testing.T) {
	t.Run("tool messages are shortened", func(t *testing.T) {
		msg := pub_models.Message{Role: "tool", Content: strings.Repeat("0123456789\n", 30)}
		got := PrepareDisplayMessage(msg)
		if !strings.Contains(got.Content, "[and 26 more lines]") {
			t.Fatalf("expected shortened tool output, got %q", got.Content)
		}
	})

	t.Run("assistant messages are shortened using same formatter", func(t *testing.T) {
		msg := pub_models.Message{Role: "assistant", Content: strings.Repeat("0123456789\n", 30)}
		got := PrepareDisplayMessage(msg)
		if !strings.Contains(got.Content, "[and 26 more lines]") {
			t.Fatalf("expected shortened assistant output, got %q", got.Content)
		}
	})

	t.Run("system messages are not shortened (final output)", func(t *testing.T) {
		msg := pub_models.Message{Role: "system", Content: strings.Repeat("0123456789\n", 30)}
		got := PrepareDisplayMessage(msg)
		if got.Content != msg.Content {
			t.Fatalf("expected system message to remain untouched")
		}
	})

	t.Run("assistant reasoning messages are preserved", func(t *testing.T) {
		msg := pub_models.Message{
			Role:             "assistant",
			Content:          "Body\n\nWarnings:\n- a\n- b\n- c\n- d\n- e\n- f",
			ReasoningContent: "Need tool.",
		}
		got := PrepareDisplayMessage(msg)
		if got.Content != msg.Content {
			t.Fatalf("expected reasoning-bearing assistant message to remain untouched")
		}
	})

	t.Run("mcp tool messages are shortened", func(t *testing.T) {
		msg := pub_models.Message{Role: "tool", Content: "mcp_result\n" + strings.Repeat("0123456789\n", 30)}
		got := PrepareDisplayMessage(msg)
		if !strings.Contains(got.Content, "[and 27 more lines]") {
			t.Fatalf("expected shortened mcp tool output, got %q", got.Content)
		}
	})
}

func TestCompactToolActivity(t *testing.T) {
	t.Run("keeps the first three rows, marker, and last two rows", func(t *testing.T) {
		rows := []string{"one", "two", "three", "four", "five", "six", "seven", "eight"}
		got := compactTerminalRows(strings.Join(rows, "\n"), 80, 6)
		want := []string{"one", "two", "three", "... [3 terminal rows omitted] ...", "seven", "eight"}
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Fatalf("compactTerminalRows() = %q, want %q", got, want)
		}
	})

	t.Run("counts wrapped terminal rows", func(t *testing.T) {
		got := compactTerminalRows("abcdefghijkl", 3, 3)
		want := []string{"abc", "... [2 terminal rows omitted] ...", "jkl"}
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Fatalf("compactTerminalRows() = %q, want %q", got, want)
		}
	})

	t.Run("pairs a concise call header with its indented result", func(t *testing.T) {
		t.Setenv("NO_COLOR", "true")
		var out strings.Builder
		inputs := pub_models.Input{"long": true, "directory": "/tmp/example"}
		call := pub_models.Call{Name: "ls", Inputs: &inputs}
		if err := PrintToolActivity(&out, call, "one\ntwo\nthree\nfour", 80, 3); err != nil {
			t.Fatalf("PrintToolActivity: %v", err)
		}
		if got, want := out.String(), "▸ ls  directory=/tmp/example  long=true\n  one\n  ... [2 terminal rows omitted] ...\n  four\n\n"; got != want {
			t.Fatalf("PrintToolActivity() = %q, want %q", got, want)
		}
	})

	t.Run("renders MCP names as server dot tool and marks errors", func(t *testing.T) {
		t.Setenv("NO_COLOR", "true")
		var out strings.Builder
		call := pub_models.Call{Name: "mcp_filesystem_list_directory"}
		if err := PrintToolActivity(&out, call, "ERROR: permission denied", 80, 6); err != nil {
			t.Fatalf("PrintToolActivity: %v", err)
		}
		if got, want := out.String(), "▸ filesystem.list_directory\n  ✗ ERROR: permission denied\n\n"; got != want {
			t.Fatalf("PrintToolActivity() = %q, want %q", got, want)
		}
	})

	t.Run("keeps multiline and styled inputs on one safe header row", func(t *testing.T) {
		t.Setenv("NO_COLOR", "true")
		var out strings.Builder
		inputs := pub_models.Input{"command": "printf 'one\ntwo'\n\x1b[31mecho red\x1b[0m"}
		call := pub_models.Call{Name: "cmd", Inputs: &inputs}
		if err := PrintToolActivity(&out, call, "done", 120, 6); err != nil {
			t.Fatalf("PrintToolActivity: %v", err)
		}
		lines := strings.Split(strings.TrimSuffix(out.String(), "\n\n"), "\n")
		if len(lines) != 2 {
			t.Fatalf("expected one header and one result row, got %q", out.String())
		}
		if strings.Contains(out.String(), "\x1b[") {
			t.Fatalf("expected tool-provided ANSI escapes to be removed, got %q", out.String())
		}
	})

	t.Run("sanitizes tool names and keys including OSC controls", func(t *testing.T) {
		t.Setenv("NO_COLOR", "true")
		var out strings.Builder
		inputs := pub_models.Input{"key\n\x1b]8;;https://bad.example\a": "value\r\x1b]0;title\a"}
		call := pub_models.Call{Name: "tool\x1b[2J\nname", Inputs: &inputs}
		if err := PrintToolActivity(&out, call, "done", 120, 6); err != nil {
			t.Fatalf("PrintToolActivity: %v", err)
		}
		for _, forbidden := range []string{"\x1b", "\r", "\a"} {
			if strings.Contains(out.String(), forbidden) {
				t.Fatalf("header contains terminal control %q: %q", forbidden, out.String())
			}
		}
		if got := strings.Count(strings.TrimSuffix(out.String(), "\n\n"), "\n"); got != 1 {
			t.Fatalf("expected one safe header row and one result row, got %q", out.String())
		}
	})

	t.Run("summarizes async logs without JSON escaping", func(t *testing.T) {
		t.Setenv("NO_COLOR", "true")
		var out strings.Builder
		call := pub_models.Call{Name: "async_cmd_logs"}
		result := `{"async_cmd_id":"async_cmd_1","status":"succeeded","stdout":{"preview":"line one\nline two","truncated":false,"log_path":"/tmp/out"},"stderr":{"preview":"warning","truncated":false,"log_path":"/tmp/err"}}`
		if err := PrintToolActivity(&out, call, result, 80, 6); err != nil {
			t.Fatalf("PrintToolActivity: %v", err)
		}
		got := out.String()
		for _, want := range []string{"status=succeeded id=async_cmd_1", "stdout:", "line one", "line two", "stderr:", "warning"} {
			if !strings.Contains(got, want) {
				t.Fatalf("expected %q in %q", want, got)
			}
		}
		if strings.Contains(got, `\n`) {
			t.Fatalf("expected decoded log preview, got %q", got)
		}
	})

	t.Run("summarizes async await lifecycle data", func(t *testing.T) {
		t.Setenv("NO_COLOR", "true")
		var out strings.Builder
		call := pub_models.Call{Name: "async_cmd_await"}
		result := `{"result":"completed","async_cmds":[{"async_cmd_id":"async_cmd_1","status":"succeeded","exit_code":0,"stdout_log_path":"/tmp/out"}]}`
		if err := PrintToolActivity(&out, call, result, 120, 6); err != nil {
			t.Fatalf("PrintToolActivity: %v", err)
		}
		got := out.String()
		for _, want := range []string{"result=completed", "status=succeeded", "id=async_cmd_1", "exit=0"} {
			if !strings.Contains(got, want) {
				t.Fatalf("expected %q in %q", want, got)
			}
		}
		if strings.Contains(got, "stdout_log_path") {
			t.Fatalf("expected lifecycle paths to be omitted, got %q", got)
		}
	})
}

func TestActivityViewport(t *testing.T) {
	t.Run("wraps wide unicode by terminal cells", func(t *testing.T) {
		viewport := NewActivityViewport(10, 6, 6)
		viewport.AppendReasoning("界界界界界")

		got := viewport.Rows()
		want := []string{"∴ thinking", "  界界界界", "  界"}
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Fatalf("Rows() = %q, want display-cell wrapping %q", got, want)
		}
	})

	t.Run("coalesces streamed reasoning chunks into one wrapped block", func(t *testing.T) {
		viewport := NewActivityViewport(20, 6, 6)
		viewport.AppendReasoning("inspect ")
		viewport.AppendReasoning("the repository")

		got := viewport.Rows()
		want := []string{"∴ thinking", "  inspect the reposi", "  tory"}
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Fatalf("Rows() = %q, want %q", got, want)
		}
	})

	t.Run("starts a new thinking block after reasoning finishes", func(t *testing.T) {
		viewport := NewActivityViewport(40, 8, 8)
		viewport.AppendReasoning("first")
		viewport.FinishReasoning()
		viewport.AppendTool(pub_models.Call{Name: "pwd"}, "/tmp", 2)
		viewport.AppendReasoning("second")

		if got := strings.Count(strings.Join(viewport.Rows(), "\n"), "∴ thinking"); got != 2 {
			t.Fatalf("thinking header count = %d, want 2; rows=%q", got, viewport.Rows())
		}
	})

	t.Run("keeps mixed activity within one rolling viewport", func(t *testing.T) {
		viewport := NewActivityViewport(20, 5, 5)
		viewport.AppendReasoning("one two")
		inputs := pub_models.Input{"path": "/tmp"}
		viewport.AppendTool(pub_models.Call{Name: "ls", Inputs: &inputs}, "first\nsecond", 6)

		got := viewport.Rows()
		want := []string{"  one two", "▸ ls  path=/tmp", "  first", "  second", ""}
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Fatalf("Rows() = %q, want %q", got, want)
		}
	})

	t.Run("sanitizes display-only reasoning and handles tiny limits", func(t *testing.T) {
		viewport := NewActivityViewport(0, 0, 0)
		viewport.AppendReasoning("safe\x1b[31m text\x1b[0m")

		if got, want := viewport.Rows(), []string{"∴ thinking"}; strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Fatalf("Rows() = %q, want %q", got, want)
		}
		if got := viewport.Content(); got != "safe text" {
			t.Fatalf("Content() = %q, want sanitized display content", got)
		}
	})

	t.Run("redraws only the changed trailing rows", func(t *testing.T) {
		t.Setenv("NO_COLOR", "true")
		var out strings.Builder
		viewport := NewActivityViewport(80, 3, 3)
		viewport.AppendReasoning("first")
		if err := viewport.Render(&out); err != nil {
			t.Fatalf("first Render(): %v", err)
		}
		viewport.AppendReasoning(" second")
		if err := viewport.Render(&out); err != nil {
			t.Fatalf("second Render(): %v", err)
		}
		if got := out.String(); !strings.Contains(got, "\x1b[1A\r\x1b[J") {
			t.Fatalf("expected a single-row redraw, got %q", got)
		}
	})

	t.Run("appends new rows without clearing or cursor moves", func(t *testing.T) {
		t.Setenv("NO_COLOR", "true")
		var out strings.Builder
		viewport := NewActivityViewport(80, 6, 6)
		viewport.AppendReasoning("first")
		if err := viewport.Render(&out); err != nil {
			t.Fatalf("first Render(): %v", err)
		}
		viewport.AppendTool(pub_models.Call{Name: "pwd"}, "/tmp", 2)
		if err := viewport.Render(&out); err != nil {
			t.Fatalf("second Render(): %v", err)
		}
		if strings.Contains(out.String(), "\x1b[") {
			t.Fatalf("pure appends must not emit control sequences, got %q", out.String())
		}
		for _, want := range []string{"∴ thinking", "  first", "▸ pwd", "  /tmp"} {
			if !strings.Contains(out.String(), want) {
				t.Fatalf("expected %q in %q", want, out.String())
			}
		}
	})

	t.Run("skips render when content is unchanged", func(t *testing.T) {
		t.Setenv("NO_COLOR", "true")
		var out strings.Builder
		viewport := NewActivityViewport(80, 3, 3)
		viewport.AppendReasoning("first")
		if err := viewport.Render(&out); err != nil {
			t.Fatalf("first Render(): %v", err)
		}
		if err := viewport.Render(&out); err != nil {
			t.Fatalf("second Render(): %v", err)
		}
		if got := strings.Count(out.String(), "  first"); got != 1 {
			t.Fatalf("unchanged render must not reprint rows, got %q", out.String())
		}
	})

	t.Run("full redraw when the window scrolls", func(t *testing.T) {
		t.Setenv("NO_COLOR", "true")
		var out strings.Builder
		viewport := NewActivityViewport(80, 3, 3)
		viewport.AppendReasoning("inspect")
		if err := viewport.Render(&out); err != nil {
			t.Fatalf("first Render(): %v", err)
		}
		viewport.AppendTool(pub_models.Call{Name: "pwd"}, "/tmp", 2)
		if err := viewport.Render(&out); err != nil {
			t.Fatalf("second Render(): %v", err)
		}
		if !strings.Contains(out.String(), "\x1b[2A\r\x1b[J") {
			t.Fatalf("expected a full redraw when the drawn rows shift, got %q", out.String())
		}
	})

	t.Run("pop redraws only the removed block", func(t *testing.T) {
		t.Setenv("NO_COLOR", "true")
		var out strings.Builder
		viewport := NewActivityViewport(80, 6, 6)
		viewport.AppendReasoning("inspect")
		viewport.AppendText("let me check")
		if err := viewport.Render(&out); err != nil {
			t.Fatalf("first Render(): %v", err)
		}
		viewport.RemoveTextBlock()
		if err := viewport.Render(&out); err != nil {
			t.Fatalf("second Render(): %v", err)
		}
		if !strings.Contains(out.String(), "\x1b[2A\r\x1b[J") {
			t.Fatalf("expected the pop to clear only the removed rows, got %q", out.String())
		}
	})

	t.Run("detaches a rendered region without discarding its history", func(t *testing.T) {
		t.Setenv("NO_COLOR", "true")
		var first strings.Builder
		viewport := NewActivityViewport(80, 6, 6)
		viewport.AppendReasoning("inspect")
		if err := viewport.Render(&first); err != nil {
			t.Fatalf("first Render(): %v", err)
		}

		if got, want := viewport.DetachRenderedRegion(), 2; got != want {
			t.Fatalf("DetachRenderedRegion() = %d, want %d", got, want)
		}
		viewport.AppendTool(pub_models.Call{Name: "pwd"}, "/tmp", 2)
		var second strings.Builder
		if err := viewport.Render(&second); err != nil {
			t.Fatalf("second Render(): %v", err)
		}
		if strings.Contains(second.String(), "\x1b[") {
			t.Fatalf("detached viewport must render at the new cursor position, got %q", second.String())
		}
		for _, want := range []string{"∴ thinking", "  inspect", "▸ pwd", "  /tmp"} {
			if !strings.Contains(second.String(), want) {
				t.Fatalf("detached viewport lost %q from history: %q", want, second.String())
			}
		}
	})

	t.Run("uses the reasoning hue only for the thinking header", func(t *testing.T) {
		t.Setenv("NO_COLOR", "false")
		var out strings.Builder
		viewport := NewActivityViewport(80, 3, 3)
		viewport.AppendReasoning("inspect")
		if err := viewport.Render(&out); err != nil {
			t.Fatalf("Render(): %v", err)
		}
		if !strings.Contains(out.String(), RoleColor("reasoning")+"∴ thinking") {
			t.Fatalf("expected warm thinking header, got %q", out.String())
		}
		if strings.Contains(out.String(), RoleColor("reasoning")+"  inspect") {
			t.Fatalf("reasoning body must remain neutral, got %q", out.String())
		}
	})

	t.Run("coalesces streamed assistant prose into one block", func(t *testing.T) {
		viewport := NewActivityViewport(20, 6, 6)
		viewport.AppendText("let me ")
		viewport.AppendText("check")

		got := viewport.Rows()
		want := []string{"assistant", "  let me check"}
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Fatalf("Rows() = %q, want %q", got, want)
		}
		if !viewport.TextBlockActive() {
			t.Fatal("TextBlockActive() = false, want true")
		}
	})

	t.Run("remove text block pops only the trailing assistant prose", func(t *testing.T) {
		viewport := NewActivityViewport(40, 8, 8)
		viewport.AppendReasoning("inspect")
		viewport.AppendText("let me check")

		if got, want := viewport.RemoveTextBlock(), 2; got != want {
			t.Fatalf("RemoveTextBlock() = %d, want %d", got, want)
		}
		got := viewport.Rows()
		want := []string{"∴ thinking", "  inspect"}
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Fatalf("Rows() = %q, want %q", got, want)
		}
		if viewport.TextBlockActive() {
			t.Fatal("TextBlockActive() = true after remove, want false")
		}
		if got := viewport.RemoveTextBlock(); got != 0 {
			t.Fatalf("RemoveTextBlock() on empty block = %d, want 0", got)
		}
	})

	t.Run("finish text starts a new prose block", func(t *testing.T) {
		viewport := NewActivityViewport(40, 8, 8)
		viewport.AppendText("first")
		viewport.FinishText()
		viewport.AppendText("second")

		if got := strings.Count(strings.Join(viewport.Rows(), "\n"), "assistant"); got != 2 {
			t.Fatalf("assistant header count = %d, want 2; rows=%q", got, viewport.Rows())
		}
	})

	t.Run("reasoning and tool blocks close the active prose block", func(t *testing.T) {
		viewport := NewActivityViewport(40, 12, 12)
		viewport.AppendText("let me check")
		viewport.AppendTool(pub_models.Call{Name: "pwd"}, "/tmp", 2)
		viewport.AppendReasoning("verify")
		viewport.AppendText("second")

		if got := strings.Count(strings.Join(viewport.Rows(), "\n"), "assistant"); got != 2 {
			t.Fatalf("assistant header count = %d, want 2; rows=%q", got, viewport.Rows())
		}
	})

	t.Run("uses the assistant role hue for the prose header", func(t *testing.T) {
		t.Setenv("NO_COLOR", "false")
		var out strings.Builder
		viewport := NewActivityViewport(80, 3, 3)
		viewport.AppendText("hello")
		if err := viewport.Render(&out); err != nil {
			t.Fatalf("Render(): %v", err)
		}
		if !strings.Contains(out.String(), RoleColor("assistant")+"assistant") {
			t.Fatalf("expected assistant role color on the prose header, got %q", out.String())
		}
	})
}

func TestAttemptPrettyPrint_ReasoningContent(t *testing.T) {
	// Test raw mode includes reasoning.
	t.Run("raw mode includes reasoning", func(t *testing.T) {
		msg := pub_models.Message{
			Role:             "assistant",
			Content:          "final answer",
			ReasoningContent: "step by step",
		}
		var b strings.Builder
		if err := AttemptPrettyPrint(&b, msg, "user", true); err != nil {
			t.Fatalf("AttemptPrettyPrint: %v", err)
		}
		got := b.String()
		if !strings.Contains(got, "[thinking]") {
			t.Fatalf("expected [thinking] marker, got: %q", got)
		}
		if !strings.Contains(got, "step by step") {
			t.Fatalf("expected reasoning text, got: %q", got)
		}
		if !strings.Contains(got, "final answer") {
			t.Fatalf("expected content text, got: %q", got)
		}
	})

	// Test NO_COLOR includes reasoning.
	t.Run("NO_COLOR includes reasoning", func(t *testing.T) {
		t.Setenv("NO_COLOR", "true")
		msg := pub_models.Message{
			Role:             "assistant",
			Content:          "final answer",
			ReasoningContent: "step by step",
		}
		var b strings.Builder
		if err := AttemptPrettyPrint(&b, msg, "user", false); err != nil {
			t.Fatalf("AttemptPrettyPrint: %v", err)
		}
		got := b.String()
		if !strings.Contains(got, "[thinking]") {
			t.Fatalf("expected [thinking] marker, got: %q", got)
		}
		if !strings.Contains(got, "step by step") {
			t.Fatalf("expected reasoning text, got: %q", got)
		}
	})

	// Test no reasoning leaves output unchanged.
	t.Run("no reasoning unchanged", func(t *testing.T) {
		msg := pub_models.Message{
			Role:    "assistant",
			Content: "just an answer",
		}
		var b strings.Builder
		if err := AttemptPrettyPrint(&b, msg, "user", true); err != nil {
			t.Fatalf("AttemptPrettyPrint: %v", err)
		}
		got := b.String()
		if strings.Contains(got, "[thinking]") {
			t.Fatalf("expected no thinking markers, got: %q", got)
		}
		if !strings.Contains(got, "just an answer") {
			t.Fatalf("expected content, got: %q", got)
		}
	})
}

// failAfterWriter writes up to remaining bytes, then fails. It pins the
// phase-4 frame contract: a partial write must leave the viewport dirty so
// the next Render retries the full frame.
type failAfterWriter struct {
	remaining int
	buf       bytes.Buffer
}

func (w *failAfterWriter) Write(p []byte) (int, error) {
	if len(p) <= w.remaining {
		w.remaining -= len(p)
		w.buf.Write(p)
		return len(p), nil
	}
	n := w.remaining
	if n > 0 {
		w.buf.Write(p[:n])
	}
	w.remaining = 0
	return n, errors.New("injected write failure")
}

// shortWriter accepts at most three bytes per call and reports no error,
// pinning the io.Writer short-write contract: Render must treat a short write
// as a failed frame and retry the full frame on the next call.
type shortWriter struct{ buf strings.Builder }

func (w *shortWriter) Write(p []byte) (int, error) {
	if len(p) > 3 {
		w.buf.Write(p[:3])
		return 3, nil
	}
	w.buf.Write(p)
	return len(p), nil
}

func TestActivityViewportResize(t *testing.T) {
	t.Run("width narrows rewraps retained blocks", func(t *testing.T) {
		t.Setenv("NO_COLOR", "true")
		var out strings.Builder
		viewport := NewActivityViewport(20, 10, 10)
		viewport.AppendReasoning("inspect directory")
		inputs := pub_models.Input{"path": "/tmp"}
		viewport.AppendTool(pub_models.Call{Name: "ls", Inputs: &inputs}, "one two three", 6)
		if err := viewport.Render(&out); err != nil {
			t.Fatalf("Render(): %v", err)
		}

		viewport.Resize(10, 10)

		got := strings.Join(viewport.Rows(), "\n")
		for _, want := range []string{"∴ thinking", "  inspect ", "  director", "  y", "  one two ", "  three"} {
			if !strings.Contains(got, want) {
				t.Fatalf("Rows() after narrow resize missing %q: %q", want, got)
			}
		}
		if strings.Contains(got, "  inspect directory") {
			t.Fatalf("stale old-width row survived the rewrap: %q", got)
		}
		if err := viewport.Render(&out); err != nil {
			t.Fatalf("Render() after resize: %v", err)
		}
		if !strings.Contains(out.String(), "\x1b[5A\r\x1b[J") {
			t.Fatalf("expected a full-frame redraw after resize, got %q", out.String())
		}
	})

	t.Run("reasoning text and tool activity survive reflow", func(t *testing.T) {
		t.Setenv("NO_COLOR", "true")
		viewport := NewActivityViewport(40, 20, 20)
		viewport.AppendReasoning("think step one\nthink step two")
		viewport.AppendText("checking the plan")
		viewport.AppendTool(pub_models.Call{Name: "pwd"}, "/tmp/work", 2)
		viewport.Resize(20, 12)
		got := strings.Join(viewport.Rows(), "\n")
		for _, want := range []string{"∴ thinking", "think step", "assistant", "checking the", "▸ pwd", "  /tmp/work"} {
			if !strings.Contains(got, want) {
				t.Fatalf("Rows() after reflow missing %q: %q", want, got)
			}
		}
	})

	t.Run("height shrink keeps only the effective-height rows", func(t *testing.T) {
		t.Setenv("NO_COLOR", "true")
		var first strings.Builder
		viewport := NewActivityViewport(40, 10, 10)
		viewport.AppendReasoning("line one\nline two\nline three")
		viewport.AppendTool(pub_models.Call{Name: "pwd"}, "a\nb\nc\nd\ne\nf\ng", 6)
		if err := viewport.Render(&first); err != nil {
			t.Fatalf("Render(): %v", err)
		}

		viewport.Resize(40, 5)

		if got := len(viewport.Rows()); got != 5 {
			t.Fatalf("Rows() after shrink = %d, want effective height 5", got)
		}
		var redraw strings.Builder
		if err := viewport.Render(&redraw); err != nil {
			t.Fatalf("Render() after shrink: %v", err)
		}
		if !strings.Contains(redraw.String(), "\x1b[10A\r\x1b[J") {
			t.Fatalf("expected full-frame clear of the old window, got %q", redraw.String())
		}
		for _, gone := range []string{"∴ thinking", "  line one"} {
			if strings.Contains(redraw.String(), gone) {
				t.Fatalf("shrunk window still shows %q: %q", gone, redraw.String())
			}
		}
	})

	t.Run("height grow brings retained content back into view", func(t *testing.T) {
		t.Setenv("NO_COLOR", "true")
		viewport := NewActivityViewport(40, 30, 30) // cap high enough that the terminal height binds
		viewport.Resize(40, 5)
		viewport.AppendReasoning("one\ntwo\nthree\nfour\nfive\nsix\nseven\neight")
		rows := viewport.Rows()
		if len(rows) != 5 {
			t.Fatalf("Rows() at height 5 = %d rows, want 5", len(rows))
		}
		if strings.Contains(strings.Join(rows, "\n"), "  three") {
			t.Fatalf("middle rows must be dropped at height 5: %q", rows)
		}
		viewport.Resize(40, 10)
		got := strings.Join(viewport.Rows(), "\n")
		if !strings.Contains(got, "  three") {
			t.Fatalf("height grow must reappear retained content, got %q", got)
		}
		if len(viewport.Rows()) != 9 {
			t.Fatalf("Rows() after grow = %d rows, want 9", len(viewport.Rows()))
		}
	})

	t.Run("unchanged dimensions leave the viewport clean", func(t *testing.T) {
		t.Setenv("NO_COLOR", "true")
		var first strings.Builder
		viewport := NewActivityViewport(40, 8, 8)
		viewport.AppendReasoning("first")
		if err := viewport.Render(&first); err != nil {
			t.Fatalf("Render(): %v", err)
		}
		viewport.Resize(40, 20) // effective height stays min(8, 20) = 8
		var after strings.Builder
		if err := viewport.Render(&after); err != nil {
			t.Fatalf("Render() after a no-op Resize: %v", err)
		}
		if after.Len() != 0 {
			t.Fatalf("no-op resize must not redraw, got %q", after.String())
		}
	})

	t.Run("resize never writes to the writer", func(t *testing.T) {
		t.Setenv("NO_COLOR", "true")
		var out strings.Builder
		viewport := NewActivityViewport(40, 8, 8)
		viewport.AppendReasoning("first")
		if err := viewport.Render(&out); err != nil {
			t.Fatalf("Render(): %v", err)
		}
		// Resize takes no writer and must not emit anything; Render is the
		// only writer in the viewport contract.
		before := out.String()
		viewport.Resize(30, 6)
		if got := out.String(); got != before {
			t.Fatalf("Resize wrote to the writer: %q", got)
		}
		var redraw strings.Builder
		if err := viewport.Render(&redraw); err != nil {
			t.Fatalf("Render(): %v", err)
		}
		if !strings.Contains(redraw.String(), "\x1b[2A\r\x1b[J") {
			t.Fatalf("expected the resize redraw to go through Render, got %q", redraw.String())
		}
	})

	t.Run("resize before the first append renders at the new dimensions", func(t *testing.T) {
		t.Setenv("NO_COLOR", "true")
		var out strings.Builder
		viewport := NewActivityViewport(80, 30, 30)
		viewport.Resize(40, 10)
		viewport.AppendReasoning("one two three four five six seven eight")
		want := []string{"∴ thinking", "  one two three four five six seven eigh", "  t"}
		if got := viewport.Rows(); strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Fatalf("Rows() = %q, want %q", got, want)
		}
		if err := viewport.Render(&out); err != nil {
			t.Fatalf("Render(): %v", err)
		}
		if !strings.Contains(out.String(), "  t") {
			t.Fatalf("first render must use the resized width, got %q", out.String())
		}
	})

	t.Run("two consecutive resizes converge on the last dimensions", func(t *testing.T) {
		t.Setenv("NO_COLOR", "true")
		var first strings.Builder
		viewport := NewActivityViewport(80, 30, 30)
		viewport.AppendReasoning("one two three four five six seven eight")
		if err := viewport.Render(&first); err != nil {
			t.Fatalf("Render(): %v", err)
		}
		viewport.Resize(60, 20)
		viewport.Resize(40, 10)
		var redraw strings.Builder
		if err := viewport.Render(&redraw); err != nil {
			t.Fatalf("Render() after resizes: %v", err)
		}
		if !strings.Contains(redraw.String(), "\x1b[2A\r\x1b[J") {
			t.Fatalf("expected one full frame over the old region, got %q", redraw.String())
		}
		if !strings.Contains(redraw.String(), "  t") {
			t.Fatalf("expected the last width to win, got %q", redraw.String())
		}
		var clean strings.Builder
		if err := viewport.Render(&clean); err != nil {
			t.Fatalf("Render() after clean: %v", err)
		}
		if clean.Len() != 0 {
			t.Fatalf("clean viewport must not redraw, got %q", clean.String())
		}
	})

	t.Run("resize while a text block is active keeps coalescing", func(t *testing.T) {
		t.Setenv("NO_COLOR", "true")
		var out strings.Builder
		viewport := NewActivityViewport(80, 10, 10)
		viewport.AppendText("alpha beta")
		viewport.Resize(10, 10)
		viewport.AppendText(" gamma delta epsilon")
		if err := viewport.Render(&out); err != nil {
			t.Fatalf("Render(): %v", err)
		}
		got := strings.Join(viewport.Rows(), "\n")
		if strings.Count(got, "assistant") != 1 {
			t.Fatalf("text must coalesce into one block after resize, got %q", got)
		}
		if !strings.Contains(got, "  alpha be") || !strings.Contains(got, "  psilon") {
			t.Fatalf("streamed tokens must rewrap and continue at the new width, got %q", got)
		}
		if !viewport.TextBlockActive() {
			t.Fatal("TextBlockActive() = false after resize, want true")
		}
	})

	t.Run("resize during the final-answer pop keeps the pop correct", func(t *testing.T) {
		t.Setenv("NO_COLOR", "true")
		var first strings.Builder
		viewport := NewActivityViewport(80, 10, 10)
		viewport.AppendReasoning("inspect")
		viewport.AppendText("final answer text")
		if err := viewport.Render(&first); err != nil {
			t.Fatalf("Render(): %v", err)
		}
		if removed := viewport.RemoveTextBlock(); removed == 0 {
			t.Fatal("RemoveTextBlock() = 0, want the answer block removed")
		}
		viewport.Resize(40, 10)
		var redraw strings.Builder
		if err := viewport.Render(&redraw); err != nil {
			t.Fatalf("Render() after pop and resize: %v", err)
		}
		if strings.Contains(redraw.String(), "final answer text") {
			t.Fatalf("popped answer must stay out of the window, got %q", redraw.String())
		}
		if !strings.Contains(redraw.String(), "∴ thinking") {
			t.Fatalf("retained reasoning must survive the pop and resize, got %q", redraw.String())
		}
	})

	t.Run("terminal shrink below the drawn window clears and rewrites", func(t *testing.T) {
		t.Setenv("NO_COLOR", "true")
		var first strings.Builder
		viewport := NewActivityViewport(40, 10, 10)
		viewport.AppendReasoning("one\ntwo\nthree")
		viewport.AppendTool(pub_models.Call{Name: "pwd"}, "a\nb", 6)
		if err := viewport.Render(&first); err != nil {
			t.Fatalf("Render(): %v", err)
		}
		viewport.Resize(40, 3)
		if got := len(viewport.Rows()); got != 3 {
			t.Fatalf("Rows() after shrink = %d, want effective height 3", got)
		}
		var redraw strings.Builder
		if err := viewport.Render(&redraw); err != nil {
			t.Fatalf("Render() after shrink: %v", err)
		}
		if !strings.Contains(redraw.String(), "\x1b[8A\r\x1b[J") {
			t.Fatalf("expected full clear of the old window, got %q", redraw.String())
		}
		if strings.Contains(redraw.String(), "∴ thinking") {
			t.Fatalf("shrunk window must not reprint evicted rows, got %q", redraw.String())
		}
	})

	t.Run("partial frame write leaves the viewport dirty and retries", func(t *testing.T) {
		t.Setenv("NO_COLOR", "true")
		var first strings.Builder
		viewport := NewActivityViewport(40, 10, 10)
		viewport.AppendReasoning("inspect")
		if err := viewport.Render(&first); err != nil {
			t.Fatalf("Render(): %v", err)
		}
		viewport.Resize(30, 10)
		failing := &failAfterWriter{remaining: 4}
		if err := viewport.Render(failing); err == nil {
			t.Fatal("Render() with a failing writer must return an error")
		}
		var retry strings.Builder
		if err := viewport.Render(&retry); err != nil {
			t.Fatalf("Render() retry: %v", err)
		}
		if !strings.Contains(retry.String(), "\x1b[2A\r\x1b[J") {
			t.Fatalf("retry must emit a full frame over the old region, got %q", retry.String())
		}
		if !strings.Contains(retry.String(), "  inspect") {
			t.Fatalf("retry must rewrite the retained rows, got %q", retry.String())
		}
		var clean strings.Builder
		if err := viewport.Render(&clean); err != nil {
			t.Fatalf("Render() after clean: %v", err)
		}
		if clean.Len() != 0 {
			t.Fatalf("viewport must be clean after a successful full frame, got %q", clean.String())
		}
	})

	t.Run("write failure on the diff path marks the viewport dirty", func(t *testing.T) {
		t.Setenv("NO_COLOR", "true")
		var first strings.Builder
		viewport := NewActivityViewport(40, 10, 10)
		viewport.AppendReasoning("first")
		if err := viewport.Render(&first); err != nil {
			t.Fatalf("Render(): %v", err)
		}
		viewport.AppendReasoning(" second")
		if err := viewport.Render(&failAfterWriter{}); err == nil {
			t.Fatal("Render() with a failing writer must return an error")
		}
		var retry strings.Builder
		if err := viewport.Render(&retry); err != nil {
			t.Fatalf("Render() retry: %v", err)
		}
		if !strings.Contains(retry.String(), "\x1b[2A\r\x1b[J") {
			t.Fatalf("retry must emit a full frame, got %q", retry.String())
		}
	})

	t.Run("resize with no content renders nothing and stays clean", func(t *testing.T) {
		t.Setenv("NO_COLOR", "true")
		var out strings.Builder
		viewport := NewActivityViewport(40, 8, 8)
		viewport.Resize(30, 6)
		if err := viewport.Render(&out); err != nil {
			t.Fatalf("Render(): %v", err)
		}
		if out.Len() != 0 {
			t.Fatalf("empty dirty viewport must render nothing, got %q", out.String())
		}
		if err := viewport.Render(&out); err != nil {
			t.Fatalf("second Render(): %v", err)
		}
		if out.Len() != 0 {
			t.Fatalf("clean viewport must stay silent, got %q", out.String())
		}
	})

	t.Run("short write without error leaves the viewport dirty", func(t *testing.T) {
		t.Setenv("NO_COLOR", "true")
		var first strings.Builder
		viewport := NewActivityViewport(40, 10, 10)
		viewport.AppendReasoning("inspect")
		if err := viewport.Render(&first); err != nil {
			t.Fatalf("Render(): %v", err)
		}
		viewport.Resize(30, 10)
		if err := viewport.Render(&shortWriter{}); err == nil {
			t.Fatal("Render() with a short writer must return an error")
		}
		var retry strings.Builder
		if err := viewport.Render(&retry); err != nil {
			t.Fatalf("Render() retry: %v", err)
		}
		if !strings.Contains(retry.String(), "\x1b[2A\r\x1b[J") {
			t.Fatalf("retry must emit a full frame over the old region, got %q", retry.String())
		}
	})

	t.Run("empty text block renders stable empty rows", func(t *testing.T) {
		t.Setenv("NO_COLOR", "true")
		var out strings.Builder
		viewport := NewActivityViewport(40, 8, 8)
		viewport.AppendText("")
		want := []string{"assistant", "  "}
		if got := viewport.Rows(); strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Fatalf("Rows() = %q, want %q", got, want)
		}
		if err := viewport.Render(&out); err != nil {
			t.Fatalf("Render(): %v", err)
		}
		if err := viewport.Render(&out); err != nil {
			t.Fatalf("second Render(): %v", err)
		}
		if got := strings.Count(out.String(), "assistant"); got != 1 {
			t.Fatalf("unchanged empty block must render once, got %d copies", got)
		}
	})

	t.Run("effective height is the cap capped by terminal height", func(t *testing.T) {
		viewport := NewActivityViewport(80, 10, 10)
		viewport.Resize(80, 99)
		viewport.AppendReasoning("one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nnine\nten\neleven\ntwelve")
		if got := len(viewport.Rows()); got != 10 {
			t.Fatalf("Rows() = %d rows, want the cap 10", got)
		}
		viewport.Resize(80, 4)
		if got := len(viewport.Rows()); got != 4 {
			t.Fatalf("Rows() = %d rows, want terminal height 4", got)
		}
		viewport.Resize(80, 0)
		if got := len(viewport.Rows()); got != 1 {
			t.Fatalf("Rows() = %d rows, want clamped height 1", got)
		}
	})

	t.Run("initial height binds the terminal height at creation", func(t *testing.T) {
		viewport := NewActivityViewport(40, 30, 5)
		viewport.AppendReasoning("one\ntwo\nthree\nfour\nfive\nsix\nseven\neight")
		if got := len(viewport.Rows()); got != 5 {
			t.Fatalf("Rows() = %d rows, want the terminal height 5 bound at creation", got)
		}
		// The min(cap, terminal height) binding stays live: growing the
		// terminal reveals retained content without any prior Resize.
		viewport.Resize(40, 8)
		if got := len(viewport.Rows()); got != 8 {
			t.Fatalf("Rows() after terminal grow = %d rows, want 8", got)
		}
	})

	t.Run("clamps width and height below one", func(t *testing.T) {
		t.Setenv("NO_COLOR", "true")
		viewport := NewActivityViewport(0, 0, 0)
		viewport.Resize(-10, -10)
		viewport.AppendReasoning("x")
		want := []string{"∴ thinking"}
		if got := viewport.Rows(); strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Fatalf("Rows() = %q, want %q", got, want)
		}
	})

	t.Run("retention evicts the oldest finished blocks", func(t *testing.T) {
		viewport := NewActivityViewport(80, 30, 30)
		for i := range maxActivityBlocks + 4 {
			viewport.AppendReasoning(fmt.Sprintf("block %d", i))
			viewport.FinishReasoning()
		}
		if got := len(viewport.blocks); got != maxActivityBlocks {
			t.Fatalf("retained blocks = %d, want %d", got, maxActivityBlocks)
		}
		if got, want := viewport.blocks[0].content, "block 4"; got != want {
			t.Fatalf("oldest retained block = %q, want %q", got, want)
		}
	})

	t.Run("retention bounds content per block", func(t *testing.T) {
		viewport := NewActivityViewport(80, 30, 30)
		big := strings.Repeat("x", maxActivityBlockRunes*2)
		viewport.AppendReasoning(big)
		got := viewport.activeReasoning.content
		if utf8.RuneCountInString(got) > maxActivityBlockRunes {
			t.Fatalf("retained content = %d runes, want at most %d", utf8.RuneCountInString(got), maxActivityBlockRunes)
		}
		before, after, found := strings.Cut(got, contentTruncationMarker)
		if !found {
			t.Fatal("expected the truncation marker in over-budget content")
		}
		if before != strings.Repeat("x", utf8.RuneCountInString(before)) {
			t.Fatal("content head must survive unchanged")
		}
		if after != strings.Repeat("x", utf8.RuneCountInString(after)) {
			t.Fatal("content tail must survive unchanged")
		}
		if utf8.RuneCountInString(before)+utf8.RuneCountInString(after)+utf8.RuneCountInString(contentTruncationMarker) > maxActivityBlockRunes {
			t.Fatal("retained content must stay within the rune budget")
		}
	})

	t.Run("nil viewport is safe", func(t *testing.T) {
		var viewport *ActivityViewport
		viewport.AppendReasoning("x")
		viewport.AppendText("y")
		viewport.AppendTool(pub_models.Call{Name: "pwd"}, "z", 2)
		viewport.FinishReasoning()
		viewport.FinishText()
		viewport.Resize(40, 10)
		if got := viewport.Rows(); got != nil {
			t.Fatalf("Rows() on nil = %v, want nil", got)
		}
		if got := viewport.Content(); got != "" {
			t.Fatalf("Content() on nil = %q, want empty", got)
		}
		if got := viewport.RemoveTextBlock(); got != 0 {
			t.Fatalf("RemoveTextBlock() on nil = %d, want 0", got)
		}
		if got := viewport.DetachRenderedRegion(); got != 0 {
			t.Fatalf("DetachRenderedRegion() on nil = %d, want 0", got)
		}
		if viewport.TextBlockActive() {
			t.Fatal("TextBlockActive() on nil = true, want false")
		}
		var out strings.Builder
		if err := viewport.Render(&out); err != nil {
			t.Fatalf("Render() on nil: %v", err)
		}
	})
}
