// Package opencode reads opencode conversations from its SQLite database.
//
// opencode keeps one database for the whole machine: $XDG_DATA_HOME/opencode,
// else ~/.local/share/opencode, and inside it opencode.db. Session metadata
// lives in session_v2 and the conversation in session_message, whose data
// column holds one JSON message. The tables are addressed by column position,
// so every read first proves that the positions still name the columns it was
// written for. Every field is read best-effort: a missing field drops that
// field, and a message this reader cannot decode contributes nothing.
//
// The reader never writes to the database. A write-ahead log is replayed by
// internal/sqlitepage, so a running opencode still reads as the committed
// state.
package opencode

import (
	"context"
	"encoding/json"
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
	sourceName   = "opencode"
	sessionTable = "session_v2"
	messageTable = "session_message"
	previewRunes = 100
)

// Column positions in session_v2, which drizzle migrations only append to.
// sessionColumns binds each position to its name so a rebuild that reorders
// the table fails instead of misreading a column.
const (
	colSessionID     = 0
	colSessionParent = 3
	colSessionCwd    = 7
	colSessionTitle  = 9
	colSessionModel  = 26
	colSessionBorn   = 27
)

var sessionColumns = map[int]string{
	colSessionID:     "id",
	colSessionParent: "parent_id",
	colSessionCwd:    "directory",
	colSessionTitle:  "title",
	colSessionModel:  "model",
	colSessionBorn:   "time_created",
}

const (
	colMessageSession = 1
	colMessageKind    = 2
	colMessageSeq     = 3
	colMessageData    = 6
)

var messageColumns = map[int]string{
	colMessageSession: "session_id",
	colMessageKind:    "type",
	colMessageSeq:     "seq",
	colMessageData:    "data",
}

// SourceReader discovers and reads opencode sessions.
type SourceReader struct {
	// DBPath overrides the database path. It exists for tests and for a
	// non-default install; production code leaves it empty.
	DBPath string
}

func (r SourceReader) Source() string { return sourceName }

var (
	errNoDatabase = errors.New("opencode database not found")
	errStopScan   = errors.New("stop scan")
)

// Discover lists every user-facing opencode session.
//
// The foreign cache is per file, but one database holds every session, so the
// cache cannot represent this source. Discovery scans the database instead.
func (r SourceReader) Discover(ctx context.Context, _ vendors.SourceCache) ([]vendors.SourceRow, error) {
	db, err := r.open()
	if err != nil {
		if errors.Is(err, errNoDatabase) {
			return nil, nil
		}
		return nil, err
	}
	defer db.Close()

	counts, err := r.countMessages(ctx, db)
	if err != nil {
		return nil, err
	}
	return r.discoverRows(ctx, db, counts)
}

// sessionCounts is what discovery learns from a session's messages: the exact
// message count and the first prompt.
type sessionCounts struct {
	count     int
	firstUser string
	firstSeq  int64
}

func (r SourceReader) countMessages(ctx context.Context, db *sqlitepage.DB) (map[string]sessionCounts, error) {
	tbl, err := messageTableFor(db)
	if err != nil {
		return nil, err
	}
	counts := map[string]sessionCounts{}
	err = tbl.Rows(func(_ int64, vals []sqlitepage.Value) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		row, ok := messageRowOf(vals)
		if !ok {
			return nil
		}
		c := counts[row.session]
		msgs := row.conversation()
		c.count += len(msgs)
		if len(msgs) > 0 && msgs[0].Role == "user" && (c.firstUser == "" || row.seq < c.firstSeq) {
			c.firstUser = msgs[0].Content
			c.firstSeq = row.seq
		}
		counts[row.session] = c
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan opencode messages: %w", err)
	}
	return counts, nil
}

