package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	geminiSourceName     = "gemini"
	geminiPreviewRunes   = 100
	geminiMissingPreview = "(no preview)"
)

var errStopGeminiWalk = errors.New("stop gemini walk")

// SourceReader discovers and reads Gemini CLI sessions.
//
// It reads the Gemini CLI store that F1 names. A session file is either an
// append-only JSONL log or a legacy JSON ConversationRecord. The reader never
// writes to the store. (F1, F2, F7)
type SourceReader struct {
	// Root overrides the Gemini home. It exists for tests and a non-default
	// install; production code leaves it empty.
	Root string
	// FS is the filesystem the reader opens. It exists for tests; production
	// code leaves it nil.
	FS fs.FS
}

// Source returns the stable source name "gemini". (Source.1, F10)
func (r SourceReader) Source() string { return geminiSourceName }

// Discover lists every main Gemini CLI conversation.
//
//   - A missing store root yields no rows and no error. (Discover.1, F4, L1)
//   - One row per main conversation file; a subagent file, a file without a
//     sessionId, and a file whose replay yields no conversation message yield
//     none. (Discover.2, F1, F2, F4, F10, L2, L3, L4, L5)
//   - MessageCount equals len(the messages Read returns) minus the preamble.
//     (Discover.3, F5)
//   - FirstUserMessage is the metadata summary when present, else the first
//     prompt, truncated to the preview width F9 defines; FullFirstUserMessage is
//     the full first prompt, else the preview. (Discover.4, F9)
//   - Cwd is the trimmed content of <slug>/.project_root, else empty; Created is
//     startTime; Model is the last model recorded on a gemini message.
//     (Discover.5, F10, L1)
//   - A file that cannot be replayed to its end is skipped and not cached; a
//     cache hit costs one stat and no open. (Discover.6, F6, F7)
func (r SourceReader) Discover(ctx context.Context, cache vendors.SourceCache) ([]vendors.SourceRow, error) {
	return vendors.WalkSessionRows(ctx, "gemini", cache, r.walkSessions, r.discoverFile)
}

// Read returns one Gemini CLI conversation as a clai Chat.
//
//   - Messages start with one system preamble; Source is "gemini" and SourceID
//     is the requested id. (Read.1, F5, F6, F7, F10)
//   - Replay applies metadata records, message records keyed by id, $set
//     (including its messages replacement), $patch (content, tool results,
//     updates, removeIds, orderIds) in file order, and $rewindTo, which drops
//     the rewound record and every message after it. (Read.2, F2, L3)
//   - A settled tool call with a result becomes a structured Call plus one
//     {role: "tool"} message; any other call becomes one
//     [tool call: name(args)] text line. (Read.3, F8, L5)
//   - A user message whose trimmed text is empty or starts with /, ?,
//     <session_context> or <hook_context> is not a message. (Read.4, F9)
//   - A legacy JSON document and a JSONL log use the same message mapping. A
//     file with no sessionId, a subagent session, an empty id, an unknown id and
//     a session with no conversation messages are errors. (Read.5, F1, F2, F4, L4)
func (r SourceReader) Read(ctx context.Context, cache vendors.SourceCache, sourceID string) (pub_models.Chat, error) {
	if sourceID == "" {
		return pub_models.Chat{}, errors.New("read gemini session: empty session id")
	}
	absPath, err := r.findSession(ctx, cache, sourceID)
	if err != nil {
		return pub_models.Chat{}, err
	}
	conv, ok, err := r.readConversation(absPath)
	if err != nil {
		return pub_models.Chat{}, fmt.Errorf("read gemini session %q: %w", sourceID, err)
	}
	if !ok || conv.sessionID != sourceID {
		return pub_models.Chat{}, fmt.Errorf("gemini session %q not found", sourceID)
	}
	if conv.kind == "subagent" {
		return pub_models.Chat{}, fmt.Errorf("gemini session %q is a subagent session", sourceID)
	}
	msgs := conv.mapped()
	if len(msgs) == 0 {
		return pub_models.Chat{}, fmt.Errorf("gemini session %q has no messages", sourceID)
	}
	created := conv.start
	if created.IsZero() {
		if info, statErr := vendors.StatAbs(r.FS, absPath); statErr == nil {
			created = info.ModTime()
		}
	}
	sys := fmt.Sprintf("Continued from Gemini CLI session %s", sourceID)
	if cwd := r.projectRoot(absPath); cwd != "" {
		sys = fmt.Sprintf("Continued from Gemini CLI session %s (originally at %s).", sourceID, cwd)
	}
	return pub_models.Chat{
		Created:  created,
		Source:   geminiSourceName,
		SourceID: sourceID,
		Messages: append([]pub_models.Message{{Role: "system", Content: sys}}, msgs...),
	}, nil
}

