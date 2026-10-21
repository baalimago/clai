package cursor

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
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
	cliSourcePrefix = "cli:"
	cliMetaTable    = "meta"
	cliBlobsTable   = "blobs"
	cliMetaKey      = "0"
	cliStoreName    = "store.db"
	cliMetaName     = "meta.json"
)

var errNoCLIConversation = errors.New("cursor cli chat has no conversation")

// Cursor CLI writes one store.db per chat under
// <home>/.cursor/chats/<workspace>/<chat>/. The store holds a protobuf root
// blob whose repeated field 1 is the ordered list of message blob ids. Each
// message blob is JSON with a role and content. The sibling meta.json carries
// the title and working directory that the store itself does not.
func (r SourceReader) cliRoot() string {
	if r.CLIRoot != "" {
		return r.CLIRoot
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".cursor", "chats")
}

func (r SourceReader) discoverCLI(ctx context.Context) ([]vendors.SourceRow, error) {
	root := r.cliRoot()
	if root == "" {
		return nil, nil
	}
	workspaces, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read cursor cli root %q: %w", root, err)
	}
	var rows []vendors.SourceRow
	var errs []error
	for _, workspace := range workspaces {
		if !workspace.IsDir() {
			continue
		}
		chats, err := os.ReadDir(filepath.Join(root, workspace.Name()))
		if err != nil {
			errs = append(errs, fmt.Errorf("read cursor cli workspace %q: %w", workspace.Name(), err))
			continue
		}
		for _, chat := range chats {
			if !chat.IsDir() {
				continue
			}
			row, err := readCLIRow(ctx, filepath.Join(root, workspace.Name(), chat.Name()))
			if err != nil {
				if errors.Is(err, errNoCLIConversation) {
					continue
				}
				errs = append(errs, err)
				continue
			}
			rows = append(rows, row)
		}
	}
	if len(rows) == 0 && len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return rows, nil
}

func readCLIRow(ctx context.Context, dir string) (vendors.SourceRow, error) {
	if err := ctx.Err(); err != nil {
		return vendors.SourceRow{}, err
	}
	dbPath := filepath.Join(dir, cliStoreName)
	if _, err := os.Stat(dbPath); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return vendors.SourceRow{}, errNoCLIConversation
		}
		return vendors.SourceRow{}, fmt.Errorf("stat cursor cli store %q: %w", dbPath, err)
	}
	fileMeta, err := readCLIFileMeta(dir)
	if err != nil {
		return vendors.SourceRow{}, err
	}
	db, err := sqlitepage.Open(dbPath)
	if err != nil {
		return vendors.SourceRow{}, fmt.Errorf("open cursor cli store %q: %w", dbPath, err)
	}
	defer db.Close()

	storeMeta, ids, blobs, err := readCLIStore(ctx, db)
	if err != nil {
		return vendors.SourceRow{}, fmt.Errorf("read cursor cli store %q: %w", dbPath, err)
	}
	if storeMeta.SubagentInfo != nil && storeMeta.SubagentInfo.ParentAgentID != "" {
		return vendors.SourceRow{}, errNoCLIConversation
	}

	msgs, preview, err := mapCLIBlobs(ctx, ids, blobs)
	if err != nil {
		return vendors.SourceRow{}, err
	}
	if len(msgs) == 0 {
		return vendors.SourceRow{}, errNoCLIConversation
	}
	title := strings.TrimSpace(fileMeta.Title)
	if title == "" {
		title = strings.TrimSpace(storeMeta.Name)
	}
	if title == "" {
		title = preview
	}
	created := fileMeta.created()
	if created.IsZero() {
		created = storeMeta.created()
	}
	if created.IsZero() {
		if info, err := os.Stat(dbPath); err == nil {
			created = info.ModTime()
		}
	}
	return vendors.SourceRow{
		Source:               sourceName,
		SourceID:             cliSourcePrefix + filepath.Base(dir),
		Created:              created,
		FirstUserMessage:     vendors.TruncateOneLine(title, previewRunes),
		FullFirstUserMessage: preview,
		MessageCount:         len(msgs),
		Model:                storeMeta.model(),
		RawPath:              dbPath,
		Cwd:                  fileMeta.Cwd,
	}, nil
}

