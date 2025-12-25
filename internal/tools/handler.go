package tools

import (
	"fmt"
	"os"

	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/clai/pkg/tools"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/debug"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

// Registry is the global registry of available LLM tools.
var Registry = NewRegistry()

// Init initializes the global Registry with available local LLM tools.
// If the Registry has already been initialized, it simply returns.
func Init() {
	if Registry.hasBeenInit {
		return
	}
	Registry.hasBeenInit = true
	Registry.Set(tools.FileTree.Specification().Name, tools.FileTree)
	Registry.Set(tools.Cat.Specification().Name, tools.Cat)
	Registry.Set(tools.Find.Specification().Name, tools.Find)
	Registry.Set(tools.FileType.Specification().Name, tools.FileType)
	Registry.Set(tools.LS.Specification().Name, tools.LS)
	Registry.Set(tools.WebsiteText.Specification().Name, tools.WebsiteText)
	Registry.Set(tools.RipGrep.Specification().Name, tools.RipGrep)
	Registry.Set(tools.Go.Specification().Name, tools.Go)
	Registry.Set(tools.WriteFile.Specification().Name, tools.WriteFile)
	Registry.Set(tools.FreetextCmd.Specification().Name, tools.FreetextCmd)
	Registry.Set(tools.Sed.Specification().Name, tools.Sed)
	Registry.Set(tools.RowsBetween.Specification().Name, tools.RowsBetween)
	Registry.Set(tools.LineCount.Specification().Name, tools.LineCount)
	Registry.Set(tools.Git.Specification().Name, tools.Git)
	Registry.Set(tools.FFProbe.Specification().Name, tools.FFProbe)
	Registry.Set(tools.Date.Specification().Name, tools.Date)
	Registry.Set(tools.Pwd.Specification().Name, tools.Pwd)
	Registry.Set(tools.ClaiHelp.Specification().Name, tools.ClaiHelp)
	Registry.Set(tools.ClaiRun.Specification().Name, tools.ClaiRun)
	Registry.Set(tools.ClaiCheck.Specification().Name, tools.ClaiCheck)
	Registry.Set(tools.ClaiResult.Specification().Name, tools.ClaiResult)
	Registry.Set(tools.ClaiWaitForWorkers.Specification().Name, tools.ClaiWaitForWorkers)
	Registry.Set(tools.Date.Specification().Name, tools.Date)
}

// Invoke the call, and gather both error and output in the same string
func Invoke(call pub_models.Call) string {
	t, exists := Registry.Get(call.Name)
	if !exists {
		return "ERROR: unknown tool call: " + call.Name
	}
	if misc.Truthy(os.Getenv("DEBUG_CALL")) {
		ancli.Noticef("Invoke call: %v", debug.IndentedJsonFmt(call))
	}
	inp := pub_models.Input{}
	if call.Inputs != nil {
		inp = *call.Inputs
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