func (r SourceReader) home() string {
	if r.Root != "" {
		return r.Root
	}
	if os.Getenv("SANDBOX") == "sandbox-exec" {
		return vendors.HomeRelativeRoot("", ".cache", ".gemini")
	}
	return vendors.HomeRelativeRoot("", ".gemini")
}

// walkSessions visits every session file under <home>/tmp. WalkJSONLFiles
// fixes the .jsonl suffix, but Gemini still reads legacy .json documents in
// the same chats directory, so this reader owns its walk.
func (r SourceReader) walkSessions(ctx context.Context, visit func(absPath string) bool) error {
	root := r.home()
	if root == "" {
		return nil
	}
	tmp := filepath.Join(root, "tmp")
	st, err := os.Stat(tmp)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat %q: %w", tmp, err)
	}
	if !st.IsDir() {
		return nil
	}
	err = filepath.WalkDir(tmp, func(p string, d fs.DirEntry, walkErr error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Base(filepath.Dir(p)) != "chats" {
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".jsonl") && !strings.HasSuffix(name, ".json") {
			return nil
		}
		if visit(p) {
			return errStopGeminiWalk
		}
		return nil
	})
	if err != nil && !errors.Is(err, errStopGeminiWalk) {
		return fmt.Errorf("walk %q: %w", tmp, err)
	}
	return nil
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
	// A failed read is a fact about this run, not about the bytes, so it is
	// never cached. A file that names no session is cached as a zero row.
	if err != nil {
		return vendors.SourceRow{}, false
	}
	row := vendors.SourceRow{}
	if ok {
		row = conv.sourceRow(absPath, info, r.projectRoot(absPath))
	}
	if cacheable {
		cache.Store(absPath, info, row)
	}
	return row, row.SourceID != ""
}

func (r SourceReader) findSession(ctx context.Context, cache vendors.SourceCache, sourceID string) (string, error) {
	return vendors.FindSessionFile(ctx, geminiSourceName, r.FS, cache, sourceID, r.walkSessions, r.fileSessionID)
}