func (r SourceReader) discoverRows(ctx context.Context, db *sqlitepage.DB, counts map[string]sessionCounts) ([]vendors.SourceRow, error) {
	tbl, err := sessionTableFor(db)
	if err != nil {
		return nil, err
	}
	rows := []vendors.SourceRow{}
	err = tbl.Rows(func(_ int64, vals []sqlitepage.Value) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		s, ok := sessionOf(vals)
		if !ok || s.parent != "" {
			return nil
		}
		c, ok := counts[s.id]
		if !ok || c.count == 0 {
			return nil
		}
		// The title is the readable preview; the field that carries the full
		// prompt keeps the prompt, because grouping hashes it.
		preview := s.title
		full := c.firstUser
		if strings.TrimSpace(full) == "" {
			full = preview
		}
		if strings.TrimSpace(preview) == "" {
			preview = full
		}
		rows = append(rows, vendors.SourceRow{
			Source:               sourceName,
			SourceID:             s.id,
			Created:              s.created(),
			FirstUserMessage:     vendors.TruncateOneLine(preview, previewRunes),
			FullFirstUserMessage: full,
			MessageCount:         c.count,
			Model:                s.modelName(),
			RawPath:              r.databasePath(),
			Cwd:                  s.cwd,
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan opencode sessions: %w", err)
	}
	return rows, nil
}

func (r SourceReader) Read(ctx context.Context, _ vendors.SourceCache, sourceID string) (pub_models.Chat, error) {
	if sourceID == "" {
		return pub_models.Chat{}, errors.New("read opencode session: empty session id")
	}
	db, err := r.open()
	if err != nil {
		return pub_models.Chat{}, fmt.Errorf("read opencode session %q: %w", sourceID, err)
	}
	defer db.Close()

	sess, err := findSession(ctx, db, sourceID)
	if err != nil {
		return pub_models.Chat{}, err
	}
	if sess.parent != "" {
		return pub_models.Chat{}, fmt.Errorf("opencode session %q is a subagent of %q", sourceID, sess.parent)
	}
	messages, err := r.sessionMessages(ctx, db, sourceID)
	if err != nil {
		return pub_models.Chat{}, err
	}
	if len(messages) == 0 {
		return pub_models.Chat{}, fmt.Errorf("opencode session %q has no messages", sourceID)
	}

	sys := fmt.Sprintf("Continued from opencode session %s", sourceID)
	if sess.cwd != "" {
		sys = fmt.Sprintf("Continued from opencode session %s (originally at %s).", sourceID, sess.cwd)
	}
	return pub_models.Chat{
		Created:  sess.created(),
		Source:   sourceName,
		SourceID: sourceID,
		Messages: append([]pub_models.Message{{Role: "system", Content: sys}}, messages...),
	}, nil
}

func (r SourceReader) sessionMessages(ctx context.Context, db *sqlitepage.DB, sourceID string) ([]pub_models.Message, error) {
	tbl, err := messageTableFor(db)
	if err != nil {
		return nil, err
	}
	rows := []messageRow{}
	err = tbl.Rows(func(_ int64, vals []sqlitepage.Value) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		row, ok := messageRowOf(vals)
		if !ok || row.session != sourceID {
			return nil
		}
		rows = append(rows, row)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan opencode messages: %w", err)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].seq < rows[j].seq })

	msgs := []pub_models.Message{}
	for _, row := range rows {
		msgs = append(msgs, row.conversation()...)
	}
	return msgs, nil
}

func messageTableFor(db *sqlitepage.DB) (*sqlitepage.Table, error) {
	return db.TableWithColumns(messageTable, messageColumns)
}

func sessionTableFor(db *sqlitepage.DB) (*sqlitepage.Table, error) {
	return db.TableWithColumns(sessionTable, sessionColumns)
}

func findSession(ctx context.Context, db *sqlitepage.DB, sourceID string) (sessionRow, error) {
	tbl, err := sessionTableFor(db)
	if err != nil {
		return sessionRow{}, err
	}
	var found sessionRow
	err = tbl.Rows(func(_ int64, vals []sqlitepage.Value) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		s, ok := sessionOf(vals)
		if !ok || s.id != sourceID {
			return nil
		}
		found = s
		return errStopScan
	})
	if err != nil && !errors.Is(err, errStopScan) {
		return sessionRow{}, fmt.Errorf("scan opencode sessions: %w", err)
	}
	if found.id == "" {
		return sessionRow{}, fmt.Errorf("opencode session %q not found", sourceID)
	}
	return found, nil
}

func (r SourceReader) open() (*sqlitepage.DB, error) {
	p := r.databasePath()
	if p == "" {
		return nil, errNoDatabase
	}
	db, err := sqlitepage.Open(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", errNoDatabase, p)
		}
		return nil, fmt.Errorf("open opencode database %q: %w", p, err)
	}
	return db, nil
}

// databasePath follows opencode's own rule: XDG data home, else
// ~/.local/share on every platform, Windows included.
func (r SourceReader) databasePath() string {
	if r.DBPath != "" {
		return r.DBPath
	}
	dir := os.Getenv("XDG_DATA_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dir, sourceName, "opencode.db")
}

type sessionRow struct {
	id       string
	parent   string
	title    string
	cwd      string
	rawModel string
	born     int64
}

func sessionOf(vals []sqlitepage.Value) (sessionRow, bool) {
	if len(vals) <= colSessionBorn {
		return sessionRow{}, false
	}
	if vals[colSessionID].Kind != sqlitepage.Text || vals[colSessionID].Text == "" {
		return sessionRow{}, false
	}
	return sessionRow{
		id:       vals[colSessionID].Text,
		parent:   textAt(vals, colSessionParent),
		title:    textAt(vals, colSessionTitle),
		cwd:      textAt(vals, colSessionCwd),
		rawModel: textAt(vals, colSessionModel),
		born:     intAt(vals, colSessionBorn),
	}, true
}

func (s sessionRow) created() time.Time {
	if s.born <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(s.born)
}

// modelName flattens the stored Model.Ref into the provider/model spelling
// clai shows elsewhere.
func (s sessionRow) modelName() string {
	var ref struct {
		ID       string `json:"id"`
		Provider string `json:"providerID"`
	}
	if err := json.Unmarshal([]byte(s.rawModel), &ref); err != nil {
		return ""
	}
	if ref.ID == "" {
		return ""
	}
	if ref.Provider == "" {
		return ref.ID
	}
	return ref.Provider + "/" + ref.ID
}

type messageRow struct {
	session string
	kind    string
	seq     int64
	data    []byte
}

func messageRowOf(vals []sqlitepage.Value) (messageRow, bool) {
	if len(vals) <= colMessageData {
		return messageRow{}, false
	}
	if vals[colMessageSession].Kind != sqlitepage.Text || vals[colMessageSession].Text == "" {
		return messageRow{}, false
	}
	if vals[colMessageKind].Kind != sqlitepage.Text || vals[colMessageData].Kind != sqlitepage.Text {
		return messageRow{}, false
	}
	return messageRow{
		session: vals[colMessageSession].Text,
		kind:    vals[colMessageKind].Text,
		seq:     intAt(vals, colMessageSeq),
		data:    []byte(vals[colMessageData].Text),
	}, true
}

// conversation is the one mapper both discovery and Read use, so the count a
// row shows cannot drift from the messages a clone contains.
func (r messageRow) conversation() []pub_models.Message {
	switch r.kind {
	case "user":
		if msg, ok := r.userMessage(); ok {
			return []pub_models.Message{msg}
		}
	case "assistant":
		return r.assistantMessages()
	}
	return nil
}

type userData struct {
	Text string `json:"text"`
}

func (r messageRow) userMessage() (pub_models.Message, bool) {
	var u userData
	if err := json.Unmarshal(r.data, &u); err != nil {
		return pub_models.Message{}, false
	}
	if strings.TrimSpace(u.Text) == "" {
		return pub_models.Message{}, false
	}
	return pub_models.Message{Role: "user", Content: u.Text}, true
}

// part is one entry of an assistant message's content array.
type part struct {
	Type  string `json:"type"`
	Text  string `json:"text"`
	ID    string `json:"id"`
	Name  string `json:"name"`
	State struct {
		Status  string          `json:"status"`
		Input   json.RawMessage `json:"input"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Error *struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	} `json:"state"`
}

type assistantData struct {
	Content []part `json:"content"`
}

// assistantMessages maps one stored assistant step to a clai turn: its text
// and reasoning become one assistant message, and every settled tool part
// becomes a tool call plus its result. A call without a result is rendered as
// text, because an unpaired structured call would make a later provider
// request invalid.
func (r messageRow) assistantMessages() []pub_models.Message {
	var a assistantData
	if err := json.Unmarshal(r.data, &a); err != nil {
		return nil
	}
	texts := []string{}
	reasoning := []string{}
	calls := []pub_models.Call{}
	results := []pub_models.Message{}
	for _, p := range a.Content {
		switch p.Type {
		case "text":
			if strings.TrimSpace(p.Text) != "" {
				texts = append(texts, p.Text)
			}
		case "reasoning":
			if strings.TrimSpace(p.Text) != "" {
				reasoning = append(reasoning, p.Text)
			}
		case "tool":
			if result, call, ok := p.toolCall(); ok {
				calls = append(calls, call)
				results = append(results, result)
				continue
			}
			texts = append(texts, fmt.Sprintf("[tool call: %s(%s)]", p.Name, p.arguments()))
		}
	}
	head := pub_models.Message{
		Role:             "assistant",
		Content:          strings.Join(texts, "\n"),
		ReasoningContent: strings.Join(reasoning, "\n"),
		ToolCalls:        calls,
	}
	if head.Content == "" && len(head.ToolCalls) == 0 {
		if head.ReasoningContent == "" {
			return nil
		}
		head.Content = "[thinking] " + head.ReasoningContent
	}
	return append([]pub_models.Message{head}, results...)
}

// toolCall reports whether the part settled, and returns its result message
// and call. A streaming or running call is not settled.
func (p part) toolCall() (pub_models.Message, pub_models.Call, bool) {
	switch p.State.Status {
	case "completed", "error":
	default:
		return pub_models.Message{}, pub_models.Call{}, false
	}
	if p.ID == "" || p.Name == "" {
		return pub_models.Message{}, pub_models.Call{}, false
	}
	call := pub_models.Call{
		ID:   p.ID,
		Name: p.Name,
		Function: pub_models.Specification{
			Name:      p.Name,
			Arguments: p.arguments(),
		},
	}
	call.Patch()
	return pub_models.Message{
		Role:       "tool",
		ToolCallID: p.ID,
		Content:    p.resultText(),
	}, call, true
}

func (p part) arguments() string {
	raw := strings.TrimSpace(string(p.State.Input))
	if raw == "" || raw == "null" {
		return "{}"
	}
	return raw
}

func (p part) resultText() string {
	if p.State.Error != nil {
		return fmt.Sprintf("tool error: %s", p.State.Error.Message)
	}
	texts := make([]string, 0, len(p.State.Content))
	for _, c := range p.State.Content {
		if strings.TrimSpace(c.Text) != "" {
			texts = append(texts, c.Text)
		}
	}
	return strings.Join(texts, "\n")
}

func textAt(vals []sqlitepage.Value, i int) string {
	if i < 0 || i >= len(vals) || vals[i].Kind != sqlitepage.Text {
		return ""
	}
	return vals[i].Text
}

func intAt(vals []sqlitepage.Value, i int) int64 {
	if i < 0 || i >= len(vals) || vals[i].Kind != sqlitepage.Integer {
		return 0
	}
	return vals[i].Int
}
