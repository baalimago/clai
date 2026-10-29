// Package antigravity reads Antigravity conversations from its trajectory
// databases.
//
// Antigravity keeps one SQLite database per conversation under
// <home>/conversations/<cascade-id>.db, where <home> is ~/.gemini/antigravity
// for the desktop app, ~/.gemini/antigravity-ide for the separately installed
// IDE and ~/.gemini/antigravity-cli for the CLI. The steps table holds one
// protobuf-encoded step per row: a user prompt, a model turn, a tool result, or
// an operational event. A conversation's workspace is recorded in the
// trajectory_metadata_blob table.
//
// The app also keeps one index per store root, conversation_summaries.db. It is
// the only place that carries a generated conversation title and the parent of
// an agent run the app spawned, so the reader consults it first and falls back
// to the trajectory alone when it is absent.
//
// The reader never writes to the store. A write-ahead log is replayed by
// internal/sqlitepage, so a running Antigravity still reads as the committed
// state.
package antigravity

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/baalimago/clai/internal/sqlitepage"
	"github.com/baalimago/clai/internal/vendors"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

const (
	sourceName   = "antigravity"
	previewRunes = 100
)

// Step types. Every other type is an operational event and maps to no message.
const (
	stepUser       int64 = 14
	stepAssistant  int64 = 15
	stepError      int64 = 17
	stepTaskNotice int64 = 101
	stepToolResult int64 = 132
)

// Column positions in the steps and trajectory_metadata_blob tables. The
// position binding is proved before any row is read, so a rebuilt table
// fails instead of misreading a column.
const (
	colStepIdx     = 0
	colStepType    = 1
	colStepPayload = 9

	colMetadataID   = 0
	colMetadataData = 1
)

var stepColumns = map[int]string{
	colStepIdx:     "idx",
	colStepType:    "step_type",
	colStepPayload: "step_payload",
}

var metadataColumns = map[int]string{
	colMetadataID:   "id",
	colMetadataData: "data",
}

// Step payload field numbers.
const (
	fieldStepType     = 1
	fieldMeta         = 5
	fieldUser         = 19
	fieldAssistant    = 20
	fieldToolResult   = 140
	fieldMetaTime     = 1
	fieldMetaCall     = 4
	fieldUserText     = 2
	fieldUserPart     = 3
	fieldAssistantTxt = 1
	fieldAssistantAlt = 8
	fieldThought      = 3
	fieldToolCall     = 7
	fieldCallID       = 1
	fieldCallName     = 2
	fieldCallArgs     = 3
	fieldResultBody   = 2
	fieldResultText   = 1
)

// SourceReader discovers and reads Antigravity conversations.
type SourceReader struct {
	// Roots overrides the store homes. It exists for tests and a non-default
	// install; production code leaves it empty.
	Roots []string
}

// Source returns the stable source name "antigravity".
func (r SourceReader) Source() string { return sourceName }

