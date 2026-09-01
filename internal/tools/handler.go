package tools

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/baalimago/clai/internal/debugflags"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/clai/pkg/tools"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/debug"
)

type contextualTool interface {
	CallWithContext(context.Context, pub_models.Input) (string, error)
}

// Registry is the global registry of available LLM tools.
var Registry = NewRegistry()

// Init initializes the global Registry with available local LLM tools.
// If the Registry has already been initialized, it simply returns. It is safe
// to call concurrently; the registration happens exactly once and every caller
// blocks until it has completed.
func Init() {
	Registry.initOnce.Do(func() {
		registerLocalTools(Registry, exec.LookPath)
	})
}

type executableLookup func(string) (string, error)

type executableTool struct {
	executable string
	tool       pub_models.LLMTool
}

// registerLocalTools adds native tools and executable-backed tools available
// in PATH. lookup is injected so availability behavior is deterministic in tests.
func registerLocalTools(reg *registry, lookup executableLookup) {
	nativeTools := []pub_models.LLMTool{
		tools.Mkdir,
		tools.Mktemp,
		tools.WebsiteText,
		tools.WriteFile,
		tools.ApplyPatch,
		tools.Sed,
		tools.RowsBetween,
		tools.LineCount,
		tools.AudioTranscribe,
		tools.ClaiCheck,
		tools.ClaiResult,
		tools.ClaiWaitForWorkers,
		tools.AsyncCmdRun,
		tools.AsyncCmdStatus,
		tools.AsyncCmdLogs,
		tools.AsyncCmdAwait,
		tools.AsyncCmdCancel,
		tools.LoadSkill,
	}
	for _, tool := range nativeTools {
		reg.Set(tool.Specification().Name, tool)
	}

	executableTools := []executableTool{
		{"tree", tools.FileTree},
		{"cat", tools.Cat},
		{"find", tools.Find},
		{"file", tools.FileType},
		{"ls", tools.LS},
		{"cp", tools.Cp},
		{"rsync", tools.Rsync},
		{"rg", tools.RipGrep},
		{"go", tools.Go},
		{"sh", tools.Cmd},
		{"git", tools.Git},
		{"ffprobe", tools.FFProbe},
		{"date", tools.Date},
		{"pwd", tools.Pwd},
		{tools.ClaiBinaryPath, tools.ClaiHelp},
		{tools.ClaiBinaryPath, tools.ClaiRun},
	}
	for _, candidate := range executableTools {
		if _, err := lookup(candidate.executable); err != nil {
			continue
		}
		reg.Set(candidate.tool.Specification().Name, candidate.tool)
	}

	// Legacy production alias: freetext_command predates the cmd name.
	if _, ok := reg.Get(tools.Cmd.Specification().Name); ok {
		reg.SetAlias("freetext_command", tools.Cmd.Specification().Name, tools.Cmd)
	}
	// Legacy production alias: async_cmd_run predates the async_cmd rename.
	// Existing callers, tool-glob selections, and mock-vendor traffic keep
	// working; async_cmd is the canonical name and the clai tools listing
	// groups the alias under it.
	reg.SetAlias("async_cmd_run", tools.AsyncCmdRun.Specification().Name, tools.AsyncCmdRun)
}

// Invoke the call, and gather both error and output in the same string.
// It resolves against the process-global Registry and exists for the CLI
// tool listing path and tests; agent runs dispatch through InvokeWith so
// their per-run tool instances are used.
func Invoke(ctx context.Context, call pub_models.Call) string {
	return InvokeWith(ctx, call, nil)
}

// InvokeWith resolves the call against toolset first, falling back to the
// global Registry. toolset is the per-run tool table owned by one agent
// run; resolving it before the process-global registry keeps stateful tools
// (MCP tools, WithTools instances) scoped to the agent that registered them
// instead of whatever instance another concurrent Setup wrote last.
func InvokeWith(ctx context.Context, call pub_models.Call, toolset map[string]pub_models.LLMTool) string {
	var t pub_models.LLMTool
	var exists bool
	if toolset != nil {
		t, exists = toolset[call.Name]
	}
	if !exists {
		t, exists = Registry.Get(call.Name)
	}
	if !exists {
		return "ERROR: unknown tool call: " + call.Name
	}
	if debugflags.Enabled("CALL") {
		ancli.Noticef("Invoke call: %v", debug.IndentedJsonFmt(call))
	}
	inp := pub_models.Input{}
	if call.Inputs != nil {
		inp = *call.Inputs
	}
	if ct, ok := t.(contextualTool); ok {
		out, err := ct.CallWithContext(ctx, inp)
		if err != nil {
			return fmt.Sprintf("ERROR: failed to run tool: %v, error: %v", call.Name, err)
		}
		return out
	}
	out, err := t.Call(inp)
	if err != nil {
		return fmt.Sprintf("ERROR: failed to run tool: %v, error: %v", call.Name, err)
	}
	return out
}

// ToolFromName looks at the static tools.Tools map
func ToolFromName(name string) pub_models.Specification {
	t, exists := Registry.Get(name)
	if !exists {
		return pub_models.Specification{}
	}
	return t.Specification()
}
