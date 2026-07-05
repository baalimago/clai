package anthropic

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/baalimago/clai/internal/vendors"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// SourceReader reads Claude Code / Claude Desktop conversation logs from disk.
//
// Storage (best-effort, observed): ~/.claude/projects/<project>/*.jsonl
// Each line is JSON with a "type" such as: user, assistant, system, queue-operation.
//
// This reader is intentionally conservative: discovery is bounded and skips
// rows with missing SourceID.
//
// FS is injectable for tests.
// If nil, os.DirFS("/") is used (absolute paths will work).
//
// Note: we keep the host-path logic (HOME expansion) outside the FS.
// The FS is only used for opening files.
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
	return "claude-code"
}

const (
	discoverMaxLines = 200
)

func (r SourceReader) Discover(ctx context.Context) ([]vendors.SourceRow, error) {
	root := r.projectsRoot()
	if root == "" {
		return []vendors.SourceRow{}, nil
	}
	st, err := os.Stat(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []vendors.SourceRow{}, nil
		}
		return nil, fmt.Errorf("stat claude projects dir %q: %w", root, err)
	}
	if !st.IsDir() {
		return []vendors.SourceRow{}, nil
	}

	var rows []vendors.SourceRow
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		row, ok := r.discoverOne(ctx, p)
		if !ok {
			return nil
		}
		rows = append(rows, row)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk claude projects dir %q: %w", root, err)
	}
	return rows, nil
}

func (r SourceReader) discoverOne(ctx context.Context, absPath string) (vendors.SourceRow, bool) {
	f, err := r.open(absPath)
	if err != nil {
		return vendors.SourceRow{}, false
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 0, 1<<20), 1<<20) // 1MB max token: keep bounded scanners robust.

	row := vendors.SourceRow{Source: r.Source(), RawPath: absPath}
	lines := 0
	for s.Scan() {
		select {
		case <-ctx.Done():
			return vendors.SourceRow{}, false
		default:
		}
		lines++
		if lines > discoverMaxLines {
			break
		}
		line := strings.TrimSpace(s.Text())
		if line == "" {
			continue
		}
		var env map[string]any
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			continue
		}
		if row.SourceID == "" {
			if sid, _ := env["sessionId"].(string); sid != "" {
				row.SourceID = sid
			}
		}
		if row.Created.IsZero() {
			if ts, _ := env["timestamp"].(string); ts != "" {
				if t, err := time.Parse(time.RFC3339, ts); err == nil {
					row.Created = t
				}
			}
		}
		typ, _ := env["type"].(string)
		switch typ {
		case "user":
			row.MessageCount++
			if row.FirstUserMessage == "" {
				row.FirstUserMessage, row.FullFirstUserMessage = extractUserContentStrings(env)
			}
		case "assistant":
			row.MessageCount++
			if row.Model == "" {
				if msg, _ := env["message"].(map[string]any); msg != nil {
					if m, _ := msg["model"].(string); m != "" {
						row.Model = m
					}
				}
			}
		}
	}
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

func extractUserContentStrings(env map[string]any) (string, string) {
	msg, _ := env["message"].(map[string]any)
	if msg == nil {
		return "", ""
	}
	c := msg["content"]
	s, _ := c.(string)
	return vendors.TruncateOneLine(s, 100), s
}

func (r SourceReader) Read(ctx context.Context, sourceID string) (pub_models.Chat, error) {
	absPath, err := r.findSessionFile(sourceID)
	if err != nil {
		return pub_models.Chat{}, err
	}
	f, err := r.open(absPath)
	if err != nil {
		return pub_models.Chat{}, err
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 0, 1<<20), 10<<20) // 10MB max token: JSONL lines can be large.
	msgs := make([]pub_models.Message, 0, 128)
	created := time.Time{}
	cwd := ""
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" {
			continue
		}
		var env map[string]any
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			continue
		}
		if sid, _ := env["sessionId"].(string); sid != "" && sid != sourceID {
			continue
		}
		typ, _ := env["type"].(string)
		switch typ {
		case "system", "queue-operation":
			continue
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
			msgs = append(msgs, mapAssistantMessage(env)...)
		}
	}
	if err := s.Err(); err != nil {
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

func (r SourceReader) findSessionFile(sourceID string) (string, error) {
	root := r.projectsRoot()
	if root == "" {
		return "", fmt.Errorf("claude projects root not configured")
	}
	var found string
	errFound := errors.New("found")
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		// best-effort: skip dirs quickly
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		ok, err := fileHasSessionID(r, p, sourceID)
		if err != nil {
			return nil
		}
		if ok {
			found = p
			return errFound
		}
		return nil
	})
	if err != nil && !errors.Is(err, errFound) {
		return "", fmt.Errorf("walk claude projects dir %q: %w", root, err)
	}
	if found == "" {
		return "", fmt.Errorf("claude session %q not found", sourceID)
	}
	return found, nil
}