// Discover lists every Antigravity conversation.
//
//   - A missing store root yields no rows and no error.
//   - One row per conversation database that holds at least one user message and
//     that the index does not mark as a child agent run. A database without a
//     user message, a database the index nests under a parent, a database
//     without the steps table and a file that is not a SQLite database yield
//     none. The index is asked first; when it is absent or silent the
//     trajectory decides.
//   - MessageCount is the number of messages Read returns minus the system
//     preamble, because discovery and Read share one mapper.
//   - FirstUserMessage is the index title when the app generated one, else the
//     first user prompt, truncated to the preview width; FullFirstUserMessage is
//     always the full first prompt.
//   - Cwd is the first workspace URI in trajectory_metadata_blob, else the first
//     workspace URI in the index; Created is the first step timestamp, else the
//     file modification time.
//   - A database that cannot be read is skipped and not cached. A cache hit
//     costs one stat and no open.
func (r SourceReader) Discover(ctx context.Context, cache vendors.SourceCache) ([]vendors.SourceRow, error) {
	rows := []vendors.SourceRow{}
	seen := map[string]struct{}{}
	err := r.walkSessions(ctx, func(absPath, sourceID string, idx map[string]summary) bool {
		if _, ok := seen[sourceID]; ok {
			return false
		}
		row, ok := r.discoverFile(cache, absPath, sourceID, idx[sourceID])
		if !ok {
			return false
		}
		seen[sourceID] = struct{}{}
		rows = append(rows, row)
		return false
	})
	if err != nil {
		return nil, fmt.Errorf("discover antigravity sessions: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("discover antigravity sessions: %w", err)
	}
	return rows, nil
}

// Read returns one Antigravity conversation as a clai Chat.
//
//   - Messages start with one system preamble; Source is "antigravity" and
//     SourceID is the requested id.
//   - A user step maps to one user message. An assistant step maps to one
//     assistant message with its text, reasoning and settled tool calls; each
//     settled call is followed by its tool result. A call without a result,
//     id or name becomes one [tool call: name(args)] text line.
//   - Error, task-notification and unknown steps map to no message.
//   - An empty id, an unknown id, a child agent run and a conversation without
//     a user message are errors. A child run is never a clone target.
func (r SourceReader) Read(ctx context.Context, cache vendors.SourceCache, sourceID string) (pub_models.Chat, error) {
	if sourceID == "" {
		return pub_models.Chat{}, errors.New("read antigravity session: empty session id")
	}
	absPath, sum, err := r.findSession(ctx, cache, sourceID)
	if err != nil {
		return pub_models.Chat{}, err
	}
	if sum.child() {
		if sum.parent != "" {
			return pub_models.Chat{}, fmt.Errorf("antigravity session %q is a subagent of %q", sourceID, sum.parent)
		}
		return pub_models.Chat{}, fmt.Errorf("antigravity session %q is a subagent session", sourceID)
	}
	conv, err := r.readConversation(absPath)
	if err != nil {
		return pub_models.Chat{}, fmt.Errorf("read antigravity session %q: %w", sourceID, err)
	}
	if !conv.hasUser {
		return pub_models.Chat{}, fmt.Errorf("antigravity session %q has no user message", sourceID)
	}
	created := conv.created
	if created.IsZero() {
		if info, statErr := os.Stat(absPath); statErr == nil {
			created = info.ModTime()
		}
	}
	sys := fmt.Sprintf("Continued from Antigravity session %s", sourceID)
	cwd := conv.cwd
	if cwd == "" {
		cwd = sum.cwd
	}
	if cwd != "" {
		sys = fmt.Sprintf("Continued from Antigravity session %s (originally at %s).", sourceID, cwd)
	}
	return pub_models.Chat{
		Created:  created,
		Source:   sourceName,
		SourceID: sourceID,
		Messages: append([]pub_models.Message{{Role: "system", Content: sys}}, conv.messages...),
	}, nil
}

func (r SourceReader) discoverFile(cache vendors.SourceCache, absPath, sourceID string, sum summary) (vendors.SourceRow, bool) {
	info, err := stateInfoOf(absPath)
	if err != nil {
		return vendors.SourceRow{}, false
	}
	// An indexed child run needs no trajectory read: the index already answers
	// what the row would say, and a child is never a row. The index is fresh on
	// every run, so it also retires a row the cache still holds.
	if sum.child() {
		if cache != nil {
			cache.Store(absPath, info, vendors.SourceRow{})
		}
		return vendors.SourceRow{}, false
	}
	if cache != nil {
		if row, ok := cache.Lookup(absPath, info); ok {
			if row.SourceID == "" {
				return vendors.SourceRow{}, false
			}
			row = withSummary(row, sum)
			cache.Store(absPath, info, row)
			return row, true
		}
	}
	conv, err := r.readConversation(absPath)
	if err != nil {
		return vendors.SourceRow{}, false
	}
	if !conv.hasUser {
		if cache != nil {
			cache.Store(absPath, info, vendors.SourceRow{})
		}
		return vendors.SourceRow{}, false
	}
	row := withSummary(vendors.SourceRow{
		Source:               sourceName,
		SourceID:             sourceID,
		Created:              conv.created,
		FirstUserMessage:     vendors.TruncateOneLine(conv.firstUser, previewRunes),
		FullFirstUserMessage: conv.firstUser,
		MessageCount:         len(conv.messages),
		RawPath:              absPath,
		Cwd:                  conv.cwd,
	}, sum)
	if row.Created.IsZero() {
		row.Created = info.ModTime()
	}
	if cache != nil {
		cache.Store(absPath, info, row)
	}
	return row, true
}

// withSummary applies the fields the app's index answers to a row, whether the
// row came from the cache or from a read. The index is read on every run, so a
// title the app generated after the row was cached still reaches the list.
func withSummary(row vendors.SourceRow, sum summary) vendors.SourceRow {
	if sum.title != "" {
		row.FirstUserMessage = vendors.TruncateOneLine(sum.title, previewRunes)
	}
	if row.Cwd == "" {
		row.Cwd = sum.cwd
	}
	return row
}

// findSession returns the database of one conversation together with the
// entry the store index holds for it.
func (r SourceReader) findSession(ctx context.Context, cache vendors.SourceCache, sourceID string) (string, summary, error) {
	if cache != nil {
		if p, ok := cache.Locate(sourceName, sourceID); ok {
			if info, err := stateInfoOf(p); err == nil {
				if row, ok := cache.Lookup(p, info); ok && row.SourceID == sourceID {
					return p, readSummaries(storeRoot(p))[sourceID], nil
				}
			}
		}
	}
	var (
		found string
		sum   summary
	)
	err := r.walkSessions(ctx, func(absPath, id string, idx map[string]summary) bool {
		if id != sourceID {
			return false
		}
		found, sum = absPath, idx[id]
		return true
	})
	if err != nil {
		return "", summary{}, fmt.Errorf("find antigravity session %q: %w", sourceID, err)
	}
	if found == "" {
		return "", summary{}, fmt.Errorf("antigravity session %q not found", sourceID)
	}
	return found, sum, nil
}

func (r SourceReader) walkSessions(ctx context.Context, visit func(absPath, sourceID string, idx map[string]summary) bool) error {
	roots, err := r.roots()
	if err != nil {
		return err
	}
	for _, root := range roots {
		convs := filepath.Join(root, "conversations")
		entries, err := os.ReadDir(convs)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return fmt.Errorf("read %q: %w", convs, err)
		}
		idx := readSummaries(root)
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".db") {
				continue
			}
			sourceID := strings.TrimSuffix(entry.Name(), ".db")
			if sourceID == "" {
				continue
			}
			if visit(filepath.Join(convs, entry.Name()), sourceID, idx) {
				return nil
			}
		}
	}
	return nil
}

