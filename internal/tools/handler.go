package tools

import (
	"fmt"
	"os"

	pub_models "github.com/baalimago/clai/pkg/text/models"
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
	Registry.Set(FileTree.Specification().Name, FileTree)
	Registry.Set(Cat.Specification().Name, Cat)
	Registry.Set(Find.Specification().Name, Find)
	Registry.Set(FileType.Specification().Name, FileType)
	Registry.Set(LS.Specification().Name, LS)
	Registry.Set(WebsiteText.Specification().Name, WebsiteText)
	Registry.Set(RipGrep.Specification().Name, RipGrep)
	Registry.Set(Go.Specification().Name, Go)
	Registry.Set(WriteFile.Specification().Name, WriteFile)
	Registry.Set(FreetextCmd.Specification().Name, FreetextCmd)
	Registry.Set(Sed.Specification().Name, Sed)
	Registry.Set(RowsBetween.Specification().Name, RowsBetween)
	Registry.Set(LineCount.Specification().Name, LineCount)
	Registry.Set(Git.Specification().Name, Git)
	Registry.Set(Recall.Specification().Name, Recall)
	Registry.Set(FFProbe.Specification().Name, FFProbe)
	Registry.Set(Date.Specification().Name, Date)
	Registry.Set(Pwd.Specification().Name, Pwd)
	Registry.Set(ClaiHelp.Specification().Name, ClaiHelp)
	Registry.Set(ClaiRun.Specification().Name, ClaiRun)
	Registry.Set(ClaiCheck.Specification().Name, ClaiCheck)
	Registry.Set(ClaiResult.Specification().Name, ClaiResult)
	Registry.Set(ClaiWaitForWorkers.Specification().Name, ClaiWaitForWorkers)
	Registry.Set(Date.Specification().Name, Date)
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
