package antigravity

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/baalimago/clai/internal/sqlitepage"
)

// summariesFile is the app's own conversation index. It lives beside the
// conversations directory, one per store root.
const summariesFile = "conversation_summaries.db"

// Summary columns, addressed by name because the app adds columns over time.
const (
	colSummaryID        = "conversation_id"
	colSummaryTitle     = "title"
	colSummaryWorkspace = "workspace_uris"
	colSummaryParent    = "parent_conversation_id"
	colSummaryDepth     = "nesting_depth"
)

// summary is one row of the app's conversation index: the fields a trajectory
// database cannot answer, or answers more expensively.
type summary struct {
	title  string
	cwd    string
	parent string
	depth  int64
}

// child reports whether the row describes an agent run the app spawned for a
// conversation, not a conversation a user started.
func (s summary) child() bool { return s.parent != "" || s.depth > 0 }

// readSummaries returns the index of one store root, keyed by conversation id.
// The index only enriches discovery, so every failure yields no entries: an
// old, partial or corrupt index must never hide a conversation. A caller then
// falls back to the trajectory alone.
func readSummaries(root string) map[string]summary {
	out := map[string]summary{}
	if root == "" {
		return out
	}
	db, err := sqlitepage.Open(filepath.Join(root, summariesFile))
	if err != nil {
		return out
	}
	defer db.Close()
	tbl, err := db.Table("conversation_summaries")
	if err != nil {
		return out
	}
	cols, err := tbl.Columns()
	if err != nil {
		return out
	}
	pos := columnPositions(cols)
	id := positionOf(pos, colSummaryID)
	if id < 0 {
		return out
	}
	title := positionOf(pos, colSummaryTitle)
	workspace := positionOf(pos, colSummaryWorkspace)
	parent := positionOf(pos, colSummaryParent)
	depth := positionOf(pos, colSummaryDepth)
	err = tbl.Rows(func(_ int64, vals []sqlitepage.Value) error {
		key := textAt(vals, id)
		if key == "" {
			return nil
		}
		out[key] = summary{
			title:  strings.TrimSpace(textAt(vals, title)),
			cwd:    workspacePath(textAt(vals, workspace)),
			parent: textAt(vals, parent),
			depth:  intAt(vals, depth),
		}
		return nil
	})
	if err != nil {
		return map[string]summary{}
	}
	return out
}

// columnPositions maps each declared column to its position in a row.
func columnPositions(cols []string) map[string]int {
	pos := make(map[string]int, len(cols))
	for i, name := range cols {
		pos[name] = i
	}
	return pos
}

// positionOf answers -1 for a column this build does not find, so every field
// the app adds or removes degrades to absence instead of misreading a value.
func positionOf(pos map[string]int, name string) int {
	if i, ok := pos[name]; ok {
		return i
	}
	return -1
}

// workspacePath returns the first workspace URI of a summary as a filesystem
// path. The column holds a JSON array of URIs, best-effort.
func workspacePath(raw string) string {
	if raw == "" {
		return ""
	}
	var uris []string
	if err := json.Unmarshal([]byte(raw), &uris); err != nil {
		return ""
	}
	for _, uri := range uris {
		if after, ok := strings.CutPrefix(uri, "file://"); ok {
			return after
		}
	}
	return ""
}

func textAt(vals []sqlitepage.Value, i int) string {
	if i < 0 || i >= len(vals) || vals[i].Kind != sqlitepage.Text {
		return ""
	}
	return vals[i].Text
}
