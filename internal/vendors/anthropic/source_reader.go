package anthropic

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/baalimago/clai/internal/vendors"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// SourceReader reads Claude Code / Claude Desktop conversation logs from disk.
//
// Storage (best-effort, observed): ~/.claude/projects/<project>/*.jsonl
// Each line is JSON with a "type" such as: user, assistant, system, queue-operation.
// Task-subagent transcripts live under <sessionId>/subagents/ and carry the
// parent's sessionId ("isSidechain": true) — they are never sessions themselves,
// so both the directory and sidechain lines are skipped everywhere.
//
// This reader is intentionally conservative: discovery is bounded and skips
// rows with missing SourceID.
//
// FS is injectable for tests; if nil, the host root filesystem is used. The
// host-path logic (HOME expansion, walking) stays outside the FS — it is only
// used for opening files.
//
// clai never writes back to these sources.
type SourceReader struct {
	FS fs.FS
	// Root is the absolute directory that contains the Claude projects.
	// If empty, defaults to $HOME/.claude/projects.
	//
	// This exists primarily for tests; production code should leave it empty.
	Root string
}

func (r SourceReader) Source() string {
	return "claude-code"
}

var toolCallKeys = vendors.ToolCallBlockKeys{Type: "tool_use", Args: "input"}

// skipDirs holds directory names never containing sessions of their own.
var skipDirs = []string{"subagents"}

func (r SourceReader) Discover(ctx context.Context) ([]vendors.SourceRow, error) {
	rows := []vendors.SourceRow{}
	err := vendors.WalkJSONLFiles(ctx, r.projectsRoot(), skipDirs, func(p string) bool {
		if row, ok := r.discoverOne(p); ok {
			rows = append(rows, row)
		}
		return false
	})
	if err != nil {
		return nil, fmt.Errorf("discover claude sessions: %w", err)
	}
	return rows, nil
}

func (r SourceReader) discoverOne(absPath string) (vendors.SourceRow, bool) {
	f, err := vendors.OpenAbs(r.FS, absPath)
	if err != nil {
		return vendors.SourceRow{}, false
	}
	defer f.Close()

	row := vendors.SourceRow{Source: r.Source(), RawPath: absPath}
	// Discovery is best effort: a scan error just yields sparser metadata.
	_ = vendors.ScanJSONLLines(f, vendors.ReadMaxToken, vendors.DiscoverMaxLines, func(env map[string]any) bool {
		if isSidechain(env) {
			return true
		}
		if row.SourceID == "" {
			if sid, _ := env["sessionId"].(string); sid != "" {
				row.SourceID = sid
			}
		}
		if row.Created.IsZero() {
			if ts, _ := env["timestamp"].(string); ts != "" {
				if t, err := time.Parse(time.RFC3339, ts); err == nil {
					row.Created = t
				}
			}
		}
		typ, _ := env["type"].(string)
		switch typ {
		case "user":
			row.MessageCount++
			if row.FirstUserMessage == "" {
				row.FirstUserMessage, row.FullFirstUserMessage = extractUserContentStrings(env)
			}
		case "assistant":
			row.MessageCount++
			if row.Model == "" {
				if msg, _ := env["message"].(map[string]any); msg != nil {
					if m, _ := msg["model"].(string); m != "" {
						row.Model = m
					}
				}
			}
		}
		return true
	})
	if row.SourceID == "" {
		return vendors.SourceRow{}, false
	}
	if row.Created.IsZero() {
		if st, err := os.Stat(absPath); err == nil {
			row.Created = st.ModTime()
		}
	}
	if row.FirstUserMessage == "" {
		row.FirstUserMessage = "(no preview)"
	}
	return row, true
}

func extractUserContentStrings(env map[string]any) (string, string) {
	msg, _ := env["message"].(map[string]any)
	if msg == nil {
		return "", ""
	}
	// Block-array user content previews only its text blocks — tool_result
	// blocks are machine output, not the user's words.
	s := vendors.TextBlocksContent(msg["content"])
	return vendors.TruncateOneLine(s, 100), s
}

func isSidechain(env map[string]any) bool {
	sc, _ := env["isSidechain"].(bool)
	return sc
}

