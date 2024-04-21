package tools

import "fmt"

// Invoke the call, and gather both error and output in the same string
func Invoke(call Call) string {
	switch call.Name {
	case "test":
		return fmt.Sprintf("%+v", call)
	case "local_file_tree":
		// This information needs to be passed to the AI, so that it may
		// respond appropriatelly
		out, err := FileTree.Call(call.Inputs)
		if err != nil {
			return fmt.Sprintf("ERROR: failed to run tool: %v, error: %v", call.Name, err)
		} else {
			return out
		}
	default:
		return "ERROR: unknown tool call: " + call.Name
	}
}
