package tools

import pub_models "github.com/baalimago/clai/pkg/text/models"

// builtinShadowMap declares, for an MCP tool's remote name (without its
// server prefix), the built-in tool that already covers the same
// capability in-process. It is data, not a heuristic: a guess that marks
// an unrelated tool as redundant is worse than no marker, so every entry
// here names a real tool of the filesystem MCP server this worklog
// measured (worklog 2026-10-02-mcp-connection-cost, phase 7, D13 — shadowing
// is reported, never auto-resolved). A remote name absent from this map is
// simply unmarked.
// get_file_info is deliberately absent (sign-off review, 2026-10-03): the
// MCP filesystem server's get_file_info returns size, modification time and
// permissions — a stat(1)-shaped result — while FileTypeTool wraps file(1),
// which reports a file's content type. The two answer different questions,
// so the entry was wrong on its merits, not merely a style choice; removed
// rather than repointed, since no built-in here answers get_file_info's
// actual question.
var builtinShadowMap = map[string]pub_models.ToolName{
	"read_file":                 pub_models.CatTool,
	"read_text_file":            pub_models.CatTool,
	"read_multiple_files":       pub_models.CatTool,
	"write_file":                pub_models.WriteFileTool,
	"edit_file":                 pub_models.ApplyPatchTool,
	"list_directory":            pub_models.LSTool,
	"list_directory_with_sizes": pub_models.LSTool,
	"directory_tree":            pub_models.FileTreeTool,
	"search_files":              pub_models.FindTool,
	"create_directory":          pub_models.MkdirTool,
}

// shadowingBuiltin reports the built-in that covers remoteName, and whether
// one is declared at all.
func shadowingBuiltin(remoteName string) (pub_models.ToolName, bool) {
	builtin, ok := builtinShadowMap[remoteName]
	return builtin, ok
}

// builtinRegistered reports whether name is available on this host: the
// process-global Registry holds it, which is how registerLocalTools already
// gates a built-in whose executable is absent. The marker asks the registry
// rather than probing the filesystem itself.
func builtinRegistered(name pub_models.ToolName) bool {
	_, ok := Registry.Get(string(name))
	return ok
}
