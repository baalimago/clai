package text

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/go_away_boilerplate/pkg/dimensions"

	"github.com/baalimago/clai/internal/vendors"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	pkgtools "github.com/baalimago/clai/pkg/tools"
)

func Test_toolCallError(t *testing.T) {
	tests := []struct {
		name    string
		out     string
		wantErr bool
	}{
		{name: "error convention output", out: "ERROR: command exploded", wantErr: true},
		{name: "plain output", out: "42 files found", wantErr: false},
		{name: "empty output", out: "", wantErr: false},
		{name: "error marker mid-output", out: "the log said ERROR: nope", wantErr: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := toolCallError("bash", tt.out)
			if (err != nil) != tt.wantErr {
				t.Errorf("toolCallError(%q) = %v, wantErr %v", tt.out, err, tt.wantErr)
			}
			if err != nil {
				if !strings.Contains(err.Error(), "command exploded") {
					t.Errorf("error lost the tool output: %v", err)
				}
				if !strings.Contains(err.Error(), `"bash"`) {
					t.Errorf("error does not name the tool call: %v", err)
				}
			}
		})
	}
}

func Test_finalizeAssistantTextPlain_EchoedToolCallDropped(t *testing.T) {
	var out strings.Builder
	q := &Querier[*MockQuerier]{
		Raw:  true, // raw display: no terminal clearing, no pretty print
		out:  &out,
		dims: dimensions.Dimensions{Width: 80, Height: 24},
	}
	e := toolExecutor[*MockQuerier]{querier: q}
	call := pub_models.Call{Name: "cat", Inputs: &pub_models.Input{}}
	session := &QuerySession{}

	if err := e.finalizeAssistantTextPlain(t.Context(), session, call.PrettyPrint(), call); err != nil {
		t.Fatalf("finalizeAssistantTextPlain: %v", err)
	}
	if session.FinalAssistantText != "" {
		t.Errorf("echoed tool-call text kept as assistant prose: %q", session.FinalAssistantText)
	}
	if q.fullMsg != "" {
		t.Errorf("echoed tool-call text kept in querier state: %q", q.fullMsg)
	}
}

func Test_finalizeAssistantTextPlain_ProseIsFinalized(t *testing.T) {
	var out strings.Builder
	q := &Querier[*MockQuerier]{
		out:              &out,
		outputModeKnown:  true,
		outputIsTerminal: true,
		dims:             dimensions.Dimensions{Width: 80, Height: 24},
	}
	e := toolExecutor[*MockQuerier]{querier: q}
	call := pub_models.Call{Name: "cat", Inputs: &pub_models.Input{}}
	session := &QuerySession{}

	if err := e.finalizeAssistantTextPlain(t.Context(), session, "let me check the file", call); err != nil {
		t.Fatalf("finalizeAssistantTextPlain: %v", err)
	}
	if session.FinalAssistantText != "let me check the file" {
		t.Errorf("FinalAssistantText = %q, want the streamed prose", session.FinalAssistantText)
	}
	if !strings.Contains(out.String(), "let me check the file") {
		t.Errorf("prose not re-printed as a proper message; out: %q", out.String())
	}
}

// writeMockPriceFile writes the mock vendor's model config with a price
// entry, so the cost manager becomes ready and enriches the chat.
func writeMockPriceFile(t *testing.T, confDir string) {
	t.Helper()
	price := `{"price":{"input_usd_per_token":0.001,"input_cached_usd_per_token":0.0005,"output_usd_per_token":0.002}}`
	if err := os.WriteFile(filepath.Join(confDir, "mock_test_test.json"), []byte(price), 0o644); err != nil {
		t.Fatalf("write price file: %v", err)
	}
}

// toolResult returns the content of the first tool message in chat.
func toolResult(t *testing.T, chat pub_models.Chat) string {
	t.Helper()
	for _, msg := range chat.Messages {
		if msg.Role == "tool" {
			return msg.Content
		}
	}
	t.Fatalf("no tool message in %d messages", len(chat.Messages))
	return ""
}

