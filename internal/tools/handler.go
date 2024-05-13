package tools

import "fmt"

// Invoke the call, and gather both error and output in the same string
func Invoke(call Call) string {
	var (
		out string
		err error
	)
	switch call.Name {
	case "test":
		return fmt.Sprintf("%+v", call)
	case "file_tree":
		out, err = FileTree.Call(call.Inputs)
	case "cat":
		// ... an unfortunate combination of packages and functions
		out, err = Cat.Call(call.Inputs)
	case "find":
		out, err = Find.Call(call.Inputs)
	case "file_type":
		out, err = FileType.Call(call.Inputs)
	case "ls":
		out, err = LS.Call(call.Inputs)
	case "website_text":
		out, err = WebsiteText.Call(call.Inputs)
	case "rg":
		out, err = RipGrep.Call(call.Inputs)
	default:
		return "ERROR: unknown tool call: " + call.Name
	}
	if err != nil {
		return fmt.Sprintf("ERROR: failed to run tool: %v, error: %v", call.Name, err)
	}
	return out
}

func UserFunctionFromName(name string) UserFunction {
	switch name {
	case "file_tree":
		return FileTree.UserFunction()
	case "cat":
		return Cat.UserFunction()
	case "find":
		return Find.UserFunction()
	case "file_type":
		return FileType.UserFunction()
	case "ls":
		return LS.UserFunction()
	default:
		return UserFunction{}
	}
}
