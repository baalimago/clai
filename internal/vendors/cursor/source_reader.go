// Package cursor reads Cursor composer conversations from the global
// state.vscdb SQLite file.
//
// Cursor stores one JSON document per chat under composerData:<composerId>
// and one JSON document per message under bubbleId:<deviceId>:<bubbleId>.
// The database is undocumented and changes between Cursor releases, so every
// field is read best-effort: a missing field drops that field, never the
// conversation.
//
// The reader never writes to the database. A write-ahead log is replayed by
// internal/sqlitepage; a log the page reader cannot parse is refused, because a
// read without it would silently return stale history.
package cursor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/baalimago/clai/internal/sqlitepage"
	"github.com/baalimago/clai/internal/vendors"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

const (
	sourceName   = "cursor"
	kvTable      = "cursorDiskKV"
	composerKey  = "composerData:"
	bubbleKey    = "bubbleId:"
	previewRunes = 100
	toolArgRunes = 200
)

// SourceReader discovers and reads Cursor composer chats.
type SourceReader struct {
	// DBPath overrides the global state.vscdb path. It exists for tests and
	// for a non-default install; production code leaves it empty.
	DBPath string
	// CLIRoot overrides the Cursor CLI chats directory. It exists for tests;
	// production code leaves it empty.
	CLIRoot string
}

func (r SourceReader) Source() string { return sourceName }

var (
	errNoDatabase = errors.New("cursor database not found")
	errStopScan   = errors.New("stop scan")
)

// Discover lists Cursor chats from both the IDE database and the CLI store.
//
// The foreign cache is per file, but one state.vscdb holds every chat, so the
// cache cannot represent this source. Discovery scans the database instead.
// A failed storage does not hide a storage that answered: the reader reports
// an error only when neither storage produced a row.
func (r SourceReader) Discover(ctx context.Context, _ vendors.SourceCache) ([]vendors.SourceRow, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	guiRows, guiErr := r.discoverGUI(ctx)
	cliRows, cliErr := r.discoverCLI(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows := append(guiRows, cliRows...)
	if len(rows) > 0 {
		return rows, nil
	}
	if err := errors.Join(guiErr, cliErr); err != nil {
		return nil, err
	}
	return nil, nil
}

func (r SourceReader) discoverGUI(ctx context.Context) ([]vendors.SourceRow, error) {
	db, err := r.open()
	if err != nil {
		if errors.Is(err, errNoDatabase) {
			return nil, nil
		}
		return nil, err
	}
	defer db.Close()

	tbl, err := db.Table(kvTable)
	if err != nil {
		return nil, fmt.Errorf("read cursor database: %w", err)
	}
	chats := []composer{}
	err = tbl.Rows(func(_ int64, vals []sqlitepage.Value) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		key, value, ok := keyValue(vals)
		if !ok || !strings.HasPrefix(key, composerKey) {
			return nil
		}
		c := parseComposer(value)
		if keyID := strings.TrimPrefix(key, composerKey); keyID != "" {
			c.ID = keyID
		}
		if c.ID == "" || len(c.Headers) == 0 {
			return nil
		}
		chats = append(chats, c)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan cursor database: %w", err)
	}
	if err := r.fillFallbacks(ctx, tbl, chats); err != nil {
		return nil, err
	}

	rows := make([]vendors.SourceRow, 0, len(chats))
	for _, c := range chats {
		preview := c.preview()
		rows = append(rows, vendors.SourceRow{
			Source:               sourceName,
			SourceID:             c.ID,
			Created:              c.created(),
			FirstUserMessage:     vendors.TruncateOneLine(preview, previewRunes),
			FullFirstUserMessage: preview,
			MessageCount:         len(c.Headers),
			Model:                c.model(),
			RawPath:              r.databasePath(),
			Cwd:                  c.cwd(),
		})
	}
	return rows, nil
}

// fillFallbacks reads the first bubble only for chats that lack preview text
// or a working directory. Both fields live on that bubble in stores that do
// not fill composerData.gitWorktree. The second scan stops as soon as every
// wanted bubble is found.
func (r SourceReader) fillFallbacks(ctx context.Context, tbl *sqlitepage.Table, chats []composer) error {
	wanted := map[string][]int{}
	for i := range chats {
		if chats[i].Headers[0].BubbleID == "" {
			continue
		}
		if chats[i].preview() != "" && chats[i].cwd() != "" {
			continue
		}
		wanted[chats[i].Headers[0].BubbleID] = append(wanted[chats[i].Headers[0].BubbleID], i)
	}
	if len(wanted) == 0 {
		return nil
	}
	remaining := len(wanted)
	err := tbl.Rows(func(_ int64, vals []sqlitepage.Value) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		key, value, ok := keyValue(vals)
		if !ok || !strings.HasPrefix(key, bubbleKey) {
			return nil
		}
		id := keySuffix(key)
		indexes, ok := wanted[id]
		if !ok {
			return nil
		}
		b := parseBubble(value)
		for _, i := range indexes {
			if chats[i].cwd() == "" {
				chats[i].FallbackCwd = b.WorkspaceProjectDir
			}
			if chats[i].preview() == "" && b.Type == 1 {
				chats[i].FallbackPreview = b.text()
			}
		}
		delete(wanted, id)
		remaining--
		if remaining == 0 {
			return errStopScan
		}
		return nil
	})
	if err != nil && !errors.Is(err, errStopScan) {
		return fmt.Errorf("read cursor chat fallbacks: %w", err)
	}
	return nil
}

