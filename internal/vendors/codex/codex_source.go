package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	"github.com/baalimago/clai/internal/vendors"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// SourceReader reads Codex rollout logs from disk.
//
// Storage (best-effort, observed): ~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl
// and ~/.codex/archived_sessions/rollout-*.jsonl. Each line is an envelope
// with a type and a payload. The first line is session_meta; it carries the
// session id, working directory and start time. turn_context carries the
// model. response_item payloads carry the conversation:
//
//   - message role user/assistant holds input_text/output_text parts. Codex
//     injects context as extra user messages (environment, AGENTS.md, plugin
//     list, compaction history); those are not prompts and are dropped.
//   - reasoning holds a summary and encrypted content. The summary is attached
//     to the next assistant message; the encrypted body cannot be replayed.
//   - function_call/custom_tool_call and their _output pairs are mapped to
//     tool calls and tool results. A custom call has free-form input, which is
//     JSON-encoded so the call stays syntactically valid.
//
// Subagent rollouts (thread_source "subagent" or a parent_thread_id) are child
// agents, not user conversations, so discovery skips them.
//
// clai never writes back to these sources.
type SourceReader struct {
	FS fs.FS
	// Root is the absolute Codex directory that contains sessions/ and
	// archived_sessions/. If empty, defaults to $HOME/.codex. It exists
	// primarily for tests; production code should leave it empty.
	Root string
}

func (r SourceReader) Source() string {
	return codexSourceName
}

const codexSourceName = "codex"

type codexSchema struct {
	root string
}

func (r SourceReader) schema() codexSchema {
	return codexSchema{root: r.codexRoot()}
}

func (s codexSchema) SourceName() string { return codexSourceName }

func (s codexSchema) Roots() []string {
	if s.root == "" {
		return nil
	}
	return []string{
		filepath.Join(s.root, "sessions"),
		filepath.Join(s.root, "archived_sessions"),
	}
}

func (s codexSchema) Root() string {
	roots := s.Roots()
	if len(roots) == 0 {
		return ""
	}
	return roots[0]
}

func (s codexSchema) SkipDirs() []string { return nil }

type codexRecord struct {
	Type      string       `json:"type"`
	Timestamp string       `json:"timestamp"`
	Payload   codexPayload `json:"payload"`
}

type codexPayload struct {
	Type           string          `json:"type"`
	SessionID      string          `json:"session_id"`
	ID             string          `json:"id"`
	Cwd            string          `json:"cwd"`
	Timestamp      string          `json:"timestamp"`
	ThreadSource   string          `json:"thread_source"`
	ParentThreadID string          `json:"parent_thread_id"`
	Model          string          `json:"model"`
	Role           string          `json:"role"`
	Content        json.RawMessage `json:"content"`
	Summary        codexTextParts  `json:"summary"`
	Name           string          `json:"name"`
	Arguments      string          `json:"arguments"`
	Input          string          `json:"input"`
	CallID         string          `json:"call_id"`
	Output         json.RawMessage `json:"output"`
}

type codexPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// codexTextParts accepts the two shapes Codex uses for text collections: a
// typed part array (reasoning summaries) and a plain string (turn summaries).
type codexTextParts []codexPart

func (p *codexTextParts) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		*p = codexTextParts{{Type: "text", Text: s}}
		return nil
	}
	var parts []codexPart
	if err := json.Unmarshal(data, &parts); err != nil {
		return err
	}
	*p = parts
	return nil
}

func (p codexPayload) sessionID() string {
	if p.SessionID != "" {
		return p.SessionID
	}
	return p.ID
}

func (p codexPayload) subagent() bool {
	return p.ThreadSource == "subagent" || p.ParentThreadID != ""
}

func (p codexPayload) started() time.Time {
	return codexTime(p.Timestamp)
}

var codexInjectedUserPrefixes = []string{
	"<environment_context>",
	"<recommended_plugins>",
	"<skills_instructions>",
	"<user_instructions>",
	"<turn_aborted>",
	"# AGENTS.md instructions for ",
	"The following is the Codex agent history",
}

