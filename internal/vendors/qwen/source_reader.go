// Package qwen reads Qwen Code conversations from its append-only record log.
package qwen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/baalimago/clai/internal/vendors"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

const (
	qwenSourceName     = "qwen"
	qwenPreviewRunes   = 100
	qwenMissingPreview = "(no preview)"
)

// SourceReader discovers and reads Qwen Code sessions.
//
// It reads the Qwen Code store that F1 names: one record log per session. The
// active transcript is one parentUuid chain. The reader never writes to the
// store. (F1, F3, F7)
type SourceReader struct {
	// Root overrides the runtime base. It exists for tests and a non-default
	// install; production code leaves it empty.
	Root string
	// FS is the filesystem the reader opens. It exists for tests; production
	// code leaves it nil.
	FS fs.FS
}

// Source returns the stable source name "qwen". (Source.1, F10)
func (r SourceReader) Source() string { return qwenSourceName }

// Discover lists every main Qwen Code conversation.
//
//   - A missing store root yields no rows and no error. (Discover.1, F4, L1)
//   - One row per file with a sessionId and at least one conversation message;
//     an empty file and a file whose chain has no conversation message yield
//     none. (Discover.2, F1, F3, F4, F10, L2, L3, L4, L5)
//   - MessageCount equals len(the messages Read returns) minus the preamble.
//     (Discover.3, F5)
//   - FirstUserMessage is the last custom_title payload when present, else the
//     first prompt, truncated to the preview width F9 defines;
//     FullFirstUserMessage is the full first prompt, else the preview.
//     (Discover.4, F9)
//   - Cwd is the first record's cwd; Created is the first record's timestamp,
//     else the file modification time; Model is the last assistant model.
//     (Discover.5, F10, L1)
//   - A file that cannot be read to its end is skipped and not cached; a cache
//     hit costs one stat and no open. (Discover.6, F6, F7)
func (r SourceReader) Discover(ctx context.Context, cache vendors.SourceCache) ([]vendors.SourceRow, error) {
	return vendors.WalkSessionRows(ctx, "qwen", cache, r.walkSessions, r.discoverFile)
}

// Read returns one Qwen Code conversation as a clai Chat.
//
//   - Messages start with one system preamble; Source is "qwen" and SourceID is
//     the requested id. (Read.1, F5, F6, F7, F10)
//   - The conversation is the parentUuid chain from the last conversation record
//     to the root, with records that share a uuid aggregated in file order.
//     Artifact records, non-conversation system subtypes and sidechain records
//     never join the chain; other system records join it and map to no message.
//     (Read.2, F3, L2, L3, L5)
//   - A call with a tool_result record becomes a structured Call plus one
//     {role: "tool"} message; any other call becomes one
//     [tool call: name(args)] text line. (Read.3, F8, L4)
//   - A user record's text is systemPayload.displayText when the payload pairs
//     it with hook context, else its text parts without the prompt-submit
//     context part; agent_mention and agent_message records are not prompts.
//     (Read.4, F9, L4)
//   - An empty id, an unknown id, a file with mixed session ids, and a chain with
//     no conversation message are errors. (Read.5, F1, F3, F4, L2)
func (r SourceReader) Read(ctx context.Context, cache vendors.SourceCache, sourceID string) (pub_models.Chat, error) {
	if sourceID == "" {
		return pub_models.Chat{}, errors.New("read qwen session: empty session id")
	}
	absPath, err := r.findSession(ctx, cache, sourceID)
	if err != nil {
		return pub_models.Chat{}, err
	}
	conv, ok, err := r.readConversation(absPath)
	if err != nil {
		return pub_models.Chat{}, fmt.Errorf("read qwen session %q: %w", sourceID, err)
	}
	if !ok || conv.sessionID != sourceID {
		return pub_models.Chat{}, fmt.Errorf("qwen session %q not found", sourceID)
	}
	msgs := conv.mapped()
	if len(msgs) == 0 {
		return pub_models.Chat{}, fmt.Errorf("qwen session %q has no messages", sourceID)
	}
	created := conv.start
	if created.IsZero() {
		if info, statErr := vendors.StatAbs(r.FS, absPath); statErr == nil {
			created = info.ModTime()
		}
	}
	sys := fmt.Sprintf("Continued from Qwen Code session %s", sourceID)
	if conv.cwd != "" {
		sys = fmt.Sprintf("Continued from Qwen Code session %s (originally at %s).", sourceID, conv.cwd)
	}
	return pub_models.Chat{
		Created:  created,
		Source:   qwenSourceName,
		SourceID: sourceID,
		Messages: append([]pub_models.Message{{Role: "system", Content: sys}}, msgs...),
	}, nil
}

