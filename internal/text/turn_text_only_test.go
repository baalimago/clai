package text

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/baalimago/clai/internal/vendors/anthropic"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

const textOnlyGoldenDir = "testdata/turn_text_only"

// textOnlyReplies are an Anthropic stream that calls ls, then answers.
var textOnlyReplies = []string{
	"event: content_block_start\n" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"ls","input":{}}}` + "\n\n" +
		"event: content_block_stop\n" +
		`data: {"type":"content_block_stop","index":0}` + "\n\n",
	"event: content_block_start\n" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
		"event: content_block_delta\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"One file: a.txt"}}` + "\n\n" +
		"event: content_block_stop\n" +
		`data: {"type":"content_block_stop","index":0}` + "\n\n" +
		"event: message_stop\n" +
		`data: {"type":"message_stop"}` + "\n\n",
}

type fixedTool struct {
	name, out string
}

func (f fixedTool) Call(pub_models.Input) (string, error) { return f.out, nil }

func (f fixedTool) Specification() pub_models.Specification {
	return pub_models.Specification{Name: f.name, Description: "fixed " + f.name, Inputs: &pub_models.InputSchema{Type: "object"}}
}

// textOnlyRun is one observed text-only turn: request bodies, output and the files it persisted.
type textOnlyRun struct {
	bodies []string
	out    string
	global string
	conv   string
}

var (
	createdField = regexp.MustCompile(`"created": ?"[^"]*"`)
	originField  = regexp.MustCompile(`"origin_dir": ?"[^"]*"`)
)

func normalizePersisted(t *testing.T, b []byte, id string) string {
	t.Helper()
	s := createdField.ReplaceAllString(string(b), `"created":"<created>"`)
	s = originField.ReplaceAllString(s, `"origin_dir":"<origin>"`)
	if id != "" {
		s = strings.ReplaceAll(s, id, "<id>")
	}
	return s
}

// runTextOnlyTurn runs a turn through the real Anthropic encoder: the user asks, the
// model calls ls, then answers. prepare may adjust the querier before the run; the
// session's chat is copied to sessionChat when it is set.
func runTextOnlyTurn(t *testing.T, prepare func(q *Querier[*anthropic.Claude], configDir string), sessionChat *pub_models.Chat) textOnlyRun {
	t.Helper()
	var (
		mu     sync.Mutex
		bodies []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		mu.Lock()
		n := len(bodies)
		bodies = append(bodies, string(b))
		mu.Unlock()
		if n >= len(textOnlyReplies) {
			t.Errorf("unexpected request %d", n+1)
			return
		}
		_, _ = io.WriteString(w, textOnlyReplies[n])
	}))
	defer srv.Close()

	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	claude := &anthropic.Claude{Model: "claude-sonnet-5-5", MaxTokens: 1024, AnthropicVersion: "2023-06-01", URL: srv.URL}
	if err := claude.Setup(); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	ls := fixedTool{name: "ls", out: "a.txt"}
	claude.RegisterTool(ls)
	configDir := t.TempDir()
	var out strings.Builder
	q := &Querier[*anthropic.Claude]{
		Raw:       true,
		out:       &out,
		configDir: configDir,
		Model:     claude,
		tooling:   tooling{run: map[string]pub_models.LLMTool{"ls": ls}},
	}
	if prepare != nil {
		prepare(q, configDir)
	}
	session := &QuerySession{
		Chat: pub_models.Chat{Messages: []pub_models.Message{
			{Role: "system", Content: "You are terse."},
			{Role: "user", Content: "list the files"},
		}},
		ShouldSaveReply: true,
		Raw:             true,
	}
	runner := sessionRunner[*anthropic.Claude]{
		querier:      q,
		finalizer:    sessionFinalizer[*anthropic.Claude]{querier: q},
		toolExecutor: toolExecutor[*anthropic.Claude]{querier: q},
	}
	if err := runner.Run(context.Background(), session); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if sessionChat != nil {
		*sessionChat = session.Chat
	}
	convDir := filepath.Join(configDir, "conversations")
	global, err := os.ReadFile(filepath.Join(convDir, "globalScope.json"))
	if err != nil {
		t.Fatalf("read globalScope: %v", err)
	}
	entries, err := os.ReadDir(convDir)
	if err != nil {
		t.Fatalf("list conversations: %v", err)
	}
	var promoted []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") && e.Name() != "globalScope.json" && !strings.HasPrefix(e.Name(), "index") {
			promoted = append(promoted, e.Name())
		}
	}
	if len(promoted) != 1 {
		t.Fatalf("want one promoted conversation, got %v", promoted)
	}
	id := strings.TrimSuffix(promoted[0], ".json")
	conv, err := os.ReadFile(filepath.Join(convDir, promoted[0]))
	if err != nil {
		t.Fatalf("read promoted conversation: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	return textOnlyRun{
		bodies: append([]string(nil), bodies...),
		out:    out.String(),
		global: normalizePersisted(t, global, id),
		conv:   normalizePersisted(t, conv, id),
	}
}

func (r textOnlyRun) files() map[string]string {
	m := map[string]string{"output.txt": r.out, "globalScope.json": r.global, "conversation.json": r.conv}
	for i, b := range r.bodies {
		m["request-"+string(rune('1'+i))+".json"] = b
	}
	return m
}

// assertTextOnlyGolden compares run with the goldens captured from the runner before
// this worklog's turn wiring (I10).
func assertTextOnlyGolden(t *testing.T, run textOnlyRun) {
	t.Helper()
	for name, got := range run.files() {
		want, err := os.ReadFile(filepath.Join(textOnlyGoldenDir, name))
		if err != nil {
			t.Fatalf("read golden %s: %v", name, err)
		}
		if got != string(want) {
			t.Errorf("%s differs from the pre-worklog golden\ngot:\n%s\nwant:\n%s", name, got, want)
		}
	}
}