func codexIsInjectedUserText(text string) bool {
	for _, prefix := range codexInjectedUserPrefixes {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

func codexUserText(content json.RawMessage) string {
	text := strings.TrimSpace(codexContentText(content))
	if text == "" || codexIsInjectedUserText(text) {
		return ""
	}
	return text
}

func codexPartsText(parts codexTextParts) string {
	texts := make([]string, 0, len(parts))
	for _, p := range parts {
		switch p.Type {
		case "input_text", "output_text", "text", "summary_text":
			if strings.TrimSpace(p.Text) != "" {
				texts = append(texts, p.Text)
			}
		}
	}
	return strings.Join(texts, "\n")
}

// codexContentText flattens a content value that is either a plain string or
// an array of typed text parts. Tool output uses the same shape.
func codexContentText(content json.RawMessage) string {
	content = bytes.TrimSpace(content)
	if len(content) == 0 {
		return ""
	}
	if content[0] == '"' {
		var s string
		if err := json.Unmarshal(content, &s); err != nil {
			return ""
		}
		return s
	}
	var parts codexTextParts
	if err := json.Unmarshal(content, &parts); err != nil {
		return ""
	}
	return codexPartsText(parts)
}

func (s codexSchema) Fields(line []byte) vendors.LineFields {
	var rec codexRecord
	// A decode error leaves every field zero; a type error still fills the rest.
	_ = json.Unmarshal(line, &rec)
	switch rec.Type {
	case "session_meta":
		if rec.Payload.subagent() {
			return vendors.LineFields{Role: vendors.LineRoleSkip}
		}
		f := vendors.LineFields{SessionID: rec.Payload.sessionID(), Cwd: rec.Payload.Cwd}
		f.Timestamp = rec.Payload.started()
		if f.Timestamp.IsZero() {
			f.Timestamp = codexTime(rec.Timestamp)
		}
		return f
	case "turn_context":
		return vendors.LineFields{Model: rec.Payload.Model}
	case "response_item":
		switch rec.Payload.Type {
		case "message":
			switch rec.Payload.Role {
			case "user":
				text := codexUserText(rec.Payload.Content)
				if text == "" {
					return vendors.LineFields{}
				}
				return vendors.LineFields{Role: vendors.LineRoleUser, UserText: text}
			case "assistant":
				if codexContentText(rec.Payload.Content) == "" {
					return vendors.LineFields{}
				}
				return vendors.LineFields{Role: vendors.LineRoleAssistant}
			}
		case "function_call", "custom_tool_call":
			return vendors.LineFields{Role: vendors.LineRoleAssistant}
		case "function_call_output", "custom_tool_call_output":
			return vendors.LineFields{Role: vendors.LineRoleTool}
		}
	}
	return vendors.LineFields{}
}

var codexContributingMarkers = [][]byte{
	[]byte(`"session_meta"`),
	[]byte(`"turn_context"`),
	[]byte(`"response_item"`),
}

// MayContribute is sound only for canonically spelled JSON keys and values,
// which no test can enforce because Fields decodes case-insensitively (D26).
func (s codexSchema) MayContribute(line []byte) bool {
	for _, marker := range codexContributingMarkers {
		if bytes.Contains(line, marker) {
			return true
		}
	}
	return false
}

// AcceptFirstLine drops a whole subagent rollout. A subagent transcript copies
// the parent session_meta into its own body, so a line-level skip alone would
// let that copied identity claim the child file as the parent conversation.
func (s codexSchema) AcceptFirstLine(line []byte) bool {
	var rec codexRecord
	if err := json.Unmarshal(line, &rec); err != nil {
		return true
	}
	if rec.Type != "session_meta" {
		return true
	}
	return !rec.Payload.subagent()
}

func (r SourceReader) Discover(ctx context.Context, cache vendors.SourceCache) ([]vendors.SourceRow, error) {
	return vendors.DiscoverJSONL(ctx, r.schema(), r.FS, cache)
}

func (r SourceReader) Read(ctx context.Context, cache vendors.SourceCache, sourceID string) (pub_models.Chat, error) {
	if sourceID == "" {
		return pub_models.Chat{}, errors.New("read codex session: empty session id")
	}
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
	pendingReasoning := ""
	err = vendors.ScanJSONLRecords(f, vendors.ReadMaxToken, func(rec codexRecord) bool {
		if ctx.Err() != nil {
			return false
		}
		switch rec.Type {
		case "session_meta":
			if rec.Payload.subagent() {
				return false
			}
			sessionFound = true
			if created.IsZero() {
				created = rec.Payload.started()
				if created.IsZero() {
					created = codexTime(rec.Timestamp)
				}
			}
			if cwd == "" {
				cwd = rec.Payload.Cwd
			}
		case "response_item":
			msgs, pendingReasoning = appendCodexResponseItem(msgs, rec.Payload, pendingReasoning)
		}
		return true
	})
	if err != nil {
		return pub_models.Chat{}, fmt.Errorf("scan codex transcript %q: %w", absPath, err)
	}
	if err := ctx.Err(); err != nil {
		return pub_models.Chat{}, err
	}
	if !sessionFound {
		return pub_models.Chat{}, fmt.Errorf("read codex session %q: no session metadata in %q", sourceID, absPath)
	}

	sys := fmt.Sprintf("Continued from Codex session %s", sourceID)
	if cwd != "" {
		sys = fmt.Sprintf("Continued from Codex session %s (originally at %s).", sourceID, cwd)
	}
	chat := pub_models.Chat{
		Created:  created,
		Source:   r.Source(),
		SourceID: sourceID,
		Messages: append([]pub_models.Message{{Role: "system", Content: sys}}, msgs...),
	}
	if chat.Created.IsZero() {
		if info, statErr := vendors.StatAbs(r.FS, absPath); statErr == nil {
			chat.Created = info.ModTime()
		}
	}
	return chat, nil
}

func appendCodexResponseItem(msgs []pub_models.Message, p codexPayload, pendingReasoning string) ([]pub_models.Message, string) {
	switch p.Type {
	case "message":
		switch p.Role {
		case "user":
			if userText := codexUserText(p.Content); userText != "" {
				msgs = append(msgs, pub_models.Message{Role: "user", Content: userText})
			}
			pendingReasoning = ""
		case "assistant":
			if text := codexContentText(p.Content); text != "" {
				msgs = append(msgs, pub_models.Message{
					Role:             "assistant",
					Content:          text,
					ReasoningContent: pendingReasoning,
				})
				pendingReasoning = ""
			}
		}
	case "reasoning":
		if summary := codexPartsText(p.Summary); summary != "" {
			pendingReasoning = vendors.JoinNonEmpty(pendingReasoning, summary)
		}
	case "function_call", "custom_tool_call":
		msgs = append(msgs, codexToolCallMessage(p, pendingReasoning))
		pendingReasoning = ""
	case "function_call_output", "custom_tool_call_output":
		msgs = append(msgs, pub_models.Message{
			Role:       "tool",
			ToolCallID: p.CallID,
			Content:    codexContentText(p.Output),
		})
	}
	return msgs, pendingReasoning
}

func codexToolCallMessage(p codexPayload, reasoning string) pub_models.Message {
	args := p.Arguments
	if p.Type == "custom_tool_call" {
		encoded, err := json.Marshal(p.Input)
		if err != nil {
			encoded = []byte(`""`)
		}
		args = string(encoded)
	}
	if strings.TrimSpace(args) == "" {
		args = "{}"
	}
	call := pub_models.Call{
		ID:   p.CallID,
		Name: p.Name,
		Function: pub_models.Specification{
			Name:      p.Name,
			Arguments: args,
		},
	}
	call.Patch()
	return pub_models.Message{
		Role:             "assistant",
		ToolCalls:        []pub_models.Call{call},
		ReasoningContent: reasoning,
	}
}

func codexTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func (r SourceReader) codexRoot() string {
	return vendors.HomeRelativeRoot(r.Root, ".codex")
}