func (r SourceReader) Read(ctx context.Context, _ vendors.SourceCache, sourceID string) (pub_models.Chat, error) {
	if sourceID == "" {
		return pub_models.Chat{}, errors.New("read cursor chat: empty session id")
	}
	if after, ok := strings.CutPrefix(sourceID, cliSourcePrefix); ok {
		return r.readCLI(ctx, after)
	}
	return r.readGUI(ctx, sourceID)
}

func (r SourceReader) readGUI(ctx context.Context, sourceID string) (pub_models.Chat, error) {
	db, err := r.open()
	if err != nil {
		return pub_models.Chat{}, fmt.Errorf("read cursor chat %q: %w", sourceID, err)
	}
	defer db.Close()

	tbl, err := db.Table(kvTable)
	if err != nil {
		return pub_models.Chat{}, fmt.Errorf("read cursor database: %w", err)
	}
	c, err := findComposer(ctx, tbl, sourceID)
	if err != nil {
		return pub_models.Chat{}, err
	}
	if c.ID == "" {
		return pub_models.Chat{}, fmt.Errorf("cursor chat %q not found", sourceID)
	}
	bubbles, err := findBubbles(ctx, tbl, c)
	if err != nil {
		return pub_models.Chat{}, err
	}

	msgs := make([]pub_models.Message, 0, len(c.Headers))
	cwd := c.cwd()
	var firstBubbleTime time.Time
	for _, h := range c.Headers {
		b, ok := bubbles[h.BubbleID]
		if !ok {
			continue
		}
		if firstBubbleTime.IsZero() {
			firstBubbleTime = b.created()
		}
		if cwd == "" {
			cwd = b.WorkspaceProjectDir
		}
		if msg, ok := b.message(); ok {
			msgs = append(msgs, msg)
		}
	}
	created := c.created()
	if created.IsZero() {
		created = firstBubbleTime
	}
	if created.IsZero() {
		if st, err := os.Stat(r.databasePath()); err == nil {
			created = st.ModTime()
		}
	}

	sys := fmt.Sprintf("Continued from Cursor session %s", sourceID)
	if cwd != "" {
		sys = fmt.Sprintf("Continued from Cursor session %s (originally at %s).", sourceID, cwd)
	}
	return pub_models.Chat{
		Created:  created,
		Source:   sourceName,
		SourceID: sourceID,
		Messages: append([]pub_models.Message{{Role: "system", Content: sys}}, msgs...),
	}, nil
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
		return nil, fmt.Errorf("open cursor database %q: %w", p, err)
	}
	return db, nil
}

func (r SourceReader) databasePath() string {
	if r.DBPath != "" {
		return r.DBPath
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "Cursor", "User", "globalStorage", "state.vscdb")
}

type composer struct {
	ID              string `json:"composerId"`
	Name            string `json:"name"`
	Text            string `json:"text"`
	FallbackPreview string `json:"-"`
	FallbackCwd     string `json:"-"`
	CreatedAt       int64  `json:"createdAt"`
	ModelConfig     struct {
		ModelName string `json:"modelName"`
	} `json:"modelConfig"`
	LastUsedModel string `json:"lastUsedModel"`
	GitWorktree   struct {
		WorktreePath string `json:"worktreePath"`
	} `json:"gitWorktree"`
	Headers []struct {
		BubbleID string `json:"bubbleId"`
	} `json:"fullConversationHeadersOnly"`
}

func parseComposer(value []byte) composer {
	var c composer
	_ = json.Unmarshal(value, &c)
	return c
}

