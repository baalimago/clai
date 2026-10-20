package anthropic

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/baalimago/clai/internal/vendors"
)

// desktopMetadata is the subset of a Claude Desktop local_*.json session file
// that enriches a transcript discovered from ~/.claude/projects. Desktop chat
// bodies are server-side; only code sessions leave a local transcript.
type desktopMetadata struct {
	SessionID    string      `json:"sessionId"`
	CLISessionID string      `json:"cliSessionId"`
	Cwd          string      `json:"cwd"`
	Title        string      `json:"title"`
	Model        string      `json:"model"`
	CreatedAt    desktopTime `json:"createdAt"`
}

type desktopTime struct{ time.Time }

// UnmarshalJSON accepts the millisecond number the store uses today and the
// RFC 3339 spelling older builds used.
func (t *desktopTime) UnmarshalJSON(b []byte) error {
	var n int64
	if err := json.Unmarshal(b, &n); err == nil {
		switch {
		case n > 1_000_000_000_000:
			t.Time = time.UnixMilli(n)
		case n > 0:
			t.Time = time.Unix(n, 0)
		}
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return nil
	}
	if parsed, err := time.Parse(time.RFC3339Nano, s); err == nil {
		t.Time = parsed
	}
	return nil
}

// enrichDesktopMetadata replaces the JSONL preview with the desktop title and
// fills fields the transcript does not carry. The full first user message
// stays untouched, because grouping hashes it.
func (r SourceReader) enrichDesktopMetadata(rows []vendors.SourceRow) {
	if len(rows) == 0 {
		return
	}
	bySession := desktopSessions(r.desktopRoot())
	if len(bySession) == 0 {
		return
	}
	for i := range rows {
		meta, ok := bySession[rows[i].SourceID]
		if !ok {
			continue
		}
		if title := strings.TrimSpace(meta.Title); title != "" {
			rows[i].FirstUserMessage = vendors.TruncateOneLine(title, 100)
		}
		if rows[i].Model == "" {
			rows[i].Model = meta.Model
		}
		if rows[i].Cwd == "" {
			rows[i].Cwd = meta.Cwd
		}
		if rows[i].Created.IsZero() {
			rows[i].Created = meta.CreatedAt.Time
		}
	}
}

func (r SourceReader) desktopRoot() string {
	if r.DesktopRoot != "" {
		return r.DesktopRoot
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "Claude", "claude-code-sessions")
}

// desktopSessions maps both identifiers a local session carries to its
// metadata, because the transcript is written under cliSessionId while the
// desktop row is keyed by sessionId.
func desktopSessions(root string) map[string]desktopMetadata {
	out := map[string]desktopMetadata{}
	if root == "" {
		return out
	}
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !strings.HasPrefix(d.Name(), "local_") || !strings.HasSuffix(d.Name(), ".json") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		var meta desktopMetadata
		if err := json.Unmarshal(b, &meta); err != nil {
			return nil
		}
		if meta.CLISessionID != "" {
			out[meta.CLISessionID] = meta
		}
		if meta.SessionID != "" {
			if _, exists := out[meta.SessionID]; !exists {
				out[meta.SessionID] = meta
			}
		}
		return nil
	})
	return out
}
