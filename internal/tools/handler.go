package tools

import (
	"context"
	"maps"
	"os/exec"
	"slices"
	"sync"

	"github.com/baalimago/clai/internal/runenv"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/clai/pkg/tools"
)

// Registry is the global registry of available LLM tools.
var Registry = NewRegistry()

// Init initializes the global Registry with available local LLM tools.
// If the Registry has already been initialized, it simply returns. It is safe
// to call concurrently; the registration happens exactly once and every caller
// blocks until it has completed.
func Init(ctx context.Context) {
	Registry.initOnce.Do(func() {
		registerLocalTools(Registry, exec.LookPath)
		for _, name := range slices.Sorted(maps.Keys(Registry.All())) {
			runenv.Debugf(ctx, runenv.SubsystemToolsRegistrySet, "adding tool to registry, name: %v", name)
		}
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

// ToolFromName returns the specification of the built-in registered as name.
// It describes a tool and never runs one.
func ToolFromName(name string) pub_models.Specification {
	t, exists := Registry.Get(name)
	if !exists {
		return pub_models.Specification{}
	}
	return t.Specification()
}
