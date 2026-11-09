package text

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/testenv/procenv"
	"github.com/baalimago/clai/internal/tools"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type discardToolBox struct{}

func (discardToolBox) RegisterTool(pub_models.LLMTool) {}

// offeredTools is the offer of a querier built by literal: every tool of
// run, offered under its own name.
func offeredTools(run map[string]pub_models.LLMTool) *tools.Offered {
	offered := tools.NewOffered()
	for _, tool := range run {
		offered.Offer(discardToolBox{}, tool)
	}
	return offered
}

func TestOfferedTools_keysByTheToolsOwnName(t *testing.T) {
	offered := offeredTools(map[string]pub_models.LLMTool{"ignored": setupToolsTestTool{name: "cat"}})
	if _, ok := offered.Get("cat"); !ok {
		t.Fatalf("offered = %v, want cat", offered.Names())
	}
}

// offeredRegistry is the offer of a querier built by literal in a test that
// registers its tools in the process registry: every registered tool, and a
// stub under each of names for the tools the executor dispatches itself
// (load_skill, the lookback tools).
func offeredRegistry(names ...string) *tools.Offered {
	offered := offeredTools(tools.Registry.All())
	for _, name := range names {
		offered.Offer(discardToolBox{}, setupToolsTestTool{name: name})
	}
	return offered
}

// offeredAfterSetup runs setupTooling over the real built-ins for a
// selection and returns what it offered.
func offeredAfterSetup(t *testing.T, globs ...string) (*tools.Offered, []string) {
	t.Helper()
	confDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(confDir, "mcpServers"), 0o755); err != nil {
		t.Fatalf("mkdir mcpServers: %v", err)
	}
	t.Setenv("CLAI_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	completer := &toolBoxCompleter{}
	conf := &Configurations{UseTools: true, ConfigDir: confDir, RequestedToolGlobs: globs}
	if err := setupTooling(procenv.Context(t.Context()), completer, conf, &recordingSuccessSink{}); err != nil {
		t.Fatalf("setupTooling: %v", err)
	}
	return conf.Offered, completer.registered
}

// A legacy alias reaches its tool in a run that offers the tool, narrowed or
// not, and names nothing in a run that does not.
func TestSetupTooling_aliasReachesOnlyAnOfferedTool(t *testing.T) {
	aliases := map[string]string{"async_cmd_run": "async_cmd", "freetext_command": "cmd"}
	rows := []struct {
		name     string
		globs    []string
		callable bool
	}{
		{name: "every tool", callable: true},
		{name: "the aliased tools selected", globs: []string{"cmd", "async_cmd"}, callable: true},
		{name: "another tool selected", globs: []string{"pwd"}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			offered, registered := offeredAfterSetup(t, row.globs...)
			for alias, canonical := range aliases {
				tool, ok := offered.Get(alias)
				if ok != row.callable {
					t.Fatalf("alias %q callable = %v, want %v (offered %v)", alias, ok, row.callable, offered.Names())
				}
				if ok && tool.Specification().Name != canonical {
					t.Fatalf("alias %q reaches %q, want %q", alias, tool.Specification().Name, canonical)
				}
			}
			// What the model was offered and what its calls can reach are one set.
			if names := offered.Names(); len(names) != len(registered) {
				t.Fatalf("offered %v but registered %v on the model", names, registered)
			}
			for _, name := range registered {
				if _, ok := offered.Get(name); !ok {
					t.Fatalf("%q is registered on the model but not callable", name)
				}
			}
		})
	}
}

// A hidden tool is offered only to a run that selects it by name.
func TestSetupTooling_hiddenToolIsCallableOnlyWhenSelected(t *testing.T) {
	const hidden = "clai_check"
	if all, _ := offeredAfterSetup(t); len(all.Names()) == 0 {
		t.Fatal("an unnarrowed run offered nothing")
	} else if _, ok := all.Get(hidden); ok {
		t.Fatalf("an unnarrowed run offers hidden %q", hidden)
	}
	if selected, _ := offeredAfterSetup(t, hidden); len(selected.Names()) != 1 {
		t.Fatalf("offered = %v, want only %q", selected.Names(), hidden)
	} else if _, ok := selected.Get(hidden); !ok {
		t.Fatalf("a run selecting %q cannot call it", hidden)
	}
}

// The executor refuses a built-in the run did not offer although the process
// registry holds it, tells the model so and runs nothing.
func TestExecute_unofferedBuiltinIsRefused(t *testing.T) {
	tools.Init(context.Background())
	if _, ok := tools.Registry.Get("cmd"); !ok {
		t.Fatal("the registry holds no cmd, so the test proves nothing")
	}
	marker := filepath.Join(t.TempDir(), "ran")
	var out strings.Builder
	q := &Querier[*MockQuerier]{
		Raw: true,
		out: &out,
		tooling: tooling{offered: offeredTools(map[string]pub_models.LLMTool{
			"pwd": setupToolsTestTool{name: "pwd"},
		})},
	}
	session := &QuerySession{}
	inputs := pub_models.Input{"command": "touch " + marker}
	call := pub_models.Call{ID: "call-1", Name: "cmd", Inputs: &inputs}

	if err := (toolExecutor[*MockQuerier]{querier: q}).Execute(context.Background(), session, call); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("the unoffered tool ran: marker stat err = %v, want not exist", err)
	}
	last := session.Chat.Messages[len(session.Chat.Messages)-1]
	if last.Role != "tool" || last.ToolCallID != "call-1" || last.Content != "ERROR: unknown tool call: cmd" {
		t.Fatalf("tool result = %+v, want the unknown-tool refusal for call-1", last)
	}
	if !strings.Contains(out.String(), "ERROR: unknown tool call: cmd") {
		t.Fatalf("output = %q, want the refusal shown", out.String())
	}
}