func (r SourceReader) projectsRoot() string {
	if r.Root != "" {
		return r.Root
	}
	h := os.Getenv("HOME")
	if h == "" {
		return ""
	}
	return filepath.Join(h, ".claude", "projects")
}

func fileHasSessionID(r SourceReader, absPath, want string) (bool, error) {
	f, err := r.open(absPath)
	if err != nil {
		return false, err
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 0, 1<<20), 1<<20)
	lines := 0
	for s.Scan() {
		lines++
		if lines > discoverMaxLines {
			break
		}
		line := strings.TrimSpace(s.Text())
		if line == "" {
			continue
		}
		var env map[string]any
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			continue
		}
		if sid, _ := env["sessionId"].(string); sid == want {
			return true, nil
		}
	}
	if err := s.Err(); err != nil {
		return false, err
	}
	return false, nil
}

func mapUserMessage(env map[string]any) []pub_models.Message {
	msg, _ := env["message"].(map[string]any)
	if msg == nil {
		return nil
	}
	c := msg["content"]
	// user message can be string OR array of blocks (tool_result)
	if s, ok := c.(string); ok {
		return []pub_models.Message{{Role: "user", Content: s}}
	}
	arr, ok := c.([]any)
	if !ok {
		return nil
	}
	out := make([]pub_models.Message, 0, len(arr))
	for _, v := range arr {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if typ, _ := m["type"].(string); typ != "tool_result" {
			continue
		}
		toolID, _ := m["tool_use_id"].(string)
		content, _ := m["content"].(string)
		out = append(out, pub_models.Message{Role: "tool", ToolCallID: toolID, Content: content})
	}
	return out
}

func mapAssistantMessage(env map[string]any) []pub_models.Message {
	msg, _ := env["message"].(map[string]any)
	if msg == nil {
		return nil
	}
	c := msg["content"]
	arr, ok := c.([]any)
	if !ok {
		// sometimes might be string.
		s, _ := c.(string)
		if s != "" {
			return []pub_models.Message{{Role: "assistant", Content: s}}
		}
		return nil
	}
	out := pub_models.Message{Role: "assistant"}
	texts := make([]string, 0, 2)
	for _, v := range arr {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		typ, _ := m["type"].(string)
		switch typ {
		case "text":
			if t, _ := m["text"].(string); t != "" {
				texts = append(texts, t)
			}
		case "thinking":
			if th, _ := m["thinking"].(string); th != "" {
				out.ReasoningContent = vendors.JoinNonEmpty(out.ReasoningContent, th)
			}
		case "tool_use":
			id, _ := m["id"].(string)
			name, _ := m["name"].(string)
			input := m["input"]
			args := "{}"
			if input != nil {
				if b, err := json.Marshal(input); err == nil {
					args = string(b)
				}
			}
			call := pub_models.Call{
				ID: id,
				Function: pub_models.Specification{
					Name:      name,
					Arguments: args,
				},
			}
			call.Patch()
			out.ToolCalls = append(out.ToolCalls, call)
		}
	}
	out.Content = strings.Join(texts, "\n")
	// Skip empty assistant messages (stream "start" markers, etc).
	// DeepSeek and similar APIs require content or tool_calls to be set.
	if out.Content == "" && len(out.ToolCalls) == 0 {
		if out.ReasoningContent != "" {
			out.Content = "[thinking] " + out.ReasoningContent
		} else {
			return nil
		}
	}
	return []pub_models.Message{out}
}

func (r SourceReader) open(absPath string) (io.ReadCloser, error) {
	fsys := r.FS
	if fsys == nil {
		fsys = os.DirFS("/")
	}
	// absPath is absolute; os.DirFS("/") expects paths without leading slash.
	p := strings.TrimPrefix(absPath, string(filepath.Separator))
	f, err := fsys.Open(p)
	if err != nil {
		return nil, fmt.Errorf("open %q: %w", absPath, err)
	}
	rc, ok := f.(io.ReadCloser)
	if ok {
		return rc, nil
	}
	return io.NopCloser(f), nil
}
