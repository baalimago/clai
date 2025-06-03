package tools

import "fmt"

var Tools = map[string]AiTool{
	"file_tree":        FileTree,
	"cat":              Cat,
	"find":             Find,
	"file_type":        FileType,
	"ls":               LS,
	"website_text":     WebsiteText,
	"rg":               RipGrep,
	"go":               Go,
	"write_file":       WriteFile,
	"freetext_command": FreetextCmd,
}

// Invoke the call, and gather both error and output in the same string
func Invoke(call Call) string {
	t, exists := Tools[call.Name]
	if !exists {
		return "ERROR: unknown tool call: " + call.Name
	}
	out, err := t.Call(call.Inputs)
	if err != nil {
		return fmt.Sprintf("ERROR: failed to run tool: %v, error: %v", call.Name, err)
	}
	return out
}

func UserFunctionFromName(name string) UserFunction {
	t, exists := Tools[name]
	if !exists {
		return UserFunction{}
	}
	return t.UserFunction()
}
