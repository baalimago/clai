package tools

import (
	"fmt"
	"sync"
)

type toolRegistry struct {
	mu    sync.RWMutex
	tools map[string]LLMTool
}

func (tr *toolRegistry) Get(name string) (LLMTool, bool) {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	t, ok := tr.tools[name]
	return t, ok
}

func (tr *toolRegistry) Register(tool LLMTool) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.tools[tool.Specification().Name] = tool
}

func (tr *toolRegistry) All() map[string]LLMTool {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	copy := make(map[string]LLMTool, len(tr.tools))
	for k, v := range tr.tools {
		copy[k] = v
	}
	return copy
}

var Tools = toolRegistry{
	tools: map[string]LLMTool{
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
		"sed":              Sed,
		"rows_between":     RowsBetween,
		"line_count":       LineCount,
	},
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
