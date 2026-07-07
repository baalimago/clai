package pi

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/baalimago/clai/internal/vendors"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// SourceReader reads pi agent session logs from disk.
//
// Storage (best-effort, observed): ~/.pi/agent/sessions/<project>/*.jsonl
// Each line is JSON with a "type" such as: session, message, model_change,
// thinking_level_change. The session id lives only on the "session" line;
// messages carry role user/assistant/toolResult.
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
	// Root is the absolute directory that contains the pi sessions.
	// If empty, defaults to $HOME/.pi/agent/sessions.
	//
	// This exists primarily for tests; production code should leave it empty.
	Root string
}

func (r SourceReader) Source() string {
	return "pi"
}

var toolCallKeys = vendors.ToolCallBlockKeys{Type: "toolCall", Args: "arguments"}

func (r SourceReader) Discover(ctx context.Context) ([]vendors.SourceRow, error) {
	rows := []vendors.SourceRow{}
	err := vendors.WalkJSONLFiles(ctx, r.sessionsRoot(), nil, func(p string) bool {
		if row, ok := r.discoverOne(p); ok {
			rows = append(rows, row)
		}
		return false
	})
	if err != nil {
		return nil, fmt.Errorf("discover pi sessions: %w", err)
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
		topType, _ := env["type"].(string)
		switch topType {
		case "session":
			if row.SourceID == "" {
				if sid, _ := env["id"].(string); sid != "" {
					row.SourceID = sid
				}
			}
			if row.Cwd == "" {
				if v, _ := env["cwd"].(string); v != "" {
					row.Cwd = v
				}
			}
			if row.Created.IsZero() {
				if ts, _ := env["timestamp"].(string); ts != "" {
					if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
						row.Created = t
					}
				}
			}
		case "message":
			msg, _ := env["message"].(map[string]any)
			if msg == nil {
				return true
			}
			role, _ := msg["role"].(string)
			switch role {
			case "user":
				row.MessageCount++
				if row.FirstUserMessage == "" {
					full := vendors.TextBlocksContent(msg["content"])
					row.FirstUserMessage = vendors.TruncateOneLine(full, 100)
					row.FullFirstUserMessage = full
				}
			case "assistant":
				row.MessageCount++
				if row.Model == "" {
					if m, _ := msg["model"].(string); m != "" {
						row.Model = m
					}
				}
			case "toolResult":
				row.MessageCount++
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
	sessionFound := false
	err = vendors.ScanJSONLLines(f, vendors.ReadMaxToken, 0, func(env map[string]any) bool {
		topType, _ := env["type"].(string)
		switch topType {
		case "session":
			sessionFound = true
			if created.IsZero() {
				if ts, _ := env["timestamp"].(string); ts != "" {
					if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
						created = t
					}
				}
			}
			if cwd == "" {
				if v, _ := env["cwd"].(string); v != "" {
					cwd = v
				}
			}
		case "message":
			if !sessionFound {
				return true
			}
			msg, _ := env["message"].(map[string]any)
			if msg == nil {
				return true
			}
			role, _ := msg["role"].(string)
			switch role {
			case "user":
				msgs = append(msgs, mapPiUserMessage(msg)...)
			case "assistant":
				msgs = append(msgs, vendors.MapAssistantBlocks(msg["content"], toolCallKeys)...)
			case "toolResult":
				msgs = append(msgs, mapPiToolResultMessage(msg))
			}
		}
		return true
	})
	if err != nil {
		return pub_models.Chat{}, fmt.Errorf("scan jsonl %q: %w", absPath, err)
	}

	// Post-process: normalize pi's parallel/interleaved tool call pattern
	msgs = vendors.NormalizeToolCallSequence(msgs)

	sys := pub_models.Message{Role: "system", Content: fmt.Sprintf("Continued from Pi session %s", sourceID)}
	if cwd != "" {
		sys.Content = fmt.Sprintf("Continued from Pi session %s (originally at %s).", sourceID, cwd)
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
	root := r.sessionsRoot()
	if root == "" {
		return "", fmt.Errorf("pi sessions root not configured")
	}
	var found string
	err := vendors.WalkJSONLFiles(ctx, root, nil, func(p string) bool {
		if r.fileHasSessionID(p, sourceID) {
			found = p
			return true
		}
		return false
	})
	if err != nil {
		return "", fmt.Errorf("find pi session: %w", err)
	}
	if found == "" {
		return "", fmt.Errorf("pi session %q not found", sourceID)
	}
	return found, nil
}

func (r SourceReader) sessionsRoot() string {
	return vendors.HomeRelativeRoot(r.Root, ".pi", "agent", "sessions")
}

func (r SourceReader) fileHasSessionID(absPath, want string) bool {
	f, err := vendors.OpenAbs(r.FS, absPath)
	if err != nil {
		return false
	}
	defer f.Close()

	found := false
	_ = vendors.ScanJSONLLines(f, vendors.ReadMaxToken, vendors.DiscoverMaxLines, func(env map[string]any) bool {
		if typ, _ := env["type"].(string); typ == "session" {
			// The session id lives only on the "session" line: match or bail.
			sid, _ := env["id"].(string)
			found = sid == want
			return false
		}
		return true
	})
	return found
}

func mapPiUserMessage(msg map[string]any) []pub_models.Message {
	content := vendors.TextBlocksContent(msg["content"])
	if content == "" {
		return nil
	}
	return []pub_models.Message{{Role: "user", Content: content}}
}

func mapPiToolResultMessage(msg map[string]any) pub_models.Message {
	toolCallID, _ := msg["toolCallId"].(string)
	return pub_models.Message{
		Role:       "tool",
		ToolCallID: toolCallID,
		Content:    vendors.TextBlocksContent(msg["content"]),
	}
}