func (r SourceReader) fileSessionID(absPath string) (string, error) {
	f, err := vendors.OpenAbs(r.FS, absPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if strings.HasSuffix(absPath, ".json") {
		b, err := io.ReadAll(f)
		if err != nil {
			return "", err
		}
		var doc geminiRecord
		if err := json.Unmarshal(b, &doc); err != nil {
			return "", nil
		}
		return doc.SessionID, nil
	}
	id := ""
	err = vendors.ScanJSONLRecords(f, vendors.ReadMaxToken, func(rec geminiRecord) bool {
		if rec.SessionID != "" {
			id = rec.SessionID
			return false
		}
		if rec.Set != nil && rec.Set.SessionID != nil && *rec.Set.SessionID != "" {
			id = *rec.Set.SessionID
			return false
		}
		return true
	})
	return id, err
}

func (r SourceReader) projectRoot(absPath string) string {
	chats := filepath.Dir(absPath)
	if filepath.Base(chats) != "chats" {
		return ""
	}
	slug := filepath.Dir(chats)
	f, err := vendors.OpenAbs(r.FS, filepath.Join(slug, ".project_root"))
	if err != nil {
		return ""
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func (r SourceReader) readConversation(absPath string) (geminiConversation, bool, error) {
	f, err := vendors.OpenAbs(r.FS, absPath)
	if err != nil {
		return geminiConversation{}, false, err
	}
	defer f.Close()
	if strings.HasSuffix(absPath, ".json") {
		return readGeminiDocument(f)
	}
	return replayGeminiJSONL(f)
}

type geminiRecord struct {
	ID        string           `json:"id"`
	Timestamp string           `json:"timestamp"`
	Type      string           `json:"type"`
	Content   json.RawMessage  `json:"content"`
	ToolCalls []geminiToolCall `json:"toolCalls"`
	Thoughts  []geminiThought  `json:"thoughts"`
	Model     string           `json:"model"`

	SessionID   string         `json:"sessionId"`
	ProjectHash string         `json:"projectHash"`
	StartTime   string         `json:"startTime"`
	LastUpdated string         `json:"lastUpdated"`
	Summary     string         `json:"summary"`
	Kind        string         `json:"kind"`
	Directories []string       `json:"directories"`
	Messages    []geminiRecord `json:"messages"`

	Set      *geminiSet   `json:"$set"`
	Patch    *geminiPatch `json:"$patch"`
	RewindTo string       `json:"$rewindTo"`
}

type geminiSet struct {
	SessionID   *string         `json:"sessionId"`
	ProjectHash *string         `json:"projectHash"`
	StartTime   *string         `json:"startTime"`
	LastUpdated *string         `json:"lastUpdated"`
	Summary     *string         `json:"summary"`
	Kind        *string         `json:"kind"`
	Directories *[]string       `json:"directories"`
	Messages    *[]geminiRecord `json:"messages"`
}

type geminiPatch struct {
	ID        string                `json:"id"`
	Content   json.RawMessage       `json:"content"`
	ToolCalls []geminiToolCallPatch `json:"toolCalls"`
	Updates   []geminiMessagePatch  `json:"updates"`
	RemoveIDs []string              `json:"removeIds"`
	OrderIDs  []string              `json:"orderIds"`
}

type geminiMessagePatch struct {
	ID        string                `json:"id"`
	Content   json.RawMessage       `json:"content"`
	ToolCalls []geminiToolCallPatch `json:"toolCalls"`
}

type geminiToolCallPatch struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result"`
}

type geminiToolCall struct {
	ID     string          `json:"id"`
	Name   string          `json:"name"`
	Args   json.RawMessage `json:"args"`
	Result json.RawMessage `json:"result"`
	Status string          `json:"status"`
}

type geminiThought struct {
	Subject     string `json:"subject"`
	Description string `json:"description"`
}

type geminiConversation struct {
	sessionID string
	start     time.Time
	summary   string
	kind      string
	model     string
	messages  []geminiRecord
}

type geminiAccumulator struct {
	sessionID string
	startTime string
	summary   string
	kind      string
	byID      map[string]geminiRecord
	order     []string
}

func replayGeminiJSONL(r io.Reader) (geminiConversation, bool, error) {
	acc := &geminiAccumulator{byID: map[string]geminiRecord{}}
	err := vendors.ScanJSONLRecords(r, vendors.ReadMaxToken, func(rec geminiRecord) bool {
		acc.add(rec)
		return true
	})
	if err != nil {
		return geminiConversation{}, false, err
	}
	conv, ok := acc.finalize()
	return conv, ok, nil
}

func readGeminiDocument(r io.Reader) (geminiConversation, bool, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return geminiConversation{}, false, err
	}
	var doc geminiRecord
	if err := json.Unmarshal(b, &doc); err != nil {
		return geminiConversation{}, false, nil
	}
	if doc.SessionID == "" {
		return geminiConversation{}, false, nil
	}
	model := ""
	for _, msg := range doc.Messages {
		if msg.Type == "gemini" && msg.Model != "" {
			model = msg.Model
		}
	}
	return geminiConversation{
		sessionID: doc.SessionID,
		start:     geminiTime(doc.StartTime),
		summary:   doc.Summary,
		kind:      doc.Kind,
		model:     model,
		messages:  doc.Messages,
	}, true, nil
}

func (a *geminiAccumulator) add(rec geminiRecord) {
	switch {
	case rec.RewindTo != "":
		a.rewind(rec.RewindTo)
	case rec.Patch != nil:
		a.patch(rec.Patch)
	case rec.ID != "":
		a.message(rec)
	case rec.Set != nil:
		a.set(rec.Set)
	default:
		a.metadata(rec)
	}
}

func (a *geminiAccumulator) message(rec geminiRecord) {
	if _, ok := a.byID[rec.ID]; !ok {
		a.order = append(a.order, rec.ID)
	}
	a.byID[rec.ID] = rec
}

func (a *geminiAccumulator) rewind(id string) {
	idx := slices.Index(a.order, id)
	if idx < 0 {
		a.byID = map[string]geminiRecord{}
		a.order = nil
		return
	}
	for _, dropped := range a.order[idx:] {
		delete(a.byID, dropped)
	}
	a.order = a.order[:idx]
}

func (a *geminiAccumulator) set(s *geminiSet) {
	if s.SessionID != nil {
		a.sessionID = *s.SessionID
	}
	if s.StartTime != nil {
		a.startTime = *s.StartTime
	}
	if s.Summary != nil {
		a.summary = *s.Summary
	}
	if s.Kind != nil {
		a.kind = *s.Kind
	}
	if s.Messages != nil {
		a.byID = map[string]geminiRecord{}
		a.order = nil
		for _, msg := range *s.Messages {
			if msg.ID != "" {
				a.message(msg)
			}
		}
	}
}

func (a *geminiAccumulator) patch(p *geminiPatch) {
	if p.ID != "" {
		a.applyPatch(geminiMessagePatch{ID: p.ID, Content: p.Content, ToolCalls: p.ToolCalls})
	}
	for _, update := range p.Updates {
		a.applyPatch(update)
	}
	for _, id := range p.RemoveIDs {
		delete(a.byID, id)
		a.order = slices.DeleteFunc(a.order, func(v string) bool { return v == id })
	}
	if len(p.OrderIDs) > 0 {
		a.reorder(p.OrderIDs)
	}
}

func (a *geminiAccumulator) applyPatch(p geminiMessagePatch) {
	rec, ok := a.byID[p.ID]
	if !ok {
		return
	}
	if len(p.Content) > 0 {
		rec.Content = p.Content
	}
	for _, tc := range p.ToolCalls {
		if len(tc.Result) == 0 {
			continue
		}
		found := false
		for i := range rec.ToolCalls {
			if rec.ToolCalls[i].ID == tc.ID {
				rec.ToolCalls[i].Result = tc.Result
				found = true
			}
		}
		if !found {
			rec.ToolCalls = append(rec.ToolCalls, geminiToolCall{ID: tc.ID, Result: tc.Result})
		}
	}
	a.byID[p.ID] = rec
}

func (a *geminiAccumulator) reorder(ids []string) {
	seen := map[string]struct{}{}
	ordered := []string{}
	for _, id := range ids {
		if _, ok := a.byID[id]; ok {
			seen[id] = struct{}{}
			ordered = append(ordered, id)
		}
	}
	prefix := []string{}
	for _, id := range a.order {
		if _, ok := seen[id]; !ok {
			prefix = append(prefix, id)
		}
	}
	a.order = append(prefix, ordered...)
}

func (a *geminiAccumulator) metadata(rec geminiRecord) {
	if rec.SessionID != "" {
		a.sessionID = rec.SessionID
	}
	if rec.StartTime != "" {
		a.startTime = rec.StartTime
	}
	if rec.Summary != "" {
		a.summary = rec.Summary
	}
	if rec.Kind != "" {
		a.kind = rec.Kind
	}
	for _, msg := range rec.Messages {
		if msg.ID != "" {
			a.message(msg)
		}
	}
}

func (a *geminiAccumulator) finalize() (geminiConversation, bool) {
	if a.sessionID == "" {
		return geminiConversation{}, false
	}
	msgs := make([]geminiRecord, 0, len(a.order))
	model := ""
	for _, id := range a.order {
		rec := a.byID[id]
		msgs = append(msgs, rec)
		if rec.Type == "gemini" && rec.Model != "" {
			model = rec.Model
		}
	}
	return geminiConversation{
		sessionID: a.sessionID,
		start:     geminiTime(a.startTime),
		summary:   a.summary,
		kind:      a.kind,
		model:     model,
		messages:  msgs,
	}, true
}

func geminiTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.000Z"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

type geminiPart struct {
	Text             string                  `json:"text"`
	Thought          bool                    `json:"thought"`
	FunctionCall     *geminiFunctionCall     `json:"functionCall"`
	FunctionResponse *geminiFunctionResponse `json:"functionResponse"`
}

type geminiFunctionCall struct {
	ID   string          `json:"id"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

type geminiFunctionResponse struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Response json.RawMessage `json:"response"`
}

func geminiParts(raw json.RawMessage) []geminiPart {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil
	}
	switch raw[0] {
	case '"':
		var s string
		if json.Unmarshal(raw, &s) != nil || s == "" {
			return nil
		}
		return []geminiPart{{Text: s}}
	case '[':
		var parts []geminiPart
		if json.Unmarshal(raw, &parts) != nil {
			return nil
		}
		return parts
	case '{':
		var part geminiPart
		if json.Unmarshal(raw, &part) != nil {
			return nil
		}
		return []geminiPart{part}
	}
	return nil
}

var geminiIgnoredUserPrefixes = []string{"/", "?", "<session_context>", "<hook_context>"}

func geminiUserText(raw json.RawMessage) string {
	texts := []string{}
	for _, part := range geminiParts(raw) {
		if part.Text != "" && !part.Thought {
			texts = append(texts, part.Text)
		}
	}
	text := strings.Join(texts, "\n")
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	for _, prefix := range geminiIgnoredUserPrefixes {
		if strings.HasPrefix(trimmed, prefix) {
			return ""
		}
	}
	return text
}

type geminiCall struct {
	id     string
	name   string
	args   string
	result json.RawMessage
}

func geminiAssistantMessages(rec geminiRecord) []pub_models.Message {
	parts := geminiParts(rec.Content)
	modern := false
	for _, part := range parts {
		if part.FunctionCall != nil || part.Thought {
			modern = true
			break
		}
	}
	texts := []string{}
	reasoning := []string{}
	calls := []geminiCall{}
	if modern {
		for _, part := range parts {
			switch {
			case part.FunctionCall != nil:
				call := geminiCall{
					id:   part.FunctionCall.ID,
					name: part.FunctionCall.Name,
					args: geminiArgs(part.FunctionCall.Args),
				}
				if meta, ok := geminiToolMeta(rec.ToolCalls, call.id); ok {
					call.result = meta.Result
				}
				calls = append(calls, call)
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
	} else {
		for _, thought := range rec.Thoughts {
			text := thought.Description
			if thought.Subject != "" {
				text = "**" + thought.Subject + "** " + thought.Description
			}
			if strings.TrimSpace(text) != "" {
				reasoning = append(reasoning, text)
			}
		}
		for _, part := range parts {
			if part.Text != "" && !part.Thought {
				texts = append(texts, part.Text)
			}
		}
		for _, tc := range rec.ToolCalls {
			calls = append(calls, geminiCall{
				id:     tc.ID,
				name:   tc.Name,
				args:   geminiArgs(tc.Args),
				result: tc.Result,
			})
		}
	}

	msg := pub_models.Message{Role: "assistant"}
	toolMsgs := []pub_models.Message{}
	for _, call := range calls {
		if call.id == "" || call.name == "" || !geminiSettled(call) {
			texts = append(texts, fmt.Sprintf("[tool call: %s(%s)]", call.name, call.args))
			continue
		}
		c := pub_models.Call{
			ID:   call.id,
			Name: call.name,
			Function: pub_models.Specification{
				Name:      call.name,
				Arguments: call.args,
			},
		}
		c.Patch()
		msg.ToolCalls = append(msg.ToolCalls, c)
		toolMsgs = append(toolMsgs, pub_models.Message{
			Role:       "tool",
			ToolCallID: call.id,
			Content:    geminiResultText(call.result),
		})
	}
	msg.Content = strings.Join(texts, "\n")
	msg.ReasoningContent = strings.Join(reasoning, "\n")
	if msg.Content == "" && len(msg.ToolCalls) == 0 {
		if msg.ReasoningContent == "" {
			return nil
		}
		msg.Content = "[thinking] " + msg.ReasoningContent
	}
	return append([]pub_models.Message{msg}, toolMsgs...)
}

func geminiArgs(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "{}"
	}
	return string(raw)
}

// A result patch is the settlement signal: the result arrives after the call
// record is written, and the patch does not carry a status.
func geminiSettled(call geminiCall) bool {
	return geminiHasResult(call.result)
}

func geminiHasResult(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) > 0 && !bytes.Equal(raw, []byte("null"))
}

func geminiToolMeta(calls []geminiToolCall, id string) (geminiToolCall, bool) {
	if id == "" {
		return geminiToolCall{}, false
	}
	for _, call := range calls {
		if call.ID == id {
			return call, true
		}
	}
	return geminiToolCall{}, false
}

func geminiResultText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if !geminiHasResult(raw) {
		return ""
	}
	switch raw[0] {
	case '"':
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
	case '[':
		var parts []geminiPart
		if json.Unmarshal(raw, &parts) == nil {
			texts := []string{}
			for _, part := range parts {
				if part.Text != "" {
					texts = append(texts, part.Text)
				}
				if part.FunctionResponse != nil {
					if text := geminiResponseText(part.FunctionResponse.Response); text != "" {
						texts = append(texts, text)
					}
				}
			}
			return strings.Join(texts, "\n")
		}
	case '{':
		var part geminiPart
		if json.Unmarshal(raw, &part) == nil && part.FunctionResponse != nil {
			return geminiResponseText(part.FunctionResponse.Response)
		}
	}
	return string(raw)
}

func geminiResponseText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if !geminiHasResult(raw) {
		return ""
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
	}
	var response struct {
		Output any `json:"output"`
	}
	if json.Unmarshal(raw, &response) == nil && response.Output != nil {
		switch v := response.Output.(type) {
		case string:
			return v
		default:
			b, err := json.Marshal(v)
			if err == nil {
				return string(b)
			}
		}
	}
	return string(raw)
}

func (c geminiConversation) mapped() []pub_models.Message {
	out := []pub_models.Message{}
	for _, rec := range c.messages {
		switch rec.Type {
		case "user":
			if text := geminiUserText(rec.Content); text != "" {
				out = append(out, pub_models.Message{Role: "user", Content: text})
			}
		case "gemini":
			out = append(out, geminiAssistantMessages(rec)...)
		}
	}
	return out
}

func (c geminiConversation) sourceRow(absPath string, info fs.FileInfo, cwd string) vendors.SourceRow {
	if c.sessionID == "" || c.kind == "subagent" {
		return vendors.SourceRow{}
	}
	msgs := c.mapped()
	if len(msgs) == 0 {
		return vendors.SourceRow{}
	}
	first := firstGeminiUserMessage(msgs)
	preview := c.summary
	if strings.TrimSpace(preview) == "" {
		preview = first
	}
	full := first
	if full == "" {
		full = preview
	}
	preview = vendors.TruncateOneLine(preview, geminiPreviewRunes)
	if preview == "" {
		preview = geminiMissingPreview
	}
	created := c.start
	if created.IsZero() && info != nil {
		created = info.ModTime()
	}
	return vendors.SourceRow{
		Source:               geminiSourceName,
		SourceID:             c.sessionID,
		Created:              created,
		FirstUserMessage:     preview,
		FullFirstUserMessage: full,
		MessageCount:         len(msgs),
		Model:                c.model,
		RawPath:              absPath,
		Cwd:                  cwd,
	}
}

func firstGeminiUserMessage(msgs []pub_models.Message) string {
	for _, msg := range msgs {
		if msg.Role == "user" && strings.TrimSpace(msg.Content) != "" {
			return msg.Content
		}
	}
	return ""
}