// storeRoot is the home a conversation database lives under: the parent of its
// conversations directory, where the app's index sits.
func storeRoot(absPath string) string {
	return filepath.Dir(filepath.Dir(absPath))
}

func (r SourceReader) roots() ([]string, error) {
	if len(r.Roots) > 0 {
		return r.Roots, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home directory: %w", err)
	}
	base := filepath.Join(home, ".gemini")
	return []string{
		filepath.Join(base, "antigravity"),
		filepath.Join(base, "antigravity-ide"),
		filepath.Join(base, "antigravity-cli"),
	}, nil
}

type conversation struct {
	messages  []pub_models.Message
	firstUser string
	hasUser   bool
	created   time.Time
	cwd       string
}

func (r SourceReader) readConversation(absPath string) (conversation, error) {
	db, err := sqlitepage.Open(absPath)
	if err != nil {
		return conversation{}, fmt.Errorf("open antigravity database %q: %w", absPath, err)
	}
	defer db.Close()

	steps, err := readSteps(db)
	if err != nil {
		return conversation{}, err
	}
	parsed := make([]parsedStep, 0, len(steps))
	for _, raw := range steps {
		step, err := parseStep(raw)
		if err != nil {
			return conversation{}, fmt.Errorf("parse antigravity step %d: %w", raw.idx, err)
		}
		parsed = append(parsed, step)
	}
	conv := mapSteps(parsed)
	if conv.created.IsZero() {
		for _, step := range parsed {
			if !step.created.IsZero() {
				conv.created = step.created
				break
			}
		}
	}
	conv.cwd, _ = readCwd(db)
	return conv, nil
}

type rawStep struct {
	idx     int64
	typ     int64
	payload []byte
}