func (c composer) preview() string {
	for _, s := range []string{c.Text, c.Name, c.FallbackPreview} {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func (c composer) model() string {
	if c.ModelConfig.ModelName != "" {
		return c.ModelConfig.ModelName
	}
	return c.LastUsedModel
}

func (c composer) cwd() string {
	if c.GitWorktree.WorktreePath != "" {
		return c.GitWorktree.WorktreePath
	}
	return c.FallbackCwd
}

func (c composer) created() time.Time {
	if c.CreatedAt <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(c.CreatedAt)
}

type bubble struct {
	Type              int             `json:"type"`
	Text              string          `json:"text"`
	RichText          json.RawMessage `json:"richText"`
	Thinking          json.RawMessage `json:"thinking"`
	AllThinkingBlocks []struct {
		Text string `json:"text"`
	} `json:"allThinkingBlocks"`
	ToolFormerData *struct {
		Name    string          `json:"name"`
		Tool    string          `json:"tool"`
		RawArgs json.RawMessage `json:"rawArgs"`
		Params  json.RawMessage `json:"params"`
	} `json:"toolFormerData"`
	CreatedAt           string `json:"createdAt"`
	WorkspaceProjectDir string `json:"workspaceProjectDir"`
}

func parseBubble(value []byte) bubble {
	var b bubble
	_ = json.Unmarshal(value, &b)
	return b
}

func (b bubble) text() string {
	if strings.TrimSpace(b.Text) != "" {
		return b.Text
	}
	return richText(b.RichText)
}

func (b bubble) reasoning() string {
	if s := thinkingText(b.Thinking); s != "" {
		return s
	}
	parts := make([]string, 0, len(b.AllThinkingBlocks))
	for _, block := range b.AllThinkingBlocks {
		if block.Text != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func (b bubble) message() (pub_models.Message, bool) {
	content := strings.TrimSpace(b.text())
	if b.Type != 1 && b.ToolFormerData != nil {
		content = strings.TrimSpace(strings.Join(nonEmpty(content, b.toolCall()), "\n"))
	}
	reasoning := b.reasoning()
	if b.Type != 1 && content == "" {
		if reasoning == "" {
			return pub_models.Message{}, false
		}
		content = "[thinking] " + reasoning
	}
	if content == "" && reasoning == "" {
		return pub_models.Message{}, false
	}
	role := "assistant"
	if b.Type == 1 {
		role = "user"
	}
	return pub_models.Message{Role: role, Content: content, ReasoningContent: reasoning}, true
}

func (b bubble) toolCall() string {
	tf := b.ToolFormerData
	if tf == nil {
		return ""
	}
	name := firstNonEmpty(tf.Name, tf.Tool, "tool")
	args := firstNonEmpty(rawText(tf.RawArgs), rawText(tf.Params))
	args = vendors.TruncateOneLine(args, toolArgRunes)
	return "[tool call: " + name + "(" + args + ")]"
}

func (b bubble) created() time.Time {
	if b.CreatedAt == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, b.CreatedAt); err == nil {
			return t
		}
	}
	return time.Time{}
}

// richText flattens Cursor's Lexical tree, keeping array order and taking a
// text leaf once. A mention node has no text leaf and is skipped.
func richText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return ""
	}
	var out strings.Builder
	appendText(v, &out)
	return strings.TrimSpace(out.String())
}

func appendText(v any, out *strings.Builder) {
	switch t := v.(type) {
	case []any:
		for _, e := range t {
			appendText(e, out)
		}
	case map[string]any:
		if s, ok := t["text"].(string); ok && s != "" {
			out.WriteString(s)
			return
		}
		if root, ok := t["root"]; ok {
			appendText(root, out)
			return
		}
		if kids, ok := t["children"].([]any); ok {
			appendText(kids, out)
		}
	}
}

func thinkingText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var obj struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		return obj.Text
	}
	return ""
}

func rawText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func nonEmpty(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			out = append(out, v)
		}
	}
	return out
}

func keyValue(vals []sqlitepage.Value) (string, []byte, bool) {
	if len(vals) < 2 || vals[0].Kind != sqlitepage.Text {
		return "", nil, false
	}
	switch vals[1].Kind {
	case sqlitepage.Blob:
		return vals[0].Text, vals[1].Blob, true
	case sqlitepage.Text:
		return vals[0].Text, []byte(vals[1].Text), true
	default:
		return vals[0].Text, nil, true
	}
}

func keySuffix(key string) string {
	if i := strings.LastIndex(key, ":"); i >= 0 {
		return key[i+1:]
	}
	return key
}

func findComposer(ctx context.Context, tbl *sqlitepage.Table, sourceID string) (composer, error) {
	var found composer
	wanted := composerKey + sourceID
	err := tbl.Rows(func(_ int64, vals []sqlitepage.Value) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		key, value, ok := keyValue(vals)
		if !ok || key != wanted {
			return nil
		}
		found = parseComposer(value)
		if found.ID == "" {
			found.ID = sourceID
		}
		return errStopScan
	})
	if err != nil && !errors.Is(err, errStopScan) {
		return composer{}, fmt.Errorf("find cursor chat %q: %w", sourceID, err)
	}
	return found, nil
}

func findBubbles(ctx context.Context, tbl *sqlitepage.Table, c composer) (map[string]bubble, error) {
	wanted := make(map[string]bool, len(c.Headers))
	for _, h := range c.Headers {
		if h.BubbleID != "" {
			wanted[h.BubbleID] = true
		}
	}
	found := make(map[string]bubble, len(wanted))
	if len(wanted) == 0 {
		return found, nil
	}
	err := tbl.Rows(func(_ int64, vals []sqlitepage.Value) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		key, value, ok := keyValue(vals)
		if !ok || !strings.HasPrefix(key, bubbleKey) {
			return nil
		}
		id := keySuffix(key)
		if !wanted[id] {
			return nil
		}
		found[id] = parseBubble(value)
		if len(found) == len(wanted) {
			return errStopScan
		}
		return nil
	})
	if err != nil && !errors.Is(err, errStopScan) {
		return nil, fmt.Errorf("read cursor chat %q messages: %w", c.ID, err)
	}
	return found, nil
}
