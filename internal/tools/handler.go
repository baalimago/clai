package tools

import (
	"context"
	"fmt"

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
	Registry.initOnce.Do(registerLocalTools)
}

// registerLocalTools adds every locally available LLM tool to the global
// Registry. Only called via Init, which guarantees single execution.
func registerLocalTools() {
	Registry.Set(tools.FileTree.Specification().Name, tools.FileTree)
	Registry.Set(tools.Cat.Specification().Name, tools.Cat)
	Registry.Set(tools.Find.Specification().Name, tools.Find)
	Registry.Set(tools.FileType.Specification().Name, tools.FileType)
	Registry.Set(tools.LS.Specification().Name, tools.LS)
	Registry.Set(tools.Mkdir.Specification().Name, tools.Mkdir)
	Registry.Set(tools.Mktemp.Specification().Name, tools.Mktemp)
	Registry.Set(tools.WebsiteText.Specification().Name, tools.WebsiteText)
	Registry.Set(tools.RipGrep.Specification().Name, tools.RipGrep)
	Registry.Set(tools.Go.Specification().Name, tools.Go)
	Registry.Set(tools.WriteFile.Specification().Name, tools.WriteFile)
	Registry.Set(tools.ApplyPatch.Specification().Name, tools.ApplyPatch)
	Registry.Set(tools.Cmd.Specification().Name, tools.Cmd)
	// Legacy production alias: freetext_command predates the cmd name and is
	// the same freetext shell tool. It stays resolvable and selectable, and
	// the clai tools listing groups it under cmd.
	Registry.SetAlias("freetext_command", tools.Cmd.Specification().Name, tools.Cmd)
	Registry.Set(tools.Sed.Specification().Name, tools.Sed)
	Registry.Set(tools.RowsBetween.Specification().Name, tools.RowsBetween)
	Registry.Set(tools.LineCount.Specification().Name, tools.LineCount)
	Registry.Set(tools.Git.Specification().Name, tools.Git)
	Registry.Set(tools.FFProbe.Specification().Name, tools.FFProbe)
	Registry.Set(tools.AudioTranscribe.Specification().Name, tools.AudioTranscribe)
	Registry.Set(tools.Date.Specification().Name, tools.Date)
	Registry.Set(tools.Pwd.Specification().Name, tools.Pwd)
	Registry.Set(tools.ClaiHelp.Specification().Name, tools.ClaiHelp)
	Registry.Set(tools.ClaiRun.Specification().Name, tools.ClaiRun)
	Registry.Set(tools.ClaiCheck.Specification().Name, tools.ClaiCheck)
	Registry.Set(tools.ClaiResult.Specification().Name, tools.ClaiResult)
	Registry.Set(tools.ClaiWaitForWorkers.Specification().Name, tools.ClaiWaitForWorkers)
	Registry.Set(tools.AsyncCmdRun.Specification().Name, tools.AsyncCmdRun)
	// Legacy production alias: async_cmd_run predates the async_cmd rename.
	// Existing callers, tool-glob selections, and mock-vendor traffic keep
	// working; async_cmd is the canonical name and the clai tools listing
	// groups the alias under it.
	Registry.SetAlias("async_cmd_run", tools.AsyncCmdRun.Specification().Name, tools.AsyncCmdRun)
	Registry.Set(tools.AsyncCmdStatus.Specification().Name, tools.AsyncCmdStatus)
	Registry.Set(tools.AsyncCmdLogs.Specification().Name, tools.AsyncCmdLogs)
	Registry.Set(tools.AsyncCmdAwait.Specification().Name, tools.AsyncCmdAwait)
	Registry.Set(tools.AsyncCmdCancel.Specification().Name, tools.AsyncCmdCancel)
	Registry.Set(tools.LoadSkill.Specification().Name, tools.LoadSkill)
	Registry.Set(tools.Date.Specification().Name, tools.Date)
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