func (r SourceReader) readCLI(ctx context.Context, chatID string) (pub_models.Chat, error) {
	if chatID == "" {
		return pub_models.Chat{}, errors.New("read cursor cli chat: empty session id")
	}
	dir, err := r.cliChatDir(chatID)
	if err != nil {
		return pub_models.Chat{}, err
	}
	fileMeta, err := readCLIFileMeta(dir)
	if err != nil {
		return pub_models.Chat{}, err
	}
	dbPath := filepath.Join(dir, cliStoreName)
	db, err := sqlitepage.Open(dbPath)
	if err != nil {
		return pub_models.Chat{}, fmt.Errorf("open cursor cli chat %q: %w", chatID, err)
	}
	defer db.Close()

	storeMeta, ids, blobs, err := readCLIStore(ctx, db)
	if err != nil {
		return pub_models.Chat{}, fmt.Errorf("read cursor cli chat %q: %w", chatID, err)
	}

	msgs, _, err := mapCLIBlobs(ctx, ids, blobs)
	if err != nil {
		return pub_models.Chat{}, err
	}

	created := fileMeta.created()
	if created.IsZero() {
		created = storeMeta.created()
	}
	if created.IsZero() {
		if info, err := os.Stat(dbPath); err == nil {
			created = info.ModTime()
		}
	}
	sys := fmt.Sprintf("Continued from Cursor CLI session %s", chatID)
	if fileMeta.Cwd != "" {
		sys = fmt.Sprintf("Continued from Cursor CLI session %s (originally at %s).", chatID, fileMeta.Cwd)
	}
	return pub_models.Chat{
		Created:  created,
		Source:   sourceName,
		SourceID: cliSourcePrefix + chatID,
		Messages: append([]pub_models.Message{{Role: "system", Content: sys}}, msgs...),
	}, nil
}

func mapCLIBlobs(ctx context.Context, ids []string, blobs map[string][]byte) ([]pub_models.Message, string, error) {
	msgs := make([]pub_models.Message, 0, len(ids))
	preview := ""
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		blob := parseCLIBlob(blobs[id])
		switch blob.Role {
		case "user":
			text := cleanCLIUserText(blob.text())
			if text != "" {
				if preview == "" {
					preview = text
				}
				msgs = append(msgs, pub_models.Message{Role: "user", Content: text})
			}
		case "assistant":
			if msg, ok := blob.message(); ok {
				msgs = append(msgs, msg)
			}
		}
	}
	return msgs, preview, nil
}

func (r SourceReader) cliChatDir(chatID string) (string, error) {
	root := r.cliRoot()
	if root == "" {
		return "", fmt.Errorf("cursor cli chat %q: %w", chatID, errNoDatabase)
	}
	var found string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return errStopScan
			}
			return err
		}
		if d.IsDir() || d.Name() != cliStoreName {
			return nil
		}
		if filepath.Base(filepath.Dir(p)) != chatID {
			return nil
		}
		found = filepath.Dir(p)
		return errStopScan
	})
	if errors.Is(err, errStopScan) {
		return found, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("cursor cli chat %q: %w", chatID, errNoDatabase)
	}
	if err != nil {
		return "", fmt.Errorf("find cursor cli chat %q: %w", chatID, err)
	}
	return "", fmt.Errorf("cursor cli chat %q not found", chatID)
}

type cliFileMeta struct {
	Title       string `json:"title"`
	Cwd         string `json:"cwd"`
	CreatedAtMs int64  `json:"createdAtMs"`
}

func readCLIFileMeta(dir string) (cliFileMeta, error) {
	var meta cliFileMeta
	b, err := os.ReadFile(filepath.Join(dir, cliMetaName))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return meta, nil
		}
		return cliFileMeta{}, fmt.Errorf("read cursor cli metadata in %q: %w", dir, err)
	}
	if err := json.Unmarshal(b, &meta); err != nil {
		return cliFileMeta{}, fmt.Errorf("decode cursor cli metadata in %q: %w", dir, err)
	}
	return meta, nil
}

func (m cliFileMeta) created() time.Time {
	if m.CreatedAtMs <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(m.CreatedAtMs)
}

