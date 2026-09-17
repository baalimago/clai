package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
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
	// Root is the absolute directory that contains the Claude projects.
	// If empty, defaults to $HOME/.claude/projects.
	//
	// This exists primarily for tests; production code should leave it empty.
	Root string
}

func (r SourceReader) Source() string {
	return sourceName
}

const sourceName = "claude-code"

var toolCallKeys = vendors.ToolCallBlockKeys{Type: "tool_use", Args: "input"}

// skipDirs holds directory names never containing sessions of their own.
var skipDirs = []string{"subagents"}

// schema is this vendor's whole contribution: one line's meaning, and a root.
type schema struct {
	root string
}

func (r SourceReader) schema() schema {
	return schema{root: r.projectsRoot()}
}

func (s schema) SourceName() string { return sourceName }

func (s schema) Root() string { return s.root }

func (s schema) SkipDirs() []string { return skipDirs }

// discoverLine keeps its nested values raw, so a line whose "message" is not
// an object still contributes its identity, as the decoded form did.
type discoverLine struct {
	Type        string          `json:"type"`
	SessionID   string          `json:"sessionId"`
	Cwd         string          `json:"cwd"`
	Timestamp   string          `json:"timestamp"`
	IsSidechain bool            `json:"isSidechain"`
	Message     json.RawMessage `json:"message"`
}

type discoverMessage struct {
	Model   string          `json:"model"`
	Content json.RawMessage `json:"content"`
}

// contributingMarkers gate the decode. Over-inclusive only costs a decode;
// TestSchemaConformance_anthropic guards against under-inclusive.
var contributingMarkers = [][]byte{
	[]byte(`"sessionId"`),
	[]byte(`"cwd"`),
	[]byte(`"timestamp"`),
	[]byte(`"isSidechain"`),
	[]byte(`"user"`),
	[]byte(`"assistant"`),
}

// MayContribute is sound only for canonically spelled JSON keys, which no test
// can enforce because Fields decodes case-insensitively (D26).
func (s schema) MayContribute(line []byte) bool {
	for _, marker := range contributingMarkers {
		if bytes.Contains(line, marker) {
			return true
		}
	}
	return false
}

// Fields reports what one Claude Code line contributes. Sidechain (subagent)
// lines carry the parent's sessionId but contribute nothing at all.
func (s schema) Fields(line []byte) vendors.LineFields {
	var env discoverLine
	// A decode error leaves every field zero; a type error still fills the rest.
	_ = json.Unmarshal(line, &env)
	if env.IsSidechain {
		return vendors.LineFields{Role: vendors.LineRoleSkip}
	}
	f := vendors.LineFields{SessionID: env.SessionID, Cwd: env.Cwd}
	if env.Timestamp != "" {
		if t, err := time.Parse(time.RFC3339, env.Timestamp); err == nil {
			f.Timestamp = t
		}
	}
	if env.Type != "user" && env.Type != "assistant" {
		return f
	}
	var msg discoverMessage
	_ = json.Unmarshal(env.Message, &msg)
	if env.Type == "assistant" {
		f.Role = vendors.LineRoleAssistant
		f.Model = msg.Model
		return f
	}
	f.Role = vendors.LineRoleUser
	// Block-array user content previews only its text blocks — tool_result
	// blocks are machine output, not the user's words.
	f.UserText = vendors.RawTextBlocksContent(msg.Content)
	return f
}

func (r SourceReader) Discover(ctx context.Context, cache vendors.SourceCache) ([]vendors.SourceRow, error) {
	return vendors.DiscoverJSONL(ctx, r.schema(), r.FS, cache)
}

func isSidechain(env map[string]any) bool {
	sc, _ := env["isSidechain"].(bool)
	return sc
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
	err = vendors.ScanJSONLLines(f, vendors.ReadMaxToken, func(env map[string]any) bool {
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

func (r SourceReader) projectsRoot() string {
	return vendors.HomeRelativeRoot(r.Root, ".claude", "projects")
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