// TestToolExecutor_attachesCmdBanPolicy pins the D29 attachment point: the
// executor's single invoke site carries the querier's own list, so a banned
// command is refused inside the tool result and never spawns.
func TestToolExecutor_attachesCmdBanPolicy(t *testing.T) {
	run := func(t *testing.T, cmdBan []string, marker string) pub_models.Chat {
		t.Helper()
		q := &Querier[*MockQuerier]{
			Raw:     true,
			out:     &strings.Builder{},
			cmdBan:  cmdBan,
			tooling: tooling{run: map[string]pub_models.LLMTool{"cmd": pkgtools.Cmd}},
		}
		session := &QuerySession{}
		call := pub_models.Call{ID: "c1", Name: "cmd", Inputs: &pub_models.Input{"command": "touch " + marker}}
		if err := (toolExecutor[*MockQuerier]{querier: q}).Execute(t.Context(), session, call); err != nil {
			t.Fatalf("Execute: %v", err)
		}
		return session.Chat
	}

	banned := filepath.Join(t.TempDir(), "banned-marker")
	out := toolResult(t, run(t, []string{"touch"}, banned))
	if !strings.HasPrefix(out, "ERROR:") || !strings.Contains(out, `matched entry "touch"`) {
		t.Fatalf("tool result = %q, want a refusal naming touch", out)
	}
	if _, err := os.Stat(banned); !os.IsNotExist(err) {
		t.Fatalf("banned command must never spawn, marker stat: %v", err)
	}

	allowed := filepath.Join(t.TempDir(), "allowed-marker")
	if out := toolResult(t, run(t, nil, allowed)); strings.Contains(out, "banned by policy") {
		t.Fatalf("empty list must be permissive, got %q", out)
	}
	if _, err := os.Stat(allowed); err != nil {
		t.Fatalf("permissive run must spawn: %v", err)
	}
}

// TestToolExecutor_secondQuerierDoesNotAlterPolicy is the integration proof
// of D29 through the real querier, mock vendor and cmd tool: constructing a
// second querier with an empty list mid-run leaves the first run's policy in
// force, and the second querier itself stays permissive.
func TestToolExecutor_secondQuerierDoesNotAlterPolicy(t *testing.T) {
	t.Setenv("CLAI_DISABLE_COST_ERR_LOG_GOROUTINE", "1")
	confDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(confDir, "mcpServers"), 0o755); err != nil {
		t.Fatalf("mkdir mcpServers: %v", err)
	}
	writeMockPriceFile(t, confDir)
	newQuerier := func(cmdBan []string) *Querier[*vendors.Mock] {
		t.Helper()
		q, err := NewQuerier(t.Context(), Configurations{
			Model:              "test",
			ConfigDir:          confDir,
			UseTools:           true,
			RequestedToolGlobs: []string{"cmd"},
			CmdBan:             cmdBan,
			Raw:                true,
			Out:                &strings.Builder{},
		}, &vendors.Mock{})
		if err != nil {
			t.Fatalf("NewQuerier: %v", err)
		}
		return &q
	}
	query := func(q *Querier[*vendors.Mock], id, marker string) pub_models.Chat {
		t.Helper()
		t.Setenv("CLAI_MOCK_CMD_COMMAND", "touch "+marker)
		chat, err := q.TextQuery(t.Context(), pub_models.Chat{ID: id, Messages: []pub_models.Message{{Role: "user", Content: "please tool_cmd"}}})
		if err != nil {
			t.Fatalf("TextQuery: %v", err)
		}
		return chat
	}

	a := newQuerier([]string{"touch"})
	b := newQuerier(nil)

	markerA := filepath.Join(t.TempDir(), "a-marker")
	if out := toolResult(t, query(a, "a", markerA)); !strings.HasPrefix(out, "ERROR:") || !strings.Contains(out, `matched entry "touch"`) {
		t.Fatalf("querier A tool result = %q, want a refusal naming touch", out)
	}
	if _, err := os.Stat(markerA); !os.IsNotExist(err) {
		t.Fatalf("A's banned command must never spawn, marker stat: %v", err)
	}

	markerB := filepath.Join(t.TempDir(), "b-marker")
	if out := toolResult(t, query(b, "b", markerB)); strings.Contains(out, "banned by policy") {
		t.Fatalf("querier B must be permissive, got %q", out)
	}
	if _, err := os.Stat(markerB); err != nil {
		t.Fatalf("B's permissive command must spawn: %v", err)
	}
}