func readSteps(db *sqlitepage.DB) ([]rawStep, error) {
	tbl, err := db.TableWithColumns("steps", stepColumns)
	if err != nil {
		return nil, err
	}
	steps := []rawStep{}
	err = tbl.Rows(func(_ int64, vals []sqlitepage.Value) error {
		if len(vals) <= colStepPayload {
			return nil
		}
		if vals[colStepType].Kind != sqlitepage.Integer {
			return fmt.Errorf("steps column %d is not an integer", colStepType)
		}
		payload, ok := blobOf(vals, colStepPayload)
		if !ok {
			return nil
		}
		steps = append(steps, rawStep{
			idx:     intAt(vals, colStepIdx),
			typ:     vals[colStepType].Int,
			payload: payload,
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan antigravity steps: %w", err)
	}
	sort.SliceStable(steps, func(i, j int) bool { return steps[i].idx < steps[j].idx })
	return steps, nil
}

func readCwd(db *sqlitepage.DB) (string, error) {
	tbl, err := db.Table("trajectory_metadata_blob")
	if err != nil {
		if errors.Is(err, sqlitepage.ErrUnsupported) {
			return "", nil
		}
		return "", err
	}
	cols, err := tbl.Columns()
	if err != nil {
		return "", err
	}
	if err := sqlitepage.CheckColumns("trajectory_metadata_blob", cols, metadataColumns); err != nil {
		return "", err
	}
	cwd := ""
	err = tbl.Rows(func(_ int64, vals []sqlitepage.Value) error {
		if len(vals) <= colMetadataData {
			return nil
		}
		if vals[colMetadataID].Kind != sqlitepage.Text || vals[colMetadataID].Text != "main" {
			return nil
		}
		data, ok := blobOf(vals, colMetadataData)
		if !ok {
			return nil
		}
		cwd = workspaceURI(data)
		return nil
	})
	if err != nil {
		return "", err
	}
	return cwd, nil
}

// workspaceURI returns the first workspace URI recorded in the trajectory
// metadata message. The field is best-effort: an unreadable blob yields no
// directory, not an error.
func workspaceURI(data []byte) string {
	fields, err := parseWire(data)
	if err != nil {
		return ""
	}
	for _, f := range fields {
		if f.num != 1 || f.typ != wireBytes {
			continue
		}
		inner, err := parseWire(f.b)
		if err != nil {
			continue
		}
		for _, g := range inner {
			if g.num != 1 || g.typ != wireBytes {
				continue
			}
			uri := string(g.b)
			if after, ok := strings.CutPrefix(uri, "file://"); ok {
				return after
			}
		}
	}
	return ""
}

func blobOf(vals []sqlitepage.Value, i int) ([]byte, bool) {
	if i < 0 || i >= len(vals) {
		return nil, false
	}
	switch vals[i].Kind {
	case sqlitepage.Blob:
		return vals[i].Blob, true
	case sqlitepage.Text:
		return []byte(vals[i].Text), true
	default:
		return nil, false
	}
}

func intAt(vals []sqlitepage.Value, i int) int64 {
	if i < 0 || i >= len(vals) || vals[i].Kind != sqlitepage.Integer {
		return 0
	}
	return vals[i].Int
}

type parsedStep struct {
	idx       int64
	typ       int64
	created   time.Time
	user      string
	text      string
	thought   string
	calls     []toolCall
	resultID  string
	resultTxt string
}

type toolCall struct {
	id   string
	name string
	args string
}

func parseStep(raw rawStep) (parsedStep, error) {
	fields, err := parseWire(raw.payload)
	if err != nil {
		return parsedStep{}, err
	}
	if got, ok := firstVarint(fields, fieldStepType); ok && int64(got) != raw.typ {
		return parsedStep{}, fmt.Errorf("payload step type %d does not match column type %d", got, raw.typ)
	}
	step := parsedStep{idx: raw.idx, typ: raw.typ}
	if meta, ok := firstBytes(fields, fieldMeta); ok {
		step.created = parseTimestamp(meta)
	}
	switch raw.typ {
	case stepUser:
		if body, ok := firstBytes(fields, fieldUser); ok {
			step.user = parseUser(body)
		}
	case stepAssistant:
		if body, ok := firstBytes(fields, fieldAssistant); ok {
			text, thought, calls, err := parseAssistant(body)
			if err != nil {
				return parsedStep{}, err
			}
			step.text = text
			step.thought = thought
			step.calls = calls
		}
	case stepToolResult:
		if meta, ok := firstBytes(fields, fieldMeta); ok {
			step.resultID = parseCallID(meta)
		}
		if body, ok := firstBytes(fields, fieldToolResult); ok {
			step.resultTxt = parseToolResult(body)
		}
	}
	return step, nil
}

func parseTimestamp(meta []byte) time.Time {
	fields, err := parseWire(meta)
	if err != nil {
		return time.Time{}
	}
	stamp, ok := firstBytes(fields, fieldMetaTime)
	if !ok {
		return time.Time{}
	}
	parts, err := parseWire(stamp)
	if err != nil {
		return time.Time{}
	}
	sec, ok := firstVarint(parts, 1)
	if !ok || sec == 0 {
		return time.Time{}
	}
	nanos, _ := firstVarint(parts, 2)
	return time.Unix(int64(sec), int64(nanos)).UTC()
}

func parseUser(body []byte) string {
	fields, err := parseWire(body)
	if err != nil {
		return ""
	}
	if text, ok := firstBytes(fields, fieldUserText); ok {
		return string(text)
	}
	var b strings.Builder
	for _, f := range fields {
		if f.num != fieldUserPart || f.typ != wireBytes {
			continue
		}
		b.WriteString(userPart(f.b))
	}
	return b.String()
}

func userPart(data []byte) string {
	fields, err := parseWire(data)
	if err == nil {
		if text, ok := firstBytes(fields, 1); ok {
			return string(text)
		}
	}
	return string(data)
}

func parseAssistant(body []byte) (string, string, []toolCall, error) {
	fields, err := parseWire(body)
	if err != nil {
		return "", "", nil, err
	}
	text := textField(fields, fieldAssistantTxt)
	if strings.TrimSpace(text) == "" {
		text = textField(fields, fieldAssistantAlt)
	}
	thought := textField(fields, fieldThought)
	calls := []toolCall{}
	for _, f := range fields {
		if f.num != fieldToolCall || f.typ != wireBytes {
			continue
		}
		call, err := parseToolCall(f.b)
		if err != nil {
			return "", "", nil, err
		}
		if call.id == "" && call.name == "" {
			continue
		}
		calls = append(calls, call)
	}
	return text, thought, calls, nil
}

func parseToolCall(body []byte) (toolCall, error) {
	fields, err := parseWire(body)
	if err != nil {
		return toolCall{}, err
	}
	return toolCall{
		id:   textField(fields, fieldCallID),
		name: textField(fields, fieldCallName),
		args: textField(fields, fieldCallArgs),
	}, nil
}

func parseToolResult(body []byte) string {
	fields, err := parseWire(body)
	if err != nil {
		return ""
	}
	result, ok := firstBytes(fields, fieldResultBody)
	if !ok {
		return ""
	}
	inner, err := parseWire(result)
	if err != nil {
		return ""
	}
	return textField(inner, fieldResultText)
}

func parseCallID(meta []byte) string {
	fields, err := parseWire(meta)
	if err != nil {
		return ""
	}
	call, ok := firstBytes(fields, fieldMetaCall)
	if !ok {
		return ""
	}
	inner, err := parseWire(call)
	if err != nil {
		return ""
	}
	return textField(inner, fieldCallID)
}

func textField(fields []wireField, num int) string {
	value, ok := firstBytes(fields, num)
	if !ok {
		return ""
	}
	return string(value)
}

func mapSteps(steps []parsedStep) conversation {
	results := map[string]string{}
	for _, step := range steps {
		if step.typ != stepToolResult || step.resultID == "" {
			continue
		}
		if _, exists := results[step.resultID]; !exists {
			results[step.resultID] = step.resultTxt
		}
	}
	conv := conversation{}
	for _, step := range steps {
		switch step.typ {
		case stepUser:
			text := strings.TrimSpace(step.user)
			if text == "" {
				continue
			}
			conv.messages = append(conv.messages, pub_models.Message{Role: "user", Content: text})
			conv.hasUser = true
			if conv.firstUser == "" {
				conv.firstUser = text
			}
		case stepAssistant:
			msg, toolMsgs := assistantMessages(step, results)
			if msg == nil {
				continue
			}
			conv.messages = append(conv.messages, *msg)
			conv.messages = append(conv.messages, toolMsgs...)
		case stepError, stepTaskNotice:
			// Operational events are not conversation messages.
		}
	}
	return conv
}

func assistantMessages(step parsedStep, results map[string]string) (*pub_models.Message, []pub_models.Message) {
	msg := pub_models.Message{
		Role:             "assistant",
		Content:          step.text,
		ReasoningContent: step.thought,
	}
	toolMsgs := []pub_models.Message{}
	unpaired := []string{}
	for _, call := range step.calls {
		result, settled := results[call.id]
		if call.id == "" || call.name == "" || !settled {
			unpaired = append(unpaired, fmt.Sprintf("[tool call: %s(%s)]", call.name, call.args))
			continue
		}
		patched := pub_models.Call{
			ID:   call.id,
			Name: call.name,
			Function: pub_models.Specification{
				Name:      call.name,
				Arguments: call.args,
			},
		}
		patched.Patch()
		msg.ToolCalls = append(msg.ToolCalls, patched)
		toolMsgs = append(toolMsgs, pub_models.Message{
			Role:       "tool",
			ToolCallID: call.id,
			Content:    result,
		})
	}
	if len(unpaired) > 0 {
		msg.Content = vendors.JoinNonEmpty(msg.Content, strings.Join(unpaired, "\n"))
	}
	if strings.TrimSpace(msg.Content) == "" && len(msg.ToolCalls) == 0 {
		if strings.TrimSpace(msg.ReasoningContent) == "" {
			return nil, nil
		}
		msg.Content = "[thinking] " + msg.ReasoningContent
	}
	return &msg, toolMsgs
}

// cacheInfo combines the database and its write-ahead log into one cache key,
// so a row cannot survive a change that only reached the log.
type cacheInfo struct {
	name string
	size int64
	mod  time.Time
}

func (s cacheInfo) Name() string       { return s.name }
func (s cacheInfo) Size() int64        { return s.size }
func (s cacheInfo) Mode() fs.FileMode  { return 0 }
func (s cacheInfo) ModTime() time.Time { return s.mod }
func (s cacheInfo) IsDir() bool        { return false }
func (s cacheInfo) Sys() any           { return nil }

func stateInfoOf(path string) (fs.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	out := cacheInfo{name: info.Name(), size: info.Size(), mod: info.ModTime()}
	wal, err := os.Stat(path + "-wal")
	if err == nil {
		out.size += wal.Size()
		if wal.ModTime().After(out.mod) {
			out.mod = wal.ModTime()
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	return out, nil
}

// wireField is one protobuf wire value.
type wireField struct {
	num int
	typ int
	u   uint64
	b   []byte
}

const (
	wireVarint = 0
	wire64     = 1
	wireBytes  = 2
	wire32     = 5
)

func parseWire(data []byte) ([]wireField, error) {
	fields := []wireField{}
	for i := 0; i < len(data); {
		key, n := binary.Uvarint(data[i:])
		if n <= 0 {
			return nil, errors.New("invalid protobuf key")
		}
		i += n
		num := key >> 3
		typ := int(key & 7)
		if num == 0 {
			return nil, errors.New("protobuf field number 0")
		}
		f := wireField{num: int(num), typ: typ}
		switch typ {
		case wireVarint:
			value, n := binary.Uvarint(data[i:])
			if n <= 0 {
				return nil, errors.New("invalid protobuf varint")
			}
			f.u = value
			i += n
		case wire64:
			if i+8 > len(data) {
				return nil, errors.New("short protobuf 64-bit value")
			}
			f.b = data[i : i+8]
			i += 8
		case wireBytes:
			length, n := binary.Uvarint(data[i:])
			if n <= 0 {
				return nil, errors.New("invalid protobuf length")
			}
			i += n
			if length > uint64(len(data)-i) {
				return nil, errors.New("protobuf length exceeds buffer")
			}
			f.b = data[i : i+int(length)]
			i += int(length)
		case wire32:
			if i+4 > len(data) {
				return nil, errors.New("short protobuf 32-bit value")
			}
			f.b = data[i : i+4]
			i += 4
		default:
			return nil, fmt.Errorf("unsupported protobuf wire type %d", typ)
		}
		fields = append(fields, f)
	}
	return fields, nil
}

func firstBytes(fields []wireField, num int) ([]byte, bool) {
	for _, f := range fields {
		if f.num == num && f.typ == wireBytes {
			return f.b, true
		}
	}
	return nil, false
}

func firstVarint(fields []wireField, num int) (uint64, bool) {
	for _, f := range fields {
		if f.num == num && f.typ == wireVarint {
			return f.u, true
		}
	}
	return 0, false
}