func (r SourceReader) root() string {
	if r.Root != "" {
		return r.Root
	}
	if dir := os.Getenv("QWEN_RUNTIME_DIR"); dir != "" {
		return dir
	}
	return vendors.HomeRelativeRoot(os.Getenv("QWEN_HOME"), ".qwen")
}

func (r SourceReader) walkSessions(ctx context.Context, visit func(absPath string) bool) error {
	root := r.root()
	if root == "" {
		return nil
	}
	// Subagent transcripts live under <project>/subagents and never hold a user
	// conversation, so the walk skips them. (F3, Read.2)
	return vendors.WalkJSONLFiles(ctx, filepath.Join(root, "projects"), []string{"subagents"}, func(absPath string) bool {
		return visit(absPath)
	})
}

func (r SourceReader) discoverFile(cache vendors.SourceCache, absPath string) (vendors.SourceRow, bool) {
	info, statErr := vendors.StatAbs(r.FS, absPath)
	cacheable := cache != nil && statErr == nil
	if cacheable {
		if row, ok := cache.Lookup(absPath, info); ok {
			return row, row.SourceID != ""
		}
	}
	conv, ok, err := r.readConversation(absPath)
	if err != nil {
		return vendors.SourceRow{}, false
	}
	row := vendors.SourceRow{}
	if ok {
		row = conv.sourceRow(absPath, info)
	}
	if cacheable {
		cache.Store(absPath, info, row)
	}
	return row, row.SourceID != ""
}

func (r SourceReader) findSession(ctx context.Context, cache vendors.SourceCache, sourceID string) (string, error) {
	return vendors.FindSessionFile(ctx, qwenSourceName, r.FS, cache, sourceID, r.walkSessions, r.fileSessionID)
}

