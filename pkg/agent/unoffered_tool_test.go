package agent

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/testenv/stubllm"
	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/clai/pkg/text/models"
)

// queryCallingCmd runs one query of an agent built from opts against a model
// that answers with a cmd call creating marker. It returns the chat and the
// tool names the first request offered.
func queryCallingCmd(t *testing.T, marker string, opts ...Option) (models.Chat, []string) {
	t.Helper()
	// A host that has run any tool-using agent holds every built-in in the
	// process registry.
	tools.Init(context.Background())
	cfgDir := filepath.Join(t.TempDir(), "clai")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", cfgDir, err)
	}
	provider := stubllm.Start(t, func(body string) stubllm.Answer {
		if stubllm.HasToolResult(body) {
			return stubllm.Answer{Text: "done"}
		}
		return stubllm.TouchCall("cmd", marker)
	})
	provider.WriteModel(t, cfgDir, "gpt-stub")
	a := New(append([]Option{WithConfigDir(cfgDir), WithModel("gpt-stub")}, opts...)...)
	if err := a.Setup(t.Context()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	chat, err := a.Query(t.Context(), models.Chat{Messages: []models.Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	offered := stubllm.OfferedTools(t, provider.Requests()[0])
	slices.Sort(offered)
	return chat, offered
}

func toolResults(chat models.Chat) []string {
	var out []string
	for _, msg := range chat.Messages {
		if msg.Role == "tool" {
			out = append(out, msg.Content)
		}
	}
	return out
}

// An agent narrowed by WithTools or WithToolGlobs refuses a call of a
// built-in it did not offer.
func TestAgentRefusesUnofferedToolCall(t *testing.T) {
	rows := []struct {
		name    string
		opt     Option
		offered []string
	}{
		{name: "WithTools", opt: WithTools([]models.LLMTool{&mockTool{}}), offered: []string{"mockTool"}},
		{name: "WithToolGlobs", opt: WithToolGlobs("pwd"), offered: []string{"pwd"}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "ran")

			chat, offered := queryCallingCmd(t, marker, row.opt)

			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("the unoffered tool ran: marker stat err = %v, want not exist", err)
			}
			if !slices.Equal(offered, row.offered) {
				t.Fatalf("offered tools = %v, want %v", offered, row.offered)
			}
			if got := toolResults(chat); !slices.Equal(got, []string{"ERROR: unknown tool call: cmd"}) {
				t.Fatalf("tool results = %q, want the unknown-tool refusal", got)
			}
		})
	}
}

// An agent with no tool option offers every visible built-in, cmd included,
// and so runs a cmd call (README, Tools).
func TestAgentWithoutToolOptionOffersEveryBuiltin(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")

	chat, offered := queryCallingCmd(t, marker)

	for _, name := range []string{"cmd", "write_file", "apply_patch"} {
		if !slices.Contains(offered, name) {
			t.Fatalf("offered tools = %v, want %q among them", offered, name)
		}
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the offered cmd call did not run: %v", err)
	}
	if got := toolResults(chat); len(got) != 1 || strings.HasPrefix(got[0], "ERROR") {
		t.Fatalf("tool results = %q, want one successful result", got)
	}
}
