package pi

import (
	"bytes"
	"context"
	"encoding/json"
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
// This reader is intentionally conservative: discovery skips rows with
// missing SourceID. Its cost is bounded by the foreign index, not by a line
// cap: a cache hit costs one stat and opens nothing.
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
	return sourceName
}

const sourceName = "pi"

var toolCallKeys = vendors.ToolCallBlockKeys{Type: "toolCall", Args: "arguments"}

// schema is this vendor's whole contribution: one line's meaning, and a root.
type schema struct {
	root string
}

func (r SourceReader) schema() schema {
	return schema{root: r.sessionsRoot()}
}

func (s schema) SourceName() string { return sourceName }

func (s schema) Root() string { return s.root }

func (s schema) SkipDirs() []string { return nil }

// discoverLine is the typed view of one pi line; the nested message stays raw
// until its role is known.
type discoverLine struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Cwd       string          `json:"cwd"`
	Timestamp string          `json:"timestamp"`
	Message   json.RawMessage `json:"message"`
}

type discoverMessage struct {
	Role    string          `json:"role"`
	Model   string          `json:"model"`
	Content json.RawMessage `json:"content"`
}

// contributingTypes are the only two Fields reads: identity on a session line,
// every count on a message line. TestSchemaConformance_pi keeps that honest.
var contributingTypes = [][]byte{
	[]byte(`"session"`),
	[]byte(`"message"`),
}

// MayContribute is sound only for canonically spelled keys and values, which
// no test can enforce because Fields decodes case-insensitively (D26).
func (s schema) MayContribute(line []byte) bool {
	for _, marker := range contributingTypes {
		if bytes.Contains(line, marker) {
			return true
		}
	}
	return false
}

// Fields reports what one pi line contributes. Identity, cwd and timestamp
// live only on the "session" line; a toolResult counts as a message.
func (s schema) Fields(line []byte) vendors.LineFields {
	var env discoverLine
	// A decode error leaves every field zero; a type error still fills the rest.
	_ = json.Unmarshal(line, &env)
	f := vendors.LineFields{}
	switch env.Type {
	case "session":
		f.SessionID, f.Cwd = env.ID, env.Cwd
		if env.Timestamp != "" {
			if t, err := time.Parse(time.RFC3339Nano, env.Timestamp); err == nil {
				f.Timestamp = t
			}
		}
	case "message":
		var msg discoverMessage
		if err := json.Unmarshal(env.Message, &msg); err != nil {
			return f
		}
		switch msg.Role {
		case "user":
			f.Role = vendors.LineRoleUser
			f.UserText = vendors.RawTextBlocksContent(msg.Content)
		case "assistant":
			f.Role = vendors.LineRoleAssistant
			f.Model = msg.Model
		case "toolResult":
			f.Role = vendors.LineRoleTool
		}
	}
	return f
}

func (r SourceReader) Discover(ctx context.Context, cache vendors.SourceCache) ([]vendors.SourceRow, error) {
	return vendors.DiscoverJSONL(ctx, r.schema(), r.FS, cache)
}

func (r SourceReader) Read(ctx context.Context, cache vendors.SourceCache, sourceID string) (pub_models.Chat, error) {
	absPath, err := vendors.FindJSONLSession(ctx, r.schema(), r.FS, cache, sourceID)
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
	err = vendors.ScanJSONLLines(f, vendors.ReadMaxToken, func(env map[string]any) bool {
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

func (r SourceReader) sessionsRoot() string {
	return vendors.HomeRelativeRoot(r.Root, ".pi", "agent", "sessions")
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