func (r SourceReader) Read(ctx context.Context, sourceID string) (pub_models.Chat, error) {
	absPath, err := r.findSessionFile(ctx, sourceID)
	if err != nil {
		return pub_models.Chat{}, err
	}
	f, err := vendors.OpenAbs(r.FS, absPath)
	if err != nil {
		return pub_models.Chat{}, err
	}
	defer f.Close()

	msgs := make([]pub_models.Message, 0, 128)
	created := time.Time{}
	cwd := ""
	err = vendors.ScanJSONLLines(f, vendors.ReadMaxToken, 0, func(env map[string]any) bool {
		if sid, _ := env["sessionId"].(string); sid != "" && sid != sourceID {
			return true
		}
		// Sidechain lines are subagent-internal conversation, not the session.
		if isSidechain(env) {
			return true
		}
		typ, _ := env["type"].(string)
		switch typ {
		case "user":
			if created.IsZero() {
				if ts, _ := env["timestamp"].(string); ts != "" {
					if t, err := time.Parse(time.RFC3339, ts); err == nil {
						created = t
					}
				}
			}
			if cwd == "" {
				if v, _ := env["cwd"].(string); v != "" {
					cwd = v
				}
			}
			msgs = append(msgs, mapUserMessage(env)...)
		case "assistant":
			if msg, _ := env["message"].(map[string]any); msg != nil {
				msgs = append(msgs, vendors.MapAssistantBlocks(msg["content"], toolCallKeys)...)
			}
		}
		return true
	})
	if err != nil {
		return pub_models.Chat{}, fmt.Errorf("scan jsonl %q: %w", absPath, err)
	}

	// Post-process: normalize Claude Code's parallel/interleaved tool call
	// pattern into the strict sequential format APIs require.
	msgs = vendors.NormalizeToolCallSequence(msgs)

	sys := pub_models.Message{Role: "system", Content: fmt.Sprintf("Continued from Claude Code session %s", sourceID)}
	if cwd != "" {
		sys.Content = fmt.Sprintf("Continued from Claude Code session %s (originally at %s).", sourceID, cwd)
	}

	chat := pub_models.Chat{
		Created:  created,
		ID:       "",
		Source:   r.Source(),
		SourceID: sourceID,
		Messages: append([]pub_models.Message{sys}, msgs...),
	}
	if chat.Created.IsZero() {
		if st, err := os.Stat(absPath); err == nil {
			chat.Created = st.ModTime()
		}
	}
	return chat, nil
}

func (r SourceReader) findSessionFile(ctx context.Context, sourceID string) (string, error) {
	root := r.projectsRoot()
	if root == "" {
		return "", fmt.Errorf("claude projects root not configured")
	}
	var found string
	err := vendors.WalkJSONLFiles(ctx, root, skipDirs, func(p string) bool {
		if r.fileHasSessionID(p, sourceID) {
			found = p
			return true
		}
		return false
	})
	if err != nil {
		return "", fmt.Errorf("find claude session: %w", err)
	}
	if found == "" {
		return "", fmt.Errorf("claude session %q not found", sourceID)
	}
	return found, nil
}

func (r SourceReader) projectsRoot() string {
	return vendors.HomeRelativeRoot(r.Root, ".claude", "projects")
}

func (r SourceReader) fileHasSessionID(absPath, want string) bool {
	f, err := vendors.OpenAbs(r.FS, absPath)
	if err != nil {
		return false
	}
	defer f.Close()

	found := false
	_ = vendors.ScanJSONLLines(f, vendors.ReadMaxToken, vendors.DiscoverMaxLines, func(env map[string]any) bool {
		if isSidechain(env) {
			return true
		}
		if sid, _ := env["sessionId"].(string); sid == want {
			found = true
			return false
		}
		return true
	})
	return found
}

func mapUserMessage(env map[string]any) []pub_models.Message {
	msg, _ := env["message"].(map[string]any)
	if msg == nil {
		return nil
	}
	c := msg["content"]
	// user message can be string OR array of blocks (text and/or tool_result)
	if s, ok := c.(string); ok {
		return []pub_models.Message{{Role: "user", Content: s}}
	}
	arr, ok := c.([]any)
	if !ok {
		return nil
	}
	out := make([]pub_models.Message, 0, len(arr))
	texts := make([]string, 0, 1)
	for _, v := range arr {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		switch typ, _ := m["type"].(string); typ {
		case "tool_result":
			toolID, _ := m["tool_use_id"].(string)
			out = append(out, pub_models.Message{Role: "tool", ToolCallID: toolID, Content: vendors.TextBlocksContent(m["content"])})
		case "text":
			if t, _ := m["text"].(string); t != "" {
				texts = append(texts, t)
			}
		}
	}
	if len(texts) > 0 {
		out = append(out, pub_models.Message{Role: "user", Content: strings.Join(texts, "\n")})
	}
	return out
}
