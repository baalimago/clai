package tools

import "fmt"

// Tools is the global registry of available LLM tools.
var Tools = NewRegistry()

func init() {
	Tools.Set("file_tree", FileTree)
	Tools.Set("cat", Cat)
	Tools.Set("find", Find)
	Tools.Set("file_type", FileType)
	Tools.Set("ls", LS)
	Tools.Set("website_text", WebsiteText)
	Tools.Set("rg", RipGrep)
	Tools.Set("go", Go)
	Tools.Set("write_file", WriteFile)
	Tools.Set("freetext_command", FreetextCmd)
	Tools.Set("sed", Sed)
	Tools.Set("rows_between", RowsBetween)
	Tools.Set("line_count", LineCount)
}

// Invoke the call, and gather both error and output in the same string
func Invoke(call Call) string {
	t, exists := Tools.Get(call.Name)
	if !exists {
		return "ERROR: unknown tool call: " + call.Name
	}
	out, err := t.Call(call.Inputs)
	if err != nil {
		return fmt.Sprintf("ERROR: failed to run tool: %v, error: %v", call.Name, err)
	}
	return out
}

// ToolFromName looks at the static tools.Tools map
func ToolFromName(name string) Specification {
	t, exists := Tools.Get(name)
	if !exists {
		return Specification{}
	}
	return t.Specification()
}