func (r SourceReader) fileSessionID(absPath string) (string, error) {
	f, err := vendors.OpenAbs(r.FS, absPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	id := ""
	err = vendors.ScanJSONLRecords(f, vendors.ReadMaxToken, func(rec qwenRecord) bool {
		if rec.SessionID != "" {
			id = rec.SessionID
			return false
		}
		return true
	})
	return id, err
}

func (r SourceReader) readConversation(absPath string) (qwenConversation, bool, error) {
	f, err := vendors.OpenAbs(r.FS, absPath)
	if err != nil {
		return qwenConversation{}, false, err
	}
	defer f.Close()
	records := []qwenRecord{}
	err = vendors.ScanJSONLRecords(f, vendors.ReadMaxToken, func(rec qwenRecord) bool {
		records = append(records, rec)
		return true
	})
	if err != nil {
		return qwenConversation{}, false, err
	}
	return prepareQwen(records)
}

type qwenRecord struct {
	UUID             string                `json:"uuid"`
	ParentUUID       *string               `json:"parentUuid"`
	SessionID        string                `json:"sessionId"`
	Timestamp        string                `json:"timestamp"`
	Type             string                `json:"type"`
	Subtype          string                `json:"subtype"`
	Message          *qwenContent          `json:"message"`
	Model            string                `json:"model"`
	Cwd              string                `json:"cwd"`
	IsSidechain      bool                  `json:"isSidechain"`
	SystemPayload    json.RawMessage       `json:"systemPayload"`
	ToolCallResult   *qwenToolCallResult   `json:"toolCallResult"`
	ExecutionContext *qwenExecutionContext `json:"executionContext"`
}

type qwenContent struct {
	Role  string     `json:"role"`
	Parts []qwenPart `json:"parts"`
}

type qwenPart struct {
	Text             string                `json:"text"`
	Thought          bool                  `json:"thought"`
	FunctionCall     *qwenFunctionCall     `json:"functionCall"`
	FunctionResponse *qwenFunctionResponse `json:"functionResponse"`
}

type qwenFunctionCall struct {
	ID   string          `json:"id"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

type qwenFunctionResponse struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Response json.RawMessage `json:"response"`
}

type qwenToolCallResult struct {
	CallID        string          `json:"callId"`
	Status        string          `json:"status"`
	ResultDisplay json.RawMessage `json:"resultDisplay"`
}

type qwenExecutionContext struct {
	ModelID string `json:"modelId"`
}

type qwenConversation struct {
	sessionID string
	start     time.Time
	cwd       string
	model     string
	title     string
	records   []qwenRecord
}

type qwenGroup struct {
	first  qwenRecord
	parts  []qwenPart
	model  string
	result *qwenToolCallResult
	stamp  string
}

func (g *qwenGroup) add(rec qwenRecord) {
	if rec.Message != nil {
		g.parts = append(g.parts, rec.Message.Parts...)
	}
	if g.model == "" && rec.Model != "" {
		g.model = rec.Model
	}
	if g.result == nil && rec.ToolCallResult != nil {
		g.result = rec.ToolCallResult
	}
	if rec.Timestamp > g.stamp {
		g.stamp = rec.Timestamp
	}
}

func (g *qwenGroup) record() qwenRecord {
	rec := g.first
	if rec.Message != nil || len(g.parts) > 0 {
		role := ""
		if rec.Message != nil {
			role = rec.Message.Role
		}
		rec.Message = &qwenContent{Role: role, Parts: g.parts}
	}
	if g.model != "" {
		rec.Model = g.model
	}
	if rec.ToolCallResult == nil && g.result != nil {
		rec.ToolCallResult = g.result
	}
	if g.stamp != "" {
		rec.Timestamp = g.stamp
	}
	return rec
}

func prepareQwen(records []qwenRecord) (qwenConversation, bool, error) {
	sessionID := ""
	sessionIDs := map[string]struct{}{}
	for _, rec := range records {
		if rec.SessionID == "" {
			continue
		}
		sessionIDs[rec.SessionID] = struct{}{}
		if sessionID == "" {
			sessionID = rec.SessionID
		}
	}
	if len(sessionIDs) > 1 {
		return qwenConversation{}, false, fmt.Errorf("mixed session ids in one transcript")
	}
	if sessionID == "" {
		return qwenConversation{}, false, nil
	}
	conv := qwenConversation{sessionID: sessionID}
	for _, rec := range records {
		if conv.start.IsZero() {
			conv.start = qwenTime(rec.Timestamp)
		}
		if conv.cwd == "" {
			conv.cwd = rec.Cwd
		}
		if rec.Type == "system" && rec.Subtype == "custom_title" {
			if title := qwenTitle(rec.SystemPayload); title != "" {
				conv.title = title
			}
		}
	}

	groups := map[string]*qwenGroup{}
	order := []string{}
	for _, rec := range records {
		if rec.SessionID != sessionID || !isQwenConversationRecord(rec) || rec.UUID == "" {
			continue
		}
		group, ok := groups[rec.UUID]
		if !ok {
			group = &qwenGroup{first: rec}
			groups[rec.UUID] = group
			order = append(order, rec.UUID)
		}
		group.add(rec)
	}
	if len(order) == 0 {
		return conv, true, nil
	}
	conv.records = qwenActiveChain(order, groups)

	for _, rec := range conv.records {
		if rec.Type != "assistant" {
			continue
		}
		if rec.Model != "" {
			conv.model = rec.Model
		} else if rec.ExecutionContext != nil && rec.ExecutionContext.ModelID != "" {
			conv.model = rec.ExecutionContext.ModelID
		}
	}
	return conv, true, nil
}

// qwenActiveChain picks the last chain tip whose chain holds a conversation
// message. A trailing system record with no parent is a tip of its own; taking
// it blindly would hide the conversation behind it. (L3)
func qwenActiveChain(order []string, groups map[string]*qwenGroup) []qwenRecord {
	referenced := map[string]struct{}{}
	for _, uuid := range order {
		parent := groups[uuid].first.ParentUUID
		if parent != nil && *parent != "" {
			referenced[*parent] = struct{}{}
		}
	}
	for i := len(order) - 1; i >= 0; i-- {
		if _, isParent := referenced[order[i]]; isParent {
			continue
		}
		chain := qwenChain(order[i], groups)
		if !qwenChainHasMessage(chain) {
			continue
		}
		return chain
	}
	return qwenChain(order[len(order)-1], groups)
}

func qwenChainHasMessage(chain []qwenRecord) bool {
	for _, rec := range chain {
		switch rec.Type {
		case "user", "assistant", "tool_result":
			return true
		}
	}
	return false
}

func qwenChain(leaf string, groups map[string]*qwenGroup) []qwenRecord {
	chain := []qwenRecord{}
	visited := map[string]struct{}{}
	for current := leaf; current != ""; {
		if _, seen := visited[current]; seen {
			break
		}
		visited[current] = struct{}{}
		group, ok := groups[current]
		if !ok {
			break
		}
		chain = append(chain, group.record())
		if group.first.ParentUUID == nil || *group.first.ParentUUID == "" {
			break
		}
		current = *group.first.ParentUUID
	}
	slices.Reverse(chain)
	return chain
}

func isQwenConversationRecord(rec qwenRecord) bool {
	if rec.IsSidechain {
		return false
	}
	switch rec.Type {
	case "user", "assistant", "tool_result":
		return true
	case "system":
		switch rec.Subtype {
		case "session_artifact_event", "session_artifact_snapshot", "session_sources_snapshot",
			"managed_session_header_v1", "managed_session_event_v1", "managed_session_commit_v1":
			return false
		}
		return true
	}
	return false
}

func qwenTitle(raw json.RawMessage) string {
	var payload struct {
		CustomTitle string `json:"customTitle"`
		Title       string `json:"custom_title"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return ""
	}
	if payload.CustomTitle != "" {
		return payload.CustomTitle
	}
	return payload.Title
}

func qwenTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func (c qwenConversation) mapped() []pub_models.Message {
	resultIDs := map[string]struct{}{}
	for _, rec := range c.records {
		if rec.Type != "tool_result" {
			continue
		}
		if rec.Message != nil {
			for _, part := range rec.Message.Parts {
				if part.FunctionResponse != nil && part.FunctionResponse.ID != "" {
					resultIDs[part.FunctionResponse.ID] = struct{}{}
				}
			}
		}
		if rec.ToolCallResult != nil && rec.ToolCallResult.CallID != "" {
			resultIDs[rec.ToolCallResult.CallID] = struct{}{}
		}
	}
	out := []pub_models.Message{}
	for _, rec := range c.records {
		switch rec.Type {
		case "user":
			if text := qwenUserText(rec); strings.TrimSpace(text) != "" {
				out = append(out, pub_models.Message{Role: "user", Content: text})
			}
		case "assistant":
			out = append(out, qwenAssistantMessages(rec, resultIDs)...)
		case "tool_result":
			out = append(out, qwenToolMessages(rec)...)
		}
	}
	return out
}

func qwenAssistantMessages(rec qwenRecord, resultIDs map[string]struct{}) []pub_models.Message {
	if rec.Message == nil {
		return nil
	}
	texts := []string{}
	reasoning := []string{}
	msg := pub_models.Message{Role: "assistant"}
	for _, part := range rec.Message.Parts {
		switch {
		case part.FunctionCall != nil:
			call := part.FunctionCall
			args := qwenArgs(call.Args)
			if call.ID != "" && call.Name != "" {
				if _, ok := resultIDs[call.ID]; ok {
					c := pub_models.Call{
						ID:   call.ID,
						Name: call.Name,
						Function: pub_models.Specification{
							Name:      call.Name,
							Arguments: args,
						},
					}
					c.Patch()
					msg.ToolCalls = append(msg.ToolCalls, c)
					continue
				}
			}
			texts = append(texts, fmt.Sprintf("[tool call: %s(%s)]", call.Name, args))
		case part.Thought:
			if part.Text != "" {
				reasoning = append(reasoning, part.Text)
			}
		default:
			if part.Text != "" {
				texts = append(texts, part.Text)
			}
		}
	}
	msg.Content = strings.Join(texts, "\n")
	msg.ReasoningContent = strings.Join(reasoning, "\n")
	if msg.Content == "" && len(msg.ToolCalls) == 0 {
		if msg.ReasoningContent == "" {
			return nil
		}
		msg.Content = "[thinking] " + msg.ReasoningContent
	}
	return []pub_models.Message{msg}
}

func qwenToolMessages(rec qwenRecord) []pub_models.Message {
	out := []pub_models.Message{}
	if rec.Message != nil {
		for _, part := range rec.Message.Parts {
			if part.FunctionResponse == nil {
				continue
			}
			id := part.FunctionResponse.ID
			if id == "" && rec.ToolCallResult != nil {
				id = rec.ToolCallResult.CallID
			}
			if id == "" {
				continue
			}
			content := qwenResponseText(part.FunctionResponse.Response)
			if content == "" {
				content = qwenResultDisplay(rec.ToolCallResult)
			}
			out = append(out, pub_models.Message{Role: "tool", ToolCallID: id, Content: content})
		}
	}
	if len(out) == 0 && rec.ToolCallResult != nil && rec.ToolCallResult.CallID != "" {
		out = append(out, pub_models.Message{
			Role:       "tool",
			ToolCallID: rec.ToolCallResult.CallID,
			Content:    qwenResultDisplay(rec.ToolCallResult),
		})
	}
	return out
}

