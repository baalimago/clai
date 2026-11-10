package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// providerStep answers request n (from 1) of a scripted provider.
type providerStep func(w http.ResponseWriter, r *http.Request, n int32)

// scriptedProvider serves model gpt-<name> over the OpenAI chat-completions
// route with step, and returns its request counter.
func scriptedProvider(t *testing.T, name string, step providerStep) *atomic.Int32 {
	t.Helper()
	t.Setenv("OPENAI_API_KEY", "stub-key")
	return serveModel(t, "openai_gpt_gpt-"+name+".json", "gpt-"+name, "/v1/chat/completions", step)
}

// scriptedAnthropic serves model claude-<name> over the Messages route.
func scriptedAnthropic(t *testing.T, name string, step providerStep) *atomic.Int32 {
	t.Helper()
	t.Setenv("ANTHROPIC_API_KEY", "stub-key")
	return serveModel(t, "anthropic_claude_claude-"+name+".json", "claude-"+name, "/v1/messages", step)
}

func serveModel(t *testing.T, confName, model, route string, step providerStep) *atomic.Int32 {
	t.Helper()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The server notices a client that left only once the body is read.
		_, _ = io.Copy(io.Discard, r.Body)
		step(w, r, requests.Add(1))
	}))
	t.Cleanup(server.Close)
	confDir := os.Getenv("CLAI_CONFIG_DIR")
	conf, err := json.Marshal(map[string]any{"model": model, "url": server.URL + route})
	if err != nil {
		t.Fatalf("marshal model config: %v", err)
	}
	if err := os.MkdirAll(confDir, 0o755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(confDir, confName), conf, 0o644); err != nil {
		t.Fatalf("write model config: %v", err)
	}
	return &requests
}

// sse writes chunks as one event stream response and flushes each.
func sse(w http.ResponseWriter, chunks ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, chunk := range chunks {
		fmt.Fprint(w, chunk)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}
}

func chatDelta(text string) string {
	content, _ := json.Marshal(text)
	return fmt.Sprintf("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":%s}}]}\n\n", content)
}

func chatReasoning(text string) string {
	content, _ := json.Marshal(text)
	return fmt.Sprintf("data: {\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":%s}}]}\n\n", content)
}

func chatToolCall(name, arguments string) string {
	args, _ := json.Marshal(arguments)
	return fmt.Sprintf("data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":%q,\"arguments\":%s}}]}}]}\n\n", name, args)
}

const (
	chatFinishStop  = "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"
	chatFinishTools = "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n"
	chatDone        = "data: [DONE]\n\n"
)

// syncBuffer is a stream the run and the test may touch concurrently.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// runProcess runs clai in-process over buffers; adjust changes the process
// and the collaborators before the run.
func runProcess(t *testing.T, args string, adjust func(*process, *Deps)) cliRun {
	t.Helper()
	t.Chdir(t.TempDir())
	var stdout, stderr syncBuffer
	p := osProcess()
	p.stdin = strings.NewReader("")
	p.stdout = &stdout
	p.stderr = &stderr
	p.stdinTerminal, p.stdoutTerminal, p.stderrTerminal = false, false, false
	deps := scenarioDeps()
	if adjust != nil {
		adjust(&p, &deps)
	}
	status := run(strings.Fields(args), deps, p)
	return cliRun{status: status, stdout: stdout.String(), stderr: stderr.String()}
}

// recordTypes lists the type of each JSON line of stream; a line that is no
// JSON object with a string type fails the test.
func recordTypes(t *testing.T, stream string) []string {
	t.Helper()
	var types []string
	for line := range strings.Lines(stream) {
		var record struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(line), &record); err != nil || record.Type == "" {
			t.Fatalf("line is not a record: %q (%v)", line, err)
		}
		types = append(types, record.Type)
	}
	return types
}

// savedMessages reads the roles and contents of the conversation the run
// persisted as the previous query.
func savedMessages(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(os.Getenv("CLAI_CONFIG_DIR"), "conversations", "globalScope.json"))
	if err != nil {
		t.Fatalf("read the persisted conversation: %v", err)
	}
	var chat struct {
		Messages []struct {
			Role      string            `json:"role"`
			Content   any               `json:"content"`
			ToolCalls []json.RawMessage `json:"tool_calls"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &chat); err != nil {
		t.Fatalf("decode the persisted conversation: %v", err)
	}
	var out []string
	for _, m := range chat.Messages {
		if m.Role == "system" {
			continue
		}
		entry := fmt.Sprintf("%s:%v", m.Role, m.Content)
		if len(m.ToolCalls) > 0 {
			entry = fmt.Sprintf("%s:calls=%d", m.Role, len(m.ToolCalls))
		}
		out = append(out, entry)
	}
	return out
}