type cliStoreMeta struct {
	AgentID          string           `json:"agentId"`
	LatestRootBlobID string           `json:"latestRootBlobId"`
	Name             string           `json:"name"`
	CreatedAt        int64            `json:"createdAt"`
	LastUsedModel    string           `json:"lastUsedModel"`
	SubagentInfo     *cliSubagentInfo `json:"subagentInfo"`
}

type cliSubagentInfo struct {
	ParentAgentID string `json:"parentAgentId"`
}

func (m cliStoreMeta) created() time.Time {
	if m.CreatedAt <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(m.CreatedAt)
}

func (m cliStoreMeta) model() string {
	if m.LastUsedModel == "default" {
		return ""
	}
	return m.LastUsedModel
}

func readCLIStore(ctx context.Context, db *sqlitepage.DB) (cliStoreMeta, []string, map[string][]byte, error) {
	meta, err := readCLIStoreMeta(ctx, db)
	if err != nil {
		return cliStoreMeta{}, nil, nil, err
	}
	if meta.LatestRootBlobID == "" {
		return meta, nil, nil, errNoCLIConversation
	}
	root, err := readCLIBlob(ctx, db, meta.LatestRootBlobID)
	if err != nil {
		return cliStoreMeta{}, nil, nil, err
	}
	ids, err := readRootBlobIDs(root)
	if err != nil {
		return cliStoreMeta{}, nil, nil, err
	}
	if len(ids) == 0 {
		return meta, ids, nil, errNoCLIConversation
	}
	wanted := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		wanted[id] = struct{}{}
	}
	blobs, err := readCLIBlobs(ctx, db, wanted)
	if err != nil {
		return cliStoreMeta{}, nil, nil, err
	}
	return meta, ids, blobs, nil
}

func readCLIBlob(ctx context.Context, db *sqlitepage.DB, id string) ([]byte, error) {
	tbl, err := db.Table(cliBlobsTable)
	if err != nil {
		return nil, err
	}
	var found []byte
	err = tbl.Rows(func(_ int64, vals []sqlitepage.Value) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		key, value, ok := keyValue(vals)
		if !ok || key != id {
			return nil
		}
		found = append([]byte(nil), value...)
		return errStopScan
	})
	if err != nil && !errors.Is(err, errStopScan) {
		return nil, err
	}
	if found == nil {
		return nil, fmt.Errorf("cursor cli blob %q not found", id)
	}
	return found, nil
}

func readCLIBlobs(ctx context.Context, db *sqlitepage.DB, wanted map[string]struct{}) (map[string][]byte, error) {
	tbl, err := db.Table(cliBlobsTable)
	if err != nil {
		return nil, err
	}
	blobs := make(map[string][]byte, len(wanted))
	err = tbl.Rows(func(_ int64, vals []sqlitepage.Value) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		key, value, ok := keyValue(vals)
		if !ok || key == "" {
			return nil
		}
		if _, ok := wanted[key]; !ok {
			return nil
		}
		blobs[key] = append([]byte(nil), value...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return blobs, nil
}

func readCLIStoreMeta(ctx context.Context, db *sqlitepage.DB) (cliStoreMeta, error) {
	tbl, err := db.Table(cliMetaTable)
	if err != nil {
		return cliStoreMeta{}, err
	}
	var encoded string
	err = tbl.Rows(func(_ int64, vals []sqlitepage.Value) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		key, value, ok := keyValue(vals)
		if !ok || key != cliMetaKey {
			return nil
		}
		encoded = string(value)
		return errStopScan
	})
	if err != nil && !errors.Is(err, errStopScan) {
		return cliStoreMeta{}, err
	}
	if encoded == "" {
		return cliStoreMeta{}, fmt.Errorf("%w: no metadata", errNoCLIConversation)
	}
	raw, err := hex.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return cliStoreMeta{}, fmt.Errorf("decode cursor cli metadata: %w", err)
	}
	var meta cliStoreMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return cliStoreMeta{}, fmt.Errorf("decode cursor cli metadata: %w", err)
	}
	return meta, nil
}