func qwenUserText(rec qwenRecord) string {
	switch rec.Subtype {
	case "agent_mention", "agent_message":
		return ""
	}
	if rec.Message == nil {
		return ""
	}
	payload := qwenUserPayloadOf(rec.SystemPayload)
	if payload.hasDisplayText && (payload.hookContext != "" || qwenHasFinalPromptContext(rec.Message.Parts)) {
		return payload.displayText
	}
	texts := []string{}
	for _, part := range rec.Message.Parts {
		if part.Text == "" || part.Thought || qwenIsPromptSubmitContext(part.Text) {
			continue
		}
		texts = append(texts, part.Text)
	}
	return strings.Join(texts, "\n")
}

type qwenUserPayload struct {
	displayText    string
	hasDisplayText bool
	hookContext    string
}

func qwenUserPayloadOf(raw json.RawMessage) qwenUserPayload {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return qwenUserPayload{}
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return qwenUserPayload{}
	}
	payload := qwenUserPayload{}
	if display, ok := fields["displayText"]; ok {
		payload.hasDisplayText = true
		_ = json.Unmarshal(display, &payload.displayText)
	}
	if hook, ok := fields["hookContext"]; ok {
		_ = json.Unmarshal(hook, &payload.hookContext)
	}
	return payload
}

func qwenHasFinalPromptContext(parts []qwenPart) bool {
	if len(parts) < 2 {
		return false
	}
	return qwenIsPromptSubmitContext(parts[len(parts)-1].Text)
}

func qwenIsPromptSubmitContext(text string) bool {
	trimmed := strings.TrimSpace(text)
	return strings.HasPrefix(trimmed, "<qwen:user-prompt-submit-context>") &&
		strings.HasSuffix(trimmed, "</qwen:user-prompt-submit-context>")
}

func qwenArgs(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "{}"
	}
	return string(raw)
}

func qwenResponseText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
	}
	var response struct {
		Output json.RawMessage `json:"output"`
	}
	if json.Unmarshal(raw, &response) == nil && len(response.Output) > 0 {
		output := bytes.TrimSpace(response.Output)
		if len(output) > 0 && output[0] == '"' {
			var s string
			if json.Unmarshal(output, &s) == nil {
				return s
			}
		}
		return string(output)
	}
	return string(raw)
}

func qwenResultDisplay(result *qwenToolCallResult) string {
	if result == nil {
		return ""
	}
	raw := bytes.TrimSpace(result.ResultDisplay)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
	}
	var display struct {
		Text   string `json:"text"`
		Output string `json:"output"`
	}
	if json.Unmarshal(raw, &display) == nil {
		if display.Text != "" {
			return display.Text
		}
		if display.Output != "" {
			return display.Output
		}
	}
	return string(raw)
}

func (c qwenConversation) sourceRow(absPath string, info fs.FileInfo) vendors.SourceRow {
	if c.sessionID == "" {
		return vendors.SourceRow{}
	}
	msgs := c.mapped()
	if len(msgs) == 0 {
		return vendors.SourceRow{}
	}
	first := firstQwenUserMessage(msgs)
	preview := c.title
	if strings.TrimSpace(preview) == "" {
		preview = first
	}
	full := first
	if full == "" {
		full = preview
	}
	preview = vendors.TruncateOneLine(preview, qwenPreviewRunes)
	if preview == "" {
		preview = qwenMissingPreview
	}
	created := c.start
	if created.IsZero() && info != nil {
		created = info.ModTime()
	}
	return vendors.SourceRow{
		Source:               qwenSourceName,
		SourceID:             c.sessionID,
		Created:              created,
		FirstUserMessage:     preview,
		FullFirstUserMessage: full,
		MessageCount:         len(msgs),
		Model:                c.model,
		RawPath:              absPath,
		Cwd:                  c.cwd,
	}
}

func firstQwenUserMessage(msgs []pub_models.Message) string {
	for _, msg := range msgs {
		if msg.Role == "user" && strings.TrimSpace(msg.Content) != "" {
			return msg.Content
		}
	}
	return ""
}
