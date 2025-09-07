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
	Registry.Set("file_tree", FileTree)
	Registry.Set("cat", Cat)
	Registry.Set("find", Find)
	Registry.Set("file_type", FileType)
	Registry.Set("ls", LS)
	Registry.Set("website_text", WebsiteText)
	Registry.Set("rg", RipGrep)
	Registry.Set("go", Go)
	Registry.Set("write_file", WriteFile)
	Registry.Set("freetext_command", FreetextCmd)
	Registry.Set("sed", Sed)
	Registry.Set("rows_between", RowsBetween)
	Registry.Set("line_count", LineCount)
	Registry.Set("git", Git)
	Registry.Set("recall", Recall)
	Registry.Set("ffprobe", FFProbe)
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