// readRootBlobIDs parses the root blob's repeated field 1. Each value is the
// 32-byte id of one message blob, in conversation order.
func readRootBlobIDs(raw []byte) ([]string, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("%w: root blob is missing", errNoCLIConversation)
	}
	var ids []string
	for i := 0; i < len(raw); {
		key, n := binary.Uvarint(raw[i:])
		if n <= 0 {
			return nil, errors.New("decode cursor cli root blob: bad field key")
		}
		i += n
		field, wire := key>>3, key&7
		switch wire {
		case 0:
			_, n := binary.Uvarint(raw[i:])
			if n <= 0 {
				return nil, errors.New("decode cursor cli root blob: bad varint")
			}
			i += n
		case 1:
			if i+8 > len(raw) {
				return nil, errors.New("decode cursor cli root blob: short fixed64")
			}
			i += 8
		case 2:
			ln, n := binary.Uvarint(raw[i:])
			if n <= 0 {
				return nil, errors.New("decode cursor cli root blob: bad length")
			}
			i += n
			if ln > uint64(len(raw)-i) {
				return nil, errors.New("decode cursor cli root blob: length past end")
			}
			if field == 1 && ln == 32 {
				ids = append(ids, hex.EncodeToString(raw[i:i+32]))
			}
			i += int(ln)
		case 5:
			if i+4 > len(raw) {
				return nil, errors.New("decode cursor cli root blob: short fixed32")
			}
			i += 4
		default:
			return nil, fmt.Errorf("decode cursor cli root blob: unsupported wire type %d", wire)
		}
	}
	return ids, nil
}

type cliBlob struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

func parseCLIBlob(raw []byte) cliBlob {
	var b cliBlob
	_ = json.Unmarshal(raw, &b)
	return b
}

func (b cliBlob) text() string {
	var s string
	if err := json.Unmarshal(b.Content, &s); err == nil {
		return s
	}
	var parts []cliPart
	if err := json.Unmarshal(b.Content, &parts); err != nil {
		return ""
	}
	var out []string
	for _, part := range parts {
		if part.Type == "text" && strings.TrimSpace(part.Text) != "" {
			out = append(out, part.Text)
		}
	}
	return strings.Join(out, "\n")
}

func (b cliBlob) message() (pub_models.Message, bool) {
	var parts []cliPart
	if err := json.Unmarshal(b.Content, &parts); err != nil {
		return pub_models.Message{}, false
	}
	var content []string
	var reasoning []string
	for _, part := range parts {
		switch part.Type {
		case "text":
			if strings.TrimSpace(part.Text) != "" {
				content = append(content, part.Text)
			}
		case "reasoning":
			if strings.TrimSpace(part.Text) != "" {
				reasoning = append(reasoning, part.Text)
			}
		case "tool-call":
			content = append(content, part.toolCall())
		}
	}
	joinedContent := strings.TrimSpace(strings.Join(content, "\n"))
	joinedReasoning := strings.TrimSpace(strings.Join(reasoning, "\n"))
	if joinedContent == "" && joinedReasoning == "" {
		return pub_models.Message{}, false
	}
	if joinedContent == "" {
		joinedContent = "[thinking] " + joinedReasoning
	}
	return pub_models.Message{
		Role:             "assistant",
		Content:          joinedContent,
		ReasoningContent: joinedReasoning,
	}, true
}

type cliPart struct {
	Type     string          `json:"type"`
	Text     string          `json:"text"`
	ToolName string          `json:"toolName"`
	Args     json.RawMessage `json:"args"`
}

func (p cliPart) toolCall() string {
	name := p.ToolName
	if strings.TrimSpace(name) == "" {
		name = "tool"
	}
	args := compactJSON(p.Args)
	args = vendors.TruncateOneLine(args, toolArgRunes)
	return "[tool call: " + name + "(" + args + ")]"
}

func compactJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err == nil {
		return buf.String()
	}
	return string(raw)
}

// cleanCLIUserText keeps the user query and drops the context envelopes Cursor
// wraps around it: user info, git status, timestamps and task notifications.
func cleanCLIUserText(s string) string {
	s = strings.TrimSpace(s)
	if _, after, ok := strings.Cut(s, "<user_query>"); ok {
		rest := after
		if before, _, ok := strings.Cut(rest, "</user_query>"); ok {
			return strings.TrimSpace(before)
		}
		return strings.TrimSpace(rest)
	}
	for _, envelope := range []string{"<user_info>", "<git_status>", "<system_notification>"} {
		if strings.Contains(s, envelope) {
			return ""
		}
	}
	return s
}
