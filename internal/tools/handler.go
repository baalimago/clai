package tools

import (
	"context"
	"fmt"
	"os/exec"
	"slices"
	"sync"

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
	hidden     bool
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
		tools.AsyncCmdRun,
		tools.AsyncCmdStatus,
		tools.AsyncCmdLogs,
		tools.AsyncCmdAwait,
		tools.AsyncCmdCancel,
		tools.LoadSkill,
		tools.ViewImage,
	}
	for _, tool := range nativeTools {
		reg.Set(tool.Specification().Name, tool)
	}

	executableTools := []executableTool{
		{executable: "tree", tool: tools.FileTree},
		{executable: "cat", tool: tools.Cat},
		{executable: "head", tool: tools.Head},
		{executable: "tail", tool: tools.Tail},
		{executable: "jq", tool: tools.JQ},
		{executable: "find", tool: tools.Find},
		{executable: "file", tool: tools.FileType},
		{executable: "ls", tool: tools.LS},
		{executable: "cp", tool: tools.Cp},
		{executable: "rsync", tool: tools.Rsync},
		{executable: "rg", tool: tools.RipGrep},
		{executable: "go", tool: tools.Go},
		{executable: "sh", tool: tools.Cmd},
		{executable: "git", tool: tools.Git},
		{executable: "ffprobe", tool: tools.FFProbe},
		{executable: "ffmpeg", tool: tools.AudioSources},
		{executable: "ffmpeg", tool: tools.AudioRecord},
		{executable: "date", tool: tools.Date},
		{executable: "pwd", tool: tools.Pwd},
		// The clai_* worker tools stay callable by name for profiles that
		// list them, but are hidden: orchestration goes through async_cmd
		// running clai.
		{executable: tools.ClaiBinaryPath, tool: tools.ClaiHelp, hidden: true},
		{executable: tools.ClaiBinaryPath, tool: tools.ClaiRun, hidden: true},
	}
	available := lookupExecutables(executableTools, lookup)
	for _, candidate := range executableTools {
		if !available[candidate.executable] {
			continue
		}
		if candidate.hidden {
			reg.SetHidden(candidate.tool.Specification().Name, candidate.tool)
			continue
		}
		reg.Set(candidate.tool.Specification().Name, candidate.tool)
	}

	for _, tool := range []pub_models.LLMTool{tools.ClaiCheck, tools.ClaiResult, tools.ClaiWaitForWorkers} {
		reg.SetHidden(tool.Specification().Name, tool)
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

// lookupExecutables searches PATH for every distinct executable at once: each
// search is a stat per PATH entry, and run in sequence they dominate setup.
func lookupExecutables(candidates []executableTool, lookup executableLookup) map[string]bool {
	var names []string
	for _, candidate := range candidates {
		if !slices.Contains(names, candidate.executable) {
			names = append(names, candidate.executable)
		}
	}
	found := make([]bool, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Go(func() {
			_, err := lookup(name)
			found[i] = err == nil
		})
	}
	wg.Wait()
	available := make(map[string]bool, len(names))
	for i, name := range names {
		available[name] = found[i]
	}
	return available
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
